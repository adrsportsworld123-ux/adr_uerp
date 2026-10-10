package printing

import (
	"strings"
	"testing"
)

func labelText(cmds []Command) string {
	var sb strings.Builder
	for _, c := range cmds {
		sb.WriteString(c.Text)
	}
	return sb.String()
}

var sample = LabelData{
	ProductName: "Paracetamol 500mg Strip", SKU: "RX-PCM-500", MRP: "35.00", SellingPrice: "32.00",
	BarcodeCode: "8901234567897", Symbology: "EAN13", BatchNo: "B2026-07", ExpiryDate: "2027-06-30",
}

func TestDetailedLabelCarriesBatchAndExpiry(t *testing.T) {
	cmds := Decode(BuildLabel("detailed", sample))
	text := labelText(cmds)
	for _, want := range []string{"Paracetamol 500mg Strip", "MRP: 35.00", "Price: 32.00", "SKU: RX-PCM-500", "Batch: B2026-07", "Exp: 2027-06-30"} {
		if !strings.Contains(text, want) {
			t.Errorf("detailed label missing %q in %q", want, text)
		}
	}
	var barcodes int
	for _, c := range cmds {
		if c.IsBarcode && c.BarcodeSymbology == symEAN13 && c.BarcodeData == sample.BarcodeCode {
			barcodes++
		}
	}
	if barcodes != 1 {
		t.Errorf("want one EAN-13 barcode, got %d", barcodes)
	}

	noBatch := sample
	noBatch.BatchNo, noBatch.ExpiryDate = "", ""
	if strings.Contains(labelText(Decode(BuildLabel("detailed", noBatch))), "Batch:") {
		t.Error("an untracked variant's detailed label shouldn't print a batch line")
	}
}

func TestQRLabelEncodesTheBarcode(t *testing.T) {
	cmds := Decode(BuildLabel("qr", sample))
	var qr *Command
	for i := range cmds {
		if cmds[i].IsQR {
			qr = &cmds[i]
		}
		if cmds[i].IsBarcode {
			t.Error("the QR template prints no linear barcode")
		}
	}
	if qr == nil || qr.QRData != sample.BarcodeCode || qr.QRModuleSize != 6 {
		t.Fatalf("QR command = %+v, want data %q size 6", qr, sample.BarcodeCode)
	}
	if text := labelText(cmds); !strings.Contains(text, "Price: 32.00") {
		t.Errorf("qr label text %q lacks the price", text)
	}
}

func TestCopiesRepeatTheLabel(t *testing.T) {
	cmds := Decode(BuildLabels("standard", sample, 3))
	n := 0
	for _, c := range cmds {
		if c.IsBarcode {
			n++
		}
	}
	if n != 3 {
		t.Errorf("3 copies printed %d barcodes", n)
	}
	if !IsLabelTemplate("qr") || IsLabelTemplate("fancy") {
		t.Error("IsLabelTemplate")
	}
}

func TestTruncateIsRuneSafe(t *testing.T) {
	if got := truncate("₹₹₹₹", 2); got != "₹₹" {
		t.Errorf("truncate = %q", got)
	}
}
