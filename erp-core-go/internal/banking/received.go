package banking

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/accounting"
	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/customers"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/locale"
)

type ReceivedCheque struct {
	ChequeID     string  `json:"cheque_id"`
	BranchID     string  `json:"branch_id"`
	CustomerID   *string `json:"customer_id"`
	CustomerName *string `json:"customer_name"`
	SalesOrderID *string `json:"sales_order_id"`
	OrderNumber  *string `json:"order_number"`
	DrawerName   string  `json:"drawer_name"`
	DrawerBank   string  `json:"drawer_bank"`
	DrawerBranch string  `json:"drawer_branch"`
	ChequeNumber string  `json:"cheque_number"`
	ChequeDate   string  `json:"cheque_date"`
	Amount       string  `json:"amount"`
	Status       string  `json:"status"`
	// PostDated: dated after today and not yet deposited.
	PostDated            bool    `json:"post_dated"`
	DepositBankAccountID *string `json:"deposit_bank_account_id"`
	DepositBankAccount   *string `json:"deposit_bank_account"`
	DepositedOn          *string `json:"deposited_on"`
	ClearedOn            *string `json:"cleared_on"`
	BouncedOn            *string `json:"bounced_on"`
	StatusReason         string  `json:"status_reason"`
	Notes                string  `json:"notes"`
	CreatedAt            string  `json:"created_at"`
}

const receivedSelect = `
	SELECT rc.id::text, rc.branch_id::text, rc.customer_id::text, c.name, rc.sales_order_id::text, so.order_number,
	       rc.drawer_name, COALESCE(rc.drawer_bank, ''), COALESCE(rc.drawer_branch, ''), rc.cheque_number, rc.cheque_date::text,
	       rc.amount::text, rc.status,
	       rc.status = 'received' AND rc.cheque_date > (now() AT TIME ZONE app_merchant_tz())::date,
	       rc.deposit_bank_account_id::text, ba.bank_name || ' ••' || right(ba.account_number, 4),
	       rc.deposited_on::text, rc.cleared_on::text, rc.bounced_on::text, COALESCE(rc.status_reason, ''), COALESCE(rc.notes, ''),
	       rc.created_at::text
	FROM received_cheques rc
	LEFT JOIN customers c ON c.id = rc.customer_id
	LEFT JOIN sales_orders so ON so.id = rc.sales_order_id
	LEFT JOIN bank_accounts ba ON ba.id = rc.deposit_bank_account_id`

func scanReceived(row pgx.Row) (ReceivedCheque, error) {
	var c ReceivedCheque
	err := row.Scan(&c.ChequeID, &c.BranchID, &c.CustomerID, &c.CustomerName, &c.SalesOrderID, &c.OrderNumber,
		&c.DrawerName, &c.DrawerBank, &c.DrawerBranch, &c.ChequeNumber, &c.ChequeDate, &c.Amount, &c.Status, &c.PostDated,
		&c.DepositBankAccountID, &c.DepositBankAccount, &c.DepositedOn, &c.ClearedOn, &c.BouncedOn, &c.StatusReason, &c.Notes, &c.CreatedAt)
	return c, err
}

func loadReceived(ctx context.Context, tx pgx.Tx, id string) (ReceivedCheque, error) {
	return scanReceived(tx.QueryRow(ctx, receivedSelect+` WHERE rc.id::text = $1 AND app_branch_visible(rc.branch_id)`, id))
}

