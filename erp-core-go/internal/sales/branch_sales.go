package sales

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

// Branch overrides of the sales policy: whether open-price items take
// discounts and promotions (migrations/062), and whether the POS grid
// lists them first (migrations/063). NULL = the merchant's default.

type branchSalesSetting struct {
	BranchID           string `json:"branch_id"`
	BranchName         string `json:"branch_name"`
	OpenPriceDiscounts *bool  `json:"open_price_discounts"` // null = the merchant's
	Effective          bool   `json:"effective_open_price_discounts"`
	OpenPriceFirst     *bool  `json:"open_price_first"` // null = the merchant's
	EffectiveFirst     bool   `json:"effective_open_price_first"`
	// migrations/071: standard | customer | shop; null = the merchant's.
	PriceBenefit     *string `json:"price_benefit"`
	EffectiveBenefit string  `json:"effective_price_benefit"`
}

func validBenefit(s string) bool { return s == "standard" || s == "customer" || s == "shop" }

// salesSettingsResponse is GET/PUT /sales/settings: the merchant's policy
// and every branch's override. Additive: the merchant fields are as before.
type salesSettingsResponse struct {
	salesSettings
	Branches []branchSalesSetting `json:"branches"`
}

func loadSalesSettingsResponse(ctx context.Context, tx pgx.Tx) (salesSettingsResponse, error) {
	var resp salesSettingsResponse
	var err error
	if resp.salesSettings, err = loadSalesSettings(ctx, tx); err != nil {
		return resp, err
	}
	rows, err := tx.Query(ctx, `
		SELECT b.id::text, b.name, bs.open_price_discounts, app_open_price_discountable(b.id),
		       bs.open_price_first, app_open_price_first(b.id), bs.price_benefit, app_price_benefit(b.id)
		FROM branches b LEFT JOIN branch_sales_settings bs ON bs.branch_id = b.id
		ORDER BY b.name`)
	if err != nil {
		return resp, err
	}
	defer rows.Close()
	resp.Branches = []branchSalesSetting{}
	for rows.Next() {
		var b branchSalesSetting
		if err := rows.Scan(&b.BranchID, &b.BranchName, &b.OpenPriceDiscounts, &b.Effective, &b.OpenPriceFirst, &b.EffectiveFirst,
			&b.PriceBenefit, &b.EffectiveBenefit); err != nil {
			return resp, err
		}
		resp.Branches = append(resp.Branches, b)
	}
	return resp, rows.Err()
}

// UpdateBranchSettings: PUT /sales/settings/branches/{id} — sales.settings.
// {"open_price_discounts": true | false | null, "open_price_first": ...,
// "price_benefit": "standard" | "customer" | "shop" | null};
// null = follow the merchant; a field left out keeps what is saved.
func (h *Handler) UpdateBranchSettings(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	branchID := chi.URLParam(r, "id")
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	field := func(name string) (set bool, v *bool, ok bool) {
		m, present := raw[name]
		if !present {
			return false, nil, true
		}
		err := json.Unmarshal(m, &v)
		return true, v, err == nil
	}
	discSet, disc, ok1 := field("open_price_discounts")
	firstSet, first, ok2 := field("open_price_first")
	if !ok1 || !ok2 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "open_price_discounts and open_price_first are true, false or null")
		return
	}
	var benefit *string
	_, benefitSet := raw["price_benefit"]
	if benefitSet {
		if err := json.Unmarshal(raw["price_benefit"], &benefit); err != nil || (benefit != nil && !validBenefit(*benefit)) {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "price_benefit is standard, customer, shop or null")
			return
		}
	}
	var resp salesSettingsResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var beforeDisc, beforeFirst *bool
		var beforeBenefit *string
		var exists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM branches WHERE id::text = $1),
			       (SELECT open_price_discounts FROM branch_sales_settings WHERE branch_id::text = $1),
			       (SELECT open_price_first FROM branch_sales_settings WHERE branch_id::text = $1),
			       (SELECT price_benefit FROM branch_sales_settings WHERE branch_id::text = $1)`, branchID).
			Scan(&exists, &beforeDisc, &beforeFirst, &beforeBenefit); err != nil {
			return err
		}
		if !exists {
			return pgx.ErrNoRows
		}
		if !discSet {
			disc = beforeDisc
		}
		if !firstSet {
			first = beforeFirst
		}
		if !benefitSet {
			benefit = beforeBenefit
		}
		var err error
		if disc == nil && first == nil && benefit == nil {
			_, err = tx.Exec(ctx, `DELETE FROM branch_sales_settings WHERE branch_id::text = $1`, branchID)
		} else {
			_, err = tx.Exec(ctx, `
				INSERT INTO branch_sales_settings (branch_id, merchant_id, open_price_discounts, open_price_first, price_benefit, updated_by)
				VALUES ($1::uuid, current_setting('app.tenant_id')::uuid, $2, $3, $5, $4)
				ON CONFLICT (branch_id) DO UPDATE SET open_price_discounts = $2, open_price_first = $3, price_benefit = $5, updated_by = $4, updated_at = now()`,
				branchID, disc, first, claims.UserID, benefit)
		}
		if err != nil {
			return err
		}
		if resp, err = loadSalesSettingsResponse(ctx, tx); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "branch_sales_settings", EntityID: branchID, Action: "update", PerformedBy: claims.UserID,
			Before: map[string]any{"open_price_discounts": beforeDisc, "open_price_first": beforeFirst, "price_benefit": beforeBenefit},
			After:  map[string]any{"open_price_discounts": disc, "open_price_first": first, "price_benefit": benefit}})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "branch not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not save the branch's sales policy")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}
