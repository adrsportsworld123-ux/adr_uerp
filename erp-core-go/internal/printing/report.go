package printing

import (
	"strings"
	"unicode/utf8"
)

// ReportPrint is a till report (the shift X report, the day's Z report)
// laid out for a thermal printer: a heading, then sections of label/value
// rows. The caller (internal/sales) decides the content; this only lays it
// out, the same way BuildReceipt lays out a sale.
type ReportPrint struct {
	MerchantName string
	BranchName   string
	Header       []string // the branch letterhead's lines under its name
	Title        string   // "X REPORT", "Z REPORT"
	Subtitle     []string // "Shift 3 · Till 1", "18 Oct 2026", "Printed ..."
	Sections     []ReportSection
	Footer       []string
}

type ReportSection struct {
	Heading string
	Rows    []ReportRow
}

// ReportRow is one line: a label on the left and a value on the right.
// Strong rows print bold. A row with no value prints its label alone.
type ReportRow struct {
	Label  string
	Value  string
	Strong bool
}

// ReportWidth is the characters per line on a standard 80 mm (Font A)
// receipt, the same rule width BuildReceipt uses.
const ReportWidth = len(ruleLine)

// padRow puts label and value on one line of width w, the value right-
// aligned; a label too long for both is wrapped onto its own line first.
func padRow(label, value string, w int) []string {
	if value == "" {
		return []string{label}
	}
	ll, vl := utf8.RuneCountInString(label), utf8.RuneCountInString(value)
	if ll+1+vl <= w {
		return []string{label + strings.Repeat(" ", w-ll-vl) + value}
	}
	return []string{label, strings.Repeat(" ", max(w-vl, 0)) + value}
}

// BuildReport renders r as an ESC/POS print job.
func BuildReport(r ReportPrint) []byte {
	b := New().AlignCenter().
		Bold(true).DoubleSize(true).Line(r.MerchantName).DoubleSize(false).Bold(false).
		Line(r.BranchName)
	for _, l := range r.Header {
		b.Line(l)
	}
	b.FeedLines(1).Bold(true).DoubleSize(true).Line(r.Title).DoubleSize(false).Bold(false)
	for _, l := range r.Subtitle {
		b.Line(l)
	}
	b.AlignLeft().Line(ruleLine)
	for _, s := range r.Sections {
		if s.Heading != "" {
			b.Bold(true).Line(strings.ToUpper(s.Heading)).Bold(false)
		}
		for _, row := range s.Rows {
			if row.Strong {
				b.Bold(true)
			}
			for _, l := range padRow(row.Label, row.Value, ReportWidth) {
				b.Line(l)
			}
			if row.Strong {
				b.Bold(false)
			}
		}
		b.Line(ruleLine)
	}
	if len(r.Footer) > 0 {
		b.AlignCenter()
		for _, l := range r.Footer {
			b.Line(l)
		}
	}
	b.FeedLines(3).Cut()
	return b.Bytes()
}
