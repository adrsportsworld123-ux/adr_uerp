package accounting

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/approvals"
	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/softdelete"
)

// Phase 14: journal vouchers, the accounting settings and the period lock
// (migrations/043_jv_workflow.sql). A voucher is a manual entry being
// prepared; only posting writes to the ledger, and a posted voucher is
// corrected by reversing it. Auto-generated entries (sales, returns,
// purchases ...) aren't vouchers: they stay read-only here and link to the
// document that made them, which has its own way to undo it.

var (
	ErrPeriodLocked         = errors.New("the period is locked")
	errVoucherState         = errors.New("voucher is not in the right state for that")
	errApprovalRequired     = errors.New("journal vouchers need approval")
	errSelfApproval         = errors.New("you can't approve your own voucher")
	errVoucherForbidden     = errors.New("needs accounting.jv_approve")
	errVoucherInvalid       = errors.New("invalid voucher")
	errUnknownAccount       = errors.New("unknown or inactive account")
	errApprovalNotConfigued = errors.New("approval is off")
)

type accountingSettings struct {
	JVApprovalRequired bool    `json:"jv_approval_required"`
	LockedThrough      *string `json:"locked_through"` // YYYY-MM-DD, or null
}

func loadAccountingSettings(ctx context.Context, tx pgx.Tx) (accountingSettings, error) {
	var s accountingSettings
	err := tx.QueryRow(ctx, `SELECT jv_approval_required, locked_through::text FROM accounting_settings`).Scan(&s.JVApprovalRequired, &s.LockedThrough)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, nil
	}
	return s, err
}

// checkPeriodOpen refuses a posting dated on or before the merchant's lock
// date. entryDate nil means today. Called by postJournalEntry, so every
// posting in the system is held to it.
func checkPeriodOpen(ctx context.Context, tx pgx.Tx, entryDate *string) error {
	var locked bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM accounting_settings
		               WHERE locked_through IS NOT NULL AND COALESCE($1::date, CURRENT_DATE) <= locked_through)`, entryDate).Scan(&locked); err != nil {
		return err
	}
	if locked {
		return ErrPeriodLocked
	}
	return nil
}

func hasPermission(ctx context.Context, tx pgx.Tx, userID, code string) (bool, error) {
	perms, err := authn.FetchPermissions(ctx, tx, userID)
	if err != nil {
		return false, err
	}
	return authn.HasPermission(perms, code), nil
}

// GetAccountingSettings: GET /accounting/settings
func (h *Handler) GetAccountingSettings(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var s accountingSettings
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		s, err = loadAccountingSettings(ctx, tx)
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load accounting settings")
		return
	}
	httpx.JSON(w, http.StatusOK, s)
}

// UpdateAccountingSettings: PUT /accounting/settings — accounting.manage.
// The lock date can move forward or back (reopening a period is an
// administrator's decision, and audited like every change here).
func (h *Handler) UpdateAccountingSettings(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req accountingSettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.LockedThrough != nil && *req.LockedThrough != "" {
		if _, err := time.Parse("2006-01-02", *req.LockedThrough); err != nil {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "locked_through must be YYYY-MM-DD or null")
			return
		}
	} else {
		req.LockedThrough = nil
	}
	var saved accountingSettings
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := loadAccountingSettings(ctx, tx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO accounting_settings (merchant_id, jv_approval_required, locked_through, updated_by, updated_at)
			VALUES (current_setting('app.tenant_id')::uuid, $1, $2::date, $3, now())
			ON CONFLICT (merchant_id) DO UPDATE SET jv_approval_required = $1, locked_through = $2::date, updated_by = $3, updated_at = now()`,
			req.JVApprovalRequired, req.LockedThrough, claims.UserID); err != nil {
			return err
		}
		if saved, err = loadAccountingSettings(ctx, tx); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "accounting_settings", EntityID: claims.TenantID, Action: "update",
			PerformedBy: claims.UserID, Before: before, After: saved})
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not save accounting settings")
		return
	}
	httpx.JSON(w, http.StatusOK, saved)
}

// ---------------------------------------------------------------------
// Vouchers
// ---------------------------------------------------------------------

type voucherLineInput struct {
	AccountCode string  `json:"account_code"`
	Debit       float64 `json:"debit"`
	Credit      float64 `json:"credit"`
	PartyType   string  `json:"party_type"`
	PartyID     string  `json:"party_id"`
	Memo        string  `json:"memo"`
}

