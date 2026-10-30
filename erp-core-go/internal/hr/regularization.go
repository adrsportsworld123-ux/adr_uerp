package hr

// Attendance regularization (§3.78; migrations/075): the employee asks for
// a check-in/out to be corrected (a missed check-out, a wrong time) or a
// forgotten day to be added; approval applies it exactly as HR's own
// session correction would, the request named in the reason.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/locale"
)

type regularization struct {
	ID               string  `json:"regularization_id"`
	Number           string  `json:"request_number"`
	UserID           string  `json:"user_id"`
	Employee         string  `json:"employee"`
	EmployeeCode     *string `json:"employee_code"`
	BranchID         *string `json:"branch_id"`
	Branch           *string `json:"branch"`
	WorkDate         string  `json:"work_date"`
	SessionID        *string `json:"session_id"` // null: add a session
	ClockIn          *string `json:"clock_in"`
	ClockOut         *string `json:"clock_out"`
	BeforeClockIn    *string `json:"before_clock_in"`
	BeforeClockOut   *string `json:"before_clock_out"`
	Reason           string  `json:"reason"`
	Status           string  `json:"status"`
	Source           string  `json:"source"`
	RequestedBy      *string `json:"requested_by"`
	RequestedAt      string  `json:"requested_at"`
	AppliedSessionID *string `json:"applied_session_id"`
	DecidedBy        *string `json:"decided_by"`
	DecidedAt        *string `json:"decided_at"`
	DecisionNote     *string `json:"decision_note"`
	CancelledBy      *string `json:"cancelled_by"`
	CancelledAt      *string `json:"cancelled_at"`
	CancelReason     *string `json:"cancel_reason"`
}

const regularizationSelect = `
	SELECT r.id::text, r.request_number, r.user_id::text, u.name, u.employee_code, r.branch_id::text, b.name,
	       r.work_date::text, r.session_id::text, to_char(r.clock_in AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
	       to_char(r.clock_out AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
	       to_char(r.before_clock_in AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
	       to_char(r.before_clock_out AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
	       r.reason, r.status, r.source, rb.name, r.requested_at::text, r.applied_session_id::text,
	       db.name, r.decided_at::text, r.decision_note, cb.name, r.cancelled_at::text, r.cancel_reason
	FROM attendance_regularizations r
	JOIN users u ON u.id = r.user_id
	LEFT JOIN branches b ON b.id = r.branch_id
	LEFT JOIN users rb ON rb.id = r.requested_by
	LEFT JOIN users db ON db.id = r.decided_by
	LEFT JOIN users cb ON cb.id = r.cancelled_by`

func scanRegularization(row pgx.Row, g *regularization) error {
	return row.Scan(&g.ID, &g.Number, &g.UserID, &g.Employee, &g.EmployeeCode, &g.BranchID, &g.Branch,
		&g.WorkDate, &g.SessionID, &g.ClockIn, &g.ClockOut, &g.BeforeClockIn, &g.BeforeClockOut,
		&g.Reason, &g.Status, &g.Source, &g.RequestedBy, &g.RequestedAt, &g.AppliedSessionID,
		&g.DecidedBy, &g.DecidedAt, &g.DecisionNote, &g.CancelledBy, &g.CancelledAt, &g.CancelReason)
}

func loadRegularization(ctx context.Context, tx pgx.Tx, id string) (regularization, error) {
	var g regularization
	return g, scanRegularization(tx.QueryRow(ctx, regularizationSelect+` WHERE r.id::text = $1`, id), &g)
}

func parseStamp(s *string) (*time.Time, error) {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, *s)
	return &t, err
}

