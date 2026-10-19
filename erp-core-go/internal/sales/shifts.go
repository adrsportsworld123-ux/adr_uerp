package sales

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
	"github.com/jackc/pgx/v5/pgconn"

	"erp-core-go/internal/accounting"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

// Phase 13: POS shifts (migrations/040_pos_shifts.sql). One till's drawer
// from open to close: an opening float, cash moved in and out, an X report
// any time, and a counted Z close. The close is recorded through
// accounting.RecordCashCount, so it is a cash reconciliation like any
// other, and its expected cash comes from accounting.ComputeCashFlow, the
// same function the daily count uses.

var (
	errShiftAlreadyOpen  = errors.New("this till already has an open shift")
	errShiftClosed       = errors.New("this shift is closed")
	errShiftNotOpen      = errors.New("no open shift on this till")
	errTerminalNotPOS    = errors.New("not an active POS till of this merchant")
	errDayCountedDaily   = errors.New("this branch's cash for today was already counted as a whole-branch daily count")
	errInsufficientCash  = errors.New("more than the drawer should hold")
	errMovementApproval  = errors.New("a paid-out needs a manager's approval")
	errInvalidMovement   = errors.New("invalid cash movement")
	errShiftNotFound     = errors.New("shift not found")
	movementDefaultLabel = map[string]string{"float_in": "Float from the safe", "drop": "Cash drop to the safe"}
)

type shiftCash struct {
	OpeningFloat string `json:"opening_float"`
	CashSales    string `json:"cash_sales"`
	CashRefunds  string `json:"cash_refunds"`
	FloatIn      string `json:"float_in"`
	Drops        string `json:"drops"`
	PaidIn       string `json:"paid_in"`
	PaidOut      string `json:"paid_out"`
	Expected     string `json:"expected"`
}

type shiftMovement struct {
	MovementID  string  `json:"movement_id"`
	Kind        string  `json:"kind"`
	Amount      string  `json:"amount"`
	Reason      string  `json:"reason"`
	PerformedBy string  `json:"performed_by"`
	ApprovedBy  *string `json:"approved_by"`
	CreatedAt   string  `json:"created_at"`
}

type shiftClose struct {
	CountedTotal     string  `json:"counted_total"`
	Variance         string  `json:"variance"`
	Reason           string  `json:"reason"`
	ApprovedBy       *string `json:"approved_by"`
	ReconciliationID string  `json:"cash_reconciliation_id"`
}

// shiftReport is the X report (open shift, live) and the Z report (closed,
// frozen in pos_shifts.z_report).
type shiftReport struct {
	Kind          string            `json:"kind"` // "X" | "Z"
	GeneratedAt   string            `json:"generated_at"`
	OrderCount    int               `json:"order_count"`
	VoidedCount   int               `json:"voided_count"`
	NetSales      string            `json:"net_sales"`
	ByMethod      map[string]string `json:"by_payment_method"`
	ReturnCount   int               `json:"return_count"`
	RefundTotal   string            `json:"refund_total"`
	RefundsMethod map[string]string `json:"refunds_by_method"`
	Cash          shiftCash         `json:"cash"`
	Movements     []shiftMovement   `json:"movements"`
	Close         *shiftClose       `json:"close,omitempty"`
}

type shiftResponse struct {
	ShiftID       string       `json:"shift_id"`
	ShiftNumber   int          `json:"shift_number"`
	Status        string       `json:"status"`
	BranchID      string       `json:"branch_id"`
	BranchName    string       `json:"branch_name"`
	TerminalID    string       `json:"pos_terminal_id"`
	TerminalName  string       `json:"terminal_name"`
	OpeningFloat  string       `json:"opening_float"`
	OpenedBy      string       `json:"opened_by"`
	OpenedAt      string       `json:"opened_at"`
	ClosedBy      *string      `json:"closed_by"`
	ClosedAt      *string      `json:"closed_at"`
	Report        *shiftReport `json:"report,omitempty"`
	Replayed      bool         `json:"replayed,omitempty"`
	openedAtTime  time.Time
	closedAtTime  *time.Time
	openingFloatV float64
	zReport       []byte
}

