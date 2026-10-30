package hr

// Permission — a few hours off in a working day (§3.78; migrations/075).
// Approved permissions are free while the month's count and minutes stay
// within attendance_settings' limits; the minutes beyond are unpaid, and
// payroll docks them as a part of a day.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

type permissionRequest struct {
	ID            string  `json:"permission_request_id"`
	Number        string  `json:"request_number"`
	UserID        string  `json:"user_id"`
	Employee      string  `json:"employee"`
	EmployeeCode  *string `json:"employee_code"`
	BranchID      *string `json:"branch_id"`
	Branch        *string `json:"branch"`
	WorkDate      string  `json:"work_date"`
	FromTime      string  `json:"from_time"` // HH:MM
	ToTime        string  `json:"to_time"`
	Minutes       int     `json:"minutes"`
	Reason        string  `json:"reason"`
	Status        string  `json:"status"`
	Source        string  `json:"source"`
	RequestedBy   *string `json:"requested_by"`
	RequestedAt   string  `json:"requested_at"`
	PaidMinutes   *int    `json:"paid_minutes"`
	UnpaidMinutes *int    `json:"unpaid_minutes"`
	DecidedBy     *string `json:"decided_by"`
	DecidedAt     *string `json:"decided_at"`
	DecisionNote  *string `json:"decision_note"`
	CancelledBy   *string `json:"cancelled_by"`
	CancelledAt   *string `json:"cancelled_at"`
	CancelReason  *string `json:"cancel_reason"`
}

const permissionSelect = `
	SELECT r.id::text, r.request_number, r.user_id::text, u.name, u.employee_code, r.branch_id::text, b.name,
	       r.work_date::text, to_char(r.from_time, 'HH24:MI'), to_char(r.to_time, 'HH24:MI'), r.minutes, r.reason, r.status, r.source,
	       rb.name, r.requested_at::text, r.paid_minutes, r.unpaid_minutes,
	       db.name, r.decided_at::text, r.decision_note, cb.name, r.cancelled_at::text, r.cancel_reason
	FROM permission_requests r
	JOIN users u ON u.id = r.user_id
	LEFT JOIN branches b ON b.id = r.branch_id
	LEFT JOIN users rb ON rb.id = r.requested_by
	LEFT JOIN users db ON db.id = r.decided_by
	LEFT JOIN users cb ON cb.id = r.cancelled_by`

func scanPermission(row pgx.Row, p *permissionRequest) error {
	return row.Scan(&p.ID, &p.Number, &p.UserID, &p.Employee, &p.EmployeeCode, &p.BranchID, &p.Branch,
		&p.WorkDate, &p.FromTime, &p.ToTime, &p.Minutes, &p.Reason, &p.Status, &p.Source,
		&p.RequestedBy, &p.RequestedAt, &p.PaidMinutes, &p.UnpaidMinutes,
		&p.DecidedBy, &p.DecidedAt, &p.DecisionNote, &p.CancelledBy, &p.CancelledAt, &p.CancelReason)
}

func loadPermission(ctx context.Context, tx pgx.Tx, id string) (permissionRequest, error) {
	var p permissionRequest
	return p, scanPermission(tx.QueryRow(ctx, permissionSelect+` WHERE r.id::text = $1`, id), &p)
}

func parseClock(s string) (time.Time, bool) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	return t, err == nil
}

