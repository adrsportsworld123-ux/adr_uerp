"use client";

// Phase 3, sub-area 3's admin surface (erp-core-go's phased_roadmap.md /
// docs/phase0_1_design.md §3.12). Two independent concerns on one screen,
// matching Promotions & Loyalty's own precedent of bundling a phase's
// related sub-features rather than one route each:
//   1. The sent-notification audit log (GET /notifications) — read-only,
//      gated by notifications.view (the first endpoint in this app whose
//      READ, not just its writes, is permission-gated — a POS User sees
//      the API's own "you don't have the notifications.view permission"
//      message the same way any other 403 surfaces here).
//   2. Low-stock reorder-point configuration (PATCH /inventory/reorder-point)
//      — this app has no general Inventory screen yet, so this is the only
//      place reorder_point is settable from the UI.
// Deliberately no "resend receipt" button here — that's an action on a
// specific finalized order, which belongs next to that order's own detail
// view once one exists in this app, not a standalone admin form.
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Branch, Notification, Product, StockLevel } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

export default function NotificationsPage() {
  return (
    <div className="flex flex-col gap-6 max-w-4xl">
      <h1 className="text-2xl font-semibold">Notifications</h1>
      <NotificationLogSection />
      <ReorderPointSection />
    </div>
  );
}

// ---------------------------------------------------------------------
// Sent-notification log
// ---------------------------------------------------------------------

