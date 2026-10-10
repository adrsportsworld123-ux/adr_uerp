"use client";

// Phase 10 (erp-core-go's phased_roadmap.md; internal/pricing/schedule.go):
// price changes with a future effective date. A background sweeper
// applies each one within a minute of its time, through the same rules
// as an immediate price edit — a change that would sell below cost on the
// day is marked failed with the reason rather than skipped silently.
// Bulk revisions can be scheduled too, from the Pricing page's bulk
// update (set an effective date before Apply).
import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Product, ScheduledPriceChange } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

const STATUS_VARIANT: Record<string, "default" | "secondary" | "outline" | "destructive"> = {
  pending: "secondary",
  applied: "default",
  cancelled: "outline",
  failed: "destructive",
};

function fmtWhen(iso: string) {
  return new Date(iso).toLocaleString();
}

export default function ScheduledChangesPage() {
  const [changes, setChanges] = useState<ScheduledPriceChange[]>([]);
  const [statusFilter, setStatusFilter] = useState("pending");
  const [products, setProducts] = useState<Product[]>([]);

  const [variantId, setVariantId] = useState("");
  const [sellingPrice, setSellingPrice] = useState("");
  const [mrp, setMrp] = useState("");
  const [costPrice, setCostPrice] = useState("");
  const [effectiveAt, setEffectiveAt] = useState("");
  const [reason, setReason] = useState("");
  const [override, setOverride] = useState(false);
  const [saving, setSaving] = useState(false);

  const fetchChanges = useCallback(
    () => api.get<{ scheduled_changes: ScheduledPriceChange[] }>(`/api/v1/pricing/scheduled-changes?status=${statusFilter}`),
    [statusFilter]
  );

  useEffect(() => {
    fetchChanges()
      .then((d) => setChanges(d.scheduled_changes))
      .catch((e) => toast.error(e instanceof ApiError ? e.message : "Could not load scheduled changes"));
  }, [fetchChanges]);

  useEffect(() => {
    api.get<{ products: Product[] }>("/api/v1/products?limit=200").then((d) => setProducts(d.products)).catch(() => {});
  }, []);

  function refresh() {
    fetchChanges()
      .then((d) => setChanges(d.scheduled_changes))
      .catch((e) => toast.error(e instanceof ApiError ? e.message : "Could not load scheduled changes"));
  }

  const variantOptions = products.flatMap((p) =>
    p.variants.map((v) => ({ id: v.variant_id, label: `${p.name} (${v.sku}) — now ₹${v.selling_price}` }))
  );

  async function schedule() {
    if (!variantId || !effectiveAt || (!sellingPrice && !mrp && !costPrice)) {
      toast.error("Pick a variant, a date/time, and at least one new price");
      return;
    }
    setSaving(true);
    try {
      await api.post("/api/v1/pricing/scheduled-changes", {
        variant_id: variantId,
        ...(sellingPrice ? { selling_price: Number(sellingPrice) } : {}),
        ...(mrp ? { mrp: Number(mrp) } : {}),
        ...(costPrice ? { cost_price: Number(costPrice) } : {}),
        // datetime-local is the browser's local time; toISOString sends
        // the same instant as UTC, which the API accepts as RFC3339.
        effective_at: new Date(effectiveAt).toISOString(),
        reason,
        override,
      });
      toast.success("Price change scheduled");
      setSellingPrice("");
      setMrp("");
      setCostPrice("");
      setEffectiveAt("");
      setReason("");
      setOverride(false);
      refresh();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not schedule price change");
    } finally {
      setSaving(false);
    }
  }

  async function cancel(id: string) {
    try {
      await api.post(`/api/v1/pricing/scheduled-changes/${id}/cancel`);
      toast.success("Scheduled change cancelled");
      refresh();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not cancel");
    }
  }

  return (
    <div className="flex flex-col gap-6 max-w-5xl">
      <h1 className="text-2xl font-semibold">Scheduled Price Changes</h1>
      <p className="text-sm text-muted-foreground">
        Set a price to change at a future date and time — applied automatically within a minute of that time, with the same
        below-cost check as an immediate edit. A change that would sell below cost on the day is marked <em>failed</em> with the
        reason, never skipped silently.
      </p>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Schedule a change</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <div className="flex flex-col gap-2">
            <Label>Product variant</Label>
            <Select value={variantId} onValueChange={(v) => setVariantId(v ?? "")} items={Object.fromEntries(variantOptions.map((o) => [o.id, o.label]))}>
              <SelectTrigger>
                <SelectValue placeholder="Choose a product variant" />
              </SelectTrigger>
              <SelectContent>
                {variantOptions.map((o) => (
                  <SelectItem key={o.id} value={o.id}>
                    {o.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="grid grid-cols-2 sm:grid-cols-4 gap-4">
            <div className="flex flex-col gap-2">
              <Label htmlFor="newSelling">New selling price</Label>
              <Input id="newSelling" type="number" min="0" step="0.01" value={sellingPrice} onChange={(e) => setSellingPrice(e.target.value)} placeholder="unchanged" />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="newMrp">New MRP</Label>
              <Input id="newMrp" type="number" min="0" step="0.01" value={mrp} onChange={(e) => setMrp(e.target.value)} placeholder="unchanged" />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="newCost">New cost price</Label>
              <Input id="newCost" type="number" min="0" step="0.01" value={costPrice} onChange={(e) => setCostPrice(e.target.value)} placeholder="unchanged" />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="effectiveAt">Effective at</Label>
              <Input id="effectiveAt" type="datetime-local" value={effectiveAt} onChange={(e) => setEffectiveAt(e.target.value)} />
            </div>
          </div>
          <div className="flex items-end gap-4 flex-wrap">
            <div className="flex flex-col gap-2 flex-1 min-w-48">
              <Label htmlFor="reason">Reason</Label>
              <Input id="reason" value={reason} onChange={(e) => setReason(e.target.value)} placeholder="e.g. festive price revision" />
            </div>
            <label className="flex items-center gap-2 text-sm">
              <input type="checkbox" checked={override} onChange={(e) => setOverride(e.target.checked)} />
              Allow below cost
            </label>
            <Button onClick={schedule} disabled={saving}>
              {saving ? "Scheduling..." : "Schedule"}
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="flex flex-row items-center justify-between">
          <CardTitle className="text-base">Changes</CardTitle>
          <div className="w-44">
            <Select
              value={statusFilter}
              onValueChange={(v) => setStatusFilter(v ?? "")}
              items={{ "": "All statuses", pending: "Pending", applied: "Applied", failed: "Failed", cancelled: "Cancelled" }}
            >
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="">All statuses</SelectItem>
                <SelectItem value="pending">Pending</SelectItem>
                <SelectItem value="applied">Applied</SelectItem>
                <SelectItem value="failed">Failed</SelectItem>
                <SelectItem value="cancelled">Cancelled</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Product</TableHead>
                <TableHead>New prices</TableHead>
                <TableHead>Effective</TableHead>
                <TableHead>Status</TableHead>
                <TableHead></TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {changes.map((c) => (
                <TableRow key={c.scheduled_change_id}>
                  <TableCell>
                    {c.product_name} <span className="text-xs text-muted-foreground">({c.sku})</span>
                    {c.reason && <div className="text-xs text-muted-foreground">{c.reason}</div>}
                  </TableCell>
                  <TableCell className="text-sm">
                    {c.selling_price && <div>Selling ₹{c.selling_price}</div>}
                    {c.mrp && <div>MRP ₹{c.mrp}</div>}
                    {c.cost_price && <div>Cost ₹{c.cost_price}</div>}
                  </TableCell>
                  <TableCell className="text-sm">{fmtWhen(c.effective_at)}</TableCell>
                  <TableCell>
                    <Badge variant={STATUS_VARIANT[c.status] ?? "outline"}>{c.status}</Badge>
                    {c.failure_reason && <div className="text-xs text-destructive mt-1">{c.failure_reason}</div>}
                  </TableCell>
                  <TableCell>
                    {c.status === "pending" && (
                      <Button size="sm" variant="outline" onClick={() => cancel(c.scheduled_change_id)}>
                        Cancel
                      </Button>
                    )}
                  </TableCell>
                </TableRow>
              ))}
              {changes.length === 0 && (
                <TableRow>
                  <TableCell colSpan={5} className="text-center text-sm text-muted-foreground py-6">
                    No scheduled changes
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
