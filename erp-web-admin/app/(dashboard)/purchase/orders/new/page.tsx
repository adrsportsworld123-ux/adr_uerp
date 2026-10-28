"use client";

// A new purchase order, saved as a draft (erp-core-go POST
// /purchase/orders). Totals come back from the server; the draft can be
// reviewed and its PDF checked before it is issued to the supplier.
import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { Plus, Trash2 } from "lucide-react";
import { api, ApiError } from "@/lib/api-client";
import { PurchaseOrder, Supplier } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { PageHeader } from "@/components/data/page-header";
import { Field, Panel, selectClass } from "@/components/data/panel";
import { BranchSelect, useMyBranches } from "@/components/data/branch-select";
import { ProductPicker, VariantOption } from "@/components/data/product-picker";
import { PartnerSelect, usePartners } from "@/components/partners/use-partners";

import { currencySymbol } from "@/lib/currency";
type DraftLine = { option: VariantOption | null; quantity: string; unitCost: string };
const blankLine = (): DraftLine => ({ option: null, quantity: "1", unitCost: "" });

export default function NewPurchaseOrderPage() {
  const router = useRouter();
  const { branches, defaultBranchId } = useMyBranches();
  const [suppliers, setSuppliers] = useState<Supplier[]>([]);
  const [supplierId, setSupplierId] = useState("");
  const [branchId, setBranchId] = useState("");
  const [expectedDate, setExpectedDate] = useState("");
  const [notes, setNotes] = useState("");
  // erp-core-go migrations/074: the partner putting up the money, if any.
  const partners = usePartners();
  const [fundedBy, setFundedBy] = useState("");
  const [lines, setLines] = useState<DraftLine[]>([blankLine()]);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const branch = branchId || defaultBranchId;

  useEffect(() => {
    api
      .get<{ suppliers: Supplier[] }>("/api/v1/suppliers")
      .then((d) => setSuppliers(d.suppliers.filter((s) => s.status === "active")))
      .catch(() => {});
  }, []);

  const update = (i: number, patch: Partial<DraftLine>) => setLines(lines.map((l, idx) => (idx === i ? { ...l, ...patch } : l)));
  const ready = lines.filter((l) => l.option && Number(l.quantity) > 0 && l.unitCost !== "" && Number(l.unitCost) >= 0);

  async function save() {
    setSubmitting(true);
    setError(null);
    try {
      const po = await api.post<PurchaseOrder>("/api/v1/purchase/orders", {
        supplier_id: supplierId,
        branch_id: branch,
        expected_date: expectedDate || undefined,
        notes,
        ...(fundedBy ? { funded_by_partner_id: fundedBy } : {}),
        lines: ready.map((l) => ({ variant_id: l.option!.variantId, quantity: Number(l.quantity), unit_cost: Number(l.unitCost) })),
      });
      router.push(`/purchase/orders/${po.po_id}`);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not reach the server");
      setSubmitting(false);
    }
  }

  return (
    <div className="flex max-w-4xl flex-col gap-5">
      <PageHeader title="New purchase order" description="Saved as a draft. Review it, then issue it to the supplier." />

      <Panel title="Order">
        <div className="grid gap-4 sm:grid-cols-3">
          <Field label="Supplier *" htmlFor="poSupplier">
            <select id="poSupplier" value={supplierId} onChange={(e) => setSupplierId(e.target.value)} className={`${selectClass} w-full`}>
              <option value="">Select a supplier</option>
              {suppliers.map((s) => (
                <option key={s.supplier_id} value={s.supplier_id}>
                  {s.name}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Deliver to branch *" htmlFor="poBranch">
            <BranchSelect id="poBranch" branches={branches} value={branch} onChange={setBranchId} className="w-full" />
          </Field>
          <Field label="Expected delivery" htmlFor="poExpected">
            <Input id="poExpected" type="date" value={expectedDate} onChange={(e) => setExpectedDate(e.target.value)} />
          </Field>
        </div>
        <Field label="Notes for the supplier" htmlFor="poNotes">
          <Textarea id="poNotes" rows={2} value={notes} onChange={(e) => setNotes(e.target.value)} />
        </Field>
        {partners.length > 0 && (
          <Field label="Funded by partner (optional)" htmlFor="poFundedBy" className="sm:max-w-xs">
            <PartnerSelect id="poFundedBy" partners={partners} value={fundedBy} onChange={setFundedBy} noneLabel="The shop" />
          </Field>
        )}
      </Panel>

      <Panel title="Items" description="Quantity and the agreed unit cost before GST. The PO's total is worked out when you save.">
        <div className="flex flex-col gap-2">
          {lines.map((l, i) => (
            <div key={i} className="flex flex-wrap items-end gap-2">
              <Field label={i === 0 ? "Product" : ""} className="min-w-64 flex-1">
                <ProductPicker ariaLabel={`Line ${i + 1} product`} value={l.option} onChange={(o) => update(i, { option: o })} />
              </Field>
              <Field label={i === 0 ? "Quantity" : ""} className="w-28">
                <Input aria-label={`Line ${i + 1} quantity`} type="number" min="0.001" step="0.001" value={l.quantity} onChange={(e) => update(i, { quantity: e.target.value })} />
              </Field>
              <Field label={i === 0 ? `Unit cost (${currencySymbol()})` : ""} className="w-32">
                <Input aria-label={`Line ${i + 1} unit cost`} type="number" min="0" step="0.01" value={l.unitCost} onChange={(e) => update(i, { unitCost: e.target.value })} />
              </Field>
              <Button variant="ghost" size="icon" aria-label={`Remove line ${i + 1}`} disabled={lines.length === 1} onClick={() => setLines(lines.filter((_, idx) => idx !== i))}>
                <Trash2 />
              </Button>
            </div>
          ))}
          <Button variant="outline" className="w-fit" onClick={() => setLines([...lines, blankLine()])}>
            <Plus /> Add item
          </Button>
        </div>
      </Panel>

      {error && (
        <p className="text-sm text-destructive" role="alert">
          {error}
        </p>
      )}
      <div className="flex gap-2">
        <Button onClick={save} disabled={submitting || !supplierId || !branch || ready.length === 0 || ready.length !== lines.length}>
          {submitting ? "Saving…" : "Save draft"}
        </Button>
        <Button variant="outline" onClick={() => router.push("/purchase/orders")}>
          Cancel
        </Button>
      </div>
    </div>
  );
}
