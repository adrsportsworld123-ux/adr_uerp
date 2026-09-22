"use client";

// Phase 1's Product & Catalog Management — the actual product-creation
// gap, found during an explicit "review previous phases for anything
// missing" audit: POST /products (and PATCH /products/{id}) had existed
// in phase0_1_design.md's own contract table since Phase 0/1, but
// neither the endpoint nor any UI for it was ever built — after four
// phases of "fully complete" work, there was still no way to add a
// second product to the one hard-seeded in migrations/002_seed.sql.
// Gated by the new catalog.manage permission (Branch Manager/Merchant
// Admin), same tiered-role pattern pricing.manage/credit.manage already
// use.
import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Category, CatalogBrand, TaxSlab, CreatedProduct } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

export default function NewProductPage() {
  const router = useRouter();

  const [name, setName] = useState("");
  const [shortDescription, setShortDescription] = useState("");
  const [hsnCode, setHsnCode] = useState("");
  const [categoryId, setCategoryId] = useState("");
  const [brandId, setBrandId] = useState("");
  const [taxSlabId, setTaxSlabId] = useState("");

  const [sku, setSku] = useState("");
  const [costPrice, setCostPrice] = useState("");
  const [mrp, setMrp] = useState("");
  const [sellingPrice, setSellingPrice] = useState("");

  const [categories, setCategories] = useState<Category[]>([]);
  const [brands, setBrands] = useState<CatalogBrand[]>([]);
  const [taxSlabs, setTaxSlabs] = useState<TaxSlab[]>([]);

  const [newCategoryName, setNewCategoryName] = useState("");
  const [newBrandName, setNewBrandName] = useState("");

  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  function loadLookups() {
    api.get<{ categories: Category[] }>("/api/v1/categories").then((d) => setCategories(d.categories)).catch(() => {});
    api.get<{ brands: CatalogBrand[] }>("/api/v1/brands").then((d) => setBrands(d.brands)).catch(() => {});
    api.get<{ tax_slabs: TaxSlab[] }>("/api/v1/tax-slabs").then((d) => setTaxSlabs(d.tax_slabs)).catch(() => {});
  }
  useEffect(loadLookups, []);

  async function addCategory() {
    if (!newCategoryName.trim()) return;
    try {
      const c = await api.post<Category>("/api/v1/categories", { name: newCategoryName.trim() });
      setCategories((prev) => [...prev, c]);
      setCategoryId(c.category_id);
      setNewCategoryName("");
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not create category");
    }
  }

  async function addBrand() {
    if (!newBrandName.trim()) return;
    try {
      const b = await api.post<CatalogBrand>("/api/v1/brands", { name: newBrandName.trim() });
      setBrands((prev) => [...prev, b]);
      setBrandId(b.brand_id);
      setNewBrandName("");
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not create brand");
    }
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const product = await api.post<CreatedProduct>("/api/v1/products", {
        name,
        short_description: shortDescription,
        hsn_code: hsnCode,
        category_id: categoryId || undefined,
        brand_id: brandId || undefined,
        tax_slab_id: taxSlabId || undefined,
        variants: [
          {
            sku,
            cost_price: Number(costPrice) || 0,
            mrp: Number(mrp),
            selling_price: Number(sellingPrice),
          },
        ],
      });
      toast.success(`Created ${product.name}`);
      router.push("/pricing");
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not create product");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex flex-col gap-6 max-w-2xl">
      <h1 className="text-2xl font-semibold">New Product</h1>
      <form onSubmit={submit}>
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Product</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            <div className="flex flex-col gap-2">
              <Label htmlFor="name">Name</Label>
              <Input id="name" required value={name} onChange={(e) => setName(e.target.value)} />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="shortDescription">Short description</Label>
              <Input id="shortDescription" value={shortDescription} onChange={(e) => setShortDescription(e.target.value)} />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="hsnCode">HSN code</Label>
              <Input id="hsnCode" value={hsnCode} onChange={(e) => setHsnCode(e.target.value)} />
            </div>

            <div className="flex items-end gap-2">
              <div className="flex flex-col gap-2 flex-1">
                <Label>Category (optional)</Label>
                <Select
                  value={categoryId}
                  onValueChange={(v) => setCategoryId(v ?? "")}
                  items={Object.fromEntries(categories.map((c) => [c.category_id, c.name]))}
                >
                  <SelectTrigger>
                    <SelectValue placeholder="None" />
                  </SelectTrigger>
                  <SelectContent>
                    {categories.map((c) => (
                      <SelectItem key={c.category_id} value={c.category_id}>
                        {c.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <Input
                placeholder="New category"
                value={newCategoryName}
                onChange={(e) => setNewCategoryName(e.target.value)}
                className="w-40"
              />
              <Button type="button" variant="outline" onClick={addCategory} disabled={!newCategoryName.trim()}>
                Add
              </Button>
            </div>

            <div className="flex items-end gap-2">
              <div className="flex flex-col gap-2 flex-1">
                <Label>Brand (optional)</Label>
                <Select
                  value={brandId}
                  onValueChange={(v) => setBrandId(v ?? "")}
                  items={Object.fromEntries(brands.map((b) => [b.brand_id, b.name]))}
                >
                  <SelectTrigger>
                    <SelectValue placeholder="None" />
                  </SelectTrigger>
                  <SelectContent>
                    {brands.map((b) => (
                      <SelectItem key={b.brand_id} value={b.brand_id}>
                        {b.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <Input placeholder="New brand" value={newBrandName} onChange={(e) => setNewBrandName(e.target.value)} className="w-40" />
              <Button type="button" variant="outline" onClick={addBrand} disabled={!newBrandName.trim()}>
                Add
              </Button>
            </div>

            <div className="flex flex-col gap-2">
              <Label>Tax slab</Label>
              <Select
                value={taxSlabId}
                onValueChange={(v) => setTaxSlabId(v ?? "")}
                items={Object.fromEntries(taxSlabs.map((t) => [t.tax_slab_id, `${t.name} (CGST ${t.cgst_rate}% + SGST ${t.sgst_rate}%)`]))}
              >
                <SelectTrigger>
                  <SelectValue placeholder="None (0% GST)" />
                </SelectTrigger>
                <SelectContent>
                  {taxSlabs.map((t) => (
                    <SelectItem key={t.tax_slab_id} value={t.tax_slab_id}>
                      {t.name} (CGST {t.cgst_rate}% + SGST {t.sgst_rate}%)
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </CardContent>
        </Card>

        <Card className="mt-4">
          <CardHeader>
            <CardTitle className="text-base">Initial variant</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            <p className="text-xs text-zinc-500">
              A product needs at least one sellable variant to exist — this creates the first one. Add size/color variants and
              barcodes afterward from Pricing and the product&apos;s own barcode-assignment endpoint.
            </p>
            <div className="flex flex-col gap-2">
              <Label htmlFor="sku">SKU</Label>
              <Input id="sku" required value={sku} onChange={(e) => setSku(e.target.value)} />
            </div>
            <div className="grid grid-cols-3 gap-4">
              <div className="flex flex-col gap-2">
                <Label htmlFor="costPrice">Cost price (₹)</Label>
                <Input id="costPrice" type="number" min="0" step="0.01" value={costPrice} onChange={(e) => setCostPrice(e.target.value)} />
              </div>
              <div className="flex flex-col gap-2">
                <Label htmlFor="mrp">MRP (₹)</Label>
                <Input id="mrp" type="number" min="0.01" step="0.01" required value={mrp} onChange={(e) => setMrp(e.target.value)} />
              </div>
              <div className="flex flex-col gap-2">
                <Label htmlFor="sellingPrice">Selling price (₹)</Label>
                <Input
                  id="sellingPrice"
                  type="number"
                  min="0.01"
                  step="0.01"
                  required
                  value={sellingPrice}
                  onChange={(e) => setSellingPrice(e.target.value)}
                />
              </div>
            </div>

            {error && <p className="text-sm text-red-600">{error}</p>}
            <Button type="submit" disabled={busy} className="w-fit">
              {busy ? "Creating..." : "Create product"}
            </Button>
          </CardContent>
        </Card>
      </form>
    </div>
  );
}
