// Phase 4, sub-area 4: Reconciliation & Audit — Payment Gateway
// reconciliation (phased_roadmap.md; pos_frd_complete.md §16). See
// migrations/017_payment_gateway_reconciliation.sql's header comment for
// scope (reconciliation type #2 of 4 — #1 Cash and #4 Inter-branch
// Transfer already closed; #3 Inventory is this same sub-area's last
// item, not yet built) and for exactly why, unlike bank reconciliation,
// matching here posts a real journal entry (Dr Bank + Dr Payment Gateway
// Fees / Cr the clearing account) rather than only linking one.
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
// POST /accounting/payment-gateway/settlement/import — gated by
// payment_gateway_reconciliation.manage.
// ---------------------------------------------------------------------

type importSettlementRequest struct {
	Gateway  string `json:"gateway"`
	Filename string `json:"filename"`
	CSV      string `json:"csv"` // header row "settlement_date,reference,amount" then one row per settled transaction
}

type settlementLineResponse struct {
	LineID           string  `json:"line_id"`
	SettlementDate   string  `json:"settlement_date"`
	Reference        string  `json:"reference"`
	Amount           string  `json:"amount"`
	Status           string  `json:"status"`
	MatchedPaymentID *string `json:"matched_payment_id"`
}

type settlementImportResponse struct {
	ImportID     string                   `json:"import_id"`
	Gateway      string                   `json:"gateway"`
	Filename     string                   `json:"filename"`
	LineCount    int                      `json:"line_count"`
	MatchedCount int                      `json:"matched_count"`
	Lines        []settlementLineResponse `json:"lines"`
}

var (
	errEmptySettlementCSV      = errors.New("csv has no data rows")
	errInvalidSettlementCSVRow = errors.New("could not parse a csv row — expected settlement_date,reference,amount")
)

func (h *Handler) ImportPaymentGatewaySettlement(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req importSettlementRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if strings.TrimSpace(req.Gateway) == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "gateway is required")
		return
	}
	rows, err := parseSettlementCSV(req.CSV)
	switch {
	case errors.Is(err, errEmptySettlementCSV):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "csv has no data rows")
		return
	case errors.Is(err, errInvalidSettlementCSVRow):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse a csv row — expected columns: settlement_date,reference,amount")
		return
	case err != nil:
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse csv")
		return
	}

	var resp settlementImportResponse
	err = h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var importID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO payment_gateway_settlement_imports (id, merchant_id, gateway, filename, line_count, imported_by)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4)
			RETURNING id`, req.Gateway, req.Filename, len(rows), claims.UserID,
		).Scan(&importID); err != nil {
			return err
		}

		lineIDs := make([]string, len(rows))
		for i, row := range rows {
			if err := tx.QueryRow(ctx, `
				INSERT INTO payment_gateway_settlement_lines (id, merchant_id, import_id, settlement_date, reference, amount)
				VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4)
				RETURNING id`, importID, row.date, row.reference, row.amount,
			).Scan(&lineIDs[i]); err != nil {
				return err
			}
		}

		matched, err := autoMatchSettlementLines(ctx, tx, lineIDs, claims.UserID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE payment_gateway_settlement_imports SET matched_count = $1 WHERE id = $2`, matched, importID); err != nil {
			return err
		}

		lines, err := loadSettlementLines(ctx, tx, importID, "")
		if err != nil {
			return err
		}
		resp = settlementImportResponse{ImportID: importID, Gateway: req.Gateway, Filename: req.Filename, LineCount: len(rows), MatchedCount: matched, Lines: lines}
		return nil
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not import payment gateway settlement")
		return
	}
	httpx.JSON(w, http.StatusCreated, resp)
}

type settlementCSVRow struct {
	date      string
	reference string
	amount    float64
}

// parseSettlementCSV trusts column ORDER (settlement_date, reference,
// amount), same simplifying choice as bank_reconciliation.go's
// parseStatementCSV. Amount is always positive — a settlement is always
// money arriving, never a withdrawal, unlike a bank statement line.
func parseSettlementCSV(raw string) ([]settlementCSVRow, error) {
	reader := csv.NewReader(strings.NewReader(raw))
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) < 2 {
		return nil, errEmptySettlementCSV
	}
	var rows []settlementCSVRow
	for _, rec := range records[1:] { // skip header
		if len(rec) < 3 {
			return nil, errInvalidSettlementCSVRow
		}
		date := strings.TrimSpace(rec[0])
		if _, err := time.Parse("2006-01-02", date); err != nil {
			return nil, errInvalidSettlementCSVRow
		}
		reference := strings.TrimSpace(rec[1])
		if reference == "" {
			return nil, errInvalidSettlementCSVRow
		}
		amount, err := strconv.ParseFloat(strings.TrimSpace(rec[2]), 64)
		if err != nil || amount <= 0 {
			return nil, errInvalidSettlementCSVRow
		}
		rows = append(rows, settlementCSVRow{date: date, reference: reference, amount: amount})
	}
	if len(rows) == 0 {
		return nil, errEmptySettlementCSV
	}
	return rows, nil
}

