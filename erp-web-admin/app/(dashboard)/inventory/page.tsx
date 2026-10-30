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
import { Branch, ProductDetail, StockLevel } from "@/lib/types";
import { rupees } from "@/lib/format";
import { withUnit } from "@/components/catalog/units";
import { StatusChip } from "@/components/data/status-chip";
import { ProductPicker, VariantOption } from "@/components/data/product-picker";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

/** Everything that tells this variant apart: its product, options, codes, unit and prices. */
function VariantDetails({ detail, variantId }: { detail: ProductDetail | null; variantId: string }) {
  if (!detail) return <p className="text-sm text-muted-foreground">Loading the variant…</p>;
  const v = detail.variants.find((x) => x.variant_id === variantId);
  if (!v) return null;
  const options = Object.entries(v.attribute_combo ?? {});
  const unit = detail.unit?.symbol;
  const rows: [string, React.ReactNode][] = [
    ["Product", detail.name],
    ["Category · brand", [detail.category_name, detail.brand_name].filter(Boolean).join(" · ") || "—"],
    ["SKU", <span key="sku" className="font-mono">{v.sku}</span>],
    ["Variant", options.length ? options.map(([k, val]) => `${k}: ${val}`).join(" · ") : detail.variants.length > 1 ? "—" : "The only variant"],
    [
      "Barcodes",
      v.barcodes && v.barcodes.length ? (
        <span key="codes" className="font-mono">{v.barcodes.map((b) => `${b.code}${b.pack_label ? ` (${b.pack_label})` : ""}`).join(" · ")}</span>
      ) : (
        [v.original_barcode, v.generated_barcode].filter(Boolean).join(" · ") || "—"
      ),
    ],
    ["Counted in", detail.unit ? `${detail.unit.name} (${detail.unit.symbol})` : "—"],
    ["Cost · MRP · selling", `${rupees(v.cost_price)} · ${rupees(v.mrp)} · ${rupees(v.selling_price)}${detail.price_includes_tax ? " (incl. GST)" : " (+ GST)"}`],
    ["GST", detail.tax_slab_name ?? "—"],
    ["Tracking", [v.track_batch ? "Batch & expiry" : "", v.track_serial ? "Serial numbers" : "", v.plu_code ? `Scale PLU ${v.plu_code}` : ""].filter(Boolean).join(" · ") || "None"],
    ["On hand, every branch", withUnit(v.on_hand_total, unit)],
  ];
  return (
    <div className="rounded-lg border bg-muted/30 p-3" data-testid="variant-details">
      <div className="mb-2 flex items-center justify-between gap-2">
        <span className="text-sm font-semibold">Selected variant</span>
        <StatusChip status={v.status} />
      </div>
      <dl className="grid grid-cols-1 gap-x-6 gap-y-1 text-sm sm:grid-cols-2">
        {rows.map(([k, val]) => (
          <div key={k} className="flex justify-between gap-3">
            <dt className="text-muted-foreground">{k}</dt>
            <dd className="text-end">{val}</dd>
          </div>
        ))}
      </dl>
    </div>
  );
}

