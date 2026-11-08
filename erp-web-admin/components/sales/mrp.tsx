// MRP on the sales grid and the savings under the totals (erp-core-go
// migrations/081). The MRP is information only: no total includes it.
// A line's mrp is the MRP when it went into the cart (null without one);
// mrp_discount is MRP × quantity less the price with GST, before any
// discount. The branch's show_mrp_discount setting decides whether
// "Discount on MRP" and "You saved" show.

import { rupees } from "@/lib/format";

export interface MrpFields {
  mrp?: string | null;
  mrp_discount?: string;
}

export interface MrpSavings {
  mrp_discount_total?: string;
  total_savings?: string;
  show_mrp_discount?: boolean;
}

/** The MRP in small text under a price, struck through when the item sells below it. */
export function MrpNote({ line }: { line: MrpFields }) {
  if (!line.mrp) return null;
  const below = Number(line.mrp_discount ?? 0) > 0;
  return (
    <div className="text-xs font-normal text-muted-foreground" data-testid="line-mrp">
      MRP <span className={below ? "line-through" : undefined}>{rupees(line.mrp)}</span>
    </div>
  );
}

/** "Discount on MRP −₹100" under an item, when the branch shows it. */
export function MrpDiscountNote({ line, show }: { line: MrpFields; show?: boolean }) {
  if (!show || !(Number(line.mrp_discount ?? 0) > 0)) return null;
  return (
    <div className="text-xs font-medium text-pay" data-testid="line-mrp-discount">
      Discount on MRP −{rupees(line.mrp_discount)}
    </div>
  );
}

/** The savings rows of a totals <dl> (two columns), when the branch shows them. */
export function SavingsRowsDl({ order }: { order?: MrpSavings | null }) {
  if (!order?.show_mrp_discount || !(Number(order.total_savings ?? 0) > 0)) return null;
  return (
    <>
      {Number(order.mrp_discount_total ?? 0) > 0 && (
        <>
          <dt className="text-muted-foreground">Discount on MRP</dt>
          <dd className="tabular text-right text-pay" data-testid="sale-mrp-discount">
            {rupees(order.mrp_discount_total)}
          </dd>
        </>
      )}
      <dt className="font-medium text-pay">You saved</dt>
      <dd className="tabular text-right font-medium text-pay" data-testid="sale-you-saved">
        {rupees(order.total_savings)}
      </dd>
    </>
  );
}
