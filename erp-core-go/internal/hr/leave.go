package hr

// Leave (§3.78; migrations/075): leave types with a yearly quota, an
// employee's balance, and leave requests. An approved request marks its
// days on the attendance rows; payroll docks the unpaid part.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
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

// maxLeaveSpan: one request covers at most this many days.
const maxLeaveSpan = 62

type leaveType struct {
	ID           string   `json:"leave_type_id"`
	Code         string   `json:"code"`
	Name         string   `json:"name"`
	Paid         bool     `json:"paid"`
	AnnualQuota  *float64 `json:"annual_quota"` // days a leave year; null = no limit
	AllowHalfDay bool     `json:"allow_half_day"`
	Status       string   `json:"status"`
	SortOrder    int      `json:"sort_order"`
}

const leaveTypeSelect = `SELECT id::text, code, name, paid, annual_quota::float8, allow_half_day, status, sort_order FROM leave_types`

func scanLeaveType(row pgx.Row, t *leaveType) error {
	return row.Scan(&t.ID, &t.Code, &t.Name, &t.Paid, &t.AnnualQuota, &t.AllowHalfDay, &t.Status, &t.SortOrder)
}

// ListLeaveTypes: GET /hr/leave-types — everyone (an employee picks one).
func (h *Handler) ListLeaveTypes(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	out := []leaveType{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, leaveTypeSelect+` ORDER BY status, sort_order, name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var t leaveType
			if err := scanLeaveType(rows, &t); err != nil {
				return err
			}
			out = append(out, t)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list leave types")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"leave_types": out})
}

type leaveTypeInput struct {
	Code         *string  `json:"code"`
	Name         *string  `json:"name"`
	Paid         *bool    `json:"paid"`
	AnnualQuota  *float64 `json:"annual_quota"`
	NoLimit      bool     `json:"no_limit"` // PATCH: clear the quota
	AllowHalfDay *bool    `json:"allow_half_day"`
	Status       *string  `json:"status"`
	SortOrder    *int     `json:"sort_order"`
}

func (in leaveTypeInput) check() error {
	if in.Code != nil && (strings.TrimSpace(*in.Code) == "" || len(*in.Code) > 10) {
		return badRequest("code is 1 to 10 characters")
	}
	if in.Name != nil && strings.TrimSpace(*in.Name) == "" {
		return badRequest("name is required")
	}
	if in.AnnualQuota != nil && (*in.AnnualQuota < 0 || *in.AnnualQuota > 366 || math.Mod(*in.AnnualQuota*2, 1) != 0) {
		return badRequest("annual_quota is 0 to 366 days, in half days")
	}
	if in.Status != nil && *in.Status != "active" && *in.Status != "inactive" {
		return badRequest("status must be active or inactive")
	}
	return nil
}

// CreateLeaveType: POST /hr/leave-types (hr.manage)
func (h *Handler) CreateLeaveType(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var in leaveTypeInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if in.Code == nil || in.Name == nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "code and name are required")
		return
	}
	if err := in.check(); err != nil {
		writeReqError(w, err, "leave type")
		return
	}
	paid, half, order := true, true, 0
	if in.Paid != nil {
		paid = *in.Paid
	}
	if in.AllowHalfDay != nil {
		half = *in.AllowHalfDay
	}
	if in.SortOrder != nil {
		order = *in.SortOrder
	}
	var t leaveType
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var id string
		if err := tx.QueryRow(ctx, `
			INSERT INTO leave_types (merchant_id, code, name, paid, annual_quota, allow_half_day, sort_order)
			VALUES (current_setting('app.tenant_id')::uuid, upper($1), $2, $3, $4, $5, $6) RETURNING id::text`,
			strings.TrimSpace(*in.Code), strings.TrimSpace(*in.Name), paid, in.AnnualQuota, half, order).Scan(&id); err != nil {
			if isUniqueViolation(err) {
				return conflict("LEAVE_TYPE_EXISTS", "a leave type with code %s already exists", strings.ToUpper(strings.TrimSpace(*in.Code)))
			}
			return err
		}
		if err := scanLeaveType(tx.QueryRow(ctx, leaveTypeSelect+` WHERE id::text = $1`, id), &t); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "leave_types", EntityID: id, Action: "create", PerformedBy: claims.UserID, After: t})
	})
	if err != nil {
		writeReqError(w, err, "leave type")
		return
	}
	httpx.JSON(w, http.StatusCreated, t)
}

// UpdateLeaveType: PATCH /hr/leave-types/{id} (hr.manage). A change of
// quota applies to balances from now on; approved leave keeps how it was
// paid.
func (h *Handler) UpdateLeaveType(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var in leaveTypeInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if err := in.check(); err != nil {
		writeReqError(w, err, "leave type")
		return
	}
	var t leaveType
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var before leaveType
		if err := scanLeaveType(tx.QueryRow(ctx, leaveTypeSelect+` WHERE id::text = $1 FOR UPDATE`, id), &before); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE leave_types SET code = COALESCE(upper(NULLIF(trim($2), '')), code), name = COALESCE(NULLIF(trim($3), ''), name),
			  paid = COALESCE($4, paid),
			  annual_quota = CASE WHEN $5 THEN NULL WHEN $6::numeric IS NOT NULL THEN $6::numeric ELSE annual_quota END,
			  allow_half_day = COALESCE($7, allow_half_day), status = COALESCE($8, status), sort_order = COALESCE($9, sort_order)
			WHERE id::text = $1`, id, in.Code, in.Name, in.Paid, in.NoLimit, in.AnnualQuota, in.AllowHalfDay, in.Status, in.SortOrder); err != nil {
			if isUniqueViolation(err) {
				return conflict("LEAVE_TYPE_EXISTS", "another leave type already has that code")
			}
			return err
		}
		if err := scanLeaveType(tx.QueryRow(ctx, leaveTypeSelect+` WHERE id::text = $1`, id), &t); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "leave_types", EntityID: id, Action: "update", PerformedBy: claims.UserID, Before: before, After: t})
	})
	if err != nil {
		writeReqError(w, err, "leave type")
		return
	}
	httpx.JSON(w, http.StatusOK, t)
}