type voucherInput struct {
	BranchID    string             `json:"branch_id"`
	EntryDate   string             `json:"entry_date"` // YYYY-MM-DD; default today
	Description string             `json:"description"`
	Lines       []voucherLineInput `json:"lines"`
	// Action on create: "draft", "submit" or "post". Default: "post" when
	// approval is off (today's behaviour), "submit" when it's on.
	Action string `json:"action"`
}

type voucherLine struct {
	LineNo      int     `json:"line_no"`
	AccountCode string  `json:"account_code"`
	AccountName string  `json:"account_name"`
	Debit       string  `json:"debit"`
	Credit      string  `json:"credit"`
	PartyType   *string `json:"party_type"`
	PartyID     *string `json:"party_id"`
	PartyName   *string `json:"party_name"`
	Memo        *string `json:"memo"`
}

// partyNameSQL names a line's party (alias l: party_type, party_id). A
// party deleted since is hidden by its table's policy, so it reads null.
const partyNameSQL = `CASE l.party_type
	WHEN 'customer' THEN (SELECT c.name FROM customers c WHERE c.id = l.party_id)
	WHEN 'supplier' THEN (SELECT s.name FROM suppliers s WHERE s.id = l.party_id) END`

// partyAccounts: the control accounts whose balance is the sum of the
// parties' balances (the party ledger). A line on one must name its party,
// or the party ledger stops agreeing with the account.
var partyAccounts = map[string]string{AccountReceivable: "customer", AccountPayable: "supplier"}

type voucher struct {
	VoucherID      string        `json:"voucher_id"`
	VoucherNumber  string        `json:"voucher_number"`
	Status         string        `json:"status"`
	BranchID       *string       `json:"branch_id"`
	EntryDate      string        `json:"entry_date"`
	Description    string        `json:"description"`
	Total          string        `json:"total"`
	CreatedBy      string        `json:"created_by"`
	CreatedByID    string        `json:"created_by_id"`
	SubmittedBy    *string       `json:"submitted_by"`
	ApprovedBy     *string       `json:"approved_by"`
	PostedBy       *string       `json:"posted_by"`
	ReversedBy     *string       `json:"reversed_by"`
	RejectionNote  *string       `json:"rejection_note"`
	ReversalReason *string       `json:"reversal_reason"`
	EntryNumber    *string       `json:"entry_number"`
	ReversalNumber *string       `json:"reversal_entry_number"`
	CreatedAt      string        `json:"created_at"`
	Lines          []voucherLine `json:"lines,omitempty"`
}

const voucherSelect = `
	SELECT v.id, v.voucher_number, v.status, v.branch_id::text, v.entry_date::text, v.description,
	       COALESCE((SELECT sum(debit) FROM journal_voucher_lines l WHERE l.voucher_id = v.id), 0)::text,
	       cu.name, v.created_by::text, su.name, au.name, pu.name, ru.name, v.rejection_note, v.reversal_reason,
	       je.entry_number, rje.entry_number, to_char(v.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
	FROM journal_vouchers v
	JOIN users cu ON cu.id = v.created_by
	LEFT JOIN users su ON su.id = v.submitted_by
	LEFT JOIN users au ON au.id = v.approved_by
	LEFT JOIN users pu ON pu.id = v.posted_by
	LEFT JOIN users ru ON ru.id = v.reversed_by
	LEFT JOIN journal_entries je ON je.id = v.journal_entry_id
	LEFT JOIN journal_entries rje ON rje.id = v.reversal_entry_id`

func scanVoucher(row pgx.Row) (voucher, error) {
	var v voucher
	err := row.Scan(&v.VoucherID, &v.VoucherNumber, &v.Status, &v.BranchID, &v.EntryDate, &v.Description, &v.Total,
		&v.CreatedBy, &v.CreatedByID, &v.SubmittedBy, &v.ApprovedBy, &v.PostedBy, &v.ReversedBy, &v.RejectionNote, &v.ReversalReason,
		&v.EntryNumber, &v.ReversalNumber, &v.CreatedAt)
	return v, err
}

