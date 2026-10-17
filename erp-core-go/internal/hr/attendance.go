package hr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/activity"
	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

var (
	errClockInRequired   = errors.New("no clock-in recorded for today")
	errAlreadyClockedOut = errors.New("already clocked out for today")
	errLocationRequired  = errors.New("location required")
	errSourceNotAllowed  = errors.New("punching from this client is turned off")
)

// errOutsideGeofence carries what the refusal tells the employee.
type errOutsideGeofence struct {
	distanceM, radiusM int
	branch             string
}

func (e *errOutsideGeofence) Error() string { return "outside the branch geofence" }

// punchEvidence is everything recorded about one check-in or check-out
// beyond its time (migrations/052).
type punchEvidence struct {
	Latitude   *float64 `json:"latitude,omitempty"`
	Longitude  *float64 `json:"longitude,omitempty"`
	AccuracyM  *float64 `json:"accuracy_m,omitempty"`
	IP         *string  `json:"ip,omitempty"`
	UserAgent  *string  `json:"user_agent,omitempty"`
	Source     *string  `json:"source,omitempty"`
	TerminalID *string  `json:"terminal_id,omitempty"`
	DistanceM  *int     `json:"distance_m,omitempty"`
	Geofence   *string  `json:"geofence,omitempty"` // inside | outside | unknown | not_configured
}

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

	BranchID         *string       `json:"branch_id,omitempty"`
	BranchName       *string       `json:"branch_name,omitempty"`
	ClockInEvidence  punchEvidence `json:"clock_in_evidence"`
	ClockOutEvidence punchEvidence `json:"clock_out_evidence"`
	CorrectedBy      *string       `json:"corrected_by,omitempty"`
	CorrectedAt      *string       `json:"corrected_at,omitempty"`
	CorrectionReason *string       `json:"correction_reason,omitempty"`
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
// Punch times are compared with the shift in the branch's own timezone
// (the database's when the row has no branch), so the result doesn't
// depend on the server's TimeZone setting.
const attendanceSelect = `
	SELECT a.id, a.user_id, a.work_date::text, a.clock_in::text, a.clock_out::text, a.status, a.hours_worked::text,
	       CASE WHEN a.clock_in IS NOT NULL AND s.start_time IS NOT NULL
	            THEN GREATEST(0, ROUND(EXTRACT(EPOCH FROM ((a.clock_in AT TIME ZONE z.tz)::time - s.start_time)) / 60)::int)
	       END,
	       CASE WHEN a.clock_out IS NOT NULL AND s.end_time IS NOT NULL
	            THEN GREATEST(0, ROUND(EXTRACT(EPOCH FROM (s.end_time - (a.clock_out AT TIME ZONE z.tz)::time)) / 60)::int)
	       END,
	       a.branch_id::text, b.name,
	       a.clock_in_latitude::float8, a.clock_in_longitude::float8, a.clock_in_accuracy_m::float8, a.clock_in_ip,
	       a.clock_in_user_agent, a.clock_in_source, a.clock_in_terminal_id::text, a.clock_in_distance_m, a.clock_in_geofence,
	       a.clock_out_latitude::float8, a.clock_out_longitude::float8, a.clock_out_accuracy_m::float8, a.clock_out_ip,
	       a.clock_out_user_agent, a.clock_out_source, a.clock_out_terminal_id::text, a.clock_out_distance_m, a.clock_out_geofence,
	       a.corrected_by::text, a.corrected_at::text, a.correction_reason
	FROM attendance a
	LEFT JOIN users u ON u.id = a.user_id
	LEFT JOIN shifts s ON s.id = u.shift_id
	LEFT JOIN branches b ON b.id = a.branch_id
	CROSS JOIN LATERAL (SELECT COALESCE(b.timezone, current_setting('TimeZone')) AS tz) z`

func scanAttendance(row pgx.Row, a *attendanceResponse) error {
	in, out := &a.ClockInEvidence, &a.ClockOutEvidence
	return row.Scan(&a.ID, &a.UserID, &a.WorkDate, &a.ClockIn, &a.ClockOut, &a.Status, &a.HoursWorked,
		&a.LateMinutes, &a.EarlyDepartureMinutes, &a.BranchID, &a.BranchName,
		&in.Latitude, &in.Longitude, &in.AccuracyM, &in.IP, &in.UserAgent, &in.Source, &in.TerminalID, &in.DistanceM, &in.Geofence,
		&out.Latitude, &out.Longitude, &out.AccuracyM, &out.IP, &out.UserAgent, &out.Source, &out.TerminalID, &out.DistanceM, &out.Geofence,
		&a.CorrectedBy, &a.CorrectedAt, &a.CorrectionReason)
}