// autoMatchSettlementLines tries each newly-imported line against
// captured card/UPI payments by exact reference match — a gateway
// reference/UTR is unique per transaction, so (unlike bank
// reconciliation's amount+date heuristic) an exact string match is the
// right primary key here, not a fuzzy window.
//
// Three outcomes per line, matching the FRD's "identify missing/
// duplicate": (1) exactly one unclaimed payment shares this reference —
// match it and post the settlement journal entry; (2) the reference
// belongs to a payment that's already claimed by a different settlement
// line, or another line in this same batch already claims it — mark
// 'duplicate' rather than silently double-posting; (3) no payment shares
// this reference at all — leave 'unmatched' for manual review (could be
// a genuinely untracked transaction, or simply hasn't been imported/
// created here yet).
func autoMatchSettlementLines(ctx context.Context, tx pgx.Tx, lineIDs []string, matchedBy string) (int, error) {
	matched := 0
	seenReferences := make(map[string]bool)
	for _, lineID := range lineIDs {
		var reference string
		var amount float64
		if err := tx.QueryRow(ctx, `SELECT reference, amount FROM payment_gateway_settlement_lines WHERE id = $1`, lineID).
			Scan(&reference, &amount); err != nil {
			return matched, err
		}

		if seenReferences[reference] {
			if _, err := tx.Exec(ctx, `UPDATE payment_gateway_settlement_lines SET status = 'duplicate' WHERE id = $1`, lineID); err != nil {
				return matched, err
			}
			continue
		}

		var alreadyClaimed bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM payment_gateway_settlement_lines psl
				JOIN payments p ON p.id = psl.matched_payment_id
				WHERE p.reference_no = $1)`, reference,
		).Scan(&alreadyClaimed); err != nil {
			return matched, err
		}
		if alreadyClaimed {
			if _, err := tx.Exec(ctx, `UPDATE payment_gateway_settlement_lines SET status = 'duplicate' WHERE id = $1`, lineID); err != nil {
				return matched, err
			}
			seenReferences[reference] = true
			continue
		}

		var candidateIDs []string
		rows, err := tx.Query(ctx, `
			SELECT id FROM payments
			WHERE reference_no = $1 AND method IN ('card','upi') AND status = 'captured'`, reference)
		if err != nil {
			return matched, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return matched, err
			}
			candidateIDs = append(candidateIDs, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return matched, err
		}

		seenReferences[reference] = true
		if len(candidateIDs) != 1 {
			continue // none, or ambiguous (shouldn't happen for a real gateway reference) — leave for manual review
		}

		if err := matchSettlementLine(ctx, tx, lineID, candidateIDs[0], amount, matchedBy); err != nil {
			return matched, err
		}
		matched++
	}
	return matched, nil
}

// matchSettlementLine links a settlement line to a payment and posts the
// real accounting event this represents: money moving out of the
// clearing account it landed in at checkout, into Bank, net of whatever
// the gateway kept as its fee. See this file's package doc comment.
func matchSettlementLine(ctx context.Context, tx pgx.Tx, lineID, paymentID string, settlementAmount float64, matchedBy string) error {
	var method string
	var paymentAmount float64
	var branchID string
	if err := tx.QueryRow(ctx, `
		SELECT p.method, p.amount, so.branch_id
		FROM payments p JOIN sales_orders so ON so.id = p.sales_order_id
		WHERE p.id = $1`, paymentID,
	).Scan(&method, &paymentAmount, &branchID); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE payment_gateway_settlement_lines
		SET status = 'matched', matched_payment_id = $1, matched_at = now(), matched_by = $2
		WHERE id = $3`, paymentID, matchedBy, lineID); err != nil {
		return err
	}

	if err := postSettlementJournal(ctx, tx, branchID, lineID, matchedBy, "payment_gateway_settlement",
		"Payment gateway settlement", method, paymentAmount, settlementAmount); err != nil {
		return err
	}
	return nil
}

