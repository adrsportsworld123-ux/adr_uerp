// Package partners is partnership management (phase0_1_design.md §3.77,
// migrations/074): each partner's capital account and profit share.
//
// A partner has their own capital sub-account (3000-NN under Partners'
// Capital). Everything that changes it goes through the ledger:
//   - pay-in: the partner puts money in (Dr cash/bank, Cr their capital);
//   - pay-out: the partner takes money out (Dr their capital, Cr cash/bank);
//   - paid from their own money: an expense (internal/expenses) or a
//     supplier bill payment (internal/purchase, method "partner") credits
//     their capital instead of cash or bank.
//
// partner_transactions keeps the partner's side of each, for the
// statement. The profit share is worked out, not posted: the period's net
// profit (accounting.NetProfit, the same figure as the P&L report) times
// the partner's ratio. A year-end transfer of profit to capital, if the
// firm makes one, is a journal voucher.
package partners

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
)

type Handler struct {
	DB *db.DB
}

// AccountCapital is Partners' Capital, the parent of every partner's own
// 3000-NN account.
const AccountCapital = "3000"

var (
	ErrNoPartner      = errors.New("partner not found")
	ErrPartnerRetired = errors.New("partner has retired")
	errInvalid        = errors.New("invalid")
)

// Capital: the partner's capital account code and name, for a posting
// that credits (or debits) it. Refuses a retired partner.
func Capital(ctx context.Context, tx pgx.Tx, partnerID string) (code, name string, err error) {
	var status string
	err = tx.QueryRow(ctx, `
		SELECT a.code, p.name, p.status FROM partners p JOIN chart_of_accounts a ON a.id = p.account_id
		WHERE p.id::text = $1`, partnerID).Scan(&code, &name, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNoPartner
	}
	if err == nil && status != "active" {
		return "", "", ErrPartnerRetired
	}
	return code, name, err
}

// Txn is one line of a partner's register (partner_transactions).
type Txn struct {
	PartnerID, Type, Date, Method, BankAccountID, BranchID, Reference, Notes string
	Amount                                                                   float64 // + adds to their capital, − takes from it
	ExpenseID, BillPaymentID, JournalEntryID                                 string
	CreatedBy                                                                string
}

