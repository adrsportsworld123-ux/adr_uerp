// Package banking is the merchant's bank accounts (merchant and branch
// level), their cheque books, and the cheques written and received
// (migrations/066). Payments that name a bank account post to its ledger
// account; payments that don't post to Bank (1002) exactly as before.
package banking

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"erp-core-go/internal/accounting"
	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

type Handler struct {
	DB *db.DB
}

// Layout calibrates the cheque print for one bank's leaf.
type Layout struct {
	OffsetX float64 `json:"offset_x_mm"`
	OffsetY float64 `json:"offset_y_mm"`
	// SignatoryLine is printed above the signature box ("For ADR Sports").
	SignatoryLine string `json:"signatory_line,omitempty"`
}

type Account struct {
	BankAccountID string  `json:"bank_account_id"`
	BranchID      *string `json:"branch_id"` // null = merchant level
	BranchName    *string `json:"branch_name"`
	AccountName   string  `json:"account_name"`
	BankName      string  `json:"bank_name"`
	BankBranch    string  `json:"bank_branch"`
	AccountNumber string  `json:"account_number"`
	IFSC          string  `json:"ifsc"`
	AccountType   string  `json:"account_type"`
	LedgerCode    string  `json:"ledger_code"`
	LedgerName    string  `json:"ledger_name"`
	// Balance is the ledger account's posted balance (debit − credit).
	Balance      string `json:"balance"`
	IsDefault    bool   `json:"is_default"`
	Status       string `json:"status"`
	ChequeLayout Layout `json:"cheque_layout"`
	Notes        string `json:"notes"`
	ChequeBooks  int    `json:"cheque_books"`
	LeavesLeft   int    `json:"leaves_left"`
	DisplayName  string `json:"display_name"` // "HDFC Bank ••1234"
	CreatedAt    string `json:"created_at"`
}

const accountSelect = `
	SELECT ba.id::text, ba.branch_id::text, b.name, ba.account_name, ba.bank_name, COALESCE(ba.bank_branch, ''),
	       ba.account_number, COALESCE(ba.ifsc, ''), ba.account_type, coa.code, coa.name,
	       COALESCE((SELECT sum(jl.debit - jl.credit) FROM journal_lines jl JOIN journal_entries je ON je.id = jl.journal_entry_id
	                 WHERE jl.account_id = ba.ledger_account_id AND je.status = 'posted'), 0)::numeric(14,2)::text,
	       ba.is_default, ba.status, ba.cheque_layout, COALESCE(ba.notes, ''),
	       (SELECT count(*) FROM cheque_books cb WHERE cb.bank_account_id = ba.id AND cb.status = 'active')::int,
	       COALESCE((SELECT sum(cb.series_to - cb.series_from + 1) FROM cheque_books cb WHERE cb.bank_account_id = ba.id AND cb.status = 'active'), 0)::int
	         - (SELECT count(*) FROM issued_cheques ic JOIN cheque_books cb ON cb.id = ic.cheque_book_id
	            WHERE cb.bank_account_id = ba.id AND cb.status = 'active')::int,
	       ba.created_at::text
	FROM bank_accounts ba
	JOIN chart_of_accounts coa ON coa.id = ba.ledger_account_id
	LEFT JOIN branches b ON b.id = ba.branch_id`

func scanAccount(row pgx.Row) (Account, error) {
	var a Account
	var layout []byte
	err := row.Scan(&a.BankAccountID, &a.BranchID, &a.BranchName, &a.AccountName, &a.BankName, &a.BankBranch,
		&a.AccountNumber, &a.IFSC, &a.AccountType, &a.LedgerCode, &a.LedgerName, &a.Balance,
		&a.IsDefault, &a.Status, &layout, &a.Notes, &a.ChequeBooks, &a.LeavesLeft, &a.CreatedAt)
	if err == nil {
		_ = json.Unmarshal(layout, &a.ChequeLayout)
		a.DisplayName = DisplayName(a.BankName, a.AccountNumber)
	}
	return a, err
}

