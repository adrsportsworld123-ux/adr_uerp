// Package reports implements Phase 1's roadmap item "Basic reports: daily
// sales, stock summary, EOD cash reconciliation." Not in phase0_1_design.md
// §3's original contract table (written before this item was scoped in
// detail) — endpoints below follow the same conventions (WithTenant,
// {"error":{...}} shape, NUMERIC-as-string) as everything else and should
// be added to that table's history alongside this package.
package reports

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

type Handler struct {
	DB *db.DB
}

func dateParam(r *http.Request) (string, bool) {
	d := r.URL.Query().Get("date")
	if d == "" {
		d = time.Now().Format("2006-01-02")
	}
	if _, err := time.Parse("2006-01-02", d); err != nil {
		return "", false
	}
	return d, true
}

// ---------------------------------------------------------------------
// GET /reports/daily-sales?branch_id=&date=YYYY-MM-DD
// ---------------------------------------------------------------------

type dailySalesResponse struct {
	BranchID        string             `json:"branch_id"`
	Date            string             `json:"date"`
	OrderCount      int                `json:"order_count"`
	Subtotal        string             `json:"subtotal"`
	DiscountTotal   string             `json:"discount_total"`
	TaxTotal        string             `json:"tax_total"`
	GrandTotal      string             `json:"grand_total"`
	ByPaymentMethod []paymentBreakdown `json:"by_payment_method"`
}

type paymentBreakdown struct {
	Method string `json:"method"`
	Amount string `json:"amount"`
	Count  int    `json:"count"`
}

func (h *Handler) DailySales(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	branchID := r.URL.Query().Get("branch_id")
	if branchID == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "branch_id is required")
		return
	}
	date, ok := dateParam(r)
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "date must be YYYY-MM-DD")
		return
	}

	resp := dailySalesResponse{BranchID: branchID, Date: date}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			SELECT COUNT(*), COALESCE(SUM(subtotal),0)::text, COALESCE(SUM(discount_total),0)::text,
			       COALESCE(SUM(tax_total),0)::text, COALESCE(SUM(grand_total),0)::text
			FROM sales_orders
			WHERE branch_id = $1 AND status = 'finalized' AND finalized_at::date = $2::date`,
			branchID, date,
		).Scan(&resp.OrderCount, &resp.Subtotal, &resp.DiscountTotal, &resp.TaxTotal, &resp.GrandTotal); err != nil {
			return err
		}

		rows, err := tx.Query(ctx, `
			SELECT p.method, COALESCE(SUM(p.amount),0)::text, COUNT(*)
			FROM payments p
			JOIN sales_orders so ON so.id = p.sales_order_id
			WHERE so.branch_id = $1 AND so.status = 'finalized' AND so.finalized_at::date = $2::date AND p.status = 'captured'
			GROUP BY p.method
			ORDER BY p.method`, branchID, date)
		if err != nil {
			return err
		}
		defer rows.Close()
		resp.ByPaymentMethod = []paymentBreakdown{}
		for rows.Next() {
			var b paymentBreakdown
			if err := rows.Scan(&b.Method, &b.Amount, &b.Count); err != nil {
				return err
			}
			resp.ByPaymentMethod = append(resp.ByPaymentMethod, b)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not build daily sales report")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// ---------------------------------------------------------------------
// GET /reports/stock-summary?branch_id=
// ---------------------------------------------------------------------

type stockSummaryLine struct {
	VariantID string `json:"variant_id"`
	SKU       string `json:"sku"`
	Product   string `json:"product_name"`
	OnHand    string `json:"on_hand"`
	Reserved  string `json:"reserved"`
	Available string `json:"available"`
}

func (h *Handler) StockSummary(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	branchID := r.URL.Query().Get("branch_id")
	if branchID == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "branch_id is required")
		return
	}

	lines := []stockSummaryLine{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT sl.variant_id, pv.sku, p.name, sl.on_hand::text, sl.reserved::text, (sl.on_hand - sl.reserved)::text
			FROM stock_levels sl
			JOIN product_variants pv ON pv.id = sl.variant_id
			JOIN products p ON p.id = pv.product_id
			WHERE sl.branch_id = $1
			ORDER BY p.name`, branchID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var l stockSummaryLine
			if err := rows.Scan(&l.VariantID, &l.SKU, &l.Product, &l.OnHand, &l.Reserved, &l.Available); err != nil {
				return err
			}
			lines = append(lines, l)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not build stock summary")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"branch_id": branchID, "lines": lines})
}

// ---------------------------------------------------------------------
// GET /reports/eod-cash?branch_id=&date=YYYY-MM-DD
// ---------------------------------------------------------------------

type eodCashResponse struct {
	BranchID   string `json:"branch_id"`
	Date       string `json:"date"`
	CashTotal  string `json:"cash_total"`
	CashOrders int    `json:"cash_order_count"`
}

func (h *Handler) EODCash(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	branchID := r.URL.Query().Get("branch_id")
	if branchID == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "branch_id is required")
		return
	}
	date, ok := dateParam(r)
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "date must be YYYY-MM-DD")
		return
	}

	resp := eodCashResponse{BranchID: branchID, Date: date}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(p.amount),0)::text, COUNT(*)
			FROM payments p
			JOIN sales_orders so ON so.id = p.sales_order_id
			WHERE so.branch_id = $1 AND so.status = 'finalized' AND so.finalized_at::date = $2::date
			  AND p.method = 'cash' AND p.status = 'captured'`,
			branchID, date,
		).Scan(&resp.CashTotal, &resp.CashOrders)
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not build EOD cash report")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}
