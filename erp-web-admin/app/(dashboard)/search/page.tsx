"use client";

// Product Search (internal/search — OpenSearch-backed), distinct from the
// Pricing screen's GET /products browse: free-text and typo-tolerant
// (fuzzy multi_match server-side), filtered by category (with its
// subcategories), brand and price, with live stock beside each hit — the
// API reads quantities from Postgres, never from the index, so they're
// current. Built on the shared list kit like every other list. When
// OpenSearch isn't reachable the table shows the API's "temporarily
// unavailable" message with a retry instead of breaking.
import { Suspense, useEffect, useState } from "react";
import Link from "next/link";
import { api } from "@/lib/api-client";
import { CatalogBrand, Category, SearchHit } from "@/lib/types";
import { qty } from "@/lib/format";
import { buttonVariants } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Column, DataTable } from "@/components/data/data-table";
import { PageHeader } from "@/components/data/page-header";
import { SearchOption, SearchSelect, categoryOption } from "@/components/data/search-select";
import { useListState } from "@/components/data/use-list-state";
import { useServerList } from "@/components/data/use-server-list";

function marginPct(sellingPrice: number, costPrice: number): number | null {
  if (sellingPrice <= 0) return null;
  return Math.round(((sellingPrice - costPrice) / sellingPrice) * 100 * 100) / 100;
}

const columns: Column<SearchHit>[] = [
  {
    key: "name",
    header: "Product",
    cell: (h) => (
      <div className="flex flex-col leading-tight">
        <span className="font-medium">{h.name}</span>
        {h.hsn_code && <span className="text-xs text-muted-foreground">HSN {h.hsn_code}</span>}
      </div>
    ),
  },
  { key: "sku", header: "SKU", cell: (h) => <span className="font-mono text-xs">{h.sku}</span> },
  { key: "category", header: "Category", cell: (h) => h.category_name || "—" },
  { key: "brand", header: "Brand", cell: (h) => h.brand_name || "—" },
  {
    key: "available",
    header: "Available",
    align: "right",
    cell: (h) => {
      const available = Number(h.available ?? 0);
      const onHand = Number(h.on_hand ?? 0);
      return (
        <div className="flex flex-col leading-tight">
          <span className={`tabular font-medium ${available <= 0 ? "text-destructive" : ""}`}>{qty(available)}</span>
          {onHand !== available && <span className="text-xs text-muted-foreground">{qty(onHand)} on hand</span>}
        </div>
      );
    },
  },
  { key: "cost", header: "Cost", align: "right", cell: (h) => <span className="tabular">₹{h.cost_price}</span> },
  { key: "selling", header: "Selling", align: "right", cell: (h) => <span className="tabular">₹{h.selling_price}</span> },
  {
    key: "margin",
    header: "Margin",
    align: "right",
    cell: (h) => {
      const m = marginPct(h.selling_price, h.cost_price);
      return <span className={`tabular ${m !== null && m < 0 ? "font-medium text-destructive" : ""}`}>{m ?? "—"}%</span>;
    },
  },
];

// A price bound that applies as you type, after a pause — like the search box.
function PriceBound({ id, label, placeholder, value, onChange }: { id: string; label: string; placeholder: string; value: string; onChange: (v: string) => void }) {
  const [text, setText] = useState(value);
  useEffect(() => {
    if (text === value) return;
    const t = setTimeout(() => onChange(text), 400);
    return () => clearTimeout(t);
  }, [text, value, onChange]);
  return (
    <Input id={id} aria-label={label} placeholder={placeholder} type="number" step="0.01" min="0" className="h-9 w-24" value={text} onChange={(e) => setText(e.target.value)} />
  );
}

function useCatalogFilters() {
  const [categories, setCategories] = useState<SearchOption[]>([]);
  const [brands, setBrands] = useState<SearchOption[]>([]);
  useEffect(() => {
    api.get<{ categories: Category[] }>("/api/v1/categories?status=active&sort=path").then((d) => setCategories(d.categories.map(categoryOption))).catch(() => {});
    api.get<{ brands: CatalogBrand[] }>("/api/v1/brands?status=active").then((d) => setBrands(d.brands.map((b) => ({ value: b.brand_id, label: b.name })))).catch(() => {});
  }, []);
  return { categories, brands };
}

function SearchView() {
  const { state, update, apiQuery, activeFilterCount } = useListState({
    sort: "relevance",
    filterKeys: ["category_id", "brand_id", "min_price", "max_price"],
  });
  const list = useServerList<SearchHit>("/api/v1/products/search", "results", apiQuery);
  const { categories, brands } = useCatalogFilters();

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Product Search"
        description="Typo-tolerant search across name, SKU and HSN, filtered by category, brand and price. Available is live stock that can still be sold."
      />
      <DataTable<SearchHit>
        columns={columns}
        rows={list.rows}
        total={list.total}
        loading={list.loading}
        error={list.error}
        onRetry={list.reload}
        state={state}
        update={(p) => {
          list.setLoading(true);
          update(p);
        }}
        rowKey={(h) => h.variant_id}
        searchPlaceholder="Name, SKU, or HSN code"
        searchLabel="Search"
        searchId="q"
        activeFilterCount={activeFilterCount}
        onClearFilters={() => update({ q: "", filters: { category_id: "", brand_id: "", min_price: "", max_price: "" } })}
        filters={
          <>
            <div className="w-52">
              <SearchSelect
                ariaLabel="Filter by category"
                value={state.filters.category_id}
                onChange={(v) => update({ filters: { category_id: v } })}
                options={categories}
                noneLabel="All categories"
                placeholder="All categories"
              />
            </div>
            <div className="w-44">
              <SearchSelect
                ariaLabel="Filter by brand"
                value={state.filters.brand_id}
                onChange={(v) => update({ filters: { brand_id: v } })}
                options={brands}
                noneLabel="All brands"
                placeholder="All brands"
              />
            </div>
            <PriceBound id="minPrice" label="Min price (₹)" placeholder="Min ₹" value={state.filters.min_price} onChange={(v) => update({ filters: { min_price: v } })} />
            <PriceBound id="maxPrice" label="Max price (₹)" placeholder="Max ₹" value={state.filters.max_price} onChange={(v) => update({ filters: { max_price: v } })} />
          </>
        }
        empty={{ title: "No products to search yet", body: "Products appear here as soon as they're added to the catalogue." }}
        rowActions={(h) => (
          <Link
            href={`/pricing?q=${encodeURIComponent(h.sku)}`}
            className={buttonVariants({ variant: "outline", size: "sm" })}
            aria-label={`Edit price of ${h.sku}`}
          >
            Edit price
          </Link>
        )}
      />
    </div>
  );
}

export default function SearchPage() {
  return (
    <Suspense>
      <SearchView />
    </Suspense>
  );
}
