"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { api, ApiError } from "@/lib/api-client";
import { GRNSummary, Bill } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

export default function NewBillPage() {
  const router = useRouter();
  const [grns, setGrns] = useState<GRNSummary[]>([]);
  const [grnId, setGrnId] = useState("");
  const [supplierInvoiceNumber, setSupplierInvoiceNumber] = useState("");
  const [taxTotal, setTaxTotal] = useState("0");
  const [dueDate, setDueDate] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api.get<{ grns: GRNSummary[] }>("/api/v1/purchase/grn?status=completed").then((d) => setGrns(d.grns));
  }, []);

  const selectedGrn = grns.find((g) => g.grn_id === grnId);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      const bill = await api.post<Bill>("/api/v1/purchase/bills", {
        grn_id: grnId,
        supplier_invoice_number: supplierInvoiceNumber,
        tax_total: Number(taxTotal) || 0,
        due_date: dueDate || undefined,
      });
      router.push(`/purchase/bills/${bill.bill_id}`);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not reach the server");
      setSubmitting(false);
    }
  }

  return (
    <div className="max-w-lg">
      <Card>
        <CardHeader>
          <CardTitle>New purchase bill</CardTitle>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleSubmit} className="flex flex-col gap-4">
            <div className="flex flex-col gap-2">
              <Label>Completed GRN *</Label>
              <Select
                value={grnId}
                onValueChange={(v) => setGrnId(v ?? "")}
                items={Object.fromEntries(grns.map((g) => [g.grn_id, `${g.grn_number} — ₹${g.grand_total}`]))}
                required
              >
                <SelectTrigger>
                  <SelectValue placeholder="Select a completed GRN" />
                </SelectTrigger>
                <SelectContent>
                  {grns.map((g) => (
                    <SelectItem key={g.grn_id} value={g.grn_id}>
                      {g.grn_number} — ₹{g.grand_total}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              {grns.length === 0 && <p className="text-xs text-zinc-500">No completed GRNs available to bill yet.</p>}
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="invoiceNumber">Supplier invoice number</Label>
              <Input id="invoiceNumber" value={supplierInvoiceNumber} onChange={(e) => setSupplierInvoiceNumber(e.target.value)} />
            </div>
            <div className="grid grid-cols-2 gap-4">
              <div className="flex flex-col gap-2">
                <Label htmlFor="tax">GST on this bill (₹)</Label>
                <Input id="tax" type="number" min="0" step="0.01" value={taxTotal} onChange={(e) => setTaxTotal(e.target.value)} />
              </div>
              <div className="flex flex-col gap-2">
                <Label htmlFor="dueDate">Due date</Label>
                <Input id="dueDate" type="date" value={dueDate} onChange={(e) => setDueDate(e.target.value)} />
              </div>
            </div>
            {selectedGrn && (
              <p className="text-xs text-zinc-500">
                Bill grand total will be ₹{selectedGrn.grand_total} (GRN) + ₹{taxTotal || 0} (GST) = ₹
                {(Number(selectedGrn.grand_total) + (Number(taxTotal) || 0)).toFixed(2)}
              </p>
            )}
            {error && <p className="text-sm text-red-600">{error}</p>}
            <Button type="submit" disabled={submitting || !grnId}>
              {submitting ? "Creating..." : "Create bill"}
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  );
}
