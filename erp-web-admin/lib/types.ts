// Mirrors erp-core-go's JSON response shapes exactly (internal/purchase,
// internal/accounting, internal/branches, internal/pricing) — money/
// quantity fields stay as strings, same reasoning as api-client.ts's
// header comment. Pricing's margin_pct/markup_pct come back as JSON
// numbers (not strings) from the Go side, since they're always
// server-computed, never round-tripped as a stored NUMERIC value.

export interface ProductVariant {
  variant_id: string;
  sku: string;
  cost_price: string;
  mrp: string;
  selling_price: string;
  // selling_price with tax removed (= selling_price when tax-exclusive) —
  // what margin/markup are computed on, since cost_price is pre-tax.
  net_selling_price: string;
  // What the customer pays for one unit, tax included.
  gross_selling_price: string;
  margin_pct: number | null;
  markup_pct: number | null;
  // This variant's two possible barcodes (internal/catalog/barcode_assign.go) —
  // the real one already on the product (manually entered) and this
  // system's own auto-generated one. Either or both can be null; they
  // coexist rather than one replacing the other.
  original_barcode: string | null;
  generated_barcode: string | null;
  // Phase 8 batch/expiry tracking — stock screens show batch fields only
  // for these (a stock increase on one needs a batch_no).
  track_batch: boolean;
  status: VariantStatus;
  // Live stock over the branches you may see (GET /products, db.StockByVariant):
  // on hand, and what's still sellable (on hand less open carts).
  on_hand?: string;
  available?: string;
}

export type ProductStatus = "active" | "inactive" | "discontinued";
export type VariantStatus = ProductStatus | "out_of_stock";

export interface Product {
  product_id: string;
  name: string;
  unit?: UnitRef | null; // erp-core-go migrations/076
  status: ProductStatus;
  hsn_code: string;
  // erp-core-go migrations/033: whether every variant's selling_price
  // already includes tax, and the product's total tax rate.
  price_includes_tax: boolean;
  tax_rate_pct: number;
  variants: ProductVariant[];
  // Phase 11 list columns (GET /products) — names for display, ids for links.
  category_id?: string | null;
  category_name?: string | null;
  brand_id?: string | null;
  brand_name?: string | null;
}

// GET/PUT /pricing/settings — merchant-wide default for new products.
export interface PricingSettings {
  prices_include_tax_default: boolean;
}

// From GET /products/search (internal/search) — a denormalized OpenSearch
// document, one per variant, not the nested Product/ProductVariant shape
// GET /products returns. margin/markup aren't computed server-side here
// (the index doesn't store them), so the search screen derives them
// client-side from cost_price/selling_price when it needs to show them.
export interface SearchHit {
  variant_id: string;
  product_id: string;
  name: string;
  sku: string;
  hsn_code: string;
  category_name?: string;
  brand_name?: string;
  cost_price: number;
  mrp: number;
  selling_price: number;
  status: string;
  // Live stock (GET /products/search attaches it from Postgres, never the index).
  on_hand?: string;
  reserved?: string;
  available?: string;
}

export interface SearchResponse {
  results: SearchHit[] | null;
  total: number;
  page: number;
  limit: number;
}

export interface Customer {
  customer_id: string;
  name: string;
  phone: string;
  email: string;
  customer_type: "b2c" | "b2b";
  address: string;
  date_of_birth: string | null;
  anniversary: string | null;
  company_name: string;
  gstin: string;
  status: string;
  total_spend: string;
  transaction_count: number;
  last_purchase_at: string | null;
  segment: "new" | "regular" | "vip" | "dormant";
  credit_limit: string;
  payment_terms: "due_on_receipt" | "net_7" | "net_15" | "net_30" | "net_60" | "net_90";
  credit_hold: boolean;
  // "" = plain retail pricing (see erp-core-go's internal/pricing.ResolvePrice).
  price_list_id: string;
}

export interface CustomerOrderHistoryEntry {
  order_number: string;
  branch_name: string;
  finalized_at: string;
  grand_total: string;
}

export interface CustomerDetail extends Customer {
  recent_orders: CustomerOrderHistoryEntry[];
}

export interface Branch {
  branch_id: string;
  name: string;
  code: string;
  timezone: string;
  gstin: string;
  status: string;
  business_types?: string[]; // erp-core-go migrations/077
}

// GET/PUT /branches/{id}/print-settings (erp-core-go internal/documents):
// the letterhead every downloadable document of the branch carries.
export interface PrintSettings {
  branch_id: string;
  display_name: string;
  address: string;
  phone: string;
  email: string;
  header_text: string;
  receipt_footer: string | null; // null = the default "Thank you, visit again!"
  purchase_footer: string;
  show_logo: boolean;
  show_gstin: boolean;
  /** Print the SKU under each item on receipts and invoices (migrations/062). */
  show_sku?: boolean;
  receipt_format: "a4" | "thermal";
  has_logo: boolean;
  logo_updated_at: string | null;
  updated_at: string | null;
}

export interface TransferLine {
  line_id: string;
  variant_id: string;
  sku: string;
  product_name: string;
  requested_quantity: string;
  sent_quantity: string | null;
  received_quantity: string | null;
}

export interface Transfer {
  transfer_id: string;
  transfer_number: string;
  status: string;
  from_branch_id: string;
  to_branch_id: string;
  notes: string;
  rejection_reason?: string;
  lines: TransferLine[];
}

export interface TransferSummary {
  transfer_id: string;
  transfer_number: string;
  status: string;
  from_branch_id: string;
  to_branch_id: string;
  created_at: string;
}

export interface Supplier {
  supplier_id: string;
  name: string;
  legal_name: string;
  gstin: string;
  contact_name: string;
  email: string;
  phone: string;
  payment_terms: string;
  credit_limit: string;
  status: string;
}

export interface GRNLine {
  line_id: string;
  variant_id: string;
  sku: string;
  product_name: string;
  quantity: string;
  unit_cost: string;
  landed_unit_cost: string;
  line_total: string;
  batch_no: string;
  expiry_date: string | null;
  /** The purchase order line this receives; null on a direct GRN. */
  po_line_id: string | null;
}

export interface GRN {
  grn_id: string;
  grn_number: string;
  status: string;
  supplier_id: string;
  branch_id: string;
  freight_amount: string;
  other_charges: string;
  subtotal: string;
  grand_total: string;
  /** The purchase order received against; null for a direct GRN. */
  purchase_order_id: string | null;
  po_number: string;
  /** Its purchase bill (the supplier's invoice), once entered. */
  bill_id: string | null;
  bill_number: string | null;
  lines: GRNLine[];
}

export interface GRNSummary {
  grn_id: string;
  grn_number: string;
  status: string;
  supplier_id: string;
  branch_id: string;
  grand_total: string;
  created_at: string;
  purchase_order_id: string | null;
  po_number: string;
  /** Has a purchase bill already (a GRN is billed once). */
  billed: boolean;
}

// Purchase orders (erp-core-go migrations/057): PO → GRNs → a bill per GRN.
export type PurchaseOrderStatus = "draft" | "open" | "partially_received" | "completed" | "cancelled" | "closed";

export interface PurchaseOrderLine {
  line_id: string;
  variant_id: string;
  sku: string;
  product_name: string;
  quantity: string;
  unit_cost: string;
  line_total: string;
  received_qty: string;
  pending_qty: string;
  track_batch: boolean;
  /** Received beyond the ordered quantity on an invoice import whose differences were accepted (erp-core-go §3.74). */
  excess_qty?: string;
}

/** An item received against a PO that wasn't on it (an invoice import, erp-core-go §3.74). */
export interface PurchaseOrderUnplanned {
  variant_id: string;
  sku: string;
  product_name: string;
  quantity: string;
  line_total: string;
}

export interface PurchaseOrderGRN {
  grn_id: string;
  grn_number: string;
  status: string;
  grand_total: string;
  created_at: string;
  bill_id: string | null;
  bill_number: string | null;
}

export interface PurchaseOrder {
  po_id: string;
  po_number: string;
  status: PurchaseOrderStatus;
  branch_id: string;
  supplier_id: string;
  supplier_name: string;
  order_date: string;
  expected_date: string | null;
  subtotal: string;
  notes: string;
  close_reason: string;
  created_at: string;
  lines: PurchaseOrderLine[];
  grns: PurchaseOrderGRN[];
  unplanned?: PurchaseOrderUnplanned[];
  /** The partner funding the order (erp-core-go migrations/074). */
  funded_by_partner_id?: string | null;
  funded_by_partner?: string | null;
}

