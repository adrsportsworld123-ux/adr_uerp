// Package loans is employee loans and salary advances (migrations/067):
// a schedule of monthly instalments with optional flat interest, paid out
// in cash, by transfer or by cheque, recovered through payroll (the run
// deducts every instalment due; finalizing it recovers them) or repaid by
// hand, and foreclosable. Principal sits in Employee Loans & Advances
// (1150) under the employee; interest is income (4050) when it is
// recovered.
package loans

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/accounting"
	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/banking"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/locale"
)

type Handler struct {
	DB *db.DB
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// Installment is one month of the schedule.
type Installment struct {
	Seq           int     `json:"seq"`
	DueMonth      int     `json:"due_month"`
	DueYear       int     `json:"due_year"`
	PrincipalPart float64 `json:"principal_part"`
	InterestPart  float64 `json:"interest_part"`
	Amount        float64 `json:"amount"`
	Status        string  `json:"status"`
	RecoveredOn   *string `json:"recovered_on"`
	RecoveredVia  *string `json:"recovered_via"`
}

// Schedule splits a loan into tenure monthly instalments: flat interest
// (principal × rate% × months / 12), principal and interest spread evenly,
// the last instalment taking the rounding so the parts add up exactly.
func Schedule(principal, ratePct float64, tenure, startMonth, startYear int) (totalInterest, emi float64, out []Installment) {
	totalInterest = round2(principal * ratePct / 100 * float64(tenure) / 12)
	pPart := round2(principal / float64(tenure))
	iPart := round2(totalInterest / float64(tenure))
	var pSum, iSum float64
	m, y := startMonth, startYear
	for i := 1; i <= tenure; i++ {
		p, it := pPart, iPart
		if i == tenure {
			p, it = round2(principal-pSum), round2(totalInterest-iSum)
		}
		pSum, iSum = round2(pSum+p), round2(iSum+it)
		out = append(out, Installment{Seq: i, DueMonth: m, DueYear: y, PrincipalPart: p, InterestPart: it, Amount: round2(p + it), Status: "pending"})
		if m++; m > 12 {
			m, y = 1, y+1
		}
	}
	return totalInterest, round2(pPart + iPart), out
}

type Repayment struct {
	RepaymentID   string  `json:"repayment_id"`
	Amount        string  `json:"amount"`
	PrincipalPart string  `json:"principal_part"`
	InterestPart  string  `json:"interest_part"`
	Method        string  `json:"method"`
	ReferenceNo   string  `json:"reference_no"`
	PaidOn        string  `json:"paid_on"`
	Foreclosure   bool    `json:"foreclosure"`
	PayrollRunID  *string `json:"payroll_run_id"`
}

type Loan struct {
	LoanID             string  `json:"loan_id"`
	LoanNumber         string  `json:"loan_number"`
	UserID             string  `json:"user_id"`
	EmployeeName       string  `json:"employee_name"`
	EmployeeCode       *string `json:"employee_code"`
	BranchID           *string `json:"branch_id"`
	LoanType           string  `json:"loan_type"`
	Purpose            string  `json:"purpose"`
	Principal          string  `json:"principal"`
	InterestRate       string  `json:"interest_rate"`
	TenureMonths       int     `json:"tenure_months"`
	TotalInterest      string  `json:"total_interest"`
	EMIAmount          string  `json:"emi_amount"`
	StartMonth         int     `json:"start_month"`
	StartYear          int     `json:"start_year"`
	DisbursedOn        string  `json:"disbursed_on"`
	DisbursementMethod string  `json:"disbursement_method"`
	BankAccountID      *string `json:"bank_account_id"`
	IssuedChequeID     *string `json:"issued_cheque_id"`
	Status             string  `json:"status"`
	ClosedOn           *string `json:"closed_on"`
	// Outstanding: principal and interest of the instalments not yet recovered.
	OutstandingPrincipal string        `json:"outstanding_principal"`
	OutstandingInterest  string        `json:"outstanding_interest"`
	Recovered            string        `json:"recovered"`
	InstallmentsLeft     int           `json:"installments_left"`
	CreatedAt            string        `json:"created_at"`
	Schedule             []Installment `json:"schedule,omitempty"`
	Repayments           []Repayment   `json:"repayments,omitempty"`
}

const loanSelect = `
	SELECT l.id::text, l.loan_number, l.user_id::text, u.name, u.employee_code, l.branch_id::text, l.loan_type, COALESCE(l.purpose, ''),
	       l.principal::text, l.interest_rate::text, l.tenure_months, l.total_interest::text, l.emi_amount::text,
	       l.start_month, l.start_year, l.disbursed_on::text, l.disbursement_method, l.bank_account_id::text, l.issued_cheque_id::text,
	       l.status, l.closed_on::text,
	       COALESCE((SELECT sum(i.principal_part) FROM employee_loan_installments i WHERE i.loan_id = l.id AND i.status = 'pending'), 0)::numeric(14,2)::text,
	       COALESCE((SELECT sum(i.interest_part) FROM employee_loan_installments i WHERE i.loan_id = l.id AND i.status = 'pending'), 0)::numeric(14,2)::text,
	       COALESCE((SELECT sum(r.amount) FROM employee_loan_repayments r WHERE r.loan_id = l.id), 0)::numeric(14,2)::text,
	       (SELECT count(*) FROM employee_loan_installments i WHERE i.loan_id = l.id AND i.status = 'pending')::int,
	       l.created_at::text
	FROM employee_loans l JOIN users u ON u.id = l.user_id`

func scanLoan(row pgx.Row) (Loan, error) {
	var l Loan
	err := row.Scan(&l.LoanID, &l.LoanNumber, &l.UserID, &l.EmployeeName, &l.EmployeeCode, &l.BranchID, &l.LoanType, &l.Purpose,
		&l.Principal, &l.InterestRate, &l.TenureMonths, &l.TotalInterest, &l.EMIAmount, &l.StartMonth, &l.StartYear,
		&l.DisbursedOn, &l.DisbursementMethod, &l.BankAccountID, &l.IssuedChequeID, &l.Status, &l.ClosedOn,
		&l.OutstandingPrincipal, &l.OutstandingInterest, &l.Recovered, &l.InstallmentsLeft, &l.CreatedAt)
	return l, err
}

func loadLoan(ctx context.Context, tx pgx.Tx, id string, detail bool) (Loan, error) {
	l, err := scanLoan(tx.QueryRow(ctx, loanSelect+` WHERE l.id::text = $1`, id))
	if err != nil || !detail {
		return l, err
	}
	rows, err := tx.Query(ctx, `SELECT seq, due_month, due_year, principal_part, interest_part, amount, status, recovered_on::text, recovered_via
		FROM employee_loan_installments WHERE loan_id::text = $1 ORDER BY seq`, id)
	if err != nil {
		return l, err
	}
	l.Schedule = []Installment{}
	for rows.Next() {
		var i Installment
		if err := rows.Scan(&i.Seq, &i.DueMonth, &i.DueYear, &i.PrincipalPart, &i.InterestPart, &i.Amount, &i.Status, &i.RecoveredOn, &i.RecoveredVia); err != nil {
			rows.Close()
			return l, err
		}
		l.Schedule = append(l.Schedule, i)
	}
	rows.Close()
	rrows, err := tx.Query(ctx, `SELECT id::text, amount::text, principal_part::text, interest_part::text, method, COALESCE(reference_no, ''),
		paid_on::text, foreclosure, payroll_run_id::text FROM employee_loan_repayments WHERE loan_id::text = $1 ORDER BY paid_on, created_at`, id)
	if err != nil {
		return l, err
	}
	defer rrows.Close()
	l.Repayments = []Repayment{}
	for rrows.Next() {
		var r Repayment
		if err := rrows.Scan(&r.RepaymentID, &r.Amount, &r.PrincipalPart, &r.InterestPart, &r.Method, &r.ReferenceNo, &r.PaidOn, &r.Foreclosure, &r.PayrollRunID); err != nil {
			return l, err
		}
		l.Repayments = append(l.Repayments, r)
	}
	return l, rrows.Err()
}

type errMsg struct {
	status int
	code   string
	msg    string
}

func (e errMsg) Error() string { return e.msg }

func bad(format string, args ...any) error {
	return errMsg{http.StatusBadRequest, "INVALID_REQUEST", fmt.Sprintf(format, args...)}
}

func conflict(format string, args ...any) error {
	return errMsg{http.StatusConflict, "INVALID_STATE", fmt.Sprintf(format, args...)}
}

func respond(w http.ResponseWriter, err error, what string) {
	var m errMsg
	switch {
	case errors.As(err, &m):
		httpx.Error(w, m.status, m.code, m.msg)
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", what+" not found")
	case errors.Is(err, accounting.ErrNoBankAccount):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "bank account not found")
	case errors.Is(err, accounting.ErrBankAccountInactive):
		httpx.Error(w, http.StatusConflict, "BANK_ACCOUNT_INACTIVE", "that bank account is inactive")
	case errors.Is(err, accounting.ErrPeriodLocked):
		httpx.Error(w, http.StatusConflict, "PERIOD_LOCKED", "that date is in a locked accounting period")
	case banking.IsClientError(err):
		banking.WriteClientError(w, err)
	default:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not save the "+what)
	}
}

