// Mirrors erp-core-go's JSON response shapes exactly (internal/purchase,
// internal/accounting, internal/branches) — money/quantity fields stay as
// strings, same reasoning as api-client.ts's header comment.

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