// leaveYearOf: the leave year a day falls in, named by the calendar year
// it starts in (a year starting in April: 10 Jan 2027 is in 2026).
func leaveYearOf(d time.Time, startMonth int) int {
	if int(d.Month()) < startMonth {
		return d.Year() - 1
	}
	return d.Year()
}

func leaveYearRange(year, startMonth int) (time.Time, time.Time) {
	from := time.Date(year, time.Month(startMonth), 1, 0, 0, 0, 0, time.UTC)
	return from, from.AddDate(1, 0, -1)
}

type leaveBalance struct {
	LeaveTypeID string   `json:"leave_type_id"`
	Code        string   `json:"code"`
	Name        string   `json:"name"`
	Paid        bool     `json:"paid"`
	Status      string   `json:"status"`
	Entitled    *float64 `json:"entitled"`  // this year's allowance; null = no limit
	Override    bool     `json:"override"`  // the employee's own allowance, not the type's quota
	Taken       float64  `json:"taken"`     // approved days paid from it (every approved day for an unpaid type)
	Unpaid      float64  `json:"unpaid"`    // approved days beyond the balance, unpaid
	Pending     float64  `json:"pending"`   // days waiting for approval
	Available   *float64 `json:"available"` // entitled − taken; null = no limit
}

type permissionUsage struct {
	Month            string `json:"month"` // YYYY-MM
	FreeCount        int    `json:"free_count"`
	FreeMinutes      int    `json:"free_minutes"`
	MaxMinutes       int    `json:"max_minutes"`
	UsedCount        int    `json:"used_count"`
	UsedFreeMinutes  int    `json:"used_free_minutes"`
	UnpaidMinutes    int    `json:"unpaid_minutes"`
	PendingCount     int    `json:"pending_count"`
	RemainingCount   int    `json:"remaining_count"`
	RemainingMinutes int    `json:"remaining_minutes"`
}

type balancesResponse struct {
	UserID     string          `json:"user_id"`
	Name       string          `json:"name"`
	LeaveYear  int             `json:"leave_year"`
	YearFrom   string          `json:"year_from"`
	YearTo     string          `json:"year_to"`
	Balances   []leaveBalance  `json:"balances"`
	Permission permissionUsage `json:"permission"`
}

// entitlement: the employee's allowance of a type for a leave year
// (their own allocation, else the type's quota; nil = no limit).
func entitlement(ctx context.Context, tx pgx.Tx, userID, typeID string, year int) (*float64, bool, error) {
	var own, quota *float64
	err := tx.QueryRow(ctx, `
		SELECT (SELECT days::float8 FROM leave_allocations WHERE user_id::text = $1 AND leave_type_id::text = $2 AND leave_year = $3),
		       annual_quota::float8
		FROM leave_types WHERE id::text = $2`, userID, typeID, year).Scan(&own, &quota)
	if own != nil {
		return own, true, err
	}
	return quota, false, err
}

