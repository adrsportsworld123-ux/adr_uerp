package hr

// Several check-ins and check-outs per day (migrations/053): every
// check-in opens an attendance_sessions row and the next check-out closes
// it. The day's attendance row is the summary payroll and the register
// read — first check-in, last check-out, worked hours (breaks excluded) —
// rebuilt from the sessions by recalcDay whenever one changes.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

type sessionResponse struct {
	ID               string        `json:"id"`
	ClockIn          string        `json:"clock_in"`
	ClockOut         *string       `json:"clock_out,omitempty"`
	Minutes          *int          `json:"minutes,omitempty"` // worked minutes, once closed
	ClockInEvidence  punchEvidence `json:"clock_in_evidence"`
	ClockOutEvidence punchEvidence `json:"clock_out_evidence"`
	CorrectedAt      *string       `json:"corrected_at,omitempty"`
	CorrectionReason *string       `json:"correction_reason,omitempty"`
}

// evidenceJSON builds one punch's evidence object in SQL, for the
// sessions aggregate in attendanceSelect.
func evidenceJSON(side string) string {
	c := func(col string) string { return "s." + side + "_" + col }
	return `json_build_object('latitude', ` + c("latitude") + `, 'longitude', ` + c("longitude") + `, 'accuracy_m', ` + c("accuracy_m") +
		`, 'ip', ` + c("ip") + `, 'user_agent', ` + c("user_agent") + `, 'source', ` + c("source") + `, 'terminal_id', ` + c("terminal_id") +
		`, 'distance_m', ` + c("distance_m") + `, 'geofence', ` + c("geofence") + `)`
}

// sessionsAggregate is the day's sessions as one JSON array, oldest first.
var sessionsAggregate = `COALESCE((SELECT json_agg(json_strip_nulls(json_build_object(
		'id', s.id, 'clock_in', s.clock_in::text, 'clock_out', s.clock_out::text,
		'minutes', CASE WHEN s.clock_out IS NOT NULL THEN ROUND(EXTRACT(EPOCH FROM (s.clock_out - s.clock_in)) / 60)::int END,
		'clock_in_evidence', ` + evidenceJSON("clock_in") + `, 'clock_out_evidence', ` + evidenceJSON("clock_out") + `,
		'corrected_at', s.corrected_at::text, 'correction_reason', s.correction_reason)) ORDER BY s.clock_in)
	FROM attendance_sessions s WHERE s.attendance_id = a.id), '[]')::text`

func decodeSessions(raw string) []sessionResponse {
	out := []sessionResponse{}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

// lockPunches serializes one employee's punches for the rest of the
// transaction, so a double-tapped check-in can't open two sessions.
func lockPunches(ctx context.Context, tx pgx.Tx, userID string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('attendance:' || $1))`, userID)
	return err
}

// openSession: the caller's session awaiting a check-out, from the last 20
// hours (a shift past midnight still closes). pgx.ErrNoRows when none.
func openSession(ctx context.Context, tx pgx.Tx, userID string) (sessionID, attendanceID string, err error) {
	err = tx.QueryRow(ctx, `
		SELECT id::text, attendance_id::text FROM attendance_sessions
		WHERE user_id = $1 AND clock_out IS NULL AND clock_in > now() - interval '20 hours'
		ORDER BY clock_in DESC LIMIT 1`, userID).Scan(&sessionID, &attendanceID)
	return
}

// recalcDay rebuilds a day's summary from its sessions: first check-in and
// its evidence, last check-out and its evidence (none while a session is
// open), and the worked hours of the closed sessions. A day with no
// sessions (a leave mark) is left as it is.
func recalcDay(ctx context.Context, tx pgx.Tx, attendanceID string) error {
	_, err := tx.Exec(ctx, `
		WITH s AS (SELECT * FROM attendance_sessions WHERE attendance_id::text = $1),
		     f AS (SELECT * FROM s ORDER BY clock_in LIMIT 1),
		     l AS (SELECT * FROM s WHERE NOT EXISTS (SELECT 1 FROM s o WHERE o.clock_out IS NULL) ORDER BY clock_out DESC LIMIT 1)
		UPDATE attendance a SET
		  clock_in = f.clock_in, clock_in_latitude = f.clock_in_latitude, clock_in_longitude = f.clock_in_longitude,
		  clock_in_accuracy_m = f.clock_in_accuracy_m, clock_in_ip = f.clock_in_ip, clock_in_user_agent = f.clock_in_user_agent,
		  clock_in_source = f.clock_in_source, clock_in_terminal_id = f.clock_in_terminal_id,
		  clock_in_distance_m = f.clock_in_distance_m, clock_in_geofence = f.clock_in_geofence,
		  clock_out = l.clock_out, clock_out_latitude = l.clock_out_latitude, clock_out_longitude = l.clock_out_longitude,
		  clock_out_accuracy_m = l.clock_out_accuracy_m, clock_out_ip = l.clock_out_ip, clock_out_user_agent = l.clock_out_user_agent,
		  clock_out_source = l.clock_out_source, clock_out_terminal_id = l.clock_out_terminal_id,
		  clock_out_distance_m = l.clock_out_distance_m, clock_out_geofence = l.clock_out_geofence,
		  hours_worked = (SELECT ROUND(COALESCE(SUM(EXTRACT(EPOCH FROM (clock_out - clock_in))), 0) / 3600.0, 2) FROM s WHERE clock_out IS NOT NULL)
		FROM f LEFT JOIN l ON true
		WHERE a.id::text = $1`, attendanceID)
	return err
}

// errSessionOverlap: a corrected or added session would overlap another of
// the same employee's sessions.
var errSessionOverlap = errors.New("overlaps another session")

func checkOverlap(ctx context.Context, tx pgx.Tx, userID, exceptID string, in time.Time, out *time.Time) error {
	var overlaps bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM attendance_sessions
		WHERE user_id::text = $1 AND id::text <> $2
		  AND tstzrange(clock_in, COALESCE(clock_out, 'infinity')) && tstzrange($3::timestamptz, COALESCE($4::timestamptz, 'infinity')))`,
		userID, exceptID, in, out).Scan(&overlaps)
	if err == nil && overlaps {
		return errSessionOverlap
	}
	return err
}

