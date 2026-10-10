"use client";

import { Suspense, useEffect, useState } from "react";
import { api } from "@/lib/api-client";
import { MasterConfig, SimpleMasterPage } from "@/components/masters/simple-master-page";

type Category = { category_id: string; parent_id: string | null; name: string; path: string; status: string; product_count: number };
type CategoryForm = { name: string; parent_id: string };

// Parent is chosen once, at creation: moving a subtree later would change
// every descendant's path and the reach of category-scoped promotions and
// price rules, so the API doesn't offer re-parenting (see taxonomy.go).
function ParentPicker({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const [options, setOptions] = useState<Category[] | null>(null);
  useEffect(() => {
    api
      .get<{ categories: Category[] }>("/api/v1/categories?status=active&sort=path")
      .then((d) => setOptions(d.categories))
      .catch(() => setOptions([]));
  }, []);
  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor="category-parent" className="text-sm font-medium">
        Parent category
      </label>
      <select
        id="category-parent"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={options === null}
        className="h-9 rounded-lg border bg-card px-2 text-sm"
      >
        <option value="">None (top level)</option>
        {(options ?? []).map((c) => (
          <option key={c.category_id} value={c.category_id}>
            {c.path.replace(/^\//, "").replace(/\//g, " › ")}
          </option>
        ))}
      </select>
      <p className="text-xs text-muted-foreground">Can&rsquo;t be changed after the category is created.</p>
    </div>
  );
}

const config: MasterConfig<Category, CategoryForm> = {
  entity: "category",
  noun: "category",
  nounPlural: "Categories",
  description: "The product tree used for browsing at the POS, attribute sets, promotions and reports.",
  path: "/api/v1/categories",
  listKey: "categories",
  idOf: (c) => c.category_id,
  permission: "catalog.manage",
  defaultSort: "path",
  searchPlaceholder: "Search categories",
  emptyBody: "Create top-level categories like Cricket or Badminton, then sub-categories under them.",
  columns: [
    {
      key: "path",
      header: "Category",
      sort: "path",
      cell: (c) => {
        const parts = c.path.replace(/^\//, "").split("/");
        const parent = parts.slice(0, -1).join(" › ");
        return (
          <div className="flex flex-col leading-tight" style={{ paddingLeft: `${(parts.length - 1) * 16}px` }}>
            <span className="font-medium">{c.name}</span>
            {parent && <span className="text-xs text-muted-foreground">{parent}</span>}
          </div>
        );
      },
    },
  ],
  blank: { name: "", parent_id: "" },
  fromRow: (c) => ({ name: c.name, parent_id: c.parent_id ?? "" }),
  editNote: "Renaming updates this category's path and every sub-category's path. The parent can't be changed.",
  createOnlyFields: (f, set) => <ParentPicker value={f.parent_id} onChange={(v) => set({ parent_id: v })} />,
  validate: (f) => (f.name.trim() ? null : "Enter a category name."),
  toCreateBody: (f) => ({ name: f.name.trim(), parent_id: f.parent_id }),
};

export default function CategoriesPage() {
  return (
    <Suspense>
      <SimpleMasterPage config={config} />
    </Suspense>
  );
}
