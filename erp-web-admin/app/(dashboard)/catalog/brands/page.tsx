"use client";

import { Suspense } from "react";
import { MasterConfig, SimpleMasterPage } from "@/components/masters/simple-master-page";

type Brand = { brand_id: string; name: string; status: string; product_count: number };
type BrandForm = { name: string };

const config: MasterConfig<Brand, BrandForm> = {
  entity: "brand",
  noun: "brand",
  nounPlural: "Brands",
  description: "Manufacturer and label names used to group and filter products.",
  path: "/api/v1/brands",
  listKey: "brands",
  idOf: (b) => b.brand_id,
  permission: "catalog.manage",
  defaultSort: "name",
  searchPlaceholder: "Search brands",
  emptyBody: "Add the brands you stock so products can be filtered and reported by brand.",
  columns: [{ key: "name", header: "Brand", sort: "name", cell: (b) => <span className="font-medium">{b.name}</span> }],
  blank: { name: "" },
  fromRow: (b) => ({ name: b.name }),
  validate: (f) => (f.name.trim() ? null : "Enter a brand name."),
  toCreateBody: (f) => ({ name: f.name.trim() }),
};

export default function BrandsPage() {
  return (
    <Suspense>
      <SimpleMasterPage config={config} />
    </Suspense>
  );
}
