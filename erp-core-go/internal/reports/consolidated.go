package reports

import (
	"context"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

type branchSalesLine struct {
	BranchID   string `json:"branch_id"`
	BranchName string `json:"branch_name"`
	OrderCount int    `json:"order_count"`
	GrandTotal string `json:"grand_total"`
}

// ConsolidatedSales: GET /reports/consolidated-sales?date=YYYY-MM-DD — the
// "consolidated cross-branch reporting" phased_roadmap.md's Multi-Branch
// sub-area asks for: every branch's finalized-sales total for a date, not
// scoped to one branch like GET /reports/daily-sales.
func (h *Handler) ConsolidatedSales(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	date, ok := dateParam(r)
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "date must be YYYY-MM-DD")
		return
	}

	lines := []branchSalesLine{}
	var totalOrders int
	var totalAmount string
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT b.id, b.name, COUNT(so.id), COALESCE(SUM(so.grand_total),0)::text
			FROM branches b
			LEFT JOIN sales_orders so ON so.branch_id = b.id AND so.status = 'finalized' AND so.finalized_at::date = $1::date
			GROUP BY b.id, b.name
			ORDER BY b.name`, date)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var l branchSalesLine
			if err := rows.Scan(&l.BranchID, &l.BranchName, &l.OrderCount, &l.GrandTotal); err != nil {
				return err
			}
			lines = append(lines, l)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			SELECT COUNT(*), COALESCE(SUM(grand_total),0)::text FROM sales_orders
			WHERE status = 'finalized' AND finalized_at::date = $1::date`, date,
		).Scan(&totalOrders, &totalAmount)
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not build consolidated sales report")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"date": date, "branches": lines, "total_order_count": totalOrders, "total_grand_total": totalAmount,
	})
}

type branchStockLine struct {
	BranchID  string `json:"branch_id"`
	OnHand    string `json:"on_hand"`
	Reserved  string `json:"reserved"`
	Available string `json:"available"`
}

type consolidatedStockEntry struct {
	VariantID   string            `json:"variant_id"`
	SKU         string            `json:"sku"`
	Product     string            `json:"product_name"`
	ByBranch    []branchStockLine `json:"by_branch"`
	TotalOnHand string            `json:"total_on_hand"`
}

// ConsolidatedStock: GET /reports/consolidated-stock?variant_id= (optional)
// — the same variant's stock across every branch side by side, for
// deciding where to transfer from/to. Without variant_id, returns every
// variant that has a stock_levels row anywhere.
func (h *Handler) ConsolidatedStock(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	variantID := r.URL.Query().Get("variant_id")

	entries := []consolidatedStockEntry{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		variantRows, err := tx.Query(ctx, `
			SELECT DISTINCT sl.variant_id, pv.sku, p.name
			FROM stock_levels sl
			JOIN product_variants pv ON pv.id = sl.variant_id
			JOIN products p ON p.id = pv.product_id
			WHERE ($1 = '' OR sl.variant_id::text = $1)
			ORDER BY p.name`, variantID)
		if err != nil {
			return err
		}
		type variant struct{ id, sku, name string }
		var variants []variant
		for variantRows.Next() {
			var v variant
			if err := variantRows.Scan(&v.id, &v.sku, &v.name); err != nil {
				variantRows.Close()
				return err
			}
			variants = append(variants, v)
		}
		variantRows.Close()
		if err := variantRows.Err(); err != nil {
			return err
		}

		for _, v := range variants {
			stockRows, err := tx.Query(ctx, `
				SELECT branch_id, on_hand::text, reserved::text, (on_hand - reserved)::text
				FROM stock_levels WHERE variant_id = $1 ORDER BY branch_id`, v.id)
			if err != nil {
				return err
			}
			var byBranch []branchStockLine
			var totalOnHand float64
			for stockRows.Next() {
				var l branchStockLine
				var onHand float64
				if err := stockRows.Scan(&l.BranchID, &l.OnHand, &l.Reserved, &l.Available); err != nil {
					stockRows.Close()
					return err
				}
				onHand, _ = strconv.ParseFloat(l.OnHand, 64)
				totalOnHand += onHand
				byBranch = append(byBranch, l)
			}
			stockRows.Close()
			if err := stockRows.Err(); err != nil {
				return err
			}
			entries = append(entries, consolidatedStockEntry{
				VariantID: v.id, SKU: v.sku, Product: v.name, ByBranch: byBranch,
				TotalOnHand: strconv.FormatFloat(totalOnHand, 'f', 3, 64),
			})
		}
		return nil
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not build consolidated stock report")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"entries": entries})
}