func loadVoucher(ctx context.Context, tx pgx.Tx, id string, lock bool) (voucher, error) {
	q := voucherSelect + ` WHERE v.id::text = $1`
	if lock {
		q += ` FOR UPDATE OF v`
	}
	v, err := scanVoucher(tx.QueryRow(ctx, q, id))
	if err != nil {
		return v, err
	}
	rows, err := tx.Query(ctx, `
		SELECT l.line_no, a.code, a.name, l.debit::text, l.credit::text, l.party_type, l.party_id::text, `+partyNameSQL+`, l.memo
		FROM journal_voucher_lines l JOIN chart_of_accounts a ON a.id = l.account_id
		WHERE l.voucher_id::text = $1 ORDER BY l.line_no`, id)
	if err != nil {
		return v, err
	}
	defer rows.Close()
	v.Lines = []voucherLine{}
	for rows.Next() {
		var l voucherLine
		if err := rows.Scan(&l.LineNo, &l.AccountCode, &l.AccountName, &l.Debit, &l.Credit, &l.PartyType, &l.PartyID, &l.PartyName, &l.Memo); err != nil {
			return v, err
		}
		v.Lines = append(v.Lines, l)
	}
	return v, rows.Err()
}

// validate checks a voucher's shape: a description, a date, at least two
// one-sided positive lines that balance to the paisa.
func (in *voucherInput) validate() error {
	in.Description = strings.TrimSpace(in.Description)
	if in.EntryDate == "" {
		in.EntryDate = time.Now().Format("2006-01-02")
	}
	if _, err := time.Parse("2006-01-02", in.EntryDate); err != nil {
		return fmt.Errorf("%w: entry_date must be YYYY-MM-DD", errVoucherInvalid)
	}
	if in.Description == "" || len(in.Lines) < 2 {
		return fmt.Errorf("%w: a description and at least two lines are required", errVoucherInvalid)
	}
	var dr, cr int64
	for i, l := range in.Lines {
		d, c := paise(l.Debit), paise(l.Credit)
		if strings.TrimSpace(l.AccountCode) == "" || d < 0 || c < 0 || (d == 0) == (c == 0) {
			return fmt.Errorf("%w: line %d needs an account and exactly one of debit or credit, above zero", errVoucherInvalid, i+1)
		}
		in.Lines[i].AccountCode = strings.TrimSpace(l.AccountCode)
		in.Lines[i].PartyType = strings.TrimSpace(l.PartyType)
		in.Lines[i].PartyID = strings.TrimSpace(l.PartyID)
		l = in.Lines[i]
		if l.PartyType != "" && l.PartyType != "customer" && l.PartyType != "supplier" {
			return fmt.Errorf("%w: line %d party_type must be customer or supplier", errVoucherInvalid, i+1)
		}
		if (l.PartyType == "") != (l.PartyID == "") {
			return fmt.Errorf("%w: line %d needs both a party type and a party, or neither", errVoucherInvalid, i+1)
		}
		if l.PartyID != "" && !uuidRe.MatchString(l.PartyID) {
			return fmt.Errorf("%w: line %d party_id is not a valid id", errVoucherInvalid, i+1)
		}
		if want, ok := partyAccounts[l.AccountCode]; ok && l.PartyType != want {
			return fmt.Errorf("%w: line %d is on a %s control account: choose the %s it belongs to", errVoucherInvalid, i+1,
				map[string]string{"customer": "receivables", "supplier": "payables"}[want], want)
		}
		dr += d
		cr += c
	}
	if dr != cr {
		return fmt.Errorf("%w: debits (%.2f) and credits (%.2f) must be equal", errVoucherInvalid, float64(dr)/100, float64(cr)/100)
	}
	return nil
}

