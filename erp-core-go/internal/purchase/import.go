package purchase

// Purchase invoice import: a supplier's invoice keyed (or exported) into a
// CSV template, previewed, then booked in one transaction — exactly what
// receiving it by hand does (GRN → complete → bill), plus creating the
// products the shop doesn't stock yet:
//
//   - a line that matches an existing product adds to its stock (the GRN
//     completion's stock-in and Weighted Average Cost, unchanged);
//   - a line that doesn't creates the product first (catalog's own create
//     path, with a scannable barcode) and then receives it;
//   - the bill carries the invoice's GST, date and round-off, so the
//     payable equals the printed total to the paisa.
//
// Preview and commit run the same resolution (resolveImport); commit
// trusts nothing from the client but the user's match choices, refuses
// anything the preview would flag as an error, and books everything or
// nothing. The supplier invoice number can only be imported once.

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/accounting"
	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/catalog"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/locale"
	"erp-core-go/internal/search"
)

const maxImportCSV = 1 << 20 // 1 MB: thousands of lines

// templateColumns is the template's header, in order. Only item_name,
// quantity and unit_cost are always required; mrp and sale_gst_rate are
// required for a line that creates a new product.
var templateColumns = []string{
	"item_name", "quantity", "unit_cost", "gst_rate", "mrp", "selling_price", "line_total",
	"sku", "barcode", "hsn_code", "category", "brand", "sale_gst_rate", "batch_no", "expiry_date",
}

// columnAliases lets a supplier's own export headings map onto the template.
var columnAliases = map[string]string{
	"item": "item_name", "items": "item_name", "description": "item_name", "product": "item_name", "product_name": "item_name", "name": "item_name",
	"qty": "quantity", "rate": "unit_cost", "cost": "unit_cost", "purchase_rate": "unit_cost", "price": "unit_cost",
	"gst": "gst_rate", "gst_%": "gst_rate", "tax_%": "gst_rate", "tax_rate": "gst_rate", "purchase_gst_rate": "gst_rate",
	"total": "line_total", "amount": "line_total", "hsn": "hsn_code", "ean": "barcode", "upc": "barcode",
	"sale_price": "selling_price", "selling_gst_rate": "sale_gst_rate", "batch": "batch_no", "expiry": "expiry_date",
}

type importRequest struct {
	SupplierID    string   `json:"supplier_id"`
	BranchID      string   `json:"branch_id"`
	InvoiceNumber string   `json:"invoice_number"`
	InvoiceDate   string   `json:"invoice_date"` // YYYY-MM-DD, not in the future
	DueDate       string   `json:"due_date"`     // optional
	Freight       float64  `json:"freight_amount"`
	OtherCharges  float64  `json:"other_charges"`
	InvoiceTotal  *float64 `json:"invoice_total"` // the printed total: checked, and the difference under ₹1 becomes round-off
	CSV           string   `json:"csv"`
	// Matches: the user's choice per CSV row (the row number in the
	// preview): a variant_id to receive into, or "new" to create a product
	// even though a similar one exists.
	Matches map[string]string `json:"matches"`
}

type suggestion struct {
	VariantID string `json:"variant_id"`
	Name      string `json:"name"`
	SKU       string `json:"sku"`
	MRP       string `json:"mrp"`
	Score     int    `json:"score"` // 0-100 word overlap
}

type newProduct struct {
	Name         string `json:"name"`
	SKU          string `json:"sku"`
	Category     string `json:"category"`
	Brand        string `json:"brand"`
	TaxSlab      string `json:"tax_slab"`
	MRP          string `json:"mrp"`
	SellingPrice string `json:"selling_price"`
	Barcode      string `json:"barcode"` // "" = one is generated

	categoryID, brandID, taxSlabID string
	mrp, selling                   float64
	newBrand                       string
	key                            string // rows creating the same product share it
}

type importLine struct {
	Row          int          `json:"row"`
	ItemName     string       `json:"item_name"`
	Quantity     string       `json:"quantity"`
	UnitCost     string       `json:"unit_cost"`
	GSTRate      string       `json:"gst_rate"`
	LineCost     string       `json:"line_cost"`
	LineTax      string       `json:"line_tax"`
	Action       string       `json:"action"` // add_stock | create
	MatchedBy    string       `json:"matched_by,omitempty"`
	VariantID    string       `json:"variant_id,omitempty"`
	ProductName  string       `json:"product_name,omitempty"`
	SKU          string       `json:"sku,omitempty"`
	CurrentMRP   string       `json:"current_mrp,omitempty"`
	OnHand       string       `json:"on_hand,omitempty"` // at the receiving branch, before this invoice
	NewProduct   *newProduct  `json:"new_product,omitempty"`
	Suggestions  []suggestion `json:"suggestions"`
	Warnings     []string     `json:"warnings"`
	Errors       []string     `json:"errors"`
	qty, cost    float64
	tax          float64
	batch, expry string
}

