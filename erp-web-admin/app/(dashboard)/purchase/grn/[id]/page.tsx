"use client";

import { useEffect, useState, useCallback } from "react";
import { useParams } from "next/navigation";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { GRN } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Separator } from "@/components/ui/separator";

interface BarcodeLookup {
  product_id: string;
  product_name: string;
  variant_id: string;
  sku: string;
}

export default function GRNDetailPage() {
  const { id } = useParams<{ id: string }>();
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
    try {
      const product = await api.get<BarcodeLookup>(`/api/v1/products/barcode/${encodeURIComponent(barcode)}`);
      setResolved(product);
    } catch (e) {
      setLookupError(e instanceof ApiError ? e.message : "Could not reach the server");
    }
  }

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
      });
      setBarcode("");
      setResolved(null);
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

  if (error) return <p className="text-sm text-red-600">{error}</p>;
  if (!grn) return <p className="text-sm text-zinc-500">Loading…</p>;

  const isDraft = grn.status === "draft";

  return (
    <div className="flex flex-col gap-6 max-w-3xl">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">{grn.grn_number}</h1>
          <Badge variant={isDraft ? "outline" : "default"}>{grn.status}</Badge>
        </div>
        {isDraft && grn.lines.length > 0 && (
          <Button onClick={handleComplete} disabled={busy}>
            Complete GRN
          </Button>
        )}
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Lines</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          {grn.lines.length === 0 ? (
            <p className="text-sm text-zinc-500">No lines yet.</p>
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
                </TableRow>
              </TableHeader>
              <TableBody>
                {grn.lines.map((l) => (
                  <TableRow key={l.line_id}>
                    <TableCell>
                      {l.product_name} <span className="text-zinc-400">({l.sku})</span>
                    </TableCell>
                    <TableCell>{l.quantity}</TableCell>
                    <TableCell>₹{l.unit_cost}</TableCell>
                    <TableCell>{l.landed_unit_cost ? `₹${l.landed_unit_cost}` : "—"}</TableCell>
                    <TableCell>₹{l.line_total}</TableCell>
                    <TableCell>{l.batch_no ? `${l.batch_no}${l.expiry_date ? ` (exp. ${l.expiry_date})` : ""}` : "—"}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}

          <Separator />

          <div className="flex flex-col gap-2 text-sm">
            <div className="flex justify-between">
              <span className="text-zinc-500">Subtotal</span>
              <span>₹{grn.subtotal}</span>
            </div>
            <div className="flex justify-between">
              <span className="text-zinc-500">Freight</span>
              <span>₹{grn.freight_amount}</span>
            </div>
            <div className="flex justify-between">
              <span className="text-zinc-500">Other charges</span>
              <span>₹{grn.other_charges}</span>
            </div>
            <div className="flex justify-between font-semibold">
              <span>Grand total</span>
              <span>₹{grn.grand_total}</span>
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
            <form onSubmit={handleLookup} className="flex gap-2 items-end">
              <div className="flex flex-col gap-2 flex-1">
                <Label htmlFor="barcode">Scan or enter barcode</Label>
                <Input id="barcode" value={barcode} onChange={(e) => setBarcode(e.target.value)} placeholder="8901234567890" />
              </div>
              <Button type="submit" variant="outline">
                Look up
              </Button>
            </form>
            {lookupError && <p className="text-sm text-red-600">{lookupError}</p>}
            {resolved && (
              <form onSubmit={handleAddLine} className="flex flex-col gap-3 border rounded-md p-3 bg-zinc-50">
                <p className="text-sm font-medium">
                  {resolved.product_name} <span className="text-zinc-400">({resolved.sku})</span>
                </p>
                <div className="grid grid-cols-2 gap-4">
                  <div className="flex flex-col gap-2">
                    <Label htmlFor="quantity">Quantity *</Label>
                    <Input id="quantity" type="number" min="0.001" step="0.001" required value={quantity} onChange={(e) => setQuantity(e.target.value)} />
                  </div>
                  <div className="flex flex-col gap-2">
                    <Label htmlFor="unitCost">Unit cost (₹) *</Label>
                    <Input id="unitCost" type="number" min="0" step="0.01" required value={unitCost} onChange={(e) => setUnitCost(e.target.value)} />
                  </div>
                </div>
                <div className="grid grid-cols-2 gap-4">
                  <div className="flex flex-col gap-2">
                    <Label htmlFor="batchNo">Batch/lot no. (only if this product tracks batches)</Label>
                    <Input id="batchNo" value={batchNo} onChange={(e) => setBatchNo(e.target.value)} placeholder="e.g. LOT-2026-09-A" />
                  </div>
                  <div className="flex flex-col gap-2">
                    <Label htmlFor="expiryDate">Expiry date (optional)</Label>
                    <Input id="expiryDate" type="date" value={expiryDate} onChange={(e) => setExpiryDate(e.target.value)} />
                  </div>
                </div>
                <Button type="submit" disabled={busy}>
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
