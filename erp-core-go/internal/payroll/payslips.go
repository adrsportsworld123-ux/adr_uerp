package payroll

// Payslips to read, print and download (§3.78): one payslip with the
// employee's details and the company letterhead, a run's payslips
// together (to print them all), and the PDF. An employee sees their own
// once the run is finalized; payroll.manage sees any.

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/documents"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/taxes"
)

// payslipCols / scanPayslip read a payslips row p (with users u and
// payroll_runs pr) into a payslipResponse.
const payslipCols = `p.user_id, u.name, p.basic::text, p.hra::text, p.special_allowance::text, p.other_allowances::text,
	p.commission_amount::text, p.gross_earnings::text, p.days_in_period::text, p.days_present::text,
	p.pf_employee::text, p.pf_employer::text, p.esi_employee::text, p.esi_employer::text, p.pt_amount::text,
	p.tds_amount::text, p.lwf_employee::text, p.lwf_employer::text, p.total_deductions::text, p.net_pay::text,
	p.loan_deduction::text, p.leave_paid_days::text, p.leave_unpaid_days::text, p.absent_days::text, p.permission_unpaid_minutes,
	p.id::text, pr.id::text, pr.period_month, pr.period_year, pr.status`

func payslipDest(ps *payslipResponse) []any {
	return []any{&ps.UserID, &ps.Name, &ps.Basic, &ps.HRA, &ps.SpecialAllowance, &ps.OtherAllowances,
		&ps.CommissionAmount, &ps.GrossEarnings, &ps.DaysInPeriod, &ps.DaysPresent, &ps.PFEmployee, &ps.PFEmployer,
		&ps.ESIEmployee, &ps.ESIEmployer, &ps.PTAmount, &ps.TDSAmount, &ps.LWFEmployee, &ps.LWFEmployer,
		&ps.TotalDeductions, &ps.NetPay, &ps.LoanDeduction, &ps.LeavePaidDays, &ps.LeaveUnpaidDays, &ps.AbsentDays,
		&ps.PermissionUnpaidMinutes, &ps.PayslipID, &ps.RunID, &ps.PeriodMonth, &ps.PeriodYear, &ps.RunStatus}
}

// payslipEmployee is what a printed payslip says about the employee.
type payslipEmployee struct {
	EmployeeCode  *string `json:"employee_code"`
	Designation   *string `json:"designation"`
	Department    *string `json:"department"`
	BranchID      *string `json:"branch_id"`
	Branch        *string `json:"branch"`
	DateOfJoining *string `json:"date_of_joining"`
	PAN           *string `json:"pan_number"`
	UAN           *string `json:"uan_number"`
	ESI           *string `json:"esi_number"`
	BankAccount   *string `json:"bank_account_no"`
	BankIFSC      *string `json:"bank_ifsc"`
}

type payslipCompany struct {
	Name    string   `json:"name"`
	Address []string `json:"address"`
	Phone   string   `json:"phone,omitempty"`
	Email   string   `json:"email,omitempty"`
	GSTIN   string   `json:"gstin,omitempty"`
}

type payslipDocument struct {
	payslipResponse
	Employee    payslipEmployee `json:"employee"`
	Company     payslipCompany  `json:"company"`
	Period      string          `json:"period"` // "October 2026"
	NetPayWords string          `json:"net_pay_words"`
	Currency    taxes.Currency  `json:"currency"`
	letterhead  documents.Letterhead
}

const payslipFrom = `
	FROM payslips p JOIN users u ON u.id = p.user_id JOIN payroll_runs pr ON pr.id = p.payroll_run_id
	LEFT JOIN branches b ON b.id = u.branch_id`

const employeeCols = `u.employee_code, u.designation, u.department, u.branch_id::text, b.name, u.date_of_joining::text,
	u.pan_number, u.uan_number, u.esi_number, u.bank_account_no, u.bank_ifsc`

