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
//
// Against a purchase order (purchase_order_id, §3.74) the GRN and bill are
// the PO's, and each line is matched to the PO line for its product. A
// name that only nearly matches is never taken automatically ("GRIP" and
// "TOWEL GRIP" are different items): the ordered items it resembles are
// suggested first, marked on_po. A delivery that differs from the PO is
// received as it is:
//
//   - less than ordered leaves the rest pending, or close_short closes the
//     PO short with a reason;
//   - more than pending, and items not on the PO at all (existing products
//     restocked, new ones created), need accept_variances: the GRN is then
//     marked po_variance_accepted, the PO line takes what was pending and
//     the rest is recorded as its excess_qty.
//
// Without a purchase order the import is unchanged.

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
	// PurchaseOrder: the PO's id or number (§3.74). Empty = a direct
	// import, as before. Supplier and branch default to the PO's.
	PurchaseOrder   string `json:"purchase_order_id"`
	AcceptVariances bool   `json:"accept_variances"` // receive items not on the PO and more than pending
	CloseShort      bool   `json:"close_short"`      // close the PO short if something is still pending after this invoice
	CloseReason     string `json:"close_reason"`
}

type suggestion struct {
	VariantID string `json:"variant_id"`
	Name      string `json:"name"`
	SKU       string `json:"sku"`
	MRP       string `json:"mrp"`
	Score     int    `json:"score"` // 0-100 word overlap
	OnPO      bool   `json:"on_po,omitempty"`
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
	Row         int          `json:"row"`
	ItemName    string       `json:"item_name"`
	Quantity    string       `json:"quantity"`
	UnitCost    string       `json:"unit_cost"`
	GSTRate     string       `json:"gst_rate"`
	LineCost    string       `json:"line_cost"`
	LineTax     string       `json:"line_tax"`
	Action      string       `json:"action"` // add_stock | create
	MatchedBy   string       `json:"matched_by,omitempty"`
	VariantID   string       `json:"variant_id,omitempty"`
	ProductName string       `json:"product_name,omitempty"`
	SKU         string       `json:"sku,omitempty"`
	CurrentMRP  string       `json:"current_mrp,omitempty"`
	OnHand      string       `json:"on_hand,omitempty"` // at the receiving branch, before this invoice
	NewProduct  *newProduct  `json:"new_product,omitempty"`
	Suggestions []suggestion `json:"suggestions"`
	Warnings    []string     `json:"warnings"`
	Errors      []string     `json:"errors"`
	// Against a PO: po_status is on_po (within what is pending), over
	// (beyond it) or unplanned (not on the PO); po_line_id the PO line it
	// receives.
	POLineID     string `json:"po_line_id,omitempty"`
	POStatus     string `json:"po_status,omitempty"`
	POOrdered    string `json:"po_ordered,omitempty"`
	POPending    string `json:"po_pending,omitempty"`
	qty, cost    float64
	tax          float64
	batch, expry string
}

// poImportLine: one PO line and what this invoice does to it.
type poImportLine struct {
	LineID       string `json:"po_line_id"`
	VariantID    string `json:"variant_id"`
	ProductName  string `json:"product_name"`
	SKU          string `json:"sku"`
	UnitCost     string `json:"unit_cost"`
	Ordered      string `json:"ordered"`
	Received     string `json:"received"` // before this invoice
	Pending      string `json:"pending"`  // before this invoice, net of other open draft GRNs
	Invoiced     string `json:"invoiced"`
	Excess       string `json:"excess"`
	PendingAfter string `json:"pending_after"`
	Status       string `json:"status"` // full | short | over | not_delivered | done (nothing was pending)

	ordered, received, drafts, pending, invoiced, unitCost float64
}