// postSettlementJournal posts Dr Bank (settlementAmount) + Dr/Cr Payment
// Gateway Fees (the shortfall or, on reversal, its mirror) / Cr the
// clearing account (paymentAmount) — or the exact swapped-debit/credit
// mirror image for a reversal (sourceType
// "payment_gateway_settlement_reversal"), the same "post a mirror entry,
// never delete" treatment internal/sales/void.go already gives a voided
// sale.
func postSettlementJournal(ctx context.Context, tx pgx.Tx, branchID, lineID, performedBy, sourceType, description, method string, paymentAmount, settlementAmount float64) error {
	clearingAccount := AccountCodeForPaymentMethod(method)
	fee := round2(paymentAmount - settlementAmount)

	var lines []JournalLine
	reversing := sourceType == "payment_gateway_settlement_reversal"
	if !reversing {
		lines = append(lines, JournalLine{AccountCode: AccountBank, Debit: settlementAmount})
		if fee > 0.01 {
			lines = append(lines, JournalLine{AccountCode: AccountPaymentGatewayFees, Debit: fee})
		} else if fee < -0.01 {
			lines = append(lines, JournalLine{AccountCode: AccountPaymentGatewayFees, Credit: -fee})
		}
		lines = append(lines, JournalLine{AccountCode: clearingAccount, Credit: paymentAmount})
	} else {
		lines = append(lines, JournalLine{AccountCode: AccountBank, Credit: settlementAmount})
		if fee > 0.01 {
			lines = append(lines, JournalLine{AccountCode: AccountPaymentGatewayFees, Credit: fee})
		} else if fee < -0.01 {
			lines = append(lines, JournalLine{AccountCode: AccountPaymentGatewayFees, Debit: -fee})
		}
		lines = append(lines, JournalLine{AccountCode: clearingAccount, Debit: paymentAmount})
	}

	_, err := PostJournalEntry(ctx, tx, branchID, sourceType, lineID, description, performedBy, lines)
	return err
}

func loadSettlementLines(ctx context.Context, tx pgx.Tx, importID, status string) ([]settlementLineResponse, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, settlement_date::text, reference, amount::text, status, matched_payment_id::text
		FROM payment_gateway_settlement_lines
		WHERE ($1 = '' OR import_id = $1::uuid) AND ($2 = '' OR status = $2)
		ORDER BY settlement_date, created_at`, importID, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	lines := []settlementLineResponse{}
	for rows.Next() {
		var l settlementLineResponse
		if err := rows.Scan(&l.LineID, &l.SettlementDate, &l.Reference, &l.Amount, &l.Status, &l.MatchedPaymentID); err != nil {
			return nil, err
		}
		lines = append(lines, l)
	}
	return lines, rows.Err()
}

// ---------------------------------------------------------------------
// GET /accounting/payment-gateway/settlement/imports — open read,
// matching every other accounting report in this package.
// ---------------------------------------------------------------------

type settlementImportSummary struct {
	ImportID     string `json:"import_id"`
	Gateway      string `json:"gateway"`
	Filename     string `json:"filename"`
	LineCount    int    `json:"line_count"`
	MatchedCount int    `json:"matched_count"`
	ImportedAt   string `json:"imported_at"`
}

func (h *Handler) ListPaymentGatewaySettlementImports(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	imports := []settlementImportSummary{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, gateway, COALESCE(filename,''), line_count, matched_count, imported_at::text
			FROM payment_gateway_settlement_imports ORDER BY imported_at DESC`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s settlementImportSummary
			if err := rows.Scan(&s.ImportID, &s.Gateway, &s.Filename, &s.LineCount, &s.MatchedCount, &s.ImportedAt); err != nil {
				return err
			}
			imports = append(imports, s)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list payment gateway settlement imports")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"imports": imports})
}

// ---------------------------------------------------------------------
// GET /accounting/payment-gateway/settlement/lines?import_id=&status=
// ---------------------------------------------------------------------

func (h *Handler) ListPaymentGatewaySettlementLines(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	importID := r.URL.Query().Get("import_id")
	status := r.URL.Query().Get("status")

	var lines []settlementLineResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		lines, err = loadSettlementLines(ctx, tx, importID, status)
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list payment gateway settlement lines")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"lines": lines})
}

// ---------------------------------------------------------------------
// GET /accounting/payment-gateway/settlement/lines/{id}/candidates —
// unclaimed captured card/UPI payments within a week of this settlement
// line's date, for a manual-matching picker. Shows near-misses (not
// scoped by exact reference) since a human reviewing candidates is
// exactly the case where the reference didn't auto-match cleanly.
// ---------------------------------------------------------------------

