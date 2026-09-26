package reports

import (
	"context"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

type BranchSalesLine struct {
	BranchID   string `json:"branch_id"`
	BranchName string `json:"branch_name"`
	OrderCount int    `json:"order_count"`
	GrandTotal string `json:"grand_total"`
}

type ConsolidatedSalesResponse struct {
	Date            string            `json:"date"`
	Branches        []BranchSalesLine `json:"branches"`
	TotalOrderCount int               `json:"total_order_count"`
	TotalGrandTotal string            `json:"total_grand_total"`
}

// BuildConsolidatedSales — see handlers.go's BuildDailySales doc comment
// for why this is exported and split out from the HTTP handler.
func BuildConsolidatedSales(ctx context.Context, tx pgx.Tx, date string) (ConsolidatedSalesResponse, error) {
	resp := ConsolidatedSalesResponse{Date: date, Branches: []BranchSalesLine{}}
	rows, err := tx.Query(ctx, `
		SELECT b.id, b.name, COUNT(so.id), COALESCE(SUM(so.grand_total),0)::text
		FROM branches b
		LEFT JOIN sales_orders so ON so.branch_id = b.id AND so.status = 'finalized' AND so.finalized_at::date = $1::date
		GROUP BY b.id, b.name
		ORDER BY b.name`, date)
	if err != nil {
		return resp, err
	}
	for rows.Next() {
		var l BranchSalesLine
		if err := rows.Scan(&l.BranchID, &l.BranchName, &l.OrderCount, &l.GrandTotal); err != nil {
			rows.Close()
			return resp, err
		}
		resp.Branches = append(resp.Branches, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return resp, err
	}
	err = tx.QueryRow(ctx, `
		SELECT COUNT(*), COALESCE(SUM(grand_total),0)::text FROM sales_orders
		WHERE status = 'finalized' AND finalized_at::date = $1::date`, date,
	).Scan(&resp.TotalOrderCount, &resp.TotalGrandTotal)
	return resp, err
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

	var resp ConsolidatedSalesResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		resp, err = BuildConsolidatedSales(ctx, tx, date)
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not build consolidated sales report")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

type BranchStockLine struct {
	BranchID  string `json:"branch_id"`
	OnHand    string `json:"on_hand"`
	Reserved  string `json:"reserved"`
	Available string `json:"available"`
}

type ConsolidatedStockEntry struct {
	VariantID   string            `json:"variant_id"`
	SKU         string            `json:"sku"`
	Product     string            `json:"product_name"`
	ByBranch    []BranchStockLine `json:"by_branch"`
	TotalOnHand string            `json:"total_on_hand"`
}

// BuildConsolidatedStock — see handlers.go's BuildDailySales doc comment
// for why this is exported and split out from the HTTP handler.
func BuildConsolidatedStock(ctx context.Context, tx pgx.Tx, variantID string) ([]ConsolidatedStockEntry, error) {
	entries := []ConsolidatedStockEntry{}
	variantRows, err := tx.Query(ctx, `
		SELECT DISTINCT sl.variant_id, pv.sku, p.name
		FROM stock_levels sl
		JOIN product_variants pv ON pv.id = sl.variant_id
		JOIN products p ON p.id = pv.product_id
		WHERE ($1 = '' OR sl.variant_id::text = $1)
		ORDER BY p.name`, variantID)
	if err != nil {
		return nil, err
	}
	type variant struct{ id, sku, name string }
	var variants []variant
	for variantRows.Next() {
		var v variant
		if err := variantRows.Scan(&v.id, &v.sku, &v.name); err != nil {
			variantRows.Close()
			return nil, err
		}
		variants = append(variants, v)
	}
	variantRows.Close()
	if err := variantRows.Err(); err != nil {
		return nil, err
	}

	for _, v := range variants {
		stockRows, err := tx.Query(ctx, `
			SELECT branch_id, on_hand::text, reserved::text, (on_hand - reserved)::text
			FROM stock_levels WHERE variant_id = $1 ORDER BY branch_id`, v.id)
		if err != nil {
			return nil, err
		}
		var byBranch []BranchStockLine
		var totalOnHand float64
		for stockRows.Next() {
			var l BranchStockLine
			var onHand float64
			if err := stockRows.Scan(&l.BranchID, &l.OnHand, &l.Reserved, &l.Available); err != nil {
				stockRows.Close()
				return nil, err
			}
			onHand, _ = strconv.ParseFloat(l.OnHand, 64)
			totalOnHand += onHand
			byBranch = append(byBranch, l)
		}
		stockRows.Close()
		if err := stockRows.Err(); err != nil {
			return nil, err
		}
		entries = append(entries, ConsolidatedStockEntry{
			VariantID: v.id, SKU: v.sku, Product: v.name, ByBranch: byBranch,
			TotalOnHand: strconv.FormatFloat(totalOnHand, 'f', 3, 64),
		})
	}
	return entries, nil
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

	var entries []ConsolidatedStockEntry
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		entries, err = BuildConsolidatedStock(ctx, tx, variantID)
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not build consolidated stock report")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"entries": entries})
}