func loadBalances(ctx context.Context, tx pgx.Tx, userID string, year int) ([]leaveBalance, error) {
	rows, err := tx.Query(ctx, `
		SELECT t.id::text, t.code, t.name, t.paid, t.status,
		       COALESCE(al.days, t.annual_quota)::float8, al.days IS NOT NULL,
		       COALESCE(sum(CASE WHEN r.status = 'approved' THEN CASE WHEN t.paid THEN r.paid_days ELSE r.days END END), 0)::float8,
		       COALESCE(sum(CASE WHEN r.status = 'approved' AND t.paid THEN r.unpaid_days END), 0)::float8,
		       COALESCE(sum(CASE WHEN r.status = 'pending' THEN r.days END), 0)::float8
		FROM leave_types t
		LEFT JOIN leave_allocations al ON al.leave_type_id = t.id AND al.user_id::text = $1 AND al.leave_year = $2
		LEFT JOIN leave_requests r ON r.leave_type_id = t.id AND r.user_id::text = $1 AND r.leave_year = $2
		GROUP BY t.id, al.days
		ORDER BY t.status, t.sort_order, t.name`, userID, year)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []leaveBalance{}
	for rows.Next() {
		var b leaveBalance
		if err := rows.Scan(&b.LeaveTypeID, &b.Code, &b.Name, &b.Paid, &b.Status, &b.Entitled, &b.Override, &b.Taken, &b.Unpaid, &b.Pending); err != nil {
			return nil, err
		}
		if !b.Paid {
			b.Entitled = nil
		}
		if b.Entitled != nil {
			a := math.Max(*b.Entitled-b.Taken, 0)
			b.Available = &a
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func loadPermissionUsage(ctx context.Context, tx pgx.Tx, s attendanceSettings, userID string, month time.Time) (permissionUsage, error) {
	u := permissionUsage{Month: month.Format("2006-01"), FreeCount: s.PermissionFreeCount, FreeMinutes: s.PermissionFreeMinutes, MaxMinutes: s.PermissionMaxMinutes}
	err := tx.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status = 'approved'),
		       COALESCE(sum(paid_minutes) FILTER (WHERE status = 'approved'), 0),
		       COALESCE(sum(unpaid_minutes) FILTER (WHERE status = 'approved'), 0),
		       count(*) FILTER (WHERE status = 'pending')
		FROM permission_requests
		WHERE user_id::text = $1 AND date_trunc('month', work_date) = date_trunc('month', $2::date)`, userID, month).
		Scan(&u.UsedCount, &u.UsedFreeMinutes, &u.UnpaidMinutes, &u.PendingCount)
	u.RemainingCount = max(u.FreeCount-u.UsedCount, 0)
	u.RemainingMinutes = max(u.FreeMinutes-u.UsedFreeMinutes, 0)
	if u.RemainingCount == 0 {
		u.RemainingMinutes = 0
	}
	return u, err
}

// LeaveBalances: GET /hr/leave-balances?user_id=&year=&month=YYYY-MM —
// the caller's own; another employee's with hr.manage. year is a leave
// year (default: today's); month is the permission month (default: this one).
func (h *Handler) LeaveBalances(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	q := r.URL.Query()
	var resp balancesResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		s, err := requestSubject(ctx, tx, claims.UserID, q.Get("user_id"))
		if err != nil {
			return err
		}
		settings, err := loadAttendanceSettings(ctx, tx)
		if err != nil {
			return err
		}
		today := merchantToday(ctx, tx)
		resp.UserID, resp.Name = s.userID, s.name
		resp.LeaveYear = leaveYearOf(today, settings.LeaveYearStartMonth)
		if y := q.Get("year"); y != "" {
			n, err := strconv.Atoi(y)
			if err != nil || n < 2000 || n > 2200 {
				return badRequest("year must be like 2026")
			}
			resp.LeaveYear = n
		}
		from, to := leaveYearRange(resp.LeaveYear, settings.LeaveYearStartMonth)
		resp.YearFrom, resp.YearTo = from.Format("2006-01-02"), to.Format("2006-01-02")
		if resp.Balances, err = loadBalances(ctx, tx, s.userID, resp.LeaveYear); err != nil {
			return err
		}
		month := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
		if m := q.Get("month"); m != "" {
			t, err := time.Parse("2006-01", m)
			if err != nil {
				return badRequest("month must be YYYY-MM")
			}
			month = t
		}
		resp.Permission, err = loadPermissionUsage(ctx, tx, settings, s.userID, month)
		return err
	})
	if err != nil {
		writeReqError(w, err, "leave balance")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// SetLeaveAllocation: PUT /hr/leave-balances {user_id, leave_type_id,
// leave_year, days, note} (hr.manage) — an employee's own allowance for
// a year (a late joiner, a carried-forward balance). days null removes it,
// so the type's quota applies again.
func (h *Handler) SetLeaveAllocation(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req struct {
		UserID      string   `json:"user_id"`
		LeaveTypeID string   `json:"leave_type_id"`
		LeaveYear   int      `json:"leave_year"`
		Days        *float64 `json:"days"`
		Note        string   `json:"note"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.UserID == "" || req.LeaveTypeID == "" || req.LeaveYear < 2000 || req.LeaveYear > 2200 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "user_id, leave_type_id and leave_year are required")
		return
	}
	if req.Days != nil && (*req.Days < 0 || *req.Days > 366 || math.Mod(*req.Days*2, 1) != 0) {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "days is 0 to 366, in half days")
		return
	}
	var out []leaveBalance
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var known bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id::text = $1) AND EXISTS (SELECT 1 FROM leave_types WHERE id::text = $2)`,
			req.UserID, req.LeaveTypeID).Scan(&known); err != nil {
			return err
		}
		if !known {
			return pgx.ErrNoRows
		}
		before, _, err := entitlement(ctx, tx, req.UserID, req.LeaveTypeID, req.LeaveYear)
		if err != nil {
			return err
		}
		if req.Days == nil {
			if _, err := tx.Exec(ctx, `DELETE FROM leave_allocations WHERE user_id::text = $1 AND leave_type_id::text = $2 AND leave_year = $3`,
				req.UserID, req.LeaveTypeID, req.LeaveYear); err != nil {
				return err
			}
		} else if _, err := tx.Exec(ctx, `
			INSERT INTO leave_allocations (merchant_id, user_id, leave_type_id, leave_year, days, note, set_by)
			VALUES (current_setting('app.tenant_id')::uuid, $1::uuid, $2::uuid, $3, $4, NULLIF(trim($5), ''), $6)
			ON CONFLICT (user_id, leave_type_id, leave_year) DO UPDATE SET days = EXCLUDED.days, note = EXCLUDED.note,
			    set_by = EXCLUDED.set_by, set_at = now()`,
			req.UserID, req.LeaveTypeID, req.LeaveYear, *req.Days, req.Note, claims.UserID); err != nil {
			return err
		}
		if out, err = loadBalances(ctx, tx, req.UserID, req.LeaveYear); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "leave_allocations", EntityID: req.UserID, Action: "update", PerformedBy: claims.UserID,
			Before: map[string]any{"leave_type_id": req.LeaveTypeID, "leave_year": req.LeaveYear, "days": before},
			After:  map[string]any{"leave_type_id": req.LeaveTypeID, "leave_year": req.LeaveYear, "days": req.Days}, Reason: strings.TrimSpace(req.Note)})
	})
	if err != nil {
		writeReqError(w, err, "employee or leave type")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"user_id": req.UserID, "leave_year": req.LeaveYear, "balances": out})
}

// ---------------------------------------------------------------------
// Leave requests
// ---------------------------------------------------------------------

type leaveRequest struct {
	ID           string   `json:"leave_request_id"`
	Number       string   `json:"request_number"`
	UserID       string   `json:"user_id"`
	Employee     string   `json:"employee"`
	EmployeeCode *string  `json:"employee_code"`
	BranchID     *string  `json:"branch_id"`
	Branch       *string  `json:"branch"`
	LeaveTypeID  string   `json:"leave_type_id"`
	LeaveType    string   `json:"leave_type"`
	LeaveCode    string   `json:"leave_code"`
	Paid         bool     `json:"paid_type"`
	LeaveYear    int      `json:"leave_year"`
	FromDate     string   `json:"from_date"`
	ToDate       string   `json:"to_date"`
	HalfDay      bool     `json:"half_day"`
	Days         float64  `json:"days"`
	Reason       string   `json:"reason"`
	Status       string   `json:"status"`
	Source       string   `json:"source"`
	RequestedBy  *string  `json:"requested_by"`
	RequestedAt  string   `json:"requested_at"`
	PaidDays     *float64 `json:"paid_days"`
	UnpaidDays   *float64 `json:"unpaid_days"`
	DecidedBy    *string  `json:"decided_by"`
	DecidedAt    *string  `json:"decided_at"`
	DecisionNote *string  `json:"decision_note"`
	CancelledBy  *string  `json:"cancelled_by"`
	CancelledAt  *string  `json:"cancelled_at"`
	CancelReason *string  `json:"cancel_reason"`
}

const leaveRequestSelect = `
	SELECT r.id::text, r.request_number, r.user_id::text, u.name, u.employee_code, r.branch_id::text, b.name,
	       r.leave_type_id::text, t.name, t.code, t.paid, r.leave_year, r.from_date::text, r.to_date::text, r.half_day, r.days::float8,
	       r.reason, r.status, r.source, rb.name, r.requested_at::text, r.paid_days::float8, r.unpaid_days::float8,
	       db.name, r.decided_at::text, r.decision_note, cb.name, r.cancelled_at::text, r.cancel_reason
	FROM leave_requests r
	JOIN users u ON u.id = r.user_id
	JOIN leave_types t ON t.id = r.leave_type_id
	LEFT JOIN branches b ON b.id = r.branch_id
	LEFT JOIN users rb ON rb.id = r.requested_by
	LEFT JOIN users db ON db.id = r.decided_by
	LEFT JOIN users cb ON cb.id = r.cancelled_by`

func scanLeaveRequest(row pgx.Row, l *leaveRequest) error {
	return row.Scan(&l.ID, &l.Number, &l.UserID, &l.Employee, &l.EmployeeCode, &l.BranchID, &l.Branch,
		&l.LeaveTypeID, &l.LeaveType, &l.LeaveCode, &l.Paid, &l.LeaveYear, &l.FromDate, &l.ToDate, &l.HalfDay, &l.Days,
		&l.Reason, &l.Status, &l.Source, &l.RequestedBy, &l.RequestedAt, &l.PaidDays, &l.UnpaidDays,
		&l.DecidedBy, &l.DecidedAt, &l.DecisionNote, &l.CancelledBy, &l.CancelledAt, &l.CancelReason)
}

func loadLeaveRequest(ctx context.Context, tx pgx.Tx, id string) (leaveRequest, error) {
	var l leaveRequest
	return l, scanLeaveRequest(tx.QueryRow(ctx, leaveRequestSelect+` WHERE r.id::text = $1`, id), &l)
}

// CreateLeaveRequest: POST /hr/leave-requests {user_id?, leave_type_id,
// from_date, to_date, half_day?, reason, source?} — the caller's own; for
// another employee with hr.manage.
func (h *Handler) CreateLeaveRequest(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req struct {
		UserID      string `json:"user_id"`
		LeaveTypeID string `json:"leave_type_id"`
		FromDate    string `json:"from_date"`
		ToDate      string `json:"to_date"`
		HalfDay     bool   `json:"half_day"`
		Reason      string `json:"reason"`
		Source      string `json:"source"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.ToDate == "" {
		req.ToDate = req.FromDate
	}
	from, ok1 := parseDate(req.FromDate)
	to, ok2 := parseDate(req.ToDate)
	source, ok3 := validSource(req.Source)
	switch {
	case !ok1 || !ok2 || req.LeaveTypeID == "":
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "leave_type_id and from_date (YYYY-MM-DD) are required")
		return
	case to.Before(from):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "to_date can't be before from_date")
		return
	case int(to.Sub(from).Hours()/24)+1 > maxLeaveSpan:
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "one request covers at most 62 days")
		return
	case req.HalfDay && !to.Equal(from):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "a half day is a single date")
		return
	case strings.TrimSpace(req.Reason) == "":
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "a reason is required")
		return
	case !ok3:
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "source must be pos, web or api")
		return
	}
	days := float64(int(to.Sub(from).Hours()/24) + 1)
	if req.HalfDay {
		days = 0.5
	}
	var resp leaveRequest
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		s, err := requestSubject(ctx, tx, claims.UserID, req.UserID)
		if err != nil {
			return err
		}
		if s.joining != nil && from.Before(*s.joining) {
			return badRequest("%s joined on %s", s.name, s.joining.Format("02 Jan 2006"))
		}
		if s.exit != nil && to.After(*s.exit) {
			return badRequest("%s left on %s", s.name, s.exit.Format("02 Jan 2006"))
		}
		var t leaveType
		if err := scanLeaveType(tx.QueryRow(ctx, leaveTypeSelect+` WHERE id::text = $1`, req.LeaveTypeID), &t); errors.Is(err, pgx.ErrNoRows) {
			return badRequest("choose a leave type")
		} else if err != nil {
			return err
		}
		if t.Status != "active" {
			return badRequest("%s is no longer in use", t.Name)
		}
		if req.HalfDay && !t.AllowHalfDay {
			return badRequest("%s can't be taken as a half day", t.Name)
		}
		settings, err := loadAttendanceSettings(ctx, tx)
		if err != nil {
			return err
		}
		year := leaveYearOf(from, settings.LeaveYearStartMonth)
		if leaveYearOf(to, settings.LeaveYearStartMonth) != year {
			_, end := leaveYearRange(year, settings.LeaveYearStartMonth)
			return badRequest("the leave year ends on %s; ask for the days after it separately", end.Format("02 Jan 2006"))
		}
		if err := lockPunches(ctx, tx, s.userID); err != nil {
			return err
		}
		var clash string
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE((SELECT request_number FROM leave_requests WHERE user_id::text = $1 AND status IN ('pending', 'approved')
			  AND daterange(from_date, to_date, '[]') && daterange($2::date, $3::date, '[]') LIMIT 1), '')`,
			s.userID, from, to).Scan(&clash); err != nil {
			return err
		}
		if clash != "" {
			return conflict("LEAVE_OVERLAP", "these dates overlap leave request %s", clash)
		}
		number, err := nextRequestNumber(ctx, tx, "leave", "LV")
		if err != nil {
			return err
		}
		var id string
		if err := tx.QueryRow(ctx, `
			INSERT INTO leave_requests (merchant_id, request_number, user_id, branch_id, leave_type_id, leave_year, from_date, to_date,
			    half_day, days, reason, source, requested_by)
			VALUES (current_setting('app.tenant_id')::uuid, $1, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7, $8, $9, $10, $11, $12)
			RETURNING id::text`,
			number, s.userID, s.branchID, t.ID, year, from, to, req.HalfDay, days, strings.TrimSpace(req.Reason), source, claims.UserID).Scan(&id); err != nil {
			return err
		}
		if resp, err = loadLeaveRequest(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "leave_requests", EntityID: id, Action: "create", PerformedBy: claims.UserID, After: resp})
	})
	if err != nil {
		writeReqError(w, err, "leave request")
		return
	}
	httpx.JSON(w, http.StatusCreated, resp)
}

// ListLeaveRequests: GET /hr/leave-requests?mine=1&user_id=&status=&from=&to=
// — the caller's own; with hr.manage everyone's in their branch scope.
func (h *Handler) ListLeaveRequests(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	q := r.URL.Query()
	out := []leaveRequest{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		who, err := listScope(ctx, tx, claims.UserID, q.Get("mine") == "1" || q.Get("mine") == "true", q.Get("user_id"))
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, leaveRequestSelect+`
			WHERE ($1 = '' OR r.user_id::text = $1) AND ($2 = '' OR r.status = $2)
			  AND ($3 = '' OR r.to_date >= $3::date) AND ($4 = '' OR r.from_date <= $4::date)
			ORDER BY r.status = 'pending' DESC, r.from_date DESC, r.request_number DESC LIMIT 500`,
			who, q.Get("status"), dateOrEmpty(q.Get("from")), dateOrEmpty(q.Get("to")))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var l leaveRequest
			if err := scanLeaveRequest(rows, &l); err != nil {
				return err
			}
			out = append(out, l)
		}
		return rows.Err()
	})
	if err != nil {
		writeReqError(w, err, "leave requests")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"leave_requests": out})
}

// dateOrEmpty: a YYYY-MM-DD filter, or "" (no filter) when it isn't one.
func dateOrEmpty(s string) string {
	if _, ok := parseDate(s); ok {
		return s
	}
	return ""
}

func readDecision(r *http.Request) (decision, error) {
	var d decision
	if r.ContentLength == 0 {
		return d, nil
	}
	err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&d)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	return d, err
}

// ApproveLeave: POST /hr/leave-requests/{id}/approve {note?} (hr.manage;
// never your own). Each day is marked on the employee's attendance:
// paid while the type's balance lasts (earliest days first), unpaid after.
func (h *Handler) ApproveLeave(w http.ResponseWriter, r *http.Request) {
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
	var resp leaveRequest
	err = h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := lockLeaveRequest(ctx, tx, id, claims.UserID)
		if err != nil {
			return err
		}
		from, _ := parseDate(before.FromDate)
		to, _ := parseDate(before.ToDate)
		if err := payrollFinalized(ctx, tx, from, to); err != nil {
			return err
		}
		if err := lockPunches(ctx, tx, before.UserID); err != nil {
			return err
		}
		// The paid budget: what is left of the type's balance this leave
		// year (no limit: all of it); none for an unpaid type.
		budget := 0.0
		if before.Paid {
			ent, _, err := entitlement(ctx, tx, before.UserID, before.LeaveTypeID, before.LeaveYear)
			if err != nil {
				return err
			}
			budget = before.Days
			if ent != nil {
				var taken float64
				if err := tx.QueryRow(ctx, `
					SELECT COALESCE(sum(paid_days), 0)::float8 FROM leave_requests
					WHERE user_id::text = $1 AND leave_type_id::text = $2 AND leave_year = $3 AND status = 'approved'`,
					before.UserID, before.LeaveTypeID, before.LeaveYear).Scan(&taken); err != nil {
					return err
				}
				budget = math.Min(before.Days, math.Max(*ent-taken, 0))
			}
		}
		paidTotal := budget
		note := "Leave " + before.Number + " (" + before.LeaveType + ")"
		for day := from; !day.After(to); day = day.AddDate(0, 0, 1) {
			dd := 1.0
			if before.HalfDay {
				dd = 0.5
			}
			paid := math.Min(dd, budget)
			budget -= paid
			if err := markLeaveDay(ctx, tx, before, day, dd, dd-paid, claims.UserID, note); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE leave_requests SET status = 'approved', paid_days = $2, unpaid_days = $3, decided_by = $4, decided_at = now(),
			  decision_note = NULLIF(trim($5), '') WHERE id::text = $1`,
			id, paidTotal, before.Days-paidTotal, claims.UserID, d.Note); err != nil {
			return err
		}
		if resp, err = loadLeaveRequest(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "leave_requests", EntityID: id, Action: "update", PerformedBy: claims.UserID,
			Before: before, After: resp, Reason: "approved"})
	})
	if err != nil {
		writeReqError(w, err, "leave request")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// lockLeaveRequest: a pending request someone other than its employee may decide.
func lockLeaveRequest(ctx context.Context, tx pgx.Tx, id, decider string) (leaveRequest, error) {
	if _, err := tx.Exec(ctx, `SELECT 1 FROM leave_requests WHERE id::text = $1 FOR UPDATE`, id); err != nil {
		return leaveRequest{}, err
	}
	l, err := loadLeaveRequest(ctx, tx, id)
	if err != nil {
		return l, err
	}
	if l.Status != "pending" {
		return l, errNotPending
	}
	if l.UserID == decider {
		return l, errSelfApproval
	}
	return l, nil
}

// markLeaveDay puts one approved leave day on the employee's attendance:
// a new row (status leave), or the day's existing row, remembering its
// status so a cancel puts it back. A full day already worked (it has a
// check-in) is refused; a half day may share a worked day.
func markLeaveDay(ctx context.Context, tx pgx.Tx, l leaveRequest, day time.Time, days, unpaid float64, by, note string) error {
	var attID, status string
	var other *string
	var worked bool
	err := tx.QueryRow(ctx, `
		SELECT a.id::text, a.status, a.leave_request_id::text, EXISTS (SELECT 1 FROM attendance_sessions s WHERE s.attendance_id = a.id)
		FROM attendance a WHERE a.user_id::text = $1 AND a.work_date = $2 FOR UPDATE`, l.UserID, day).Scan(&attID, &status, &other, &worked)
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, `
			INSERT INTO attendance (merchant_id, user_id, branch_id, work_date, status, leave_request_id, leave_days, leave_unpaid_days,
			    corrected_by, corrected_at, correction_reason)
			VALUES (current_setting('app.tenant_id')::uuid, $1::uuid, $2::uuid, $3, 'leave', $4::uuid, $5, $6, $7, now(), $8)`,
			l.UserID, l.BranchID, day, l.ID, days, unpaid, by, note)
		return err
	} else if err != nil {
		return err
	}
	if other != nil {
		return conflict("LEAVE_OVERLAP", "%s is already on another approved leave", day.Format("02 Jan 2006"))
	}
	if worked && !l.HalfDay {
		return conflict("LEAVE_DAY_WORKED", "%s checked in on %s; ask for a half day, or correct the attendance first", l.Employee, day.Format("02 Jan 2006"))
	}
	_, err = tx.Exec(ctx, `
		UPDATE attendance SET leave_request_id = $2::uuid, leave_days = $3, leave_unpaid_days = $4, leave_prev_status = status,
		  status = CASE WHEN $5 THEN status ELSE 'leave' END, corrected_by = $6, corrected_at = now(), correction_reason = $7
		WHERE id::text = $1`, attID, l.ID, days, unpaid, worked, by, note)
	return err
}

