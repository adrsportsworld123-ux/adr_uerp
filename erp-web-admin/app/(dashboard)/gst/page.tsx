"use client";

// Phase 4, sub-area 2's admin surface (erp-core-go's phased_roadmap.md /
// docs/phase0_1_design.md §3.14). A viewer, not a filer: the merchant (or
// their CA/GSP) still uploads gstr1_json/gstr3b_json to the GST portal
// themselves — this screen's job is showing the computed summary,
// flagging whether it reconciles against the ledger, and making the raw
// export JSON easy to copy out. See the API's own `disclaimer` field
// (rendered as-is below, not paraphrased) for why the JSON's exact field
// shape needs validating against the GST portal's offline tool before a
// real filing — this app can't do that validation itself.
import { useCallback, useEffect, useState } from "react";
import { api, ApiError } from "@/lib/api-client";
import { GSTR1Response, GSTR3BResponse } from "@/lib/types";
import { localDate, rupees } from "@/lib/format";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { PageHeader } from "@/components/data/page-header";
import { Field, FilterBar, Panel, Stat } from "@/components/data/panel";
import { StatusChip } from "@/components/data/status-chip";

export default function GSTReturnsPage() {
  const [period, setPeriod] = useState(localDate().slice(0, 7)); // YYYY-MM
  const [gstr1, setGstr1] = useState<GSTR1Response | null>(null);
  const [gstr3b, setGstr3b] = useState<GSTR3BResponse | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(() => {
    Promise.all([
      api.get<GSTR1Response>(`/api/v1/gst/gstr1?period=${period}`),
      api.get<GSTR3BResponse>(`/api/v1/gst/gstr3b?period=${period}`),
    ])
      .then(([r1, r3b]) => {
        setGstr1(r1);
        setGstr3b(r3b);
        setError(null);
      })
      .catch((e) => {
        setGstr1(null);
        setGstr3b(null);
        setError(e instanceof ApiError ? e.message : "Could not reach the server");
      });
  }, [period]);
  useEffect(load, [load]);

  return (
    <div className="flex max-w-5xl flex-col gap-5">
      <PageHeader
        title="GST Returns"
        description="GSTR-3B and GSTR-1 computed from the books, checked against the ledger. Copy the JSON out to file on the GST portal."
      />
      <FilterBar>
        <Field label="Period" htmlFor="period">
          <Input id="period" type="month" value={period} onChange={(e) => setPeriod(e.target.value)} className="w-40" />
        </Field>
      </FilterBar>

      {error && (
        <p className="text-sm text-destructive" role="alert">
          {error}
        </p>
      )}

      {gstr3b && (
        <Panel title="GSTR-3B — monthly summary">
          <div className="grid grid-cols-2 gap-4 sm:grid-cols-3">
            <Stat label="Outward taxable value" value={rupees(gstr3b.summary.outward_taxable_value)} />
            <Stat label="Total outward tax" value={rupees(gstr3b.summary.total_outward_tax)} />
            <Stat label="Net tax payable" value={rupees(gstr3b.summary.net_tax_payable)} strong />
            <Stat label="CGST / SGST / IGST" value={`${rupees(gstr3b.summary.cgst)} / ${rupees(gstr3b.summary.sgst)} / ${rupees(gstr3b.summary.igst)}`} />
            <Stat label="ITC available" value={rupees(gstr3b.summary.itc_available)} />
          </div>
          <ReconciliationRow reconciliation={gstr3b.reconciliation} />
          <JsonPanel label="gstr3b_json" value={gstr3b.gstr3b_json} />
        </Panel>
      )}

      {gstr1 && (
        <Panel title="GSTR-1 — outward supplies">
          <div className="grid grid-cols-2 gap-4 sm:grid-cols-3">
            <Stat label="B2B parties / invoices" value={`${gstr1.summary.b2b_party_count} / ${gstr1.summary.b2b_invoice_count}`} />
            <Stat label="B2B taxable value" value={rupees(gstr1.summary.b2b_taxable_value)} />
            <Stat label="B2C taxable value" value={rupees(gstr1.summary.b2c_taxable_value)} />
            <Stat label="Total taxable value" value={rupees(gstr1.summary.total_taxable_value)} strong />
            <Stat label="Total tax" value={rupees(gstr1.summary.total_tax)} />
          </div>
          <ReconciliationRow reconciliation={gstr1.reconciliation} />
          {gstr1.notes.length > 0 && (
            <div className="flex flex-col gap-1 rounded-lg bg-warn-soft p-3">
              <p className="text-xs font-medium text-warn">Known gaps in this export:</p>
              <ul className="list-inside list-disc text-xs text-warn">
                {gstr1.notes.map((n, i) => (
                  <li key={i}>{n}</li>
                ))}
              </ul>
            </div>
          )}
          <JsonPanel label="gstr1_json" value={gstr1.gstr1_json} />
        </Panel>
      )}

      {(gstr1 || gstr3b) && <p className="text-xs text-muted-foreground">{gstr1?.disclaimer ?? gstr3b?.disclaimer}</p>}
    </div>
  );
}

function ReconciliationRow({ reconciliation }: { reconciliation: { computed_gst_output: string; ledger_gst_payable: string; matches: boolean } }) {
  return (
    <div className="flex flex-wrap items-center gap-3 border-t pt-3 text-sm">
      <StatusChip status={reconciliation.matches ? "reconciled" : "mismatch"} tone={reconciliation.matches ? "ok" : "bad"} label={reconciliation.matches ? "Reconciled" : "Mismatch"} />
      <span className="tabular text-muted-foreground">
        Computed {rupees(reconciliation.computed_gst_output)} vs. ledger {rupees(reconciliation.ledger_gst_payable)}
      </span>
    </div>
  );
}

function JsonPanel({ label, value }: { label: string; value: unknown }) {
  const [copied, setCopied] = useState(false);
  const json = JSON.stringify(value, null, 2);

  async function copy() {
    try {
      await navigator.clipboard.writeText(json);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // Clipboard access can be denied by the browser — the JSON is still
      // visible below to select and copy manually.
    }
  }

  return (
    <details className="rounded-lg border p-3">
      <summary className="flex cursor-pointer items-center justify-between text-sm font-medium">
        <span className="font-mono">{label}</span>
        <Button size="sm" variant="outline" onClick={(e) => { e.preventDefault(); copy(); }}>
          {copied ? "Copied" : "Copy"}
        </Button>
      </summary>
      <pre className="mt-2 max-h-96 overflow-x-auto rounded bg-muted/50 p-2 text-xs">{json}</pre>
    </details>
  );
}