func paise(v float64) int64 { return int64(math.Round(v * 100)) }

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func writeVoucherLines(ctx context.Context, tx pgx.Tx, voucherID string, lines []voucherLineInput) error {
	if _, err := tx.Exec(ctx, `DELETE FROM journal_voucher_lines WHERE voucher_id::text = $1`, voucherID); err != nil {
		return err
	}
	for i, l := range lines {
		var accountID string
		err := tx.QueryRow(ctx, `SELECT id FROM chart_of_accounts WHERE code = $1 AND status = 'active'`, strings.TrimSpace(l.AccountCode)).Scan(&accountID)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: %s", errUnknownAccount, l.AccountCode)
		} else if err != nil {
			return err
		}
		if l.PartyID != "" {
			var found bool
			if err := tx.QueryRow(ctx, `SELECT CASE $1 WHEN 'customer' THEN EXISTS (SELECT 1 FROM customers WHERE id::text = $2)
				ELSE EXISTS (SELECT 1 FROM suppliers WHERE id::text = $2) END`, l.PartyType, l.PartyID).Scan(&found); err != nil {
				return err
			}
			if !found {
				return fmt.Errorf("%w: line %d: no such %s", errVoucherInvalid, i+1, l.PartyType)
			}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO journal_voucher_lines (voucher_id, line_no, account_id, debit, credit, party_type, party_id, memo)
			VALUES ($1::uuid, $2, $3, $4, $5, NULLIF($6, ''), NULLIF($7, '')::uuid, NULLIF($8, ''))`,
			voucherID, i+1, accountID, float64(paise(l.Debit))/100, float64(paise(l.Credit))/100, l.PartyType, l.PartyID, strings.TrimSpace(l.Memo)); err != nil {
			return err
		}
	}
	return nil
}

// postVoucher writes the voucher's lines to the ledger, dated its entry
// date (the period lock applies), and marks it posted.
func postVoucher(ctx context.Context, tx pgx.Tx, v voucher, userID string) error {
	lines := make([]JournalLine, 0, len(v.Lines))
	for _, l := range v.Lines {
		jl := JournalLine{AccountCode: l.AccountCode}
		fmt.Sscan(l.Debit, &jl.Debit)
		fmt.Sscan(l.Credit, &jl.Credit)
		if l.PartyType != nil && l.PartyID != nil {
			jl.PartyType, jl.PartyID = *l.PartyType, *l.PartyID
		}
		lines = append(lines, jl)
	}
	branch := ""
	if v.BranchID != nil {
		branch = *v.BranchID
	}
	number, err := PostJournalEntryOnDate(ctx, tx, branch, "manual", v.VoucherID, v.Description+" ("+v.VoucherNumber+")", userID, v.EntryDate, lines)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE journal_vouchers SET status = 'posted', posted_by = $1, posted_at = now(), updated_at = now(),
		       journal_entry_id = (SELECT id FROM journal_entries WHERE entry_number = $2)
		WHERE id::text = $3`, userID, number, v.VoucherID)
	return err
}

func voucherAudit(ctx context.Context, tx pgx.Tx, v voucher, userID, action, reason string) error {
	return audit.Log(ctx, tx, audit.Entry{EntityType: "journal_vouchers", EntityID: v.VoucherID, Action: "update", PerformedBy: userID,
		After: map[string]any{"voucher_number": v.VoucherNumber, "action": action, "status": v.Status}, Reason: reason})
}

func writeVoucherError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "voucher not found")
	case errors.Is(err, errVoucherInvalid), errors.Is(err, errUnknownAccount):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
	case errors.Is(err, ErrPeriodLocked):
		httpx.Error(w, http.StatusConflict, "PERIOD_LOCKED", "that date is in a locked period; pick a date after the lock or ask an administrator to reopen it")
	case errors.Is(err, errApprovalRequired):
		httpx.Error(w, http.StatusConflict, "JV_APPROVAL_REQUIRED", "journal vouchers need approval before they post: submit it instead")
	case errors.Is(err, errApprovalNotConfigued):
		httpx.Error(w, http.StatusConflict, "JV_APPROVAL_OFF", "this voucher doesn't need approval (approval is off, or it's within the amount that posts directly): post it")
	case errors.Is(err, errVoucherState):
		httpx.Error(w, http.StatusConflict, "VOUCHER_STATE", err.Error())
	case errors.Is(err, errSelfApproval):
		httpx.Error(w, http.StatusForbidden, "SELF_APPROVAL", "a voucher has to be approved by someone other than the person who made it")
	case errors.Is(err, errVoucherForbidden):
		httpx.Error(w, http.StatusForbidden, "FORBIDDEN", "needs the accounting.jv_approve permission")
	default:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not save the voucher")
	}
}

// jvNeedsApproval: JV approval is on and (Phase 15 approval policy) the
// voucher is above the amount that may post without it.
func jvNeedsApproval(ctx context.Context, tx pgx.Tx, settings accountingSettings, total float64) (bool, error) {
	if !settings.JVApprovalRequired {
		return false, nil
	}
	policy, err := approvals.Load(ctx, tx)
	if err != nil {
		return false, err
	}
	return policy.JVApprovalAbove == nil || total > *policy.JVApprovalAbove+0.004, nil
}

