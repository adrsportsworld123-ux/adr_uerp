package promotions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/sales"
)

type couponResponse struct {
	CouponID              string   `json:"coupon_id"`
	Code                  string   `json:"code"`
	PromoType             string   `json:"promo_type"`
	Value                 string   `json:"value"`
	MinPurchaseAmount     *string  `json:"min_purchase_amount"`
	UsageLimitTotal       *int     `json:"usage_limit_total"`
	UsageLimitPerCustomer *int     `json:"usage_limit_per_customer"`
	UsageCount            int      `json:"usage_count"`
	ValidFrom             *string  `json:"valid_from"`
	ValidUntil            *string  `json:"valid_until"`
	Channels              []string `json:"channels"`
	BranchID              *string  `json:"branch_id"`
	Active                bool     `json:"active"`
}

const couponColumns = `
	id, code, promo_type, value::text, min_purchase_amount::text,
	usage_limit_total, usage_limit_per_customer, usage_count,
	valid_from::text, valid_until::text, channels, branch_id::text, active
`

func scanCoupon(row pgx.Row) (couponResponse, error) {
	var c couponResponse
	err := row.Scan(&c.CouponID, &c.Code, &c.PromoType, &c.Value, &c.MinPurchaseAmount,
		&c.UsageLimitTotal, &c.UsageLimitPerCustomer, &c.UsageCount,
		&c.ValidFrom, &c.ValidUntil, &c.Channels, &c.BranchID, &c.Active)
	return c, err
}

// ---------------------------------------------------------------------
// POST /coupons — gated by promotions.manage.
// ---------------------------------------------------------------------

type createCouponRequest struct {
	Code                  string   `json:"code"`
	PromoType             string   `json:"promo_type"` // "percent" | "fixed"
	Value                 float64  `json:"value"`
	MinPurchaseAmount     float64  `json:"min_purchase_amount"`
	UsageLimitTotal       *int     `json:"usage_limit_total"`
	UsageLimitPerCustomer *int     `json:"usage_limit_per_customer"`
	ValidFrom             string   `json:"valid_from"`
	ValidUntil            string   `json:"valid_until"`
	Channels              []string `json:"channels"`
	BranchID              string   `json:"branch_id"`
}

func (h *Handler) CreateCoupon(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req createCouponRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if msg := validateCoupon(&req); msg != "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", msg)
		return
	}

	var couponID string
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO coupons
				(id, merchant_id, code, promo_type, value, min_purchase_amount,
				 usage_limit_total, usage_limit_per_customer, valid_from, valid_until, channels, branch_id)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, NULLIF($4,0),
			        $5, $6, NULLIF($7,'')::timestamptz, NULLIF($8,'')::timestamptz, NULLIF($9,'{}'::text[]), NULLIF($10,'')::uuid)
			RETURNING id`,
			req.Code, req.PromoType, req.Value, req.MinPurchaseAmount,
			req.UsageLimitTotal, req.UsageLimitPerCustomer, req.ValidFrom, req.ValidUntil, req.Channels, req.BranchID,
		).Scan(&couponID)
	})
	if isUniqueViolation(err) {
		httpx.Error(w, http.StatusConflict, "COUPON_CODE_EXISTS", "a coupon with this code already exists")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create coupon")
		return
	}

	coupon, err := h.fetchCouponByID(r.Context(), claims.TenantID, couponID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "coupon created but could not be reloaded")
		return
	}
	httpx.JSON(w, http.StatusCreated, coupon)
}

// ---------------------------------------------------------------------
// GET /coupons?active= — open to any authenticated user, same reasoning
// as ListPromotions (reads aren't the business risk).
// ---------------------------------------------------------------------

func (h *Handler) ListCoupons(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	activeFilter := r.URL.Query().Get("active")

	list := []couponResponse{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT `+couponColumns+` FROM coupons
			WHERE $1 = '' OR active = ($1 = 'true')
			ORDER BY created_at DESC`, activeFilter)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scanCoupon(rows)
			if err != nil {
				return err
			}
			list = append(list, c)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list coupons")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"coupons": list})
}

// ---------------------------------------------------------------------
// PATCH /coupons/{id} — gated by promotions.manage. Every field is
// editable (absent = unchanged; "" clears an optional one), validated
// with create's rules. Past redemptions keep the amount they were given
// (coupon_redemptions.discount_amount), so an edit changes only future
// sales. DELETE /coupons/{id} (crud.go) removes a coupon never redeemed.
// ---------------------------------------------------------------------

