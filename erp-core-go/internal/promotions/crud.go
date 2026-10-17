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

	"erp-core-go/internal/authn"
	"erp-core-go/internal/dependencies"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/softdelete"
)

type promotionResponse struct {
	PromotionID      string      `json:"promotion_id"`
	Name             string      `json:"name"`
	PromoType        string      `json:"promo_type"`
	ApplicationLevel string      `json:"application_level"`
	ProductID        *string     `json:"product_id"`
	CategoryID       *string     `json:"category_id"`
	TargetSegment    *string     `json:"target_segment"`
	Config           promoConfig `json:"config"`
	Stacking         string      `json:"stacking"`
	StartsAt         *string     `json:"starts_at"`
	EndsAt           *string     `json:"ends_at"`
	DaysOfWeek       []int16     `json:"days_of_week"`
	TimeStart        *string     `json:"time_start"`
	TimeEnd          *string     `json:"time_end"`
	Active           bool        `json:"active"`
	// ProductName/CategoryName name the scope, so a list or an edit form
	// can show it without a second lookup.
	ProductName  *string `json:"product_name"`
	CategoryName *string `json:"category_name"`
	// TimesUsed is how many sales this promotion discounted; a used
	// promotion can be edited or deactivated but not deleted.
	TimesUsed int `json:"times_used"`
}

const promotionColumns = `
	pr.id, pr.name, pr.promo_type, pr.application_level, pr.product_id::text, pr.category_id::text,
	pr.target_segment, pr.config, pr.stacking, pr.starts_at::text, pr.ends_at::text,
	pr.days_of_week, to_char(pr.time_start, 'HH24:MI'), to_char(pr.time_end, 'HH24:MI'), pr.active,
	(SELECT name FROM products WHERE id = pr.product_id),
	(SELECT name FROM categories WHERE id = pr.category_id),
	(SELECT count(DISTINCT sales_order_id) FROM sales_order_discounts WHERE promotion_id = pr.id)::int
`

func scanPromotion(row pgx.Row) (promotionResponse, error) {
	var p promotionResponse
	var rawConfig []byte
	err := row.Scan(&p.PromotionID, &p.Name, &p.PromoType, &p.ApplicationLevel, &p.ProductID, &p.CategoryID,
		&p.TargetSegment, &rawConfig, &p.Stacking, &p.StartsAt, &p.EndsAt,
		&p.DaysOfWeek, &p.TimeStart, &p.TimeEnd, &p.Active, &p.ProductName, &p.CategoryName, &p.TimesUsed)
	if err != nil {
		return p, err
	}
	_ = json.Unmarshal(rawConfig, &p.Config)
	return p, nil
}

// ---------------------------------------------------------------------
// POST /promotions — gated by promotions.manage (router.go), same
// "pricing mistakes/promo mistakes are a real business risk" reasoning
// pricing.manage and inventory.adjust already established.
// ---------------------------------------------------------------------

type createPromotionRequest struct {
	Name             string      `json:"name"`
	PromoType        string      `json:"promo_type"`
	ApplicationLevel string      `json:"application_level"` // "order" (default) | "product" | "category"
	ProductID        string      `json:"product_id"`
	CategoryID       string      `json:"category_id"`
	TargetSegment    string      `json:"target_segment"`
	Config           promoConfig `json:"config"`
	Stacking         string      `json:"stacking"` // "exclusive" (default) | "stackable"
	StartsAt         string      `json:"starts_at"`
	EndsAt           string      `json:"ends_at"`
	DaysOfWeek       []int       `json:"days_of_week"`
	TimeStart        string      `json:"time_start"`
	TimeEnd          string      `json:"time_end"`
}

