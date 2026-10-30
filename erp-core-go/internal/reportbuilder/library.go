package reportbuilder

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/locale"
	"erp-core-go/internal/subscriptions"
)

// The standard report library (phase0_1_design.md §3.63): ready-made
// reports over the same allow-listed datasets the builder uses, so they
// run through the same compiler, limits, branch scope and export jobs. A
// person picks a report, a period (or dates) and a few filters; nobody
// designs anything. Library reports run on every plan (the builder needs
// custom_report_builder), and saving one keeps it, with its filters and a
// relative period, in the saved-reports list to run again ("regenerate")
// whenever it's wanted: "this month" always means the current month.
//
// Every generated export is kept (report_runs, the person's last 50) and
// can be downloaded again or regenerated, with the same dates or fresh
// ones.

// LibraryReport is one standard report.
type LibraryReport struct {
	Key           string   `json:"key"`
	Group         string   `json:"group"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Spec          Spec     `json:"spec"`
	DefaultPeriod string   `json:"default_period,omitempty"`
	QuickFilters  []string `json:"quick_filters"` // dataset field keys offered as filters
}

// Periods are the relative ranges a report can cover, in India Standard
// Time; weeks run Monday to Sunday and the financial year April to March.
var Periods = []struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}{
	{"today", "Today"}, {"yesterday", "Yesterday"}, {"this_week", "This week"}, {"last_week", "Last week"},
	{"last_7_days", "Last 7 days"}, {"this_month", "This month"}, {"last_month", "Last month"}, {"last_30_days", "Last 30 days"},
	{"this_quarter", "This quarter"}, {"last_quarter", "Last quarter"}, {"this_fy", "This financial year"}, {"last_fy", "Last financial year"},
}

var istZone = time.FixedZone("IST", 5*3600+30*60)

// ResolvePeriod turns a period into YYYY-MM-DD dates as of now, on now's
// own clock (the merchant's time zone, Spec.now).
func ResolvePeriod(period string, now time.Time) (string, string, bool) {
	loc := now.Location()
	n := now
	today := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, loc)
	monday := today.AddDate(0, 0, -((int(today.Weekday()) + 6) % 7))
	month := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, loc)
	quarter := time.Date(today.Year(), time.Month((int(today.Month())-1)/3*3+1), 1, 0, 0, 0, 0, loc)
	fy := time.Date(today.Year(), time.April, 1, 0, 0, 0, 0, loc)
	if today.Before(fy) {
		fy = fy.AddDate(-1, 0, 0)
	}
	var from, to time.Time
	switch period {
	case "today":
		from, to = today, today
	case "yesterday":
		from, to = today.AddDate(0, 0, -1), today.AddDate(0, 0, -1)
	case "this_week":
		from, to = monday, today
	case "last_week":
		from, to = monday.AddDate(0, 0, -7), monday.AddDate(0, 0, -1)
	case "last_7_days":
		from, to = today.AddDate(0, 0, -6), today
	case "this_month":
		from, to = month, today
	case "last_month":
		from, to = month.AddDate(0, -1, 0), month.AddDate(0, 0, -1)
	case "last_30_days":
		from, to = today.AddDate(0, 0, -29), today
	case "this_quarter":
		from, to = quarter, today
	case "last_quarter":
		from, to = quarter.AddDate(0, -3, 0), quarter.AddDate(0, 0, -1)
	case "this_fy":
		from, to = fy, today
	case "last_fy":
		from, to = fy.AddDate(-1, 0, 0), fy.AddDate(0, 0, -1)
	default:
		return "", "", false
	}
	return from.Format("2006-01-02"), to.Format("2006-01-02"), true
}

func cols(fields ...string) []Column {
	out := make([]Column, len(fields))
	for i, f := range fields {
		if k, agg, ok := strings.Cut(f, ":"); ok {
			out[i] = Column{Field: k, Agg: agg}
		} else {
			out[i] = Column{Field: f}
		}
	}
	return out
}

func asc(i ...int) []Sort {
	out := make([]Sort, len(i))
	for j, c := range i {
		out[j] = Sort{Column: c}
	}
	return out
}

func desc(c int) []Sort { return []Sort{{Column: c, Desc: true}} }

// named gives columns their own headings, in order ("" keeps the
// generated one).
func named(cs []Column, labels ...string) []Column {
	for i, l := range labels {
		if i < len(cs) && l != "" {
			cs[i].Label = l
		}
	}
	return cs
}

// Library is the catalogue. A report's spec is fixed here; the person
// running it adds dates and filters.
var Library = []LibraryReport{
	// Sales
	{Key: "sales_register", Group: "Sales", Name: "Sales register", Description: "Every sale with its customer, cashier and totals.",
		Spec:          Spec{Dataset: "sales", Columns: cols("date", "order_number", "branch", "terminal", "cashier", "customer", "status", "subtotal", "discount_total", "cgst", "sgst", "igst", "tax_total", "grand_total", "round_off", "net_total"), Sort: asc(0, 1)},
		DefaultPeriod: "today", QuickFilters: []string{"branch", "channel", "status", "cashier", "customer"}},
	{Key: "sales_daily", Group: "Sales", Name: "Daily sales summary", Description: "Bills, discounts, tax and sales per day.",
		Spec: Spec{Dataset: "sales", Columns: cols("date", "order_number:count", "subtotal:sum", "discount_total:sum", "cgst:sum", "sgst:sum", "igst:sum", "tax_total:sum", "grand_total:sum", "round_off:sum", "net_total:sum", "net_total:avg"), Sort: asc(0),
			Filters: []Filter{{Field: "status", Op: "ne", Value: "voided"}}},
		DefaultPeriod: "this_month", QuickFilters: []string{"branch", "channel"}},
	{Key: "sales_monthly", Group: "Sales", Name: "Monthly sales summary", Description: "Bills and sales per month.",
		Spec: Spec{Dataset: "sales", Columns: cols("month", "order_number:count", "discount_total:sum", "cgst:sum", "sgst:sum", "igst:sum", "tax_total:sum", "grand_total:sum", "round_off:sum", "net_total:sum", "net_total:avg"), Sort: asc(0),
			Filters: []Filter{{Field: "status", Op: "ne", Value: "voided"}}},
		DefaultPeriod: "this_fy", QuickFilters: []string{"branch", "channel"}},
	{Key: "sales_by_branch", Group: "Sales", Name: "Sales by branch", Description: "Each branch's bills, sales and average bill.",
		Spec: Spec{Dataset: "sales", Columns: cols("branch", "order_number:count", "net_total:sum", "net_total:avg", "round_off:sum"), Sort: desc(2),
			Filters: []Filter{{Field: "status", Op: "ne", Value: "voided"}}},
		DefaultPeriod: "this_month", QuickFilters: []string{"channel"}},
	{Key: "sales_by_cashier", Group: "Sales", Name: "Sales by cashier", Description: "Each cashier's bills, sales and average bill.",
		Spec: Spec{Dataset: "sales", Columns: cols("cashier", "order_number:count", "net_total:sum", "net_total:avg", "discount_total:sum", "round_off:sum"), Sort: desc(2),
			Filters: []Filter{{Field: "status", Op: "ne", Value: "voided"}}},
		DefaultPeriod: "this_month", QuickFilters: []string{"branch"}},
	{Key: "sales_by_customer", Group: "Sales", Name: "Sales by customer", Description: "What each customer bought, and how much is on credit.",
		Spec: Spec{Dataset: "sales", Columns: cols("customer", "customer_type", "order_number:count", "net_total:sum", "credit_amount:sum"), Sort: desc(3),
			Filters: []Filter{{Field: "status", Op: "ne", Value: "voided"}, {Field: "customer", Op: "not_empty"}}},
		DefaultPeriod: "this_month", QuickFilters: []string{"branch", "customer_type"}},
	{Key: "sales_hourly", Group: "Sales", Name: "Sales by hour of day", Description: "When in the day sales happen.",
		Spec: Spec{Dataset: "sales", Columns: cols("hour", "order_number:count", "net_total:sum"), Sort: asc(0),
			Filters: []Filter{{Field: "status", Op: "ne", Value: "voided"}}},
		DefaultPeriod: "last_30_days", QuickFilters: []string{"branch", "weekday"}},

	// Items
	{Key: "items_by_product", Group: "Items", Name: "Item-wise sales", Description: "Quantity, value, tax and margin for every product sold.",
		Spec:          Spec{Dataset: "sales_items", Columns: cols("product", "sku", "quantity:sum", "taxable_value:sum", "tax_amount:sum", "line_total:sum", "margin:sum"), Sort: desc(5)},
		DefaultPeriod: "this_month", QuickFilters: []string{"branch", "category", "brand", "product"}},
	{Key: "items_by_category", Group: "Items", Name: "Category-wise sales", Description: "Quantity, value and margin per category.",
		Spec:          Spec{Dataset: "sales_items", Columns: cols("category", "quantity:sum", "line_total:sum", "margin:sum"), Sort: desc(2)},
		DefaultPeriod: "this_month", QuickFilters: []string{"branch", "brand"}},
	{Key: "items_by_brand", Group: "Items", Name: "Brand-wise sales", Description: "Quantity, value and margin per brand.",
		Spec:          Spec{Dataset: "sales_items", Columns: cols("brand", "quantity:sum", "line_total:sum", "margin:sum"), Sort: desc(2)},
		DefaultPeriod: "this_month", QuickFilters: []string{"branch", "category"}},
	{Key: "items_register", Group: "Items", Name: "Items sold register", Description: "Every line sold: product, quantity, price, discount and tax.",
		Spec:          Spec{Dataset: "sales_items", Columns: cols("date", "order_number", "product", "sku", "quantity", "unit_price", "discount", "tax_amount", "line_total"), Sort: asc(0, 1)},
		DefaultPeriod: "today", QuickFilters: []string{"branch", "category", "brand", "product"}},
	{Key: "items_gst_summary", Group: "Items", Name: "GST rate summary", Description: "Taxable value and GST collected at each rate, with the HSN codes.",
		Spec:          Spec{Dataset: "sales_items", Columns: cols("tax_rate", "hsn", "quantity:sum", "taxable_value:sum", "cgst:sum", "sgst:sum", "igst:sum", "tax_amount:sum", "line_total:sum"), Sort: asc(0, 1)},
		DefaultPeriod: "last_month", QuickFilters: []string{"branch"}},

	// Payments
	{Key: "payments_by_method", Group: "Payments", Name: "Payments by method", Description: "How customers paid: count and amount per tender.",
		Spec: Spec{Dataset: "payments", Columns: cols("method", "order_number:count", "amount:sum"), Sort: desc(2),
			Filters: []Filter{{Field: "status", Op: "eq", Value: "captured"}}},
		DefaultPeriod: "today", QuickFilters: []string{"branch", "terminal", "channel"}},
	{Key: "payments_daily", Group: "Payments", Name: "Daily payments by method", Description: "Each day's takings per tender.",
		Spec: Spec{Dataset: "payments", Columns: cols("date", "method", "amount:sum"), Sort: asc(0, 1),
			Filters: []Filter{{Field: "status", Op: "eq", Value: "captured"}}},
		DefaultPeriod: "this_month", QuickFilters: []string{"branch", "method"}},
	{Key: "payments_register", Group: "Payments", Name: "Payments register", Description: "Every payment taken, with its sale.",
		Spec:          Spec{Dataset: "payments", Columns: cols("date", "order_number", "method", "status", "branch", "terminal", "amount"), Sort: asc(0, 1)},
		DefaultPeriod: "today", QuickFilters: []string{"branch", "method", "status"}},

	// Stock
	{Key: "stock_on_hand", Group: "Stock", Name: "Stock on hand", Description: "Current stock per item and branch, valued at cost and selling price.",
		Spec:         Spec{Dataset: "stock", Columns: cols("branch", "product", "sku", "category", "on_hand", "reserved", "available", "value_cost", "value_retail"), Sort: asc(0, 1)},
		QuickFilters: []string{"branch", "category", "brand", "product"}},
	{Key: "stock_valuation", Group: "Stock", Name: "Stock valuation by category", Description: "Units and value of stock per category.",
		Spec:         Spec{Dataset: "stock", Columns: cols("category", "on_hand:sum", "value_cost:sum", "value_retail:sum"), Sort: desc(2)},
		QuickFilters: []string{"branch", "brand"}},
	{Key: "stock_low", Group: "Stock", Name: "Low stock", Description: "Items at or below their reorder point.",
		Spec: Spec{Dataset: "stock", Columns: cols("branch", "product", "sku", "available", "reorder_point"), Sort: asc(0, 3),
			Filters: []Filter{{Field: "below_reorder", Op: "is_true"}}},
		QuickFilters: []string{"branch", "category", "brand"}},
	{Key: "stock_movements", Group: "Stock", Name: "Stock movement register", Description: "Every change to stock: sales, returns, purchases, transfers and adjustments.",
		Spec:          Spec{Dataset: "stock_movements", Columns: cols("date", "branch", "product", "sku", "movement_type", "reason", "quantity", "value_cost"), Sort: asc(0)},
		DefaultPeriod: "this_week", QuickFilters: []string{"branch", "movement_type", "product", "category"}},
	{Key: "stock_movement_summary", Group: "Stock", Name: "Stock movement summary", Description: "Net change per product and kind of movement.",
		Spec:          Spec{Dataset: "stock_movements", Columns: cols("product", "sku", "movement_type", "quantity:sum", "value_cost:sum"), Sort: asc(0, 2)},
		DefaultPeriod: "this_month", QuickFilters: []string{"branch", "category", "brand"}},

	// Products
	{Key: "product_list", Group: "Products", Name: "Product price list", Description: "Every item with its tax, cost, MRP, selling price and margin.",
		Spec:         Spec{Dataset: "products", Columns: cols("product", "sku", "category", "brand", "hsn", "tax_rate", "cost_price", "mrp", "selling_price", "margin_pct", "status"), Sort: asc(0, 1)},
		QuickFilters: []string{"category", "brand", "status"}},
	{Key: "product_low_margin", Group: "Products", Name: "Lowest-margin products", Description: "Active items ordered by margin, lowest first.",
		Spec: Spec{Dataset: "products", Columns: cols("product", "sku", "category", "cost_price", "selling_price", "margin_pct"), Sort: asc(5),
			Filters: []Filter{{Field: "status", Op: "eq", Value: "active"}}},
		QuickFilters: []string{"category", "brand"}},

	// Purchases
	{Key: "purchase_register", Group: "Purchases", Name: "Purchase register", Description: "Every supplier bill with its tax, charges, payments and balance.",
		Spec:          Spec{Dataset: "purchases", Columns: cols("date", "bill_number", "supplier_invoice", "supplier", "supplier_gstin", "branch", "subtotal", "cgst", "sgst", "igst", "tax_total", "freight", "other_charges", "grand_total", "amount_paid", "outstanding", "status"), Sort: asc(0, 1)},
		DefaultPeriod: "this_month", QuickFilters: []string{"branch", "supplier", "status"}},
	{Key: "purchase_by_supplier", Group: "Purchases", Name: "Purchases by supplier", Description: "Bills, value, paid and outstanding per supplier.",
		Spec:          Spec{Dataset: "purchases", Columns: cols("supplier", "bill_number:count", "grand_total:sum", "amount_paid:sum", "outstanding:sum"), Sort: desc(2)},
		DefaultPeriod: "this_fy", QuickFilters: []string{"branch"}},
	{Key: "purchase_payables", Group: "Purchases", Name: "Outstanding payables", Description: "Unpaid and part-paid bills, with what's still owed and whether it's overdue.",
		Spec: Spec{Dataset: "purchases", Columns: cols("supplier", "bill_number", "date", "due_date", "overdue", "grand_total", "outstanding"), Sort: asc(3),
			Filters: []Filter{{Field: "status", Op: "ne", Value: "paid"}}},
		DefaultPeriod: "this_fy", QuickFilters: []string{"branch", "supplier"}},
	// Outstanding, party-wise (§3.77): as of now, no period.
	{Key: "credit_outstanding_by_customer", Group: "Outstanding", Name: "Credit outstanding by customer",
		Description: "What each customer still owes on credit sales, with their credit limit and how overdue it is (not yet due, 0–30, 31–60, 61–90, over 90 days).",
		Spec: Spec{Dataset: "receivables", Columns: named(cols("customer", "phone", "credit_limit:max", "invoice:count", "outstanding:sum",
			"not_due:sum", "days_0_30:sum", "days_31_60:sum", "days_61_90:sum", "days_90_plus:sum"),
			"", "", "Credit limit", "Open sales", "Outstanding", "Not yet due", "0–30 days", "31–60 days", "61–90 days", "Over 90 days"), Sort: desc(4)},
		QuickFilters: []string{"branch", "customer", "customer_type", "overdue"}},
	{Key: "credit_outstanding_invoices", Group: "Outstanding", Name: "Open credit sales",
		Description: "Every credit sale not yet fully paid: customer, sale, due date, days overdue and what is still owed.",
		Spec: Spec{Dataset: "receivables", Columns: cols("customer", "phone", "invoice", "date", "due_date", "days_overdue", "credit_amount", "credit_paid", "outstanding"),
			Sort: []Sort{{Column: 0}, {Column: 4}}},
		QuickFilters: []string{"branch", "customer", "overdue"}},
	{Key: "payables_outstanding_by_supplier", Group: "Outstanding", Name: "Payables outstanding by supplier",
		Description: "What is still owed to each supplier, and how overdue it is (not yet due, 0–30, 31–60, 61–90, over 90 days).",
		Spec: Spec{Dataset: "payables", Columns: named(cols("supplier", "supplier_gstin", "bill_number:count", "outstanding:sum",
			"not_due:sum", "days_0_30:sum", "days_31_60:sum", "days_61_90:sum", "days_90_plus:sum"),
			"", "", "Open bills", "Outstanding", "Not yet due", "0–30 days", "31–60 days", "61–90 days", "Over 90 days"), Sort: desc(3)},
		QuickFilters: []string{"branch", "supplier", "overdue"}},
	// Farm (§3.81): the log and each batch's figures.
	{Key: "farm_daily_log", Group: "Farm", Name: "Daily farm log",
		Description:   "Every entry of the farm's log: mortality, culls, feed, medicine, vaccination, weights, eggs, milk and harvests.",
		Spec:          Spec{Dataset: "farm_events", Columns: cols("date", "batch", "house", "event_type", "head_count", "product", "quantity", "unit", "amount", "cause", "vaccine", "notes"), Sort: asc(0, 1)},
		DefaultPeriod: "this_month", QuickFilters: []string{"branch", "batch", "event_type", "species"}},
	{Key: "farm_batch_performance", Group: "Farm", Name: "Flock and herd performance",
		Description: "Each batch: placed, died, alive, mortality %, feed, costs, produce and net cost.",
		Spec: Spec{Dataset: "farm_batches", Columns: cols("batch", "species", "purpose", "house", "placed_on", "age_days", "placed", "dead", "alive", "mortality_pct",
			"feed_kg", "feed_cost", "health_cost", "output_value", "net_cost"), Sort: desc(4)},
		QuickFilters: []string{"branch", "species", "status"}},
	{Key: "farm_feed_by_batch", Group: "Farm", Name: "Feed used by batch",
		Description: "How much of each feed every batch ate, and what it cost.",
		Spec: Spec{Dataset: "farm_events", Columns: named(cols("batch", "product", "unit", "quantity:sum", "amount:sum"), "", "Feed", "", "Quantity", "Cost"), Sort: asc(0, 1),
			Filters: []Filter{{Field: "event_type", Op: "eq", Value: "Feed"}}},
		DefaultPeriod: "this_month", QuickFilters: []string{"branch", "batch"}},
	{Key: "farm_mortality", Group: "Farm", Name: "Mortality by cause",
		Description: "Deaths per batch and cause.",
		Spec: Spec{Dataset: "farm_events", Columns: named(cols("batch", "cause", "head_count:sum"), "", "", "Died"), Sort: desc(2),
			Filters: []Filter{{Field: "event_type", Op: "eq", Value: "Mortality"}}},
		DefaultPeriod: "this_month", QuickFilters: []string{"branch", "batch"}},
	{Key: "farm_production_daily", Group: "Farm", Name: "Production by day",
		Description: "Eggs, milk and harvests put into stock each day, with their value.",
		Spec: Spec{Dataset: "farm_events", Columns: named(cols("date", "product", "unit", "quantity:sum", "amount:sum"), "", "Produce", "", "Quantity", "Value"), Sort: asc(0, 1),
			Filters: []Filter{{Field: "event_type", Op: "eq", Value: "Production"}}},
		DefaultPeriod: "this_month", QuickFilters: []string{"branch", "batch"}},

	{Key: "farm_milk_by_animal", Group: "Farm", Name: "Milk by animal",
		Description:   "Litres each animal gave in the period, morning and evening together.",
		Spec:          Spec{Dataset: "farm_milk", Columns: named(cols("batch", "animal", "animal_name", "litres:sum", "date:count"), "", "", "", "Litres", "Milkings"), Sort: desc(3)},
		DefaultPeriod: "this_month", QuickFilters: []string{"branch", "batch", "animal"}},
	{Key: "farm_treatments", Group: "Farm", Name: "Treatments and withdrawals",
		Description: "Every medicine and vaccination with its dose, vet and the dates meat, milk and eggs may be taken again.",
		Spec: Spec{Dataset: "farm_events", Columns: cols("date", "batch", "animal", "event_type", "product", "vaccine", "quantity", "unit", "dose", "vet",
			"meat_until", "milk_until", "eggs_until", "amount"), Sort: asc(0, 1),
			Filters: []Filter{{Field: "event_type", Op: "in", Value: []any{"Medicine", "Vaccination"}}}},
		DefaultPeriod: "this_month", QuickFilters: []string{"branch", "batch", "animal"}},

	// Expenses (§3.78): recorded and imported expenses, cancelled ones left out.
	{Key: "expense_register", Group: "Expenses", Name: "Expense register",
		Description: "Every expense with its head, branch, vendor, who paid it and whether it is still unpaid.",
		Spec: Spec{Dataset: "expenses", Columns: cols("date", "expense_number", "head", "branch", "description", "vendor", "reference", "paid_by", "partner", "payment_status", "amount"), Sort: asc(0, 1),
			Filters: []Filter{{Field: "status", Op: "eq", Value: "posted"}}},
		DefaultPeriod: "this_month", QuickFilters: []string{"branch", "head", "paid_by", "partner", "payment_status"}},
	{Key: "expense_head_monthly", Group: "Expenses", Name: "Expenses by head and month",
		Description: "What was spent on each head, month by month.",
		Spec: Spec{Dataset: "expenses", Columns: named(cols("month", "head", "expense_number:count", "amount:sum"), "", "", "Expenses", "Amount"), Sort: asc(0, 1),
			Filters: []Filter{{Field: "status", Op: "eq", Value: "posted"}}},
		DefaultPeriod: "this_fy", QuickFilters: []string{"branch", "head", "account_type"}},
	{Key: "expense_by_branch", Group: "Expenses", Name: "Expenses by branch",
		Description: "Each branch's expenses: total, paid, still unpaid and paid by partners.",
		Spec: Spec{Dataset: "expenses", Columns: named(cols("branch", "expense_number:count", "amount:sum", "unpaid_amount:sum", "partner_amount:sum"), "", "Expenses", "Amount", "Unpaid", "Paid by partners"), Sort: desc(2),
			Filters: []Filter{{Field: "status", Op: "eq", Value: "posted"}}},
		DefaultPeriod: "this_fy", QuickFilters: []string{"head", "account_type"}},
	{Key: "expense_unpaid_partner", Group: "Expenses", Name: "Unpaid and partner-paid expenses",
		Description: "Expenses still to be paid, and expenses a partner paid from their own money (which went to their capital).",
		Spec: Spec{Dataset: "expenses", Columns: cols("date", "expense_number", "head", "branch", "vendor", "payment_status", "partner", "unpaid_amount", "partner_amount"), Sort: asc(0, 1),
			Filters: []Filter{{Field: "status", Op: "eq", Value: "posted"}, {Field: "paid_by", Op: "in", Value: []any{"Unpaid", "Partner"}}}},
		DefaultPeriod: "this_fy", QuickFilters: []string{"branch", "partner", "payment_status"}},
	{Key: "purchase_items", Group: "Purchases", Name: "Items purchased", Description: "Every line received on a completed GRN, at its cost.",
		Spec:          Spec{Dataset: "purchase_items", Columns: cols("date", "grn_number", "supplier", "product", "sku", "quantity", "unit_cost", "landed_unit_cost", "landed_total"), Sort: asc(0, 1)},
		DefaultPeriod: "this_month", QuickFilters: []string{"branch", "supplier", "category", "product"}},
	{Key: "purchase_items_by_product", Group: "Purchases", Name: "Purchases by product", Description: "Quantity and landed cost bought per product.",
		Spec:          Spec{Dataset: "purchase_items", Columns: cols("product", "sku", "quantity:sum", "landed_total:sum", "landed_unit_cost:avg"), Sort: desc(3)},
		DefaultPeriod: "this_month", QuickFilters: []string{"branch", "supplier", "category"}},

	// Accounts
	{Key: "profit_loss", Group: "Accounts", Name: "Profit and loss", Description: "Income, cost of goods sold and expenses; the total is the profit (a loss when negative). Cost of goods uses current cost prices.",
		Spec:          Spec{Dataset: "profit_loss", Columns: cols("section", "line", "amount:sum"), Sort: asc(0, 1)},
		DefaultPeriod: "this_month", QuickFilters: []string{"branch"}},
	{Key: "profit_loss_monthly", Group: "Accounts", Name: "Monthly profit and loss", Description: "Each month's income, cost of goods and expenses.",
		Spec:          Spec{Dataset: "profit_loss", Columns: cols("month", "section", "amount:sum"), Sort: asc(0, 1)},
		DefaultPeriod: "this_fy", QuickFilters: []string{"branch"}},
	{Key: "journal_report", Group: "Accounts", Name: "Journal report", Description: "Every posted ledger line: entry, account, party, debit and credit.",
		Spec:          Spec{Dataset: "journal_lines", Columns: cols("date", "entry_number", "source_type", "description", "account_code", "account", "party", "debit", "credit"), Sort: asc(0, 1)},
		DefaultPeriod: "this_month", QuickFilters: []string{"branch", "source_type", "account", "party"}},
	{Key: "trial_balance", Group: "Accounts", Name: "Account summary (trial balance)", Description: "Debits and credits per account over the period; they balance.",
		Spec:          Spec{Dataset: "journal_lines", Columns: cols("account_code", "account", "account_type", "debit:sum", "credit:sum", "net:sum"), Sort: asc(0)},
		DefaultPeriod: "this_fy", QuickFilters: []string{"branch"}},
	{Key: "general_ledger", Group: "Accounts", Name: "General ledger", Description: "Every line, account by account and in date order. Filter on one account for its ledger.",
		Spec:          Spec{Dataset: "journal_lines", Columns: cols("account_code", "account", "date", "entry_number", "description", "party", "debit", "credit"), Sort: asc(0, 2, 3)},
		DefaultPeriod: "this_month", QuickFilters: []string{"account", "branch", "party"}},
	{Key: "party_ledger_summary", Group: "Accounts", Name: "Ledger by party", Description: "Debits, credits and net per customer and supplier.",
		Spec: Spec{Dataset: "journal_lines", Columns: cols("party_type", "party", "debit:sum", "credit:sum", "net:sum"), Sort: asc(0, 1),
			Filters: []Filter{{Field: "party_type", Op: "not_empty"}}},
		DefaultPeriod: "this_fy", QuickFilters: []string{"party_type", "party"}},

	// Audit
	{Key: "audit_trail", Group: "Audit", Name: "Audit report", Description: "Every recorded change: what, when, by whom and why.",
		Spec:          Spec{Dataset: "audit_log", Columns: cols("date", "time", "entity_type", "action", "performed_by", "reason", "entity_id", "changes"), Sort: []Sort{{Column: 0, Desc: true}, {Column: 1, Desc: true}}},
		DefaultPeriod: "today", QuickFilters: []string{"entity_type", "action", "performed_by"}},
	{Key: "audit_by_user", Group: "Audit", Name: "Changes by user", Description: "How many changes of each kind each person made.",
		Spec:          Spec{Dataset: "audit_log", Columns: cols("performed_by", "entity_type", "action", "entity_id:count"), Sort: asc(0, 1)},
		DefaultPeriod: "this_month", QuickFilters: []string{"entity_type", "performed_by"}},
	{Key: "sales_audit", Group: "Audit", Name: "Sales audit report", Description: "Voids, released carts, returns, manual discounts and open prices: who did each and who approved it.",
		Spec:          Spec{Dataset: "sales_audit", Columns: cols("date", "time", "event", "order_number", "branch", "terminal", "done_by", "approved_by", "reason", "amount"), Sort: asc(0, 1)},
		DefaultPeriod: "this_week", QuickFilters: []string{"branch", "event", "done_by"}},
	{Key: "sales_audit_summary", Group: "Audit", Name: "Sales audit summary", Description: "Count and value of each kind of sales event per person.",
		Spec:          Spec{Dataset: "sales_audit", Columns: cols("done_by", "event", "order_number:count", "amount:sum"), Sort: asc(0, 1)},
		DefaultPeriod: "this_month", QuickFilters: []string{"branch", "event"}},
}

func FindLibrary(key string) (LibraryReport, bool) {
	for _, l := range Library {
		if l.Key == key {
			return l, true
		}
	}
	return LibraryReport{}, false
}

// libraryRequest is what a person adds to a report when running it.
type libraryRequest struct {
	Period   string   `json:"period"`
	DateFrom string   `json:"date_from"`
	DateTo   string   `json:"date_to"`
	Filters  []Filter `json:"filters"`
	// export / save
	Format      string `json:"format"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Shared      bool   `json:"shared"`
}