export interface PurchaseOrderSummary {
  po_id: string;
  po_number: string;
  status: PurchaseOrderStatus;
  branch_id: string;
  supplier_id: string;
  supplier_name: string;
  order_date: string;
  expected_date: string | null;
  subtotal: string;
  ordered_qty: string;
  received_qty: string;
  created_at: string;
}

export interface Bill {
  bill_id: string;
  bill_number: string;
  supplier_invoice_number: string;
  supplier_id: string;
  grn_id: string;
  subtotal: string;
  tax_total: string;
  freight_amount: string;
  other_charges: string;
  grand_total: string;
  amount_paid: string;
  status: string;
  /** The supplier invoice's tax by head (migrations/060), and how the branch prints it. */
  cgst_amount?: string;
  sgst_amount?: string;
  igst_amount?: string;
  cess_amount?: string;
  inter_state?: boolean;
  tax_rows?: TaxRow[];
  /** The partner funding the bill's purchase order (migrations/074). */
  funded_by_partner_id?: string | null;
  funded_by_partner?: string | null;
}

/** A partner (erp-core-go migrations/074). capital only for partners.manage. */
export interface Partner {
  partner_id: string;
  name: string;
  phone: string | null;
  email: string | null;
  pan: string | null;
  share_ratio: string;
  status: "active" | "retired";
  joined_on: string | null;
  left_on: string | null;
  notes: string | null;
  account_code: string;
  capital?: string;
}

/** An expense head and the account it posts to (migrations/074). */
export interface ExpenseHead {
  head_id: string;
  name: string;
  account_id: string;
  account_code: string;
  account_name: string;
  account_type: "asset" | "expense" | string;
  status: "active" | "inactive";
}

export interface Expense {
  expense_id: string;
  expense_number: string;
  branch_id: string;
  branch: string;
  expense_date: string;
  head_id: string;
  head: string;
  account_code: string;
  account_name: string;
  description: string | null;
  vendor: string | null;
  reference: string | null;
  amount: string;
  paid_by: "cash" | "bank" | "partner" | "unpaid";
  bank_account_id: string | null;
  bank_account: string | null;
  partner_id: string | null;
  partner: string | null;
  status: "posted" | "cancelled";
  paid_at: string | null;
  paid_with: string | null;
  cancel_reason: string | null;
  import_batch: string | null;
  created_at: string;
}

// ---------------------------------------------------------------------
// Tax split on bills and invoices (erp-core-go migrations/060,
// internal/taxes): CGST + SGST intra-state, IGST inter-state, or one
// labelled tax (VAT, Sales Tax) for a merchant outside India.
// ---------------------------------------------------------------------

export type TaxRegime = "gst_split" | "single";

export interface TaxRow {
  label: string;
  amount: string;
}

export interface TaxBucket {
  rate: string;
  taxable_value: string;
  cgst: string;
  sgst: string;
  igst: string;
  cess: string;
  tax_amount: string;
}

export interface TaxBreakup {
  regime: TaxRegime;
  tax_label: string;
  inter_state: boolean;
  /** 2-digit GST state code; "" when not known. */
  place_of_supply: string;
  place_of_supply_name: string;
  cgst: string;
  sgst: string;
  igst: string;
  cess: string;
  total: string;
  /** The tax lines to print under the subtotal ("CGST @ 2.5%"). */
  rows: TaxRow[];
  summary: TaxBucket[];
}

export interface TaxEffective {
  regime: TaxRegime;
  tax_label: string;
  state_code: string;
  currency_code: string;
  currency_symbol: string;
}

export interface TaxSettings {
  merchant: { regime: TaxRegime; tax_label: string; state_code: string | null; currency_code: string; currency_symbol: string };
  branches: {
    branch_id: string;
    branch_name: string;
    branch_gstin: string;
    regime: TaxRegime | null;
    tax_label: string | null;
    state_code: string | null;
    /** migrations/061: null = the merchant's currency. */
    currency_code: string | null;
    currency_symbol: string | null;
    effective: TaxEffective;
  }[];
  states: { code: string; name: string }[];
  currencies: { code: string; symbol: string }[];
}

export interface BillSummary {
  bill_id: string;
  bill_number: string;
  supplier_id: string;
  grand_total: string;
  amount_paid: string;
  status: string;
  bill_date: string;
}

export interface PurchaseReturnSummary {
  return_id: string;
  return_number: string;
  supplier_id: string;
  reason: string;
  status: string;
  grand_total: string;
  created_at: string;
}

export interface Account {
  account_id: string;
  code: string;
  name: string;
  account_type: string;
  is_system: boolean;
  status: string;
}

export interface LedgerLine {
  entry_id: string;
  entry_number: string;
  entry_date: string;
  description: string;
  source_type: string;
  account_code: string;
  account_name: string;
  debit: string;
  credit: string;
  running_balance: string;
}

export interface DayBookLine {
  account_code: string;
  account_name: string;
  debit: string;
  credit: string;
}

export interface DayBookEntry {
  entry_number: string;
  source_type: string;
  description: string;
  total_debit: string;
  total_credit: string;
  lines: DayBookLine[];
}

/** GET /accounting/day-book: the totals are summed by the server. */
export interface DayBook {
  entries: DayBookEntry[];
  total_debit: string;
  total_credit: string;
}

/** GET /accounting/cash-book: the totals are summed by the server. */
export interface CashBook {
  lines: LedgerLine[];
  net_movement: string;
  total_debit: string;
  total_credit: string;
}

// Mirrors erp-core-go's internal/promotions.promoConfig — one unified
// shape covering every promo_type's parameters (only the fields relevant
// to a given promo_type are populated), same "one flexible shape over
// type-specific plumbing" choice the Go side made.
export interface VolumeTier {
  min_qty: number;
  max_qty: number; // 0 = unbounded ("11+")
  discount_pct: number;
  // erp-core-go migrations/070: "percent" (or absent) uses discount_pct;
  // "amount_per_unit" is discount_amount off each piece, "amount" off the lot.
  discount_type?: "percent" | "amount_per_unit" | "amount";
  discount_amount?: number;
}

export interface PromotionConfig {
  value_percent?: number;
  value_amount?: number;
  buy_qty?: number;
  get_qty?: number;
  get_discount_pct?: number;
  tiers?: VolumeTier[];
  min_purchase_amount?: number;
  discount_amount?: number;
  discount_pct?: number;
  // buy_x_get_y (erp-core-go migrations/070)
  buy_product_ids?: string[];
  get_product_ids?: string[];
  max_sets?: number;
}

export interface Promotion {
  promotion_id: string;
  name: string;
  promo_type: "percent" | "fixed" | "bogo" | "volume" | "min_value" | "buy_x_get_y";
  application_level: "order" | "product" | "category";
  product_id: string | null;
  category_id: string | null;
  target_segment: "vip" | "regular" | "new" | "dormant" | null;
  config: PromotionConfig;
  stacking: "exclusive" | "stackable";
  starts_at: string | null;
  ends_at: string | null;
  days_of_week: number[] | null; // 0 = Sunday .. 6 = Saturday
  time_start: string | null; // "HH:MM"
  time_end: string | null;
  active: boolean;
  product_name: string | null;
  category_name: string | null;
  // Sales this promotion discounted; a used promotion can't be deleted.
  times_used: number;
  // What the percent or amount is taken from (erp-core-go migrations/065).
  apply_on: "selling_price" | "mrp";
  // Worked out by the API now: running, scheduled, expired or inactive.
  state: "running" | "scheduled" | "expired" | "inactive";
  // A whole-order promotion's optional filters (erp-core-go migrations/069):
  // a line must match every list that isn't empty; all empty = every item.
  scope_categories?: { id: string; name: string }[];
  scope_brands?: { id: string; name: string }[];
  scope_products?: { id: string; name: string }[];
  // buy_x_get_y: the products bought and rewarded, named.
  buy_products?: { id: string; name: string }[];
  get_products?: { id: string; name: string }[];
}

export interface Coupon {
  coupon_id: string;
  code: string;
  promo_type: "percent" | "fixed";
  value: string;
  min_purchase_amount: string | null;
  usage_limit_total: number | null;
  usage_limit_per_customer: number | null;
  usage_count: number;
  valid_from: string | null;
  valid_until: string | null;
  channels: string[] | null;
  branch_id: string | null;
  active: boolean;
}

export interface LoyaltyConfig {
  earn_rupees_per_point: string;
  redeem_points_per_rupee: string;
  expiry_months: number;
}

export interface LoyaltyLedgerEntry {
  entry_type: "earn" | "redeem";
  points: number;
  balance_after: number;
  sales_order_id: string | null;
  created_at: string;
}

export interface LoyaltyBalance {
  customer_id: string;
  available_points: number;
  ledger: LoyaltyLedgerEntry[];
}

