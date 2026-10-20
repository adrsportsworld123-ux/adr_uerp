"use client";

// Phase 3, sub-area 2's admin surface (erp-core-go's phased_roadmap.md /
// docs/phase0_1_design.md §3.11, §3.49). Full CRUD for promotions and
// coupons: create and edit in a dialog, activate/deactivate from the row,
// and delete only what no sale has used (a used one is deactivated instead,
// so a past sale's discount record keeps its source). Applying a promotion
// or coupon to a live sale stays a POS-terminal action (erp-pos-flutter);
// a customer's loyalty balance is on their own page (/customers/[id]).
import { useEffect, useMemo, useState } from "react";
import { Copy, Loader2, Pencil, Plus, Search, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Branch, Category, Coupon, LoyaltyConfig, Promotion, PromotionConfig, VolumeTier } from "@/lib/types";
import { rupees } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Modal } from "@/components/ui/dialog";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { PageHeader } from "@/components/data/page-header";
import { StatusChip } from "@/components/data/status-chip";
import { DeleteWithDependencies } from "@/components/data/dependency-dialog";
import { ProductPicker, VariantOption } from "@/components/data/product-picker";
import { SearchSelect, categoryOption } from "@/components/data/search-select";
import { DaysOfWeekPicker, describeWhen, fromLocalInput, toLocalInput } from "@/components/pricing/schedule-fields";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

import { currencySymbol } from "@/lib/currency";
export default function PromotionsPage() {
  return (
    <div className="flex max-w-6xl flex-col gap-6">
      <PageHeader title="Promotions & Loyalty" description="Discounts the POS applies automatically, coupon codes, and how loyalty points are earned and redeemed." />
      {/* Jump bar: all three sections stay on the page (nothing hidden behind
          tabs), so this is navigation, not state. */}
      <nav aria-label="Sections" className="sticky top-14 z-20 -mx-1 flex gap-1 rounded-lg border bg-card/95 p-1 backdrop-blur">
        {[
          ["#promotions", "Promotions"],
          ["#coupons", "Coupons"],
          ["#loyalty", "Loyalty"],
        ].map(([href, label]) => (
          <a key={href} href={href} className="rounded-md px-3 py-1.5 text-sm text-muted-foreground hover:bg-muted hover:text-foreground">
            {label}
          </a>
        ))}
      </nav>
      <section id="promotions" className="scroll-mt-28">
        <PromotionsSection />
      </section>
      <section id="coupons" className="scroll-mt-28">
        <CouponsSection />
      </section>
      <section id="loyalty" className="scroll-mt-28">
        <LoyaltySettingsSection />
      </section>
    </div>
  );
}

const selectClass = "h-9 rounded-lg border bg-card px-2 text-sm";
const errorText = (e: unknown, fallback: string) => (e instanceof ApiError || e instanceof Error ? e.message : fallback);

const ACTIVE_OPTIONS: [string, string][] = [
  ["", "All statuses"],
  ["active", "Active"],
  ["inactive", "Inactive"],
];

// A list's search box and status filter, shared by both sections.
function ListControls({
  query,
  setQuery,
  status,
  setStatus,
  label,
  options = ACTIVE_OPTIONS,
}: {
  query: string;
  setQuery: (q: string) => void;
  status: string;
  setStatus: (s: string) => void;
  label: string;
  options?: [string, string][];
}) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      <div className="relative">
        <Search className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" aria-hidden />
        <input
          type="search"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={`Search ${label}`}
          aria-label={`Search ${label}`}
          className="h-9 w-56 rounded-lg border bg-card pr-3 pl-8 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/30"
        />
      </div>
      <select aria-label={`${label} status`} value={status} onChange={(e) => setStatus(e.target.value)} className={selectClass}>
        {options.map(([value, text]) => (
          <option key={value} value={value}>
            {text}
          </option>
        ))}
      </select>
    </div>
  );
}