// CreatePermission: POST /hr/permission-requests {user_id?, work_date,
// from_time, to_time (HH:MM), reason, source?}
func (h *Handler) CreatePermission(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req struct {
		UserID   string `json:"user_id"`
		WorkDate string `json:"work_date"`
		FromTime string `json:"from_time"`
		ToTime   string `json:"to_time"`
		Reason   string `json:"reason"`
		Source   string `json:"source"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	day, ok1 := parseDate(req.WorkDate)
	fromT, ok2 := parseClock(req.FromTime)
	toT, ok3 := parseClock(req.ToTime)
	source, ok4 := validSource(req.Source)
	switch {
	case !ok1 || !ok2 || !ok3:
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "work_date (YYYY-MM-DD), from_time and to_time (HH:MM) are required")
		return
	case !toT.After(fromT):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "to_time must be after from_time")
		return
	case strings.TrimSpace(req.Reason) == "":
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "a reason is required")
		return
	case !ok4:
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "source must be pos, web or api")
		return
	}
	minutes := int(toT.Sub(fromT).Minutes())
	var resp permissionRequest
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		s, err := requestSubject(ctx, tx, claims.UserID, req.UserID)
		if err != nil {
			return err
		}
		settings, err := loadAttendanceSettings(ctx, tx)
		if err != nil {
			return err
		}
		if minutes > settings.PermissionMaxMinutes {
			return &reqErr{http.StatusBadRequest, "PERMISSION_TOO_LONG", "a permission is at most " + hoursText(settings.PermissionMaxMinutes) + "; ask for a half day's leave instead"}
		}
		today := merchantToday(ctx, tx)
		if day.Before(today.AddDate(0, 0, -settings.RegularizeWithinDays)) || day.After(today.AddDate(0, 0, 60)) {
			return badRequest("the date must be within the last %d days or the next 60", settings.RegularizeWithinDays)
		}
		if (s.joining != nil && day.Before(*s.joining)) || (s.exit != nil && day.After(*s.exit)) {
			return badRequest("%s wasn't employed on that date", s.name)
		}
		if err := lockPunches(ctx, tx, s.userID); err != nil {
			return err
		}
		var clash string
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE((SELECT request_number FROM permission_requests WHERE user_id::text = $1 AND work_date = $2 AND status IN ('pending', 'approved')
			  AND from_time < $4::time AND to_time > $3::time LIMIT 1), '')`,
			s.userID, day, req.FromTime, req.ToTime).Scan(&clash); err != nil {
			return err
		}
		if clash != "" {
			return conflict("PERMISSION_OVERLAP", "these times overlap permission %s", clash)
		}
		number, err := nextRequestNumber(ctx, tx, "permission", "PR")
		if err != nil {
			return err
		}
		var id string
		if err := tx.QueryRow(ctx, `
			INSERT INTO permission_requests (merchant_id, request_number, user_id, branch_id, work_date, from_time, to_time, minutes,
			    reason, source, requested_by)
			VALUES (current_setting('app.tenant_id')::uuid, $1, $2::uuid, $3::uuid, $4, $5::time, $6::time, $7, $8, $9, $10)
			RETURNING id::text`,
			number, s.userID, s.branchID, day, req.FromTime, req.ToTime, minutes, strings.TrimSpace(req.Reason), source, claims.UserID).Scan(&id); err != nil {
			return err
		}
		if resp, err = loadPermission(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "permission_requests", EntityID: id, Action: "create", PerformedBy: claims.UserID, After: resp})
	})
	if err != nil {
		writeReqError(w, err, "permission request")
		return
	}
	httpx.JSON(w, http.StatusCreated, resp)
}

// hoursText: 240 → "4 h", 90 → "1 h 30 min".
func hoursText(m int) string {
	h, mm := m/60, m%60
	switch {
	case h == 0:
		return strconv.Itoa(mm) + " min"
	case mm == 0:
		return strconv.Itoa(h) + " h"
	}
	return strconv.Itoa(h) + " h " + strconv.Itoa(mm) + " min"
}

// ListPermissions: GET /hr/permission-requests?mine=1&user_id=&status=&from=&to=
func (h *Handler) ListPermissions(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	q := r.URL.Query()
	out := []permissionRequest{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		who, err := listScope(ctx, tx, claims.UserID, q.Get("mine") == "1" || q.Get("mine") == "true", q.Get("user_id"))
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, permissionSelect+`
			WHERE ($1 = '' OR r.user_id::text = $1) AND ($2 = '' OR r.status = $2)
			  AND ($3 = '' OR r.work_date >= $3::date) AND ($4 = '' OR r.work_date <= $4::date)
			ORDER BY r.status = 'pending' DESC, r.work_date DESC, r.request_number DESC LIMIT 500`,
			who, q.Get("status"), dateOrEmpty(q.Get("from")), dateOrEmpty(q.Get("to")))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var p permissionRequest
			if err := scanPermission(rows, &p); err != nil {
				return err
			}
			out = append(out, p)
		}
		return rows.Err()
	})
	if err != nil {
		writeReqError(w, err, "permission requests")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"permission_requests": out})
}

