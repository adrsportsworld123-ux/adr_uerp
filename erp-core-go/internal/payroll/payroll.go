package payroll

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/accounting"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/hr"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/loans"
)

var (
	errRunAlreadyFinalized = errors.New("payroll run is already finalized")
	errRunNotFound         = errors.New("payroll run not found")
)

type payslipResponse struct {
	UserID           string `json:"user_id"`
	Name             string `json:"name"`
	Basic            string `json:"basic"`
	HRA              string `json:"hra"`
	SpecialAllowance string `json:"special_allowance"`
	OtherAllowances  string `json:"other_allowances"`
	CommissionAmount string `json:"commission_amount"`
	GrossEarnings    string `json:"gross_earnings"`
	DaysInPeriod     string `json:"days_in_period"`
	DaysPresent      string `json:"days_present"`
	PFEmployee       string `json:"pf_employee"`
	PFEmployer       string `json:"pf_employer"`
	ESIEmployee      string `json:"esi_employee"`
	ESIEmployer      string `json:"esi_employer"`
	PTAmount         string `json:"pt_amount"`
	TDSAmount        string `json:"tds_amount"`
	LWFEmployee      string `json:"lwf_employee"`
	LWFEmployer      string `json:"lwf_employer"`
	TotalDeductions  string `json:"total_deductions"`
	NetPay           string `json:"net_pay"`
	// LoanDeduction: employee loan and advance instalments recovered from
	// this pay (migrations/067); part of total_deductions.
	LoanDeduction string `json:"loan_deduction"`
	// Leave and permission (migrations/075): days of approved leave paid
	// and unpaid, explicit absences, and permission minutes beyond the
	// month's free allowance. Unpaid leave, absences and those minutes
	// are what reduced days_present.
	LeavePaidDays           string `json:"leave_paid_days"`
	LeaveUnpaidDays         string `json:"leave_unpaid_days"`
	AbsentDays              string `json:"absent_days"`
	PermissionUnpaidMinutes int    `json:"permission_unpaid_minutes"`
	// The payslip itself and its run, for printing and self-service.
	PayslipID   string `json:"payslip_id,omitempty"`
	RunID       string `json:"payroll_run_id,omitempty"`
	PeriodMonth int    `json:"period_month,omitempty"`
	PeriodYear  int    `json:"period_year,omitempty"`
	RunStatus   string `json:"run_status,omitempty"`
}

type payrollRunResponse struct {
	ID               string            `json:"id"`
	PeriodMonth      int               `json:"period_month"`
	PeriodYear       int               `json:"period_year"`
	Status           string            `json:"status"`
	TotalGross       string            `json:"total_gross"`
	TotalDeductions  string            `json:"total_deductions"`
	TotalNet         string            `json:"total_net"`
	FinalizedAt      string            `json:"finalized_at,omitempty"`
	SkippedEmployees []string          `json:"skipped_employees,omitempty"`
	Payslips         []payslipResponse `json:"payslips,omitempty"`
}

type createRunRequest struct {
	PeriodMonth int `json:"period_month"`
	PeriodYear  int `json:"period_year"`
}

