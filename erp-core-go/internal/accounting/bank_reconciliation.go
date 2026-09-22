// Phase 4, sub-area 3: Bank Reconciliation (phased_roadmap.md;
// pos_frd_complete.md §8) — see migrations/015_bank_reconciliation.sql's
// header comment for scope (CSV only, one shared Bank ledger account per
// merchant). Auto-match runs once, immediately after import, against
// journal_lines already posted to the AccountBank account by every other
// part of this codebase that touches it (sales/purchase-bill/receivable
// payments via bank_transfer or cheque) — nothing here creates or alters
// a journal entry, this only links an existing one to an imported
// statement row.
package accounting

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

// ---------------------------------------------------------------------
// POST /accounting/bank-statement/import — gated by
// bank_reconciliation.manage.
// ---------------------------------------------------------------------

type importStatementRequest struct {
	Filename string `json:"filename"`
	CSV      string `json:"csv"` // raw file content: header row "date,description,reference,amount" then one row per transaction
}

type statementLineResponse struct {
	LineID               string  `json:"line_id"`
	TxnDate              string  `json:"txn_date"`
	Description          string  `json:"description"`
	Reference            string  `json:"reference"`
	Amount               string  `json:"amount"`
	Status               string  `json:"status"`
	MatchedJournalLineID *string `json:"matched_journal_line_id"`
}

type importResponse struct {
	ImportID     string                  `json:"import_id"`
	Filename     string                  `json:"filename"`
	LineCount    int                     `json:"line_count"`
	MatchedCount int                     `json:"matched_count"`
	Lines        []statementLineResponse `json:"lines"`
}

var (
	errEmptyCSV      = errors.New("csv has no data rows")
	errInvalidCSVRow = errors.New("could not parse a csv row — expected date,description,reference,amount")
)

// AccountBank's the account bank reconciliation is always scoped to (see
// this file's package doc comment) — journal_lines rows for
// bank_transfer/cheque payments already land here via
// AccountCodeForPaymentMethod.
func (h *Handler) ImportBankStatement(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req importStatementRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	rows, err := parseStatementCSV(req.CSV)
	switch {
	case errors.Is(err, errEmptyCSV):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "csv has no data rows")
		return
	case errors.Is(err, errInvalidCSVRow):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse a csv row — expected columns: date,description,reference,amount")
		return
	case err != nil:
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse csv")
		return
	}

	var resp importResponse
	err = h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var importID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO bank_statement_imports (id, merchant_id, filename, line_count, imported_by)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3)
			RETURNING id`, req.Filename, len(rows), claims.UserID,
		).Scan(&importID); err != nil {
			return err
		}

		lineIDs := make([]string, len(rows))
		for i, row := range rows {
			if err := tx.QueryRow(ctx, `
				INSERT INTO bank_statement_lines (id, merchant_id, import_id, txn_date, description, reference, amount)
				VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, $5)
				RETURNING id`, importID, row.date, row.description, row.reference, row.amount,
			).Scan(&lineIDs[i]); err != nil {
				return err
			}
		}

		matched, err := autoMatchLines(ctx, tx, lineIDs, claims.UserID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE bank_statement_imports SET matched_count = $1 WHERE id = $2`, matched, importID); err != nil {
			return err
		}

		lines, err := loadStatementLines(ctx, tx, importID, "")
		if err != nil {
			return err
		}
		resp = importResponse{ImportID: importID, Filename: req.Filename, LineCount: len(rows), MatchedCount: matched, Lines: lines}
		return nil
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not import bank statement")
		return
	}
	httpx.JSON(w, http.StatusCreated, resp)
}

type csvRow struct {
	date        string
	description string
	reference   string
	amount      float64
}