func lockPermission(ctx context.Context, tx pgx.Tx, id, decider string) (permissionRequest, error) {
	if _, err := tx.Exec(ctx, `SELECT 1 FROM permission_requests WHERE id::text = $1 FOR UPDATE`, id); err != nil {
		return permissionRequest{}, err
	}
	p, err := loadPermission(ctx, tx, id)
	if err != nil {
		return p, err
	}
	if p.Status != "pending" {
		return p, errNotPending
	}
	if p.UserID == decider {
		return p, errSelfApproval
	}
	return p, nil
}

// freeMinutes: how much of a permission is free, given the month's
// approved ones (count, free minutes used) and the limits.
func freeMinutes(minutes, usedCount, usedFree, freeCount, freeMins int) int {
	if usedCount >= freeCount {
		return 0
	}
	return min(minutes, max(freeMins-usedFree, 0))
}

// ApprovePermission: POST /hr/permission-requests/{id}/approve {note?}
// (hr.manage; never your own). The free part is worked out against the
// month's permissions approved before it.
func (h *Handler) ApprovePermission(w http.ResponseWriter, r *http.Request) {
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
	var resp permissionRequest
	err = h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := lockPermission(ctx, tx, id, claims.UserID)
		if err != nil {
			return err
		}
		day, _ := parseDate(before.WorkDate)
		if err := payrollFinalized(ctx, tx, day, day); err != nil {
			return err
		}
		if err := lockPunches(ctx, tx, before.UserID); err != nil {
			return err
		}
		settings, err := loadAttendanceSettings(ctx, tx)
		if err != nil {
			return err
		}
		usage, err := loadPermissionUsage(ctx, tx, settings, before.UserID, day)
		if err != nil {
			return err
		}
		paid := freeMinutes(before.Minutes, usage.UsedCount, usage.UsedFreeMinutes, settings.PermissionFreeCount, settings.PermissionFreeMinutes)
		if _, err := tx.Exec(ctx, `
			UPDATE permission_requests SET status = 'approved', paid_minutes = $2, unpaid_minutes = $3, decided_by = $4, decided_at = now(),
			  decision_note = NULLIF(trim($5), '') WHERE id::text = $1`,
			id, paid, before.Minutes-paid, claims.UserID, d.Note); err != nil {
			return err
		}
		if resp, err = loadPermission(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "permission_requests", EntityID: id, Action: "update", PerformedBy: claims.UserID,
			Before: before, After: resp, Reason: "approved"})
	})
	if err != nil {
		writeReqError(w, err, "permission request")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// RejectPermission: POST /hr/permission-requests/{id}/reject {note}
func (h *Handler) RejectPermission(w http.ResponseWriter, r *http.Request) {
	h.decideSimple(w, r, "permission_requests", "permission request", func(ctx context.Context, tx pgx.Tx, id, by string) (any, error) {
		return lockPermission(ctx, tx, id, by)
	}, func(ctx context.Context, tx pgx.Tx, id string) (any, error) { return loadPermission(ctx, tx, id) })
}

// CancelPermission: POST /hr/permission-requests/{id}/cancel {reason} —
// your own pending one; hr.manage anyone's, approved too (unless that
// month's payroll is finalized). Permissions approved after it keep how
// they were paid.
func (h *Handler) CancelPermission(w http.ResponseWriter, r *http.Request) {
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
	var resp permissionRequest
	err = h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM permission_requests WHERE id::text = $1 FOR UPDATE`, id); err != nil {
			return err
		}
		before, err := loadPermission(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := canCancel(ctx, tx, claims.UserID, before.UserID, before.Status, true); err != nil {
			return err
		}
		if before.Status == "approved" {
			day, _ := parseDate(before.WorkDate)
			if err := payrollFinalized(ctx, tx, day, day); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE permission_requests SET status = 'cancelled', cancelled_by = $2, cancelled_at = now(), cancel_reason = NULLIF(trim($3), '')
			WHERE id::text = $1`, id, claims.UserID, d.Reason); err != nil {
			return err
		}
		if resp, err = loadPermission(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "permission_requests", EntityID: id, Action: "update", PerformedBy: claims.UserID,
			Before: before, After: resp, Reason: "cancelled: " + strings.TrimSpace(d.Reason)})
	})
	if err != nil {
		writeReqError(w, err, "permission request")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}