type importSummary struct {
	Lines         int     `json:"lines"`
	Quantity      string  `json:"quantity"`
	Subtotal      string  `json:"subtotal"`
	Tax           string  `json:"tax"`
	Freight       string  `json:"freight"`
	OtherCharges  string  `json:"other_charges"`
	ComputedTotal string  `json:"computed_total"`
	InvoiceTotal  *string `json:"invoice_total"`
	RoundOff      string  `json:"round_off"`
	AddStock      int     `json:"add_stock"`
	Create        int     `json:"create"`
}

type importPreview struct {
	Supplier      string        `json:"supplier"`
	Branch        string        `json:"branch"`
	InvoiceNumber string        `json:"invoice_number"`
	InvoiceDate   string        `json:"invoice_date"`
	Lines         []*importLine `json:"lines"`
	Summary       importSummary `json:"summary"`
	NewBrands     []string      `json:"new_brands"`
	Errors        []string      `json:"errors"` // invoice-level
	Ready         bool          `json:"ready"`  // no errors anywhere: commit will go through

	roundOff, subtotal, tax float64
	billDate                *string
}

// ---------------------------------------------------------------------
// Parsing
// ---------------------------------------------------------------------

var numberJunk = regexp.MustCompile(`[₹,\s]|(?i)rs\.?|(?i)pcs|(?i)nos|(?i)units?|%`)

