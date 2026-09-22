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
import { api, ApiError, SEED_BRANCH_ID } from "@/lib/api-client";
import { Branch, CashReconciliation, CashReconciliationSummary } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

const DENOMINATIONS = [2000, 500, 200, 100, 50, 20, 10, 5, 2, 1];

function today() {
  return new Date().toISOString().slice(0, 10);
}
function daysAgo(n: number) {
  const d = new Date();
  d.setDate(d.getDate() - n);
  return d.toISOString().slice(0, 10);
}

export default function CashReconciliationPage() {
  return (
    <div className="flex flex-col gap-6 max-w-3xl">
      <h1 className="text-2xl font-semibold">Cash Reconciliation</h1>
      <NewCountSection />
      <HistorySection />
    </div>
  );
}

function NewCountSection() {
  const [branchId, setBranchId] = useState(SEED_BRANCH_ID);
  const [branches, setBranches] = useState<Branch[]>([]);
  const [date, setDate] = useState(today());
  const [openingFloat, setOpeningFloat] = useState("0");
  const [cashSales, setCashSales] = useState<string | null>(null);
  const [counts, setCounts] = useState<Record<number, string>>({});
  const [reason, setReason] = useState("");
  const [authorizedBy, setAuthorizedBy] = useState("");
  const [authorizedPin, setAuthorizedPin] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<CashReconciliation | null>(null);

  useEffect(() => {
    api
      .get<{ branches: Branch[] }>("/api/v1/branches")
      .then((d) => setBranches(d.branches))
      .catch(() => {});
  }, []);

  const loadCashSales = useCallback(() => {
    api
      .get<{ cash_total: string }>(`/api/v1/reports/eod-cash?branch_id=${branchId}&date=${date}`)
      .then((d) => setCashSales(d.cash_total))
      .catch(() => setCashSales(null));
  }, [branchId, date]);
  useEffect(loadCashSales, [loadCashSales]);

  const countedTotal = DENOMINATIONS.reduce((sum, d) => sum + d * (Number(counts[d]) || 0), 0);
  const systemExpected = (Number(openingFloat) || 0) + (Number(cashSales) || 0);
  const variance = Math.round((countedTotal - systemExpected) * 100) / 100;
  const needsApproval = variance !== 0;

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const denominations = DENOMINATIONS.filter((d) => Number(counts[d]) > 0).map((d) => ({
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
    <Card>
      <CardHeader>
        <CardTitle className="text-base">New count</CardTitle>
      </CardHeader>
      <CardContent>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <div className="flex gap-4 flex-wrap items-end">
            <div className="flex flex-col gap-2 w-48">
              <Label>Branch</Label>
              <Select
                value={branchId}
                onValueChange={(v) => setBranchId(v ?? SEED_BRANCH_ID)}
                items={Object.fromEntries(branches.map((b) => [b.branch_id, b.name]))}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {branches.map((b) => (
                    <SelectItem key={b.branch_id} value={b.branch_id}>
                      {b.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="date">Date</Label>
              <Input id="date" type="date" value={date} onChange={(e) => setDate(e.target.value)} className="w-40" />
            </div>
            <div className="flex flex-col gap-2 w-40">
              <Label htmlFor="openingFloat">Opening float (₹)</Label>
              <Input id="openingFloat" type="number" min="0" step="0.01" value={openingFloat} onChange={(e) => setOpeningFloat(e.target.value)} />
            </div>
          </div>

          <p className="text-xs text-zinc-500">
            System cash sales for this date: {cashSales !== null ? `₹${cashSales}` : "—"}. System expected (opening + sales): ₹
            {systemExpected.toFixed(2)}
          </p>

          <div className="grid grid-cols-5 gap-3">
            {DENOMINATIONS.map((d) => (
              <div key={d} className="flex flex-col gap-1">
                <Label className="text-xs">₹{d}</Label>
                <Input
                  type="number"
                  min="0"
                  step="1"
                  className="h-8"
                  value={counts[d] ?? ""}
                  onChange={(e) => setCounts({ ...counts, [d]: e.target.value })}
                />
              </div>
            ))}
          </div>

          <div className="flex items-center gap-4 border-t pt-3 text-sm">
            <span>
              Counted total: <span className="font-semibold">₹{countedTotal.toFixed(2)}</span>
            </span>
            <span className={variance === 0 ? "text-green-700" : "text-red-600 font-semibold"}>
              Variance: ₹{variance.toFixed(2)} {variance > 0 ? "(overage)" : variance < 0 ? "(shortage)" : ""}
            </span>
          </div>

          {needsApproval && (
            <div className="flex flex-col gap-3 border rounded-lg p-3 bg-amber-50">
              <p className="text-xs text-amber-800">
                A non-zero variance needs a reason and a Branch Manager or Merchant Admin&apos;s PIN before this can be closed.
              </p>
              <div className="flex flex-col gap-2">
                <Label htmlFor="reason">Reason</Label>
                <Input id="reason" required value={reason} onChange={(e) => setReason(e.target.value)} />
              </div>
              <div className="flex gap-3">
                <div className="flex flex-col gap-2">
                  <Label htmlFor="authorizedBy">Approver user ID</Label>
                  <Input id="authorizedBy" required value={authorizedBy} onChange={(e) => setAuthorizedBy(e.target.value)} className="w-64" />
                </div>
                <div className="flex flex-col gap-2">
                  <Label htmlFor="authorizedPin">Approver PIN</Label>
                  <Input id="authorizedPin" type="password" required value={authorizedPin} onChange={(e) => setAuthorizedPin(e.target.value)} className="w-32" />
                </div>
              </div>
            </div>
          )}

          {error && <p className="text-sm text-red-600">{error}</p>}
          <Button type="submit" disabled={busy} className="w-fit">
            {busy ? "Submitting..." : "Submit reconciliation"}
          </Button>

          {result && (
            <div className="text-sm border-t pt-3">
              <Badge variant={Number(result.variance) === 0 ? "default" : "secondary"}>
                {result.recon_date}: variance ₹{result.variance}
              </Badge>
            </div>
          )}
        </form>
      </CardContent>
    </Card>
  );
}

function HistorySection() {
  const [branchId, setBranchId] = useState(SEED_BRANCH_ID);
  const [start, setStart] = useState(daysAgo(30));
  const [end, setEnd] = useState(today());
  const [history, setHistory] = useState<CashReconciliationSummary[]>([]);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(() => {
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
    <Card>
      <CardHeader>
        <CardTitle className="text-base">History</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <div className="flex items-center gap-2 flex-wrap">
          <Label htmlFor="histBranch" className="text-sm">
            Branch ID
          </Label>
          <Input id="histBranch" value={branchId} onChange={(e) => setBranchId(e.target.value)} className="w-64 font-mono text-xs" />
          <Label htmlFor="histStart" className="text-sm">
            From
          </Label>
          <Input id="histStart" type="date" value={start} onChange={(e) => setStart(e.target.value)} className="w-40" />
          <Label htmlFor="histEnd" className="text-sm">
            To
          </Label>
          <Input id="histEnd" type="date" value={end} onChange={(e) => setEnd(e.target.value)} className="w-40" />
        </div>

        {error && <p className="text-sm text-red-600">{error}</p>}

        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Date</TableHead>
              <TableHead>Opening</TableHead>
              <TableHead>Expected</TableHead>
              <TableHead>Counted</TableHead>
              <TableHead>Variance</TableHead>
              <TableHead>Approved</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {history.map((h) => (
              <TableRow key={h.recon_date}>
                <TableCell className="text-xs">{h.recon_date}</TableCell>
                <TableCell>₹{h.opening_float}</TableCell>
                <TableCell>₹{h.system_expected}</TableCell>
                <TableCell>₹{h.counted_total}</TableCell>
                <TableCell className={Number(h.variance) !== 0 ? "text-red-600" : ""}>₹{h.variance}</TableCell>
                <TableCell>{h.authorized ? <Badge variant="default">Yes</Badge> : <span className="text-zinc-400">—</span>}</TableCell>
              </TableRow>
            ))}
            {history.length === 0 && (
              <TableRow>
                <TableCell colSpan={6} className="text-center text-sm text-zinc-500 py-6">
                  No reconciliations in range
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  );
}
