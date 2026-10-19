package printing

import (
	"strings"
	"testing"
)

func TestReportRoundTrip(t *testing.T) {
	raw := BuildReport(ReportPrint{
		MerchantName: "Acme Sports", BranchName: "MG Road", Title: "Z REPORT", Subtitle: []string{"2026-10-17", "Z no. 4"},
		Sections: []ReportSection{
			{Heading: "Sales", Rows: []ReportRow{{Label: "Net sales", Value: "12345.00", Strong: true}, {Label: "First: SO-1"}}},
			{Heading: "Tenders", Rows: []ReportRow{{Label: "A very long label that cannot share its line", Value: "99.00"}}},
		},
	})
	var text strings.Builder
	for _, c := range Decode(raw) {
		text.WriteString(c.Text + "\n")
	}
	got := text.String()
	for _, want := range []string{"Acme Sports", "Z REPORT", "Z no. 4", "SALES", "First: SO-1", "TENDERS"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if !strings.Contains(got, "Net sales"+strings.Repeat(" ", ReportWidth-len("Net sales")-len("12345.00"))+"12345.00") {
		t.Errorf("value not right-aligned on its line:\n%s", got)
	}
}

func TestPadRow(t *testing.T) {
	if l := padRow("Cash", "10.00", 32); len(l) != 1 || len(l[0]) != 32 {
		t.Errorf("short row: %q", l)
	}
	if l := padRow(strings.Repeat("x", 30), "10.00", 32); len(l) != 2 || !strings.HasSuffix(l[1], "10.00") || len(l[1]) != 32 {
		t.Errorf("wrapped row: %q", l)
	}
}