// Mirrors internal/notifications — see that package's doc comment for why
// it's content-agnostic (channel/category/status only; body/subject are
// whatever the triggering domain package assembled).
export interface Notification {
  notification_id: string;
  channel: "email" | "sms" | "whatsapp";
  category: "receipt" | "low_stock" | "payment_reminder";
  recipient: string;
  subject: string | null;
  body: string;
  reference_type: string | null;
  reference_id: string | null;
  status: "sent" | "failed";
  error_message: string | null;
  created_at: string;
}

export interface StockLevel {
  branch_id: string;
  variant_id: string;
  on_hand: string;
  reserved: string;
  available: string;
  reorder_point: string;
}

// Mirrors internal/customers/credit.go — Phase 4's B2B Credit Facility.
export interface AgingBuckets {
  current: string;
  days_0_30: string;
  days_30_60: string;
  days_60_90: string;
  days_90_plus: string;
}

export interface OpenInvoice {
  order_id: string;
  order_number: string;
  due_date: string;
  credit_amount: string;
  credit_paid: string;
  outstanding: string;
}

export interface CustomerCredit {
  customer_id: string;
  credit_limit: string;
  payment_terms: "due_on_receipt" | "net_7" | "net_15" | "net_30" | "net_60" | "net_90";
  credit_hold: boolean;
  outstanding_total: string;
  aging: AgingBuckets;
  open_invoices: OpenInvoice[];
}

// Mirrors internal/gst — see that package's own doc comment for the
// disclaimer on gstr1_json/gstr3b_json's exact field shape.
export interface GSTReconciliation {
  computed_gst_output: string;
  ledger_gst_payable: string;
  matches: boolean;
}

export interface GSTR3BSummary {
  period: string;
  outward_taxable_value: string;
  cgst: string;
  sgst: string;
  igst: string;
  cess: string;
  total_outward_tax: string;
  itc_available: string;
  net_tax_payable: string;
}

export interface GSTR3BResponse {
  period: string;
  gstin: string;
  summary: GSTR3BSummary;
  gstr3b_json: unknown;
  reconciliation: GSTReconciliation;
  disclaimer: string;
}

export interface GSTR1Summary {
  period: string;
  b2b_party_count: number;
  b2b_invoice_count: number;
  b2b_taxable_value: string;
  b2c_taxable_value: string;
  total_taxable_value: string;
  total_tax: string;
}

export interface GSTR1Response {
  period: string;
  gstin: string;
  summary: GSTR1Summary;
  gstr1_json: unknown;
  reconciliation: GSTReconciliation;
  disclaimer: string;
  notes: string[];
}

// Mirrors internal/accounting/bank_reconciliation.go.
export interface BankStatementLine {
  line_id: string;
  txn_date: string;
  description: string;
  reference: string;
  amount: string;
  status: "unmatched" | "matched" | "ignored";
  matched_journal_line_id: string | null;
}

export interface BankStatementImportSummary {
  import_id: string;
  filename: string;
  line_count: number;
  matched_count: number;
  imported_at: string;
}

export interface BankStatementImportResult extends BankStatementImportSummary {
  lines: BankStatementLine[];
}

export interface JournalLineCandidate {
  journal_line_id: string;
  entry_date: string;
  description: string;
  source_type: string;
  debit: string;
  credit: string;
}

export interface BankReconciliationReport {
  start: string;
  end: string;
  matched_total: string;
  unmatched_statement_lines: BankStatementLine[];
  unmatched_ledger_lines: JournalLineCandidate[];
}

// Mirrors internal/accounting/payment_gateway_reconciliation.go.
export interface SettlementLine {
  line_id: string;
  settlement_date: string;
  reference: string;
  amount: string;
  status: "unmatched" | "matched" | "duplicate";
  matched_payment_id: string | null;
}

export interface SettlementImportSummary {
  import_id: string;
  gateway: string;
  filename: string;
  line_count: number;
  matched_count: number;
  imported_at: string;
}

export interface SettlementImportResult extends SettlementImportSummary {
  lines: SettlementLine[];
}

export interface PaymentCandidate {
  payment_id: string;
  sales_order_id: string;
  method: string;
  amount: string;
  reference_no: string;
  created_at: string;
}

export interface MissingPayment {
  payment_id: string;
  sales_order_id: string;
  method: string;
  amount: string;
  reference_no: string;
  created_at: string;
}

export interface PaymentGatewayReconciliationReport {
  start: string;
  end: string;
  matched_total: string;
  unmatched_settlement_lines: SettlementLine[];
  duplicate_settlement_lines: SettlementLine[];
  missing_payments: MissingPayment[];
}

// Mirrors internal/accounting/cash_reconciliation.go.
export interface DenominationLine {
  denomination: string;
  count: number;
  subtotal: string;
}

export interface CashReconciliation {
  reconciliation_id: string;
  branch_id: string;
  recon_date: string;
  opening_float: string;
  system_expected: string;
  counted_total: string;
  variance: string;
  reason: string;
  authorized: boolean;
  denominations: DenominationLine[];
  created_at: string;
}

export interface CashReconciliationSummary {
  recon_date: string;
  opening_float: string;
  system_expected: string;
  counted_total: string;
  variance: string;
  authorized: boolean;
  // Phase 13: set when the count is a POS shift close (one till).
  pos_shift_id: string | null;
  terminal_name: string | null;
  shift_number: number | null;
}

// Mirrors internal/inventory/reconciliation.go.
export interface InventoryReconciliationLine {
  variant_id: string;
  system_qty: string;
  counted_qty: string;
  variance: string;
  variance_value: string;
}

export interface InventoryReconciliation {
  reconciliation_id: string;
  branch_id: string;
  recon_type: "cycle" | "full";
  recon_date: string;
  reason: string;
  variance_value: string;
  authorized: boolean;
  lines: InventoryReconciliationLine[];
  created_at: string;
}

export interface InventoryReconciliationSummary {
  reconciliation_id: string;
  recon_type: "cycle" | "full";
  recon_date: string;
  variance_value: string;
  authorized: boolean;
}

// Mirrors internal/reports.
export interface PaymentBreakdown {
  method: string;
  amount: string;
  count: number;
}

export interface DailySalesReport {
  branch_id: string;
  date: string;
  order_count: number;
  subtotal: string;
  discount_total: string;
  tax_total: string;
  grand_total: string;
  /** Checkout round-off waivers (signed sum) and the total after them (= the tenders). */
  round_off_total: string;
  net_total: string;
  by_payment_method: PaymentBreakdown[];
  /** The day's tax by head, as the invoices printed it (migrations/060). */
  cgst_total?: string;
  sgst_total?: string;
  igst_total?: string;
  cess_total?: string;
}

export interface StockSummaryLine {
  variant_id: string;
  sku: string;
  product_name: string;
  on_hand: string;
  reserved: string;
  available: string;
}

export interface EODCashReport {
  branch_id: string;
  date: string;
  cash_total: string;
  cash_order_count: number;
}

export interface BranchSalesLine {
  branch_id: string;
  branch_name: string;
  order_count: number;
  grand_total: string;
  /** After checkout round-off waivers. */
  net_total: string;
}

export interface ConsolidatedSalesReport {
  date: string;
  branches: BranchSalesLine[];
  total_order_count: number;
  total_grand_total: string;
  /** After checkout round-off waivers. */
  total_net_total: string;
}

export interface BranchStockLine {
  branch_id: string;
  on_hand: string;
  reserved: string;
  available: string;
}

export interface ConsolidatedStockEntry {
  variant_id: string;
  sku: string;
  product_name: string;
  by_branch: BranchStockLine[];
  total_on_hand: string;
}

// Mirrors internal/inventory's stockResponse.
export interface StockLevel {
  branch_id: string;
  variant_id: string;
  on_hand: string;
  reserved: string;
  available: string;
  reorder_point: string;
}

// Mirrors internal/catalog/taxonomy.go and products_write.go.
export interface Category {
  category_id: string;
  parent_id: string | null;
  name: string;
  path: string;
}

export interface CatalogBrand {
  brand_id: string;
  name: string;
}

// Phase 8: Vertical Expansion — Apparel. Mirrors internal/catalog/collections.go.
export interface CollectionSummary {
  collection_id: string;
  name: string;
  season: string;
}

// Phase 8: Vertical Expansion — Grocery/FMCG. Mirrors internal/inventory/batches.go.
export interface ExpiringBatch {
  batch_id: string;
  branch_id: string;
  variant_id: string;
  sku: string;
  product_name: string;
  batch_no: string;
  expiry_date: string | null;
  quantity_remaining: string;
  days_until_expiry: number | null;
}