type updateCouponRequest struct {
	Code                  *string   `json:"code"`
	PromoType             *string   `json:"promo_type"`
	Value                 *float64  `json:"value"`
	MinPurchaseAmount     *float64  `json:"min_purchase_amount"`
	UsageLimitTotal       *int      `json:"usage_limit_total"`
	UsageLimitPerCustomer *int      `json:"usage_limit_per_customer"`
	ValidFrom             *string   `json:"valid_from"`
	ValidUntil            *string   `json:"valid_until"`
	Channels              *[]string `json:"channels"`
	BranchID              *string   `json:"branch_id"`
	Active                *bool     `json:"active"`
	// ClearUsageLimitTotal/ClearUsageLimitPerCustomer remove a limit (a
	// JSON null can't be told apart from "absent" on an *int).
	ClearUsageLimitTotal       bool `json:"clear_usage_limit_total"`
	ClearUsageLimitPerCustomer bool `json:"clear_usage_limit_per_customer"`
}

var errCouponInvalid = errors.New("invalid coupon")

func (h *Handler) UpdateCoupon(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	couponID := chi.URLParam(r, "id")

	var req updateCouponRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}

	var invalid string
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		cur, err := scanCoupon(tx.QueryRow(ctx, `SELECT `+couponColumns+` FROM coupons WHERE id = $1 FOR UPDATE`, couponID))
		if err != nil {
			return err
		}
		m := createCouponRequest{
			Code: cur.Code, PromoType: cur.PromoType, Value: parseMoney(&cur.Value), MinPurchaseAmount: parseMoney(cur.MinPurchaseAmount),
			UsageLimitTotal: cur.UsageLimitTotal, UsageLimitPerCustomer: cur.UsageLimitPerCustomer,
			ValidFrom: rfc3339(cur.ValidFrom), ValidUntil: rfc3339(cur.ValidUntil),
			Channels: cur.Channels, BranchID: derefOr(cur.BranchID, ""),
		}
		for _, f := range []struct{ dst, src *string }{
			{&m.Code, req.Code}, {&m.PromoType, req.PromoType}, {&m.ValidFrom, req.ValidFrom},
			{&m.ValidUntil, req.ValidUntil}, {&m.BranchID, req.BranchID},
		} {
			if f.src != nil {
				*f.dst = *f.src
			}
		}
		if req.Value != nil {
			m.Value = *req.Value
		}
		if req.MinPurchaseAmount != nil {
			m.MinPurchaseAmount = *req.MinPurchaseAmount
		}
		if req.UsageLimitTotal != nil {
			m.UsageLimitTotal = req.UsageLimitTotal
		}
		if req.ClearUsageLimitTotal {
			m.UsageLimitTotal = nil
		}
		if req.UsageLimitPerCustomer != nil {
			m.UsageLimitPerCustomer = req.UsageLimitPerCustomer
		}
		if req.ClearUsageLimitPerCustomer {
			m.UsageLimitPerCustomer = nil
		}
		if req.Channels != nil {
			m.Channels = *req.Channels
		}
		if invalid = validateCoupon(&m); invalid != "" {
			return errCouponInvalid
		}
		_, err = tx.Exec(ctx, `
			UPDATE coupons SET
				code = $1, promo_type = $2, value = $3, min_purchase_amount = NULLIF($4,0),
				usage_limit_total = $5, usage_limit_per_customer = $6,
				valid_from = NULLIF($7,'')::timestamptz, valid_until = NULLIF($8,'')::timestamptz,
				channels = NULLIF($9,'{}'::text[]), branch_id = NULLIF($10,'')::uuid,
				active = COALESCE($11, active)
			WHERE id = $12`,
			m.Code, m.PromoType, m.Value, m.MinPurchaseAmount, m.UsageLimitTotal, m.UsageLimitPerCustomer,
			m.ValidFrom, m.ValidUntil, m.Channels, m.BranchID, req.Active, couponID)
		return err
	})
	switch {
	case errors.Is(err, errCouponInvalid):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", invalid)
		return
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "coupon not found")
		return
	case isUniqueViolation(err):
		httpx.Error(w, http.StatusConflict, "COUPON_CODE_EXISTS", "a coupon with this code already exists")
		return
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not update coupon")
		return
	}
	coupon, err := h.fetchCouponByID(r.Context(), claims.TenantID, couponID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "coupon updated but could not be reloaded")
		return
	}
	httpx.JSON(w, http.StatusOK, coupon)
}

