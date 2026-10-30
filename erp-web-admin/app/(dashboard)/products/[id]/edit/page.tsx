"use client";

// Product editing (erp-core-go internal/catalog/products_edit.go). Until
// this screen a product could be created but never corrected: no way to
// fix a typo'd name, move it to the right category, change a variant's
// SKU or attributes, add a size later, or discontinue it.
//
// Prices are edited here too, but saved through PATCH
// /pricing/variants/{id} — the same endpoint the Pricing page uses — so
// the below-cost guard and price history still apply; the variant
// endpoint itself refuses price fields. Each section saves on its own and
// re-seeds from the server's response (the keyed child components below),
// so what you see after a save is what the server stored.
// The unit (erp-core-go §3.79) is what the stock is counted in; each
// barcode's pack is what one scan adds.
import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { useParams } from "next/navigation";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import {
  CatalogBrand,
  Category,
  CategoryAttribute,
  CollectionSummary,
  ProductDetail,
  ProductDetailVariant,
  TaxSlab,
} from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { SearchSelect } from "@/components/data/search-select";
import { BarcodePacks, UnitSelect, useUnits } from "@/components/catalog/units";

const STATUS_BADGE: Record<string, "default" | "secondary" | "outline" | "destructive"> = {
  active: "default",
  inactive: "secondary",
  discontinued: "destructive",
  out_of_stock: "outline",
};

interface Lookups {
  categories: Category[];
  brands: CatalogBrand[];
  taxSlabs: TaxSlab[];
  collections: CollectionSummary[];
}

export default function ProductEditPage() {
  const { id } = useParams<{ id: string }>();
  const [product, setProduct] = useState<ProductDetail | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [lookups, setLookups] = useState<Lookups>({ categories: [], brands: [], taxSlabs: [], collections: [] });
  const [attrs, setAttrs] = useState<CategoryAttribute[]>([]);

  useEffect(() => {
    api
      .get<ProductDetail>(`/api/v1/products/${id}`)
      .then(setProduct)
      .catch((e) => setLoadError(e instanceof ApiError ? e.message : "Could not load product"));
    Promise.all([
      api.get<{ categories: Category[] }>("/api/v1/categories").then((d) => d.categories).catch(() => []),
      api.get<{ brands: CatalogBrand[] }>("/api/v1/brands").then((d) => d.brands).catch(() => []),
      api.get<{ tax_slabs: TaxSlab[] }>("/api/v1/tax-slabs").then((d) => d.tax_slabs).catch(() => []),
      api.get<{ collections: CollectionSummary[] }>("/api/v1/collections").then((d) => d.collections).catch(() => []),
    ]).then(([categories, brands, taxSlabs, collections]) => setLookups({ categories, brands, taxSlabs, collections }));
  }, [id]);

  // The SAVED category's attribute set drives the variant attribute fields.
  const categoryId = product?.category_id ?? "";
  useEffect(() => {
    const load = categoryId
      ? api.get<{ attributes: CategoryAttribute[] }>(`/api/v1/categories/${categoryId}/attributes`).then((d) => d.attributes)
      : Promise.resolve<CategoryAttribute[]>([]);
    load.then(setAttrs).catch(() => setAttrs([]));
  }, [categoryId]);

  const onSaved = useCallback((p: ProductDetail) => setProduct(p), []);

  if (loadError) return <p className="text-sm text-destructive">{loadError}</p>;
  if (!product) return <p className="text-sm text-muted-foreground">Loading…</p>;

  return (
    <div className="flex flex-col gap-6 max-w-3xl">
      <div className="flex items-center justify-between">
        <div>
          <Link href="/products" className="text-sm text-muted-foreground hover:underline">
            ← Products
          </Link>
          <h1 className="text-2xl font-semibold flex items-center gap-3">
            {product.name}
            <Badge variant={STATUS_BADGE[product.status] ?? "outline"}>{product.status}</Badge>
          </h1>
        </div>
      </div>

      <ProductSection key={`p-${product.updated_at}`} product={product} lookups={lookups} onSaved={onSaved} />

      <h2 className="text-lg font-semibold">Variants</h2>
      {product.variants.map((v) => (
        <VariantSection key={JSON.stringify(v)} productId={product.product_id} variant={v} attrs={attrs} onSaved={onSaved} product={product} />
      ))}

      <AddVariantSection key={`add-${product.variants.length}`} productId={product.product_id} attrs={attrs} onSaved={onSaved} />
    </div>
  );
}

