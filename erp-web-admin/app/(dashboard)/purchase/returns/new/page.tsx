"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { api, ApiError, SEED_BRANCH_ID } from "@/lib/api-client";
import { Supplier, PurchaseReturnSummary } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

interface DraftLine {
  variant_id: string;
  product_name: string;
  sku: string;
  quantity: number;
  unit_cost: number;
}

export default function NewPurchaseReturnPage() {
  const router = useRouter();
  const [suppliers, setSuppliers] = useState<Supplier[]>([]);
  const [supplierId, setSupplierId] = useState("");
  const [reason, setReason] = useState("");
  const [lines, setLines] = useState<DraftLine[]>([]);

  const [barcode, setBarcode] = useState("");
  const [quantity, setQuantity] = useState("");
  const [unitCost, setUnitCost] = useState("");
  const [lineError, setLineError] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    api.get<{ suppliers: Supplier[] }>("/api/v1/suppliers").then((d) => setSuppliers(d.suppliers));
  }, []);

  async function handleAddLine(e: React.FormEvent) {
    e.preventDefault();
    setLineError(null);
    try {
      const product = await api.get<{ variant_id: string; product_name: string; sku: string }>(
        `/api/v1/products/barcode/${encodeURIComponent(barcode)}`
      );
      setLines((prev) => [
        ...prev,
        { variant_id: product.variant_id, product_name: product.product_name, sku: product.sku, quantity: Number(quantity), unit_cost: Number(unitCost) },
      ]);
      setBarcode("");
      setQuantity("");
      setUnitCost("");
    } catch (e) {
      setLineError(e instanceof ApiError ? e.message : "Could not reach the server");
    }
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      const ret = await api.post<PurchaseReturnSummary>("/api/v1/purchase/returns", {
        supplier_id: supplierId,
        branch_id: SEED_BRANCH_ID,
        reason,
        lines: lines.map((l) => ({ variant_id: l.variant_id, quantity: l.quantity, unit_cost: l.unit_cost })),
      });
      router.push("/purchase/returns");
      void ret;
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not reach the server");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="max-w-lg flex flex-col gap-6">
      <Card>
        <CardHeader>
          <CardTitle>New purchase return</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <div className="flex flex-col gap-2">
            <Label>Supplier *</Label>
            <Select value={supplierId} onValueChange={(v) => setSupplierId(v ?? "")}>
              <SelectTrigger>
                <SelectValue placeholder="Select a supplier" />
              </SelectTrigger>
              <SelectContent>
                {suppliers.map((s) => (
                  <SelectItem key={s.supplier_id} value={s.supplier_id}>
                    {s.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="reason">Reason *</Label>
            <Input id="reason" required value={reason} onChange={(e) => setReason(e.target.value)} placeholder="Damaged in transit" />
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Lines</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          {lines.length > 0 && (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Product</TableHead>
                  <TableHead>Qty</TableHead>
                  <TableHead>Unit cost</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {lines.map((l, i) => (
                  <TableRow key={i}>
                    <TableCell>
                      {l.product_name} <span className="text-zinc-400">({l.sku})</span>
                    </TableCell>
                    <TableCell>{l.quantity}</TableCell>
                    <TableCell>₹{l.unit_cost}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
          <form onSubmit={handleAddLine} className="flex flex-col gap-3 border rounded-md p-3 bg-zinc-50">
            <div className="flex flex-col gap-2">
              <Label htmlFor="barcode">Scan or enter barcode</Label>
              <Input id="barcode" required value={barcode} onChange={(e) => setBarcode(e.target.value)} placeholder="8901234567890" />
            </div>
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
            {lineError && <p className="text-sm text-red-600">{lineError}</p>}
            <Button type="submit" variant="outline">
              Add line
            </Button>
          </form>
        </CardContent>
      </Card>

      {error && <p className="text-sm text-red-600">{error}</p>}
      <Button onClick={handleSubmit} disabled={submitting || !supplierId || !reason || lines.length === 0}>
        {submitting ? "Submitting..." : "Submit return"}
      </Button>
    </div>
  );
}