type paymentCandidate struct {
	PaymentID    string `json:"payment_id"`
	SalesOrderID string `json:"sales_order_id"`
	Method       string `json:"method"`
	Amount       string `json:"amount"`
	ReferenceNo  string `json:"reference_no"`
	CreatedAt    string `json:"created_at"`
}

func (h *Handler) PaymentGatewaySettlementLineCandidates(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	lineID := chi.URLParam(r, "id")

	candidates := []paymentCandidate{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var settlementDate string
		if err := tx.QueryRow(ctx, `SELECT settlement_date::text FROM payment_gateway_settlement_lines WHERE id = $1`, lineID).Scan(&settlementDate); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT p.id, p.sales_order_id::text, p.method, p.amount::text, COALESCE(p.reference_no,''), p.created_at::text
			FROM payments p
			LEFT JOIN payment_gateway_settlement_lines psl ON psl.matched_payment_id = p.id
			WHERE p.method IN ('card','upi') AND p.status = 'captured' AND psl.id IS NULL
			  AND p.created_at::date BETWEEN $1::date - INTERVAL '7 days' AND $1::date + INTERVAL '7 days'
			ORDER BY p.created_at`, settlementDate)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c paymentCandidate
			if err := rows.Scan(&c.PaymentID, &c.SalesOrderID, &c.Method, &c.Amount, &c.ReferenceNo, &c.CreatedAt); err != nil {
				return err
			}
			candidates = append(candidates, c)
		}
		return rows.Err()
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "settlement line not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load match candidates")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"candidates": candidates})
}

// ---------------------------------------------------------------------
// POST /accounting/payment-gateway/settlement/lines/{id}/match — gated
// by payment_gateway_reconciliation.manage. {payment_id}
// ---------------------------------------------------------------------

type matchSettlementRequest struct {
	PaymentID string `json:"payment_id"`
}

var (
	errPaymentAlreadyMatched = errors.New("this payment is already matched to a different settlement line")
	errPaymentNotEligible    = errors.New("payment must be a captured card/upi payment")
)

func (h *Handler) MatchPaymentGatewaySettlementLine(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	lineID := chi.URLParam(r, "id")
	var req matchSettlementRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PaymentID == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "payment_id is required")
		return
	}

	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var eligible bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payments WHERE id = $1 AND method IN ('card','upi') AND status = 'captured')`, req.PaymentID).
			Scan(&eligible); err != nil {
			return err
		}
		if !eligible {
			return errPaymentNotEligible
		}
		var alreadyClaimed bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_gateway_settlement_lines WHERE matched_payment_id = $1)`, req.PaymentID).
			Scan(&alreadyClaimed); err != nil {
			return err
		}
		if alreadyClaimed {
			return errPaymentAlreadyMatched
		}

		var settlementAmount float64
		if err := tx.QueryRow(ctx, `SELECT amount FROM payment_gateway_settlement_lines WHERE id = $1`, lineID).Scan(&settlementAmount); err != nil {
			return err
		}
		return matchSettlementLine(ctx, tx, lineID, req.PaymentID, settlementAmount, claims.UserID)
	})

	switch {
	case errors.Is(err, errPaymentNotEligible):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "payment must be a captured card/upi payment")
	case errors.Is(err, errPaymentAlreadyMatched):
		httpx.Error(w, http.StatusConflict, "ALREADY_MATCHED", "this payment is already matched to a different settlement line")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "settlement line not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not match settlement line")
	default:
		httpx.JSON(w, http.StatusOK, map[string]any{"status": "matched"})
	}
}

// ---------------------------------------------------------------------
// POST /accounting/payment-gateway/settlement/lines/{id}/unmatch —
// gated by payment_gateway_reconciliation.manage. Posts the mirror-image
// reversal of the journal entry match() posted, then clears the link —
// the original entry is never deleted, matching this codebase's
// immutable-audit-trail convention (internal/sales/void.go's own
// comment: "posted the exact mirror image ... original entry never
// deleted").
// ---------------------------------------------------------------------

var errSettlementLineNotMatched = errors.New("this settlement line is not currently matched")

func (h *Handler) UnmatchPaymentGatewaySettlementLine(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	lineID := chi.URLParam(r, "id")

	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var settlementAmount float64
		var matchedPaymentID *string
		if err := tx.QueryRow(ctx, `
			SELECT amount, matched_payment_id::text FROM payment_gateway_settlement_lines WHERE id = $1`, lineID,
		).Scan(&settlementAmount, &matchedPaymentID); err != nil {
			return err
		}
		if matchedPaymentID == nil {
			return errSettlementLineNotMatched
		}

		var method string
		var paymentAmount float64
		var branchID string
		if err := tx.QueryRow(ctx, `
			SELECT p.method, p.amount, so.branch_id
			FROM payments p JOIN sales_orders so ON so.id = p.sales_order_id
			WHERE p.id = $1`, *matchedPaymentID,
		).Scan(&method, &paymentAmount, &branchID); err != nil {
			return err
		}

		if err := postSettlementJournal(ctx, tx, branchID, lineID, claims.UserID, "payment_gateway_settlement_reversal",
			"Unmatched payment gateway settlement", method, paymentAmount, settlementAmount); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
			UPDATE payment_gateway_settlement_lines
			SET status = 'unmatched', matched_payment_id = NULL, matched_at = NULL, matched_by = NULL
			WHERE id = $1`, lineID); err != nil {
			return err
		}
		return nil
	})

	switch {
	case errors.Is(err, errSettlementLineNotMatched):
		httpx.Error(w, http.StatusConflict, "NOT_MATCHED", "this settlement line is not currently matched")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "settlement line not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not unmatch settlement line")
	default:
		httpx.JSON(w, http.StatusOK, map[string]any{"status": "unmatched"})
	}
}

