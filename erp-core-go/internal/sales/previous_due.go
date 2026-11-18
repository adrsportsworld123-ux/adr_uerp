package sales

import (
	"context"
	"errors"
	"math"
	"net/http"
	"sync/atomic"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/customers"
	"erp-core-go/internal/httpx"
)

// A customer's previous outstanding, collected with the current sale
// (migrations/087, §3.92). The till and New sale show what the customer
// still owes on earlier credit sales; the cashier keeps it on the bill
// (collecting all or part of it now) or leaves it off. The payments then
// cover the bill and what is collected. The collected part comes out of
// the non-credit tenders, last first (credit can't pay credit), and is
// applied to the open credit sales oldest first, each through
// customers.ApplyReceivablePayment, so it books and counts in the drawer
// exactly as collecting each sale on its own does. The sale keeps only its
// own tenders: its total, tax, payments and journal are as without it.

// previousDueReady: migrations/087 is applied. Until it is, sales read and
// check out exactly as before and only keeping a previous outstanding on
// the bill is refused; once seen, it stays true for the process.
var previousDueReady atomic.Bool

func previousDueColumns(ctx context.Context, tx pgx.Tx) (bool, error) {
	if previousDueReady.Load() {
		return true, nil
	}
	var ok bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'sales_orders' AND column_name = 'previous_due')`).Scan(&ok); err != nil {
		return false, err
	}
	if ok {
		previousDueReady.Store(true)
	}
	return ok, nil
}

var (
	errPreviousDueUnavailable = errors.New("collecting a previous outstanding needs database migration 087")
	errPreviousDueNoCustomer  = errors.New("a previous outstanding needs a customer on the sale")
	errPreviousDueTooMuch     = errors.New("more than the customer's previous outstanding")
	errPreviousDueNotCovered  = errors.New("the cash, card or UPI paid doesn't cover the previous outstanding collected")
)

type previousDueRequest struct {
	// Collect: how much of the outstanding is paid now (0 = shown on the
	// bill, nothing collected); at most the outstanding.
	Collect float64 `json:"collect"`
}

// dueInvoice is one earlier credit sale still owed.
type dueInvoice struct {
	OrderID     string  `json:"order_id"`
	OrderNumber string  `json:"order_number"`
	Date        string  `json:"date"`
	DueDate     *string `json:"due_date"`
	Outstanding string  `json:"outstanding"`
	pending     float64
}

// previousDueResponse: GET /sales/orders/{id}/previous-due.
type previousDueResponse struct {
	CustomerID  *string      `json:"customer_id"`
	Outstanding string       `json:"outstanding"`
	Invoices    []dueInvoice `json:"invoices"`
}

// previousDueSummary: what a sale collected, for the sale and its receipt.
type previousDueSummary struct {
	Outstanding string `json:"outstanding"` // before this sale
	Collected   string `json:"collected"`
	// BalanceAfter: still owed after this sale — the outstanding less what
	// was collected, plus this sale's own credit.
	BalanceAfter string `json:"balance_after"`
	// TotalReceived: the bill as paid plus what was collected.
	TotalReceived string           `json:"total_received"`
	Payments      []duePaymentLine `json:"payments"`
}

type duePaymentLine struct {
	OrderNumber string `json:"order_number"`
	Method      string `json:"method"`
	Amount      string `json:"amount"`
}

// openDueInvoices: the customer's credit sales still owed, oldest first —
// the order a collection pays them in — leaving out exclude (the current
// sale). lock takes them FOR UPDATE, for checkout.
func openDueInvoices(ctx context.Context, tx pgx.Tx, customerID, exclude string, lock bool) ([]dueInvoice, float64, error) {
	q := `
		SELECT so.id::text, so.order_number, to_char(COALESCE(so.finalized_at, so.created_at), 'YYYY-MM-DD'), so.due_date::text,
		       ` + pendingSQL + `::float8
		FROM sales_orders so
		WHERE so.customer_id::text = $1 AND so.id::text <> $2
		  AND so.status IN ('finalized','partially_refunded') AND so.credit_amount - so.credit_paid > 0.004
		ORDER BY COALESCE(so.finalized_at, so.created_at), so.id`
	if lock {
		q += ` FOR UPDATE OF so`
	}
	rows, err := tx.Query(ctx, q, customerID, exclude)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	invoices := []dueInvoice{}
	total := 0.0
	for rows.Next() {
		var d dueInvoice
		if err := rows.Scan(&d.OrderID, &d.OrderNumber, &d.Date, &d.DueDate, &d.pending); err != nil {
			return nil, 0, err
		}
		d.pending = round2(d.pending)
		d.Outstanding = money2(d.pending)
		total += d.pending
		invoices = append(invoices, d)
	}
	return invoices, round2(total), rows.Err()
}

// GetPreviousDue: GET /sales/orders/{id}/previous-due — what the sale's
// customer owes on earlier sales (0 and no invoices for a walk-in).
func (h *Handler) GetPreviousDue(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	orderID := chi.URLParam(r, "id")
	resp := previousDueResponse{Outstanding: "0.00", Invoices: []dueInvoice{}}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT customer_id::text FROM sales_orders WHERE id::text = $1`, orderID).Scan(&resp.CustomerID); err != nil {
			return err
		}
		if resp.CustomerID == nil {
			return nil
		}
		invoices, total, err := openDueInvoices(ctx, tx, *resp.CustomerID, orderID, false)
		resp.Invoices, resp.Outstanding = invoices, money2(total)
		return err
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "order not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load the previous outstanding")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}

