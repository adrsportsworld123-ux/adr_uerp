"use client";

// Phase 4, sub-area 4's second reconciliation surface (erp-core-go's
// phased_roadmap.md / docs/phase0_1_design.md §3.17). CSV only, same
// scope note as Bank Reconciliation's screen. Gated writes (import,
// match, unmatch) are attempted and surfaced as a toast error on 403,
// same pattern every other gated screen in this app already uses.
import { Fragment, useCallback, useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import {
  PaymentGatewayReconciliationReport,
  SettlementImportResult,
  SettlementImportSummary,
  SettlementLine,
  PaymentCandidate,
} from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { PageHeader } from "@/components/data/page-header";
import { Field, Panel } from "@/components/data/panel";
import { StatusChip } from "@/components/data/status-chip";
import { METHOD_LABEL, localDate, rupees } from "@/lib/format";


export default function PaymentGatewayReconciliationPage() {
  // A match/unmatch made from either section below has to invalidate the
  // other — both render on this one page at once, unlike GST/Cash
  // Reconciliation's single-widget screens, so without this a match made
  // in one section leaves the other showing stale data (e.g. a payment
  // the Import section just matched still listed as "missing" in the
  // Report section beneath it) with nothing on screen to suggest a
  // refresh is needed. Found live: an e2e run matched a settlement line
  // from the Import section and the Report section's already-rendered
  // "missing payments" list kept the same payment listed, unchanged,
  // right next to it. `refreshToken` is a version counter bumped by
  // either section; both re-fetch whenever it changes.
  const [refreshToken, setRefreshToken] = useState(0);
  const bump = useCallback(() => setRefreshToken((v) => v + 1), []);
  return (
    <div className="flex max-w-5xl flex-col gap-5">
      <PageHeader title="Payment Gateway Reconciliation" description="Import a gateway settlement, match each line to a captured card or UPI payment, and find payments that never settled." />
      <ImportSection refreshToken={refreshToken} onMatchChanged={bump} />
      <ReconciliationReportSection refreshToken={refreshToken} onMatchChanged={bump} />
    </div>
  );
}

// ---------------------------------------------------------------------
// Import + imported-lines viewer with inline matching
// ---------------------------------------------------------------------

function ImportSection({ refreshToken, onMatchChanged }: { refreshToken: number; onMatchChanged: () => void }) {
  const fileInput = useRef<HTMLInputElement>(null);
  const [gateway, setGateway] = useState("Razorpay");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [imports, setImports] = useState<SettlementImportSummary[]>([]);
  const [activeImport, setActiveImport] = useState<SettlementImportResult | null>(null);

  const loadImports = useCallback(() => {
    api
      .get<{ imports: SettlementImportSummary[] }>("/api/v1/accounting/payment-gateway/settlement/imports")
      .then((d) => setImports(d.imports))
      .catch(() => {});
  }, []);
  useEffect(loadImports, [loadImports, refreshToken]);
  // A match/unmatch made from the Report section's own candidate picker
  // (below) changes this import's matched_count and this section's own
  // activeImport view, if one is open — refresh both, not just the
  // imports list above.
  useEffect(() => {
    if (activeImport) loadLines(activeImport.import_id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [refreshToken]);

  async function loadLines(importId: string) {
    try {
      const lines = await api.get<{ lines: SettlementLine[] }>(`/api/v1/accounting/payment-gateway/settlement/lines?import_id=${importId}`);
      const summary = imports.find((i) => i.import_id === importId);
      setActiveImport({
        import_id: importId,
        gateway: summary?.gateway ?? "",
        filename: summary?.filename ?? "",
        line_count: summary?.line_count ?? lines.lines.length,
        matched_count: lines.lines.filter((l) => l.status === "matched").length,
        imported_at: summary?.imported_at ?? "",
        lines: lines.lines,
      });
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not load settlement lines");
    }
  }

  async function handleFile(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    if (!file) return;
    setBusy(true);
    setError(null);
    try {
      const csv = await file.text();
      const result = await api.post<SettlementImportResult>("/api/v1/accounting/payment-gateway/settlement/import", {
        gateway,
        filename: file.name,
        csv,
      });
      toast.success(`Imported ${result.line_count} line(s), ${result.matched_count} auto-matched`);
      setActiveImport(result);
      loadImports();
      if (result.matched_count > 0) onMatchChanged();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not import settlement");
    } finally {
      setBusy(false);
      if (fileInput.current) fileInput.current.value = "";
    }
  }

  return (
    <Panel title="Import a payment gateway settlement">
      <p className="text-xs text-muted-foreground">
        CSV only, columns in order: settlement_date (YYYY-MM-DD), reference (the gateway&apos;s UTR/transaction id), amount
        (net of the gateway&apos;s own fee). Auto-matches against captured card/UPI payments by exact reference on import;
        unmatched or duplicate lines are left for manual review below.
      </p>
      <div className="flex flex-wrap items-end gap-3">
        <Field label="Gateway" htmlFor="gateway">
          <Input id="gateway" value={gateway} onChange={(e) => setGateway(e.target.value)} disabled={busy} className="w-40" />
        </Field>
        <Field label="Settlement CSV" htmlFor="settlement-file">
          <Input id="settlement-file" ref={fileInput} type="file" accept=".csv,text/csv" onChange={handleFile} disabled={busy} className="max-w-xs" />
        </Field>
        {busy && <span className="pb-2 text-sm text-muted-foreground">Importing…</span>}
      </div>
      {error && <p className="text-sm text-destructive">{error}</p>}

      {imports.length > 0 && (
        <div className="flex flex-col gap-2 border-t pt-4">
          <p className="text-xs font-medium text-muted-foreground">Past imports</p>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Gateway</TableHead>
                <TableHead>File</TableHead>
                <TableHead>Lines</TableHead>
                <TableHead>Matched</TableHead>
                <TableHead>Imported</TableHead>
                <TableHead></TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {imports.map((imp) => (
                <TableRow key={imp.import_id}>
                  <TableCell>{imp.gateway}</TableCell>
                  <TableCell>{imp.filename || "—"}</TableCell>
                  <TableCell>{imp.line_count}</TableCell>
                  <TableCell>
                    {imp.matched_count} / {imp.line_count}
                  </TableCell>
                  <TableCell className="text-xs text-muted-foreground">{imp.imported_at}</TableCell>
                  <TableCell>
                    <Button size="sm" variant="outline" onClick={() => loadLines(imp.import_id)}>
                      View
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      {activeImport && (
        <div className="border-t pt-4" data-testid="import-lines">
          <SettlementLinesTable
            lines={activeImport.lines}
            onChanged={() => {
              loadLines(activeImport.import_id);
              onMatchChanged();
            }}
          />
        </div>
      )}
    </Panel>
  );
}

function SettlementLinesTable({ lines, onChanged }: { lines: SettlementLine[]; onChanged: () => void }) {
  const [pickingFor, setPickingFor] = useState<string | null>(null);
  const [candidates, setCandidates] = useState<PaymentCandidate[]>([]);

  async function openPicker(lineId: string) {
    try {
      const res = await api.get<{ candidates: PaymentCandidate[] }>(`/api/v1/accounting/payment-gateway/settlement/lines/${lineId}/candidates`);
      setCandidates(res.candidates);
      setPickingFor(lineId);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not load match candidates");
    }
  }

  async function match(lineId: string, paymentId: string) {
    try {
      await api.post(`/api/v1/accounting/payment-gateway/settlement/lines/${lineId}/match`, { payment_id: paymentId });
      toast.success("Matched");
      setPickingFor(null);
      onChanged();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not match line");
    }
  }

  async function unmatch(lineId: string) {
    try {
      await api.post(`/api/v1/accounting/payment-gateway/settlement/lines/${lineId}/unmatch`, {});
      toast.success("Unmatched");
      onChanged();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not unmatch line");
    }
  }

  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Date</TableHead>
          <TableHead>Reference</TableHead>
          <TableHead className="text-right">Amount</TableHead>
          <TableHead>Status</TableHead>
          <TableHead></TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {lines.map((l) => (
          <Fragment key={l.line_id}>
            <TableRow>
              <TableCell className="text-xs">{l.settlement_date}</TableCell>
              <TableCell className="font-mono text-xs">{l.reference}</TableCell>
              <TableCell className="tabular text-right">{rupees(l.amount)}</TableCell>
              <TableCell>
                <StatusChip status={l.status} tone={l.status === "matched" ? "ok" : l.status === "duplicate" ? "bad" : "warn"} />
              </TableCell>
              <TableCell>
                {l.status === "matched" ? (
                  <Button size="sm" variant="ghost" onClick={() => unmatch(l.line_id)}>
                    Unmatch
                  </Button>
                ) : (
                  <Button size="sm" variant="outline" onClick={() => openPicker(l.line_id)}>
                    Match
                  </Button>
                )}
              </TableCell>
            </TableRow>
            {pickingFor === l.line_id && (
              <TableRow>
                <TableCell colSpan={5} className="bg-muted/50">
                  <div className="flex flex-col gap-2 py-2">
                    <p className="text-xs text-muted-foreground">Unclaimed captured card/UPI payments within a week of this line&apos;s date:</p>
                    {candidates.length === 0 && <p className="text-sm text-muted-foreground">No candidates found.</p>}
                    {candidates.map((c) => (
                      <div key={c.payment_id} className="flex items-center gap-3 text-sm">
                        <span className="text-xs text-muted-foreground">{c.created_at}</span>
                        <StatusChip status={c.method} tone="muted" label={METHOD_LABEL[c.method] ?? c.method} />
                        <span className="font-mono text-xs">{c.reference_no || "—"}</span>
                        <span className="tabular">{rupees(c.amount)}</span>
                        <Button size="sm" onClick={() => match(l.line_id, c.payment_id)}>
                          Select
                        </Button>
                      </div>
                    ))}
                    <Button size="sm" variant="ghost" className="w-fit" onClick={() => setPickingFor(null)}>
                      Cancel
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            )}
          </Fragment>
        ))}
        {lines.length === 0 && (
          <TableRow>
            <TableCell colSpan={5} className="text-center text-sm text-muted-foreground py-6">
              No lines
            </TableCell>
          </TableRow>
        )}
      </TableBody>
    </Table>
  );
}

// ---------------------------------------------------------------------
// Reconciliation report
// ---------------------------------------------------------------------

function ReconciliationReportSection({ refreshToken, onMatchChanged }: { refreshToken: number; onMatchChanged: () => void }) {
  const [start, setStart] = useState(localDate(-30));
  const [end, setEnd] = useState(localDate());
  const [report, setReport] = useState<PaymentGatewayReconciliationReport | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(() => {
    api
      .get<PaymentGatewayReconciliationReport>(`/api/v1/accounting/payment-gateway/reconciliation?start=${start}&end=${end}`)
      .then((r) => {
        setReport(r);
        setError(null);
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load reconciliation report"));
  }, [start, end]);
  useEffect(load, [load, refreshToken]);

  return (
    <Panel title="Reconciliation report">
      <div className="flex flex-wrap items-end gap-3">
        <Field label="From" htmlFor="start">
          <Input id="start" type="date" value={start} onChange={(e) => setStart(e.target.value)} className="w-40" />
        </Field>
        <Field label="To" htmlFor="end">
          <Input id="end" type="date" value={end} onChange={(e) => setEnd(e.target.value)} className="w-40" />
        </Field>
      </div>

      {error && <p className="text-sm text-destructive">{error}</p>}

      {report && (
        <>
          <p className="text-sm">
            Matched total in range: <span className="tabular font-semibold">{rupees(report.matched_total)}</span>
          </p>

          <div className="flex flex-col gap-2">
            <p className="text-xs font-medium text-muted-foreground">Unmatched settlement lines ({report.unmatched_settlement_lines.length})</p>
            {report.unmatched_settlement_lines.length === 0 ? (
              <p className="text-sm text-muted-foreground">None — every imported line in range is matched.</p>
            ) : (
              <SettlementLinesTable
                lines={report.unmatched_settlement_lines}
                onChanged={() => {
                  load();
                  onMatchChanged();
                }}
              />
            )}
          </div>

          <div className="flex flex-col gap-2">
            <p className="text-xs font-medium text-muted-foreground">Duplicate settlement lines ({report.duplicate_settlement_lines.length})</p>
            {report.duplicate_settlement_lines.length === 0 ? (
              <p className="text-sm text-muted-foreground">None.</p>
            ) : (
              <SettlementLinesTable
                lines={report.duplicate_settlement_lines}
                onChanged={() => {
                  load();
                  onMatchChanged();
                }}
              />
            )}
          </div>

          <div className="flex flex-col gap-2">
            <p className="text-xs font-medium text-muted-foreground">Missing payments ({report.missing_payments.length})</p>
            <p className="text-xs text-muted-foreground">Captured card/UPI payments in range with no matching settlement — money collected that hasn&apos;t shown up in the bank yet.</p>
            {report.missing_payments.length === 0 ? (
              <p className="text-sm text-muted-foreground">None — every captured card/UPI payment in range has settled.</p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Date</TableHead>
                    <TableHead>Method</TableHead>
                    <TableHead>Reference</TableHead>
                    <TableHead className="text-right">Amount</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {report.missing_payments.map((p) => (
                    <TableRow key={p.payment_id}>
                      <TableCell className="text-xs">{p.created_at}</TableCell>
                      <TableCell>
                        <StatusChip status={p.method} tone="muted" label={METHOD_LABEL[p.method] ?? p.method} />
                      </TableCell>
                      <TableCell className="font-mono text-xs">{p.reference_no || "—"}</TableCell>
                      <TableCell className="tabular text-right">{rupees(p.amount)}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </div>
        </>
      )}
    </Panel>
  );
}