// ---------------------------------------------------------------------
// GET /accounting/payment-gateway/reconciliation?start=&end= — the
// FRD's "identify missing/duplicate": every unmatched/duplicate
// settlement line in range, plus every captured card/UPI payment in
// range with no matching settlement at all — the operationally
// important half of this report, since it's money already collected
// that hasn't shown up in the bank yet.
// ---------------------------------------------------------------------

type missingPayment struct {
	PaymentID    string `json:"payment_id"`
	SalesOrderID string `json:"sales_order_id"`
	Method       string `json:"method"`
	Amount       string `json:"amount"`
	ReferenceNo  string `json:"reference_no"`
	CreatedAt    string `json:"created_at"`
}

type paymentGatewayReconciliationReport struct {
	Start                    string                   `json:"start"`
	End                      string                   `json:"end"`
	MatchedTotal             string                   `json:"matched_total"`
	UnmatchedSettlementLines []settlementLineResponse `json:"unmatched_settlement_lines"`
	DuplicateSettlementLines []settlementLineResponse `json:"duplicate_settlement_lines"`
	MissingPayments          []missingPayment         `json:"missing_payments"`
}

func (h *Handler) PaymentGatewayReconciliationReport(w http.ResponseWriter, r *http.Request) {
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

	resp := paymentGatewayReconciliationReport{Start: start, End: end, UnmatchedSettlementLines: []settlementLineResponse{}, DuplicateSettlementLines: []settlementLineResponse{}, MissingPayments: []missingPayment{}}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var matchedTotal float64
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(amount),0) FROM payment_gateway_settlement_lines
			WHERE status = 'matched' AND settlement_date BETWEEN $1::date AND $2::date`, start, end,
		).Scan(&matchedTotal); err != nil {
			return err
		}
		resp.MatchedTotal = formatMoney(matchedTotal)

		for _, status := range []string{"unmatched", "duplicate"} {
			lines, err := loadSettlementLines(ctx, tx, "", status)
			if err != nil {
				return err
			}
			var filtered []settlementLineResponse
			for _, l := range lines {
				if l.SettlementDate >= start && l.SettlementDate <= end {
					filtered = append(filtered, l)
				}
			}
			if filtered == nil {
				filtered = []settlementLineResponse{}
			}
			if status == "unmatched" {
				resp.UnmatchedSettlementLines = filtered
			} else {
				resp.DuplicateSettlementLines = filtered
			}
		}

		rows, err := tx.Query(ctx, `
			SELECT p.id, p.sales_order_id::text, p.method, p.amount::text, COALESCE(p.reference_no,''), p.created_at::text
			FROM payments p
			LEFT JOIN payment_gateway_settlement_lines psl ON psl.matched_payment_id = p.id
			WHERE p.method IN ('card','upi') AND p.status = 'captured' AND psl.id IS NULL
			  AND p.created_at::date BETWEEN $1::date AND $2::date
			ORDER BY p.created_at`, start, end)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var m missingPayment
			if err := rows.Scan(&m.PaymentID, &m.SalesOrderID, &m.Method, &m.Amount, &m.ReferenceNo, &m.CreatedAt); err != nil {
				return err
			}
			resp.MissingPayments = append(resp.MissingPayments, m)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not build reconciliation report")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}
