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

// Branch overrides of the sales policy (migrations/062). Today one: whether
// open-price items take discounts and promotions at that branch. NULL =
// the merchant's default (sales_settings.open_price_discounts).

type branchSalesSetting struct {
	BranchID           string `json:"branch_id"`
	BranchName         string `json:"branch_name"`
	OpenPriceDiscounts *bool  `json:"open_price_discounts"` // null = the merchant's
	Effective          bool   `json:"effective_open_price_discounts"`
}

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
		SELECT b.id::text, b.name, bs.open_price_discounts, app_open_price_discountable(b.id)
		FROM branches b LEFT JOIN branch_sales_settings bs ON bs.branch_id = b.id
		ORDER BY b.name`)
	if err != nil {
		return resp, err
	}
	defer rows.Close()
	resp.Branches = []branchSalesSetting{}
	for rows.Next() {
		var b branchSalesSetting
		if err := rows.Scan(&b.BranchID, &b.BranchName, &b.OpenPriceDiscounts, &b.Effective); err != nil {
			return resp, err
		}
		resp.Branches = append(resp.Branches, b)
	}
	return resp, rows.Err()
}

// UpdateBranchSettings: PUT /sales/settings/branches/{id} — sales.settings.
// {"open_price_discounts": true | false | null}; null = follow the merchant.
func (h *Handler) UpdateBranchSettings(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	branchID := chi.URLParam(r, "id")
	var req struct {
		OpenPriceDiscounts *bool `json:"open_price_discounts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	var resp salesSettingsResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var before *bool
		var exists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM branches WHERE id::text = $1),
			       (SELECT open_price_discounts FROM branch_sales_settings WHERE branch_id::text = $1)`, branchID).Scan(&exists, &before); err != nil {
			return err
		}
		if !exists {
			return pgx.ErrNoRows
		}
		var err error
		if req.OpenPriceDiscounts == nil {
			_, err = tx.Exec(ctx, `DELETE FROM branch_sales_settings WHERE branch_id::text = $1`, branchID)
		} else {
			_, err = tx.Exec(ctx, `
				INSERT INTO branch_sales_settings (branch_id, merchant_id, open_price_discounts, updated_by)
				VALUES ($1::uuid, current_setting('app.tenant_id')::uuid, $2, $3)
				ON CONFLICT (branch_id) DO UPDATE SET open_price_discounts = $2, updated_by = $3, updated_at = now()`,
				branchID, *req.OpenPriceDiscounts, claims.UserID)
		}
		if err != nil {
			return err
		}
		if resp, err = loadSalesSettingsResponse(ctx, tx); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "branch_sales_settings", EntityID: branchID, Action: "update", PerformedBy: claims.UserID,
			Before: map[string]any{"open_price_discounts": before}, After: map[string]any{"open_price_discounts": req.OpenPriceDiscounts}})
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
