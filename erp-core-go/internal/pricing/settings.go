package pricing

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

// ---------------------------------------------------------------------
// GET/PUT /pricing/settings — merchant-wide pricing defaults
// (migrations/033's pricing_settings). Today that's one setting: whether
// newly created products default to tax-inclusive selling prices. It's a
// default only — every product carries its own products.price_includes_tax,
// and changing this never touches existing products.
// ---------------------------------------------------------------------

type pricingSettings struct {
	PricesIncludeTaxDefault bool `json:"prices_include_tax_default"`
}

type updatePricingSettingsRequest struct {
	PricesIncludeTaxDefault *bool `json:"prices_include_tax_default"`
}

// GetSettings is readable by any authenticated user — erp-web-admin's New
// Product form uses it to pre-fill the inclusive/exclusive toggle. A
// merchant that never saved settings gets the all-false defaults, not 404.
func (h *Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var resp pricingSettings
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT COALESCE((SELECT prices_include_tax_default FROM pricing_settings), false)`).
			Scan(&resp.PricesIncludeTaxDefault)
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load pricing settings")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// UpdateSettings is gated by pricing.manage at the router. PUT is
// naturally idempotent here (an upsert of the same value), so no
// idempotency key is needed for a client retry.
func (h *Handler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req updatePricingSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.PricesIncludeTaxDefault == nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "prices_include_tax_default is required")
		return
	}

	var resp pricingSettings
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO pricing_settings (merchant_id, prices_include_tax_default, updated_by, updated_at)
			VALUES (current_setting('app.tenant_id')::uuid, $1, $2, now())
			ON CONFLICT (merchant_id) DO UPDATE
			  SET prices_include_tax_default = EXCLUDED.prices_include_tax_default,
			      updated_by = EXCLUDED.updated_by, updated_at = now()
			RETURNING prices_include_tax_default`, *req.PricesIncludeTaxDefault, claims.UserID,
		).Scan(&resp.PricesIncludeTaxDefault)
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not save pricing settings")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// loadTaxBasis returns a variant's total tax rate and whether its
// product's prices are tax-inclusive — what margin and negative-margin
// checks need to compare a selling price against (tax-exclusive) cost.
func loadTaxBasis(ctx context.Context, tx pgx.Tx, variantID string) (taxRatePct float64, inclusive bool, err error) {
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(ts.cgst_rate,0) + COALESCE(ts.sgst_rate,0) + COALESCE(ts.igst_rate,0) + COALESCE(ts.cess_rate,0),
		       p.price_includes_tax
		FROM product_variants pv
		JOIN products p ON p.id = pv.product_id
		LEFT JOIN tax_slabs ts ON ts.id = p.tax_slab_id
		WHERE pv.id = $1`, variantID,
	).Scan(&taxRatePct, &inclusive)
	return taxRatePct, inclusive, err
}