// specFor is the report's spec with the person's dates and filters: fixed
// dates when both are given, else the period (theirs, or the report's).
func (l LibraryReport) specFor(req libraryRequest) Spec {
	s := l.Spec
	s.Filters = append(append([]Filter{}, l.Spec.Filters...), req.Filters...)
	s.DateFrom, s.DateTo, s.Period = "", "", ""
	if ds, _ := FindDataset(s.Dataset); ds.HasDateRange {
		switch {
		case req.DateFrom != "" && req.DateTo != "":
			s.DateFrom, s.DateTo = req.DateFrom, req.DateTo
		case req.Period != "":
			s.Period = req.Period
		default:
			s.Period = l.DefaultPeriod
		}
	}
	return s
}

// resolved fills in the dates a period stands for, as of now, keeping the
// period, so an export records exactly which days it covered.
func resolved(s Spec) Spec {
	if s.Period != "" && s.DateFrom == "" && s.DateTo == "" {
		if f, t, ok := ResolvePeriod(s.Period, s.now()); ok {
			s.DateFrom, s.DateTo = f, t
		}
	}
	return s
}

// LibraryList: GET /reports/library — the catalogue, with the fields each
// report's filters use and the periods.
func (h *Handler) LibraryList(w http.ResponseWriter, r *http.Request) {
	type filterInfo struct {
		Key   string   `json:"key"`
		Label string   `json:"label"`
		Type  string   `json:"type"`
		Ops   []string `json:"ops"`
	}
	type item struct {
		LibraryReport
		DatasetLabel string       `json:"dataset_label"`
		HasDateRange bool         `json:"has_date_range"`
		Filters      []filterInfo `json:"filter_fields"`
	}
	out := make([]item, 0, len(Library))
	for _, l := range Library {
		ds, _ := FindDataset(l.Spec.Dataset)
		it := item{LibraryReport: l, DatasetLabel: ds.Label, HasDateRange: ds.HasDateRange, Filters: []filterInfo{}}
		for _, k := range l.QuickFilters {
			if f, ok := ds.Field(k); ok {
				it.Filters = append(it.Filters, filterInfo{Key: f.Key, Label: f.Label, Type: f.Type, Ops: OpsFor(f.Type)})
			}
		}
		out = append(out, it)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"reports": out, "periods": Periods,
		"limits": map[string]any{"preview_rows": PreviewRows, "export_rows": ExportRows, "max_date_span_days": MaxDateSpanDays}})
}