// ---------------------------------------------------------------------
// Settings and geofences
// ---------------------------------------------------------------------

type attendanceSettings struct {
	RequireLocation bool    `json:"require_location"`
	GeofenceMode    string  `json:"geofence_mode"` // off | flag | block
	AllowWeb        bool    `json:"allow_web"`
	AllowPOS        bool    `json:"allow_pos"`
	UpdatedAt       *string `json:"updated_at"`
}

func loadAttendanceSettings(ctx context.Context, tx pgx.Tx) (attendanceSettings, error) {
	s := attendanceSettings{GeofenceMode: "flag", AllowWeb: true, AllowPOS: true}
	err := tx.QueryRow(ctx, `
		SELECT require_location, geofence_mode, allow_web, allow_pos, updated_at::text
		FROM attendance_settings WHERE merchant_id = current_setting('app.tenant_id')::uuid`,
	).Scan(&s.RequireLocation, &s.GeofenceMode, &s.AllowWeb, &s.AllowPOS, &s.UpdatedAt)
	if err == pgx.ErrNoRows {
		return s, nil
	}
	return s, err
}

type geofence struct {
	BranchID   string  `json:"branch_id"`
	BranchName string  `json:"branch_name"`
	Latitude   float64 `json:"latitude"`
	Longitude  float64 `json:"longitude"`
	RadiusM    int     `json:"radius_m"`
	Enabled    bool    `json:"enabled"`
	UpdatedAt  string  `json:"updated_at"`
}

func loadGeofence(ctx context.Context, tx pgx.Tx, branchID string) (*geofence, error) {
	var g geofence
	err := tx.QueryRow(ctx, `
		SELECT g.branch_id::text, b.name, g.latitude::float8, g.longitude::float8, g.radius_m, g.enabled, g.updated_at::text
		FROM branch_geofences g JOIN branches b ON b.id = g.branch_id
		WHERE g.branch_id::text = $1`, branchID,
	).Scan(&g.BranchID, &g.BranchName, &g.Latitude, &g.Longitude, &g.RadiusM, &g.Enabled, &g.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return &g, err
}

// haversineM: great-circle distance in metres.
func haversineM(lat1, lng1, lat2, lng2 float64) float64 {
	const earthRadiusM = 6371000.0
	rad := math.Pi / 180
	dLat, dLng := (lat2-lat1)*rad, (lng2-lng1)*rad
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * earthRadiusM * math.Asin(math.Min(1, math.Sqrt(h)))
}

// classify places a punch against the branch's fence. The fix's own
// accuracy is subtracted from the distance: a punch is outside only when
// even the most favourable reading of the fix is outside.
func classify(g *geofence, ev *punchEvidence) {
	status := "not_configured"
	switch {
	case g == nil || !g.Enabled:
	case ev.Latitude == nil || ev.Longitude == nil:
		status = "unknown"
	default:
		d := haversineM(*ev.Latitude, *ev.Longitude, g.Latitude, g.Longitude)
		dm := int(math.Round(d))
		ev.DistanceM = &dm
		slack := 0.0
		if ev.AccuracyM != nil {
			slack = math.Min(*ev.AccuracyM, 1000) // a ±5 km "fix" proves nothing either way
		}
		status = "inside"
		if d-slack > float64(g.RadiusM) {
			status = "outside"
		}
	}
	ev.Geofence = &status
}

// ---------------------------------------------------------------------
// Check-in / check-out
// ---------------------------------------------------------------------

type punchRequest struct {
	Latitude  *float64 `json:"latitude"`
	Longitude *float64 `json:"longitude"`
	AccuracyM *float64 `json:"accuracy_m"`
}

// readPunch builds the evidence for a punch: the location the device
// sent, and what the server itself saw — IP, user agent, which client,
// which terminal (only if it is this merchant's).
func readPunch(r *http.Request) (punchEvidence, error) {
	var req punchRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil && err != io.EOF {
			return punchEvidence{}, fmt.Errorf("could not parse request body")
		}
	}
	if (req.Latitude == nil) != (req.Longitude == nil) {
		return punchEvidence{}, fmt.Errorf("latitude and longitude go together")
	}
	if req.Latitude != nil && (*req.Latitude < -90 || *req.Latitude > 90 || *req.Longitude < -180 || *req.Longitude > 180) {
		return punchEvidence{}, fmt.Errorf("latitude or longitude is out of range")
	}
	if req.Latitude != nil && *req.Latitude == 0 && *req.Longitude == 0 {
		return punchEvidence{}, fmt.Errorf("0,0 is not a real location — the device didn't get a fix")
	}
	if req.AccuracyM != nil && (*req.AccuracyM < 0 || *req.AccuracyM > 1e6) {
		req.AccuracyM = nil
	}
	ip, ua, src := activity.ClientIP(r), r.UserAgent(), activity.Client(r)
	if len(ua) > 400 {
		ua = ua[:400]
	}
	ev := punchEvidence{Latitude: req.Latitude, Longitude: req.Longitude, AccuracyM: req.AccuracyM, IP: &ip, UserAgent: &ua, Source: &src}
	if t := strings.TrimSpace(r.Header.Get("X-Terminal-Id")); t != "" {
		ev.TerminalID = &t
	}
	return ev, nil
}

