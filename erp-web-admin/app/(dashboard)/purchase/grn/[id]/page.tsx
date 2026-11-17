"use client";

import { useEffect, useState, useCallback } from "react";
import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { GRN } from "@/lib/types";
import { Button, buttonVariants } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Separator } from "@/components/ui/separator";
import { ProductPicker } from "@/components/data/product-picker";
import { DownloadPdfButton, PrintPdfButton } from "@/components/data/document-actions";
import { DeleteDocumentButton } from "@/components/purchase/delete-document-dialog";

import { rupees } from "@/lib/format";
import { currencySymbol } from "@/lib/currency";
import { PageHeader } from "@/components/data/page-header";
import { ScanChoice, ScanResult, optionText } from "@/components/catalog/mrp-lots";
interface BarcodeLookup extends ScanResult {
  product_id: string;
  product_name: string;
  variant_id: string;
  sku: string;
  mrp?: string;
  // A pack barcode (erp-core-go migrations/076): what one scan holds, in the product's unit.
  unit?: { symbol: string } | null;
  pack_qty?: string;
  pack_label?: string;
}

export default function GRNDetailPage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  const [grn, setGrn] = useState<GRN | null>(null);
  const [error, setError] = useState<string | null>(null);

  const [barcode, setBarcode] = useState("");
  const [resolved, setResolved] = useState<BarcodeLookup | null>(null);
  const [quantity, setQuantity] = useState("");
  const [unitCost, setUnitCost] = useState("");
  // Phase 8 (Grocery/FMCG) — optional; the backend itself refuses with
  // BATCH_REQUIRED if the scanned variant has track_batch=true and this
  // is left blank, so the form doesn't need to know that flag up front.
  const [batchNo, setBatchNo] = useState("");
  const [expiryDate, setExpiryDate] = useState("");
  // erp-core-go migrations/083: the MRP printed on what arrived. A different
  // one is received into that MRP's lot when the GRN is completed.
  const [mrp, setMrp] = useState("");
  const [lotPrice, setLotPrice] = useState("");
  const [choosing, setChoosing] = useState<BarcodeLookup | null>(null);
  const [lookupError, setLookupError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(() => {
    api
      .get<GRN>(`/api/v1/purchase/grn/${id}`)
      .then(setGrn)
      .catch((e) => setError(e.message));
  }, [id]);

  useEffect(() => {
    load();
  }, [load]);

  async function handleLookup(e: React.FormEvent) {
    e.preventDefault();
    setLookupError(null);
    setResolved(null);
    setChoosing(null);
    try {
      // purpose=receive: a shared printed code offers its colours/sizes, never MRP lots.
      const product = await api.get<BarcodeLookup>(`/api/v1/products/barcode/${encodeURIComponent(barcode)}?purpose=receive`);
      if (product.choice_required && product.options?.length) {
        setChoosing(product);
        return;
      }
      pick(product);
      // A pack (a bag of 25 kg, a box of 12) starts the quantity at its contents.
      if (product.pack_qty && Number(product.pack_qty) !== 1 && !quantity) setQuantity(String(Number(product.pack_qty)));
    } catch (e) {
      setLookupError(e instanceof ApiError ? e.message : "Could not reach the server");
    }
  }

  function pick(product: BarcodeLookup) {
    setResolved(product);
    setMrp(product.mrp ? String(Number(product.mrp)) : "");
    setLotPrice("");
  }

  const newMrp = resolved?.mrp != null && mrp !== "" && Math.abs(Number(mrp) - Number(resolved.mrp)) >= 0.005;
  const lotPriceProblem = newMrp && lotPrice !== "" && (!(Number(lotPrice) > 0) || Number(lotPrice) > Number(mrp));

  async function handleAddLine(e: React.FormEvent) {
    e.preventDefault();
    if (!resolved) return;
    setBusy(true);
    try {
      await api.post(`/api/v1/purchase/grn/${id}/lines`, {
        variant_id: resolved.variant_id,
        quantity: Number(quantity),
        unit_cost: Number(unitCost),
        batch_no: batchNo || undefined,
        expiry_date: expiryDate || undefined,
        ...(newMrp ? { mrp: Number(mrp), ...(lotPrice ? { selling_price: Number(lotPrice) } : {}) } : {}),
      });
      setBarcode("");
      setResolved(null);
      setMrp("");
      setLotPrice("");
      setQuantity("");
      setUnitCost("");
      setBatchNo("");
      setExpiryDate("");
      load();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not add line");
    } finally {
      setBusy(false);
    }
  }

  async function handleComplete() {
    setBusy(true);
    try {
      await api.post(`/api/v1/purchase/grn/${id}/complete`);
      toast.success("GRN completed — stock and cost updated");
      load();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not complete GRN");
    } finally {
      setBusy(false);
    }
  }

  if (error) return <p className="text-sm text-destructive">{error}</p>;
  if (!grn) return <p className="text-sm text-muted-foreground">Loading…</p>;

  const isDraft = grn.status === "draft";

  return (
    <div className="flex flex-col gap-6 max-w-4xl">
      <PageHeader
        title={
          <>
            {grn.grn_number}
            <Badge variant={isDraft ? "outline" : "default"}>{grn.status}</Badge>
          </>
        }
        description={
          grn.purchase_order_id ? (
            <Link href={`/purchase/orders/${grn.purchase_order_id}`} className="underline-offset-2 hover:underline" data-testid="grn-po-link">
              Against {grn.po_number}
            </Link>
          ) : undefined
        }
        actions={
          <>
              {grn.lines.length > 0 && (
                <>
                  <DownloadPdfButton path={`/api/v1/purchase/grn/${grn.grn_id}/pdf`} label={isDraft ? "Draft PDF" : "Download PDF"} size="default" />
                  <PrintPdfButton path={`/api/v1/purchase/grn/${grn.grn_id}/pdf`} />
                </>
              )}
              {isDraft && (
                <DeleteDocumentButton
                  path={`/api/v1/purchase/grn/${grn.grn_id}`}
                  what="GRN"
                  number={grn.grn_number}
                  disabled={busy}
                  note={
                    grn.purchase_order_id
                      ? `Nothing has been received yet, so stock is unchanged and ${grn.po_number}'s pending quantity is free again. You can restore it from the Recycle Bin.`
                      : "Nothing has been received yet, so stock is unchanged. You can restore it from the Recycle Bin."
                  }
                  onDeleted={() => router.push(grn.purchase_order_id ? `/purchase/orders/${grn.purchase_order_id}` : "/purchase/grn")}
                />
              )}
              {isDraft && grn.lines.length > 0 && (
                <Button onClick={handleComplete} disabled={busy}>
                  Complete GRN
                </Button>
              )}
              {grn.status === "completed" &&
                (grn.bill_id ? (
                  <Link href={`/purchase/bills/${grn.bill_id}`} className={buttonVariants({ variant: "outline" })}>
                    Bill {grn.bill_number}
                  </Link>
                ) : (
                  <Link href={`/purchase/bills/new?grn_id=${grn.grn_id}`} className={buttonVariants({ variant: "outline" })}>
                    Enter purchase bill
                  </Link>
                ))}
          </>
        }
      />

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Lines</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          {grn.lines.length === 0 ? (
            <p className="text-sm text-muted-foreground">No lines yet.</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Product</TableHead>
                  <TableHead>Qty</TableHead>
                  <TableHead>Unit Cost</TableHead>
                  <TableHead>Landed Unit Cost</TableHead>
                  <TableHead>Line Total</TableHead>
                  <TableHead>Batch / Expiry</TableHead>
                  <TableHead>MRP</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {grn.lines.map((l) => (
                  <TableRow key={l.line_id}>
                    <TableCell>
                      {l.product_name} <span className="text-muted-foreground">({l.sku})</span>
                    </TableCell>
                    <TableCell>{l.quantity}</TableCell>
                    <TableCell>{rupees(l.unit_cost)}</TableCell>
                    <TableCell>{l.landed_unit_cost ? `${rupees(l.landed_unit_cost)}` : "—"}</TableCell>
                    <TableCell>{rupees(l.line_total)}</TableCell>
                    <TableCell>{l.batch_no ? `${l.batch_no}${l.expiry_date ? ` (exp. ${l.expiry_date})` : ""}` : "—"}</TableCell>
                    <TableCell data-testid={`grn-line-mrp-${l.sku}`}>
                      {rupees(l.mrp ?? l.variant_mrp ?? "0")}
                      {isDraft && l.mrp && l.variant_mrp && Math.abs(Number(l.mrp) - Number(l.variant_mrp)) >= 0.005 && (
                        <span className="block text-xs text-muted-foreground">new MRP · own lot on completion</span>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}

          <Separator />

          <div className="flex flex-col gap-2 text-sm">
            <div className="flex justify-between">
              <span className="text-muted-foreground">Subtotal</span>
              <span>{rupees(grn.subtotal)}</span>
            </div>
            <div className="flex justify-between">
              <span className="text-muted-foreground">Freight</span>
              <span>{rupees(grn.freight_amount)}</span>
            </div>
            <div className="flex justify-between">
              <span className="text-muted-foreground">Other charges</span>
              <span>{rupees(grn.other_charges)}</span>
            </div>
            <div className="flex justify-between font-semibold">
              <span>Grand total</span>
              <span>{rupees(grn.grand_total)}</span>
            </div>
          </div>
        </CardContent>
      </Card>

      {isDraft && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Add a line</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            {grn.purchase_order_id && (
              <p className="text-xs text-muted-foreground">
                This GRN receives {grn.po_number}: only items on that PO can be added, up to what is still pending.
              </p>
            )}
            <form onSubmit={handleLookup} className="flex gap-2 items-end">
              <div className="flex flex-col gap-2 flex-1">
                <Label htmlFor="barcode">Scan or enter barcode</Label>
                <Input id="barcode" value={barcode} onChange={(e) => setBarcode(e.target.value)} placeholder="8901234567890" />
              </div>
              <Button type="submit" variant="outline">
                Look up
              </Button>
            </form>
            <div className="flex flex-col gap-2">
              <Label htmlFor="grnProductSearch">Or search by name or SKU</Label>
              <ProductPicker
                id="grnProductSearch"
                ariaLabel="Search a product to receive"
                status="all"
                value={null}
                placeholder="Search a product"
                onChange={(o) => {
                  setLookupError(null);
                  setBarcode("");
                  setChoosing(null);
                  pick({ product_id: o.productId, product_name: o.productName, variant_id: o.variantId, sku: o.sku, mrp: o.mrp });
                }}
              />
            </div>
            {lookupError && <p className="text-sm text-destructive">{lookupError}</p>}
            {choosing && (
              <ScanChoice
                result={choosing}
                onCancel={() => setChoosing(null)}
                onPick={(o) => {
                  setChoosing(null);
                  pick({ ...choosing, variant_id: o.variant_id, sku: o.sku, mrp: o.mrp, product_name: `${choosing.product_name} · ${optionText(o)}` });
                }}
              />
            )}
            {resolved && (
              <form onSubmit={handleAddLine} className="flex flex-col gap-3 border rounded-md p-3 bg-muted/50">
                <p className="text-sm font-medium">
                  {resolved.product_name} <span className="text-muted-foreground">({resolved.sku})</span>
                  {resolved.pack_label && <span className="ms-2 rounded bg-muted px-1.5 py-0.5 text-xs">{resolved.pack_label}</span>}
                </p>
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                  <div className="flex flex-col gap-2">
                    <Label htmlFor="quantity">Quantity{resolved.unit ? ` (${resolved.unit.symbol})` : ""} *</Label>
                    <Input id="quantity" type="number" min="0.001" step="0.001" required value={quantity} onChange={(e) => setQuantity(e.target.value)} />
                  </div>
                  <div className="flex flex-col gap-2">
                    <Label htmlFor="unitCost">
                      Cost per {resolved.unit?.symbol ?? "unit"} ({currencySymbol()}) *
                    </Label>
                    <Input id="unitCost" type="number" min="0" step="0.01" required value={unitCost} onChange={(e) => setUnitCost(e.target.value)} />
                  </div>
                </div>
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                  <div className="flex flex-col gap-2">
                    <Label htmlFor="batchNo">Batch/lot no. (only if this product tracks batches)</Label>
                    <Input id="batchNo" value={batchNo} onChange={(e) => setBatchNo(e.target.value)} placeholder="e.g. LOT-2026-09-A" />
                  </div>
                  <div className="flex flex-col gap-2">
                    <Label htmlFor="expiryDate">Expiry date (optional)</Label>
                    <Input id="expiryDate" type="date" value={expiryDate} onChange={(e) => setExpiryDate(e.target.value)} />
                  </div>
                </div>
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                  <div className="flex flex-col gap-2">
                    <Label htmlFor="grnMrp">MRP printed on the pack ({currencySymbol()})</Label>
                    <Input id="grnMrp" type="number" min="0.01" step="0.01" value={mrp} onChange={(e) => setMrp(e.target.value)} />
                  </div>
                  {newMrp && (
                    <div className="flex flex-col gap-2">
                      <Label htmlFor="grnLotPrice">Sell the new MRP at ({currencySymbol()})</Label>
                      <Input
                        id="grnLotPrice"
                        type="number"
                        min="0.01"
                        step="0.01"
                        value={lotPrice}
                        placeholder="the item's price"
                        onChange={(e) => setLotPrice(e.target.value)}
                      />
                    </div>
                  )}
                </div>
                {newMrp && (
                  <p className="text-xs text-muted-foreground" data-testid="grn-new-mrp-note">
                    Not the item&apos;s MRP ({rupees(resolved.mrp ?? "0")}). On completion this stock goes into its own MRP lot at {rupees(mrp)}, so the old and new
                    stock sell side by side under the same barcode. A lot that already exists keeps its price.
                  </p>
                )}
                {lotPriceProblem && <p className="text-sm text-destructive">The selling price must be above 0 and not above the MRP</p>}
                <Button type="submit" disabled={busy || lotPriceProblem}>
                  Add line
                </Button>
              </form>
            )}
          </CardContent>
        </Card>
      )}
    </div>
  );
}
