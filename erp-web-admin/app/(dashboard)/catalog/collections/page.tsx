"use client";

// Collections & markdowns (Phase 8 Apparel; erp-core-go migrations/084,
// §3.87). A collection is a season or drop products are tagged with (on the
// product). A markdown plan steps its prices down on dates — 20% off from
// the 1st, 40% from the 15th, until the 31st — through ordinary price rules
// on the collection, so the till and every report price it like any rule.
// A product's own (variant) rule still wins. Collections: catalog.manage;
// plans: pricing.manage.
import { useCallback, useEffect, useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import type { Collection, MarkdownPlan } from "@/lib/types";
import { dateTime, localDate } from "@/lib/format";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Modal } from "@/components/ui/dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { PageHeader } from "@/components/data/page-header";
import { Field, Panel, selectClass } from "@/components/data/panel";
import { StatusChip } from "@/components/data/status-chip";
import { useMyBranches } from "@/components/data/branch-select";

const STEP_TONE = { live: "active", upcoming: "pending", ended: "inactive" } as const;

export default function CollectionsPage() {
  const { can } = usePermissions();
  const [collections, setCollections] = useState<Collection[] | null>(null);
  const [selected, setSelected] = useState<string>("");
  const [creating, setCreating] = useState(false);

  const load = useCallback(() => {
    api
      .get<{ collections: Collection[] }>("/api/v1/collections")
      .then((d) => {
        setCollections(d.collections);
        setSelected((s) => s || d.collections[0]?.collection_id || "");
      })
      .catch(() => setCollections([]));
  }, []);
  useEffect(load, [load]);
  const current = collections?.find((c) => c.collection_id === selected);

  return (
    <div className="max-w-5xl space-y-5">
      <PageHeader
        title="Collections & Markdowns"
        description="Seasons and drops products are tagged with, and their end-of-season markdowns: prices that step down on dates and stop on their own."
        actions={
          <Button onClick={() => setCreating(true)} disabled={!can("catalog.manage")} title={can("catalog.manage") ? undefined : missingPermissionHint("catalog.manage")}>
            <Plus /> New collection
          </Button>
        }
      />
      <div className="grid grid-cols-1 gap-5 md:grid-cols-[16rem_1fr]">
        <Panel title="Collections">
          {collections && collections.length === 0 && <p className="text-sm text-muted-foreground">No collections yet.</p>}
          <ul className="flex flex-col gap-1" data-testid="collections-list">
            {(collections ?? []).map((c) => (
              <li key={c.collection_id}>
                <button
                  type="button"
                  onClick={() => setSelected(c.collection_id)}
                  className={`w-full rounded-lg px-2 py-1.5 text-start text-sm ${c.collection_id === selected ? "bg-primary-soft font-medium text-primary" : "hover:bg-muted"}`}
                >
                  {c.name}
                  {c.season && <span className="block text-xs text-muted-foreground">{c.season}</span>}
                </button>
              </li>
            ))}
          </ul>
        </Panel>
        {current ? <MarkdownPlans key={current.collection_id} collection={current} /> : <Panel>Choose a collection.</Panel>}
      </div>
      {creating && (
        <NewCollection
          onClose={() => setCreating(false)}
          onSaved={(c) => {
            setCreating(false);
            setSelected(c.collection_id);
            load();
          }}
        />
      )}
    </div>
  );
}

function NewCollection({ onClose, onSaved }: { onClose: () => void; onSaved: (c: Collection) => void }) {
  const [name, setName] = useState("");
  const [season, setSeason] = useState("");
  const [busy, setBusy] = useState(false);
  async function save(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      const c = await api.post<Collection>("/api/v1/collections", { name: name.trim(), season: season.trim() });
      toast.success(`${c.name} added — tag products with it on their page`);
      onSaved(c);
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : "Could not add the collection");
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title="New collection"
      footer={
        <Button type="submit" form="new-collection" disabled={busy || !name.trim()}>
          Add collection
        </Button>
      }
    >
      <form id="new-collection" onSubmit={save} className="flex flex-col gap-4">
        <Field label="Name" htmlFor="col-name">
          <Input id="col-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="Winter 2026" autoFocus />
        </Field>
        <Field label="Season (optional)" htmlFor="col-season">
          <Input id="col-season" value={season} onChange={(e) => setSeason(e.target.value)} placeholder="Winter" />
        </Field>
      </form>
    </Modal>
  );
}

