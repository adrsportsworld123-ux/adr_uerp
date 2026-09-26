package ai

import (
	"context"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

type recommendation struct {
	VariantID         string `json:"variant_id"`
	ProductName       string `json:"product_name"`
	SKU               string `json:"sku"`
	SellingPrice      string `json:"selling_price"`
	CoOccurrenceCount int    `json:"co_occurrence_count"`
	Confidence        string `json:"confidence"` // P(this variant | the source variant), 0-1
}

// GetRecommendations: GET /ai/recommendations/{variant_id}?limit= — a
// market-basket "customers who bought this also bought..." list, computed
// directly from this merchant's own finalized-order history (never a
// trained model): for every other finalized order the source variant
// appeared in, count how often each OTHER variant appeared in that same
// order, then rank by that count. confidence is the classic market-basket
// metric P(B|A) — of the orders containing the source variant, what
// fraction also contained this one — not "lift" (which would also divide
// by B's overall popularity); a real v2 worth adding once this is proven
// useful, not built speculatively here.
//
// Open to any authenticated user, not permission-gated — this is exactly
// the kind of read a POS terminal would want at checkout ("frequently
// bought with this") as much as a back-office merchandiser would, and it
// exposes nothing beyond product names/SKUs/prices every role can already
// see via GET /products.
func (h *Handler) GetRecommendations(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	variantID := chi.URLParam(r, "variant_id")
	limit := 5
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 50 {
		limit = v
	}

	var sourceOrderCount int
	recommendations := []recommendation{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			SELECT COUNT(DISTINCT sol.sales_order_id)
			FROM sales_order_lines sol
			JOIN sales_orders so ON so.id = sol.sales_order_id
			WHERE sol.variant_id = $1 AND so.status = 'finalized'`, variantID,
		).Scan(&sourceOrderCount); err != nil {
			return err
		}
		if sourceOrderCount == 0 {
			return nil // never sold (or never in a finalized order) — nothing to correlate against, not an error
		}

		rows, err := tx.Query(ctx, `
			WITH source_orders AS (
				SELECT DISTINCT sol.sales_order_id
				FROM sales_order_lines sol
				JOIN sales_orders so ON so.id = sol.sales_order_id
				WHERE sol.variant_id = $1 AND so.status = 'finalized'
			)
			SELECT sol2.variant_id::text, p.name, pv.sku, pv.selling_price::text,
			       COUNT(DISTINCT sol2.sales_order_id) AS co_count
			FROM sales_order_lines sol2
			JOIN source_orders src ON src.sales_order_id = sol2.sales_order_id
			JOIN product_variants pv ON pv.id = sol2.variant_id
			JOIN products p ON p.id = pv.product_id
			WHERE sol2.variant_id != $1
			GROUP BY sol2.variant_id, p.name, pv.sku, pv.selling_price
			ORDER BY co_count DESC, p.name
			LIMIT $2`, variantID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var rec recommendation
			if err := rows.Scan(&rec.VariantID, &rec.ProductName, &rec.SKU, &rec.SellingPrice, &rec.CoOccurrenceCount); err != nil {
				return err
			}
			rec.Confidence = strconv.FormatFloat(float64(rec.CoOccurrenceCount)/float64(sourceOrderCount), 'f', 3, 64)
			recommendations = append(recommendations, rec)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not compute recommendations")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"variant_id":         variantID,
		"source_order_count": sourceOrderCount,
		"recommendations":    recommendations,
	})
}