// punchContext resolves the branch the punch counts against — the POS
// terminal's (its token is branch-bound) or else the employee's home
// branch — checks the merchant's rules and places the punch on the fence.
func punchContext(ctx context.Context, tx pgx.Tx, userID, tokenBranchID string, ev *punchEvidence) (branchID *string, err error) {
	s, err := loadAttendanceSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	switch *ev.Source {
	case "pos":
		if !s.AllowPOS {
			return nil, errSourceNotAllowed
		}
	case "web":
		if !s.AllowWeb {
			return nil, errSourceNotAllowed
		}
	}
	if ev.TerminalID != nil {
		var termBranch string
		if err := tx.QueryRow(ctx, `SELECT branch_id::text FROM pos_terminals WHERE id::text = $1`, *ev.TerminalID).Scan(&termBranch); err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				return nil, err
			}
			ev.TerminalID = nil // not one of this merchant's terminals: don't record it
		} else if tokenBranchID == "" {
			tokenBranchID = termBranch
		}
	}
	if tokenBranchID != "" {
		branchID = &tokenBranchID
	} else if err := tx.QueryRow(ctx, `SELECT branch_id::text FROM users WHERE id = $1`, userID).Scan(&branchID); err != nil {
		return nil, err
	}

	if s.RequireLocation && ev.Latitude == nil && ev.TerminalID == nil {
		return nil, errLocationRequired
	}
	var g *geofence
	if branchID != nil && s.GeofenceMode != "off" {
		if g, err = loadGeofence(ctx, tx, *branchID); err != nil {
			return nil, err
		}
	}
	classify(g, ev)
	if s.GeofenceMode == "block" && *ev.Geofence == "outside" {
		return nil, &errOutsideGeofence{distanceM: *ev.DistanceM, radiusM: g.RadiusM, branch: g.BranchName}
	}
	if s.GeofenceMode == "block" && *ev.Geofence == "unknown" && ev.TerminalID == nil {
		// Blocking outside punches means nothing if a punch can simply
		// leave its location off. A till is placed by its pairing.
		return nil, errLocationRequired
	}
	return branchID, nil
}

func writePunchError(w http.ResponseWriter, err error, what string) {
	var outside *errOutsideGeofence
	switch {
	case errors.As(err, &outside):
		httpx.ErrorWithDetails(w, http.StatusConflict, "OUTSIDE_GEOFENCE",
			fmt.Sprintf("you are %d m from %s — check-in is allowed within %d m", outside.distanceM, outside.branch, outside.radiusM),
			map[string]any{"distance_m": outside.distanceM, "radius_m": outside.radiusM})
	case errors.Is(err, errLocationRequired):
		httpx.Error(w, http.StatusBadRequest, "LOCATION_REQUIRED", "your location is required to "+what+" — allow location access and try again")
	case errors.Is(err, errSourceNotAllowed):
		httpx.Error(w, http.StatusForbidden, "PUNCH_SOURCE_DISABLED", "your company has turned off checking in from this app")
	case errors.Is(err, errClockInRequired):
		httpx.Error(w, http.StatusConflict, "CLOCK_IN_REQUIRED", "you haven't clocked in today")
	case errors.Is(err, errAlreadyClockedOut):
		httpx.Error(w, http.StatusConflict, "ALREADY_CLOCKED_OUT", "you've already clocked out today")
	default:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not "+what)
	}
}