// validatePromotion applies defaults and returns a client-facing message,
// or "" when the promotion is valid. Create and edit share it, so an edit
// can never produce a promotion create would have refused.
func validatePromotion(req *createPromotionRequest) string {
	req.Name = strings.TrimSpace(req.Name)
	if req.ApplicationLevel == "" {
		req.ApplicationLevel = "order"
	}
	if req.Stacking == "" {
		req.Stacking = "exclusive"
	}
	if req.ApplicationLevel != "product" {
		req.ProductID = ""
	}
	if req.ApplicationLevel != "category" {
		req.CategoryID = ""
	}
	switch {
	case req.Name == "":
		return "name is required"
	case req.ApplicationLevel != "order" && req.ApplicationLevel != "product" && req.ApplicationLevel != "category":
		return "application_level must be order, product, or category"
	case req.Stacking != "exclusive" && req.Stacking != "stackable":
		return "stacking must be exclusive or stackable"
	case req.ApplicationLevel == "product" && req.ProductID == "":
		return "choose the product this promotion applies to"
	case req.ApplicationLevel == "category" && req.CategoryID == "":
		return "choose the category this promotion applies to"
	case req.TargetSegment != "" && req.TargetSegment != "vip" && req.TargetSegment != "regular" &&
		req.TargetSegment != "new" && req.TargetSegment != "dormant":
		return "target_segment must be vip, regular, new or dormant"
	case (req.TimeStart == "") != (req.TimeEnd == ""):
		return "time_start and time_end go together"
	}
	if validateConfig(req.PromoType, req.ApplicationLevel, req.Config) != nil {
		return "config does not match promo_type's required fields"
	}
	for _, d := range req.DaysOfWeek {
		if d < 0 || d > 6 {
			return "days_of_week values are 0 (Sunday) to 6 (Saturday)"
		}
	}
	if req.TimeStart != "" {
		start, err1 := time.Parse("15:04", req.TimeStart)
		end, err2 := time.Parse("15:04", req.TimeEnd)
		if err1 != nil || err2 != nil {
			return "time_start/time_end must be HH:MM (24-hour)"
		}
		if start.Equal(end) {
			return "time_start and time_end can't be equal"
		}
	}
	var starts, ends time.Time
	var err error
	if req.StartsAt != "" {
		if starts, err = time.Parse(time.RFC3339, req.StartsAt); err != nil {
			return "starts_at must be an RFC3339 timestamp"
		}
	}
	if req.EndsAt != "" {
		if ends, err = time.Parse(time.RFC3339, req.EndsAt); err != nil {
			return "ends_at must be an RFC3339 timestamp"
		}
	}
	if req.StartsAt != "" && req.EndsAt != "" && !starts.Before(ends) {
		return "starts_at must be before ends_at"
	}
	return ""
}

func (h *Handler) CreatePromotion(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req createPromotionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if msg := validatePromotion(&req); msg != "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", msg)
		return
	}
	daysOfWeek := ([]int)(nil)
	if len(req.DaysOfWeek) > 0 {
		daysOfWeek = req.DaysOfWeek
	}

	var promotionID string
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO promotions
				(id, merchant_id, name, promo_type, application_level, product_id, category_id,
				 target_segment, config, stacking, starts_at, ends_at, days_of_week, time_start, time_end)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3,
			        NULLIF($4,'')::uuid, NULLIF($5,'')::uuid, NULLIF($6,''), $7, $8,
			        NULLIF($9,'')::timestamptz, NULLIF($10,'')::timestamptz, $11,
			        NULLIF($12,'')::time, NULLIF($13,'')::time)
			RETURNING id`,
			req.Name, req.PromoType, req.ApplicationLevel, req.ProductID, req.CategoryID,
			req.TargetSegment, marshalConfig(req.Config), req.Stacking, req.StartsAt, req.EndsAt,
			daysOfWeek, req.TimeStart, req.TimeEnd,
		).Scan(&promotionID)
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create promotion")
		return
	}

	promo, err := h.fetchByID(r.Context(), claims.TenantID, promotionID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "promotion created but could not be reloaded")
		return
	}
	httpx.JSON(w, http.StatusCreated, promo)
}

// ---------------------------------------------------------------------
// GET /promotions?active=&promo_type= — open to any authenticated user
// (not gated), same as GET /branches: reads aren't the business risk,
// writes are.
// ---------------------------------------------------------------------

func (h *Handler) ListPromotions(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	q := r.URL.Query()
	activeFilter := q.Get("active")
	promoType := q.Get("promo_type")

	list := []promotionResponse{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT `+promotionColumns+`
			FROM promotions pr
			WHERE ($1 = '' OR pr.active = ($1 = 'true'))
			  AND ($2 = '' OR pr.promo_type = $2)
			ORDER BY pr.created_at DESC`, activeFilter, promoType)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			p, err := scanPromotion(rows)
			if err != nil {
				return err
			}
			list = append(list, p)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list promotions")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"promotions": list})
}

