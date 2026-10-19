// How a document's tax reads on screen (erp-core-go migrations/060,
// internal/taxes): the same rows and summary the printed invoice carries —
// CGST + SGST intra-state, IGST inter-state, or one labelled tax for a
// merchant outside India. Without a breakup (an older API) it falls back to
// the single "Tax" line.

import type { TaxBreakup, TaxBucket } from "@/lib/types";
import { rupees } from "@/lib/format";

function fmtRate(r: string | number) {
  return String(Math.round(Number(r) * 100) / 100);
}

/** The tax lines of a totals list, as dt/dd pairs for a two-column <dl>. */
export function TaxRowsDl({ breakup, taxTotal, label = "Tax" }: { breakup?: TaxBreakup | null; taxTotal: string; label?: string }) {
  const rows = breakup?.rows?.length ? breakup.rows : [{ label, amount: taxTotal }];
  return (
    <>
      {rows.map((r) => (
        <div key={r.label} className="contents" data-testid="tax-row">
          <dt className="text-muted-foreground">{r.label}</dt>
          <dd className="tabular text-right">{rupees(r.amount)}</dd>
        </div>
      ))}
    </>
  );
}

/** "Place of supply: 29 – Karnataka · inter-state (IGST)" for a GST invoice; nothing otherwise. */
export function PlaceOfSupply({ breakup }: { breakup?: TaxBreakup | null }) {
  if (!breakup || breakup.regime === "single" || !breakup.place_of_supply) return null;
  return (
    <span data-testid="place-of-supply">
      Place of supply {breakup.place_of_supply}
      {breakup.place_of_supply_name ? ` – ${breakup.place_of_supply_name}` : ""}
      {breakup.inter_state ? " · inter-state (IGST)" : " · intra-state (CGST + SGST)"}
    </span>
  );
}

/** The rate-wise summary table. */
export function TaxSummaryTable({ breakup, summary }: { breakup?: TaxBreakup | null; summary?: TaxBucket[] }) {
  const rows = breakup?.summary ?? summary ?? [];
  const single = breakup?.regime === "single";
  const inter = !single && !!breakup?.inter_state;
  const label = breakup?.tax_label || "GST";
  const hasCess = !single && rows.some((r) => Number(r.cess) !== 0);
  return (
    <table className="w-full text-sm" aria-label={`${label} by rate`}>
      <thead className="text-left text-xs text-muted-foreground">
        <tr>
          <th className="pb-2 font-medium">Rate</th>
          <th className="pb-2 text-right font-medium">Taxable value</th>
          {!single && !inter && (
            <>
              <th className="pb-2 text-right font-medium">CGST</th>
              <th className="pb-2 text-right font-medium">SGST</th>
            </>
          )}
          {inter && <th className="pb-2 text-right font-medium">IGST</th>}
          {hasCess && <th className="pb-2 text-right font-medium">Cess</th>}
          <th className="pb-2 text-right font-medium">Total {single ? label : "tax"}</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((t) => (
          <tr key={t.rate} className="border-t">
            <td className="py-1.5">
              {fmtRate(t.rate)}%
              {!single && !inter && <span className="text-xs text-muted-foreground"> ({fmtRate(Number(t.rate) / 2)} + {fmtRate(Number(t.rate) / 2)})</span>}
            </td>
            <td className="tabular py-1.5 text-right">{rupees(t.taxable_value)}</td>
            {!single && !inter && (
              <>
                <td className="tabular py-1.5 text-right">{rupees(t.cgst)}</td>
                <td className="tabular py-1.5 text-right">{rupees(t.sgst)}</td>
              </>
            )}
            {inter && <td className="tabular py-1.5 text-right">{rupees(t.igst)}</td>}
            {hasCess && <td className="tabular py-1.5 text-right">{rupees(t.cess)}</td>}
            <td className="tabular py-1.5 text-right">{rupees(t.tax_amount)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

/** Whether lines should show CGST/SGST (or IGST) columns. */
export function splitColumns(breakup?: TaxBreakup | null): "intra" | "inter" | null {
  if (!breakup || breakup.regime === "single") return null;
  return breakup.inter_state ? "inter" : "intra";
}