func validDate(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

func today(ctx context.Context, tx pgx.Tx) string {
	return time.Now().In(locale.Location(ctx, tx)).Format("2006-01-02")
}

// payLedger is the account money leaves or arrives through: cash, or a
// bank transfer / cheque through a bank account (Bank 1002 when none).
func payLedger(ctx context.Context, tx pgx.Tx, method, bankAccountID string) (accounting.JournalLine, error) {
	switch method {
	case "cash":
		return accounting.JournalLine{AccountCode: accounting.AccountCash}, nil
	case "bank_transfer", "cheque":
		code, err := accounting.BankLedgerCode(ctx, tx, bankAccountID)
		if err != nil {
			return accounting.JournalLine{}, err
		}
		l := accounting.JournalLine{AccountCode: code}
		if bankAccountID != "" {
			l.PartyType, l.PartyID = "bank_account", bankAccountID
		}
		return l, nil
	}
	return accounting.JournalLine{}, bad("method must be cash, bank_transfer or cheque")
}

// List: GET /employee-loans?user_id=&status=&q= — loans.manage.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	q := r.URL.Query()
	h.list(w, r, claims.TenantID, q.Get("user_id"), q.Get("status"), strings.TrimSpace(q.Get("q")))
}

// Mine: GET /me/loans — the signed-in employee's own loans.
func (h *Handler) Mine(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	h.list(w, r, claims.TenantID, claims.UserID, "", "")
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request, tenant, userID, status, search string) {
	list := []Loan{}
	err := h.DB.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, loanSelect+`
			WHERE ($1 = '' OR l.user_id::text = $1) AND ($2 = '' OR l.status = $2)
			  AND ($3 = '' OR u.name ILIKE '%' || $3 || '%' OR u.employee_code ILIKE '%' || $3 || '%' OR l.loan_number ILIKE '%' || $3 || '%')
			ORDER BY l.status = 'active' DESC, l.created_at DESC LIMIT 500`, userID, status, search)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			l, err := scanLoan(rows)
			if err != nil {
				return err
			}
			list = append(list, l)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list loans")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"loans": list})
}