// Record writes a partner_transactions row; the caller has posted the
// ledger entry.
func Record(ctx context.Context, tx pgx.Tx, t Txn) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO partner_transactions (merchant_id, partner_id, txn_type, txn_date, amount, method, bank_account_id, branch_id,
		                                  reference, notes, expense_id, bill_payment_id, journal_entry_id, created_by)
		VALUES (current_setting('app.tenant_id')::uuid, $1::uuid, $2, $3::date, $4, NULLIF($5,''), NULLIF($6,'')::uuid, NULLIF($7,'')::uuid,
		        NULLIF($8,''), NULLIF($9,''), NULLIF($10,'')::uuid, NULLIF($11,'')::uuid, NULLIF($12,'')::uuid, NULLIF($13,'')::uuid)`,
		t.PartnerID, t.Type, t.Date, t.Amount, t.Method, t.BankAccountID, t.BranchID, t.Reference, t.Notes,
		t.ExpenseID, t.BillPaymentID, t.JournalEntryID, t.CreatedBy)
	return err
}

// EntryID: the journal entry a posting just made (by its number).
func EntryID(ctx context.Context, tx pgx.Tx, entryNumber string) string {
	var id string
	_ = tx.QueryRow(ctx, `SELECT id::text FROM journal_entries WHERE entry_number = $1`, entryNumber).Scan(&id)
	return id
}

// ---------------------------------------------------------------------
// Partners
// ---------------------------------------------------------------------

type partner struct {
	PartnerID   string  `json:"partner_id"`
	Name        string  `json:"name"`
	Phone       *string `json:"phone"`
	Email       *string `json:"email"`
	PAN         *string `json:"pan"`
	ShareRatio  string  `json:"share_ratio"`
	Status      string  `json:"status"`
	JoinedOn    *string `json:"joined_on"`
	LeftOn      *string `json:"left_on"`
	Notes       *string `json:"notes"`
	AccountCode string  `json:"account_code"`
	// Capital: the balance of their capital account now (credit − debit);
	// only for partners.manage.
	Capital *string `json:"capital,omitempty"`
}

const partnerSelect = `
	SELECT p.id::text, p.name, p.phone, p.email, p.pan, trim_scale(p.share_ratio)::text, p.status, p.joined_on::text, p.left_on::text, p.notes,
	       a.code,
	       (SELECT COALESCE(sum(jl.credit - jl.debit), 0) FROM journal_lines jl JOIN journal_entries je ON je.id = jl.journal_entry_id
	         WHERE jl.account_id = p.account_id AND je.status = 'posted')::numeric(14,2)::text
	FROM partners p JOIN chart_of_accounts a ON a.id = p.account_id`

func scanPartner(row pgx.Row, withCapital bool) (partner, error) {
	var p partner
	var capital string
	err := row.Scan(&p.PartnerID, &p.Name, &p.Phone, &p.Email, &p.PAN, &p.ShareRatio, &p.Status, &p.JoinedOn, &p.LeftOn, &p.Notes, &p.AccountCode, &capital)
	if withCapital {
		p.Capital = &capital
	}
	return p, err
}

func canManage(ctx context.Context, tx pgx.Tx, userID string) bool {
	codes, err := authn.FetchPermissions(ctx, tx, userID)
	return err == nil && authn.HasPermission(codes, "partners.manage")
}

// List: GET /partners — any signed-in user (a bill payment or an expense
// names the partner who paid it); capital balances and the ratio check
// only for partners.manage.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	list := []partner{}
	var manage bool
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		manage = canManage(ctx, tx, claims.UserID)
		rows, err := tx.Query(ctx, partnerSelect+` ORDER BY p.status = 'active' DESC, p.name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			p, err := scanPartner(rows, manage)
			if err != nil {
				return err
			}
			list = append(list, p)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list partners")
		return
	}
	out := map[string]any{"partners": list}
	if manage {
		var total float64
		for _, p := range list {
			if p.Status == "active" {
				var v float64
				fmt.Sscan(p.ShareRatio, &v)
				total += v
			}
		}
		out["active_share_total"] = fmt.Sprintf("%.4g", total)
		out["shares_balanced"] = math.Abs(total-100) < 0.0001 || len(list) == 0
	}
	httpx.JSON(w, http.StatusOK, out)
}

type partnerRequest struct {
	Name       *string  `json:"name"`
	Phone      *string  `json:"phone"`
	Email      *string  `json:"email"`
	PAN        *string  `json:"pan"`
	ShareRatio *float64 `json:"share_ratio"`
	JoinedOn   *string  `json:"joined_on"`
	LeftOn     *string  `json:"left_on"`
	Notes      *string  `json:"notes"`
	Status     *string  `json:"status"` // active | retired (PATCH)
	Reason     string   `json:"reason"`
}

func (req *partnerRequest) check(create bool) string {
	if create && (req.Name == nil || strings.TrimSpace(*req.Name) == "") {
		return "name is required"
	}
	if req.Name != nil {
		n := strings.TrimSpace(*req.Name)
		if n == "" {
			return "name can't be empty"
		}
		req.Name = &n
	}
	if req.ShareRatio != nil && (*req.ShareRatio < 0 || *req.ShareRatio > 100) {
		return "share_ratio is a percentage from 0 to 100"
	}
	if req.Status != nil && *req.Status != "active" && *req.Status != "retired" {
		return "status is active or retired"
	}
	for _, d := range []*string{req.JoinedOn, req.LeftOn} {
		if d != nil && *d != "" {
			if _, err := time.Parse("2006-01-02", *d); err != nil {
				return "dates are YYYY-MM-DD"
			}
		}
	}
	return ""
}

