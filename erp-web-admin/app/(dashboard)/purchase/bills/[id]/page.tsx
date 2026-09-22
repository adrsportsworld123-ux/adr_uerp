"use client";

import { useEffect, useState, useCallback } from "react";
import { useParams } from "next/navigation";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Bill } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

const METHODS = ["cash", "card", "upi", "bank_transfer", "cheque"];
const METHOD_LABELS: Record<string, string> = {
  cash: "Cash",
  card: "Card",
  upi: "UPI",
  bank_transfer: "Bank transfer",
  cheque: "Cheque",
};

export default function BillDetailPage() {
  const { id } = useParams<{ id: string }>();
  const [bill, setBill] = useState<Bill | null>(null);
  const [error, setError] = useState<string | null>(null);

  const [amount, setAmount] = useState("");
  const [method, setMethod] = useState("bank_transfer");
  const [referenceNo, setReferenceNo] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [payError, setPayError] = useState<string | null>(null);

  const load = useCallback(() => {
    api
      .get<Bill>(`/api/v1/purchase/bills/${id}`)
      .then((b) => {
        setBill(b);
        const balance = Number(b.grand_total) - Number(b.amount_paid);
        setAmount(balance > 0 ? balance.toFixed(2) : "");
      })
      .catch((e) => setError(e.message));
  }, [id]);

  useEffect(() => {
    load();
  }, [load]);

  async function handlePayment(e: React.FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setPayError(null);
    try {
      await api.post(`/api/v1/purchase/bills/${id}/payments`, {
        amount: Number(amount),
        method,
        reference_no: referenceNo,
      });
      toast.success("Payment recorded");
      load();
    } catch (e) {
      setPayError(e instanceof ApiError ? e.message : "Could not reach the server");
    } finally {
      setSubmitting(false);
    }
  }

  if (error) return <p className="text-sm text-red-600">{error}</p>;
  if (!bill) return <p className="text-sm text-zinc-500">Loading…</p>;

  const balance = Number(bill.grand_total) - Number(bill.amount_paid);

  return (
    <div className="flex flex-col gap-6 max-w-lg">
      <div>
        <h1 className="text-2xl font-semibold">{bill.bill_number}</h1>
        <Badge variant={bill.status === "paid" ? "default" : "outline"}>{bill.status}</Badge>
      </div>

      <Card>
        <CardContent className="flex flex-col gap-2 text-sm pt-6">
          <div className="flex justify-between">
            <span className="text-zinc-500">Subtotal (landed cost)</span>
            <span>
              ₹{bill.subtotal} + freight ₹{bill.freight_amount} + other ₹{bill.other_charges}
            </span>
          </div>
          <div className="flex justify-between">
            <span className="text-zinc-500">GST</span>
            <span>₹{bill.tax_total}</span>
          </div>
          <div className="flex justify-between font-semibold">
            <span>Grand total</span>
            <span>₹{bill.grand_total}</span>
          </div>
          <div className="flex justify-between">
            <span className="text-zinc-500">Paid</span>
            <span>₹{bill.amount_paid}</span>
          </div>
          <div className="flex justify-between font-semibold">
            <span>Balance</span>
            <span>₹{balance.toFixed(2)}</span>
          </div>
        </CardContent>
      </Card>

      {balance > 0 && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Record a payment</CardTitle>
          </CardHeader>
          <CardContent>
            <form onSubmit={handlePayment} className="flex flex-col gap-4">
              <div className="grid grid-cols-2 gap-4">
                <div className="flex flex-col gap-2">
                  <Label htmlFor="amount">Amount (₹) *</Label>
                  <Input id="amount" type="number" min="0.01" step="0.01" required value={amount} onChange={(e) => setAmount(e.target.value)} />
                </div>
                <div className="flex flex-col gap-2">
                  <Label>Method *</Label>
                  <Select
                    value={method}
                    onValueChange={(v) => setMethod(v ?? "bank_transfer")}
                    items={Object.fromEntries(METHODS.map((m) => [m, METHOD_LABELS[m]]))}
                  >
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {METHODS.map((m) => (
                        <SelectItem key={m} value={m}>
                          {METHOD_LABELS[m]}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
              </div>
              <div className="flex flex-col gap-2">
                <Label htmlFor="reference">Reference number</Label>
                <Input id="reference" value={referenceNo} onChange={(e) => setReferenceNo(e.target.value)} />
              </div>
              {payError && <p className="text-sm text-red-600">{payError}</p>}
              <Button type="submit" disabled={submitting}>
                {submitting ? "Recording..." : "Record payment"}
              </Button>
            </form>
          </CardContent>
        </Card>
      )}
    </div>
  );
}