// ClockIn: POST /hr/attendance/clock-in {latitude?, longitude?, accuracy_m?}
// — self-service, operates on the caller's own record (claims.UserID),
// never a body-supplied employee id, so one employee can never punch in
// on another's behalf. Idempotent: a repeat call the same day leaves the
// original clock_in time (and its location) untouched (ON CONFLICT DO
// NOTHING) rather than erroring or resetting it. The body is optional
// unless the merchant requires a location; the work date is the branch's
// local date.
func (h *Handler) ClockIn(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	ev, perr := readPunch(r)
	if perr != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", perr.Error())
		return
	}

	var resp attendanceResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		branchID, err := punchContext(ctx, tx, claims.UserID, claims.BranchID, &ev)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO attendance (id, merchant_id, user_id, branch_id, work_date, clock_in, status,
			    clock_in_latitude, clock_in_longitude, clock_in_accuracy_m, clock_in_ip, clock_in_user_agent,
			    clock_in_source, clock_in_terminal_id, clock_in_distance_m, clock_in_geofence)
			SELECT gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2::uuid,
			       (now() AT TIME ZONE COALESCE((SELECT timezone FROM branches WHERE id = $2::uuid), current_setting('TimeZone')))::date,
			       now(), 'present', $3, $4, $5, $6, $7, $8, $9::uuid, $10, $11
			ON CONFLICT (user_id, work_date) DO NOTHING`,
			claims.UserID, branchID, ev.Latitude, ev.Longitude, ev.AccuracyM, ev.IP, ev.UserAgent,
			ev.Source, ev.TerminalID, ev.DistanceM, ev.Geofence); err != nil {
			return err
		}
		return loadTodayAttendance(ctx, tx, claims.UserID, &resp)
	})
	if err != nil {
		writePunchError(w, err, "clock in")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// ClockOut: POST /hr/attendance/clock-out {latitude?, longitude?, accuracy_m?}
// — self-service, same reasoning as ClockIn. Closes the caller's open
// punch from the last 20 hours rather than strictly "today's", so a shift
// that runs past midnight can still clock out.
func (h *Handler) ClockOut(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	ev, perr := readPunch(r)
	if perr != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", perr.Error())
		return
	}

	var resp attendanceResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var openID string
		err := tx.QueryRow(ctx, `
			SELECT id::text FROM attendance
			WHERE user_id = $1 AND clock_in IS NOT NULL AND clock_out IS NULL AND clock_in > now() - interval '20 hours'
			ORDER BY clock_in DESC LIMIT 1
			FOR UPDATE`, claims.UserID).Scan(&openID)
		if errors.Is(err, pgx.ErrNoRows) {
			var doneToday bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS (SELECT 1 FROM attendance WHERE user_id = $1 AND clock_out IS NOT NULL AND clock_out > now() - interval '20 hours')`,
				claims.UserID).Scan(&doneToday); err != nil {
				return err
			}
			if doneToday {
				return errAlreadyClockedOut
			}
			return errClockInRequired
		} else if err != nil {
			return err
		}
		if _, err := punchContext(ctx, tx, claims.UserID, claims.BranchID, &ev); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE attendance SET clock_out = now(),
			  hours_worked = ROUND(EXTRACT(EPOCH FROM (now() - clock_in)) / 3600.0, 2),
			  clock_out_latitude = $2, clock_out_longitude = $3, clock_out_accuracy_m = $4, clock_out_ip = $5,
			  clock_out_user_agent = $6, clock_out_source = $7, clock_out_terminal_id = $8::uuid,
			  clock_out_distance_m = $9, clock_out_geofence = $10
			WHERE id::text = $1`,
			openID, ev.Latitude, ev.Longitude, ev.AccuracyM, ev.IP, ev.UserAgent, ev.Source, ev.TerminalID,
			ev.DistanceM, ev.Geofence); err != nil {
			return err
		}
		return scanAttendance(tx.QueryRow(ctx, attendanceSelect+` WHERE a.id::text = $1`, openID), &resp)
	})
	if err != nil {
		writePunchError(w, err, "clock out")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

type myAttendanceResponse struct {
	// Open is the punch waiting for a check-out (possibly yesterday's, for
	// a shift past midnight); Today is today's row, open or closed.
	Open     *attendanceResponse `json:"open"`
	Today    *attendanceResponse `json:"today"`
	Settings attendanceSettings  `json:"settings"`
	Geofence *geofence           `json:"geofence"` // the caller's branch fence, when one is set
}

// MyAttendance: GET /hr/attendance/me — what a check-in button needs to
// render: am I checked in, and what will the merchant's rules ask of me.
func (h *Handler) MyAttendance(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var resp myAttendanceResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if resp.Settings, err = loadAttendanceSettings(ctx, tx); err != nil {
			return err
		}
		var open attendanceResponse
		err = scanAttendance(tx.QueryRow(ctx, attendanceSelect+`
			WHERE a.user_id = $1 AND a.clock_in IS NOT NULL AND a.clock_out IS NULL AND a.clock_in > now() - interval '20 hours'
			ORDER BY a.clock_in DESC LIMIT 1`, claims.UserID), &open)
		if err == nil {
			resp.Open = &open
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var today attendanceResponse
		if err = loadTodayAttendance(ctx, tx, claims.UserID, &today); err == nil {
			resp.Today = &today
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		branchID := claims.BranchID
		if branchID == "" {
			var b *string
			if err := tx.QueryRow(ctx, `SELECT branch_id::text FROM users WHERE id = $1`, claims.UserID).Scan(&b); err != nil {
				return err
			}
			if b != nil {
				branchID = *b
			}
		}
		if branchID != "" {
			g, err := loadGeofence(ctx, tx, branchID)
			if err != nil {
				return err
			}
			if g != nil && g.Enabled {
				resp.Geofence = g
			}
		}
		return nil
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load your attendance")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
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
			WHERE a.user_id::text = $1 AND ($2 = '' OR to_char(a.work_date, 'YYYY-MM') = $2)
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

type registerRow struct {
	UserID       string              `json:"user_id"`
	Name         string              `json:"name"`
	EmployeeCode *string             `json:"employee_code"`
	Designation  *string             `json:"designation"`
	HomeBranchID *string             `json:"home_branch_id"`
	Attendance   *attendanceResponse `json:"attendance"` // null: no punch and no mark that day
}

// Register: GET /hr/attendance/register?date=YYYY-MM-DD&branch_id= (hr.manage)
// — the day sheet: every current employee (of the branch, when given)
// with that day's row, so who hasn't checked in is as visible as who
// has, along with where each punch came from.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	date := r.URL.Query().Get("date")
	if date == "" {
		date = time.Now().Format("2006-01-02")
	} else if _, err := time.Parse("2006-01-02", date); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "date must be YYYY-MM-DD")
		return
	}
	branchID := r.URL.Query().Get("branch_id")

	out := []registerRow{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT u.id::text, u.name, u.employee_code, u.designation, u.branch_id::text,
			       (SELECT a.id::text FROM attendance a WHERE a.user_id = u.id AND a.work_date = $1::date)
			FROM users u
			WHERE u.status = 'active' AND u.date_of_exit IS NULL
			  AND ($2 = '' OR u.branch_id::text = $2
			       OR EXISTS (SELECT 1 FROM attendance a WHERE a.user_id = u.id AND a.work_date = $1::date AND a.branch_id::text = $2))
			ORDER BY u.name`, date, branchID)
		if err != nil {
			return err
		}
		type pending struct {
			row   registerRow
			attID *string
		}
		var list []pending
		for rows.Next() {
			var p pending
			if err := rows.Scan(&p.row.UserID, &p.row.Name, &p.row.EmployeeCode, &p.row.Designation, &p.row.HomeBranchID, &p.attID); err != nil {
				rows.Close()
				return err
			}
			list = append(list, p)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, p := range list {
			if p.attID != nil {
				var a attendanceResponse
				if err := scanAttendance(tx.QueryRow(ctx, attendanceSelect+` WHERE a.id::text = $1`, *p.attID), &a); err != nil {
					return err
				}
				p.row.Attendance = &a
			}
			out = append(out, p.row)
		}
		return nil
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load the attendance register")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"date": date, "employees": out})
}