// capitalAccount opens the partner's own 3000-NN account under Partners'
// Capital.
func capitalAccount(ctx context.Context, tx pgx.Tx, name string) (string, error) {
	var parentID string
	if err := tx.QueryRow(ctx, `SELECT id FROM chart_of_accounts WHERE code = $1`, AccountCapital).Scan(&parentID); err != nil {
		return "", err
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(substring(code FROM '^3000-([0-9]+)$')::int), 0) FROM chart_of_accounts WHERE code ~ '^3000-[0-9]+$'`).Scan(&n); err != nil {
		return "", err
	}
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO chart_of_accounts (merchant_id, parent_id, code, name, account_type, is_system)
		VALUES (current_setting('app.tenant_id')::uuid, $1, $2, $3, 'equity', true) RETURNING id`,
		parentID, fmt.Sprintf("3000-%02d", n+1), "Capital – "+name).Scan(&id)
	return id, err
}

// Create: POST /partners (partners.manage).
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req partnerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if msg := req.check(true); msg != "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", msg)
		return
	}
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return strings.TrimSpace(*p)
	}
	ratio := 0.0
	if req.ShareRatio != nil {
		ratio = *req.ShareRatio
	}
	var resp partner
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var taken bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM partners WHERE lower(name) = lower($1))`, *req.Name).Scan(&taken); err != nil {
			return err
		}
		if taken {
			return fmt.Errorf("%w: a partner named %s already exists", errInvalid, *req.Name)
		}
		accountID, err := capitalAccount(ctx, tx, *req.Name)
		if err != nil {
			return err
		}
		var id string
		if err := tx.QueryRow(ctx, `
			INSERT INTO partners (merchant_id, name, phone, email, pan, share_ratio, account_id, joined_on, notes, created_by)
			VALUES (current_setting('app.tenant_id')::uuid, $1, NULLIF($2,''), NULLIF($3,''), NULLIF(upper($4),''), $5, $6,
			        NULLIF($7,'')::date, NULLIF($8,''), $9)
			RETURNING id::text`,
			*req.Name, str(req.Phone), str(req.Email), str(req.PAN), ratio, accountID, str(req.JoinedOn), str(req.Notes), claims.UserID).Scan(&id); err != nil {
			return err
		}
		if resp, err = scanPartner(tx.QueryRow(ctx, partnerSelect+` WHERE p.id::text = $1`, id), true); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "partner", EntityID: id, Action: "create", PerformedBy: claims.UserID,
			After: map[string]any{"name": resp.Name, "share_ratio": resp.ShareRatio, "account": resp.AccountCode}})
	})
	if errors.Is(err, errInvalid) {
		httpx.Error(w, http.StatusConflict, "PARTNER_EXISTS", strings.TrimPrefix(err.Error(), "invalid: "))
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not add the partner")
		return
	}
	httpx.JSON(w, http.StatusCreated, resp)
}

// Update: PATCH /partners/{id} (partners.manage) — any field; retiring
// keeps the account and its history (status retired, left_on).
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req partnerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if msg := req.check(false); msg != "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", msg)
		return
	}
	id := chi.URLParam(r, "id")
	var resp partner
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := scanPartner(tx.QueryRow(ctx, partnerSelect+` WHERE p.id::text = $1 FOR UPDATE OF p`, id), true)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE partners SET name = COALESCE($2, name), phone = CASE WHEN $3::text IS NULL THEN phone ELSE NULLIF($3, '') END,
			       email = CASE WHEN $4::text IS NULL THEN email ELSE NULLIF($4, '') END,
			       pan = CASE WHEN $5::text IS NULL THEN pan ELSE NULLIF(upper($5), '') END,
			       share_ratio = COALESCE($6, share_ratio),
			       joined_on = CASE WHEN $7::text IS NULL THEN joined_on ELSE NULLIF($7, '')::date END,
			       left_on = CASE WHEN $8::text IS NULL THEN left_on ELSE NULLIF($8, '')::date END,
			       notes = CASE WHEN $9::text IS NULL THEN notes ELSE NULLIF($9, '') END,
			       status = COALESCE($10, status)
			WHERE id::text = $1`,
			id, req.Name, req.Phone, req.Email, req.PAN, req.ShareRatio, req.JoinedOn, req.LeftOn, req.Notes, req.Status); err != nil {
			return err
		}
		if req.Name != nil && *req.Name != before.Name {
			if _, err := tx.Exec(ctx, `UPDATE chart_of_accounts SET name = $2 WHERE code = $1`, before.AccountCode, "Capital – "+*req.Name); err != nil {
				return err
			}
		}
		if resp, err = scanPartner(tx.QueryRow(ctx, partnerSelect+` WHERE p.id::text = $1`, id), true); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "partner", EntityID: id, Action: "update", PerformedBy: claims.UserID, Reason: req.Reason,
			Before: map[string]any{"name": before.Name, "share_ratio": before.ShareRatio, "status": before.Status},
			After:  map[string]any{"name": resp.Name, "share_ratio": resp.ShareRatio, "status": resp.Status}})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "partner not found")
	case err != nil && strings.Contains(err.Error(), "uq_partners_name"):
		httpx.Error(w, http.StatusConflict, "PARTNER_EXISTS", "another partner has that name")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not update the partner")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}