func decodeLibrary(w http.ResponseWriter, r *http.Request) (LibraryReport, libraryRequest, bool) {
	l, ok := FindLibrary(chi.URLParam(r, "key"))
	if !ok {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "no such report")
		return l, libraryRequest{}, false
	}
	var req libraryRequest
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
			return l, req, false
		}
	}
	if len(req.Filters) > MaxFilters/2 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REPORT", "too many filters")
		return l, req, false
	}
	return l, req, true
}

type runResult struct {
	Result
	DateFrom string `json:"date_from,omitempty"`
	DateTo   string `json:"date_to,omitempty"`
}

func (h *Handler) previewResolved(w http.ResponseWriter, r *http.Request, spec Spec) {
	claims, _ := authn.FromContext(r.Context())
	spec.clock = locale.MerchantLocation(r.Context(), h.DB, claims.TenantID)
	spec = resolved(spec)
	c, err := Compile(spec, horizonFor(r.Context()), PreviewRows)
	if err != nil {
		writeErr(w, err)
		return
	}
	var res Result
	err = h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		res, err = Execute(ctx, tx, c, PreviewRows, previewTimeout)
		return err
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, runResult{Result: res, DateFrom: spec.DateFrom, DateTo: spec.DateTo})
}

// LibraryRun: POST /reports/library/{key}/run {period | date_from+date_to, filters}
// — the first 1,000 rows, in the caller's branch scope.
func (h *Handler) LibraryRun(w http.ResponseWriter, r *http.Request) {
	l, req, ok := decodeLibrary(w, r)
	if !ok {
		return
	}
	h.previewResolved(w, r, l.specFor(req))
}

