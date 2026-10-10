package sales

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

// Phase 13: sales history and the terminal/day summary (phased_roadmap.md
// Phase 13; FRD §15 A sales reports, brief §7). Until now the only order
// list was GET /sales/orders/held, so neither the POS nor the admin could
// show past sales. Detail for one order is the existing receipt endpoint.
//
// Dates are the BRANCH's local calendar day (branches.timezone), not the
// database's UTC day: a sale at 01:00 IST belongs to that IST date. The
// older daily reports group by finalized_at::date, which is why they
// misbehave between 00:00 and 05:30 IST; these endpoints don't.

type historyOrder struct {
	OrderID        string   `json:"order_id"`
	OrderNumber    string   `json:"order_number"`
	Status         string   `json:"status"`
	FinalizedAt    *string  `json:"finalized_at"`
	BranchID       string   `json:"branch_id"`
	BranchName     string   `json:"branch_name"`
	TerminalID     string   `json:"pos_terminal_id"`
	TerminalName   string   `json:"terminal_name"`
	CashierID      string   `json:"cashier_id"`
	CashierName    string   `json:"cashier_name"`
	CustomerID     *string  `json:"customer_id"`
	CustomerName   *string  `json:"customer_name"`
	ItemCount      string   `json:"item_count"`
	Subtotal       string   `json:"subtotal"`
	DiscountTotal  string   `json:"discount_total"`
	TaxTotal       string   `json:"tax_total"`
	GrandTotal     string   `json:"grand_total"`
	PaymentMethods []string `json:"payment_methods"`
	Channel        string   `json:"channel"`
}

// historyStatuses are the states a history list can show; cart and held
// orders are work in progress, not history.
var historyStatuses = map[string]bool{"finalized": true, "voided": true, "refunded": true, "partially_refunded": true}

// historyFilter is shared by the list and the summary so both always
// describe the same set of orders.
type historyFilter struct {
	terminalID, branchID, cashierID, status, method, q, from, to, channel string
}

func readHistoryFilter(r *http.Request) historyFilter {
	q := r.URL.Query()
	return historyFilter{
		terminalID: q.Get("terminal_id"),
		branchID:   q.Get("branch_id"),
		cashierID:  q.Get("cashier_id"),
		status:     q.Get("status"),
		method:     q.Get("payment_method"),
		q:          strings.TrimSpace(q.Get("q")),
		from:       q.Get("from"),
		to:         q.Get("to"),
		channel:    q.Get("channel"),
	}
}

// where returns the shared WHERE clause; args start at $1.
const historyWhere = `
	FROM sales_orders so
	JOIN branches b ON b.id = so.branch_id
	JOIN pos_terminals t ON t.id = so.pos_terminal_id
	JOIN users u ON u.id = so.cashier_id
	LEFT JOIN customers c ON c.id = so.customer_id
	WHERE so.status <> 'cart' AND so.status <> 'held'
	  AND ($1 = '' OR so.pos_terminal_id::text = $1)
	  AND ($2 = '' OR so.branch_id::text = $2)
	  AND ($3 = '' OR so.cashier_id::text = $3)
	  AND ($4 = '' OR so.status = $4)
	  AND ($5 = '' OR EXISTS (SELECT 1 FROM payments p WHERE p.sales_order_id = so.id AND p.method = $5))
	  AND ($6 = '' OR so.order_number ILIKE '%' || $6 || '%' OR c.name ILIKE '%' || $6 || '%' OR c.phone ILIKE '%' || $6 || '%')
	  AND ($7 = '' OR (COALESCE(so.finalized_at, so.created_at) AT TIME ZONE b.timezone)::date >= $7::date)
	  AND ($8 = '' OR (COALESCE(so.finalized_at, so.created_at) AT TIME ZONE b.timezone)::date <= $8::date)
	  AND ($9 = '' OR so.channel = $9)`

func (f historyFilter) args() []any {
	return []any{f.terminalID, f.branchID, f.cashierID, f.status, f.method, f.q, f.from, f.to, f.channel}
}

func (f historyFilter) validate(w http.ResponseWriter) bool {
	if f.channel != "" && f.channel != "pos" && f.channel != "online" && f.channel != "backoffice" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "channel must be pos, online or backoffice")
		return false
	}
	if f.status != "" && !historyStatuses[f.status] {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "status must be finalized, voided, refunded or partially_refunded")
		return false
	}
	for _, d := range []string{f.from, f.to} {
		if d != "" && !isValidDate(d) {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "from/to must be YYYY-MM-DD")
			return false
		}
	}
	return true
}