// createVoucher is the one way a manual entry comes into being, used by
// POST /accounting/vouchers and the older POST /accounting/journal-entries.
func createVoucher(ctx context.Context, tx pgx.Tx, in voucherInput, userID string) (voucher, error) {
	settings, err := loadAccountingSettings(ctx, tx)
	if err != nil {
		return voucher{}, err
	}
	var total float64
	for _, l := range in.Lines {
		total += l.Debit
	}
	needs, err := jvNeedsApproval(ctx, tx, settings, total)
	if err != nil {
		return voucher{}, err
	}
	action := in.Action
	if action == "" {
		action = "post"
		if needs {
			action = "submit"
		}
	}
	switch {
	case action != "draft" && action != "submit" && action != "post":
		return voucher{}, fmt.Errorf("%w: action must be draft, submit or post", errVoucherInvalid)
	case action == "post" && needs:
		return voucher{}, errApprovalRequired
	case action == "submit" && !needs:
		return voucher{}, errApprovalNotConfigued
	}
	if err := checkPeriodOpen(ctx, tx, &in.EntryDate); err != nil {
		return voucher{}, err
	}
	var id string
	status := map[string]string{"draft": "draft", "submit": "submitted", "post": "draft"}[action]
	if err := tx.QueryRow(ctx, `
		INSERT INTO journal_vouchers (merchant_id, voucher_number, branch_id, entry_date, description, status, created_by,
		    submitted_by, submitted_at)
		VALUES (current_setting('app.tenant_id')::uuid, $1, NULLIF($2, '')::uuid, $3::date, $4, $5, $6,
		    CASE WHEN $5 = 'submitted' THEN $6::uuid END, CASE WHEN $5 = 'submitted' THEN now() END)
		RETURNING id`, fmt.Sprintf("JV-%d", time.Now().UnixNano()), in.BranchID, in.EntryDate, in.Description, status, userID).Scan(&id); err != nil {
		return voucher{}, err
	}
	if err := writeVoucherLines(ctx, tx, id, in.Lines); err != nil {
		return voucher{}, err
	}
	v, err := loadVoucher(ctx, tx, id, true)
	if err != nil {
		return v, err
	}
	if action == "post" {
		if err := postVoucher(ctx, tx, v, userID); err != nil {
			return v, err
		}
	}
	if v, err = loadVoucher(ctx, tx, id, false); err != nil {
		return v, err
	}
	return v, voucherAudit(ctx, tx, v, userID, "create:"+action, "")
}

// CreateVoucher: POST /accounting/vouchers
func (h *Handler) CreateVoucher(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var in voucherInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if err := in.validate(); err != nil {
		writeVoucherError(w, err)
		return
	}
	var v voucher
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		v, err = createVoucher(ctx, tx, in, claims.UserID)
		return err
	})
	if err != nil {
		writeVoucherError(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, v)
}