// queueRun records a run and starts it.
func (h *Handler) queueRun(w http.ResponseWriter, r *http.Request, spec Spec, name, format string, templateID, libraryKey *string) {
	claims, _ := authn.FromContext(r.Context())
	if format != "csv" && format != "xlsx" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "format must be csv or xlsx")
		return
	}
	spec.clock = locale.MerchantLocation(r.Context(), h.DB, claims.TenantID)
	spec = resolved(spec)
	if _, err := Compile(spec, horizonFor(r.Context()), ExportRows); err != nil {
		writeErr(w, err)
		return
	}
	var run Run
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		raw, _ := json.Marshal(spec)
		var err error
		run, err = scanRun(tx.QueryRow(ctx, `INSERT INTO report_runs (merchant_id, template_id, name, spec, format, requested_by, library_key)
			VALUES (current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, $5, $6) RETURNING `+runColumns,
			templateID, name, raw, format, claims.UserID, libraryKey))
		return err
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	h.Runner.Enqueue(claims.TenantID, run.ID)
	httpx.JSON(w, http.StatusAccepted, run)
}

// LibraryExport: POST /reports/library/{key}/export {format, period | dates, filters, name?}
// — 202 with the queued run (up to 100,000 rows, in the background).
func (h *Handler) LibraryExport(w http.ResponseWriter, r *http.Request) {
	l, req, ok := decodeLibrary(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = l.Name
	}
	h.queueRun(w, r, l.specFor(req), name, req.Format, nil, &l.Key)
}

