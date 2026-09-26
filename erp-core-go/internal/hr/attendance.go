package hr

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

var (
	errClockInRequired   = errors.New("no clock-in recorded for today")
	errAlreadyClockedOut = errors.New("already clocked out for today")
)

type attendanceResponse struct {
	ID                    string  `json:"id"`
	UserID                string  `json:"user_id"`
	WorkDate              string  `json:"work_date"`
	ClockIn               *string `json:"clock_in,omitempty"`
	ClockOut              *string `json:"clock_out,omitempty"`
	Status                string  `json:"status"`
	HoursWorked           *string `json:"hours_worked,omitempty"`
	LateMinutes           *int    `json:"late_minutes,omitempty"`
	EarlyDepartureMinutes *int    `json:"early_departure_minutes,omitempty"`
}

// attendanceSelect is shared by every read path that returns an
// attendanceResponse — late_minutes/early_departure_minutes (the FRD's
// "Late/early tracking") are computed on read against the employee's
// currently-assigned shift, never stored: an employee reassigned to a
// different shift later must not retroactively change what an old
// attendance row reports as late, so this only reflects the shift
// assigned AT THE TIME the row is read, same "compute, don't cache"
// choice this codebase already makes for margin_pct/segment/etc. NULL
// (omitted from the JSON) whenever the employee has no shift assigned,
// or the relevant punch hasn't happened yet — never a false "on time".
const attendanceSelect = `
	SELECT a.id, a.user_id, a.work_date::text, a.clock_in::text, a.clock_out::text, a.status, a.hours_worked::text,
	       CASE WHEN a.clock_in IS NOT NULL AND s.start_time IS NOT NULL
	            THEN GREATEST(0, ROUND(EXTRACT(EPOCH FROM (a.clock_in::time - s.start_time)) / 60)::int)
	       END,
	       CASE WHEN a.clock_out IS NOT NULL AND s.end_time IS NOT NULL
	            THEN GREATEST(0, ROUND(EXTRACT(EPOCH FROM (s.end_time - a.clock_out::time)) / 60)::int)
	       END
	FROM attendance a
	LEFT JOIN users u ON u.id = a.user_id
	LEFT JOIN shifts s ON s.id = u.shift_id`

func scanAttendance(row pgx.Row, a *attendanceResponse) error {
	return row.Scan(&a.ID, &a.UserID, &a.WorkDate, &a.ClockIn, &a.ClockOut, &a.Status, &a.HoursWorked,
		&a.LateMinutes, &a.EarlyDepartureMinutes)
}

// ClockIn: POST /hr/attendance/clock-in — self-service, operates on the
// caller's own record (claims.UserID), never a body-supplied employee id,
// so one employee can never punch in on another's behalf. Idempotent: a
// repeat call the same day leaves the original clock_in time untouched
// (ON CONFLICT DO NOTHING) rather than erroring or resetting it.
func (h *Handler) ClockIn(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}

	var resp attendanceResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var branchID *string
		_ = tx.QueryRow(ctx, `SELECT branch_id FROM users WHERE id = $1`, claims.UserID).Scan(&branchID)

		if _, err := tx.Exec(ctx, `
			INSERT INTO attendance (id, merchant_id, user_id, branch_id, work_date, clock_in, status)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, CURRENT_DATE, now(), 'present')
			ON CONFLICT (user_id, work_date) DO NOTHING`, claims.UserID, branchID); err != nil {
			return err
		}
		return loadTodayAttendance(ctx, tx, claims.UserID, &resp)
	})

	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not clock in")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// ClockOut: POST /hr/attendance/clock-out — self-service, same reasoning
