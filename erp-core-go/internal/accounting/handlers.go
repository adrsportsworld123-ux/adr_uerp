package accounting

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

// ---------------------------------------------------------------------
// Chart of accounts
// ---------------------------------------------------------------------

type accountResponse struct {
	AccountID string `json:"account_id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	Type      string `json:"account_type"`
	IsSystem  bool   `json:"is_system"`
	Status    string `json:"status"`
}

// ListAccounts: GET /accounting/accounts
func (h *Handler) ListAccounts(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	accounts := []accountResponse{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, code, name, account_type, is_system, status FROM chart_of_accounts ORDER BY code`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a accountResponse
			if err := rows.Scan(&a.AccountID, &a.Code, &a.Name, &a.Type, &a.IsSystem, &a.Status); err != nil {
				return err
			}
			accounts = append(accounts, a)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list accounts")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"accounts": accounts})
}

type createAccountRequest struct {
	Code string `json:"code"`
	Name string `json:"name"`
	Type string `json:"account_type"`
}

// CreateAccount: POST /accounting/accounts — a merchant adding a custom
// account beyond the system defaults (FRD §8's "Add accounts, sub-accounts").
// Sub-account hierarchy (parent_id) isn't exposed here yet — flat accounts
// only, matching how much of the hierarchy the FRD's Phase 2 scope
// actually needs.
func (h *Handler) CreateAccount(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req createAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	switch req.Type {
	case "asset", "liability", "equity", "income", "expense":
	default:
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "account_type must be one of asset, liability, equity, income, expense")
		return
	}
	if req.Code == "" || req.Name == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "code and name are required")
		return
	}

	var resp accountResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO chart_of_accounts (id, merchant_id, code, name, account_type, is_system)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, false)
			RETURNING id, code, name, account_type, is_system, status`,
			req.Code, req.Name, req.Type,
		).Scan(&resp.AccountID, &resp.Code, &resp.Name, &resp.Type, &resp.IsSystem, &resp.Status)
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create account — code may already be in use")
		return
	}
	httpx.JSON(w, http.StatusCreated, resp)
}

// ---------------------------------------------------------------------
// Manual journal entry
// ---------------------------------------------------------------------

type manualJournalLine struct {
	AccountCode string  `json:"account_code"`
	Debit       float64 `json:"debit"`
	Credit      float64 `json:"credit"`
}

type manualJournalRequest struct {
	BranchID    string              `json:"branch_id"`
	Description string              `json:"description"`
	Lines       []manualJournalLine `json:"lines"`
}

// CreateManualJournalEntry: POST /accounting/journal-entries — posts
// immediately (no approval workflow — see this migration's header comment
// for why that's out of scope for now, same simplification as skipping
// Purchase's formal PO step).
func (h *Handler) CreateManualJournalEntry(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req manualJournalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.Description == "" || len(req.Lines) < 2 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "description and at least two lines are required")
		return
	}

	lines := make([]JournalLine, 0, len(req.Lines))
	for _, l := range req.Lines {
		if l.AccountCode == "" || (l.Debit == 0) == (l.Credit == 0) {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "each line needs an account_code and exactly one of debit/credit set")
			return
		}
		lines = append(lines, JournalLine{AccountCode: l.AccountCode, Debit: l.Debit, Credit: l.Credit})
	}

	var entryNumber string
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		num, err := PostJournalEntry(ctx, tx, req.BranchID, "manual", "", req.Description, claims.UserID, lines)
		entryNumber = num
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]string{"status": "posted", "entry_number": entryNumber})
}

// ---------------------------------------------------------------------
// Party ledger
// ---------------------------------------------------------------------

type ledgerLine struct {
	EntryDate   string `json:"entry_date"`
	Description string `json:"description"`
	SourceType  string `json:"source_type"`
	Debit       string `json:"debit"`
	Credit      string `json:"credit"`
}

// PartyLedger: GET /accounting/party-ledger?party_type=customer|supplier&party_id=
// Receivables (customer) or payables (supplier) tracking per FRD §8.
func (h *Handler) PartyLedger(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	partyType := r.URL.Query().Get("party_type")
	partyID := r.URL.Query().Get("party_id")
	if (partyType != "customer" && partyType != "supplier") || partyID == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "party_type (customer|supplier) and party_id are required")
		return
	}

	lines := []ledgerLine{}
	var balance float64
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT je.entry_date::text, COALESCE(je.description,''), je.source_type, jl.debit::text, jl.credit::text, jl.debit, jl.credit
			FROM journal_lines jl
			JOIN journal_entries je ON je.id = jl.journal_entry_id
			WHERE jl.party_type = $1 AND jl.party_id = $2
			ORDER BY je.entry_date, je.created_at`, partyType, partyID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var l ledgerLine
			var debit, credit float64
			if err := rows.Scan(&l.EntryDate, &l.Description, &l.SourceType, &l.Debit, &l.Credit, &debit, &credit); err != nil {
				return err
			}
			balance += debit - credit
			lines = append(lines, l)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load party ledger")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"party_type": partyType, "party_id": partyID,
		"lines": lines, "balance": formatMoney(balance),
	})
}