var validStatuses = map[string]bool{"present": true, "half_day": true, "leave": true, "absent": true}

type correctAttendanceRequest struct {
	Status   string  `json:"status"`
	ClockIn  *string `json:"clock_in"`  // RFC 3339; "" clears it
	ClockOut *string `json:"clock_out"` // RFC 3339; "" clears it
	Reason   string  `json:"reason"`
}

// parseOptionalTime: nil = keep, "" = clear, otherwise RFC 3339.
func parseOptionalTime(s *string) (set bool, t *time.Time, err error) {
	if s == nil {
		return false, nil, nil
	}
	if strings.TrimSpace(*s) == "" {
		return true, nil, nil
	}
	v, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		return false, nil, err
	}
	return true, &v, nil
}

// CorrectAttendance: PATCH /hr/attendance/{id} {status?, clock_in?, clock_out?, reason}
// — HR override for a missed punch or a leave/absence marking. Gated by
// hr.manage at the router level (no self-vs-other ambiguity here, unlike
// ListAttendance). The original punch evidence stays; who corrected it,
// when and why is stamped on the row and the change goes to audit_logs.
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
	if req.Status == "" && req.ClockIn == nil && req.ClockOut == nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "status is required")
		return
	}
	if req.Status != "" && !validStatuses[req.Status] {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "status must be present, half_day, leave or absent")
		return
	}
	setIn, inAt, err1 := parseOptionalTime(req.ClockIn)
	setOut, outAt, err2 := parseOptionalTime(req.ClockOut)
	if err1 != nil || err2 != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "clock_in and clock_out must be RFC 3339 timestamps")
		return
	}

	var resp attendanceResponse
	badRange := false
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var before attendanceResponse
		if err := scanAttendance(tx.QueryRow(ctx, attendanceSelect+` WHERE a.id::text = $1 FOR UPDATE OF a`, id), &before); err != nil {
			return err
		}
		var ok bool
		if err := tx.QueryRow(ctx, `
			UPDATE attendance SET
			  status = COALESCE(NULLIF($2, ''), status),
			  clock_in = CASE WHEN $3 THEN $4::timestamptz ELSE clock_in END,
			  clock_out = CASE WHEN $5 THEN $6::timestamptz ELSE clock_out END,
			  corrected_by = $7, corrected_at = now(), correction_reason = NULLIF($8, '')
			WHERE id::text = $1
			RETURNING clock_in IS NULL OR clock_out IS NULL OR clock_out > clock_in`,
			id, req.Status, setIn, inAt, setOut, outAt, claims.UserID, strings.TrimSpace(req.Reason)).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			badRange = true
			return errors.New("clock_out before clock_in")
		}
		if _, err := tx.Exec(ctx, `
			UPDATE attendance SET hours_worked = CASE WHEN clock_in IS NOT NULL AND clock_out IS NOT NULL
			    THEN ROUND(EXTRACT(EPOCH FROM (clock_out - clock_in)) / 3600.0, 2) END
			WHERE id::text = $1`, id); err != nil {
			return err
		}
		if err := scanAttendance(tx.QueryRow(ctx, attendanceSelect+` WHERE a.id::text = $1`, id), &resp); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "attendance", EntityID: resp.ID, Action: "update",
			PerformedBy: claims.UserID, Before: before, After: resp, Reason: strings.TrimSpace(req.Reason)})
	})
	switch {
	case badRange:
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "clock_out must be after clock_in")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "no attendance record with this id")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not correct attendance")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}

