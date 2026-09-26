// Package pricing implements Phase 2's Pricing Management sub-area
// (phased_roadmap.md; pos_frd_complete.md §11) — cost/MRP/selling price
// management, margin calculation, and bulk updates against the one
// selling_price product_variants has — plus, since Phase 7
// (price_lists.go, resolve.go), wholesale price lists: named per-variant
// override price sets a customer can be assigned to. Still deliberately
// deferred (see migrations/009_pricing.sql's header, and
// migrations/026_wholesale_b2b.sql's for the Phase 7 follow-up on this
// same note): scheduled future-dated price changes and dynamic/
// time-based pricing.
package pricing

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/search"
)

type Handler struct {
	DB *db.DB
	// Search is optional (nil, or a disabled *search.Client, are both
	// fine) — pricing must keep working with search unconfigured. When
	// present, a successful price change is reindexed so the search
	// screen's prices don't drift stale until the next full reindex.
	Search *search.Client
}

var errNegativeMargin = errors.New("this price would result in a negative margin")

func marginPct(sellingPrice, costPrice float64) *float64 {
	if sellingPrice <= 0 {
		return nil
	}
	m := round2((sellingPrice - costPrice) / sellingPrice * 100)
	return &m
}

func markupPct(sellingPrice, costPrice float64) *float64 {
	if costPrice <= 0 {
		return nil
	}
	m := round2((sellingPrice - costPrice) / costPrice * 100)
	return &m
}

// ---------------------------------------------------------------------
// POST /pricing/calculate — a pure calculator, no DB access: the FRD's
// "cost-plus pricing tool" (cost + markup% = selling price) and "target
// margin tool" (cost + margin% = selling price) in one endpoint.
// ---------------------------------------------------------------------

type calculateRequest struct {
	CostPrice float64  `json:"cost_price"`
	MarkupPct *float64 `json:"markup_pct"`
	MarginPct *float64 `json:"margin_pct"`
}

type calculateResponse struct {
	SellingPrice float64  `json:"selling_price"`
	MarginPct    *float64 `json:"margin_pct"`
	MarkupPct    *float64 `json:"markup_pct"`
}

func (h *Handler) Calculate(w http.ResponseWriter, r *http.Request) {
	if _, ok := authn.FromContext(r.Context()); !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req calculateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.CostPrice <= 0 || (req.MarkupPct == nil) == (req.MarginPct == nil) {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "cost_price and exactly one of markup_pct/margin_pct are required")
		return
	}

	var sellingPrice float64
	switch {
	case req.MarkupPct != nil:
		sellingPrice = req.CostPrice * (1 + *req.MarkupPct/100)
	case req.MarginPct != nil:
		if *req.MarginPct >= 100 {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "margin_pct must be less than 100")
			return
		}
		sellingPrice = req.CostPrice / (1 - *req.MarginPct/100)
	}

	httpx.JSON(w, http.StatusOK, calculateResponse{
		SellingPrice: round2(sellingPrice),
		MarginPct:    marginPct(sellingPrice, req.CostPrice),
		MarkupPct:    markupPct(sellingPrice, req.CostPrice),
	})
}

// ---------------------------------------------------------------------
// PATCH /pricing/variants/{id} — the actual write. Blocks a resulting
// negative margin unless override is set (FRD: "Block negative margin
// sales (override required)" — applied here at price-setting time, not
// at checkout, since that's where a negative margin is actually decided).
// ---------------------------------------------------------------------

type updatePricingRequest struct {
	CostPrice    *float64 `json:"cost_price"`
	MRP          *float64 `json:"mrp"`
	SellingPrice *float64 `json:"selling_price"`
	Reason       string   `json:"reason"`
	Override     bool     `json:"override"`
}

type pricingResponse struct {
	VariantID    string   `json:"variant_id"`
	CostPrice    float64  `json:"cost_price"`
	MRP          float64  `json:"mrp"`
	SellingPrice float64  `json:"selling_price"`
	MarginPct    *float64 `json:"margin_pct"`
	MarkupPct    *float64 `json:"markup_pct"`
}

func (h *Handler) UpdateVariantPricing(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	variantID := chi.URLParam(r, "id")

	var req updatePricingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}

	var resp pricingResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var costPrice, mrp, sellingPrice float64
		if err := tx.QueryRow(ctx, `SELECT cost_price, mrp, selling_price FROM product_variants WHERE id = $1`, variantID).
			Scan(&costPrice, &mrp, &sellingPrice); err != nil {
			return err
		}

		newCost, newMRP, newSelling := costPrice, mrp, sellingPrice
		if req.CostPrice != nil {
			newCost = *req.CostPrice
		}
		if req.MRP != nil {
			newMRP = *req.MRP
		}
		if req.SellingPrice != nil {
			newSelling = *req.SellingPrice
		}

		if newSelling < newCost && !req.Override {
			return errNegativeMargin
		}

		if _, err := tx.Exec(ctx, `UPDATE product_variants SET cost_price = $1, mrp = $2, selling_price = $3, updated_at = now() WHERE id = $4`,
			newCost, newMRP, newSelling, variantID); err != nil {
			return err
		}

		if err := recordPriceChange(ctx, tx, variantID, "cost_price", costPrice, newCost, req.Reason, claims.UserID); err != nil {
			return err
		}
		if err := recordPriceChange(ctx, tx, variantID, "mrp", mrp, newMRP, req.Reason, claims.UserID); err != nil {
			return err
		}
		if err := recordPriceChange(ctx, tx, variantID, "selling_price", sellingPrice, newSelling, req.Reason, claims.UserID); err != nil {
			return err
		}

		resp = pricingResponse{
			VariantID: variantID, CostPrice: newCost, MRP: newMRP, SellingPrice: newSelling,
			MarginPct: marginPct(newSelling, newCost), MarkupPct: markupPct(newSelling, newCost),
		}
		return nil
	})

	switch {
	case errors.Is(err, errNegativeMargin):
		httpx.Error(w, http.StatusConflict, "NEGATIVE_MARGIN", "this price would sell below cost — pass override:true to allow it anyway")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "variant not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not update pricing")
	default:
		h.reindex(r.Context(), claims.TenantID, variantID)
		httpx.JSON(w, http.StatusOK, resp)
	}
}

// reindex is best-effort and never affects the request's outcome — a
// search index write failing must not roll back or fail a price change
// that already committed in Postgres, the actual system of record.
func (h *Handler) reindex(ctx context.Context, tenantID, variantID string) {
	if !h.Search.Enabled() {
		return
	}
	doc, err := search.FetchDocument(ctx, h.DB, tenantID, variantID)
	if err != nil {
		log.Printf("pricing: reindex variant %s: fetch: %v", variantID, err)
		return
	}
	if err := h.Search.IndexDocument(ctx, doc); err != nil {
		log.Printf("pricing: reindex variant %s: index: %v", variantID, err)
	}
}

// recordPriceChange writes a price_history row only when the field
// actually changed — an update that only touches selling_price shouldn't
// leave two no-op history rows for cost_price/mrp sitting at the same value.
func recordPriceChange(ctx context.Context, tx pgx.Tx, variantID, field string, oldValue, newValue float64, reason, changedBy string) error {
	if oldValue == newValue {
		return nil
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO price_history (id, merchant_id, variant_id, field, old_value, new_value, reason, changed_by)
		VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, $5, $6)`,
		variantID, field, oldValue, newValue, reason, changedBy)
	return err
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}