// CreateOrRecomputeRun: POST /payroll/runs — computes (or, while still
// 'draft', recomputes) every eligible employee's payslip for the period.
// An employee with no salary structure on file, or who wasn't employed
// at any point during the period (date_of_joining after it ends, or
// date_of_exit before it starts), is skipped and named in
// skipped_employees rather than silently omitted or defaulted to zero.
func (h *Handler) CreateOrRecomputeRun(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req createRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.PeriodMonth < 1 || req.PeriodMonth > 12 || req.PeriodYear < 2000 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "valid period_month (1-12) and period_year are required")
		return
	}

	periodStart := time.Date(req.PeriodYear, time.Month(req.PeriodMonth), 1, 0, 0, 0, 0, time.UTC)
	periodEnd := periodStart.AddDate(0, 1, 0).Add(-24 * time.Hour) // last day of the month
	daysInMonth := periodEnd.Day()

	var resp payrollRunResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var runID, status string
		err := tx.QueryRow(ctx, `
			INSERT INTO payroll_runs (id, merchant_id, period_month, period_year, created_by)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3)
			ON CONFLICT (merchant_id, period_month, period_year) DO UPDATE SET period_month = EXCLUDED.period_month
			RETURNING id, status`, req.PeriodMonth, req.PeriodYear, claims.UserID,
		).Scan(&runID, &status)
		if err != nil {
			return err
		}
		if status == "finalized" {
			return errRunAlreadyFinalized
		}

		cfg, err := hr.LoadStatutoryConfig(ctx, tx)
		if err != nil {
			return err
		}

		empRows, err := tx.Query(ctx, `
			SELECT id, name, date_of_joining, date_of_exit
			FROM users
			WHERE employee_code IS NOT NULL
			  AND (date_of_joining IS NULL OR date_of_joining <= $2)
			  AND (date_of_exit IS NULL OR date_of_exit >= $1)`, periodStart, periodEnd)
		if err != nil {
			return err
		}
		type emp struct {
			id, name      string
			joining, exit *time.Time
		}
		var employees []emp
		for empRows.Next() {
			var e emp
			if err := empRows.Scan(&e.id, &e.name, &e.joining, &e.exit); err != nil {
				empRows.Close()
				return err
			}
			employees = append(employees, e)
		}
		empRows.Close()
		if err := empRows.Err(); err != nil {
			return err
		}

		resp.Payslips = []payslipResponse{}
		resp.SkippedEmployees = []string{}
		var totalGross, totalDeductions, totalNet float64

		for _, e := range employees {
			var basic, hraAmt, special, other float64
			var pfApplicable bool
			serr := tx.QueryRow(ctx, `
				SELECT basic, hra, special_allowance, other_allowances, pf_applicable
				FROM salary_structures
				WHERE user_id = $1 AND effective_from <= $2
				ORDER BY effective_from DESC LIMIT 1`, e.id, periodEnd,
			).Scan(&basic, &hraAmt, &special, &other, &pfApplicable)
			if errors.Is(serr, pgx.ErrNoRows) {
				resp.SkippedEmployees = append(resp.SkippedEmployees, e.name+" (no salary structure on file)")
				continue
			} else if serr != nil {
				return serr
			}

			effStart, effEnd := periodStart, periodEnd
			if e.joining != nil && e.joining.After(effStart) {
				effStart = *e.joining
			}
			if e.exit != nil && e.exit.Before(effEnd) {
				effEnd = *e.exit
			}
			daysInPeriod := float64(int(effEnd.Sub(effStart).Hours()/24) + 1)
			if daysInPeriod > float64(daysInMonth) {
				daysInPeriod = float64(daysInMonth)
			}

			var attendedRows int
			var absentDays, halfDays, leavePaid, leaveUnpaid float64
			arows, err := tx.Query(ctx, `
				SELECT status, leave_request_id IS NOT NULL, COALESCE(leave_days, 0)::float8, leave_unpaid_days::float8
				FROM attendance WHERE user_id = $1 AND work_date BETWEEN $2 AND $3`, e.id, effStart, effEnd)
			if err != nil {
				return err
			}
			for arows.Next() {
				var st string
				var onLeave bool
				var leaveDays, unpaid float64
				if err := arows.Scan(&st, &onLeave, &leaveDays, &unpaid); err != nil {
					arows.Close()
					return err
				}
				attendedRows++
				// A day of an approved leave request (migrations/075) docks
				// only its unpaid part; HR marking it absent still docks it all.
				if onLeave && st != "absent" {
					leavePaid += leaveDays - unpaid
					leaveUnpaid += unpaid
					continue
				}
				switch st {
				case "absent":
					absentDays++
				case "half_day":
					halfDays++
				}
			}
			arows.Close()
			if err := arows.Err(); err != nil {
				return err
			}

			// Attendance-based proration only docks pay for an explicit
			// 'absent'/'half_day' record — a calendar day with no
			// attendance row at all (a weekly off, a holiday, or simply
			// an employee this system never asked to clock in/out) is
			// treated as paid, not as an unmarked absence. An employee
			// with zero attendance rows for the whole period is paid in
			// full (salaried staff this deployment doesn't clock-track).
			// Approved permission minutes beyond the month's free allowance
			// (migrations/075), as a part of the employee's working day.
			var permUnpaid int
			var dayMinutes float64
			if err := tx.QueryRow(ctx, `
				SELECT COALESCE((SELECT sum(unpaid_minutes) FROM permission_requests
				                 WHERE user_id = $1 AND status = 'approved' AND work_date BETWEEN $2 AND $3), 0)::int,
				       COALESCE((SELECT CASE WHEN s.end_time > s.start_time THEN EXTRACT(EPOCH FROM (s.end_time - s.start_time)) / 60
				                             ELSE EXTRACT(EPOCH FROM (s.end_time - s.start_time + interval '24 hours')) / 60 END
				                 FROM users u JOIN shifts s ON s.id = u.shift_id WHERE u.id = $1),
				                (SELECT standard_day_hours * 60 FROM attendance_settings WHERE merchant_id = current_setting('app.tenant_id')::uuid),
				                480)::float8`,
				e.id, effStart, effEnd).Scan(&permUnpaid, &dayMinutes); err != nil {
				return err
			}
			permDays := 0.0
			if permUnpaid > 0 && dayMinutes > 0 {
				permDays = float64(permUnpaid) / dayMinutes
			}

			daysPresent := daysInPeriod
			if attendedRows > 0 || permDays > 0 {
				daysPresent = daysInPeriod - absentDays - 0.5*halfDays - leaveUnpaid - permDays
				if daysPresent < 0 {
					daysPresent = 0
				}
			}
			daysPresent = math.Round(daysPresent*100) / 100
			prorate := daysPresent / daysInPeriod

			basicP := round2(basic * prorate)
			hraP := round2(hraAmt * prorate)
			specialP := round2(special * prorate)
			otherP := round2(other * prorate)

			var commissionAmount float64
			_ = tx.QueryRow(ctx, `
				SELECT COALESCE(SUM(commission_amount),0) FROM commission_earnings
				WHERE user_id = $1 AND period_month = $2 AND period_year = $3`,
				e.id, req.PeriodMonth, req.PeriodYear).Scan(&commissionAmount)

			fixedGross := basicP + hraP + specialP + otherP
			grossEarnings := round2(fixedGross + commissionAmount)

			var pfEmployee, pfEmployer float64
			if pfApplicable {
				pfWageBase := basicP
				if pfWageBase > cfg.PFWageCeiling {
					pfWageBase = cfg.PFWageCeiling
				}
				pfEmployee = round2(pfWageBase * cfg.PFEmployeeRate / 100)
				pfEmployer = round2(pfWageBase * cfg.PFEmployerRate / 100)
			}

			var esiEmployee, esiEmployer float64
			if fixedGross <= cfg.ESIWageCeiling && cfg.ESIWageCeiling > 0 {
				esiEmployee = round2(fixedGross * cfg.ESIEmployeeRate / 100)
				esiEmployer = round2(fixedGross * cfg.ESIEmployerRate / 100)
			}

			ptAmount := ptForGross(cfg.PTSlabs, grossEarnings)
			tdsAmount := round2(grossEarnings * cfg.TDSRatePercent / 100)

			totalDed := round2(pfEmployee + esiEmployee + ptAmount + tdsAmount + cfg.LWFEmployee)
			netPay := round2(grossEarnings - totalDed)

			// Employee loans (migrations/067): the instalments due by this
			// month that fit in the net pay; 0 for anyone without a loan.
			var existingPayslip string
			if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT id::text FROM payslips WHERE payroll_run_id = $1 AND user_id = $2), '')`,
				runID, e.id).Scan(&existingPayslip); err != nil {
				return err
			}
			loanIDs, loanDed, err := loans.PlanPayrollDeduction(ctx, tx, e.id, existingPayslip, req.PeriodMonth, req.PeriodYear, netPay)
			if err != nil {
				return err
			}
			totalDed = round2(totalDed + loanDed)
			netPay = round2(netPay - loanDed)

			var ps payslipResponse
			var payslipID string
			if err := tx.QueryRow(ctx, `
				INSERT INTO payslips (id, merchant_id, payroll_run_id, user_id, basic, hra, special_allowance, other_allowances,
				                       commission_amount, gross_earnings, days_in_period, days_present,
				                       pf_employee, pf_employer, esi_employee, esi_employer, pt_amount, tds_amount,
				                       lwf_employee, lwf_employer, total_deductions, net_pay, loan_deduction,
				                       leave_paid_days, leave_unpaid_days, permission_unpaid_minutes, absent_days)
				VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
				        $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25)
				ON CONFLICT (payroll_run_id, user_id) DO UPDATE SET
				  basic = EXCLUDED.basic, hra = EXCLUDED.hra, special_allowance = EXCLUDED.special_allowance,
				  other_allowances = EXCLUDED.other_allowances, commission_amount = EXCLUDED.commission_amount,
				  gross_earnings = EXCLUDED.gross_earnings, days_in_period = EXCLUDED.days_in_period,
				  days_present = EXCLUDED.days_present, pf_employee = EXCLUDED.pf_employee, pf_employer = EXCLUDED.pf_employer,
				  esi_employee = EXCLUDED.esi_employee, esi_employer = EXCLUDED.esi_employer, pt_amount = EXCLUDED.pt_amount,
				  tds_amount = EXCLUDED.tds_amount, lwf_employee = EXCLUDED.lwf_employee, lwf_employer = EXCLUDED.lwf_employer,
				  total_deductions = EXCLUDED.total_deductions, net_pay = EXCLUDED.net_pay, loan_deduction = EXCLUDED.loan_deduction,
				  leave_paid_days = EXCLUDED.leave_paid_days, leave_unpaid_days = EXCLUDED.leave_unpaid_days,
				  permission_unpaid_minutes = EXCLUDED.permission_unpaid_minutes, absent_days = EXCLUDED.absent_days
				RETURNING user_id, basic::text, hra::text, special_allowance::text, other_allowances::text,
				          commission_amount::text, gross_earnings::text, days_in_period::text, days_present::text,
				          pf_employee::text, pf_employer::text, esi_employee::text, esi_employer::text, pt_amount::text,
				          tds_amount::text, lwf_employee::text, lwf_employer::text, total_deductions::text, net_pay::text,
				          loan_deduction::text, id::text`,
				runID, e.id, basicP, hraP, specialP, otherP, commissionAmount, grossEarnings, daysInPeriod, daysPresent,
				pfEmployee, pfEmployer, esiEmployee, esiEmployer, ptAmount, tdsAmount, cfg.LWFEmployee, cfg.LWFEmployer, totalDed, netPay,
				loanDed, leavePaid, leaveUnpaid, permUnpaid, absentDays+0.5*halfDays,
			).Scan(&ps.UserID, &ps.Basic, &ps.HRA, &ps.SpecialAllowance, &ps.OtherAllowances, &ps.CommissionAmount,
				&ps.GrossEarnings, &ps.DaysInPeriod, &ps.DaysPresent, &ps.PFEmployee, &ps.PFEmployer, &ps.ESIEmployee,
				&ps.ESIEmployer, &ps.PTAmount, &ps.TDSAmount, &ps.LWFEmployee, &ps.LWFEmployer, &ps.TotalDeductions, &ps.NetPay,
				&ps.LoanDeduction, &payslipID,
			); err != nil {
				return err
			}
			if err := loans.AssignPayrollDeduction(ctx, tx, payslipID, loanIDs); err != nil {
				return err
			}
			ps.Name = e.name
			ps.PayslipID = payslipID
			ps.LeavePaidDays, ps.LeaveUnpaidDays = formatDays(leavePaid), formatDays(leaveUnpaid)
			ps.AbsentDays, ps.PermissionUnpaidMinutes = formatDays(absentDays+0.5*halfDays), permUnpaid
			resp.Payslips = append(resp.Payslips, ps)

			totalGross += grossEarnings
			totalDeductions += totalDed
			totalNet += netPay
		}

		return tx.QueryRow(ctx, `
			UPDATE payroll_runs SET total_gross = $2, total_deductions = $3, total_net = $4
			WHERE id = $1
			RETURNING id, period_month, period_year, status, total_gross::text, total_deductions::text, total_net::text`,
			runID, round2(totalGross), round2(totalDeductions), round2(totalNet),
		).Scan(&resp.ID, &resp.PeriodMonth, &resp.PeriodYear, &resp.Status, &resp.TotalGross, &resp.TotalDeductions, &resp.TotalNet)
	})

	switch {
	case errors.Is(err, errRunAlreadyFinalized):
		httpx.Error(w, http.StatusConflict, "PAYROLL_RUN_FINALIZED", "this period's payroll run is already finalized and can't be recomputed")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not run payroll")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}

// FinalizeRun: POST /payroll/runs/{id}/finalize — locks the run; its
// payslips stop being recomputable by a later POST /payroll/runs call
// for the same period.
func (h *Handler) FinalizeRun(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")

	var resp payrollRunResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			UPDATE payroll_runs SET status = 'finalized', finalized_at = now()
			WHERE id = $1 AND status = 'draft'
			RETURNING id, period_month, period_year, status, total_gross::text, total_deductions::text, total_net::text, finalized_at::text`,
			id,
		).Scan(&resp.ID, &resp.PeriodMonth, &resp.PeriodYear, &resp.Status, &resp.TotalGross, &resp.TotalDeductions, &resp.TotalNet, &resp.FinalizedAt); err != nil {
			return err
		}
		// The loan instalments the payslips deducted are recovered now (migrations/067).
		return loans.RecoverForRun(ctx, tx, resp.ID, resp.PeriodMonth, resp.PeriodYear, claims.UserID)
	})
	switch {
	case errors.Is(err, loans.ErrRecomputeNeeded):
		httpx.Error(w, http.StatusConflict, "PAYROLL_RECOMPUTE_NEEDED", err.Error()+": run the payroll for this period again, then finalize")
	case errors.Is(err, accounting.ErrPeriodLocked):
		httpx.Error(w, http.StatusConflict, "PERIOD_LOCKED", "the period's last day is in a locked accounting period")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusConflict, "PAYROLL_RUN_FINALIZED", "no draft payroll run with this id (already finalized, or doesn't exist)")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not finalize payroll run")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}