// ---------------------------------------------------------------------
// Day Book / Cash Book
// ---------------------------------------------------------------------

func dateParam(r *http.Request) (string, bool) {
	d := r.URL.Query().Get("date")
	if d == "" {
		d = time.Now().Format("2006-01-02")
	}
	if _, err := time.Parse("2006-01-02", d); err != nil {
		return "", false
	}
	return d, true
}

type dayBookEntry struct {
	EntryNumber string             `json:"entry_number"`
	SourceType  string             `json:"source_type"`
	Description string             `json:"description"`
	Lines       []dayBookEntryLine `json:"lines"`
}

type dayBookEntryLine struct {
	AccountCode string `json:"account_code"`
	AccountName string `json:"account_name"`
	Debit       string `json:"debit"`
	Credit      string `json:"credit"`
}

// DayBook: GET /accounting/day-book?branch_id=&date=YYYY-MM-DD — every
// journal entry posted for a branch on a date, per FRD §8/Reports.
func (h *Handler) DayBook(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	branchID := r.URL.Query().Get("branch_id")
	if branchID == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "branch_id is required")
		return
	}
	date, ok := dateParam(r)
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "date must be YYYY-MM-DD")
		return
	}

	entries := []dayBookEntry{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		entryRows, err := tx.Query(ctx, `
			SELECT id, entry_number, source_type, COALESCE(description,'')
			FROM journal_entries WHERE branch_id = $1 AND entry_date = $2::date AND status = 'posted'
			ORDER BY created_at`, branchID, date)
		if err != nil {
			return err
		}
		type entryRow struct{ id, number, sourceType, description string }
		var entryRowsList []entryRow
		for entryRows.Next() {
			var e entryRow
			if err := entryRows.Scan(&e.id, &e.number, &e.sourceType, &e.description); err != nil {
				entryRows.Close()
				return err
			}
			entryRowsList = append(entryRowsList, e)
		}
		entryRows.Close()
		if err := entryRows.Err(); err != nil {
			return err
		}

		for _, e := range entryRowsList {
			lineRows, err := tx.Query(ctx, `
				SELECT coa.code, coa.name, jl.debit::text, jl.credit::text
				FROM journal_lines jl JOIN chart_of_accounts coa ON coa.id = jl.account_id
				WHERE jl.journal_entry_id = $1 ORDER BY jl.created_at`, e.id)
			if err != nil {
				return err
			}
			var lines []dayBookEntryLine
			for lineRows.Next() {
				var l dayBookEntryLine
				if err := lineRows.Scan(&l.AccountCode, &l.AccountName, &l.Debit, &l.Credit); err != nil {
					lineRows.Close()
					return err
				}
				lines = append(lines, l)
			}
			lineRows.Close()
			if err := lineRows.Err(); err != nil {
				return err
			}
			entries = append(entries, dayBookEntry{EntryNumber: e.number, SourceType: e.sourceType, Description: e.description, Lines: lines})
		}
		return nil
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not build day book")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"branch_id": branchID, "date": date, "entries": entries})
}

// CashBook: GET /accounting/cash-book?branch_id=&date=YYYY-MM-DD — journal
// lines against the Cash/Bank/clearing accounts only, for a branch/date.
func (h *Handler) CashBook(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	branchID := r.URL.Query().Get("branch_id")
	if branchID == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "branch_id is required")
		return
	}
	date, ok := dateParam(r)
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "date must be YYYY-MM-DD")
		return
	}

	lines := []ledgerLine{}
	var netMovement float64
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT je.entry_date::text, COALESCE(je.description,''), je.source_type, jl.debit::text, jl.credit::text, jl.debit, jl.credit
			FROM journal_lines jl
			JOIN journal_entries je ON je.id = jl.journal_entry_id
			JOIN chart_of_accounts coa ON coa.id = jl.account_id
			WHERE je.branch_id = $1 AND je.entry_date = $2::date AND je.status = 'posted'
			  AND coa.code IN ($3, $4, $5, $6)
			ORDER BY je.created_at`, branchID, date, AccountCash, AccountBank, AccountCardClearing, AccountUPIClearing)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var l ledgerLine
			var debit, credit float64
			if err := rows.Scan(&l.EntryDate, &l.Description, &l.SourceType, &l.Debit, &l.Credit, &debit, &credit); err != nil {
				return err
			}
			netMovement += debit - credit
			lines = append(lines, l)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not build cash book")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"branch_id": branchID, "date": date, "lines": lines, "net_movement": formatMoney(netMovement),
	})
}

func formatMoney(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64)
}
