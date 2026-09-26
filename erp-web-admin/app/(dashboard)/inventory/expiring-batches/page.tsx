"use client";

// Phase 8: Vertical Expansion — Grocery/FMCG's "expiry tracking" report
// (erp-core-go's phased_roadmap.md; internal/inventory/batches.go). Every
// non-exhausted batch expiring within the chosen window, soonest first —
// already-expired batches are included too (not hidden once the deadline
// passes), which is exactly what "stricter expiry compliance" needs
// surfaced for review.
import { useCallback, useEffect, useState } from "react";
import { api, ApiError } from "@/lib/api-client";
import { Branch, ExpiringBatch } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

export default function ExpiringBatchesPage() {
  const [branches, setBranches] = useState<Branch[]>([]);
  const [branchId, setBranchId] = useState("");
  const [days, setDays] = useState("7");
  const [batches, setBatches] = useState<ExpiringBatch[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const fetchBatches = useCallback(() => {
    const params = new URLSearchParams();
    if (branchId) params.set("branch_id", branchId);
    if (days) params.set("days", days);
    return api.get<{ batches: ExpiringBatch[] }>(`/api/v1/inventory/expiring-batches?${params.toString()}`);
  }, [branchId, days]);

  useEffect(() => {
    api.get<{ branches: Branch[] }>("/api/v1/branches").then((d) => setBranches(d.branches)).catch(() => {});
    fetchBatches().then((d) => setBatches(d.batches)).catch((e) => setError(e instanceof ApiError ? e.message : "Could not load expiring batches"));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  function refresh() {
    setLoading(true);
    fetchBatches()
      .then((d) => {
        setBatches(d.batches);
        setError(null);
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load expiring batches"))
      .finally(() => setLoading(false));
  }

  return (
    <div className="flex flex-col gap-6 max-w-4xl">
      <h1 className="text-2xl font-semibold">Expiring Batches</h1>
      <p className="text-sm text-zinc-500">
        Every batch-tracked variant&apos;s remaining stock, soonest-to-expire first. A sale already refuses to draw
        from an expired batch — this is the visibility to act before it gets there.
      </p>
      {error && <p className="text-sm text-red-600">{error}</p>}

      <Card>
        <CardContent className="flex items-end gap-4 flex-wrap pt-6">
          <div className="flex flex-col gap-2 w-48">
            <Label>Branch</Label>
            <Select value={branchId} onValueChange={(v) => setBranchId(v ?? "")} items={Object.fromEntries(branches.map((b) => [b.branch_id, b.name]))}>
              <SelectTrigger><SelectValue placeholder="All branches" /></SelectTrigger>
              <SelectContent>
                {branches.map((b) => (
                  <SelectItem key={b.branch_id} value={b.branch_id}>{b.name}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-2 w-36">
            <Label htmlFor="days">Window (days)</Label>
            <Input id="days" type="number" min="1" value={days} onChange={(e) => setDays(e.target.value)} />
          </div>
          <Button onClick={refresh} disabled={loading}>
            {loading ? "Loading..." : "Refresh"}
          </Button>
        </CardContent>
      </Card>

      <Card>
        <CardContent className="pt-6">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Product</TableHead>
                <TableHead>Batch</TableHead>
                <TableHead>Expiry</TableHead>
                <TableHead>Remaining</TableHead>
                <TableHead>Status</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {batches.map((b) => (
                <TableRow key={b.batch_id}>
                  <TableCell>{b.product_name} <span className="text-xs text-zinc-500">({b.sku})</span></TableCell>
                  <TableCell>{b.batch_no}</TableCell>
                  <TableCell>{b.expiry_date}</TableCell>
                  <TableCell>{b.quantity_remaining}</TableCell>
                  <TableCell>
                    {b.days_until_expiry !== null && b.days_until_expiry < 0 ? (
                      <Badge variant="destructive">Expired</Badge>
                    ) : (
                      <Badge variant="outline">{b.days_until_expiry} day(s) left</Badge>
                    )}
                  </TableCell>
                </TableRow>
              ))}
              {batches.length === 0 && !loading && (
                <TableRow>
                  <TableCell colSpan={5} className="text-center text-sm text-zinc-500 py-6">
                    Nothing expiring in this window
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
    </div>
  );
}