// ListOrders: GET /sales/orders?terminal_id=&branch_id=&cashier_id=&status=
// &payment_method=&channel=&q=&from=&to=&page=&limit=  — newest first, paged
// (default 25). q matches order number, customer name or phone.
func (h *Handler) ListOrders(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	f := readHistoryFilter(r)
	if !f.validate(w) {
		return
	}
	p := httpx.ParseListParams(r, map[string]string{
		"recent": "COALESCE(so.finalized_at, so.created_at)", "total": "so.grand_total", "number": "so.order_number",
	}, "recent")
	if r.URL.Query().Get("dir") == "" {
		p.OrderBy = "COALESCE(so.finalized_at, so.created_at) DESC NULLS LAST"
	}
	limit, offset := p.Limit, p.Offset // always paged: history is unbounded

	orders := []historyOrder{}
	total := 0
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		args := f.args()
		if err := tx.QueryRow(ctx, `SELECT count(*)`+historyWhere, args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT so.id, so.order_number, so.status, so.finalized_at::text,
			       so.branch_id, b.name, so.pos_terminal_id, t.name, so.cashier_id, u.name,
			       so.customer_id::text, c.name,
			       COALESCE((SELECT sum(l.quantity) FROM sales_order_lines l WHERE l.sales_order_id = so.id), 0)::text,
			       so.subtotal::text, so.discount_total::text, so.tax_total::text, so.grand_total::text,
			       COALESCE((SELECT array_agg(DISTINCT p.method) FROM payments p WHERE p.sales_order_id = so.id), '{}'),
			       so.channel`+historyWhere+`
			ORDER BY `+p.OrderBy+`, so.id
			LIMIT $10 OFFSET $11`, append(args, limit, offset)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var o historyOrder
			if err := rows.Scan(&o.OrderID, &o.OrderNumber, &o.Status, &o.FinalizedAt,
				&o.BranchID, &o.BranchName, &o.TerminalID, &o.TerminalName, &o.CashierID, &o.CashierName,
				&o.CustomerID, &o.CustomerName, &o.ItemCount,
				&o.Subtotal, &o.DiscountTotal, &o.TaxTotal, &o.GrandTotal, &o.PaymentMethods, &o.Channel); err != nil {
				return err
			}
			orders = append(orders, o)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list sales")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"orders": orders, "total": total, "page": p.Page, "limit": p.Limit})
}

type summaryCashier struct {
	CashierID   string `json:"cashier_id"`
	CashierName string `json:"cashier_name"`
	OrderCount  int    `json:"order_count"`
	NetSales    string `json:"net_sales"`
}

type summaryMethod struct {
	Method string `json:"method"`
	Amount string `json:"amount"`
	Count  int    `json:"count"`
}

// Summary: GET /sales/summary — same filters as ListOrders. Totals count
// only completed sales (finalized, and partially refunded ones at their
// original value — refunds are reported separately); voided/refunded
// orders are counted, not summed.
func (h *Handler) Summary(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	f := readHistoryFilter(r)
	if !f.validate(w) {
		return
	}
	resp := map[string]any{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		args := f.args()
		var orders, voided, refunded int
		var gross, disc, tax, net string
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FILTER (WHERE so.status IN ('finalized','partially_refunded')),
			       count(*) FILTER (WHERE so.status = 'voided'),
			       count(*) FILTER (WHERE so.status = 'refunded'),
			       COALESCE(sum(so.subtotal) FILTER (WHERE so.status IN ('finalized','partially_refunded')), 0)::text,
			       COALESCE(sum(so.discount_total) FILTER (WHERE so.status IN ('finalized','partially_refunded')), 0)::text,
			       COALESCE(sum(so.tax_total) FILTER (WHERE so.status IN ('finalized','partially_refunded')), 0)::text,
			       COALESCE(sum(so.grand_total) FILTER (WHERE so.status IN ('finalized','partially_refunded')), 0)::text`+historyWhere,
			args...).Scan(&orders, &voided, &refunded, &gross, &disc, &tax, &net); err != nil {
			return err
		}
		resp["order_count"], resp["voided_count"], resp["refunded_count"] = orders, voided, refunded
		resp["gross_sales"], resp["discount_total"], resp["tax_total"], resp["net_sales"] = gross, disc, tax, net

		methods := []summaryMethod{}
		rows, err := tx.Query(ctx, `
			SELECT p.method, COALESCE(sum(p.amount),0)::text, count(*)
			FROM payments p
			WHERE p.status = 'captured' AND p.sales_order_id IN (SELECT so.id`+historyWhere+`
			  AND so.status IN ('finalized','partially_refunded'))
			GROUP BY p.method ORDER BY p.method`, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var m summaryMethod
			if err := rows.Scan(&m.Method, &m.Amount, &m.Count); err != nil {
				rows.Close()
				return err
			}
			methods = append(methods, m)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		resp["by_payment_method"] = methods

		cashiers := []summaryCashier{}
		rows, err = tx.Query(ctx, `
			SELECT so.cashier_id, u.name, count(*), COALESCE(sum(so.grand_total),0)::text`+historyWhere+`
			  AND so.status IN ('finalized','partially_refunded')
			GROUP BY so.cashier_id, u.name ORDER BY sum(so.grand_total) DESC`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c summaryCashier
			if err := rows.Scan(&c.CashierID, &c.CashierName, &c.OrderCount, &c.NetSales); err != nil {
				return err
			}
			cashiers = append(cashiers, c)
		}
		resp["by_cashier"] = cashiers
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not build the sales summary")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

func isValidDate(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}
