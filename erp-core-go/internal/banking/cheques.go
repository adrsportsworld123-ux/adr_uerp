package banking

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/accounting"
	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/documents"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/locale"
	"erp-core-go/internal/taxes"
)

// ---------------------------------------------------------------------
// Cheque books
// ---------------------------------------------------------------------

type ChequeBook struct {
	ChequeBookID  string  `json:"cheque_book_id"`
	BankAccountID string  `json:"bank_account_id"`
	SeriesFrom    int64   `json:"series_from"`
	SeriesTo      int64   `json:"series_to"`
	LeafDigits    int     `json:"leaf_digits"`
	ReceivedOn    *string `json:"received_on"`
	Status        string  `json:"status"`
	Notes         string  `json:"notes"`
	Used          int     `json:"used"`
	// NextLeaf is the lowest number not yet written or cancelled, "" when
	// the book is used up.
	NextLeaf  string `json:"next_leaf"`
	CreatedAt string `json:"created_at"`
}

func leafNumber(n int64, digits int) string {
	return fmt.Sprintf("%0*d", digits, n)
}

func scanBook(ctx context.Context, tx pgx.Tx, row pgx.Row) (ChequeBook, error) {
	var b ChequeBook
	if err := row.Scan(&b.ChequeBookID, &b.BankAccountID, &b.SeriesFrom, &b.SeriesTo, &b.LeafDigits, &b.ReceivedOn,
		&b.Status, &b.Notes, &b.Used, &b.CreatedAt); err != nil {
		return b, err
	}
	next, err := nextLeaf(ctx, tx, b)
	b.NextLeaf = next
	return b, err
}

const bookSelect = `
	SELECT cb.id::text, cb.bank_account_id::text, cb.series_from, cb.series_to, cb.leaf_digits, cb.received_on::text,
	       cb.status, COALESCE(cb.notes, ''),
	       (SELECT count(*) FROM issued_cheques ic WHERE ic.cheque_book_id = cb.id)::int, cb.created_at::text
	FROM cheque_books cb`

func nextLeaf(ctx context.Context, tx pgx.Tx, b ChequeBook) (string, error) {
	var n *int64
	err := tx.QueryRow(ctx, `
		SELECT min(s) FROM generate_series($2::bigint, $3::bigint) s
		WHERE NOT EXISTS (SELECT 1 FROM issued_cheques ic WHERE ic.cheque_book_id::text = $1 AND ic.cheque_number = lpad(s::text, $4, '0'))`,
		b.ChequeBookID, b.SeriesFrom, b.SeriesTo, b.LeafDigits).Scan(&n)
	if err != nil || n == nil {
		return "", err
	}
	return leafNumber(*n, b.LeafDigits), nil
}

// ListBooks: GET /bank-accounts/{id}/cheque-books.
func (h *Handler) ListBooks(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	list := []ChequeBook{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := loadAccount(ctx, tx, id); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, bookSelect+` WHERE cb.bank_account_id::text = $1 ORDER BY cb.status, cb.series_from`, id)
		if err != nil {
			return err
		}
		var books []ChequeBook
		for rows.Next() {
			var b ChequeBook
			if err := rows.Scan(&b.ChequeBookID, &b.BankAccountID, &b.SeriesFrom, &b.SeriesTo, &b.LeafDigits, &b.ReceivedOn,
				&b.Status, &b.Notes, &b.Used, &b.CreatedAt); err != nil {
				rows.Close()
				return err
			}
			books = append(books, b)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, b := range books {
			if b.NextLeaf, err = nextLeaf(ctx, tx, b); err != nil {
				return err
			}
			list = append(list, b)
		}
		return nil
	})
	if err != nil {
		respondErr(w, err, "cheque book")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"cheque_books": list})
}

