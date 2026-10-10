package printing

type LabelData struct {
	ProductName  string
	SKU          string
	MRP          string
	SellingPrice string
	BarcodeCode  string
	Symbology    string // "EAN13" or anything else -> printed as CODE128
	// Phase 13, the "detailed" template: the batch the stock on the shelf
	// came from. Empty for a variant that isn't batch-tracked.
	BatchNo    string
	ExpiryDate string // YYYY-MM-DD
}

// LabelTemplates are the predefined templates of pos_frd_complete.md §12.
// Phase 1 built the first two; Phase 13 adds the other two:
//   - "standard" (50x30mm: name, barcode, MRP and price, SKU)
//   - "compact"  (40x20mm: name, barcode, MRP)
//   - "detailed" (100x50mm: name, barcode, MRP and price, SKU, batch and expiry)
//   - "qr"       (50x50mm: QR code of the barcode, name, price)
var LabelTemplates = []string{"standard", "compact", "detailed", "qr"}

// IsLabelTemplate reports whether t is one of LabelTemplates.
func IsLabelTemplate(t string) bool {
	for _, x := range LabelTemplates {
		if x == t {
			return true
		}
	}
	return false
}

// BuildLabel renders one label. Anything not in LabelTemplates falls back
// to "standard" (Phase 1 behaviour, kept for old callers).
func BuildLabel(template string, d LabelData) []byte {
	return BuildLabels(template, d, 1)
}

// BuildLabels renders `copies` identical labels in one print job, each
// ending in a cut, so a printer gets one connection for a whole shelf.
func BuildLabels(template string, d LabelData, copies int) []byte {
	if copies < 1 {
		copies = 1
	}
	b := New()
	for i := 0; i < copies; i++ {
		b.AlignCenter()
		writeLabel(b, template, d)
		b.FeedLines(2).Cut()
	}
	return b.Bytes()
}

func writeLabel(b *Builder, template string, d LabelData) {
	switch template {
	case "compact":
		b.Bold(true).Line(truncate(d.ProductName, 20)).Bold(false)
		printBarcode(b, d, 40, 2)
		b.Line("MRP: " + d.MRP)
	case "detailed":
		b.Bold(true).Line(truncate(d.ProductName, 48)).Bold(false)
		printBarcode(b, d, 80, 3)
		b.Line("MRP: " + d.MRP + "   Price: " + d.SellingPrice)
		b.Line("SKU: " + d.SKU)
		if d.BatchNo != "" {
			batch := "Batch: " + d.BatchNo
			if d.ExpiryDate != "" {
				batch += "   Exp: " + d.ExpiryDate
			}
			b.Line(batch)
		}
	case "qr":
		b.QRCode(d.BarcodeCode, 6)
		b.Bold(true).Line(truncate(d.ProductName, 24)).Bold(false)
		b.Line("Price: " + d.SellingPrice)
	default: // "standard"
		b.Bold(true).Line(truncate(d.ProductName, 32)).Bold(false)
		printBarcode(b, d, 60, 2)
		b.Line("MRP: " + d.MRP + "   Price: " + d.SellingPrice)
		b.Line("SKU: " + d.SKU)
	}
}

func printBarcode(b *Builder, d LabelData, height, width byte) {
	if d.Symbology == "EAN13" {
		b.BarcodeEAN13(d.BarcodeCode, height, width)
	} else {
		b.BarcodeCode128(d.BarcodeCode, height, width)
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