// UpdateVoucher: PUT /accounting/vouchers/{id} — only a draft (including
// one sent back) can change; a submitted, approved or posted one can't.
func (h *Handler) UpdateVoucher(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var in voucherInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if err := in.validate(); err != nil {
		writeVoucherError(w, err)
		return
	}
	id := chi.URLParam(r, "id")
	var v voucher
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		cur, err := loadVoucher(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if cur.Status != "draft" {
			return fmt.Errorf("%w: only a draft can be edited (this one is %s)", errVoucherState, cur.Status)
		}
		if err := checkPeriodOpen(ctx, tx, &in.EntryDate); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE journal_vouchers SET branch_id = NULLIF($1, '')::uuid, entry_date = $2::date, description = $3, updated_at = now()
			WHERE id::text = $4`, in.BranchID, in.EntryDate, in.Description, id); err != nil {
			return err
		}
		if err := writeVoucherLines(ctx, tx, id, in.Lines); err != nil {
			return err
		}
		if v, err = loadVoucher(ctx, tx, id, false); err != nil {
			return err
		}
		return voucherAudit(ctx, tx, v, claims.UserID, "edit", "")
	})
	if err != nil {
		writeVoucherError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, v)
}

// DeleteVoucher: DELETE /accounting/vouchers/{id} — a draft only.
func (h *Handler) DeleteVoucher(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		cur, err := loadVoucher(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if cur.Status != "draft" {
			return fmt.Errorf("%w: only a draft can be deleted (this one is %s)", errVoucherState, cur.Status)
		}
		// softdelete writes the generic delete entry; voucherAudit keeps
		// the voucher's own trail (its lines, in its own shape) as before.
		if err := softdelete.Delete(ctx, tx, "journal_vouchers", id, claims.UserID, ""); err != nil {
			return err
		}
		return voucherAudit(ctx, tx, cur, claims.UserID, "delete", "")
	})
	if err != nil {
		writeVoucherError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// VoucherAction: POST /accounting/vouchers/{id}/{action} where action is
// submit, approve, reject (back to draft, with a note), post or reverse
// ({reason, date?}: a mirror entry; the voucher is then 'reversed').
func (h *Handler) VoucherAction(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id, action := chi.URLParam(r, "id"), chi.URLParam(r, "action")
	var body struct {
		Note   string `json:"note"`
		Reason string `json:"reason"`
		Date   string `json:"date"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	var v voucher
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		settings, err := loadAccountingSettings(ctx, tx)
		if err != nil {
			return err
		}
		cur, err := loadVoucher(ctx, tx, id, true)
		if err != nil {
			return err
		}
		var total float64
		fmt.Sscan(cur.Total, &total)
		needs, err := jvNeedsApproval(ctx, tx, settings, total)
		if err != nil {
			return err
		}
		needApprover := func() error {
			ok, err := hasPermission(ctx, tx, claims.UserID, "accounting.jv_approve")
			if err != nil {
				return err
			}
			if !ok {
				return errVoucherForbidden
			}
			return nil
		}
		state := func(want ...string) error {
			for _, s := range want {
				if cur.Status == s {
					return nil
				}
			}
			return fmt.Errorf("%w: can't %s a voucher that is %s", errVoucherState, action, cur.Status)
		}
		reason := ""
		switch action {
		case "submit":
			if !needs {
				return errApprovalNotConfigued
			}
			if err := state("draft"); err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `UPDATE journal_vouchers SET status = 'submitted', submitted_by = $1, submitted_at = now(), updated_at = now() WHERE id::text = $2`, claims.UserID, id)
		case "approve", "reject":
			if err := state("submitted"); err != nil {
				return err
			}
			if err := needApprover(); err != nil {
				return err
			}
			if action == "approve" {
				if cur.CreatedByID == claims.UserID {
					return errSelfApproval
				}
				_, err = tx.Exec(ctx, `UPDATE journal_vouchers SET status = 'approved', approved_by = $1, approved_at = now(), updated_at = now() WHERE id::text = $2`, claims.UserID, id)
			} else {
				reason = strings.TrimSpace(body.Note)
				if reason == "" {
					return fmt.Errorf("%w: say why it's being sent back (note)", errVoucherInvalid)
				}
				_, err = tx.Exec(ctx, `UPDATE journal_vouchers SET status = 'draft', rejection_note = $1, submitted_by = NULL, submitted_at = NULL, updated_at = now() WHERE id::text = $2`, reason, id)
			}
		case "post":
			if needs {
				if err := state("approved"); err != nil {
					return fmt.Errorf("%w (approval is on: submit and approve it first)", err)
				}
			} else if err := state("draft", "approved"); err != nil {
				return err
			}
			err = postVoucher(ctx, tx, cur, claims.UserID)
		case "reverse":
			if err := state("posted"); err != nil {
				return err
			}
			if settings.JVApprovalRequired {
				if err := needApprover(); err != nil {
					return err
				}
			}
			reason = strings.TrimSpace(body.Reason)
			if reason == "" {
				return fmt.Errorf("%w: a reason is required", errVoucherInvalid)
			}
			date := body.Date
			if date == "" {
				date = time.Now().Format("2006-01-02")
			} else if _, perr := time.Parse("2006-01-02", date); perr != nil {
				return fmt.Errorf("%w: date must be YYYY-MM-DD", errVoucherInvalid)
			}
			mirror := make([]JournalLine, 0, len(cur.Lines))
			for _, l := range cur.Lines {
				jl := JournalLine{AccountCode: l.AccountCode}
				fmt.Sscan(l.Credit, &jl.Debit) // swapped
				fmt.Sscan(l.Debit, &jl.Credit)
				if l.PartyType != nil && l.PartyID != nil {
					jl.PartyType, jl.PartyID = *l.PartyType, *l.PartyID
				}
				mirror = append(mirror, jl)
			}
			branch := ""
			if cur.BranchID != nil {
				branch = *cur.BranchID
			}
			number, perr := PostJournalEntryOnDate(ctx, tx, branch, "manual_reversal", cur.VoucherID,
				fmt.Sprintf("Reversal of %s (%s): %s", cur.VoucherNumber, cur.Description, reason), claims.UserID, date, mirror)
			if perr != nil {
				return perr
			}
			_, err = tx.Exec(ctx, `
				UPDATE journal_vouchers SET status = 'reversed', reversed_by = $1, reversed_at = now(), reversal_reason = $2, updated_at = now(),
				       reversal_entry_id = (SELECT id FROM journal_entries WHERE entry_number = $3)
				WHERE id::text = $4`, claims.UserID, reason, number, id)
		default:
			return fmt.Errorf("%w: unknown action %q", errVoucherInvalid, action)
		}
		if err != nil {
			return err
		}
		if v, err = loadVoucher(ctx, tx, id, false); err != nil {
			return err
		}
		return voucherAudit(ctx, tx, v, claims.UserID, action, reason)
	})
	if err != nil {
		writeVoucherError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, v)
}

