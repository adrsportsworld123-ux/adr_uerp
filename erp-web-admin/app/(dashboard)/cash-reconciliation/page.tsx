"use client";

// Phase 4, sub-area 4's admin surface (erp-core-go's phased_roadmap.md /
// docs/phase0_1_design.md §3.16) — cash reconciliation, reconciliation
// type #1 of pos_frd_complete.md §16's four. Variance and whether an
// approval will be needed are computed client-side as the cashier types
// (system expected = opening float + that date's real cash sales,
// fetched from the existing EOD Cash report; counted total = the
// denomination grid's own running sum) so the reason/approval fields
// only appear once they're actually needed, rather than a submit-then-
// retry round trip. authorized_by is a raw user-id text input — no
// user-list endpoint exists yet, the same documented gap Party Ledger's
// customer field already has in this app.
import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Branch, CashReconciliation, CashReconciliationSummary } from "@/lib/types";
import { localDate, rupees } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { PageHeader } from "@/components/data/page-header";
import { Field, Panel } from "@/components/data/panel";
import { BranchSelect, useMyBranches } from "@/components/data/branch-select";
import { StatusChip } from "@/components/data/status-chip";

import { currencySymbol } from "@/lib/currency";

// The INR notes and coins, until the merchant's own list loads.
const INR_DENOMINATIONS = [2000, 500, 200, 100, 50, 20, 10, 5, 2, 1];

/**
 * The notes and coins the merchant counts (Localization, erp-core-go
 * migrations/064): its own list, or its currency's standard set.
 */
function useDenominations(): number[] {
  const [list, setList] = useState(INR_DENOMINATIONS);
  useEffect(() => {
    api
      .get<{ effective: { denominations: string[] } }>("/api/v1/settings/localization")
      .then((s) => {
        const values = (s.effective.denominations ?? []).map(Number).filter((d) => d > 0);
        if (values.length > 0) setList(values);
      })
      .catch(() => {}); // an older API: the INR set
  }, []);
  return list;
}

export default function CashReconciliationPage() {
  const { branches, defaultBranchId } = useMyBranches();
  return (
    <div className="flex max-w-5xl flex-col gap-5">
      <PageHeader
        title="Cash Reconciliation"
        description="Count a branch's cash for a day against what the system expects. A difference needs a reason and a manager's approval, and posts an adjustment."
      />
      <NewCountSection branches={branches} defaultBranchId={defaultBranchId} />
      <HistorySection branches={branches} defaultBranchId={defaultBranchId} />
    </div>
  );
}

function NewCountSection({ branches, defaultBranchId }: { branches: Branch[]; defaultBranchId: string }) {
  const [picked, setPicked] = useState("");
  const branchId = picked || defaultBranchId;
  const [date, setDate] = useState(localDate());
  const [openingFloat, setOpeningFloat] = useState("0");
  const [cashSales, setCashSales] = useState<string | null>(null);
  const [counts, setCounts] = useState<Record<number, string>>({});
  const [reason, setReason] = useState("");
  const [authorizedBy, setAuthorizedBy] = useState("");
  const [authorizedPin, setAuthorizedPin] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<CashReconciliation | null>(null);
  const denomList = useDenominations();

  const loadCashSales = useCallback(() => {
    if (!branchId) return;
    api
      .get<{ cash_total: string }>(`/api/v1/reports/eod-cash?branch_id=${branchId}&date=${date}`)
      .then((d) => setCashSales(d.cash_total))
      .catch(() => setCashSales(null));
  }, [branchId, date]);
  useEffect(loadCashSales, [loadCashSales]);

  // In paise, so coins below one (0.25) add up exactly.
  const countedTotal = denomList.reduce((sum, d) => sum + Math.round(d * 100) * (Number(counts[d]) || 0), 0) / 100;
  const systemExpected = (Number(openingFloat) || 0) + (Number(cashSales) || 0);
  const variance = Math.round((countedTotal - systemExpected) * 100) / 100;
  const needsApproval = variance !== 0;

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const denominations = denomList.filter((d) => Number(counts[d]) > 0).map((d) => ({
        denomination: d,
        count: Number(counts[d]),
      }));
      const res = await api.post<CashReconciliation>("/api/v1/cash-reconciliation", {
        branch_id: branchId,
        recon_date: date,
        opening_float: Number(openingFloat) || 0,
        denominations,
        ...(needsApproval ? { reason, authorized_by: authorizedBy, authorized_pin: authorizedPin } : {}),
      });
      toast.success(variance === 0 ? "Reconciled — no variance" : "Reconciled with variance, adjustment posted");
      setResult(res);
      setCounts({});
      setReason("");
      setAuthorizedBy("");
      setAuthorizedPin("");
      loadCashSales();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not submit cash reconciliation");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Panel title="New count">
      <form onSubmit={submit} className="flex flex-col gap-4">
        <div className="flex flex-wrap items-end gap-3">
          <Field label="Branch" htmlFor="count-branch">
            <BranchSelect id="count-branch" branches={branches} value={branchId} onChange={setPicked} />
          </Field>
          <Field label="Date" htmlFor="date">
            <Input id="date" type="date" value={date} onChange={(e) => setDate(e.target.value)} className="w-40" />
          </Field>
          <Field label={`Opening float (${currencySymbol()})`} htmlFor="openingFloat" className="w-40">
            <Input id="openingFloat" type="number" min="0" step="0.01" value={openingFloat} onChange={(e) => setOpeningFloat(e.target.value)} />
          </Field>
        </div>

        <p className="text-sm text-muted-foreground">
          System cash sales for this date: <span className="tabular">{cashSales !== null ? rupees(cashSales) : "—"}</span>. System expected
          (opening + sales): <span className="tabular">{rupees(systemExpected)}</span>
        </p>

        <div className="grid grid-cols-5 gap-3">
          {denomList.map((d) => (
            <div key={d} className="flex flex-col gap-1">
              <Label className="text-xs">{currencySymbol()}{d}</Label>
              <Input type="number" min="0" step="1" className="h-8" value={counts[d] ?? ""} onChange={(e) => setCounts({ ...counts, [d]: e.target.value })} />
            </div>
          ))}
        </div>

        <div className="flex flex-wrap items-center gap-4 border-t pt-3 text-sm">
          <span>
            Counted total: <span className="tabular font-semibold">{rupees(countedTotal)}</span>
          </span>
          <span className={`tabular ${variance === 0 ? "text-pay" : "font-semibold text-destructive"}`}>
            Variance: {rupees(Math.abs(variance))} {variance > 0 ? "(overage)" : variance < 0 ? "(shortage)" : ""}
          </span>
        </div>

        {needsApproval && (
          <div className="flex flex-col gap-3 rounded-lg border bg-warn-soft p-3">
            <p className="text-xs text-warn">A non-zero variance needs a reason and a Branch Manager or Merchant Admin&apos;s PIN before this can be closed.</p>
            <Field label="Reason" htmlFor="reason">
              <Input id="reason" required value={reason} onChange={(e) => setReason(e.target.value)} />
            </Field>
            <div className="flex flex-wrap gap-3">
              <Field label="Approver user ID" htmlFor="authorizedBy">
                <Input id="authorizedBy" required value={authorizedBy} onChange={(e) => setAuthorizedBy(e.target.value)} className="w-64" />
              </Field>
              <Field label="Approver PIN" htmlFor="authorizedPin">
                <Input id="authorizedPin" type="password" required value={authorizedPin} onChange={(e) => setAuthorizedPin(e.target.value)} className="w-32" />
              </Field>
            </div>
          </div>
        )}

        {error && <p className="text-sm text-destructive">{error}</p>}
        <Button type="submit" disabled={busy || !branchId} className="w-fit">
          {busy ? "Submitting…" : "Submit reconciliation"}
        </Button>

        {result && (
          <div className="border-t pt-3 text-sm">
            <StatusChip status="reconciled" tone={Number(result.variance) === 0 ? "ok" : "warn"} label={`${result.recon_date}: variance ${rupees(result.variance)}`} />
          </div>
        )}
      </form>
    </Panel>
  );
}

