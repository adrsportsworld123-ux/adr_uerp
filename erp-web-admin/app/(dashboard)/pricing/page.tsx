"use client";

// Pricing on the shared list kit (Phase 11 DataTable, URL-kept filters,
// paging and states like every other list), with live stock per variant
// and the price edit in a dialog — the same row pattern as the masters.
import { Suspense, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { Pencil, Plus } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { CatalogBrand, Category, PricingSettings, Product, ProductVariant } from "@/lib/types";
import { SearchOption, SearchSelect, categoryOption } from "@/components/data/search-select";
import { Button, buttonVariants } from "@/components/ui/button";
import { Modal } from "@/components/ui/dialog";
import { Column, DataTable } from "@/components/data/data-table";
import { PageHeader } from "@/components/data/page-header";
import { useListState } from "@/components/data/use-list-state";
import { useServerList } from "@/components/data/use-server-list";
import { qty, rupees } from "@/lib/format";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

import { currencySymbol } from "@/lib/currency";
import type { PriceSuggestion } from "@/lib/types";
import { SuggestionChip, SuggestionHint, fetchSuggestions, useLiveSuggestion, useSuggestionSettings } from "@/components/pricing/suggestions";

function marginClass(pct: number | null) {
  if (pct === null) return "";
  return pct < 0 ? "text-destructive font-medium" : "text-foreground";
}

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

// One row per variant (prices live on variants); the server pages products.
type PriceRow = { product: Product; variant: ProductVariant };

// Live stock beside the price (GET /products returns it per variant over
// the branches you may see): what's left to sell, and what's on the shelf
// when open carts hold some of it.
function StockCell({ v }: { v: ProductVariant }) {
  const available = Number(v.available ?? 0);
  const onHand = Number(v.on_hand ?? 0);
  return (
    <div className="flex flex-col leading-tight">
      <span className={`tabular font-medium ${available <= 0 ? "text-destructive" : ""}`}>{qty(available)}</span>
      {onHand !== available && <span className="text-xs text-muted-foreground">{qty(onHand)} on hand</span>}
    </div>
  );
}

function PricingList() {
  const { state, update, apiQuery, activeFilterCount } = useListState({ sort: "name", limit: 50, filterKeys: ["category_id", "brand_id"] });
  const list = useServerList<Product>("/api/v1/products", "products", apiQuery);
  const filters = useCatalogFilters();
  const [editing, setEditing] = useState<PriceRow | null>(null);
  const rows = useMemo(() => list.rows?.flatMap((p) => p.variants.map((v) => ({ product: p, variant: v }))) ?? null, [list.rows]);
  // Price suggestions (§3.83), when the plan has them and they're on.
  const { enabled: suggestionsOn } = useSuggestionSettings();
  const [suggested, setSuggested] = useState<Record<string, PriceSuggestion>>({});
  useEffect(() => {
    if (!suggestionsOn || !rows || rows.length === 0) return;
    let live = true;
    fetchSuggestions(rows.slice(0, 200).map((r) => ({ key: r.variant.variant_id, variant_id: r.variant.variant_id })))
      .then((items) => live && setSuggested(Object.fromEntries(items.map((i) => [i.key, i]))))
      .catch(() => {});
    return () => {
      live = false;
    };
  }, [rows, suggestionsOn]);

  const columns: Column<PriceRow>[] = [
    {
      key: "name",
      header: "Product",
      sort: "name",
      cell: ({ product }) => (
        <div className="flex flex-col leading-tight">
          <span className="font-medium">{product.name}</span>
          {(product.category_name || product.brand_name) && (
            <span className="text-xs text-muted-foreground">{[product.category_name, product.brand_name].filter(Boolean).join(" · ")}</span>
          )}
        </div>
      ),
    },
    { key: "sku", header: "SKU", cell: ({ variant }) => <span className="font-mono text-xs">{variant.sku}</span> },
    { key: "available", header: "Available", align: "right", cell: ({ variant }) => <StockCell v={variant} /> },
    { key: "cost", header: "Cost", align: "right", cell: ({ variant }) => <span className="tabular">{rupees(variant.cost_price)}</span> },
    { key: "mrp", header: "MRP", align: "right", cell: ({ variant }) => <span className="tabular">{rupees(variant.mrp)}</span> },
    {
      key: "selling",
      header: "Selling",
      align: "right",
      cell: ({ product, variant }) => (
        <div className="flex flex-col leading-tight">
          <span className="tabular">{rupees(variant.selling_price)}</span>
          {/* Server-computed (internal/taxcalc) — never derived in JS, see lib/api-client.ts's money note */}
          <span className="text-xs text-muted-foreground">
            {product.price_includes_tax ? `net ${rupees(variant.net_selling_price)}` : `customer pays ${rupees(variant.gross_selling_price)}`}
          </span>
        </div>
      ),
    },
    ...(suggestionsOn
      ? [
          {
            key: "suggested",
            header: "Suggested",
            align: "right" as const,
            cell: ({ variant }: PriceRow) => {
              const s = suggested[variant.variant_id];
              if (!s) return <span className="text-xs text-muted-foreground">—</span>;
              return (
                <div className="flex flex-col items-end gap-0.5 leading-tight" title={s.note || s.rule || undefined}>
                  {s.suggested_price != null && <span className="tabular">{rupees(s.suggested_price)}</span>}
                  <SuggestionChip s={s} />
                </div>
              );
            },
          },
        ]
      : []),
    { key: "gst", header: "GST basis", cell: ({ product }) => <TaxBasisCell product={product} onChanged={list.reload} /> },
    {
      key: "margin",
      header: "Margin",
      align: "right",
      cell: ({ variant }) => <span className={`tabular ${marginClass(variant.margin_pct)}`}>{variant.margin_pct ?? "—"}%</span>,
    },
    {
      key: "markup",
      header: "Markup",
      align: "right",
      cell: ({ variant }) => <span className={`tabular ${marginClass(variant.markup_pct)}`}>{variant.markup_pct ?? "—"}%</span>,
    },
    { key: "barcode", header: "Barcode", cell: ({ variant }) => <BarcodeCell variant={variant} onAssigned={list.reload} /> },
  ];

  return (
    <>
      <DataTable<PriceRow>
        columns={columns}
        rows={rows}
        total={list.total}
        loading={list.loading}
        error={list.error}
        onRetry={list.reload}
        state={state}
        update={(p) => {
          list.setLoading(true);
          update(p);
        }}
        rowKey={({ variant }) => variant.variant_id}
        searchPlaceholder="Search name, SKU or barcode"
        searchLabel="Search products"
        activeFilterCount={activeFilterCount}
        onClearFilters={() => update({ q: "", filters: { category_id: "", brand_id: "" } })}
        filters={
          <>
            <div className="w-52">
              <SearchSelect
                ariaLabel="Filter by category"
                value={state.filters.category_id}
                onChange={(v) => update({ filters: { category_id: v } })}
                options={filters.categories}
                noneLabel="All categories"
                placeholder="All categories"
              />
            </div>
            <div className="w-44">
              <SearchSelect
                ariaLabel="Filter by brand"
                value={state.filters.brand_id}
                onChange={(v) => update({ filters: { brand_id: v } })}
                options={filters.brands}
                noneLabel="All brands"
                placeholder="All brands"
              />
            </div>
          </>
        }
        empty={{
          title: "No products yet",
          body: "Add products to set their prices here.",
          action: (
            <Link href="/products/new" className={buttonVariants({ size: "sm" })}>
              <Plus aria-hidden />
              New product
            </Link>
          ),
        }}
        // No row-click editing: the GST basis and barcode cells are controls
        // of their own, and a click there must not open the price dialog.
        rowActions={(r) => (
          <Button variant="ghost" size="icon-sm" aria-label={`Edit price of ${r.variant.sku}`} title="Edit pricing" onClick={() => setEditing(r)}>
            <Pencil />
          </Button>
        )}
      />
      {editing && (
        <PriceDialog
          row={editing}
          suggestionsOn={suggestionsOn}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            list.reload();
          }}
        />
      )}
    </>
  );
}

