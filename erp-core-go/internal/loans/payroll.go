package loans

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/accounting"
)

// PlanPayrollDeduction picks what a payslip deducts for an employee's
// loans: the pending instalments of their active loans due in or before
// the period (oldest first), as many whole instalments as fit in
// available (the net pay before it), skipping any another payslip has
// already taken. It returns their ids and total; nothing is written.
func PlanPayrollDeduction(ctx context.Context, tx pgx.Tx, userID, payslipID string, month, year int, available float64) ([]string, float64, error) {
	rows, err := tx.Query(ctx, `
		SELECT i.id::text, i.amount
		FROM employee_loan_installments i JOIN employee_loans l ON l.id = i.loan_id
		WHERE l.user_id::text = $1 AND l.status = 'active' AND i.status = 'pending'
		  AND (i.due_year * 12 + i.due_month) <= ($2 * 12 + $3)
		  AND (i.payslip_id IS NULL OR i.payslip_id::text = $4)
		ORDER BY i.due_year, i.due_month, l.created_at, i.seq`, userID, year, month, payslipID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var ids []string
	var total float64
	for rows.Next() {
		var id string
		var amt float64
		if err := rows.Scan(&id, &amt); err != nil {
			return nil, 0, err
		}
		if total+amt > available+0.001 {
			break // the rest waits for a month with room (oldest first, so stop here)
		}
		ids = append(ids, id)
		total = round2(total + amt)
	}
	return ids, total, rows.Err()
}

// AssignPayrollDeduction ties the planned instalments to the payslip
// (and frees any it no longer takes, after a recompute).
func AssignPayrollDeduction(ctx context.Context, tx pgx.Tx, payslipID string, ids []string) error {
	if _, err := tx.Exec(ctx, `UPDATE employee_loan_installments SET payslip_id = NULL
		WHERE payslip_id::text = $1 AND status = 'pending' AND NOT (id::text = ANY($2))`, payslipID, ids); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `UPDATE employee_loan_installments SET payslip_id = $1::uuid WHERE id::text = ANY($2) AND status = 'pending'`, payslipID, ids)
	return err
}

// ErrRecomputeNeeded: an instalment a payslip deducts was repaid by hand
// since the run was computed; the run must be recomputed first.
var ErrRecomputeNeeded = errors.New("loan instalments changed since this payroll run was computed")

// RecoverForRun runs when a payroll run is finalized: the instalments its
// payslips deducted are recovered (dated the period's last day), one
// repayment per loan is recorded, and per employee
//
//	Dr Salaries Payable / Cr Employee Loans (principal) / Cr Interest Income
//
// is posted. Loans with nothing left close.
func RecoverForRun(ctx context.Context, tx pgx.Tx, runID string, month, year int, userID string) error {
	periodEnd := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, -1).Format("2006-01-02")
	type slip struct {
		id, userID, name string
		branchID         *string
		deduction        float64
	}
	srows, err := tx.Query(ctx, `
		SELECT p.id::text, p.user_id::text, u.name, u.branch_id::text, p.loan_deduction
		FROM payslips p JOIN users u ON u.id = p.user_id WHERE p.payroll_run_id::text = $1 AND p.loan_deduction > 0`, runID)
	if err != nil {
		return err
	}
	var slips []slip
	for srows.Next() {
		var s slip
		if err := srows.Scan(&s.id, &s.userID, &s.name, &s.branchID, &s.deduction); err != nil {
			srows.Close()
			return err
		}
		slips = append(slips, s)
	}
	srows.Close()
	if err := srows.Err(); err != nil {
		return err
	}
	for _, s := range slips {
		type inst struct {
			id, loanID          string
			principal, interest float64
		}
		irows, err := tx.Query(ctx, `SELECT id::text, loan_id::text, principal_part, interest_part FROM employee_loan_installments
			WHERE payslip_id::text = $1 AND status = 'pending' ORDER BY loan_id, seq FOR UPDATE`, s.id)
		if err != nil {
			return err
		}
		var insts []inst
		var total float64
		for irows.Next() {
			var i inst
			if err := irows.Scan(&i.id, &i.loanID, &i.principal, &i.interest); err != nil {
				irows.Close()
				return err
			}
			insts = append(insts, i)
			total += i.principal + i.interest
		}
		irows.Close()
		if err := irows.Err(); err != nil {
			return err
		}
		if math.Abs(round2(total)-s.deduction) > 0.005 {
			return fmt.Errorf("%w (%s)", ErrRecomputeNeeded, s.name)
		}
		perLoan := map[string][2]float64{}
		var order []string
		var principal, interest float64
		for _, i := range insts {
			if _, ok := perLoan[i.loanID]; !ok {
				order = append(order, i.loanID)
			}
			v := perLoan[i.loanID]
			perLoan[i.loanID] = [2]float64{v[0] + i.principal, v[1] + i.interest}
			principal += i.principal
			interest += i.interest
			if _, err := tx.Exec(ctx, `UPDATE employee_loan_installments SET status = 'recovered', recovered_on = $2::date, recovered_via = 'payroll'
				WHERE id::text = $1`, i.id, periodEnd); err != nil {
				return err
			}
		}
		for _, loanID := range order {
			v := perLoan[loanID]
			if _, err := tx.Exec(ctx, `
				INSERT INTO employee_loan_repayments (merchant_id, loan_id, amount, principal_part, interest_part, method, paid_on, payroll_run_id, created_by)
				VALUES (current_setting('app.tenant_id')::uuid, $1::uuid, $2, $3, $4, 'payroll', $5::date, $6::uuid, $7)`,
				loanID, round2(v[0]+v[1]), round2(v[0]), round2(v[1]), periodEnd, runID, userID); err != nil {
				return err
			}
			if err := closeIfDone(ctx, tx, loanID, periodEnd, false); err != nil {
				return err
			}
		}
		principal, interest = round2(principal), round2(interest)
		lines := []accounting.JournalLine{
			{AccountCode: accounting.AccountSalariesPayable, Debit: round2(principal + interest), PartyType: "employee", PartyID: s.userID},
			{AccountCode: accounting.AccountEmployeeLoans, Credit: principal, PartyType: "employee", PartyID: s.userID},
		}
		if interest > 0 {
			lines = append(lines, accounting.JournalLine{AccountCode: accounting.AccountInterestIncome, Credit: interest})
		}
		if _, err := accounting.PostJournalEntryOnDate(ctx, tx, derefStr(s.branchID), "loan_repayment", runID,
			fmt.Sprintf("Loan recovery from %s's salary, %02d/%d", s.name, month, year), userID, periodEnd, lines); err != nil {
			return err
		}
	}
	return nil
}
