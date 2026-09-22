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
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";

function currentPeriod() {
  return new Date().toISOString().slice(0, 7); // YYYY-MM
}

export default function GSTReturnsPage() {
  const [period, setPeriod] = useState(currentPeriod());
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
    <div className="flex flex-col gap-6 max-w-4xl">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">GST Returns</h1>
        <div className="flex items-center gap-2">
          <Label htmlFor="period" className="text-sm">
            Period
          </Label>
          <Input id="period" type="month" value={period} onChange={(e) => setPeriod(e.target.value)} className="w-40" />
        </div>
      </div>

      {error && <p className="text-sm text-red-600">{error}</p>}

      {gstr3b && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">GSTR-3B — monthly summary</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            <dl className="grid grid-cols-3 gap-3 text-sm">
              <dt className="text-zinc-500">Outward taxable value</dt>
              <dt className="text-zinc-500">Total outward tax</dt>
              <dt className="text-zinc-500">Net tax payable</dt>
              <dd>₹{gstr3b.summary.outward_taxable_value}</dd>
              <dd>₹{gstr3b.summary.total_outward_tax}</dd>
              <dd className="font-semibold">₹{gstr3b.summary.net_tax_payable}</dd>
              <dt className="text-zinc-500">CGST / SGST / IGST</dt>
              <dt className="text-zinc-500">ITC available</dt>
              <dt></dt>
              <dd>
                ₹{gstr3b.summary.cgst} / ₹{gstr3b.summary.sgst} / ₹{gstr3b.summary.igst}
              </dd>
              <dd>₹{gstr3b.summary.itc_available}</dd>
              <dd></dd>
            </dl>
            <ReconciliationRow reconciliation={gstr3b.reconciliation} />
            <JsonPanel label="gstr3b_json" value={gstr3b.gstr3b_json} />
          </CardContent>
        </Card>
      )}

      {gstr1 && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">GSTR-1 — outward supplies</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            <dl className="grid grid-cols-3 gap-3 text-sm">
              <dt className="text-zinc-500">B2B parties / invoices</dt>
              <dt className="text-zinc-500">B2B taxable value</dt>
              <dt className="text-zinc-500">B2C taxable value</dt>
              <dd>
                {gstr1.summary.b2b_party_count} / {gstr1.summary.b2b_invoice_count}
              </dd>
              <dd>₹{gstr1.summary.b2b_taxable_value}</dd>
              <dd>₹{gstr1.summary.b2c_taxable_value}</dd>
              <dt className="text-zinc-500">Total taxable value</dt>
              <dt className="text-zinc-500">Total tax</dt>
              <dt></dt>
              <dd className="font-semibold">₹{gstr1.summary.total_taxable_value}</dd>
              <dd>₹{gstr1.summary.total_tax}</dd>
              <dd></dd>
            </dl>
            <ReconciliationRow reconciliation={gstr1.reconciliation} />
            {gstr1.notes.length > 0 && (
              <div className="flex flex-col gap-1">
                <p className="text-xs font-medium text-zinc-600">Known gaps in this export:</p>
                <ul className="list-disc list-inside text-xs text-zinc-500">
                  {gstr1.notes.map((n, i) => (
                    <li key={i}>{n}</li>
                  ))}
                </ul>
              </div>
            )}
            <JsonPanel label="gstr1_json" value={gstr1.gstr1_json} />
          </CardContent>
        </Card>
      )}

      {(gstr1 || gstr3b) && (
        <p className="text-xs text-zinc-500">{gstr1?.disclaimer ?? gstr3b?.disclaimer}</p>
      )}
    </div>
  );
}

function ReconciliationRow({ reconciliation }: { reconciliation: { computed_gst_output: string; ledger_gst_payable: string; matches: boolean } }) {
  return (
    <div className="flex items-center gap-3 text-sm border-t pt-3">
      <Badge variant={reconciliation.matches ? "default" : "destructive"}>{reconciliation.matches ? "Reconciled" : "Mismatch"}</Badge>
      <span className="text-zinc-600">
        Computed ₹{reconciliation.computed_gst_output} vs. ledger ₹{reconciliation.ledger_gst_payable}
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
    <details className="border rounded-lg p-3">
      <summary className="text-sm font-medium cursor-pointer flex items-center justify-between">
        <span>{label}</span>
        <Button size="sm" variant="outline" onClick={(e) => { e.preventDefault(); copy(); }}>
          {copied ? "Copied" : "Copy"}
        </Button>
      </summary>
      <pre className="text-xs bg-zinc-50 rounded p-2 mt-2 overflow-x-auto max-h-96">{json}</pre>
    </details>
  );
}