export default function PricingPage() {
  const filters = useCatalogFilters();
  const [reloadKey, setReloadKey] = useState(0);
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Pricing"
        description="Selling prices, margins and stock for every product. Available is what can still be sold — on hand less what open carts hold."
        actions={
          <Link href="/products/new" className={buttonVariants()}>
            <Plus aria-hidden />
            New product
          </Link>
        }
      />
      <Suspense>
        <PricingList key={reloadKey} />
      </Suspense>
      <div className="grid gap-6 lg:grid-cols-2">
        <Calculator />
        <TaxPricingSettings />
      </div>
      <BulkUpdate onApplied={() => setReloadKey((k) => k + 1)} categories={filters.categories} brands={filters.brands} />
    </div>
  );
}

// Edit one variant's pricing: its original barcode (the code printed on
// the product), cost, MRP and selling price. With price suggestions on
// (§3.83) the selling price starts at the suggestion for the cost and MRP
// in the form, follows them as they change, and stays editable. A reason
// is recorded with the price history; a price below cost needs an
// explicit override (NEGATIVE_MARGIN), same rule the API enforces.
function PriceDialog({ row, suggestionsOn, onClose, onSaved }: { row: PriceRow; suggestionsOn: boolean; onClose: () => void; onSaved: () => void }) {
  const { product, variant } = row;
  const [barcode, setBarcode] = useState(variant.original_barcode ?? "");
  const [cost, setCost] = useState(variant.cost_price);
  const [mrp, setMrp] = useState(variant.mrp);
  // null = not typed: the field shows the suggestion (else the current price).
  const [typedPrice, setTypedPrice] = useState<string | null>(null);
  const [reason, setReason] = useState("");
  const [override, setOverride] = useState(false);
  const [needsOverride, setNeedsOverride] = useState(false);
  const [busy, setBusy] = useState(false);
  const suggestion = useLiveSuggestion({ key: variant.variant_id, variant_id: variant.variant_id, cost_price: Number(cost) || 0, mrp: Number(mrp) || 0 }, suggestionsOn);
  const sellingPrice = typedPrice ?? (suggestion?.suggested_price != null ? String(suggestion.suggested_price) : variant.selling_price);
  const prefilled = typedPrice === null && suggestion?.suggested_price != null && Number(sellingPrice) !== Number(variant.selling_price);

  async function save() {
    setBusy(true);
    try {
      const code = barcode.trim();
      if (code && code !== (variant.original_barcode ?? "")) {
        await api.put(`/api/v1/products/variants/${variant.variant_id}/original-barcode`, { code });
      }
      await api.patch(`/api/v1/pricing/variants/${variant.variant_id}`, {
        cost_price: Number(cost) || 0,
        mrp: Number(mrp),
        selling_price: Number(sellingPrice),
        reason,
        override,
      });
      toast.success("Pricing updated");
      onSaved();
    } catch (e) {
      if (e instanceof ApiError && e.code === "NEGATIVE_MARGIN") {
        setNeedsOverride(true);
      } else {
        toast.error(e instanceof ApiError ? e.message : "Could not update pricing");
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title="Edit pricing"
      description={`${product.name} · ${variant.sku} · now sells at ${rupees(variant.selling_price)}`}
      width={560}
      footer={
        <>
          <Button variant="outline" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={save} disabled={busy || !(Number(sellingPrice) > 0) || !(Number(mrp) > 0)}>
            Save
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <div className="flex flex-col gap-2">
          <Label htmlFor="originalBarcode">Original barcode</Label>
          <Input
            id="originalBarcode"
            value={barcode}
            onChange={(e) => setBarcode(e.target.value)}
            placeholder="The code printed on the product"
            className="w-64 font-mono"
          />
          {variant.generated_barcode && <p className="text-xs text-muted-foreground">Our own code {variant.generated_barcode} stays as it is.</p>}
        </div>
        <div className="grid grid-cols-3 gap-3">
          <div className="flex flex-col gap-2">
            <Label htmlFor="costPrice">Cost price ({currencySymbol()})</Label>
            <Input id="costPrice" type="number" min="0" step="0.01" value={cost} onChange={(e) => setCost(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="mrpPrice">MRP ({currencySymbol()})</Label>
            <Input id="mrpPrice" type="number" min="0.01" step="0.01" value={mrp} onChange={(e) => setMrp(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="sellingPrice">
              Selling price ({currencySymbol()}
              {product.price_includes_tax ? ", incl. GST" : ""})
            </Label>
            <Input id="sellingPrice" type="number" step="0.01" value={sellingPrice} onChange={(e) => setTypedPrice(e.target.value)} autoFocus />
          </div>
        </div>
        {suggestion && <SuggestionHint s={suggestion} current={sellingPrice} onUse={(p) => setTypedPrice(String(p))} />}
        {prefilled && (
          <p className="text-xs text-muted-foreground">
            Filled in from the suggestion.{" "}
            <button type="button" className="underline underline-offset-2" onClick={() => setTypedPrice(variant.selling_price)}>
              Keep {rupees(variant.selling_price)}
            </button>
          </p>
        )}
        <div className="flex flex-col gap-2">
          <Label htmlFor="priceReason">Reason</Label>
          <Input id="priceReason" placeholder="e.g. New MRP from supplier" value={reason} onChange={(e) => setReason(e.target.value)} />
        </div>
        {needsOverride && (
          <label className="flex items-center gap-2 text-sm text-destructive">
            <input type="checkbox" checked={override} onChange={(e) => setOverride(e.target.checked)} />
            This sells below cost — override anyway
          </label>
        )}
      </div>
    </Modal>
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
            <Label htmlFor="cost">Cost price ({currencySymbol()})</Label>
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
            Selling price: <span className="font-semibold">{rupees(result.selling_price)}</span>
            {result.selling_price !== result.net_selling_price && <> (incl. GST; net {rupees(result.net_selling_price)})</>} — margin{" "}
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
            <Label htmlFor="roundTo">Round to ({currencySymbol()})</Label>
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
                    <TableCell>{rupees(it.old_price)}</TableCell>
                    <TableCell>{rupees(it.new_price)}</TableCell>
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