// validateCoupon normalises the code and returns a client-facing message,
// or "" when valid. Create and edit share it.
func validateCoupon(req *createCouponRequest) string {
	req.Code = strings.ToUpper(strings.TrimSpace(req.Code))
	switch {
	case req.Code == "" || (req.PromoType != "percent" && req.PromoType != "fixed") || req.Value <= 0:
		return "code, promo_type (percent|fixed), and a positive value are required"
	case req.PromoType == "percent" && req.Value > 100:
		return "a percent coupon's value must be 100 or less"
	case req.MinPurchaseAmount < 0:
		return "min_purchase_amount can't be negative"
	case req.UsageLimitTotal != nil && *req.UsageLimitTotal < 1, req.UsageLimitPerCustomer != nil && *req.UsageLimitPerCustomer < 1:
		return "a usage limit must be at least 1 (leave it empty for unlimited)"
	}
	for _, c := range req.Channels {
		if c != "pos" && c != "online" && c != "backoffice" {
			return "channels may only contain pos, online and backoffice"
		}
	}
	from, okFrom := parseWhen(req.ValidFrom)
	until, okUntil := parseWhen(req.ValidUntil)
	if (req.ValidFrom != "" && !okFrom) || (req.ValidUntil != "" && !okUntil) {
		return "valid_from/valid_until must be RFC3339 timestamps or YYYY-MM-DD dates"
	}
	if okFrom && okUntil && !from.Before(until) {
		return "valid_from must be before valid_until"
	}
	return ""
}

func parseWhen(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	t, err := time.Parse("2006-01-02", s)
	return t, err == nil
}

func parseMoney(s *string) float64 {
	if s == nil {
		return 0
	}
	v, _ := strconv.ParseFloat(*s, 64)
	return v
}

func (h *Handler) fetchCouponByID(ctx context.Context, tenantID, couponID string) (couponResponse, error) {
	var c couponResponse
	err := h.DB.WithTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `SELECT `+couponColumns+` FROM coupons WHERE id = $1`, couponID)
		var scanErr error
		c, scanErr = scanCoupon(row)
		return scanErr
	})
	return c, err
}

// ---------------------------------------------------------------------
// POST /sales/orders/{id}/coupons {code} — validates every FRD §5 coupon
// constraint this codebase can evaluate at apply-time (usage caps,
// validity window, min purchase, channel, branch — see
// migrations/012_promotions_loyalty.sql's header comment for why payment
// method restriction is out of scope) and applies it via
// sales.ApplyDiscountLayer. One coupon per order
// (coupon_redemptions.sales_order_id is UNIQUE) — checked explicitly here
// rather than caught as a constraint violation, so a second attempt gets
// a clear 409 rather than a generic 500.
// ---------------------------------------------------------------------

type applyCouponRequest struct {
	Code string `json:"code"`
}

var (
	errCouponNotFound  = errors.New("no active coupon with this code")
	errCouponNotValid  = errors.New("coupon is outside its validity window, branch, or channel")
	errCouponMinSpend  = errors.New("order subtotal does not meet the coupon's minimum purchase amount")
	errCouponExhausted = errors.New("coupon has reached its usage limit")
	errCouponApplied   = errors.New("a coupon has already been applied to this order")
)