// parseStatementCSV expects a header row (ignored — this parser trusts
// column ORDER, not header names, to keep it simple: date, description,
// reference, amount) followed by one data row per transaction. Amount is
// signed: positive = deposit, negative = withdrawal (see migrations/
// 015_bank_reconciliation.sql's column comment).
func parseStatementCSV(raw string) ([]csvRow, error) {
	reader := csv.NewReader(strings.NewReader(raw))
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) < 2 {
		return nil, errEmptyCSV
	}
	var rows []csvRow
	for _, rec := range records[1:] { // skip header
		if len(rec) < 4 {
			return nil, errInvalidCSVRow
		}
		date := strings.TrimSpace(rec[0])
		if _, err := time.Parse("2006-01-02", date); err != nil {
			return nil, errInvalidCSVRow
		}
		amount, err := strconv.ParseFloat(strings.TrimSpace(rec[3]), 64)
		if err != nil {
			return nil, errInvalidCSVRow
		}
		rows = append(rows, csvRow{date: date, description: strings.TrimSpace(rec[1]), reference: strings.TrimSpace(rec[2]), amount: amount})
	}
	if len(rows) == 0 {
		return nil, errEmptyCSV
	}
	return rows, nil
}

// autoMatchLines tries each newly-imported line against unclaimed Bank
// journal_lines within a 3-day window of its txn_date, matching on
// amount (within a paisa — the same epsilon tolerance
// RecordBillPayment/RecordReceivablePayment already use for overpayment
// checks) only when there is exactly one candidate — an ambiguous
// multiple-candidate case is deliberately left unmatched for manual
// review rather than guessing, since a wrong auto-match would silently
// corrupt the reconciliation report.
//
// The $3::numeric casts on both uses of the amount parameter are load-
// bearing, not decoration: found live during this sub-area's own
// verification pass that without them, Postgres's extended-protocol type
// inference for a placeholder used in both a `$3 > 0` comparison and
// `ABS($3)` — combined with the rest of this query's other clauses —
// silently resolved to the wrong parameter type, so pgx's float64 bind
// value never matched a single row, with no error raised on either side.
// The query is provably correct with each clause tested in isolation;
// only the combined, ambiguously-typed form fails. Casting removes the
// ambiguity outright rather than working around a specific inferred type.
func autoMatchLines(ctx context.Context, tx pgx.Tx, lineIDs []string, matchedBy string) (int, error) {
	matched := 0
	for _, lineID := range lineIDs {
		var txnDate string
		var amount float64
		if err := tx.QueryRow(ctx, `SELECT txn_date::text, amount FROM bank_statement_lines WHERE id = $1`, lineID).
			Scan(&txnDate, &amount); err != nil {
			return matched, err
		}

		rows, err := tx.Query(ctx, `
			SELECT jl.id
			FROM journal_lines jl
			JOIN journal_entries je ON je.id = jl.journal_entry_id
			JOIN chart_of_accounts coa ON coa.id = jl.account_id
			LEFT JOIN bank_statement_lines bsl ON bsl.matched_journal_line_id = jl.id
			WHERE coa.code = $1 AND je.status = 'posted' AND bsl.id IS NULL
			  AND je.entry_date BETWEEN $2::date - INTERVAL '3 days' AND $2::date + INTERVAL '3 days'
			  AND ABS((CASE WHEN $3::numeric > 0 THEN jl.debit ELSE jl.credit END) - ABS($3::numeric)) < 0.01`,
			AccountBank, txnDate, amount)
		if err != nil {
			return matched, err
		}
		var candidates []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return matched, err
			}
			candidates = append(candidates, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return matched, err
		}

		if len(candidates) != 1 {
			continue // none, or ambiguous — leave for manual matching
		}
		if _, err := tx.Exec(ctx, `
			UPDATE bank_statement_lines SET status = 'matched', matched_journal_line_id = $1, matched_at = now(), matched_by = $2
			WHERE id = $3`, candidates[0], matchedBy, lineID); err != nil {
			return matched, err
		}
		matched++
	}
	return matched, nil
}

