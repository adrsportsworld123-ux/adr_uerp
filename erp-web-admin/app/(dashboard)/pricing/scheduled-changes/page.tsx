"use client";

// Phase 10 (erp-core-go's phased_roadmap.md; internal/pricing/schedule.go):
// price changes with a future effective date. A background sweeper
// applies each one within a minute of its time, through the same rules
// as an immediate price edit — a change that would sell below cost on the
// day is marked failed with the reason rather than skipped silently.
// Bulk revisions can be scheduled too, from the Pricing page's bulk
// update (set an effective date before Apply).
//
// CRUD: schedule and edit in a dialog (pending only), cancel (the status
// change), and delete anything not yet applied. An applied change is the
// variant's price history and stays.
import { useCallback, useEffect, useState } from "react";
import { Loader2, Pencil, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { ScheduledPriceChange } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { AlertModal, Modal } from "@/components/ui/dialog";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { PageHeader } from "@/components/data/page-header";
import { StatusChip } from "@/components/data/status-chip";
import { ProductPicker, VariantOption } from "@/components/data/product-picker";
import { toLocalInput } from "@/components/pricing/schedule-fields";

import { rupees } from "@/lib/format";
const errorText = (e: unknown, fallback: string) => (e instanceof ApiError || e instanceof Error ? e.message : fallback);
const STATUS_TONE = { pending: "info", applied: "ok", cancelled: "muted", failed: "bad" } as const;

function fmtWhen(ts: string) {
  const v = toLocalInput(ts);
  return v ? new Date(v).toLocaleString("en-IN", { day: "numeric", month: "short", year: "numeric", hour: "2-digit", minute: "2-digit" }) : ts;
}

export default function ScheduledChangesPage() {
  const [changes, setChanges] = useState<ScheduledPriceChange[]>([]);
  const [loading, setLoading] = useState(true);
  const [statusFilter, setStatusFilter] = useState("pending");
  const [editing, setEditing] = useState<ScheduledPriceChange | "new" | null>(null);
  const [deleting, setDeleting] = useState<ScheduledPriceChange | null>(null);

  const refresh = useCallback(() => {
    api
      .get<{ scheduled_changes: ScheduledPriceChange[] }>(`/api/v1/pricing/scheduled-changes?status=${statusFilter}`)
      .then((d) => setChanges(d.scheduled_changes))
      .catch((e) => toast.error(errorText(e, "Could not load scheduled changes")))
      .finally(() => setLoading(false));
  }, [statusFilter]);
  useEffect(refresh, [refresh]);

  async function cancel(id: string) {
    try {
      await api.post(`/api/v1/pricing/scheduled-changes/${id}/cancel`);
      toast.success("Scheduled change cancelled");
      refresh();
    } catch (e) {
      toast.error(errorText(e, "Could not cancel"));
    }
  }

  async function remove(c: ScheduledPriceChange) {
    setDeleting(null);
    try {
      await api.delete(`/api/v1/pricing/scheduled-changes/${c.scheduled_change_id}`);
      toast.success("Scheduled change deleted");
      refresh();
    } catch (e) {
      toast.error(errorText(e, "Could not delete"));
    }
  }

  return (
    <div className="flex max-w-6xl flex-col gap-6">
      <PageHeader
        title="Scheduled Price Changes"
        description="Set a price to change at a future date and time. It's applied automatically within a minute of that time, with the same below-cost check as an immediate edit; a change that would sell below cost on the day is marked failed with the reason, never skipped silently."
        actions={
          <Button onClick={() => setEditing("new")}>
            <Plus /> Schedule a change
          </Button>
        }
      />

      <Card>
        <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3">
          <CardTitle className="text-base">Changes</CardTitle>
          <select aria-label="Status" value={statusFilter} onChange={(e) => setStatusFilter(e.target.value)} className="h-9 rounded-lg border bg-card px-2 text-sm">
            <option value="">All statuses</option>
            <option value="pending">Pending</option>
            <option value="applied">Applied</option>
            <option value="failed">Failed</option>
            <option value="cancelled">Cancelled</option>
          </select>
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Product</TableHead>
                <TableHead>New prices</TableHead>
                <TableHead>Effective</TableHead>
                <TableHead>Status</TableHead>
                <TableHead className="text-right">Actions</TableHead>
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
                    {c.selling_price && <div>Selling {rupees(c.selling_price)}</div>}
                    {c.mrp && <div>MRP {rupees(c.mrp)}</div>}
                    {c.cost_price && <div>Cost {rupees(c.cost_price)}</div>}
                  </TableCell>
                  <TableCell className="text-sm">{fmtWhen(c.effective_at)}</TableCell>
                  <TableCell>
                    <StatusChip status={c.status} tone={STATUS_TONE[c.status]} />
                    {c.failure_reason && <div className="mt-1 text-xs text-destructive">{c.failure_reason}</div>}
                  </TableCell>
                  <TableCell>
                    <div className="flex justify-end gap-1.5">
                      {c.status === "pending" && (
                        <>
                          <Button size="sm" variant="outline" onClick={() => setEditing(c)}>
                            <Pencil /> Edit
                          </Button>
                          <Button size="sm" variant="outline" onClick={() => cancel(c.scheduled_change_id)}>
                            Cancel
                          </Button>
                        </>
                      )}
                      {c.status !== "applied" && (
                        <Button size="sm" variant="ghost" aria-label="Delete" className="text-destructive hover:text-destructive" onClick={() => setDeleting(c)}>
                          <Trash2 />
                        </Button>
                      )}
                    </div>
                  </TableCell>
                </TableRow>
              ))}
              {!loading && changes.length === 0 && (
                <TableRow>
                  <TableCell colSpan={5} className="py-6 text-center text-sm text-muted-foreground">
                    No scheduled changes
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      {editing !== null && (
        <ScheduleDialog
          key={editing === "new" ? "new" : editing.scheduled_change_id}
          change={editing === "new" ? null : editing}
          onClose={() => setEditing(null)}
          onSaved={refresh}
        />
      )}
      <AlertModal
        open={!!deleting}
        onOpenChange={(o) => !o && setDeleting(null)}
        title="Delete this scheduled change?"
        actions={
          <>
            <Button variant="outline" onClick={() => setDeleting(null)}>
              Keep it
            </Button>
            <Button variant="destructive" onClick={() => deleting && remove(deleting)}>
              Delete
            </Button>
          </>
        }
      >
        {deleting?.status === "pending"
          ? `The price of ${deleting.product_name} won't change on ${fmtWhen(deleting.effective_at)}. `
          : "It never changed a price, so removing it loses no history. "}
        This can&rsquo;t be undone.
      </AlertModal>
    </div>
  );
}