func (h *Handler) ApplyCoupon(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	orderID := chi.URLParam(r, "id")

	var req applyCouponRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	code := strings.ToUpper(strings.TrimSpace(req.Code))
	if code == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "code is required")
		return
	}

	var resp any
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var status, branchID string
		var customerID *string
		if err := tx.QueryRow(ctx, `SELECT status, branch_id, customer_id FROM sales_orders WHERE id = $1`, orderID).
			Scan(&status, &branchID, &customerID); err != nil {
			return err
		}
		if status != "cart" {
			return errOrderNotEditable
		}

		var alreadyApplied bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM coupon_redemptions WHERE sales_order_id = $1)`, orderID).
			Scan(&alreadyApplied); err != nil {
			return err
		}
		if alreadyApplied {
			return errCouponApplied
		}

		var couponID, promoType string
		var value float64
		var minPurchase *float64
		var usageLimitTotal, usageLimitPerCustomer *int
		var usageCount int
		var validFrom, validUntil *time.Time
		var channels []string
		var couponBranchID *string
		if err := tx.QueryRow(ctx, `
			SELECT id, promo_type, value, min_purchase_amount, usage_limit_total, usage_limit_per_customer,
			       usage_count, valid_from, valid_until, channels, branch_id
			FROM coupons WHERE code = $1 AND active`, code,
		).Scan(&couponID, &promoType, &value, &minPurchase, &usageLimitTotal, &usageLimitPerCustomer,
			&usageCount, &validFrom, &validUntil, &channels, &couponBranchID); err != nil {
			if err == pgx.ErrNoRows {
				return errCouponNotFound
			}
			return err
		}

		now := time.Now()
		if validFrom != nil && now.Before(*validFrom) {
			return errCouponNotValid
		}
		if validUntil != nil && now.After(*validUntil) {
			return errCouponNotValid
		}
		if couponBranchID != nil && *couponBranchID != branchID {
			return errCouponNotValid
		}
		if len(channels) > 0 && !containsStr(channels, "pos") {
			// Every order through this API is a POS-channel sale — see
			// this function's doc comment; a coupon scoped to only
			// online/mobile is correctly never applicable here.
			return errCouponNotValid
		}
		if usageLimitTotal != nil && usageCount >= *usageLimitTotal {
			return errCouponExhausted
		}
		if usageLimitPerCustomer != nil && customerID != nil {
			var custUsage int
			if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM coupon_redemptions WHERE coupon_id = $1 AND customer_id = $2`,
				couponID, *customerID).Scan(&custUsage); err != nil {
				return err
			}
			if custUsage >= *usageLimitPerCustomer {
				return errCouponExhausted
			}
		}

		var subtotal float64
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(sol.unit_price*sol.quantity),0)
			FROM sales_order_lines sol JOIN sales_orders so ON so.id = sol.sales_order_id
			WHERE sol.sales_order_id = $1 AND `+sales.DiscountableLineSQL, orderID).
			Scan(&subtotal); err != nil {
			return err
		}
		if minPurchase != nil && subtotal < *minPurchase {
			return errCouponMinSpend
		}

		amount := value
		if promoType == "percent" {
			amount = round2(subtotal * value / 100)
		}

		state, err := sales.ApplyDiscountLayer(ctx, tx, sales.DiscountLayer{
			OrderID:   orderID,
			Type:      "coupon",
			Amount:    amount,
			CouponID:  &couponID,
			AppliedBy: claims.UserID,
			Reason:    "coupon:" + code,
		})
		if err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO coupon_redemptions (id, merchant_id, coupon_id, customer_id, sales_order_id, discount_amount)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4)`,
			couponID, customerID, orderID, amount); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE coupons SET usage_count = usage_count + 1 WHERE id = $1`, couponID); err != nil {
			return err
		}

		resp = map[string]any{"discount_amount": amount, "order": state}
		return nil
	})

	switch {
	case errors.Is(err, errOrderNotEditable):
		httpx.Error(w, http.StatusConflict, "ORDER_NOT_EDITABLE", "this order is no longer a cart")
	case errors.Is(err, errCouponApplied):
		httpx.Error(w, http.StatusConflict, "COUPON_ALREADY_APPLIED", "a coupon has already been applied to this order")
	case errors.Is(err, errCouponNotFound):
		httpx.Error(w, http.StatusNotFound, "COUPON_NOT_FOUND", "no active coupon with this code")
	case errors.Is(err, errCouponNotValid):
		httpx.Error(w, http.StatusConflict, "COUPON_NOT_VALID", "coupon is outside its validity window, branch, or channel")
	case errors.Is(err, errCouponMinSpend):
		httpx.Error(w, http.StatusConflict, "COUPON_MIN_PURCHASE_NOT_MET", "order subtotal does not meet the coupon's minimum purchase amount")
	case errors.Is(err, errCouponExhausted):
		httpx.Error(w, http.StatusConflict, "COUPON_LIMIT_REACHED", "coupon has reached its usage limit")
	case sales.IsOpenPriceNotDiscountable(err):
		httpx.Error(w, http.StatusConflict, "OPEN_PRICE_NOT_DISCOUNTABLE", err.Error())
	case errors.Is(err, sales.ErrDiscountExceedsAvailable):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "order has nothing left to discount")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "order not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not apply coupon")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}

func containsStr(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}

// isUniqueViolation matches internal/catalog/barcode_assign.go's own
// helper of the same purpose (Postgres error code 23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