func loadShift(ctx context.Context, tx pgx.Tx, merchantID, shiftID string, lock bool) (shiftResponse, error) {
	var s shiftResponse
	q := `
		SELECT sh.id, sh.shift_number, sh.status, sh.branch_id, b.name, sh.pos_terminal_id, t.name, sh.opening_float::text, sh.opening_float,
		       ou.name, sh.opened_at, cu.name, sh.closed_at, sh.z_report
		FROM pos_shifts sh
		JOIN pos_terminals t ON t.id = sh.pos_terminal_id AND t.merchant_id = $2
		JOIN branches b ON b.id = sh.branch_id
		JOIN users ou ON ou.id = sh.opened_by
		LEFT JOIN users cu ON cu.id = sh.closed_by
		WHERE sh.id::text = $1`
	if lock {
		q += ` FOR UPDATE OF sh`
	}
	err := tx.QueryRow(ctx, q, shiftID, merchantID).Scan(&s.ShiftID, &s.ShiftNumber, &s.Status, &s.BranchID, &s.BranchName, &s.TerminalID, &s.TerminalName,
		&s.OpeningFloat, &s.openingFloatV, &s.OpenedBy, &s.openedAtTime, &s.ClosedBy, &s.closedAtTime, &s.zReport)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, errShiftNotFound
	}
	if err != nil {
		return s, err
	}
	s.OpenedAt = s.openedAtTime.UTC().Format(time.RFC3339)
	if s.closedAtTime != nil {
		c := s.closedAtTime.UTC().Format(time.RFC3339)
		s.ClosedAt = &c
	}
	return s, nil
}

func money(v float64) string { return fmt.Sprintf("%.2f", v) }

