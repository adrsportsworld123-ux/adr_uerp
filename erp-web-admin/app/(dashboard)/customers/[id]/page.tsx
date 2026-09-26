"use client";

import { useCallback, useEffect, useState } from "react";
import { useParams } from "next/navigation";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { CustomerCredit, CustomerDetail, LoyaltyBalance, PriceListSummary } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

const SEGMENT_VARIANT: Record<string, "default" | "secondary" | "outline" | "destructive"> = {
  vip: "default",
  regular: "secondary",
  new: "outline",
  dormant: "destructive",
};

export default function CustomerDetailPage() {
  const { id } = useParams<{ id: string }>();
  const [customer, setCustomer] = useState<CustomerDetail | null>(null);
  const [loyalty, setLoyalty] = useState<LoyaltyBalance | null>(null);
  const [credit, setCredit] = useState<CustomerCredit | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState(false);
  const [busy, setBusy] = useState(false);

  const [form, setForm] = useState({ name: "", phone: "", email: "", address: "", company_name: "", gstin: "" });

  const load = useCallback(() => {
    api
      .get<CustomerDetail>(`/api/v1/customers/${id}`)
      .then((c) => {
        setCustomer(c);
        setForm({ name: c.name, phone: c.phone, email: c.email, address: c.address, company_name: c.company_name, gstin: c.gstin });
      })
      .catch((e) => setError(e.message));
  }, [id]);

  useEffect(load, [load]);

  useEffect(() => {
    // Best-effort: fetched separately from the profile so a transient
    // failure here (or this merchant simply never configuring loyalty)
    // doesn't block the rest of the page from rendering.
    api
      .get<LoyaltyBalance>(`/api/v1/customers/${id}/loyalty`)
      .then(setLoyalty)
      .catch(() => setLoyalty(null));
  }, [id]);

  const loadCredit = useCallback(() => {
    api
      .get<CustomerCredit>(`/api/v1/customers/${id}/credit`)
      .then(setCredit)
      .catch(() => setCredit(null));
  }, [id]);
  useEffect(loadCredit, [loadCredit]);

  async function save() {
    setBusy(true);
    try {
      await api.patch(`/api/v1/customers/${id}`, form);
      toast.success("Customer updated");
      setEditing(false);
      load();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not update customer");
    } finally {
      setBusy(false);
    }
  }

  if (error) return <p className="text-sm text-red-600">{error}</p>;
  if (!customer) return <p className="text-sm text-zinc-500">Loading…</p>;

  return (
    <div className="flex flex-col gap-6 max-w-2xl">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">{customer.name}</h1>
        <Badge variant={SEGMENT_VARIANT[customer.segment] ?? "outline"}>{customer.segment}</Badge>
      </div>

      <Card>
        <CardHeader className="flex flex-row items-center justify-between">
          <CardTitle className="text-base">Profile</CardTitle>
          {!editing && (
            <Button size="sm" variant="outline" onClick={() => setEditing(true)}>
              Edit
            </Button>
          )}
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          {editing ? (
            <>
              <div className="grid grid-cols-2 gap-4">
                <div className="flex flex-col gap-2">
                  <Label>Name</Label>
                  <Input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
                </div>
                <div className="flex flex-col gap-2">
                  <Label>Phone</Label>
                  <Input value={form.phone} onChange={(e) => setForm({ ...form, phone: e.target.value })} />
                </div>
              </div>
              <div className="flex flex-col gap-2">
                <Label>Email</Label>
                <Input value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} />
              </div>
              <div className="flex flex-col gap-2">
                <Label>Address</Label>
                <Input value={form.address} onChange={(e) => setForm({ ...form, address: e.target.value })} />
              </div>
              {customer.customer_type === "b2b" && (
                <>
                  <div className="flex flex-col gap-2">
                    <Label>Company name</Label>
                    <Input value={form.company_name} onChange={(e) => setForm({ ...form, company_name: e.target.value })} />
                  </div>
                  <div className="flex flex-col gap-2">
                    <Label>GSTIN</Label>
                    <Input value={form.gstin} onChange={(e) => setForm({ ...form, gstin: e.target.value })} />
                  </div>
                </>
              )}
              <div className="flex gap-2">
                <Button size="sm" disabled={busy} onClick={save}>
                  Save
                </Button>
                <Button size="sm" variant="ghost" onClick={() => setEditing(false)}>
                  Cancel
                </Button>
              </div>
            </>
          ) : (
            <dl className="grid grid-cols-2 gap-3 text-sm">
              <dt className="text-zinc-500">Type</dt>
              <dd className="uppercase">{customer.customer_type}</dd>
              <dt className="text-zinc-500">Phone</dt>
              <dd>{customer.phone || "—"}</dd>
              <dt className="text-zinc-500">Email</dt>
              <dd>{customer.email || "—"}</dd>
              <dt className="text-zinc-500">Address</dt>
              <dd>{customer.address || "—"}</dd>
              {customer.customer_type === "b2b" && (
                <>
                  <dt className="text-zinc-500">Company</dt>
                  <dd>{customer.company_name || "—"}</dd>
                  <dt className="text-zinc-500">GSTIN</dt>
                  <dd>{customer.gstin || "—"}</dd>
                </>
              )}
              <dt className="text-zinc-500">Total spend</dt>
              <dd>₹{customer.total_spend}</dd>
              <dt className="text-zinc-500">Transactions</dt>
              <dd>{customer.transaction_count}</dd>
              <dt className="text-zinc-500">Last purchase</dt>
              <dd>{customer.last_purchase_at ?? "Never"}</dd>
            </dl>
          )}
        </CardContent>
      </Card>

      {credit && <CreditSection customerId={id} credit={credit} onChanged={loadCredit} />}

      {customer && (
        <PriceListSection key={customer.price_list_id} customerId={id} customer={customer} onChanged={load} />
      )}

      {loyalty && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Loyalty points</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            <p className="text-sm">
              Available balance: <span className="font-semibold">{loyalty.available_points} points</span>
            </p>
            <p className="text-xs text-zinc-500">
              Redeeming or earning happens at the POS terminal, not here — this is a read-only view of the ledger
              erp-core-go already keeps.
            </p>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Type</TableHead>
                  <TableHead>Points</TableHead>
                  <TableHead>Balance after</TableHead>
                  <TableHead>Date</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {loyalty.ledger.map((entry, i) => (
                  <TableRow key={i}>
                    <TableCell className="capitalize">{entry.entry_type}</TableCell>
                    <TableCell className={entry.points < 0 ? "text-red-600" : "text-green-700"}>
                      {entry.points > 0 ? `+${entry.points}` : entry.points}
                    </TableCell>
                    <TableCell>{entry.balance_after}</TableCell>
                    <TableCell>{entry.created_at}</TableCell>
                  </TableRow>
                ))}
                {loyalty.ledger.length === 0 && (
                  <TableRow>
                    <TableCell colSpan={4} className="text-center text-sm text-zinc-500 py-6">
                      No loyalty activity yet
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      )}

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Purchase history</CardTitle>
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Order</TableHead>
                <TableHead>Branch</TableHead>
                <TableHead>Date</TableHead>
                <TableHead>Total</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {customer.recent_orders.map((o) => (
                <TableRow key={o.order_number}>
                  <TableCell className="font-mono text-xs">{o.order_number}</TableCell>
                  <TableCell>{o.branch_name}</TableCell>
                  <TableCell>{o.finalized_at}</TableCell>
                  <TableCell>₹{o.grand_total}</TableCell>
                </TableRow>
              ))}
              {customer.recent_orders.length === 0 && (
                <TableRow>
                  <TableCell colSpan={4} className="text-center text-sm text-zinc-500 py-6">
                    No purchases yet
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
          <p className="text-xs text-zinc-500 mt-2">
            Purchase history is consolidated across every branch — a customer&apos;s profile isn&apos;t tied to where they first registered.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}

// ---------------------------------------------------------------------
// Credit facility (Phase 4, sub-area 1) — outstanding balance, aging,
// settings (limit/terms/hold, gated by credit.manage — attempted and
// surfaced as a toast error on 403, same pattern Promotions & Loyalty
// already established for its own gated writes), and a per-invoice
// "record payment" action. Deliberately no "make a credit sale" control
// here — that's the POS terminal's job (erp-pos-flutter), same boundary
// this app draws everywhere else (Pricing, Promotions & Loyalty).
// ---------------------------------------------------------------------

const PAYMENT_TERMS_LABEL: Record<string, string> = {
  due_on_receipt: "Due on receipt",
  net_7: "Net 7",
  net_15: "Net 15",
  net_30: "Net 30",
  net_60: "Net 60",
  net_90: "Net 90",
};

function CreditSection({ customerId, credit, onChanged }: { customerId: string; credit: CustomerCredit; onChanged: () => void }) {
  const [editing, setEditing] = useState(false);
  const [creditLimit, setCreditLimit] = useState(credit.credit_limit);
  const [paymentTerms, setPaymentTerms] = useState(credit.payment_terms);
  const [creditHold, setCreditHold] = useState(credit.credit_hold);
  const [busy, setBusy] = useState(false);
  const [payingInvoice, setPayingInvoice] = useState<string | null>(null);
  const [paymentAmount, setPaymentAmount] = useState("");
  const [paymentMethod, setPaymentMethod] = useState<"cash" | "card" | "upi" | "bank_transfer" | "cheque">("bank_transfer");

  async function saveSettings() {
    setBusy(true);
    try {
      await api.patch(`/api/v1/customers/${customerId}/credit`, {
        credit_limit: Number(creditLimit),
        payment_terms: paymentTerms,
        credit_hold: creditHold,
      });
      toast.success("Credit settings saved");
      setEditing(false);
      onChanged();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not save credit settings");
    } finally {
      setBusy(false);
    }
  }

  async function recordPayment(orderId: string) {
    setBusy(true);
    try {
      await api.post(`/api/v1/customers/${customerId}/payments`, {
        sales_order_id: orderId,
        amount: Number(paymentAmount),
        method: paymentMethod,
      });
      toast.success("Payment recorded");
      setPayingInvoice(null);
      setPaymentAmount("");
      onChanged();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not record payment");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle className="text-base">Credit</CardTitle>
        <div className="flex items-center gap-2">
          {credit.credit_hold && <Badge variant="destructive">Credit hold</Badge>}
          {!editing && (
            <Button size="sm" variant="outline" onClick={() => setEditing(true)}>
              Edit
            </Button>
          )}
        </div>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {editing ? (
          <div className="flex flex-col gap-4">
            <div className="grid grid-cols-2 gap-4">
              <div className="flex flex-col gap-2">
                <Label htmlFor="creditLimit">Credit limit (₹)</Label>
                <Input id="creditLimit" type="number" min="0" step="0.01" value={creditLimit} onChange={(e) => setCreditLimit(e.target.value)} />
              </div>
              <div className="flex flex-col gap-2">
                <Label>Payment terms</Label>
                <Select
                  value={paymentTerms}
                  onValueChange={(v) => setPaymentTerms((v as typeof paymentTerms) ?? "due_on_receipt")}
                  items={PAYMENT_TERMS_LABEL}
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {Object.entries(PAYMENT_TERMS_LABEL).map(([value, label]) => (
                      <SelectItem key={value} value={value}>
                        {label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            </div>
            <label className="flex items-center gap-2 text-sm">
              <input type="checkbox" checked={creditHold} onChange={(e) => setCreditHold(e.target.checked)} />
              Credit hold (blocks any new credit sale regardless of limit)
            </label>
            <div className="flex gap-2">
              <Button size="sm" disabled={busy} onClick={saveSettings}>
                Save
              </Button>
              <Button size="sm" variant="ghost" onClick={() => setEditing(false)}>
                Cancel
              </Button>
            </div>
          </div>
        ) : (
          <dl className="grid grid-cols-2 gap-3 text-sm">
            <dt className="text-zinc-500">Credit limit</dt>
            <dd>₹{credit.credit_limit}</dd>
            <dt className="text-zinc-500">Payment terms</dt>
            <dd>{PAYMENT_TERMS_LABEL[credit.payment_terms] ?? credit.payment_terms}</dd>
            <dt className="text-zinc-500">Outstanding</dt>
            <dd className="font-semibold">₹{credit.outstanding_total}</dd>
          </dl>
        )}

        <div className="grid grid-cols-5 gap-2 text-xs border-t pt-3">
          <div className="flex flex-col gap-1">
            <span className="text-zinc-500">Current</span>
            <span>₹{credit.aging.current}</span>
          </div>
          <div className="flex flex-col gap-1">
            <span className="text-zinc-500">0-30 days</span>
            <span>₹{credit.aging.days_0_30}</span>
          </div>
          <div className="flex flex-col gap-1">
            <span className="text-zinc-500">30-60 days</span>
            <span>₹{credit.aging.days_30_60}</span>
          </div>
          <div className="flex flex-col gap-1">
            <span className="text-zinc-500">60-90 days</span>
            <span>₹{credit.aging.days_60_90}</span>
          </div>
          <div className="flex flex-col gap-1">
            <span className="text-red-600">90+ days</span>
            <span className="text-red-600">₹{credit.aging.days_90_plus}</span>
          </div>
        </div>

        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Order</TableHead>
              <TableHead>Due</TableHead>
              <TableHead>Amount</TableHead>
              <TableHead>Paid</TableHead>
              <TableHead>Outstanding</TableHead>
              <TableHead></TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {credit.open_invoices.map((inv) => (
              <TableRow key={inv.order_id}>
                <TableCell className="font-mono text-xs">{inv.order_number}</TableCell>
                <TableCell className="text-xs">{inv.due_date || "—"}</TableCell>
                <TableCell>₹{inv.credit_amount}</TableCell>
                <TableCell>₹{inv.credit_paid}</TableCell>
                <TableCell className="font-medium">₹{inv.outstanding}</TableCell>
                <TableCell>
                  {payingInvoice === inv.order_id ? (
                    <div className="flex gap-1 items-center">
                      <Input
                        type="number"
                        min="0.01"
                        step="0.01"
                        max={inv.outstanding}
                        className="w-24 h-7"
                        value={paymentAmount}
                        onChange={(e) => setPaymentAmount(e.target.value)}
                      />
                      <Select
                        value={paymentMethod}
                        onValueChange={(v) => setPaymentMethod((v as typeof paymentMethod) ?? "bank_transfer")}
                        items={{ bank_transfer: "Bank transfer", cheque: "Cheque", cash: "Cash", card: "Card", upi: "UPI" }}
                      >
                        <SelectTrigger className="w-28 h-7">
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem value="bank_transfer">Bank transfer</SelectItem>
                          <SelectItem value="cheque">Cheque</SelectItem>
                          <SelectItem value="cash">Cash</SelectItem>
                          <SelectItem value="card">Card</SelectItem>
                          <SelectItem value="upi">UPI</SelectItem>
                        </SelectContent>
                      </Select>
                      <Button size="sm" disabled={busy} onClick={() => recordPayment(inv.order_id)}>
                        Record
                      </Button>
                      <Button size="sm" variant="ghost" onClick={() => setPayingInvoice(null)}>
                        Cancel
                      </Button>
                    </div>
                  ) : (
                    <Button size="sm" variant="outline" onClick={() => setPayingInvoice(inv.order_id)}>
                      Record payment
                    </Button>
                  )}
                </TableCell>
              </TableRow>
            ))}
            {credit.open_invoices.length === 0 && (
              <TableRow>
                <TableCell colSpan={6} className="text-center text-sm text-zinc-500 py-6">
                  No outstanding credit sales
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  );
}

// ---------------------------------------------------------------------
// Phase 7: wholesale price list assignment. "" (no price list) means this
// customer keeps buying at plain retail selling_price — see erp-core-go's
// internal/pricing.ResolvePrice. Price lists themselves are managed on
// the Pricing > Price Lists screen; this is just the assignment.
// ---------------------------------------------------------------------

function PriceListSection({ customerId, customer, onChanged }: { customerId: string; customer: CustomerDetail; onChanged: () => void }) {
  const [lists, setLists] = useState<PriceListSummary[]>([]);
  const [selected, setSelected] = useState(customer.price_list_id);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api
      .get<{ price_lists: PriceListSummary[] }>("/api/v1/pricing/price-lists")
      .then((d) => setLists(d.price_lists))
      .catch(() => setLists([]));
  }, []);

  async function save() {
    setBusy(true);
    try {
      await api.patch(`/api/v1/customers/${customerId}`, { price_list_id: selected });
      toast.success("Price list assignment saved");
      onChanged();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not save price list assignment");
    } finally {
      setBusy(false);
    }
  }

  const listNames: Record<string, string> = { "": "Retail (no price list)", ...Object.fromEntries(lists.map((l) => [l.price_list_id, l.name])) };

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Wholesale price list</CardTitle>
      </CardHeader>
      <CardContent className="flex items-end gap-2">
        <div className="flex flex-col gap-2 w-64">
          <Label>Assigned price list</Label>
          <Select value={selected} onValueChange={(v) => setSelected(v ?? "")} items={listNames}>
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="">Retail (no price list)</SelectItem>
              {lists.map((l) => (
                <SelectItem key={l.price_list_id} value={l.price_list_id}>
                  {l.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <Button size="sm" disabled={busy || selected === customer.price_list_id} onClick={save}>
          Assign
        </Button>
      </CardContent>
    </Card>
  );
}