// splitTenders takes collect out of the tenders that can pay a receivable
// (creditPaymentMethods: not credit), last tender first, and returns the
// sale's tenders and the collection's. ok is false when they don't reach
// collect.
func splitTenders(payments []checkoutPayment, collect float64) (sale, due []checkoutPayment, ok bool) {
	left := round2(collect)
	cut := make([]float64, len(payments))
	for i := len(payments) - 1; i >= 0 && left > 0.004; i-- {
		p := payments[i]
		if !creditPaymentMethods[p.Method] || p.Amount <= 0 {
			continue
		}
		take := round2(math.Min(p.Amount, left))
		cut[i] = take
		left = round2(left - take)
	}
	if left > 0.004 {
		return payments, nil, false
	}
	for i, p := range payments {
		if cut[i] == 0 {
			sale = append(sale, p)
			continue
		}
		due = append(due, checkoutPayment{Method: p.Method, Amount: cut[i], Reference: p.Reference})
		if rest := round2(p.Amount - cut[i]); rest > 0.004 {
			p.Amount = rest
			sale = append(sale, p)
		}
	}
	return sale, due, true
}

// previousDuePlan: checkout's collection, settled before the sale's own
// payment checks (which then see only the sale's tenders).
type previousDuePlan struct {
	outstanding, collect float64
	invoices             []dueInvoice
	due                  []checkoutPayment
}

// planPreviousDue checks the request against the customer's open credit
// sales (locked) and splits the tenders; req.Payments becomes the sale's.
func planPreviousDue(ctx context.Context, tx pgx.Tx, req *checkoutRequest, customerID *string, orderID string) (*previousDuePlan, error) {
	if req.PreviousDue == nil {
		return nil, nil
	}
	if ready, err := previousDueColumns(ctx, tx); err != nil {
		return nil, err
	} else if !ready {
		return nil, errPreviousDueUnavailable
	}
	if customerID == nil {
		return nil, errPreviousDueNoCustomer
	}
	invoices, total, err := openDueInvoices(ctx, tx, *customerID, orderID, true)
	if err != nil {
		return nil, err
	}
	collect := round2(req.PreviousDue.Collect)
	if collect < 0 || collect > total+0.004 {
		return nil, errPreviousDueTooMuch
	}
	plan := &previousDuePlan{outstanding: total, collect: collect, invoices: invoices}
	if collect > 0 {
		sale, due, ok := splitTenders(req.Payments, collect)
		if !ok {
			return nil, errPreviousDueNotCovered
		}
		req.Payments, plan.due = sale, due
	}
	return plan, nil
}

