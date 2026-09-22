"use client";

// Phase 3, sub-area 2's admin surface (erp-core-go's phased_roadmap.md /
// docs/phase0_1_design.md §3.11). Deliberately CRUD/config only, not a
// cart-flow UI: applying a promotion/coupon/loyalty redemption to a live
// sale is a POS-terminal action (erp-pos-flutter), the same boundary this
// app already draws for Pricing (edits prices here; a sale picks them up
// automatically, this app never "sells" anything). A customer's loyalty
// balance/ledger is shown read-only on their own detail page
// (/customers/[id]) instead of here, since it's customer-scoped state,
// not merchant-wide configuration like the three sections below.
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Coupon, LoyaltyConfig, Product, Promotion, PromotionConfig, VolumeTier } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

export default function PromotionsPage() {
  return (
    <div className="flex flex-col gap-6 max-w-4xl">
      <h1 className="text-2xl font-semibold">Promotions & Loyalty</h1>
      <PromotionsSection />
      <CouponsSection />
      <LoyaltySettingsSection />
    </div>
  );
}

// ---------------------------------------------------------------------
// Promotions
// ---------------------------------------------------------------------

const PROMO_TYPES = [
  { value: "percent", label: "% off" },
  { value: "fixed", label: "Flat amount off" },
  { value: "bogo", label: "Buy X get Y (BOGO)" },
  { value: "volume", label: "Volume tiers" },
  { value: "min_value", label: "Minimum purchase" },
] as const;

