package purchase

// Reading a supplier's invoice document into import lines (§3.90).
//
// Two sources, one result. A typed PDF is read from its text: the browser
// extracts each run of text with its position (pdf.js) and sends the
// pages; parseDocPages finds the item table by its column headings, puts
// every piece of text under the heading it sits beneath, and joins a
// line's wrapped second row onto it. A photo or a scanned PDF has no
// text, so the AI reader (invoice_ai.go) transcribes it into the same
// rows. Then buildLines applies the supplier's template (§3.90,
// migrations/086) — what the "variant" column means, how sizes are
// written, where the cost comes from, the brand — and the lines go to the
// screen's editable table and on to the CSV preview and commit, which are
// unchanged. Nothing here writes anything.
//
// Everything in this file is pure (no database), so the rules are tested
// against real invoices in invoice_reader_test.go.

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// docItem is one run of text on a PDF page, in points from the page's
// top-left corner: X the left edge, Y the baseline, W the width.
type docItem struct {
	S string  `json:"s"`
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
}

type docPage struct {
	Width  float64   `json:"width"`
	Height float64   `json:"height"`
	Items  []docItem `json:"items"`
}

// invoiceColumns: the printed headings each field may go by. A template
// lists a supplier's own; an empty list means the defaults.
type invoiceColumns struct {
	Serial   []string `json:"serial"`
	Item     []string `json:"item_name"`
	Variant  []string `json:"variant"`
	HSN      []string `json:"hsn_code"`
	Quantity []string `json:"quantity"`
	MRP      []string `json:"mrp"`
	Rate     []string `json:"rate"`
	Tax      []string `json:"tax"`
	GSTRate  []string `json:"gst_rate"`
	Total    []string `json:"line_total"`
}

func defaultInvoiceColumns() invoiceColumns {
	return invoiceColumns{
		Serial:   []string{"No", "No.", "S.No", "S No", "Sl", "Sl No", "Sr", "Sr No", "#"},
		Item:     []string{"Items", "Item", "Item Name", "Description", "Particulars", "Product", "Product Name", "Description of Goods"},
		Variant:  []string{"Batch No.", "Batch", "Size", "Variant"},
		HSN:      []string{"HSN No.", "HSN", "HSN/SAC", "HSN Code"},
		Quantity: []string{"Qty.", "Qty", "Quantity"},
		MRP:      []string{"MRP"},
		Rate:     []string{"Rate", "Price", "Unit Price", "Rate/Unit"},
		Tax:      []string{"Tax", "Tax Amt", "GST Amt", "GST"},
		GSTRate:  []string{"GST %", "Tax %", "GST Rate"},
		Total:    []string{"Total", "Amount", "Net Amount", "Line Total"},
	}
}

