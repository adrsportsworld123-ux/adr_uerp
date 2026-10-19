package documents

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"erp-core-go/internal/taxes"
)

func TestAmount(t *testing.T) {
	cases := map[string]string{
		"0":           "0.00",
		"5":           "5.00",
		"12.5":        "12.50",
		"999.99":      "999.99",
		"1000":        "1,000.00",
		"123456.50":   "1,23,456.50",
		"12345678.00": "1,23,45,678.00",
		"-1500.25":    "-1,500.25",
		"-0.00":       "0.00",
		"":            "0.00",
		"abc":         "abc",
	}
	for in, want := range cases {
		if got := Amount(in); got != want {
			t.Errorf("Amount(%q) = %q, want %q", in, got, want)
		}
	}
	if got := INR("-25.5"); got != "-₹25.50" {
		t.Errorf("INR(-25.5) = %q", got)
	}
}

func TestQty(t *testing.T) {
	for in, want := range map[string]string{"2.000": "2", "1.250": "1.25", "10": "10", "0.500": "0.5"} {
		if got := Qty(in); got != want {
			t.Errorf("Qty(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSafeFilename(t *testing.T) {
	if got := safeFilename(`INV/24-25/001"x`); got != "INV-24-25-001-x" {
		t.Errorf("got %q", got)
	}
}

func TestValidateSettings(t *testing.T) {
	ok := settingsRequest{DisplayName: "ADR Sports", Email: "a@b.in"}
	if msg := validateSettings(&ok); msg != "" || ok.ReceiptFormat != "a4" {
		t.Fatalf("valid request rejected: %q (%q)", msg, ok.ReceiptFormat)
	}
	bad := []settingsRequest{
		{ReceiptFormat: "letter"},
		{Email: "not-an-email"},
		{DisplayName: strings.Repeat("x", 121)},
		{HeaderText: "1\n2\n3\n4\n5\n6\n7"},
	}
	for i, r := range bad {
		if msg := validateSettings(&r); msg == "" {
			t.Errorf("case %d: expected a validation error", i)
		}
	}
}

func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.NRGBA{R: uint8(x * 255 / w), G: 80, B: 160, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestNormalizeLogo(t *testing.T) {
	out, mime, msg := normalizeLogo(testPNG(t, 200, 100))
	if msg != "" || mime != "image/png" || len(out) == 0 {
		t.Fatalf("valid PNG rejected: %q", msg)
	}
	if _, _, msg := normalizeLogo([]byte("<svg/>")); msg == "" {
		t.Error("non-image accepted")
	}
	if _, _, msg := normalizeLogo(testPNG(t, 8, 8)); msg == "" {
		t.Error("tiny image accepted")
	}
}

func sampleLetterhead(t *testing.T) Letterhead {
	return Letterhead{
		Name: "ADR Sports World", BranchName: "MG Road",
		Address: []string{"12, MG Road", "Bengaluru 560001"},
		Phone:   "080 1234 5678", Email: "store@adrsw.com", GSTIN: "29ABCDE1234F1Z5",
		Header: []string{"Authorised dealer — all leading sports brands"},
		Footer: []string{"Goods once sold can be exchanged within 7 days with bill.", "Thank you, visit again!"},
		Logo:   testPNG(t, 240, 120), LogoMime: "image/png",
	}
}

// TestRender renders every document type on every paper it supports and
// checks each is a real PDF. With DOCS_OUT set, the files are written
// there to inspect by eye.
func TestRender(t *testing.T) {
	lh := sampleLetterhead(t)
	long := SaleInput{OrderNumber: "BLR01-000123", Status: "voided", Date: "16 Oct 2026, 18:05", Cashier: "Ravi"}
	for i := 0; i < 45; i++ {
		long.Lines = append(long.Lines, SaleLine{Name: "Item with a fairly long product name that has to wrap onto a second line", SKU: "SKU-1",
			Quantity: "1.000", UnitPrice: "100.00", Discount: "0.00", Tax: "18.00", Total: "118.00"})
	}
	long.Subtotal, long.Tax, long.Grand = "4500.00", "810.00", "5310.00"

	docs := map[string]struct {
		d Document
		f Format
	}{
		"receipt-a4":      {SampleDocument(lh, KindReceipt), FormatA4},
		"receipt-thermal": {SampleDocument(lh, KindReceipt), FormatThermal},
		"receipt-long":    {SaleReceipt(lh, long), FormatA4},
		"grn":             {SampleDocument(lh, KindPurchase), FormatA4},
		"bill": {PurchaseBill(BillInput{Number: "PB-0001", SupplierInvoice: "SG/2026/88", Status: "partially_paid", BillDate: "11 Oct 2026",
			DueDate: "10 Nov 2026", GRNNumber: "GRN-0004", Supplier: Supplier{Name: "SG Sports"},
			Lines:    []PurchaseLine{{Name: "Bat", Quantity: "2.000", UnitCost: "1000.00", Total: "2000.00"}},
			Subtotal: "2000.00", Tax: "360.00", RoundOff: "-0.40", Grand: "2359.60", Paid: "1000.00", Balance: "1359.60",
			Payments: [][2]string{{"Bank transfer · 12 Oct 2026 · UTR123", "1000.00"}}}), FormatA4},
		"return": {PurchaseReturn(ReturnInput{Number: "PR-0001", Status: "completed", Date: "12 Oct 2026", Reason: "Damaged in transit",
			Supplier: Supplier{Name: "SG Sports"}, Lines: []PurchaseLine{{Name: "Ball", Quantity: "3.000", UnitCost: "200.00", Total: "600.00"}},
			Grand: "600.00"}), FormatA4},
	}
	// GST split invoices: intra-state (CGST + SGST) and inter-state
	// (IGST) with mixed rates, on both papers.
	docs["receipt-gst-intra"] = struct {
		d Document
		f Format
	}{SaleReceipt(lh, gstSale(false)), FormatA4}
	docs["receipt-gst-inter"] = struct {
		d Document
		f Format
	}{SaleReceipt(lh, gstSale(true)), FormatA4}
	docs["receipt-gst-thermal"] = struct {
		d Document
		f Format
	}{SaleReceipt(lh, gstSale(false)), FormatThermal}
	// The same documents with no letterhead customisation at all.
	docs["receipt-plain-thermal"] = struct {
		d Document
		f Format
	}{SaleReceipt(Letterhead{Name: "Acme"}, long), FormatThermal}

	out := os.Getenv("DOCS_OUT")
	for name, c := range docs {
		pdf, err := Render(lh, c.d, c.f)
		if name == "receipt-plain-thermal" {
			pdf, err = Render(Letterhead{Name: "Acme"}, c.d, c.f)
		}
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !bytes.HasPrefix(pdf, []byte("%PDF-")) || len(pdf) < 1000 {
			t.Fatalf("%s: not a PDF (%d bytes)", name, len(pdf))
		}
		if out != "" {
			if err := os.WriteFile(filepath.Join(out, name+".pdf"), pdf, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestBrokenLogoStillRenders(t *testing.T) {
	lh := sampleLetterhead(t)
	lh.Logo = []byte("not really a png")
	if _, err := Render(lh, SampleDocument(lh, KindReceipt), FormatA4); err != nil {
		t.Fatalf("a bad logo must not fail the document: %v", err)
	}
}

func TestReceiptTitleAndStamp(t *testing.T) {
	d := SaleReceipt(Letterhead{GSTIN: "X"}, SaleInput{Status: "finalized"})
	if d.Title != "TAX INVOICE" || d.Stamp != "" {
		t.Errorf("got %q / %q", d.Title, d.Stamp)
	}
	d = SaleReceipt(Letterhead{}, SaleInput{Status: "voided"})
	if d.Title != "RECEIPT" || d.Stamp != "VOIDED" {
		t.Errorf("got %q / %q", d.Title, d.Stamp)
	}
}

func TestReceiptRoundOff(t *testing.T) {
	d := SaleReceipt(Letterhead{}, SaleInput{Status: "finalized", Subtotal: "2299.00", Tax: "413.82", Grand: "2712.82", RoundOff: "-0.82", Payable: "2712.00"})
	var labels []string
	for _, tl := range d.Totals {
		labels = append(labels, tl.Label+"="+tl.Value)
	}
	got := strings.Join(labels, ", ")
	if got != "Subtotal=₹2,299.00, Tax=₹413.82, Total=₹2,712.82, Round off=-₹0.82, Net payable=₹2,712.00" {
		t.Errorf("got %s", got)
	}
	d = SaleReceipt(Letterhead{}, SaleInput{Status: "finalized", Subtotal: "100.00", Tax: "0.00", Grand: "100.00", RoundOff: "0.00", Payable: "100.00"})
	if last := d.Totals[len(d.Totals)-1]; last.Label != "Total" || !last.Bold {
		t.Errorf("no round-off should end on a bold Total, got %+v", d.Totals)
	}
}

// gstSale is a two-rate sale (18% and 5%) to a B2B customer, split for an
// intra- or inter-state supply.
func gstSale(inter bool) SaleInput {
	lines := []taxes.Line{{Rate: 18, Taxable: 2000, Tax: 360}, {Rate: 5, Taxable: 24.29, Tax: 1.21}}
	b := taxes.Build(taxes.Settings{Regime: taxes.RegimeGSTSplit, TaxLabel: "GST"}, inter, "29", lines)
	in := SaleInput{OrderNumber: "BLR01-000124", Status: "finalized", Date: "19 Oct 2026, 17:20", Cashier: "Ravi",
		CustomerName: "Sharma Traders", CustomerGSTIN: "29ABCDE1234F1Z5", Breakup: &b,
		Subtotal: "2024.29", Discount: "0.00", Tax: "361.21", Grand: "2385.50"}
	for i, l := range lines {
		a := taxes.SplitLine(l.Tax, 0, inter)
		in.Lines = append(in.Lines, SaleLine{Name: []string{"Cricket bat", "SS medal"}[i], SKU: "SKU-" + itoa(i+1),
			Quantity: "1.000", UnitPrice: []string{"2000.00", "25.50"}[i], Discount: "0.00",
			Tax: []string{"360.00", "1.21"}[i], Total: []string{"2360.00", "25.50"}[i],
			LineTax: LineTax{HSN: "9506", Rate: []string{"18.00", "5.00"}[i], Taxable: []string{"2000.00", "24.29"}[i],
				CGST: money2(a.CGST), SGST: money2(a.SGST), IGST: money2(a.IGST)}})
	}
	return in
}

func money2(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

func TestGSTInvoiceLayout(t *testing.T) {
	intra := SaleReceipt(Letterhead{GSTIN: "29AAACA1234A1Z5"}, gstSale(false))
	var cols []string
	for _, c := range intra.Columns {
		cols = append(cols, c.Title)
	}
	if got := strings.Join(cols, "|"); got != "#|Item|Qty|Rate|Disc.|Taxable|GST %|CGST|SGST|Amount" {
		t.Errorf("intra columns %s", got)
	}
	var totals []string
	for _, tl := range intra.Totals {
		totals = append(totals, tl.Label+"="+tl.Value)
	}
	if got := strings.Join(totals, ", "); got != "Subtotal=₹2,024.29, CGST=₹180.61, SGST=₹180.60, Total=₹2,385.50" {
		t.Errorf("intra totals %s", got)
	}
	if intra.TaxTable == nil || len(intra.TaxTable.Rows) != 2 || intra.TaxTable.Rows[1][0] != "18% (9 + 9)" {
		t.Errorf("intra summary %+v", intra.TaxTable)
	}
	if len(intra.Meta) < 2 || intra.Meta[1] != [2]string{"Place of supply", "29 – Karnataka"} {
		t.Errorf("meta %+v", intra.Meta)
	}
	if !strings.Contains(intra.Rows[0].Cells[1], "HSN 9506") || intra.Party == nil || intra.Party.Lines[0] != "GSTIN: 29ABCDE1234F1Z5" {
		t.Errorf("item cell %q / party %+v", intra.Rows[0].Cells[1], intra.Party)
	}

	inter := SaleReceipt(Letterhead{}, gstSale(true))
	cols = cols[:0]
	for _, c := range inter.Columns {
		cols = append(cols, c.Title)
	}
	if got := strings.Join(cols, "|"); got != "#|Item|Qty|Rate|Disc.|Taxable|GST %|IGST|Amount" {
		t.Errorf("inter columns %s", got)
	}
	if inter.Totals[1] != (Total{Label: "IGST", Value: "₹361.21"}) {
		t.Errorf("inter totals %+v", inter.Totals)
	}

	// A single-tax merchant: one column and one total under its own label.
	b := taxes.Build(taxes.Settings{Regime: taxes.RegimeSingle, TaxLabel: "VAT"}, false, "", []taxes.Line{{Rate: 5, Taxable: 100, Tax: 5}})
	vat := SaleReceipt(Letterhead{}, SaleInput{Status: "finalized", Breakup: &b, Subtotal: "100.00", Tax: "5.00", Grand: "105.00",
		Lines: []SaleLine{{Name: "Ball", Quantity: "1.000", UnitPrice: "100.00", Discount: "0.00", Tax: "5.00", Total: "105.00"}}})
	if vat.Columns[5].Title != "VAT" || vat.Totals[1] != (Total{Label: "VAT @ 5%", Value: "₹5.00"}) || len(vat.Meta) != 1 {
		t.Errorf("single regime %+v / %+v / %+v", vat.Columns, vat.Totals, vat.Meta)
	}
}

func TestCurrencyLocalize(t *testing.T) {
	usd := taxes.Currency{Code: "USD", Symbol: "$"}
	for in, want := range map[string]string{
		"₹1,23,456.50":               "$123,456.50",
		"−₹350.00":                   "−$350.00",
		"-₹0.82":                     "-$0.82",
		"12,34,567.00":               "1,234,567.00",
		"1 × 2,299.00  (−350.00)":    "1 × 2,299.00  (−350.00)",
		"Reason: damaged":            "Reason: damaged",
		"18% (9 + 9)":                "18% (9 + 9)",
		"Refunded by Cash ₹2,712.82": "Refunded by Cash $2,712.82",
	} {
		if got := convertMoney(in, usd); got != want {
			t.Errorf("convertMoney(%q) = %q, want %q", in, got, want)
		}
	}
	if got := convertMoney("₹1,000.00", taxes.Currency{Code: "AED", Symbol: "AED"}); got != "AED 1,000.00" {
		t.Errorf("letter symbol: %q", got)
	}
	// An INR document renders untouched; a USD one is localized end to end.
	d := SaleReceipt(Letterhead{}, gstSale(false))
	l := localize(d, usd)
	if l.Totals[len(l.Totals)-1].Value != "$2,385.50" || d.Totals[len(d.Totals)-1].Value != "₹2,385.50" {
		t.Errorf("localize totals: %+v / original %+v", l.Totals, d.Totals)
	}
	if _, err := Render(Letterhead{Name: "Acme", Currency: usd}, d, FormatA4); err != nil {
		t.Fatal(err)
	}
}

func TestCreditNoteLayout(t *testing.T) {
	sale := gstSale(true)
	d := CreditNote(Letterhead{}, CreditNoteInput{Number: "RET-1", Date: "19 Oct 2026", InvoiceNumber: sale.OrderNumber, InvoiceDate: "18 Oct 2026",
		Reason: "Damaged", RefundMethod: "upi", CustomerName: "Sharma Traders", CustomerGSTIN: "29ABCDE1234F1Z5",
		Lines: sale.Lines, Taxable: "2024.29", Tax: "361.21", RoundOff: "0.50", Refund: "2385.00", Breakup: sale.Breakup})
	if d.Title != "CREDIT NOTE" || d.Meta[0] != [2]string{"Against invoice", sale.OrderNumber} {
		t.Fatalf("header %q %+v", d.Title, d.Meta)
	}
	var totals []string
	for _, tl := range d.Totals {
		totals = append(totals, tl.Label+"="+tl.Value)
	}
	if got := strings.Join(totals, ", "); got != "Taxable value=₹2,024.29, IGST=₹361.21, Round off returned=−₹0.50, Refund=₹2,385.00" {
		t.Errorf("totals %s", got)
	}
	if d.TaxTable == nil || len(d.Payments) != 1 || d.Payments[0][0] != "Refunded by UPI" {
		t.Errorf("summary / payments %+v %+v", d.TaxTable, d.Payments)
	}
	if _, err := Render(Letterhead{Name: "Acme"}, d, FormatThermal); err != nil {
		t.Fatal(err)
	}
}
