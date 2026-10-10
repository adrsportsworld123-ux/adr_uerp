package sales

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/accounting"
	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

// Phase 14: the back office's view of one sale and its one post-sale
// correction. Everything else that changes a finalized sale already has
// its own endpoint and rules: void (sales.void), return/refund (approval
// per sales_settings). See migrations/042_backoffice_sales.sql.

type detailLine struct {
	LineID           string  `json:"line_id"`
	SKU              string  `json:"sku"`
	ProductName      string  `json:"product_name"`
	Quantity         string  `json:"quantity"`
	ReturnedQuantity string  `json:"returned_quantity"`
	UnitPrice        string  `json:"unit_price"`
	DiscountAmount   string  `json:"discount_amount"`
	TaxableValue     string  `json:"taxable_value"`
	TaxRate          string  `json:"tax_rate"` // effective %, from the line's own figures
	TaxAmount        string  `json:"tax_amount"`
	LineTotal        string  `json:"line_total"`
	PriceIncludesTax bool    `json:"price_includes_tax"`
	OpenPrice        bool    `json:"open_price"`
	PriceApprovedBy  *string `json:"price_approved_by"`
}

type taxBucket struct {
	Rate    string `json:"rate"`
	Taxable string `json:"taxable_value"`
	Tax     string `json:"tax_amount"`
	CGST    string `json:"cgst"`
	SGST    string `json:"sgst"`
}

type detailPayment struct {
	PaymentID   string              `json:"payment_id"`
	Method      string              `json:"method"`
	Amount      string              `json:"amount"`
	Reference   string              `json:"reference"`
	Status      string              `json:"status"`
	CreatedAt   string              `json:"created_at"`
	Corrections []paymentCorrection `json:"corrections"`
}

type paymentCorrection struct {
	FromMethod string `json:"from_method"`
	ToMethod   string `json:"to_method"`
	Reason     string `json:"reason"`
	By         string `json:"performed_by"`
	ApprovedBy string `json:"approved_by"`
	CreatedAt  string `json:"created_at"`
}

type detailDiscount struct {
	Type         string  `json:"type"`
	Percent      string  `json:"value_percent"`
	Amount       string  `json:"discount_amount"`
	AppliedBy    string  `json:"applied_by"`
	AuthorizedBy *string `json:"authorized_by"`
	Reason       string  `json:"reason"`
}

type detailJournal struct {
	EntryNumber string `json:"entry_number"`
	EntryDate   string `json:"entry_date"`
	SourceType  string `json:"source_type"`
	Description string `json:"description"`
	Amount      string `json:"amount"`
}

type detailAudit struct {
	EntityType string          `json:"entity_type"`
	Action     string          `json:"action"`
	By         *string         `json:"performed_by"`
	Reason     string          `json:"reason"`
	After      json.RawMessage `json:"after"`
	CreatedAt  string          `json:"created_at"`
}

type orderDetail struct {
	OrderID         string           `json:"order_id"`
	OrderNumber     string           `json:"order_number"`
	Status          string           `json:"status"`
	Channel         string           `json:"channel"`
	BranchID        string           `json:"branch_id"`
	BranchName      string           `json:"branch_name"`
	TerminalName    string           `json:"terminal_name"`
	CashierName     string           `json:"cashier_name"`
	CustomerID      *string          `json:"customer_id"`
	CustomerName    *string          `json:"customer_name"`
	CustomerPhone   *string          `json:"customer_phone"`
	CreatedAt       string           `json:"created_at"`
	FinalizedAt     *string          `json:"finalized_at"`
	DeviceCreatedAt *string          `json:"device_created_at"`
	Subtotal        string           `json:"subtotal"`
	DiscountTotal   string           `json:"discount_total"`
	TaxTotal        string           `json:"tax_total"`
	GrandTotal      string           `json:"grand_total"`
	CreditAmount    string           `json:"credit_amount"`
	CreditPaid      string           `json:"credit_paid"`
	Lines           []detailLine     `json:"lines"`
	TaxSummary      []taxBucket      `json:"tax_summary"`
	Payments        []detailPayment  `json:"payments"`
	Discounts       []detailDiscount `json:"discounts"`
	Returns         []returnResponse `json:"returns"`
	Journal         []detailJournal  `json:"journal"`
	Audit           []detailAudit    `json:"audit"`
}