// Get: GET /employee-loans/{id} — loans.manage, or the employee's own.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var l Loan
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if l, err = loadLoan(ctx, tx, chi.URLParam(r, "id"), true); err != nil {
			return err
		}
		if l.UserID != claims.UserID {
			codes, err := authn.FetchPermissions(ctx, tx, claims.UserID)
			if err != nil {
				return err
			}
			if !authn.HasPermission(codes, "loans.manage") {
				return pgx.ErrNoRows
			}
		}
		return nil
	})
	if err != nil {
		respond(w, err, "loan")
		return
	}
	httpx.JSON(w, http.StatusOK, l)
}

// PreviewSchedule: POST /employee-loans/preview {principal, interest_rate,
// tenure_months, start_month, start_year} — the schedule a loan would get.
func (h *Handler) PreviewSchedule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Principal    float64 `json:"principal"`
		InterestRate float64 `json:"interest_rate"`
		TenureMonths int     `json:"tenure_months"`
		StartMonth   int     `json:"start_month"`
		StartYear    int     `json:"start_year"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if msg := checkTerms(req.Principal, req.InterestRate, req.TenureMonths, req.StartMonth, req.StartYear); msg != "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", msg)
		return
	}
	interest, emi, sched := Schedule(req.Principal, req.InterestRate, req.TenureMonths, req.StartMonth, req.StartYear)
	httpx.JSON(w, http.StatusOK, map[string]any{"total_interest": fmt.Sprintf("%.2f", interest), "emi_amount": fmt.Sprintf("%.2f", emi),
		"total_payable": fmt.Sprintf("%.2f", round2(req.Principal+interest)), "schedule": sched})
}

func checkTerms(principal, rate float64, tenure, month, year int) string {
	switch {
	case principal <= 0 || principal >= 1e9:
		return "principal must be positive"
	case rate < 0 || rate > 60:
		return "interest_rate is 0 to 60 (% a year, flat)"
	case tenure < 1 || tenure > 120:
		return "tenure_months is 1 to 120"
	case month < 1 || month > 12 || year < 2000 || year > 2100:
		return "start_month (1-12) and start_year are required"
	}
	return ""
}

type createRequest struct {
	UserID             string  `json:"user_id"`
	LoanType           string  `json:"loan_type"` // loan (default) | advance
	Purpose            string  `json:"purpose"`
	Principal          float64 `json:"principal"`
	InterestRate       float64 `json:"interest_rate"`
	TenureMonths       int     `json:"tenure_months"`
	StartMonth         int     `json:"start_month"`
	StartYear          int     `json:"start_year"`
	DisbursedOn        string  `json:"disbursed_on"` // default today
	DisbursementMethod string  `json:"disbursement_method"`
	BankAccountID      string  `json:"bank_account_id"`
	// For a cheque from a bank account: the leaf ("" = the next one).
	ChequeBookID string `json:"cheque_book_id"`
	ChequeNumber string `json:"cheque_number"`
	ChequeDate   string `json:"cheque_date"`
	ApprovedBy   string `json:"approved_by"`
}

// Create: POST /employee-loans — loans.manage. Disburses the loan: the
// schedule is written and Dr Employee Loans (the employee) / Cr the cash
// or bank it was paid from is posted on disbursed_on. A cheque from a
// bank account is written on the cheque register too.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.LoanType == "" {
		req.LoanType = "loan"
	}
	req.Purpose = strings.TrimSpace(req.Purpose)
	msg := checkTerms(req.Principal, req.InterestRate, req.TenureMonths, req.StartMonth, req.StartYear)
	switch {
	case msg != "":
	case req.UserID == "":
		msg = "user_id (the employee) is required"
	case req.LoanType != "loan" && req.LoanType != "advance":
		msg = "loan_type must be loan or advance"
	case req.DisbursementMethod != "cash" && req.DisbursementMethod != "bank_transfer" && req.DisbursementMethod != "cheque":
		msg = "disbursement_method must be cash, bank_transfer or cheque"
	case req.DisbursedOn != "" && !validDate(req.DisbursedOn):
		msg = "disbursed_on must be YYYY-MM-DD"
	case len(req.Purpose) > 200:
		msg = "purpose is at most 200 characters"
	}
	if msg != "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", msg)
		return
	}
	if req.DisbursementMethod == "cash" {
		req.BankAccountID = ""
	}
	var resp Loan
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var name, status string
		var branchID *string
		if err := tx.QueryRow(ctx, `SELECT name, status, branch_id::text FROM users WHERE id::text = $1 AND employee_code IS NOT NULL`, req.UserID).
			Scan(&name, &status, &branchID); errors.Is(err, pgx.ErrNoRows) {
			return bad("user_id is not an employee")
		} else if err != nil {
			return err
		}
		if status != "active" {
			return conflict("%s is not an active employee", name)
		}
		if req.DisbursedOn == "" {
			req.DisbursedOn = today(ctx, tx)
		}
		if req.DisbursedOn > today(ctx, tx) {
			return bad("disbursed_on can't be in the future")
		}
		pay, err := payLedger(ctx, tx, req.DisbursementMethod, req.BankAccountID)
		if err != nil {
			return err
		}
		interest, emi, sched := Schedule(req.Principal, req.InterestRate, req.TenureMonths, req.StartMonth, req.StartYear)
		var seq int
		if err := tx.QueryRow(ctx, `SELECT count(*) + 1 FROM employee_loans`).Scan(&seq); err != nil {
			return err
		}
		number := fmt.Sprintf("LN-%s-%04d", req.DisbursedOn[:4], seq)
		var id string
		if err := tx.QueryRow(ctx, `
			INSERT INTO employee_loans (merchant_id, loan_number, user_id, branch_id, loan_type, purpose, principal, interest_rate, tenure_months,
			                            total_interest, emi_amount, start_month, start_year, disbursed_on, disbursement_method, bank_account_id,
			                            approved_by, created_by)
			VALUES (current_setting('app.tenant_id')::uuid, $1, $2::uuid, $3::uuid, $4, NULLIF($5, ''), $6, $7, $8, $9, $10, $11, $12, $13::date, $14,
			        NULLIF($15, '')::uuid, NULLIF($16, '')::uuid, $17) RETURNING id`,
			number, req.UserID, branchID, req.LoanType, req.Purpose, req.Principal, req.InterestRate, req.TenureMonths, interest, emi,
			req.StartMonth, req.StartYear, req.DisbursedOn, req.DisbursementMethod, req.BankAccountID, req.ApprovedBy, claims.UserID).Scan(&id); err != nil {
			return err
		}
		for _, i := range sched {
			if _, err := tx.Exec(ctx, `INSERT INTO employee_loan_installments (loan_id, seq, due_month, due_year, principal_part, interest_part, amount)
				VALUES ($1::uuid, $2, $3, $4, $5, $6, $7)`, id, i.Seq, i.DueMonth, i.DueYear, i.PrincipalPart, i.InterestPart, i.Amount); err != nil {
				return err
			}
		}
		if req.DisbursementMethod == "cheque" && req.BankAccountID != "" {
			chequeDate := req.ChequeDate
			if chequeDate == "" {
				chequeDate = req.DisbursedOn
			}
			chequeID, err := banking.Issue(ctx, tx, banking.IssueRequest{BankAccountID: req.BankAccountID, ChequeBookID: req.ChequeBookID,
				ChequeNumber: req.ChequeNumber, ChequeDate: chequeDate, PayeeName: name, Amount: req.Principal,
				PartyType: "employee", PartyID: req.UserID, BranchID: derefStr(branchID), Memo: "Employee " + req.LoanType + " " + number}, claims.UserID)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE employee_loans SET issued_cheque_id = $2::uuid WHERE id::text = $1`, id, chequeID); err != nil {
				return err
			}
		}
		pay.Credit = req.Principal
		if _, err := accounting.PostJournalEntryOnDate(ctx, tx, derefStr(branchID), "loan_disbursement", id,
			fmt.Sprintf("Employee %s %s to %s", req.LoanType, number, name), claims.UserID, req.DisbursedOn, []accounting.JournalLine{
				{AccountCode: accounting.AccountEmployeeLoans, Debit: req.Principal, PartyType: "employee", PartyID: req.UserID},
				pay,
			}); err != nil {
			return err
		}
		if resp, err = loadLoan(ctx, tx, id, true); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "employee_loan", EntityID: id, Action: "create", PerformedBy: claims.UserID, After: resp})
	})
	if err != nil {
		respond(w, err, "loan")
		return
	}
	httpx.JSON(w, http.StatusCreated, resp)
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

