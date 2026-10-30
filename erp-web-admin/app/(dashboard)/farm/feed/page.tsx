"use client";

// Feed (erp-core-go §3.82): the farm's feed stock in days left at the
// larger of the plans' rations and the last week's use (low under the
// farm's warning days); feeding plans by age (grams a head a day, or a
// pond's % of body weight); and the farm's own feed — recipes of
// ingredients mixed into a feed in stock at their combined cost.
// farm.record; plans, recipes, settings and cancelling a mix: farm.manage.
import { FormEvent, Suspense, useCallback, useEffect, useState } from "react";
import { Loader2, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api-client";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import type { FarmFeedPlan, FarmFeedStockRow, FarmMix, FarmRecipe, FarmSpecies } from "@/lib/types";
import { localDate, rupees, shortDate } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Modal } from "@/components/ui/dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { PageHeader } from "@/components/data/page-header";
import { Field, FilterBar, Panel, selectClass } from "@/components/data/panel";
import { StatusChip } from "@/components/data/status-chip";
import { ProductPicker, VariantOption } from "@/components/data/product-picker";
import { FarmBranchSelect, PURPOSES, PURPOSE_LABEL, SPECIES_LABEL, message, useFarmBranches } from "@/components/farm/farm";
import { TabStrip, useTab } from "@/components/farm/tabs";

const TABS = ["stock", "plans", "mixing"] as const;
const TAB_LABEL = { stock: "Feed stock", plans: "Feeding plans", mixing: "Our own feed" };

function FeedPageInner() {
  const { can } = usePermissions();
  const [tab, setTab] = useTab(TABS, "/farm/feed");
  if (!can("farm.record")) return <p className="text-sm text-muted-foreground">{missingPermissionHint("farm.record")}</p>;
  return (
    <div className="max-w-5xl space-y-5">
      <PageHeader title="Feed" description="Feed stock in days left, feeding plans by age, and mixing your own feed from ingredients." />
      <TabStrip tabs={TABS} labels={TAB_LABEL} value={tab} onChange={setTab} label="Feed" />
      {tab === "stock" ? <StockTab /> : tab === "plans" ? <PlansTab /> : <MixingTab />}
    </div>
  );
}

export default function FeedPage() {
  return (
    <Suspense>
      <FeedPageInner />
    </Suspense>
  );
}

