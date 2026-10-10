package printing

import (
	"fmt"
	"math"
	"strings"
	"unicode"
)

// Phase 14 label studio (pos_frd_complete.md §12, phased_roadmap.md Phase
// 14): any label size, chosen fields, and the three direct-thermal printer
// languages the FRD names: ZPL (Zebra), TSPL (TSC and most generic thermal
// label printers) and ESC/POS (the receipt-printer family). One layout
// (Layout) drives ZPL and TSPL, so both place the same fields in the same
// spots; ESC/POS is line-based and only honours the field choices.

// LabelFields picks what a label shows. Code is "barcode", "qr" or "none".
type LabelFields struct {
	Name  bool   `json:"name"`
	SKU   bool   `json:"sku"`
	Price bool   `json:"price"`
	MRP   bool   `json:"mrp"`
	Batch bool   `json:"batch"`
	Code  string `json:"code"`
}

// LabelSpec is one label size at one printer resolution.
type LabelSpec struct {
	WidthMM  float64     `json:"width_mm"`
	HeightMM float64     `json:"height_mm"`
	DPI      int         `json:"dpi"`
	Fields   LabelFields `json:"fields"`
}

// LabelPresets are §12's predefined sizes, with the fields each prints
// (the same four the POS's templates use).
var LabelPresets = map[string]LabelSpec{
	"standard": {WidthMM: 50, HeightMM: 30, DPI: 203, Fields: LabelFields{Name: true, SKU: true, Price: true, MRP: true, Code: "barcode"}},
	"compact":  {WidthMM: 40, HeightMM: 20, DPI: 203, Fields: LabelFields{Name: true, MRP: true, Code: "barcode"}},
	"detailed": {WidthMM: 100, HeightMM: 50, DPI: 203, Fields: LabelFields{Name: true, SKU: true, Price: true, MRP: true, Batch: true, Code: "barcode"}},
	"qr":       {WidthMM: 50, HeightMM: 50, DPI: 203, Fields: LabelFields{Name: true, Price: true, Code: "qr"}},
}

// Validate checks a spec is one a thermal label printer can take.
func (s LabelSpec) Validate() error {
	switch {
	case s.WidthMM < 20 || s.WidthMM > 120 || s.HeightMM < 15 || s.HeightMM > 150:
		return fmt.Errorf("label size must be 20–120 mm wide and 15–150 mm tall")
	case s.DPI != 203 && s.DPI != 300:
		return fmt.Errorf("dpi must be 203 or 300")
	case s.Fields.Code != "barcode" && s.Fields.Code != "qr" && s.Fields.Code != "none":
		return fmt.Errorf("code must be barcode, qr or none")
	}
	return nil
}

func (s LabelSpec) dots(mm float64) int { return int(math.Round(mm / 25.4 * float64(s.DPI))) }

// Element is one placed field, in printer dots from the label's top left.
type Element struct {
	Kind   string // "text" | "barcode" | "qr"
	X, Y   int
	W, H   int // text: box width and character height; barcode: height; qr: size
	Text   string
	Module int // barcode/qr: module width in dots
}

// Scan is the scannability verdict for a label's code at this size.
type Scan struct {
	Level    string  `json:"level"` // "ok" | "small" | "fail" | "none"
	ModuleMM float64 `json:"module_mm"`
	Message  string  `json:"message"`
}

// LabelLayout is where everything goes, plus the scan verdict.
type LabelLayout struct {
	WidthDots, HeightDots int
	Elements              []Element
	Scan                  Scan
}