// ---------------------------------------------------------------------
// Pay-in / pay-out
// ---------------------------------------------------------------------

type moneyRequest struct {
	Amount        float64 `json:"amount"`
	Date          string  `json:"date"`   // YYYY-MM-DD, default today, not in the future
	Method        string  `json:"method"` // cash | bank_transfer | cheque
	BankAccountID string  `json:"bank_account_id"`
	BranchID      string  `json:"branch_id"`
	Reference     string  `json:"reference"`
	Notes         string  `json:"notes"`
}

// PayIn: POST /partners/{id}/pay-in — the partner puts money in.
func (h *Handler) PayIn(w http.ResponseWriter, r *http.Request) { h.money(w, r, "pay_in") }

// PayOut: POST /partners/{id}/pay-out — the partner takes money out
// (drawings).
func (h *Handler) PayOut(w http.ResponseWriter, r *http.Request) { h.money(w, r, "pay_out") }

func (h *Handler) money(w http.ResponseWriter, r *http.Request, kind string) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req moneyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	req.Amount = math.Round(req.Amount*100) / 100
	switch {
	case req.Amount <= 0:
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "a positive amount is required")
		return
	case req.Method != "cash" && req.Method != "bank_transfer" && req.Method != "cheque":
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "method is cash, bank_transfer or cheque")
		return
	case req.BranchID == "":
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "branch_id is required (whose books it goes in)")
		return
	}
	loc := locale.MerchantLocation(r.Context(), h.DB, claims.TenantID)
	today := time.Now().In(loc).Format("2006-01-02")
	if req.Date == "" {
		req.Date = today
	} else if _, err := time.Parse("2006-01-02", req.Date); err != nil || req.Date > today {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "date is YYYY-MM-DD and not in the future")
		return
	}
	id := chi.URLParam(r, "id")
	var out map[string]any
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		capCode, name, err := Capital(ctx, tx, id)
		if err != nil {
			return err
		}
		money, err := accounting.PaymentLedgerCode(ctx, tx, req.Method, req.BankAccountID)
		if err != nil {
			return err
		}
		moneyLine := accounting.JournalLine{AccountCode: money}
		if req.BankAccountID != "" && req.Method != "cash" {
			moneyLine.PartyType, moneyLine.PartyID = "bank_account", req.BankAccountID
		}
		capLine := accounting.JournalLine{AccountCode: capCode, PartyType: "partner", PartyID: id}
		amount, source, desc := req.Amount, "partner_pay_in", "Capital paid in by "+name
		if kind == "pay_in" {
			moneyLine.Debit, capLine.Credit = req.Amount, req.Amount
		} else {
			moneyLine.Credit, capLine.Debit = req.Amount, req.Amount
			amount, source, desc = -req.Amount, "partner_pay_out", "Drawings by "+name
		}
		if req.Notes != "" {
			desc += " — " + req.Notes
		}
		num, err := accounting.PostJournalEntryOnDate(ctx, tx, req.BranchID, source, id, desc, claims.UserID, req.Date, []accounting.JournalLine{moneyLine, capLine})
		if err != nil {
			return err
		}
		method := req.Method
		if err := Record(ctx, tx, Txn{PartnerID: id, Type: kind, Date: req.Date, Amount: amount, Method: method, BankAccountID: req.BankAccountID,
			BranchID: req.BranchID, Reference: req.Reference, Notes: req.Notes, JournalEntryID: EntryID(ctx, tx, num), CreatedBy: claims.UserID}); err != nil {
			return err
		}
		out = map[string]any{"partner_id": id, "entry_number": num, "type": kind, "amount": fmt.Sprintf("%.2f", req.Amount)}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "partner", EntityID: id, Action: "update", PerformedBy: claims.UserID,
			Reason: strings.ReplaceAll(kind, "_", "-"), After: out})
	})
	if respondErr(w, err, "could not record it") {
		return
	}
	httpx.JSON(w, http.StatusCreated, out)
}

