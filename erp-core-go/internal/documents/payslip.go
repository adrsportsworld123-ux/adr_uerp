package documents

import (
	"strconv"
	"strings"
)

// PayslipInput is one employee's payslip for a month (§3.78): the payroll
// run's figures as the API's NUMERIC strings.
type PayslipInput struct {
	Period, Status                          string // "October 2026"; the run's status (draft prints a stamp)
	Employee, EmployeeCode                  string
	Designation, Department, Branch, Joined string
	PAN, UAN, ESI, BankAccount, BankIFSC    string
	DaysInPeriod, DaysPaid                  string
	LeavePaid, LeaveUnpaid, Absent          string
	PermissionUnpaidMinutes                 int
	Basic, HRA, Special, Other, Commission  string
	Gross                                   string
	PF, ESIDeduction, PT, TDS, LWF, Loan    string
	TotalDeductions, NetPay                 string
	EmployerPF, EmployerESI, EmployerLWF    string
	NetPayWords                             string
}

// Payslip: earnings and deductions side by side, the days the pay is for
// (leave paid and unpaid, absences, unpaid permission), net pay in
// figures and words, and the employer's own contributions as a note.
func Payslip(in PayslipInput) Document {
	d := Document{
		Title: "PAYSLIP",
		Date:  "For " + in.Period,
		Party: &Party{Label: "Employee", Name: in.Employee},
	}
	if in.Status != "finalized" {
		d.Stamp = "DRAFT"
	}
	for _, l := range []string{
		join(" · ", in.EmployeeCode, in.Designation, in.Department),
		prefixed("Branch: ", in.Branch),
		prefixed("Joined: ", in.Joined),
	} {
		if l != "" {
			d.Party.Lines = append(d.Party.Lines, l)
		}
	}
	for _, kv := range [][2]string{
		{"PAN", in.PAN}, {"UAN", in.UAN}, {"ESI no.", in.ESI}, {"Bank a/c", maskAccount(in.BankAccount)}, {"IFSC", in.BankIFSC},
		{"Days in month", Qty(in.DaysInPeriod)}, {"Days paid", Qty(in.DaysPaid)},
	} {
		if kv[1] != "" {
			d.Meta = append(d.Meta, kv)
		}
	}
	type line struct{ label, amount string }
	earn := []line{{"Basic", in.Basic}, {"HRA", in.HRA}, {"Special allowance", in.Special}, {"Other allowances", in.Other}, {"Commission", in.Commission}}
	ded := []line{{"Provident fund", in.PF}, {"ESI", in.ESIDeduction}, {"Professional tax", in.PT}, {"Income tax (TDS)", in.TDS},
		{"Labour welfare fund", in.LWF}, {"Loan / advance recovery", in.Loan}}
	keep := func(ls []line, always int) []line {
		out := []line{}
		for i, l := range ls {
			if i < always || isNonZero(l.amount) {
				out = append(out, l)
			}
		}
		return out
	}
	earn, ded = keep(earn, 1), keep(ded, 0)
	d.Columns = []Column{{Title: "Earnings", Width: 3}, {Title: "Amount", Width: 1.6, Right: true}, {Title: "Deductions", Width: 3}, {Title: "Amount", Width: 1.6, Right: true}}
	for i := 0; i < max(len(earn), len(ded)); i++ {
		cells := make([]string, 4)
		if i < len(earn) {
			cells[0], cells[1] = earn[i].label, Amount(earn[i].amount)
		}
		if i < len(ded) {
			cells[2], cells[3] = ded[i].label, Amount(ded[i].amount)
		}
		d.Rows = append(d.Rows, Row{Cells: cells})
	}
	d.Totals = []Total{{Label: "Gross earnings", Value: Amount(in.Gross)}, {Label: "Total deductions", Value: Amount(in.TotalDeductions)},
		{Label: "Net pay", Value: Amount(in.NetPay), Bold: true}}
	if in.NetPayWords != "" {
		d.Notes = append(d.Notes, "Net pay in words: "+in.NetPayWords)
	}
	var days []string
	if isNonZero(in.LeavePaid) {
		days = append(days, "paid leave "+Qty(in.LeavePaid)+" day(s)")
	}
	if isNonZero(in.LeaveUnpaid) {
		days = append(days, "unpaid leave "+Qty(in.LeaveUnpaid)+" day(s)")
	}
	if isNonZero(in.Absent) {
		days = append(days, "absent "+Qty(in.Absent)+" day(s)")
	}
	if in.PermissionUnpaidMinutes > 0 {
		days = append(days, "unpaid permission "+strconv.Itoa(in.PermissionUnpaidMinutes)+" min")
	}
	if len(days) > 0 {
		d.Notes = append(d.Notes, "This month: "+strings.Join(days, ", ")+".")
	}
	var er []string
	for _, kv := range [][2]string{{"PF", in.EmployerPF}, {"ESI", in.EmployerESI}, {"LWF", in.EmployerLWF}} {
		if isNonZero(kv[1]) {
			er = append(er, kv[0]+" "+Amount(kv[1]))
		}
	}
	if len(er) > 0 {
		d.Notes = append(d.Notes, "Employer's contribution (not deducted from pay): "+strings.Join(er, ", ")+".")
	}
	return d
}

func join(sep string, parts ...string) string {
	out := []string{}
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, strings.TrimSpace(p))
		}
	}
	return strings.Join(out, sep)
}

func prefixed(prefix, s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return prefix + s
}

// maskAccount shows only the last four digits of a bank account.
func maskAccount(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 4 {
		return s
	}
	return strings.Repeat("X", len(s)-4) + s[len(s)-4:]
}
