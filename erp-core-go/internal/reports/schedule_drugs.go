package reports

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

// Phase 8 (Pharmacy): "regulatory reporting" — an honest scope note: this
// is a real report over this merchant's own already-posted transaction
// data (which finalized lines sold a scheduled drug, and whether a
// prescription was on file for that sale), for internal audit visibility.
// It is NOT a specific government e-filing/API integration to a state
// drug-control authority — that's a real, separate, much bigger
// compliance project this doesn't pretend to replace.

type scheduleDrugSaleLine struct {
	OrderID        string  `json:"order_id"`
	OrderNumber    string  `json:"order_number"`
	FinalizedAt    string  `json:"finalized_at"`
	VariantID      string  `json:"variant_id"`
	SKU            string  `json:"sku"`
	ProductName    string  `json:"product_name"`
	DrugSchedule   string  `json:"drug_schedule"`
	Quantity       string  `json:"quantity"`
	PrescriptionID *string `json:"prescription_id"`
}

// GetScheduleDrugSales: GET /reports/schedule-drug-sales?branch_id=&date=
func (h *Handler) GetScheduleDrugSales(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	branchID := r.URL.Query().Get("branch_id")
	date, ok := dateParam(r)
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "date must be YYYY-MM-DD")
		return
	}

	lines := []scheduleDrugSaleLine{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT so.id::text, so.order_number, so.finalized_at::text,
			       sol.variant_id::text, pv.sku, p.name,
			       pv.attribute_combo->>'Drug Schedule', sol.quantity::text,
			       so.prescription_id::text
			FROM sales_order_lines sol
			JOIN sales_orders so ON so.id = sol.sales_order_id
			JOIN product_variants pv ON pv.id = sol.variant_id
			JOIN products p ON p.id = pv.product_id
			WHERE so.status = 'finalized'
			  AND so.finalized_at::date = $1::date
			  AND ($2 = '' OR so.branch_id::text = $2)
			  AND COALESCE(pv.attribute_combo->>'Drug Schedule', 'OTC') != 'OTC'
			ORDER BY so.finalized_at`, date, branchID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var l scheduleDrugSaleLine
			if err := rows.Scan(&l.OrderID, &l.OrderNumber, &l.FinalizedAt, &l.VariantID, &l.SKU, &l.ProductName,
				&l.DrugSchedule, &l.Quantity, &l.PrescriptionID); err != nil {
				return err
			}
			lines = append(lines, l)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not build schedule-drug sales report")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"date": date, "branch_id": branchID, "lines": lines})
}