type pendingInst struct {
	id                       string
	principal, interest, amt float64
}

func pendingInstallments(ctx context.Context, tx pgx.Tx, loanID string) ([]pendingInst, error) {
	rows, err := tx.Query(ctx, `SELECT id::text, principal_part, interest_part, amount FROM employee_loan_installments
		WHERE loan_id::text = $1 AND status = 'pending' ORDER BY seq FOR UPDATE`, loanID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pendingInst
	for rows.Next() {
		var p pendingInst
		if err := rows.Scan(&p.id, &p.principal, &p.interest, &p.amt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Repay: POST /employee-loans/{id}/repayments {amount, method,
// bank_account_id?, reference_no?, paid_on?} — loans.manage. Settles the
// oldest instalments first; a part payment reduces the next instalment
// (its interest first). Posts Dr cash/bank / Cr Employee Loans
// (principal) / Cr Interest Income. The loan closes when nothing is left.
//
// POST /employee-loans/{id}/foreclose {method, bank_account_id?,
// reference_no?, paid_on?, waive_interest?} settles everything left:
// the remaining principal, plus the remaining interest unless it is
// waived (default: waived).
func (h *Handler) Repay(w http.ResponseWriter, r *http.Request) {
	h.repay(w, r, false)
}

func (h *Handler) Foreclose(w http.ResponseWriter, r *http.Request) {
	h.repay(w, r, true)
}

func (h *Handler) repay(w http.ResponseWriter, r *http.Request, foreclose bool) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var req struct {
		Amount        float64 `json:"amount"`
		Method        string  `json:"method"`
		BankAccountID string  `json:"bank_account_id"`
		ReferenceNo   string  `json:"reference_no"`
		PaidOn        string  `json:"paid_on"`
		WaiveInterest *bool   `json:"waive_interest"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.PaidOn != "" && !validDate(req.PaidOn) {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "paid_on must be YYYY-MM-DD")
		return
	}
	if !foreclose && req.Amount <= 0 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "a positive amount is required")
		return
	}
	if req.Method == "cash" {
		req.BankAccountID = ""
	}
	var resp Loan
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		l, err := loadLoan(ctx, tx, id, false)
		if err != nil {
			return err
		}
		if l.Status != "active" {
			return conflict("this loan is %s", l.Status)
		}
		if req.PaidOn == "" {
			req.PaidOn = today(ctx, tx)
		}
		if req.PaidOn < l.DisbursedOn || req.PaidOn > today(ctx, tx) {
			return bad("paid_on must be between the disbursement and today")
		}
		pay, err := payLedger(ctx, tx, req.Method, req.BankAccountID)
		if err != nil {
			return err
		}
		pending, err := pendingInstallments(ctx, tx, id)
		if err != nil {
			return err
		}
		var principal, interest float64
		if foreclose {
			waive := req.WaiveInterest == nil || *req.WaiveInterest
			for _, p := range pending {
				principal += p.principal
				if !waive {
					interest += p.interest
				}
			}
			principal, interest = round2(principal), round2(interest)
			if _, err := tx.Exec(ctx, `UPDATE employee_loan_installments SET status = 'recovered', recovered_on = $2::date, recovered_via = $3
				WHERE loan_id::text = $1 AND status = 'pending'`, id, req.PaidOn, req.Method); err != nil {
				return err
			}
		} else {
			var outstanding float64
			for _, p := range pending {
				outstanding += p.amt
			}
			if req.Amount > round2(outstanding)+0.001 {
				return conflict("that is more than the %.2f left on this loan (foreclose to settle it)", outstanding)
			}
			left := round2(req.Amount)
			for _, p := range pending {
				if left <= 0 {
					break
				}
				if left+0.001 >= p.amt {
					principal, interest, left = principal+p.principal, interest+p.interest, round2(left-p.amt)
					if _, err := tx.Exec(ctx, `UPDATE employee_loan_installments SET status = 'recovered', recovered_on = $2::date, recovered_via = $3
						WHERE id::text = $1`, p.id, req.PaidOn, req.Method); err != nil {
						return err
					}
					continue
				}
				// A part of this instalment: its interest first.
				i := math.Min(left, p.interest)
				pr := round2(left - i)
				principal, interest = principal+pr, interest+i
				if _, err := tx.Exec(ctx, `UPDATE employee_loan_installments SET principal_part = principal_part - $2,
					interest_part = interest_part - $3, amount = amount - $4 WHERE id::text = $1`, p.id, pr, i, left); err != nil {
					return err
				}
				left = 0
			}
			principal, interest = round2(principal), round2(interest)
		}
		amount := round2(principal + interest)
		if amount <= 0 {
			return conflict("nothing is left to repay")
		}
		var repaymentID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO employee_loan_repayments (merchant_id, loan_id, amount, principal_part, interest_part, method, bank_account_id,
			                                      reference_no, paid_on, foreclosure, created_by)
			VALUES (current_setting('app.tenant_id')::uuid, $1::uuid, $2, $3, $4, $5, NULLIF($6, '')::uuid, NULLIF($7, ''), $8::date, $9, $10)
			RETURNING id`, id, amount, principal, interest, req.Method, req.BankAccountID, strings.TrimSpace(req.ReferenceNo),
			req.PaidOn, foreclose, claims.UserID).Scan(&repaymentID); err != nil {
			return err
		}
		pay.Debit = amount
		lines := []accounting.JournalLine{pay,
			{AccountCode: accounting.AccountEmployeeLoans, Credit: principal, PartyType: "employee", PartyID: l.UserID}}
		if interest > 0 {
			lines = append(lines, accounting.JournalLine{AccountCode: accounting.AccountInterestIncome, Credit: interest})
		}
		what := "repayment"
		if foreclose {
			what = "foreclosure"
		}
		if _, err := accounting.PostJournalEntryOnDate(ctx, tx, derefStr(l.BranchID), "loan_repayment", id,
			fmt.Sprintf("Loan %s %s from %s", l.LoanNumber, what, l.EmployeeName), claims.UserID, req.PaidOn, lines); err != nil {
			return err
		}
		if err := closeIfDone(ctx, tx, id, req.PaidOn, foreclose); err != nil {
			return err
		}
		if resp, err = loadLoan(ctx, tx, id, true); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "employee_loan", EntityID: id, Action: "update", PerformedBy: claims.UserID, Before: l, After: resp, Reason: what})
	})
	if err != nil {
		respond(w, err, "loan")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// closeIfDone closes a loan with no instalment left.
func closeIfDone(ctx context.Context, tx pgx.Tx, loanID, on string, foreclosed bool) error {
	status := "closed"
	if foreclosed {
		status = "foreclosed"
	}
	_, err := tx.Exec(ctx, `UPDATE employee_loans SET status = $2, closed_on = $3::date
		WHERE id::text = $1 AND status = 'active'
		  AND NOT EXISTS (SELECT 1 FROM employee_loan_installments WHERE loan_id = employee_loans.id AND status = 'pending')`, loanID, status, on)
	return err
}
