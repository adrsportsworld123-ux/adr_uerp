package hr

// Requests an employee makes and someone with hr.manage approves (§3.78;
// migrations/075): leave, permission (a few hours off) and attendance
// regularization. Shared here: request numbers, the decision rules
// (pending only; never your own) and the error shape. The three kinds are
// in leave.go, permission.go and regularization.go.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/locale"
)

// reqErr is a refusal with its HTTP status and error code.
type reqErr struct {
	status int
	code   string
	msg    string
}

func (e *reqErr) Error() string { return e.msg }

func badRequest(format string, a ...any) error {
	return &reqErr{http.StatusBadRequest, "INVALID_REQUEST", fmt.Sprintf(format, a...)}
}

func conflict(code, format string, a ...any) error {
	return &reqErr{http.StatusConflict, code, fmt.Sprintf(format, a...)}
}

var (
	errNotPending   = conflict("REQUEST_NOT_PENDING", "this request has already been decided or cancelled")
	errSelfApproval = &reqErr{http.StatusForbidden, "SELF_APPROVAL", "you can't approve or reject your own request; someone else with hr.manage does"}
	errOtherNeedsHR = &reqErr{http.StatusForbidden, "FORBIDDEN", "you need the hr.manage permission for another employee's requests"}
	errPayrollFinal = conflict("PAYROLL_FINALIZED", "the payroll for that month is finalized; this can no longer change it")
)

// writeReqError answers a request endpoint's error.
func writeReqError(w http.ResponseWriter, err error, what string) {
	var re *reqErr
	switch {
	case errors.As(err, &re):
		httpx.Error(w, re.status, re.code, re.msg)
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", what+" not found")
	case errors.Is(err, errSessionOverlap):
		httpx.Error(w, http.StatusConflict, "SESSION_OVERLAP", "these times overlap another check-in session of this employee")
	case errors.Is(err, errBadSessionTimes):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "check-out must be after check-in")
	default:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not save the "+what)
	}
}

// nextRequestNumber: LV-00001, PR-00001, RG-00001 — one counter per
// merchant and kind, row-locked by the upsert.
func nextRequestNumber(ctx context.Context, tx pgx.Tx, kind, prefix string) (string, error) {
	var n int
	err := tx.QueryRow(ctx, `
		INSERT INTO hr_request_counters (merchant_id, kind, last_no) VALUES (current_setting('app.tenant_id')::uuid, $1, 1)
		ON CONFLICT (merchant_id, kind) DO UPDATE SET last_no = hr_request_counters.last_no + 1
		RETURNING last_no`, kind).Scan(&n)
	return fmt.Sprintf("%s-%05d", prefix, n), err
}

func hasPermission(ctx context.Context, tx pgx.Tx, userID, code string) (bool, error) {
	codes, err := authn.FetchPermissions(ctx, tx, userID)
	if err != nil {
		return false, err
	}
	return authn.HasPermission(codes, code), nil
}

// requestSubject resolves whose request this is: the caller's own, or
// (with hr.manage) the employee named. It returns the employee's branch
// and their joining/exit dates.
type subject struct {
	userID, name string
	branchID     *string
	joining      *time.Time
	exit         *time.Time
}

func requestSubject(ctx context.Context, tx pgx.Tx, caller, named string) (subject, error) {
	s := subject{userID: caller}
	if named != "" && named != caller {
		ok, err := hasPermission(ctx, tx, caller, "hr.manage")
		if err != nil {
			return s, err
		}
		if !ok {
			return s, errOtherNeedsHR
		}
		s.userID = named
	}
	err := tx.QueryRow(ctx, `SELECT name, branch_id::text, date_of_joining, date_of_exit FROM users WHERE id::text = $1`, s.userID).
		Scan(&s.name, &s.branchID, &s.joining, &s.exit)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, &reqErr{http.StatusNotFound, "NOT_FOUND", "employee not found"}
	}
	return s, err
}

// listScope: whose requests a list shows. mine, or no hr.manage: the
// caller's own; otherwise everyone's, or the employee named.
func listScope(ctx context.Context, tx pgx.Tx, caller string, mine bool, named string) (string, error) {
	if mine {
		return caller, nil
	}
	ok, err := hasPermission(ctx, tx, caller, "hr.manage")
	if err != nil {
		return "", err
	}
	if !ok {
		if named != "" && named != caller {
			return "", errOtherNeedsHR
		}
		return caller, nil
	}
	return named, nil
}

// merchantToday is today on the merchant's clock (migrations/064).
func merchantToday(ctx context.Context, tx pgx.Tx) time.Time {
	now := time.Now().In(locale.Location(ctx, tx))
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

// payrollFinalized: a finalized payroll run covers some day in [from, to].
func payrollFinalized(ctx context.Context, tx pgx.Tx, from, to time.Time) error {
	var final bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM payroll_runs WHERE status = 'finalized'
		  AND make_date(period_year, period_month, 1) BETWEEN date_trunc('month', $1::date)::date AND $2::date)`, from, to).Scan(&final); err != nil {
		return err
	}
	if final {
		return errPayrollFinal
	}
	return nil
}

// decision is what approve / reject / cancel take.
type decision struct {
	Note   string `json:"note"`
	Reason string `json:"reason"`
}

func parseDate(s string) (time.Time, bool) {
	t, err := time.Parse("2006-01-02", s)
	return t, err == nil
}

// validSource: where a request was made; web unless the client says.
func validSource(s string) (string, bool) {
	switch s {
	case "":
		return "web", true
	case "pos", "web", "api":
		return s, true
	}
	return "", false
}
