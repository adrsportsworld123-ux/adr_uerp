"use client";

// The product master list — the entry point to editing a product
// (products/[id]/edit). The status filter can find inactive and
// discontinued products, which disappear from every picker, so they can
// be reactivated. Phase 11: server-side search (name, SKU or barcode),
// category/brand/status filters, sort and paging, all kept in the URL.
import { Suspense, useEffect, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { Plus } from "lucide-react";
import { api } from "@/lib/api-client";
import { Product } from "@/lib/types";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import { Button, buttonVariants } from "@/components/ui/button";
import { Column, DataTable } from "@/components/data/data-table";
import { PageHeader } from "@/components/data/page-header";
import { StatusChip } from "@/components/data/status-chip";
import { useListState } from "@/components/data/use-list-state";
import { useServerList } from "@/components/data/use-server-list";

type Option = { id: string; name: string };

function rupees(n: number) {
  return `₹${n.toLocaleString("en-IN", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}

function priceRange(p: Product) {
  const prices = p.variants.map((v) => Number(v.selling_price)).filter((n) => Number.isFinite(n));
  if (prices.length === 0) return "—";
  const lo = Math.min(...prices);
  const hi = Math.max(...prices);
  return lo === hi ? rupees(lo) : `${rupees(lo)} – ${rupees(hi)}`;
}

const columns: Column<Product>[] = [
  {
    key: "name",
    header: "Product",
    sort: "name",
    cell: (p) => (
      <div className="flex min-w-48 flex-col leading-tight">
        <span className="font-medium">{p.name}</span>
        <span className="font-mono text-xs text-muted-foreground">
          {p.variants.slice(0, 2).map((v) => v.sku).join(", ")}
          {p.variants.length > 2 ? ` +${p.variants.length - 2} more` : ""}
        </span>
      </div>
    ),
  },
  { key: "category", header: "Category", cell: (p) => p.category_name ?? <span className="text-muted-foreground">—</span> },
  { key: "brand", header: "Brand", cell: (p) => p.brand_name ?? <span className="text-muted-foreground">—</span> },
  {
    key: "variants",
    header: "Variants",
    align: "right",
    cell: (p) => {
      const off = p.variants.filter((v) => v.status !== "active").length;
      return (
        <span className="tabular">
          {p.variants.length}
          {off > 0 && <span className="ml-1 text-xs text-warn">({off} off sale)</span>}
        </span>
      );
    },
  },
  { key: "price", header: "Selling price", align: "right", cell: (p) => <span className="tabular whitespace-nowrap">{priceRange(p)}</span> },
  { key: "status", header: "Status", sort: "status", cell: (p) => <StatusChip status={p.status} /> },
];

function useOptions(path: string, key: string, idKey: string) {
  const [options, setOptions] = useState<Option[]>([]);
  useEffect(() => {
    api
      .get<Record<string, Record<string, string>[]>>(`${path}?status=active`)
      .then((d) => setOptions((d[key] ?? []).map((r) => ({ id: r[idKey], name: r.path ? r.path.replace(/^\//, "").replace(/\//g, " › ") : r.name }))))
      .catch(() => setOptions([]));
  }, [path, key, idKey]);
  return options;
}

function ProductsList() {
  const router = useRouter();
  const { can } = usePermissions();
  const canCreate = can("catalog.manage");
  const { state, update, apiQuery, activeFilterCount } = useListState({
    sort: "name",
    status: "all",
    filterKeys: ["category_id", "brand_id"],
  });
  const list = useServerList<Product>("/api/v1/products", "products", apiQuery);
  const categories = useOptions("/api/v1/categories", "categories", "category_id");
  const brands = useOptions("/api/v1/brands", "brands", "brand_id");

  const selectClass = "h-9 max-w-52 rounded-lg border bg-card px-2 text-sm";

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Products"
        description={list.rows ? `${list.total.toLocaleString("en-IN")} products match the current filters` : "Your catalog: everything the POS can sell."}
        actions={
          canCreate ? (
            <Link href="/products/new" className={buttonVariants()}>
              <Plus aria-hidden />
              New product
            </Link>
          ) : (
            <span className={buttonVariants({ className: "pointer-events-none opacity-50" })} title={missingPermissionHint("catalog.manage")} aria-disabled>
              <Plus aria-hidden />
              New product
            </span>
          )
        }
      />
      <DataTable<Product>
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
        rowKey={(p) => p.product_id}
        searchPlaceholder="Search name, SKU or barcode"
        searchLabel="Search products"
        activeFilterCount={activeFilterCount}
        onClearFilters={() => update({ q: "", status: "all", filters: { category_id: "", brand_id: "" } })}
        filters={
          <>
            <select aria-label="Status" value={state.status || "all"} onChange={(e) => update({ status: e.target.value })} className={selectClass}>
              <option value="all">All statuses</option>
              <option value="active">Active</option>
              <option value="inactive">Inactive</option>
              <option value="discontinued">Discontinued</option>
            </select>
            <select
              aria-label="Category"
              value={state.filters.category_id}
              onChange={(e) => update({ filters: { category_id: e.target.value } })}
              className={selectClass}
            >
              <option value="">All categories</option>
              {categories.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </select>
            <select
              aria-label="Brand"
              value={state.filters.brand_id}
              onChange={(e) => update({ filters: { brand_id: e.target.value } })}
              className={selectClass}
            >
              <option value="">All brands</option>
              {brands.map((b) => (
                <option key={b.id} value={b.id}>
                  {b.name}
                </option>
              ))}
            </select>
          </>
        }
        empty={{
          title: "No products yet",
          body: "Add your first product, or import your catalog, so the POS has something to sell.",
          action: canCreate ? (
            <Link href="/products/new" className={buttonVariants({ size: "sm" })}>
              <Plus aria-hidden />
              New product
            </Link>
          ) : undefined,
        }}
        onRowClick={(p) => router.push(`/products/${p.product_id}/edit`)}
        rowActions={(p) => (
          <Button variant="outline" size="sm" aria-label={`Edit ${p.name}`} onClick={() => router.push(`/products/${p.product_id}/edit`)}>
            Edit
          </Button>
        )}
      />
    </div>
  );
}

export default function ProductsPage() {
  return (
    <Suspense>
      <ProductsList />
    </Suspense>
  );
}