// Phase 8: Vertical Expansion — Pharmacy. Mirrors internal/pharmacy/prescriptions.go.
export interface Prescription {
  prescription_id: string;
  customer_id: string;
  doctor_name: string;
  doctor_reg_no: string;
  notes: string;
  created_at: string;
}

// Phase 8: Vertical Expansion — mirrors internal/catalog/attributes.go.
export interface AttributeValue {
  value_id: string;
  value: string;
  sort_order: number;
}

export interface CatalogAttribute {
  attribute_id: string;
  name: string;
  input_type: "select" | "text" | "number";
  status?: string; // active | inactive (migrations/054)
  values: AttributeValue[];
}

export interface CategoryAttribute extends CatalogAttribute {
  required: boolean;
  unit: string;
  sort_order: number;
}

export interface TaxSlab {
  tax_slab_id: string;
  name: string;
  cgst_rate: string;
  sgst_rate: string;
  igst_rate: string;
  cess_rate: string;
}

export interface NewProductVariantInput {
  sku: string;
  cost_price: number;
  mrp: number;
  selling_price: number;
  track_batch?: boolean;
  plu_code?: string;
  attribute_combo?: Record<string, string>;
  // The real barcode already on the product (manually entered — EAN-13
  // auto-detected/validated server-side for a 13-digit code) and/or this
  // system's own auto-generated one — both can be set together and
  // coexist, see internal/catalog/barcode_assign.go's assignBarcodeInTx.
  original_barcode?: string;
  generate_barcode?: boolean;
}

export interface CreatedProduct {
  product_id: string;
  name: string;
  short_description: string;
  hsn_code: string;
  category_id: string | null;
  brand_id: string | null;
  tax_slab_id: string | null;
  collection_id: string | null;
  product_type: string;
  status: string;
  price_includes_tax: boolean;
  variants: {
    variant_id: string;
    sku: string;
    cost_price: string;
    mrp: string;
    selling_price: string;
    original_barcode: string | null;
    generated_barcode: string | null;
  }[];
}

// Mirrors internal/audit.
export interface AuditLogEntry {
  id: number;
  entity_type: string;
  entity_id: string;
  action: string;
  performed_by: string | null;
  before_value: string | null;
  after_value: string | null;
  reason: string;
  created_at: string;
  checksum: string | null;
}

export interface AuditVerifyResult {
  valid: boolean;
  rows_checked: number;
  legacy_rows_skipped: number;
  broken_at_id: number | null;
  broken_reason: string | null;
}

// Mirrors internal/activity (migrations/051): one row per POS / web-admin request.
export interface ActivityEntry {
  id: number;
  occurred_at: string;
  user_id: string | null;
  user_name: string | null;
  branch_id: string | null;
  branch_name: string | null;
  terminal_id: string | null;
  terminal_name: string | null;
  client: "pos" | "web" | "api";
  method: string;
  route: string;
  path: string;
  module: string;
  entity_id: string | null;
  status_code: number;
  duration_ms: number;
  ip: string | null;
  user_agent: string | null;
  request_id: string | null;
  request_body: unknown;
}

export interface AuditSettings {
  activity_log_enabled: boolean;
  log_read_requests: boolean;
  capture_request_body: boolean;
  disabled_modules: string[];
  updated_at: string | null;
  updated_by: string | null;
  entity_audit_trail: "always_on";
}

// Mirrors internal/softdelete (migrations/050).
export interface RecycleBinItem {
  entity: string;
  id: string;
  label: string;
  deleted_at: string;
  deleted_by: string | null;
  deleted_by_name: string | null;
}

// Mirrors internal/hr/attendance.go (migrations/052).
export interface PunchEvidence {
  latitude?: number;
  longitude?: number;
  accuracy_m?: number;
  ip?: string;
  user_agent?: string;
  source?: "pos" | "web" | "api" | "manual";
  terminal_id?: string;
  distance_m?: number;
  geofence?: "inside" | "outside" | "unknown" | "not_configured";
}

export interface AttendanceRecord {
  id: string;
  user_id: string;
  work_date: string;
  clock_in?: string;
  clock_out?: string;
  status: "present" | "half_day" | "leave" | "absent";
  hours_worked?: string;
  late_minutes?: number;
  early_departure_minutes?: number;
  branch_id?: string;
  branch_name?: string;
  clock_in_evidence: PunchEvidence;
  clock_out_evidence: PunchEvidence;
  corrected_by?: string;
  corrected_at?: string;
  correction_reason?: string;
  // Every check-in/check-out pair of the day, oldest first (migrations/053);
  // the fields above summarise them.
  sessions: AttendanceSession[];
}

export interface AttendanceSession {
  id: string;
  clock_in: string;
  clock_out?: string;
  minutes?: number;
  clock_in_evidence: PunchEvidence;
  clock_out_evidence: PunchEvidence;
  corrected_at?: string;
  correction_reason?: string;
}

export interface AttendanceSettings {
  require_location: boolean;
  geofence_mode: "off" | "flag" | "block";
  allow_web: boolean;
  allow_pos: boolean;
  // Leave, permission and regularization rules (migrations/075).
  leave_year_start_month: number;
  permission_free_count: number;
  permission_free_minutes: number;
  permission_max_minutes: number;
  standard_day_hours: number;
  regularize_within_days: number;
  updated_at: string | null;
}

export interface Geofence {
  branch_id: string;
  branch_name: string;
  latitude: number;
  longitude: number;
  radius_m: number;
  enabled: boolean;
  updated_at: string;
}

export interface MyAttendance {
  open: AttendanceRecord | null;
  open_session_id?: string;
  open_since?: string;
  today: AttendanceRecord | null;
  settings: AttendanceSettings;
  geofence: Geofence | null;
}

export interface AttendanceRegisterRow {
  user_id: string;
  name: string;
  employee_code: string | null;
  designation: string | null;
  home_branch_id: string | null;
  attendance: AttendanceRecord | null;
}

// Mirrors internal/einvoice.
export interface EInvoice {
  sales_order_id: string;
  gsp_provider: string;
  irn: string;
  ack_no: string;
  ack_date: string;
  signed_qr_code: string;
  status: string;
}

export interface EWayBill {
  sales_order_id: string;
  gsp_provider: string;
  ewb_no: string;
  ewb_date: string;
  valid_until: string;
  vehicle_no: string;
  transporter_id: string;
  distance_km: number;
  status: string;
  interstate: boolean;
  required_by_rule: boolean;
}

// Mirrors internal/hr and internal/payroll (Phase 5).
export interface Employee {
  id: string;
  name: string;
  email?: string;
  phone?: string;
  employee_code: string;
  branch_id?: string;
  status: string;
  designation?: string;
  department?: string;
  employment_type: string;
  date_of_joining?: string;
  date_of_exit?: string;
  pan_number?: string;
  uan_number?: string;
  esi_number?: string;
  bank_account_no?: string;
  bank_ifsc?: string;
  shift_id?: string;
  roles: string[];
}

export interface Shift {
  id: string;
  branch_id?: string;
  name: string;
  start_time: string;
  end_time: string;
  status: string;
}

export interface SalaryStructure {
  effective_from: string;
  basic: string;
  hra: string;
  special_allowance: string;
  other_allowances: string;
  pf_applicable: boolean;
}

export interface PTSlab {
  min: number;
  max: number | null;
  amount: number;
}

export interface StatutoryConfig {
  pf_employee_rate: number;
  pf_employer_rate: number;
  pf_wage_ceiling: number;
  esi_employee_rate: number;
  esi_employer_rate: number;
  esi_wage_ceiling: number;
  pt_slabs: PTSlab[];
  lwf_employee_amount: number;
  lwf_employer_amount: number;
  tds_rate_percent: number;
}

export interface CommissionTier {
  min_net_sales: number;
  rate_percent: number;
}

export interface CommissionRule {
  id: string;
  name: string;
  category_id?: string;
  tiers: CommissionTier[];
  status: string;
}

export interface Payslip {
  user_id: string;
  name: string;
  basic: string;
  hra: string;
  special_allowance: string;
  other_allowances: string;
  commission_amount: string;
  gross_earnings: string;
  days_in_period: string;
  days_present: string;
  pf_employee: string;
  pf_employer: string;
  esi_employee: string;
  esi_employer: string;
  pt_amount: string;
  tds_amount: string;
  lwf_employee: string;
  lwf_employer: string;
  total_deductions: string;
  net_pay: string;
  // Employee loan instalments recovered from this pay (migrations/067).
  loan_deduction?: string;
  // Leave and permission (migrations/075): what reduced days_present.
  leave_paid_days?: string;
  leave_unpaid_days?: string;
  absent_days?: string;
  permission_unpaid_minutes?: number;
  payslip_id?: string;
  payroll_run_id?: string;
  period_month?: number;
  period_year?: number;
  run_status?: string;
}