// Optional-link select: "" = none (the API clears the link on "").
function LinkSelect({
  label,
  value,
  onChange,
  options,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  options: { id: string; name: string }[];
}) {
  const id = `link-${label.toLowerCase().replace(/\s+/g, "-")}`;
  return (
    <div className="flex flex-col gap-2">
      <Label htmlFor={id}>{label}</Label>
      <SearchSelect
        id={id}
        ariaLabel={label}
        value={value}
        onChange={onChange}
        options={options.map((o) => ({ value: o.id, label: o.name.replace(/^\//, "").replace(/\//g, " › ") || o.name }))}
        noneLabel="None"
        placeholder="None"
      />
    </div>
  );
}

function ProductSection({ product, lookups, onSaved }: { product: ProductDetail; lookups: Lookups; onSaved: (p: ProductDetail) => void }) {
  const [name, setName] = useState(product.name);
  const [shortDescription, setShortDescription] = useState(product.short_description);
  const [hsnCode, setHsnCode] = useState(product.hsn_code);
  const [categoryId, setCategoryId] = useState(product.category_id ?? "");
  const [brandId, setBrandId] = useState(product.brand_id ?? "");
  const [taxSlabId, setTaxSlabId] = useState(product.tax_slab_id ?? "");
  const [collectionId, setCollectionId] = useState(product.collection_id ?? "");
  const [status, setStatus] = useState<string>(product.status);
  const [priceIncludesTax, setPriceIncludesTax] = useState(product.price_includes_tax);
  // The POS tile colour (migrations/063): "" = none of its own (an
  // open-price item then shows the POS's standard open-price colour).
  const [posColor, setPosColor] = useState(product.pos_color ?? "");
  const { units } = useUnits();
  const [unitId, setUnitId] = useState(product.unit?.unit_id ?? "");
  const [unitInUse, setUnitInUse] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // Only what actually changed is sent — a PATCH, not a rewrite.
  const changes: Record<string, string | boolean> = {};
  if (name !== product.name) changes.name = name;
  if (shortDescription !== product.short_description) changes.short_description = shortDescription;
  if (hsnCode !== product.hsn_code) changes.hsn_code = hsnCode;
  if (categoryId !== (product.category_id ?? "")) changes.category_id = categoryId;
  if (brandId !== (product.brand_id ?? "")) changes.brand_id = brandId;
  if (taxSlabId !== (product.tax_slab_id ?? "")) changes.tax_slab_id = taxSlabId;
  if (collectionId !== (product.collection_id ?? "")) changes.collection_id = collectionId;
  if (status !== product.status) changes.status = status;
  if (priceIncludesTax !== product.price_includes_tax) changes.price_includes_tax = priceIncludesTax;
  if (posColor !== (product.pos_color ?? "")) changes.pos_color = posColor;
  if (unitId && unitId !== (product.unit?.unit_id ?? "")) changes.unit_id = unitId;
  const dirty = Object.keys(changes).length > 0;

  async function save(e: React.FormEvent | null, confirmUnit = false) {
    e?.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const p = await api.patch<ProductDetail>(`/api/v1/products/${product.product_id}`, confirmUnit ? { ...changes, unit_change_confirmed: true } : changes);
      toast.success("Product saved");
      setUnitInUse(false);
      onSaved(p);
    } catch (e) {
      if (e instanceof ApiError && e.code === "UNIT_IN_USE") setUnitInUse(true);
      else setError(e instanceof ApiError ? e.message : "Could not save product");
    } finally {
      setBusy(false);
    }
  }
  const newUnit = units.find((u) => u.unit_id === unitId);

  return (
    <form onSubmit={save}>
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Product details</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <div className="flex flex-col gap-2">
            <Label htmlFor="name">Name</Label>
            <Input id="name" required value={name} onChange={(e) => setName(e.target.value)} />
          </div>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <div className="flex flex-col gap-2">
              <Label htmlFor="shortDescription">Short description</Label>
              <Input id="shortDescription" value={shortDescription} onChange={(e) => setShortDescription(e.target.value)} />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="hsnCode">HSN code</Label>
              <Input id="hsnCode" value={hsnCode} onChange={(e) => setHsnCode(e.target.value)} />
            </div>
          </div>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <LinkSelect label="Category" value={categoryId} onChange={setCategoryId} options={lookups.categories.map((c) => ({ id: c.category_id, name: c.path || c.name }))} />
            <LinkSelect label="Brand" value={brandId} onChange={setBrandId} options={lookups.brands.map((b) => ({ id: b.brand_id, name: b.name }))} />
            <LinkSelect label="Tax slab" value={taxSlabId} onChange={setTaxSlabId} options={lookups.taxSlabs.map((t) => ({ id: t.tax_slab_id, name: t.name }))} />
            <LinkSelect label="Collection" value={collectionId} onChange={setCollectionId} options={lookups.collections.map((c) => ({ id: c.collection_id, name: c.name }))} />
          </div>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <div className="flex flex-col gap-2">
              <Label htmlFor="unit">Counted in</Label>
              <UnitSelect id="unit" value={unitId} onChange={setUnitId} units={units} current={product.unit} />
              <span className="text-xs text-muted-foreground">
                Stock, cost and price are per {newUnit ? newUnit.name.toLowerCase() : "unit"}
                {newUnit && newUnit.decimals === 0 ? "; sold in whole numbers" : newUnit ? `; up to ${newUnit.decimals} decimal place(s)` : ""}.
              </span>
            </div>
          </div>
          {unitInUse && (
            <div className="rounded-lg border border-warn/40 bg-warn/10 p-3 text-sm" role="alert">
              This product already has stock or sales counted in {product.unit?.name.toLowerCase() ?? "its unit"}. After the change the same numbers mean{" "}
              {newUnit?.name.toLowerCase() ?? "the new unit"} (for example, 10 in stock becomes 10 {newUnit?.symbol}).
              <div className="mt-2 flex gap-2">
                <Button type="button" size="sm" variant="destructive" disabled={busy} onClick={() => save(null, true)}>
                  Change the unit anyway
                </Button>
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  onClick={() => {
                    setUnitId(product.unit?.unit_id ?? "");
                    setUnitInUse(false);
                  }}
                >
                  Keep {product.unit?.name.toLowerCase()}
                </Button>
              </div>
            </div>
          )}
          {categoryId !== (product.category_id ?? "") && (
            <p className="text-xs text-warn">
              Changing the category re-checks every variant against the new category&apos;s required attributes — the save is refused, naming the
              variant, if one doesn&apos;t fit. Set its attributes first.
            </p>
          )}
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 items-end">
            <div className="flex flex-col gap-2">
              <Label>Status</Label>
              <Select
                value={status}
                onValueChange={(v) => setStatus(v ?? product.status)}
                items={{ active: "Active — on sale", inactive: "Inactive — hidden, not sellable", discontinued: "Discontinued — no longer sold" }}
              >
                <SelectTrigger aria-label="Product status">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="active">Active — on sale</SelectItem>
                  <SelectItem value="inactive">Inactive — hidden, not sellable</SelectItem>
                  <SelectItem value="discontinued">Discontinued — no longer sold</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <label className="flex items-center gap-2 text-sm pb-2">
              <input type="checkbox" checked={priceIncludesTax} onChange={(e) => setPriceIncludesTax(e.target.checked)} />
              Prices include GST
            </label>
          </div>
          <div className="flex flex-wrap items-center gap-3 text-sm">
            <Label htmlFor="pos-color">POS tile colour</Label>
            <input
              id="pos-color"
              type="color"
              className="h-9 w-14 cursor-pointer rounded border bg-card p-1"
              value={posColor || "#7445a8"}
              onChange={(e) => setPosColor(e.target.value.toUpperCase())}
            />
            <div className="flex flex-wrap gap-1" role="group" aria-label="Quick colours">
              {POS_COLORS.map((c) => (
                <button
                  key={c.hex}
                  type="button"
                  title={c.name}
                  aria-label={c.name}
                  aria-pressed={posColor === c.hex}
                  onClick={() => setPosColor(c.hex)}
                  className={`size-6 rounded-full border ${posColor === c.hex ? "ring-2 ring-ring ring-offset-1" : ""}`}
                  style={{ background: c.hex }}
                />
              ))}
            </div>
            <span className="text-xs text-muted-foreground" data-testid="pos-color-value">
              {posColor ? posColor : "None — an open-price item uses the standard open-price colour"}
            </span>
            {posColor && (
              <Button type="button" size="sm" variant="ghost" onClick={() => setPosColor("")}>
                No colour
              </Button>
            )}
          </div>
          {priceIncludesTax !== product.price_includes_tax && (
            <p className="text-xs text-warn">
              This re-reads every variant&apos;s current selling price {priceIncludesTax ? "as GST-inclusive" : "as GST-exclusive"} — the shelf price
              {priceIncludesTax ? " becomes the bill total" : " gets GST added on top"}. Past sales are unaffected.
            </p>
          )}
          {error && <p className="text-sm text-destructive">{error}</p>}
          <Button type="submit" disabled={busy || !dirty || !name.trim()} className="w-fit">
            {busy ? "Saving..." : "Save product"}
          </Button>
        </CardContent>
      </Card>
    </form>
  );
}

function AttributeFields({
  attrs,
  values,
  onChange,
}: {
  attrs: CategoryAttribute[];
  values: Record<string, string>;
  onChange: (name: string, value: string) => void;
}) {
  if (!attrs.length) return null;
  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
      {attrs.map((a) => (
        <div key={a.attribute_id} className="flex flex-col gap-2">
          <Label>
            {a.name}
            {a.unit ? ` (${a.unit})` : ""}
            {a.required ? " *" : ""}
          </Label>
          {a.input_type === "select" ? (
            <Select value={values[a.name] ?? ""} onValueChange={(v) => onChange(a.name, v ?? "")} items={Object.fromEntries(a.values.map((v) => [v.value, v.value]))}>
              <SelectTrigger aria-label={a.name}>
                <SelectValue placeholder={a.required ? "Required" : "Optional"} />
              </SelectTrigger>
              <SelectContent>
                {a.values.map((v) => (
                  <SelectItem key={v.value_id} value={v.value}>
                    {v.value}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          ) : (
            <Input aria-label={a.name} type={a.input_type === "number" ? "number" : "text"} value={values[a.name] ?? ""} onChange={(e) => onChange(a.name, e.target.value)} />
          )}
        </div>
      ))}
    </div>
  );
}

function comboToStrings(combo: Record<string, string | number>): Record<string, string> {
  return Object.fromEntries(Object.entries(combo).map(([k, v]) => [k, String(v)]));
}

function VariantSection({
  productId,
  variant,
  attrs,
  onSaved,
  product,
}: {
  productId: string;
  variant: ProductDetailVariant;
  attrs: CategoryAttribute[];
  onSaved: (p: ProductDetail) => void;
  product: ProductDetail;
}) {
  const [sku, setSku] = useState(variant.sku);
  const [attrValues, setAttrValues] = useState<Record<string, string>>(comboToStrings(variant.attribute_combo));
  const [pluCode, setPluCode] = useState(variant.plu_code ?? "");
  const [status, setStatus] = useState<string>(variant.status);
  const [trackBatch, setTrackBatch] = useState(variant.track_batch);
  const [costPrice, setCostPrice] = useState(variant.cost_price);
  const [mrp, setMrp] = useState(variant.mrp);
  const [sellingPrice, setSellingPrice] = useState(variant.selling_price);
  const [priceReason, setPriceReason] = useState("");
  const [needsOverride, setNeedsOverride] = useState(false);
  const [override, setOverride] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const hasStock = Number(variant.on_hand_total) !== 0;

  const originalAttrs = comboToStrings(variant.attribute_combo);
  const cleanAttrs = Object.fromEntries(Object.entries(attrValues).filter(([, v]) => v !== ""));
  const attrsChanged = JSON.stringify(cleanAttrs) !== JSON.stringify(Object.fromEntries(Object.entries(originalAttrs).filter(([, v]) => v !== "")));

  const changes: Record<string, unknown> = {};
  if (sku !== variant.sku) changes.sku = sku;
  if (attrsChanged) changes.attribute_combo = cleanAttrs;
  if (pluCode !== (variant.plu_code ?? "")) changes.plu_code = pluCode;
  if (status !== variant.status) changes.status = status;
  if (trackBatch !== variant.track_batch) changes.track_batch = trackBatch;
  const variantDirty = Object.keys(changes).length > 0;

  const priceChanges: Record<string, number> = {};
  if (Number(costPrice) !== Number(variant.cost_price)) priceChanges.cost_price = Number(costPrice);
  if (Number(mrp) !== Number(variant.mrp)) priceChanges.mrp = Number(mrp);
  if (Number(sellingPrice) !== Number(variant.selling_price)) priceChanges.selling_price = Number(sellingPrice);
  const pricesDirty = Object.keys(priceChanges).length > 0;

  async function reload() {
    onSaved(await api.get<ProductDetail>(`/api/v1/products/${productId}`));
  }

  async function saveVariant() {
    setBusy(true);
    setError(null);
    try {
      const p = await api.patch<ProductDetail>(`/api/v1/products/variants/${variant.variant_id}`, changes);
      toast.success(`Variant ${p.variants.find((v) => v.variant_id === variant.variant_id)?.sku ?? ""} saved`);
      onSaved(p);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not save variant");
    } finally {
      setBusy(false);
    }
  }

  async function savePrices() {
    setBusy(true);
    setError(null);
    try {
      await api.patch(`/api/v1/pricing/variants/${variant.variant_id}`, { ...priceChanges, reason: priceReason, override });
      toast.success("Prices saved");
      await reload();
    } catch (e) {
      if (e instanceof ApiError && e.code === "NEGATIVE_MARGIN") setNeedsOverride(true);
      setError(e instanceof ApiError ? e.message : "Could not save prices");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card data-testid={`variant-${variant.sku}`}>
      <CardHeader>
        <CardTitle className="text-base flex items-center gap-3">
          {variant.sku}
          <Badge variant={STATUS_BADGE[variant.status] ?? "outline"}>{variant.status}</Badge>
          <span className="text-xs font-normal text-muted-foreground">stock across branches: {variant.on_hand_total}</span>
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
          <div className="flex flex-col gap-2">
            <Label htmlFor={`sku-${variant.variant_id}`}>SKU</Label>
            <Input id={`sku-${variant.variant_id}`} value={sku} onChange={(e) => setSku(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor={`plu-${variant.variant_id}`}>PLU code (weighing scale)</Label>
            <Input id={`plu-${variant.variant_id}`} value={pluCode} onChange={(e) => setPluCode(e.target.value)} placeholder="none" />
          </div>
          <div className="flex flex-col gap-2">
            <Label>Variant status</Label>
            <Select
              value={status}
              onValueChange={(v) => setStatus(v ?? variant.status)}
              items={{ active: "Active", inactive: "Inactive", discontinued: "Discontinued", out_of_stock: "Out of stock" }}
            >
              <SelectTrigger aria-label="Variant status">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="active">Active</SelectItem>
                <SelectItem value="inactive">Inactive</SelectItem>
                <SelectItem value="discontinued">Discontinued</SelectItem>
                {variant.status === "out_of_stock" && <SelectItem value="out_of_stock">Out of stock</SelectItem>}
              </SelectContent>
            </Select>
          </div>
        </div>
        <AttributeFields attrs={attrs} values={attrValues} onChange={(n, v) => setAttrValues((prev) => ({ ...prev, [n]: v }))} />
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={trackBatch} disabled={hasStock} onChange={(e) => setTrackBatch(e.target.checked)} />
          Batch / expiry tracking
          {hasStock && <span className="text-xs text-muted-foreground">— can only be switched while this variant holds no stock</span>}
        </label>
        <BarcodePacks variantId={variant.variant_id} barcodes={variant.barcodes ?? []} unit={product.unit} onChanged={reload} />
        <Button variant="outline" className="w-fit" disabled={busy || !variantDirty || !sku.trim()} onClick={saveVariant}>
          Save variant
        </Button>

        <div className="border-t pt-4 flex flex-col gap-3">
          <div className="grid grid-cols-2 sm:grid-cols-4 gap-4">
            <div className="flex flex-col gap-2">
              <Label htmlFor={`cost-${variant.variant_id}`}>Cost price</Label>
              <Input id={`cost-${variant.variant_id}`} type="number" min="0" step="0.01" value={costPrice} onChange={(e) => setCostPrice(e.target.value)} />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor={`mrp-${variant.variant_id}`}>MRP</Label>
              <Input id={`mrp-${variant.variant_id}`} type="number" min="0" step="0.01" value={mrp} onChange={(e) => setMrp(e.target.value)} />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor={`sell-${variant.variant_id}`}>Selling price</Label>
              <Input id={`sell-${variant.variant_id}`} type="number" min="0" step="0.01" value={sellingPrice} onChange={(e) => setSellingPrice(e.target.value)} />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor={`reason-${variant.variant_id}`}>Reason</Label>
              <Input id={`reason-${variant.variant_id}`} value={priceReason} onChange={(e) => setPriceReason(e.target.value)} placeholder="price history note" />
            </div>
          </div>
          {needsOverride && (
            <label className="flex items-center gap-2 text-sm text-destructive">
              <input type="checkbox" checked={override} onChange={(e) => setOverride(e.target.checked)} />
              This sells below cost — save anyway
            </label>
          )}
          <Button variant="outline" className="w-fit" disabled={busy || !pricesDirty} onClick={savePrices}>
            Save prices
          </Button>
        </div>
        <OpenPriceSection variant={variant} onSaved={onSaved} />
        {error && <p className="text-sm text-destructive">{error}</p>}
      </CardContent>
    </Card>
  );
}

// Phase 13 open price: the cashier types the price at the till, within a
// range; above the approval amount a Branch Manager / Merchant Admin
// approves with code + PIN. A pricing decision, so it needs pricing.manage.
function OpenPriceSection({ variant, onSaved }: { variant: ProductDetailVariant; onSaved: (p: ProductDetail) => void }) {
  const { can } = usePermissions();
  const allowed = can("pricing.manage");
  const op = variant.open_price;
  const [enabled, setEnabled] = useState(op != null);
  const [min, setMin] = useState(op?.min ?? "0");
  const [max, setMax] = useState(op?.max ?? variant.mrp);
  const [approval, setApproval] = useState(op?.approval_above ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const id = variant.variant_id;

  const minN = Number(min);
  const maxN = Number(max);
  const approvalN = approval.trim() === "" ? null : Number(approval);
  const problem = !enabled
    ? null
    : min.trim() === "" || max.trim() === "" || Number.isNaN(minN) || Number.isNaN(maxN)
      ? "Enter a minimum and a maximum"
      : minN < 0 || maxN <= 0 || minN > maxN
        ? "The minimum must be 0 or more and not above the maximum"
        : approvalN != null && (Number.isNaN(approvalN) || approvalN < minN || approvalN > maxN)
          ? "The approval amount must be between the minimum and maximum"
          : null;
  const dirty =
    enabled !== (op != null) ||
    (enabled && (minN !== Number(op?.min ?? NaN) || maxN !== Number(op?.max ?? NaN) || approvalN !== (op?.approval_above == null ? null : Number(op.approval_above))));

  async function save() {
    setBusy(true);
    setError(null);
    try {
      const body = enabled ? { enabled, min: minN, max: maxN, approval_above: approvalN } : { enabled: false };
      const p = await api.patch<ProductDetail>(`/api/v1/products/variants/${id}`, { open_price: body });
      toast.success(enabled ? "Open price saved" : "Open price turned off");
      onSaved(p);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not save open price");
    } finally {
      setBusy(false);
    }
  }

  return (
    <fieldset className="border-t pt-4 flex flex-col gap-3" disabled={!allowed} aria-describedby={allowed ? undefined : `op-perm-${id}`}>
      <legend className="sr-only">Open price</legend>
      <label className="flex items-center gap-2 text-sm font-medium">
        <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
        Open price at the till
      </label>
      <p className="text-xs text-muted-foreground -mt-2">
        The cashier types the price when selling (alterations, loose goods, one-off deals). Stock is tracked as usual; the selling price above is shown as
        the suggestion.
      </p>
      {enabled && (
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
          <div className="flex flex-col gap-2">
            <Label htmlFor={`op-min-${id}`}>Lowest price</Label>
            <Input id={`op-min-${id}`} type="number" min="0" step="0.01" value={min} onChange={(e) => setMin(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor={`op-max-${id}`}>Highest price</Label>
            <Input id={`op-max-${id}`} type="number" min="0" step="0.01" value={max} onChange={(e) => setMax(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor={`op-approval-${id}`}>Manager approval above</Label>
            <Input
              id={`op-approval-${id}`}
              type="number"
              min="0"
              step="0.01"
              value={approval}
              placeholder="never"
              onChange={(e) => setApproval(e.target.value)}
            />
          </div>
        </div>
      )}
      {problem && (
        <p className="text-sm text-destructive" role="alert">
          {problem}
        </p>
      )}
      {error && <p className="text-sm text-destructive">{error}</p>}
      {!allowed && (
        <p id={`op-perm-${id}`} className="text-xs text-muted-foreground">
          {missingPermissionHint("pricing.manage")}
        </p>
      )}
      <Button variant="outline" className="w-fit" disabled={busy || !dirty || problem != null} onClick={save}>
        Save open price
      </Button>
    </fieldset>
  );
}

function AddVariantSection({ productId, attrs, onSaved }: { productId: string; attrs: CategoryAttribute[]; onSaved: (p: ProductDetail) => void }) {
  const [sku, setSku] = useState("");
  const [costPrice, setCostPrice] = useState("");
  const [mrp, setMrp] = useState("");
  const [sellingPrice, setSellingPrice] = useState("");
  const [attrValues, setAttrValues] = useState<Record<string, string>>({});
  const [generateBarcode, setGenerateBarcode] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function add(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const p = await api.post<ProductDetail>(`/api/v1/products/${productId}/variants`, {
        sku,
        cost_price: Number(costPrice) || 0,
        mrp: Number(mrp),
        selling_price: Number(sellingPrice),
        generate_barcode: generateBarcode,
        attribute_combo: Object.fromEntries(Object.entries(attrValues).filter(([, v]) => v !== "")),
      });
      toast.success(`Variant ${sku} added`);
      onSaved(p);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not add variant");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={add}>
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Add a variant</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <div className="grid grid-cols-2 sm:grid-cols-4 gap-4">
            <div className="flex flex-col gap-2">
              <Label htmlFor="newSku">SKU</Label>
              <Input id="newSku" required value={sku} onChange={(e) => setSku(e.target.value)} />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="newCost">Cost price</Label>
              <Input id="newCost" type="number" min="0" step="0.01" value={costPrice} onChange={(e) => setCostPrice(e.target.value)} />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="newMrp">MRP</Label>
              <Input id="newMrp" type="number" min="0.01" step="0.01" required value={mrp} onChange={(e) => setMrp(e.target.value)} />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="newSelling">Selling price</Label>
              <Input id="newSelling" type="number" min="0.01" step="0.01" required value={sellingPrice} onChange={(e) => setSellingPrice(e.target.value)} />
            </div>
          </div>
          <AttributeFields attrs={attrs} values={attrValues} onChange={(n, v) => setAttrValues((prev) => ({ ...prev, [n]: v }))} />
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={generateBarcode} onChange={(e) => setGenerateBarcode(e.target.checked)} />
            Generate a system barcode
          </label>
          {error && <p className="text-sm text-destructive">{error}</p>}
          <Button type="submit" variant="outline" className="w-fit" disabled={busy}>
            {busy ? "Adding..." : "Add variant"}
          </Button>
        </CardContent>
      </Card>
    </form>
  );
}

// Quick picks for the POS tile colour; any colour can be chosen.
const POS_COLORS = [
  { name: "Amber", hex: "#F59E0B" },
  { name: "Orange", hex: "#EA580C" },
  { name: "Red", hex: "#DC2626" },
  { name: "Pink", hex: "#DB2777" },
  { name: "Purple", hex: "#7445A8" },
  { name: "Blue", hex: "#2563EB" },
  { name: "Sky", hex: "#0EA5E9" },
  { name: "Teal", hex: "#0C7486" },
  { name: "Green", hex: "#16A34A" },
];