// as ClockIn.
func (h *Handler) ClockOut(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}

	var resp attendanceResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var clockIn *string
		if err := tx.QueryRow(ctx, `SELECT clock_in::text FROM attendance WHERE user_id = $1 AND work_date = CURRENT_DATE`, claims.UserID).
			Scan(&clockIn); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errClockInRequired
			}
			return err
		}
		if clockIn == nil {
			return errClockInRequired
		}

		var alreadyOut bool
		if err := tx.QueryRow(ctx, `SELECT clock_out IS NOT NULL FROM attendance WHERE user_id = $1 AND work_date = CURRENT_DATE`, claims.UserID).
			Scan(&alreadyOut); err != nil {
			return err
		}
		if alreadyOut {
			return errAlreadyClockedOut
		}

		if _, err := tx.Exec(ctx, `
			UPDATE attendance SET clock_out = now(),
			  hours_worked = ROUND(EXTRACT(EPOCH FROM (now() - clock_in)) / 3600.0, 2)
			WHERE user_id = $1 AND work_date = CURRENT_DATE`, claims.UserID); err != nil {
			return err
		}
		return loadTodayAttendance(ctx, tx, claims.UserID, &resp)
	})

	switch {
	case errors.Is(err, errClockInRequired):
		httpx.Error(w, http.StatusConflict, "CLOCK_IN_REQUIRED", "you haven't clocked in today")
	case errors.Is(err, errAlreadyClockedOut):
		httpx.Error(w, http.StatusConflict, "ALREADY_CLOCKED_OUT", "you've already clocked out today")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not clock out")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}

// ListAttendance: GET /hr/attendance?user_id=&month=YYYY-MM — defaults to
// the caller's own record; viewing another user's attendance requires
// hr.manage, checked in-handler (not router middleware) since it's
// conditional on whether user_id was even supplied.
func (h *Handler) ListAttendance(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		userID = claims.UserID
	}
	month := r.URL.Query().Get("month")

	records := []attendanceResponse{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if userID != claims.UserID {
			codes, err := authn.FetchPermissions(ctx, tx, claims.UserID)
			if err != nil {
				return err
			}
			if !authn.HasPermission(codes, "hr.manage") {
				return errForbiddenOtherEmployee
			}
		}
		rows, err := tx.Query(ctx, attendanceSelect+`
			WHERE a.user_id = $1 AND ($2 = '' OR to_char(a.work_date, 'YYYY-MM') = $2)
			ORDER BY a.work_date DESC`, userID, month)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a attendanceResponse
			if err := scanAttendance(rows, &a); err != nil {
				return err
			}
			records = append(records, a)
		}
		return rows.Err()
	})

	switch {
	case errors.Is(err, errForbiddenOtherEmployee):
		httpx.Error(w, http.StatusForbidden, "FORBIDDEN", "you don't have the hr.manage permission")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load attendance")
	default:
		httpx.JSON(w, http.StatusOK, map[string]any{"attendance": records})
	}
}

var errForbiddenOtherEmployee = errors.New("forbidden: another employee's attendance")

type correctAttendanceRequest struct {
	Status string `json:"status"`
}

// CorrectAttendance: PATCH /hr/attendance/{id} — HR override for a missed
// punch or a leave/absence marking. Gated by hr.manage at the router
// level (no self-vs-other ambiguity here, unlike ListAttendance).
func (h *Handler) CorrectAttendance(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var req correctAttendanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.Status == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "status is required")
		return
	}

	var resp attendanceResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var userID string
		if err := tx.QueryRow(ctx, `UPDATE attendance SET status = $2 WHERE id = $1 RETURNING user_id`, id, req.Status).Scan(&userID); err != nil {
			return err
		}
		row := tx.QueryRow(ctx, attendanceSelect+` WHERE a.id = $1`, id)
		return scanAttendance(row, &resp)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "no attendance record with this id")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not correct attendance")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

func loadTodayAttendance(ctx context.Context, tx pgx.Tx, userID string, resp *attendanceResponse) error {
	row := tx.QueryRow(ctx, attendanceSelect+` WHERE a.user_id = $1 AND a.work_date = CURRENT_DATE`, userID)
	return scanAttendance(row, resp)
}