function PromotionsSection() {
  const [promotions, setPromotions] = useState<Promotion[]>([]);
  const [products, setProducts] = useState<Product[]>([]);
  const [error, setError] = useState<string | null>(null);

  function load() {
    api
      .get<{ promotions: Promotion[] }>("/api/v1/promotions")
      .then((d) => setPromotions(d.promotions))
      .catch((e) => setError(e.message));
  }
  useEffect(load, []);
  useEffect(() => {
    api
      .get<{ products: Product[] }>("/api/v1/products")
      .then((d) => setProducts(d.products))
      .catch(() => {}); // best-effort — only used to populate the product picker below
  }, []);

  async function toggleActive(p: Promotion) {
    try {
      await api.patch(`/api/v1/promotions/${p.promotion_id}`, { active: !p.active });
      toast.success(p.active ? "Promotion deactivated" : "Promotion activated");
      load();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not update promotion");
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Promotions</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-6">
        <NewPromotionForm products={products} onCreated={load} />

        {error && <p className="text-sm text-red-600">{error}</p>}
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Type</TableHead>
              <TableHead>Scope</TableHead>
              <TableHead>Stacking</TableHead>
              <TableHead>Status</TableHead>
              <TableHead></TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {promotions.map((p) => (
              <TableRow key={p.promotion_id}>
                <TableCell className="font-medium">{p.name}</TableCell>
                <TableCell className="text-xs">{PROMO_TYPES.find((t) => t.value === p.promo_type)?.label ?? p.promo_type}</TableCell>
                <TableCell className="text-xs">
                  {p.application_level}
                  {p.target_segment && <span className="text-zinc-400"> · {p.target_segment}</span>}
                </TableCell>
                <TableCell className="text-xs">{p.stacking}</TableCell>
                <TableCell>
                  <Badge variant={p.active ? "default" : "secondary"}>{p.active ? "active" : "inactive"}</Badge>
                </TableCell>
                <TableCell>
                  <Button size="sm" variant="outline" onClick={() => toggleActive(p)}>
                    {p.active ? "Deactivate" : "Activate"}
                  </Button>
                </TableCell>
              </TableRow>
            ))}
            {promotions.length === 0 && (
              <TableRow>
                <TableCell colSpan={6} className="text-center text-sm text-zinc-500 py-6">
                  No promotions yet
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  );
}

function NewPromotionForm({ products, onCreated }: { products: Product[]; onCreated: () => void }) {
  const [name, setName] = useState("");
  const [promoType, setPromoType] = useState<(typeof PROMO_TYPES)[number]["value"]>("percent");
  const [applicationLevel, setApplicationLevel] = useState<"order" | "product" | "category">("order");
  const [productId, setProductId] = useState("");
  const [categoryId, setCategoryId] = useState("");
  const [targetSegment, setTargetSegment] = useState<string>("");
  const [stacking, setStacking] = useState<"exclusive" | "stackable">("exclusive");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // One field per promo_type, kept in local state and assembled into
  // PromotionConfig on submit — mirrors the Go side's single unified
  // config shape (internal/promotions.promoConfig) rather than five
  // separate forms.
  const [valuePercent, setValuePercent] = useState("");
  const [valueAmount, setValueAmount] = useState("");
  const [buyQty, setBuyQty] = useState("");
  const [getQty, setGetQty] = useState("");
  const [getDiscountPct, setGetDiscountPct] = useState("");
  const [tiers, setTiers] = useState<VolumeTier[]>([{ min_qty: 3, max_qty: 5, discount_pct: 5 }]);
  const [minPurchaseAmount, setMinPurchaseAmount] = useState("");
  const [minValueMode, setMinValueMode] = useState<"discount_amount" | "discount_pct">("discount_amount");
  const [minValueAmount, setMinValueAmount] = useState("");

  // bogo/volume are inherently product/category-scoped (erp-core-go
  // rejects application_level=order for these) — steer the level picker
  // there in the same event that changes promo_type, rather than a
  // render-triggering effect (react-hooks/set-state-in-effect).
  function handlePromoTypeChange(v: string) {
    const next = (v as typeof promoType) ?? "percent";
    setPromoType(next);
    if ((next === "bogo" || next === "volume") && applicationLevel === "order") {
      setApplicationLevel("product");
    }
  }

  function buildConfig(): PromotionConfig {
    switch (promoType) {
      case "percent":
        return { value_percent: Number(valuePercent) };
      case "fixed":
        return { value_amount: Number(valueAmount) };
      case "bogo":
        return { buy_qty: Number(buyQty), get_qty: Number(getQty), get_discount_pct: Number(getDiscountPct) };
      case "volume":
        return { tiers };
      case "min_value":
        return {
          min_purchase_amount: Number(minPurchaseAmount),
          discount_amount: minValueMode === "discount_amount" ? Number(minValueAmount) : undefined,
          discount_pct: minValueMode === "discount_pct" ? Number(minValueAmount) : undefined,
        };
    }
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await api.post("/api/v1/promotions", {
        name,
        promo_type: promoType,
        application_level: applicationLevel,
        product_id: applicationLevel === "product" ? productId : "",
        category_id: applicationLevel === "category" ? categoryId : "",
        target_segment: targetSegment,
        config: buildConfig(),
        stacking,
      });
      toast.success("Promotion created");
      setName("");
      setValuePercent("");
      setValueAmount("");
      setBuyQty("");
      setGetQty("");
      setGetDiscountPct("");
      setMinPurchaseAmount("");
      setMinValueAmount("");
      onCreated();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not create promotion");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={handleSubmit} className="flex flex-col gap-4 border-b pb-6">
      <div className="grid grid-cols-2 gap-4">
        <div className="flex flex-col gap-2">
          <Label htmlFor="promoName">Name *</Label>
          <Input id="promoName" required value={name} onChange={(e) => setName(e.target.value)} />
        </div>
        <div className="flex flex-col gap-2">
          <Label>Type</Label>
          <Select
            value={promoType}
            onValueChange={(v) => handlePromoTypeChange(v ?? "percent")}
            items={Object.fromEntries(PROMO_TYPES.map((t) => [t.value, t.label]))}
          >
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {PROMO_TYPES.map((t) => (
                <SelectItem key={t.value} value={t.value}>
                  {t.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </div>

      <div className="grid grid-cols-3 gap-4">
        <div className="flex flex-col gap-2">
          <Label>Applies to</Label>
          <Select
            value={applicationLevel}
            onValueChange={(v) => setApplicationLevel((v as typeof applicationLevel) ?? "order")}
            items={{ order: "Whole order", product: "One product", category: "One category" }}
          >
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="order" disabled={promoType === "bogo" || promoType === "volume"}>
                Whole order
              </SelectItem>
              <SelectItem value="product">One product</SelectItem>
              <SelectItem value="category">One category</SelectItem>
            </SelectContent>
          </Select>
        </div>
        {applicationLevel === "product" && (
          <div className="flex flex-col gap-2">
            <Label>Product</Label>
            <Select
              value={productId}
              onValueChange={(v) => setProductId(v ?? "")}
              items={Object.fromEntries(products.map((p) => [p.product_id, p.name]))}
            >
              <SelectTrigger>
                <SelectValue placeholder="Select a product" />
              </SelectTrigger>
              <SelectContent>
                {products.map((p) => (
                  <SelectItem key={p.product_id} value={p.product_id}>
                    {p.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        )}
        {applicationLevel === "category" && (
          <div className="flex flex-col gap-2">
            <Label htmlFor="categoryId">Category ID</Label>
            <Input id="categoryId" required value={categoryId} onChange={(e) => setCategoryId(e.target.value)} placeholder="UUID" />
            <p className="text-xs text-zinc-500">No category picker yet — same gap as Party Ledger&apos;s customer field.</p>
          </div>
        )}
        <div className="flex flex-col gap-2">
          <Label>Customer segment (optional)</Label>
          <Select
            value={targetSegment || "any"}
            onValueChange={(v) => setTargetSegment(v === "any" ? "" : (v ?? ""))}
            items={{ any: "Any customer", vip: "VIP", regular: "Regular", new: "New", dormant: "Dormant" }}
          >
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="any">Any customer</SelectItem>
              <SelectItem value="vip">VIP</SelectItem>
              <SelectItem value="regular">Regular</SelectItem>
              <SelectItem value="new">New</SelectItem>
              <SelectItem value="dormant">Dormant</SelectItem>
            </SelectContent>
          </Select>
        </div>
      </div>

      <PromoTypeFields
        promoType={promoType}
        valuePercent={valuePercent}
        setValuePercent={setValuePercent}
        valueAmount={valueAmount}
        setValueAmount={setValueAmount}
        buyQty={buyQty}
        setBuyQty={setBuyQty}
        getQty={getQty}
        setGetQty={setGetQty}
        getDiscountPct={getDiscountPct}
        setGetDiscountPct={setGetDiscountPct}
        tiers={tiers}
        setTiers={setTiers}
        minPurchaseAmount={minPurchaseAmount}
        setMinPurchaseAmount={setMinPurchaseAmount}
        minValueMode={minValueMode}
        setMinValueMode={setMinValueMode}
        minValueAmount={minValueAmount}
        setMinValueAmount={setMinValueAmount}
      />

      <div className="flex flex-col gap-2 w-48">
        <Label>Stacking</Label>
        <Select
          value={stacking}
          onValueChange={(v) => setStacking((v as typeof stacking) ?? "exclusive")}
          items={{ exclusive: "Exclusive", stackable: "Stackable" }}
        >
          <SelectTrigger>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="exclusive">Exclusive</SelectItem>
            <SelectItem value="stackable">Stackable</SelectItem>
          </SelectContent>
        </Select>
      </div>

      {error && <p className="text-sm text-red-600">{error}</p>}
      <Button type="submit" disabled={busy} className="w-fit">
        {busy ? "Creating..." : "Create promotion"}
      </Button>
    </form>
  );
}

interface PromoTypeFieldsProps {
  promoType: (typeof PROMO_TYPES)[number]["value"];
  valuePercent: string;
  setValuePercent: (v: string) => void;
  valueAmount: string;
  setValueAmount: (v: string) => void;
  buyQty: string;
  setBuyQty: (v: string) => void;
  getQty: string;
  setGetQty: (v: string) => void;
  getDiscountPct: string;
  setGetDiscountPct: (v: string) => void;
  tiers: VolumeTier[];
  setTiers: (v: VolumeTier[]) => void;
  minPurchaseAmount: string;
  setMinPurchaseAmount: (v: string) => void;
  minValueMode: "discount_amount" | "discount_pct";
  setMinValueMode: (v: "discount_amount" | "discount_pct") => void;
  minValueAmount: string;
  setMinValueAmount: (v: string) => void;
}

function PromoTypeFields(p: PromoTypeFieldsProps) {
  if (p.promoType === "percent") {
    return (
      <div className="flex flex-col gap-2 w-40">
        <Label htmlFor="valuePercent">Percent off *</Label>
        <Input id="valuePercent" type="number" min="0.01" max="100" step="0.01" required value={p.valuePercent} onChange={(e) => p.setValuePercent(e.target.value)} />
      </div>
    );
  }
  if (p.promoType === "fixed") {
    return (
      <div className="flex flex-col gap-2 w-40">
        <Label htmlFor="valueAmount">Amount off (₹) *</Label>
        <Input id="valueAmount" type="number" min="0.01" step="0.01" required value={p.valueAmount} onChange={(e) => p.setValueAmount(e.target.value)} />
      </div>
    );
  }
  if (p.promoType === "bogo") {
    return (
      <div className="flex gap-4">
        <div className="flex flex-col gap-2 w-28">
          <Label htmlFor="buyQty">Buy qty *</Label>
          <Input id="buyQty" type="number" min="1" step="1" required value={p.buyQty} onChange={(e) => p.setBuyQty(e.target.value)} />
        </div>
        <div className="flex flex-col gap-2 w-28">
          <Label htmlFor="getQty">Get qty *</Label>
          <Input id="getQty" type="number" min="1" step="1" required value={p.getQty} onChange={(e) => p.setGetQty(e.target.value)} />
        </div>
        <div className="flex flex-col gap-2 w-36">
          <Label htmlFor="getDiscountPct">Discount on those (%) *</Label>
          <Input id="getDiscountPct" type="number" min="0.01" max="100" step="0.01" required value={p.getDiscountPct} onChange={(e) => p.setGetDiscountPct(e.target.value)} />
        </div>
      </div>
    );
  }
  if (p.promoType === "volume") {
    function updateTier(i: number, field: keyof VolumeTier, value: number) {
      const next = [...p.tiers];
      next[i] = { ...next[i], [field]: value };
      p.setTiers(next);
    }
    return (
      <div className="flex flex-col gap-2">
        <Label>Quantity tiers *</Label>
        {p.tiers.map((t, i) => (
          <div key={i} className="flex gap-2 items-end">
            <div className="flex flex-col gap-1">
              <span className="text-xs text-zinc-500">Min qty</span>
              <Input type="number" min="1" step="1" className="w-24" value={t.min_qty} onChange={(e) => updateTier(i, "min_qty", Number(e.target.value))} />
            </div>
            <div className="flex flex-col gap-1">
              <span className="text-xs text-zinc-500">Max qty (0 = no limit)</span>
              <Input type="number" min="0" step="1" className="w-32" value={t.max_qty} onChange={(e) => updateTier(i, "max_qty", Number(e.target.value))} />
            </div>
            <div className="flex flex-col gap-1">
              <span className="text-xs text-zinc-500">Discount %</span>
              <Input type="number" min="0.01" max="100" step="0.01" className="w-24" value={t.discount_pct} onChange={(e) => updateTier(i, "discount_pct", Number(e.target.value))} />
            </div>
            {p.tiers.length > 1 && (
              <Button type="button" size="sm" variant="ghost" onClick={() => p.setTiers(p.tiers.filter((_, idx) => idx !== i))}>
                Remove
              </Button>
            )}
          </div>
        ))}
        <Button
          type="button"
          size="sm"
          variant="outline"
          className="w-fit"
          onClick={() => p.setTiers([...p.tiers, { min_qty: 1, max_qty: 0, discount_pct: 5 }])}
        >
          Add tier
        </Button>
      </div>
    );
  }
  // min_value
  return (
    <div className="flex gap-4 items-end">
      <div className="flex flex-col gap-2 w-40">
        <Label htmlFor="minPurchaseAmount">Min purchase (₹) *</Label>
        <Input id="minPurchaseAmount" type="number" min="0.01" step="0.01" required value={p.minPurchaseAmount} onChange={(e) => p.setMinPurchaseAmount(e.target.value)} />
      </div>
      <div className="flex flex-col gap-2 w-40">
        <Label>Discount as</Label>
        <Select
          value={p.minValueMode}
          onValueChange={(v) => p.setMinValueMode((v as typeof p.minValueMode) ?? "discount_amount")}
          items={{ discount_amount: "Flat amount (₹)", discount_pct: "Percent (%)" }}
        >
          <SelectTrigger>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="discount_amount">Flat amount (₹)</SelectItem>
            <SelectItem value="discount_pct">Percent (%)</SelectItem>
          </SelectContent>
        </Select>
      </div>
      <div className="flex flex-col gap-2 w-32">
        <Label htmlFor="minValueAmount">{p.minValueMode === "discount_amount" ? "Amount off *" : "Percent off *"}</Label>
        <Input id="minValueAmount" type="number" min="0.01" step="0.01" required value={p.minValueAmount} onChange={(e) => p.setMinValueAmount(e.target.value)} />
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------
// Coupons
// ---------------------------------------------------------------------

function CouponsSection() {
  const [coupons, setCoupons] = useState<Coupon[]>([]);
  const [error, setError] = useState<string | null>(null);

  const [code, setCode] = useState("");
  const [promoType, setPromoType] = useState<"percent" | "fixed">("fixed");
  const [value, setValue] = useState("");
  const [minPurchase, setMinPurchase] = useState("");
  const [usageLimitTotal, setUsageLimitTotal] = useState("");
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);

  function load() {
    api
      .get<{ coupons: Coupon[] }>("/api/v1/coupons")
      .then((d) => setCoupons(d.coupons))
      .catch((e) => setError(e.message));
  }
  useEffect(load, []);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setFormError(null);
    setBusy(true);
    try {
      await api.post("/api/v1/coupons", {
        code,
        promo_type: promoType,
        value: Number(value),
        min_purchase_amount: Number(minPurchase) || 0,
        usage_limit_total: usageLimitTotal ? Number(usageLimitTotal) : null,
      });
      toast.success("Coupon created");
      setCode("");
      setValue("");
      setMinPurchase("");
      setUsageLimitTotal("");
      load();
    } catch (e) {
      setFormError(e instanceof ApiError ? e.message : "Could not create coupon");
    } finally {
      setBusy(false);
    }
  }

  async function toggleActive(c: Coupon) {
    try {
      await api.patch(`/api/v1/coupons/${c.coupon_id}`, { active: !c.active });
      toast.success(c.active ? "Coupon deactivated" : "Coupon activated");
      load();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not update coupon");
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Coupons</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-6">
        <form onSubmit={handleSubmit} className="flex gap-2 items-end flex-wrap border-b pb-6">
          <div className="flex flex-col gap-2">
            <Label htmlFor="code">Code *</Label>
            <Input id="code" required className="w-32 uppercase" value={code} onChange={(e) => setCode(e.target.value.toUpperCase())} />
          </div>
          <div className="flex flex-col gap-2">
            <Label>Type</Label>
            <Select
              value={promoType}
              onValueChange={(v) => setPromoType((v as typeof promoType) ?? "fixed")}
              items={{ fixed: "Flat amount", percent: "Percent" }}
            >
              <SelectTrigger className="w-32">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="fixed">Flat amount</SelectItem>
                <SelectItem value="percent">Percent</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="couponValue">Value *</Label>
            <Input id="couponValue" type="number" min="0.01" step="0.01" required className="w-28" value={value} onChange={(e) => setValue(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="minPurchase">Min purchase (₹)</Label>
            <Input id="minPurchase" type="number" min="0" step="0.01" className="w-32" value={minPurchase} onChange={(e) => setMinPurchase(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="usageLimitTotal">Total usage limit</Label>
            <Input id="usageLimitTotal" type="number" min="1" step="1" className="w-32" placeholder="Unlimited" value={usageLimitTotal} onChange={(e) => setUsageLimitTotal(e.target.value)} />
          </div>
          <Button type="submit" disabled={busy}>
            {busy ? "Creating..." : "Create coupon"}
          </Button>
          {formError && <p className="text-sm text-red-600 w-full">{formError}</p>}
        </form>

        {error && <p className="text-sm text-red-600">{error}</p>}
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Code</TableHead>
              <TableHead>Value</TableHead>
              <TableHead>Min purchase</TableHead>
              <TableHead>Usage</TableHead>
              <TableHead>Status</TableHead>
              <TableHead></TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {coupons.map((c) => (
              <TableRow key={c.coupon_id}>
                <TableCell className="font-mono text-xs font-medium">{c.code}</TableCell>
                <TableCell>{c.promo_type === "percent" ? `${c.value}%` : `₹${c.value}`}</TableCell>
                <TableCell>{c.min_purchase_amount ? `₹${c.min_purchase_amount}` : "—"}</TableCell>
                <TableCell>
                  {c.usage_count}
                  {c.usage_limit_total ? ` / ${c.usage_limit_total}` : ""}
                </TableCell>
                <TableCell>
                  <Badge variant={c.active ? "default" : "secondary"}>{c.active ? "active" : "inactive"}</Badge>
                </TableCell>
                <TableCell>
                  <Button size="sm" variant="outline" onClick={() => toggleActive(c)}>
                    {c.active ? "Deactivate" : "Activate"}
                  </Button>
                </TableCell>
              </TableRow>
            ))}
            {coupons.length === 0 && (
              <TableRow>
                <TableCell colSpan={6} className="text-center text-sm text-zinc-500 py-6">
                  No coupons yet
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  );
}

// ---------------------------------------------------------------------
// Loyalty settings
// ---------------------------------------------------------------------

function LoyaltySettingsSection() {
  const [config, setConfig] = useState<LoyaltyConfig | null>(null);
  const [earnRate, setEarnRate] = useState("");
  const [redeemRate, setRedeemRate] = useState("");
  const [expiryMonths, setExpiryMonths] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api
      .get<LoyaltyConfig>("/api/v1/loyalty/config")
      .then((c) => {
        setConfig(c);
        setEarnRate(c.earn_rupees_per_point);
        setRedeemRate(c.redeem_points_per_rupee);
        setExpiryMonths(String(c.expiry_months));
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load loyalty config"));
  }, []);

  async function save() {
    setBusy(true);
    setError(null);
    try {
      const c = await api.patch<LoyaltyConfig>("/api/v1/loyalty/config", {
        earn_rupees_per_point: Number(earnRate),
        redeem_points_per_rupee: Number(redeemRate),
        expiry_months: Number(expiryMonths),
      });
      setConfig(c);
      toast.success("Loyalty settings saved");
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not save loyalty settings");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Loyalty settings</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <p className="text-xs text-zinc-500">
          A customer&apos;s whole point balance expires together if they go this many months without a new sale (&quot;rolling&quot;
          expiry) — not per-batch. Points earn automatically at checkout; redemption happens from the POS terminal, not here.
        </p>
        <div className="flex gap-4 flex-wrap items-end">
          <div className="flex flex-col gap-2 w-48">
            <Label htmlFor="earnRate">₹ spent per point earned</Label>
            <Input id="earnRate" type="number" min="0.01" step="0.01" value={earnRate} onChange={(e) => setEarnRate(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2 w-48">
            <Label htmlFor="redeemRate">Points per ₹1 redeemed</Label>
            <Input id="redeemRate" type="number" min="0.01" step="0.01" value={redeemRate} onChange={(e) => setRedeemRate(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2 w-48">
            <Label htmlFor="expiryMonths">Expiry (months of inactivity)</Label>
            <Input id="expiryMonths" type="number" min="1" step="1" value={expiryMonths} onChange={(e) => setExpiryMonths(e.target.value)} />
          </div>
          <Button disabled={busy || !config} onClick={save}>
            {busy ? "Saving..." : "Save"}
          </Button>
        </div>
        {error && <p className="text-sm text-red-600">{error}</p>}
      </CardContent>
    </Card>
  );
}
