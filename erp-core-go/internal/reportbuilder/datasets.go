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
	TText     = "text"
	TNumber   = "number"
	TMoney    = "money"
	TDate     = "date"
	TBool     = "bool"
	istDate   = "AT TIME ZONE 'Asia/Kolkata')::date"
	istMonth  = "AT TIME ZONE 'Asia/Kolkata', 'YYYY-MM')"
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
	HasDateRange bool   `json:"has_date_range"`
	from         string // FROM and JOINs: fixed SQL
	where        string // the base condition: fixed SQL
	date         string // the date expression the range applies to
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
)

const saleTime = "COALESCE(so.finalized_at, so.created_at)"

const productJoins = `
	JOIN product_variants v ON v.id = %s
	JOIN products p ON p.id = v.product_id
	LEFT JOIN categories cat ON cat.id = p.category_id
	LEFT JOIN brands br ON br.id = p.brand_id`

// Datasets is the allow-list. Adding a dataset or field is a code review,
// never a user action.
var Datasets = []Dataset{
	{
		Key: "sales", Label: "Sales", Description: "One row per sale (not open carts). Totals are what the customer was charged.",
		HasDateRange: true, date: "(" + saleTime + " " + istDate,
		from: `sales_orders so
			JOIN branches b ON b.id = so.branch_id
			LEFT JOIN pos_terminals t ON t.id = so.pos_terminal_id
			LEFT JOIN customers c ON c.id = so.customer_id
			LEFT JOIN users u ON u.id = so.cashier_id`,
		where: "so.status NOT IN ('cart', 'held_voided')", // a dropped hold was never a sale (migrations/055)
		Fields: []Field{
			{"date", "Date", TDate, "(" + saleTime + " " + istDate},
			{"month", "Month", TText, "to_char(" + saleTime + " " + istMonth},
			{"weekday", "Weekday", TText, "trim(to_char(" + saleTime + " AT TIME ZONE 'Asia/Kolkata', 'Day'))"},
			{"hour", "Hour of day", TNumber, "extract(hour FROM " + saleTime + " AT TIME ZONE 'Asia/Kolkata')::int"},
			{"order_number", "Sale number", TText, "so.order_number"},
			{"status", "Status", TText, "so.status"},
			fChannel, fBranch, fTerminal,
			{"cashier", "Cashier", TText, "u.name"},
			{"customer", "Customer", TText, "c.name"},
			{"customer_type", "Customer type", TText, "c.customer_type"},
			{"subtotal", "Subtotal", TMoney, "so.subtotal"},
			{"discount_total", "Discounts", TMoney, "so.discount_total"},
			{"tax_total", "Tax", TMoney, "so.tax_total"},
			{"grand_total", "Total", TMoney, "so.grand_total"},
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
			fChannel, fBranch, fTerminal, fProduct, fSKU, fCategory, fBrand,
			{"hsn", "HSN", TText, "p.hsn_code"},
			{"tax_rate", "GST rate %", TNumber, "COALESCE(ts.cgst_rate + ts.sgst_rate + ts.igst_rate, 0)"},
			{"quantity", "Quantity", TNumber, "l.quantity"},
			{"unit_price", "Unit price", TMoney, "l.unit_price"},
			{"discount", "Discount", TMoney, "l.discount_amount"},
			{"taxable_value", "Taxable value", TMoney, "COALESCE(l.taxable_value, l.line_total - l.tax_amount)"},
			{"tax_amount", "Tax", TMoney, "l.tax_amount"},
			{"line_total", "Line total", TMoney, "l.line_total"},
			{"cost", "Cost (current)", TMoney, "round(l.quantity * v.cost_price, 2)"},
			{"margin", "Margin (current cost)", TMoney, "round(COALESCE(l.taxable_value, l.line_total - l.tax_amount) - l.quantity * v.cost_price, 2)"},
			{"open_price", "Open price", TBool, "l.open_price"},
		},
	},
	{
		Key: "payments", Label: "Payments", Description: "One row per payment taken on a sale.",
		HasDateRange: true, date: "(pm.created_at " + istDate,
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
			fBranch, fProduct, fSKU, fCategory, fBrand,
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
			fBranch, fProduct, fSKU, fCategory, fBrand,
			{"movement_type", "Movement", TText, "sm.movement_type"},
			{"reason", "Reason", TText, "sm.reason"},
			{"quantity", "Quantity change", TNumber, "sm.quantity_delta"},
			{"value_cost", "Value at cost", TMoney, "round(sm.quantity_delta * v.cost_price, 2)"},
		},
	},
	{
		Key: "products", Label: "Products", Description: "The catalogue: one row per item (variant) with its prices.",
		from:  "product_variants v JOIN products p ON p.id = v.product_id LEFT JOIN categories cat ON cat.id = p.category_id LEFT JOIN brands br ON br.id = p.brand_id LEFT JOIN tax_slabs ts ON ts.id = p.tax_slab_id",
		where: "true",
		Fields: []Field{
			fProduct, fSKU, fCategory, fBrand,
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
		Key: "journal_lines", Label: "Journal lines", Description: "Posted ledger lines (manual and automatic), by account.",
		HasDateRange: true, date: "je.entry_date",
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
			{"debit", "Debit", TMoney, "jl.debit"},
			{"credit", "Credit", TMoney, "jl.credit"},
			{"net", "Debit − credit", TMoney, "jl.debit - jl.credit"},
		},
	},
}

func FindDataset(key string) (Dataset, bool) {
	for _, d := range Datasets {
		if d.Key == key {
			return d, true
		}
	}
	return Dataset{}, false
}
