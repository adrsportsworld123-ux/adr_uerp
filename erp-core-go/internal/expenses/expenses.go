// Package expenses records the business's expenses (phase0_1_design.md
// §3.77, migrations/074): rent, advertising, transport, an advance paid to
// the landlord, interior work — each under an expense head that names the
// account it posts to, so a deposit or fixtures land on the balance sheet
// and the rest in profit and loss.
//
// An expense is paid by cash, bank (one of the merchant's accounts), a
// partner from their own money (it adds to their capital,
// internal/partners) or is unpaid (Expenses Payable until paid). Posting:
// Dr the head's account, Cr what paid it. Paying an unpaid one: Dr
// Expenses Payable, Cr cash/bank/partner. Cancelling posts the reverse of
// both; nothing is deleted. import.go books a whole list from a CSV.
package expenses

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
	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/locale"
	"erp-core-go/internal/partners"
)

type Handler struct {
	DB *db.DB
}

var (
	errInvalid  = errors.New("invalid")
	errNotFound = errors.New("not found")
	errState    = errors.New("wrong state")
)

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", errInvalid, fmt.Sprintf(format, a...))
}

// ---------------------------------------------------------------------
// Heads
// ---------------------------------------------------------------------

type head struct {
	HeadID      string `json:"head_id"`
	Name        string `json:"name"`
	AccountID   string `json:"account_id"`
	AccountCode string `json:"account_code"`
	AccountName string `json:"account_name"`
	AccountType string `json:"account_type"`
	Status      string `json:"status"`
}

const headSelect = `
	SELECT h.id::text, h.name, a.id::text, a.code, a.name, a.account_type, h.status
	FROM expense_heads h JOIN chart_of_accounts a ON a.id = h.account_id`

func scanHead(row pgx.Row) (head, error) {
	var h head
	err := row.Scan(&h.HeadID, &h.Name, &h.AccountID, &h.AccountCode, &h.AccountName, &h.AccountType, &h.Status)
	return h, err
}