// CreateBook: POST /bank-accounts/{id}/cheque-books {series_from,
// series_to, leaf_digits?, received_on?, notes?} — bank.manage. A series
// may not overlap another book of the same account.
func (h *Handler) CreateBook(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var req struct {
		SeriesFrom int64  `json:"series_from"`
		SeriesTo   int64  `json:"series_to"`
		LeafDigits int    `json:"leaf_digits"`
		ReceivedOn string `json:"received_on"`
		Notes      string `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.LeafDigits == 0 {
		req.LeafDigits = 6
	}
	bad := ""
	switch {
	case req.SeriesFrom <= 0 || req.SeriesTo < req.SeriesFrom:
		bad = "series_from and series_to must be positive, from ≤ to"
	case req.SeriesTo-req.SeriesFrom >= 1000:
		bad = "a cheque book has at most 1000 leaves"
	case req.LeafDigits < 4 || req.LeafDigits > 10 || len(strconv.FormatInt(req.SeriesTo, 10)) > req.LeafDigits:
		bad = "leaf_digits must be 4 to 10 and fit the series"
	case req.ReceivedOn != "" && !validDate(req.ReceivedOn):
		bad = "received_on must be YYYY-MM-DD"
	}
	if bad != "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", bad)
		return
	}
	var resp ChequeBook
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := loadAccount(ctx, tx, id); err != nil {
			return err
		}
		var overlap bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM cheque_books WHERE bank_account_id::text = $1
			AND series_from <= $3 AND series_to >= $2)`, id, req.SeriesFrom, req.SeriesTo).Scan(&overlap); err != nil {
			return err
		}
		if overlap {
			return conflict("that series overlaps another cheque book of this account")
		}
		var bookID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO cheque_books (merchant_id, bank_account_id, series_from, series_to, leaf_digits, received_on, notes, created_by)
			VALUES (current_setting('app.tenant_id')::uuid, $1::uuid, $2, $3, $4, NULLIF($5, '')::date, NULLIF($6, ''), $7) RETURNING id`,
			id, req.SeriesFrom, req.SeriesTo, req.LeafDigits, req.ReceivedOn, strings.TrimSpace(req.Notes), claims.UserID).Scan(&bookID); err != nil {
			return err
		}
		var err error
		if resp, err = scanBook(ctx, tx, tx.QueryRow(ctx, bookSelect+` WHERE cb.id::text = $1`, bookID)); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "cheque_book", EntityID: bookID, Action: "create", PerformedBy: claims.UserID, After: resp})
	})
	if err != nil {
		if bad == "" {
			respondErr(w, err, "cheque book")
			return
		}
		writeErr(w, err, bad, "cheque book")
		return
	}
	httpx.JSON(w, http.StatusCreated, resp)
}

// CloseBook: POST /cheque-books/{id}/close — bank.manage; no more leaves
// are taken from it (a lost or retired book).
func (h *Handler) CloseBook(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var resp ChequeBook
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE cheque_books SET status = 'closed' WHERE id::text = $1 AND status = 'active'`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		if resp, err = scanBook(ctx, tx, tx.QueryRow(ctx, bookSelect+` WHERE cb.id::text = $1`, id)); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "cheque_book", EntityID: id, Action: "update", PerformedBy: claims.UserID, After: resp, Reason: "closed"})
	})
	if err != nil {
		respondErr(w, err, "active cheque book")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

func validDate(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// ---------------------------------------------------------------------
// Issued cheques
// ---------------------------------------------------------------------

type IssuedCheque struct {
	ChequeID       string  `json:"cheque_id"`
	BankAccountID  string  `json:"bank_account_id"`
	BankAccount    string  `json:"bank_account"`
	ChequeBookID   *string `json:"cheque_book_id"`
	ChequeNumber   string  `json:"cheque_number"`
	ChequeDate     string  `json:"cheque_date"`
	PayeeName      string  `json:"payee_name"`
	Amount         string  `json:"amount"`
	AccountPayee   bool    `json:"account_payee"`
	PartyType      *string `json:"party_type"`
	PartyID        *string `json:"party_id"`
	PurchaseBillID *string `json:"purchase_bill_id"`
	BillNumber     *string `json:"bill_number"`
	BillPaymentID  *string `json:"bill_payment_id"`
	Memo           string  `json:"memo"`
	Status         string  `json:"status"`
	ClearedOn      *string `json:"cleared_on"`
	BouncedOn      *string `json:"bounced_on"`
	StatusReason   string  `json:"status_reason"`
	PrintCount     int     `json:"print_count"`
	CreatedAt      string  `json:"created_at"`
}

const issuedSelect = `
	SELECT ic.id::text, ic.bank_account_id::text, ba.bank_name || ' ••' || right(ba.account_number, 4), ic.cheque_book_id::text,
	       ic.cheque_number, ic.cheque_date::text, ic.payee_name, ic.amount::text, ic.account_payee, ic.party_type, ic.party_id::text,
	       ic.purchase_bill_id::text, pb.bill_number, ic.bill_payment_id::text, COALESCE(ic.memo, ''), ic.status,
	       ic.cleared_on::text, ic.bounced_on::text, COALESCE(ic.status_reason, ''), ic.print_count, ic.created_at::text
	FROM issued_cheques ic
	JOIN bank_accounts ba ON ba.id = ic.bank_account_id
	LEFT JOIN purchase_bills pb ON pb.id = ic.purchase_bill_id`

func scanIssued(row pgx.Row) (IssuedCheque, error) {
	var c IssuedCheque
	err := row.Scan(&c.ChequeID, &c.BankAccountID, &c.BankAccount, &c.ChequeBookID, &c.ChequeNumber, &c.ChequeDate,
		&c.PayeeName, &c.Amount, &c.AccountPayee, &c.PartyType, &c.PartyID, &c.PurchaseBillID, &c.BillNumber, &c.BillPaymentID,
		&c.Memo, &c.Status, &c.ClearedOn, &c.BouncedOn, &c.StatusReason, &c.PrintCount, &c.CreatedAt)
	return c, err
}

func loadIssued(ctx context.Context, tx pgx.Tx, id string) (IssuedCheque, error) {
	return scanIssued(tx.QueryRow(ctx, issuedSelect+` WHERE ic.id::text = $1 AND (ba.branch_id IS NULL OR app_branch_visible(ba.branch_id))`, id))
}

// IssueRequest is one cheque to write. ChequeNumber "" takes the book's
// next leaf (ChequeBookID, or the account's first active book with one).
type IssueRequest struct {
	BankAccountID  string  `json:"bank_account_id"`
	ChequeBookID   string  `json:"cheque_book_id"`
	ChequeNumber   string  `json:"cheque_number"`
	ChequeDate     string  `json:"cheque_date"`
	PayeeName      string  `json:"payee_name"`
	Amount         float64 `json:"amount"`
	AccountPayee   *bool   `json:"account_payee"`
	PartyType      string  `json:"party_type"`
	PartyID        string  `json:"party_id"`
	Memo           string  `json:"memo"`
	PurchaseBillID string  `json:"-"`
	BillPaymentID  string  `json:"-"`
	BranchID       string  `json:"-"`
}

// Issue writes one cheque inside the caller's transaction (a supplier
// bill payment calls it too) and returns its id. The leaf must lie in an
// active book of the account (when a book is named or the number is
// left to it) and be unused.
func Issue(ctx context.Context, tx pgx.Tx, req IssueRequest, userID string) (string, error) {
	req.PayeeName = strings.TrimSpace(req.PayeeName)
	req.ChequeNumber = strings.TrimSpace(req.ChequeNumber)
	switch {
	case req.BankAccountID == "":
		return "", invalidf("bank_account_id is required for a cheque")
	case req.PayeeName == "" || len(req.PayeeName) > 120:
		return "", invalidf("payee_name is required (at most 120 characters)")
	case req.Amount <= 0 || req.Amount >= 1e11:
		return "", invalidf("a cheque needs a positive amount")
	case !validDate(req.ChequeDate):
		return "", invalidf("cheque_date must be YYYY-MM-DD")
	case req.PartyType != "" && req.PartyType != "supplier" && req.PartyType != "employee":
		return "", invalidf("party_type must be supplier or employee")
	case (req.PartyType == "") != (req.PartyID == ""):
		return "", invalidf("party_type and party_id go together")
	}
	if _, err := LedgerCode(ctx, tx, req.BankAccountID); err != nil {
		return "", err
	}
	bookID, number, err := pickLeaf(ctx, tx, req.BankAccountID, req.ChequeBookID, req.ChequeNumber)
	if err != nil {
		return "", err
	}
	payee := true
	if req.AccountPayee != nil {
		payee = *req.AccountPayee
	}
	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO issued_cheques (merchant_id, branch_id, bank_account_id, cheque_book_id, cheque_number, cheque_date, payee_name,
		                            amount, account_payee, party_type, party_id, purchase_bill_id, bill_payment_id, memo, created_by)
		VALUES (current_setting('app.tenant_id')::uuid, NULLIF($1, '')::uuid, $2::uuid, NULLIF($3, '')::uuid, $4, $5::date, $6, $7, $8,
		        NULLIF($9, ''), NULLIF($10, '')::uuid, NULLIF($11, '')::uuid, NULLIF($12, '')::uuid, NULLIF($13, ''), $14)
		RETURNING id`, req.BranchID, req.BankAccountID, bookID, number, req.ChequeDate, req.PayeeName, req.Amount, payee,
		req.PartyType, req.PartyID, req.PurchaseBillID, req.BillPaymentID, strings.TrimSpace(req.Memo), userID).Scan(&id)
	if isUnique(err) {
		return "", conflict("cheque %s of this account is already written or cancelled", number)
	}
	return id, err
}

// pickLeaf resolves the book and leaf number a cheque uses.
func pickLeaf(ctx context.Context, tx pgx.Tx, accountID, bookID, number string) (string, string, error) {
	books, err := tx.Query(ctx, bookSelect+` WHERE cb.bank_account_id::text = $1 AND cb.status = 'active'
		AND ($2 = '' OR cb.id::text = $2) ORDER BY cb.series_from`, accountID, bookID)
	if err != nil {
		return "", "", err
	}
	var list []ChequeBook
	for books.Next() {
		var b ChequeBook
		if err := books.Scan(&b.ChequeBookID, &b.BankAccountID, &b.SeriesFrom, &b.SeriesTo, &b.LeafDigits, &b.ReceivedOn,
			&b.Status, &b.Notes, &b.Used, &b.CreatedAt); err != nil {
			books.Close()
			return "", "", err
		}
		list = append(list, b)
	}
	books.Close()
	if err := books.Err(); err != nil {
		return "", "", err
	}
	if bookID != "" && len(list) == 0 {
		return "", "", conflict("that cheque book is not an active book of this account")
	}
	if number == "" {
		for _, b := range list {
			next, err := nextLeaf(ctx, tx, b)
			if err != nil {
				return "", "", err
			}
			if next != "" {
				return b.ChequeBookID, next, nil
			}
		}
		return "", "", conflict("no cheque leaves left: add a cheque book to this account, or give the cheque number")
	}
	n, err := strconv.ParseInt(number, 10, 64)
	if err != nil || n <= 0 {
		return "", "", invalidf("cheque_number must be digits")
	}
	for _, b := range list {
		if n >= b.SeriesFrom && n <= b.SeriesTo {
			return b.ChequeBookID, leafNumber(n, b.LeafDigits), nil
		}
	}
	if bookID != "" {
		return "", "", conflict("cheque %s is not in that cheque book", number)
	}
	// A leaf from no book on file (a loose leaf, or books not entered).
	return "", number, nil
}

// errInvalidMsg carries a validation message for the client.
type errInvalidMsg struct{ msg string }

func (e errInvalidMsg) Error() string { return e.msg }

func invalidf(format string, args ...any) error { return errInvalidMsg{fmt.Sprintf(format, args...)} }

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "SQLSTATE 23505")
}

// respondErr maps every error banking returns.
func respondErr(w http.ResponseWriter, err error, what string) {
	var inv errInvalidMsg
	var con errConflict
	switch {
	case errors.As(err, &inv):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", inv.msg)
	case errors.As(err, &con):
		httpx.Error(w, http.StatusConflict, "INVALID_STATE", con.msg)
	default:
		writeErr(w, err, "", what)
	}
}

// ListIssued: GET /cheques/issued?status=&bank_account_id=&from=&to=&q=
func (h *Handler) ListIssued(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	q := r.URL.Query()
	list := []IssuedCheque{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, issuedSelect+`
			WHERE (ba.branch_id IS NULL OR app_branch_visible(ba.branch_id))
			  AND ($1 = '' OR ic.status = $1) AND ($2 = '' OR ic.bank_account_id::text = $2)
			  AND ($3 = '' OR ic.cheque_date >= $3::date) AND ($4 = '' OR ic.cheque_date <= $4::date)
			  AND ($5 = '' OR ic.cheque_number LIKE '%' || $5 || '%' OR ic.payee_name ILIKE '%' || $5 || '%')
			ORDER BY ic.cheque_date DESC, ic.cheque_number DESC LIMIT 500`,
			q.Get("status"), q.Get("bank_account_id"), q.Get("from"), q.Get("to"), strings.TrimSpace(q.Get("q")))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scanIssued(rows)
			if err != nil {
				return err
			}
			list = append(list, c)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list cheques")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"cheques": list})
}

// CreateIssued: POST /cheques/issued — cheques.manage. A cheque for any
// payee (rent, a deposit, an employee): it goes on the register and can
// be printed; its ledger entry is the voucher that records the payment.
// Supplier bills are paid by cheque from the bill (POST
// /purchase/bills/{id}/payments with method cheque), which posts it.
func (h *Handler) CreateIssued(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req IssueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	var resp IssuedCheque
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		id, err := Issue(ctx, tx, req, claims.UserID)
		if err != nil {
			return err
		}
		if resp, err = loadIssued(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "issued_cheque", EntityID: id, Action: "create", PerformedBy: claims.UserID, After: resp})
	})
	if err != nil {
		respondErr(w, err, "cheque")
		return
	}
	httpx.JSON(w, http.StatusCreated, resp)
}

// CancelLeaf: POST /cheques/issued/cancel-leaf {bank_account_id,
// cheque_book_id?, cheque_number, reason} — cheques.manage. A spoilt or
// lost leaf that was never written, recorded so it is never used.
func (h *Handler) CancelLeaf(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req struct {
		BankAccountID string `json:"bank_account_id"`
		ChequeBookID  string `json:"cheque_book_id"`
		ChequeNumber  string `json:"cheque_number"`
		Reason        string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if req.Reason == "" || strings.TrimSpace(req.ChequeNumber) == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "cheque_number and a reason are required")
		return
	}
	var resp IssuedCheque
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := LedgerCode(ctx, tx, req.BankAccountID); err != nil {
			return err
		}
		bookID, number, err := pickLeaf(ctx, tx, req.BankAccountID, req.ChequeBookID, strings.TrimSpace(req.ChequeNumber))
		if err != nil {
			return err
		}
		var id string
		err = tx.QueryRow(ctx, `
			INSERT INTO issued_cheques (merchant_id, bank_account_id, cheque_book_id, cheque_number, cheque_date, status, status_reason, created_by)
			VALUES (current_setting('app.tenant_id')::uuid, $1::uuid, NULLIF($2, '')::uuid, $3, CURRENT_DATE, 'cancelled', $4, $5) RETURNING id`,
			req.BankAccountID, bookID, number, req.Reason, claims.UserID).Scan(&id)
		if isUnique(err) {
			return conflict("cheque %s of this account is already written or cancelled", number)
		} else if err != nil {
			return err
		}
		if resp, err = loadIssued(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "issued_cheque", EntityID: id, Action: "create", PerformedBy: claims.UserID, After: resp, Reason: "leaf cancelled: " + req.Reason})
	})
	if err != nil {
		respondErr(w, err, "cheque")
		return
	}
	httpx.JSON(w, http.StatusCreated, resp)
}

// IssuedAction: POST /cheques/issued/{id}/{action} — cheques.manage.
//
//	clear  {date?}          the bank paid it (status issued → cleared)
//	bounce {date?, reason}  the bank returned it: a bill payment it made
//	                        is reversed (Dr Bank, Cr Accounts Payable) and
//	                        the bill is owed again
//	cancel {reason}         stopped or voided before it was presented;
//	                        a bill payment it made is reversed the same way
func (h *Handler) IssuedAction(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id, action := chi.URLParam(r, "id"), chi.URLParam(r, "action")
	var req struct {
		Date   string `json:"date"`
		Reason string `json:"reason"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
			return
		}
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if action != "clear" && action != "bounce" && action != "cancel" {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "unknown action")
		return
	}
	if (action == "bounce" || action == "cancel") && req.Reason == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "a reason is required")
		return
	}
	if req.Date != "" && !validDate(req.Date) {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "date must be YYYY-MM-DD")
		return
	}
	var resp IssuedCheque
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		c, err := loadIssued(ctx, tx, id)
		if err != nil {
			return err
		}
		date := req.Date
		if date == "" {
			date = time.Now().In(locale.Location(ctx, tx)).Format("2006-01-02")
		}
		if c.Status != "issued" {
			return conflict("this cheque is %s", c.Status)
		}
		switch action {
		case "clear":
			if date < c.ChequeDate {
				return conflict("a cheque can't clear before its date (%s)", c.ChequeDate)
			}
			_, err = tx.Exec(ctx, `UPDATE issued_cheques SET status = 'cleared', cleared_on = $2::date WHERE id::text = $1`, id, date)
		case "bounce", "cancel":
			if c.BillPaymentID != nil {
				if err := reverseBillPayment(ctx, tx, c, date, claims.UserID); err != nil {
					return err
				}
			}
			status := map[string]string{"bounce": "bounced", "cancel": "cancelled"}[action]
			_, err = tx.Exec(ctx, `UPDATE issued_cheques SET status = $2, bounced_on = CASE WHEN $2 = 'bounced' THEN $3::date END,
				status_reason = $4 WHERE id::text = $1`, id, status, date, req.Reason)
		}
		if err != nil {
			return err
		}
		if resp, err = loadIssued(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "issued_cheque", EntityID: id, Action: "update", PerformedBy: claims.UserID,
			Before: c, After: resp, Reason: strings.TrimSpace(action + " " + req.Reason)})
	})
	if err != nil {
		respondErr(w, err, "cheque")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// reverseBillPayment undoes the supplier bill payment a cheque made:
// the payment is marked reversed, the bill owes that much again, and
// Dr Bank (the cheque's account) / Cr Accounts Payable is posted.
func reverseBillPayment(ctx context.Context, tx pgx.Tx, c IssuedCheque, date, userID string) error {
	var billID, branchID, supplierID string
	var amount float64
	var reversed bool
	if err := tx.QueryRow(ctx, `
		SELECT p.purchase_bill_id::text, b.branch_id::text, b.supplier_id::text, p.amount, p.reversed_at IS NOT NULL
		FROM purchase_bill_payments p JOIN purchase_bills b ON b.id = p.purchase_bill_id WHERE p.id::text = $1 FOR UPDATE OF p`,
		*c.BillPaymentID).Scan(&billID, &branchID, &supplierID, &amount, &reversed); err != nil {
		return err
	}
	if reversed {
		return nil
	}
	code, err := ledgerCodeAny(ctx, tx, c.BankAccountID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE purchase_bill_payments SET reversed_at = now() WHERE id::text = $1`, *c.BillPaymentID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE purchase_bills SET amount_paid = GREATEST(amount_paid - $2, 0),
		       status = CASE WHEN amount_paid - $2 <= 0.009 THEN 'unpaid' ELSE 'partially_paid' END
		WHERE id::text = $1`, billID, amount); err != nil {
		return err
	}
	_, err = accounting.PostJournalEntryOnDate(ctx, tx, branchID, "cheque_bounce", c.ChequeID,
		fmt.Sprintf("Cheque %s reversed: bill payment undone", c.ChequeNumber), userID, date, []accounting.JournalLine{
			{AccountCode: code, Debit: amount, PartyType: "bank_account", PartyID: c.BankAccountID},
			{AccountCode: accounting.AccountPayable, Credit: amount, PartyType: "supplier", PartyID: supplierID},
		})
	return err
}

// ledgerCodeAny is LedgerCode for an account that may have been made
// inactive since (reversing an old payment still posts to it).
func ledgerCodeAny(ctx context.Context, tx pgx.Tx, id string) (string, error) {
	var code string
	err := tx.QueryRow(ctx, `SELECT coa.code FROM bank_accounts ba JOIN chart_of_accounts coa ON coa.id = ba.ledger_account_id WHERE ba.id::text = $1`, id).Scan(&code)
	return code, err
}

// PrintIssued: GET /cheques/issued/{id}/print — the leaf as a PDF the
// size of the cheque (CTS-2010), calibrated by the account's layout.
// Each print is counted on the register.
func (h *Handler) PrintIssued(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var pdf []byte
	var number string
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		c, err := loadIssued(ctx, tx, id)
		if err != nil {
			return err
		}
		if c.Status == "cancelled" {
			return conflict("a cancelled cheque can't be printed")
		}
		acct, err := loadAccount(ctx, tx, c.BankAccountID)
		if err != nil {
			return err
		}
		cur, err := taxes.MerchantCurrency(ctx, tx)
		if err != nil {
			return err
		}
		amount, _ := strconv.ParseFloat(c.Amount, 64)
		number = c.ChequeNumber
		if pdf, err = documents.RenderCheque(documents.Cheque{PayeeName: c.PayeeName, Amount: amount, Date: c.ChequeDate,
			AccountPayee: c.AccountPayee, Currency: cur, OffsetX: acct.ChequeLayout.OffsetX, OffsetY: acct.ChequeLayout.OffsetY,
			SignatoryLine: acct.ChequeLayout.SignatoryLine}); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE issued_cheques SET print_count = print_count + 1, last_printed_at = now() WHERE id::text = $1`, id)
		return err
	})
	if err != nil {
		respondErr(w, err, "cheque")
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="cheque-%s.pdf"`, number))
	_, _ = w.Write(pdf)
}

// IsClientError reports a validation or state error from Issue (a used
// leaf, no leaves left), which another package's handler answers with
// WriteClientError.
func IsClientError(err error) bool {
	var inv errInvalidMsg
	var con errConflict
	return errors.As(err, &inv) || errors.As(err, &con)
}

// WriteClientError answers an IsClientError error.
func WriteClientError(w http.ResponseWriter, err error) { respondErr(w, err, "cheque") }