// A payslip to print (GET /payroll/payslips/{id}, /payroll/runs/{id}/payslips).
export interface PayslipDocument extends Payslip {
  employee: {
    employee_code: string | null;
    designation: string | null;
    department: string | null;
    branch_id: string | null;
    branch: string | null;
    date_of_joining: string | null;
    pan_number: string | null;
    uan_number: string | null;
    esi_number: string | null;
    bank_account_no: string | null;
    bank_ifsc: string | null;
  };
  company: { name: string; address: string[] | null; phone?: string; email?: string; gstin?: string };
  period: string;
  net_pay_words: string;
  currency: { code: string; symbol: string };
}

// Leave, permission and attendance regularization (erp-core-go §3.78,
// internal/hr/leave.go, permission.go, regularization.go).
export type RequestStatus = "pending" | "approved" | "rejected" | "cancelled";

export interface LeaveType {
  leave_type_id: string;
  code: string;
  name: string;
  paid: boolean;
  annual_quota: number | null;
  allow_half_day: boolean;
  status: "active" | "inactive";
  sort_order: number;
}

export interface LeaveBalance {
  leave_type_id: string;
  code: string;
  name: string;
  paid: boolean;
  status: string;
  entitled: number | null;
  override: boolean;
  taken: number;
  unpaid: number;
  pending: number;
  available: number | null;
}

export interface PermissionUsage {
  month: string;
  free_count: number;
  free_minutes: number;
  max_minutes: number;
  used_count: number;
  used_free_minutes: number;
  unpaid_minutes: number;
  pending_count: number;
  remaining_count: number;
  remaining_minutes: number;
}

export interface LeaveBalances {
  user_id: string;
  name: string;
  leave_year: number;
  year_from: string;
  year_to: string;
  balances: LeaveBalance[];
  permission: PermissionUsage;
}

interface HrRequestBase {
  request_number: string;
  user_id: string;
  employee: string;
  employee_code: string | null;
  branch_id: string | null;
  branch: string | null;
  reason: string;
  status: RequestStatus;
  source: "pos" | "web" | "api";
  requested_by: string | null;
  requested_at: string;
  decided_by: string | null;
  decided_at: string | null;
  decision_note: string | null;
  cancelled_by: string | null;
  cancelled_at: string | null;
  cancel_reason: string | null;
}

export interface LeaveRequest extends HrRequestBase {
  leave_request_id: string;
  leave_type_id: string;
  leave_type: string;
  leave_code: string;
  paid_type: boolean;
  leave_year: number;
  from_date: string;
  to_date: string;
  half_day: boolean;
  days: number;
  paid_days: number | null;
  unpaid_days: number | null;
}

export interface PermissionRequest extends HrRequestBase {
  permission_request_id: string;
  work_date: string;
  from_time: string;
  to_time: string;
  minutes: number;
  paid_minutes: number | null;
  unpaid_minutes: number | null;
}

export interface Regularization extends HrRequestBase {
  regularization_id: string;
  work_date: string;
  session_id: string | null;
  clock_in: string | null;
  clock_out: string | null;
  before_clock_in: string | null;
  before_clock_out: string | null;
  applied_session_id: string | null;
}

export interface PayrollRun {
  id: string;
  period_month: number;
  period_year: number;
  status: string;
  total_gross: string;
  total_deductions: string;
  total_net: string;
  finalized_at?: string;
  skipped_employees?: string[];
  payslips?: Payslip[];
}

export interface ChallanSummary {
  period_month: number;
  period_year: number;
  pf_total: string;
  pf_employer_eps: string;
  pf_employer_epf: string;
  esi_total: string;
  pt_total: string;
  tds_total: string;
  lwf_total: string;
}

// Mirrors internal/rbac (UACL — role/permission administration).
export interface RBACPermission {
  id: string;
  code: string;
  description: string;
}

export interface RBACRole {
  id: string;
  name: string;
  is_system_default: boolean;
  permission_codes: string[];
  user_count: number;
  // Phase 15: all branches' data, or only the holder's own branch(es).
  data_scope: "all" | "branch";
}

// Mirrors internal/ai (Phase 6 v1 — reorder suggestions & recommendations).
export interface ReorderSuggestion {
  branch_id: string;
  variant_id: string;
  product_name: string;
  sku: string;
  on_hand: string;
  reserved: string;
  available: string;
  reorder_point: string;
  daily_velocity: string;
  days_of_stock_remaining: string | null;
  suggested_reorder_qty: string;
  reason: string;
  // erp-core-go §3.77: who supplied it last and at what cost (null if never received), and its cost price.
  last_supplier_id?: string | null;
  last_supplier_name?: string | null;
  last_unit_cost?: string | null;
  cost_price?: string;
}

export interface Recommendation {
  variant_id: string;
  product_name: string;
  sku: string;
  selling_price: string;
  co_occurrence_count: number;
  confidence: string;
}

// Mirrors internal/ai/nlpbi.go (Phase 6 v1 — NLP-BI).
export interface ChartPoint {
  label: string;
  value: string;
}

export interface ChartSpec {
  type: "bar" | "stat";
  title: string;
  series?: ChartPoint[];
  value?: string;
}

export interface AskResponse {
  question: string;
  intent: string;
  answer: string;
  chart: ChartSpec;
  data: unknown;
}

// Mirrors internal/ai/copilot.go (Phase 6 v1 — AI Copilot).
export interface CopilotMessage {
  role: "user" | "assistant";
  content: string;
}

// Phase 7: Wholesale/B2B & Omnichannel — mirrors internal/pricing/price_lists.go.
export interface PriceListSummary {
  price_list_id: string;
  name: string;
  item_count: number;
  // Customers on this list; a list with customers can't be deleted.
  customer_count: number;
}

export interface PriceListItem {
  variant_id: string;
  sku: string;
  product_name: string;
  price: string;
}

export interface PriceListDetail {
  price_list_id: string;
  name: string;
  items: PriceListItem[];
}

// Mirrors internal/quotations/handlers.go.
export type QuotationStatus = "draft" | "sent" | "accepted" | "rejected" | "expired" | "converted";

export interface QuotationLine {
  line_id: string;
  variant_id: string;
  sku: string;
  product_name: string;
  quantity: string;
  unit_price: string;
  tax_amount: string;
  line_total: string;
}

export interface Quotation {
  quotation_id: string;
  quote_number: string;
  branch_id: string;
  customer_id: string;
  status: QuotationStatus;
  valid_until: string;
  subtotal: string;
  tax_total: string;
  grand_total: string;
  notes: string;
  converted_sales_order_id: string | null;
  created_at: string;
  lines: QuotationLine[];
  tax_breakup?: TaxBreakup;
}

export interface QuotationSummary {
  quotation_id: string;
  quote_number: string;
  customer_id: string;
  status: QuotationStatus;
  valid_until: string;
  grand_total: string;
  created_at: string;
}

// Phase 10 (erp-core-go internal/pricing/schedule.go, rules.go)
export type ScheduledChangeStatus = "pending" | "applied" | "cancelled" | "failed";

export interface ScheduledPriceChange {
  scheduled_change_id: string;
  variant_id: string;
  sku: string;
  product_name: string;
  // null = this field isn't changed by the schedule
  cost_price: string | null;
  mrp: string | null;
  selling_price: string | null;
  effective_at: string;
  reason: string | null;
  override: boolean;
  status: ScheduledChangeStatus;
  failure_reason: string | null;
  created_at: string;
  applied_at: string | null;
}

export interface PriceRule {
  price_rule_id: string;
  name: string;
  variant_id: string | null;
  sku: string | null;
  product_name: string | null;
  category_id: string | null;
  category_name: string | null;
  branch_id: string | null;
  rule_type: "fixed_price" | "percent";
  value: string;
  priority: number;
  starts_at: string | null;
  ends_at: string | null;
  days_of_week: number[] | null; // 0 = Sunday .. 6 = Saturday
  time_start: string | null; // "HH:MM", branch-local
  time_end: string | null;
  active: boolean;
  created_at: string;
  // Sale lines this rule priced; a used rule can be deactivated, not edited or deleted.
  times_used: number;
}

export interface EffectivePrice {
  variant_id: string;
  at: string;
  unit_price: string;
  base_selling_price: string;
  source: "price_list" | "price_rule" | "markup_rule" | "mrp" | "base";
  price_rule_id: string | null;
  price_rule_name?: string;
  // erp-core-go migrations/071
  markup_rule_id?: string | null;
  markup_rule_name?: string;
  price_basis?: "price_list" | "price_rule" | "markup" | "selling_price" | "mrp";
  price_benefit?: "standard" | "customer" | "shop";
}