func loadHeads(ctx context.Context, tx pgx.Tx) ([]head, error) {
	rows, err := tx.Query(ctx, headSelect+` ORDER BY h.status = 'active' DESC, h.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []head{}
	for rows.Next() {
		h, err := scanHead(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// ListHeads: GET /expense-heads (any signed-in user).
func (h *Handler) ListHeads(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var heads []head
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		heads, err = loadHeads(ctx, tx)
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list expense heads")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"heads": heads})
}

// headAccount: an expense head may post to an asset (a deposit, fixtures)
// or an expense account.
func headAccount(ctx context.Context, tx pgx.Tx, code string) (string, error) {
	var id, typ string
	err := tx.QueryRow(ctx, `SELECT id::text, account_type FROM chart_of_accounts WHERE code = $1`, strings.TrimSpace(code)).Scan(&id, &typ)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", invalid("account %s doesn't exist", code)
	}
	if err == nil && typ != "expense" && typ != "asset" {
		return "", invalid("an expense head posts to an expense or asset account, not %s", typ)
	}
	return id, err
}

// SaveHead: POST /expense-heads {name, account_code} and PATCH
// /expense-heads/{id} {name?, account_code?, status?} (expenses.manage).
// A changed account applies to expenses entered from then on.
func (h *Handler) SaveHead(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req struct {
		Name        *string `json:"name"`
		AccountCode *string `json:"account_code"`
		Status      *string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	id := chi.URLParam(r, "id")
	var resp head
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if req.Name != nil {
			n := strings.TrimSpace(*req.Name)
			if n == "" {
				return invalid("name can't be empty")
			}
			req.Name = &n
		}
		if req.Status != nil && *req.Status != "active" && *req.Status != "inactive" {
			return invalid("status is active or inactive")
		}
		var accountID *string
		if req.AccountCode != nil {
			a, err := headAccount(ctx, tx, *req.AccountCode)
			if err != nil {
				return err
			}
			accountID = &a
		}
		if id == "" {
			if req.Name == nil || accountID == nil {
				return invalid("name and account_code are required")
			}
			if err := tx.QueryRow(ctx, `INSERT INTO expense_heads (merchant_id, name, account_id) VALUES (current_setting('app.tenant_id')::uuid, $1, $2::uuid)
				RETURNING id::text`, *req.Name, *accountID).Scan(&id); err != nil {
				return err
			}
		} else {
			tag, err := tx.Exec(ctx, `UPDATE expense_heads SET name = COALESCE($2, name), account_id = COALESCE($3::uuid, account_id),
				status = COALESCE($4, status) WHERE id::text = $1`, id, req.Name, accountID, req.Status)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return errNotFound
			}
		}
		var err error
		if resp, err = scanHead(tx.QueryRow(ctx, headSelect+` WHERE h.id::text = $1`, id)); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "expense_head", EntityID: id, Action: map[bool]string{true: "create", false: "update"}[chi.URLParam(r, "id") == ""],
			PerformedBy: claims.UserID, After: resp})
	})
	if respond(w, err, "could not save the expense head") {
		return
	}
	httpx.JSON(w, map[bool]int{true: http.StatusCreated, false: http.StatusOK}[chi.URLParam(r, "id") == ""], resp)
}

// ---------------------------------------------------------------------
// Expenses
// ---------------------------------------------------------------------

// Input is one expense to record (the form and every import row).
type Input struct {
	BranchID      string  `json:"branch_id"`
	Date          string  `json:"expense_date"` // YYYY-MM-DD, not in the future
	HeadID        string  `json:"head_id"`
	Amount        float64 `json:"amount"`
	Description   string  `json:"description"`
	Vendor        string  `json:"vendor"`
	Reference     string  `json:"reference"`
	PaidBy        string  `json:"paid_by"` // cash | bank | partner | unpaid
	BankAccountID string  `json:"bank_account_id"`
	PartnerID     string  `json:"partner_id"`
	batch         string
}

// paidFrom: the credit side of a payment by cash, bank or a partner.
func paidFrom(ctx context.Context, tx pgx.Tx, by, bankAccountID, partnerID string) (accounting.JournalLine, error) {
	switch by {
	case "cash":
		return accounting.JournalLine{AccountCode: accounting.AccountCash}, nil
	case "bank":
		code, err := accounting.BankLedgerCode(ctx, tx, bankAccountID)
		l := accounting.JournalLine{AccountCode: code}
		if bankAccountID != "" {
			l.PartyType, l.PartyID = "bank_account", bankAccountID
		}
		return l, err
	case "partner":
		if partnerID == "" {
			return accounting.JournalLine{}, invalid("choose the partner who paid")
		}
		code, _, err := partners.Capital(ctx, tx, partnerID)
		return accounting.JournalLine{AccountCode: code, PartyType: "partner", PartyID: partnerID}, err
	case "unpaid":
		return accounting.JournalLine{AccountCode: accounting.AccountExpensesPayable}, nil
	}
	return accounting.JournalLine{}, invalid("paid_by is cash, bank, partner or unpaid")
}

// Create records and posts one expense in the caller's transaction.
func Create(ctx context.Context, tx pgx.Tx, in Input, userID string) (string, error) {
	in.Amount = math.Round(in.Amount*100) / 100
	if in.Amount <= 0 {
		return "", invalid("the amount must be above 0")
	}
	if in.BranchID == "" {
		return "", invalid("choose the branch whose books it goes in")
	}
	if in.PaidBy != "bank" {
		in.BankAccountID = ""
	}
	if in.PaidBy != "partner" {
		in.PartnerID = ""
	}
	var accountID, accountCode, headName, headStatus string
	err := tx.QueryRow(ctx, `SELECT a.id::text, a.code, h.name, h.status FROM expense_heads h JOIN chart_of_accounts a ON a.id = h.account_id WHERE h.id::text = $1`,
		in.HeadID).Scan(&accountID, &accountCode, &headName, &headStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", invalid("choose the expense head")
	} else if err != nil {
		return "", err
	}
	if headStatus != "active" {
		return "", invalid("expense head %s is inactive", headName)
	}
	credit, err := paidFrom(ctx, tx, in.PaidBy, in.BankAccountID, in.PartnerID)
	if err != nil {
		return "", err
	}
	credit.Credit = in.Amount
	var id, number string
	if err := tx.QueryRow(ctx, `
		INSERT INTO expenses (merchant_id, expense_number, branch_id, expense_date, head_id, account_id, description, vendor, reference,
		                      amount, paid_by, bank_account_id, partner_id, import_batch, created_by)
		VALUES (current_setting('app.tenant_id')::uuid, 'EXP-' || to_char(clock_timestamp(), 'YYYYMMDDHH24MISSUS'), $1::uuid, $2::date, $3::uuid, $4::uuid,
		        NULLIF($5,''), NULLIF($6,''), NULLIF($7,''), $8, $9, NULLIF($10,'')::uuid, NULLIF($11,'')::uuid, NULLIF($12,''), $13)
		RETURNING id::text, expense_number`,
		in.BranchID, in.Date, in.HeadID, accountID, strings.TrimSpace(in.Description), strings.TrimSpace(in.Vendor), strings.TrimSpace(in.Reference),
		in.Amount, in.PaidBy, in.BankAccountID, in.PartnerID, in.batch, userID).Scan(&id, &number); err != nil {
		return "", err
	}
	desc := headName
	if d := strings.TrimSpace(in.Description); d != "" {
		desc += " — " + d
	} else if v := strings.TrimSpace(in.Vendor); v != "" {
		desc += " — " + v
	}
	num, err := accounting.PostJournalEntryOnDate(ctx, tx, in.BranchID, "expense", id, "Expense "+number+": "+desc, userID, in.Date, []accounting.JournalLine{
		{AccountCode: accountCode, Debit: in.Amount}, credit,
	})
	if err != nil {
		return "", err
	}
	if in.PaidBy == "partner" {
		if err := partners.Record(ctx, tx, partners.Txn{PartnerID: in.PartnerID, Type: "paid_expense", Date: in.Date, Amount: in.Amount, Method: "own_funds",
			BranchID: in.BranchID, Reference: in.Reference, Notes: desc, ExpenseID: id, JournalEntryID: partners.EntryID(ctx, tx, num), CreatedBy: userID}); err != nil {
			return "", err
		}
	}
	return id, nil
}

type expense struct {
	ExpenseID     string  `json:"expense_id"`
	Number        string  `json:"expense_number"`
	BranchID      string  `json:"branch_id"`
	Branch        string  `json:"branch"`
	Date          string  `json:"expense_date"`
	HeadID        string  `json:"head_id"`
	Head          string  `json:"head"`
	AccountCode   string  `json:"account_code"`
	AccountName   string  `json:"account_name"`
	Description   *string `json:"description"`
	Vendor        *string `json:"vendor"`
	Reference     *string `json:"reference"`
	Amount        string  `json:"amount"`
	PaidBy        string  `json:"paid_by"`
	BankAccountID *string `json:"bank_account_id"`
	BankAccount   *string `json:"bank_account"`
	PartnerID     *string `json:"partner_id"`
	Partner       *string `json:"partner"`
	Status        string  `json:"status"`
	PaidAt        *string `json:"paid_at"`
	PaidWith      *string `json:"paid_with"`
	CancelReason  *string `json:"cancel_reason"`
	ImportBatch   *string `json:"import_batch"`
	CreatedAt     string  `json:"created_at"`
}

const expenseSelect = `
	SELECT e.id::text, e.expense_number, e.branch_id::text, b.name, e.expense_date::text, e.head_id::text, h.name, a.code, a.name,
	       e.description, e.vendor, e.reference, e.amount::text, e.paid_by, e.bank_account_id::text,
	       (SELECT ba.bank_name || ' ••' || right(ba.account_number, 4) FROM bank_accounts ba WHERE ba.id = e.bank_account_id),
	       e.partner_id::text, (SELECT p.name FROM partners p WHERE p.id = e.partner_id),
	       e.status, e.paid_at::text, e.paid_with, e.cancel_reason, e.import_batch, e.created_at::text
	FROM expenses e
	JOIN branches b ON b.id = e.branch_id
	JOIN expense_heads h ON h.id = e.head_id
	JOIN chart_of_accounts a ON a.id = e.account_id`

func scanExpense(row pgx.Row) (expense, error) {
	var e expense
	err := row.Scan(&e.ExpenseID, &e.Number, &e.BranchID, &e.Branch, &e.Date, &e.HeadID, &e.Head, &e.AccountCode, &e.AccountName,
		&e.Description, &e.Vendor, &e.Reference, &e.Amount, &e.PaidBy, &e.BankAccountID, &e.BankAccount, &e.PartnerID, &e.Partner,
		&e.Status, &e.PaidAt, &e.PaidWith, &e.CancelReason, &e.ImportBatch, &e.CreatedAt)
	return e, err
}

func today(r *http.Request, h *Handler, tenantID string) string {
	return time.Now().In(locale.MerchantLocation(r.Context(), h.DB, tenantID)).Format("2006-01-02")
}

func checkDate(d, today string) error {
	if _, err := time.Parse("2006-01-02", d); err != nil {
		return invalid("dates are YYYY-MM-DD")
	}
	if d > today {
		return invalid("the date can't be in the future")
	}
	return nil
}

// CreateExpense: POST /expenses (expenses.manage).
func (h *Handler) CreateExpense(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var in Input
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	t := today(r, h, claims.TenantID)
	if in.Date == "" {
		in.Date = t
	}
	var resp expense
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := checkDate(in.Date, t); err != nil {
			return err
		}
		id, err := Create(ctx, tx, in, claims.UserID)
		if err != nil {
			return err
		}
		if resp, err = scanExpense(tx.QueryRow(ctx, expenseSelect+` WHERE e.id::text = $1`, id)); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "expense", EntityID: id, Action: "create", PerformedBy: claims.UserID,
			After: map[string]any{"number": resp.Number, "head": resp.Head, "amount": resp.Amount, "paid_by": resp.PaidBy, "date": resp.Date}})
	})
	if respond(w, err, "could not record the expense") {
		return
	}
	httpx.JSON(w, http.StatusCreated, resp)
}

// ListExpenses: GET /expenses?from=&to=&head_id=&branch_id=&paid_by=&status=&q=
// (expenses.manage), newest first, with totals by head.
func (h *Handler) ListExpenses(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	q := r.URL.Query()
	from, to := q.Get("from"), q.Get("to")
	if to == "" {
		to = today(r, h, claims.TenantID)
	}
	if from == "" {
		from = "2000-01-01"
	}
	list := []expense{}
	type total struct {
		Head   string `json:"head"`
		Count  int    `json:"count"`
		Amount string `json:"amount"`
	}
	totals := []total{}
	var sum string
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		where := `
			WHERE e.expense_date BETWEEN $1::date AND $2::date
			  AND ($3 = '' OR e.head_id::text = $3) AND ($4 = '' OR e.branch_id::text = $4)
			  AND ($5 = '' OR e.paid_by = $5) AND ($6 = '' OR e.status = $6)
			  AND ($7 = '' OR e.expense_number ILIKE '%' || $7 || '%' OR e.description ILIKE '%' || $7 || '%' OR e.vendor ILIKE '%' || $7 || '%'
			       OR e.reference ILIKE '%' || $7 || '%')`
		args := []any{from, to, q.Get("head_id"), q.Get("branch_id"), q.Get("paid_by"), q.Get("status"), strings.TrimSpace(q.Get("q"))}
		rows, err := tx.Query(ctx, expenseSelect+where+` ORDER BY e.expense_date DESC, e.created_at DESC LIMIT 1000`, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			e, err := scanExpense(rows)
			if err != nil {
				rows.Close()
				return err
			}
			list = append(list, e)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		rows, err = tx.Query(ctx, `SELECT h.name, count(*), sum(e.amount)::text FROM expenses e JOIN expense_heads h ON h.id = e.head_id`+
			where+` AND e.status = 'posted' GROUP BY h.name ORDER BY sum(e.amount) DESC`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var t total
			if err := rows.Scan(&t.Head, &t.Count, &t.Amount); err != nil {
				return err
			}
			totals = append(totals, t)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT COALESCE(sum(e.amount), 0)::numeric(14,2)::text FROM expenses e`+where+` AND e.status = 'posted'`, args...).Scan(&sum)
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list expenses")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"expenses": list, "by_head": totals, "total": sum, "from": from, "to": to})
}

