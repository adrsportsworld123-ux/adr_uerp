"use client";

import { Suspense, useEffect, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { api, ApiError } from "@/lib/api-client";
import { GRNSummary, Bill } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

import { rupees } from "@/lib/format";
import { currencySymbol } from "@/lib/currency";
export default function NewBillPage() {
  return (
    <Suspense>
      <NewBillForm />
    </Suspense>
  );
}

// ?grn_id= preselects the GRN (the "Enter bill" links on a GRN and a PO).
function NewBillForm() {
  const router = useRouter();
  const [grns, setGrns] = useState<GRNSummary[]>([]);
  const [grnId, setGrnId] = useState(useSearchParams().get("grn_id") ?? "");
  const [supplierInvoiceNumber, setSupplierInvoiceNumber] = useState("");
  const [taxTotal, setTaxTotal] = useState("0");
  // How the supplier's tax splits (erp-core-go migrations/060): "auto" lets
  // the server compare the supplier's GSTIN state with the branch's.
  const [supply, setSupply] = useState<"auto" | "intra" | "inter">("auto");
  const [dueDate, setDueDate] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    // A GRN is billed once; already-billed ones aren't offered.
    api.get<{ grns: GRNSummary[] }>("/api/v1/purchase/grn?status=completed").then((d) => setGrns(d.grns.filter((g) => !g.billed)));
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
        ...(supply === "auto" ? {} : { inter_state: supply === "inter" }),
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
                items={Object.fromEntries(grns.map((g) => [g.grn_id, `${g.grn_number} — ${rupees(g.grand_total)}`]))}
                required
              >
                <SelectTrigger>
                  <SelectValue placeholder="Select a completed GRN" />
                </SelectTrigger>
                <SelectContent>
                  {grns.map((g) => (
                    <SelectItem key={g.grn_id} value={g.grn_id}>
                      {g.grn_number} — {rupees(g.grand_total)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              {grns.length === 0 && <p className="text-xs text-muted-foreground">No completed GRNs available to bill yet.</p>}
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="invoiceNumber">Supplier invoice number</Label>
              <Input id="invoiceNumber" value={supplierInvoiceNumber} onChange={(e) => setSupplierInvoiceNumber(e.target.value)} />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="supply">Supplier&apos;s tax is</Label>
              <select id="supply" value={supply} onChange={(e) => setSupply(e.target.value as typeof supply)} className="h-9 rounded-lg border bg-card px-2 text-sm">
                <option value="auto">Decided by the supplier&apos;s GSTIN state</option>
                <option value="intra">CGST + SGST (same state)</option>
                <option value="inter">IGST (another state)</option>
              </select>
              <p className="text-xs text-muted-foreground">
                {supply === "inter"
                  ? "The whole tax is recorded as IGST."
                  : supply === "intra"
                    ? "The tax is recorded half as CGST and half as SGST."
                    : "IGST when the supplier's GSTIN is from another state than the branch, else CGST + SGST."}
              </p>
            </div>
            <div className="grid grid-cols-2 gap-4">
              <div className="flex flex-col gap-2">
                <Label htmlFor="tax">GST on this bill ({currencySymbol()})</Label>
                <Input id="tax" type="number" min="0" step="0.01" value={taxTotal} onChange={(e) => setTaxTotal(e.target.value)} />
              </div>
              <div className="flex flex-col gap-2">
                <Label htmlFor="dueDate">Due date</Label>
                <Input id="dueDate" type="date" value={dueDate} onChange={(e) => setDueDate(e.target.value)} />
              </div>
            </div>
            {selectedGrn && (
              <p className="text-xs text-muted-foreground">
                Bill grand total will be {rupees(selectedGrn.grand_total)} (GRN) + {rupees(taxTotal || 0)} (GST) = {currencySymbol()}
                {(Number(selectedGrn.grand_total) + (Number(taxTotal) || 0)).toFixed(2)}
              </p>
            )}
            {error && <p className="text-sm text-destructive">{error}</p>}
            <Button type="submit" disabled={submitting || !grnId}>
              {submitting ? "Creating..." : "Create bill"}
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  );
}