// ListRuns: GET /payroll/runs
func (h *Handler) ListRuns(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	runs := []payrollRunResponse{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, period_month, period_year, status, total_gross::text, total_deductions::text, total_net::text, COALESCE(finalized_at::text,'')
			FROM payroll_runs ORDER BY period_year DESC, period_month DESC`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var run payrollRunResponse
			if err := rows.Scan(&run.ID, &run.PeriodMonth, &run.PeriodYear, &run.Status, &run.TotalGross, &run.TotalDeductions, &run.TotalNet, &run.FinalizedAt); err != nil {
				return err
			}
			runs = append(runs, run)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list payroll runs")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"payroll_runs": runs})
}

// GetRun: GET /payroll/runs/{id} — includes every payslip in the run.
func (h *Handler) GetRun(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")

	var resp payrollRunResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			SELECT id, period_month, period_year, status, total_gross::text, total_deductions::text, total_net::text, COALESCE(finalized_at::text,'')
			FROM payroll_runs WHERE id = $1`, id,
		).Scan(&resp.ID, &resp.PeriodMonth, &resp.PeriodYear, &resp.Status, &resp.TotalGross, &resp.TotalDeductions, &resp.TotalNet, &resp.FinalizedAt); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT `+payslipCols+`
			FROM payslips p JOIN users u ON u.id = p.user_id JOIN payroll_runs pr ON pr.id = p.payroll_run_id
			WHERE p.payroll_run_id = $1 ORDER BY u.name`, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		resp.Payslips = []payslipResponse{}
		for rows.Next() {
			var ps payslipResponse
			if err := rows.Scan(payslipDest(&ps)...); err != nil {
				return err
			}
			resp.Payslips = append(resp.Payslips, ps)
		}
		return rows.Err()
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "no payroll run with this id")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load payroll run")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// GetMyPayslips: GET /payroll/employees/{id}/payslips — self-service
// (finalized runs only: a draft can still change) unless the caller has
// payroll.manage, checked in-handler for the same
// reason internal/hr's ListAttendance checks hr.manage in-handler: the
// gate depends on whose id was requested, not a fixed router-level rule.
func (h *Handler) GetMyPayslips(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	employeeID := chi.URLParam(r, "id")

	payslips := []payslipResponse{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		codes, err := authn.FetchPermissions(ctx, tx, claims.UserID)
		if err != nil {
			return err
		}
		manager := authn.HasPermission(codes, "payroll.manage")
		if employeeID != claims.UserID && !manager {
			return errForbiddenOtherPayslip
		}
		rows, err := tx.Query(ctx, `
			SELECT `+payslipCols+`
			FROM payslips p JOIN users u ON u.id = p.user_id JOIN payroll_runs pr ON pr.id = p.payroll_run_id
			WHERE p.user_id = $1 AND ($2 OR pr.status = 'finalized')
			ORDER BY pr.period_year DESC, pr.period_month DESC`, employeeID, manager)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var ps payslipResponse
			if err := rows.Scan(payslipDest(&ps)...); err != nil {
				return err
			}
			payslips = append(payslips, ps)
		}
		return rows.Err()
	})
	switch {
	case errors.Is(err, errForbiddenOtherPayslip):
		httpx.Error(w, http.StatusForbidden, "FORBIDDEN", "you don't have the payroll.manage permission")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load payslips")
	default:
		httpx.JSON(w, http.StatusOK, map[string]any{"payslips": payslips})
	}
}

var errForbiddenOtherPayslip = errors.New("forbidden: another employee's payslip")

type challanResponse struct {
	PeriodMonth   int    `json:"period_month"`
	PeriodYear    int    `json:"period_year"`
	PFTotal       string `json:"pf_total"`        // employee + employer, the actual EPFO challan payable
	PFEmployerEPS string `json:"pf_employer_eps"` // sub-split of the employer's own PF share the EPFO challan itself requires — see doc comment
	PFEmployerEPF string `json:"pf_employer_epf"`
	ESITotal      string `json:"esi_total"` // employee + employer, the actual ESIC challan payable
	PTTotal       string `json:"pt_total"`
	TDSTotal      string `json:"tds_total"`
	LWFTotal      string `json:"lwf_total"`
}

// epsShareOfEmployerPF is the statutory fixed sub-allocation of the
// employer's PF contribution: 8.33 of the (near-)universal 12% employer
// rate goes to the Employee Pension Scheme, the remaining 3.67 to the
// employee's own EPF account — a real EPFO challan needs this split
// filed separately, not just one combined "employer PF" figure. This is
// a fixed statutory ratio, not proportional to whatever pf_employer_rate
// a merchant has configured in statutory_config — if that rate isn't the
// standard 12%, this split is an approximation that preserves the
// invariant eps + epf == the actual computed employer PF total, rather
// than silently assuming 8.33/3.67 of a non-standard rate is still
// statutorily meaningful.
const epsShareOfEmployerPF = 8.33 / 12.0

// GetChallanSummary: GET /payroll/runs/{id}/challan — the FRD's "challan
// generation," scoped honestly: this computes the correct total payable
// per statutory head from this run's own payslips, which is the number
// that goes on a real EPFO/ESIC/PT challan — it does not produce the
// actual government e-challan file or submit anything, the same
// "export the number, don't build the portal integration" boundary
// internal/gst's GSTR-1/3B export already draws.
func (h *Handler) GetChallanSummary(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")

	var resp challanResponse
	var pfEmployerTotal float64
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT period_month, period_year FROM payroll_runs WHERE id = $1`, id).
			Scan(&resp.PeriodMonth, &resp.PeriodYear); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(pf_employee + pf_employer),0)::text, COALESCE(SUM(pf_employer),0), COALESCE(SUM(esi_employee + esi_employer),0)::text,
			       COALESCE(SUM(pt_amount),0)::text, COALESCE(SUM(tds_amount),0)::text, COALESCE(SUM(lwf_employee + lwf_employer),0)::text
			FROM payslips WHERE payroll_run_id = $1`, id,
		).Scan(&resp.PFTotal, &pfEmployerTotal, &resp.ESITotal, &resp.PTTotal, &resp.TDSTotal, &resp.LWFTotal)
	})
	if err == nil {
		eps := round2(pfEmployerTotal * epsShareOfEmployerPF)
		resp.PFEmployerEPS = formatMoney(eps)
		resp.PFEmployerEPF = formatMoney(round2(pfEmployerTotal - eps))
	}
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "no payroll run with this id")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not compute challan summary")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// round2 rounds half away from zero. (int64(v*100+0.5) truncated
// negatives toward zero: -10.00 became -9.99.)
func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func formatDays(v float64) string {
	return strconv.FormatFloat(v, 'f', 1, 64)
}

func formatMoney(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64)
}

func ptForGross(slabs []hr.PTSlab, gross float64) float64 {
	for _, s := range slabs {
		if gross >= s.Min && (s.Max == nil || gross < *s.Max) {
			return s.Amount
		}
	}
	return 0
}
