package documents

import (
	"regexp"
	"strings"
	"unicode"

	"erp-core-go/internal/taxes"
)

// Builders format money the Indian way with the rupee sign (Amount, INR).
// A branch in another currency (tax_settings.currency_code, migrations/061)
// gets every amount renamed and regrouped when the document is rendered —
// "₹1,23,456.50" becomes "$123,456.50", "AED 123,456.50" — so no builder
// needs to know the currency. An INR document is never touched.

var moneyToken = regexp.MustCompile(`₹?\d[\d,]*\.\d{2}\b`)

// localize rewrites every amount in d for c.
func localize(d Document, c taxes.Currency) Document {
	conv := func(s string) string { return convertMoney(s, c) }
	each := func(ss []string) []string {
		out := make([]string, len(ss))
		for i, s := range ss {
			out[i] = conv(s)
		}
		return out
	}
	rows := make([]Row, len(d.Rows))
	for i, r := range d.Rows {
		rows[i] = Row{Cells: each(r.Cells), Title: r.Title, Detail: conv(r.Detail), Amount: conv(r.Amount)}
	}
	d.Rows = rows
	totals := make([]Total, len(d.Totals))
	for i, t := range d.Totals {
		totals[i] = Total{Label: t.Label, Value: conv(t.Value), Bold: t.Bold}
	}
	d.Totals = totals
	pays := make([][2]string, len(d.Payments))
	for i, p := range d.Payments {
		pays[i] = [2]string{p[0], conv(p[1])}
	}
	d.Payments = pays
	if d.TaxTable != nil {
		t := *d.TaxTable
		t.Rows = make([][]string, len(d.TaxTable.Rows))
		for i, r := range d.TaxTable.Rows {
			t.Rows[i] = each(r)
		}
		d.TaxTable = &t
	}
	d.Notes = each(d.Notes)
	return d
}

func convertMoney(s string, c taxes.Currency) string {
	return moneyToken.ReplaceAllStringFunc(s, func(tok string) string {
		sym := strings.HasPrefix(tok, "₹")
		tok = strings.TrimPrefix(tok, "₹")
		whole, frac, _ := strings.Cut(strings.ReplaceAll(tok, ",", ""), ".")
		out := groupThousands(whole) + "." + frac
		if sym {
			out = symbolPrefix(c.Symbol) + out
		}
		return out
	})
}

// symbolPrefix: "$" sticks to the number, a letter symbol ("AED") gets a space.
func symbolPrefix(sym string) string {
	if sym == "" {
		return ""
	}
	r := []rune(sym)
	if unicode.IsLetter(r[len(r)-1]) {
		return sym + " "
	}
	return sym
}

func groupThousands(d string) string {
	if len(d) <= 3 {
		return d
	}
	var b strings.Builder
	pre := len(d) % 3
	if pre > 0 {
		b.WriteString(d[:pre])
	}
	for i := pre; i < len(d); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(d[i : i+3])
	}
	return b.String()
}
