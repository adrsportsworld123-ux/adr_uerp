"use client";

// Reorder suggestions → purchase orders (erp-core-go §3.77). Tick the
// items to order, adjust the quantity (it starts at the suggestion, rounded
// up to a whole unit) and the supplier (it starts at whoever supplied the
// item last), then "Create purchase orders": one draft PO per supplier and
// branch, through the ordinary POST /purchase/orders, at the last cost paid
// (or the current cost price). Drafts are checked and issued on the PO page
// as usual; nothing is sent to a supplier from here.
import { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { Loader2, ShoppingCart } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Branch, PurchaseOrder, ReorderSuggestion, Supplier } from "@/lib/types";
import { rupees } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { StatusChip } from "@/components/data/status-chip";
import { selectClass } from "@/components/data/panel";

type Pick = { qty: string; supplierId: string };
const keyOf = (s: ReorderSuggestion) => `${s.branch_id}|${s.variant_id}`;
const startQty = (s: ReorderSuggestion) => String(Math.max(1, Math.ceil(Number(s.suggested_reorder_qty) || 0)));
const unitCost = (s: ReorderSuggestion) => Number(s.last_unit_cost ?? s.cost_price ?? 0) || 0;

export function ReorderToPO({ suggestions, branches, loading }: { suggestions: ReorderSuggestion[]; branches: Branch[]; loading: boolean }) {
  const [suppliers, setSuppliers] = useState<Supplier[]>([]);
  const [picked, setPicked] = useState<Record<string, Pick>>({});
  const [busy, setBusy] = useState(false);
  const [created, setCreated] = useState<{ po_id: string; po_number: string; supplier: string; lines: number }[]>([]);

  useEffect(() => {
    api
      .get<{ suppliers: Supplier[] }>("/api/v1/suppliers")
      .then((d) => setSuppliers(d.suppliers))
      .catch(() => {});
  }, []);

  const branchName = (id: string) => branches.find((b) => b.branch_id === id)?.name ?? "—";
  const supplierName = (id: string) => suppliers.find((s) => s.supplier_id === id)?.name ?? "—";
  const manyBranches = new Set(suggestions.map((s) => s.branch_id)).size > 1;

  function toggle(s: ReorderSuggestion, on: boolean) {
    setPicked((p) => {
      const next = { ...p };
      if (on) next[keyOf(s)] = p[keyOf(s)] ?? { qty: startQty(s), supplierId: s.last_supplier_id ?? "" };
      else delete next[keyOf(s)];
      return next;
    });
  }
  const set = (s: ReorderSuggestion, patch: Partial<Pick>) => setPicked((p) => ({ ...p, [keyOf(s)]: { ...p[keyOf(s)], ...patch } }));
  const allOn = suggestions.length > 0 && suggestions.every((s) => picked[keyOf(s)]);

  const chosen = suggestions.filter((s) => picked[keyOf(s)]);
  const problems = useMemo(() => {
    const out: string[] = [];
    if (chosen.some((s) => !picked[keyOf(s)].supplierId)) out.push("choose a supplier for every ticked item");
    if (chosen.some((s) => !(Number(picked[keyOf(s)].qty) > 0))) out.push("every ticked item needs a quantity above 0");
    return out;
  }, [chosen, picked]);
  const groups = useMemo(() => {
    const g = new Map<string, ReorderSuggestion[]>();
    for (const s of chosen) {
      const k = `${picked[keyOf(s)].supplierId}|${s.branch_id}`;
      g.set(k, [...(g.get(k) ?? []), s]);
    }
    return g;
  }, [chosen, picked]);

  async function create() {
    setBusy(true);
    const made: typeof created = [];
    try {
      for (const [k, items] of groups) {
        const [supplierId, branchId] = k.split("|");
        const po = await api.post<PurchaseOrder>("/api/v1/purchase/orders", {
          supplier_id: supplierId,
          branch_id: branchId,
          notes: "Raised from reorder suggestions",
          lines: items.map((s) => ({ variant_id: s.variant_id, quantity: Number(picked[keyOf(s)].qty), unit_cost: unitCost(s) })),
        });
        made.push({ po_id: po.po_id, po_number: po.po_number, supplier: supplierName(supplierId), lines: items.length });
      }
      toast.success(`${made.length} draft purchase order(s) created`);
      setPicked({});
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not create the purchase orders");
    } finally {
      setCreated((c) => [...made, ...c]);
      setBusy(false);
    }
  }

  return (
    <div className="flex flex-col gap-3">
      {created.length > 0 && (
        <div className="rounded-lg border border-pay/40 bg-pay-soft/40 p-3 text-sm" data-testid="reorder-created">
          <span className="font-medium">Draft purchase orders created:</span>{" "}
          {created.map((c, i) => (
            <span key={c.po_id}>
              {i > 0 && " · "}
              <Link href={`/purchase/orders/${c.po_id}`} className="text-primary hover:underline">
                {c.po_number}
              </Link>{" "}
              ({c.supplier}, {c.lines} item(s))
            </span>
          ))}
          <span className="block text-xs text-muted-foreground">Check each one and issue it to the supplier from its page.</span>
        </div>
      )}
      <Table data-testid="reorder-table">
        <TableHeader>
          <TableRow>
            <TableHead className="w-8">
              <input type="checkbox" aria-label="Select every suggestion" checked={allOn} onChange={(e) => suggestions.forEach((s) => toggle(s, e.target.checked))} />
            </TableHead>
            <TableHead>Product</TableHead>
            {manyBranches && <TableHead>Branch</TableHead>}
            <TableHead className="text-right">Available</TableHead>
            <TableHead className="text-right">Reorder pt.</TableHead>
            <TableHead className="text-right">Per day</TableHead>
            <TableHead className="text-right">Days left</TableHead>
            <TableHead className="text-right">Suggested</TableHead>
            <TableHead>Order qty</TableHead>
            <TableHead>Supplier</TableHead>
            <TableHead className="text-right">Unit cost</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {suggestions.map((s) => {
            const p = picked[keyOf(s)];
            return (
              <TableRow key={keyOf(s)}>
                <TableCell>
                  <input type="checkbox" aria-label={`Order ${s.product_name}`} checked={!!p} onChange={(e) => toggle(s, e.target.checked)} />
                </TableCell>
                <TableCell className="max-w-64 whitespace-normal">
                  {s.product_name} <span className="text-xs text-muted-foreground">({s.sku})</span>
                  <StatusChip status="" tone="bad" label={s.reason} className="mt-1" />
                </TableCell>
                {manyBranches && <TableCell className="text-sm">{branchName(s.branch_id)}</TableCell>}
                <TableCell className="tabular text-right">{s.available}</TableCell>
                <TableCell className="tabular text-right">{s.reorder_point}</TableCell>
                <TableCell className="tabular text-right">{s.daily_velocity}</TableCell>
                <TableCell className="tabular text-right">{s.days_of_stock_remaining ?? "—"}</TableCell>
                <TableCell className="tabular text-right font-medium">{s.suggested_reorder_qty}</TableCell>
                <TableCell>
                  <Input
                    type="number"
                    min="0.001"
                    step="any"
                    className="h-8 w-24"
                    aria-label={`Quantity of ${s.product_name}`}
                    disabled={!p}
                    value={p?.qty ?? startQty(s)}
                    onChange={(e) => set(s, { qty: e.target.value })}
                  />
                </TableCell>
                <TableCell>
                  <select
                    aria-label={`Supplier for ${s.product_name}`}
                    disabled={!p}
                    value={p?.supplierId ?? s.last_supplier_id ?? ""}
                    onChange={(e) => set(s, { supplierId: e.target.value })}
                    className={`${selectClass} h-8 w-44`}
                  >
                    <option value="">Choose a supplier</option>
                    {suppliers.map((x) => (
                      <option key={x.supplier_id} value={x.supplier_id}>
                        {x.name}
                        {x.supplier_id === s.last_supplier_id ? " (last)" : ""}
                      </option>
                    ))}
                  </select>
                </TableCell>
                <TableCell className="tabular text-right text-sm">
                  {rupees(unitCost(s))}
                  {s.last_unit_cost == null && <span className="block text-xs text-muted-foreground">cost price</span>}
                </TableCell>
              </TableRow>
            );
          })}
          {suggestions.length === 0 && !loading && (
            <TableRow>
              <TableCell colSpan={manyBranches ? 11 : 10} className="py-6 text-center text-sm text-muted-foreground">
                Nothing is at risk of stocking out right now
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
      {suggestions.length > 0 && (
        <div className="flex flex-wrap items-center gap-3">
          <Button onClick={create} disabled={busy || chosen.length === 0 || problems.length > 0}>
            {busy ? <Loader2 className="animate-spin" aria-hidden /> : <ShoppingCart aria-hidden />}
            Create purchase orders{chosen.length > 0 ? ` (${groups.size} PO, ${chosen.length} item(s))` : ""}
          </Button>
          <span className="text-xs text-muted-foreground">
            {problems.length > 0 ? problems.join("; ") : "One draft PO per supplier and branch, at the last cost paid. Nothing is sent until you issue it."}
          </span>
        </div>
      )}
    </div>
  );
}
