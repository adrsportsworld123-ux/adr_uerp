package printing

type LabelData struct {
	ProductName  string
	SKU          string
	MRP          string
	SellingPrice string
	BarcodeCode  string
	Symbology    string // "EAN13" or anything else -> printed as CODE128
}

// BuildLabel renders one of Phase 1's two scoped label templates
// (pos_frd_complete.md §12 names four; phased_roadmap.md's Phase 1 scope
// deliberately narrows that to just these two — Detailed/QR templates,
// batch/lot/expiry fields, and a WYSIWYG designer are all explicitly
// deferred, matching how Pricing's migration scoped down from its own
// FRD section):
//   - "standard" (§12: 50x30mm — Name, barcode, MRP, SKU)
//   - "compact"  (§12: 40x20mm — Name, barcode, MRP)
//
// Anything else falls back to "standard".
func BuildLabel(template string, d LabelData) []byte {
	b := New().AlignCenter()

	switch template {
	case "compact":
		b.Bold(true).Line(truncate(d.ProductName, 20)).Bold(false)
		printBarcode(b, d, 40, 2)
		b.Line("MRP: " + d.MRP)
	default: // "standard"
		b.Bold(true).Line(truncate(d.ProductName, 32)).Bold(false)
		printBarcode(b, d, 60, 2)
		b.Line("MRP: " + d.MRP + "   Price: " + d.SellingPrice)
		b.Line("SKU: " + d.SKU)
	}

	b.FeedLines(2).Cut()
	return b.Bytes()
}

func printBarcode(b *Builder, d LabelData, height, width byte) {
	if d.Symbology == "EAN13" {
		b.BarcodeEAN13(d.BarcodeCode, height, width)
	} else {
		b.BarcodeCode128(d.BarcodeCode, height, width)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
