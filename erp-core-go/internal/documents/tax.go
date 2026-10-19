package documents

import (
	"strconv"

	"erp-core-go/internal/taxes"
)

func num(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// How a document prints its tax (migrations/060, internal/taxes): under
// the GST split regime every line shows its taxable value, GST rate and
// CGST + SGST (intra-state) or IGST (inter-state), the totals list each
// tax separately, and a rate-wise tax summary follows the totals. Under
// a single-tax regime one column and one total carry the merchant's own
// label (VAT, Sales Tax). A document built without a breakup (nil) keeps
// the original single "Tax" column and total.

// LineTax is one line's tax as the document prints it.
type LineTax struct {
	HSN, Rate, Taxable, CGST, SGST, IGST string
}

// Table is a small grid printed after the totals (the tax summary).
type Table struct {
	Title   string
	Columns []Column
	Rows    [][]string
}

func splitBreakup(b *taxes.Breakup) bool { return b != nil && b.Regime != taxes.RegimeSingle }

// itemColumns: the line-item columns of a sale invoice or quotation,
// from "Rate" (the unit price) on; the caller puts # and Item first.
func itemColumns(b *taxes.Breakup, discount bool) []Column {
	cols := []Column{{Title: "Qty", Width: 8, Right: true}, {Title: "Rate", Width: 12, Right: true}}
	if discount {
		cols = append(cols, Column{Title: "Disc.", Width: 10, Right: true})
	}
	switch {
	case splitBreakup(b):
		cols = append(cols, Column{Title: "Taxable", Width: 13, Right: true}, Column{Title: "GST %", Width: 8, Right: true})
		if b.InterState {
			cols = append(cols, Column{Title: "IGST", Width: 12, Right: true})
		} else {
			cols = append(cols, Column{Title: "CGST", Width: 11, Right: true}, Column{Title: "SGST", Width: 11, Right: true})
		}
	case b != nil:
		cols = append(cols, Column{Title: b.TaxLabel, Width: 11, Right: true})
	default:
		cols = append(cols, Column{Title: "Tax", Width: 11, Right: true})
	}
	return append(cols, Column{Title: "Amount", Width: 14, Right: true})
}

// itemCells matches itemColumns for one line.
func itemCells(b *taxes.Breakup, discount bool, qty, price, disc, tax, total string, t LineTax) []string {
	cells := []string{Qty(qty), Amount(price)}
	if discount {
		cells = append(cells, Amount(disc))
	}
	if splitBreakup(b) {
		cells = append(cells, Amount(t.Taxable), taxes.FormatRate(num(t.Rate)))
		if b.InterState {
			cells = append(cells, Amount(t.IGST))
		} else {
			cells = append(cells, Amount(t.CGST), Amount(t.SGST))
		}
	} else {
		cells = append(cells, Amount(tax))
	}
	return append(cells, Amount(total))
}

// itemName is the Item cell: name, then SKU and HSN on the next line.
func itemName(name, sku, hsn string, b *taxes.Breakup) string {
	sub := sku
	if splitBreakup(b) && hsn != "" {
		if sub != "" {
			sub += " · "
		}
		sub += "HSN " + hsn
	}
	if sub != "" {
		name += "\n" + sub
	}
	return name
}

// taxTotals: the tax lines under the subtotal.
func taxTotals(b *taxes.Breakup, label, tax string) []Total {
	if b == nil || len(b.Rows) == 0 {
		return []Total{{Label: label, Value: INR(tax)}}
	}
	out := make([]Total, 0, len(b.Rows))
	for _, r := range b.Rows {
		out = append(out, Total{Label: r.Label, Value: INR(r.Amount)})
	}
	return out
}

// taxSummary: the rate-wise summary a GST invoice carries; nil when
// there is nothing to summarise.
func taxSummary(b *taxes.Breakup) *Table {
	if b == nil || len(b.Summary) == 0 {
		return nil
	}
	t := &Table{Title: "Tax summary"}
	split := splitBreakup(b)
	rateTitle := b.TaxLabel + " rate"
	t.Columns = []Column{{Title: rateTitle, Width: 14}, {Title: "Taxable value", Width: 16, Right: true}}
	switch {
	case split && b.InterState:
		t.Columns = append(t.Columns, Column{Title: "IGST", Width: 14, Right: true})
	case split:
		t.Columns = append(t.Columns, Column{Title: "CGST", Width: 14, Right: true}, Column{Title: "SGST", Width: 14, Right: true})
	}
	hasCess := split && !taxes.IsZero(b.Cess)
	if hasCess {
		t.Columns = append(t.Columns, Column{Title: "Cess", Width: 12, Right: true})
	}
	t.Columns = append(t.Columns, Column{Title: "Total tax", Width: 14, Right: true})
	for _, s := range b.Summary {
		rate := taxes.FormatRate(num(s.Rate)) + "%"
		row := []string{rate, Amount(s.Taxable)}
		switch {
		case split && b.InterState:
			row = append(row, Amount(s.IGST))
		case split:
			half := taxes.FormatRate(num(s.Rate) / 2)
			row[0] = rate + " (" + half + " + " + half + ")"
			row = append(row, Amount(s.CGST), Amount(s.SGST))
		}
		if hasCess {
			row = append(row, Amount(s.Cess))
		}
		t.Rows = append(t.Rows, append(row, Amount(s.Tax)))
	}
	return t
}

// supplyMeta: the place of supply line of a GST invoice.
func supplyMeta(b *taxes.Breakup) [][2]string {
	if !splitBreakup(b) || b.PlaceOfSupply == "" {
		return nil
	}
	return [][2]string{{"Place of supply", taxes.PlaceOfSupply(b.PlaceOfSupply)}}
}
