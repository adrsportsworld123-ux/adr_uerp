"use client";

// Price Suggestions (erp-core-go §3.83, internal/pricing/suggest.go): a
// suggested selling price from each item's cost and MRP. Plan feature
// price_suggestion (the shell shows the plan preview without it). Only the
// Merchant Admin (pricing.suggestion.manage) switches it on and sets the
// rules; anyone pricing sees the suggestions on Pricing and New product,
// and pricing.manage applies them in bulk after a preview. Hold and Check
// items are never applied.
import { FormEvent, useCallback, useEffect, useState } from "react";
import { Loader2, Pencil, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import type { CatalogBrand, Category, PriceSuggestion, SuggestionMethod, SuggestionRule, SuggestionRounding } from "@/lib/types";
import { usePermissions } from "@/lib/permissions";
import { rupees } from "@/lib/format";
import { currencySymbol } from "@/lib/currency";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { AlertModal, Modal } from "@/components/ui/dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { PageHeader } from "@/components/data/page-header";
import { Field, FilterBar, Panel, selectClass } from "@/components/data/panel";
import { StatusChip } from "@/components/data/status-chip";
import { SearchSelect, SearchOption, categoryOption } from "@/components/data/search-select";
import { MultiVariantPick, PickedRef } from "@/components/data/multi-pick";
import { ROUNDINGS, SuggestionChip, fetchSuggestions, roundingLabel, useSuggestionSettings } from "@/components/pricing/suggestions";

const errorText = (e: unknown, fallback: string) => (e instanceof ApiError ? e.message : fallback);

function methodText(r: SuggestionRule) {
  const cost = r.cost_pct == null ? "" : r.cost_type === "margin" ? `${r.cost_pct}% margin on cost` : `cost + ${r.cost_pct}%`;
  const mrp = r.mrp_discount_pct == null ? "" : `MRP − ${r.mrp_discount_pct}%`;
  switch (r.method) {
    case "cost":
      return cost;
    case "mrp":
      return mrp;
    case "lower":
      return `the lower of ${cost} and ${mrp}`;
    default:
      return `the higher of ${cost} and ${mrp}`;
  }
}

function scopeText(r: SuggestionRule) {
  if (r.category && r.brand) return `${r.category} · ${r.brand}`;
  if (r.category) return `${r.category} (and under it)`;
  if (r.brand) return `Brand: ${r.brand}`;
  return "Every product";
}

function useCatalog() {
  const [categories, setCategories] = useState<SearchOption[]>([]);
  const [brands, setBrands] = useState<SearchOption[]>([]);
  useEffect(() => {
    api.get<{ categories: Category[] }>("/api/v1/categories?status=active&sort=path").then((d) => setCategories(d.categories.map(categoryOption))).catch(() => {});
    api.get<{ brands: CatalogBrand[] }>("/api/v1/brands?status=active").then((d) => setBrands(d.brands.map((b) => ({ value: b.brand_id, label: b.name })))).catch(() => {});
  }, []);
  return { categories, brands };
}

export default function PriceSuggestionsPage() {
  const { settings, reload } = useSuggestionSettings();
  const { can } = usePermissions();
  const catalog = useCatalog();
  const [rules, setRules] = useState<SuggestionRule[]>([]);
  const [editing, setEditing] = useState<SuggestionRule | "new" | null>(null);
  const [deleting, setDeleting] = useState<SuggestionRule | null>(null);
  const [busy, setBusy] = useState(false);
  const manage = !!settings?.can_manage;

  const loadRules = useCallback(() => {
    api
      .get<{ rules: SuggestionRule[] }>("/api/v1/pricing/suggestion-rules")
      .then((d) => setRules(d.rules))
      .catch((e) => toast.error(errorText(e, "Could not load the rules")));
  }, []);
  useEffect(loadRules, [loadRules]);

  async function save(body: { enabled?: boolean; cost_includes_tax?: boolean }) {
    setBusy(true);
    try {
      await api.put("/api/v1/pricing/suggestion-settings", body);
      toast.success(body.enabled === undefined ? "Saved" : body.enabled ? "Price suggestions are on" : "Price suggestions are off");
      reload();
      loadRules();
    } catch (e) {
      toast.error(errorText(e, "Could not save"));
    } finally {
      setBusy(false);
    }
  }

  async function setActive(r: SuggestionRule, active: boolean) {
    try {
      await api.patch(`/api/v1/pricing/suggestion-rules/${r.rule_id}`, { active });
      loadRules();
    } catch (e) {
      toast.error(errorText(e, "Could not change the rule"));
    }
  }

  return (
    <div className="flex max-w-5xl flex-col gap-6">
      <PageHeader
        title="Price Suggestions"
        description="A suggested selling price from each item's cost and MRP. It fills in the price on Pricing and New product; you still decide and save it."
        actions={
          manage && (
            <Button onClick={() => setEditing("new")}>
              <Plus aria-hidden /> New rule
            </Button>
          )
        }
      />
      <Panel title="Settings" description={manage ? "Only the Merchant Admin changes these." : "Set by the Merchant Admin."}>
        {!settings ? (
          <p className="text-sm text-muted-foreground">Loading…</p>
        ) : (
          <div className="flex flex-col gap-3 text-sm">
            <label className="flex items-center gap-2">
              <input
                id="sg-enabled"
                type="checkbox"
                checked={settings.enabled}
                disabled={!manage || busy}
                onChange={(e) => save({ enabled: e.target.checked })}
              />
              Suggest selling prices
            </label>
            <label className="flex items-center gap-2">
              <input
                id="sg-cost-tax"
                type="checkbox"
                checked={settings.cost_includes_tax}
                disabled={!manage || busy}
                onChange={(e) => save({ cost_includes_tax: e.target.checked })}
              />
              Our cost prices already include GST (no input credit)
            </label>
            <p className="text-xs text-muted-foreground">
              Off: GST is added to the cost of a GST-inclusive item first, so the margin is on the price before GST. A suggestion is never above
              MRP: one that rounds over it is the MRP (&ldquo;At MRP&rdquo;). When cost is at or above MRP there is none (&ldquo;Hold&rdquo;), and
              without an MRP it is marked &ldquo;Check MRP&rdquo;.
            </p>
          </div>
        )}
      </Panel>

      <Panel title="Rules" description="The most specific rule wins: category and brand, then the nearest category, then brand, then every product.">
        <Table data-testid="suggestion-rules">
          <TableHeader>
            <TableRow>
              <TableHead>Rule</TableHead>
              <TableHead>For</TableHead>
              <TableHead>Price</TableHead>
              <TableHead>Rounding</TableHead>
              <TableHead className="text-right">Min margin</TableHead>
              <TableHead>Status</TableHead>
              {manage && <TableHead className="w-28" />}
            </TableRow>
          </TableHeader>
          <TableBody>
            {rules.length === 0 && (
              <TableRow>
                <TableCell colSpan={7} className="text-sm text-muted-foreground">
                  No rules yet. Switching suggestions on adds one for every product (a 15% margin).
                </TableCell>
              </TableRow>
            )}
            {rules.map((r) => (
              <TableRow key={r.rule_id}>
                <TableCell className="font-medium">{r.name}</TableCell>
                <TableCell className="text-sm">{scopeText(r)}</TableCell>
                <TableCell className="text-sm">{methodText(r)}</TableCell>
                <TableCell className="text-sm">{roundingLabel(r.rounding)}</TableCell>
                <TableCell className="text-right tabular">{r.min_margin_pct}%</TableCell>
                <TableCell>
                  <StatusChip status="" tone={r.active ? "ok" : "muted"} label={r.active ? "Active" : "Off"} />
                </TableCell>
                {manage && (
                  <TableCell className="text-right">
                    <div className="flex justify-end gap-1">
                      <Button size="sm" variant="ghost" className="h-7 px-2 text-xs" onClick={() => setActive(r, !r.active)}>
                        {r.active ? "Switch off" : "Switch on"}
                      </Button>
                      <Button size="icon-sm" variant="ghost" aria-label={`Edit ${r.name}`} onClick={() => setEditing(r)}>
                        <Pencil />
                      </Button>
                      <Button size="icon-sm" variant="ghost" aria-label={`Delete ${r.name}`} onClick={() => setDeleting(r)}>
                        <Trash2 />
                      </Button>
                    </div>
                  </TableCell>
                )}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </Panel>

      {settings?.enabled && <TryIt categories={catalog.categories} brands={catalog.brands} />}
      {settings?.enabled && <ApplyInBulk categories={catalog.categories} brands={catalog.brands} canApply={can("pricing.manage")} />}

      {editing && (
        <RuleDialog
          rule={editing === "new" ? null : editing}
          categories={catalog.categories}
          brands={catalog.brands}
          onClose={() => setEditing(null)}
          onSaved={loadRules}
        />
      )}
      {deleting && (
        <AlertModal
          open
          onOpenChange={(o) => !o && setDeleting(null)}
          title={`Delete ${deleting.name}?`}
          actions={
            <>
              <Button variant="outline" onClick={() => setDeleting(null)}>
                Cancel
              </Button>
              <Button
                variant="destructive"
                onClick={async () => {
                  try {
                    await api.delete(`/api/v1/pricing/suggestion-rules/${deleting.rule_id}`);
                    toast.success(`${deleting.name} deleted`);
                    setDeleting(null);
                    loadRules();
                  } catch (e) {
                    toast.error(errorText(e, "Could not delete the rule"));
                  }
                }}
              >
                Delete
              </Button>
            </>
          }
        >
          It goes to the Recycle Bin, from where it can be restored.
        </AlertModal>
      )}
    </div>
  );
}

function RuleDialog({
  rule,
  categories,
  brands,
  onClose,
  onSaved,
}: {
  rule: SuggestionRule | null;
  categories: SearchOption[];
  brands: SearchOption[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const [name, setName] = useState(rule?.name ?? "");
  const [categoryId, setCategoryId] = useState(rule?.category_id ?? "");
  const [brandId, setBrandId] = useState(rule?.brand_id ?? "");
  const [method, setMethod] = useState<SuggestionMethod>(rule?.method ?? "cost");
  const [costType, setCostType] = useState<"margin" | "markup">(rule?.cost_type ?? "margin");
  const [costPct, setCostPct] = useState(rule?.cost_pct != null ? String(rule.cost_pct) : "");
  const [mrpPct, setMrpPct] = useState(rule?.mrp_discount_pct != null ? String(rule.mrp_discount_pct) : "");
  const [minMargin, setMinMargin] = useState(String(rule?.min_margin_pct ?? 0));
  const [rounding, setRounding] = useState<SuggestionRounding>(rule?.rounding ?? "charm");
  const [priority, setPriority] = useState(String(rule?.priority ?? 0));
  const [busy, setBusy] = useState(false);
  const usesCost = method !== "mrp";
  const usesMRP = method !== "cost";

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    const body = {
      name,
      category_id: categoryId,
      brand_id: brandId,
      method,
      cost_type: costType,
      cost_pct: usesCost ? Number(costPct) : undefined,
      mrp_discount_pct: usesMRP ? Number(mrpPct) : undefined,
      min_margin_pct: Number(minMargin) || 0,
      rounding,
      priority: Number(priority) || 0,
    };
    try {
      if (rule) await api.put(`/api/v1/pricing/suggestion-rules/${rule.rule_id}`, body);
      else await api.post("/api/v1/pricing/suggestion-rules", body);
      toast.success(`${name} saved`);
      onSaved();
      onClose();
    } catch (err) {
      toast.error(errorText(err, "Could not save the rule"));
    } finally {
      setBusy(false);
    }
  }
  const ready = name.trim() && (!usesCost || costPct !== "") && (!usesMRP || mrpPct !== "");
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={rule ? `Edit ${rule.name}` : "New rule"}
      width={620}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="suggestion-rule" disabled={busy || !ready}>
            {busy && <Loader2 className="animate-spin" aria-hidden />} Save
          </Button>
        </>
      }
    >
      <form id="suggestion-rule" onSubmit={save} className="grid grid-cols-2 gap-3">
        <Field label="Name *" htmlFor="sr-name" className="col-span-2">
          <Input id="sr-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="Consumables & accessories" />
        </Field>
        <Field label="Category (and under it)" htmlFor="sr-category">
          <SearchSelect id="sr-category" ariaLabel="Rule category" value={categoryId} onChange={setCategoryId} options={categories} noneLabel="Any category" placeholder="Any category" />
        </Field>
        <Field label="Brand" htmlFor="sr-brand">
          <SearchSelect id="sr-brand" ariaLabel="Rule brand" value={brandId} onChange={setBrandId} options={brands} noneLabel="Any brand" placeholder="Any brand" />
        </Field>
        <Field label="Price from" htmlFor="sr-method">
          <select id="sr-method" value={method} onChange={(e) => setMethod(e.target.value as SuggestionMethod)} className={`${selectClass} w-full`}>
            <option value="cost">Cost</option>
            <option value="mrp">MRP less a discount</option>
            <option value="lower">The lower of the two</option>
            <option value="higher">The higher of the two</option>
          </select>
        </Field>
        <Field label="Rounding" htmlFor="sr-rounding">
          <select id="sr-rounding" value={rounding} onChange={(e) => setRounding(e.target.value as SuggestionRounding)} className={`${selectClass} w-full`}>
            {ROUNDINGS.map((r) => (
              <option key={r} value={r}>
                {roundingLabel(r)}
              </option>
            ))}
          </select>
        </Field>
        {usesCost && (
          <>
            <Field label="From cost" htmlFor="sr-cost-type">
              <select id="sr-cost-type" value={costType} onChange={(e) => setCostType(e.target.value as "margin" | "markup")} className={`${selectClass} w-full`}>
                <option value="margin">Margin on the price (cost ÷ (1 − %))</option>
                <option value="markup">Markup on cost (cost × (1 + %))</option>
              </select>
            </Field>
            <Field label={costType === "margin" ? "Margin % *" : "Markup % *"} htmlFor="sr-cost-pct">
              <Input id="sr-cost-pct" type="number" min="0" step="0.01" value={costPct} onChange={(e) => setCostPct(e.target.value)} />
            </Field>
          </>
        )}
        {usesMRP && (
          <Field label="Discount from MRP % *" htmlFor="sr-mrp-pct">
            <Input id="sr-mrp-pct" type="number" min="0" step="0.01" value={mrpPct} onChange={(e) => setMrpPct(e.target.value)} />
          </Field>
        )}
        <Field label="Never below a margin of %" htmlFor="sr-min">
          <Input id="sr-min" type="number" min="0" step="0.01" value={minMargin} onChange={(e) => setMinMargin(e.target.value)} />
        </Field>
        <Field label="Priority (same scope: higher wins)" htmlFor="sr-priority">
          <Input id="sr-priority" type="number" step="1" value={priority} onChange={(e) => setPriority(e.target.value)} />
        </Field>
      </form>
    </Modal>
  );
}

/** Work a suggestion out for figures, to see what the rules give. */
function TryIt({ categories, brands }: { categories: SearchOption[]; brands: SearchOption[] }) {
  const [cost, setCost] = useState("");
  const [mrp, setMrp] = useState("");
  const [categoryId, setCategoryId] = useState("");
  const [brandId, setBrandId] = useState("");
  const [inclusive, setInclusive] = useState(true);
  const [result, setResult] = useState<PriceSuggestion | null>(null);
  async function run(e: FormEvent) {
    e.preventDefault();
    try {
      const r = await fetchSuggestions([
        { key: "try", cost_price: Number(cost), mrp: Number(mrp) || 0, category_id: categoryId, brand_id: brandId, price_includes_tax: inclusive },
      ]);
      setResult(r[0] ?? null);
    } catch (err) {
      toast.error(errorText(err, "Could not work it out"));
    }
  }
  return (
    <Panel title="Try it" description="What the rules suggest for a cost and MRP (GST is taken as nil here; an item's own GST is used on Pricing).">
      <form onSubmit={run} className="flex flex-wrap items-end gap-3">
        <Field label={`Cost (${currencySymbol()})`} htmlFor="try-cost">
          <Input id="try-cost" type="number" min="0" step="0.01" className="w-28" value={cost} onChange={(e) => setCost(e.target.value)} />
        </Field>
        <Field label={`MRP (${currencySymbol()})`} htmlFor="try-mrp">
          <Input id="try-mrp" type="number" min="0" step="0.01" className="w-28" value={mrp} onChange={(e) => setMrp(e.target.value)} />
        </Field>
        <Field label="Category" htmlFor="try-category">
          <div className="w-52">
            <SearchSelect id="try-category" ariaLabel="Try category" value={categoryId} onChange={setCategoryId} options={categories} noneLabel="Any" placeholder="Any" />
          </div>
        </Field>
        <Field label="Brand" htmlFor="try-brand">
          <div className="w-44">
            <SearchSelect id="try-brand" ariaLabel="Try brand" value={brandId} onChange={setBrandId} options={brands} noneLabel="Any" placeholder="Any" />
          </div>
        </Field>
        <label className="flex items-center gap-2 pb-2 text-sm">
          <input type="checkbox" checked={inclusive} onChange={(e) => setInclusive(e.target.checked)} /> Price includes GST
        </label>
        <Button type="submit" variant="outline" disabled={!(Number(cost) > 0)}>
          Suggest
        </Button>
      </form>
      {result && (
        <div className="mt-3 flex flex-wrap items-center gap-2 text-sm" data-testid="try-result">
          <SuggestionChip s={result} />
          {result.suggested_price != null && <span className="font-semibold tabular">{rupees(result.suggested_price)}</span>}
          {result.rule && <span className="text-muted-foreground">{result.rule}</span>}
          {result.margin_pct != null && <span className="text-muted-foreground">{result.margin_pct}% margin</span>}
          {result.note && <span className="w-full text-xs text-muted-foreground">{result.note}</span>}
        </div>
      )}
    </Panel>
  );
}

type BulkRow = PriceSuggestion & {
  result: "would_update" | "updated" | "unchanged" | "skipped" | "skipped_negative_margin" | "skipped_above_mrp";
  price: number | null;
  edited: boolean;
};
const RESULT_LABEL: Record<BulkRow["result"], string> = {
  would_update: "Will change",
  updated: "Changed",
  unchanged: "Already there",
  skipped: "Skipped",
  skipped_negative_margin: "Skipped: below cost",
  skipped_above_mrp: "Skipped: above MRP",
};

/**
 * Preview, then set the chosen items' selling prices: each row's price
 * starts at its suggestion and can be typed over; only ticked rows are
 * applied. Narrow it by category, brand, a search, or chosen variants.
 */
function ApplyInBulk({ categories, brands, canApply }: { categories: SearchOption[]; brands: SearchOption[]; canApply: boolean }) {
  const [categoryId, setCategoryId] = useState("");
  const [brandId, setBrandId] = useState("");
  const [q, setQ] = useState("");
  const [variants, setVariants] = useState<PickedRef[]>([]);
  const [reason, setReason] = useState("");
  const [rows, setRows] = useState<BulkRow[] | null>(null);
  // Typed prices and the rows ticked to apply, by variant.
  const [prices, setPrices] = useState<Record<string, string>>({});
  const [ticked, setTicked] = useState<Record<string, boolean>>({});
  const [busy, setBusy] = useState(false);
  const priceOf = (r: BulkRow) => prices[r.key] ?? (r.price != null ? String(r.price) : "");
  const changed = (r: BulkRow) => {
    const p = Number(priceOf(r));
    return p > 0 && p !== Number(r.current_price);
  };
  const isTyped = (r: BulkRow) => prices[r.key] !== undefined && Number(prices[r.key]) !== r.price;
  const chosen = (rows ?? []).filter((r) => ticked[r.key] && changed(r));

  async function preview() {
    setBusy(true);
    try {
      const d = await api.post<{ items: BulkRow[] }>("/api/v1/pricing/suggest/preview", {
        filter: { category_id: categoryId, brand_id: brandId },
        q,
        variant_ids: variants.map((v) => v.id),
      });
      setRows(d.items);
      setPrices({});
      setTicked(Object.fromEntries(d.items.map((r) => [r.key, r.result === "would_update"])));
    } catch (e) {
      toast.error(errorText(e, "Could not work out the suggestions"));
    } finally {
      setBusy(false);
    }
  }

  async function apply() {
    if (!rows) return;
    setBusy(true);
    try {
      const edited = Object.fromEntries(chosen.filter(isTyped).map((r) => [r.key, Number(prices[r.key])]));
      const d = await api.post<{ items: BulkRow[]; updated: number }>("/api/v1/pricing/suggest/apply", {
        filter: { category_id: "", brand_id: "" },
        variant_ids: chosen.map((r) => r.key),
        prices: edited,
        reason: reason || undefined,
      });
      const byKey = Object.fromEntries(d.items.map((r) => [r.key, r]));
      setRows(rows.map((r) => byKey[r.key] ?? r));
      setPrices({});
      setTicked({});
      toast.success(`${d.updated} price${d.updated === 1 ? "" : "s"} changed`);
    } catch (e) {
      toast.error(errorText(e, "Could not apply the prices"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Panel
      title="Apply in bulk"
      description="Preview, change any price you want, and tick the rows to apply. Hold and Check MRP rows start unticked; a price above MRP or below cost is never applied."
    >
      <FilterBar>
        <Field label="Category" htmlFor="bulk-sg-category">
          <div className="w-52">
            <SearchSelect
              id="bulk-sg-category"
              ariaLabel="Apply to category"
              value={categoryId}
              onChange={setCategoryId}
              options={categories}
              noneLabel="All categories"
              placeholder="All categories"
            />
          </div>
        </Field>
        <Field label="Brand" htmlFor="bulk-sg-brand">
          <div className="w-44">
            <SearchSelect id="bulk-sg-brand" ariaLabel="Apply to brand" value={brandId} onChange={setBrandId} options={brands} noneLabel="All brands" placeholder="All brands" />
          </div>
        </Field>
        <Field label="Name, SKU or barcode" htmlFor="bulk-sg-q">
          <Input id="bulk-sg-q" className="w-44" value={q} onChange={(e) => setQ(e.target.value)} />
        </Field>
        <Field label="Variants" htmlFor="bulk-sg-variants">
          <div className="w-72">
            <MultiVariantPick id="bulk-sg-variants" value={variants} onChange={setVariants} emptyText="Every variant that matches" />
          </div>
        </Field>
        <Field label="Reason" htmlFor="bulk-sg-reason">
          <Input id="bulk-sg-reason" className="w-44" value={reason} onChange={(e) => setReason(e.target.value)} placeholder="Suggested price" />
        </Field>
        <Button variant="outline" onClick={preview} disabled={busy}>
          {busy && <Loader2 className="animate-spin" aria-hidden />} Preview
        </Button>
        {rows && chosen.length > 0 && (
          <Button onClick={apply} disabled={busy || !canApply} title={canApply ? undefined : "Needs pricing.manage"}>
            Apply {chosen.length} price{chosen.length === 1 ? "" : "s"}
          </Button>
        )}
      </FilterBar>
      {rows && (
        <Table data-testid="bulk-suggestions">
          <TableHeader>
            <TableRow>
              <TableHead className="w-10">
                <input
                  type="checkbox"
                  aria-label="Tick every row that changes"
                  checked={rows.some(changed) && rows.filter(changed).every((r) => ticked[r.key])}
                  onChange={(e) => setTicked(Object.fromEntries(rows.map((r) => [r.key, e.target.checked && changed(r)])))}
                />
              </TableHead>
              <TableHead>Item</TableHead>
              <TableHead className="text-right">Cost</TableHead>
              <TableHead className="text-right">MRP</TableHead>
              <TableHead className="text-right">Now</TableHead>
              <TableHead>Suggestion</TableHead>
              <TableHead className="w-32 text-right">New price</TableHead>
              <TableHead>Result</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.length === 0 && (
              <TableRow>
                <TableCell colSpan={8} className="text-sm text-muted-foreground">
                  No items.
                </TableCell>
              </TableRow>
            )}
            {rows.map((r) => {
              const label = `${r.product_name ?? ""} ${r.sku ?? ""}`.trim();
              return (
                <TableRow key={r.key}>
                  <TableCell>
                    <input
                      type="checkbox"
                      aria-label={`Apply ${label}`}
                      checked={!!ticked[r.key]}
                      disabled={!changed(r)}
                      onChange={(e) => setTicked((t) => ({ ...t, [r.key]: e.target.checked }))}
                    />
                  </TableCell>
                  <TableCell>
                    {r.product_name} <span className="text-xs text-muted-foreground">{r.sku}</span>
                  </TableCell>
                  <TableCell className="text-right tabular">{rupees(r.cost_price)}</TableCell>
                  <TableCell className="text-right tabular">{rupees(r.mrp)}</TableCell>
                  <TableCell className="text-right tabular">{rupees(r.current_price)}</TableCell>
                  <TableCell>
                    <div className="flex items-center gap-2" title={r.note || undefined}>
                      {r.suggested_price != null && <span className="tabular">{rupees(r.suggested_price)}</span>}
                      <SuggestionChip s={r} />
                    </div>
                    {r.rule && <span className="block text-xs text-muted-foreground">{r.rule}</span>}
                  </TableCell>
                  <TableCell className="text-right">
                    <Input
                      aria-label={`New price of ${label}`}
                      type="number"
                      min="0.01"
                      step="0.01"
                      className={`ms-auto w-28 text-right ${isTyped(r) ? "border-primary" : ""}`}
                      value={priceOf(r)}
                      placeholder="—"
                      onChange={(e) => {
                        const v = e.target.value;
                        setPrices((p) => ({ ...p, [r.key]: v }));
                        setTicked((t) => ({ ...t, [r.key]: Number(v) > 0 && Number(v) !== Number(r.current_price) }));
                      }}
                    />
                    {isTyped(r) && <span className="block text-xs text-primary">Edited</span>}
                  </TableCell>
                  <TableCell className="text-sm">{r.edited && r.result === "updated" ? "Changed (edited)" : RESULT_LABEL[r.result]}</TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      )}
    </Panel>
  );
}
