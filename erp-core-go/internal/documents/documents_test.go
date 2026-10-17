package documents

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
