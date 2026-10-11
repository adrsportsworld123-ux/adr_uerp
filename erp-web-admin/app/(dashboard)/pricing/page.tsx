"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { CatalogBrand, Category, PricingSettings, Product, ProductVariant } from "@/lib/types";
import { SearchOption, SearchSelect, categoryOption } from "@/components/data/search-select";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

function marginClass(pct: number | null) {
  if (pct === null) return "";
  return pct < 0 ? "text-destructive font-medium" : "text-foreground";
}

const PAGE_SIZE = 50;

// Category and brand options for the filters on this page, loaded once.
function useCatalogFilters() {
  const [categories, setCategories] = useState<SearchOption[]>([]);
  const [brands, setBrands] = useState<SearchOption[]>([]);
  useEffect(() => {
    api.get<{ categories: Category[] }>("/api/v1/categories?status=active&sort=path").then((d) => setCategories(d.categories.map(categoryOption))).catch(() => {});
    api.get<{ brands: CatalogBrand[] }>("/api/v1/brands?status=active").then((d) => setBrands(d.brands.map((b) => ({ value: b.brand_id, label: b.name })))).catch(() => {});
  }, []);
  return { categories, brands };
}

export default function PricingPage() {
  const [products, setProducts] = useState<Product[]>([]);
  const [total, setTotal] = useState(0);
  const [error, setError] = useState<string | null>(null);
  // Server-side search and paging: the list used to load the first 200
  // products only, so on a larger catalogue the rest could never be
  // priced from here.
  const [query, setQuery] = useState("");
  const [categoryId, setCategoryId] = useState("");
  const [brandId, setBrandId] = useState("");
  const [page, setPage] = useState(1);
  const [reloadKey, setReloadKey] = useState(0);
  const filters = useCatalogFilters();

  useEffect(() => {
    let cancelled = false;
    const t = setTimeout(() => {
      const p = new URLSearchParams({ limit: String(PAGE_SIZE), page: String(page), sort: "name" });
      if (query.trim()) p.set("q", query.trim());
      if (categoryId) p.set("category_id", categoryId);
      if (brandId) p.set("brand_id", brandId);
      api
        .get<{ products: Product[]; total: number }>(`/api/v1/products?${p.toString()}`)
        .then((d) => {
          if (cancelled) return;
          setProducts(d.products);
          setTotal(d.total);
          setError(null);
        })
        .catch((e) => !cancelled && setError(e.message));
    }, 250);
    return () => {
      cancelled = true;
      clearTimeout(t);
    };
  }, [query, categoryId, brandId, page, reloadKey]);
  const load = () => setReloadKey((k) => k + 1);
  const pages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  return (
    <div className="flex flex-col gap-6 max-w-4xl">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Pricing</h1>
        <Link href="/products/new">
          <Button variant="outline">New product</Button>
        </Link>
      </div>
      {error && <p className="text-sm text-destructive">{error}</p>}

      <TaxPricingSettings />

      <Card>
        <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3">
          <CardTitle className="text-base">
            Products <span className="font-normal text-muted-foreground">({total})</span>
          </CardTitle>
          <div className="flex flex-wrap items-center gap-2">
            <input
              type="search"
              value={query}
              onChange={(e) => {
                setQuery(e.target.value);
                setPage(1);
              }}
              placeholder="Search name, SKU or barcode"
              aria-label="Search products"
              className="h-9 w-60 rounded-lg border bg-card px-3 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/30"
            />
            <div className="w-52">
              <SearchSelect
                ariaLabel="Filter by category"
                value={categoryId}
                onChange={(v) => {
                  setCategoryId(v);
                  setPage(1);
                }}
                options={filters.categories}
                noneLabel="All categories"
                placeholder="All categories"
              />
            </div>
            <div className="w-44">
              <SearchSelect
                ariaLabel="Filter by brand"
                value={brandId}
                onChange={(v) => {
                  setBrandId(v);
                  setPage(1);
                }}
                options={filters.brands}
                noneLabel="All brands"
                placeholder="All brands"
              />
            </div>
          </div>
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Product</TableHead>
                <TableHead>SKU</TableHead>
                <TableHead>Cost</TableHead>
                <TableHead>MRP</TableHead>
                <TableHead>Selling</TableHead>
                <TableHead>GST basis</TableHead>
                <TableHead title="Computed on the pre-GST (net) selling price">Margin</TableHead>
                <TableHead>Markup</TableHead>
                <TableHead>Barcode</TableHead>
                <TableHead></TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {products.flatMap((p) =>
                p.variants.map((v) => (
                  <VariantRow key={v.variant_id} product={p} variant={v} onSaved={load} />
                ))
              )}
              {products.length === 0 && (
                <TableRow>
                  <TableCell colSpan={10} className="py-6 text-center text-sm text-muted-foreground">
                    {query || categoryId || brandId ? "No product matches these filters" : "No products yet"}
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
          {total > PAGE_SIZE && (
            <div className="mt-3 flex items-center justify-end gap-2 text-sm">
              <span className="text-muted-foreground">
                Page {page} of {pages}
              </span>
              <Button size="sm" variant="outline" disabled={page <= 1} onClick={() => setPage(page - 1)}>
                Previous
              </Button>
              <Button size="sm" variant="outline" disabled={page >= pages} onClick={() => setPage(page + 1)}>
                Next
              </Button>
            </div>
          )}
        </CardContent>
      </Card>

      <Calculator />
      <BulkUpdate onApplied={load} categories={filters.categories} brands={filters.brands} />
    </div>
  );
}

function VariantRow({ product, variant, onSaved }: { product: Product; variant: ProductVariant; onSaved: () => void }) {
  const [editing, setEditing] = useState(false);
  const [sellingPrice, setSellingPrice] = useState(variant.selling_price);
  const [reason, setReason] = useState("");
  const [override, setOverride] = useState(false);
  const [needsOverride, setNeedsOverride] = useState(false);
  const [busy, setBusy] = useState(false);

  async function save() {
    setBusy(true);
    try {
      await api.patch(`/api/v1/pricing/variants/${variant.variant_id}`, {
        selling_price: Number(sellingPrice),
        reason,
        override,
      });
      toast.success("Price updated");
      setEditing(false);
      setNeedsOverride(false);
      onSaved();
    } catch (e) {
      if (e instanceof ApiError && e.code === "NEGATIVE_MARGIN") {
        setNeedsOverride(true);
      } else {
        toast.error(e instanceof ApiError ? e.message : "Could not update price");
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <TableRow>
      <TableCell>{product.name}</TableCell>
      <TableCell className="font-mono text-xs">{variant.sku}</TableCell>
      <TableCell>₹{variant.cost_price}</TableCell>
      <TableCell>₹{variant.mrp}</TableCell>
      <TableCell>
        {editing ? (
          <Input className="w-24" type="number" step="0.01" value={sellingPrice} onChange={(e) => setSellingPrice(e.target.value)} />
        ) : (
          <div className="flex flex-col">
            <span>₹{variant.selling_price}</span>
            {/* Server-computed (internal/taxcalc) — never derived in JS, see lib/api-client.ts's money note */}
            <span className="text-xs text-muted-foreground">
              {product.price_includes_tax ? `net ₹${variant.net_selling_price}` : `customer pays ₹${variant.gross_selling_price}`}
            </span>
          </div>
        )}
      </TableCell>
      <TableCell>
        <TaxBasisCell product={product} onChanged={onSaved} />
      </TableCell>
      <TableCell className={marginClass(variant.margin_pct)}>{variant.margin_pct ?? "—"}%</TableCell>
      <TableCell className={marginClass(variant.markup_pct)}>{variant.markup_pct ?? "—"}%</TableCell>
      <TableCell>
        <BarcodeCell variant={variant} onAssigned={onSaved} />
      </TableCell>
      <TableCell>
        {editing ? (
          <div className="flex flex-col gap-2 w-56">
            <Input placeholder="Reason" value={reason} onChange={(e) => setReason(e.target.value)} className="h-7 text-xs" />
            {needsOverride && (
              <label className="flex items-center gap-1 text-xs text-destructive">
                <input type="checkbox" checked={override} onChange={(e) => setOverride(e.target.checked)} />
                This sells below cost — override anyway
              </label>
            )}
            <div className="flex gap-1">
              <Button size="sm" disabled={busy} onClick={save}>
                Save
              </Button>
              <Button size="sm" variant="ghost" onClick={() => setEditing(false)}>
                Cancel
              </Button>
            </div>
          </div>
        ) : (
          <Button size="sm" variant="outline" onClick={() => setEditing(true)}>
            Edit
          </Button>
        )}
      </TableCell>
    </TableRow>
  );
}

// Merchant-wide default for NEW products only (GET/PUT /pricing/settings,
// PUT gated by pricing.manage) — existing products keep their own flag.
// Saves on toggle; a 403 surfaces as a toast like every other gated write.
function TaxPricingSettings() {
  const [settings, setSettings] = useState<PricingSettings | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api
      .get<PricingSettings>("/api/v1/pricing/settings")
      .then(setSettings)
      .catch(() => {});
  }, []);

  async function toggle(next: boolean) {
    setBusy(true);
    try {
      setSettings(await api.put<PricingSettings>("/api/v1/pricing/settings", { prices_include_tax_default: next }));
      toast.success(next ? "New products will default to GST-inclusive prices" : "New products will default to GST-exclusive prices");
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not save pricing settings");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">GST pricing default</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-1">
        <label className="flex items-center gap-2 text-sm">
          <input
            id="pricesIncludeTaxDefault"
            type="checkbox"
            disabled={!settings || busy}
            checked={settings?.prices_include_tax_default ?? false}
            onChange={(e) => toggle(e.target.checked)}
          />
          New products&apos; selling prices include GST
        </label>
        <p className="text-xs text-muted-foreground">
          Inclusive (MRP-style): the customer pays exactly the selling price and GST is extracted from it. Exclusive: GST is added on
          top at billing. Each product can override this below; changing it never affects existing products.
        </p>
      </CardContent>
    </Card>
  );
}

// Per-product inclusive/exclusive switch (PATCH /products/{id}, gated by
// catalog.manage). Switching reinterprets the stored selling price rather
// than converting it (₹1180 stays ₹1180 — what the customer pays changes),
// so it asks first. Sales already made keep their own snapshot.
function TaxBasisCell({ product, onChanged }: { product: Product; onChanged: () => void }) {
  const [busy, setBusy] = useState(false);

  async function flip() {
    const next = !product.price_includes_tax;
    const ok = window.confirm(
      `Switch "${product.name}" to GST-${next ? "inclusive" : "exclusive"} pricing?\n\n` +
        `The stored selling price stays the same, so the customer will pay ` +
        `${next ? "exactly that price (GST extracted from it)" : "that price PLUS GST"}. Adjust the price afterwards if needed.`
    );
    if (!ok) return;
    setBusy(true);
    try {
      await api.patch(`/api/v1/products/${product.product_id}`, { price_includes_tax: next });
      toast.success(`${product.name}: prices now GST-${next ? "inclusive" : "exclusive"}`);
      onChanged();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not change GST basis");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex flex-col items-start gap-1 text-xs">
      <span className={`rounded px-1.5 py-0.5 ${product.price_includes_tax ? "bg-pay-soft text-pay" : "bg-muted text-foreground"}`}>
        {product.price_includes_tax ? "Incl." : "Excl."} GST {product.tax_rate_pct}%
      </span>
      <Button size="sm" variant="ghost" className="h-6 px-1 text-xs" disabled={busy} onClick={flip}>
        Switch to {product.price_includes_tax ? "excl." : "incl."}
      </Button>
    </div>
  );
}

// Manual-enter-or-generate barcode assignment, keeping the product's
// original barcode and this system's own generated one as two separate,
// independently-managed slots — neither call replaces the other, see
// internal/catalog/barcode_assign.go's assignBarcodeInTx/source column.
// POST /products/variants/{id}/barcodes already supported both paths;
// this is the UI for it on the one real product-list screen this admin has
// (New Product also offers this at creation time now).
function BarcodeCell({ variant, onAssigned }: { variant: ProductVariant; onAssigned: () => void }) {
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);

  async function assign(manualCode: string | null) {
    setBusy(true);
    try {
      await api.post(`/api/v1/products/variants/${variant.variant_id}/barcodes`, manualCode ? { code: manualCode } : {});
      toast.success("Barcode assigned");
      setCode("");
      onAssigned();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not assign barcode");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex flex-col gap-1.5 text-xs">
      <div className="flex items-center gap-1">
        <span className="text-muted-foreground w-14 shrink-0">Original</span>
        {variant.original_barcode ? (
          <span className="font-mono">{variant.original_barcode}</span>
        ) : (
          <>
            <Input
              placeholder="Enter code"
              className="h-7 w-24 text-xs"
              value={code}
              onChange={(e) => setCode(e.target.value)}
            />
            <Button size="sm" variant="outline" disabled={busy || !code} onClick={() => assign(code)}>
              Assign
            </Button>
          </>
        )}
      </div>
      <div className="flex items-center gap-1">
        <span className="text-muted-foreground w-14 shrink-0">System</span>
        {variant.generated_barcode ? (
          <span className="font-mono">{variant.generated_barcode}</span>
        ) : (
          <Button size="sm" variant="ghost" disabled={busy} onClick={() => assign(null)}>
            Generate
          </Button>
        )}
      </div>
    </div>
  );
}

interface CalculateResult {
  selling_price: number;
  net_selling_price: number;
  margin_pct: number | null;
  markup_pct: number | null;
}

// Margin/markup always apply to the pre-GST price (cost is pre-GST too);
// with "Price includes GST" the result is grossed up so it can be saved
// directly as an inclusive selling price.
function Calculator() {
  const [costPrice, setCostPrice] = useState("");
  const [mode, setMode] = useState<"margin_pct" | "markup_pct">("margin_pct");
  const [pct, setPct] = useState("");
  const [taxRate, setTaxRate] = useState("");
  const [inclusive, setInclusive] = useState(false);
  const [result, setResult] = useState<CalculateResult | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function calculate(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    try {
      const res = await api.post<CalculateResult>("/api/v1/pricing/calculate", {
        cost_price: Number(costPrice),
        [mode]: Number(pct),
        tax_rate_pct: Number(taxRate) || 0,
        price_includes_tax: inclusive,
      });
      setResult(res);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not reach the server");
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Pricing calculator</CardTitle>
      </CardHeader>
      <CardContent>
        <form onSubmit={calculate} className="flex gap-2 items-end flex-wrap">
          <div className="flex flex-col gap-2">
            <Label htmlFor="cost">Cost price (₹)</Label>
            <Input id="cost" type="number" min="0.01" step="0.01" required className="w-32" value={costPrice} onChange={(e) => setCostPrice(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2">
            <Label>Target</Label>
            <Select
              value={mode}
              onValueChange={(v) => setMode((v as "margin_pct" | "markup_pct") ?? "margin_pct")}
              items={{ margin_pct: "Margin %", markup_pct: "Markup %" }}
            >
              <SelectTrigger className="w-36">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="margin_pct">Margin %</SelectItem>
                <SelectItem value="markup_pct">Markup %</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="pct">%</Label>
            <Input id="pct" type="number" step="0.01" required className="w-24" value={pct} onChange={(e) => setPct(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="calcTaxRate">GST %</Label>
            <Input
              id="calcTaxRate"
              type="number"
              min="0"
              step="0.01"
              className="w-20"
              placeholder="0"
              value={taxRate}
              onChange={(e) => setTaxRate(e.target.value)}
            />
          </div>
          <label className="flex items-center gap-2 text-sm pb-2">
            <input id="calcInclusive" type="checkbox" checked={inclusive} onChange={(e) => setInclusive(e.target.checked)} />
            Price includes GST
          </label>
          <Button type="submit">Calculate</Button>
        </form>
        {error && <p className="text-sm text-destructive mt-2">{error}</p>}
        {result && (
          <p className="text-sm mt-3">
            Selling price: <span className="font-semibold">₹{result.selling_price}</span>
            {result.selling_price !== result.net_selling_price && <> (incl. GST; net ₹{result.net_selling_price})</>} — margin{" "}
            {result.margin_pct}%, markup {result.markup_pct}% on the pre-GST price
          </p>
        )}
      </CardContent>
    </Card>
  );
}

interface BulkItem {
  variant_id: string;
  sku: string;
  product_name: string;
  old_price: number;
  new_price: number;
  status: "would_update" | "updated" | "scheduled" | "skipped_negative_margin";
}

function BulkUpdate({ onApplied, categories, brands }: { onApplied: () => void; categories: SearchOption[]; brands: SearchOption[] }) {
  const [categoryId, setCategoryId] = useState("");
  const [brandId, setBrandId] = useState("");
  const [method, setMethod] = useState<"percent" | "fixed" | "set">("percent");
  const [value, setValue] = useState("");
  const [roundTo, setRoundTo] = useState("");
  const [reason, setReason] = useState("");
  const [override, setOverride] = useState(false);
  // Phase 10: optional — set it and Apply schedules the computed prices
  // (Scheduled Price Changes page) instead of changing them now.
  const [effectiveAt, setEffectiveAt] = useState("");
  const [items, setItems] = useState<BulkItem[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  function buildBody() {
    return {
      filter: { category_id: categoryId, brand_id: brandId },
      method,
      value: Number(value) || 0,
      round_to: Number(roundTo) || 0,
      reason,
      override,
      ...(effectiveAt ? { effective_at: new Date(effectiveAt).toISOString() } : {}),
    };
  }

  async function preview(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    try {
      const res = await api.post<{ items: BulkItem[] }>("/api/v1/pricing/bulk-update/preview", buildBody());
      setItems(res.items);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not reach the server");
    }
  }

  async function apply() {
    setBusy(true);
    try {
      const res = await api.post<{ items: BulkItem[] }>("/api/v1/pricing/bulk-update/apply", buildBody());
      setItems(res.items);
      toast.success(effectiveAt ? "Bulk price update scheduled" : "Bulk price update applied");
      onApplied();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not apply bulk update");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Bulk price update</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <p className="text-xs text-muted-foreground">
          Applies to every product in the chosen category and brand (leave both empty for the whole catalogue). Preview before applying.
        </p>
        <form onSubmit={preview} className="flex gap-2 items-end flex-wrap">
          <div className="flex w-56 flex-col gap-2">
            <Label htmlFor="bulkCategory">Category</Label>
            <SearchSelect
              id="bulkCategory"
              ariaLabel="Bulk update category"
              value={categoryId}
              onChange={(v) => {
                setCategoryId(v);
                setItems(null);
              }}
              options={categories}
              noneLabel="All categories"
              placeholder="All categories"
            />
          </div>
          <div className="flex w-44 flex-col gap-2">
            <Label htmlFor="bulkBrand">Brand</Label>
            <SearchSelect
              id="bulkBrand"
              ariaLabel="Bulk update brand"
              value={brandId}
              onChange={(v) => {
                setBrandId(v);
                setItems(null);
              }}
              options={brands}
              noneLabel="All brands"
              placeholder="All brands"
            />
          </div>
          <div className="flex flex-col gap-2">
            <Label>Method</Label>
            <Select
              value={method}
              onValueChange={(v) => setMethod((v as "percent" | "fixed" | "set") ?? "percent")}
              items={{ percent: "% change", fixed: "Fixed amount", set: "Set price" }}
            >
              <SelectTrigger className="w-32">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="percent">% change</SelectItem>
                <SelectItem value="fixed">Fixed amount</SelectItem>
                <SelectItem value="set">Set price</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="value">Value</Label>
            <Input id="value" type="number" step="0.01" required className="w-28" value={value} onChange={(e) => setValue(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="roundTo">Round to (₹)</Label>
            <Input id="roundTo" type="number" min="0" step="1" className="w-24" value={roundTo} onChange={(e) => setRoundTo(e.target.value)} placeholder="0" />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="reason">Reason</Label>
            <Input id="reason" className="w-40" value={reason} onChange={(e) => setReason(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="bulkEffectiveAt">Effective (optional)</Label>
            <Input id="bulkEffectiveAt" type="datetime-local" className="w-52" value={effectiveAt} onChange={(e) => setEffectiveAt(e.target.value)} />
          </div>
          <Button type="submit" variant="outline">
            Preview
          </Button>
        </form>

        {error && <p className="text-sm text-destructive">{error}</p>}

        {items && (
          <>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Product</TableHead>
                  <TableHead>Old</TableHead>
                  <TableHead>New</TableHead>
                  <TableHead>Status</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((it) => (
                  <TableRow key={it.variant_id}>
                    <TableCell>
                      {it.product_name} <span className="text-muted-foreground">({it.sku})</span>
                    </TableCell>
                    <TableCell>₹{it.old_price}</TableCell>
                    <TableCell>₹{it.new_price}</TableCell>
                    <TableCell className={it.status === "skipped_negative_margin" ? "text-destructive" : ""}>{it.status}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            {items.some((it) => it.status === "skipped_negative_margin") && (
              <label className="flex items-center gap-2 text-sm text-destructive">
                <input type="checkbox" checked={override} onChange={(e) => setOverride(e.target.checked)} />
                Some items sell below cost — check to override and apply anyway
              </label>
            )}
            <Button disabled={busy || items.every((it) => it.status !== "would_update" && !override)} onClick={apply}>
              {busy ? "Applying..." : effectiveAt ? "Schedule" : "Apply"}
            </Button>
          </>
        )}
      </CardContent>
    </Card>
  );
}