// ListReceived: GET /cheques/received?status=&branch_id=&from=&to=&q=&post_dated=true
func (h *Handler) ListReceived(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	q := r.URL.Query()
	list := []ReceivedCheque{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, receivedSelect+`
			WHERE app_branch_visible(rc.branch_id)
			  AND ($1 = '' OR rc.status = $1) AND ($2 = '' OR rc.branch_id::text = $2)
			  AND ($3 = '' OR rc.cheque_date >= $3::date) AND ($4 = '' OR rc.cheque_date <= $4::date)
			  AND ($5 = '' OR rc.cheque_number LIKE '%' || $5 || '%' OR rc.drawer_name ILIKE '%' || $5 || '%' OR c.name ILIKE '%' || $5 || '%')
			  AND ($6 <> 'true' OR (rc.status = 'received' AND rc.cheque_date > (now() AT TIME ZONE app_merchant_tz())::date))
			ORDER BY rc.cheque_date, rc.created_at LIMIT 500`,
			q.Get("status"), q.Get("branch_id"), q.Get("from"), q.Get("to"), strings.TrimSpace(q.Get("q")), q.Get("post_dated"))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scanReceived(rows)
			if err != nil {
				return err
			}
			list = append(list, c)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list received cheques")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"cheques": list})
}

// CreateReceived: POST /cheques/received {branch_id, customer_id,
// sales_order_id?, drawer_name?, drawer_bank?, drawer_branch?,
// cheque_number, cheque_date, amount, notes?} — cheques.manage. Nothing
// is posted yet: the receipt is recorded when the cheque is deposited (a
// post-dated cheque, on or after its date).
func (h *Handler) CreateReceived(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req struct {
		BranchID     string  `json:"branch_id"`
		CustomerID   string  `json:"customer_id"`
		SalesOrderID string  `json:"sales_order_id"`
		DrawerName   string  `json:"drawer_name"`
		DrawerBank   string  `json:"drawer_bank"`
		DrawerBranch string  `json:"drawer_branch"`
		ChequeNumber string  `json:"cheque_number"`
		ChequeDate   string  `json:"cheque_date"`
		Amount       float64 `json:"amount"`
		Notes        string  `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	for _, f := range []*string{&req.BranchID, &req.CustomerID, &req.SalesOrderID, &req.DrawerName, &req.DrawerBank, &req.DrawerBranch, &req.ChequeNumber, &req.Notes} {
		*f = strings.TrimSpace(*f)
	}
	bad := ""
	switch {
	case req.BranchID == "" || req.CustomerID == "":
		bad = "branch_id and customer_id are required"
	case req.ChequeNumber == "" || len(req.ChequeNumber) > 20:
		bad = "cheque_number is required (at most 20 characters)"
	case !validDate(req.ChequeDate):
		bad = "cheque_date must be YYYY-MM-DD"
	case req.Amount <= 0 || req.Amount >= 1e11:
		bad = "amount must be positive"
	case len(req.DrawerName) > 120 || len(req.DrawerBank) > 120 || len(req.DrawerBranch) > 120:
		bad = "names are at most 120 characters"
	}
	if bad != "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", bad)
		return
	}
	var resp ReceivedCheque
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var name string
		if err := tx.QueryRow(ctx, `SELECT name FROM customers WHERE id::text = $1`, req.CustomerID).Scan(&name); errors.Is(err, pgx.ErrNoRows) {
			return invalidf("customer not found")
		} else if err != nil {
			return err
		}
		if req.DrawerName == "" {
			req.DrawerName = name
		}
		var visible bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM branches WHERE id::text = $1 AND app_branch_visible(id))`, req.BranchID).Scan(&visible); err != nil {
			return err
		}
		if !visible {
			return invalidf("branch_id is not a branch you can manage")
		}
		if req.SalesOrderID != "" {
			var outstanding float64
			if err := tx.QueryRow(ctx, `SELECT credit_amount - credit_paid FROM sales_orders WHERE id::text = $1 AND customer_id::text = $2`,
				req.SalesOrderID, req.CustomerID).Scan(&outstanding); errors.Is(err, pgx.ErrNoRows) {
				return invalidf("sales_order_id is not a credit sale of this customer")
			} else if err != nil {
				return err
			}
			if req.Amount > outstanding+0.01 {
				return conflict("the cheque is more than the invoice's outstanding %.2f", outstanding)
			}
		}
		var id string
		if err := tx.QueryRow(ctx, `
			INSERT INTO received_cheques (merchant_id, branch_id, customer_id, sales_order_id, drawer_name, drawer_bank, drawer_branch,
			                              cheque_number, cheque_date, amount, notes, created_by)
			VALUES (current_setting('app.tenant_id')::uuid, $1::uuid, $2::uuid, NULLIF($3, '')::uuid, $4, NULLIF($5, ''), NULLIF($6, ''),
			        $7, $8::date, $9, NULLIF($10, ''), $11) RETURNING id`,
			req.BranchID, req.CustomerID, req.SalesOrderID, req.DrawerName, req.DrawerBank, req.DrawerBranch,
			req.ChequeNumber, req.ChequeDate, req.Amount, req.Notes, claims.UserID).Scan(&id); err != nil {
			return err
		}
		var err error
		if resp, err = loadReceived(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "received_cheque", EntityID: id, Action: "create", PerformedBy: claims.UserID, After: resp})
	})
	if err != nil {
		respondErr(w, err, "received cheque")
		return
	}
	httpx.JSON(w, http.StatusCreated, resp)
}