// buildReport computes the shift's figures over [opened_at, to). to is nil
// for a live X report.
func buildReport(ctx context.Context, tx pgx.Tx, s shiftResponse, to *time.Time) (shiftReport, accounting.CashFlow, float64, error) {
	rep := shiftReport{Kind: "X", ByMethod: map[string]string{}, RefundsMethod: map[string]string{}, Movements: []shiftMovement{}}
	from := s.openedAtTime
	flow, err := accounting.ComputeCashFlow(ctx, tx, accounting.CashScope{BranchID: s.BranchID, TerminalID: s.TerminalID, From: &from, To: to})
	if err != nil {
		return rep, flow, 0, err
	}
	expected := round2(s.openingFloatV + flow.Net())
	rep.Cash = shiftCash{
		OpeningFloat: money(s.openingFloatV), CashSales: money(flow.CashSales), CashRefunds: money(flow.CashRefunds),
		FloatIn: money(flow.FloatIn), Drops: money(flow.Drops), PaidIn: money(flow.PaidIn), PaidOut: money(flow.PaidOut),
		Expected: money(expected),
	}

	const moment = `COALESCE(so.device_created_at, so.finalized_at)`
	var net float64
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE so.status <> 'voided'), count(*) FILTER (WHERE so.status = 'voided'),
		       COALESCE(sum(so.grand_total) FILTER (WHERE so.status <> 'voided'), 0)
		FROM sales_orders so
		WHERE so.pos_terminal_id::text = $1 AND so.status IN ('finalized', 'voided', 'partially_refunded', 'refunded')
		  AND `+moment+` >= $2 AND ($3::timestamptz IS NULL OR `+moment+` < $3)`,
		s.TerminalID, from, to).Scan(&rep.OrderCount, &rep.VoidedCount, &net); err != nil {
		return rep, flow, 0, err
	}
	rep.NetSales = money(net)
	rows, err := tx.Query(ctx, `
		SELECT p.method, sum(p.amount)
		FROM payments p JOIN sales_orders so ON so.id = p.sales_order_id
		WHERE so.pos_terminal_id::text = $1 AND so.status IN ('finalized', 'partially_refunded', 'refunded') AND p.status = 'captured'
		  AND `+moment+` >= $2 AND ($3::timestamptz IS NULL OR `+moment+` < $3)
		GROUP BY p.method`, s.TerminalID, from, to)
	if err != nil {
		return rep, flow, 0, err
	}
	for rows.Next() {
		var m string
		var a float64
		if err := rows.Scan(&m, &a); err != nil {
			rows.Close()
			return rep, flow, 0, err
		}
		rep.ByMethod[m] = money(a)
	}
	rows.Close()

	var refundTotal float64
	rows, err = tx.Query(ctx, `
		SELECT refund_method, count(*), sum(refund_amount)
		FROM sales_returns
		WHERE pos_terminal_id::text = $1 AND created_at >= $2 AND ($3::timestamptz IS NULL OR created_at < $3)
		GROUP BY refund_method`, s.TerminalID, from, to)
	if err != nil {
		return rep, flow, 0, err
	}
	for rows.Next() {
		var m string
		var n int
		var a float64
		if err := rows.Scan(&m, &n, &a); err != nil {
			rows.Close()
			return rep, flow, 0, err
		}
		rep.ReturnCount += n
		refundTotal += a
		rep.RefundsMethod[m] = money(a)
	}
	rows.Close()
	rep.RefundTotal = money(refundTotal)

	rows, err = tx.Query(ctx, `
		SELECT m.id, m.kind, m.amount::text, m.reason, pu.name, au.name, m.created_at
		FROM pos_cash_movements m
		JOIN users pu ON pu.id = m.performed_by
		LEFT JOIN users au ON au.id = m.approved_by
		WHERE m.shift_id = $1 ORDER BY m.created_at`, s.ShiftID)
	if err != nil {
		return rep, flow, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var mv shiftMovement
		var at time.Time
		if err := rows.Scan(&mv.MovementID, &mv.Kind, &mv.Amount, &mv.Reason, &mv.PerformedBy, &mv.ApprovedBy, &at); err != nil {
			return rep, flow, 0, err
		}
		mv.CreatedAt = at.UTC().Format(time.RFC3339)
		rep.Movements = append(rep.Movements, mv)
	}
	gen := time.Now().UTC()
	if to != nil {
		gen = to.UTC()
	}
	rep.GeneratedAt = gen.Format(time.RFC3339)
	return rep, flow, expected, rows.Err()
}

// withReport attaches the live X report (open) or the frozen Z (closed).
func withReport(ctx context.Context, tx pgx.Tx, s *shiftResponse) error {
	if s.Status == "closed" && len(s.zReport) > 0 {
		var z shiftReport
		if err := json.Unmarshal(s.zReport, &z); err != nil {
			return err
		}
		s.Report = &z
		return nil
	}
	rep, _, _, err := buildReport(ctx, tx, *s, nil)
	if err != nil {
		return err
	}
	s.Report = &rep
	return nil
}

// requireOpenShift: when the merchant requires shifts, a POS till (not the
// online storefront's virtual one) takes payment only during an open shift.
func requireOpenShift(ctx context.Context, tx pgx.Tx, orderID string) error {
	settings, err := loadSalesSettings(ctx, tx)
	if err != nil || !settings.ShiftsRequired {
		return err
	}
	var channel string
	var open bool
	if err := tx.QueryRow(ctx, `
		SELECT t.channel, EXISTS (SELECT 1 FROM pos_shifts sh WHERE sh.pos_terminal_id = so.pos_terminal_id AND sh.status = 'open')
		FROM sales_orders so JOIN pos_terminals t ON t.id = so.pos_terminal_id
		WHERE so.id = $1`, orderID).Scan(&channel, &open); err != nil {
		return err
	}
	if channel == "pos" && !open {
		return errShiftNotOpen
	}
	return nil
}

func writeShiftError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, errShiftNotFound):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "shift not found")
	case errors.Is(err, errTerminalNotPOS):
		httpx.Error(w, http.StatusConflict, "TERMINAL_UNAVAILABLE", "shifts are for an active POS till of this merchant, in an active branch")
	case errors.Is(err, errShiftAlreadyOpen):
		httpx.Error(w, http.StatusConflict, "SHIFT_ALREADY_OPEN", "this till already has an open shift; close it first")
	case errors.Is(err, errShiftClosed):
		httpx.Error(w, http.StatusConflict, "SHIFT_CLOSED", "this shift is already closed")
	case errors.Is(err, errDayCountedDaily):
		httpx.Error(w, http.StatusConflict, "DAY_ALREADY_RECONCILED", "today's cash for this branch was already counted as a whole-branch daily count")
	case errors.Is(err, errInsufficientCash):
		httpx.Error(w, http.StatusConflict, "INSUFFICIENT_CASH", err.Error())
	case errors.Is(err, errMovementApproval):
		httpx.Error(w, http.StatusForbidden, "CASH_MOVEMENT_NOT_APPROVED", "a paid-out needs a Branch Manager or Merchant Admin's employee code and PIN")
	case errors.Is(err, errInvalidMovement):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
	case errors.Is(err, accounting.ErrVarianceNeedsReason):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "the count doesn't match; give a reason")
	case errors.Is(err, accounting.ErrVarianceNotAuthorized):
		httpx.Error(w, http.StatusForbidden, "VARIANCE_NOT_AUTHORIZED", "the count doesn't match; a Branch Manager or Merchant Admin must approve with their employee code and PIN")
	default:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", fallback)
	}
}

// CurrentShift: GET /pos-shifts/current?terminal_id= — the till's open
// shift with its live X report, or {"shift": null}.
func (h *Handler) CurrentShift(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	terminalID := r.URL.Query().Get("terminal_id")
	if terminalID == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "terminal_id is required")
		return
	}
	var resp *shiftResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var id string
		err := tx.QueryRow(ctx, `SELECT id FROM pos_shifts WHERE pos_terminal_id::text = $1 AND status = 'open'`, terminalID).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		} else if err != nil {
			return err
		}
		s, err := loadShift(ctx, tx, claims.TenantID, id, false)
		if err != nil {
			return err
		}
		if err := withReport(ctx, tx, &s); err != nil {
			return err
		}
		resp = &s
		return nil
	})
	if err != nil {
		writeShiftError(w, err, "could not load the shift")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"shift": resp})
}

// GetShift: GET /pos-shifts/{id} — with the X (open) or Z (closed) report.
func (h *Handler) GetShift(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var s shiftResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if s, err = loadShift(ctx, tx, claims.TenantID, chi.URLParam(r, "id"), false); err != nil {
			return err
		}
		return withReport(ctx, tx, &s)
	})
	if err != nil {
		writeShiftError(w, err, "could not load the shift")
		return
	}
	httpx.JSON(w, http.StatusOK, s)
}

// OpenShift: POST /pos-shifts {pos_terminal_id, opening_float, idempotency_key}
func (h *Handler) OpenShift(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req struct {
		TerminalID     string  `json:"pos_terminal_id"`
		OpeningFloat   float64 `json:"opening_float"`
		IdempotencyKey string  `json:"idempotency_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.TerminalID == "" || strings.TrimSpace(req.IdempotencyKey) == "" || req.OpeningFloat < 0 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "pos_terminal_id, idempotency_key and a non-negative opening_float are required")
		return
	}
	var s shiftResponse
	code := http.StatusCreated
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var existing string
		if err := tx.QueryRow(ctx, `SELECT id FROM pos_shifts WHERE idempotency_key = $1`, req.IdempotencyKey).Scan(&existing); err == nil {
			code = http.StatusOK
			var err error
			if s, err = loadShift(ctx, tx, claims.TenantID, existing, false); err != nil {
				return err
			}
			s.Replayed = true
			return withReport(ctx, tx, &s)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		// pos_terminals has no RLS: merchant_id explicitly.
		var branchID string
		err := tx.QueryRow(ctx, `
			SELECT t.branch_id FROM pos_terminals t JOIN branches b ON b.id = t.branch_id
			WHERE t.id::text = $1 AND t.merchant_id = $2 AND t.channel = 'pos' AND t.status = 'active' AND b.status = 'active'`,
			req.TerminalID, claims.TenantID).Scan(&branchID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errTerminalNotPOS
		} else if err != nil {
			return err
		}
		var countedDaily bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM cash_reconciliations WHERE branch_id = $1 AND recon_date = current_date AND pos_shift_id IS NULL)`,
			branchID).Scan(&countedDaily); err != nil {
			return err
		}
		if countedDaily {
			return errDayCountedDaily
		}
		var open bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pos_shifts WHERE pos_terminal_id::text = $1 AND status = 'open')`, req.TerminalID).Scan(&open); err != nil {
			return err
		}
		if open {
			return errShiftAlreadyOpen
		}
		var id string
		if err := tx.QueryRow(ctx, `
			INSERT INTO pos_shifts (merchant_id, branch_id, pos_terminal_id, shift_number, opening_float, opened_by, idempotency_key)
			VALUES (current_setting('app.tenant_id')::uuid, $1, $2::uuid,
			        (SELECT COALESCE(max(shift_number), 0) + 1 FROM pos_shifts WHERE pos_terminal_id = $2::uuid), $3, $4, $5)
			RETURNING id`, branchID, req.TerminalID, req.OpeningFloat, claims.UserID, req.IdempotencyKey).Scan(&id); err != nil {
			if isUniqueViolation(err) {
				return errShiftAlreadyOpen // a second open raced this one
			}
			return err
		}
		if s, err = loadShift(ctx, tx, claims.TenantID, id, false); err != nil {
			return err
		}
		return withReport(ctx, tx, &s)
	})
	if err != nil {
		writeShiftError(w, err, "could not open the shift")
		return
	}
	httpx.JSON(w, code, s)
}