type sessionEditRequest struct {
	ClockIn  string  `json:"clock_in"`  // RFC 3339; required when adding
	ClockOut *string `json:"clock_out"` // RFC 3339; "" = still open
	Reason   string  `json:"reason"`
}

func (req sessionEditRequest) times() (in *time.Time, out *time.Time, outSet bool, err error) {
	if req.ClockIn != "" {
		t, err := time.Parse(time.RFC3339, req.ClockIn)
		if err != nil {
			return nil, nil, false, err
		}
		in = &t
	}
	if req.ClockOut != nil {
		outSet = true
		if strings.TrimSpace(*req.ClockOut) != "" {
			t, err := time.Parse(time.RFC3339, *req.ClockOut)
			if err != nil {
				return nil, nil, false, err
			}
			out = &t
		}
	}
	return in, out, outSet, nil
}

func writeSessionError(w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", what+" not found")
	case errors.Is(err, errSessionOverlap):
		httpx.Error(w, http.StatusConflict, "SESSION_OVERLAP", "these times overlap another check-in session of this employee")
	case errors.Is(err, errBadSessionTimes):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "check-out must be after check-in")
	default:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not save the session")
	}
}

var errBadSessionTimes = errors.New("check-out before check-in")

// AddSession: POST /hr/attendance/{id}/sessions {clock_in, clock_out?, reason}
// (hr.manage) — a check-in/out pair the employee forgot to punch.
func (h *Handler) AddSession(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	attendanceID := chi.URLParam(r, "id")
	var req sessionEditRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	in, out, _, err := req.times()
	if err != nil || in == nil || strings.TrimSpace(req.Reason) == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "clock_in (RFC 3339) and a reason are required")
		return
	}
	var resp attendanceResponse
	err = h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := addSession(ctx, tx, attendanceID, *in, out, claims.UserID, strings.TrimSpace(req.Reason)); err != nil {
			return err
		}
		return scanAttendance(tx.QueryRow(ctx, attendanceSelect+` WHERE a.id::text = $1`, attendanceID), &resp)
	})
	if err != nil {
		writeSessionError(w, err, "attendance record")
		return
	}
	httpx.JSON(w, http.StatusCreated, resp)
}