function StockTab() {
  const { can } = usePermissions();
  const [branchId, setBranchId] = useState("");
  const [rows, setRows] = useState<FarmFeedStockRow[] | null>(null);
  const [lowDays, setLowDays] = useState("");
  const load = useCallback(() => {
    api
      .get<{ items: FarmFeedStockRow[]; feed_low_days: number }>(`/api/v1/farm/feed/stock${branchId ? `?branch_id=${branchId}` : ""}`)
      .then((d) => {
        setRows(d.items);
        setLowDays(String(d.feed_low_days));
      })
      .catch((e) => toast.error(message(e, "Could not load the feed stock")));
  }, [branchId]);
  useEffect(load, [load]);
  async function saveLow() {
    try {
      await api.put("/api/v1/farm/settings", { feed_low_days: Number(lowDays) });
      toast.success("Saved");
      load();
    } catch (e) {
      toast.error(message(e, "Could not save"));
    }
  }
  return (
    <>
      <FilterBar>
        <Field label="Farm" htmlFor="fs-branch">
          <FarmBranchSelect id="fs-branch" value={branchId} onChange={setBranchId} allLabel="Every farm" />
        </Field>
        <Field label="Low when it lasts under (days)" htmlFor="fs-low">
          <div className="flex gap-2">
            <Input id="fs-low" type="number" min="1" step="1" className="w-20" value={lowDays} disabled={!can("farm.manage")} onChange={(e) => setLowDays(e.target.value)} />
            {can("farm.manage") && (
              <Button variant="outline" onClick={saveLow}>
                Save
              </Button>
            )}
          </div>
        </Field>
      </FilterBar>
      <Panel title="Feed stock" description="Feed items (Health & Medicine › Farm items), the feeds the plans name, and what was fed in the last 30 days.">
        <Table data-testid="feed-stock">
          <TableHeader>
            <TableRow>
              <TableHead>Feed</TableHead>
              <TableHead>Farm</TableHead>
              <TableHead className="text-right">In stock</TableHead>
              <TableHead className="text-right">Plan a day</TableHead>
              <TableHead className="text-right">Used a day (7 days)</TableHead>
              <TableHead className="text-right">Days left</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows?.length === 0 && (
              <TableRow>
                <TableCell colSpan={6} className="py-5 text-center text-sm text-muted-foreground">
                  No feed yet. Mark products as feed in Health & Medicine › Farm items, or name them in a feeding plan.
                </TableCell>
              </TableRow>
            )}
            {rows?.map((r) => (
              <TableRow key={`${r.branch_id}-${r.product_id}`}>
                <TableCell className="font-medium">{r.product}</TableCell>
                <TableCell className="text-sm">{r.branch}</TableCell>
                <TableCell className="text-right tabular">
                  {r.free} {r.unit}
                </TableCell>
                <TableCell className="text-right tabular">{r.planned_daily != null ? `${r.planned_daily} ${r.unit}` : "—"}</TableCell>
                <TableCell className="text-right tabular">{r.used_daily ? `${r.used_daily} ${r.unit}` : "—"}</TableCell>
                <TableCell className="text-right">
                  {r.days_left == null ? (
                    <span className="text-muted-foreground">—</span>
                  ) : (
                    <StatusChip status="" tone={r.low ? "bad" : "ok"} label={`${r.days_left} days`} />
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </Panel>
    </>
  );
}

function PlansTab() {
  const { can } = usePermissions();
  const [plans, setPlans] = useState<FarmFeedPlan[]>([]);
  const [editing, setEditing] = useState<FarmFeedPlan | "new" | null>(null);
  const load = useCallback(() => {
    api
      .get<{ feed_plans: FarmFeedPlan[] }>("/api/v1/farm/feed-plans")
      .then((d) => setPlans(d.feed_plans))
      .catch((e) => toast.error(message(e, "Could not load the plans")));
  }, []);
  useEffect(load, [load]);
  return (
    <>
      <div className="flex items-center justify-between gap-3">
        <p className="text-sm text-muted-foreground">
          The standard plans are typical practice — confirm them with your nutritionist. Choose the feed in stock for each line to see it in feed stock.
        </p>
        <Button onClick={() => setEditing("new")} disabled={!can("farm.manage")}>
          <Plus aria-hidden /> New plan
        </Button>
      </div>
      {plans.map((p) => (
        <Panel
          key={p.feed_plan_id}
          title={p.name}
          description={`${SPECIES_LABEL[p.species]}${p.purpose ? ` · ${PURPOSE_LABEL[p.purpose] ?? p.purpose}` : ""} · ${p.basis === "per_head" ? "grams a head a day" : "% of body weight a day"}${p.is_system ? " · standard" : ""} · ${p.batches} active batch${p.batches === 1 ? "" : "es"}`}
          actions={
            <div className="flex items-center gap-2">
              <StatusChip status="" tone={p.status === "active" ? "ok" : "muted"} label={p.status === "active" ? "Active" : "Off"} />
              <Button size="sm" variant="ghost" disabled={!can("farm.manage")} onClick={() => setEditing(p)} aria-label={`Edit ${p.name}`}>
                Edit
              </Button>
            </div>
          }
        >
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-32">Days of age</TableHead>
                <TableHead>Feed</TableHead>
                <TableHead>In stock as</TableHead>
                <TableHead className="text-right">A day</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {p.items.map((i) => (
                <TableRow key={i.plan_item_id}>
                  <TableCell className="tabular">
                    {i.day_from}–{i.day_to ?? "on"}
                  </TableCell>
                  <TableCell className="text-sm">{i.feed_name}</TableCell>
                  <TableCell className="text-sm text-muted-foreground">{i.product ?? "—"}</TableCell>
                  <TableCell className="text-right tabular">{i.grams_per_head != null ? `${i.grams_per_head} g a head` : `${i.pct_body_weight}% of weight`}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Panel>
      ))}
      {editing && <PlanDialog plan={editing === "new" ? null : editing} onClose={() => setEditing(null)} onSaved={load} />}
    </>
  );
}

type PlanRow = { from: string; to: string; feed: string; product: VariantOption | null; amount: string };

function PlanDialog({ plan, onClose, onSaved }: { plan: FarmFeedPlan | null; onClose: () => void; onSaved: () => void }) {
  const [species, setSpecies] = useState<FarmSpecies>(plan?.species ?? "chicken");
  const [purpose, setPurpose] = useState(plan?.purpose ?? "");
  const [name, setName] = useState(plan?.name ?? "");
  const [basis, setBasis] = useState<"per_head" | "pct_biomass">(plan?.basis ?? "per_head");
  const [status, setStatus] = useState(plan?.status ?? "active");
  const [rows, setRows] = useState<PlanRow[]>(
    plan?.items.map((i) => ({
      from: String(i.day_from),
      to: i.day_to != null ? String(i.day_to) : "",
      feed: i.feed_name,
      product: i.product_id
        ? { variantId: i.product_id, productId: i.product_id, productName: i.product ?? "", sku: "", sellingPrice: "", label: i.product ?? "", trackBatch: false }
        : null,
      amount: String(i.grams_per_head ?? i.pct_body_weight ?? ""),
    })) ?? [{ from: "0", to: "", feed: "", product: null, amount: "" }],
  );
  const [busy, setBusy] = useState(false);
  const set = (i: number, patch: Partial<PlanRow>) => setRows((r) => r.map((x, j) => (j === i ? { ...x, ...patch } : x)));
  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    const items = rows
      .filter((r) => r.feed.trim())
      .map((r) => ({
        day_from: Number(r.from) || 0,
        day_to: r.to.trim() === "" ? undefined : Number(r.to),
        feed_name: r.feed,
        product_id: r.product?.productId,
        grams_per_head: basis === "per_head" ? Number(r.amount) : undefined,
        pct_body_weight: basis === "pct_biomass" ? Number(r.amount) : undefined,
      }));
    try {
      if (plan) await api.put(`/api/v1/farm/feed-plans/${plan.feed_plan_id}`, { name, purpose, basis, status, items });
      else await api.post("/api/v1/farm/feed-plans", { species, purpose, name, basis, items });
      toast.success(`${name} saved`);
      onSaved();
      onClose();
    } catch (err) {
      toast.error(message(err, "Could not save the plan"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={plan ? `Edit ${plan.name}` : "New feeding plan"}
      width={820}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="feed-plan" disabled={busy || !name.trim()}>
            {busy && <Loader2 className="animate-spin" aria-hidden />} Save
          </Button>
        </>
      }
    >
      <form id="feed-plan" onSubmit={save} className="space-y-3">
        <div className="grid grid-cols-4 gap-3">
          <Field label="Species" htmlFor="fp-species">
            <select id="fp-species" value={species} disabled={!!plan} onChange={(e) => setSpecies(e.target.value as FarmSpecies)} className={`${selectClass} w-full`}>
              {Object.entries(SPECIES_LABEL).map(([k, v]) => (
                <option key={k} value={k}>
                  {v}
                </option>
              ))}
            </select>
          </Field>
          <Field label="For" htmlFor="fp-purpose">
            <select id="fp-purpose" value={purpose} onChange={(e) => setPurpose(e.target.value)} className={`${selectClass} w-full`}>
              <option value="">Any purpose</option>
              {PURPOSES[species].map((p) => (
                <option key={p} value={p}>
                  {PURPOSE_LABEL[p] ?? p}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Ration as" htmlFor="fp-basis">
            <select id="fp-basis" value={basis} onChange={(e) => setBasis(e.target.value as "per_head" | "pct_biomass")} className={`${selectClass} w-full`}>
              <option value="per_head">Grams a head a day</option>
              <option value="pct_biomass">% of body weight a day</option>
            </select>
          </Field>
          {plan && (
            <Field label="Status" htmlFor="fp-status">
              <select id="fp-status" value={status} onChange={(e) => setStatus(e.target.value as "active" | "inactive")} className={`${selectClass} w-full`}>
                <option value="active">Active</option>
                <option value="inactive">Off</option>
              </select>
            </Field>
          )}
        </div>
        <Field label="Name *" htmlFor="fp-name">
          <Input id="fp-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="Our broiler feeding" />
        </Field>
        <div className="space-y-2">
          {rows.map((r, i) => (
            <div key={i} className="grid grid-cols-[64px_64px_1fr_1.2fr_96px_auto] items-end gap-2">
              <Field label={i === 0 ? "Day from" : ""} htmlFor={`fp-from-${i}`}>
                <Input id={`fp-from-${i}`} type="number" min="0" value={r.from} aria-label={`Day from, line ${i + 1}`} onChange={(e) => set(i, { from: e.target.value })} />
              </Field>
              <Field label={i === 0 ? "to" : ""} htmlFor={`fp-to-${i}`}>
                <Input id={`fp-to-${i}`} type="number" min="0" value={r.to} placeholder="on" aria-label={`Day to, line ${i + 1}`} onChange={(e) => set(i, { to: e.target.value })} />
              </Field>
              <Field label={i === 0 ? "Feed" : ""} htmlFor={`fp-feed-${i}`}>
                <Input id={`fp-feed-${i}`} value={r.feed} aria-label={`Feed, line ${i + 1}`} onChange={(e) => set(i, { feed: e.target.value })} />
              </Field>
              <Field label={i === 0 ? "In stock as" : ""} htmlFor={`fp-product-${i}`}>
                <ProductPicker id={`fp-product-${i}`} mode="product" value={r.product} onChange={(o) => set(i, { product: o })} placeholder="Choose the feed" />
              </Field>
              <Field label={i === 0 ? (basis === "per_head" ? "g a head" : "% weight") : ""} htmlFor={`fp-amount-${i}`}>
                <Input id={`fp-amount-${i}`} type="number" min="0" step="any" value={r.amount} aria-label={`Amount, line ${i + 1}`} onChange={(e) => set(i, { amount: e.target.value })} />
              </Field>
              <Button type="button" size="icon-sm" variant="ghost" aria-label={`Remove line ${i + 1}`} onClick={() => setRows((x) => x.filter((_, j) => j !== i))}>
                <Trash2 />
              </Button>
            </div>
          ))}
          <Button type="button" size="sm" variant="outline" onClick={() => setRows((x) => [...x, { from: "", to: "", feed: "", product: null, amount: "" }])}>
            <Plus aria-hidden /> Add a line
          </Button>
        </div>
      </form>
    </Modal>
  );
}

function MixingTab() {
  const { can } = usePermissions();
  const [recipes, setRecipes] = useState<FarmRecipe[]>([]);
  const [mixes, setMixes] = useState<FarmMix[]>([]);
  const [editing, setEditing] = useState<FarmRecipe | "new" | null>(null);
  const [mixing, setMixing] = useState<FarmRecipe | null>(null);
  const load = useCallback(() => {
    api
      .get<{ recipes: FarmRecipe[] }>("/api/v1/farm/feed/recipes")
      .then((d) => setRecipes(d.recipes))
      .catch((e) => toast.error(message(e, "Could not load the recipes")));
    api
      .get<{ mixes: FarmMix[] }>("/api/v1/farm/feed/mixes")
      .then((d) => setMixes(d.mixes))
      .catch(() => setMixes([]));
  }, []);
  useEffect(load, [load]);
  async function cancel(m: FarmMix) {
    const reason = window.prompt(`Cancel ${m.mix_number}? Its feed leaves stock and the ingredients come back. Why?`);
    if (!reason) return;
    try {
      await api.post(`/api/v1/farm/feed/mixes/${m.mix_id}/cancel`, { reason });
      toast.success(`${m.mix_number} cancelled`);
      load();
    } catch (e) {
      toast.error(message(e, "Could not cancel it"));
    }
  }
  return (
    <>
      <Panel
        title="Recipes"
        description="Ingredients for one batch of a feed. The cost is at today's ingredient costs."
        actions={
          <Button size="sm" onClick={() => setEditing("new")} disabled={!can("farm.manage")}>
            <Plus aria-hidden /> New recipe
          </Button>
        }
      >
        <Table data-testid="feed-recipes">
          <TableHeader>
            <TableRow>
              <TableHead>Recipe</TableHead>
              <TableHead>Makes</TableHead>
              <TableHead>Ingredients</TableHead>
              <TableHead className="text-right">Cost a unit</TableHead>
              <TableHead className="text-right">Action</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {recipes.length === 0 && (
              <TableRow>
                <TableCell colSpan={5} className="py-5 text-center text-sm text-muted-foreground">
                  No recipes yet.
                </TableCell>
              </TableRow>
            )}
            {recipes.map((r) => (
              <TableRow key={r.recipe_id} className={r.status !== "active" ? "opacity-60" : undefined}>
                <TableCell className="font-medium">{r.name}</TableCell>
                <TableCell className="text-sm">
                  {r.batch_size} {r.output_unit} {r.output}
                </TableCell>
                <TableCell className="text-sm">{r.items.map((i) => `${i.product} ${i.quantity} ${i.unit}`).join(" · ")}</TableCell>
                <TableCell className="text-right tabular">{rupees(r.cost_per_unit)}</TableCell>
                <TableCell className="text-right">
                  <div className="flex justify-end gap-1">
                    {r.status === "active" && (
                      <Button size="sm" onClick={() => setMixing(r)} aria-label={`Mix ${r.name}`}>
                        Mix
                      </Button>
                    )}
                    <Button size="sm" variant="ghost" disabled={!can("farm.manage")} onClick={() => setEditing(r)} aria-label={`Edit ${r.name}`}>
                      Edit
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </Panel>
      <Panel title="Mixes">
        <Table data-testid="feed-mixes">
          <TableHeader>
            <TableRow>
              <TableHead>Mix</TableHead>
              <TableHead>Date</TableHead>
              <TableHead>Made</TableHead>
              <TableHead>Used</TableHead>
              <TableHead className="text-right">Cost</TableHead>
              <TableHead className="text-right">Action</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {mixes.length === 0 && (
              <TableRow>
                <TableCell colSpan={6} className="py-5 text-center text-sm text-muted-foreground">
                  No mixes yet.
                </TableCell>
              </TableRow>
            )}
            {mixes.map((m) => (
              <TableRow key={m.mix_id} className={m.status === "cancelled" ? "opacity-60" : undefined}>
                <TableCell className="text-sm">
                  <span className="font-medium">{m.mix_number}</span>
                  <span className="block text-xs text-muted-foreground">{[m.recipe, m.branch].filter(Boolean).join(" · ")}</span>
                  {m.status === "cancelled" && <span className="block text-xs text-destructive">Cancelled: {m.cancel_reason}</span>}
                </TableCell>
                <TableCell className="text-sm">{shortDate(m.mixed_on)}</TableCell>
                <TableCell className="text-sm">
                  {m.output_qty} {m.output_unit} {m.output}
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">{m.lines.map((l) => `${l.product} ${l.quantity} ${l.unit}`).join(" · ")}</TableCell>
                <TableCell className="text-right tabular">
                  {rupees(m.total_cost)}
                  <span className="block text-xs text-muted-foreground">
                    {rupees(m.unit_cost)}/{m.output_unit}
                  </span>
                </TableCell>
                <TableCell className="text-right">
                  {m.status === "posted" && can("farm.manage") && (
                    <Button size="sm" variant="ghost" className="text-destructive hover:text-destructive" onClick={() => cancel(m)}>
                      Cancel
                    </Button>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </Panel>
      {editing && <RecipeDialog recipe={editing === "new" ? null : editing} onClose={() => setEditing(null)} onSaved={load} />}
      {mixing && <MixDialog recipe={mixing} onClose={() => setMixing(null)} onSaved={load} />}
    </>
  );
}

type Line = { item: VariantOption | null; qty: string };

function RecipeDialog({ recipe, onClose, onSaved }: { recipe: FarmRecipe | null; onClose: () => void; onSaved: () => void }) {
  const opt = (variantId: string, label: string): VariantOption => ({ variantId, productId: "", productName: label, sku: "", sellingPrice: "", label, trackBatch: false });
  const [name, setName] = useState(recipe?.name ?? "");
  const [output, setOutput] = useState<VariantOption | null>(recipe ? opt(recipe.output_variant_id, recipe.output) : null);
  const [size, setSize] = useState(recipe ? String(recipe.batch_size) : "100");
  const [status, setStatus] = useState(recipe?.status ?? "active");
  const [lines, setLines] = useState<Line[]>(recipe?.items.map((i) => ({ item: opt(i.variant_id, i.product), qty: String(i.quantity) })) ?? [{ item: null, qty: "" }]);
  const [busy, setBusy] = useState(false);
  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    const body = {
      name,
      output_variant_id: output?.variantId,
      batch_size: Number(size),
      status,
      items: lines.filter((l) => l.item && Number(l.qty) > 0).map((l) => ({ variant_id: l.item!.variantId, quantity: Number(l.qty) })),
    };
    try {
      if (recipe) await api.put(`/api/v1/farm/feed/recipes/${recipe.recipe_id}`, body);
      else await api.post("/api/v1/farm/feed/recipes", body);
      toast.success(`${name} saved`);
      onSaved();
      onClose();
    } catch (err) {
      toast.error(message(err, "Could not save the recipe"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={recipe ? `Edit ${recipe.name}` : "New recipe"}
      width={680}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="feed-recipe" disabled={busy || !name.trim() || !output || !(Number(size) > 0)}>
            {busy && <Loader2 className="animate-spin" aria-hidden />} Save
          </Button>
        </>
      }
    >
      <form id="feed-recipe" onSubmit={save} className="space-y-3">
        <div className="grid grid-cols-2 gap-3">
          <Field label="Name *" htmlFor="rc-name">
            <Input id="rc-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="Layer mash" />
          </Field>
          {recipe && (
            <Field label="Status" htmlFor="rc-status">
              <select id="rc-status" value={status} onChange={(e) => setStatus(e.target.value as "active" | "inactive")} className={`${selectClass} w-full`}>
                <option value="active">Active</option>
                <option value="inactive">Off</option>
              </select>
            </Field>
          )}
          <Field label="Makes (the feed in stock) *" htmlFor="rc-output">
            <ProductPicker id="rc-output" value={output} onChange={setOutput} placeholder="Home layer mash" />
          </Field>
          <Field label="One batch makes (in its unit) *" htmlFor="rc-size">
            <Input id="rc-size" type="number" min="0" step="any" value={size} onChange={(e) => setSize(e.target.value)} />
          </Field>
        </div>
        <p className="text-xs text-muted-foreground">Ingredients for one batch:</p>
        {lines.map((l, i) => (
          <div key={i} className="grid grid-cols-[1fr_120px_auto] items-end gap-2">
            <ProductPicker id={`rc-item-${i}`} ariaLabel={`Ingredient ${i + 1}`} value={l.item} onChange={(o) => setLines((x) => x.map((y, j) => (j === i ? { ...y, item: o } : y)))} placeholder="Maize, soya meal, premix…" />
            <Input
              aria-label={`Quantity of ingredient ${i + 1}`}
              type="number"
              min="0"
              step="any"
              value={l.qty}
              onChange={(e) => setLines((x) => x.map((y, j) => (j === i ? { ...y, qty: e.target.value } : y)))}
            />
            <Button type="button" size="icon-sm" variant="ghost" aria-label={`Remove ingredient ${i + 1}`} onClick={() => setLines((x) => x.filter((_, j) => j !== i))}>
              <Trash2 />
            </Button>
          </div>
        ))}
        <Button type="button" size="sm" variant="outline" onClick={() => setLines((x) => [...x, { item: null, qty: "" }])}>
          <Plus aria-hidden /> Add an ingredient
        </Button>
      </form>
    </Modal>
  );
}

function MixDialog({ recipe, onClose, onSaved }: { recipe: FarmRecipe; onClose: () => void; onSaved: () => void }) {
  const branches = useFarmBranches();
  const [branchId, setBranchId] = useState(branches[0]?.branch_id ?? "");
  const [qty, setQty] = useState(String(recipe.batch_size));
  const [date, setDate] = useState(() => localDate());
  const [busy, setBusy] = useState(false);
  const scale = (Number(qty) || 0) / recipe.batch_size;
  async function go() {
    setBusy(true);
    try {
      const m = await api.post<FarmMix>("/api/v1/farm/feed/mixes", { branch_id: branchId, recipe_id: recipe.recipe_id, output_qty: Number(qty), mixed_on: date });
      toast.success(`${m.mix_number}: ${m.output_qty} ${m.output_unit} at ${rupees(m.unit_cost)} a ${m.output_unit}`);
      onSaved();
      onClose();
    } catch (e) {
      toast.error(message(e, "Could not mix it"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={`Mix ${recipe.name}`}
      description={`The ingredients leave the farm's stock at their cost; ${recipe.output} comes in at their total.`}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={go} disabled={busy || !branchId || !(Number(qty) > 0)}>
            {busy && <Loader2 className="animate-spin" aria-hidden />} Mix
          </Button>
        </>
      }
    >
      <div className="grid grid-cols-3 gap-3">
        <Field label="Farm" htmlFor="mx-branch">
          <FarmBranchSelect id="mx-branch" value={branchId} onChange={setBranchId} />
        </Field>
        <Field label={`How much (${recipe.output_unit})`} htmlFor="mx-qty">
          <Input id="mx-qty" type="number" min="0" step="any" value={qty} onChange={(e) => setQty(e.target.value)} />
        </Field>
        <Field label="Date" htmlFor="mx-date">
          <Input id="mx-date" type="date" value={date} max={localDate()} onChange={(e) => setDate(e.target.value)} />
        </Field>
      </div>
      <ul className="mt-3 space-y-1 text-sm" data-testid="mix-preview">
        {recipe.items.map((i) => (
          <li key={i.variant_id} className="flex justify-between">
            <span>{i.product}</span>
            <span className="tabular">
              {Math.round(i.quantity * scale * 1000) / 1000} {i.unit}
            </span>
          </li>
        ))}
        <li className="flex justify-between border-t pt-1 font-medium">
          <span>About</span>
          <span className="tabular">{rupees(Math.round(recipe.cost_per_batch * scale * 100) / 100)}</span>
        </li>
      </ul>
    </Modal>
  );
}