// respondErr maps the errors a partner posting can meet; true = handled.
func respondErr(w http.ResponseWriter, err error, fallback string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, ErrNoPartner), errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "partner not found")
	case errors.Is(err, ErrPartnerRetired):
		httpx.Error(w, http.StatusConflict, "PARTNER_RETIRED", "this partner has retired")
	case errors.Is(err, accounting.ErrPeriodLocked):
		httpx.Error(w, http.StatusConflict, "PERIOD_LOCKED", "that date is in a locked accounting period")
	case errors.Is(err, accounting.ErrNoBankAccount):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "bank account not found")
	case errors.Is(err, accounting.ErrBankAccountInactive):
		httpx.Error(w, http.StatusConflict, "BANK_ACCOUNT_INACTIVE", "that bank account is inactive")
	default:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", fallback)
	}
	return true
}

// ---------------------------------------------------------------------
// Statement and summary
// ---------------------------------------------------------------------

type statementLine struct {
	Date        string  `json:"date"`
	EntryNumber string  `json:"entry_number"`
	Type        string  `json:"type"` // pay_in | pay_out | paid_expense | paid_bill | reversal | journal (a voucher on the account)
	Description string  `json:"description"`
	Reference   *string `json:"reference"`
	In          string  `json:"in"`  // adds to capital
	Out         string  `json:"out"` // takes from it
	Balance     string  `json:"balance"`
}

func period(r *http.Request, loc *time.Location) (from, to string, ok bool) {
	q := r.URL.Query()
	from, to = q.Get("from"), q.Get("to")
	now := time.Now().In(loc)
	if to == "" {
		to = now.Format("2006-01-02")
	}
	if from == "" {
		fy := time.Date(now.Year(), time.April, 1, 0, 0, 0, 0, loc) // the financial year so far
		if now.Before(fy) {
			fy = fy.AddDate(-1, 0, 0)
		}
		from = fy.Format("2006-01-02")
	}
	f, e1 := time.Parse("2006-01-02", from)
	t, e2 := time.Parse("2006-01-02", to)
	return from, to, e1 == nil && e2 == nil && !t.Before(f)
}