function MarkdownPlans({ collection }: { collection: Collection }) {
  const { can } = usePermissions();
  const allowed = can("pricing.manage");
  const [plans, setPlans] = useState<MarkdownPlan[] | null>(null);
  const [planning, setPlanning] = useState(false);
  const [busy, setBusy] = useState(false);
  const load = useCallback(() => {
    api
      .get<{ plans: MarkdownPlan[] }>(`/api/v1/collections/${collection.collection_id}/markdown-plans`)
      .then((d) => setPlans(d.plans))
      .catch(() => setPlans([]));
  }, [collection.collection_id]);
  useEffect(load, [load]);

  async function cancel(p: MarkdownPlan) {
    setBusy(true);
    try {
      await api.post(`/api/v1/markdown-plans/${p.markdown_plan_id}/cancel`, {});
      toast.success(`${p.name} cancelled — the collection sells at its own prices again`);
      load();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not cancel the plan");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Panel
      title={`${collection.name} — markdowns`}
      description="Each step is a percent off the selling price (never above MRP), from its date until the next step or the end."
      actions={
        <Button size="sm" onClick={() => setPlanning(true)} disabled={!allowed} title={allowed ? undefined : missingPermissionHint("pricing.manage")}>
          <Plus /> New markdown plan
        </Button>
      }
      data-testid="markdown-plans"
    >
      {plans && plans.length === 0 && <p className="text-sm text-muted-foreground">No markdown plans for this collection.</p>}
      {(plans ?? []).map((p) => (
        <div key={p.markdown_plan_id} className="flex flex-col gap-2 rounded-lg border p-3" data-testid={`plan-${p.name}`}>
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div>
              <span className="font-medium">{p.name}</span> <StatusChip status={p.status === "active" ? "active" : "cancelled"} />
              <span className="block text-xs text-muted-foreground">
                {p.branch_name ?? "Every branch"} · {p.products} product{p.products === 1 ? "" : "s"} · made {dateTime(p.created_at)}
                {p.cancelled_at ? ` · cancelled ${dateTime(p.cancelled_at)}` : ""}
              </span>
            </div>
            {p.status === "active" && (
              <Button size="sm" variant="outline" disabled={!allowed || busy} onClick={() => cancel(p)}>
                Cancel plan
              </Button>
            )}
          </div>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Off</TableHead>
                <TableHead>From</TableHead>
                <TableHead>Until</TableHead>
                <TableHead>State</TableHead>
                <TableHead className="text-right">Lines sold</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {p.steps.map((s) => (
                <TableRow key={s.price_rule_id}>
                  <TableCell className="tabular font-medium">{Number(s.percent_off)}%</TableCell>
                  <TableCell className="text-sm">{dateTime(s.starts_at)}</TableCell>
                  <TableCell className="text-sm">{s.ends_at ? dateTime(s.ends_at) : "—"}</TableCell>
                  <TableCell>
                    <StatusChip status={STEP_TONE[s.state]} label={s.state} />
                  </TableCell>
                  <TableCell className="tabular text-right">{s.times_used}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      ))}
      {planning && (
        <NewPlan
          collection={collection}
          onClose={() => setPlanning(false)}
          onSaved={() => {
            setPlanning(false);
            load();
          }}
        />
      )}
    </Panel>
  );
}

function NewPlan({ collection, onClose, onSaved }: { collection: Collection; onClose: () => void; onSaved: () => void }) {
  const { branches } = useMyBranches();
  const [name, setName] = useState(`${collection.name} clearance`);
  const [branchId, setBranchId] = useState("");
  const [steps, setSteps] = useState([{ starts_on: localDate(), percent_off: "20" }]);
  const [endsOn, setEndsOn] = useState("");
  const [belowCost, setBelowCost] = useState<string[] | null>(null);
  const [override, setOverride] = useState(false);
  const [busy, setBusy] = useState(false);

  const problem = steps.some((s) => !s.starts_on || !(Number(s.percent_off) > 0) || Number(s.percent_off) > 90)
    ? "Each step needs a date and 1–90% off"
    : steps.some((s, i) => i > 0 && s.starts_on <= steps[i - 1].starts_on)
      ? "Each step starts after the one before"
      : endsOn && endsOn <= steps[steps.length - 1].starts_on
        ? "The end comes after the last step starts"
        : null;

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      await api.post(`/api/v1/collections/${collection.collection_id}/markdown-plans`, {
        name: name.trim(),
        branch_id: branchId,
        steps: steps.map((s) => ({ starts_on: s.starts_on, percent_off: Number(s.percent_off) })),
        ends_on: endsOn,
        override,
      });
      toast.success("Markdown plan set — the till prices it from its first date");
      onSaved();
    } catch (err) {
      if (err instanceof ApiError && err.code === "NEGATIVE_MARGIN") {
        setBelowCost(((err.details as { below_cost_skus?: string[] })?.below_cost_skus ?? []) as string[]);
      } else {
        toast.error(err instanceof ApiError ? err.message : "Could not set the plan");
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      open
      width={560}
      onOpenChange={(o) => !o && onClose()}
      title="New markdown plan"
      description={collection.name}
      footer={
        <Button type="submit" form="new-plan" disabled={busy || problem != null || (belowCost != null && !override)}>
          Set markdown
        </Button>
      }
    >
      <form id="new-plan" onSubmit={save} className="flex flex-col gap-4">
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <Field label="Name" htmlFor="plan-name">
            <Input id="plan-name" value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Field label="Branch" htmlFor="plan-branch">
            <select id="plan-branch" value={branchId} onChange={(e) => setBranchId(e.target.value)} className={selectClass}>
              <option value="">Every branch</option>
              {branches.map((b) => (
                <option key={b.branch_id} value={b.branch_id}>
                  {b.name}
                </option>
              ))}
            </select>
          </Field>
        </div>
        <div className="flex flex-col gap-2">
          <span className="text-sm font-medium">Steps</span>
          {steps.map((s, i) => (
            <div key={i} className="flex flex-wrap items-center gap-2" data-testid={`plan-step-${i}`}>
              <Input
                type="date"
                aria-label={`Step ${i + 1} starts`}
                className="w-40"
                value={s.starts_on}
                onChange={(e) => setSteps((st) => st.map((x, j) => (j === i ? { ...x, starts_on: e.target.value } : x)))}
              />
              <Input
                type="number"
                aria-label={`Step ${i + 1} percent off`}
                className="w-24"
                min="1"
                max="90"
                step="1"
                value={s.percent_off}
                onChange={(e) => setSteps((st) => st.map((x, j) => (j === i ? { ...x, percent_off: e.target.value } : x)))}
              />
              <span className="text-sm">% off</span>
              {steps.length > 1 && (
                <Button type="button" variant="ghost" size="icon-sm" aria-label="Remove step" onClick={() => setSteps((st) => st.filter((_, j) => j !== i))}>
                  <Trash2 />
                </Button>
              )}
            </div>
          ))}
          {steps.length < 10 && (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="w-fit"
              onClick={() => setSteps((st) => [...st, { starts_on: "", percent_off: String(Math.min(90, Number(st[st.length - 1].percent_off) + 20)) }])}
            >
              <Plus /> Add a step
            </Button>
          )}
        </div>
        <Field label="Ends on (optional)" htmlFor="plan-ends">
          <Input id="plan-ends" type="date" className="w-40" value={endsOn} onChange={(e) => setEndsOn(e.target.value)} />
        </Field>
        {problem && <p className="text-sm text-destructive">{problem}</p>}
        {belowCost && (
          <label className="flex items-start gap-2 text-sm text-destructive" data-testid="plan-below-cost">
            <input type="checkbox" className="mt-1" checked={override} onChange={(e) => setOverride(e.target.checked)} />
            <span>
              The deepest step sells {belowCost.length} item{belowCost.length === 1 ? "" : "s"} below cost ({belowCost.slice(0, 5).join(", ")}
              {belowCost.length > 5 ? "…" : ""}). Clear them anyway.
            </span>
          </label>
        )}
      </form>
    </Modal>
  );
}
