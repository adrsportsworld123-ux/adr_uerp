"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { api, ApiError } from "@/lib/api-client";
import { Branch, Transfer } from "@/lib/types";
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
}

export default function NewTransferPage() {
  const router = useRouter();
  const [branches, setBranches] = useState<Branch[]>([]);
  const [fromBranchId, setFromBranchId] = useState("");
  const [toBranchId, setToBranchId] = useState("");
  const [notes, setNotes] = useState("");
  const [lines, setLines] = useState<DraftLine[]>([]);

  const [barcode, setBarcode] = useState("");
  const [quantity, setQuantity] = useState("");
  const [lineError, setLineError] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    api.get<{ branches: Branch[] }>("/api/v1/branches").then((d) => setBranches(d.branches));
  }, []);

  async function handleAddLine(e: React.FormEvent) {
    e.preventDefault();
    setLineError(null);
    try {
      const product = await api.get<{ variant_id: string; product_name: string; sku: string }>(
        `/api/v1/products/barcode/${encodeURIComponent(barcode)}`
      );
      setLines((prev) => [...prev, { variant_id: product.variant_id, product_name: product.product_name, sku: product.sku, quantity: Number(quantity) }]);
      setBarcode("");
      setQuantity("");
    } catch (e) {
      setLineError(e instanceof ApiError ? e.message : "Could not reach the server");
    }
  }

  async function handleSubmit() {
    setSubmitting(true);
    setError(null);
    try {
      const transfer = await api.post<Transfer>("/api/v1/branch-transfers", {
        from_branch_id: fromBranchId,
        to_branch_id: toBranchId,
        notes,
        lines: lines.map((l) => ({ variant_id: l.variant_id, quantity: l.quantity })),
      });
      router.push(`/transfers/${transfer.transfer_id}`);
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
          <CardTitle>New branch transfer</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <div className="grid grid-cols-2 gap-4">
            <div className="flex flex-col gap-2">
              <Label>From branch *</Label>
              <Select value={fromBranchId} onValueChange={(v) => setFromBranchId(v ?? "")}>
                <SelectTrigger>
                  <SelectValue placeholder="Source" />
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
            <div className="flex flex-col gap-2">
              <Label>To branch *</Label>
              <Select value={toBranchId} onValueChange={(v) => setToBranchId(v ?? "")}>
                <SelectTrigger>
                  <SelectValue placeholder="Destination" />
                </SelectTrigger>
                <SelectContent>
                  {branches.filter((b) => b.branch_id !== fromBranchId).map((b) => (
                    <SelectItem key={b.branch_id} value={b.branch_id}>
                      {b.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="notes">Notes</Label>
            <Input id="notes" value={notes} onChange={(e) => setNotes(e.target.value)} />
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
                </TableRow>
              </TableHeader>
              <TableBody>
                {lines.map((l, i) => (
                  <TableRow key={i}>
                    <TableCell>
                      {l.product_name} <span className="text-zinc-400">({l.sku})</span>
                    </TableCell>
                    <TableCell>{l.quantity}</TableCell>
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
            <div className="flex flex-col gap-2">
              <Label htmlFor="quantity">Quantity *</Label>
              <Input id="quantity" type="number" min="0.001" step="0.001" required value={quantity} onChange={(e) => setQuantity(e.target.value)} />
            </div>
            {lineError && <p className="text-sm text-red-600">{lineError}</p>}
            <Button type="submit" variant="outline">
              Add line
            </Button>
          </form>
        </CardContent>
      </Card>

      {error && <p className="text-sm text-red-600">{error}</p>}
      <Button onClick={handleSubmit} disabled={submitting || !fromBranchId || !toBranchId || lines.length === 0}>
        {submitting ? "Submitting..." : "Request transfer"}
      </Button>
    </div>
  );
}
