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
	if !IsValidDate(d) {
		return "", false
	}
	return d, true
}

// IsValidDate reports whether s is a real calendar date in YYYY-MM-DD
// form. Exported for internal/ai's NLP-BI feature, which resolves a
// natural-language date phrase ("yesterday", "last Monday") to this same
// format before calling a Build* function above.
func IsValidDate(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// ---------------------------------------------------------------------
// GET /reports/daily-sales?branch_id=&date=YYYY-MM-DD
// ---------------------------------------------------------------------

type DailySalesResponse struct {
	BranchID        string             `json:"branch_id"`
	Date            string             `json:"date"`
	OrderCount      int                `json:"order_count"`
	Subtotal        string             `json:"subtotal"`
	DiscountTotal   string             `json:"discount_total"`
	TaxTotal        string             `json:"tax_total"`
	GrandTotal      string             `json:"grand_total"`
	ByPaymentMethod []PaymentBreakdown `json:"by_payment_method"`
}

type PaymentBreakdown struct {
	Method string `json:"method"`
	Amount string `json:"amount"`
	Count  int    `json:"count"`
}

// BuildDailySales runs the daily-sales report query against an
// already-open, already-tenant-scoped transaction. Exported so
// internal/ai's NLP-BI feature (POST /ai/ask) can call the exact same
// report logic the HTTP handler below uses, inside its own WithTenant
// transaction, instead of duplicating this SQL or making a second HTTP
// round trip to itself.
func BuildDailySales(ctx context.Context, tx pgx.Tx, branchID, date string) (DailySalesResponse, error) {
	resp := DailySalesResponse{BranchID: branchID, Date: date}
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*), COALESCE(SUM(subtotal),0)::text, COALESCE(SUM(discount_total),0)::text,
		       COALESCE(SUM(tax_total),0)::text, COALESCE(SUM(grand_total),0)::text
		FROM sales_orders
		WHERE branch_id = $1 AND status = 'finalized' AND finalized_at::date = $2::date`,
		branchID, date,
	).Scan(&resp.OrderCount, &resp.Subtotal, &resp.DiscountTotal, &resp.TaxTotal, &resp.GrandTotal); err != nil {
		return resp, err
	}

	rows, err := tx.Query(ctx, `
		SELECT p.method, COALESCE(SUM(p.amount),0)::text, COUNT(*)
		FROM payments p
		JOIN sales_orders so ON so.id = p.sales_order_id
		WHERE so.branch_id = $1 AND so.status = 'finalized' AND so.finalized_at::date = $2::date AND p.status = 'captured'
		GROUP BY p.method
		ORDER BY p.method`, branchID, date)
	if err != nil {
		return resp, err
	}
	defer rows.Close()
	resp.ByPaymentMethod = []PaymentBreakdown{}
	for rows.Next() {
		var b PaymentBreakdown
		if err := rows.Scan(&b.Method, &b.Amount, &b.Count); err != nil {
			return resp, err
		}
		resp.ByPaymentMethod = append(resp.ByPaymentMethod, b)
	}
	return resp, rows.Err()
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

	var resp DailySalesResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		resp, err = BuildDailySales(ctx, tx, branchID, date)
		return err
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

type StockSummaryLine struct {
	VariantID string `json:"variant_id"`
	SKU       string `json:"sku"`
	Product   string `json:"product_name"`
	OnHand    string `json:"on_hand"`
	Reserved  string `json:"reserved"`
	Available string `json:"available"`
}

// BuildStockSummary — see BuildDailySales's doc comment for why this is
// exported and split out from the HTTP handler.
func BuildStockSummary(ctx context.Context, tx pgx.Tx, branchID string) ([]StockSummaryLine, error) {
	lines := []StockSummaryLine{}
	rows, err := tx.Query(ctx, `
		SELECT sl.variant_id, pv.sku, p.name, sl.on_hand::text, sl.reserved::text, (sl.on_hand - sl.reserved)::text
		FROM stock_levels sl
		JOIN product_variants pv ON pv.id = sl.variant_id
		JOIN products p ON p.id = pv.product_id
		WHERE sl.branch_id = $1
		ORDER BY p.name`, branchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var l StockSummaryLine
		if err := rows.Scan(&l.VariantID, &l.SKU, &l.Product, &l.OnHand, &l.Reserved, &l.Available); err != nil {
			return nil, err
		}
		lines = append(lines, l)
	}
	return lines, rows.Err()
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

	var lines []StockSummaryLine
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		lines, err = BuildStockSummary(ctx, tx, branchID)
		return err
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

type EODCashResponse struct {
	BranchID   string `json:"branch_id"`
	Date       string `json:"date"`
	CashTotal  string `json:"cash_total"`
	CashOrders int    `json:"cash_order_count"`
}

// BuildEODCash — see BuildDailySales's doc comment for why this is
// exported and split out from the HTTP handler.
func BuildEODCash(ctx context.Context, tx pgx.Tx, branchID, date string) (EODCashResponse, error) {
	resp := EODCashResponse{BranchID: branchID, Date: date}
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(p.amount),0)::text, COUNT(*)
		FROM payments p
		JOIN sales_orders so ON so.id = p.sales_order_id
		WHERE so.branch_id = $1 AND so.status = 'finalized' AND so.finalized_at::date = $2::date
		  AND p.method = 'cash' AND p.status = 'captured'`,
		branchID, date,
	).Scan(&resp.CashTotal, &resp.CashOrders)
	return resp, err
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

	var resp EODCashResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		resp, err = BuildEODCash(ctx, tx, branchID, date)
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not build EOD cash report")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}