// PayExpense: POST /expenses/{id}/pay {date, paid_with: cash|bank|partner,
// bank_account_id, partner_id} (expenses.manage) — settles an unpaid one:
// Dr Expenses Payable, Cr what paid it.
func (h *Handler) PayExpense(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req struct {
		Date          string `json:"date"`
		PaidWith      string `json:"paid_with"`
		BankAccountID string `json:"bank_account_id"`
		PartnerID     string `json:"partner_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	t := today(r, h, claims.TenantID)
	if req.Date == "" {
		req.Date = t
	}
	id := chi.URLParam(r, "id")
	var resp expense
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := checkDate(req.Date, t); err != nil {
			return err
		}
		if req.PaidWith != "cash" && req.PaidWith != "bank" && req.PaidWith != "partner" {
			return invalid("paid_with is cash, bank or partner")
		}
		cur, err := scanExpense(tx.QueryRow(ctx, expenseSelect+` WHERE e.id::text = $1 FOR UPDATE OF e`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		} else if err != nil {
			return err
		}
		if cur.Status != "posted" || cur.PaidBy != "unpaid" || cur.PaidAt != nil {
			return fmt.Errorf("%w: only an unpaid expense that hasn't been paid can be paid", errState)
		}
		if req.Date < cur.Date {
			return invalid("it can't be paid before its own date")
		}
		credit, err := paidFrom(ctx, tx, req.PaidWith, req.BankAccountID, req.PartnerID)
		if err != nil {
			return err
		}
		var amount float64
		fmt.Sscan(cur.Amount, &amount)
		credit.Credit = amount
		num, err := accounting.PostJournalEntryOnDate(ctx, tx, cur.BranchID, "expense_payment", id, "Payment of expense "+cur.Number+": "+cur.Head, claims.UserID, req.Date,
			[]accounting.JournalLine{{AccountCode: accounting.AccountExpensesPayable, Debit: amount}, credit})
		if err != nil {
			return err
		}
		if req.PaidWith == "partner" {
			if err := partners.Record(ctx, tx, partners.Txn{PartnerID: req.PartnerID, Type: "paid_expense", Date: req.Date, Amount: amount, Method: "own_funds",
				BranchID: cur.BranchID, Notes: "Paid expense " + cur.Number + ": " + cur.Head, ExpenseID: id, JournalEntryID: partners.EntryID(ctx, tx, num),
				CreatedBy: claims.UserID}); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE expenses SET paid_at = $2::date, paid_with = $3, paid_bank_account_id = NULLIF($4,'')::uuid,
			paid_partner_id = NULLIF($5,'')::uuid WHERE id::text = $1`, id, req.Date, req.PaidWith,
			map[bool]string{true: req.BankAccountID}[req.PaidWith == "bank"], map[bool]string{true: req.PartnerID}[req.PaidWith == "partner"]); err != nil {
			return err
		}
		if resp, err = scanExpense(tx.QueryRow(ctx, expenseSelect+` WHERE e.id::text = $1`, id)); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "expense", EntityID: id, Action: "update", PerformedBy: claims.UserID, Reason: "paid",
			After: map[string]any{"paid_at": req.Date, "paid_with": req.PaidWith}})
	})
	if respond(w, err, "could not pay the expense") {
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// CancelExpense: POST /expenses/{id}/cancel {reason} (expenses.manage) —
// posts the reverse of the expense (and of its payment), dated today; a
// partner's capital comes back down.
func (h *Handler) CancelExpense(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	req.Reason = strings.TrimSpace(req.Reason)
	t := today(r, h, claims.TenantID)
	id := chi.URLParam(r, "id")
	var resp expense
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if req.Reason == "" {
			return invalid("say why it is cancelled")
		}
		cur, err := scanExpense(tx.QueryRow(ctx, expenseSelect+` WHERE e.id::text = $1 FOR UPDATE OF e`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		} else if err != nil {
			return err
		}
		if cur.Status != "posted" {
			return fmt.Errorf("%w: already cancelled", errState)
		}
		// Every entry this expense posted, mirrored.
		rows, err := tx.Query(ctx, `
			SELECT je.id::text, a.code, jl.debit::float8, jl.credit::float8, COALESCE(jl.party_type, ''), COALESCE(jl.party_id::text, '')
			FROM journal_entries je JOIN journal_lines jl ON jl.journal_entry_id = je.id JOIN chart_of_accounts a ON a.id = jl.account_id
			WHERE je.source_type IN ('expense', 'expense_payment') AND je.source_id::text = $1 AND je.status = 'posted'
			ORDER BY je.created_at`, id)
		if err != nil {
			return err
		}
		var mirror []accounting.JournalLine
		partnerBack := map[string]float64{}
		for rows.Next() {
			var entryID, code, pType, pID string
			var dr, cr float64
			if err := rows.Scan(&entryID, &code, &dr, &cr, &pType, &pID); err != nil {
				rows.Close()
				return err
			}
			mirror = append(mirror, accounting.JournalLine{AccountCode: code, Debit: cr, Credit: dr, PartyType: pType, PartyID: pID})
			if pType == "partner" {
				partnerBack[pID] += cr - dr
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		num, err := accounting.PostJournalEntryOnDate(ctx, tx, cur.BranchID, "expense_cancel", id, "Cancelled expense "+cur.Number+": "+req.Reason, claims.UserID, t, mirror)
		if err != nil {
			return err
		}
		for pID, amt := range partnerBack {
			if amt == 0 {
				continue
			}
			if err := partners.Record(ctx, tx, partners.Txn{PartnerID: pID, Type: "reversal", Date: t, Amount: -amt, BranchID: cur.BranchID,
				Notes: "Expense " + cur.Number + " cancelled", ExpenseID: id, JournalEntryID: partners.EntryID(ctx, tx, num), CreatedBy: claims.UserID}); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE expenses SET status = 'cancelled', cancel_reason = $2 WHERE id::text = $1`, id, req.Reason); err != nil {
			return err
		}
		if resp, err = scanExpense(tx.QueryRow(ctx, expenseSelect+` WHERE e.id::text = $1`, id)); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "expense", EntityID: id, Action: "update", PerformedBy: claims.UserID, Reason: "cancelled: " + req.Reason})
	})
	if respond(w, err, "could not cancel the expense") {
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// respond maps the errors; true = handled.
func respond(w http.ResponseWriter, err error, fallback string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, errInvalid):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", strings.TrimPrefix(err.Error(), "invalid: "))
	case errors.Is(err, errState):
		httpx.Error(w, http.StatusConflict, "INVALID_STATE", strings.TrimPrefix(err.Error(), "wrong state: "))
	case errors.Is(err, errNotFound), errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "not found")
	case errors.Is(err, partners.ErrNoPartner):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "partner not found")
	case errors.Is(err, partners.ErrPartnerRetired):
		httpx.Error(w, http.StatusConflict, "PARTNER_RETIRED", "that partner has retired")
	case errors.Is(err, accounting.ErrPeriodLocked):
		httpx.Error(w, http.StatusConflict, "PERIOD_LOCKED", "that date is in a locked accounting period")
	case errors.Is(err, accounting.ErrNoBankAccount):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "bank account not found")
	case errors.Is(err, accounting.ErrBankAccountInactive):
		httpx.Error(w, http.StatusConflict, "BANK_ACCOUNT_INACTIVE", "that bank account is inactive")
	case strings.Contains(err.Error(), "uq_expense_heads_name"):
		httpx.Error(w, http.StatusConflict, "HEAD_EXISTS", "an expense head with that name exists")
	default:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", fallback)
	}
	return true
}