func loadStatementLines(ctx context.Context, tx pgx.Tx, importID, status string) ([]statementLineResponse, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, txn_date::text, COALESCE(description,''), COALESCE(reference,''), amount::text, status, matched_journal_line_id::text
		FROM bank_statement_lines
		WHERE ($1 = '' OR import_id = $1::uuid) AND ($2 = '' OR status = $2)
		ORDER BY txn_date, created_at`, importID, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	lines := []statementLineResponse{}
	for rows.Next() {
		var l statementLineResponse
		if err := rows.Scan(&l.LineID, &l.TxnDate, &l.Description, &l.Reference, &l.Amount, &l.Status, &l.MatchedJournalLineID); err != nil {
			return nil, err
		}
		lines = append(lines, l)
	}
	return lines, rows.Err()
}

// ---------------------------------------------------------------------
// GET /accounting/bank-statement/imports — open read, matching every
// other accounting report in this package.
// ---------------------------------------------------------------------

type importSummary struct {
	ImportID     string `json:"import_id"`
	Filename     string `json:"filename"`
	LineCount    int    `json:"line_count"`
	MatchedCount int    `json:"matched_count"`
	ImportedAt   string `json:"imported_at"`
}

func (h *Handler) ListBankStatementImports(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	imports := []importSummary{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, COALESCE(filename,''), line_count, matched_count, imported_at::text
			FROM bank_statement_imports ORDER BY imported_at DESC`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s importSummary
			if err := rows.Scan(&s.ImportID, &s.Filename, &s.LineCount, &s.MatchedCount, &s.ImportedAt); err != nil {
				return err
			}
			imports = append(imports, s)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list bank statement imports")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"imports": imports})
}

// ---------------------------------------------------------------------
// GET /accounting/bank-statement/lines?import_id=&status=
// ---------------------------------------------------------------------

func (h *Handler) ListBankStatementLines(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	importID := r.URL.Query().Get("import_id")
	status := r.URL.Query().Get("status")

	var lines []statementLineResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		lines, err = loadStatementLines(ctx, tx, importID, status)
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list bank statement lines")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"lines": lines})
}

// ---------------------------------------------------------------------
// GET /accounting/bank-statement/lines/{id}/candidates — unclaimed Bank
// journal lines within a week of this statement line's date, for a
// manual-matching picker. Not scoped by exact amount (unlike
// autoMatchLines) — a human reviewing candidates benefits from seeing
// near-misses (a bank fee shaved off the expected amount, say), not just
// exact matches.
// ---------------------------------------------------------------------

type journalLineCandidate struct {
	JournalLineID string `json:"journal_line_id"`
	EntryDate     string `json:"entry_date"`
	Description   string `json:"description"`
	SourceType    string `json:"source_type"`
	Debit         string `json:"debit"`
	Credit        string `json:"credit"`
}

func (h *Handler) BankStatementLineCandidates(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	lineID := chi.URLParam(r, "id")

	candidates := []journalLineCandidate{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var txnDate string
		if err := tx.QueryRow(ctx, `SELECT txn_date::text FROM bank_statement_lines WHERE id = $1`, lineID).Scan(&txnDate); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT jl.id, je.entry_date::text, COALESCE(je.description,''), je.source_type, jl.debit::text, jl.credit::text
			FROM journal_lines jl
			JOIN journal_entries je ON je.id = jl.journal_entry_id
			JOIN chart_of_accounts coa ON coa.id = jl.account_id
			LEFT JOIN bank_statement_lines bsl ON bsl.matched_journal_line_id = jl.id
			WHERE coa.code = $1 AND je.status = 'posted' AND bsl.id IS NULL
			  AND je.entry_date BETWEEN $2::date - INTERVAL '7 days' AND $2::date + INTERVAL '7 days'
			ORDER BY je.entry_date`, AccountBank, txnDate)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c journalLineCandidate
			if err := rows.Scan(&c.JournalLineID, &c.EntryDate, &c.Description, &c.SourceType, &c.Debit, &c.Credit); err != nil {
				return err
			}
			candidates = append(candidates, c)
		}
		return rows.Err()
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "statement line not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load match candidates")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"candidates": candidates})
}

// ---------------------------------------------------------------------
// POST /accounting/bank-statement/lines/{id}/match — gated by
// bank_reconciliation.manage. {journal_line_id}
// ---------------------------------------------------------------------

type matchLineRequest struct {
	JournalLineID string `json:"journal_line_id"`
}

var errAlreadyMatched = errors.New("this journal line is already matched to a different statement line")

func (h *Handler) MatchBankStatementLine(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	lineID := chi.URLParam(r, "id")
	var req matchLineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.JournalLineID == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "journal_line_id is required")
		return
	}

	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var alreadyClaimed bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM bank_statement_lines WHERE matched_journal_line_id = $1)`, req.JournalLineID).
			Scan(&alreadyClaimed); err != nil {
			return err
		}
		if alreadyClaimed {
			return errAlreadyMatched
		}
		tag, err := tx.Exec(ctx, `
			UPDATE bank_statement_lines SET status = 'matched', matched_journal_line_id = $1, matched_at = now(), matched_by = $2
			WHERE id = $3`, req.JournalLineID, claims.UserID, lineID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return nil
	})

	switch {
	case errors.Is(err, errAlreadyMatched):
		httpx.Error(w, http.StatusConflict, "ALREADY_MATCHED", "this journal line is already matched to a different statement line")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "statement line not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not match statement line")
	default:
		httpx.JSON(w, http.StatusOK, map[string]any{"status": "matched"})
	}
}

