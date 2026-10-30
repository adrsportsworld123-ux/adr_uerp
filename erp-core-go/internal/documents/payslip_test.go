package documents

import (
	"strings"
	"testing"
)

func TestPayslip(t *testing.T) {
	in := PayslipInput{Period: "October 2026", Status: "finalized", Employee: "Ravi Kumar", EmployeeCode: "EMP001",
		BankAccount: "123456789012", DaysInPeriod: "31.00", DaysPaid: "29.81",
		LeavePaid: "4.0", LeaveUnpaid: "1.0", Absent: "0.0", PermissionUnpaidMinutes: 90,
		Basic: "28838.71", HRA: "0.00", Special: "0.00", Other: "0.00", Commission: "0.00", Gross: "28838.71",
		PF: "0.00", ESIDeduction: "0.00", PT: "200.00", TDS: "0.00", LWF: "0.00", Loan: "5000.00",
		TotalDeductions: "5200.00", NetPay: "23638.71", EmployerPF: "1800.00", NetPayWords: "Rupees … Only"}
	d := Payslip(in)
	if d.Stamp != "" || d.Title != "PAYSLIP" || d.Date != "For October 2026" {
		t.Fatalf("header: %q %q %q", d.Title, d.Date, d.Stamp)
	}
	// Basic always; zero earnings and deductions left out; loan recovery shown.
	if len(d.Rows) != 2 || d.Rows[0].Cells[0] != "Basic" || d.Rows[0].Cells[2] != "Professional tax" || d.Rows[1].Cells[2] != "Loan / advance recovery" || d.Rows[1].Cells[0] != "" {
		t.Errorf("rows: %+v", d.Rows)
	}
	meta := map[string]string{}
	for _, kv := range d.Meta {
		meta[kv[0]] = kv[1]
	}
	if meta["Bank a/c"] != "XXXXXXXX9012" || meta["Days paid"] == "" || meta["PAN"] != "" {
		t.Errorf("meta: %v", d.Meta)
	}
	notes := strings.Join(d.Notes, "\n")
	for _, want := range []string{"Net pay in words: Rupees", "paid leave 4 day(s)", "unpaid leave 1 day(s)", "unpaid permission 90 min", "Employer's contribution", "PF 1,800.00"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes lack %q: %s", want, notes)
		}
	}
	if strings.Contains(notes, "absent") {
		t.Errorf("no absences, yet: %s", notes)
	}
	in.Status = "draft"
	if Payslip(in).Stamp != "DRAFT" {
		t.Error("a draft run's payslip is stamped DRAFT")
	}
	if _, err := Render(Letterhead{Name: "Acme"}, d, FormatA4); err != nil {
		t.Fatal(err)
	}
}
