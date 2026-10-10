"use client";

// Plans as data (Phase 16): price per POS, limits, features, retention,
// trial and grace days, offered or retired. A change applies to every
// merchant on the plan within half a minute (the entitlement cache) and is
// written to the platform audit log with its reason.
import { useEffect, useState } from "react";
import { Loader2 } from "lucide-react";
import { toast } from "sonner";
import { ApiError } from "@/lib/api-client";
import { Plan, platformApi } from "@/lib/platform-api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { PageHeader } from "@/components/data/page-header";
import { FEATURE_LABEL } from "@/components/shell/plan";

const LIMIT_LABEL: Record<string, string> = {
  branches: "Branches",
  pos_terminals: "POS terminals",
  products: "Products",
  transactions_per_month: "Sales / month",
};

type Draft = {
  name: string;
  price: string;
  limits: Record<string, string>; // "" = unlimited
  features: string[];
  retention: string; // "" = lifetime
  trial: string;
  grace: string;
  active: boolean;
  reason: string;
};

function toDraft(p: Plan, kinds: string[]): Draft {
  return {
    name: p.name,
    price: p.price_per_pos_month,
    limits: Object.fromEntries(kinds.map((k) => [k, p.limits[k] == null ? "" : String(p.limits[k])])),
    features: p.features,
    retention: p.retention_days == null ? "" : String(p.retention_days),
    trial: String(p.trial_days),
    grace: String(p.grace_days),
    active: p.active,
    reason: "",
  };
}

function PlanEditor({ plan, features, kinds, onSaved }: { plan: Plan; features: string[]; kinds: string[]; onSaved: (p: Plan) => void }) {
  const [d, setD] = useState<Draft>(() => toDraft(plan, kinds));
  const [busy, setBusy] = useState(false);
  const id = (s: string) => `${plan.code}-${s}`;

  async function save() {
    setBusy(true);
    try {
      const saved = await platformApi.patch<Plan>(`/platform/plans/${plan.code}`, {
        name: d.name,
        price_per_pos_month: Number(d.price),
        limits: Object.fromEntries(kinds.map((k) => [k, d.limits[k].trim() === "" ? null : Number(d.limits[k])])),
        features: d.features,
        ...(d.retention.trim() === "" ? { lifetime_retention: true } : { retention_days: Number(d.retention) }),
        trial_days: Number(d.trial),
        grace_days: Number(d.grace),
        active: d.active,
        reason: d.reason,
      });
      toast.success(`${saved.name} saved`);
      onSaved(saved);
      setD(toDraft(saved, kinds));
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not save the plan");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section aria-labelledby={id("h")} className="space-y-4 rounded-xl border bg-card p-5">
      <div className="flex flex-wrap items-center gap-3">
        <h2 id={id("h")} className="text-base font-semibold">
          {plan.name} <span className="text-xs font-normal text-muted-foreground">({plan.code})</span>
        </h2>
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={d.active} onChange={(e) => setD({ ...d, active: e.target.checked })} /> Offered to merchants
        </label>
      </div>
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor={id("name")}>Name</Label>
          <Input id={id("name")} value={d.name} onChange={(e) => setD({ ...d, name: e.target.value })} />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor={id("price")}>₹ per POS / month</Label>
          <Input id={id("price")} type="number" min={0} value={d.price} onChange={(e) => setD({ ...d, price: e.target.value })} />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor={id("trial")}>Trial days</Label>
          <Input id={id("trial")} type="number" min={0} value={d.trial} onChange={(e) => setD({ ...d, trial: e.target.value })} />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor={id("grace")}>Grace days</Label>
          <Input id={id("grace")} type="number" min={0} value={d.grace} onChange={(e) => setD({ ...d, grace: e.target.value })} />
        </div>
        {kinds.map((k) => (
          <div key={k} className="flex flex-col gap-1.5">
            <Label htmlFor={id(k)}>{LIMIT_LABEL[k] ?? k}</Label>
            <Input
              id={id(k)}
              type="number"
              min={0}
              placeholder="Unlimited"
              value={d.limits[k]}
              onChange={(e) => setD({ ...d, limits: { ...d.limits, [k]: e.target.value } })}
            />
          </div>
        ))}
        <div className="flex flex-col gap-1.5">
          <Label htmlFor={id("ret")}>Report history (days)</Label>
          <Input id={id("ret")} type="number" min={1} placeholder="Lifetime" value={d.retention} onChange={(e) => setD({ ...d, retention: e.target.value })} />
        </div>
      </div>
      <fieldset>
        <legend className="mb-2 text-sm font-medium">Features</legend>
        <div className="grid gap-1.5 sm:grid-cols-2 lg:grid-cols-3">
          {features.map((f) => (
            <label key={f} className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={d.features.includes(f)}
                onChange={(e) => setD({ ...d, features: e.target.checked ? [...d.features, f] : d.features.filter((x) => x !== f) })}
              />
              {FEATURE_LABEL[f] ?? f}
            </label>
          ))}
        </div>
      </fieldset>
      <div className="flex flex-wrap items-end gap-3">
        <div className="flex min-w-64 flex-1 flex-col gap-1.5">
          <Label htmlFor={id("reason")}>Reason (audited)</Label>
          <Input id={id("reason")} value={d.reason} onChange={(e) => setD({ ...d, reason: e.target.value })} />
        </div>
        <Button disabled={busy || !d.reason.trim()} onClick={save}>
          {busy && <Loader2 className="size-4 animate-spin" aria-hidden />}
          Save {plan.name}
        </Button>
      </div>
    </section>
  );
}

export default function PlansPage() {
  const [data, setData] = useState<{ plans: Plan[]; features: string[]; limit_kinds: string[] } | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    platformApi
      .get<{ plans: Plan[]; features: string[]; limit_kinds: string[] }>("/platform/plans")
      .then(setData)
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load plans"));
  }, []);

  return (
    <div className="space-y-4">
      <PageHeader
        title="Plans"
        description="What each plan costs and includes. Changes apply to every merchant on the plan; lowering a limit never deactivates anything, it stops new creation above it."
      />
      {error && <p className="text-sm text-destructive">{error}</p>}
      {data?.plans.map((p) => (
        <PlanEditor
          key={p.code}
          plan={p}
          features={data.features}
          kinds={data.limit_kinds}
          onSaved={(saved) => setData((x) => (x ? { ...x, plans: x.plans.map((q) => (q.code === saved.code ? saved : q)) } : x))}
        />
      ))}
    </div>
  );
}