// Layout stacks the chosen fields top to bottom inside a 2 mm margin:
// name, then the code (taking the room the text rows leave), then the
// price line, SKU and batch. Text height is about 3 mm at any dpi.
func Layout(s LabelSpec, d LabelData) LabelLayout {
	l := LabelLayout{WidthDots: s.dots(s.WidthMM), HeightDots: s.dots(s.HeightMM)}
	margin := s.dots(2)
	text := s.dots(3)
	gap := s.dots(0.8)
	usableW := l.WidthDots - 2*margin

	var top, bottom []Element
	if s.Fields.Name {
		top = append(top, Element{Kind: "text", W: usableW, H: text, Text: d.ProductName})
	}
	var priceParts []string
	if s.Fields.MRP && d.MRP != "" {
		priceParts = append(priceParts, "MRP "+d.MRP)
	}
	if s.Fields.Price && d.SellingPrice != "" {
		priceParts = append(priceParts, "Price "+d.SellingPrice)
	}
	if len(priceParts) > 0 {
		bottom = append(bottom, Element{Kind: "text", W: usableW, H: text, Text: strings.Join(priceParts, "  ")})
	}
	if s.Fields.SKU && d.SKU != "" {
		bottom = append(bottom, Element{Kind: "text", W: usableW, H: text, Text: "SKU " + d.SKU})
	}
	if s.Fields.Batch && d.BatchNo != "" {
		b := "Batch " + d.BatchNo
		if d.ExpiryDate != "" {
			b += "  Exp " + d.ExpiryDate
		}
		bottom = append(bottom, Element{Kind: "text", W: usableW, H: text, Text: b})
	}

	y := margin
	for _, e := range top {
		e.X, e.Y = margin, y
		l.Elements = append(l.Elements, e)
		y += e.H + gap
	}
	textBelow := len(bottom) * (text + gap)
	codeH := l.HeightDots - margin - textBelow - y
	l.Scan = Scan{Level: "none", Message: "no code on this label"}
	if s.Fields.Code != "none" && d.BarcodeCode != "" && codeH > 0 {
		switch s.Fields.Code {
		case "qr":
			modules := qrModules(d.BarcodeCode)
			side := min(usableW, codeH)
			module := side / modules
			l.Scan = judge(s, module, 2, 3, "QR module")
			size := module * modules
			l.Elements = append(l.Elements, Element{Kind: "qr", X: margin + (usableW-size)/2, Y: y, W: size, H: size, Text: d.BarcodeCode, Module: max(module, 1)})
			y += size + gap
		default:
			modules := linearModules(d)
			module := usableW / modules
			l.Scan = judge(s, module, 2, 2, "bar")
			// Leave room under the bars for the printed digits.
			barH := max(codeH-text-gap, s.dots(5))
			width := max(module, 1) * modules
			l.Elements = append(l.Elements, Element{Kind: "barcode", X: margin + (usableW-width)/2, Y: y, H: barH, Text: d.BarcodeCode, Module: max(module, 1)})
			y += barH + text + gap
		}
	} else if s.Fields.Code != "none" && d.BarcodeCode == "" {
		l.Scan = Scan{Level: "fail", Message: "this item has no barcode yet"}
	} else if s.Fields.Code != "none" {
		l.Scan = Scan{Level: "fail", Message: "the text leaves no room for the code: make the label taller or turn fields off"}
	}
	for _, e := range bottom {
		e.X, e.Y = margin, y
		l.Elements = append(l.Elements, e)
		y += e.H + gap
	}
	return l
}

// judge turns a module width in dots into a verdict. A narrow bar under
// 2 dots (0.25 mm at 203 dpi) can't be printed reliably by a thermal head;
// a QR module under 3 dots scans only with good scanners.
func judge(s LabelSpec, module, failBelow, smallBelow int, what string) Scan {
	mm := math.Round(float64(module)/float64(s.DPI)*25.4*1000) / 1000
	switch {
	case module < failBelow:
		return Scan{Level: "fail", ModuleMM: mm, Message: fmt.Sprintf("the narrowest %s would be %.2f mm, too fine for a %d dpi printer: widen the label or use a QR code", what, mm, s.DPI)}
	case module < smallBelow:
		return Scan{Level: "small", ModuleMM: mm, Message: fmt.Sprintf("the %s is %.2f mm, small: it scans with a good scanner; a bigger label is safer", what, mm)}
	}
	return Scan{Level: "ok", ModuleMM: mm, Message: fmt.Sprintf("%s width %.2f mm: scannable", what, mm)}
}

// linearModules counts the modules of an EAN-13 or Code 128 symbol,
// quiet zones included.
func linearModules(d LabelData) int {
	if d.Symbology == "EAN13" {
		return 95 + 18
	}
	n := len(d.BarcodeCode)
	data := n
	if allDigits(d.BarcodeCode) && n%2 == 0 {
		data = n / 2 // Code 128 set C packs two digits per symbol
	}
	return 11*(data+3) + 2 + 20
}

// qrModules is the side, quiet zone included, of the smallest QR version
// (error correction M, byte mode) that holds the data.
func qrModules(data string) int {
	capacity := []int{14, 26, 42, 62, 84, 106, 122, 152}
	for v, c := range capacity {
		if len(data) <= c {
			return 21 + 4*v + 8
		}
	}
	return 21 + 4*len(capacity) + 8
}

func allDigits(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return s != ""
}

// cleanText keeps a field from breaking the printer language: ZPL treats
// ^ and ~ as commands, TSPL ends a string at ".
func cleanText(s string) string {
	return strings.NewReplacer("^", " ", "~", " ", "\"", "'", "\n", " ", "\r", " ").Replace(s)
}