// DisplayName: "HDFC Bank ••1234".
func DisplayName(bank, number string) string {
	n := strings.TrimSpace(number)
	if len(n) > 4 {
		n = n[len(n)-4:]
	}
	return strings.TrimSpace(bank) + " ••" + n
}

func loadAccount(ctx context.Context, tx pgx.Tx, id string) (Account, error) {
	return scanAccount(tx.QueryRow(ctx, accountSelect+` WHERE ba.id::text = $1 AND (ba.branch_id IS NULL OR app_branch_visible(ba.branch_id))`, id))
}

// LedgerCode is accounting.BankLedgerCode: "" = Bank (1002).
func LedgerCode(ctx context.Context, tx pgx.Tx, id string) (string, error) {
	return accounting.BankLedgerCode(ctx, tx, strings.TrimSpace(id))
}

var (
	ErrNoBankAccount       = accounting.ErrNoBankAccount
	ErrBankAccountInactive = accounting.ErrBankAccountInactive
	errInvalid             = errors.New("invalid")
)

// ListAccounts: GET /bank-accounts?branch_id=&status= — any signed-in
// user (payment forms choose from it). A branch-scoped user sees the
// merchant-level accounts and their branches' own.
func (h *Handler) ListAccounts(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	q := r.URL.Query()
	branchID, status := q.Get("branch_id"), q.Get("status")
	list := []Account{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, accountSelect+`
			WHERE (ba.branch_id IS NULL OR app_branch_visible(ba.branch_id))
			  AND ($1 = '' OR ba.branch_id IS NULL OR ba.branch_id::text = $1)
			  AND ($2 = '' OR ba.status = $2)
			ORDER BY ba.branch_id NULLS FIRST, ba.is_default DESC, ba.bank_name, ba.account_number`, branchID, status)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			a, err := scanAccount(rows)
			if err != nil {
				return err
			}
			list = append(list, a)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list bank accounts")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"bank_accounts": list})
}

type accountRequest struct {
	BranchID      *string `json:"branch_id"`
	AccountName   *string `json:"account_name"`
	BankName      *string `json:"bank_name"`
	BankBranch    *string `json:"bank_branch"`
	AccountNumber *string `json:"account_number"`
	IFSC          *string `json:"ifsc"`
	AccountType   *string `json:"account_type"`
	IsDefault     *bool   `json:"is_default"`
	Status        *string `json:"status"`
	ChequeLayout  *Layout `json:"cheque_layout"`
	Notes         *string `json:"notes"`
	// Ledger (create only): "main" posts to Bank (1002), "own" opens a
	// sub-account. Default: main for the first account, own afterwards.
	Ledger string `json:"ledger"`
}

var (
	ifscPattern    = regexp.MustCompile(`^[A-Z]{4}0[A-Z0-9]{6}$`)
	accountPattern = regexp.MustCompile(`^[0-9A-Za-z-]{4,34}$`)
)

func (req *accountRequest) apply(a *Account) string {
	str := func(dst *string, src *string, upper bool) {
		if src != nil {
			v := strings.TrimSpace(*src)
			if upper {
				v = strings.ToUpper(v)
			}
			*dst = v
		}
	}
	str(&a.AccountName, req.AccountName, false)
	str(&a.BankName, req.BankName, false)
	str(&a.BankBranch, req.BankBranch, false)
	str(&a.AccountNumber, req.AccountNumber, false)
	str(&a.IFSC, req.IFSC, true)
	str(&a.AccountType, req.AccountType, false)
	str(&a.Status, req.Status, false)
	str(&a.Notes, req.Notes, false)
	a.AccountNumber = strings.ReplaceAll(a.AccountNumber, " ", "")
	if req.IsDefault != nil {
		a.IsDefault = *req.IsDefault
	}
	if req.ChequeLayout != nil {
		a.ChequeLayout = *req.ChequeLayout
		a.ChequeLayout.SignatoryLine = strings.TrimSpace(a.ChequeLayout.SignatoryLine)
	}
	switch {
	case a.AccountName == "" || a.BankName == "":
		return "account_name and bank_name are required"
	case len(a.AccountName) > 120 || len(a.BankName) > 120 || len(a.BankBranch) > 120:
		return "names are at most 120 characters"
	case !accountPattern.MatchString(a.AccountNumber):
		return "account_number must be 4 to 34 letters or digits"
	case a.IFSC != "" && !ifscPattern.MatchString(a.IFSC):
		return "ifsc is not a valid IFSC (4 letters, 0, 6 letters or digits)"
	case a.AccountType != "current" && a.AccountType != "savings" && a.AccountType != "overdraft" && a.AccountType != "cash_credit":
		return "account_type must be current, savings, overdraft or cash_credit"
	case a.Status != "active" && a.Status != "inactive":
		return "status must be active or inactive"
	case a.ChequeLayout.OffsetX < -20 || a.ChequeLayout.OffsetX > 20 || a.ChequeLayout.OffsetY < -20 || a.ChequeLayout.OffsetY > 20:
		return "cheque_layout offsets are -20 to 20 mm"
	case len(a.ChequeLayout.SignatoryLine) > 60:
		return "cheque_layout.signatory_line is at most 60 characters"
	}
	if a.Status == "inactive" {
		a.IsDefault = false
	}
	return ""
}

