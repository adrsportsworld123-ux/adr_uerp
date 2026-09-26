"use client";

// Phase 7: Wholesale/B2B & Omnichannel (erp-core-go's phased_roadmap.md;
// internal/quotations). A quotation is a pricing proposal for a named
// customer, built whole (not incrementally like a POS cart). The only
// place it becomes a real, stock-reserving order is Convert, which opens
// a real cart through the exact same pipeline a walk-in POS sale uses —
// see internal/quotations' package doc for why that's the whole point.
import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Branch, Customer, Product, Quotation, QuotationSummary } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

const STATUS_VARIANT: Record<string, "default" | "secondary" | "outline" | "destructive"> = {
  draft: "outline",
  sent: "secondary",
  accepted: "default",
  converted: "default",
  rejected: "destructive",
  expired: "destructive",
};

type DraftLine = { variantId: string; quantity: string };

export default function QuotationsPage() {
  const [branches, setBranches] = useState<Branch[]>([]);
  const [customers, setCustomers] = useState<Customer[]>([]);
  const [products, setProducts] = useState<Product[]>([]);
  const [quotations, setQuotations] = useState<QuotationSummary[]>([]);
  const [statusFilter, setStatusFilter] = useState("");

  const [branchId, setBranchId] = useState("");
  const [customerId, setCustomerId] = useState("");
  const [validUntil, setValidUntil] = useState("");
  const [notes, setNotes] = useState("");
  const [lines, setLines] = useState<DraftLine[]>([{ variantId: "", quantity: "1" }]);
  const [creating, setCreating] = useState(false);

  const [selected, setSelected] = useState<Quotation | null>(null);
  const [busyAction, setBusyAction] = useState(false);

  const fetchQuotations = useCallback(() => {
    const params = new URLSearchParams();
    if (statusFilter) params.set("status", statusFilter);
    return api.get<{ quotations: QuotationSummary[] }>(`/api/v1/quotations?${params.toString()}`);
  }, [statusFilter]);

  useEffect(() => {
    api.get<{ branches: Branch[] }>("/api/v1/branches").then((d) => setBranches(d.branches)).catch(() => {});
    api.get<{ customers: Customer[] }>("/api/v1/customers?limit=200").then((d) => setCustomers(d.customers)).catch(() => {});
    api.get<{ products: Product[] }>("/api/v1/products?limit=200").then((d) => setProducts(d.products)).catch(() => {});
    fetchQuotations().then((d) => setQuotations(d.quotations)).catch(() => {});
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  function refreshList() {
    fetchQuotations()
      .then((d) => setQuotations(d.quotations))
      .catch((e) => toast.error(e instanceof ApiError ? e.message : "Could not load quotations"));
  }

  function openQuotation(id: string) {
    api
      .get<Quotation>(`/api/v1/quotations/${id}`)
      .then(setSelected)
      .catch((e) => toast.error(e instanceof ApiError ? e.message : "Could not load quotation"));
  }

  function addLine() {
    setLines([...lines, { variantId: "", quantity: "1" }]);
  }
  function removeLine(i: number) {
    setLines(lines.filter((_, idx) => idx !== i));
  }
  function updateLine(i: number, patch: Partial<DraftLine>) {
    setLines(lines.map((l, idx) => (idx === i ? { ...l, ...patch } : l)));
  }

  async function createQuotation() {
    const validLines = lines.filter((l) => l.variantId && Number(l.quantity) > 0);
    if (!branchId || !customerId || validLines.length === 0) {
      toast.error("Branch, customer, and at least one line are required");
      return;
    }
    setCreating(true);
    try {
      await api.post("/api/v1/quotations", {
        branch_id: branchId,
        customer_id: customerId,
        valid_until: validUntil || undefined,
        notes,
        lines: validLines.map((l) => ({ variant_id: l.variantId, quantity: Number(l.quantity) })),
      });
      toast.success("Quotation created");
      setBranchId("");
      setCustomerId("");
      setValidUntil("");
      setNotes("");
      setLines([{ variantId: "", quantity: "1" }]);
      refreshList();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not create quotation");
    } finally {
      setCreating(false);
    }
  }

  async function transition(action: "send" | "accept" | "reject" | "convert") {
    if (!selected) return;
    setBusyAction(true);
    try {
      const result = await api.post<{ order_id?: string }>(`/api/v1/quotations/${selected.quotation_id}/${action}`);
      if (action === "convert" && result.order_id) {
        toast.success(`Converted to sales order ${result.order_id}`);
      } else {
        toast.success(`Quotation ${action === "send" ? "sent" : action + "ed"}`);
      }
      openQuotation(selected.quotation_id);
      refreshList();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : `Could not ${action} quotation`);
    } finally {
      setBusyAction(false);
    }
  }

  async function deleteQuotation() {
    if (!selected) return;
    setBusyAction(true);
    try {
      await api.delete(`/api/v1/quotations/${selected.quotation_id}`);
      toast.success("Quotation deleted");
      setSelected(null);
      refreshList();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not delete quotation");
    } finally {
      setBusyAction(false);
    }
  }

  const variantOptions = products.flatMap((p) => p.variants.map((v) => ({ id: v.variant_id, label: `${p.name} (${v.sku})` })));

  return (
    <div className="flex flex-col gap-6 max-w-5xl">
      <h1 className="text-2xl font-semibold">B2B Quotations</h1>
      <p className="text-sm text-zinc-500">
        A quote is a pricing proposal for a customer — it never touches stock until it&apos;s accepted and converted,
        at which point it becomes a real order through the same cart pipeline any walk-in sale uses. Leave a
        line&apos;s price blank to auto-resolve the customer&apos;s wholesale price (if assigned one) or plain retail
        price.
      </p>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">New quotation</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <div className="grid grid-cols-3 gap-4">
            <div className="flex flex-col gap-2">
              <Label>Branch</Label>
              <Select value={branchId} onValueChange={(v) => setBranchId(v ?? "")} items={Object.fromEntries(branches.map((b) => [b.branch_id, b.name]))}>
                <SelectTrigger><SelectValue placeholder="Select branch" /></SelectTrigger>
                <SelectContent>
                  {branches.map((b) => (
                    <SelectItem key={b.branch_id} value={b.branch_id}>{b.name}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-2">
              <Label>Customer</Label>
              <Select value={customerId} onValueChange={(v) => setCustomerId(v ?? "")} items={Object.fromEntries(customers.map((c) => [c.customer_id, c.name || c.phone]))}>
                <SelectTrigger><SelectValue placeholder="Select customer" /></SelectTrigger>
                <SelectContent>
                  {customers.map((c) => (
                    <SelectItem key={c.customer_id} value={c.customer_id}>{c.name || c.phone} {c.customer_type === "b2b" ? "(B2B)" : ""}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="validUntil">Valid until (optional, default +30 days)</Label>
              <Input id="validUntil" type="date" value={validUntil} onChange={(e) => setValidUntil(e.target.value)} />
            </div>
          </div>

          <div className="flex flex-col gap-2">
            <Label htmlFor="notes">Notes</Label>
            <Textarea id="notes" value={notes} onChange={(e) => setNotes(e.target.value)} rows={2} />
          </div>

          <div className="flex flex-col gap-2">
            <Label>Lines</Label>
            {lines.map((l, i) => (
              <div key={i} className="flex items-end gap-2">
                <div className="flex-1">
                  <Select value={l.variantId} onValueChange={(v) => updateLine(i, { variantId: v ?? "" })} items={Object.fromEntries(variantOptions.map((o) => [o.id, o.label]))}>
                    <SelectTrigger><SelectValue placeholder="Select a product" /></SelectTrigger>
                    <SelectContent>
                      {variantOptions.map((o) => (
                        <SelectItem key={o.id} value={o.id}>{o.label}</SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <Input className="w-28" type="number" min="1" value={l.quantity} onChange={(e) => updateLine(i, { quantity: e.target.value })} placeholder="Qty" />
                <Button variant="outline" onClick={() => removeLine(i)} disabled={lines.length === 1}>Remove</Button>
              </div>
            ))}
            <Button variant="outline" onClick={addLine} className="w-fit">+ Add line</Button>
          </div>

          <Button onClick={createQuotation} disabled={creating} className="w-fit">
            {creating ? "Creating..." : "Create quotation"}
          </Button>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="flex flex-row items-center justify-between">
          <CardTitle className="text-base">Quotations</CardTitle>
          <div className="w-48">
            <Select value={statusFilter} onValueChange={(v) => setStatusFilter(v ?? "")} items={{ "": "All statuses", draft: "Draft", sent: "Sent", accepted: "Accepted", rejected: "Rejected", expired: "Expired", converted: "Converted" }}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="">All statuses</SelectItem>
                <SelectItem value="draft">Draft</SelectItem>
                <SelectItem value="sent">Sent</SelectItem>
                <SelectItem value="accepted">Accepted</SelectItem>
                <SelectItem value="rejected">Rejected</SelectItem>
                <SelectItem value="expired">Expired</SelectItem>
                <SelectItem value="converted">Converted</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </CardHeader>
        <CardContent>
          <Button variant="outline" size="sm" className="mb-3" onClick={refreshList}>Refresh</Button>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Quote #</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Valid until</TableHead>
                <TableHead>Grand total</TableHead>
                <TableHead></TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {quotations.map((q) => (
                <TableRow key={q.quotation_id}>
                  <TableCell>{q.quote_number}</TableCell>
                  <TableCell><Badge variant={STATUS_VARIANT[q.status] ?? "outline"}>{q.status}</Badge></TableCell>
                  <TableCell>{q.valid_until}</TableCell>
                  <TableCell>₹{q.grand_total}</TableCell>
                  <TableCell>
                    <Button size="sm" variant="outline" onClick={() => openQuotation(q.quotation_id)}>View</Button>
                  </TableCell>
                </TableRow>
              ))}
              {quotations.length === 0 && (
                <TableRow>
                  <TableCell colSpan={5} className="text-center text-sm text-zinc-500 py-6">No quotations yet</TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      {selected && (
        <Card>
          <CardHeader className="flex flex-row items-center justify-between">
            <CardTitle className="text-base">{selected.quote_number}</CardTitle>
            <Badge variant={STATUS_VARIANT[selected.status] ?? "outline"}>{selected.status}</Badge>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            {selected.notes && <p className="text-sm text-zinc-600">{selected.notes}</p>}
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Product</TableHead>
                  <TableHead>Qty</TableHead>
                  <TableHead>Unit price</TableHead>
                  <TableHead>Line total</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {selected.lines.map((l) => (
                  <TableRow key={l.line_id}>
                    <TableCell>{l.product_name} <span className="text-xs text-zinc-500">({l.sku})</span></TableCell>
                    <TableCell>{l.quantity}</TableCell>
                    <TableCell>₹{l.unit_price}</TableCell>
                    <TableCell>₹{l.line_total}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            <div className="text-sm text-right font-medium">Grand total: ₹{selected.grand_total}</div>

            {selected.converted_sales_order_id && (
              <p className="text-sm text-emerald-700">Converted to sales order {selected.converted_sales_order_id}</p>
            )}

            <div className="flex gap-2">
              {selected.status === "draft" && (
                <>
                  <Button size="sm" disabled={busyAction} onClick={() => transition("send")}>Send</Button>
                  <Button size="sm" variant="destructive" disabled={busyAction} onClick={deleteQuotation}>Delete</Button>
                </>
              )}
              {selected.status === "sent" && (
                <>
                  <Button size="sm" disabled={busyAction} onClick={() => transition("accept")}>Accept</Button>
                  <Button size="sm" variant="destructive" disabled={busyAction} onClick={() => transition("reject")}>Reject</Button>
                </>
              )}
              {selected.status === "accepted" && (
                <Button size="sm" disabled={busyAction} onClick={() => transition("convert")}>Convert to order</Button>
              )}
            </div>
          </CardContent>
        </Card>
      )}
    </div>
  );
}
