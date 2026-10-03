"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { PricingSettings, Product, ProductVariant } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

function marginClass(pct: number | null) {
  if (pct === null) return "";
  return pct < 0 ? "text-red-600 font-medium" : "text-zinc-700";
}

export default function PricingPage() {
  const [products, setProducts] = useState<Product[]>([]);
  const [error, setError] = useState<string | null>(null);

  function load() {
    // limit=200 (the backend's own cap) rather than the default 50 —
    // found live: as the catalog grows (many e2e specs each creating
    // real products), a brand-new product can sort alphabetically past
    // the default page-1 window and never appear here at all.
    api
      .get<{ products: Product[] }>("/api/v1/products?limit=200")
      .then((d) => setProducts(d.products))
      .catch((e) => setError(e.message));
  }
  useEffect(load, []);

  return (
    <div className="flex flex-col gap-6 max-w-4xl">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Pricing</h1>
        <Link href="/products/new">
          <Button variant="outline">New product</Button>
        </Link>
      </div>
      {error && <p className="text-sm text-red-600">{error}</p>}

      <TaxPricingSettings />

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Products</CardTitle>
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
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      <Calculator />
      <BulkUpdate onApplied={load} />
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
            <span className="text-xs text-zinc-500">
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
              <label className="flex items-center gap-1 text-xs text-red-600">
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
        <p className="text-xs text-zinc-500">
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
      <span className={`rounded px-1.5 py-0.5 ${product.price_includes_tax ? "bg-emerald-50 text-emerald-700" : "bg-zinc-100 text-zinc-700"}`}>
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
        <span className="text-zinc-400 w-14 shrink-0">Original</span>
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
        <span className="text-zinc-400 w-14 shrink-0">System</span>
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
        {error && <p className="text-sm text-red-600 mt-2">{error}</p>}
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
  status: "would_update" | "updated" | "skipped_negative_margin";
}

function BulkUpdate({ onApplied }: { onApplied: () => void }) {
  const [method, setMethod] = useState<"percent" | "fixed" | "set">("percent");
  const [value, setValue] = useState("");
  const [roundTo, setRoundTo] = useState("");
  const [reason, setReason] = useState("");
  const [override, setOverride] = useState(false);
  const [items, setItems] = useState<BulkItem[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  function buildBody() {
    return {
      filter: {},
      method,
      value: Number(value) || 0,
      round_to: Number(roundTo) || 0,
      reason,
      override,
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
      toast.success("Bulk price update applied");
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
        <p className="text-xs text-zinc-500">Applies to every product (no category/brand filter in this UI yet). Preview before applying.</p>
        <form onSubmit={preview} className="flex gap-2 items-end flex-wrap">
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
          <Button type="submit" variant="outline">
            Preview
          </Button>
        </form>

        {error && <p className="text-sm text-red-600">{error}</p>}

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
                      {it.product_name} <span className="text-zinc-400">({it.sku})</span>
                    </TableCell>
                    <TableCell>₹{it.old_price}</TableCell>
                    <TableCell>₹{it.new_price}</TableCell>
                    <TableCell className={it.status === "skipped_negative_margin" ? "text-red-600" : ""}>{it.status}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            {items.some((it) => it.status === "skipped_negative_margin") && (
              <label className="flex items-center gap-2 text-sm text-red-600">
                <input type="checkbox" checked={override} onChange={(e) => setOverride(e.target.checked)} />
                Some items sell below cost — check to override and apply anyway
              </label>
            )}
            <Button disabled={busy || items.every((it) => it.status !== "would_update" && !override)} onClick={apply}>
              {busy ? "Applying..." : "Apply"}
            </Button>
          </>
        )}
      </CardContent>
    </Card>
  );
}