// AddCashMovement: POST /pos-shifts/{id}/cash-movements
// {kind: float_in|drop|paid_in|paid_out, amount, reason, approver_code?, approver_pin?, idempotency_key}
func (h *Handler) AddCashMovement(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req struct {
		Kind           string  `json:"kind"`
		Amount         float64 `json:"amount"`
		Reason         string  `json:"reason"`
		ApproverCode   string  `json:"approver_code"`
		ApproverPIN    string  `json:"approver_pin"`
		IdempotencyKey string  `json:"idempotency_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	req.Reason = strings.TrimSpace(req.Reason)
	switch {
	case req.Kind != "float_in" && req.Kind != "drop" && req.Kind != "paid_in" && req.Kind != "paid_out":
		writeShiftError(w, fmt.Errorf("%w: kind must be float_in, drop, paid_in or paid_out", errInvalidMovement), "")
		return
	case round2(req.Amount) <= 0:
		writeShiftError(w, fmt.Errorf("%w: amount must be positive", errInvalidMovement), "")
		return
	case strings.TrimSpace(req.IdempotencyKey) == "":
		writeShiftError(w, fmt.Errorf("%w: idempotency_key is required", errInvalidMovement), "")
		return
	case req.Reason == "" && (req.Kind == "paid_in" || req.Kind == "paid_out"):
		writeShiftError(w, fmt.Errorf("%w: say what the money was for", errInvalidMovement), "")
		return
	}
	if req.Reason == "" {
		req.Reason = movementDefaultLabel[req.Kind]
	}
	amount := round2(req.Amount)

	var s shiftResponse
	code := http.StatusCreated
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if s, err = loadShift(ctx, tx, claims.TenantID, chi.URLParam(r, "id"), true); err != nil {
			return err
		}
		var dup bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pos_cash_movements WHERE idempotency_key = $1)`, req.IdempotencyKey).Scan(&dup); err != nil {
			return err
		}
		if dup {
			code = http.StatusOK
			s.Replayed = true
			return withReport(ctx, tx, &s)
		}
		if s.Status != "open" {
			return errShiftClosed
		}
		var approvedBy *string
		if req.Kind == "paid_out" {
			id, err := approverFor(ctx, tx, req.ApproverCode, req.ApproverPIN, s.BranchID)
			if err != nil {
				return err
			}
			if id == "" {
				return errMovementApproval
			}
			approvedBy = &id
		}
		if req.Kind == "drop" || req.Kind == "paid_out" {
			_, _, expected, err := buildReport(ctx, tx, s, nil)
			if err != nil {
				return err
			}
			if amount > expected+0.004 {
				return fmt.Errorf("%w: the drawer should hold ₹%.2f", errInsufficientCash, expected)
			}
		}
		var movementID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO pos_cash_movements (merchant_id, shift_id, branch_id, kind, amount, reason, performed_by, approved_by, idempotency_key)
			VALUES (current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
			s.ShiftID, s.BranchID, req.Kind, amount, req.Reason, claims.UserID, approvedBy, req.IdempotencyKey).Scan(&movementID); err != nil {
			return err
		}
		// Only money entering or leaving the business posts; float and
		// drops move cash within account 1001 (migrations/040).
		var lines []accounting.JournalLine
		switch req.Kind {
		case "paid_in":
			lines = []accounting.JournalLine{{AccountCode: accounting.AccountCash, Debit: amount}, {AccountCode: accounting.AccountOtherIncome, Credit: amount}}
		case "paid_out":
			lines = []accounting.JournalLine{{AccountCode: accounting.AccountPettyCash, Debit: amount}, {AccountCode: accounting.AccountCash, Credit: amount}}
		}
		if lines != nil {
			if _, err := accounting.PostJournalEntry(ctx, tx, s.BranchID, "cash_movement", movementID,
				fmt.Sprintf("%s · %s shift %d: %s", strings.ReplaceAll(req.Kind, "_", " "), s.TerminalName, s.ShiftNumber, req.Reason), claims.UserID, lines); err != nil {
				return err
			}
		}
		return withReport(ctx, tx, &s)
	})
	if err != nil {
		writeShiftError(w, err, "could not record the cash movement")
		return
	}
	httpx.JSON(w, code, s)
}

