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
import { DownloadPdfButton, PrintPdfButton } from "@/components/data/document-actions";
import { BankAccountSelect, defaultAccount, useBankAccounts } from "@/components/banking/bank-account-select";
import Link from "next/link";

import { rupees } from "@/lib/format";
import { currencySymbol } from "@/lib/currency";
import { PartnerSelect, usePartners } from "@/components/partners/use-partners";

const METHOD_LABELS: Record<string, string> = {
  cash: "Cash",
  card: "Card",
  upi: "UPI",
  bank_transfer: "Bank transfer",
  cheque: "Cheque",
  // erp-core-go migrations/074: a partner paid the supplier from their own
  // money; it adds to their capital. Offered only when there are partners.
  partner: "Paid by a partner",
};

export default function BillDetailPage() {
  const { id } = useParams<{ id: string }>();
  const [bill, setBill] = useState<Bill | null>(null);
  const [error, setError] = useState<string | null>(null);

  const [amount, setAmount] = useState("");
  const [method, setMethod] = useState("bank_transfer");
  const [referenceNo, setReferenceNo] = useState("");
  // Bank accounts (erp-core-go migrations/066): which account a transfer
  // or cheque went from; a cheque from one is also written on the cheque
  // register. With no bank account set up, the form is as it always was.
  const accounts = useBankAccounts();
  const [bankAccountId, setBankAccountId] = useState<string | null>(null);
  const bankAccount = bankAccountId ?? defaultAccount(accounts)?.bank_account_id ?? "";
  const [chequeNumber, setChequeNumber] = useState("");
  const [chequeDate, setChequeDate] = useState("");
  const [accountPayee, setAccountPayee] = useState(true);
  const throughBank = (method === "bank_transfer" || method === "cheque") && accounts.length > 0;
  const partners = usePartners();
  const [partnerId, setPartnerId] = useState("");
  const METHODS = ["cash", "card", "upi", "bank_transfer", "cheque", ...(partners.length > 0 ? ["partner"] : [])];
  const [submitting, setSubmitting] = useState(false);
  const [payError, setPayError] = useState<string | null>(null);

  const load = useCallback(() => {
    api
      .get<Bill>(`/api/v1/purchase/bills/${id}`)
      .then((b) => {
        setBill(b);
        const balance = Number(b.grand_total) - Number(b.amount_paid);
        setAmount(balance > 0 ? balance.toFixed(2) : "");
        // A partner funds this bill's purchase order: they pay it, unless changed.
        if (b.funded_by_partner_id) {
          setMethod("partner");
          setPartnerId(b.funded_by_partner_id);
        }
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
        ...(method === "partner" ? { partner_id: partnerId } : {}),
        ...(throughBank && bankAccount ? { bank_account_id: bankAccount } : {}),
        ...(throughBank && bankAccount && method === "cheque"
          ? { cheque_number: chequeNumber, cheque_date: chequeDate, account_payee: accountPayee }
          : {}),
      });
      toast.success(throughBank && bankAccount && method === "cheque" ? "Payment recorded — the cheque is on the register to print" : "Payment recorded");
      setChequeNumber("");
      load();
    } catch (e) {
      setPayError(e instanceof ApiError ? e.message : "Could not reach the server");
    } finally {
      setSubmitting(false);
    }
  }

  if (error) return <p className="text-sm text-destructive">{error}</p>;
  if (!bill) return <p className="text-sm text-muted-foreground">Loading…</p>;

  const balance = Number(bill.grand_total) - Number(bill.amount_paid);

  return (
    <div className="flex flex-col gap-6 max-w-lg">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold">{bill.bill_number}</h1>
          <Badge variant={bill.status === "paid" ? "default" : "outline"}>{bill.status}</Badge>
        </div>
        <div className="flex items-center gap-2">
          <DownloadPdfButton path={`/api/v1/purchase/bills/${bill.bill_id}/pdf`} size="default" />
          <PrintPdfButton path={`/api/v1/purchase/bills/${bill.bill_id}/pdf`} />
        </div>
      </div>

      <Card>
        <CardContent className="flex flex-col gap-2 text-sm pt-6">
          <div className="flex justify-between">
            <span className="text-muted-foreground">Subtotal (landed cost)</span>
            <span>
              {rupees(bill.subtotal)} + freight {rupees(bill.freight_amount)} + other {rupees(bill.other_charges)}
            </span>
          </div>
          {(bill.tax_rows?.length ? bill.tax_rows : [{ label: "GST", amount: bill.tax_total }]).map((t) => (
            <div key={t.label} className="flex justify-between" data-testid="bill-tax-row">
              <span className="text-muted-foreground">{t.label}</span>
              <span>{rupees(t.amount)}</span>
            </div>
          ))}
          <div className="flex justify-between font-semibold">
            <span>Grand total</span>
            <span>{rupees(bill.grand_total)}</span>
          </div>
          <div className="flex justify-between">
            <span className="text-muted-foreground">Paid</span>
            <span>{rupees(bill.amount_paid)}</span>
          </div>
          <div className="flex justify-between font-semibold">
            <span>Balance</span>
            <span>{rupees(balance.toFixed(2))}</span>
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
                  <Label htmlFor="amount">Amount ({currencySymbol()}) *</Label>
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
              {method === "partner" && (
                <div className="flex flex-col gap-2">
                  <Label htmlFor="pay-partner">Partner who paid *</Label>
                  <PartnerSelect id="pay-partner" partners={partners} value={partnerId} onChange={setPartnerId} />
                  <span className="text-xs text-muted-foreground">
                    Paid from their own money: it is added to their capital, not taken from the shop&apos;s cash or bank.
                    {bill.funded_by_partner && ` ${bill.funded_by_partner} funds this purchase order.`}
                  </span>
                </div>
              )}
              {throughBank && (
                <div className="flex flex-col gap-2">
                  <Label htmlFor="pay-bank">From account</Label>
                  <BankAccountSelect id="pay-bank" accounts={accounts} value={bankAccount} onChange={setBankAccountId} allowNone className="w-full" />
                </div>
              )}
              {throughBank && method === "cheque" && bankAccount && (
                <div className="grid grid-cols-2 gap-4">
                  <div className="flex flex-col gap-2">
                    <Label htmlFor="pay-cheque-no">Cheque number</Label>
                    <Input
                      id="pay-cheque-no"
                      inputMode="numeric"
                      value={chequeNumber}
                      onChange={(e) => setChequeNumber(e.target.value.replace(/\D/g, ""))}
                      placeholder="The next leaf"
                    />
                  </div>
                  <div className="flex flex-col gap-2">
                    <Label htmlFor="pay-cheque-date">Cheque date</Label>
                    <Input id="pay-cheque-date" type="date" value={chequeDate} onChange={(e) => setChequeDate(e.target.value)} />
                  </div>
                  <label className="col-span-2 flex items-center gap-2 text-sm">
                    <input type="checkbox" checked={accountPayee} onChange={(e) => setAccountPayee(e.target.checked)} /> Cross &quot;A/c Payee&quot;
                    <Link href="/cheques" className="ms-auto text-xs text-muted-foreground hover:underline">
                      Cheque register
                    </Link>
                  </label>
                </div>
              )}
              <div className="flex flex-col gap-2">
                <Label htmlFor="reference">Reference number</Label>
                <Input id="reference" value={referenceNo} onChange={(e) => setReferenceNo(e.target.value)} />
              </div>
              {payError && <p className="text-sm text-destructive">{payError}</p>}
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