/** A markup rule (erp-core-go migrations/071): cost + percent or amount. */
export interface MarkupRule {
  markup_rule_id: string;
  name: string;
  variant_id: string | null;
  sku: string | null;
  product_name: string | null;
  category_id: string | null;
  category_name: string | null;
  branch_id: string | null;
  branch_name: string | null;
  markup_type: "percent" | "fixed";
  value: string;
  priority: number;
  starts_at: string | null;
  ends_at: string | null;
  days_of_week: number[] | null;
  time_start: string | null;
  time_end: string | null;
  active: boolean;
  created_at: string;
  times_used: number;
}

// GET /products/{id} (erp-core-go internal/catalog/products_edit.go) —
// everything the product edit screen needs in one call.
export interface ProductDetailVariant {
  variant_id: string;
  sku: string;
  attribute_combo: Record<string, string | number>;
  cost_price: string;
  mrp: string;
  selling_price: string;
  track_serial: boolean;
  track_batch: boolean;
  plu_code: string | null;
  status: VariantStatus;
  original_barcode: string | null;
  generated_barcode: string | null;
  // Stock across all branches — batch tracking can only be switched at 0.
  on_hand_total: string;
  // Phase 13: null = fixed price; otherwise the cashier types a price in
  // [min, max], and above approval_above a manager approves.
  open_price: OpenPrice | null;
  // Every barcode with its pack (erp-core-go migrations/076).
  barcodes?: BarcodeInfo[];
}

// Units of measure (erp-core-go §3.79, internal/catalog/units.go).
export type UnitKind = "count" | "weight" | "volume" | "length" | "area" | "pack";

export interface Unit {
  unit_id: string;
  code: string;
  name: string;
  symbol: string;
  kind: UnitKind;
  factor: number | null; // in kg / L / m / sq m / pcs; null for a pack unit
  decimals: number;
  is_system: boolean;
  status: "active" | "inactive";
  sort_order: number;
  products: number;
}

export interface UnitRef {
  unit_id: string;
  code: string;
  symbol: string;
  name: string;
  kind: UnitKind;
  decimals: number;
}

export interface BarcodeInfo {
  code: string;
  symbology: string;
  source: "original" | "generated";
  is_primary: boolean;
  pack_qty: string; // base units one scan adds
  pack_unit_id: string | null;
  pack_size: string | null;
  pack_label: string; // "Tray of 30 pcs", "500 g"; "" for one base unit
}

export interface OpenPrice {
  min: string;
  max: string;
  approval_above: string | null;
}

export interface ProductDetail {
  product_id: string;
  name: string;
  short_description: string;
  hsn_code: string;
  category_id: string | null;
  category_name: string | null;
  brand_id: string | null;
  brand_name: string | null;
  tax_slab_id: string | null;
  tax_slab_name: string | null;
  collection_id: string | null;
  collection_name: string | null;
  product_type: string;
  status: ProductStatus;
  price_includes_tax: boolean;
  updated_at: string;
  variants: ProductDetailVariant[];
  unit?: UnitRef | null;
  /** The POS tile colour, #RRGGBB (erp-core-go migrations/063); null = none of its own. */
  pos_color?: string | null;
}

// ---------------------------------------------------------------------
// Phase 14: back-office sales (erp-core-go internal/sales history.go,
// detail.go, returns.go). Money stays a string end to end.
// ---------------------------------------------------------------------

export type SaleChannel = "pos" | "online" | "backoffice";

export interface SaleSummaryRow {
  order_id: string;
  order_number: string;
  status: string;
  finalized_at: string | null;
  branch_name: string;
  terminal_name: string;
  cashier_name: string;
  customer_name: string | null;
  item_count: string;
  grand_total: string;
  /** Checkout round-off waiver, signed ("-0.75"), and what was paid. */
  round_off: string;
  payable_total: string;
  payment_methods: string[];
  channel: SaleChannel;
}

export interface SalesSummary {
  order_count: number;
  voided_count: number;
  refunded_count: number;
  /** After checkout round-off waivers, so it matches the tenders. */
  net_sales: string;
  round_off_total: string;
  discount_total: string;
  tax_total: string;
  cgst_total?: string;
  sgst_total?: string;
  igst_total?: string;
  by_payment_method: { method: string; amount: string; count: number }[];
}

export interface SaleDetailLine {
  line_id: string;
  sku: string;
  product_name: string;
  quantity: string;
  returned_quantity: string;
  unit_price: string;
  discount_amount: string;
  taxable_value: string;
  tax_rate: string;
  tax_amount: string;
  cgst_amount?: string;
  sgst_amount?: string;
  igst_amount?: string;
  cess_amount?: string;
  hsn_code?: string;
  line_total: string;
  open_price: boolean;
  price_approved_by: string | null;
  unit_symbol?: string; // erp-core-go migrations/076
}

export interface SaleDetailPayment {
  payment_id: string;
  method: string;
  amount: string;
  reference: string;
  status: string;
  created_at: string;
  corrections: { from_method: string; to_method: string; reason: string; performed_by: string; approved_by: string; created_at: string }[];
}

export interface SaleReturn {
  return_id: string;
  return_number: string;
  refund_method: string;
  refund_amount: string;
  /** The part of the sale's round-off waiver this return gave back (refund = goods' value less it). */
  round_off: string;
  tax_amount?: string;
  /** The credit note's tax, split as the invoice charged it (migrations/060). */
  tax_breakup?: TaxBreakup;
  restocked: boolean;
  approved_by: string | null;
  created_at: string;
}

export interface SaleDetail {
  order_id: string;
  order_number: string;
  status: string;
  channel: SaleChannel;
  branch_id: string;
  branch_name: string;
  terminal_name: string;
  cashier_name: string;
  customer_id: string | null;
  customer_name: string | null;
  customer_phone: string | null;
  customer_gstin?: string | null;
  created_at: string;
  finalized_at: string | null;
  device_created_at: string | null;
  subtotal: string;
  discount_total: string;
  tax_total: string;
  grand_total: string;
  /** Checkout round-off waiver, signed, and grand_total + round_off (what was paid). */
  round_off: string;
  payable_total: string;
  credit_amount: string;
  credit_paid: string;
  lines: SaleDetailLine[];
  tax_summary: { rate: string; taxable_value: string; tax_amount: string; cgst: string; sgst: string; igst?: string; cess?: string }[];
  tax_breakup?: TaxBreakup;
  payments: SaleDetailPayment[];
  /** value_percent is null for a rupee-amount discount (fixed manual, promotion, loyalty). */
  discounts: { type: string; value_percent: string | null; discount_amount: string; applied_by: string; authorized_by: string | null; reason: string }[];
  returns: SaleReturn[];
  journal: { entry_number: string; entry_date: string; source_type: string; description: string; amount: string }[];
  audit: { entity_type: string; action: string; performed_by: string | null; reason: string; created_at: string }[];
}

export interface ReturnableOrder {
  order_id: string;
  order_number: string;
  status: string;
  returnable: boolean;
  window_open: boolean;
  requires_approval: boolean;
  // Phase 15: refunds at or below this need no approver; null = every refund.
  approval_above: number | null;
  credit_outstanding: string;
  /** The sale's checkout waiver (signed) and how much of it returns already gave back. */
  round_off: string;
  round_off_returned: string;
  lines: { line_id: string; product_name: string; sku: string; sold_quantity: string; returned_quantity: string; returnable_quantity: string; unit_refund: string }[];
}

// The cart as POST /sales/orders and its line endpoints return it.
export interface CartOrder {
  order_id: string;
  order_number: string;
  status: string;
  channel: SaleChannel;
  subtotal: string;
  discount_total: string;
  tax_total: string;
  grand_total: string;
  /** Set at checkout: the waiver (signed) and what was payable after it. */
  round_off: string;
  payable_total: string;
  /** product_name is an open-price line's own description when it has one (description), else catalog_name (migrations/062). */
  lines: { line_id: string; variant_id: string; sku: string; product_name: string; quantity: string; unit_price: string; discount_amount: string; tax_amount: string; line_total: string; open_price?: boolean; description?: string | null; catalog_name?: string; unit_symbol?: string; unit_decimals?: number }[];
  tax_breakup?: TaxBreakup;
  /** The delivery state set on the cart (migrations/061); null = the usual rule. */
  ship_to_state?: string | null;
}

// ---------------------------------------------------------------------
// Phase 14: journal vouchers and the ledger (erp-core-go
// internal/accounting/vouchers.go).
// ---------------------------------------------------------------------

export type VoucherStatus = "draft" | "submitted" | "approved" | "posted" | "reversed";