function HistorySection({ branches, defaultBranchId }: { branches: Branch[]; defaultBranchId: string }) {
  const [picked, setPicked] = useState("");
  const branchId = picked || defaultBranchId;
  const [start, setStart] = useState(localDate(-30));
  const [end, setEnd] = useState(localDate());
  const [history, setHistory] = useState<CashReconciliationSummary[]>([]);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(() => {
    if (!branchId) return;
    api
      .get<{ history: CashReconciliationSummary[] }>(`/api/v1/cash-reconciliation/history?branch_id=${branchId}&start=${start}&end=${end}`)
      .then((d) => {
        setHistory(d.history);
        setError(null);
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load history"));
  }, [branchId, start, end]);
  useEffect(load, [load]);

  return (
    <Panel title="History">
      <div className="flex flex-wrap items-end gap-3">
        <Field label="Branch" htmlFor="histBranch">
          <BranchSelect id="histBranch" branches={branches} value={branchId} onChange={setPicked} />
        </Field>
        <Field label="From" htmlFor="histStart">
          <Input id="histStart" type="date" value={start} onChange={(e) => setStart(e.target.value)} className="w-40" />
        </Field>
        <Field label="To" htmlFor="histEnd">
          <Input id="histEnd" type="date" value={end} onChange={(e) => setEnd(e.target.value)} className="w-40" />
        </Field>
      </div>

      {error && <p className="text-sm text-destructive">{error}</p>}

      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Date</TableHead>
            <TableHead>Drawer</TableHead>
            <TableHead className="text-right">Opening</TableHead>
            <TableHead className="text-right">Expected</TableHead>
            <TableHead className="text-right">Counted</TableHead>
            <TableHead className="text-right">Variance</TableHead>
            <TableHead>Approved</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {history.map((h) => (
            <TableRow key={h.pos_shift_id ?? h.recon_date}>
              <TableCell className="text-xs">{h.recon_date}</TableCell>
              <TableCell className="text-xs">{h.pos_shift_id ? `${h.terminal_name} · shift ${h.shift_number}` : "Whole branch"}</TableCell>
              <TableCell className="tabular text-right">{rupees(h.opening_float)}</TableCell>
              <TableCell className="tabular text-right">{rupees(h.system_expected)}</TableCell>
              <TableCell className="tabular text-right">{rupees(h.counted_total)}</TableCell>
              <TableCell className={`tabular text-right ${Number(h.variance) !== 0 ? "text-destructive" : ""}`}>{rupees(h.variance)}</TableCell>
              <TableCell>{h.authorized ? <StatusChip status="approved" tone="ok" label="Approved" /> : <span className="text-muted-foreground">—</span>}</TableCell>
            </TableRow>
          ))}
          {history.length === 0 && (
            <TableRow>
              <TableCell colSpan={7} className="py-6 text-center text-sm text-muted-foreground">
                No reconciliations in range
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
    </Panel>
  );
}
