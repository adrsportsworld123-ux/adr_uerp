// Package documents renders the merchant's downloadable business
// documents — sale receipts, GRNs, purchase bills, purchase returns — as
// PDF, each under its branch's letterhead (branch_print_settings: logo,
// display name, address, contact lines, custom header and footer;
// migrations/049).
//
// Rendering is server-side on purpose: the web admin and the POS download
// the same bytes, so a receipt can't look different depending on which
// client printed it — the same reasoning internal/printing already applies
// to ESC/POS ("the JSON and printed receipt can never drift apart"). Each
// domain package assembles a Document from its own loader and calls
// Render; this package never queries a sale or a GRN itself.
package documents

import (
	"bytes"
	_ "embed"
	"fmt"
	"strings"

	"github.com/go-pdf/fpdf"

	"erp-core-go/internal/taxes"
)

// Geist (SIL OFL 1.1, fonts/Geist-OFL.txt) — the design system's face,
// already bundled by erp-pos-flutter. Embedded, not read from disk, so
// the binary renders anywhere; it carries the ₹ glyph the core PDF fonts
// lack.
var (
	//go:embed fonts/Geist-Regular.ttf
	fontRegular []byte
	//go:embed fonts/Geist-SemiBold.ttf
	fontBold []byte
)

// Format is the paper a document renders on.
type Format string

const (
	FormatA4      Format = "a4"
	FormatThermal Format = "thermal" // 80 mm roll, page as long as the content
)

// ParseFormat accepts "a4" or "thermal"; anything else is fallback.
func ParseFormat(s string, fallback Format) Format {
	switch Format(strings.ToLower(strings.TrimSpace(s))) {
	case FormatA4:
		return FormatA4
	case FormatThermal:
		return FormatThermal
	}
	return fallback
}

// Letterhead is everything above and below a document's own content,
// resolved from the branch's print settings (LoadLetterhead).
type Letterhead struct {
	Name       string   // display name (settings) → merchant trade name → legal name
	BranchName string   // printed under Name unless it would repeat it
	Address    []string // one entry per printed line
	Phone      string
	Email      string
	GSTIN      string // "" when the branch hides it or has none
	Header     []string
	Footer     []string // the footer for this kind of document (receipt or purchase)
	Logo       []byte   // nil when there is none or the branch hides it
	LogoMime   string
	// The branch's currency (migrations/061); zero = INR / ₹.
	Currency taxes.Currency
}

// Column of the A4 line-item table. Width is relative: the widths are
// scaled to the printable width.
type Column struct {
	Title string
	Width float64
	Right bool // right-align (numbers)
}

// Row is one line item. Cells feed the A4 table (one per Column). The
// thermal layout prints Title on its own (wrapped) line, then Detail on
// the left and Amount on the right; a document that is never rendered on
// thermal paper can leave those empty.
type Row struct {
	Cells  []string
	Title  string
	Detail string
	Amount string
}

// Total is one line of the totals block under the table.
type Total struct {
	Label string
	Value string
	Bold  bool
}

// Party is the counterparty block: the customer of a sale, the supplier
// of a purchase document.
type Party struct {
	Label string // "Bill to", "Supplier"
	Name  string
	Lines []string
}

// Document is one business document, ready to render.
type Document struct {
	Title    string // "TAX INVOICE", "GOODS RECEIPT NOTE"
	Number   string
	Date     string
	Stamp    string // "VOIDED", "DRAFT" — printed prominently when the document isn't final
	Meta     [][2]string
	Party    *Party
	Columns  []Column
	Rows     []Row
	Totals   []Total
	TaxTable *Table      // the rate-wise tax summary under the totals; nil = none
	Payments [][2]string // method, amount
	Notes    []string
}

// Render returns the document as a PDF.
func Render(lh Letterhead, d Document, f Format) ([]byte, error) {
	if !lh.Currency.Indian() || (lh.Currency.Symbol != "" && lh.Currency.Symbol != "₹") {
		d = localize(d, lh.Currency)
	}
	if f == FormatThermal {
		return renderThermal(lh, d)
	}
	return renderA4(lh, d)
}

func newPDF(orientation string, size fpdf.SizeType) *fpdf.Fpdf {
	pdf := fpdf.NewCustom(&fpdf.InitType{OrientationStr: orientation, UnitStr: "mm", Size: size})
	pdf.AddUTF8FontFromBytes("geist", "", fontRegular)
	pdf.AddUTF8FontFromBytes("geist", "B", fontBold)
	pdf.SetFont("geist", "", 9)
	pdf.SetCreator("ADR uERP", true)
	return pdf
}

