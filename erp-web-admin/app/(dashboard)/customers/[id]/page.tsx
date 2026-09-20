"use client";

import { useCallback, useEffect, useState } from "react";
import { useParams } from "next/navigation";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { CustomerDetail, LoyaltyBalance } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";

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
