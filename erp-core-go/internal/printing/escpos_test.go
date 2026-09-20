package printing

import (
	"strings"
	"testing"
)

// TestReceiptRoundTrip verifies BuildReceipt's actual output bytes — not
// just that it runs — by decoding them back and checking the real
// content survived: product names, totals, and payment lines. This is
// the closest this environment can get to "print it and look," per the
// package doc's note about no printer hardware being available here.
func TestReceiptRoundTrip(t *testing.T) {
	data := ReceiptData{
		MerchantName: "Acme Sports",
		BranchName:   "MG Road",
		OrderNumber:  "SO-000123",
		CashierName:  "Ravi Kumar",
		Lines: []ReceiptLine{
			{ProductName: "SG Cricket Bat", SKU: "SG-BAT-SH", Quantity: "1", UnitPrice: "2299.00", LineTotal: "2299.00"},
		},
		Subtotal:      "2299.00",
		DiscountTotal: "0.00",
		TaxTotal:      "413.82",
		GrandTotal:    "2712.82",
		Payments:      []ReceiptPayment{{Method: "cash", Amount: "2712.82"}},
	}

	raw := BuildReceipt(data)
	cmds := Decode(raw)

	var allText strings.Builder
	for _, c := range cmds {
		allText.WriteString(c.Text)
	}
	joined := allText.String()

	for _, want := range []string{"Acme Sports", "MG Road", "SO-000123", "Ravi Kumar", "SG Cricket Bat", "2712.82", "cash"} {
		if !strings.Contains(joined, want) {
			t.Errorf("decoded receipt text missing %q\nfull text:\n%s", want, joined)
		}
	}

	if !strings.HasPrefix(string(raw), "\x1B\x40") {
		t.Error("receipt bytes must start with the ESC @ init command")
	}
	if !strings.HasSuffix(string(raw), cmdCutFull) {
		t.Error("receipt bytes must end with the cut command")
	}
}

// TestLabelBarcodeCommand verifies the label's barcode command carries
// exactly the symbology and data a real printer needs, decoded back from
// the actual bytes rather than asserted from the Builder call site.
func TestLabelBarcodeCommand(t *testing.T) {
	tests := []struct {
		name       string
		template   string
		symbology  string
		wantSym    byte
		wantInText string
	}{
		{"standard EAN13", "standard", "EAN13", symEAN13, "SKU: SG-BAT-SH"},
		{"compact CODE128", "compact", "CODE128", symCODE128, "MRP: 2499.00"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := BuildLabel(tt.template, LabelData{
				ProductName: "SG Cricket Bat", SKU: "SG-BAT-SH",
				MRP: "2499.00", SellingPrice: "2299.00",
				BarcodeCode: "2000000000012", Symbology: tt.symbology,
			})
			cmds := Decode(raw)

			var foundBarcode bool
			var allText strings.Builder
			for _, c := range cmds {
				if c.IsBarcode {
					foundBarcode = true
					if c.BarcodeSymbology != tt.wantSym {
						t.Errorf("barcode symbology = %d, want %d", c.BarcodeSymbology, tt.wantSym)
					}
					if c.BarcodeData != "2000000000012" {
						t.Errorf("barcode data = %q, want %q", c.BarcodeData, "2000000000012")
					}
				}
				allText.WriteString(c.Text)
			}
			if !foundBarcode {
				t.Fatal("no barcode command found in decoded label")
			}
			if !strings.Contains(allText.String(), tt.wantInText) {
				t.Errorf("decoded label text missing %q\nfull text:\n%s", tt.wantInText, allText.String())
			}
		})
	}
}

func TestDecodeIgnoresFormattingCommands(t *testing.T) {
	raw := New().AlignCenter().Bold(true).DoubleSize(true).Line("Hello").DoubleSize(false).Bold(false).AlignLeft().Cut().Bytes()
	cmds := Decode(raw)
	if len(cmds) != 1 || cmds[0].Text != "Hello\n" {
		t.Fatalf("expected a single decoded text command %q, got %+v", "Hello\n", cmds)
	}
}