export interface VoucherLine {
  line_no: number;
  account_code: string;
  account_name: string;
  debit: string;
  credit: string;
  party_type: "customer" | "supplier" | null;
  party_id: string | null;
  party_name: string | null;
  memo: string | null;
}

export interface Voucher {
  voucher_id: string;
  voucher_number: string;
  status: VoucherStatus;
  branch_id: string | null;
  entry_date: string;
  description: string;
  total: string;
  created_by: string;
  created_by_id: string;
  submitted_by: string | null;
  approved_by: string | null;
  posted_by: string | null;
  reversed_by: string | null;
  rejection_note: string | null;
  reversal_reason: string | null;
  entry_number: string | null;
  reversal_entry_number: string | null;
  created_at: string;
  lines?: VoucherLine[];
}

export interface LedgerEntry {
  entry_id: string;
  entry_number: string;
  entry_date: string;
  source_type: string;
  source_id: string | null;
  description: string;
  branch_name: string | null;
  amount: string;
  created_by: string | null;
  lines?: {
    account_code: string;
    account_name: string;
    debit: string;
    credit: string;
    party_type: "customer" | "supplier" | null;
    party_id: string | null;
    party_name: string | null;
  }[];
}

export interface AccountingSettings {
  jv_approval_required: boolean;
  locked_through: string | null;
}

// ---------------------------------------------------------------------
// Bank accounts and cheques (erp-core-go migrations/066, internal/banking)
// ---------------------------------------------------------------------

export interface ChequeLayout {
  offset_x_mm: number;
  offset_y_mm: number;
  signatory_line?: string;
}

export interface BankAccount {
  bank_account_id: string;
  branch_id: string | null; // null = merchant level
  branch_name: string | null;
  account_name: string;
  bank_name: string;
  bank_branch: string;
  account_number: string;
  ifsc: string;
  account_type: "current" | "savings" | "overdraft" | "cash_credit";
  ledger_code: string;
  ledger_name: string;
  balance: string;
  is_default: boolean;
  status: "active" | "inactive";
  cheque_layout: ChequeLayout;
  notes: string;
  cheque_books: number;
  leaves_left: number;
  display_name: string;
  created_at: string;
}

export interface ChequeBook {
  cheque_book_id: string;
  bank_account_id: string;
  series_from: number;
  series_to: number;
  leaf_digits: number;
  received_on: string | null;
  status: "active" | "closed";
  notes: string;
  used: number;
  next_leaf: string;
  created_at: string;
}

export interface IssuedCheque {
  cheque_id: string;
  bank_account_id: string;
  bank_account: string;
  cheque_book_id: string | null;
  cheque_number: string;
  cheque_date: string;
  payee_name: string;
  amount: string;
  account_payee: boolean;
  party_type: "supplier" | "employee" | null;
  party_id: string | null;
  purchase_bill_id: string | null;
  bill_number: string | null;
  bill_payment_id: string | null;
  memo: string;
  status: "issued" | "cleared" | "bounced" | "cancelled";
  cleared_on: string | null;
  bounced_on: string | null;
  status_reason: string;
  print_count: number;
  created_at: string;
}

export interface ReceivedCheque {
  cheque_id: string;
  branch_id: string;
  customer_id: string | null;
  customer_name: string | null;
  sales_order_id: string | null;
  order_number: string | null;
  drawer_name: string;
  drawer_bank: string;
  drawer_branch: string;
  cheque_number: string;
  cheque_date: string;
  amount: string;
  status: "received" | "deposited" | "cleared" | "bounced" | "returned";
  post_dated: boolean;
  deposit_bank_account_id: string | null;
  deposit_bank_account: string | null;
  deposited_on: string | null;
  cleared_on: string | null;
  bounced_on: string | null;
  status_reason: string;
  notes: string;
  created_at: string;
}

// ---------------------------------------------------------------------
// Employee loans (erp-core-go migrations/067, internal/loans)
// ---------------------------------------------------------------------

export interface LoanInstallment {
  seq: number;
  due_month: number;
  due_year: number;
  principal_part: number;
  interest_part: number;
  amount: number;
  status: "pending" | "recovered";
  recovered_on: string | null;
  recovered_via: string | null;
}

export interface LoanRepayment {
  repayment_id: string;
  amount: string;
  principal_part: string;
  interest_part: string;
  method: string;
  reference_no: string;
  paid_on: string;
  foreclosure: boolean;
  payroll_run_id: string | null;
}

export interface EmployeeLoan {
  loan_id: string;
  loan_number: string;
  user_id: string;
  employee_name: string;
  employee_code: string | null;
  branch_id: string | null;
  loan_type: "loan" | "advance";
  purpose: string;
  principal: string;
  interest_rate: string;
  tenure_months: number;
  total_interest: string;
  emi_amount: string;
  start_month: number;
  start_year: number;
  disbursed_on: string;
  disbursement_method: "cash" | "bank_transfer" | "cheque";
  bank_account_id: string | null;
  issued_cheque_id: string | null;
  status: "active" | "closed" | "foreclosed";
  closed_on: string | null;
  outstanding_principal: string;
  outstanding_interest: string;
  recovered: string;
  installments_left: number;
  created_at: string;
  schedule?: LoanInstallment[];
  repayments?: LoanRepayment[];
}

// Farm management (erp-core-go §3.81, internal/farm).
export type FarmSpecies = "chicken" | "goat" | "rabbit" | "cow" | "pig" | "fish";

export interface FarmHouse {
  house_id: string;
  branch_id: string;
  branch: string;
  name: string;
  kind: "shed" | "pen" | "cage" | "paddock" | "pond" | "tank" | "other";
  species: FarmSpecies;
  capacity: number | null;
  area_sqft: number | null;
  notes: string | null;
  status: "active" | "inactive";
  occupied: number;
  active_batch: string | null;
}

export interface FarmKPI {
  age_days: number;
  alive: number;
  dead: number;
  culled: number;
  harvested: number;
  mortality_pct: number;
  feed_kg: number;
  feed_cost: number;
  health_cost: number;
  stock_cost: number;
  other_cost: number;
  output_value: number;
  net_cost: number;
  cost_per_head: number | null;
  latest_avg_weight_kg: number | null;
  latest_weighed_on: string | null;
  live_weight_kg: number | null;
  harvested_kg: number;
  fcr: number | null;
  feed_per_head_kg: number | null;
  eggs_total: number;
  eggs_today: number;
  laying_7d_pct: number | null;
  milk_total_l: number;
  outputs: { variant_id: string; product: string; unit: string; quantity: number; value: number }[];
  vaccines_due: number;
  // §3.82
  born: number;
  added: number;
  moved_out: number;
  on_register: number;
  feed_today: FarmRation[];
  feed_planned_kg: number | null;
  feed_vs_plan_pct: number | null;
  biomass_kg: number | null;
  withdrawal: FarmWithdrawalDates;
  animals_in_withdrawal: number;
}

export interface FarmRation {
  feed_name: string;
  product_id: string | null;
  product: string | null;
  kg: number | null;
}

/** The dates meat, milk and eggs may be taken again (null = none pending). */
export interface FarmWithdrawalDates {
  meat_until: string | null;
  milk_until: string | null;
  eggs_until: string | null;
}

export interface FarmBatch {
  batch_id: string;
  batch_number: string;
  name: string | null;
  branch_id: string;
  branch: string;
  house_id: string | null;
  house: string | null;
  species: FarmSpecies;
  purpose: string;
  breed: string | null;
  supplier_id: string | null;
  supplier: string | null;
  source: string | null;
  placed_on: string;
  age_at_placement_days: number;
  initial_count: number;
  initial_avg_weight_kg: number | null;
  schedule_id: string | null;
  schedule: string | null;
  feed_plan_id: string | null;
  feed_plan: string | null;
  status: "active" | "closed";
  closed_on: string | null;
  close_note: string | null;
  notes: string | null;
  kpi: FarmKPI;
}

export interface FarmDue {
  schedule_item_id: string;
  day_of_age: number;
  kind: "vaccination" | "medicine" | "task";
  name: string;
  method: string | null;
  due_on: string;
  state: "done" | "overdue" | "due_today" | "upcoming" | "before_placement" | "not_done";
  done_on: string | null;
  event_id: string | null;
  repeat_every_days: number | null;
}

export type FarmEventType =
  | "placement"
  | "mortality"
  | "cull"
  | "feed"
  | "medicine"
  | "vaccination"
  | "weight"
  | "production"
  | "expense"
  | "note"
  | "birth"
  | "addition"
  | "transfer_in"
  | "transfer_out";

export type FarmProduceKind = "meat" | "milk" | "eggs" | "other";