// fullLeaveDay: the day is a full day of approved leave.
func fullLeaveDay(ctx context.Context, tx pgx.Tx, userID string, day time.Time) (bool, error) {
	var on bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM attendance WHERE user_id::text = $1 AND work_date = $2 AND leave_request_id IS NOT NULL AND leave_days = 1)`,
		userID, day).Scan(&on)
	return on, err
}

// missedCheckOut explains an overlap caused by an earlier check-in that
// was never checked out (an open session overlaps everything after it):
// that one is corrected first.
func missedCheckOut(ctx context.Context, tx pgx.Tx, userID string, in time.Time, overlap error) error {
	if !errors.Is(overlap, errSessionOverlap) {
		return overlap
	}
	var day *time.Time
	if err := tx.QueryRow(ctx, `SELECT max(a.work_date) FROM attendance_sessions s JOIN attendance a ON a.id = s.attendance_id
		WHERE s.user_id::text = $1 AND s.clock_out IS NULL AND s.clock_in < $2`, userID, in).Scan(&day); err != nil || day == nil {
		return overlap
	}
	return conflict("MISSED_CHECK_OUT", "the check-in on %s has no check-out yet; correct that one first", day.Format("02 Jan 2006"))
}

var errLeaveDay = conflict("REGULARIZE_LEAVE_DAY", "that day is a full day of approved leave; cancel the leave first")

// CreateRegularization: POST /hr/regularizations {user_id?, work_date,
// session_id?, clock_in?, clock_out?, reason, source?} — with session_id,
// correct that session (clock_in and/or clock_out); without, add a
// check-in/out (both times) on work_date. Times are RFC 3339.
func (h *Handler) CreateRegularization(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req struct {
		UserID    string  `json:"user_id"`
		WorkDate  string  `json:"work_date"`
		SessionID string  `json:"session_id"`
		ClockIn   *string `json:"clock_in"`
		ClockOut  *string `json:"clock_out"`
		Reason    string  `json:"reason"`
		Source    string  `json:"source"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	day, okDay := parseDate(req.WorkDate)
	in, err1 := parseStamp(req.ClockIn)
	out, err2 := parseStamp(req.ClockOut)
	source, okSrc := validSource(req.Source)
	switch {
	case !okDay:
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "work_date (YYYY-MM-DD) is required")
		return
	case err1 != nil || err2 != nil:
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "clock_in and clock_out must be RFC 3339 timestamps")
		return
	case in == nil && out == nil:
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "give the correct check-in, check-out or both")
		return
	case req.SessionID == "" && (in == nil || out == nil):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "to add a missed day give both the check-in and the check-out")
		return
	case strings.TrimSpace(req.Reason) == "":
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "a reason is required")
		return
	case !okSrc:
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "source must be pos, web or api")
		return
	}
	var resp regularization
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		s, err := requestSubject(ctx, tx, claims.UserID, req.UserID)
		if err != nil {
			return err
		}
		settings, err := loadAttendanceSettings(ctx, tx)
		if err != nil {
			return err
		}
		today := merchantToday(ctx, tx)
		if day.After(today) || day.Before(today.AddDate(0, 0, -settings.RegularizeWithinDays)) {
			return badRequest("you can ask to correct the last %d days, up to today", settings.RegularizeWithinDays)
		}
		loc := locale.Location(ctx, tx)
		if in != nil {
			l := in.In(loc)
			if time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, time.UTC) != day {
				return badRequest("the check-in must be on %s", day.Format("02 Jan 2006"))
			}
		}
		if out != nil && out.After(time.Now()) {
			return badRequest("the check-out can't be in the future")
		}
		if err := lockPunches(ctx, tx, s.userID); err != nil {
			return err
		}
		if on, err := fullLeaveDay(ctx, tx, s.userID, day); err != nil {
			return err
		} else if on {
			return errLeaveDay
		}
		var beforeIn, beforeOut *time.Time
		var sessionID *string
		if req.SessionID != "" {
			var owner string
			var workDate time.Time
			if err := tx.QueryRow(ctx, `
				SELECT s.user_id::text, a.work_date, s.clock_in, s.clock_out FROM attendance_sessions s JOIN attendance a ON a.id = s.attendance_id
				WHERE s.id::text = $1`, req.SessionID).Scan(&owner, &workDate, &beforeIn, &beforeOut); errors.Is(err, pgx.ErrNoRows) || (err == nil && owner != s.userID) {
				return &reqErr{http.StatusNotFound, "NOT_FOUND", "that check-in session isn't this employee's"}
			} else if err != nil {
				return err
			}
			if !workDate.Equal(day) {
				return badRequest("that session is on %s", workDate.Format("02 Jan 2006"))
			}
			var pending string
			if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT request_number FROM attendance_regularizations WHERE session_id::text = $1 AND status = 'pending' LIMIT 1), '')`,
				req.SessionID).Scan(&pending); err != nil {
				return err
			}
			if pending != "" {
				return conflict("REGULARIZATION_PENDING", "request %s for this session is already waiting", pending)
			}
			newIn, newOut := *beforeIn, beforeOut
			if in != nil {
				newIn = *in
			}
			if out != nil {
				newOut = out
			}
			if newOut != nil && !newOut.After(newIn) {
				return errBadSessionTimes
			}
			if err := checkOverlap(ctx, tx, s.userID, req.SessionID, newIn, newOut); err != nil {
				return err
			}
			sessionID = &req.SessionID
		} else {
			if !out.After(*in) {
				return errBadSessionTimes
			}
			if err := checkOverlap(ctx, tx, s.userID, "", *in, out); err != nil {
				return missedCheckOut(ctx, tx, s.userID, *in, err)
			}
		}
		number, err := nextRequestNumber(ctx, tx, "regularization", "RG")
		if err != nil {
			return err
		}
		var id string
		if err := tx.QueryRow(ctx, `
			INSERT INTO attendance_regularizations (merchant_id, request_number, user_id, branch_id, work_date, session_id, clock_in, clock_out,
			    before_clock_in, before_clock_out, reason, source, requested_by)
			VALUES (current_setting('app.tenant_id')::uuid, $1, $2::uuid, $3::uuid, $4, $5::uuid, $6, $7, $8, $9, $10, $11, $12)
			RETURNING id::text`,
			number, s.userID, s.branchID, day, sessionID, in, out, beforeIn, beforeOut, strings.TrimSpace(req.Reason), source, claims.UserID).Scan(&id); err != nil {
			return err
		}
		if resp, err = loadRegularization(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "attendance_regularizations", EntityID: id, Action: "create", PerformedBy: claims.UserID, After: resp})
	})
	if err != nil {
		writeReqError(w, err, "regularization")
		return
	}
	httpx.JSON(w, http.StatusCreated, resp)
}

// ListRegularizations: GET /hr/regularizations?mine=1&user_id=&status=&from=&to=
func (h *Handler) ListRegularizations(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	q := r.URL.Query()
	out := []regularization{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		who, err := listScope(ctx, tx, claims.UserID, q.Get("mine") == "1" || q.Get("mine") == "true", q.Get("user_id"))
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, regularizationSelect+`
			WHERE ($1 = '' OR r.user_id::text = $1) AND ($2 = '' OR r.status = $2)
			  AND ($3 = '' OR r.work_date >= $3::date) AND ($4 = '' OR r.work_date <= $4::date)
			ORDER BY r.status = 'pending' DESC, r.work_date DESC, r.request_number DESC LIMIT 500`,
			who, q.Get("status"), dateOrEmpty(q.Get("from")), dateOrEmpty(q.Get("to")))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var g regularization
			if err := scanRegularization(rows, &g); err != nil {
				return err
			}
			out = append(out, g)
		}
		return rows.Err()
	})
	if err != nil {
		writeReqError(w, err, "regularizations")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"regularizations": out})
}

func lockRegularization(ctx context.Context, tx pgx.Tx, id, decider string) (regularization, error) {
	if _, err := tx.Exec(ctx, `SELECT 1 FROM attendance_regularizations WHERE id::text = $1 FOR UPDATE`, id); err != nil {
		return regularization{}, err
	}
	g, err := loadRegularization(ctx, tx, id)
	if err != nil {
		return g, err
	}
	if g.Status != "pending" {
		return g, errNotPending
	}
	if g.UserID == decider {
		return g, errSelfApproval
	}
	return g, nil
}

// ApproveRegularization: POST /hr/regularizations/{id}/approve {note?}
// (hr.manage; never your own). The session is corrected, or the missed
// check-in/out added (the day created when it has no row; an absent day
// becomes present).
func (h *Handler) ApproveRegularization(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	d, err := readDecision(r)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	var resp regularization
	err = h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := lockRegularization(ctx, tx, id, claims.UserID)
		if err != nil {
			return err
		}
		in, _ := parseStamp(before.ClockIn)
		out, _ := parseStamp(before.ClockOut)
		day, _ := parseDate(before.WorkDate)
		reason := "Regularization " + before.Number + ": " + before.Reason
		if on, err := fullLeaveDay(ctx, tx, before.UserID, day); err != nil {
			return err
		} else if on {
			return errLeaveDay
		}
		var applied string
		if before.SessionID != nil {
			if _, err := correctSession(ctx, tx, *before.SessionID, in, out != nil, out, claims.UserID, reason); errors.Is(err, pgx.ErrNoRows) {
				return conflict("SESSION_GONE", "that check-in session no longer exists")
			} else if err != nil {
				return err
			}
			applied = *before.SessionID
		} else {
			var attID string
			err := tx.QueryRow(ctx, `SELECT id::text FROM attendance WHERE user_id::text = $1 AND work_date = $2`, before.UserID, day).Scan(&attID)
			if errors.Is(err, pgx.ErrNoRows) {
				err = tx.QueryRow(ctx, `
					INSERT INTO attendance (merchant_id, user_id, branch_id, work_date, status, corrected_by, corrected_at, correction_reason)
					VALUES (current_setting('app.tenant_id')::uuid, $1::uuid, $2::uuid, $3, 'present', $4, now(), $5) RETURNING id::text`,
					before.UserID, before.BranchID, day, claims.UserID, reason).Scan(&attID)
			} else if err == nil {
				_, err = tx.Exec(ctx, `UPDATE attendance SET status = 'present' WHERE id::text = $1 AND status = 'absent'`, attID)
			}
			if err != nil {
				return err
			}
			if applied, err = addSession(ctx, tx, attID, *in, out, claims.UserID, reason); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE attendance_regularizations SET status = 'approved', applied_session_id = $2::uuid, decided_by = $3, decided_at = now(),
			  decision_note = NULLIF(trim($4), '') WHERE id::text = $1`, id, applied, claims.UserID, d.Note); err != nil {
			return err
		}
		if resp, err = loadRegularization(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "attendance_regularizations", EntityID: id, Action: "update", PerformedBy: claims.UserID,
			Before: before, After: resp, Reason: "approved"})
	})
	if err != nil {
		writeReqError(w, err, "regularization")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// RejectRegularization: POST /hr/regularizations/{id}/reject {note}
func (h *Handler) RejectRegularization(w http.ResponseWriter, r *http.Request) {
	h.decideSimple(w, r, "attendance_regularizations", "regularization", func(ctx context.Context, tx pgx.Tx, id, by string) (any, error) {
		return lockRegularization(ctx, tx, id, by)
	}, func(ctx context.Context, tx pgx.Tx, id string) (any, error) { return loadRegularization(ctx, tx, id) })
}

// CancelRegularization: POST /hr/regularizations/{id}/cancel {reason} — a
// pending one only (yours, or anyone's with hr.manage). An applied
// correction is changed by correcting the session again.
func (h *Handler) CancelRegularization(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	d, err := readDecision(r)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	var resp regularization
	err = h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM attendance_regularizations WHERE id::text = $1 FOR UPDATE`, id); err != nil {
			return err
		}
		before, err := loadRegularization(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := canCancel(ctx, tx, claims.UserID, before.UserID, before.Status, false); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE attendance_regularizations SET status = 'cancelled', cancelled_by = $2, cancelled_at = now(),
			cancel_reason = NULLIF(trim($3), '') WHERE id::text = $1`, id, claims.UserID, d.Reason); err != nil {
			return err
		}
		if resp, err = loadRegularization(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "attendance_regularizations", EntityID: id, Action: "update", PerformedBy: claims.UserID,
			Before: before, After: resp, Reason: "cancelled: " + strings.TrimSpace(d.Reason)})
	})
	if err != nil {
		writeReqError(w, err, "regularization")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}