// GetVoucher: GET /accounting/vouchers/{id}
func (h *Handler) GetVoucher(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var v voucher
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		v, err = loadVoucher(ctx, tx, chi.URLParam(r, "id"), false)
		return err
	})
	if err != nil {
		writeVoucherError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, v)
}

// ListVouchers: GET /accounting/vouchers?status=&q=&page=&limit= — newest
// first, paged (default 25). q matches the number or description.
func (h *Handler) ListVouchers(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	q := r.URL.Query()
	status := q.Get("status")
	if status == "all" {
		status = ""
	}
	p := httpx.ParseListParams(r, map[string]string{"created": "v.created_at", "date": "v.entry_date"}, "created")
	if q.Get("dir") == "" {
		p.OrderBy = "v.created_at DESC"
	}
	list := []voucher{}
	total := 0
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		where := ` WHERE ($1 = '' OR v.status = $1) AND ($2 = '' OR v.voucher_number ILIKE '%' || $2 || '%' OR v.description ILIKE '%' || $2 || '%')`
		args := []any{status, strings.TrimSpace(q.Get("q"))}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM journal_vouchers v`+where, args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, voucherSelect+where+` ORDER BY `+p.OrderBy+`, v.id LIMIT $3 OFFSET $4`, append(args, p.Limit, p.Offset)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			v, err := scanVoucher(rows)
			if err != nil {
				return err
			}
			list = append(list, v)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list vouchers")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"vouchers": list, "total": total, "page": p.Page, "limit": p.Limit})
}

// ---------------------------------------------------------------------
// The ledger, browsable: every posted entry (manual and automatic), with
// paging, search, CSV export, and each entry's source document.
// ---------------------------------------------------------------------

type ledgerEntry struct {
	EntryID     string  `json:"entry_id"`
	EntryNumber string  `json:"entry_number"`
	EntryDate   string  `json:"entry_date"`
	SourceType  string  `json:"source_type"`
	SourceID    *string `json:"source_id"`
	Description string  `json:"description"`
	BranchName  *string `json:"branch_name"`
	Amount      string  `json:"amount"`
	CreatedBy   *string `json:"created_by"`
	Lines       []struct {
		AccountCode string  `json:"account_code"`
		AccountName string  `json:"account_name"`
		Debit       string  `json:"debit"`
		Credit      string  `json:"credit"`
		PartyType   *string `json:"party_type"`
		PartyID     *string `json:"party_id"`
		PartyName   *string `json:"party_name"`
	} `json:"lines,omitempty"`
}

const ledgerFrom = `
	FROM journal_entries je
	LEFT JOIN branches b ON b.id = je.branch_id
	LEFT JOIN users u ON u.id = je.created_by`

const ledgerFilter = `
	WHERE je.status = 'posted'
	  AND ($1 = '' OR je.source_type = $1)
	  AND ($2 = '' OR je.entry_number ILIKE '%' || $2 || '%' OR je.description ILIKE '%' || $2 || '%')
	  AND ($3 = '' OR je.entry_date >= $3::date) AND ($4 = '' OR je.entry_date <= $4::date)
	  AND ($5 = '' OR je.branch_id::text = $5)
	  AND ($6 = '' OR EXISTS (SELECT 1 FROM journal_lines pl WHERE pl.journal_entry_id = je.id AND pl.party_id::text = $6))`

// ListJournalEntries: GET /accounting/journal-entries?source_type=&q=&from=&to=&branch_id=&page=&limit=&format=csv
// Paged (default 25), newest first. format=csv returns every matching
// entry's lines (up to 50,000) as a spreadsheet.
func (h *Handler) ListJournalEntries(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	q := r.URL.Query()
	for _, d := range []string{q.Get("from"), q.Get("to")} {
		if d != "" {
			if _, err := time.Parse("2006-01-02", d); err != nil {
				httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "from/to must be YYYY-MM-DD")
				return
			}
		}
	}
	args := []any{q.Get("source_type"), strings.TrimSpace(q.Get("q")), q.Get("from"), q.Get("to"), q.Get("branch_id"), q.Get("party_id")}

	if q.Get("format") == "csv" {
		var buf strings.Builder
		cw := csv.NewWriter(&buf)
		_ = cw.Write([]string{"entry_number", "entry_date", "source_type", "description", "branch", "account_code", "account_name", "debit", "credit", "party_type", "party_id", "party_name"})
		err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `
				SELECT je.entry_number, je.entry_date::text, je.source_type, COALESCE(je.description, ''), COALESCE(b.name, ''),
				       a.code, a.name, l.debit::text, l.credit::text, COALESCE(l.party_type, ''), COALESCE(l.party_id::text, ''),
				       COALESCE(`+partyNameSQL+`, '')
				`+ledgerFrom+`
				JOIN journal_lines l ON l.journal_entry_id = je.id
				JOIN chart_of_accounts a ON a.id = l.account_id`+ledgerFilter+`
				ORDER BY je.entry_date, je.created_at, je.id, l.debit DESC LIMIT 50000`, args...)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				rec := make([]string, 12)
				ptrs := make([]any, 12)
				for i := range rec {
					ptrs[i] = &rec[i]
				}
				if err := rows.Scan(ptrs...); err != nil {
					return err
				}
				_ = cw.Write(rec)
			}
			return rows.Err()
		})
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not export the journal")
			return
		}
		cw.Flush()
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="journal-%s.csv"`, time.Now().Format("20060102")))
		httpx.Binary(w, http.StatusOK, "text/csv; charset=utf-8", []byte(buf.String()))
		return
	}

	p := httpx.ParseListParams(r, map[string]string{"date": "je.entry_date", "created": "je.created_at"}, "date")
	if q.Get("dir") == "" {
		p.OrderBy = "je.entry_date DESC, je.created_at DESC"
	}
	list := []ledgerEntry{}
	total := 0
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*)`+ledgerFrom+ledgerFilter, args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT je.id, je.entry_number, je.entry_date::text, je.source_type, je.source_id::text, COALESCE(je.description, ''), b.name,
			       COALESCE((SELECT sum(debit) FROM journal_lines jl WHERE jl.journal_entry_id = je.id), 0)::text, u.name
			`+ledgerFrom+ledgerFilter+` ORDER BY `+p.OrderBy+`, je.id LIMIT $7 OFFSET $8`, append(args, p.Limit, p.Offset)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e ledgerEntry
			if err := rows.Scan(&e.EntryID, &e.EntryNumber, &e.EntryDate, &e.SourceType, &e.SourceID, &e.Description, &e.BranchName, &e.Amount, &e.CreatedBy); err != nil {
				return err
			}
			list = append(list, e)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list journal entries")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"entries": list, "total": total, "page": p.Page, "limit": p.Limit})
}