function RowActions({
  active,
  onEdit,
  onToggle,
  onDelete,
  onCopy,
  busy,
}: {
  active: boolean;
  onEdit: () => void;
  onToggle: () => void;
  onDelete: () => void;
  onCopy?: () => void;
  busy: boolean;
}) {
  return (
    <div className="flex justify-end gap-1.5">
      <Button size="sm" variant="outline" onClick={onEdit} aria-label="Edit">
        <Pencil /> Edit
      </Button>
      {onCopy && (
        <Button size="sm" variant="outline" onClick={onCopy} disabled={busy} aria-label="Copy" title="Copy into a new promotion to edit">
          <Copy /> Copy
        </Button>
      )}
      <Button size="sm" variant="outline" onClick={onToggle} disabled={busy}>
        {active ? "Deactivate" : "Activate"}
      </Button>
      <Button size="sm" variant="ghost" onClick={onDelete} aria-label="Delete" className="text-destructive hover:text-destructive">
        <Trash2 />
      </Button>
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
type PromoType = (typeof PROMO_TYPES)[number]["value"];

const SEGMENTS: Record<string, string> = { any: "Any customer", vip: "VIP", regular: "Regular", new: "New", dormant: "Dormant" };

// A promotion's state (erp-core-go migrations/065): the filter that finds
// what is running now, and what has expired.
// "active" / "inactive" keep their meaning (the switch); the rest filter
// by state.
const STATE_OPTIONS: [string, string][] = [
  ["", "All statuses"],
  ["active", "Active"],
  ["inactive", "Inactive"],
  ["current", "Not expired"],
  ["running", "Running now"],
  ["scheduled", "Scheduled"],
  ["expired", "Expired"],
];
const STATE_LABEL: Record<Promotion["state"], string> = { running: "Active", scheduled: "Scheduled", expired: "Expired", inactive: "Inactive" };
const STATE_TONE = { running: "ok", scheduled: "info", expired: "warn", inactive: "muted" } as const;

function offerSummary(p: Promotion): string {
  const c = p.config;
  switch (p.promo_type) {
    case "percent":
      return `${c.value_percent}% off${p.apply_on === "mrp" ? " MRP" : ""}`;
    case "fixed":
      return `${rupees(c.value_amount ?? 0)} off${p.apply_on === "mrp" ? " MRP" : ""}`;
    case "bogo":
      return `Buy ${c.buy_qty} get ${c.get_qty} at ${c.get_discount_pct}% off`;
    case "volume":
      return (c.tiers ?? []).map((t) => `${t.min_qty}${t.max_qty ? `–${t.max_qty}` : "+"}: ${t.discount_pct}%`).join(", ");
    case "min_value":
      return `${c.discount_pct ? `${c.discount_pct}%` : rupees(c.discount_amount ?? 0)} off over ${rupees(c.min_purchase_amount ?? 0)}`;
  }
}

function scopeSummary(p: Promotion): string {
  if (p.application_level === "product") return p.product_name ?? "One product";
  if (p.application_level === "category") return `Category: ${p.category_name ?? "—"}`;
  return "Whole order";
}

function PromotionsSection() {
  const [promotions, setPromotions] = useState<Promotion[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [status, setStatus] = useState("");
  const [editing, setEditing] = useState<Promotion | "new" | null>(null);
  const [deleting, setDeleting] = useState<Promotion | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);

  function load() {
    api
      .get<{ promotions: Promotion[] }>("/api/v1/promotions")
      .then((d) => {
        setPromotions(d.promotions);
        setError(null);
      })
      .catch((e) => setError(e.message))
      .finally(() => setLoading(false));
  }
  useEffect(load, []);

  async function setActive(p: Promotion, active: boolean) {
    setBusyId(p.promotion_id);
    try {
      await api.patch(`/api/v1/promotions/${p.promotion_id}`, { active });
      toast.success(active ? "Promotion activated" : "Promotion deactivated");
      load();
    } catch (e) {
      toast.error(errorText(e, "Could not update promotion"));
    } finally {
      setBusyId(null);
    }
  }

  const shown = useMemo(() => {
    const q = query.trim().toLowerCase();
    return promotions.filter(
      (p) =>
        (!status ||
          (status === "active" ? p.active : status === "inactive" ? !p.active : status === "current" ? p.state !== "expired" : p.state === status)) &&
        (!q || `${p.name} ${scopeSummary(p)} ${offerSummary(p)}`.toLowerCase().includes(q)),
    );
  }, [promotions, query, status]);

  // Copy: the API makes a switched-off copy, which opens to be edited.
  async function copy(p: Promotion) {
    setBusyId(p.promotion_id);
    try {
      const c = await api.post<Promotion>(`/api/v1/promotions/${p.promotion_id}/duplicate`, {});
      toast.success("Copied — edit it, then switch it on");
      load();
      setEditing(c);
    } catch (e) {
      toast.error(errorText(e, "Could not copy the promotion"));
    } finally {
      setBusyId(null);
    }
  }

  return (
    <Card>
      <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3">
        <CardTitle className="text-base">
          Promotions <span className="font-normal text-muted-foreground">({promotions.length})</span>
        </CardTitle>
        <div className="flex flex-wrap items-center gap-2">
          <ListControls query={query} setQuery={setQuery} status={status} setStatus={setStatus} label="promotions" options={STATE_OPTIONS} />
          <Button onClick={() => setEditing("new")}>
            <Plus /> New promotion
          </Button>
        </div>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {error && <p className="text-sm text-destructive">{error}</p>}
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Offer</TableHead>
              <TableHead>Applies to</TableHead>
              <TableHead>When</TableHead>
              <TableHead className="text-right">Used</TableHead>
              <TableHead>Status</TableHead>
              <TableHead className="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {shown.map((p) => (
              <TableRow key={p.promotion_id}>
                <TableCell className="font-medium">
                  {p.name}
                  <div className="text-xs font-normal text-muted-foreground">
                    {p.stacking === "stackable" ? "Stackable" : "Exclusive"}
                    {p.target_segment && ` · ${SEGMENTS[p.target_segment]} customers`}
                  </div>
                </TableCell>
                <TableCell className="text-sm">{offerSummary(p)}</TableCell>
                <TableCell className="text-sm">{scopeSummary(p)}</TableCell>
                <TableCell className="text-xs">{describeWhen(p)}</TableCell>
                <TableCell className="tabular text-right">{p.times_used}</TableCell>
                <TableCell>
                  <StatusChip status={p.state} tone={STATE_TONE[p.state]} label={STATE_LABEL[p.state]} />
                </TableCell>
                <TableCell>
                  <RowActions
                    active={p.active}
                    busy={busyId === p.promotion_id}
                    onEdit={() => setEditing(p)}
                    onToggle={() => setActive(p, !p.active)}
                    onDelete={() => setDeleting(p)}
                    onCopy={() => copy(p)}
                  />
                </TableCell>
              </TableRow>
            ))}
            {!loading && shown.length === 0 && (
              <TableRow>
                <TableCell colSpan={7} className="py-6 text-center text-sm text-muted-foreground">
                  {promotions.length ? "No promotion matches these filters" : "No promotions yet — create one to discount sales automatically at the till"}
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </CardContent>

      {editing !== null && (
        <PromotionDialog
          key={editing === "new" ? "new" : editing.promotion_id}
          promotion={editing === "new" ? null : editing}
          onClose={() => setEditing(null)}
          onSaved={load}
        />
      )}
      {deleting && (
        <DeleteWithDependencies
          open
          onOpenChange={(o) => !o && setDeleting(null)}
          entity="promotion"
          id={deleting.promotion_id}
          name={deleting.name}
          noun="promotion"
          deletePath={`/api/v1/promotions/${deleting.promotion_id}`}
          canDeactivate={deleting.active}
          onDeactivate={() => setActive(deleting, false)}
          onDeleted={load}
        />
      )}
    </Card>
  );
}

function PromotionDialog({ promotion, onClose, onSaved }: { promotion: Promotion | null; onClose: () => void; onSaved: () => void }) {
  const p = promotion;
  const c = p?.config ?? {};
  const [name, setName] = useState(p?.name ?? "");
  const [promoType, setPromoType] = useState<PromoType>(p?.promo_type ?? "percent");
  const [applicationLevel, setApplicationLevel] = useState<"order" | "product" | "category">(p?.application_level ?? "order");
  const [product, setProduct] = useState<VariantOption | null>(
    p?.product_id
      ? { variantId: p.product_id, productId: p.product_id, productName: p.product_name ?? "", sku: "", sellingPrice: "", label: p.product_name ?? "Product", trackBatch: false }
      : null,
  );
  const [categoryId, setCategoryId] = useState(p?.category_id ?? "");
  const [categories, setCategories] = useState<Category[]>([]);
  const [targetSegment, setTargetSegment] = useState<string>(p?.target_segment ?? "");
  const [stacking, setStacking] = useState<"exclusive" | "stackable">(p?.stacking ?? "exclusive");
  const [applyOn, setApplyOn] = useState<"selling_price" | "mrp">(p?.apply_on ?? "selling_price");
  const [startsAt, setStartsAt] = useState(toLocalInput(p?.starts_at));
  const [endsAt, setEndsAt] = useState(toLocalInput(p?.ends_at));
  const [days, setDays] = useState<number[]>(p?.days_of_week ?? []);
  const [timeStart, setTimeStart] = useState(p?.time_start ?? "");
  const [timeEnd, setTimeEnd] = useState(p?.time_end ?? "");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // One field per promo_type, assembled into PromotionConfig on save —
  // mirrors the Go side's single config shape (internal/promotions.promoConfig).
  const str = (n: number | undefined) => (n === undefined || n === null ? "" : String(n));
  const [valuePercent, setValuePercent] = useState(str(c.value_percent));
  const [valueAmount, setValueAmount] = useState(str(c.value_amount));
  const [buyQty, setBuyQty] = useState(str(c.buy_qty));
  const [getQty, setGetQty] = useState(str(c.get_qty));
  const [getDiscountPct, setGetDiscountPct] = useState(str(c.get_discount_pct ?? (p ? undefined : 100)));
  const [tiers, setTiers] = useState<VolumeTier[]>(c.tiers?.length ? c.tiers : [{ min_qty: 3, max_qty: 5, discount_pct: 5 }]);
  const [minPurchaseAmount, setMinPurchaseAmount] = useState(str(c.min_purchase_amount));
  const [minValueMode, setMinValueMode] = useState<"discount_amount" | "discount_pct">(c.discount_pct ? "discount_pct" : "discount_amount");
  const [minValueAmount, setMinValueAmount] = useState(str(c.discount_pct ?? c.discount_amount));

  useEffect(() => {
    api
      .get<{ categories: Category[] }>("/api/v1/categories?status=active&sort=path")
      .then((d) => setCategories(d.categories))
      .catch(() => setCategories([]));
  }, []);
  const categoryOptions = useMemo(() => {
    const opts = categories.map(categoryOption);
    // An edited promotion keeps showing a category that has since been deactivated.
    if (p?.category_id && !opts.some((o) => o.value === p.category_id)) opts.unshift({ value: p.category_id, label: `${p.category_name ?? "Category"} (inactive)` });
    return opts;
  }, [categories, p]);

  // bogo/volume are inherently product/category-scoped (the API refuses
  // application_level=order for them) — steer the level picker in the same
  // event that changes the type.
  function handlePromoTypeChange(v: string) {
    const next = (v as PromoType) || "percent";
    setPromoType(next);
    if ((next === "bogo" || next === "volume") && applicationLevel === "order") setApplicationLevel("product");
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

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    if (applicationLevel === "product" && !product) return setError("Choose the product this promotion applies to.");
    if (applicationLevel === "category" && !categoryId) return setError("Choose the category this promotion applies to.");
    if ((timeStart === "") !== (timeEnd === "")) return setError("Give both a start and an end time, or neither.");
    setBusy(true);
    const body = {
      name: name.trim(),
      promo_type: promoType,
      application_level: applicationLevel,
      product_id: applicationLevel === "product" ? (product?.productId ?? "") : "",
      category_id: applicationLevel === "category" ? categoryId : "",
      target_segment: targetSegment,
      config: buildConfig(),
      stacking,
      apply_on: applyOn,
      starts_at: fromLocalInput(startsAt),
      ends_at: fromLocalInput(endsAt),
      days_of_week: days,
      time_start: timeStart,
      time_end: timeEnd,
    };
    try {
      if (p) await api.patch(`/api/v1/promotions/${p.promotion_id}`, body);
      else await api.post("/api/v1/promotions", body);
      toast.success(p ? "Promotion updated" : "Promotion created");
      onSaved();
      onClose();
    } catch (err) {
      setError(errorText(err, p ? "Could not update promotion" : "Could not create promotion"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={p ? `Edit “${p.name}”` : "New promotion"}
      description={p?.times_used ? `Used on ${p.times_used} sale(s). Changes apply to future sales only.` : "Applied automatically at the till when a sale qualifies."}
      width={720}
      footer={
        <>
          <Button variant="outline" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button type="submit" form="promotion-form" disabled={busy}>
            {busy && <Loader2 className="animate-spin" aria-hidden />}
            {p ? "Save changes" : "Create promotion"}
          </Button>
        </>
      }
    >
      <form id="promotion-form" onSubmit={save} className="flex flex-col gap-4">
        <div className="grid gap-4 sm:grid-cols-2">
          <div className="flex flex-col gap-2">
            <Label htmlFor="promoName">Name *</Label>
            <Input id="promoName" required autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="Diwali 10% off" />
          </div>
          <div className="flex flex-col gap-2">
            <Label>Type</Label>
            <Select value={promoType} onValueChange={(v) => handlePromoTypeChange(v ?? "percent")} items={Object.fromEntries(PROMO_TYPES.map((t) => [t.value, t.label]))}>
              <SelectTrigger aria-label="Promotion type">
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

        <div className="grid gap-4 sm:grid-cols-3">
          <div className="flex flex-col gap-2">
            <Label>Applies to</Label>
            <Select
              value={applicationLevel}
              onValueChange={(v) => setApplicationLevel((v as typeof applicationLevel) ?? "order")}
              items={{ order: "Whole order", product: "One product", category: "One category" }}
            >
              <SelectTrigger aria-label="Applies to">
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
              <Label htmlFor="promoProduct">Product *</Label>
              <ProductPicker id="promoProduct" ariaLabel="Product" mode="product" value={product} onChange={setProduct} placeholder="Search a product" />
            </div>
          )}
          {applicationLevel === "category" && (
            <div className="flex flex-col gap-2">
              <Label htmlFor="promoCategory">Category *</Label>
              <SearchSelect id="promoCategory" ariaLabel="Category" value={categoryId} onChange={setCategoryId} options={categoryOptions} placeholder="Search a category" />
              <p className="text-xs text-muted-foreground">Includes its sub-categories.</p>
            </div>
          )}
          <div className="flex flex-col gap-2">
            <Label>Customer segment</Label>
            <Select value={targetSegment || "any"} onValueChange={(v) => setTargetSegment(v === "any" ? "" : (v ?? ""))} items={SEGMENTS}>
              <SelectTrigger aria-label="Customer segment">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {Object.entries(SEGMENTS).map(([k, v]) => (
                  <SelectItem key={k} value={k}>
                    {v}
                  </SelectItem>
                ))}
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

        <div className="flex flex-col gap-2">
          <Label htmlFor="promoApplyOn">Apply on</Label>
          <select id="promoApplyOn" value={applyOn} onChange={(e) => setApplyOn(e.target.value as typeof applyOn)} className={`${selectClass} w-64`}>
            <option value="selling_price">Selling price</option>
            <option value="mrp">MRP</option>
          </select>
          <p className="text-xs text-muted-foreground">
            {applyOn === "mrp"
              ? `The promotional price is the offer off the MRP, and applies only where it is below the selling price. MRP ${rupees(1000)}, selling price ${rupees(900)}, 20% off: the customer pays ${rupees(800)}.`
              : `The offer is taken off the selling price. Selling price ${rupees(900)}, 20% off: the customer pays ${rupees(720)}.`}
          </p>
        </div>

        <div className="grid gap-4 sm:grid-cols-3">
          <div className="flex flex-col gap-2">
            <Label>Stacking</Label>
            <Select value={stacking} onValueChange={(v) => setStacking((v as typeof stacking) ?? "exclusive")} items={{ exclusive: "Exclusive", stackable: "Stackable" }}>
              <SelectTrigger aria-label="Stacking">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="exclusive">Exclusive</SelectItem>
                <SelectItem value="stackable">Stackable</SelectItem>
              </SelectContent>
            </Select>
            <p className="text-xs text-muted-foreground">
              Each item gets the offer that takes the most off it. Stackable offers combine with each other; exclusive ones never combine.
            </p>
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="promoStarts">Starts</Label>
            <Input id="promoStarts" type="datetime-local" value={startsAt} onChange={(e) => setStartsAt(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="promoEnds">Ends</Label>
            <Input id="promoEnds" type="datetime-local" value={endsAt} onChange={(e) => setEndsAt(e.target.value)} />
          </div>
        </div>

        <div className="grid gap-4 sm:grid-cols-[1fr_auto_auto] sm:items-end">
          <DaysOfWeekPicker value={days} onChange={setDays} />
          <div className="flex flex-col gap-2">
            <Label htmlFor="promoTimeStart">From (time)</Label>
            <Input id="promoTimeStart" type="time" value={timeStart} onChange={(e) => setTimeStart(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="promoTimeEnd">Until (time)</Label>
            <Input id="promoTimeEnd" type="time" value={timeEnd} onChange={(e) => setTimeEnd(e.target.value)} />
          </div>
        </div>
        <p className="text-xs text-muted-foreground">Leave dates, days and times empty for a promotion that always runs while it&rsquo;s active.</p>

        {error && (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        )}
      </form>
    </Modal>
  );
}

interface PromoTypeFieldsProps {
  promoType: PromoType;
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
      <div className="flex w-40 flex-col gap-2">
        <Label htmlFor="valuePercent">Percent off *</Label>
        <Input id="valuePercent" type="number" min="0.01" max="100" step="0.01" required value={p.valuePercent} onChange={(e) => p.setValuePercent(e.target.value)} />
      </div>
    );
  }
  if (p.promoType === "fixed") {
    return (
      <div className="flex w-40 flex-col gap-2">
        <Label htmlFor="valueAmount">Amount off ({currencySymbol()}) *</Label>
        <Input id="valueAmount" type="number" min="0.01" step="0.01" required value={p.valueAmount} onChange={(e) => p.setValueAmount(e.target.value)} />
      </div>
    );
  }
  if (p.promoType === "bogo") {
    return (
      <div className="flex flex-wrap gap-4">
        <div className="flex w-28 flex-col gap-2">
          <Label htmlFor="buyQty">Buy qty *</Label>
          <Input id="buyQty" type="number" min="1" step="1" required value={p.buyQty} onChange={(e) => p.setBuyQty(e.target.value)} />
        </div>
        <div className="flex w-28 flex-col gap-2">
          <Label htmlFor="getQty">Get qty *</Label>
          <Input id="getQty" type="number" min="1" step="1" required value={p.getQty} onChange={(e) => p.setGetQty(e.target.value)} />
        </div>
        <div className="flex w-44 flex-col gap-2">
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
      <fieldset className="flex flex-col gap-2">
        <legend className="mb-2 text-sm font-medium">Quantity tiers *</legend>
        {p.tiers.map((t, i) => (
          <div key={i} className="flex flex-wrap items-end gap-2">
            <label className="flex flex-col gap-1 text-xs text-muted-foreground">
              Min qty
              <Input type="number" min="1" step="1" className="w-24" value={t.min_qty} onChange={(e) => updateTier(i, "min_qty", Number(e.target.value))} />
            </label>
            <label className="flex flex-col gap-1 text-xs text-muted-foreground">
              Max qty (0 = no limit)
              <Input type="number" min="0" step="1" className="w-32" value={t.max_qty} onChange={(e) => updateTier(i, "max_qty", Number(e.target.value))} />
            </label>
            <label className="flex flex-col gap-1 text-xs text-muted-foreground">
              Discount %
              <Input type="number" min="0.01" max="100" step="0.01" className="w-24" value={t.discount_pct} onChange={(e) => updateTier(i, "discount_pct", Number(e.target.value))} />
            </label>
            {p.tiers.length > 1 && (
              <Button type="button" size="sm" variant="ghost" onClick={() => p.setTiers(p.tiers.filter((_, idx) => idx !== i))}>
                Remove
              </Button>
            )}
          </div>
        ))}
        <Button type="button" size="sm" variant="outline" className="w-fit" onClick={() => p.setTiers([...p.tiers, { min_qty: 1, max_qty: 0, discount_pct: 5 }])}>
          Add tier
        </Button>
      </fieldset>
    );
  }
  // min_value
  return (
    <div className="flex flex-wrap items-end gap-4">
      <div className="flex w-40 flex-col gap-2">
        <Label htmlFor="minPurchaseAmount">Min purchase ({currencySymbol()}) *</Label>
        <Input id="minPurchaseAmount" type="number" min="0.01" step="0.01" required value={p.minPurchaseAmount} onChange={(e) => p.setMinPurchaseAmount(e.target.value)} />
      </div>
      <div className="flex w-44 flex-col gap-2">
        <Label>Discount as</Label>
        <Select
          value={p.minValueMode}
          onValueChange={(v) => p.setMinValueMode((v as typeof p.minValueMode) ?? "discount_amount")}
          items={{ discount_amount: `Flat amount (${currencySymbol()})`, discount_pct: "Percent (%)" }}
        >
          <SelectTrigger aria-label="Discount as">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="discount_amount">Flat amount ({currencySymbol()})</SelectItem>
            <SelectItem value="discount_pct">Percent (%)</SelectItem>
          </SelectContent>
        </Select>
      </div>
      <div className="flex w-32 flex-col gap-2">
        <Label htmlFor="minValueAmount">{p.minValueMode === "discount_amount" ? "Amount off *" : "Percent off *"}</Label>
        <Input id="minValueAmount" type="number" min="0.01" step="0.01" required value={p.minValueAmount} onChange={(e) => p.setMinValueAmount(e.target.value)} />
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------
// Coupons
// ---------------------------------------------------------------------

const CHANNELS: [string, string][] = [
  ["pos", "Till"],
  ["online", "Online"],
  ["backoffice", "Back office"],
];

function couponValidity(c: Coupon): string {
  if (!c.valid_from && !c.valid_until) return "No end date";
  return describeWhen({ starts_at: c.valid_from, ends_at: c.valid_until });
}

function CouponsSection() {
  const [coupons, setCoupons] = useState<Coupon[]>([]);
  const [branches, setBranches] = useState<Branch[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [status, setStatus] = useState("");
  const [editing, setEditing] = useState<Coupon | "new" | null>(null);
  const [deleting, setDeleting] = useState<Coupon | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);

  function load() {
    api
      .get<{ coupons: Coupon[] }>("/api/v1/coupons")
      .then((d) => {
        setCoupons(d.coupons);
        setError(null);
      })
      .catch((e) => setError(e.message))
      .finally(() => setLoading(false));
  }
  useEffect(load, []);
  useEffect(() => {
    api.get<{ branches: Branch[] }>("/api/v1/branches?scope=mine").then((d) => setBranches(d.branches)).catch(() => {});
  }, []);

  async function setActive(c: Coupon, active: boolean) {
    setBusyId(c.coupon_id);
    try {
      await api.patch(`/api/v1/coupons/${c.coupon_id}`, { active });
      toast.success(active ? "Coupon activated" : "Coupon deactivated");
      load();
    } catch (e) {
      toast.error(errorText(e, "Could not update coupon"));
    } finally {
      setBusyId(null);
    }
  }

  const branchName = (id: string | null) => (id ? (branches.find((b) => b.branch_id === id)?.name ?? "One branch") : "All branches");
  const shown = useMemo(() => {
    const q = query.trim().toLowerCase();
    return coupons.filter((c) => (!status || (status === "active") === c.active) && (!q || c.code.toLowerCase().includes(q)));
  }, [coupons, query, status]);

  return (
    <Card>
      <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3">
        <CardTitle className="text-base">
          Coupons <span className="font-normal text-muted-foreground">({coupons.length})</span>
        </CardTitle>
        <div className="flex flex-wrap items-center gap-2">
          <ListControls query={query} setQuery={setQuery} status={status} setStatus={setStatus} label="coupons" />
          <Button onClick={() => setEditing("new")}>
            <Plus /> New coupon
          </Button>
        </div>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {error && <p className="text-sm text-destructive">{error}</p>}
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Code</TableHead>
              <TableHead>Value</TableHead>
              <TableHead>Min purchase</TableHead>
              <TableHead>Valid</TableHead>
              <TableHead>Where</TableHead>
              <TableHead className="text-right">Used</TableHead>
              <TableHead>Status</TableHead>
              <TableHead className="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {shown.map((c) => (
              <TableRow key={c.coupon_id}>
                <TableCell className="font-mono text-xs font-medium">{c.code}</TableCell>
                <TableCell>{c.promo_type === "percent" ? `${Number(c.value)}%` : rupees(c.value)}</TableCell>
                <TableCell>{c.min_purchase_amount ? rupees(c.min_purchase_amount) : "—"}</TableCell>
                <TableCell className="text-xs">{couponValidity(c)}</TableCell>
                <TableCell className="text-xs">
                  {branchName(c.branch_id)}
                  {c.channels?.length ? <div className="text-muted-foreground">{c.channels.map((x) => CHANNELS.find(([k]) => k === x)?.[1] ?? x).join(", ")}</div> : null}
                </TableCell>
                <TableCell className="tabular text-right">
                  {c.usage_count}
                  {c.usage_limit_total ? ` / ${c.usage_limit_total}` : ""}
                </TableCell>
                <TableCell>
                  <StatusChip status={c.active ? "active" : "inactive"} />
                </TableCell>
                <TableCell>
                  <RowActions
                    active={c.active}
                    busy={busyId === c.coupon_id}
                    onEdit={() => setEditing(c)}
                    onToggle={() => setActive(c, !c.active)}
                    onDelete={() => setDeleting(c)}
                  />
                </TableCell>
              </TableRow>
            ))}
            {!loading && shown.length === 0 && (
              <TableRow>
                <TableCell colSpan={8} className="py-6 text-center text-sm text-muted-foreground">
                  {coupons.length ? "No coupon matches these filters" : "No coupons yet"}
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </CardContent>

      {editing !== null && (
        <CouponDialog
          key={editing === "new" ? "new" : editing.coupon_id}
          coupon={editing === "new" ? null : editing}
          branches={branches}
          onClose={() => setEditing(null)}
          onSaved={load}
        />
      )}
      {deleting && (
        <DeleteWithDependencies
          open
          onOpenChange={(o) => !o && setDeleting(null)}
          entity="coupon"
          id={deleting.coupon_id}
          name={deleting.code}
          noun="coupon"
          deletePath={`/api/v1/coupons/${deleting.coupon_id}`}
          canDeactivate={deleting.active}
          onDeactivate={() => setActive(deleting, false)}
          onDeleted={load}
        />
      )}
    </Card>
  );
}

function CouponDialog({ coupon, branches, onClose, onSaved }: { coupon: Coupon | null; branches: Branch[]; onClose: () => void; onSaved: () => void }) {
  const c = coupon;
  const [code, setCode] = useState(c?.code ?? "");
  const [promoType, setPromoType] = useState<"percent" | "fixed">(c?.promo_type ?? "fixed");
  const [value, setValue] = useState(c ? String(Number(c.value)) : "");
  const [minPurchase, setMinPurchase] = useState(c?.min_purchase_amount ? String(Number(c.min_purchase_amount)) : "");
  const [limitTotal, setLimitTotal] = useState(c?.usage_limit_total ? String(c.usage_limit_total) : "");
  const [limitPerCustomer, setLimitPerCustomer] = useState(c?.usage_limit_per_customer ? String(c.usage_limit_per_customer) : "");
  const [validFrom, setValidFrom] = useState(toLocalInput(c?.valid_from));
  const [validUntil, setValidUntil] = useState(toLocalInput(c?.valid_until));
  const [channels, setChannels] = useState<string[]>(c?.channels ?? []);
  const [branchId, setBranchId] = useState(c?.branch_id ?? "");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    const body: Record<string, unknown> = {
      code: code.trim(),
      promo_type: promoType,
      value: Number(value),
      min_purchase_amount: Number(minPurchase) || 0,
      valid_from: fromLocalInput(validFrom),
      valid_until: fromLocalInput(validUntil),
      channels,
      branch_id: branchId,
    };
    if (limitTotal) body.usage_limit_total = Number(limitTotal);
    else if (c) body.clear_usage_limit_total = true;
    if (limitPerCustomer) body.usage_limit_per_customer = Number(limitPerCustomer);
    else if (c) body.clear_usage_limit_per_customer = true;
    try {
      if (c) await api.patch(`/api/v1/coupons/${c.coupon_id}`, body);
      else await api.post("/api/v1/coupons", body);
      toast.success(c ? "Coupon updated" : "Coupon created");
      onSaved();
      onClose();
    } catch (err) {
      setError(errorText(err, c ? "Could not update coupon" : "Could not create coupon"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={c ? `Edit coupon ${c.code}` : "New coupon"}
      description={c?.usage_count ? `Redeemed ${c.usage_count} time(s). Changes apply to future sales only.` : "The cashier types this code at the till."}
      width={640}
      footer={
        <>
          <Button variant="outline" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button type="submit" form="coupon-form" disabled={busy}>
            {busy && <Loader2 className="animate-spin" aria-hidden />}
            {c ? "Save changes" : "Create coupon"}
          </Button>
        </>
      }
    >
      <form id="coupon-form" onSubmit={save} className="flex flex-col gap-4">
        <div className="grid gap-4 sm:grid-cols-3">
          <div className="flex flex-col gap-2">
            <Label htmlFor="code">Code *</Label>
            <Input id="code" required autoFocus className="uppercase" value={code} onChange={(e) => setCode(e.target.value.toUpperCase())} placeholder="DIWALI50" />
          </div>
          <div className="flex flex-col gap-2">
            <Label>Type</Label>
            <Select value={promoType} onValueChange={(v) => setPromoType((v as typeof promoType) ?? "fixed")} items={{ fixed: "Flat amount", percent: "Percent" }}>
              <SelectTrigger aria-label="Coupon type">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="fixed">Flat amount</SelectItem>
                <SelectItem value="percent">Percent</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="couponValue">{promoType === "percent" ? "Percent off *" : `Amount off (${currencySymbol()}) *`}</Label>
            <Input id="couponValue" type="number" min="0.01" max={promoType === "percent" ? 100 : undefined} step="0.01" required value={value} onChange={(e) => setValue(e.target.value)} />
          </div>
        </div>
        <div className="grid gap-4 sm:grid-cols-3">
          <div className="flex flex-col gap-2">
            <Label htmlFor="minPurchase">Min purchase ({currencySymbol()})</Label>
            <Input id="minPurchase" type="number" min="0" step="0.01" value={minPurchase} onChange={(e) => setMinPurchase(e.target.value)} placeholder="None" />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="usageLimitTotal">Total uses</Label>
            <Input id="usageLimitTotal" type="number" min="1" step="1" placeholder="Unlimited" value={limitTotal} onChange={(e) => setLimitTotal(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="usageLimitPerCustomer">Uses per customer</Label>
            <Input id="usageLimitPerCustomer" type="number" min="1" step="1" placeholder="Unlimited" value={limitPerCustomer} onChange={(e) => setLimitPerCustomer(e.target.value)} />
          </div>
        </div>
        <div className="grid gap-4 sm:grid-cols-3">
          <div className="flex flex-col gap-2">
            <Label htmlFor="validFrom">Valid from</Label>
            <Input id="validFrom" type="datetime-local" value={validFrom} onChange={(e) => setValidFrom(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="validUntil">Valid until</Label>
            <Input id="validUntil" type="datetime-local" value={validUntil} onChange={(e) => setValidUntil(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="couponBranch">Branch</Label>
            <SearchSelect
              id="couponBranch"
              ariaLabel="Branch"
              value={branchId}
              onChange={setBranchId}
              noneLabel="All branches"
              placeholder="All branches"
              options={branches.map((b) => ({ value: b.branch_id, label: b.name }))}
            />
          </div>
        </div>
        <fieldset className="flex flex-col gap-2">
          <legend className="mb-2 text-sm font-medium">Where it can be used (none = everywhere)</legend>
          <div className="flex flex-wrap gap-4">
            {CHANNELS.map(([k, label]) => (
              <label key={k} className="flex items-center gap-2 text-sm">
                <input type="checkbox" checked={channels.includes(k)} onChange={(e) => setChannels(e.target.checked ? [...channels, k] : channels.filter((x) => x !== k))} />
                {label}
              </label>
            ))}
          </div>
        </fieldset>
        {error && (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        )}
      </form>
    </Modal>
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
        <p className="text-xs text-muted-foreground">
          A customer&apos;s whole point balance expires together if they go this many months without a new sale (&quot;rolling&quot;
          expiry) — not per-batch. Points earn automatically at checkout; redemption happens from the POS terminal, not here.
        </p>
        <div className="flex flex-wrap items-end gap-4">
          <div className="flex w-48 flex-col gap-2">
            <Label htmlFor="earnRate">{currencySymbol()} spent per point earned</Label>
            <Input id="earnRate" type="number" min="0.01" step="0.01" value={earnRate} onChange={(e) => setEarnRate(e.target.value)} />
          </div>
          <div className="flex w-48 flex-col gap-2">
            <Label htmlFor="redeemRate">Points per {currencySymbol()}1 redeemed</Label>
            <Input id="redeemRate" type="number" min="0.01" step="0.01" value={redeemRate} onChange={(e) => setRedeemRate(e.target.value)} />
          </div>
          <div className="flex w-48 flex-col gap-2">
            <Label htmlFor="expiryMonths">Expiry (months of inactivity)</Label>
            <Input id="expiryMonths" type="number" min="1" step="1" value={expiryMonths} onChange={(e) => setExpiryMonths(e.target.value)} />
          </div>
          <Button disabled={busy || !config} onClick={save}>
            {busy ? "Saving..." : "Save"}
          </Button>
        </div>
        {error && <p className="text-sm text-destructive">{error}</p>}
      </CardContent>
    </Card>
  );
}