// ReceivedAction: POST /cheques/received/{id}/{action} — cheques.manage.
//
//	deposit {bank_account_id, date?}  paid into one of our accounts (not
//	        before the cheque's date). The receipt is recorded then:
//	        against the invoice when one is named (it is owed that much
//	        less), else on account (Dr the bank, Cr Receivable).
//	clear   {date?}                   the bank credited it
//	bounce  {date?, reason}           the bank returned it: the receipt is
//	        reversed (Dr Receivable, Cr the bank) and the invoice is owed again
//	return  {reason}                  handed back before deposit
func (h *Handler) ReceivedAction(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id, action := chi.URLParam(r, "id"), chi.URLParam(r, "action")
	var req struct {
		BankAccountID string `json:"bank_account_id"`
		Date          string `json:"date"`
		Reason        string `json:"reason"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
			return
		}
	}
	req.Reason = strings.TrimSpace(req.Reason)
	switch action {
	case "deposit", "clear", "bounce", "return":
	default:
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "unknown action")
		return
	}
	if (action == "bounce" || action == "return") && req.Reason == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "a reason is required")
		return
	}
	if action == "deposit" && strings.TrimSpace(req.BankAccountID) == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "bank_account_id is required to deposit")
		return
	}
	if req.Date != "" && !validDate(req.Date) {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "date must be YYYY-MM-DD")
		return
	}
	var resp ReceivedCheque
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		c, err := loadReceived(ctx, tx, id)
		if err != nil {
			return err
		}
		date := req.Date
		if date == "" {
			date = time.Now().In(locale.Location(ctx, tx)).Format("2006-01-02")
		}
		need := map[string]string{"deposit": "received", "clear": "deposited", "bounce": "deposited", "return": "received"}[action]
		if c.Status != need {
			return conflict("this cheque is %s; it must be %s to %s", c.Status, need, action)
		}
		switch action {
		case "deposit":
			err = deposit(ctx, tx, c, strings.TrimSpace(req.BankAccountID), date, claims.UserID)
		case "clear":
			if c.DepositedOn != nil && date < *c.DepositedOn {
				return conflict("it can't clear before it was deposited (%s)", *c.DepositedOn)
			}
			_, err = tx.Exec(ctx, `UPDATE received_cheques SET status = 'cleared', cleared_on = $2::date WHERE id::text = $1`, id, date)
		case "bounce":
			err = bounceReceived(ctx, tx, c, date, req.Reason, claims.UserID)
		case "return":
			_, err = tx.Exec(ctx, `UPDATE received_cheques SET status = 'returned', status_reason = $2 WHERE id::text = $1`, id, req.Reason)
		}
		if err != nil {
			return err
		}
		if resp, err = loadReceived(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "received_cheque", EntityID: id, Action: "update", PerformedBy: claims.UserID,
			Before: c, After: resp, Reason: strings.TrimSpace(action + " " + req.Reason)})
	})
	switch {
	case errors.Is(err, customers.ErrReceivableOverpaid):
		httpx.Error(w, http.StatusConflict, "PAYMENT_EXCEEDS_BALANCE", "the cheque is more than the invoice still owes")
	case err != nil:
		respondErr(w, err, "received cheque")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}

func deposit(ctx context.Context, tx pgx.Tx, c ReceivedCheque, bankAccountID, date, userID string) error {
	if date < c.ChequeDate {
		return conflict("a post-dated cheque can't be deposited before %s", c.ChequeDate)
	}
	ledger, err := LedgerCode(ctx, tx, bankAccountID)
	if err != nil {
		return err
	}
	var amount float64
	fmt.Sscan(c.Amount, &amount)
	desc := fmt.Sprintf("Cheque %s from %s deposited", c.ChequeNumber, c.DrawerName)
	var paymentID *string
	if c.SalesOrderID != nil {
		pid, _, err := customers.ApplyReceivablePayment(ctx, tx, customers.ReceivablePayment{CustomerID: deref(c.CustomerID),
			SalesOrderID: *c.SalesOrderID, Amount: amount, Method: "cheque", ReferenceNo: c.ChequeNumber, BankAccountID: bankAccountID,
			EntryDate: date, Description: desc, UserID: userID})
		if err != nil {
			return err
		}
		paymentID = &pid
	} else {
		// On account: the customer is owed against, no invoice is settled.
		if _, err := accounting.PostJournalEntryOnDate(ctx, tx, c.BranchID, "receivable_payment", c.ChequeID, desc, userID, date, []accounting.JournalLine{
			{AccountCode: ledger, Debit: amount, PartyType: "bank_account", PartyID: bankAccountID},
			{AccountCode: accounting.AccountReceivable, Credit: amount, PartyType: "customer", PartyID: deref(c.CustomerID)},
		}); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE received_cheques SET status = 'deposited', deposit_bank_account_id = $2::uuid, deposited_on = $3::date,
		receivable_payment_id = $4::uuid WHERE id::text = $1`, c.ChequeID, bankAccountID, date, paymentID)
	return err
}

