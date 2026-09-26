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
  margin_pct: number | null;
  markup_pct: number | null;
}

export interface Product {
  product_id: string;
  name: string;
  hsn_code: string;
  variants: ProductVariant[];
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
  entry_date: string;
  description: string;
  source_type: string;
  debit: string;
  credit: string;
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
  lines: DayBookLine[];
}

// Mirrors erp-core-go's internal/promotions.promoConfig — one unified
// shape covering every promo_type's parameters (only the fields relevant
// to a given promo_type are populated), same "one flexible shape over
// type-specific plumbing" choice the Go side made.
export interface VolumeTier {
  min_qty: number;
  max_qty: number; // 0 = unbounded ("11+")
  discount_pct: number;
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
}

export interface Promotion {
  promotion_id: string;
  name: string;
  promo_type: "percent" | "fixed" | "bogo" | "volume" | "min_value";
  application_level: "order" | "product" | "category";
  product_id: string | null;
  category_id: string | null;
  target_segment: "vip" | "regular" | "new" | "dormant" | null;
  config: PromotionConfig;
  stacking: "exclusive" | "stackable";
  starts_at: string | null;
  ends_at: string | null;
  active: boolean;
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
  by_payment_method: PaymentBreakdown[];
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
}

export interface ConsolidatedSalesReport {
  date: string;
  branches: BranchSalesLine[];
  total_order_count: number;
  total_grand_total: string;
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
  variants: { variant_id: string; sku: string; cost_price: string; mrp: string; selling_price: string }[];
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