func ts(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

// OrderDetail: GET /sales/orders/{id}/detail. Any signed-in user, like the
// receipt. The tax summary groups lines by their effective rate (tax ÷
// taxable value, from each line's own stored figures, so a later change to
// a tax slab doesn't rewrite an old sale) and splits each bucket into equal
// CGST and SGST, the intra-state split of a counter sale.
func (h *Handler) OrderDetail(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	orderID := chi.URLParam(r, "id")
	var d orderDetail
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var created time.Time
		var finalized, device *time.Time
		if err := tx.QueryRow(ctx, `
			SELECT so.id, so.order_number, so.status, so.channel, so.branch_id, b.name, t.name, u.name,
			       so.customer_id::text, c.name, c.phone, so.created_at, so.finalized_at, so.device_created_at,
			       so.subtotal::text, so.discount_total::text, so.tax_total::text, so.grand_total::text,
			       so.credit_amount::text, so.credit_paid::text
			FROM sales_orders so
			JOIN branches b ON b.id = so.branch_id
			JOIN pos_terminals t ON t.id = so.pos_terminal_id
			JOIN users u ON u.id = so.cashier_id
			LEFT JOIN customers c ON c.id = so.customer_id
			WHERE so.id::text = $1`, orderID).Scan(&d.OrderID, &d.OrderNumber, &d.Status, &d.Channel, &d.BranchID, &d.BranchName,
			&d.TerminalName, &d.CashierName, &d.CustomerID, &d.CustomerName, &d.CustomerPhone, &created, &finalized, &device,
			&d.Subtotal, &d.DiscountTotal, &d.TaxTotal, &d.GrandTotal, &d.CreditAmount, &d.CreditPaid); err != nil {
			return err
		}
		d.CreatedAt = *ts(&created)
		d.FinalizedAt, d.DeviceCreatedAt = ts(finalized), ts(device)

		// Lines, with what's been returned and the effective tax rate.
		rows, err := tx.Query(ctx, `
			SELECT l.id, pv.sku, p.name, l.quantity::text,
			       COALESCE((SELECT sum(rl.quantity) FROM sales_return_lines rl WHERE rl.sales_order_line_id = l.id), 0)::text,
			       l.unit_price::text, l.discount_amount::text, l.taxable_value::text, l.taxable_value, l.tax_amount::text, l.tax_amount,
			       l.line_total::text, l.price_includes_tax, l.open_price, au.name
			FROM sales_order_lines l
			JOIN product_variants pv ON pv.id = l.variant_id
			JOIN products p ON p.id = pv.product_id
			LEFT JOIN users au ON au.id = l.price_approved_by
			WHERE l.sales_order_id::text = $1 ORDER BY l.created_at`, orderID)
		if err != nil {
			return err
		}
		type acc struct{ taxable, tax float64 }
		buckets := map[float64]*acc{}
		d.Lines = []detailLine{}
		for rows.Next() {
			var l detailLine
			var taxable, tax float64
			if err := rows.Scan(&l.LineID, &l.SKU, &l.ProductName, &l.Quantity, &l.ReturnedQuantity, &l.UnitPrice, &l.DiscountAmount,
				&l.TaxableValue, &taxable, &l.TaxAmount, &tax, &l.LineTotal, &l.PriceIncludesTax, &l.OpenPrice, &l.PriceApprovedBy); err != nil {
				rows.Close()
				return err
			}
			rate := 0.0
			if taxable > 0 {
				// Stored amounts are rounded to paise, so the quotient is
				// near, not on, the slab rate: snap to the nearest 0.25%.
				rate = math.Round(tax/taxable*100*4) / 4
			}
			l.TaxRate = fmt.Sprintf("%.2f", rate)
			if buckets[rate] == nil {
				buckets[rate] = &acc{}
			}
			buckets[rate].taxable += taxable
			buckets[rate].tax += tax
			d.Lines = append(d.Lines, l)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		rates := make([]float64, 0, len(buckets))
		for k := range buckets {
			rates = append(rates, k)
		}
		sort.Float64s(rates)
		d.TaxSummary = []taxBucket{}
		for _, rate := range rates {
			a := buckets[rate]
			half := round2(a.tax / 2)
			d.TaxSummary = append(d.TaxSummary, taxBucket{
				Rate: fmt.Sprintf("%.2f", rate), Taxable: fmt.Sprintf("%.2f", a.taxable), Tax: fmt.Sprintf("%.2f", a.tax),
				CGST: fmt.Sprintf("%.2f", half), SGST: fmt.Sprintf("%.2f", round2(a.tax-half)),
			})
		}

		// Payments, each with its corrections.
		rows, err = tx.Query(ctx, `
			SELECT id, method, amount::text, COALESCE(reference_no, ''), status, created_at
			FROM payments WHERE sales_order_id::text = $1 ORDER BY created_at`, orderID)
		if err != nil {
			return err
		}
		d.Payments = []detailPayment{}
		for rows.Next() {
			var p detailPayment
			var at time.Time
			if err := rows.Scan(&p.PaymentID, &p.Method, &p.Amount, &p.Reference, &p.Status, &at); err != nil {
				rows.Close()
				return err
			}
			p.CreatedAt = *ts(&at)
			d.Payments = append(d.Payments, p)
		}
		rows.Close()
		for i := range d.Payments {
			p := &d.Payments[i]
			p.Corrections = []paymentCorrection{}
			crows, err := tx.Query(ctx, `
				SELECT pc.from_method, pc.to_method, pc.reason, u.name, a.name, pc.created_at
				FROM payment_corrections pc JOIN users u ON u.id = pc.performed_by JOIN users a ON a.id = pc.approved_by
				WHERE pc.payment_id::text = $1 ORDER BY pc.created_at`, p.PaymentID)
			if err != nil {
				return err
			}
			for crows.Next() {
				var c paymentCorrection
				var at time.Time
				if err := crows.Scan(&c.FromMethod, &c.ToMethod, &c.Reason, &c.By, &c.ApprovedBy, &at); err != nil {
					crows.Close()
					return err
				}
				c.CreatedAt = *ts(&at)
				p.Corrections = append(p.Corrections, c)
			}
			crows.Close()
		}

		rows, err = tx.Query(ctx, `
			SELECT d.type, d.value_percent::text, d.discount_amount::text, ap.name, au.name, COALESCE(d.reason, '')
			FROM sales_order_discounts d JOIN users ap ON ap.id = d.applied_by LEFT JOIN users au ON au.id = d.authorized_by
			WHERE d.sales_order_id::text = $1 ORDER BY d.created_at`, orderID)
		if err != nil {
			return err
		}
		d.Discounts = []detailDiscount{}
		for rows.Next() {
			var x detailDiscount
			if err := rows.Scan(&x.Type, &x.Percent, &x.Amount, &x.AppliedBy, &x.AuthorizedBy, &x.Reason); err != nil {
				rows.Close()
				return err
			}
			d.Discounts = append(d.Discounts, x)
		}
		rows.Close()

		var returnIDs []string
		rows, err = tx.Query(ctx, `SELECT id FROM sales_returns WHERE sales_order_id::text = $1 ORDER BY created_at`, orderID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			returnIDs = append(returnIDs, id)
		}
		rows.Close()
		d.Returns = []returnResponse{}
		for _, id := range returnIDs {
			rr, err := loadReturn(ctx, tx, id)
			if err != nil {
				return err
			}
			d.Returns = append(d.Returns, rr)
		}

		// Every ledger posting this sale caused: the sale, a void, its
		// returns, payment corrections.
		related := append([]string{d.OrderID}, returnIDs...)
		rows, err = tx.Query(ctx, `
			SELECT je.entry_number, je.entry_date::text, je.source_type, COALESCE(je.description, ''),
			       COALESCE((SELECT sum(debit) FROM journal_lines jl WHERE jl.journal_entry_id = je.id), 0)::text
			FROM journal_entries je
			WHERE je.source_id::text = ANY($1)
			   OR (je.source_type = 'payment_correction' AND je.source_id IN (SELECT id FROM payment_corrections WHERE sales_order_id::text = $2))
			ORDER BY je.created_at`, related, orderID)
		if err != nil {
			return err
		}
		d.Journal = []detailJournal{}
		for rows.Next() {
			var j detailJournal
			if err := rows.Scan(&j.EntryNumber, &j.EntryDate, &j.SourceType, &j.Description, &j.Amount); err != nil {
				rows.Close()
				return err
			}
			d.Journal = append(d.Journal, j)
		}
		rows.Close()

		// The audit trail: entries about the order, its lines (open
		// price), its returns, and its payment corrections.
		ids := append([]string{}, related...)
		for _, l := range d.Lines {
			ids = append(ids, l.LineID)
		}
		rows, err = tx.Query(ctx, `
			SELECT a.entity_type, a.action, u.name, COALESCE(a.reason, ''), COALESCE(a.after_value, 'null'::jsonb), a.created_at
			FROM audit_logs a LEFT JOIN users u ON u.id = a.performed_by
			WHERE a.entity_id::text = ANY($1)
			   OR a.entity_id IN (SELECT id FROM payment_corrections WHERE sales_order_id::text = $2)
			ORDER BY a.created_at`, ids, orderID)
		if err != nil {
			return err
		}
		defer rows.Close()
		d.Audit = []detailAudit{}
		for rows.Next() {
			var a detailAudit
			var at time.Time
			if err := rows.Scan(&a.EntityType, &a.Action, &a.By, &a.Reason, &a.After, &at); err != nil {
				return err
			}
			a.CreatedAt = *ts(&at)
			d.Audit = append(d.Audit, a)
		}
		return rows.Err()
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "order not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load the order")
		return
	}
	httpx.JSON(w, http.StatusOK, d)
}