func bounceReceived(ctx context.Context, tx pgx.Tx, c ReceivedCheque, date, reason, userID string) error {
	desc := fmt.Sprintf("Cheque %s from %s bounced: %s", c.ChequeNumber, c.DrawerName, reason)
	var paymentID *string
	if err := tx.QueryRow(ctx, `SELECT receivable_payment_id::text FROM received_cheques WHERE id::text = $1`, c.ChequeID).Scan(&paymentID); err != nil {
		return err
	}
	if paymentID != nil {
		if err := customers.ReverseReceivablePayment(ctx, tx, *paymentID, "cheque_bounce", c.ChequeID, desc, date, userID); err != nil {
			return err
		}
	} else {
		ledger, err := ledgerCodeAny(ctx, tx, deref(c.DepositBankAccountID))
		if err != nil {
			return err
		}
		var amount float64
		fmt.Sscan(c.Amount, &amount)
		if _, err := accounting.PostJournalEntryOnDate(ctx, tx, c.BranchID, "cheque_bounce", c.ChequeID, desc, userID, date, []accounting.JournalLine{
			{AccountCode: accounting.AccountReceivable, Debit: amount, PartyType: "customer", PartyID: deref(c.CustomerID)},
			{AccountCode: ledger, Credit: amount, PartyType: "bank_account", PartyID: deref(c.DepositBankAccountID)},
		}); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `UPDATE received_cheques SET status = 'bounced', bounced_on = $2::date, status_reason = $3 WHERE id::text = $1`,
		c.ChequeID, date, reason)
	return err
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
