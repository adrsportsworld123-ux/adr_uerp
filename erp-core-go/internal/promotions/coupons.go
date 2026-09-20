package promotions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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
	req.Code = strings.ToUpper(strings.TrimSpace(req.Code))
	if req.Code == "" || (req.PromoType != "percent" && req.PromoType != "fixed") || req.Value <= 0 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "code, promo_type (percent|fixed), and a positive value are required")
		return
	}
	if req.PromoType == "percent" && req.Value > 100 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "a percent coupon's value must be 100 or less")
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
// PATCH /coupons/{id} — gated by promotions.manage. Same PATCH-only
// (no DELETE) precedent as promotions, for the same reason (past
// coupon_redemptions rows reference this id).
// ---------------------------------------------------------------------

type updateCouponRequest struct {
	Active     *bool   `json:"active"`
	ValidUntil *string `json:"valid_until"`
}

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

	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE coupons SET
				active = COALESCE($1, active),
				valid_until = CASE WHEN $2 THEN NULLIF($3,'')::timestamptz ELSE valid_until END
			WHERE id = $4`,
			req.Active, req.ValidUntil != nil, derefOr(req.ValidUntil, ""), couponID)
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not update coupon")
		return
	}
	coupon, err := h.fetchCouponByID(r.Context(), claims.TenantID, couponID)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "coupon not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "coupon updated but could not be reloaded")
		return
	}
	httpx.JSON(w, http.StatusOK, coupon)
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
		if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(unit_price*quantity),0) FROM sales_order_lines WHERE sales_order_id = $1`, orderID).
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