// RejectLeave: POST /hr/leave-requests/{id}/reject {note} (hr.manage; never your own).
func (h *Handler) RejectLeave(w http.ResponseWriter, r *http.Request) {
	h.decideSimple(w, r, "leave_requests", "leave request", func(ctx context.Context, tx pgx.Tx, id, by string) (any, error) {
		return lockLeaveRequest(ctx, tx, id, by)
	}, func(ctx context.Context, tx pgx.Tx, id string) (any, error) { return loadLeaveRequest(ctx, tx, id) })
}

// decideSimple rejects a pending request: a note is required.
func (h *Handler) decideSimple(w http.ResponseWriter, r *http.Request, table, what string,
	lock func(ctx context.Context, tx pgx.Tx, id, by string) (any, error), load func(ctx context.Context, tx pgx.Tx, id string) (any, error)) {
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
	if strings.TrimSpace(d.Note) == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "say why it is rejected (note)")
		return
	}
	var resp any
	err = h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := lock(ctx, tx, id, claims.UserID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE `+table+` SET status = 'rejected', decided_by = $2, decided_at = now(), decision_note = trim($3) WHERE id::text = $1`,
			id, claims.UserID, d.Note); err != nil {
			return err
		}
		if resp, err = load(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: table, EntityID: id, Action: "update", PerformedBy: claims.UserID,
			Before: before, After: resp, Reason: "rejected: " + strings.TrimSpace(d.Note)})
	})
	if err != nil {
		writeReqError(w, err, what)
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// CancelLeave: POST /hr/leave-requests/{id}/cancel {reason} — the employee
// cancels their own pending request; hr.manage cancels anyone's, an
// approved one too (its days come off the attendance, unless that month's
// payroll is finalized).
func (h *Handler) CancelLeave(w http.ResponseWriter, r *http.Request) {
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
	var resp leaveRequest
	err = h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM leave_requests WHERE id::text = $1 FOR UPDATE`, id); err != nil {
			return err
		}
		before, err := loadLeaveRequest(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := canCancel(ctx, tx, claims.UserID, before.UserID, before.Status, true); err != nil {
			return err
		}
		if before.Status == "approved" {
			from, _ := parseDate(before.FromDate)
			to, _ := parseDate(before.ToDate)
			if err := payrollFinalized(ctx, tx, from, to); err != nil {
				return err
			}
			if err := unmarkLeave(ctx, tx, id); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE leave_requests SET status = 'cancelled', cancelled_by = $2, cancelled_at = now(), cancel_reason = NULLIF(trim($3), '')
			WHERE id::text = $1`, id, claims.UserID, d.Reason); err != nil {
			return err
		}
		if resp, err = loadLeaveRequest(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "leave_requests", EntityID: id, Action: "update", PerformedBy: claims.UserID,
			Before: before, After: resp, Reason: "cancelled: " + strings.TrimSpace(d.Reason)})
	})
	if err != nil {
		writeReqError(w, err, "leave request")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// canCancel: your own pending request, or anyone's with hr.manage
// (approved too when approvedToo).
func canCancel(ctx context.Context, tx pgx.Tx, caller, owner, status string, approvedToo bool) error {
	if status != "pending" && !(approvedToo && status == "approved") {
		return errNotPending
	}
	if caller == owner && status == "pending" {
		return nil
	}
	ok, err := hasPermission(ctx, tx, caller, "hr.manage")
	if err != nil {
		return err
	}
	if !ok {
		if caller == owner {
			return &reqErr{http.StatusForbidden, "FORBIDDEN", "an approved request is cancelled by HR"}
		}
		return errOtherNeedsHR
	}
	return nil
}

// unmarkLeave takes a cancelled leave's days off the attendance: a row the
// leave created (and nobody checked in on) goes; any other gets its status back.
func unmarkLeave(ctx context.Context, tx pgx.Tx, leaveID string) error {
	if _, err := tx.Exec(ctx, `
		DELETE FROM attendance a WHERE a.leave_request_id::text = $1 AND a.leave_prev_status IS NULL
		  AND NOT EXISTS (SELECT 1 FROM attendance_sessions s WHERE s.attendance_id = a.id)`, leaveID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		UPDATE attendance a SET
		  status = COALESCE(a.leave_prev_status, CASE WHEN EXISTS (SELECT 1 FROM attendance_sessions s WHERE s.attendance_id = a.id) THEN 'present' ELSE a.status END),
		  leave_request_id = NULL, leave_days = NULL, leave_unpaid_days = 0, leave_prev_status = NULL
		WHERE a.leave_request_id::text = $1`, leaveID)
	return err
}