type poImportPreview struct {
	POID       string          `json:"po_id"`
	PONumber   string          `json:"po_number"`
	Status     string          `json:"status"`
	Lines      []*poImportLine `json:"lines"`
	Unplanned  int             `json:"unplanned"` // invoice lines not on the PO
	Over       int             `json:"over"`      // invoice lines beyond what is pending
	Short      int             `json:"short"`     // PO lines still pending after this invoice
	Variances  bool            `json:"variances"` // unplanned or over: needs accept_variances
	CloseShort bool            `json:"close_short"`
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
	// PO: the purchase order received against, when one was given.
	PO *poImportPreview `json:"purchase_order,omitempty"`

	roundOff, subtotal, tax float64
	billDate                *string
	supplierID, branchID    string
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
	byID       map[string]*knownVariant
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
	c.byID = map[string]*knownVariant{}
	for _, v := range c.variants {
		c.byID[v.id] = v
	}
	if err := eachRow(ctx, tx, `SELECT variant_id::text, code FROM barcodes`, func(r pgx.Rows) error {
		var vid, code string
		if err := r.Scan(&vid, &code); err != nil {
			return err
		}
		if v := c.byID[vid]; v != nil {
			c.byBarcode[strings.TrimSpace(code)] = v
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := eachRow(ctx, tx, `SELECT id::text, name, COALESCE(path, '/' || name) FROM categories WHERE status = 'active'`, func(r pgx.Rows) error {
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
		if s := overlap(w, v.words); s >= 50 {
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

// overlap: the share of words two names have in common, 0-100.
func overlap(a, b map[string]bool) int {
	inter := 0
	for k := range a {
		if b[k] {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if inter == 0 || union == 0 {
		return 0
	}
	return inter * 100 / union
}

// loadImportPO reads the purchase order an import receives against, under
// its row lock (so pending can't change between this and the GRN's
// completion in the same transaction), with each line's pending quantity
// net of other open draft GRNs.
func loadImportPO(ctx context.Context, tx pgx.Tx, ref string) (po *poImportPreview, supplierID, branchID string, err error) {
	po = &poImportPreview{Lines: []*poImportLine{}}
	if err = tx.QueryRow(ctx, `
		SELECT id::text, po_number, status, supplier_id::text, branch_id::text FROM purchase_orders
		WHERE id::text = $1 OR po_number = $1 ORDER BY (id::text = $1) DESC LIMIT 1 FOR UPDATE`, strings.TrimSpace(ref),
	).Scan(&po.POID, &po.PONumber, &po.Status, &supplierID, &branchID); err != nil {
		return nil, "", "", err
	}
	rows, err := tx.Query(ctx, `
		SELECT l.id::text, l.variant_id::text, p.name, pv.sku, l.unit_cost::float8, l.quantity::float8, l.received_qty::float8,
		       COALESCE((SELECT SUM(gl.quantity) FROM goods_receipt_lines gl JOIN goods_receipt_notes g ON g.id = gl.grn_id
		                 WHERE gl.po_line_id = l.id AND g.status = 'draft'), 0)::float8
		FROM purchase_order_lines l
		JOIN product_variants pv ON pv.id = l.variant_id
		JOIN products p ON p.id = pv.product_id
		WHERE l.po_id::text = $1
		ORDER BY l.created_at, l.id`, po.POID)
	if err != nil {
		return nil, "", "", err
	}
	defer rows.Close()
	for rows.Next() {
		l := &poImportLine{}
		if err := rows.Scan(&l.LineID, &l.VariantID, &l.ProductName, &l.SKU, &l.unitCost, &l.ordered, &l.received, &l.drafts); err != nil {
			return nil, "", "", err
		}
		l.pending = math.Max(0, l.ordered-l.received-l.drafts)
		po.Lines = append(po.Lines, l)
	}
	return po, supplierID, branchID, rows.Err()
}

// poLineFor: the PO line an invoice line for this variant receives: the
// first with something still pending after the invoice's earlier lines,
// else the last one (an over-receipt). nil = not on the PO.
func (po *poImportPreview) poLineFor(variantID string) *poImportLine {
	var last *poImportLine
	for _, l := range po.Lines {
		if l.VariantID != variantID {
			continue
		}
		if l.pending-l.invoiced > 0 {
			return l
		}
		last = l
	}
	return last
}

// suggestFirst: the ordered items an unmatched line resembles (half its
// words or more), ahead of the catalogue's other suggestions and marked
// on_po. Still only suggestions: the user picks.
func (po *poImportPreview) suggestFirst(item string, others []suggestion, cat *catalogue) []suggestion {
	w := words(item)
	var first []suggestion
	seen := map[string]bool{}
	for _, pl := range po.Lines {
		v := cat.byID[pl.VariantID]
		if v == nil || seen[v.id] {
			continue
		}
		if s := overlap(w, v.words); s >= 50 {
			seen[v.id] = true
			first = append(first, suggestion{VariantID: v.id, Name: strings.TrimSpace(v.name + " " + v.combo), SKU: v.sku, MRP: money(v.mrp), Score: s, OnPO: true})
		}
	}
	sort.SliceStable(first, func(i, j int) bool { return first[i].Score > first[j].Score })
	out := first
	for _, s := range others {
		if !seen[s.VariantID] && len(out) < 3+len(first) {
			out = append(out, s)
		}
	}
	if out == nil {
		out = []suggestion{}
	}
	return out
}

// ---------------------------------------------------------------------
// Resolution — shared by preview and commit
// ---------------------------------------------------------------------

func resolveImport(ctx context.Context, tx pgx.Tx, req importRequest) (*importPreview, error) {
	p := &importPreview{InvoiceNumber: strings.TrimSpace(req.InvoiceNumber), InvoiceDate: req.InvoiceDate, Lines: []*importLine{}, Errors: []string{}, NewBrands: []string{}}
	fail := func(msg string) { p.Errors = append(p.Errors, msg) }

	if strings.TrimSpace(req.PurchaseOrder) != "" {
		po, poSupplier, poBranch, err := loadImportPO(ctx, tx, req.PurchaseOrder)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			fail("purchase order " + strings.TrimSpace(req.PurchaseOrder) + " was not found")
		case err != nil:
			return nil, err
		default:
			p.PO = po
			if req.SupplierID == "" {
				req.SupplierID = poSupplier
			} else if req.SupplierID != poSupplier {
				fail("purchase order " + po.PONumber + " is with a different supplier")
			}
			if req.BranchID == "" {
				req.BranchID = poBranch
			} else if req.BranchID != poBranch {
				fail("purchase order " + po.PONumber + " is for a different branch — receive it at the branch it was raised for")
			}
			if po.Status != "open" && po.Status != "partially_received" {
				fail(fmt.Sprintf("purchase order %s is %s — only an issued PO still waiting for goods can be received against", po.PONumber, strings.ReplaceAll(po.Status, "_", " ")))
			}
		}
	}
	p.supplierID, p.branchID = req.SupplierID, req.BranchID

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
			if p.PO != nil && len(l.Errors) == 0 {
				if pl := p.PO.poLineFor(hit.id); pl != nil {
					l.POLineID, l.POOrdered, l.POPending = pl.LineID, qtyText(pl.ordered), qtyText(math.Max(0, pl.pending-pl.invoiced))
					if pl.invoiced+l.qty > pl.pending+1e-9 {
						l.POStatus = "over"
						p.PO.Over++
						l.Warnings = append(l.Warnings, fmt.Sprintf("%s more than is pending on the purchase order (ordered %s, pending %s)",
							qtyText(round3(pl.invoiced+l.qty-math.Max(pl.pending, pl.invoiced))), qtyText(pl.ordered), l.POPending))
					} else {
						l.POStatus = "on_po"
					}
					if pl.unitCost > 0 && math.Abs(pl.unitCost-l.cost) >= 0.01 {
						l.Warnings = append(l.Warnings, fmt.Sprintf("the rate ₹%s differs from the purchase order's ₹%s", money(l.cost), money(pl.unitCost)))
					}
					pl.invoiced += l.qty
				}
			}
			p.Summary.AddStock++
		} else if len(l.Errors) == 0 || choice == "new" {
			l.Action = "create"
			if choice != "new" {
				l.Suggestions = cat.suggest(l.ItemName)
				if p.PO != nil {
					l.Suggestions = p.PO.suggestFirst(l.ItemName, l.Suggestions, cat)
				}
				if len(l.Suggestions) > 0 && l.Suggestions[0].OnPO {
					l.Warnings = append(l.Warnings, "it looks like "+l.Suggestions[0].Name+" on the purchase order — pick it if this is the same item, or keep it as new")
				} else if len(l.Suggestions) > 0 {
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

	if p.PO != nil {
		resolvePOSummary(ctx, tx, p, req, fail)
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

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// resolvePOSummary: after the lines, against a PO: the lines not on it,
// what each PO line is left with, and whether the invoice can be booked
// (differences need accept_variances; close_short needs a reason and no
// other draft GRN open against the PO).
func resolvePOSummary(ctx context.Context, tx pgx.Tx, p *importPreview, req importRequest, fail func(string)) {
	po := p.PO
	for _, l := range p.Lines {
		if len(l.Errors) == 0 && l.POLineID == "" && l.Action != "" {
			l.POStatus = "unplanned"
			po.Unplanned++
			l.Warnings = append(l.Warnings, "not on purchase order "+po.PONumber+" — received as an extra item")
		}
	}
	for _, pl := range po.Lines {
		excess := math.Max(0, pl.invoiced-pl.pending)
		after := math.Max(0, pl.pending-pl.invoiced)
		pl.Ordered, pl.Received, pl.Pending, pl.Invoiced = qtyText(pl.ordered), qtyText(pl.received), qtyText(pl.pending), qtyText(round3(pl.invoiced))
		pl.Excess, pl.PendingAfter, pl.UnitCost = qtyText(round3(excess)), qtyText(round3(after)), money(pl.unitCost)
		switch {
		case pl.pending == 0 && pl.invoiced == 0:
			pl.Status = "done"
		case pl.invoiced == 0:
			pl.Status = "not_delivered"
		case excess > 0:
			pl.Status = "over"
		case after > 0:
			pl.Status = "short"
		default:
			pl.Status = "full"
		}
		if after > 0 {
			po.Short++
		}
	}
	po.Variances = po.Unplanned > 0 || po.Over > 0
	if po.Variances && !req.AcceptVariances {
		fail(fmt.Sprintf("the invoice differs from purchase order %s (%d item(s) not on it, %d over the pending quantity) — accept the differences to receive them as invoiced",
			po.PONumber, po.Unplanned, po.Over))
	}
	if req.CloseShort && po.Short > 0 {
		po.CloseShort = true
		if strings.TrimSpace(req.CloseReason) == "" {
			fail("give a reason for closing the purchase order short")
		}
		var drafts bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM goods_receipt_notes WHERE purchase_order_id::text = $1 AND status = 'draft')`, po.POID).Scan(&drafts); err == nil && drafts {
			fail("a draft GRN is open against purchase order " + po.PONumber + " — complete or delete it before closing the PO short")
		}
	}
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
	// Against a PO: its number and status afterwards.
	PONumber string `json:"po_number,omitempty"`
	POStatus string `json:"po_status,omitempty"`
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

		branchPrefix := p.branchID
		if len(branchPrefix) > 8 {
			branchPrefix = branchPrefix[:8]
		}
		res.GRNNumber = fmt.Sprintf("GRN-%s-%d", branchPrefix, time.Now().UnixNano())
		notes, poID, variance := "Imported from supplier invoice "+p.InvoiceNumber, "", false
		if p.PO != nil {
			notes += " against " + p.PO.PONumber
			poID, variance = p.PO.POID, p.PO.Variances
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO goods_receipt_notes (id, merchant_id, branch_id, supplier_id, grn_number, status, freight_amount, other_charges, received_by, notes,
			                                 purchase_order_id, po_variance_accepted)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, 'draft', $4, $5, $6, $7, NULLIF($8,'')::uuid, $9)
			RETURNING id::text`,
			p.branchID, p.supplierID, res.GRNNumber, req.Freight, req.OtherCharges, claims.UserID, notes, poID, variance,
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
				INSERT INTO goods_receipt_lines (id, grn_id, variant_id, quantity, unit_cost, line_total, batch_no, expiry_date, po_line_id, created_at)
				VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, NULLIF($6,''), NULLIF($7,'')::date, NULLIF($8,'')::uuid, clock_timestamp())`,
				res.GRNID, variantID, l.qty, l.cost, round2(l.qty*l.cost), l.batch, l.expry, l.POLineID); err != nil {
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
		after := map[string]any{"supplier_invoice": p.InvoiceNumber, "invoice_date": p.InvoiceDate, "grn": res.GRNNumber, "bill": res.BillNumber,
			"lines": len(p.Lines), "stock_added": res.StockAdded, "products_created": res.ProductsCreated, "brands_created": res.BrandsCreated,
			"subtotal": p.Summary.Subtotal, "tax": p.Summary.Tax, "round_off": p.Summary.RoundOff, "grand_total": bill.GrandTotal}
		if p.PO != nil {
			if p.PO.CloseShort {
				if _, err := tx.Exec(ctx, `
					UPDATE purchase_orders SET status = 'closed', close_reason = $2, updated_at = now()
					WHERE id::text = $1 AND status = 'partially_received'`, p.PO.POID, strings.TrimSpace(req.CloseReason)); err != nil {
					return err
				}
			}
			if err := tx.QueryRow(ctx, `SELECT status FROM purchase_orders WHERE id::text = $1`, p.PO.POID).Scan(&res.POStatus); err != nil {
				return err
			}
			res.PONumber = p.PO.PONumber
			after["purchase_order"], after["po_status"] = p.PO.PONumber, res.POStatus
			after["unplanned_lines"], after["over_lines"], after["variances_accepted"] = p.PO.Unplanned, p.PO.Over, p.PO.Variances
			if p.PO.CloseShort {
				after["closed_short"] = strings.TrimSpace(req.CloseReason)
			}
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "purchase_invoice_import", EntityID: bill.BillID, Action: "create", PerformedBy: claims.UserID,
			After: after})
	})
	switch {
	case err == nil && p != nil && !p.Ready:
		httpx.ErrorWithDetails(w, http.StatusBadRequest, "IMPORT_INVALID", "the invoice has errors — fix them and preview again", map[string]any{"preview": p})
	case errors.Is(err, errNeedsCatalogManage):
		httpx.Error(w, http.StatusForbidden, "FORBIDDEN", "this invoice creates new products, which needs the catalog.manage permission")
	case errors.Is(err, errBatchRequired):
		httpx.Error(w, http.StatusBadRequest, "BATCH_REQUIRED", errBatchRequired.Error())
	case isPOError(err):
		respondPO(w, err, 0, nil, "import the invoice")
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