type markAttendanceRequest struct {
	UserID   string  `json:"user_id"`
	WorkDate string  `json:"work_date"` // YYYY-MM-DD
	Status   string  `json:"status"`
	ClockIn  *string `json:"clock_in"`
	ClockOut *string `json:"clock_out"`
	Reason   string  `json:"reason"`
}

// MarkAttendance: POST /hr/attendance/mark (hr.manage) — a row for an
// employee who has none that day: leave, absence, or a day they forgot
// to punch at all. An existing row is corrected with PATCH instead
// (409 ATTENDANCE_EXISTS), so a mark never overwrites a real punch.
func (h *Handler) MarkAttendance(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req markAttendanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if _, err := time.Parse("2006-01-02", req.WorkDate); err != nil || req.UserID == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "user_id and work_date (YYYY-MM-DD) are required")
		return
	}
	if !validStatuses[req.Status] {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "status must be present, half_day, leave or absent")
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "a reason is required for a manual entry")
		return
	}
	_, inAt, err1 := parseOptionalTime(req.ClockIn)
	_, outAt, err2 := parseOptionalTime(req.ClockOut)
	if err1 != nil || err2 != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "clock_in and clock_out must be RFC 3339 timestamps")
		return
	}
	if inAt != nil && outAt != nil && !outAt.After(*inAt) {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "clock_out must be after clock_in")
		return
	}

	var resp attendanceResponse
	exists := false
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var id string
		err := tx.QueryRow(ctx, `
			INSERT INTO attendance (id, merchant_id, user_id, branch_id, work_date, clock_in, clock_out, status, hours_worked,
			    clock_in_source, clock_out_source, corrected_by, corrected_at, correction_reason)
			SELECT gen_random_uuid(), current_setting('app.tenant_id')::uuid, u.id, u.branch_id, $2::date, $3::timestamptz, $4::timestamptz, $5,
			       CASE WHEN $3::timestamptz IS NOT NULL AND $4::timestamptz IS NOT NULL
			            THEN ROUND(EXTRACT(EPOCH FROM ($4::timestamptz - $3::timestamptz)) / 3600.0, 2) END,
			       CASE WHEN $3::timestamptz IS NOT NULL THEN 'manual' END, CASE WHEN $4::timestamptz IS NOT NULL THEN 'manual' END,
			       $6, now(), $7
			FROM users u WHERE u.id::text = $1
			ON CONFLICT (user_id, work_date) DO NOTHING
			RETURNING id::text`,
			req.UserID, req.WorkDate, inAt, outAt, req.Status, claims.UserID, strings.TrimSpace(req.Reason)).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			var known bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id::text = $1)`, req.UserID).Scan(&known); err != nil {
				return err
			}
			if !known {
				return pgx.ErrNoRows
			}
			exists = true
			return nil
		} else if err != nil {
			return err
		}
		if err := scanAttendance(tx.QueryRow(ctx, attendanceSelect+` WHERE a.id::text = $1`, id), &resp); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "attendance", EntityID: id, Action: "create",
			PerformedBy: claims.UserID, After: resp, Reason: strings.TrimSpace(req.Reason)})
	})
	switch {
	case exists:
		httpx.Error(w, http.StatusConflict, "ATTENDANCE_EXISTS", "this employee already has an attendance row for that day — correct it instead")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "employee not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not mark attendance")
	default:
		httpx.JSON(w, http.StatusCreated, resp)
	}
}

// GetAttendanceSettings: GET /hr/attendance/settings (hr.manage)
func (h *Handler) GetAttendanceSettings(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var s attendanceSettings
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		s, err = loadAttendanceSettings(ctx, tx)
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load attendance settings")
		return
	}
	httpx.JSON(w, http.StatusOK, s)
}

// UpdateAttendanceSettings: PUT /hr/attendance/settings (hr.manage) —
// the whole settings object; omitted booleans keep their value.
func (h *Handler) UpdateAttendanceSettings(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req struct {
		RequireLocation *bool   `json:"require_location"`
		GeofenceMode    *string `json:"geofence_mode"`
		AllowWeb        *bool   `json:"allow_web"`
		AllowPOS        *bool   `json:"allow_pos"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.GeofenceMode != nil && *req.GeofenceMode != "off" && *req.GeofenceMode != "flag" && *req.GeofenceMode != "block" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "geofence_mode must be off, flag or block")
		return
	}
	var saved attendanceSettings
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := loadAttendanceSettings(ctx, tx)
		if err != nil {
			return err
		}
		next := before
		if req.RequireLocation != nil {
			next.RequireLocation = *req.RequireLocation
		}
		if req.GeofenceMode != nil {
			next.GeofenceMode = *req.GeofenceMode
		}
		if req.AllowWeb != nil {
			next.AllowWeb = *req.AllowWeb
		}
		if req.AllowPOS != nil {
			next.AllowPOS = *req.AllowPOS
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO attendance_settings (merchant_id, require_location, geofence_mode, allow_web, allow_pos, updated_by, updated_at)
			VALUES (current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, $5, now())
			ON CONFLICT (merchant_id) DO UPDATE SET require_location = EXCLUDED.require_location,
			    geofence_mode = EXCLUDED.geofence_mode, allow_web = EXCLUDED.allow_web, allow_pos = EXCLUDED.allow_pos,
			    updated_by = EXCLUDED.updated_by, updated_at = now()`,
			next.RequireLocation, next.GeofenceMode, next.AllowWeb, next.AllowPOS, claims.UserID); err != nil {
			return err
		}
		if saved, err = loadAttendanceSettings(ctx, tx); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "attendance_settings", EntityID: claims.TenantID, Action: "update",
			PerformedBy: claims.UserID, Before: before, After: saved})
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not save attendance settings")
		return
	}
	httpx.JSON(w, http.StatusOK, saved)
}

// ListGeofences: GET /hr/attendance/geofences (hr.manage) — the branches
// that have a fence.
func (h *Handler) ListGeofences(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	out := []geofence{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT g.branch_id::text, b.name, g.latitude::float8, g.longitude::float8, g.radius_m, g.enabled, g.updated_at::text
			FROM branch_geofences g JOIN branches b ON b.id = g.branch_id ORDER BY b.name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var g geofence
			if err := rows.Scan(&g.BranchID, &g.BranchName, &g.Latitude, &g.Longitude, &g.RadiusM, &g.Enabled, &g.UpdatedAt); err != nil {
				return err
			}
			out = append(out, g)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list geofences")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"geofences": out})
}

// SetGeofence: PUT /hr/attendance/geofences/{branch_id} {latitude, longitude, radius_m, enabled?}
// (hr.manage). There is no DELETE: switching a fence off is enabled=false,
// so its history stays with the branch.
func (h *Handler) SetGeofence(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	branchID := chi.URLParam(r, "branch_id")
	var req struct {
		Latitude  *float64 `json:"latitude"`
		Longitude *float64 `json:"longitude"`
		RadiusM   *int     `json:"radius_m"`
		Enabled   *bool    `json:"enabled"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.Latitude == nil || req.Longitude == nil || req.RadiusM == nil ||
		*req.Latitude < -90 || *req.Latitude > 90 || *req.Longitude < -180 || *req.Longitude > 180 ||
		(*req.Latitude == 0 && *req.Longitude == 0) {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "latitude, longitude (a real location) and radius_m are required")
		return
	}
	if *req.RadiusM < 10 || *req.RadiusM > 5000 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "radius_m must be between 10 and 5000")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	var saved *geofence
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var visible bool
		if err := tx.QueryRow(ctx, `SELECT app_branch_visible(id) FROM branches WHERE id::text = $1`, branchID).Scan(&visible); err != nil {
			return err
		}
		if !visible {
			return pgx.ErrNoRows
		}
		before, err := loadGeofence(ctx, tx, branchID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO branch_geofences (branch_id, merchant_id, latitude, longitude, radius_m, enabled, updated_by, updated_at)
			VALUES ($1::uuid, current_setting('app.tenant_id')::uuid, $2, $3, $4, $5, $6, now())
			ON CONFLICT (branch_id) DO UPDATE SET latitude = EXCLUDED.latitude, longitude = EXCLUDED.longitude,
			    radius_m = EXCLUDED.radius_m, enabled = EXCLUDED.enabled, updated_by = EXCLUDED.updated_by, updated_at = now()`,
			branchID, *req.Latitude, *req.Longitude, *req.RadiusM, enabled, claims.UserID); err != nil {
			return err
		}
		if saved, err = loadGeofence(ctx, tx, branchID); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "branch_geofences", EntityID: branchID, Action: "update",
			PerformedBy: claims.UserID, Before: before, After: saved})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "branch not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not save the geofence")
		return
	}
	httpx.JSON(w, http.StatusOK, saved)
}

func loadTodayAttendance(ctx context.Context, tx pgx.Tx, userID string, resp *attendanceResponse) error {
	row := tx.QueryRow(ctx, attendanceSelect+`
		WHERE a.user_id = $1 AND a.work_date = (now() AT TIME ZONE z.tz)::date`, userID)
	return scanAttendance(row, resp)
}
