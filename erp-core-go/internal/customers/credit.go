// Phase 4, sub-area 1: B2B Credit Facility (phased_roadmap.md;
// pos_frd_complete.md §6/§8) — see migrations/014_credit_facility.sql's
// header comment for the full design: a checkout payment can now use
// method="credit" (internal/sales/handlers.go's Checkout), booking that
// portion to the existing Receivable account (seeded since Phase 2,
// unused until now) instead of cash/card/upi/bank_transfer/cheque, tagged
// so it shows on GET /accounting/party-ledger?party_type=customer — this
// file is everything else: viewing a customer's outstanding balance and
// aging, recording a payment against it, and the reminder sweeper that
// finally closes the "customer-facing payment reminders" gap
// migrations/013_notifications.sql's header comment deferred here.
package customers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/accounting"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

var validPaymentTerms = map[string]bool{
	"due_on_receipt": true, "net_7": true, "net_15": true, "net_30": true, "net_90": true, "net_60": true,
}

// ---------------------------------------------------------------------
// PATCH /customers/{id}/credit — gated by credit.manage.
// ---------------------------------------------------------------------

type updateCreditRequest struct {
	CreditLimit  *float64 `json:"credit_limit"`
	PaymentTerms *string  `json:"payment_terms"`
	CreditHold   *bool    `json:"credit_hold"`
}

func (h *Handler) UpdateCustomerCredit(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	customerID := chi.URLParam(r, "id")

	var req updateCreditRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.CreditLimit != nil && *req.CreditLimit < 0 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "credit_limit must be non-negative")
		return
	}
	if req.PaymentTerms != nil && !validPaymentTerms[*req.PaymentTerms] {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "payment_terms must be one of due_on_receipt, net_7, net_15, net_30, net_60, net_90")
		return
	}

	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE customers SET
				credit_limit = COALESCE($1, credit_limit),
				payment_terms = COALESCE($2, payment_terms),
				credit_hold = COALESCE($3, credit_hold),
				updated_at = now()
			WHERE id = $4`,
			req.CreditLimit, req.PaymentTerms, req.CreditHold, customerID)
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not update credit settings")
		return
	}
	customer, err := h.fetchByID(r.Context(), claims.TenantID, customerID)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "customer not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "credit settings updated but could not be reloaded")
		return
	}
	httpx.JSON(w, http.StatusOK, customer)
}

// ---------------------------------------------------------------------
// GET /customers/{id}/credit — outstanding balance, aging buckets, and
// open invoices. Aging bucket per open invoice is computed in Go from
// due_date vs today, not a SQL CASE, since building the open_invoices
// list already means iterating every row once.
// ---------------------------------------------------------------------

type agingBuckets struct {
	Current    string `json:"current"` // due_date hasn't arrived yet
	Days0To30  string `json:"days_0_30"`
	Days30To60 string `json:"days_30_60"`
	Days60To90 string `json:"days_60_90"`
	Days90Plus string `json:"days_90_plus"`
}

type openInvoice struct {
	OrderID      string `json:"order_id"`
	OrderNumber  string `json:"order_number"`
	DueDate      string `json:"due_date"`
	CreditAmount string `json:"credit_amount"`
	CreditPaid   string `json:"credit_paid"`
	Outstanding  string `json:"outstanding"`
}

type creditResponse struct {
	CustomerID       string        `json:"customer_id"`
	CreditLimit      string        `json:"credit_limit"`
	PaymentTerms     string        `json:"payment_terms"`
	CreditHold       bool          `json:"credit_hold"`
	OutstandingTotal string        `json:"outstanding_total"`
	Aging            agingBuckets  `json:"aging"`
	OpenInvoices     []openInvoice `json:"open_invoices"`
}

func (h *Handler) GetCustomerCredit(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	customerID := chi.URLParam(r, "id")

	resp := creditResponse{CustomerID: customerID, OpenInvoices: []openInvoice{}}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT credit_limit::text, payment_terms, credit_hold FROM customers WHERE id = $1`, customerID).
			Scan(&resp.CreditLimit, &resp.PaymentTerms, &resp.CreditHold); err != nil {
			return err
		}

		rows, err := tx.Query(ctx, `
			SELECT id, order_number, due_date, credit_amount, credit_paid
			FROM sales_orders
			WHERE customer_id = $1 AND status = 'finalized' AND credit_amount > credit_paid
			ORDER BY due_date NULLS LAST`, customerID)
		if err != nil {
			return err
		}
		defer rows.Close()

		today := time.Now()
		var outstandingTotal, current, d0to30, d30to60, d60to90, d90plus float64
		for rows.Next() {
			var orderID, orderNumber string
			var dueDate *time.Time
			var creditAmount, creditPaid float64
			if err := rows.Scan(&orderID, &orderNumber, &dueDate, &creditAmount, &creditPaid); err != nil {
				return err
			}
			outstanding := creditAmount - creditPaid
			outstandingTotal += outstanding

			daysOverdue := -1 // no due date, or not yet due
			if dueDate != nil {
				daysOverdue = int(today.Sub(*dueDate).Hours() / 24)
			}
			switch {
			case daysOverdue < 0:
				current += outstanding
			case daysOverdue <= 30:
				d0to30 += outstanding
			case daysOverdue <= 60:
				d30to60 += outstanding
			case daysOverdue <= 90:
				d60to90 += outstanding
			default:
				d90plus += outstanding
			}

			dueDateStr := ""
			if dueDate != nil {
				dueDateStr = dueDate.Format("2006-01-02")
			}
			resp.OpenInvoices = append(resp.OpenInvoices, openInvoice{
				OrderID: orderID, OrderNumber: orderNumber, DueDate: dueDateStr,
				CreditAmount: formatMoney(creditAmount), CreditPaid: formatMoney(creditPaid), Outstanding: formatMoney(outstanding),
			})
		}
		if err := rows.Err(); err != nil {
			return err
		}

		resp.OutstandingTotal = formatMoney(outstandingTotal)
		resp.Aging = agingBuckets{
			Current: formatMoney(current), Days0To30: formatMoney(d0to30),
			Days30To60: formatMoney(d30to60), Days60To90: formatMoney(d60to90), Days90Plus: formatMoney(d90plus),
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "customer not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load credit status")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// formatMoney matches internal/accounting's own unexported helper of the
// same name (two decimal places, matching NUMERIC(14,2) columns).
func formatMoney(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64)
}