// ---------------------------------------------------------------------
// PATCH /promotions/{id} — gated by promotions.manage. Every field is
// editable (absent = unchanged), and the merged result goes through the
// same validatePromotion as create. A discount already given on a past
// sale is recorded on that sale (sales_order_discounts), so editing the
// terms changes only future sales.
// ---------------------------------------------------------------------

type updatePromotionRequest struct {
	Name             *string      `json:"name"`
	PromoType        *string      `json:"promo_type"`
	ApplicationLevel *string      `json:"application_level"`
	ProductID        *string      `json:"product_id"`
	CategoryID       *string      `json:"category_id"`
	TargetSegment    *string      `json:"target_segment"`
	Config           *promoConfig `json:"config"`
	Stacking         *string      `json:"stacking"`
	StartsAt         *string      `json:"starts_at"`
	EndsAt           *string      `json:"ends_at"`
	DaysOfWeek       *[]int       `json:"days_of_week"`
	TimeStart        *string      `json:"time_start"`
	TimeEnd          *string      `json:"time_end"`
	Active           *bool        `json:"active"`
}

var errPromotionInvalid = errors.New("invalid promotion")

func (h *Handler) UpdatePromotion(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	promotionID := chi.URLParam(r, "id")

	var req updatePromotionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}

	var invalid string
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		cur, err := scanPromotion(tx.QueryRow(ctx, `SELECT `+promotionColumns+` FROM promotions pr WHERE pr.id = $1 FOR UPDATE OF pr`, promotionID))
		if err != nil {
			return err
		}
		m := createPromotionRequest{
			Name: cur.Name, PromoType: cur.PromoType, ApplicationLevel: cur.ApplicationLevel,
			ProductID: derefOr(cur.ProductID, ""), CategoryID: derefOr(cur.CategoryID, ""),
			TargetSegment: derefOr(cur.TargetSegment, ""), Config: cur.Config, Stacking: cur.Stacking,
			StartsAt: rfc3339(cur.StartsAt), EndsAt: rfc3339(cur.EndsAt),
			TimeStart: derefOr(cur.TimeStart, ""), TimeEnd: derefOr(cur.TimeEnd, ""),
		}
		for _, d := range cur.DaysOfWeek {
			m.DaysOfWeek = append(m.DaysOfWeek, int(d))
		}
		for _, f := range []struct{ dst, src *string }{
			{&m.Name, req.Name}, {&m.PromoType, req.PromoType}, {&m.ApplicationLevel, req.ApplicationLevel},
			{&m.ProductID, req.ProductID}, {&m.CategoryID, req.CategoryID}, {&m.TargetSegment, req.TargetSegment},
			{&m.Stacking, req.Stacking}, {&m.StartsAt, req.StartsAt}, {&m.EndsAt, req.EndsAt},
			{&m.TimeStart, req.TimeStart}, {&m.TimeEnd, req.TimeEnd},
		} {
			if f.src != nil {
				*f.dst = *f.src
			}
		}
		if req.Config != nil {
			m.Config = *req.Config
		}
		if req.DaysOfWeek != nil {
			m.DaysOfWeek = *req.DaysOfWeek
		}
		if invalid = validatePromotion(&m); invalid != "" {
			return errPromotionInvalid
		}
		days := ([]int)(nil)
		if len(m.DaysOfWeek) > 0 {
			days = m.DaysOfWeek
		}
		_, err = tx.Exec(ctx, `
			UPDATE promotions SET
				name = $1, promo_type = $2, application_level = $3,
				product_id = NULLIF($4,'')::uuid, category_id = NULLIF($5,'')::uuid, target_segment = NULLIF($6,''),
				config = $7, stacking = $8,
				starts_at = NULLIF($9,'')::timestamptz, ends_at = NULLIF($10,'')::timestamptz,
				days_of_week = $11, time_start = NULLIF($12,'')::time, time_end = NULLIF($13,'')::time,
				active = COALESCE($14, active)
			WHERE id = $15`,
			m.Name, m.PromoType, m.ApplicationLevel, m.ProductID, m.CategoryID, m.TargetSegment,
			marshalConfig(m.Config), m.Stacking, m.StartsAt, m.EndsAt, days, m.TimeStart, m.TimeEnd,
			req.Active, promotionID)
		return err
	})

	switch {
	case errors.Is(err, errPromotionInvalid):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", invalid)
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "promotion not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not update promotion")
	default:
		promo, err := h.fetchByID(r.Context(), claims.TenantID, promotionID)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "promotion updated but could not be reloaded")
			return
		}
		httpx.JSON(w, http.StatusOK, promo)
	}
}