function ScheduleDialog({ change, onClose, onSaved }: { change: ScheduledPriceChange | null; onClose: () => void; onSaved: () => void }) {
  const c = change;
  const num = (s: string | null | undefined) => (s ? String(Number(s)) : "");
  const [variant, setVariant] = useState<VariantOption | null>(
    c ? { variantId: c.variant_id, productId: "", productName: c.product_name, sku: c.sku, sellingPrice: "", label: `${c.product_name} (${c.sku})`, trackBatch: false } : null,
  );
  const [sellingPrice, setSellingPrice] = useState(num(c?.selling_price));
  const [mrp, setMrp] = useState(num(c?.mrp));
  const [costPrice, setCostPrice] = useState(num(c?.cost_price));
  const [effectiveAt, setEffectiveAt] = useState(toLocalInput(c?.effective_at));
  const [reason, setReason] = useState(c?.reason ?? "");
  const [override, setOverride] = useState(c?.override ?? false);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    if (!variant || !effectiveAt || (!sellingPrice && !mrp && !costPrice)) {
      setError("Pick a product, a date and time, and at least one new price.");
      return;
    }
    setSaving(true);
    const body = {
      variant_id: variant.variantId,
      ...(sellingPrice ? { selling_price: Number(sellingPrice) } : {}),
      ...(mrp ? { mrp: Number(mrp) } : {}),
      ...(costPrice ? { cost_price: Number(costPrice) } : {}),
      // datetime-local is the browser's local time; toISOString sends the
      // same instant as UTC, which the API accepts as RFC3339.
      effective_at: new Date(effectiveAt).toISOString(),
      reason,
      override,
    };
    try {
      if (c) await api.put(`/api/v1/pricing/scheduled-changes/${c.scheduled_change_id}`, body);
      else await api.post("/api/v1/pricing/scheduled-changes", body);
      toast.success(c ? "Scheduled change updated" : "Price change scheduled");
      onSaved();
      onClose();
    } catch (err) {
      setError(errorText(err, c ? "Could not update the scheduled change" : "Could not schedule price change"));
    } finally {
      setSaving(false);
    }
  }

  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={c ? "Edit scheduled change" : "Schedule a price change"}
      description="Leave a price empty to keep it unchanged."
      width={640}
      footer={
        <>
          <Button variant="outline" onClick={onClose} disabled={saving}>
            Close
          </Button>
          <Button type="submit" form="schedule-form" disabled={saving}>
            {saving && <Loader2 className="animate-spin" aria-hidden />}
            {c ? "Save changes" : "Schedule"}
          </Button>
        </>
      }
    >
      <form id="schedule-form" onSubmit={save} className="flex flex-col gap-4">
        <div className="flex flex-col gap-2">
          <Label htmlFor="scheduleVariant">Product variant *</Label>
          <ProductPicker id="scheduleVariant" ariaLabel="Product variant" value={variant} onChange={setVariant} placeholder="Choose a product variant" />
          {variant?.sellingPrice && <p className="text-xs text-muted-foreground">Selling now at {rupees(variant.sellingPrice)}</p>}
        </div>
        <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
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
            <Label htmlFor="effectiveAt">Effective at *</Label>
            <Input id="effectiveAt" type="datetime-local" value={effectiveAt} onChange={(e) => setEffectiveAt(e.target.value)} />
          </div>
        </div>
        <div className="flex flex-col gap-2">
          <Label htmlFor="reason">Reason</Label>
          <Input id="reason" value={reason} onChange={(e) => setReason(e.target.value)} placeholder="e.g. festive price revision" />
        </div>
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={override} onChange={(e) => setOverride(e.target.checked)} />
          Allow below cost
        </label>
        {error && (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        )}
      </form>
    </Modal>
  );
}
