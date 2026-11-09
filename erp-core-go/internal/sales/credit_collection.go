package sales

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/accounting"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/customers"
	"erp-core-go/internal/httpx"
)

// Collecting a credit sale from the sale itself (migrations/082): the POS
// sales history and the admin's sale page record what the customer pays
// against this invoice, through customers.ApplyReceivablePayment, the same
// code as the customer page's POST /customers/{id}/payments (Dr cash/bank,
// Cr Accounts Receivable, raise credit_paid). A till names itself, so the
// cash it takes counts in its drawer (accounting.ComputeCashFlow).

var (
	errNotCreditDue     = errors.New("this sale has nothing left to pay")
	errCreditNotSettled = errors.New("only a completed sale can be paid")
)

// creditPaymentMethods: what a receivable payment can be (the
// customer_receivable_payments CHECK).
var creditPaymentMethods = map[string]bool{"cash": true, "card": true, "upi": true, "bank_transfer": true, "cheque": true}

type creditPaymentRequest struct {
	Amount        float64 `json:"amount"`
	Method        string  `json:"method"`
	ReferenceNo   string  `json:"reference_no"`
	BankAccountID string  `json:"bank_account_id"`
	// POSTerminalID: the till taking the money; "" = the back office.
	POSTerminalID  string `json:"pos_terminal_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

type creditPayment struct {
	PaymentID    string  `json:"payment_id"`
	Amount       string  `json:"amount"`
	Method       string  `json:"method"`
	ReferenceNo  *string `json:"reference_no"`
	ReceivedAt   string  `json:"received_at"`
	RecordedBy   string  `json:"recorded_by"`
	TerminalName *string `json:"terminal_name"`
	Reversed     bool    `json:"reversed"`
}

// creditState is a sale's credit as it now stands, and its payments.
type creditState struct {
	OrderID       string          `json:"order_id"`
	OrderNumber   string          `json:"order_number"`
	CustomerID    *string         `json:"customer_id"`
	CustomerName  *string         `json:"customer_name"`
	DueDate       *string         `json:"due_date"`
	CreditAmount  string          `json:"credit_amount"`
	CreditPaid    string          `json:"credit_paid"`
	PendingAmount string          `json:"pending_amount"`
	PaymentStatus string          `json:"payment_status"`
	Payments      []creditPayment `json:"payments"`
	// PaymentID: the payment this call recorded (POST only).
	PaymentID string `json:"payment_id,omitempty"`
}

func loadCreditState(ctx context.Context, tx pgx.Tx, orderID string) (creditState, error) {
	st := creditState{Payments: []creditPayment{}}
	if err := tx.QueryRow(ctx, `
		SELECT so.id::text, so.order_number, so.customer_id::text, c.name, so.due_date::text,
		       so.credit_amount::text, so.credit_paid::text, `+pendingSQL+`::text, `+paymentStatusSQL+`
		FROM sales_orders so LEFT JOIN customers c ON c.id = so.customer_id
		WHERE so.id::text = $1`, orderID).
		Scan(&st.OrderID, &st.OrderNumber, &st.CustomerID, &st.CustomerName, &st.DueDate,
			&st.CreditAmount, &st.CreditPaid, &st.PendingAmount, &st.PaymentStatus); err != nil {
		return st, err
	}
	rows, err := tx.Query(ctx, `
		SELECT p.id::text, p.amount::text, p.method, p.reference_no, p.received_at, u.name, t.name, p.reversed_at IS NOT NULL
		FROM customer_receivable_payments p
		JOIN users u ON u.id = p.recorded_by
		LEFT JOIN pos_terminals t ON t.id = p.pos_terminal_id
		WHERE p.sales_order_id::text = $1
		ORDER BY p.received_at, p.id`, orderID)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var p creditPayment
		var at time.Time
		if err := rows.Scan(&p.PaymentID, &p.Amount, &p.Method, &p.ReferenceNo, &at, &p.RecordedBy, &p.TerminalName, &p.Reversed); err != nil {
			return st, err
		}
		p.ReceivedAt = at.UTC().Format(time.RFC3339)
		st.Payments = append(st.Payments, p)
	}
	return st, rows.Err()
}

// GetCreditPayments: GET /sales/orders/{id}/credit-payments — the sale's
// credit, what is still owed, and every payment against it.
func (h *Handler) GetCreditPayments(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var st creditState
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		st, err = loadCreditState(ctx, tx, chi.URLParam(r, "id"))
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "sale not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load the sale's credit")
		return
	}
	httpx.JSON(w, http.StatusOK, st)
}

// RecordCreditPayment: POST /sales/orders/{id}/credit-payments
// {amount, method: cash|card|upi|bank_transfer|cheque, reference_no?,
// bank_account_id?, pos_terminal_id?, idempotency_key?} — at most what is
// still owed. A repeated idempotency_key answers with the sale as it
// stands, without paying twice.
func (h *Handler) RecordCreditPayment(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	orderID := chi.URLParam(r, "id")
	var req creditPaymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	req.Amount = math.Round(req.Amount*100) / 100
	req.ReferenceNo = strings.TrimSpace(req.ReferenceNo)
	if req.Amount <= 0 || !creditPaymentMethods[req.Method] {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "a positive amount and a method of cash, card, upi, bank_transfer or cheque are required")
		return
	}
	if req.Method != "bank_transfer" && req.Method != "cheque" {
		req.BankAccountID = ""
	}

	// replay answers a key already used: the sale as it stands.
	replay := func(ctx context.Context, tx pgx.Tx) (bool, error) {
		if req.IdempotencyKey == "" {
			return false, nil
		}
		var paymentID string
		err := tx.QueryRow(ctx, `SELECT id::text FROM customer_receivable_payments WHERE idempotency_key = $1 AND sales_order_id::text = $2`,
			req.IdempotencyKey, orderID).Scan(&paymentID)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return err == nil, err
	}

	var st creditState
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if done, err := replay(ctx, tx); err != nil {
			return err
		} else if done {
			var err error
			st, err = loadCreditState(ctx, tx, orderID)
			return err
		}
		var status string
		var customerID *string
		var pending float64
		if err := tx.QueryRow(ctx, `
			SELECT status, customer_id::text, credit_amount - credit_paid FROM sales_orders WHERE id::text = $1 FOR UPDATE`, orderID).
			Scan(&status, &customerID, &pending); err != nil {
			return err
		}
		if status != "finalized" && status != "partially_refunded" {
			return errCreditNotSettled
		}
		if customerID == nil || pending <= 0.004 {
			return errNotCreditDue
		}
		if req.POSTerminalID != "" {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pos_terminals WHERE id::text = $1)`, req.POSTerminalID).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return errTerminalNotPOS
			}
		}
		paymentID, _, err := customers.ApplyReceivablePayment(ctx, tx, customers.ReceivablePayment{
			CustomerID: *customerID, SalesOrderID: orderID, Amount: req.Amount, Method: req.Method,
			ReferenceNo: req.ReferenceNo, BankAccountID: req.BankAccountID, UserID: claims.UserID,
			POSTerminalID: req.POSTerminalID, IdempotencyKey: req.IdempotencyKey,
		})
		if err != nil {
			return err
		}
		if st, err = loadCreditState(ctx, tx, orderID); err != nil {
			return err
		}
		st.PaymentID = paymentID
		return nil
	})
	if isUniqueViolation(err) {
		// The same key raced in from a retry: answer with what it recorded.
		err = h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			st, err = loadCreditState(ctx, tx, orderID)
			return err
		})
	}
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "sale not found")
	case errors.Is(err, errCreditNotSettled):
		httpx.Error(w, http.StatusConflict, "ORDER_NOT_FINALIZED", err.Error())
	case errors.Is(err, errNotCreditDue):
		httpx.Error(w, http.StatusConflict, "NOTHING_DUE", err.Error())
	case errors.Is(err, customers.ErrReceivableOverpaid):
		httpx.Error(w, http.StatusConflict, "PAYMENT_EXCEEDS_BALANCE", "payment amount exceeds what is still owed on this sale")
	case errors.Is(err, errTerminalNotPOS):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "pos_terminal_id is not a till of this merchant")
	case errors.Is(err, accounting.ErrNoBankAccount):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", err.Error())
	case errors.Is(err, accounting.ErrBankAccountInactive):
		httpx.Error(w, http.StatusConflict, "BANK_ACCOUNT_INACTIVE", "that bank account is inactive")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not record the payment")
	default:
		httpx.JSON(w, http.StatusOK, st)
	}
}

// nonZeroMoney: a report row worth printing (the line is left off a report
// with nothing in it, so an existing report reads as it always has).
func nonZeroMoney(v string) bool {
	var f float64
	_, err := fmt.Sscan(v, &f)
	return err == nil && f != 0
}