// CloseShift: POST /pos-shifts/{id}/close
// {denominations: [{denomination, count}], reason?, approver_code?, approver_pin?}
// Records the count as the shift's cash reconciliation and freezes the Z.
// Closing an already-closed shift answers with its Z (a retried close).
func (h *Handler) CloseShift(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req struct {
		Denominations []accounting.DenominationCount `json:"denominations"`
		Reason        string                         `json:"reason"`
		ApproverCode  string                         `json:"approver_code"`
		ApproverPIN   string                         `json:"approver_pin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if len(req.Denominations) == 0 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "count the drawer: at least one denomination line (an empty drawer is one line with count 0)")
		return
	}
	for _, d := range req.Denominations {
		if d.Denomination <= 0 || d.Count < 0 {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "each denomination must be positive with a non-negative count")
			return
		}
	}
	req.Reason = strings.TrimSpace(req.Reason)

	var s shiftResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if s, err = loadShift(ctx, tx, claims.TenantID, chi.URLParam(r, "id"), true); err != nil {
			return err
		}
		if s.Status == "closed" {
			s.Replayed = true
			return withReport(ctx, tx, &s)
		}
		// Fix the close moment once, so the report's window and closed_at
		// agree to the microsecond.
		var closeAt time.Time
		if err := tx.QueryRow(ctx, `SELECT now()`).Scan(&closeAt); err != nil {
			return err
		}
		rep, _, expected, err := buildReport(ctx, tx, s, &closeAt)
		if err != nil {
			return err
		}
		count, err := accounting.RecordCashCount(ctx, tx, accounting.CashCount{
			BranchID: s.BranchID, ReconDate: closeAt.UTC().Format("2006-01-02"), ShiftID: s.ShiftID,
			OpeningFloat: s.openingFloatV, SystemExpected: expected, Denominations: req.Denominations,
			Reason: req.Reason, CountedBy: claims.UserID,
			Authorize: func(ctx context.Context, tx pgx.Tx) (string, error) {
				return approverFor(ctx, tx, req.ApproverCode, req.ApproverPIN, s.BranchID)
			},
		})
		if err != nil {
			return err
		}
		var approverName *string
		if count.AuthorizedBy != nil {
			var n string
			if err := tx.QueryRow(ctx, `SELECT name FROM users WHERE id = $1`, *count.AuthorizedBy).Scan(&n); err != nil {
				return err
			}
			approverName = &n
		}
		rep.Kind = "Z"
		rep.Close = &shiftClose{
			CountedTotal: money(count.CountedTotal), Variance: money(count.Variance), Reason: req.Reason,
			ApprovedBy: approverName, ReconciliationID: count.ReconciliationID,
		}
		z, err := json.Marshal(rep)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE pos_shifts SET status = 'closed', closed_by = $1, closed_at = $2, z_report = $3 WHERE id = $4`,
			claims.UserID, closeAt, z, s.ShiftID); err != nil {
			return err
		}
		if s, err = loadShift(ctx, tx, claims.TenantID, s.ShiftID, false); err != nil {
			return err
		}
		return withReport(ctx, tx, &s)
	})
	if err != nil {
		writeShiftError(w, err, "could not close the shift")
		return
	}
	httpx.JSON(w, http.StatusOK, s)
}