// withDefaults fills every field the template left empty.
func (c invoiceColumns) withDefaults() invoiceColumns {
	d := defaultInvoiceColumns()
	pick := func(v, def []string) []string {
		var out []string
		for _, s := range v {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		if len(out) == 0 {
			return def
		}
		return out
	}
	return invoiceColumns{
		Serial: pick(c.Serial, d.Serial), Item: pick(c.Item, d.Item), Variant: pick(c.Variant, d.Variant),
		HSN: pick(c.HSN, d.HSN), Quantity: pick(c.Quantity, d.Quantity), MRP: pick(c.MRP, d.MRP),
		Rate: pick(c.Rate, d.Rate), Tax: pick(c.Tax, d.Tax), GSTRate: pick(c.GSTRate, d.GSTRate), Total: pick(c.Total, d.Total),
	}
}

func (c invoiceColumns) fields() []struct {
	field  string
	labels []string
} {
	return []struct {
		field  string
		labels []string
	}{
		{"serial", c.Serial}, {"item_name", c.Item}, {"variant", c.Variant}, {"hsn_code", c.HSN}, {"quantity", c.Quantity},
		{"mrp", c.MRP}, {"rate", c.Rate}, {"tax", c.Tax}, {"gst_rate", c.GSTRate}, {"line_total", c.Total},
	}
}

// invoiceOptions: how a supplier's lines become import lines.
type invoiceOptions struct {
	// VariantUse: what the variant column (often printed "BATCH NO.")
	// holds. "size" (default): a size, added to the item's name, so each
	// size is its own item; "batch": a real batch number; "ignore".
	VariantUse string `json:"variant_use"`
	// SizeStyle: "words" (default: SMALL, MEDIUM, XLARGE, 2XLARGE),
	// "letters" (S, M, XL, XXL) or "as_printed".
	SizeStyle string `json:"size_style"`
	// NameFormat: the item's name with a size; {item} and {size}.
	NameFormat string `json:"name_format"`
	// CostBasis: "from_total" (default): the rate before GST worked back
	// from the printed line total, so the lines add up to the printed
	// total to the paisa (discounts included); "rate": the printed rate.
	CostBasis string `json:"cost_basis"`
	// LineAmount: what the printed line total is. "auto" (default): with
	// GST, unless the line's tax amount (or rate × quantity, when no tax
	// is printed) shows it is the taxable value before GST, as Tally-style
	// invoices print it; "with_gst"; "before_gst".
	LineAmount string `json:"line_amount"`
	// BrandRule for new items: "known" (default): a brand the shop already
	// has, named at the start of the item; "first_word"; "none".
	BrandRule string `json:"brand_rule"`
	// SaleGSTRate for new items; nil = the line's own GST when above 0.
	SaleGSTRate *float64 `json:"sale_gst_rate"`
	// Category for new items ("" = uncategorised).
	Category string `json:"category"`
	// DropWords are removed from the variant text ("NEW", "OLD", ...).
	DropWords []string `json:"drop_words"`
	// SizeAliases: printed variant text -> the size to use ("" = none),
	// for a supplier's own codes ("MITIGSIZE-XL": "XLARGE").
	SizeAliases map[string]string `json:"size_aliases"`
	// KeepMRPInName: leave "MRP 220" in an item's printed name (default:
	// it's taken out, and used as the MRP when that column is empty).
	KeepMRPInName bool `json:"keep_mrp_in_name"`
	// Labels the invoice's own number, date and total are printed after.
	InvoiceNumberLabels []string `json:"invoice_number_labels"`
	InvoiceDateLabels   []string `json:"invoice_date_labels"`
	TotalLabels         []string `json:"total_labels"`
	// AIHints: anything the AI reader should know about this supplier's
	// invoices, in plain words.
	AIHints string `json:"ai_hints"`
}

var defaultDropWords = []string{"NEW", "OLD", "RATE", "NEWBATCH", "BATCH", "NEWRATE"}

// normalize checks the choices and fills the defaults.
func (o *invoiceOptions) normalize() error {
	def := func(v *string, d string, allowed ...string) error {
		*v = strings.TrimSpace(*v)
		if *v == "" {
			*v = d
			return nil
		}
		for _, a := range allowed {
			if *v == a {
				return nil
			}
		}
		return fmt.Errorf("must be one of %s", strings.Join(allowed, ", "))
	}
	if err := def(&o.VariantUse, "size", "size", "batch", "ignore"); err != nil {
		return fmt.Errorf("variant_use %w", err)
	}
	if err := def(&o.SizeStyle, "words", "words", "letters", "as_printed"); err != nil {
		return fmt.Errorf("size_style %w", err)
	}
	if err := def(&o.CostBasis, "from_total", "from_total", "rate"); err != nil {
		return fmt.Errorf("cost_basis %w", err)
	}
	if err := def(&o.LineAmount, "auto", "auto", "with_gst", "before_gst"); err != nil {
		return fmt.Errorf("line_amount %w", err)
	}
	if err := def(&o.BrandRule, "known", "known", "first_word", "none"); err != nil {
		return fmt.Errorf("brand_rule %w", err)
	}
	o.NameFormat = strings.TrimSpace(o.NameFormat)
	if o.NameFormat == "" {
		o.NameFormat = "{item} - {size}"
	}
	if !strings.Contains(o.NameFormat, "{item}") || !strings.Contains(o.NameFormat, "{size}") {
		return errors.New("name_format must contain {item} and {size}")
	}
	if o.SaleGSTRate != nil && (*o.SaleGSTRate < 0 || *o.SaleGSTRate > 40) {
		return errors.New("sale_gst_rate must be 0 to 40")
	}
	o.Category = strings.TrimSpace(o.Category)
	if o.DropWords == nil {
		o.DropWords = append([]string(nil), defaultDropWords...)
	}
	clean := map[string]string{}
	for k, v := range o.SizeAliases {
		if k = strings.ToUpper(strings.TrimSpace(k)); k != "" {
			clean[k] = strings.ToUpper(strings.TrimSpace(v))
		}
	}
	o.SizeAliases = clean
	if len(o.InvoiceNumberLabels) == 0 {
		o.InvoiceNumberLabels = []string{"Invoice No", "Invoice Number", "Inv No", "Bill No", "Invoice #", "Bill Number"}
	}
	if len(o.InvoiceDateLabels) == 0 {
		o.InvoiceDateLabels = []string{"Invoice Date", "Inv Date", "Bill Date", "Dated", "Date"}
	}
	if len(o.TotalLabels) == 0 {
		o.TotalLabels = []string{"Grand Total", "Total Amount", "Invoice Total", "Invoice Amount", "Net Amount", "Net Payable", "Amount Payable", "Total"}
	}
	o.AIHints = strings.TrimSpace(o.AIHints)
	if len(o.AIHints) > 2000 {
		return errors.New("ai_hints is limited to 2000 characters")
	}
	return nil
}

// rawRow is one invoice line as printed, cell by cell.
type rawRow struct {
	Item     string `json:"item"`
	Variant  string `json:"variant"`
	HSN      string `json:"hsn"`
	Quantity string `json:"quantity"`
	MRP      string `json:"mrp"`
	Rate     string `json:"rate"`
	Tax      string `json:"tax"`
	GSTRate  string `json:"gst_rate"`
	Total    string `json:"total"`
}

// rawDoc is what was read off the document, before any template rule.
type rawDoc struct {
	Rows          []rawRow
	InvoiceNumber string
	InvoiceDate   string // YYYY-MM-DD, or "" when not found
	Total         string // the printed invoice total
	GSTINs        []string
	SupplierName  string
	SubtotalQty   string // the table's own totals row, when printed
	SubtotalTotal string
	Columns       map[string]string // field -> the heading it was found under
}

var (
	errNoText  = errors.New("the document has no text")
	errNoTable = errors.New("no item table found")
)

type textRow struct {
	y     float64
	items []docItem
}

// rowsOf groups a page's text into rows (baselines within 3pt), top to
// bottom, each left to right.
func rowsOf(p docPage) []textRow {
	var items []docItem
	for _, it := range p.Items {
		if it.S = strings.TrimSpace(it.S); it.S != "" {
			items = append(items, it)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if math.Abs(items[i].Y-items[j].Y) > 0.01 {
			return items[i].Y < items[j].Y
		}
		return items[i].X < items[j].X
	})
	var rows []textRow
	for _, it := range items {
		if n := len(rows); n > 0 && it.Y-rows[n-1].y <= 3 {
			rows[n-1].items = append(rows[n-1].items, it)
			continue
		}
		rows = append(rows, textRow{y: it.Y, items: []docItem{it}})
	}
	for i := range rows {
		sort.SliceStable(rows[i].items, func(a, b int) bool { return rows[i].items[a].X < rows[i].items[b].X })
	}
	return rows
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9%#]+`)

func labelKey(s string) string { return nonAlnum.ReplaceAllString(strings.ToLower(s), "") }

type tableCol struct {
	field  string
	label  string
	x      float64
	center float64
}

// findHeader: the row's columns when it is the table's heading row — the
// item column and at least two of quantity / rate / MRP / total.
func findHeader(row textRow, cols invoiceColumns) []tableCol {
	want := map[string]string{} // label key -> field
	for _, f := range cols.fields() {
		for _, l := range f.labels {
			if k := labelKey(l); k != "" {
				if _, taken := want[k]; !taken {
					want[k] = f.field
				}
			}
		}
	}
	used := make([]bool, len(row.items))
	got := map[string]bool{}
	var out []tableCol
	add := func(field, label string, x, right float64) {
		got[field] = true
		out = append(out, tableCol{field: field, label: label, x: x, center: (x + right) / 2})
	}
	// Two runs that read as one heading ("Unit" "Price", "HSN" "No.") first.
	for i := 0; i+1 < len(row.items); i++ {
		a, b := row.items[i], row.items[i+1]
		if used[i] || used[i+1] || b.X-(a.X+a.W) > 8 {
			continue
		}
		if f, ok := want[labelKey(a.S+" "+b.S)]; ok && !got[f] {
			used[i], used[i+1] = true, true
			add(f, a.S+" "+b.S, a.X, b.X+b.W)
		}
	}
	for i, it := range row.items {
		if used[i] {
			continue
		}
		if f, ok := want[labelKey(it.S)]; ok && !got[f] {
			used[i] = true
			add(f, it.S, it.X, it.X+it.W)
		}
	}
	n := 0
	for _, f := range []string{"quantity", "rate", "mrp", "line_total"} {
		if got[f] {
			n++
		}
	}
	if !got["item_name"] || n < 2 {
		return nil
	}
	sort.Slice(out, func(i, j int) bool { return out[i].x < out[j].x })
	return out
}

// columnFor: the column a piece of text belongs to. Text that starts where
// a heading starts is that column's (left-aligned names); anything else
// goes to the heading whose centre is nearest (centred and right-aligned
// numbers).
func columnFor(cols []tableCol, it docItem) string {
	for _, c := range cols {
		if math.Abs(it.X-c.x) <= 4 {
			return c.field
		}
	}
	mid := it.X + it.W/2
	best, dist := "", math.MaxFloat64
	for _, c := range cols {
		if d := math.Abs(mid - c.center); d < dist {
			best, dist = c.field, d
		}
	}
	return best
}

var (
	serialRE    = regexp.MustCompile(`^\d{1,4}\.?$`)
	numberRE    = regexp.MustCompile(`-?\d[\d,]*(?:\.\d+)?`)
	percentRE   = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*%`)
	gstinRE     = regexp.MustCompile(`\b\d{2}[A-Z]{5}\d{4}[A-Z][0-9A-Z]Z[0-9A-Z]\b`)
	stopRowKeys = []string{"subtotal", "total", "grandtotal", "taxableamount", "taxable", "amountinwords", "totalamount"}
)

// firstNumber: the first number in s ("₹ 2,13,694.50" -> 213694.5).
func firstNumber(s string) (float64, bool) {
	m := numberRE.FindString(s)
	if m == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(strings.ReplaceAll(m, ",", ""), 64)
	return v, err == nil
}

// lastNumber: the last number in s.
func lastNumber(s string) (float64, bool) {
	all := numberRE.FindAllString(s, -1)
	if len(all) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(strings.ReplaceAll(all[len(all)-1], ",", ""), 64)
	return v, err == nil
}

func isStopRow(cells map[string]string, row textRow) bool {
	first := labelKey(row.items[0].S)
	item := labelKey(cells["item_name"])
	for _, k := range stopRowKeys {
		if item == k || strings.HasPrefix(item, k) && cells["serial"] == "" || first == k {
			return true
		}
	}
	return false
}

// parseDocPages reads the item table and the invoice's own details off
// the pages.
func parseDocPages(pages []docPage, cols invoiceColumns, opt invoiceOptions) (rawDoc, error) {
	doc := rawDoc{Columns: map[string]string{}}
	any := false
	var allRows [][]textRow
	for _, p := range pages {
		rows := rowsOf(p)
		allRows = append(allRows, rows)
		if len(rows) > 0 {
			any = true
		}
	}
	if !any {
		return doc, errNoText
	}

	found := false
	for _, rows := range allRows {
		var header []tableCol
		var cur *rawRow
		lastY := 0.0
		for _, row := range rows {
			if header == nil {
				if h := findHeader(row, cols); h != nil {
					header = h
					found = true
					for _, c := range h {
						if _, seen := doc.Columns[c.field]; !seen {
							doc.Columns[c.field] = c.label
						}
					}
				}
				continue
			}
			if findHeader(row, cols) != nil { // repeated on the page
				continue
			}
			cells := map[string]string{}
			for _, it := range row.items {
				f := columnFor(header, it)
				if cells[f] != "" {
					cells[f] += " "
				}
				cells[f] += it.S
			}
			if isStopRow(cells, row) {
				if q, ok := firstNumber(cells["quantity"]); ok {
					doc.SubtotalQty = qtyText(q)
				}
				if t, ok := lastNumber(cells["line_total"]); ok {
					doc.SubtotalTotal = money(t)
				}
				break
			}
			_, hasSerial := doc.Columns["serial"]
			_, qtyOK := firstNumber(cells["quantity"])
			start := false
			if hasSerial {
				start = serialRE.MatchString(strings.TrimSpace(cells["serial"]))
			} else {
				start = qtyOK && cells["item_name"] != ""
			}
			switch {
			case start:
				doc.Rows = append(doc.Rows, rawRow{
					Item: cells["item_name"], Variant: cells["variant"], HSN: cells["hsn_code"], Quantity: cells["quantity"],
					MRP: cells["mrp"], Rate: cells["rate"], Tax: cells["tax"], GSTRate: cells["gst_rate"], Total: cells["line_total"],
				})
				cur = &doc.Rows[len(doc.Rows)-1]
				lastY = row.y
			case cur != nil && row.y-lastY <= 22:
				// The line's wrapped second row: a longer name, the rest
				// of the variant, the tax rate under the tax amount.
				join := func(dst *string, s string) {
					if s == "" {
						return
					}
					if *dst != "" {
						*dst += " "
					}
					*dst += s
				}
				join(&cur.Item, cells["item_name"])
				join(&cur.Variant, cells["variant"])
				join(&cur.HSN, cells["hsn_code"])
				join(&cur.Tax, cells["tax"])
				join(&cur.GSTRate, cells["gst_rate"])
				lastY = row.y
			}
		}
	}

	// The invoice's own details.
	for i, rows := range allRows {
		if i == 0 {
			doc.InvoiceNumber = findLabelled(rows, opt.InvoiceNumberLabels, looksLikeInvoiceNumber, false)
			if d := findLabelled(rows, opt.InvoiceDateLabels, func(s string) bool { return parseDocDate(s) != "" }, false); d != "" {
				doc.InvoiceDate = parseDocDate(d)
			}
		}
		if t := findLabelled(rows, opt.TotalLabels, func(s string) bool { _, ok := firstNumber(s); return ok }, true); t != "" {
			if v, ok := lastNumber(t); ok {
				doc.Total = money(v) // the last page's wins
			}
		}
		for _, row := range rows {
			for _, it := range row.items {
				for _, g := range gstinRE.FindAllString(strings.ToUpper(it.S), -1) {
					if !contains(doc.GSTINs, g) {
						doc.GSTINs = append(doc.GSTINs, g)
					}
				}
			}
		}
	}
	if !found {
		return doc, errNoTable
	}
	return doc, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func looksLikeInvoiceNumber(s string) bool {
	s = strings.TrimSpace(s)
	return s != "" && len(s) <= 40 && strings.ContainsAny(s, "0123456789") && parseDocDate(s) == "" && !strings.Contains(s, " ")
}

// findLabelled: the value printed after one of the labels — in the same
// run of text ("Invoice No: 123"), to its right on the row, or just below
// it. Labels are tried in order; last picks the rightmost value on the
// row (a total after its currency sign).
func findLabelled(rows []textRow, labels []string, ok func(string) bool, last bool) string {
	for _, label := range labels {
		lk := labelKey(label)
		if lk == "" {
			continue
		}
		for ri, row := range rows {
			for j, it := range row.items {
				k := labelKey(it.S)
				if !strings.HasPrefix(k, lk) {
					continue
				}
				// After the label in the same run.
				if rest := afterLabel(it.S, label); rest != "" && ok(rest) {
					return rest
				}
				if k != lk { // a longer label ("Invoice Date" for "Invoice")
					continue
				}
				// To its right on the row.
				var right []string
				for _, o := range row.items[j+1:] {
					right = append(right, o.S)
				}
				if last {
					if joined := strings.Join(right, " "); joined != "" && ok(joined) {
						return joined
					}
				} else {
					for _, s := range right {
						if ok(s) {
							return s
						}
					}
				}
				// Just below, in line with it.
				for _, below := range rows[ri+1:] {
					if below.y-row.y > 30 {
						break
					}
					for _, o := range below.items {
						if math.Abs(o.X-it.X) <= 12 && ok(o.S) {
							return o.S
						}
					}
				}
			}
		}
	}
	return ""
}

// afterLabel: "Invoice No: SC/1" -> "SC/1" for the label "Invoice No".
func afterLabel(s, label string) string {
	lk := labelKey(label)
	for i := range s {
		if labelKey(s[:i]) == lk && i > 0 {
			rest := strings.TrimLeft(s[i:], " .:#-–")
			if labelKey(rest) != "" && !strings.HasPrefix(labelKey(rest), "no") && !strings.HasPrefix(labelKey(rest), "date") {
				return strings.TrimSpace(rest)
			}
			return ""
		}
	}
	return ""
}

var dateRE = regexp.MustCompile(`(?i)\b(\d{1,2})[/.\- ](\d{1,2}|jan|feb|mar|apr|may|jun|jul|aug|sep|sept|oct|nov|dec)[a-z]*[/.\- ,]+(\d{4}|\d{2})\b|\b(\d{4})-(\d{2})-(\d{2})\b`)

// parseDocDate: the first date in s, as YYYY-MM-DD (day first, as Indian
// invoices print it).
func parseDocDate(s string) string {
	m := dateRE.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	if m[4] != "" {
		t, err := time.Parse("2006-01-02", m[4]+"-"+m[5]+"-"+m[6])
		if err != nil {
			return ""
		}
		return t.Format("2006-01-02")
	}
	day, _ := strconv.Atoi(m[1])
	month, err := strconv.Atoi(m[2])
	if err != nil {
		months := map[string]int{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "sept": 9, "oct": 10, "nov": 11, "dec": 12}
		month = months[strings.ToLower(m[2])]
	}
	year, _ := strconv.Atoi(m[3])
	if year < 100 {
		year += 2000
	}
	t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	if month < 1 || month > 12 || t.Day() != day {
		return ""
	}
	return t.Format("2006-01-02")
}

// readLine is one import line as the screen shows it, ready for the CSV.
type readLine struct {
	Row         int    `json:"row"`
	ItemName    string `json:"item_name"`
	PrintedName string `json:"printed_name"`
	Variant     string `json:"variant"` // the variant column as printed
	Size        string `json:"size"`
	BatchNo     string `json:"batch_no"`
	HSNCode     string `json:"hsn_code"`
	Quantity    string `json:"quantity"`
	Unit        string `json:"unit"`
	MRP         string `json:"mrp"`
	Rate        string `json:"rate"`
	UnitCost    string `json:"unit_cost"`
	GSTRate     string `json:"gst_rate"`
	Tax         string `json:"tax"`
	LineTotal   string `json:"line_total"` // with GST, as the import checks it
	// PrintedAmount: the line's amount as printed when that was before
	// GST (LineTotal then adds the tax); "" when it is LineTotal.
	PrintedAmount string   `json:"printed_amount,omitempty"`
	SellingPrice  string   `json:"selling_price"`
	Category      string   `json:"category"`
	Brand         string   `json:"brand"`
	SaleGSTRate   string   `json:"sale_gst_rate"`
	Notes         []string `json:"notes"`
}

// gstSlabs: the rates a worked-out GST snaps to.
var gstSlabs = []float64{0, 0.1, 0.25, 1, 1.5, 3, 5, 6, 7.5, 12, 18, 28, 40}

func snapGST(v float64) float64 {
	for _, s := range gstSlabs {
		if math.Abs(v-s) <= 0.6 {
			return s
		}
	}
	return round2(v)
}

var (
	mrpInTextRE = regexp.MustCompile(`(?i)\bMRP\s*[:.\-]?\s*(\d[\d,]*(?:\.\d+)?)`)
	mrpWordRE   = regexp.MustCompile(`(?i)\bMRP\b`)
	spacesRE    = regexp.MustCompile(`\s+`)
	unitRE      = regexp.MustCompile(`(?i)^\s*-?\d[\d,]*(?:\.\d+)?\s*([A-Za-z]+)`)
)

// stripMRP takes "MRP 749" / "MRP220" out of text, returning the rest and
// the MRP (0 when none).
func stripMRP(s string) (string, float64) {
	var mrp float64
	if m := mrpInTextRE.FindStringSubmatch(s); m != nil {
		mrp, _ = strconv.ParseFloat(strings.ReplaceAll(m[1], ",", ""), 64)
	}
	s = mrpInTextRE.ReplaceAllString(s, " ")
	return tidy(s), mrp
}

func tidy(s string) string {
	s = spacesRE.ReplaceAllString(strings.TrimSpace(s), " ")
	return strings.Trim(s, " -–,")
}

// sizeWords: a size as printed (spaces, dots and hyphens removed) -> its
// two spellings.
var sizeWords = map[string][2]string{
	"XS": {"XSMALL", "XS"}, "XSMALL": {"XSMALL", "XS"}, "EXTRASMALL": {"XSMALL", "XS"},
	"S": {"SMALL", "S"}, "SM": {"SMALL", "S"}, "SMALL": {"SMALL", "S"},
	"M": {"MEDIUM", "M"}, "MED": {"MEDIUM", "M"}, "MEDIUM": {"MEDIUM", "M"},
	"L": {"LARGE", "L"}, "LG": {"LARGE", "L"}, "LARGE": {"LARGE", "L"},
	"XL": {"XLARGE", "XL"}, "XLARGE": {"XLARGE", "XL"}, "EXTRALARGE": {"XLARGE", "XL"},
	"XXL": {"2XLARGE", "XXL"}, "2XL": {"2XLARGE", "XXL"}, "XXLARGE": {"2XLARGE", "XXL"}, "2XLARGE": {"2XLARGE", "XXL"},
	"XXXL": {"3XLARGE", "3XL"}, "3XL": {"3XLARGE", "3XL"}, "XXXLARGE": {"3XLARGE", "3XL"}, "3XLARGE": {"3XLARGE", "3XL"},
	"4XL": {"4XLARGE", "4XL"}, "XXXXL": {"4XLARGE", "4XL"}, "4XLARGE": {"4XLARGE", "4XL"},
}

var sizeKeyRE = regexp.MustCompile(`[\s.\-]+`)

func sizeSpelling(printed, style string) (string, bool) {
	w, ok := sizeWords[sizeKeyRE.ReplaceAllString(strings.ToUpper(printed), "")]
	if !ok {
		return "", false
	}
	switch style {
	case "letters":
		return w[1], true
	case "as_printed":
		return strings.ToUpper(strings.TrimSpace(printed)), true
	}
	return w[0], true
}

var (
	sizeAfterRE  = regexp.MustCompile(`SIZE\s*[:\-]?\s*([0-9]+(?:\.[0-9]+)?|[A-Z0-9]+(?:\s+LARGE)?)`)
	sizeBeforeRE = regexp.MustCompile(`\b([A-Z0-9]{1,4})\s*-?\s*SIZE\b`)
	codeRE       = regexp.MustCompile(`^(?:[A-Z]+[0-9]+|[0-9]+[A-Z]+)[A-Z0-9]*$`)
)

// extractSize reads the size out of the variant column's text. The MRP
// ("MRP 180", or the MRP's digits on their own), the drop words ("NEW"),
// the supplier's own item codes and the "-" placeholder are taken out; a
// size after or before the word SIZE ("SIZE 3", "MITIGSIZE-XL", "S-SIZE")
// is that size; otherwise what is left, with size words spelled the
// template's way ("X LARGE" -> XLARGE). Words the name already has are
// not added again ("MENS" on "... PAD MENS").
func extractSize(variant string, mrp float64, name string, opt invoiceOptions) string {
	t := strings.ToUpper(tidy(variant))
	if t == "" || t == "-" {
		return ""
	}
	if a, ok := opt.SizeAliases[t]; ok {
		return a
	}
	t, _ = stripMRP(t)
	t = mrpWordRE.ReplaceAllString(t, " ")
	if mrp > 0 {
		m := strconv.FormatFloat(mrp, 'f', -1, 64)
		// The MRP's digits on their own ("MENS 2415") or stuck to the end
		// of a code ("SIZEXL360").
		t = regexp.MustCompile(`(^|[^0-9.])`+regexp.QuoteMeta(m)+`$`).ReplaceAllString(t, "$1")
		t = regexp.MustCompile(`\b`+regexp.QuoteMeta(m)+`\b`).ReplaceAllString(t, " ")
	}
	t = tidy(t)

	if m := sizeAfterRE.FindStringSubmatch(t); m != nil {
		v := strings.TrimSpace(m[1])
		if _, err := strconv.ParseFloat(v, 64); err == nil {
			return "SIZE " + v
		}
		if s, ok := sizeSpelling(v, opt.SizeStyle); ok {
			return s
		}
	}
	if m := sizeBeforeRE.FindStringSubmatch(t); m != nil {
		if s, ok := sizeSpelling(m[1], opt.SizeStyle); ok {
			return s
		}
	}
	if s, ok := sizeSpelling(t, opt.SizeStyle); ok {
		return s
	}

	drop := map[string]bool{}
	for _, w := range opt.DropWords {
		drop[strings.ToUpper(strings.TrimSpace(w))] = true
	}
	var kept []string
	for _, tok := range strings.Fields(strings.ReplaceAll(t, "-", " ")) {
		if drop[tok] || tok == "SIZE" {
			continue
		}
		if len(tok) >= 6 && codeRE.MatchString(tok) { // the supplier's own item code
			continue
		}
		if s, ok := sizeSpelling(tok, opt.SizeStyle); ok {
			tok = s
		}
		kept = append(kept, tok)
	}
	if len(kept) == 0 {
		return ""
	}
	// A two-word size ("X LARGE") once the rest is gone.
	if s, ok := sizeSpelling(strings.Join(kept, ""), opt.SizeStyle); ok {
		return s
	}
	have := map[string]bool{}
	for _, w := range strings.Fields(strings.ToUpper(name)) {
		have[w] = true
	}
	allThere := true
	for _, k := range kept {
		if !have[k] {
			allThere = false
		}
	}
	if allThere {
		return ""
	}
	return strings.Join(kept, " ")
}

// brandFor: the brand of a new item under the template's rule. "known":
// the longest of the shop's brands the name starts with, else one named
// in its first three words.
func brandFor(name, rule string, brands []string) string {
	switch rule {
	case "none":
		return ""
	case "first_word":
		if f := strings.Fields(name); len(f) > 0 {
			return strings.ToUpper(f[0])
		}
		return ""
	}
	up := strings.ToUpper(name) + " "
	best := ""
	for _, b := range brands {
		bu := strings.ToUpper(strings.TrimSpace(b))
		if bu != "" && strings.HasPrefix(up, bu+" ") && len(bu) > len(best) {
			best = b
		}
	}
	if best != "" {
		return best
	}
	words := strings.Fields(strings.ToUpper(name))
	if len(words) > 3 {
		words = words[:3]
	}
	for _, w := range words {
		for _, b := range brands {
			if strings.ToUpper(strings.TrimSpace(b)) == w {
				return b
			}
		}
	}
	return ""
}

// costText: a unit cost to six places, without trailing zeros, so the
// lines add up to the printed total.
func costText(v float64) string {
	s := strconv.FormatFloat(math.Round(v*1e6)/1e6, 'f', 6, 64)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "" || s == "-" {
		return "0"
	}
	return s
}

// buildLines turns the printed rows into import lines under the template.
func buildLines(rows []rawRow, opt invoiceOptions, brands []string) []readLine {
	out := make([]readLine, 0, len(rows))
	for i, r := range rows {
		l := readLine{Row: i + 1, Variant: tidy(r.Variant), Notes: []string{}}
		name := tidy(r.Item)
		l.PrintedName = name
		mrp, _ := firstNumber(r.MRP)
		if !opt.KeepMRPInName {
			var nameMRP float64
			name, nameMRP = stripMRP(name)
			if mrp == 0 && nameMRP > 0 {
				mrp = nameMRP
			}
		}
		if _, variantMRP := stripMRP(r.Variant); mrp == 0 && variantMRP > 0 {
			mrp = variantMRP
		}
		qty, qtyOK := firstNumber(r.Quantity)
		if m := unitRE.FindStringSubmatch(r.Quantity); m != nil {
			l.Unit = strings.ToUpper(m[1])
		}
		total, totalOK := lastNumber(r.Total)
		rate, rateOK := firstNumber(r.Rate)
		taxAmt, taxOK := firstNumber(strings.Split(r.Tax, "(")[0])

		gst, gstKnown := 0.0, false
		if m := percentRE.FindStringSubmatch(r.GSTRate); m != nil {
			gst, _ = strconv.ParseFloat(m[1], 64)
			gstKnown = true
		} else if v, ok := firstNumber(r.GSTRate); ok {
			gst, gstKnown = v, true
		} else if m := percentRE.FindStringSubmatch(r.Tax); m != nil {
			gst, _ = strconv.ParseFloat(m[1], 64)
			gstKnown = true
		}
		// Whether the printed amount is before GST (the taxable value).
		before := opt.LineAmount == "before_gst"
		if opt.LineAmount == "auto" && qtyOK && qty > 0 && totalOK && total > 0 {
			if gstKnown && gst > 0 && taxOK && taxAmt > 0 {
				before = math.Abs(taxAmt-total*gst/100) < math.Abs(taxAmt-total*gst/(100+gst))
			} else if (gstKnown && gst > 0 || !gstKnown && taxOK && taxAmt > 0) && rateOK && rate > 0 {
				before = math.Abs(rate*qty-total) <= math.Max(0.02, total*0.001)
			}
		}
		if !gstKnown && taxOK && totalOK {
			if before && total > 0 {
				gst, gstKnown = snapGST(taxAmt/total*100), true
			} else if !before && total-taxAmt > 0 {
				gst, gstKnown = snapGST(taxAmt/(total-taxAmt)*100), true
			}
			if gstKnown {
				l.Notes = append(l.Notes, fmt.Sprintf("GST %s%% worked out from the tax amount", strconv.FormatFloat(gst, 'f', -1, 64)))
			}
		}
		// The amount before GST and the line total with it, as the import
		// works them out: quantity × cost, plus GST on that.
		taxable, withGST := total, total
		if before {
			withGST = total + round2(total*gst/100)
		} else {
			taxable = total / (1 + gst/100)
		}

		switch {
		case !qtyOK || qty <= 0:
			l.Notes = append(l.Notes, "no quantity could be read")
		case opt.CostBasis == "from_total" && totalOK && (gstKnown || before):
			cost := taxable / qty
			l.UnitCost = costText(cost)
			if rateOK && rate > 0 && math.Abs(cost-rate) > math.Max(0.02, rate*0.01) {
				l.Notes = append(l.Notes, fmt.Sprintf("the printed rate is %s; from the line total it comes to %s (a discount?)", money(rate), money(cost)))
			}
		case rateOK:
			l.UnitCost = costText(rate)
		case totalOK && (gstKnown || before):
			l.UnitCost = costText(taxable / qty)
		default:
			l.Notes = append(l.Notes, "no rate or total could be read")
		}
		if before && totalOK && gst > 0 {
			l.PrintedAmount = money(total)
			if opt.LineAmount == "auto" {
				l.Notes = append(l.Notes, fmt.Sprintf("the amount printed, %s, is before GST; with GST the line is %s", money(total), money(withGST)))
			}
		}
		if qtyOK {
			l.Quantity = qtyText(qty)
		}
		if mrp > 0 {
			l.MRP = money(mrp)
		}
		if rateOK {
			l.Rate = money(rate)
		}
		if taxOK {
			l.Tax = money(taxAmt)
		}
		if totalOK {
			l.LineTotal = money(withGST)
		}
		if gstKnown {
			l.GSTRate = strconv.FormatFloat(gst, 'f', -1, 64)
		}
		if h := tidy(r.HSN); h != "" && h != "-" && regexp.MustCompile(`^\d{4,8}$`).MatchString(h) {
			l.HSNCode = h
		}

		switch opt.VariantUse {
		case "size":
			l.Size = extractSize(r.Variant, mrp, name, opt)
		case "batch":
			if b := tidy(r.Variant); b != "-" {
				l.BatchNo = b
			}
		}
		l.ItemName = name
		if l.Size != "" {
			l.ItemName = strings.NewReplacer("{item}", name, "{size}", l.Size).Replace(opt.NameFormat)
		}
		l.Brand = brandFor(name, opt.BrandRule, brands)
		l.Category = opt.Category
		switch {
		case opt.SaleGSTRate != nil:
			l.SaleGSTRate = strconv.FormatFloat(*opt.SaleGSTRate, 'f', -1, 64)
		case gstKnown && gst > 0:
			l.SaleGSTRate = l.GSTRate
		}
		out = append(out, l)
	}
	return out
}

// linesCSV writes the lines in the import template's columns.
func linesCSV(lines []readLine) string {
	var b strings.Builder
	b.WriteString(strings.Join(templateColumns, ",") + "\n")
	for _, l := range lines {
		rec := map[string]string{
			"item_name": l.ItemName, "quantity": l.Quantity, "unit_cost": l.UnitCost, "gst_rate": l.GSTRate, "mrp": l.MRP,
			"selling_price": l.SellingPrice, "line_total": l.LineTotal, "hsn_code": l.HSNCode, "category": l.Category,
			"brand": l.Brand, "sale_gst_rate": l.SaleGSTRate, "batch_no": l.BatchNo,
		}
		cells := make([]string, len(templateColumns))
		for i, c := range templateColumns {
			v := rec[c]
			if strings.ContainsAny(v, ",\"\n") {
				v = `"` + strings.ReplaceAll(v, `"`, `""`) + `"`
			}
			cells[i] = v
		}
		b.WriteString(strings.Join(cells, ",") + "\n")
	}
	return b.String()
}
