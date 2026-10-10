"use client";

// Phase 4, sub-area 4's third and last reconciliation surface
// (erp-core-go's phased_roadmap.md / docs/phase0_1_design.md §3.18) —
// Inventory reconciliation, reconciliation type #3 of
// pos_frd_complete.md §16's four. Same "approval fields only appear once
// needed" shape as Cash Reconciliation's screen (§3.16), generalized to
// a list of counted lines instead of one denomination grid — this
// screen computes each line's variance client-side as counts are typed,
// so nothing round-trips to the server until the whole count is ready to
// submit. Variant picker sourced from GET /products, same documented gap
// (no dedicated stock-by-branch listing endpoint yet) Promotions'
// product picker already has — a "full" count still means manually
// adding every line you want counted, not an auto-populated branch-wide
// list.
import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { api, ApiError, SEED_BRANCH_ID } from "@/lib/api-client";
import { Branch, InventoryReconciliation, InventoryReconciliationSummary } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { ProductPicker, VariantOption as PickerOption } from "@/components/data/product-picker";

function today() {
  return new Date().toISOString().slice(0, 10);
}
function daysAgo(n: number) {
  const d = new Date();
  d.setDate(d.getDate() - n);
  return d.toISOString().slice(0, 10);
}

interface VariantOption {
  variantId: string;
  label: string;
  trackBatch: boolean;
}

export default function InventoryReconciliationPage() {
  return (
    <div className="flex flex-col gap-6 max-w-3xl">
      <h1 className="text-2xl font-semibold">Inventory Reconciliation</h1>
      <NewCountSection />
      <HistorySection />
    </div>
  );
}