export default function InventoryPage() {
  const [branchId, setBranchId] = useState(SEED_BRANCH_ID);
  const [branches, setBranches] = useState<Branch[]>([]);
  const [variant, setVariant] = useState<VariantOption | null>(null);
  const variantId = variant?.variantId ?? "";
  const [stock, setStock] = useState<StockLevel | null>(null);
  // The chosen variant's product (GET /products/{id}), so it is clear which
  // size, colour or pack is being looked up and adjusted.
  const [detail, setDetail] = useState<ProductDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const [delta, setDelta] = useState("");
  const [reason, setReason] = useState("");
  // Phase 8: only for a batch-tracked variant. Required when adding stock
  // (the server answers BATCH_REQUIRED otherwise); optional when removing
  // it, where blank writes off the expired/soonest-expiring batch first.
  const [batchNo, setBatchNo] = useState("");
  const [expiryDate, setExpiryDate] = useState("");
  const trackBatch = variant?.trackBatch ?? false;
  const batchMissing = trackBatch && Number(delta) > 0 && !batchNo.trim();
  // Phase 15: an adjustment worth more than the Approval Policies' stock
  // threshold (quantity x cost) needs an approver. The cost isn't on this
  // screen, so the fields appear once the server asks for them.
  const [needsApprover, setNeedsApprover] = useState(false);
  const [approverCode, setApproverCode] = useState("");
  const [approverPin, setApproverPin] = useState("");
  const approverMissing = needsApprover && (!approverCode.trim() || approverPin.length < 4);

  useEffect(() => {
    api
      .get<{ branches: Branch[] }>("/api/v1/branches?scope=mine")
      .then((d) => setBranches(d.branches))
      .catch(() => {});
  }, []);

  // Choosing a variant shows its details and its stock at once ("Look up"
  // still refreshes the stock).
  function choose(o: VariantOption) {
    setVariant(o);
    setStock(null);
    setDetail(null);
    api
      .get<ProductDetail>(`/api/v1/products/${o.productId}`)
      .then(setDetail)
      .catch(() => setDetail(null));
    void lookup(o.variantId);
  }

  async function lookup(id = variantId, branch = branchId) {
    if (!id) return;
    setBusy(true);
    setError(null);
    try {
      const result = await api.get<StockLevel>(`/api/v1/inventory?branch_id=${branch}&variant_id=${id}`);
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
    if (!variantId || !quantityDelta || !reason.trim() || batchMissing || approverMissing) return;
    setBusy(true);
    try {
      await api.post("/api/v1/inventory/adjustments", {
        variant_id: variantId,
        branch_id: branchId,
        quantity_delta: quantityDelta,
        reason: reason.trim(),
        ...(trackBatch && batchNo.trim() ? { batch_no: batchNo.trim(), ...(expiryDate ? { expiry_date: expiryDate } : {}) } : {}),
        ...(needsApprover ? { approver_code: approverCode.trim(), approver_pin: approverPin } : {}),
      });
      toast.success("Stock adjusted");
      setDelta("");
      setReason("");
      setBatchNo("");
      setExpiryDate("");
      setNeedsApprover(false);
      setApproverCode("");
      setApproverPin("");
      await lookup();
    } catch (e) {
      if (e instanceof ApiError && e.code === "ADJUSTMENT_NOT_APPROVED") {
        if (needsApprover) setApproverPin("");
        setNeedsApprover(true);
      }
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
                onValueChange={(v) => {
                  const b = v ?? SEED_BRANCH_ID;
                  setBranchId(b);
                  if (variantId) void lookup(variantId, b);
                }}
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
              <Label htmlFor="invVariant">Product variant</Label>
              {/* Searches the whole catalogue (name, SKU or barcode); the old
                  dropdown held only the first 200 products. */}
              <ProductPicker id="invVariant" ariaLabel="Product variant" value={variant} onChange={choose} placeholder="Choose a product variant" />
            </div>
            <Button onClick={() => lookup()} disabled={busy || !variantId}>
              Look up
            </Button>
          </div>

          {error && <p className="text-sm text-destructive">{error}</p>}

          {variant && <VariantDetails detail={detail} variantId={variant.variantId} />}

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
          <p className="text-xs text-muted-foreground">
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
            {trackBatch && (
              <>
                <div className="flex flex-col gap-2 w-36">
                  <Label htmlFor="batchNo">Batch no{Number(delta) > 0 ? " *" : ""}</Label>
                  <Input
                    id="batchNo"
                    value={batchNo}
                    onChange={(e) => setBatchNo(e.target.value)}
                    placeholder={Number(delta) > 0 ? "Required" : "Blank = oldest expiry"}
                  />
                </div>
                {Number(delta) > 0 && (
                  <div className="flex flex-col gap-2 w-40">
                    <Label htmlFor="expiryDate">Expiry (new batch)</Label>
                    <Input id="expiryDate" type="date" value={expiryDate} onChange={(e) => setExpiryDate(e.target.value)} />
                  </div>
                )}
              </>
            )}
            {needsApprover && (
              <>
                <div className="flex flex-col gap-2 w-40">
                  <Label htmlFor="approverCode">Approver&apos;s employee code</Label>
                  <Input
                    id="approverCode"
                    value={approverCode}
                    autoComplete="off"
                    onChange={(e) => setApproverCode(e.target.value.toUpperCase())}
                  />
                </div>
                <div className="flex flex-col gap-2 w-32">
                  <Label htmlFor="approverPin">Approver&apos;s PIN</Label>
                  <Input
                    id="approverPin"
                    type="password"
                    inputMode="numeric"
                    autoComplete="off"
                    value={approverPin}
                    onChange={(e) => setApproverPin(e.target.value.replace(/\D/g, ""))}
                  />
                </div>
              </>
            )}
            <Button onClick={adjust} disabled={busy || !variantId || !delta || !reason.trim() || batchMissing || approverMissing}>
              Apply
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