// parseNumber accepts what an invoice prints: "₹ 1,100", "36 PCS", "5%".
func parseNumber(s string) (float64, bool) {
	s = numberJunk.ReplaceAllString(strings.TrimSpace(s), "")
	if s == "" || s == "-" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
func money(v float64) string   { return strconv.FormatFloat(round2(v), 'f', 2, 64) }
func qtyText(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

var spaces = regexp.MustCompile(`\s+`)

// normKey is how names and SKUs are compared: case, spacing and
// surrounding blanks don't make a different product.
func normKey(s string) string {
	return strings.ToUpper(spaces.ReplaceAllString(strings.TrimSpace(s), " "))
}

func headerKey(h string) string {
	h = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")))
	h = strings.ReplaceAll(strings.ReplaceAll(h, " ", "_"), ".", "")
	if a, ok := columnAliases[h]; ok {
		return a
	}
	return h
}

type csvRow struct {
	n    int
	cols map[string]string
}

func parseImportCSV(text string) ([]csvRow, []string) {
	r := csv.NewReader(strings.NewReader(text))
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	records, err := r.ReadAll()
	if err != nil {
		return nil, []string{"the file isn't valid CSV: " + err.Error()}
	}
	if len(records) == 0 {
		return nil, []string{"the file is empty"}
	}
	header := make([]string, len(records[0]))
	seen := map[string]bool{}
	for i, h := range records[0] {
		header[i] = headerKey(h)
		seen[header[i]] = true
	}
	var errs []string
	for _, req := range []string{"item_name", "quantity", "unit_cost"} {
		if !seen[req] {
			errs = append(errs, "the file has no "+req+" column — download the template for the expected headings")
		}
	}
	if errs != nil {
		return nil, errs
	}
	var rows []csvRow
	for i, rec := range records[1:] {
		cols := map[string]string{}
		blank := true
		for j, v := range rec {
			if j < len(header) && header[j] != "" {
				cols[header[j]] = strings.TrimSpace(v)
				if strings.TrimSpace(v) != "" {
					blank = false
				}
			}
		}
		if !blank {
			rows = append(rows, csvRow{n: i + 2, cols: cols}) // spreadsheet row number: header is row 1
		}
	}
	if len(rows) == 0 {
		errs = append(errs, "the file has a header but no item rows")
	}
	return rows, errs
}

// ---------------------------------------------------------------------
// Catalogue snapshot for matching
// ---------------------------------------------------------------------

type knownVariant struct {
	id, sku, name, combo, status string
	mrp                          float64
	trackBatch                   bool
	onHand                       float64
	words                        map[string]bool
}

type catalogue struct {
	variants   []*knownVariant
	byBarcode  map[string]*knownVariant
	bySKU      map[string][]*knownVariant
	byName     map[string][]*knownVariant
	categories map[string][]string // normalized name or path → ids
	catLabel   map[string]string   // id → path
	brands     map[string]string   // normalized name → id
	slabs      map[string]string   // rate ("5", "18") → id
	slabName   map[string]string
}

func words(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.Fields(normKey(s)) {
		out[w] = true
	}
	return out
}

func loadCatalogue(ctx context.Context, tx pgx.Tx, branchID string) (*catalogue, error) {
	c := &catalogue{byBarcode: map[string]*knownVariant{}, bySKU: map[string][]*knownVariant{}, byName: map[string][]*knownVariant{},
		categories: map[string][]string{}, catLabel: map[string]string{}, brands: map[string]string{}, slabs: map[string]string{}, slabName: map[string]string{}}
	rows, err := tx.Query(ctx, `
		SELECT pv.id::text, pv.sku, p.name,
		       COALESCE((SELECT string_agg(value, ' ' ORDER BY key) FROM jsonb_each_text(pv.attribute_combo)), ''),
		       pv.status, pv.mrp::float8, pv.track_batch,
		       COALESCE((SELECT on_hand::float8 FROM stock_levels s WHERE s.variant_id = pv.id AND s.branch_id::text = $1), 0),
		       (SELECT count(*) FROM product_variants o WHERE o.product_id = p.id) = 1
		FROM product_variants pv JOIN products p ON p.id = pv.product_id`, branchID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		v := &knownVariant{}
		var only bool
		if err := rows.Scan(&v.id, &v.sku, &v.name, &v.combo, &v.status, &v.mrp, &v.trackBatch, &v.onHand, &only); err != nil {
			rows.Close()
			return nil, err
		}
		c.variants = append(c.variants, v)
		c.bySKU[normKey(v.sku)] = append(c.bySKU[normKey(v.sku)], v)
		full := normKey(v.name + " " + v.combo)
		c.byName[full] = append(c.byName[full], v)
		if only && normKey(v.name) != full {
			c.byName[normKey(v.name)] = append(c.byName[normKey(v.name)], v)
		}
		v.words = words(v.name + " " + v.combo + " " + v.sku)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	byID := map[string]*knownVariant{}
	for _, v := range c.variants {
		byID[v.id] = v
	}
	if err := eachRow(ctx, tx, `SELECT variant_id::text, code FROM barcodes`, func(r pgx.Rows) error {
		var vid, code string
		if err := r.Scan(&vid, &code); err != nil {
			return err
		}
		if v := byID[vid]; v != nil {
			c.byBarcode[strings.TrimSpace(code)] = v
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := eachRow(ctx, tx, `SELECT id::text, name, path FROM categories WHERE status = 'active'`, func(r pgx.Rows) error {
		var id, name, path string
		if err := r.Scan(&id, &name, &path); err != nil {
			return err
		}
		c.catLabel[id] = strings.TrimPrefix(path, "/")
		c.categories[normKey(name)] = append(c.categories[normKey(name)], id)
		c.categories[normKey(strings.ReplaceAll(strings.TrimPrefix(path, "/"), "/", " > "))] = []string{id}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := eachRow(ctx, tx, `SELECT id::text, name FROM brands WHERE status = 'active'`, func(r pgx.Rows) error {
		var id, name string
		if err := r.Scan(&id, &name); err != nil {
			return err
		}
		c.brands[normKey(name)] = id
		return nil
	}); err != nil {
		return nil, err
	}
	err = eachRow(ctx, tx, `SELECT id::text, name, (cgst_rate + sgst_rate)::float8, igst_rate::float8 FROM tax_slabs WHERE status = 'active'`, func(r pgx.Rows) error {
		var id, name string
		var intra, igst float64
		if err := r.Scan(&id, &name, &intra, &igst); err != nil {
			return err
		}
		rate := intra
		if rate == 0 {
			rate = igst
		}
		key := qtyText(rate)
		if _, dup := c.slabs[key]; !dup {
			c.slabs[key], c.slabName[key] = id, name
		}
		return nil
	})
	return c, err
}

func eachRow(ctx context.Context, tx pgx.Tx, sql string, fn func(pgx.Rows) error) error {
	rows, err := tx.Query(ctx, sql)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// suggest: the closest existing products by shared words, for a line that
// matched nothing exactly. Never applied automatically — "MERCURY + GREEN"
// and "MERCURY + GREEN WOT" can be different balls.
func (c *catalogue) suggest(item string) []suggestion {
	w := words(item)
	type scored struct {
		v *knownVariant
		s int
	}
	var all []scored
	for _, v := range c.variants {
		inter := 0
		for k := range w {
			if v.words[k] {
				inter++
			}
		}
		union := len(w) + len(v.words) - inter
		if inter == 0 || union == 0 {
			continue
		}
		if s := inter * 100 / union; s >= 50 {
			all = append(all, scored{v, s})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].s > all[j].s })
	out := []suggestion{}
	for i := 0; i < len(all) && i < 3; i++ {
		v := all[i].v
		out = append(out, suggestion{VariantID: v.id, Name: strings.TrimSpace(v.name + " " + v.combo), SKU: v.sku, MRP: money(v.mrp), Score: all[i].s})
	}
	return out
}

// ---------------------------------------------------------------------
// Resolution — shared by preview and commit
// ---------------------------------------------------------------------

func resolveImport(ctx context.Context, tx pgx.Tx, req importRequest) (*importPreview, error) {
	p := &importPreview{InvoiceNumber: strings.TrimSpace(req.InvoiceNumber), InvoiceDate: req.InvoiceDate, Lines: []*importLine{}, Errors: []string{}, NewBrands: []string{}}
	fail := func(msg string) { p.Errors = append(p.Errors, msg) }

	if req.SupplierID == "" || tx.QueryRow(ctx, `SELECT name FROM suppliers WHERE id::text = $1`, req.SupplierID).Scan(&p.Supplier) != nil {
		fail("choose the supplier")
	}
	if req.BranchID == "" || tx.QueryRow(ctx, `SELECT name FROM branches WHERE id::text = $1 AND app_branch_visible(id)`, req.BranchID).Scan(&p.Branch) != nil {
		fail("choose the receiving branch")
	}
	if p.InvoiceNumber == "" {
		fail("the supplier's invoice number is required")
	} else if p.Supplier != "" {
		var billNo string
		err := tx.QueryRow(ctx, `SELECT bill_number FROM purchase_bills WHERE supplier_id::text = $1 AND lower(trim(supplier_invoice_number)) = lower($2)`,
			req.SupplierID, p.InvoiceNumber).Scan(&billNo)
		if err == nil {
			fail(fmt.Sprintf("invoice %s from this supplier is already in the system (bill %s)", p.InvoiceNumber, billNo))
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}
	if d, err := pastDate(req.InvoiceDate, locale.Location(ctx, tx)); err != nil || d == nil {
		fail("the invoice date must be a date (YYYY-MM-DD) that isn't in the future")
	} else {
		p.billDate = d
	}
	if req.DueDate != "" {
		if _, err := time.Parse("2006-01-02", req.DueDate); err != nil {
			fail("the due date must be YYYY-MM-DD")
		}
	}
	if req.Freight < 0 || req.OtherCharges < 0 {
		fail("freight and other charges can't be negative")
	}
	if len(req.CSV) > maxImportCSV {
		fail("the file is larger than 1 MB — split the invoice")
		return p, nil
	}
	rows, errs := parseImportCSV(req.CSV)
	p.Errors = append(p.Errors, errs...)
	if rows == nil || req.BranchID == "" {
		return p, nil
	}

	cat, err := loadCatalogue(ctx, tx, req.BranchID)
	if err != nil {
		return nil, err
	}
	newByKey := map[string]*newProduct{}
	newBrands := map[string]bool{}
	var qtyTotal float64
	for _, row := range rows {
		get := func(k string) string { return row.cols[k] }
		l := &importLine{Row: row.n, ItemName: spaces.ReplaceAllString(strings.TrimSpace(get("item_name")), " "), Suggestions: []suggestion{}, Warnings: []string{}, Errors: []string{}}
		p.Lines = append(p.Lines, l)
		bad := func(msg string) { l.Errors = append(l.Errors, msg) }
		if l.ItemName == "" {
			bad("item_name is empty")
		}
		var ok bool
		if l.qty, ok = parseNumber(get("quantity")); !ok || l.qty <= 0 {
			bad("quantity must be a number above 0")
		}
		if l.cost, ok = parseNumber(get("unit_cost")); !ok || l.cost < 0 {
			bad("unit_cost must be a number (the rate before GST)")
		}
		gst := 0.0
		if get("gst_rate") != "" {
			if gst, ok = parseNumber(get("gst_rate")); !ok || gst < 0 || gst > 40 {
				bad("gst_rate must be a percentage such as 5 or 18")
			}
		}
		l.Quantity, l.UnitCost, l.GSTRate = qtyText(l.qty), money(l.cost), qtyText(gst)
		lineCost := round2(l.qty * l.cost)
		l.tax = round2(lineCost * gst / 100)
		l.LineCost, l.LineTax = money(lineCost), money(l.tax)
		if t := get("line_total"); t != "" {
			if printed, ok := parseNumber(t); ok && math.Abs(printed-(lineCost+l.tax)) > 1 {
				l.Warnings = append(l.Warnings, fmt.Sprintf("the printed line total ₹%s doesn't match quantity × rate + GST (₹%s) — check the quantity and rate", money(printed), money(lineCost+l.tax)))
			}
		}
		l.batch, l.expry = get("batch_no"), get("expiry_date")
		if l.expry != "" {
			if _, err := time.Parse("2006-01-02", l.expry); err != nil {
				bad("expiry_date must be YYYY-MM-DD")
			}
		}

		// Which product is it?
		var hit *knownVariant
		choice := req.Matches[strconv.Itoa(row.n)]
		switch {
		case choice == "new":
		case choice != "":
			for _, v := range cat.variants {
				if v.id == choice {
					hit, l.MatchedBy = v, "chosen"
				}
			}
			if hit == nil {
				bad("the product chosen for this line no longer exists")
			}
		default:
			if b := get("barcode"); b != "" && cat.byBarcode[b] != nil {
				hit, l.MatchedBy = cat.byBarcode[b], "barcode"
			} else if s := get("sku"); s != "" && len(cat.bySKU[normKey(s)]) == 1 {
				hit, l.MatchedBy = cat.bySKU[normKey(s)][0], "sku"
			} else if vs := cat.bySKU[normKey(l.ItemName)]; len(vs) == 1 {
				hit, l.MatchedBy = vs[0], "name"
			} else if vs := cat.byName[normKey(l.ItemName)]; len(vs) == 1 {
				hit, l.MatchedBy = vs[0], "name"
			} else if len(vs) > 1 {
				bad("more than one product has this name — put its SKU in the sku column")
			}
		}

		if hit != nil {
			l.Action, l.VariantID, l.SKU, l.CurrentMRP, l.OnHand = "add_stock", hit.id, hit.sku, money(hit.mrp), qtyText(hit.onHand)
			l.ProductName = strings.TrimSpace(hit.name + " " + hit.combo)
			if m, ok := parseNumber(get("mrp")); ok && m > 0 && math.Abs(m-hit.mrp) >= 0.01 {
				l.Warnings = append(l.Warnings, fmt.Sprintf("MRP on the invoice is ₹%s, the system has ₹%s — update it under Pricing if it changed", money(m), money(hit.mrp)))
			}
			if hit.status != "active" {
				l.Warnings = append(l.Warnings, "this product is "+hit.status+" — it will be restocked but stays "+hit.status+" until you reactivate it")
			}
			if hit.trackBatch && l.batch == "" {
				bad("this product is batch-tracked: batch_no is required")
			}
			p.Summary.AddStock++
		} else if len(l.Errors) == 0 || choice == "new" {
			l.Action = "create"
			if choice != "new" {
				l.Suggestions = cat.suggest(l.ItemName)
				if len(l.Suggestions) > 0 {
					l.Warnings = append(l.Warnings, "a similar product exists — pick it if this is the same item, or keep it as new")
				}
			}
			np := &newProduct{Name: l.ItemName, Barcode: get("barcode")}
			np.SKU = normKey(get("sku"))
			if np.SKU == "" {
				np.SKU = normKey(l.ItemName) // the convention the existing catalogue uses
			}
			np.key = np.SKU
			if prev := newByKey[np.key]; prev != nil {
				np = prev // a second row of the same new item: one product, two receipts
			} else {
				if len(cat.bySKU[np.SKU]) > 0 {
					bad("SKU " + np.SKU + " belongs to another product — give this item its own sku")
				}
				if np.mrp, ok = parseNumber(get("mrp")); !ok || np.mrp <= 0 {
					bad("mrp is required to create a new product")
				}
				np.selling = np.mrp
				if sp := get("selling_price"); sp != "" {
					if np.selling, ok = parseNumber(sp); !ok || np.selling <= 0 {
						bad("selling_price must be a positive number")
					}
				}
				if np.selling > np.mrp && np.mrp > 0 {
					bad("selling_price is above the MRP")
				}
				if np.selling > 0 && np.selling < l.cost {
					l.Warnings = append(l.Warnings, "the selling price is below the cost")
				}
				np.MRP, np.SellingPrice = money(np.mrp), money(np.selling)
				// GST it is sold at: required — a product with no tax slab
				// would be billed tax-free at the till.
				saleRate := get("sale_gst_rate")
				if saleRate == "" && gst > 0 {
					saleRate = qtyText(gst)
				}
				if saleRate == "" {
					bad("sale_gst_rate is required for a new product (the GST it is sold at, e.g. 5 or 18)")
				} else if r, ok := parseNumber(saleRate); !ok {
					bad("sale_gst_rate must be a percentage")
				} else if id := cat.slabs[qtyText(r)]; id == "" {
					bad(fmt.Sprintf("there is no active %s%% tax slab — create it under Catalog › Tax Slabs first", qtyText(r)))
				} else {
					np.taxSlabID, np.TaxSlab = id, cat.slabName[qtyText(r)]
				}
				if cName := get("category"); cName != "" {
					ids := cat.categories[normKey(strings.ReplaceAll(strings.TrimPrefix(cName, "/"), "/", " > "))]
					switch len(ids) {
					case 0:
						bad("unknown category \"" + cName + "\" — use an existing one (Catalog › Categories)")
					case 1:
						np.categoryID, np.Category = ids[0], cat.catLabel[ids[0]]
					default:
						bad("category \"" + cName + "\" exists under more than one parent — write it as Parent > " + cName)
					}
				} else {
					l.Warnings = append(l.Warnings, "no category — the product is created uncategorised")
				}
				if bName := strings.TrimSpace(get("brand")); bName != "" {
					np.Brand = bName
					if id := cat.brands[normKey(bName)]; id != "" {
						np.brandID = id
					} else {
						np.newBrand = bName
						if !newBrands[normKey(bName)] {
							newBrands[normKey(bName)] = true
							p.NewBrands = append(p.NewBrands, bName)
						}
					}
				}
				newByKey[np.key] = np
				p.Summary.Create++
			}
			l.NewProduct = np
			l.SKU = np.SKU
		}
		if len(l.Errors) == 0 {
			qtyTotal += l.qty
			p.subtotal += lineCost
			p.tax += l.tax
		}
	}

	p.subtotal, p.tax = round2(p.subtotal), round2(p.tax)
	computed := round2(p.subtotal + p.tax + req.Freight + req.OtherCharges)
	p.Summary.Lines, p.Summary.Quantity = len(p.Lines), qtyText(qtyTotal)
	p.Summary.Subtotal, p.Summary.Tax = money(p.subtotal), money(p.tax)
	p.Summary.Freight, p.Summary.OtherCharges, p.Summary.ComputedTotal = money(req.Freight), money(req.OtherCharges), money(computed)
	p.Summary.RoundOff = "0.00"
	if req.InvoiceTotal != nil {
		t := money(*req.InvoiceTotal)
		p.Summary.InvoiceTotal = &t
		diff := round2(*req.InvoiceTotal - computed)
		if math.Abs(diff) >= 1 {
			fail(fmt.Sprintf("the lines add up to ₹%s but the invoice total is ₹%s (₹%s apart) — a line is missing or mistyped", money(computed), t, money(math.Abs(diff))))
		} else {
			p.roundOff = diff
			p.Summary.RoundOff = money(diff)
		}
	}
	p.Ready = len(p.Errors) == 0
	for _, l := range p.Lines {
		if len(l.Errors) > 0 {
			p.Ready = false
		}
	}
	return p, nil
}

// ---------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------

func decodeImport(w http.ResponseWriter, r *http.Request) (importRequest, bool) {
	var req importRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxImportCSV+64<<10)).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return req, false
	}
	return req, true
}

// PreviewImport: POST /purchase/imports/preview — what the import would
// do, line by line, without changing anything.
func (h *Handler) PreviewImport(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	req, ok := decodeImport(w, r)
	if !ok {
		return
	}
	var p *importPreview
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		p, err = resolveImport(ctx, tx, req)
		return err
	})
	if err != nil {
		log.Printf("purchase: import preview: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not read the invoice")
		return
	}
	httpx.JSON(w, http.StatusOK, p)
}

type importResult struct {
	GRNID           string   `json:"grn_id"`
	GRNNumber       string   `json:"grn_number"`
	BillID          string   `json:"bill_id"`
	BillNumber      string   `json:"bill_number"`
	GrandTotal      string   `json:"grand_total"`
	StockAdded      int      `json:"stock_added"`      // lines received into existing products
	ProductsCreated int      `json:"products_created"` // new products
	BrandsCreated   []string `json:"brands_created"`
}

var errNeedsCatalogManage = errors.New("creating products needs catalog.manage")

// CommitImport: POST /purchase/imports/commit — books the invoice in one
// transaction: new brands and products, a GRN completed on the invoice
// date (stock in), and the bill (payable, GST input, round-off). Refuses
// with the preview when anything in it is an error. Retrying a commit that
// went through finds the invoice already imported: it can't double-book.
func (h *Handler) CommitImport(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	req, ok := decodeImport(w, r)
	if !ok {
		return
	}
	var (
		p       *importPreview
		res     importResult
		created []string
		failMsg string
	)
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if p, err = resolveImport(ctx, tx, req); err != nil || !p.Ready {
			return err
		}
		if p.Summary.Create > 0 {
			codes, err := authn.FetchPermissions(ctx, tx, claims.UserID)
			if err != nil {
				return err
			}
			if !authn.HasPermission(codes, "catalog.manage") {
				return errNeedsCatalogManage
			}
		}

		brandIDs := map[string]string{}
		for _, b := range p.NewBrands {
			var id string
			if err := tx.QueryRow(ctx, `INSERT INTO brands (id, merchant_id, name) VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1) RETURNING id::text`,
				b).Scan(&id); err != nil {
				return err
			}
			brandIDs[normKey(b)] = id
			res.BrandsCreated = append(res.BrandsCreated, b)
		}

		branchPrefix := req.BranchID
		if len(branchPrefix) > 8 {
			branchPrefix = branchPrefix[:8]
		}
		res.GRNNumber = fmt.Sprintf("GRN-%s-%d", branchPrefix, time.Now().UnixNano())
		if err := tx.QueryRow(ctx, `
			INSERT INTO goods_receipt_notes (id, merchant_id, branch_id, supplier_id, grn_number, status, freight_amount, other_charges, received_by, notes)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, 'draft', $4, $5, $6, $7)
			RETURNING id::text`,
			req.BranchID, req.SupplierID, res.GRNNumber, req.Freight, req.OtherCharges, claims.UserID,
			"Imported from supplier invoice "+p.InvoiceNumber,
		).Scan(&res.GRNID); err != nil {
			return err
		}

		createdByKey := map[string]string{}
		for _, l := range p.Lines {
			variantID := l.VariantID
			if l.Action == "create" {
				np := l.NewProduct
				if variantID = createdByKey[np.key]; variantID == "" {
					brandID := np.brandID
					if np.newBrand != "" {
						brandID = brandIDs[normKey(np.newBrand)]
					}
					_, variantID, err = catalog.CreateSimpleProduct(ctx, tx, catalog.SimpleProduct{
						Name: np.Name, SKU: np.SKU, CategoryID: np.categoryID, BrandID: brandID, TaxSlabID: np.taxSlabID,
						MRP: np.mrp, SellingPrice: np.selling, CostPrice: l.cost, Barcode: np.Barcode, GenerateBarcode: true,
					})
					if err != nil {
						if m := catalog.ProductErrorMessage(err); m != "" {
							failMsg = fmt.Sprintf("row %d (%s): %s", l.Row, l.ItemName, m)
						}
						return err
					}
					createdByKey[np.key] = variantID
					created = append(created, variantID)
					res.ProductsCreated++
				}
			} else {
				res.StockAdded++
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO goods_receipt_lines (id, grn_id, variant_id, quantity, unit_cost, line_total, batch_no, expiry_date)
				VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, NULLIF($6,''), NULLIF($7,'')::date)`,
				res.GRNID, variantID, l.qty, l.cost, round2(l.qty*l.cost), l.batch, l.expry); err != nil {
				return err
			}
		}
		var discard grnResponse
		if err := recalcGRNTotals(ctx, tx, res.GRNID, &discard); err != nil {
			return err
		}
		if err := completeGRNTx(ctx, tx, res.GRNID, claims.UserID, p.billDate); err != nil {
			return err
		}
		bill, err := createBillTx(ctx, tx, createBillRequest{
			GRNID: res.GRNID, SupplierInvoiceNumber: p.InvoiceNumber, TaxTotal: p.tax, DueDate: req.DueDate, RoundOff: p.roundOff,
		}, claims.UserID, p.billDate)
		if err != nil {
			return err
		}
		res.BillID, res.BillNumber, res.GrandTotal = bill.BillID, bill.BillNumber, bill.GrandTotal
		return audit.Log(ctx, tx, audit.Entry{EntityType: "purchase_invoice_import", EntityID: bill.BillID, Action: "create", PerformedBy: claims.UserID,
			After: map[string]any{"supplier_invoice": p.InvoiceNumber, "invoice_date": p.InvoiceDate, "grn": res.GRNNumber, "bill": res.BillNumber,
				"lines": len(p.Lines), "stock_added": res.StockAdded, "products_created": res.ProductsCreated, "brands_created": res.BrandsCreated,
				"subtotal": p.Summary.Subtotal, "tax": p.Summary.Tax, "round_off": p.Summary.RoundOff, "grand_total": bill.GrandTotal}})
	})
	switch {
	case err == nil && p != nil && !p.Ready:
		httpx.ErrorWithDetails(w, http.StatusBadRequest, "IMPORT_INVALID", "the invoice has errors — fix them and preview again", map[string]any{"preview": p})
	case errors.Is(err, errNeedsCatalogManage):
		httpx.Error(w, http.StatusForbidden, "FORBIDDEN", "this invoice creates new products, which needs the catalog.manage permission")
	case errors.Is(err, errBatchRequired):
		httpx.Error(w, http.StatusBadRequest, "BATCH_REQUIRED", errBatchRequired.Error())
	case failMsg != "":
		httpx.Error(w, http.StatusConflict, "IMPORT_CONFLICT", failMsg)
	case errors.Is(err, accounting.ErrPeriodLocked):
		httpx.Error(w, http.StatusConflict, "PERIOD_LOCKED", "the invoice date is in a locked accounting period")
	case err != nil:
		log.Printf("purchase: import commit: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not import the invoice — nothing was saved")
	default:
		h.indexNew(r.Context(), claims.TenantID, created)
		httpx.JSON(w, http.StatusCreated, res)
	}
}

// indexNew puts the products an import created into product search —
// best-effort after commit, the same posture as catalog's reindex.
func (h *Handler) indexNew(ctx context.Context, tenantID string, variantIDs []string) {
	if h.Search == nil || !h.Search.Enabled() {
		return
	}
	for _, id := range variantIDs {
		doc, err := search.FetchDocument(ctx, h.DB, tenantID, id)
		if err == nil {
			err = h.Search.IndexDocument(ctx, doc)
		}
		if err != nil {
			log.Printf("purchase: index imported variant %s: %v", id, err)
		}
	}
}

// ImportTemplate: GET /purchase/imports/template[?sample=1] — the CSV
// template, empty or with example rows showing an existing product being
// restocked and a new one being created.
func (h *Handler) ImportTemplate(w http.ResponseWriter, r *http.Request) {
	var buf bytes.Buffer
	buf.WriteString("\ufeff") // so Excel reads ₹ and accented names as UTF-8
	cw := csv.NewWriter(&buf)
	_ = cw.Write(templateColumns)
	name := "purchase-invoice-template.csv"
	if r.URL.Query().Get("sample") != "" {
		name = "purchase-invoice-sample.csv"
		for _, row := range [][]string{
			{"SC TEST RED BALL", "18", "260", "0", "290", "", "4680", "", "", "", "", "", "", "", ""},
			{"TYNOR HIGH PERFORMANCE INSOLE", "1", "347.46", "18", "631", "631", "410", "", "", "6307", "Supports & Braces", "TYNOR", "18", "", ""},
			{"IPL BRIDGE 999 PLAYING CARDS", "4", "26.67", "5", "45", "", "112", "", "8901234567890", "", "Indoor Games", "IPL", "5", "", ""},
		} {
			_ = cw.Write(row)
		}
	}
	cw.Flush()
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	httpx.Binary(w, http.StatusOK, "text/csv; charset=utf-8", buf.Bytes())
}