// DeletePromotion: DELETE /promotions/{id} — gated by promotions.manage.
// 204, or 409 IN_USE once a sale carries its discount (that sale's
// discount record points at it): deactivate it instead. Same shape as the
// master deletes, so the admin's dependency dialog handles both.
func (h *Handler) DeletePromotion(w http.ResponseWriter, r *http.Request) {
	h.deleteIfUnused(w, r, "promotions", "promotion")
}

// DeleteCoupon: DELETE /coupons/{id} — same rule: a coupon redeemed on any
// sale stays, deactivate it instead.
func (h *Handler) DeleteCoupon(w http.ResponseWriter, r *http.Request) {
	h.deleteIfUnused(w, r, "coupons", "coupon")
}

func (h *Handler) deleteIfUnused(w http.ResponseWriter, r *http.Request, table, what string) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	t := pgx.Identifier{table}.Sanitize()
	var blockers []dependencies.Dependency
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var locked string
		if err := tx.QueryRow(ctx, `SELECT id FROM `+t+` WHERE id::text = $1 FOR UPDATE`, id).Scan(&locked); err != nil {
			return err
		}
		deps, err := dependencies.Count(ctx, tx, table, []string{id})
		if err != nil {
			return err
		}
		if dependencies.AnyBlocking(deps) {
			blockers = deps
			return nil
		}
		return softdelete.Delete(ctx, tx, table, locked, claims.UserID, "")
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", what+" not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not delete "+what)
	case blockers != nil:
		httpx.ErrorWithDetails(w, http.StatusConflict, "IN_USE",
			"this "+what+" has been used on sales — deactivate it instead", map[string]any{"dependencies": blockers})
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// rfc3339 turns a timestamptz::text value back into the RFC3339 form the
// API accepts, so an edit that leaves a date alone re-validates cleanly.
func rfc3339(s *string) string {
	if s == nil || *s == "" {
		return ""
	}
	for _, layout := range []string{"2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999-07"} {
		if t, err := time.Parse(layout, *s); err == nil {
			return t.Format(time.RFC3339)
		}
	}
	return *s
}

func derefOr(s *string, fallback string) string {
	if s == nil {
		return fallback
	}
	return *s
}

func (h *Handler) fetchByID(ctx context.Context, tenantID, promotionID string) (promotionResponse, error) {
	var p promotionResponse
	err := h.DB.WithTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `SELECT `+promotionColumns+` FROM promotions pr WHERE pr.id = $1`, promotionID)
		var scanErr error
		p, scanErr = scanPromotion(row)
		return scanErr
	})
	return p, err
}