// ListShifts: GET /pos-shifts?branch_id=&terminal_id=&status=&from=&to=&page=&limit=
// newest first, paged (default 25). Dates are the opening day (UTC, the
// same calendar the cash reconciliation uses).
func (h *Handler) ListShifts(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	q := r.URL.Query()
	status, from, to := q.Get("status"), q.Get("from"), q.Get("to")
	if status != "" && status != "open" && status != "closed" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "status must be open or closed")
		return
	}
	for _, d := range []string{from, to} {
		if d != "" && !isValidDate(d) {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "from/to must be YYYY-MM-DD")
			return
		}
	}
	p := httpx.ParseListParams(r, map[string]string{"opened": "sh.opened_at"}, "opened")
	if q.Get("dir") == "" {
		p.OrderBy = "sh.opened_at DESC"
	}
	type row struct {
		ShiftID      string  `json:"shift_id"`
		ShiftNumber  int     `json:"shift_number"`
		Status       string  `json:"status"`
		BranchID     string  `json:"branch_id"`
		BranchName   string  `json:"branch_name"`
		TerminalID   string  `json:"pos_terminal_id"`
		TerminalName string  `json:"terminal_name"`
		OpeningFloat string  `json:"opening_float"`
		OpenedBy     string  `json:"opened_by"`
		OpenedAt     string  `json:"opened_at"`
		ClosedBy     *string `json:"closed_by"`
		ClosedAt     *string `json:"closed_at"`
		Variance     *string `json:"variance"`
		OrderCount   *int    `json:"order_count"` // closed shifts (from the frozen report); null while open
		NetSales     *string `json:"net_sales"`
	}
	list := []row{}
	total := 0
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		where := `
			FROM pos_shifts sh
			JOIN pos_terminals t ON t.id = sh.pos_terminal_id
			JOIN users u ON u.id = sh.opened_by
			JOIN branches b ON b.id = sh.branch_id
			LEFT JOIN users cu ON cu.id = sh.closed_by
			LEFT JOIN cash_reconciliations cr ON cr.pos_shift_id = sh.id
			WHERE ($1 = '' OR sh.branch_id::text = $1) AND ($2 = '' OR sh.pos_terminal_id::text = $2)
			  AND ($3 = '' OR sh.status = $3)
			  AND ($4 = '' OR sh.opened_at::date >= $4::date) AND ($5 = '' OR sh.opened_at::date <= $5::date)`
		args := []any{q.Get("branch_id"), q.Get("terminal_id"), status, from, to}
		if err := tx.QueryRow(ctx, `SELECT count(*)`+where, args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT sh.id, sh.shift_number, sh.status, sh.branch_id, b.name, sh.pos_terminal_id, t.name, sh.opening_float::text, u.name,
			       to_char(sh.opened_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), cu.name,
			       to_char(sh.closed_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), cr.variance::text,
			       (sh.z_report->>'order_count')::int, sh.z_report->>'net_sales'`+where+`
			ORDER BY `+p.OrderBy+`, sh.id LIMIT $6 OFFSET $7`, append(args, p.Limit, p.Offset)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var x row
			if err := rows.Scan(&x.ShiftID, &x.ShiftNumber, &x.Status, &x.BranchID, &x.BranchName, &x.TerminalID, &x.TerminalName, &x.OpeningFloat,
				&x.OpenedBy, &x.OpenedAt, &x.ClosedBy, &x.ClosedAt, &x.Variance, &x.OrderCount, &x.NetSales); err != nil {
				return err
			}
			list = append(list, x)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list shifts")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"shifts": list, "total": total, "page": p.Page, "limit": p.Limit})
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
