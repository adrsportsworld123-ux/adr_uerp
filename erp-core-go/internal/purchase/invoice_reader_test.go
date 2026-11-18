package purchase

import (
	"encoding/json"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

// The fixtures are real supplier invoices (PurchaseInvoices/*.pdf) as the
// browser sends them: each run of text with its position, from pdf.js.
func loadPages(t *testing.T, name string) []docPage {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var pages []docPage
	if err := json.Unmarshal(b, &pages); err != nil {
		t.Fatal(err)
	}
	return pages
}

func readInvoice(t *testing.T, name string, opt invoiceOptions, brands []string) (rawDoc, []readLine) {
	t.Helper()
	if err := opt.normalize(); err != nil {
		t.Fatal(err)
	}
	doc, err := parseDocPages(loadPages(t, name), defaultInvoiceColumns(), opt)
	if err != nil {
		t.Fatal(err)
	}
	return doc, buildLines(doc.Rows, opt, brands)
}

func sumTotals(lines []readLine) float64 {
	s := 0.0
	for _, l := range lines {
		v, _ := strconv.ParseFloat(l.LineTotal, 64)
		s += v
	}
	return s
}

func TestReadInvoice12(t *testing.T) {
	doc, lines := readInvoice(t, "invoice12.json", invoiceOptions{}, []string{"SS", "SG", "TON", "Mavis"})
	if doc.InvoiceNumber != "SC/SL/26-27/5694" || doc.InvoiceDate != "2026-11-15" || doc.Total != "17790.00" {
		t.Fatalf("header: %q %q %q", doc.InvoiceNumber, doc.InvoiceDate, doc.Total)
	}
	if len(doc.GSTINs) == 0 || doc.GSTINs[0] != "33ADFFS1165G1ZA" {
		t.Fatalf("gstin: %v", doc.GSTINs)
	}
	if doc.SubtotalQty != "25" || doc.SubtotalTotal != "17790.00" {
		t.Fatalf("subtotal row: %q %q", doc.SubtotalQty, doc.SubtotalTotal)
	}
	want := []struct{ name, qty, mrp, gst, total, cost, hsn, brand string }{
		{"MAVIS 350 SINGLE", "18", "252.00", "0", "3300.00", "183.333333", "", "Mavis"},
		{"EM QUANTUM REBEL 900 KW BAT 1,2,3 - SIZE 3", "1", "3399.00", "5", "2210.00", "2104.761905", "", ""},
		{"SS MASTER NO.4 KW BAT", "1", "2900.00", "5", "1885.00", "1795.238095", "", "SS"},
		{"TON MAXIMUS KW BAT - SIZE 4", "1", "3330.00", "5", "2165.00", "2061.904762", "", "TON"},
		{"SG SCORER CLASSIC NO.5 KW BAT", "1", "2999.00", "5", "1950.00", "1857.142857", "", "SG"},
		{"SS IPL PLAYERS KW BAT NO 5", "1", "3530.00", "5", "2295.00", "2185.714286", "", "SS"},
		{"Bat SS Devil No.6 K/w", "1", "3200.00", "5", "2080.00", "1980.952381", "95069920", "SS"},
		{"SS MASTER K/W BAT SIZE 6", "1", "2930.00", "12", "1905.00", "1700.892857", "", "SS"},
	}
	if len(lines) != len(want) {
		t.Fatalf("%d lines: %+v", len(lines), lines)
	}
	for i, w := range want {
		l := lines[i]
		if l.ItemName != w.name || l.Quantity != w.qty || l.MRP != w.mrp || l.GSTRate != w.gst || l.LineTotal != w.total ||
			l.UnitCost != w.cost || l.HSNCode != w.hsn || l.Brand != w.brand {
			t.Errorf("line %d:\n got  %q qty %s mrp %s gst %s total %s cost %s hsn %q brand %q\n want %+v",
				i+1, l.ItemName, l.Quantity, l.MRP, l.GSTRate, l.LineTotal, l.UnitCost, l.HSNCode, l.Brand, w)
		}
	}
	if s := sumTotals(lines); math.Abs(s-17790) > 0.005 {
		t.Fatalf("lines add to %v", s)
	}
	// The worked-back cost puts the paise back: Σ qty × cost × (1+gst) = the printed total.
	back := 0.0
	for _, l := range lines {
		q, _ := strconv.ParseFloat(l.Quantity, 64)
		c, _ := strconv.ParseFloat(l.UnitCost, 64)
		g, _ := strconv.ParseFloat(l.GSTRate, 64)
		back += round2(q * c * (1 + g/100))
	}
	if math.Abs(back-17790) > 0.05 {
		t.Fatalf("costs give back %v", back)
	}
	if lines[1].SaleGSTRate != "5" || lines[0].SaleGSTRate != "" {
		t.Fatalf("sale GST: %q %q", lines[1].SaleGSTRate, lines[0].SaleGSTRate)
	}
}

// Invoice 11's variant column is the size, printed every which way; the
// template's default rules give the names imported by hand before
// (PurchaseInvoices/11.csv).
func TestReadInvoice11Sizes(t *testing.T) {
	doc, lines := readInvoice(t, "invoice11.json", invoiceOptions{}, nil)
	if doc.InvoiceNumber != "SC/SL/26-27/5391" || doc.InvoiceDate != "2026-11-03" || doc.Total != "8875.00" {
		t.Fatalf("header: %q %q %q", doc.InvoiceNumber, doc.InvoiceDate, doc.Total)
	}
	want := map[int]string{
		1: "FORVO HALF TIGHTS BLACK WT - XSMALL", 2: "FORVO HALF TIGHTS BLACK WT - MEDIUM", 3: "FORVO HALF TIGHTS BLACK WT - SMALL",
		4: "FORVO HALF TIGHTS BLACK WT - XLARGE", 5: "FORVO HALF TIGHTS BLACK WT - LARGE", 6: "FORVO HALF TIGHTS BLACK WT - 2XLARGE",
		7: "MITUSHI TIGHTS NAVY BLUE - SMALL", 8: "MITUSHI TIGHTS NAVY BLUE - MEDIUM", 9: "MITUSHI TIGHTS NAVY BLUE - LARGE",
		10: "MITUSHI TIGHTS NAVY BLUE - XLARGE", 11: "MITUSHI TIGHTS BLACK - SMALL", 12: "MITUSHI TIGHTS BLACK - MEDIUM",
		13: "MITUSHI TIGHTS BLACK - LARGE", 14: "MITUSHI TIGHTS BLACK - XLARGE", 15: "MITUSHI TIGHTS BLACK - 2XLARGE",
	}
	if len(lines) != 25 {
		t.Fatalf("%d lines", len(lines))
	}
	for row, name := range want {
		if lines[row-1].ItemName != name {
			t.Errorf("line %d: %q (variant %q), want %q", row, lines[row-1].ItemName, lines[row-1].Variant, name)
		}
	}
	if s := sumTotals(lines); math.Abs(s-8875) > 0.005 {
		t.Fatalf("lines add to %v", s)
	}
	q := 0.0
	for _, l := range lines {
		v, _ := strconv.ParseFloat(l.Quantity, 64)
		q += v
	}
	if q != 49 {
		t.Fatalf("quantity %v", q)
	}
	// Letters, for a supplier the shop writes that way.
	_, lines = readInvoice(t, "invoice11.json", invoiceOptions{SizeStyle: "letters", NameFormat: "{item} ({size})"}, nil)
	if lines[5].ItemName != "FORVO HALF TIGHTS BLACK WT (XXL)" {
		t.Fatalf("letters: %q", lines[5].ItemName)
	}
}

// Invoice 1 runs over two pages (the heading repeats on the second) and
// has an MRP printed in the item's name.
func TestReadInvoice1TwoPages(t *testing.T) {
	doc, lines := readInvoice(t, "invoice1.json", invoiceOptions{}, nil)
	if doc.InvoiceNumber != "SC/SL/26-27/3872" || doc.InvoiceDate != "2026-09-14" {
		t.Fatalf("header: %q %q", doc.InvoiceNumber, doc.InvoiceDate)
	}
	total, _ := strconv.ParseFloat(doc.Total, 64)
	if s := sumTotals(lines); total == 0 || math.Abs(s-total) > 0.005 {
		t.Fatalf("lines add to %v, invoice %v (%d lines)", s, total, len(lines))
	}
	l := lines[10] // SEGA EDGE R/SHOE NAVY BLUE MRP 749, SIZE 5
	if l.ItemName != "SEGA EDGE R/SHOE NAVY BLUE - SIZE 5" || l.MRP != "749.00" || l.GSTRate != "5" {
		t.Fatalf("line 11: %+v", l)
	}
	if lines[3].ItemName != "R1 KITBAG 1500" { // R1KBGRAYN is the supplier's own code
		t.Fatalf("line 4: %q", lines[3].ItemName)
	}
	if lines[5].ItemName != "R1 KITBAG 1000 - SG" {
		t.Fatalf("line 6: %q", lines[5].ItemName)
	}
}

func TestReadInvoice3Variants(t *testing.T) {
	doc, lines := readInvoice(t, "invoice3.json", invoiceOptions{}, nil)
	total, _ := strconv.ParseFloat(doc.Total, 64)
	if s := sumTotals(lines); total == 0 || math.Abs(s-total) > 0.005 {
		t.Fatalf("lines add to %v, invoice %v (%d lines)", s, total, len(lines))
	}
	got := map[int]string{}
	for _, l := range lines {
		got[l.Row] = l.ItemName
	}
	for row, name := range map[int]string{
		1:  "NIVIA AIRSTRIKE GOAL KEEPER GLOVES - MEDIUM",
		4:  "TYNOR KNEECAP AIR 103 - SMALL",
		7:  "TYNOR KNEECAP AIR 103 - XLARGE",
		12: "MEDAL ROPE PLAIN WOT - YELLOW",
		13: "MEDAL ROPE PLAIN WOT - DARK GREEN",
		15: "BLACK SHAKER",
	} {
		if got[row] != name {
			t.Errorf("line %d: %q, want %q", row, got[row], name)
		}
	}
	if lines[14].MRP != "220.00" {
		t.Fatalf("MRP from the name: %q", lines[14].MRP)
	}
	// A template alias for the supplier's own code.
	_, lines = readInvoice(t, "invoice3.json", invoiceOptions{SizeAliases: map[string]string{"DIPKAGLNS": "SMALL"}}, nil)
	if lines[1].ItemName != "DIPAK KARATAE GLOVE NEW S - SMALL" {
		t.Fatalf("alias: %q", lines[1].ItemName)
	}
	// As a batch instead of a size.
	_, lines = readInvoice(t, "invoice3.json", invoiceOptions{VariantUse: "batch"}, nil)
	if lines[3].ItemName != "TYNOR KNEECAP AIR 103" || lines[3].BatchNo != "S-SIZE" {
		t.Fatalf("batch: %+v", lines[3])
	}
}

func TestExtractSize(t *testing.T) {
	opt := invoiceOptions{}
	if err := opt.normalize(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		variant string
		mrp     float64
		name    string
		want    string
	}{
		{"MRP 252", 252, "MAVIS 350", ""},
		{"SIZE4 MRP 3330", 3330, "TON", "SIZE 4"},
		{"SIZE -5", 2690, "YONEX", "SIZE 5"},
		{"MITIGSIZE-XL", 360, "MITUSHI", "XLARGE"},
		{"MITIG- SIZEXXL370", 370, "MITUSHI", "2XLARGE"},
		{"NEW-M", 340, "MITUSHI", "MEDIUM"},
		{"X LARGE MRP 180", 180, "FORVO", "XLARGE"},
		{"S-SIZE", 383, "TYNOR", "SMALL"},
		{"MENS 2415", 2415, "SS PLATINO B/GLOVES", "MENS"},
		{"MENS NEW", 1200, "R1 CLASSIC WK PAD MENS", ""},
		{"30 MRP 2500", 2500, "ALFA Y 30 JNR HOCKEY STICK", ""},
		{"34 MRP 2500", 2500, "ALFA Y 30 JNR HOCKEY STICK", "34"},
		{"450", 450, "HOCKEY GRIP", ""},
		{"-", 2930, "SS MASTER", ""},
		{"R1TESTBG-MENS", 1700, "R1 TEST B/GLOVES WHITE", "MENS"},
	} {
		if got := extractSize(c.variant, c.mrp, c.name, opt); got != c.want {
			t.Errorf("%q (MRP %v): %q, want %q", c.variant, c.mrp, got, c.want)
		}
	}
}

func TestOptionsValidate(t *testing.T) {
	bad := []invoiceOptions{{VariantUse: "colour"}, {CostBasis: "x"}, {NameFormat: "{item}"}, {SaleGSTRate: ptrF(50)}}
	for _, o := range bad {
		if err := o.normalize(); err == nil {
			t.Errorf("%+v accepted", o)
		}
	}
}

func ptrF(v float64) *float64 { return &v }

func TestParseDocDate(t *testing.T) {
	for in, want := range map[string]string{
		"15/11/2026 7:05 PM": "2026-11-15", "3-1-2026": "2026-01-03", "15 Nov 2026": "2026-11-15", "15-Nov-26": "2026-11-15",
		"2026-11-15": "2026-11-15", "31/02/2026": "", "SC/SL/26-27/5694": "",
	} {
		if got := parseDocDate(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestNoTable(t *testing.T) {
	opt := invoiceOptions{}
	_ = opt.normalize()
	_, err := parseDocPages([]docPage{{Items: []docItem{{S: "Hello", X: 1, Y: 1, W: 10}}}}, defaultInvoiceColumns(), opt)
	if err != errNoTable {
		t.Fatalf("got %v", err)
	}
	if _, err := parseDocPages([]docPage{{}}, defaultInvoiceColumns(), opt); err != errNoText {
		t.Fatalf("got %v", err)
	}
}

// What the model answers for a photo of invoice 12 (fenced, numbers as
// numbers or strings, the tax rate without "%"): the same lines as the
// PDF-text path once the template applies.
func TestParseAIInvoice(t *testing.T) {
	answer := "Here it is:\n```json\n" + `{
  "invoice_number": "SC/SL/26-27/5694", "invoice_date": "2026-11-15", "supplier_name": "SPORTS COM",
  "supplier_gstin": "33ADFFS1165G1ZA", "buyer_gstin": "", "invoice_total": 17790, "total_quantity": "25", "lines_total": 17790,
  "lines": [
    {"item": "MAVIS 350 SINGLE", "variant": "MRP 252", "hsn": "-", "quantity": 18, "unit": "PCS", "mrp": 252, "rate": 183.33, "tax_amount": 0, "gst_rate": 0, "line_total": 3300},
    {"item": "TON MAXIMUS KW BAT", "variant": "SIZE4 MRP 3330", "hsn": "", "quantity": "1", "unit": "PCS", "mrp": "3330", "rate": "2,061.9", "tax_amount": 103.1, "gst_rate": "5", "line_total": "2,165"},
    {"item": "SS MASTER K/W BAT SIZE 6", "variant": "-", "hsn": null, "quantity": 1, "unit": "", "mrp": 2930, "rate": 1700.89, "tax_amount": 204.11, "gst_rate": null, "line_total": 1905}
  ]
}` + "\n```"
	doc, err := parseAIInvoice(answer)
	if err != nil {
		t.Fatal(err)
	}
	if doc.InvoiceNumber != "SC/SL/26-27/5694" || doc.InvoiceDate != "2026-11-15" || doc.Total != "17790.00" || doc.SubtotalQty != "25" ||
		len(doc.GSTINs) != 1 {
		t.Fatalf("header: %+v", doc)
	}
	opt := invoiceOptions{}
	_ = opt.normalize()
	lines := buildLines(doc.Rows, opt, []string{"TON"})
	if lines[0].ItemName != "MAVIS 350 SINGLE" || lines[0].Quantity != "18" || lines[0].Unit != "PCS" || lines[0].UnitCost != "183.333333" {
		t.Fatalf("line 1: %+v", lines[0])
	}
	if lines[1].ItemName != "TON MAXIMUS KW BAT - SIZE 4" || lines[1].GSTRate != "5" || lines[1].Brand != "TON" || lines[1].UnitCost != "2061.904762" {
		t.Fatalf("line 2: %+v", lines[1])
	}
	// No rate printed: worked out from the tax amount (204.11 on 1700.89 = 12%).
	if lines[2].GSTRate != "12" || lines[2].UnitCost != "1700.892857" || len(lines[2].Notes) != 1 {
		t.Fatalf("line 3: %+v", lines[2])
	}
	if _, err := parseAIInvoice("I can't read this image."); err == nil {
		t.Fatal("prose accepted")
	}
	if _, err := parseAIInvoice(`{"lines": []}`); err != errNoTable {
		t.Fatalf("no lines: %v", err)
	}
}

func TestAIPromptCarriesTemplate(t *testing.T) {
	tpl := invoiceTemplate{Saved: true, Supplier: "SPORTS COM", Columns: defaultInvoiceColumns()}
	tpl.Options.AIHints = "The BATCH NO. column is the size."
	_ = tpl.Options.normalize()
	p := aiPrompt(tpl)
	for _, want := range []string{"SPORTS COM", "variant = Batch No.", "The BATCH NO. column is the size.", "copy it into \"variant\""} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
}

// importTotals runs the read lines through the import's own arithmetic
// (importLineAmounts), as the preview and the bill do.
func importTotals(t *testing.T, lines []readLine) (subtotal, tax float64) {
	t.Helper()
	for _, l := range lines {
		q, _ := strconv.ParseFloat(l.Quantity, 64)
		c, _ := strconv.ParseFloat(l.UnitCost, 64)
		g, _ := strconv.ParseFloat(l.GSTRate, 64)
		lc, tx := importLineAmounts(q, c, g)
		if lt, _ := strconv.ParseFloat(l.LineTotal, 64); math.Abs(lc+tx-lt) > 0.015 {
			t.Errorf("line %d: cost %s × %s + GST = %v, line total %s", l.Row, l.Quantity, l.UnitCost, round2(lc+tx), l.LineTotal)
		}
		subtotal += lc
		tax += tx
	}
	return round2(subtotal), round2(tax)
}

// What is imported is what the supplier billed: each real invoice, read
// and then priced by the import, comes to its printed total with no
// round-off, and its GST to the tax the invoice prints (the input credit).
func TestReadInvoicesImportToThePaisa(t *testing.T) {
	for _, f := range []string{"invoice1.json", "invoice3.json", "invoice11.json", "invoice12.json"} {
		doc, lines := readInvoice(t, f, invoiceOptions{}, nil)
		sub, tax := importTotals(t, lines)
		printedTax := 0.0
		for _, l := range lines {
			if l.PrintedAmount != "" {
				t.Errorf("%s line %d: read as before GST", f, l.Row)
			}
			v, _ := strconv.ParseFloat(l.Tax, 64)
			printedTax += v
		}
		total, _ := strconv.ParseFloat(doc.Total, 64)
		if d := round2(total - sub - tax); d != 0 {
			t.Errorf("%s: %v + GST %v = %v, invoice %v", f, sub, tax, round2(sub+tax), total)
		}
		if math.Abs(tax-round2(printedTax)) > 0.005 {
			t.Errorf("%s: GST %v, printed %v", f, tax, round2(printedTax))
		}
	}
}

// A Tally-style invoice prints each line's taxable value, with the tax
// beside it: the cost is the amount over the quantity, and the line total
// the import checks adds the GST.
func TestReadAmountBeforeGST(t *testing.T) {
	opt := invoiceOptions{}
	if err := opt.normalize(); err != nil {
		t.Fatal(err)
	}
	rows := []rawRow{
		{Item: "BALL A", Quantity: "10 PCS", Rate: "100.00", GSTRate: "18%", Tax: "180.00", Total: "1,000.00"}, // tax on the amount
		{Item: "BALL B", Quantity: "4", Rate: "250", GSTRate: "12", Total: "1000"},                             // no tax printed: rate × qty
		{Item: "BALL C", Quantity: "2", Rate: "500", Tax: "50.00", Total: "1000.00"},                           // no GST %: from the tax
		{Item: "BALL D", Quantity: "10", Rate: "100.00", GSTRate: "5", Tax: "47.62", Total: "1000.00"},         // with GST, discounted
	}
	lines := buildLines(rows, opt, nil)
	want := []struct{ cost, gst, total, printed string }{
		{"100", "18", "1180.00", "1000.00"},
		{"250", "12", "1120.00", "1000.00"},
		{"500", "5", "1050.00", "1000.00"},
		{"95.238095", "5", "1000.00", ""},
	}
	for i, w := range want {
		l := lines[i]
		if l.UnitCost != w.cost || l.GSTRate != w.gst || l.LineTotal != w.total || l.PrintedAmount != w.printed {
			t.Errorf("line %d: cost %s gst %s total %s printed %q, want %+v (notes %v)", i+1, l.UnitCost, l.GSTRate, l.LineTotal, l.PrintedAmount, w, l.Notes)
		}
	}
	if sub, tax := importTotals(t, lines[:3]); sub != 3000 || tax != 350 {
		t.Fatalf("import: %v + %v", sub, tax)
	}

	// The supplier's rule overrides the guess either way.
	opt.LineAmount = "with_gst"
	if l := buildLines(rows[1:2], opt, nil)[0]; l.UnitCost != "223.214286" || l.LineTotal != "1000.00" || l.PrintedAmount != "" {
		t.Fatalf("with_gst: %+v", l)
	}
	opt.LineAmount = "before_gst"
	if l := buildLines(rows[3:4], opt, nil)[0]; l.UnitCost != "100" || l.LineTotal != "1050.00" || l.PrintedAmount != "1000.00" || len(l.Notes) != 0 {
		t.Fatalf("before_gst: %+v", l)
	}
	opt.LineAmount = "sometimes"
	if err := opt.normalize(); err == nil {
		t.Fatal("line_amount accepted an unknown value")
	}
}

// Invoice 12 against a shop with only the 5% and 18% slabs (GST since
// September 2025): the 0% line has no sale GST and the 12% line no slab,
// so both are marked before the import refuses them as new items.
func TestNoteSaleGSTInvoice12(t *testing.T) {
	_, lines := readInvoice(t, "invoice12.json", invoiceOptions{}, nil)
	noteSaleGST(lines, []string{"5", "18"})
	for i, l := range lines {
		last := ""
		if len(l.Notes) > 0 {
			last = l.Notes[len(l.Notes)-1]
		}
		switch i {
		case 0:
			if !strings.HasPrefix(last, "no sale GST %") || !strings.Contains(last, "5%, 18%") {
				t.Errorf("line 1: %v", l.Notes)
			}
		case 7:
			if !strings.HasPrefix(last, "there is no active 12% tax slab") {
				t.Errorf("line 8: %v", l.Notes)
			}
		default:
			if strings.Contains(last, "tax slab") {
				t.Errorf("line %d: %v", i+1, l.Notes)
			}
		}
	}
}