// ---------------------------------------------------------------------
// POST /accounting/bank-statement/lines/{id}/unmatch — gated by
// bank_reconciliation.manage.
// ---------------------------------------------------------------------

func (h *Handler) UnmatchBankStatementLine(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	lineID := chi.URLParam(r, "id")

	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE bank_statement_lines SET status = 'unmatched', matched_journal_line_id = NULL, matched_at = NULL, matched_by = NULL
			WHERE id = $1`, lineID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "statement line not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not unmatch statement line")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "unmatched"})
}

// ---------------------------------------------------------------------
// GET /accounting/bank-reconciliation?start=&end= — the FRD's
// "reconciliation reports": every unmatched statement line and every
// unmatched Bank-account journal line in range, plus matched/unmatched
// totals on both sides. A merchant is fully reconciled for a period when
// both unmatched lists are empty.
// ---------------------------------------------------------------------

type unmatchedLedgerLine struct {
	JournalLineID string `json:"journal_line_id"`
	EntryDate     string `json:"entry_date"`
	Description   string `json:"description"`
	SourceType    string `json:"source_type"`
	Debit         string `json:"debit"`
	Credit        string `json:"credit"`
}

type reconciliationReport struct {
	Start                   string                  `json:"start"`
	End                     string                  `json:"end"`
	MatchedTotal            string                  `json:"matched_total"`
	UnmatchedStatementLines []statementLineResponse `json:"unmatched_statement_lines"`
	UnmatchedLedgerLines    []unmatchedLedgerLine   `json:"unmatched_ledger_lines"`
}

func (h *Handler) BankReconciliationReport(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	start := r.URL.Query().Get("start")
	end := r.URL.Query().Get("end")
	if _, err := time.Parse("2006-01-02", start); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "start must be YYYY-MM-DD")
		return
	}
	if _, err := time.Parse("2006-01-02", end); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "end must be YYYY-MM-DD")
		return
	}

	resp := reconciliationReport{Start: start, End: end, UnmatchedStatementLines: []statementLineResponse{}, UnmatchedLedgerLines: []unmatchedLedgerLine{}}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var matchedTotal float64
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(ABS(amount)),0) FROM bank_statement_lines
			WHERE status = 'matched' AND txn_date BETWEEN $1::date AND $2::date`, start, end,
		).Scan(&matchedTotal); err != nil {
			return err
		}
		resp.MatchedTotal = formatMoney(matchedTotal)

		unmatchedStmt, err := loadStatementLines(ctx, tx, "", "unmatched")
		if err != nil {
			return err
		}
		for _, l := range unmatchedStmt {
			if l.TxnDate >= start && l.TxnDate <= end {
				resp.UnmatchedStatementLines = append(resp.UnmatchedStatementLines, l)
			}
		}

		rows, err := tx.Query(ctx, `
			SELECT jl.id, je.entry_date::text, COALESCE(je.description,''), je.source_type, jl.debit::text, jl.credit::text
			FROM journal_lines jl
			JOIN journal_entries je ON je.id = jl.journal_entry_id
			JOIN chart_of_accounts coa ON coa.id = jl.account_id
			LEFT JOIN bank_statement_lines bsl ON bsl.matched_journal_line_id = jl.id
			WHERE coa.code = $1 AND je.status = 'posted' AND bsl.id IS NULL
			  AND je.entry_date BETWEEN $2::date AND $3::date
			ORDER BY je.entry_date`, AccountBank, start, end)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var l unmatchedLedgerLine
			if err := rows.Scan(&l.JournalLineID, &l.EntryDate, &l.Description, &l.SourceType, &l.Debit, &l.Credit); err != nil {
				return err
			}
			resp.UnmatchedLedgerLines = append(resp.UnmatchedLedgerLines, l)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not build reconciliation report")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}