// CorrectSession: PATCH /hr/attendance/sessions/{id} {clock_in?, clock_out?, reason}
// (hr.manage). The session's punch evidence stays as recorded; who
// corrected it, when and why is stamped on it and written to audit_logs.
func (h *Handler) CorrectSession(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	sessionID := chi.URLParam(r, "id")
	var req sessionEditRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	in, out, outSet, err := req.times()
	if err != nil || strings.TrimSpace(req.Reason) == "" || (in == nil && !outSet) {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "a reason and a clock_in or clock_out (RFC 3339) are required")
		return
	}
	var resp attendanceResponse
	err = h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		attendanceID, err := correctSession(ctx, tx, sessionID, in, outSet, out, claims.UserID, strings.TrimSpace(req.Reason))
		if err != nil {
			return err
		}
		return scanAttendance(tx.QueryRow(ctx, attendanceSelect+` WHERE a.id::text = $1`, attendanceID), &resp)
	})
	if err != nil {
		writeSessionError(w, err, "session")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// addSession adds a check-in/out pair (source manual) to a day, rebuilds
// the day and audits it: HR's AddSession and an approved regularization.
func addSession(ctx context.Context, tx pgx.Tx, attendanceID string, in time.Time, out *time.Time, by, reason string) (string, error) {
	var userID string
	var branchID *string
	if err := tx.QueryRow(ctx, `SELECT user_id::text, branch_id::text FROM attendance WHERE id::text = $1 FOR UPDATE`, attendanceID).Scan(&userID, &branchID); err != nil {
		return "", err
	}
	if out != nil && !out.After(in) {
		return "", errBadSessionTimes
	}
	if err := lockPunches(ctx, tx, userID); err != nil {
		return "", err
	}
	if err := checkOverlap(ctx, tx, userID, "", in, out); err != nil {
		return "", err
	}
	var sessionID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO attendance_sessions (merchant_id, attendance_id, user_id, branch_id, clock_in, clock_out,
		    clock_in_source, clock_out_source, corrected_by, corrected_at, correction_reason)
		VALUES (current_setting('app.tenant_id')::uuid, $1::uuid, $2::uuid, $3::uuid, $4, $5,
		    'manual', CASE WHEN $5::timestamptz IS NOT NULL THEN 'manual' END, $6, now(), $7)
		RETURNING id::text`,
		attendanceID, userID, branchID, in, out, by, reason).Scan(&sessionID); err != nil {
		return "", err
	}
	if err := recalcDay(ctx, tx, attendanceID); err != nil {
		return "", err
	}
	return sessionID, audit.Log(ctx, tx, audit.Entry{EntityType: "attendance_sessions", EntityID: sessionID, Action: "create",
		PerformedBy: by, After: map[string]any{"clock_in": in, "clock_out": out}, Reason: reason})
}

// correctSession changes a session's check-in and/or check-out (outSet
// with out nil reopens it), keeping its punch evidence, rebuilds the day
// and audits it: HR's CorrectSession and an approved regularization.
// Returns the session's day.
func correctSession(ctx context.Context, tx pgx.Tx, sessionID string, in *time.Time, outSet bool, out *time.Time, by, reason string) (string, error) {
	var userID, attendanceID string
	var curIn time.Time
	var curOut *time.Time
	if err := tx.QueryRow(ctx, `SELECT user_id::text, attendance_id::text, clock_in, clock_out FROM attendance_sessions WHERE id::text = $1 FOR UPDATE`,
		sessionID).Scan(&userID, &attendanceID, &curIn, &curOut); err != nil {
		return "", err
	}
	newIn, newOut := curIn, curOut
	if in != nil {
		newIn = *in
	}
	if outSet {
		newOut = out
	}
	if newOut != nil && !newOut.After(newIn) {
		return "", errBadSessionTimes
	}
	if err := lockPunches(ctx, tx, userID); err != nil {
		return "", err
	}
	if err := checkOverlap(ctx, tx, userID, sessionID, newIn, newOut); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE attendance_sessions SET clock_in = $2, clock_out = $3,
		  clock_out_source = CASE WHEN $3::timestamptz IS NOT NULL AND clock_out_source IS NULL THEN 'manual' ELSE clock_out_source END,
		  corrected_by = $4, corrected_at = now(), correction_reason = $5
		WHERE id::text = $1`, sessionID, newIn, newOut, by, reason); err != nil {
		return "", err
	}
	if err := recalcDay(ctx, tx, attendanceID); err != nil {
		return "", err
	}
	return attendanceID, audit.Log(ctx, tx, audit.Entry{EntityType: "attendance_sessions", EntityID: sessionID, Action: "update", PerformedBy: by,
		Before: map[string]any{"clock_in": curIn, "clock_out": curOut}, After: map[string]any{"clock_in": newIn, "clock_out": newOut},
		Reason: reason})
}
