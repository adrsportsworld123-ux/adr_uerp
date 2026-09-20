package promotions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
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
	Active           bool        `json:"active"`
}

const promotionColumns = `
	id, name, promo_type, application_level, product_id::text, category_id::text,
	target_segment, config, stacking, starts_at::text, ends_at::text, active
`

func scanPromotion(row pgx.Row) (promotionResponse, error) {
	var p promotionResponse
	var rawConfig []byte
	err := row.Scan(&p.PromotionID, &p.Name, &p.PromoType, &p.ApplicationLevel, &p.ProductID, &p.CategoryID,
		&p.TargetSegment, &rawConfig, &p.Stacking, &p.StartsAt, &p.EndsAt, &p.Active)
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
	if req.ApplicationLevel == "" {
		req.ApplicationLevel = "order"
	}
	if req.Stacking == "" {
		req.Stacking = "exclusive"
	}
	if req.Name == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "name is required")
		return
	}
	if req.ApplicationLevel != "order" && req.ApplicationLevel != "product" && req.ApplicationLevel != "category" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "application_level must be order, product, or category")
		return
	}
	if req.Stacking != "exclusive" && req.Stacking != "stackable" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "stacking must be exclusive or stackable")
		return
	}
	if (req.ApplicationLevel == "product") != (req.ProductID != "") {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "product_id is required for, and only for, application_level=product")
		return
	}
	if (req.ApplicationLevel == "category") != (req.CategoryID != "") {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "category_id is required for, and only for, application_level=category")
		return
	}
	if err := validateConfig(req.PromoType, req.ApplicationLevel, req.Config); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "config does not match promo_type's required fields")
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
			FROM promotions
			WHERE ($1 = '' OR active = ($1 = 'true'))
			  AND ($2 = '' OR promo_type = $2)
			ORDER BY created_at DESC`, activeFilter, promoType)
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
// PATCH /promotions/{id} — gated by promotions.manage. Deliberately no
// DELETE, matching Pricing's PATCH-only precedent: deactivate via
// active:false rather than removing history a past sale's
// sales_order_discounts.promotion_id might still reference.
// ---------------------------------------------------------------------

type updatePromotionRequest struct {
	Name     *string      `json:"name"`
	Config   *promoConfig `json:"config"`
	Stacking *string      `json:"stacking"`
	StartsAt *string      `json:"starts_at"`
	EndsAt   *string      `json:"ends_at"`
	Active   *bool        `json:"active"`
}

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
	if req.Stacking != nil && *req.Stacking != "exclusive" && *req.Stacking != "stackable" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "stacking must be exclusive or stackable")
		return
	}

	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if req.Config != nil {
			var promoType, applicationLevel string
			if err := tx.QueryRow(ctx, `SELECT promo_type, application_level FROM promotions WHERE id = $1`, promotionID).
				Scan(&promoType, &applicationLevel); err != nil {
				return err
			}
			if err := validateConfig(promoType, applicationLevel, *req.Config); err != nil {
				return errInvalidPromoConfig
			}
		}
		var rawConfig []byte
		if req.Config != nil {
			rawConfig = marshalConfig(*req.Config)
		}
		_, err := tx.Exec(ctx, `
			UPDATE promotions SET
				name = COALESCE($1, name),
				config = COALESCE($2, config),
				stacking = COALESCE($3, stacking),
				starts_at = CASE WHEN $4 THEN NULLIF($5,'')::timestamptz ELSE starts_at END,
				ends_at = CASE WHEN $6 THEN NULLIF($7,'')::timestamptz ELSE ends_at END,
				active = COALESCE($8, active)
			WHERE id = $9`,
			req.Name, rawConfig, req.Stacking,
			req.StartsAt != nil, derefOr(req.StartsAt, ""),
			req.EndsAt != nil, derefOr(req.EndsAt, ""),
			req.Active, promotionID)
		return err
	})

	switch {
	case errors.Is(err, errInvalidPromoConfig):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "config does not match promo_type's required fields")
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

func derefOr(s *string, fallback string) string {
	if s == nil {
		return fallback
	}
	return *s
}

func (h *Handler) fetchByID(ctx context.Context, tenantID, promotionID string) (promotionResponse, error) {
	var p promotionResponse
	err := h.DB.WithTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `SELECT `+promotionColumns+` FROM promotions WHERE id = $1`, promotionID)
		var scanErr error
		p, scanErr = scanPromotion(row)
		return scanErr
	})
	return p, err
}
