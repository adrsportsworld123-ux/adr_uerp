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
