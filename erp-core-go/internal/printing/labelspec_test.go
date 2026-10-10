package printing

import (
	"strings"
	"testing"
)

var ean = LabelData{ProductName: "SG Kashmir Willow Bat", SKU: "SG-BAT-SH", MRP: "2499.00", SellingPrice: "2299.00",
	BarcodeCode: "8901234567890", Symbology: "EAN13", BatchNo: "B7", ExpiryDate: "2027-01-31"}

func TestZPLSizesAndFieldsAtPreset(t *testing.T) {
	s := LabelPresets["standard"]
	z := string(BuildZPL(s, ean, 3))
	for _, want := range []string{"^XA", "^PW400", "^LL240", "^BEN", "^FD890123456789^FS", "SG Kashmir Willow Bat", "MRP 2499.00", "Price 2299.00", "SKU SG-BAT-SH", "^PQ3", "^XZ"} {
		if !strings.Contains(z, want) {
			t.Errorf("standard ZPL missing %q:\n%s", want, z)
		}
	}
	if strings.Contains(z, "Batch") {
		t.Error("standard has no batch line")
	}
	if z300 := string(BuildZPL(LabelSpec{WidthMM: 50, HeightMM: 30, DPI: 300, Fields: s.Fields}, ean, 1)); !strings.Contains(z300, "^PW591^LL354") {
		t.Errorf("300 dpi size wrong: %s", z300)
	}
}

func TestTSPLSizeBarcodeAndCopies(t *testing.T) {
	s := LabelPresets["detailed"]
	ts := string(BuildTSPL(s, ean, 2))
	for _, want := range []string{"SIZE 100 mm,50 mm", "CLS", "BARCODE ", "\"EAN13\"", "\"8901234567890\"", "Batch B7  Exp 2027-01-31", "PRINT 1,2"} {
		if !strings.Contains(ts, want) {
			t.Errorf("detailed TSPL missing %q:\n%s", want, ts)
		}
	}
}

func TestQRLayoutAndScan(t *testing.T) {
	s := LabelPresets["qr"]
	l := Layout(s, ean)
	var qr *Element
	for i := range l.Elements {
		if l.Elements[i].Kind == "qr" {
			qr = &l.Elements[i]
		}
	}
	if qr == nil || qr.Module < 3 || l.Scan.Level != "ok" {
		t.Fatalf("QR at 50x50 should be comfortably scannable: %+v scan %+v", qr, l.Scan)
	}
	if !strings.Contains(string(BuildZPL(s, ean, 1)), "^BQN,2,") {
		t.Error("ZPL QR command missing")
	}
	if !strings.Contains(string(BuildTSPL(s, ean, 1)), "QRCODE ") {
		t.Error("TSPL QR command missing")
	}
}

func TestScannabilityCatchesADenseCode128(t *testing.T) {
	long := ean
	long.Symbology, long.BarcodeCode = "CODE128", "ABCDEFGHIJ-KLMNOP-QRST"
	tiny := LabelSpec{WidthMM: 30, HeightMM: 20, DPI: 203, Fields: LabelFields{Name: true, Code: "barcode"}}
	if sc := Layout(tiny, long).Scan; sc.Level != "fail" {
		t.Errorf("a 22-char Code 128 on 30 mm should fail, got %+v", sc)
	}
	if sc := Layout(LabelPresets["standard"], ean).Scan; sc.Level != "ok" {
		t.Errorf("EAN-13 on 50 mm should be ok, got %+v", sc)
	}
	// Even digits pack two per symbol in set C: shorter than the same
	// length in letters.
	digits, letters := ean, ean
	digits.Symbology, digits.BarcodeCode = "CODE128", "20000000000391"
	letters.Symbology, letters.BarcodeCode = "CODE128", "ABCDEFGHIJKLMN"
	if linearModules(digits) >= linearModules(letters) {
		t.Error("set C should be denser for digits")
	}
}

func TestNoCodeAndCrampedLabels(t *testing.T) {
	none := ean
	none.BarcodeCode = ""
	if sc := Layout(LabelPresets["standard"], none).Scan; sc.Level != "fail" || !strings.Contains(sc.Message, "no barcode") {
		t.Errorf("missing barcode: %+v", sc)
	}
	cramped := LabelSpec{WidthMM: 40, HeightMM: 15, DPI: 203, Fields: LabelFields{Name: true, SKU: true, Price: true, MRP: true, Batch: true, Code: "barcode"}}
	if sc := Layout(cramped, ean).Scan; sc.Level != "fail" {
		t.Errorf("15 mm with five text rows leaves no room: %+v", sc)
	}
}

func TestLabelTextCannotInjectPrinterCommands(t *testing.T) {
	bad := ean
	bad.ProductName = `Bat ^XZ^XA "evil" ~JR`
	z := string(BuildZPL(LabelPresets["standard"], bad, 1))
	if strings.Count(z, "^XA") != 1 || strings.Count(z, "^XZ") != 1 || strings.Contains(z, "~JR") {
		t.Errorf("ZPL injection not neutralised: %s", z)
	}
	ts := string(BuildTSPL(LabelPresets["standard"], bad, 1))
	if strings.Contains(ts, `"evil"`) {
		t.Errorf("TSPL quote not neutralised: %s", ts)
	}
}

func TestSpecValidation(t *testing.T) {
	ok := LabelPresets["standard"]
	if ok.Validate() != nil {
		t.Error("preset should validate")
	}
	for _, bad := range []LabelSpec{
		{WidthMM: 10, HeightMM: 30, DPI: 203, Fields: ok.Fields},
		{WidthMM: 50, HeightMM: 30, DPI: 600, Fields: ok.Fields},
		{WidthMM: 50, HeightMM: 30, DPI: 203, Fields: LabelFields{Code: "datamatrix"}},
	} {
		if bad.Validate() == nil {
			t.Errorf("%+v should not validate", bad)
		}
	}
}

func TestCustomESCPOSHonoursFields(t *testing.T) {
	s := LabelSpec{WidthMM: 60, HeightMM: 40, DPI: 203, Fields: LabelFields{Name: true, Batch: true, Code: "qr"}}
	cmds := Decode(BuildCustomESCPOS(s, ean, 2))
	text := labelText(cmds)
	if !strings.Contains(text, "Batch: B7") || strings.Contains(text, "MRP") {
		t.Errorf("ESC/POS fields wrong: %q", text)
	}
	qrs := 0
	for _, c := range cmds {
		if c.IsQR {
			qrs++
		}
	}
	if qrs != 2 {
		t.Errorf("2 copies -> 2 QR codes, got %d", qrs)
	}
}