func writeErr(w http.ResponseWriter, err error, bad, what string) {
	var pgErr *pgconn.PgError
	switch {
	case errors.Is(err, errInvalid):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", bad)
	case errors.Is(err, ErrNoBankAccount):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "bank account not found")
	case errors.Is(err, ErrBankAccountInactive):
		httpx.Error(w, http.StatusConflict, "BANK_ACCOUNT_INACTIVE", "that bank account is inactive")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", what+" not found")
	case errors.Is(err, accounting.ErrPeriodLocked):
		httpx.Error(w, http.StatusConflict, "PERIOD_LOCKED", "that date is in a locked accounting period")
	case errors.As(err, &pgErr) && pgErr.Code == "23505":
		httpx.Error(w, http.StatusConflict, "DUPLICATE", "that "+what+" already exists")
	default:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not save the "+what)
	}
}

// errConflict is a state the action can't happen from ("this cheque is
// already cleared"); its message goes to the client.
type errConflict struct{ msg string }

func (e errConflict) Error() string { return e.msg }

func conflict(format string, args ...any) error { return errConflict{fmt.Sprintf(format, args...)} }

// clearOtherDefaults keeps one default per scope (merchant level, or one branch).
func clearOtherDefaults(ctx context.Context, tx pgx.Tx, a Account) error {
	_, err := tx.Exec(ctx, `UPDATE bank_accounts SET is_default = false
		WHERE id::text <> $1 AND COALESCE(branch_id::text, '') = COALESCE($2, '') AND is_default`, a.BankAccountID, a.BranchID)
	return err
}

