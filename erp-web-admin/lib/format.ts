// Display-only formatting. Money arrives from the API as NUMERIC strings and
// goes back as given; nothing here feeds a value back into a request.

const inr = new Intl.NumberFormat("en-IN", { minimumFractionDigits: 2, maximumFractionDigits: 2 });

/** "123456.5" -> "₹1,23,456.50" (Indian grouping). */
export function rupees(amount: string | number | null | undefined): string {
  const n = typeof amount === "number" ? amount : Number(amount ?? 0);
  if (Number.isNaN(n)) return `₹${amount}`;
  return `${n < 0 ? "−" : ""}₹${inr.format(Math.abs(n))}`;
}

/** "2.000" -> "2", "0.500" -> "0.5". */
export function qty(q: string | number): string {
  return String(Number(q));
}

export function dateTime(iso: string | null | undefined): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "—" : d.toLocaleString("en-IN", { day: "numeric", month: "short", year: "numeric", hour: "2-digit", minute: "2-digit" });
}

export const STATUS_LABEL: Record<string, string> = {
  finalized: "Completed",
  voided: "Voided",
  refunded: "Refunded",
  partially_refunded: "Partly refunded",
  held: "On hold",
  held_voided: "Released",
  cart: "In progress",
};

export const CHANNEL_LABEL: Record<string, string> = { pos: "Till", online: "Online", backoffice: "Back office" };

export const METHOD_LABEL: Record<string, string> = { cash: "Cash", card: "Card", upi: "UPI", credit: "Credit" };

// What made a journal entry (journal_entries.source_type).
export const SOURCE_LABEL: Record<string, string> = {
  sale: "Sale",
  sale_void: "Sale void",
  sales_return: "Sales return",
  payment_correction: "Payment correction",
  purchase_bill: "Purchase bill",
  bill_payment: "Bill payment",
  purchase_return: "Purchase return",
  inventory_adjustment: "Stock adjustment",
  inventory_reconciliation: "Stock count",
  manual: "Journal voucher",
  manual_reversal: "Voucher reversal",
  receivable_payment: "Customer payment",
  cash_reconciliation: "Cash count",
  cash_movement: "Cash in/out",
  payment_gateway_settlement: "Gateway settlement",
  payment_gateway_settlement_reversal: "Gateway reversal",
};