var (
	errPaymentNotCorrectable = errors.New("payment can't be corrected")
	errCorrectionApproval    = errors.New("correction needs approval")
)

var correctableMethods = map[string]bool{"cash": true, "card": true, "upi": true}

// CorrectPayment: POST /sales/orders/{id}/payments/{payment_id}/correct
// {to_method, to_reference?, reason, approver_code, approver_pin} —
// sales.backoffice. FRD §3 "Bill Modification": manager approval, reason,
// full audit. Fixes a tender recorded against the wrong method (cash, card
// or UPI; credit is a receivable, not a tender to swap). The payment row
// takes the new method, the change is kept in payment_corrections and
// audit_logs, and the ledger moves the amount from the old method's account
// to the new one's. Prices and quantities are corrected with a return and a
// new sale instead, which keeps the GST record of what was invoiced.
func (h *Handler) CorrectPayment(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	orderID, paymentID := chi.URLParam(r, "id"), chi.URLParam(r, "payment_id")
	var req struct {
		ToMethod     string `json:"to_method"`
		ToReference  string `json:"to_reference"`
		Reason       string `json:"reason"`
		ApproverCode string `json:"approver_code"`
		ApproverPIN  string `json:"approver_pin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if !correctableMethods[req.ToMethod] || req.Reason == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "to_method (cash, card or upi) and a reason are required")
		return
	}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var orderStatus, branchID, method, status, reference string
		var amount float64
		if err := tx.QueryRow(ctx, `
			SELECT so.status, so.branch_id, p.method, p.status, COALESCE(p.reference_no, ''), p.amount
			FROM payments p JOIN sales_orders so ON so.id = p.sales_order_id
			WHERE p.id::text = $1 AND so.id::text = $2
			FOR UPDATE OF p`, paymentID, orderID).Scan(&orderStatus, &branchID, &method, &status, &reference, &amount); err != nil {
			return err
		}
		if (orderStatus != "finalized" && orderStatus != "partially_refunded" && orderStatus != "refunded") ||
			status != "captured" || !correctableMethods[method] || method == req.ToMethod {
			return errPaymentNotCorrectable
		}
		approver, err := approverFor(ctx, tx, req.ApproverCode, req.ApproverPIN, branchID)
		if err != nil {
			return err
		}
		if approver == "" {
			return errCorrectionApproval
		}
		var correctionID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO payment_corrections (merchant_id, sales_order_id, payment_id, from_method, to_method, from_reference, to_reference,
			    amount, reason, performed_by, approved_by)
			VALUES (current_setting('app.tenant_id')::uuid, $1::uuid, $2::uuid, $3, $4, NULLIF($5, ''), NULLIF($6, ''), $7, $8, $9, $10)
			RETURNING id`, orderID, paymentID, method, req.ToMethod, reference, strings.TrimSpace(req.ToReference),
			amount, req.Reason, claims.UserID, approver).Scan(&correctionID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE payments SET method = $1, reference_no = NULLIF($2, '') WHERE id::text = $3`,
			req.ToMethod, strings.TrimSpace(req.ToReference), paymentID); err != nil {
			return err
		}
		if _, err := accounting.PostJournalEntry(ctx, tx, branchID, "payment_correction", correctionID,
			fmt.Sprintf("Payment correction %s → %s: %s", method, req.ToMethod, req.Reason), claims.UserID, []accounting.JournalLine{
				{AccountCode: accounting.AccountCodeForPaymentMethod(req.ToMethod), Debit: amount},
				{AccountCode: accounting.AccountCodeForPaymentMethod(method), Credit: amount},
			}); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{
			EntityType: "payment_corrections", EntityID: correctionID, Action: "create", PerformedBy: claims.UserID,
			Before: map[string]any{"payment_id": paymentID, "method": method, "reference": reference},
			After:  map[string]any{"payment_id": paymentID, "method": req.ToMethod, "reference": req.ToReference, "approved_by": approver, "sales_order_id": orderID},
			Reason: req.Reason,
		})
	})
	switch {
	case err == nil:
		h.OrderDetail(w, r)
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "payment not found on this order")
	case errors.Is(err, errPaymentNotCorrectable):
		httpx.Error(w, http.StatusConflict, "PAYMENT_NOT_CORRECTABLE", "only a captured cash, card or UPI payment on a completed sale can move to a different one of those methods")
	case errors.Is(err, errCorrectionApproval):
		httpx.Error(w, http.StatusForbidden, "CORRECTION_NOT_APPROVED", "a Branch Manager or Merchant Admin must approve with their employee code and PIN")
	default:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not correct the payment")
	}
}