// ------------------------------------------------------------ saved reports

// builderAllowed: a report made in the builder (no library key) runs only
// on a plan with the builder.
func builderAllowed(ctx context.Context, t Template) bool {
	if t.LibraryKey != nil {
		return true
	}
	e, ok := subscriptions.FromContext(ctx)
	return !ok || e.Plan.Has(subscriptions.FeatureCustomReportBuilder)
}

// SavedList: GET /reports/saved — the caller's saved reports and every
// shared one: those saved from the library and those built in the builder.
func (h *Handler) SavedList(w http.ResponseWriter, r *http.Request) {
	h.ListTemplates(w, r)
}

// SaveLibrary: POST /reports/saved {library_key, name, description?, shared?, period | dates, filters}
// — keep a standard report with its filters, to run again later. Any
// signed-in user may keep their own; sharing shows it to everyone.
func (h *Handler) SaveLibrary(w http.ResponseWriter, r *http.Request) {
	claims, _ := authn.FromContext(r.Context())
	var req struct {
		libraryRequest
		LibraryKey string `json:"library_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	l, ok := FindLibrary(req.LibraryKey)
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "unknown library_key")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 120 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "a name (up to 120 characters) is required")
		return
	}
	spec := l.specFor(req.libraryRequest)
	if _, err := Compile(spec, time.Time{}, PreviewRows); err != nil {
		writeErr(w, err)
		return
	}
	var t Template
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		raw, _ := json.Marshal(spec)
		var id string
		if err := tx.QueryRow(ctx, `INSERT INTO report_templates (merchant_id, name, description, spec, shared, created_by, library_key)
			VALUES (current_setting('app.tenant_id')::uuid, $1, NULLIF($2, ''), $3, $4, $5, $6) RETURNING id`,
			name, strings.TrimSpace(req.Description), raw, req.Shared, claims.UserID, l.Key).Scan(&id); err != nil {
			return err
		}
		var err error
		if t, err = getTemplate(ctx, tx, id, claims.UserID); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "report_templates", EntityID: id, Action: "create", PerformedBy: claims.UserID,
			After: map[string]any{"name": name, "library_key": l.Key, "shared": req.Shared}})
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, t)
}

func (h *Handler) savedTemplate(w http.ResponseWriter, r *http.Request) (Template, libraryRequest, bool) {
	claims, _ := authn.FromContext(r.Context())
	var req libraryRequest
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
			return Template{}, req, false
		}
	}
	var t Template
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		t, err = getTemplate(ctx, tx, chi.URLParam(r, "id"), claims.UserID)
		return err
	})
	if err != nil {
		writeErr(w, err)
		return t, req, false
	}
	if !builderAllowed(r.Context(), t) {
		httpx.Error(w, http.StatusForbidden, "FEATURE_NOT_IN_PLAN", "this report was made in the custom report builder, which isn't part of the current plan")
		return t, req, false
	}
	// Other dates for this run only.
	switch {
	case req.DateFrom != "" && req.DateTo != "":
		t.Spec.DateFrom, t.Spec.DateTo, t.Spec.Period = req.DateFrom, req.DateTo, ""
	case req.Period != "":
		t.Spec.DateFrom, t.Spec.DateTo, t.Spec.Period = "", "", req.Period
	}
	return t, req, true
}

// SavedRun: POST /reports/saved/{id}/run {period? | dates?} — run a saved
// report now (first 1,000 rows), over its own period unless told otherwise.
func (h *Handler) SavedRun(w http.ResponseWriter, r *http.Request) {
	t, _, ok := h.savedTemplate(w, r)
	if !ok {
		return
	}
	h.previewResolved(w, r, t.Spec)
}

// SavedExport: POST /reports/saved/{id}/export {format, period? | dates?}.
func (h *Handler) SavedExport(w http.ResponseWriter, r *http.Request) {
	t, req, ok := h.savedTemplate(w, r)
	if !ok {
		return
	}
	h.queueRun(w, r, t.Spec, t.Name, req.Format, &t.ID, t.LibraryKey)
}

// SavedDelete: DELETE /reports/saved/{id} — the owner's own saved report.
func (h *Handler) SavedDelete(w http.ResponseWriter, r *http.Request) {
	claims, _ := authn.FromContext(r.Context())
	var name string
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `DELETE FROM report_templates WHERE id::text = $1 AND created_by::text = $2 RETURNING name`,
			chi.URLParam(r, "id"), claims.UserID).Scan(&name); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "report_templates", EntityID: chi.URLParam(r, "id"), Action: "delete",
			PerformedBy: claims.UserID, Before: map[string]any{"name": name}})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "no saved report of yours with that id (only the person who saved a report can delete it)")
		return
	} else if err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Regenerate: POST /reports/runs/{id}/regenerate {format?, refresh?, date_from?, date_to?}
// — produce one of the caller's earlier exports again: the same dates by
// default; refresh re-reads its period as of today (a "this month" export
// made last month then covers this month); or other dates.
func (h *Handler) Regenerate(w http.ResponseWriter, r *http.Request) {
	claims, _ := authn.FromContext(r.Context())
	var req struct {
		Format   string `json:"format"`
		Refresh  bool   `json:"refresh"`
		DateFrom string `json:"date_from"`
		DateTo   string `json:"date_to"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
			return
		}
	}
	var spec Spec
	var name, format string
	var templateID, libraryKey *string
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT spec, name, format, template_id::text, library_key FROM report_runs WHERE id::text = $1 AND requested_by::text = $2`,
			chi.URLParam(r, "id"), claims.UserID).Scan(&raw, &name, &format, &templateID, &libraryKey); err != nil {
			return err
		}
		return json.Unmarshal(raw, &spec)
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	if libraryKey == nil {
		if e, ok := subscriptions.FromContext(r.Context()); ok && !e.Plan.Has(subscriptions.FeatureCustomReportBuilder) {
			httpx.Error(w, http.StatusForbidden, "FEATURE_NOT_IN_PLAN", "this report was made in the custom report builder, which isn't part of the current plan")
			return
		}
	}
	switch {
	case req.DateFrom != "" && req.DateTo != "":
		spec.DateFrom, spec.DateTo = req.DateFrom, req.DateTo
	case req.Refresh && spec.Period != "":
		spec.DateFrom, spec.DateTo = "", ""
	}
	if req.Format != "" {
		format = req.Format
	}
	h.queueRun(w, r, spec, name, format, templateID, libraryKey)
}

// ReportRuns / ReportRunDownload: GET /reports/runs and
// /reports/runs/{id}/download — the caller's exports, library and built
// alike (the builder's own /report-builder/runs needs its plan feature).
func (h *Handler) ReportRuns(w http.ResponseWriter, r *http.Request)        { h.ListRuns(w, r) }
func (h *Handler) ReportRunDownload(w http.ResponseWriter, r *http.Request) { h.Download(w, r) }
