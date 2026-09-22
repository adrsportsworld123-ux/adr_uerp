"use client";

// Phase 1's "stock adjustments with audit trail" (erp-core-go's
// phased_roadmap.md) — GET /inventory and POST /inventory/adjustments
// have existed since Phase 1, but neither erp-web-admin nor
// erp-pos-flutter ever surfaced a general stock-lookup/manual-correction
// screen; PATCH /inventory/reorder-point ended up living on the
// Notifications screen instead (documented there as "this app has no
// general Inventory screen yet"). Found during an explicit "review
// previous phases for anything missing" pass — this closes that gap
// rather than leaving reorder-point as a permanent orphan.
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { api, ApiError, SEED_BRANCH_ID } from "@/lib/api-client";
import { Branch, Product, StockLevel } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

interface VariantOption {
  variantId: string;
  label: string;
}

export default function InventoryPage() {
  const [branchId, setBranchId] = useState(SEED_BRANCH_ID);
  const [branches, setBranches] = useState<Branch[]>([]);
  const [variantOptions, setVariantOptions] = useState<VariantOption[]>([]);
  const [variantId, setVariantId] = useState("");
  const [stock, setStock] = useState<StockLevel | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const [delta, setDelta] = useState("");
  const [reason, setReason] = useState("");

  useEffect(() => {
    api
      .get<{ branches: Branch[] }>("/api/v1/branches")
      .then((d) => setBranches(d.branches))
      .catch(() => {});
    api
      .get<{ products: Product[] }>("/api/v1/products")
      .then((d) => {
        const options: VariantOption[] = [];
        for (const p of d.products) {
          for (const v of p.variants) {
            options.push({ variantId: v.variant_id, label: `${p.name} (${v.sku})` });
          }
        }
        setVariantOptions(options);
      })
      .catch(() => {});
  }, []);

  async function lookup() {
    if (!variantId) return;
    setBusy(true);
    setError(null);
    try {
      const result = await api.get<StockLevel>(`/api/v1/inventory?branch_id=${branchId}&variant_id=${variantId}`);
      setStock(result);
    } catch (e) {
      setStock(null);
      setError(e instanceof ApiError ? e.message : "Could not load stock");
    } finally {
      setBusy(false);
    }
  }

  async function adjust() {
    const quantityDelta = Number(delta);
    if (!variantId || !quantityDelta || !reason.trim()) return;
    setBusy(true);
    try {
      await api.post("/api/v1/inventory/adjustments", {
        variant_id: variantId,
        branch_id: branchId,
        quantity_delta: quantityDelta,
        reason: reason.trim(),
      });
      toast.success("Stock adjusted");
      setDelta("");
      setReason("");
      await lookup();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not adjust stock");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex flex-col gap-6 max-w-2xl">
      <h1 className="text-2xl font-semibold">Inventory</h1>
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Stock lookup</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <div className="flex items-end gap-4 flex-wrap">
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
            <div className="flex flex-col gap-2 w-64">
              <Label>Product variant</Label>
              <Select
                value={variantId}
                onValueChange={(v) => setVariantId(v ?? "")}
                items={Object.fromEntries(variantOptions.map((o) => [o.variantId, o.label]))}
              >
                <SelectTrigger>
                  <SelectValue placeholder="Choose a product variant" />
                </SelectTrigger>
                <SelectContent>
                  {variantOptions.map((o) => (
                    <SelectItem key={o.variantId} value={o.variantId}>
                      {o.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <Button onClick={lookup} disabled={busy || !variantId}>
              Look up
            </Button>
          </div>

          {error && <p className="text-sm text-red-600">{error}</p>}

          {stock && (
            <div className="grid grid-cols-2 sm:grid-cols-4 gap-3 text-sm border-t pt-4">
              <div>
                On hand: <span className="font-semibold">{stock.on_hand}</span>
              </div>
              <div>
                Reserved: <span className="font-semibold">{stock.reserved}</span>
              </div>
              <div>
                Available: <span className="font-semibold">{stock.available}</span>
              </div>
              <div>
                Reorder point: <span className="font-semibold">{stock.reorder_point}</span>
              </div>
            </div>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Manual adjustment</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <p className="text-xs text-zinc-500">
            Gated by the <code>inventory.adjust</code> permission — attempted and toast-erred on 403, same pattern every other
            gated write in this app uses. Writes a <code>stock_movements</code> row and an <code>audit_logs</code> entry
            (before/after <code>on_hand</code>).
          </p>
          <div className="flex items-end gap-4 flex-wrap">
            <div className="flex flex-col gap-2 w-40">
              <Label htmlFor="delta">Quantity delta</Label>
              <Input
                id="delta"
                type="number"
                step="0.001"
                value={delta}
                onChange={(e) => setDelta(e.target.value)}
                placeholder="+5 or -5"
              />
            </div>
            <div className="flex flex-col gap-2 flex-1 min-w-48">
              <Label htmlFor="reason">Reason</Label>
              <Input id="reason" value={reason} onChange={(e) => setReason(e.target.value)} />
            </div>
            <Button onClick={adjust} disabled={busy || !variantId || !delta || !reason.trim()}>
              Apply
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
