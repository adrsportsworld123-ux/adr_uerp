"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Product, ProductVariant } from "@/lib/types";
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
    api
      .get<{ products: Product[] }>("/api/v1/products")
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
                <TableHead>Margin</TableHead>
                <TableHead>Markup</TableHead>
                <TableHead></TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {products.flatMap((p) =>
                p.variants.map((v) => (
                  <VariantRow key={v.variant_id} productName={p.name} variant={v} onSaved={load} />
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

function VariantRow({ productName, variant, onSaved }: { productName: string; variant: ProductVariant; onSaved: () => void }) {
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
      <TableCell>{productName}</TableCell>
      <TableCell className="font-mono text-xs">{variant.sku}</TableCell>
      <TableCell>₹{variant.cost_price}</TableCell>
      <TableCell>₹{variant.mrp}</TableCell>
      <TableCell>
        {editing ? (
          <Input className="w-24" type="number" step="0.01" value={sellingPrice} onChange={(e) => setSellingPrice(e.target.value)} />
        ) : (
          `₹${variant.selling_price}`
        )}
      </TableCell>
      <TableCell className={marginClass(variant.margin_pct)}>{variant.margin_pct ?? "—"}%</TableCell>
      <TableCell className={marginClass(variant.markup_pct)}>{variant.markup_pct ?? "—"}%</TableCell>
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

function Calculator() {
  const [costPrice, setCostPrice] = useState("");
  const [mode, setMode] = useState<"margin_pct" | "markup_pct">("margin_pct");
  const [pct, setPct] = useState("");
  const [result, setResult] = useState<{ selling_price: number; margin_pct: number | null; markup_pct: number | null } | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function calculate(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    try {
      const res = await api.post<{ selling_price: number; margin_pct: number | null; markup_pct: number | null }>(
        "/api/v1/pricing/calculate",
        { cost_price: Number(costPrice), [mode]: Number(pct) }
      );
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
          <Button type="submit">Calculate</Button>
        </form>
        {error && <p className="text-sm text-red-600 mt-2">{error}</p>}
        {result && (
          <p className="text-sm mt-3">
            Selling price: <span className="font-semibold">₹{result.selling_price}</span> — margin {result.margin_pct}%, markup {result.markup_pct}%
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