// registerLogo hands the logo to fpdf and returns its name and aspect
// ratio (height/width), or "" when there is no usable logo. A logo that
// fails to decode is dropped rather than failing the whole document: the
// receipt matters more than the picture on it.
func registerLogo(pdf *fpdf.Fpdf, lh Letterhead) (string, float64) {
	if len(lh.Logo) == 0 {
		return "", 0
	}
	typ := "PNG"
	if lh.LogoMime == "image/jpeg" {
		typ = "JPG"
	}
	info := pdf.RegisterImageOptionsReader("logo", fpdf.ImageOptions{ImageType: typ}, bytes.NewReader(lh.Logo))
	if pdf.Err() || info == nil || info.Width() == 0 {
		pdf.ClearError()
		return "", 0
	}
	return "logo", info.Height() / info.Width()
}

func output(pdf *fpdf.Fpdf) ([]byte, error) {
	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ---------------------------------------------------------------------
// A4
// ---------------------------------------------------------------------

const (
	a4Margin = 12.0
	lineH    = 4.6
)

func renderA4(lh Letterhead, d Document) ([]byte, error) {
	pdf := newPDF("P", fpdf.SizeType{Wd: 210, Ht: 297})
	pdf.SetMargins(a4Margin, a4Margin, a4Margin)
	pdf.AliasNbPages("{nb}")
	pageW, pageH := pdf.GetPageSize()
	contentW := pageW - 2*a4Margin

	// Footer: the branch's footer lines, then a page counter. Its height
	// sets the auto page-break margin so content never runs into it.
	footerH := 10 + float64(len(lh.Footer))*4
	pdf.SetAutoPageBreak(true, footerH+a4Margin)
	pdf.SetFooterFunc(func() {
		pdf.SetY(pageH - a4Margin - footerH + 2)
		pdf.SetDrawColor(200, 200, 200)
		pdf.Line(a4Margin, pdf.GetY(), pageW-a4Margin, pdf.GetY())
		pdf.Ln(1.5)
		pdf.SetFont("geist", "", 8)
		pdf.SetTextColor(70, 70, 70)
		for _, l := range lh.Footer {
			pdf.CellFormat(contentW, 4, l, "", 1, "C", false, 0, "")
		}
		pdf.SetTextColor(140, 140, 140)
		pdf.SetFont("geist", "", 7)
		pdf.CellFormat(contentW/2, 4, "Computer-generated document", "", 0, "L", false, 0, "")
		pdf.CellFormat(contentW/2, 4, fmt.Sprintf("Page %d of {nb}", pdf.PageNo()), "", 0, "R", false, 0, "")
		pdf.SetTextColor(0, 0, 0)
	})
	pdf.AddPage()

	logo, ratio := registerLogo(pdf, lh)

	// Letterhead, left: logo then name/address. Right: document title.
	top := pdf.GetY()
	textX := a4Margin
	logoBottom := top
	if logo != "" {
		w, h := 30.0, 30.0*ratio
		if h > 22 {
			h, w = 22, 22/ratio
		}
		pdf.ImageOptions(logo, a4Margin, top, w, h, false, fpdf.ImageOptions{}, 0, "")
		textX = a4Margin + w + 4
		logoBottom = top + h
	}
	titleW := 62.0
	leftW := contentW - (textX - a4Margin) - titleW

	pdf.SetXY(textX, top)
	pdf.SetFont("geist", "B", 15)
	pdf.MultiCell(leftW, 6.5, lh.Name, "", "L", false)
	pdf.SetFont("geist", "", 8.5)
	for _, l := range letterheadLines(lh) {
		pdf.SetX(textX)
		pdf.MultiCell(leftW, 4, l, "", "L", false)
	}
	leftBottom := pdf.GetY()

	pdf.SetXY(pageW-a4Margin-titleW, top)
	pdf.SetFont("geist", "B", 13)
	pdf.CellFormat(titleW, 6.5, d.Title, "", 2, "R", false, 0, "")
	pdf.SetFont("geist", "", 9)
	if d.Number != "" {
		pdf.CellFormat(titleW, 4.6, "No. "+d.Number, "", 2, "R", false, 0, "")
	}
	if d.Date != "" {
		pdf.CellFormat(titleW, 4.6, d.Date, "", 2, "R", false, 0, "")
	}
	if d.Stamp != "" {
		pdf.Ln(1)
		pdf.SetFont("geist", "B", 11)
		pdf.SetTextColor(190, 30, 30)
		pdf.CellFormat(titleW, 6, d.Stamp, "", 2, "R", false, 0, "")
		pdf.SetTextColor(0, 0, 0)
	}
	pdf.SetY(maxf(leftBottom, logoBottom, pdf.GetY()) + 1.5)

	if len(lh.Header) > 0 {
		pdf.SetFont("geist", "", 8.5)
		pdf.SetTextColor(60, 60, 60)
		for _, l := range lh.Header {
			pdf.MultiCell(contentW, 4, l, "", "C", false)
		}
		pdf.SetTextColor(0, 0, 0)
	}
	pdf.Ln(1)
	pdf.SetDrawColor(30, 30, 30)
	pdf.SetLineWidth(0.4)
	pdf.Line(a4Margin, pdf.GetY(), pageW-a4Margin, pdf.GetY())
	pdf.SetLineWidth(0.2)
	pdf.Ln(3)

	// Party (left) and meta key/values (right).
	blockTop := pdf.GetY()
	half := contentW / 2
	if d.Party != nil {
		pdf.SetFont("geist", "", 7.5)
		pdf.SetTextColor(110, 110, 110)
		pdf.CellFormat(half, 4, strings.ToUpper(d.Party.Label), "", 2, "L", false, 0, "")
		pdf.SetTextColor(0, 0, 0)
		pdf.SetFont("geist", "B", 10)
		pdf.MultiCell(half-4, 5, d.Party.Name, "", "L", false)
		pdf.SetFont("geist", "", 8.5)
		for _, l := range d.Party.Lines {
			pdf.MultiCell(half-4, 4, l, "", "L", false)
		}
	}
	partyBottom := pdf.GetY()
	pdf.SetY(blockTop)
	for _, kv := range d.Meta {
		metaW := 92.0
		pdf.SetX(pageW - a4Margin - metaW)
		pdf.SetFont("geist", "", 8.5)
		pdf.SetTextColor(110, 110, 110)
		pdf.CellFormat(metaW*0.32, 4.6, kv[0], "", 0, "L", false, 0, "")
		pdf.SetTextColor(0, 0, 0)
		pdf.MultiCell(metaW*0.68, 4.6, kv[1], "", "R", false)
	}
	pdf.SetY(maxf(partyBottom, pdf.GetY()) + 4)

	// Line-item table.
	widths := scaleWidths(d.Columns, contentW)
	drawHeader := func() {
		pdf.SetFont("geist", "B", 8.5)
		pdf.SetFillColor(238, 238, 238)
		for i, c := range d.Columns {
			align := "L"
			if c.Right {
				align = "R"
			}
			pdf.CellFormat(widths[i], 7, c.Title, "", 0, align, true, 0, "")
		}
		pdf.Ln(7)
		pdf.SetFont("geist", "", 8.5)
	}
	drawHeader()
	bottomLimit := pageH - footerH - a4Margin
	pdf.SetDrawColor(220, 220, 220)
	for _, row := range d.Rows {
		// Wrap every cell, so a long product name grows the row instead
		// of overflowing into the next column.
		wrapped := make([][]string, len(d.Columns))
		n := 1
		for i := range d.Columns {
			cell := ""
			if i < len(row.Cells) {
				cell = row.Cells[i]
			}
			wrapped[i] = pdf.SplitText(cell, widths[i]-2)
			if len(wrapped[i]) == 0 {
				wrapped[i] = []string{""}
			}
			if len(wrapped[i]) > n {
				n = len(wrapped[i])
			}
		}
		h := float64(n)*lineH + 2
		if pdf.GetY()+h > bottomLimit {
			pdf.AddPage()
			drawHeader()
		}
		y := pdf.GetY()
		x := a4Margin
		for i, c := range d.Columns {
			align := "L"
			if c.Right {
				align = "R"
			}
			for j, l := range wrapped[i] {
				pdf.SetXY(x, y+1+float64(j)*lineH)
				pdf.CellFormat(widths[i], lineH, l, "", 0, align, false, 0, "")
			}
			x += widths[i]
		}
		pdf.Line(a4Margin, y+h, pageW-a4Margin, y+h)
		pdf.SetXY(a4Margin, y+h)
	}
	pdf.Ln(3)

	// Totals, right-aligned.
	totW := 80.0
	if pdf.GetY()+float64(len(d.Totals))*5.5 > bottomLimit {
		pdf.AddPage()
	}
	for _, t := range d.Totals {
		style, size, h := "", 9.0, 5.2
		if t.Bold {
			style, size, h = "B", 11, 7
		}
		pdf.SetFont("geist", style, size)
		pdf.SetX(pageW - a4Margin - totW)
		pdf.CellFormat(totW*0.55, h, t.Label, "", 0, "L", false, 0, "")
		pdf.CellFormat(totW*0.45, h, t.Value, "", 1, "R", false, 0, "")
	}

	if t := d.TaxTable; t != nil && len(t.Rows) > 0 {
		if pdf.GetY()+float64(len(t.Rows)+2)*5.5 > bottomLimit {
			pdf.AddPage()
		}
		pdf.Ln(3)
		pdf.SetFont("geist", "B", 9)
		pdf.CellFormat(contentW, 5, t.Title, "", 1, "L", false, 0, "")
		tw := scaleWidths(t.Columns, contentW*0.75)
		pdf.SetFont("geist", "B", 8)
		pdf.SetFillColor(238, 238, 238)
		for i, c := range t.Columns {
			align := "L"
			if c.Right {
				align = "R"
			}
			pdf.CellFormat(tw[i], 6, c.Title, "", 0, align, true, 0, "")
		}
		pdf.Ln(6)
		pdf.SetFont("geist", "", 8)
		for _, r := range t.Rows {
			for i, c := range t.Columns {
				align := "L"
				if c.Right {
					align = "R"
				}
				cell := ""
				if i < len(r) {
					cell = r[i]
				}
				pdf.CellFormat(tw[i], 5, cell, "B", 0, align, false, 0, "")
			}
			pdf.Ln(5)
		}
	}

	if len(d.Payments) > 0 {
		pdf.Ln(3)
		pdf.SetFont("geist", "B", 9)
		pdf.CellFormat(contentW, 5, "Payments", "", 1, "L", false, 0, "")
		pdf.SetFont("geist", "", 8.5)
		for _, p := range d.Payments {
			pdf.CellFormat(95, 4.6, p[0], "", 0, "L", false, 0, "")
			pdf.CellFormat(35, 4.6, p[1], "", 1, "R", false, 0, "")
		}
	}

	if len(d.Notes) > 0 {
		pdf.Ln(3)
		pdf.SetFont("geist", "", 8.5)
		pdf.SetTextColor(70, 70, 70)
		for _, n := range d.Notes {
			pdf.MultiCell(contentW, 4.2, n, "", "L", false)
		}
		pdf.SetTextColor(0, 0, 0)
	}

	return output(pdf)
}

// letterheadLines: branch name (unless it repeats the name), address,
// contact, GSTIN — the small print under the letterhead's name.
func letterheadLines(lh Letterhead) []string {
	var out []string
	if lh.BranchName != "" && !strings.EqualFold(lh.BranchName, lh.Name) {
		out = append(out, lh.BranchName)
	}
	out = append(out, lh.Address...)
	var contact []string
	if lh.Phone != "" {
		contact = append(contact, "Ph: "+lh.Phone)
	}
	if lh.Email != "" {
		contact = append(contact, lh.Email)
	}
	if len(contact) > 0 {
		out = append(out, strings.Join(contact, "  ·  "))
	}
	if lh.GSTIN != "" {
		out = append(out, "GSTIN: "+lh.GSTIN)
	}
	return out
}

func scaleWidths(cols []Column, total float64) []float64 {
	sum := 0.0
	for _, c := range cols {
		sum += c.Width
	}
	out := make([]float64, len(cols))
	for i, c := range cols {
		out[i] = c.Width / sum * total
	}
	return out
}

func maxf(vs ...float64) float64 {
	m := vs[0]
	for _, v := range vs[1:] {
		if v > m {
			m = v
		}
	}
	return m
}

// ---------------------------------------------------------------------
// 80 mm thermal
// ---------------------------------------------------------------------

const (
	thermalW      = 80.0
	thermalMargin = 4.0
)

// renderThermal lays the receipt out on an 80 mm roll. The page is as
// long as the content: a first pass on a very long page measures it, the
// second renders onto exactly that height, so a PDF viewer or a driver
// printing it doesn't feed metres of blank paper.
func renderThermal(lh Letterhead, d Document) ([]byte, error) {
	measure := newPDF("P", fpdf.SizeType{Wd: thermalW, Ht: 3000})
	height := drawThermal(measure, lh, d) + thermalMargin
	if height < 60 {
		height = 60
	}
	pdf := newPDF("P", fpdf.SizeType{Wd: thermalW, Ht: height})
	drawThermal(pdf, lh, d)
	return output(pdf)
}

func drawThermal(pdf *fpdf.Fpdf, lh Letterhead, d Document) float64 {
	pdf.SetMargins(thermalMargin, thermalMargin, thermalMargin)
	pdf.SetAutoPageBreak(false, 0)
	pdf.AddPage()
	w := thermalW - 2*thermalMargin

	if logo, ratio := registerLogo(pdf, lh); logo != "" {
		lw, lhgt := 36.0, 36.0*ratio
		if lhgt > 18 {
			lhgt, lw = 18, 18/ratio
		}
		pdf.ImageOptions(logo, (thermalW-lw)/2, pdf.GetY(), lw, lhgt, false, fpdf.ImageOptions{}, 0, "")
		pdf.SetY(pdf.GetY() + lhgt + 1.5)
	}
	pdf.SetFont("geist", "B", 11)
	pdf.MultiCell(w, 5, lh.Name, "", "C", false)
	pdf.SetFont("geist", "", 7.5)
	for _, l := range letterheadLines(lh) {
		pdf.MultiCell(w, 3.6, l, "", "C", false)
	}
	for _, l := range lh.Header {
		pdf.MultiCell(w, 3.6, l, "", "C", false)
	}
	rule := func() {
		pdf.Ln(1)
		pdf.SetDrawColor(0, 0, 0)
		pdf.SetDashPattern([]float64{0.6, 0.6}, 0)
		pdf.Line(thermalMargin, pdf.GetY(), thermalW-thermalMargin, pdf.GetY())
		pdf.SetDashPattern([]float64{}, 0)
		pdf.Ln(1.5)
	}
	rule()

	pdf.SetFont("geist", "B", 9)
	pdf.MultiCell(w, 4.4, d.Title, "", "C", false)
	if d.Stamp != "" {
		pdf.MultiCell(w, 4.4, d.Stamp, "", "C", false)
	}
	pdf.SetFont("geist", "", 7.5)
	kv := func(k, v string) {
		pdf.CellFormat(w*0.4, 3.8, k, "", 0, "L", false, 0, "")
		pdf.CellFormat(w*0.6, 3.8, v, "", 1, "R", false, 0, "")
	}
	if d.Number != "" {
		kv("No.", d.Number)
	}
	if d.Date != "" {
		kv("Date", d.Date)
	}
	for _, m := range d.Meta {
		kv(m[0], m[1])
	}
	if d.Party != nil {
		kv(d.Party.Label, d.Party.Name)
	}
	rule()

	for _, r := range d.Rows {
		title := r.Title
		if title == "" && len(r.Cells) > 0 {
			title = r.Cells[0]
		}
		pdf.SetFont("geist", "", 8)
		pdf.MultiCell(w, 3.8, title, "", "L", false)
		pdf.SetFont("geist", "", 7.5)
		pdf.CellFormat(w*0.62, 3.8, r.Detail, "", 0, "L", false, 0, "")
		pdf.CellFormat(w*0.38, 3.8, r.Amount, "", 1, "R", false, 0, "")
		pdf.Ln(0.6)
	}
	rule()

	for _, t := range d.Totals {
		style, size, h := "", 8.0, 4.0
		if t.Bold {
			style, size, h = "B", 10, 5.5
		}
		pdf.SetFont("geist", style, size)
		pdf.CellFormat(w*0.5, h, t.Label, "", 0, "L", false, 0, "")
		pdf.CellFormat(w*0.5, h, t.Value, "", 1, "R", false, 0, "")
	}
	// On the roll the summary is only worth its paper when the bill mixes
	// rates; a single rate is already on the tax lines ("CGST @ 2.5%").
	if t := d.TaxTable; t != nil && len(t.Rows) > 1 {
		rule()
		pdf.SetFont("geist", "B", 7.5)
		pdf.MultiCell(w, 3.8, t.Title, "", "L", false)
		pdf.SetFont("geist", "", 7.5)
		for _, r := range t.Rows {
			line := ""
			for i := 1; i < len(r) && i < len(t.Columns); i++ {
				if line != "" {
					line += "  "
				}
				line += t.Columns[i].Title + " " + r[i]
			}
			pdf.MultiCell(w, 3.6, r[0]+": "+line, "", "L", false)
		}
	}
	if len(d.Payments) > 0 {
		rule()
		pdf.SetFont("geist", "", 8)
		for _, p := range d.Payments {
			kv(p[0], p[1])
		}
	}
	for _, n := range d.Notes {
		pdf.SetFont("geist", "", 7.5)
		pdf.MultiCell(w, 3.6, n, "", "L", false)
	}
	if len(lh.Footer) > 0 {
		rule()
		pdf.SetFont("geist", "", 7.5)
		for _, l := range lh.Footer {
			pdf.MultiCell(w, 3.6, l, "", "C", false)
		}
	}
	return pdf.GetY()
}
