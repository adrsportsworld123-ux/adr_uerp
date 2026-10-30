// Package reportbuilder is the custom report builder (FRD §15 "Custom
// Builder"; phased_roadmap.md Phase 16; migrations/047). Users pick a
// dataset, columns, aggregations, filters, sort and a date range; they never
// write SQL. Every field below is a fixed, reviewed SQL expression; a spec
// can only name fields by key, and every value travels as a parameter. The
// query runs inside the caller's WithTenant transaction with their branch
// scope set, so row-level security (Phase 15) decides which rows exist, the
// same as for every built-in report.
package reportbuilder

// Field types decide formatting, which filter operators apply and which
// aggregations make sense.
const (
	TText   = "text"
	TNumber = "number"
	TMoney  = "money"
	TDate   = "date"
	TBool   = "bool"
	// The merchant's business clock (migrations/064); Asia/Kolkata unless
	// the merchant set another.
	istDate   = "AT TIME ZONE app_merchant_tz())::date"
	istMonth  = "AT TIME ZONE app_merchant_tz(), 'YYYY-MM')"
	maxFields = 20
)

type Field struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Type  string `json:"type"`
	SQL   string `json:"-"`
}

// Aggregations allowed for a field type. count and count_distinct work on
// anything.
func (f Field) Aggs() []string {
	switch f.Type {
	case TNumber, TMoney:
		return []string{"sum", "avg", "min", "max", "count", "count_distinct"}
	case TDate:
		return []string{"min", "max", "count", "count_distinct"}
	default:
		return []string{"count", "count_distinct"}
	}
}

type Dataset struct {
	Key         string  `json:"key"`
	Label       string  `json:"label"`
	Description string  `json:"description"`
	Fields      []Field `json:"fields"`
	// HasDateRange: a date range is required (and limited) on this dataset.
	HasDateRange bool `json:"has_date_range"`
	// Statutory: a record the business must keep and show in full (sales,
	// purchases, payments, the ledger, the audit trail), so the plan's
	// report history (retention) doesn't limit it. subscriptions'
	// retentionRoutes keeps the same rule for the built-in screens.
	Statutory bool   `json:"statutory"`
	from      string // FROM and JOINs: fixed SQL
	where     string // the base condition: fixed SQL
	date      string // the date expression the range applies to
}

func (d Dataset) Field(key string) (Field, bool) {
	for _, f := range d.Fields {
		if f.Key == key {
			return f, true
		}
	}
	return Field{}, false
}

// Shared dimensions, so every dataset names them the same way.
var (
	fBranch   = Field{"branch", "Branch", TText, "b.name"}
	fTerminal = Field{"terminal", "Terminal", TText, "t.name"}
	fChannel  = Field{"channel", "Channel", TText, "so.channel"}
	fProduct  = Field{"product", "Product", TText, "p.name"}
	fSKU      = Field{"sku", "SKU", TText, "v.sku"}
	fCategory = Field{"category", "Category", TText, "cat.name"}
	fBrand    = Field{"brand", "Brand", TText, "br.name"}
	// fUnit: what the product is counted in (migrations/076).
	fUnit = Field{"unit", "Unit", TText, "un.symbol"}
)

const saleTime = "COALESCE(so.finalized_at, so.created_at)"

// Ageing of what is still owed (§3.77), the same buckets as a customer's
// credit card (customers.GetCustomerCredit): not yet due (or no due date),
// then 0–30, 31–60, 61–90 and over 90 days past the due date, counted on
// the merchant's own calendar.
const merchantToday = "(now() AT TIME ZONE app_merchant_tz())::date"

func ageingFields(due, owed string) []Field {
	days := merchantToday + " - " + due
	bucket := func(cond string) string { return "CASE WHEN " + cond + " THEN " + owed + " ELSE 0 END" }
	return []Field{
		{"due_date", "Due date", TDate, due},
		{"days_overdue", "Days overdue", TNumber, "GREATEST(" + days + ", 0)"},
		{"overdue", "Overdue", TBool, "COALESCE(" + days + " > 0, false)"},
		{"not_due", "Not yet due", TMoney, bucket(due + " IS NULL OR " + days + " < 0")},
		{"days_0_30", "0–30 days", TMoney, bucket(days + " BETWEEN 0 AND 30")},
		{"days_31_60", "31–60 days", TMoney, bucket(days + " BETWEEN 31 AND 60")},
		{"days_61_90", "61–90 days", TMoney, bucket(days + " BETWEEN 61 AND 90")},
		{"days_90_plus", "Over 90 days", TMoney, bucket(days + " > 90")},
	}
}

const productJoins = `
	JOIN product_variants v ON v.id = %s
	JOIN products p ON p.id = v.product_id
	LEFT JOIN categories cat ON cat.id = p.category_id
	LEFT JOIN brands br ON br.id = p.brand_id
	LEFT JOIN units un ON un.id = p.unit_id`