// CreateAccount: POST /bank-accounts — bank.manage.
func (h *Handler) CreateAccount(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req accountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	a := Account{AccountType: "current", Status: "active"}
	bad := req.apply(&a)
	if bad == "" && req.Ledger != "" && req.Ledger != "main" && req.Ledger != "own" {
		bad = "ledger must be main or own"
	}
	if bad != "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", bad)
		return
	}
	var branchID *string
	if req.BranchID != nil && strings.TrimSpace(*req.BranchID) != "" {
		b := strings.TrimSpace(*req.BranchID)
		branchID = &b
	}
	var resp Account
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if branchID != nil {
			var visible bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM branches WHERE id::text = $1 AND app_branch_visible(id))`, *branchID).Scan(&visible); err != nil {
				return err
			}
			if !visible {
				bad = "branch_id is not a branch you can manage"
				return errInvalid
			}
		}
		var mainUsed bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM bank_accounts ba JOIN chart_of_accounts coa ON coa.id = ba.ledger_account_id WHERE coa.code = $1)`,
			accounting.AccountBank).Scan(&mainUsed); err != nil {
			return err
		}
		ledger := req.Ledger
		if ledger == "" {
			ledger = map[bool]string{false: "main", true: "own"}[mainUsed]
		}
		ledgerID, err := ledgerAccount(ctx, tx, ledger, DisplayName(a.BankName, a.AccountNumber))
		if err != nil {
			return err
		}
		layout, _ := json.Marshal(a.ChequeLayout)
		a.BranchID = branchID
		if a.IsDefault {
			if err := clearOtherDefaults(ctx, tx, a); err != nil { // before the insert: one default per scope
				return err
			}
		}
		var id string
		if err := tx.QueryRow(ctx, `
			INSERT INTO bank_accounts (merchant_id, branch_id, account_name, bank_name, bank_branch, account_number, ifsc,
			                           account_type, ledger_account_id, is_default, status, cheque_layout, notes, created_by)
			VALUES (current_setting('app.tenant_id')::uuid, $1::uuid, $2, $3, NULLIF($4, ''), $5, NULLIF($6, ''), $7, $8, $9, $10, $11, NULLIF($12, ''), $13)
			RETURNING id`, branchID, a.AccountName, a.BankName, a.BankBranch, a.AccountNumber, a.IFSC, a.AccountType,
			ledgerID, a.IsDefault, a.Status, layout, a.Notes, claims.UserID).Scan(&id); err != nil {
			return err
		}
		if resp, err = loadAccount(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "bank_account", EntityID: id, Action: "create", PerformedBy: claims.UserID, After: resp})
	})
	if err != nil {
		if bad == "" {
			respondErr(w, err, "bank account")
			return
		}
		writeErr(w, err, bad, "bank account")
		return
	}
	httpx.JSON(w, http.StatusCreated, resp)
}

// ledgerAccount: Bank (1002) for "main", else a new sub-account
// 1002-NN named after the bank account.
func ledgerAccount(ctx context.Context, tx pgx.Tx, ledger, name string) (string, error) {
	var bankID string
	if err := tx.QueryRow(ctx, `SELECT id FROM chart_of_accounts WHERE code = $1`, accounting.AccountBank).Scan(&bankID); err != nil {
		return "", fmt.Errorf("banking: the Bank account %s is missing: %w", accounting.AccountBank, err)
	}
	if ledger == "main" {
		return bankID, nil
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(substring(code FROM '^1002-([0-9]+)$')::int), 1) FROM chart_of_accounts WHERE code ~ '^1002-[0-9]+$'`).Scan(&n); err != nil {
		return "", err
	}
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO chart_of_accounts (merchant_id, parent_id, code, name, account_type, is_system)
		VALUES (current_setting('app.tenant_id')::uuid, $1, $2, $3, 'asset', true) RETURNING id`,
		bankID, fmt.Sprintf("1002-%02d", n+1), "Bank – "+name).Scan(&id)
	return id, err
}

// UpdateAccount: PATCH /bank-accounts/{id} — bank.manage. The ledger
// account and the branch never change (postings already made name them).
func (h *Handler) UpdateAccount(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var req accountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	var resp Account
	var bad string
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := loadAccount(ctx, tx, id)
		if err != nil {
			return err
		}
		a := before
		if bad = req.apply(&a); bad != "" {
			return errInvalid
		}
		layout, _ := json.Marshal(a.ChequeLayout)
		if a.IsDefault {
			if err := clearOtherDefaults(ctx, tx, a); err != nil { // before the update: one default per scope
				return err
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE bank_accounts SET account_name = $2, bank_name = $3, bank_branch = NULLIF($4, ''), account_number = $5,
			       ifsc = NULLIF($6, ''), account_type = $7, is_default = $8, status = $9, cheque_layout = $10,
			       notes = NULLIF($11, ''), updated_at = now()
			WHERE id::text = $1`, id, a.AccountName, a.BankName, a.BankBranch, a.AccountNumber, a.IFSC, a.AccountType,
			a.IsDefault, a.Status, layout, a.Notes); err != nil {
			return err
		}
		if resp, err = loadAccount(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "bank_account", EntityID: id, Action: "update", PerformedBy: claims.UserID, Before: before, After: resp})
	})
	if err != nil {
		if bad == "" {
			respondErr(w, err, "bank account")
			return
		}
		writeErr(w, err, bad, "bank account")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}
