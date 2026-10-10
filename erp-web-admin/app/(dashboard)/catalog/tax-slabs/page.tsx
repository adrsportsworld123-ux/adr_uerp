"use client";

import { Suspense } from "react";
import { MasterConfig, SimpleMasterPage } from "@/components/masters/simple-master-page";

type TaxSlab = {
  tax_slab_id: string;
  name: string;
  cgst_rate: string;
  sgst_rate: string;
  igst_rate: string;
  cess_rate: string;
  status: string;
  product_count: number;
};
type TaxSlabForm = { name: string; cgst_rate: string; sgst_rate: string; igst_rate: string; cess_rate: string };

const RATE_FIELDS = [
  ["cgst_rate", "CGST %"],
  ["sgst_rate", "SGST %"],
  ["igst_rate", "IGST %"],
  ["cess_rate", "Cess %"],
] as const;

function rate(v: string) {
  return v === "" ? 0 : Number(v);
}

const config: MasterConfig<TaxSlab, TaxSlabForm> = {
  entity: "tax_slab",
  noun: "tax slab",
  nounPlural: "Tax Slabs",
  description: "GST rate sets assigned to products. Sales lines store their own tax, so past bills never change.",
  path: "/api/v1/tax-slabs",
  listKey: "tax_slabs",
  idOf: (t) => t.tax_slab_id,
  permission: "catalog.manage",
  defaultSort: "name",
  searchPlaceholder: "Search tax slabs",
  emptyBody: "Add the GST slabs your products fall under, e.g. GST 12% as CGST 6% + SGST 6%.",
  columns: [
    { key: "name", header: "Name", sort: "name", cell: (t) => <span className="font-medium">{t.name}</span> },
    { key: "cgst", header: "CGST", align: "right", cell: (t) => <span className="tabular">{t.cgst_rate}%</span> },
    { key: "sgst", header: "SGST", align: "right", cell: (t) => <span className="tabular">{t.sgst_rate}%</span> },
    { key: "igst", header: "IGST", align: "right", cell: (t) => <span className="tabular">{t.igst_rate}%</span> },
    { key: "cess", header: "Cess", align: "right", cell: (t) => <span className="tabular">{t.cess_rate}%</span> },
    {
      key: "total",
      header: "Total",
      sort: "rate",
      align: "right",
      cell: (t) => (
        <span className="tabular font-medium">
          {(Math.max(rate(t.cgst_rate) + rate(t.sgst_rate), rate(t.igst_rate)) + rate(t.cess_rate)).toFixed(2)}%
        </span>
      ),
    },
  ],
  blank: { name: "", cgst_rate: "", sgst_rate: "", igst_rate: "", cess_rate: "" },
  fromRow: (t) => ({ name: t.name, cgst_rate: t.cgst_rate, sgst_rate: t.sgst_rate, igst_rate: t.igst_rate, cess_rate: t.cess_rate }),
  editNote: "Rates can't be edited once a slab exists. To change a rate, create a new slab, move products to it, then deactivate this one.",
  createOnlyFields: (f, set) => (
    <div className="grid grid-cols-2 gap-3">
      {RATE_FIELDS.map(([key, label]) => (
        <div key={key} className="flex flex-col gap-1.5">
          <label htmlFor={`slab-${key}`} className="text-sm font-medium">
            {label}
          </label>
          <input
            id={`slab-${key}`}
            type="number"
            inputMode="decimal"
            min={0}
            max={100}
            step="0.01"
            value={f[key]}
            placeholder="0"
            onChange={(e) => set({ [key]: e.target.value } as Partial<TaxSlabForm>)}
            className="tabular h-9 rounded-lg border bg-card px-3 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/30"
          />
        </div>
      ))}
      <p className="col-span-2 text-xs text-muted-foreground">
        Intra-state sales use CGST + SGST; inter-state sales use IGST.
      </p>
    </div>
  ),
  validate: (f) => {
    if (!f.name.trim()) return "Enter a name, e.g. “GST 12%”.";
    for (const [key, label] of RATE_FIELDS) {
      const v = rate(f[key]);
      if (Number.isNaN(v) || v < 0 || v > 100) return `${label} must be between 0 and 100.`;
    }
    return null;
  },
  toCreateBody: (f) => ({
    name: f.name.trim(),
    cgst_rate: rate(f.cgst_rate),
    sgst_rate: rate(f.sgst_rate),
    igst_rate: rate(f.igst_rate),
    cess_rate: rate(f.cess_rate),
  }),
};

export default function TaxSlabsPage() {
  return (
    <Suspense>
      <SimpleMasterPage config={config} />
    </Suspense>
  );
}