function NotificationLogSection() {
  const [notifications, setNotifications] = useState<Notification[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [category, setCategory] = useState("");
  const [channel, setChannel] = useState("");
  const [status, setStatus] = useState("");

  function load() {
    const params = new URLSearchParams();
    if (category) params.set("category", category);
    if (channel) params.set("channel", channel);
    if (status) params.set("status", status);
    api
      .get<{ notifications: Notification[] }>(`/api/v1/notifications?${params.toString()}`)
      .then((d) => {
        setNotifications(d.notifications);
        setError(null);
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not reach the server"));
  }
  useEffect(load, [category, channel, status]);

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Sent notifications</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <div className="flex gap-2 flex-wrap">
          <Select
            value={category || "all"}
            onValueChange={(v) => setCategory(v === "all" ? "" : (v ?? ""))}
            items={{ all: "All categories", receipt: "Receipt", low_stock: "Low stock", payment_reminder: "Payment reminder" }}
          >
            <SelectTrigger className="w-40">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All categories</SelectItem>
              <SelectItem value="receipt">Receipt</SelectItem>
              <SelectItem value="low_stock">Low stock</SelectItem>
              <SelectItem value="payment_reminder">Payment reminder</SelectItem>
            </SelectContent>
          </Select>
          <Select
            value={channel || "all"}
            onValueChange={(v) => setChannel(v === "all" ? "" : (v ?? ""))}
            items={{ all: "All channels", email: "Email", sms: "SMS", whatsapp: "WhatsApp" }}
          >
            <SelectTrigger className="w-32">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All channels</SelectItem>
              <SelectItem value="email">Email</SelectItem>
              <SelectItem value="sms">SMS</SelectItem>
              <SelectItem value="whatsapp">WhatsApp</SelectItem>
            </SelectContent>
          </Select>
          <Select
            value={status || "all"}
            onValueChange={(v) => setStatus(v === "all" ? "" : (v ?? ""))}
            items={{ all: "Any status", sent: "Sent", failed: "Failed" }}
          >
            <SelectTrigger className="w-32">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">Any status</SelectItem>
              <SelectItem value="sent">Sent</SelectItem>
              <SelectItem value="failed">Failed</SelectItem>
            </SelectContent>
          </Select>
        </div>

        {error && <p className="text-sm text-red-600">{error}</p>}

        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Category</TableHead>
              <TableHead>Channel</TableHead>
              <TableHead>Recipient</TableHead>
              <TableHead>Subject / body</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Sent</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {notifications.map((n) => (
              <TableRow key={n.notification_id}>
                <TableCell className="capitalize">{n.category.replace("_", " ")}</TableCell>
                <TableCell className="uppercase text-xs">{n.channel}</TableCell>
                <TableCell className="font-mono text-xs">{n.recipient}</TableCell>
                <TableCell className="max-w-xs truncate" title={n.subject ?? n.body}>
                  {n.subject ?? n.body}
                </TableCell>
                <TableCell>
                  <Badge variant={n.status === "sent" ? "default" : "destructive"}>{n.status}</Badge>
                </TableCell>
                <TableCell className="text-xs text-zinc-500">{n.created_at}</TableCell>
              </TableRow>
            ))}
            {notifications.length === 0 && !error && (
              <TableRow>
                <TableCell colSpan={6} className="text-center text-sm text-zinc-500 py-6">
                  No notifications yet
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  );
}

// ---------------------------------------------------------------------
// Low-stock reorder-point configuration
// ---------------------------------------------------------------------

function ReorderPointSection() {
  const [branches, setBranches] = useState<Branch[]>([]);
  const [products, setProducts] = useState<Product[]>([]);
  const [branchId, setBranchId] = useState("");
  const [variantId, setVariantId] = useState("");
  const [stock, setStock] = useState<StockLevel | null>(null);
  const [reorderPoint, setReorderPoint] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api
      .get<{ branches: Branch[] }>("/api/v1/branches")
      .then((d) => setBranches(d.branches))
      .catch(() => {});
    api
      .get<{ products: Product[] }>("/api/v1/products")
      .then((d) => setProducts(d.products))
      .catch(() => {});
  }, []);

  function loadStock() {
    // No synchronous setState here (react-hooks/set-state-in-effect) —
    // every state update below happens inside the fetch's .then/.catch,
    // not directly in this function's own synchronous body, since this is
    // called from an effect (see the guard below).
    api
      .get<StockLevel>(`/api/v1/inventory?branch_id=${branchId}&variant_id=${variantId}`)
      .then((s) => {
        setStock(s);
        setReorderPoint(s.reorder_point);
        setError(null);
      })
      .catch((e) => {
        setStock(null);
        // NOT_FOUND just means this variant has never been stocked at
        // this branch yet — a normal, unalarming case for a reorder point
        // to be set ahead of the first delivery, not a real error.
        if (e instanceof ApiError && e.code === "NOT_FOUND") {
          setReorderPoint("");
        } else {
          setError(e instanceof ApiError ? e.message : "Could not load stock");
        }
      });
  }
  useEffect(() => {
    // Both Selects only ever move from unset to a real value (no "clear
    // selection" option), so this guard only ever runs loadStock for a
    // genuinely new branch/variant pair — never needs to reset `stock`
    // back to null for an empty case.
    if (!branchId || !variantId) return;
    loadStock();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [branchId, variantId]);

  async function save() {
    setBusy(true);
    setError(null);
    try {
      const s = await api.patch<StockLevel>("/api/v1/inventory/reorder-point", {
        branch_id: branchId,
        variant_id: variantId,
        reorder_point: Number(reorderPoint),
      });
      setStock(s);
      toast.success("Reorder point saved");
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not save reorder point");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Low-stock alert thresholds</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <p className="text-xs text-zinc-500">
          When a variant&apos;s on-hand stock at a branch drops to or below its reorder point, every Branch Manager/Merchant
          Admin gets a low-stock email — checked periodically, not instantly.
        </p>
        <div className="flex gap-4 flex-wrap items-end">
          <div className="flex flex-col gap-2 w-48">
            <Label>Branch</Label>
            <Select
              value={branchId}
              onValueChange={(v) => setBranchId(v ?? "")}
              items={Object.fromEntries(branches.map((b) => [b.branch_id, b.name]))}
            >
              <SelectTrigger>
                <SelectValue placeholder="Select a branch" />
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
          <div className="flex flex-col gap-2 w-64">
            <Label>Product</Label>
            <Select
              value={variantId}
              onValueChange={(v) => setVariantId(v ?? "")}
              items={Object.fromEntries(products.flatMap((p) => p.variants.map((v) => [v.variant_id, `${p.name} (${v.sku})`])))}
            >
              <SelectTrigger>
                <SelectValue placeholder="Select a product" />
              </SelectTrigger>
              <SelectContent>
                {products.flatMap((p) =>
                  p.variants.map((v) => (
                    <SelectItem key={v.variant_id} value={v.variant_id}>
                      {p.name} ({v.sku})
                    </SelectItem>
                  ))
                )}
              </SelectContent>
            </Select>
          </div>
        </div>

        {error && <p className="text-sm text-red-600">{error}</p>}

        {stock && (
          <div className="flex gap-4 items-end flex-wrap border-t pt-4">
            <dl className="grid grid-cols-3 gap-x-6 text-sm">
              <dt className="text-zinc-500">On hand</dt>
              <dt className="text-zinc-500">Reserved</dt>
              <dt className="text-zinc-500">Available</dt>
              <dd>{stock.on_hand}</dd>
              <dd>{stock.reserved}</dd>
              <dd>{stock.available}</dd>
            </dl>
          </div>
        )}
        {branchId && variantId && (
          <div className="flex gap-4 items-end">
            <div className="flex flex-col gap-2 w-40">
              <Label htmlFor="reorderPoint">Reorder point</Label>
              <Input
                id="reorderPoint"
                type="number"
                min="0"
                step="0.001"
                value={reorderPoint}
                onChange={(e) => setReorderPoint(e.target.value)}
              />
            </div>
            <Button disabled={busy} onClick={save}>
              {busy ? "Saving..." : "Save"}
            </Button>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