func loadPayslipDocs(ctx context.Context, tx pgx.Tx, where string, arg string) ([]payslipDocument, error) {
	rows, err := tx.Query(ctx, `SELECT `+payslipCols+`, `+employeeCols+payslipFrom+` WHERE `+where+` ORDER BY u.name`, arg)
	if err != nil {
		return nil, err
	}
	var docs []payslipDocument
	for rows.Next() {
		var d payslipDocument
		e := &d.Employee
		dest := append(payslipDest(&d.payslipResponse), &e.EmployeeCode, &e.Designation, &e.Department, &e.BranchID, &e.Branch,
			&e.DateOfJoining, &e.PAN, &e.UAN, &e.ESI, &e.BankAccount, &e.BankIFSC)
		if err := rows.Scan(dest...); err != nil {
			rows.Close()
			return nil, err
		}
		d.Period = time.Month(d.PeriodMonth).String() + " " + strconv.Itoa(d.PeriodYear)
		docs = append(docs, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// The letterhead of the employee's branch (any branch for someone without one).
	heads := map[string]documents.Letterhead{}
	for i := range docs {
		branch := ""
		if docs[i].Employee.BranchID != nil {
			branch = *docs[i].Employee.BranchID
		} else if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT id::text FROM branches ORDER BY created_at LIMIT 1), '')`).Scan(&branch); err != nil {
			return nil, err
		}
		lh, ok := heads[branch]
		if !ok && branch != "" {
			if lh, _, err = documents.LoadLetterhead(ctx, tx, branch, documents.KindInternal); err != nil {
				return nil, err
			}
			heads[branch] = lh
		}
		docs[i].letterhead = lh
		docs[i].Company = payslipCompany{Name: lh.Name, Address: lh.Address, Phone: lh.Phone, Email: lh.Email, GSTIN: lh.GSTIN}
		docs[i].Currency = lh.Currency
		if docs[i].Currency.Code == "" {
			docs[i].Currency = taxes.Currency{Code: "INR", Symbol: "₹"}
		}
		net, _ := strconv.ParseFloat(docs[i].NetPay, 64)
		docs[i].NetPayWords = documents.AmountInWords(net, docs[i].Currency)
	}
	return docs, nil
}

var errPayslipNotYours = errors.New("not your payslip")

// onePayslip: the payslip, if the caller may see it — their own once
// finalized, or any with payroll.manage.
func onePayslip(ctx context.Context, tx pgx.Tx, claims *authn.Claims, id string) (payslipDocument, error) {
	docs, err := loadPayslipDocs(ctx, tx, `p.id::text = $1`, id)
	if err != nil {
		return payslipDocument{}, err
	}
	if len(docs) == 0 {
		return payslipDocument{}, pgx.ErrNoRows
	}
	d := docs[0]
	codes, err := authn.FetchPermissions(ctx, tx, claims.UserID)
	if err != nil {
		return d, err
	}
	if !authn.HasPermission(codes, "payroll.manage") && (d.UserID != claims.UserID || d.RunStatus != "finalized") {
		return d, errPayslipNotYours
	}
	return d, nil
}

func writePayslipError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, pgx.ErrNoRows), errors.Is(err, errPayslipNotYours):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "no payslip with this id")
	default:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load the payslip")
	}
}

// GetPayslip: GET /payroll/payslips/{id}
func (h *Handler) GetPayslip(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var d payslipDocument
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		d, err = onePayslip(ctx, tx, claims, chi.URLParam(r, "id"))
		return err
	})
	if err != nil {
		writePayslipError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, d)
}

// PayslipPDF: GET /payroll/payslips/{id}/pdf
func (h *Handler) PayslipPDF(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var d payslipDocument
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		d, err = onePayslip(ctx, tx, claims, chi.URLParam(r, "id"))
		return err
	})
	if err != nil {
		writePayslipError(w, err)
		return
	}
	pdf, err := documents.Render(d.letterhead, documents.Payslip(d.input()), documents.FormatA4)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not render the payslip")
		return
	}
	name := "payslip"
	if d.Employee.EmployeeCode != nil {
		name += "-" + *d.Employee.EmployeeCode
	}
	documents.WritePDF(w, name+"-"+strconv.Itoa(d.PeriodYear)+"-"+strconv.Itoa(d.PeriodMonth)+".pdf", pdf)
}

// RunPayslips: GET /payroll/runs/{id}/payslips (payroll.manage) — every
// payslip of a run, with what printing them needs.
func (h *Handler) RunPayslips(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var docs []payslipDocument
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var found bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM payroll_runs WHERE id::text = $1)`, chi.URLParam(r, "id")).Scan(&found); err != nil {
			return err
		}
		if !found {
			return pgx.ErrNoRows
		}
		var err error
		docs, err = loadPayslipDocs(ctx, tx, `p.payroll_run_id::text = $1`, chi.URLParam(r, "id"))
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "no payroll run with this id")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load the payslips")
		return
	}
	if docs == nil {
		docs = []payslipDocument{}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"payslips": docs})
}

func (d payslipDocument) input() documents.PayslipInput {
	s := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	joined := s(d.Employee.DateOfJoining)
	if t, err := time.Parse("2006-01-02", joined); err == nil {
		joined = t.Format("02 Jan 2006")
	}
	return documents.PayslipInput{
		Period: d.Period, Status: d.RunStatus,
		Employee: d.Name, EmployeeCode: s(d.Employee.EmployeeCode), Designation: s(d.Employee.Designation),
		Department: s(d.Employee.Department), Branch: s(d.Employee.Branch), Joined: joined,
		PAN: s(d.Employee.PAN), UAN: s(d.Employee.UAN), ESI: s(d.Employee.ESI),
		BankAccount: s(d.Employee.BankAccount), BankIFSC: s(d.Employee.BankIFSC),
		DaysInPeriod: d.DaysInPeriod, DaysPaid: d.DaysPresent,
		LeavePaid: d.LeavePaidDays, LeaveUnpaid: d.LeaveUnpaidDays, Absent: d.AbsentDays, PermissionUnpaidMinutes: d.PermissionUnpaidMinutes,
		Basic: d.Basic, HRA: d.HRA, Special: d.SpecialAllowance, Other: d.OtherAllowances, Commission: d.CommissionAmount,
		Gross: d.GrossEarnings, PF: d.PFEmployee, ESIDeduction: d.ESIEmployee, PT: d.PTAmount, TDS: d.TDSAmount,
		LWF: d.LWFEmployee, Loan: d.LoanDeduction, TotalDeductions: d.TotalDeductions, NetPay: d.NetPay,
		EmployerPF: d.PFEmployer, EmployerESI: d.ESIEmployer, EmployerLWF: d.LWFEmployer, NetPayWords: d.NetPayWords,
	}
}