export interface FarmEvent {
  event_id: string;
  batch_id: string;
  batch_number: string;
  branch_id: string;
  event_date: string;
  event_type: FarmEventType;
  head_count: number | null;
  variant_id: string | null;
  product: string | null;
  unit: string | null;
  quantity: number | null;
  unit_cost: number | null;
  amount: number | null;
  avg_weight_kg: number | null;
  sample_size: number | null;
  cause: string | null;
  vaccine: string | null;
  method: string | null;
  schedule_item_id: string | null;
  notes: string | null;
  journal_entry_id: string | null;
  status: "posted" | "cancelled";
  cancel_reason: string | null;
  created_by: string | null;
  created_at: string;
  animal_id: string | null;
  animal_tag: string | null;
  produce_kind: FarmProduceKind | null;
  dose: string | null;
  course_days: number | null;
  vet: string | null;
  withdrawal_meat_until: string | null;
  withdrawal_milk_until: string | null;
  withdrawal_eggs_until: string | null;
  withdrawal_override: string | null;
  related_batch: string | null;
}

export interface FarmSchedule {
  schedule_id: string;
  species: FarmSpecies;
  purpose: string | null;
  name: string;
  is_system: boolean;
  status: "active" | "inactive";
  items: {
    schedule_item_id: string;
    day_of_age: number;
    kind: string;
    name: string;
    method: string | null;
    notes: string | null;
    repeat_every_days: number | null;
  }[];
}

// Farm, round 2 (erp-core-go §3.82, migrations/079).
export interface FarmBreedState {
  status: "open" | "served" | "pregnant" | "male" | "";
  last_service_on: string | null;
  expected_birth_on: string | null;
  last_birth_on: string | null;
  lactating: boolean;
  days_in_milk: number | null;
  births: number;
}

export interface FarmAnimal {
  animal_id: string;
  tag: string;
  name: string | null;
  species: FarmSpecies;
  sex: "female" | "male";
  breed: string | null;
  born_on: string | null;
  age_days: number | null;
  origin: "existing" | "purchased" | "born";
  joined_on: string;
  branch_id: string;
  branch: string;
  batch_id: string;
  batch_number: string;
  house: string | null;
  dam_id: string | null;
  dam_tag: string | null;
  sire_id: string | null;
  sire_tag: string | null;
  sire_note: string | null;
  status: "active" | "dead" | "culled" | "harvested";
  exit_on: string | null;
  notes: string | null;
  latest_weight_kg: number | null;
  weighed_on: string | null;
  breeding: FarmBreedState;
  milk_7d_avg_l: number | null;
  withdrawal: FarmWithdrawalDates;
}

export type FarmBreedingType = "heat" | "service" | "pregnancy_check" | "birth" | "abortion" | "dry_off";

export interface FarmBreedingEvent {
  breeding_id: string;
  event_date: string;
  event_type: FarmBreedingType;
  method: "natural" | "ai" | null;
  sire_id: string | null;
  sire_tag: string | null;
  sire_note: string | null;
  result: "positive" | "negative" | null;
  born_alive: number | null;
  born_dead: number | null;
  notes: string | null;
  status: "posted" | "cancelled";
  created_by: string | null;
}

export interface FarmAnimalDetail extends FarmAnimal {
  events: FarmEvent[];
  breeding_events: FarmBreedingEvent[];
  milk: { date: string; am: number; pm: number; day: number; litres: number }[];
  young: FarmAnimal[];
}

export interface FarmMilkSheet {
  date: string;
  batch_id: string;
  batch_number: string;
  batch_withdrawal_milk_until: string | null;
  animals: {
    animal_id: string;
    tag: string;
    name: string | null;
    lactating: boolean;
    days_in_milk: number | null;
    am: number | null;
    pm: number | null;
    day: number | null;
    withdrawal_milk_until: string | null;
  }[];
}

export interface FarmFeedPlanItem {
  plan_item_id: string;
  day_from: number;
  day_to: number | null;
  feed_name: string;
  product_id: string | null;
  product: string | null;
  grams_per_head: number | null;
  pct_body_weight: number | null;
}

export interface FarmFeedPlan {
  feed_plan_id: string;
  species: FarmSpecies;
  purpose: string | null;
  name: string;
  basis: "per_head" | "pct_biomass";
  is_system: boolean;
  status: "active" | "inactive";
  items: FarmFeedPlanItem[];
  batches: number;
}

export interface FarmFeedStockRow {
  branch_id: string;
  branch: string;
  product_id: string;
  product: string;
  unit: string;
  free: number;
  planned_daily: number | null;
  used_daily: number;
  days_left: number | null;
  low: boolean;
}

export interface FarmRecipe {
  recipe_id: string;
  name: string;
  output_variant_id: string;
  output: string;
  output_unit: string;
  batch_size: number;
  notes: string | null;
  status: "active" | "inactive";
  items: { variant_id: string; product: string; unit: string; quantity: number; unit_cost: number }[];
  cost_per_batch: number;
  cost_per_unit: number;
}

export interface FarmMix {
  mix_id: string;
  mix_number: string;
  branch_id: string;
  branch: string;
  recipe_id: string | null;
  recipe: string | null;
  output_variant_id: string;
  output: string;
  output_unit: string;
  output_qty: number;
  total_cost: number;
  unit_cost: number;
  mixed_on: string;
  notes: string | null;
  status: "posted" | "cancelled";
  cancel_reason: string | null;
  created_by: string | null;
  lines: { variant_id: string; product: string; unit: string; quantity: number; unit_cost: number; amount: number }[];
}

export interface FarmItem {
  farm_item_id: string;
  product_id: string;
  product: string;
  unit: string;
  variant_id: string | null;
  role: "feed" | "medicine" | "vaccine";
  species: FarmSpecies[];
  withdrawal_meat_days: number | null;
  withdrawal_milk_days: number | null;
  withdrawal_eggs_days: number | null;
  dose_note: string | null;
  notes: string | null;
}

export interface FarmMedicineRow {
  branch_id: string;
  branch: string;
  product_id: string;
  product: string;
  role: "medicine" | "vaccine";
  unit: string;
  free: number;
  next_expiry: string | null;
  expired_qty: number;
  expiring_qty: number;
  used_30d: number;
  withdrawal_meat_days: number | null;
  withdrawal_milk_days: number | null;
  withdrawal_eggs_days: number | null;
}

export interface FarmWithdrawalRow {
  event_id: string;
  batch_id: string;
  batch_number: string;
  branch_id: string;
  house: string | null;
  species: FarmSpecies;
  animal_id: string | null;
  animal_tag: string | null;
  treatment: string;
  treated_on: string;
  meat_until: string | null;
  milk_until: string | null;
  eggs_until: string | null;
}

export interface FarmWaterLog {
  water_log_id: string;
  branch_id: string;
  house_id: string;
  house: string;
  logged_on: string;
  logged_time: string | null;
  do_mg_l: number | null;
  ph: number | null;
  temp_c: number | null;
  ammonia_mg_l: number | null;
  nitrite_mg_l: number | null;
  transparency_cm: number | null;
  notes: string | null;
  created_by: string | null;
  alerts: string[];
}

export interface FarmBirthDue {
  animal_id: string;
  tag: string;
  name: string | null;
  species: FarmSpecies;
  batch_number: string;
  served_on: string;
  expected_on: string;
  status: "served" | "pregnant";
}

// Selling price suggestions (erp-core-go §3.83, migrations/080).
export interface SuggestionSettings {
  enabled: boolean;
  cost_includes_tax: boolean;
  can_manage: boolean;
  rule_count: number;
}

export type SuggestionMethod = "cost" | "mrp" | "lower" | "higher";
export type SuggestionRounding = "none" | "up_1" | "up_5" | "up_10" | "end_9" | "end_99" | "charm";

export interface SuggestionRule {
  rule_id: string;
  name: string;
  category_id: string | null;
  category: string | null;
  brand_id: string | null;
  brand: string | null;
  method: SuggestionMethod;
  cost_type: "margin" | "markup";
  cost_pct: number | null;
  mrp_discount_pct: number | null;
  min_margin_pct: number;
  rounding: SuggestionRounding;
  priority: number;
  active: boolean;
  created_at: string;
}

export type SuggestionStatus = "ok" | "at_mrp" | "check" | "hold" | "no_cost" | "no_rule";

export interface PriceSuggestion {
  key: string;
  variant_id?: string;
  sku?: string;
  product_name?: string;
  current_price: number;
  cost_price: number;
  mrp: number;
  suggested_price: number | null;
  status: SuggestionStatus;
  margin_pct: number | null;
  rule_id: string | null;
  rule: string | null;
  note: string;
}