// Statement: GET /partners/{id}/statement?from=&to= (partners.manage;
// default the financial year so far) — opening capital, every entry on the
// partner's capital account with a running balance, closing capital, the
// period's net profit and the partner's share of it.
func (h *Handler) Statement(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	from, to, ok := period(r, locale.MerchantLocation(r.Context(), h.DB, claims.TenantID))
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "from and to are YYYY-MM-DD, from not after to")
		return
	}
	id := chi.URLParam(r, "id")
	var out map[string]any
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		p, err := scanPartner(tx.QueryRow(ctx, partnerSelect+` WHERE p.id::text = $1`, id), true)
		if err != nil {
			return err
		}
		var opening float64
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(sum(jl.credit - jl.debit), 0) FROM journal_lines jl JOIN journal_entries je ON je.id = jl.journal_entry_id
			JOIN partners pa ON pa.account_id = jl.account_id
			WHERE pa.id::text = $1 AND je.status = 'posted' AND je.entry_date < $2::date`, id, from).Scan(&opening); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT je.entry_date::text, je.entry_number, COALESCE(pt.txn_type, 'journal'), COALESCE(je.description, ''), pt.reference,
			       jl.credit::float8, jl.debit::float8
			FROM journal_lines jl
			JOIN journal_entries je ON je.id = jl.journal_entry_id
			JOIN partners pa ON pa.account_id = jl.account_id
			LEFT JOIN LATERAL (SELECT txn_type, reference FROM partner_transactions t
			                   WHERE t.journal_entry_id = je.id AND t.partner_id = pa.id LIMIT 1) pt ON true
			WHERE pa.id::text = $1 AND je.status = 'posted' AND je.entry_date BETWEEN $2::date AND $3::date
			ORDER BY je.entry_date, je.created_at, je.entry_number`, id, from, to)
		if err != nil {
			return err
		}
		defer rows.Close()
		lines := []statementLine{}
		bal := opening
		var in, outTotal float64
		for rows.Next() {
			var l statementLine
			var cr, dr float64
			if err := rows.Scan(&l.Date, &l.EntryNumber, &l.Type, &l.Description, &l.Reference, &cr, &dr); err != nil {
				return err
			}
			bal += cr - dr
			in += cr
			outTotal += dr
			l.In, l.Out, l.Balance = money(cr), money(dr), money(bal)
			lines = append(lines, l)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rows.Close()
		profit, err := accounting.NetProfit(ctx, tx, from, to)
		if err != nil {
			return err
		}
		var ratio float64
		fmt.Sscan(p.ShareRatio, &ratio)
		share := math.Round(profit*ratio) / 100
		out = map[string]any{
			"partner": p, "from": from, "to": to, "lines": lines,
			"opening_capital": money(opening), "paid_in": money(in), "taken_out": money(outTotal), "closing_capital": money(bal),
			"net_profit": money(profit), "share_ratio": p.ShareRatio, "profit_share": money(share),
			"closing_with_profit": money(bal + share),
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "partner not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load the statement")
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// Summary: GET /partners/summary?from=&to= (partners.manage) — every
// partner's capital at the end of the period, share ratio and profit share,
// with the period's net profit.
func (h *Handler) Summary(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	from, to, ok := period(r, locale.MerchantLocation(r.Context(), h.DB, claims.TenantID))
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "from and to are YYYY-MM-DD, from not after to")
		return
	}
	type row struct {
		PartnerID   string `json:"partner_id"`
		Name        string `json:"name"`
		Status      string `json:"status"`
		ShareRatio  string `json:"share_ratio"`
		Capital     string `json:"capital"` // at the end of the period
		PaidIn      string `json:"paid_in"` // in the period
		TakenOut    string `json:"taken_out"`
		ProfitShare string `json:"profit_share"`
	}
	var out map[string]any
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		profit, err := accounting.NetProfit(ctx, tx, from, to)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT p.id::text, p.name, p.status, trim_scale(p.share_ratio)::text, p.share_ratio::float8,
			       COALESCE(sum(jl.credit - jl.debit) FILTER (WHERE je.entry_date <= $2::date), 0)::float8,
			       COALESCE(sum(jl.credit) FILTER (WHERE je.entry_date BETWEEN $1::date AND $2::date), 0)::float8,
			       COALESCE(sum(jl.debit) FILTER (WHERE je.entry_date BETWEEN $1::date AND $2::date), 0)::float8
			FROM partners p
			LEFT JOIN journal_lines jl ON jl.account_id = p.account_id
			LEFT JOIN journal_entries je ON je.id = jl.journal_entry_id AND je.status = 'posted'
			GROUP BY p.id ORDER BY p.status = 'active' DESC, p.name`, from, to)
		if err != nil {
			return err
		}
		defer rows.Close()
		list := []row{}
		var totalRatio, totalCapital float64
		for rows.Next() {
			var x row
			var ratio, capital, in, outV float64
			if err := rows.Scan(&x.PartnerID, &x.Name, &x.Status, &x.ShareRatio, &ratio, &capital, &in, &outV); err != nil {
				return err
			}
			x.Capital, x.PaidIn, x.TakenOut, x.ProfitShare = money(capital), money(in), money(outV), money(math.Round(profit*ratio)/100)
			if x.Status == "active" {
				totalRatio += ratio
			}
			totalCapital += capital
			list = append(list, x)
		}
		out = map[string]any{"from": from, "to": to, "net_profit": money(profit), "partners": list, "total_capital": money(totalCapital),
			"active_share_total": fmt.Sprintf("%.4g", totalRatio), "shares_balanced": math.Abs(totalRatio-100) < 0.0001 || len(list) == 0}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load the partners' summary")
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func money(v float64) string { return fmt.Sprintf("%.2f", math.Round(v*100)/100) }
