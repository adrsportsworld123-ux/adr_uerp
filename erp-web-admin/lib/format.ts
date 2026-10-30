// Display-only formatting. Money arrives from the API as NUMERIC strings and
// goes back as given; nothing here feeds a value back into a request.

import { currencyPrefix, groupMoney } from "@/lib/currency";
import { merchantTimeZone, t } from "@/lib/i18n";

/** "123456.5" -> "₹1,23,456.50" for INR, "$123,456.50" for a merchant in USD (lib/currency). */
export function rupees(amount: string | number | null | undefined): string {
  const n = typeof amount === "number" ? amount : Number(amount ?? 0);
  if (Number.isNaN(n)) return `${currencyPrefix()}${amount}`;
  return `${n < 0 ? "−" : ""}${currencyPrefix()}${groupMoney(Math.abs(n))}`;
}

/** "2.000" -> "2", "0.500" -> "0.5". */
export function qty(q: string | number): string {
  return String(Number(q));
}

/**
 * Today (or `offsetDays` from it) as YYYY-MM-DD on the merchant's business
 * clock (Localization, migrations/064; Asia/Kolkata unless set), not UTC and
 * not the browser's own zone, so a default date is the day the reports use.
 */
export function localDate(offsetDays = 0): string {
  const today = new Intl.DateTimeFormat("en-CA", { timeZone: safeZone(), year: "numeric", month: "2-digit", day: "2-digit" }).format(new Date());
  if (offsetDays === 0) return today;
  const [y, m, d] = today.split("-").map(Number);
  const shifted = new Date(Date.UTC(y, m - 1, d + offsetDays));
  return shifted.toISOString().slice(0, 10);
}

/** The merchant's zone, or the browser's when the name is one this browser doesn't know. */
function safeZone(): string | undefined {
  const zone = merchantTimeZone();
  try {
    new Intl.DateTimeFormat("en", { timeZone: zone });
    return zone;
  } catch {
    return undefined;
  }
}

/** Debit/credit side of one ledger line, e.g. "Dr ₹1,200.00". */
export function drCr(debit: string, credit: string): string {
  return Number(debit) > 0 ? `Dr ${rupees(debit)}` : `Cr ${rupees(credit)}`;
}

export function dateTime(iso: string | null | undefined): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return Number.isNaN(d.getTime())
    ? "—"
    : d.toLocaleString("en-IN", { day: "numeric", month: "short", year: "numeric", hour: "2-digit", minute: "2-digit", timeZone: safeZone() });
}

/**
 * A code → label map whose labels read in the merchant's language (t()),
 * looked up as they are used, so a language change applies at once.
 */
function translated(labels: Record<string, string>): Record<string, string> {
  return new Proxy(labels, {
    get: (target, prop) => (typeof prop === "string" && Object.hasOwn(target, prop) ? t(target[prop]) : undefined),
  });
}

export const STATUS_LABEL: Record<string, string> = translated({
  finalized: "Completed",
  voided: "Voided",
  refunded: "Refunded",
  partially_refunded: "Partly refunded",
  held: "On hold",
  held_voided: "Released",
  cart: "In progress",
});

export const CHANNEL_LABEL: Record<string, string> = translated({ pos: "Till", online: "Online", backoffice: "Back office" });

export const METHOD_LABEL: Record<string, string> = translated({ cash: "Cash", card: "Card", upi: "UPI", credit: "Credit" });

// What made a journal entry (journal_entries.source_type).
export const SOURCE_LABEL: Record<string, string> = translated({
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
});

/** Minutes the merchant's zone is ahead of UTC at instant `ms`. */
function zoneOffsetMinutes(ms: number): number {
  const parts = new Intl.DateTimeFormat("en-US", {
    timeZone: safeZone(),
    hourCycle: "h23",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).formatToParts(new Date(ms));
  const n = (type: string) => Number(parts.find((p) => p.type === type)?.value);
  return Math.round((Date.UTC(n("year"), n("month") - 1, n("day"), n("hour"), n("minute")) - Math.floor(ms / 60000) * 60000) / 60000);
}

/**
 * A day and a time typed on the merchant's clock ("2026-10-18", "18:00") as
 * an RFC 3339 instant, whatever the browser's own zone. `nextDay` for a
 * check-out past midnight.
 */
export function merchantStamp(date: string, hm: string, nextDay = false): string {
  const [y, m, d] = date.split("-").map(Number);
  const [hh, mm] = hm.split(":").map(Number);
  const wall = Date.UTC(y, m - 1, d + (nextDay ? 1 : 0), hh, mm);
  let at = wall - zoneOffsetMinutes(wall) * 60000;
  at = wall - zoneOffsetMinutes(at) * 60000; // across a DST change
  return new Date(at).toISOString();
}

/** An instant's time of day on the merchant's clock, "18:05". */
export function merchantTime(iso: string | null | undefined): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "—" : new Intl.DateTimeFormat("en-GB", { timeZone: safeZone(), hour: "2-digit", minute: "2-digit", hourCycle: "h23" }).format(d);
}

/** "2026-10-18" → "18 Oct 2026". */
export function shortDate(ymd: string | null | undefined): string {
  if (!ymd) return "—";
  const [y, m, d] = ymd.split("-").map(Number);
  return new Date(Date.UTC(y, m - 1, d)).toLocaleDateString("en-IN", { day: "numeric", month: "short", year: "numeric", timeZone: "UTC" });
}