// apply records the collection against the open sales, oldest first, once
// the sale itself is finalized, and keeps the figures on the sale.
func (p *previousDuePlan) apply(ctx context.Context, tx pgx.Tx, customerID, orderID, userID string) error {
	if p == nil {
		return nil
	}
	var orderNumber string
	var terminalID *string
	if err := tx.QueryRow(ctx, `SELECT order_number, pos_terminal_id::text FROM sales_orders WHERE id::text = $1`, orderID).
		Scan(&orderNumber, &terminalID); err != nil {
		return err
	}
	terminal := ""
	if terminalID != nil {
		terminal = *terminalID
	}
	inv := 0
	for _, t := range p.due {
		left := t.Amount
		for left > 0.004 && inv < len(p.invoices) {
			d := &p.invoices[inv]
			amt := round2(math.Min(left, d.pending))
			if _, _, err := customers.ApplyReceivablePayment(ctx, tx, customers.ReceivablePayment{
				CustomerID: customerID, SalesOrderID: d.OrderID, Amount: amt, Method: t.Method, ReferenceNo: t.Reference,
				Description: "Collected with sale " + orderNumber, UserID: userID, POSTerminalID: terminal,
				CollectedWithOrderID: orderID,
			}); err != nil {
				return err
			}
			d.pending = round2(d.pending - amt)
			left = round2(left - amt)
			if d.pending <= 0.004 {
				inv++
			}
		}
	}
	_, err := tx.Exec(ctx, `UPDATE sales_orders SET previous_due = $2, previous_due_collected = $3 WHERE id::text = $1`,
		orderID, p.outstanding, p.collect)
	return err
}

// loadPreviousDue: what a sale collected of the customer's previous
// outstanding; nil when it wasn't kept on the bill.
func loadPreviousDue(ctx context.Context, tx pgx.Tx, orderID string) (*previousDueSummary, error) {
	if ready, err := previousDueColumns(ctx, tx); err != nil || !ready {
		return nil, err
	}
	var outstanding, collected, credit, payable float64
	if err := tx.QueryRow(ctx, `
		SELECT previous_due::float8, previous_due_collected::float8, credit_amount::float8, (grand_total + round_off)::float8
		FROM sales_orders WHERE id::text = $1`, orderID).Scan(&outstanding, &collected, &credit, &payable); err != nil {
		return nil, err
	}
	if outstanding <= 0.004 && collected <= 0.004 {
		return nil, nil
	}
	s := &previousDueSummary{
		Outstanding: money2(outstanding), Collected: money2(collected),
		BalanceAfter:  money2(math.Max(round2(outstanding-collected+credit), 0)),
		TotalReceived: money2(round2(payable - credit + collected)),
		Payments:      []duePaymentLine{},
	}
	// One line per sale paid, oldest first as they were paid; a sale paid
	// from two tenders names both ("cash + upi").
	rows, err := tx.Query(ctx, `
		SELECT so.order_number, string_agg(DISTINCT p.method, ' + ' ORDER BY p.method), SUM(p.amount)::text
		FROM customer_receivable_payments p JOIN sales_orders so ON so.id = p.sales_order_id
		WHERE p.collected_with_order_id::text = $1 AND p.reversed_at IS NULL
		GROUP BY so.id, so.order_number
		ORDER BY MIN(COALESCE(so.finalized_at, so.created_at)), so.id`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var l duePaymentLine
		if err := rows.Scan(&l.OrderNumber, &l.Method, &l.Amount); err != nil {
			return nil, err
		}
		s.Payments = append(s.Payments, l)
	}
	return s, rows.Err()
}

// money2: two places, never "-0.00".
func money2(v float64) string {
	if v = round2(v); math.Abs(v) < 0.005 {
		v = 0
	}
	return money(v)
}