function NewCountSection() {
  const [branchId, setBranchId] = useState(SEED_BRANCH_ID);
  const [branches, setBranches] = useState<Branch[]>([]);
  const [variantOptions, setVariantOptions] = useState<VariantOption[]>([]);
  const [reconType, setReconType] = useState<"cycle" | "full">("cycle");
  const [date, setDate] = useState(today());
  const [selectedVariant, setSelectedVariant] = useState("");
  // The picker searches the server (no catalog-size cap); variantOptions
  // keeps the label/batch flag of every variant picked so far, for lines.
  const [picked, setPicked] = useState<PickerOption | null>(null);
  function pickVariant(o: PickerOption) {
    setPicked(o);
    setSelectedVariant(o.variantId);
    setVariantOptions((prev) =>
      prev.some((x) => x.variantId === o.variantId) ? prev : [...prev, { variantId: o.variantId, label: o.label, trackBatch: o.trackBatch }],
    );
  }
  const [counts, setCounts] = useState<Record<string, string>>({}); // variant_id -> counted_qty
  const [systemQty, setSystemQty] = useState<Record<string, string>>({}); // variant_id -> live system on_hand, fetched when a line is added
  // Phase 8: batch-tracked lines only. A count ABOVE system stock needs
  // the batch the extra units belong to (server: BATCH_REQUIRED); below
  // it, blank writes off the expired/soonest-expiring batch first.
  const [batchNos, setBatchNos] = useState<Record<string, string>>({});
  const [expiries, setExpiries] = useState<Record<string, string>>({});
  const [reason, setReason] = useState("");
  const [authorizedBy, setAuthorizedBy] = useState("");
  const [authorizedPin, setAuthorizedPin] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<InventoryReconciliation | null>(null);

  useEffect(() => {
    api
      .get<{ branches: Branch[] }>("/api/v1/branches")
      .then((d) => setBranches(d.branches))
      .catch(() => {});
  }, []);

  async function addLine() {
    if (!selectedVariant || counts[selectedVariant] !== undefined) return;
    setCounts({ ...counts, [selectedVariant]: "" });
    try {
      const stock = await api.get<{ on_hand: string }>(`/api/v1/inventory?branch_id=${branchId}&variant_id=${selectedVariant}`);
      setSystemQty((prev) => ({ ...prev, [selectedVariant]: stock.on_hand }));
    } catch {
      setSystemQty((prev) => ({ ...prev, [selectedVariant]: "0.000" }));
    }
    setSelectedVariant("");
    setPicked(null);
  }

  function removeLine(variantId: string) {
    const next = { ...counts };
    delete next[variantId];
    setCounts(next);
  }

  const lineVariantIds = Object.keys(counts);
  const totalVariance = lineVariantIds.reduce((sum, vid) => {
    const sys = Number(systemQty[vid]) || 0;
    const counted = Number(counts[vid]) || 0;
    return sum + (counted - sys);
  }, 0);
  const isTracked = (vid: string) => variantOptions.find((o) => o.variantId === vid)?.trackBatch ?? false;
  const batchMissing = lineVariantIds.some(
    (vid) => isTracked(vid) && (Number(counts[vid]) || 0) - (Number(systemQty[vid]) || 0) > 0.0005 && !batchNos[vid]?.trim()
  );
  const needsApproval = lineVariantIds.some((vid) => {
    const sys = Number(systemQty[vid]) || 0;
    const counted = Number(counts[vid]) || 0;
    return Math.abs(counted - sys) > 0.0005;
  });

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const payloadCounts = lineVariantIds.map((vid) => ({
        variant_id: vid,
        counted_qty: Number(counts[vid]) || 0,
        ...(isTracked(vid) && batchNos[vid]?.trim()
          ? { batch_no: batchNos[vid].trim(), ...(expiries[vid] ? { expiry_date: expiries[vid] } : {}) }
          : {}),
      }));
      const res = await api.post<InventoryReconciliation>("/api/v1/inventory/reconciliation", {
        branch_id: branchId,
        recon_type: reconType,
        recon_date: date,
        counts: payloadCounts,
        ...(needsApproval ? { reason, authorized_by: authorizedBy, authorized_pin: authorizedPin } : {}),
      });
      toast.success(needsApproval ? "Reconciled with variance, adjustment posted" : "Reconciled — no variance");
      setResult(res);
      setCounts({});
      setSystemQty({});
      setBatchNos({});
      setExpiries({});
      setReason("");
      setAuthorizedBy("");
      setAuthorizedPin("");
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not submit inventory reconciliation");
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
            <div className="flex flex-col gap-2 w-40">
              <Label>Type</Label>
              <Select
                value={reconType}
                onValueChange={(v) => setReconType((v as "cycle" | "full") ?? "cycle")}
                items={{ cycle: "Cycle count", full: "Full audit" }}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="cycle">Cycle count</SelectItem>
                  <SelectItem value="full">Full audit</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="date">Date</Label>
              <Input id="date" type="date" value={date} onChange={(e) => setDate(e.target.value)} className="w-40" />
            </div>
          </div>

          <div className="flex items-end gap-2">
            <div className="flex flex-col gap-2 w-72">
              <Label>Add a variant to count</Label>
              <ProductPicker value={picked} onChange={pickVariant} placeholder="Choose a product variant" />
            </div>
            <Button type="button" variant="outline" onClick={addLine} disabled={!selectedVariant}>
              Add line
            </Button>
          </div>

          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Variant</TableHead>
                <TableHead>System qty</TableHead>
                <TableHead>Counted qty</TableHead>
                <TableHead>Variance</TableHead>
                <TableHead>Batch (batch-tracked only)</TableHead>
                <TableHead></TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {lineVariantIds.map((vid) => {
                const label = variantOptions.find((o) => o.variantId === vid)?.label ?? vid;
                const sys = Number(systemQty[vid]) || 0;
                const counted = Number(counts[vid]) || 0;
                const variance = Math.round((counted - sys) * 1000) / 1000;
                return (
                  <TableRow key={vid}>
                    <TableCell>{label}</TableCell>
                    <TableCell>{systemQty[vid] ?? "—"}</TableCell>
                    <TableCell>
                      <Input
                        type="number"
                        min="0"
                        step="0.001"
                        className="h-8 w-28"
                        value={counts[vid]}
                        onChange={(e) => setCounts({ ...counts, [vid]: e.target.value })}
                      />
                    </TableCell>
                    <TableCell className={variance === 0 ? "text-pay" : "text-destructive font-semibold"}>{variance}</TableCell>
                    <TableCell>
                      {isTracked(vid) ? (
                        <div className="flex gap-1">
                          <Input
                            aria-label="Batch no"
                            className="h-8 w-24"
                            value={batchNos[vid] ?? ""}
                            onChange={(e) => setBatchNos({ ...batchNos, [vid]: e.target.value })}
                            placeholder={variance > 0 ? "Required" : "Optional"}
                          />
                          {variance > 0 && (
                            <Input
                              aria-label="Expiry"
                              type="date"
                              className="h-8 w-36"
                              value={expiries[vid] ?? ""}
                              onChange={(e) => setExpiries({ ...expiries, [vid]: e.target.value })}
                            />
                          )}
                        </div>
                      ) : (
                        <span className="text-muted-foreground">—</span>
                      )}
                    </TableCell>
                    <TableCell>
                      <Button type="button" size="sm" variant="ghost" onClick={() => removeLine(vid)}>
                        Remove
                      </Button>
                    </TableCell>
                  </TableRow>
                );
              })}
              {lineVariantIds.length === 0 && (
                <TableRow>
                  <TableCell colSpan={6} className="text-center text-sm text-muted-foreground py-6">
                    No lines added yet
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>

          {lineVariantIds.length > 0 && (
            <p className="text-sm">
              Net quantity variance across all lines:{" "}
              <span className={totalVariance === 0 ? "text-pay font-semibold" : "text-destructive font-semibold"}>{Math.round(totalVariance * 1000) / 1000}</span>
            </p>
          )}

          {needsApproval && (
            <div className="flex flex-col gap-3 border rounded-lg p-3 bg-warn-soft">
              <p className="text-xs text-warn">
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

          {error && <p className="text-sm text-destructive">{error}</p>}
          {batchMissing && (
            <p className="text-sm text-warn">A batch-tracked line counted above system stock needs the batch number the extra units belong to.</p>
          )}
          <Button type="submit" disabled={busy || lineVariantIds.length === 0 || batchMissing} className="w-fit">
            {busy ? "Submitting..." : "Submit reconciliation"}
          </Button>

          {result && (
            <div className="text-sm border-t pt-3">
              <Badge variant={Number(result.variance_value) === 0 ? "default" : "secondary"}>
                {result.recon_date}: variance value ₹{result.variance_value}
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
  const [history, setHistory] = useState<InventoryReconciliationSummary[]>([]);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(() => {
    api
      .get<{ history: InventoryReconciliationSummary[] }>(`/api/v1/inventory/reconciliation/history?branch_id=${branchId}&start=${start}&end=${end}`)
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

        {error && <p className="text-sm text-destructive">{error}</p>}

        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Date</TableHead>
              <TableHead>Type</TableHead>
              <TableHead>Variance value</TableHead>
              <TableHead>Approved</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {history.map((h) => (
              <TableRow key={h.reconciliation_id}>
                <TableCell className="text-xs">{h.recon_date}</TableCell>
                <TableCell>
                  <Badge variant="outline">{h.recon_type}</Badge>
                </TableCell>
                <TableCell className={Number(h.variance_value) !== 0 ? "text-destructive" : ""}>₹{h.variance_value}</TableCell>
                <TableCell>{h.authorized ? <Badge variant="default">Yes</Badge> : <span className="text-muted-foreground">—</span>}</TableCell>
              </TableRow>
            ))}
            {history.length === 0 && (
              <TableRow>
                <TableCell colSpan={4} className="text-center text-sm text-muted-foreground py-6">
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