// GetJournalEntry: GET /accounting/journal-entries/{id} — one posted entry
// with its lines. source_type/source_id say which document made it (for a
// manual entry, its voucher), so the UI can link there; auto entries are
// read-only and are undone from their document (void, return ...).
func (h *Handler) GetJournalEntry(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var e ledgerEntry
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			SELECT je.id, je.entry_number, je.entry_date::text, je.source_type, je.source_id::text, COALESCE(je.description, ''), b.name,
			       COALESCE((SELECT sum(debit) FROM journal_lines jl WHERE jl.journal_entry_id = je.id), 0)::text, u.name
			FROM journal_entries je LEFT JOIN branches b ON b.id = je.branch_id LEFT JOIN users u ON u.id = je.created_by
			WHERE je.id::text = $1 OR je.entry_number = $1`, chi.URLParam(r, "id")).
			Scan(&e.EntryID, &e.EntryNumber, &e.EntryDate, &e.SourceType, &e.SourceID, &e.Description, &e.BranchName, &e.Amount, &e.CreatedBy); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT a.code, a.name, l.debit::text, l.credit::text, l.party_type, l.party_id::text, `+partyNameSQL+`
			FROM journal_lines l JOIN chart_of_accounts a ON a.id = l.account_id
			WHERE l.journal_entry_id::text = $1 ORDER BY l.debit DESC, a.code`, e.EntryID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var l struct {
				AccountCode string  `json:"account_code"`
				AccountName string  `json:"account_name"`
				Debit       string  `json:"debit"`
				Credit      string  `json:"credit"`
				PartyType   *string `json:"party_type"`
				PartyID     *string `json:"party_id"`
				PartyName   *string `json:"party_name"`
			}
			if err := rows.Scan(&l.AccountCode, &l.AccountName, &l.Debit, &l.Credit, &l.PartyType, &l.PartyID, &l.PartyName); err != nil {
				return err
			}
			e.Lines = append(e.Lines, l)
		}
		return rows.Err()
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "journal entry not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load the journal entry")
		return
	}
	httpx.JSON(w, http.StatusOK, e)
}