// ---------------------------------------------------------------------
// POST /customers/{id}/payments — record a payment against a specific
// credit sale. Not permission-gated, mirroring
// POST /purchase/bills/{id}/payments' own precedent (a routine
// operational entry, not a configuration change).
// ---------------------------------------------------------------------

type receivablePaymentRequest struct {
	SalesOrderID string  `json:"sales_order_id"`
	Amount       float64 `json:"amount"`
	Method       string  `json:"method"`
	ReferenceNo  string  `json:"reference_no"`
}

var errReceivableOverpaid = errors.New("payment amount exceeds this invoice's outstanding balance")

func (h *Handler) RecordReceivablePayment(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	customerID := chi.URLParam(r, "id")

	var req receivablePaymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.SalesOrderID == "" || req.Amount <= 0 || req.Method == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "sales_order_id, a positive amount, and method are required")
		return
	}

	var resp openInvoice
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var branchID string
		var creditAmount, creditPaid float64
		if err := tx.QueryRow(ctx, `
			SELECT branch_id, credit_amount, credit_paid FROM sales_orders WHERE id = $1 AND customer_id = $2`,
			req.SalesOrderID, customerID,
		).Scan(&branchID, &creditAmount, &creditPaid); err != nil {
			return err
		}
		if creditPaid+req.Amount > creditAmount+0.01 { // epsilon for float rounding, same as RecordBillPayment
			return errReceivableOverpaid
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO customer_receivable_payments (id, merchant_id, customer_id, sales_order_id, amount, method, reference_no, recorded_by)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, $5, $6)`,
			customerID, req.SalesOrderID, req.Amount, req.Method, req.ReferenceNo, claims.UserID); err != nil {
			return err
		}

		if _, err := accounting.PostJournalEntry(ctx, tx, branchID, "receivable_payment", req.SalesOrderID, "Payment against invoice", claims.UserID, []accounting.JournalLine{
			{AccountCode: accounting.AccountCodeForPaymentMethod(req.Method), Debit: req.Amount},
			{AccountCode: accounting.AccountReceivable, Credit: req.Amount, PartyType: "customer", PartyID: customerID},
		}); err != nil {
			return err
		}

		newPaid := creditPaid + req.Amount
		var orderNumber string
		if err := tx.QueryRow(ctx, `
			UPDATE sales_orders SET credit_paid = $1 WHERE id = $2 RETURNING order_number`, newPaid, req.SalesOrderID,
		).Scan(&orderNumber); err != nil {
			return err
		}

		resp = openInvoice{
			OrderID: req.SalesOrderID, OrderNumber: orderNumber,
			CreditAmount: formatMoney(creditAmount), CreditPaid: formatMoney(newPaid), Outstanding: formatMoney(creditAmount - newPaid),
		}
		return nil
	})

	switch {
	case errors.Is(err, errReceivableOverpaid):
		httpx.Error(w, http.StatusConflict, "PAYMENT_EXCEEDS_BALANCE", "payment amount exceeds this invoice's outstanding balance")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "this customer has no such credit sale")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not record payment")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}