// BuildZPL renders labels for a Zebra (or ZPL-compatible) printer.
func BuildZPL(s LabelSpec, d LabelData, copies int) []byte {
	l := Layout(s, d)
	var b strings.Builder
	b.WriteString("^XA^CI28")
	fmt.Fprintf(&b, "^PW%d^LL%d^LH0,0", l.WidthDots, l.HeightDots)
	for _, e := range l.Elements {
		switch e.Kind {
		case "text":
			fmt.Fprintf(&b, "^FO%d,%d^A0N,%d,%d^FB%d,1,0,L^FD%s^FS", e.X, e.Y, e.H, e.H*9/10, e.W, cleanText(e.Text))
		case "barcode":
			if d.Symbology == "EAN13" {
				code := e.Text
				if len(code) == 13 {
					code = code[:12] // ZPL computes the check digit
				}
				fmt.Fprintf(&b, "^FO%d,%d^BY%d^BEN,%d,Y,N^FD%s^FS", e.X, e.Y, e.Module, e.H, code)
			} else {
				fmt.Fprintf(&b, "^FO%d,%d^BY%d^BCN,%d,Y,N,N^FD%s^FS", e.X, e.Y, e.Module, e.H, cleanText(e.Text))
			}
		case "qr":
			fmt.Fprintf(&b, "^FO%d,%d^BQN,2,%d^FDMA,%s^FS", e.X, e.Y, min(e.Module, 10), cleanText(e.Text))
		}
	}
	fmt.Fprintf(&b, "^PQ%d^XZ", max(copies, 1))
	return []byte(b.String())
}

// BuildTSPL renders labels for a TSC (or TSPL-compatible) printer.
func BuildTSPL(s LabelSpec, d LabelData, copies int) []byte {
	l := Layout(s, d)
	var b strings.Builder
	fmt.Fprintf(&b, "SIZE %s mm,%s mm\r\nGAP 2 mm,0 mm\r\nDIRECTION 1\r\nCLS\r\n", trimNum(s.WidthMM), trimNum(s.HeightMM))
	for _, e := range l.Elements {
		switch e.Kind {
		case "text":
			// Font "0" is the scalable one: x/y multipliers in points.
			pt := max(e.H*72/s.DPI, 6)
			fmt.Fprintf(&b, "TEXT %d,%d,\"0\",0,%d,%d,\"%s\"\r\n", e.X, e.Y, pt, pt, cleanText(fitChars(e.Text, e.W, e.H)))
		case "barcode":
			kind := "128"
			if d.Symbology == "EAN13" {
				kind = "EAN13"
			}
			fmt.Fprintf(&b, "BARCODE %d,%d,\"%s\",%d,1,0,%d,%d,\"%s\"\r\n", e.X, e.Y, kind, e.H, e.Module, e.Module, cleanText(e.Text))
		case "qr":
			fmt.Fprintf(&b, "QRCODE %d,%d,M,%d,A,0,\"%s\"\r\n", e.X, e.Y, min(max(e.Module, 1), 10), cleanText(e.Text))
		}
	}
	fmt.Fprintf(&b, "PRINT 1,%d\r\n", max(copies, 1))
	return []byte(b.String())
}

// BuildCustomESCPOS renders a studio label on an ESC/POS printer: the same
// field choices, one per line (ESC/POS has no absolute positioning).
func BuildCustomESCPOS(s LabelSpec, d LabelData, copies int) []byte {
	chars := max(int(s.WidthMM/1.6), 12) // about 1.6 mm per character in font A
	b := New()
	for i := 0; i < max(copies, 1); i++ {
		b.AlignCenter()
		if s.Fields.Name {
			b.Bold(true).Line(truncate(d.ProductName, chars)).Bold(false)
		}
		switch s.Fields.Code {
		case "qr":
			b.QRCode(d.BarcodeCode, 6)
		case "barcode":
			if d.BarcodeCode != "" {
				printBarcode(b, d, byte(min(max(s.dots(s.HeightMM)/3, 30), 200)), 2)
			}
		}
		var parts []string
		if s.Fields.MRP {
			parts = append(parts, "MRP: "+d.MRP)
		}
		if s.Fields.Price {
			parts = append(parts, "Price: "+d.SellingPrice)
		}
		if len(parts) > 0 {
			b.Line(strings.Join(parts, "   "))
		}
		if s.Fields.SKU {
			b.Line("SKU: " + d.SKU)
		}
		if s.Fields.Batch && d.BatchNo != "" {
			line := "Batch: " + d.BatchNo
			if d.ExpiryDate != "" {
				line += "   Exp: " + d.ExpiryDate
			}
			b.Line(line)
		}
		b.FeedLines(2).Cut()
	}
	return b.Bytes()
}

// fitChars trims text that wouldn't fit a box `w` dots wide at character
// height `h` (TSPL has no field block to do it, unlike ZPL's ^FB).
func fitChars(s string, w, h int) string {
	n := max(w/max(h*6/10, 1), 4)
	return truncate(s, n)
}

func trimNum(f float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.1f", f), "0"), ".")
}