// Datasets is the allow-list. Adding a dataset or field is a code review,
// never a user action.
var Datasets = []Dataset{
	{
		Key: "sales", Label: "Sales", Description: "One row per sale (not open carts). Totals are what the customer was charged.",
		HasDateRange: true, Statutory: true, date: "(" + saleTime + " " + istDate,
		from: `sales_orders so
			JOIN branches b ON b.id = so.branch_id
			LEFT JOIN pos_terminals t ON t.id = so.pos_terminal_id
			LEFT JOIN customers c ON c.id = so.customer_id
			LEFT JOIN users u ON u.id = so.cashier_id`,
		where: "so.status NOT IN ('cart', 'held_voided')", // a dropped hold was never a sale (migrations/055)
		Fields: []Field{
			{"date", "Date", TDate, "(" + saleTime + " " + istDate},
			{"month", "Month", TText, "to_char(" + saleTime + " " + istMonth},
			{"weekday", "Weekday", TText, "trim(to_char(" + saleTime + " AT TIME ZONE app_merchant_tz(), 'Day'))"},
			{"hour", "Hour of day", TNumber, "extract(hour FROM " + saleTime + " AT TIME ZONE app_merchant_tz())::int"},
			{"order_number", "Sale number", TText, "so.order_number"},
			{"status", "Status", TText, "so.status"},
			fChannel, fBranch, fTerminal,
			{"cashier", "Cashier", TText, "u.name"},
			{"customer", "Customer", TText, "c.name"},
			{"customer_type", "Customer type", TText, "c.customer_type"},
			{"subtotal", "Subtotal", TMoney, "so.subtotal"},
			{"discount_total", "Discounts", TMoney, "so.discount_total"},
			{"tax_total", "Tax", TMoney, "so.tax_total"},
			// The tax by head, as the sale's invoice printed it: CGST +
			// SGST intra-state, IGST inter-state (migrations/060).
			{"cgst", "CGST", TMoney, saleHead("gst_cgst")},
			{"sgst", "SGST", TMoney, saleHead("gst_sgst")},
			{"igst", "IGST", TMoney, saleHead("gst_igst")},
			{"cess", "Cess", TMoney, "(SELECT COALESCE(sum(l.cess_amount), 0) FROM sales_order_lines l WHERE l.sales_order_id = so.id)"},
			{"place_of_supply", "Place of supply", TText, "so.place_of_supply"},
			{"inter_state", "Inter-state", TBool, "so.inter_state"},
			{"grand_total", "Total", TMoney, "so.grand_total"},
			// Checkout round-off waiver (signed) and the total after it,
			// what the customer paid (migrations/059).
			{"round_off", "Round off", TMoney, "so.round_off"},
			{"net_total", "Net total", TMoney, "(so.grand_total + so.round_off)"},
			{"credit_amount", "On credit", TMoney, "so.credit_amount"},
		},
	},
	{
		Key: "sales_items", Label: "Items sold", Description: "One row per sale line on completed sales (voided sales and open carts left out). Cost and margin use the item's current cost price.",
		HasDateRange: true, date: "(" + saleTime + " " + istDate,
		from: `sales_order_lines l
			JOIN sales_orders so ON so.id = l.sales_order_id
			JOIN branches b ON b.id = so.branch_id
			LEFT JOIN pos_terminals t ON t.id = so.pos_terminal_id` + sprintf(productJoins, "l.variant_id") + `
			LEFT JOIN tax_slabs ts ON ts.id = p.tax_slab_id`,
		where: "so.status IN ('finalized', 'partially_refunded', 'refunded')",
		Fields: []Field{
			{"date", "Date", TDate, "(" + saleTime + " " + istDate},
			{"month", "Month", TText, "to_char(" + saleTime + " " + istMonth},
			{"order_number", "Sale number", TText, "so.order_number"},
			fChannel, fBranch, fTerminal, fProduct, fSKU, fCategory, fBrand, fUnit,
			{"hsn", "HSN", TText, "p.hsn_code"},
			{"tax_rate", "GST rate %", TNumber, "COALESCE(l.tax_rate, ts.cgst_rate + ts.sgst_rate + ts.igst_rate, 0)"}, // as written (migrations/060)
			{"quantity", "Quantity", TNumber, "l.quantity"},
			{"unit_price", "Unit price", TMoney, "l.unit_price"},
			{"discount", "Discount", TMoney, "l.discount_amount"},
			{"taxable_value", "Taxable value", TMoney, "COALESCE(l.taxable_value, l.line_total - l.tax_amount)"},
			{"tax_amount", "Tax", TMoney, "l.tax_amount"},
			{"cgst", "CGST", TMoney, "gst_cgst(l.tax_amount, l.cess_amount, so.inter_state)"},
			{"sgst", "SGST", TMoney, "gst_sgst(l.tax_amount, l.cess_amount, so.inter_state)"},
			{"igst", "IGST", TMoney, "gst_igst(l.tax_amount, l.cess_amount, so.inter_state)"},
			{"cess", "Cess", TMoney, "l.cess_amount"},
			{"line_total", "Line total", TMoney, "l.line_total"},
			{"cost", "Cost (current)", TMoney, "round(l.quantity * v.cost_price, 2)"},
			{"margin", "Margin (current cost)", TMoney, "round(COALESCE(l.taxable_value, l.line_total - l.tax_amount) - l.quantity * v.cost_price, 2)"},
			{"open_price", "Open price", TBool, "l.open_price"},
		},
	},
	{
		Key: "payments", Label: "Payments", Description: "One row per payment taken on a sale.",
		HasDateRange: true, Statutory: true, date: "(pm.created_at " + istDate,
		from: `payments pm
			JOIN sales_orders so ON so.id = pm.sales_order_id
			JOIN branches b ON b.id = so.branch_id
			LEFT JOIN pos_terminals t ON t.id = so.pos_terminal_id`,
		where: "true",
		Fields: []Field{
			{"date", "Date", TDate, "(pm.created_at " + istDate},
			{"month", "Month", TText, "to_char(pm.created_at " + istMonth},
			{"method", "Method", TText, "pm.method"},
			{"status", "Status", TText, "pm.status"},
			{"order_number", "Sale number", TText, "so.order_number"},
			fChannel, fBranch, fTerminal,
			{"amount", "Amount", TMoney, "pm.amount"},
		},
	},
	{
		Key: "stock", Label: "Stock on hand", Description: "Current stock per item and branch, valued at cost and at selling price. No date range: it's now.",
		from: `stock_levels sl
			JOIN branches b ON b.id = sl.branch_id` + sprintf(productJoins, "sl.variant_id"),
		where: "true",
		Fields: []Field{
			fBranch, fProduct, fSKU, fCategory, fBrand, fUnit,
			{"item_status", "Item status", TText, "v.status"},
			{"on_hand", "On hand", TNumber, "sl.on_hand"},
			{"reserved", "Reserved", TNumber, "sl.reserved"},
			{"available", "Available", TNumber, "sl.on_hand - sl.reserved"},
			{"reorder_point", "Reorder point", TNumber, "sl.reorder_point"},
			{"below_reorder", "Below reorder point", TBool, "sl.reorder_point IS NOT NULL AND sl.on_hand - sl.reserved <= sl.reorder_point"},
			{"value_cost", "Value at cost", TMoney, "round(sl.on_hand * v.cost_price, 2)"},
			{"value_retail", "Value at selling price", TMoney, "round(sl.on_hand * v.selling_price, 2)"},
		},
	},
	{
		Key: "stock_movements", Label: "Stock movements", Description: "Every change to stock: sales, returns, purchases, transfers and adjustments.",
		HasDateRange: true, date: "(sm.created_at " + istDate,
		from: `stock_movements sm
			JOIN branches b ON b.id = sm.branch_id` + sprintf(productJoins, "sm.variant_id"),
		where: "true",
		Fields: []Field{
			{"date", "Date", TDate, "(sm.created_at " + istDate},
			{"month", "Month", TText, "to_char(sm.created_at " + istMonth},
			fBranch, fProduct, fSKU, fCategory, fBrand, fUnit,
			{"movement_type", "Movement", TText, "sm.movement_type"},
			{"reason", "Reason", TText, "sm.reason"},
			{"quantity", "Quantity change", TNumber, "sm.quantity_delta"},
			{"value_cost", "Value at cost", TMoney, "round(sm.quantity_delta * v.cost_price, 2)"},
		},
	},
	{
		Key: "products", Label: "Products", Description: "The catalogue: one row per item (variant) with its prices.",
		from:  "product_variants v JOIN products p ON p.id = v.product_id LEFT JOIN categories cat ON cat.id = p.category_id LEFT JOIN brands br ON br.id = p.brand_id LEFT JOIN tax_slabs ts ON ts.id = p.tax_slab_id LEFT JOIN units un ON un.id = p.unit_id",
		where: "true",
		Fields: []Field{
			fProduct, fSKU, fCategory, fBrand, fUnit,
			{"status", "Status", TText, "v.status"},
			{"hsn", "HSN", TText, "p.hsn_code"},
			{"tax_rate", "GST rate %", TNumber, "COALESCE(ts.cgst_rate + ts.sgst_rate + ts.igst_rate, 0)"},
			{"cost_price", "Cost price", TMoney, "v.cost_price"},
			{"mrp", "MRP", TMoney, "v.mrp"},
			{"selling_price", "Selling price", TMoney, "v.selling_price"},
			{"margin_pct", "Margin % (on selling price)", TNumber, "CASE WHEN v.selling_price > 0 THEN round((v.selling_price - v.cost_price) / v.selling_price * 100, 2) END"},
		},
	},
	{
		Key: "journal_lines", Label: "Journal lines", Description: "Posted ledger lines (manual and automatic), by account, with the customer or supplier each names.",
		HasDateRange: true, Statutory: true, date: "je.entry_date",
		from: `journal_lines jl
			JOIN journal_entries je ON je.id = jl.journal_entry_id
			JOIN chart_of_accounts a ON a.id = jl.account_id
			LEFT JOIN branches b ON b.id = je.branch_id`,
		where: "true",
		Fields: []Field{
			{"date", "Date", TDate, "je.entry_date"},
			{"month", "Month", TText, "to_char(je.entry_date, 'YYYY-MM')"},
			{"entry_number", "Entry", TText, "je.entry_number"},
			{"source_type", "Source", TText, "je.source_type"},
			{"description", "Description", TText, "je.description"},
			fBranch,
			{"account_code", "Account code", TText, "a.code"},
			{"account", "Account", TText, "a.name"},
			{"account_type", "Account type", TText, "a.account_type"},
			{"party_type", "Party type", TText, "jl.party_type"},
			{"party", "Party", TText, partyName},
			{"debit", "Debit", TMoney, "jl.debit"},
			{"credit", "Credit", TMoney, "jl.credit"},
			{"net", "Debit − credit", TMoney, "jl.debit - jl.credit"},
		},
	},
	{
		Key: "purchases", Label: "Purchase bills", Description: "One row per supplier bill, with what has been paid and what is still owed.",
		HasDateRange: true, Statutory: true, date: "pb.bill_date",
		from: `purchase_bills pb
			JOIN branches b ON b.id = pb.branch_id
			LEFT JOIN suppliers s ON s.id = pb.supplier_id`,
		where: "true",
		Fields: []Field{
			{"date", "Bill date", TDate, "pb.bill_date"},
			{"month", "Month", TText, "to_char(pb.bill_date, 'YYYY-MM')"},
			{"bill_number", "Bill number", TText, "pb.bill_number"},
			{"supplier_invoice", "Supplier invoice", TText, "pb.supplier_invoice_number"},
			{"supplier", "Supplier", TText, "s.name"},
			{"supplier_gstin", "Supplier GSTIN", TText, "s.gstin"},
			fBranch,
			{"status", "Status", TText, "pb.status"},
			{"due_date", "Due date", TDate, "pb.due_date"},
			{"overdue", "Overdue", TBool, "pb.status <> 'paid' AND pb.due_date < CURRENT_DATE"},
			{"subtotal", "Subtotal", TMoney, "pb.subtotal"},
			{"tax_total", "Tax", TMoney, "pb.tax_total"},
			// The supplier invoice's tax by head (migrations/060).
			{"cgst", "CGST", TMoney, "pb.cgst_amount"},
			{"sgst", "SGST", TMoney, "pb.sgst_amount"},
			{"igst", "IGST", TMoney, "pb.igst_amount"},
			{"cess", "Cess", TMoney, "pb.cess_amount"},
			{"inter_state", "Inter-state", TBool, "pb.inter_state"},
			{"freight", "Freight", TMoney, "pb.freight_amount"},
			{"other_charges", "Other charges", TMoney, "pb.other_charges"},
			{"grand_total", "Total", TMoney, "pb.grand_total"},
			{"amount_paid", "Paid", TMoney, "pb.amount_paid"},
			{"outstanding", "Outstanding", TMoney, "pb.grand_total - pb.amount_paid"},
		},
	},
	{
		Key: "purchase_items", Label: "Items received", Description: "One row per line of a completed goods receipt (GRN): what came in, from whom, at what cost.",
		HasDateRange: true, Statutory: true, date: "(g.completed_at " + istDate,
		from: `goods_receipt_lines gl
			JOIN goods_receipt_notes g ON g.id = gl.grn_id
			JOIN branches b ON b.id = g.branch_id
			LEFT JOIN suppliers s ON s.id = g.supplier_id` + sprintf(productJoins, "gl.variant_id"),
		where: "g.status = 'completed'",
		Fields: []Field{
			{"date", "Date received", TDate, "(g.completed_at " + istDate},
			{"month", "Month", TText, "to_char(g.completed_at " + istMonth},
			{"grn_number", "GRN", TText, "g.grn_number"},
			{"supplier", "Supplier", TText, "s.name"},
			fBranch, fProduct, fSKU, fCategory, fBrand, fUnit,
			{"quantity", "Quantity", TNumber, "gl.quantity"},
			{"unit_cost", "Unit cost", TMoney, "gl.unit_cost"},
			{"landed_unit_cost", "Landed unit cost", TMoney, "COALESCE(gl.landed_unit_cost, gl.unit_cost)"},
			{"line_total", "Line total", TMoney, "gl.line_total"},
			{"landed_total", "Landed total", TMoney, "round(gl.quantity * COALESCE(gl.landed_unit_cost, gl.unit_cost), 2)"},
		},
	},
	{
		// What customers still owe on credit sales, as of now (§3.77).
		Key: "receivables", Label: "Customer credit outstanding", Description: "One row per credit sale not yet fully paid, with what is still owed and how overdue it is. No date range: it's now.",
		from: `sales_orders so
			JOIN branches b ON b.id = so.branch_id
			JOIN customers c ON c.id = so.customer_id`,
		where: "so.status = 'finalized' AND so.credit_amount > so.credit_paid",
		Fields: append([]Field{
			{"customer", "Customer", TText, "c.name"},
			{"phone", "Phone", TText, "c.phone"},
			{"customer_gstin", "Customer GSTIN", TText, "c.gstin"},
			{"customer_type", "Customer type", TText, "c.customer_type"},
			{"payment_terms", "Payment terms", TText, "c.payment_terms"},
			{"credit_limit", "Credit limit", TMoney, "c.credit_limit"},
			{"credit_hold", "On credit hold", TBool, "c.credit_hold"},
			fBranch,
			{"invoice", "Sale number", TText, "so.order_number"},
			{"date", "Sale date", TDate, "(" + saleTime + " " + istDate},
			{"credit_amount", "On credit", TMoney, "so.credit_amount"},
			{"credit_paid", "Paid", TMoney, "so.credit_paid"},
			{"outstanding", "Outstanding", TMoney, "so.credit_amount - so.credit_paid"},
		}, ageingFields("so.due_date", "(so.credit_amount - so.credit_paid)")...),
	},
	{
		// What is still owed to suppliers, as of now (§3.77).
		Key: "payables", Label: "Supplier payables outstanding", Description: "One row per supplier bill not yet fully paid, with what is still owed and how overdue it is. No date range: it's now.",
		from: `purchase_bills pb
			JOIN branches b ON b.id = pb.branch_id
			LEFT JOIN suppliers s ON s.id = pb.supplier_id`,
		where: "pb.status <> 'paid' AND pb.grand_total > pb.amount_paid",
		Fields: append([]Field{
			{"supplier", "Supplier", TText, "s.name"},
			{"supplier_phone", "Phone", TText, "s.phone"},
			{"supplier_gstin", "Supplier GSTIN", TText, "s.gstin"},
			fBranch,
			{"bill_number", "Bill number", TText, "pb.bill_number"},
			{"supplier_invoice", "Supplier invoice", TText, "pb.supplier_invoice_number"},
			{"date", "Bill date", TDate, "pb.bill_date"},
			{"grand_total", "Total", TMoney, "pb.grand_total"},
			{"amount_paid", "Paid", TMoney, "pb.amount_paid"},
			{"outstanding", "Outstanding", TMoney, "pb.grand_total - pb.amount_paid"},
		}, ageingFields("pb.due_date", "(pb.grand_total - pb.amount_paid)")...),
	},
	{
		// The farm's log (§3.81): every posted event of every batch.
		Key: "farm_events", Label: "Farm log", Description: "One row per farm event — mortality, culls, feed, medicine, vaccination, weights, production, expenses — of every flock, herd and pond.",
		HasDateRange: true, date: "e.event_date",
		from: `farm_events e
			JOIN farm_batches fb ON fb.id = e.batch_id
			JOIN branches b ON b.id = e.branch_id
			LEFT JOIN farm_houses fh ON fh.id = fb.house_id
			LEFT JOIN product_variants v ON v.id = e.variant_id
			LEFT JOIN products p ON p.id = v.product_id
			LEFT JOIN units un ON un.id = p.unit_id
			LEFT JOIN users u ON u.id = e.created_by
			LEFT JOIN farm_animals fa ON fa.id = e.animal_id`,
		where: "e.status = 'posted'",
		Fields: []Field{
			{"date", "Date", TDate, "e.event_date"},
			{"month", "Month", TText, "to_char(e.event_date, 'YYYY-MM')"},
			fBranch,
			{"batch", "Batch", TText, "fb.batch_number"},
			{"species", "Species", TText, "fb.species"},
			{"purpose", "Purpose", TText, "fb.purpose"},
			{"house", "Shed / pond", TText, "fh.name"},
			{"event_type", "Event", TText, "initcap(e.event_type)"},
			{"head_count", "Heads", TNumber, "e.head_count"},
			{"product", "Item", TText, "p.name"},
			fUnit,
			{"quantity", "Quantity", TNumber, "e.quantity"},
			{"amount", "Cost / value", TMoney, "e.amount"},
			{"avg_weight", "Avg weight (kg)", TNumber, "e.avg_weight_kg"},
			{"cause", "Cause", TText, "e.cause"},
			{"vaccine", "Vaccine", TText, "e.vaccine"},
			{"notes", "Notes", TText, "e.notes"},
			{"entered_by", "Entered by", TText, "u.name"},
			// §3.82
			{"animal", "Animal", TText, "fa.tag"},
			{"produce_kind", "Produce", TText, "initcap(e.produce_kind)"},
			{"dose", "Dose", TText, "e.dose"},
			{"vet", "Vet", TText, "e.vet"},
			{"meat_until", "No meat until", TDate, "e.withdrawal_meat_until"},
			{"milk_until", "No milk until", TDate, "e.withdrawal_milk_until"},
			{"eggs_until", "No eggs until", TDate, "e.withdrawal_eggs_until"},
			{"override", "Withdrawal override", TText, "e.withdrawal_override"},
		},
	},
	{
		// Milk per animal per milking (§3.82).
		Key: "farm_milk", Label: "Milk by animal", Description: "One row per animal per milking: litres, with the herd and the day.",
		HasDateRange: true, date: "m.milked_on",
		from: `farm_milk_records m
			JOIN farm_events e ON e.id = m.event_id
			JOIN farm_animals fa ON fa.id = m.animal_id
			JOIN farm_batches fb ON fb.id = m.batch_id
			JOIN branches b ON b.id = m.branch_id`,
		where: "e.status = 'posted'",
		Fields: []Field{
			{"date", "Date", TDate, "m.milked_on"},
			{"month", "Month", TText, "to_char(m.milked_on, 'YYYY-MM')"},
			fBranch,
			{"batch", "Herd", TText, "fb.batch_number"},
			{"species", "Species", TText, "fb.species"},
			{"animal", "Animal", TText, "fa.tag"},
			{"animal_name", "Name", TText, "fa.name"},
			{"session", "Milking", TText, "upper(m.session)"},
			{"litres", "Litres", TNumber, "m.litres"},
		},
	},
	{
		// Each batch's figures (§3.81), as of now.
		Key: "farm_batches", Label: "Farm batches", Description: "One row per flock, herd or pond stocking with its counts, feed, cost and output so far. No date range: it's now.",
		from: `farm_batches fb
			JOIN branches b ON b.id = fb.branch_id
			LEFT JOIN farm_houses fh ON fh.id = fb.house_id
			CROSS JOIN LATERAL (
			  SELECT COALESCE(sum(e.head_count) FILTER (WHERE e.event_type = 'mortality'), 0) AS dead,
			         COALESCE(sum(e.head_count) FILTER (WHERE e.event_type = 'cull'), 0) AS culled,
			         COALESCE(sum(e.head_count) FILTER (WHERE e.event_type = 'production'), 0) AS harvested,
			         COALESCE(sum(e.quantity * COALESCE(un.factor, 1)) FILTER (WHERE e.event_type = 'feed' AND un.kind = 'weight'), 0) AS feed_kg,
			         COALESCE(sum(e.amount) FILTER (WHERE e.event_type = 'feed'), 0) AS feed_cost,
			         COALESCE(sum(e.amount) FILTER (WHERE e.event_type IN ('medicine', 'vaccination')), 0) AS health_cost,
			         COALESCE(sum(e.amount) FILTER (WHERE e.event_type IN ('placement', 'addition')), 0) AS stock_cost,
			         COALESCE(sum(e.head_count) FILTER (WHERE e.event_type = 'birth'), 0) AS born,
			         COALESCE(sum(e.head_count) FILTER (WHERE e.event_type IN ('addition', 'transfer_in')), 0) AS added,
			         COALESCE(sum(e.head_count) FILTER (WHERE e.event_type = 'transfer_out'), 0) AS moved_out,
			         COALESCE(sum(e.amount) FILTER (WHERE e.event_type = 'expense'), 0) AS other_cost,
			         COALESCE(sum(e.amount) FILTER (WHERE e.event_type = 'production'), 0) AS output_value
			  FROM farm_events e LEFT JOIN product_variants v ON v.id = e.variant_id LEFT JOIN products p ON p.id = v.product_id
			  LEFT JOIN units un ON un.id = p.unit_id
			  WHERE e.batch_id = fb.id AND e.status = 'posted') k`,
		where: "true",
		Fields: []Field{
			{"batch", "Batch", TText, "fb.batch_number"},
			fBranch,
			{"house", "Shed / pond", TText, "fh.name"},
			{"species", "Species", TText, "fb.species"},
			{"purpose", "Purpose", TText, "fb.purpose"},
			{"breed", "Breed", TText, "fb.breed"},
			{"placed_on", "Placed on", TDate, "fb.placed_on"},
			{"status", "Status", TText, "fb.status"},
			{"closed_on", "Closed on", TDate, "fb.closed_on"},
			{"age_days", "Age (days)", TNumber, "(COALESCE(fb.closed_on, (now() AT TIME ZONE app_merchant_tz())::date) - fb.placed_on) + fb.age_at_placement_days"},
			{"placed", "Placed", TNumber, "fb.initial_count"},
			{"dead", "Died", TNumber, "k.dead"},
			{"culled", "Culled", TNumber, "k.culled"},
			{"harvested", "Harvested", TNumber, "k.harvested"},
			{"born", "Born", TNumber, "k.born"},
			{"added", "Added", TNumber, "k.added"},
			{"alive", "Alive", TNumber, "fb.initial_count + k.born + k.added - k.dead - k.culled - k.harvested - k.moved_out"},
			{"mortality_pct", "Mortality %", TNumber, "round(k.dead * 100.0 / fb.initial_count, 2)"},
			{"feed_kg", "Feed (kg)", TNumber, "round(k.feed_kg, 3)"},
			{"feed_cost", "Feed cost", TMoney, "k.feed_cost"},
			{"health_cost", "Medicine & vaccines", TMoney, "k.health_cost"},
			{"stock_cost", "Animals placed", TMoney, "k.stock_cost"},
			{"other_cost", "Other costs", TMoney, "k.other_cost"},
			{"output_value", "Produce value", TMoney, "k.output_value"},
			{"net_cost", "Net cost", TMoney, "k.stock_cost + k.feed_cost + k.health_cost + k.other_cost - k.output_value"},
		},
	},
	{
		// Expenses recorded or imported (§3.77, §3.78). A cancelled one is
		// kept with its status; the library's reports leave it out.
		Key: "expenses", Label: "Expenses", Description: "One row per expense, with its head, branch, who paid it and whether it is still unpaid.",
		HasDateRange: true, date: "e.expense_date",
		from: `expenses e
			JOIN branches b ON b.id = e.branch_id
			JOIN expense_heads h ON h.id = e.head_id
			JOIN chart_of_accounts a ON a.id = e.account_id
			LEFT JOIN bank_accounts ba ON ba.id = e.bank_account_id
			LEFT JOIN partners pt ON pt.id = e.partner_id
			LEFT JOIN partners ppt ON ppt.id = e.paid_partner_id
			LEFT JOIN users u ON u.id = e.created_by`,
		where: "true",
		Fields: []Field{
			{"date", "Date", TDate, "e.expense_date"},
			{"month", "Month", TText, "to_char(e.expense_date, 'YYYY-MM')"},
			{"expense_number", "Expense number", TText, "e.expense_number"},
			{"head", "Head", TText, "h.name"},
			{"account", "Account", TText, "a.code || ' ' || a.name"},
			{"account_type", "Booked as", TText, "CASE a.account_type WHEN 'asset' THEN 'Asset' ELSE 'Expense' END"},
			fBranch,
			{"description", "Description", TText, "e.description"},
			{"vendor", "Vendor", TText, "e.vendor"},
			{"reference", "Reference", TText, "e.reference"},
			{"amount", "Amount", TMoney, "e.amount"},
			{"paid_by", "Paid by", TText, "CASE e.paid_by WHEN 'cash' THEN 'Cash' WHEN 'bank' THEN 'Bank' WHEN 'partner' THEN 'Partner' ELSE 'Unpaid' END"},
			{"bank_account", "Bank account", TText, "ba.account_name || ' (' || ba.bank_name || ')'"},
			{"partner", "Partner", TText, "COALESCE(pt.name, ppt.name)"},
			{"payment_status", "Payment", TText, "CASE WHEN e.paid_by <> 'unpaid' THEN 'Paid' WHEN e.paid_at IS NOT NULL THEN 'Paid later' ELSE 'Unpaid' END"},
			{"paid_on", "Paid on", TDate, "COALESCE(e.paid_at, CASE WHEN e.paid_by <> 'unpaid' THEN e.expense_date END)"},
			{"unpaid_amount", "Unpaid", TMoney, "CASE WHEN e.paid_by = 'unpaid' AND e.paid_at IS NULL THEN e.amount ELSE 0 END"},
			{"partner_amount", "Paid by partners", TMoney, "CASE WHEN e.partner_id IS NOT NULL OR e.paid_partner_id IS NOT NULL THEN e.amount ELSE 0 END"},
			{"imported", "Imported", TBool, "e.import_batch IS NOT NULL"},
			{"status", "Status", TText, "e.status"},
			{"entered_by", "Entered by", TText, "u.name"},
		},
	},
	{
		Key: "profit_loss", Label: "Profit and loss", Description: "Income and expenses from the ledger, with the cost of the items sold (at their current cost price, less items returned to stock). Amounts are signed: income positive, costs negative, so their total is the profit.",
		HasDateRange: true, date: "pl.day",
		from: `(SELECT je.entry_date AS day, je.branch_id,
			        CASE a.account_type WHEN 'income' THEN '1 Income' ELSE '3 Expenses' END AS section,
			        a.code || ' ' || a.name AS line,
			        jl.credit - jl.debit AS amount
			    FROM journal_lines jl
			    JOIN journal_entries je ON je.id = jl.journal_entry_id
			    JOIN chart_of_accounts a ON a.id = jl.account_id
			    WHERE je.status = 'posted' AND a.account_type IN ('income', 'expense')
			  UNION ALL
			  SELECT (` + saleTime + ` ` + istDate + `, so.branch_id, '2 Cost of goods sold', 'Cost of items sold', -round(l.quantity * v.cost_price, 2)
			    FROM sales_order_lines l
			    JOIN sales_orders so ON so.id = l.sales_order_id
			    JOIN product_variants v ON v.id = l.variant_id
			    WHERE so.status IN ('finalized', 'partially_refunded', 'refunded')
			  UNION ALL
			  SELECT (r.created_at ` + istDate + `, r.branch_id, '2 Cost of goods sold', 'Cost of items returned to stock', round(rl.quantity * v.cost_price, 2)
			    FROM sales_return_lines rl
			    JOIN sales_returns r ON r.id = rl.sales_return_id
			    JOIN product_variants v ON v.id = rl.variant_id
			    WHERE r.restocked) pl
			LEFT JOIN branches b ON b.id = pl.branch_id`,
		where: "true",
		Fields: []Field{
			{"date", "Date", TDate, "pl.day"},
			{"month", "Month", TText, "to_char(pl.day, 'YYYY-MM')"},
			{"section", "Section", TText, "pl.section"},
			{"line", "Line", TText, "pl.line"},
			fBranch,
			{"amount", "Amount", TMoney, "pl.amount"},
		},
	},
	{
		Key: "audit_log", Label: "Audit trail", Description: "Every recorded change: who changed what, when, and why (the tamper-evident audit log).",
		HasDateRange: true, Statutory: true, date: "(al.created_at " + istDate,
		from:  `audit_logs al LEFT JOIN users u ON u.id = al.performed_by`,
		where: "true",
		Fields: []Field{
			{"date", "Date", TDate, "(al.created_at " + istDate},
			{"time", "Time", TText, "to_char(al.created_at AT TIME ZONE app_merchant_tz(), 'HH24:MI:SS')"},
			{"entity_type", "Record type", TText, "al.entity_type"},
			{"entity_id", "Record id", TText, "al.entity_id::text"},
			{"action", "Action", TText, "al.action"},
			{"performed_by", "Done by", TText, "u.name"},
			{"reason", "Reason", TText, "al.reason"},
			{"changes", "Changes", TText, "left(COALESCE(al.after_value, al.before_value)::text, 500)"},
		},
	},
	{
		Key: "sales_audit", Label: "Sales audit", Description: "The sales events that need watching: voided sales, released carts, returns, manual discounts and open-price items, with who did and who approved each.",
		HasDateRange: true, Statutory: true, date: "(ev.at " + istDate,
		from: `(SELECT 'void' AS event, COALESCE(so.voided_at, so.finalized_at, so.created_at) AS at, so.order_number, so.branch_id, so.pos_terminal_id,
			         u.name AS done_by, NULL::text AS approved_by, so.void_reason AS reason, so.grand_total AS amount
			    FROM sales_orders so LEFT JOIN users u ON u.id = so.voided_by WHERE so.status = 'voided'
			  UNION ALL
			  SELECT 'released', COALESCE(so.voided_at, so.created_at), so.order_number, so.branch_id, so.pos_terminal_id, u.name, NULL, so.void_reason, so.grand_total
			    FROM sales_orders so LEFT JOIN users u ON u.id = so.voided_by WHERE so.status = 'held_voided'
			  UNION ALL
			  SELECT 'return', r.created_at, so.order_number, r.branch_id, r.pos_terminal_id, pu.name, au.name, r.reason, r.refund_amount
			    FROM sales_returns r JOIN sales_orders so ON so.id = r.sales_order_id
			    LEFT JOIN users pu ON pu.id = r.processed_by LEFT JOIN users au ON au.id = r.approved_by
			  UNION ALL
			  SELECT 'manual_discount', d.created_at, so.order_number, so.branch_id, so.pos_terminal_id, pu.name, au.name, d.reason, d.discount_amount
			    FROM sales_order_discounts d JOIN sales_orders so ON so.id = d.sales_order_id
			    LEFT JOIN users pu ON pu.id = d.applied_by LEFT JOIN users au ON au.id = d.authorized_by
			    WHERE d.type = 'manual' AND so.status IN ('finalized', 'partially_refunded', 'refunded', 'voided')
			  UNION ALL
			  SELECT 'open_price', ` + saleTime + `, so.order_number, so.branch_id, so.pos_terminal_id, cu.name, au.name, 'Open price: ' || v.sku,
			         round(l.unit_price * l.quantity, 2)
			    FROM sales_order_lines l JOIN sales_orders so ON so.id = l.sales_order_id JOIN product_variants v ON v.id = l.variant_id
			    LEFT JOIN users cu ON cu.id = so.cashier_id LEFT JOIN users au ON au.id = l.price_approved_by
			    WHERE l.open_price AND so.status IN ('finalized', 'partially_refunded', 'refunded', 'voided')) ev
			JOIN branches b ON b.id = ev.branch_id
			LEFT JOIN pos_terminals t ON t.id = ev.pos_terminal_id`,
		where: "true",
		Fields: []Field{
			{"date", "Date", TDate, "(ev.at " + istDate},
			{"time", "Time", TText, "to_char(ev.at AT TIME ZONE app_merchant_tz(), 'HH24:MI')"},
			{"event", "Event", TText, `CASE ev.event WHEN 'void' THEN 'Sale voided' WHEN 'released' THEN 'Cart released' WHEN 'return' THEN 'Return'
				WHEN 'manual_discount' THEN 'Manual discount' WHEN 'open_price' THEN 'Open price' END`},
			{"order_number", "Sale number", TText, "ev.order_number"},
			fBranch, fTerminal,
			{"done_by", "Done by", TText, "ev.done_by"},
			{"approved_by", "Approved by", TText, "ev.approved_by"},
			{"reason", "Reason", TText, "ev.reason"},
			{"amount", "Amount", TMoney, "ev.amount"},
		},
	},
}

// partyName names a journal line's customer or supplier (one deleted since
// reads null: its table's policy hides it).
const partyName = `CASE jl.party_type
	WHEN 'customer' THEN (SELECT c.name FROM customers c WHERE c.id = jl.party_id)
	WHEN 'supplier' THEN (SELECT su.name FROM suppliers su WHERE su.id = jl.party_id) END`

func FindDataset(key string) (Dataset, bool) {
	for _, d := range Datasets {
		if d.Key == key {
			return d, true
		}
	}
	return Dataset{}, false
}

// saleHead is one tax head of a sale, summed over its lines with the
// split its invoice printed (gst_cgst / gst_sgst / gst_igst, migrations/060).
func saleHead(fn string) string {
	return "(SELECT COALESCE(sum(" + fn + "(l.tax_amount, l.cess_amount, so.inter_state)), 0) FROM sales_order_lines l WHERE l.sales_order_id = so.id)"
}
