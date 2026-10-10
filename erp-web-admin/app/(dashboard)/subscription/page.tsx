"use client";

// Subscription & Billing (Phase 16, erp-core-go internal/subscriptions): the
// merchant's plan and status, usage against every limit, the plans on
// offer, and billing. Changing plan: a free plan switches at once; the
// first paid plan starts its trial; otherwise an invoice is raised and the
// plan switches when it's paid (through the payment provider, a stub
// today). A downgrade never deactivates anything; it only stops new
// creation above the new limits.
import { useCallback, useEffect, useState } from "react";
import { Check, Loader2 } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { rupees } from "@/lib/format";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import { Button } from "@/components/ui/button";
import { Modal } from "@/components/ui/dialog";
import { PageHeader } from "@/components/data/page-header";
import { StatusChip } from "@/components/data/status-chip";
import { FEATURE_LABEL } from "@/components/shell/plan";

type Plan = {
  code: string;
  name: string;
  price_per_pos_month: string;
  limits: Record<string, number | null>;
  features: string[];
  retention_days: number | null;
  trial_days: number;
  grace_days: number;
};
type Invoice = {
  invoice_id: string;
  invoice_number: string;
  kind: "renewal" | "plan_change" | "manual";
  plan_code: string;
  period_start: string;
  period_end: string;
  pos_count: number;
  unit_price: string;
  amount: string;
  status: "due" | "paid" | "void";
  paid_at: string | null;
  provider_ref: string | null;
};
type Summary = {
  plan: Plan;
  status: string;
  read_only: boolean;
  read_only_since: string | null;
  trial_ends_at: string | null;
  trial_used: boolean;
  current_period_end: string | null;
  grace_ends_at: string | null;
  usage: Record<string, { used: number; limit: number | null }>;
  billable_pos: number;
  invoices: Invoice[];
  plans: Plan[];
};

const LIMIT_LABEL: Record<string, string> = {
  branches: "Active branches",
  pos_terminals: "Active POS terminals",
  products: "Active products",
  transactions_per_month: "Sales this month",
};
const LIMIT_ORDER = ["branches", "pos_terminals", "products", "transactions_per_month"];

const STATUS: Record<string, { label: string; tone: "ok" | "info" | "warn" | "bad" | "muted" }> = {
  active: { label: "Active", tone: "ok" },
  trial: { label: "Trial", tone: "info" },
  grace: { label: "Payment due", tone: "warn" },
  expired: { label: "Expired: read-only", tone: "bad" },
  cancelled: { label: "Cancelled: read-only", tone: "muted" },
};

function d(iso: string | null) {
  return iso ? new Date(iso).toLocaleDateString("en-IN", { day: "numeric", month: "short", year: "numeric" }) : "—";
}

function retention(days: number | null) {
  if (days == null) return "Lifetime report history";
  if (days % 365 === 0) return `${days / 365}-year report history`;
  return `${days}-day report history`;
}

export default function SubscriptionPage() {
  const { can, refreshEntitlements } = usePermissions();
  const allowed = can("subscription.manage");
  const [s, setS] = useState<Summary | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [confirm, setConfirm] = useState<Plan | null>(null);
  const [busy, setBusy] = useState<string | null>(null);

  const load = useCallback(
    () =>
      api
        .get<Summary>("/api/v1/subscription")
        .then(setS)
        .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load the subscription")),
    [],
  );
  useEffect(() => {
    load();
  }, [load]);

  async function pay(inv: Invoice) {
    setBusy(inv.invoice_id);
    try {
      await api.post(`/api/v1/subscription/invoices/${inv.invoice_id}/pay`, {});
      toast.success(`${inv.invoice_number} paid`);
      await load();
      refreshEntitlements();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not take the payment");
    } finally {
      setBusy(null);
    }
  }

  async function change(plan: Plan) {
    setBusy(plan.code);
    try {
      const r = await api.post<{ outcome: string; invoice?: Invoice }>("/api/v1/subscription/change", { plan_code: plan.code });
      setConfirm(null);
      if (r.outcome === "trial_started") toast.success(`${plan.name} trial started`);
      else if (r.outcome === "switched") toast.success(`Now on ${plan.name}`);
      else toast.message(`Invoice ${r.invoice?.invoice_number} raised: ${plan.name} starts when it's paid`);
      await load();
      refreshEntitlements();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not change the plan");
    } finally {
      setBusy(null);
    }
  }

  if (!allowed) {
    return (
      <div className="space-y-4">
        <PageHeader title="Subscription & Billing" />
        <p className="text-sm text-muted-foreground">{missingPermissionHint("subscription.manage")}</p>
      </div>
    );
  }
  if (!s) {
    return (
      <div className="space-y-4">
        <PageHeader title="Subscription & Billing" />
        {error ? (
          <p className="text-sm text-destructive">{error}</p>
        ) : (
          <p className="flex items-center gap-2 text-sm text-muted-foreground">
            <Loader2 className="size-4 animate-spin" aria-hidden /> Loading…
          </p>
        )}
      </div>
    );
  }

  const st = STATUS[s.status] ?? { label: s.status, tone: "muted" as const };
  const due = s.invoices.filter((i) => i.status === "due");
  const lapsed = s.read_only || s.status === "grace";
  const monthly = Number(s.plan.price_per_pos_month) * s.billable_pos;

  function actionFor(p: Plan): { label: string; disabled: boolean } {
    if (p.code === s!.plan.code) return lapsed ? { label: "Renew", disabled: false } : { label: "Current plan", disabled: true };
    const free = Number(p.price_per_pos_month) === 0;
    if (!free && !s!.trial_used && Number(s!.plan.price_per_pos_month) === 0 && !s!.read_only && p.trial_days > 0)
      return { label: `Start ${p.trial_days}-day trial`, disabled: false };
    const up = Number(p.price_per_pos_month) > Number(s!.plan.price_per_pos_month);
    return { label: up ? `Upgrade to ${p.name}` : `Switch to ${p.name}`, disabled: false };
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="Subscription & Billing"
        description="Your plan, what it includes and how much of it is in use. Changing plan never deletes or deactivates anything."
      />

      <section className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.3fr)]">
        <div className="rounded-xl border bg-card p-5">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="text-lg font-semibold">{s.plan.name}</h2>
            <StatusChip status={s.status} tone={st.tone} label={st.label} />
          </div>
          <dl className="mt-4 grid grid-cols-2 gap-x-4 gap-y-2 text-sm">
            {s.status === "trial" && (
              <>
                <dt className="text-muted-foreground">Trial ends</dt>
                <dd>{d(s.trial_ends_at)}</dd>
              </>
            )}
            {s.status === "active" && (
              <>
                <dt className="text-muted-foreground">Renews</dt>
                <dd>{s.current_period_end ? d(s.current_period_end) : "No expiry"}</dd>
              </>
            )}
            {s.status === "grace" && (
              <>
                <dt className="text-muted-foreground">Read-only from</dt>
                <dd>{d(s.grace_ends_at)}</dd>
              </>
            )}
            {s.read_only && (
              <>
                <dt className="text-muted-foreground">Read-only since</dt>
                <dd>{d(s.read_only_since)}</dd>
              </>
            )}
            <dt className="text-muted-foreground">Price</dt>
            <dd>
              {Number(s.plan.price_per_pos_month) === 0
                ? "Free"
                : `${rupees(s.plan.price_per_pos_month)} per POS a month × ${s.billable_pos} = ${rupees(monthly)}`}
            </dd>
            <dt className="text-muted-foreground">History</dt>
            <dd>{retention(s.plan.retention_days)}</dd>
          </dl>
          {s.plan.retention_days != null && (
            <p className="mt-3 text-xs text-muted-foreground">
              Reports reach back {s.plan.retention_days} days. Invoices, ledgers, GST returns and the audit log are always kept and exportable in
              full.
            </p>
          )}
          {due.length > 0 && (
            <div className="mt-4 space-y-2 rounded-lg border border-warn/40 bg-warn-soft p-3">
              {due.map((i) => (
                <div key={i.invoice_id} className="flex flex-wrap items-center gap-2 text-sm">
                  <span className="flex-1">
                    {i.invoice_number}: {rupees(i.amount)} due ({i.kind === "renewal" ? "renewal" : `move to ${i.plan_code}`})
                  </span>
                  <Button size="sm" disabled={busy === i.invoice_id} onClick={() => pay(i)}>
                    {busy === i.invoice_id && <Loader2 className="size-4 animate-spin" aria-hidden />}
                    Pay {rupees(i.amount)}
                  </Button>
                </div>
              ))}
            </div>
          )}
        </div>

        <div className="rounded-xl border bg-card p-5">
          <h2 className="text-sm font-semibold">Usage</h2>
          <ul className="mt-3 space-y-3">
            {LIMIT_ORDER.map((k) => {
              const u = s.usage[k];
              if (!u) return null;
              const pct = u.limit ? Math.min(100, Math.round((u.used / u.limit) * 100)) : 0;
              const over = u.limit != null && u.used >= u.limit;
              return (
                <li key={k} className="text-sm">
                  <div className="flex justify-between gap-2">
                    <span>{LIMIT_LABEL[k]}</span>
                    <span className={over ? "font-medium text-destructive" : "text-muted-foreground"}>
                      {u.used} {u.limit == null ? "· unlimited" : `of ${u.limit}`}
                    </span>
                  </div>
                  {u.limit != null && (
                    <div
                      className="mt-1 h-1.5 rounded-full bg-muted"
                      role="progressbar"
                      aria-label={LIMIT_LABEL[k]}
                      aria-valuenow={u.used}
                      aria-valuemin={0}
                      aria-valuemax={u.limit}
                    >
                      <div className={`h-1.5 rounded-full ${over ? "bg-destructive" : "bg-primary"}`} style={{ width: `${pct}%` }} />
                    </div>
                  )}
                  {over && <p className="mt-1 text-xs text-muted-foreground">At the limit: nothing more can be added until you upgrade or deactivate one.</p>}
                </li>
              );
            })}
          </ul>
        </div>
      </section>

      <section aria-labelledby="plans-h">
        <h2 id="plans-h" className="mb-3 text-sm font-semibold">
          Plans
        </h2>
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
          {s.plans.map((p) => {
            const a = actionFor(p);
            const current = p.code === s.plan.code;
            return (
              <div key={p.code} className={`flex flex-col rounded-xl border bg-card p-4 ${current ? "border-primary" : ""}`}>
                <h3 className="font-semibold">{p.name}</h3>
                <p className="mt-1 text-sm">
                  {Number(p.price_per_pos_month) === 0 ? "Free" : `${rupees(p.price_per_pos_month)} per POS / month`}
                </p>
                <ul className="mt-3 flex-1 space-y-1 text-xs text-muted-foreground">
                  {LIMIT_ORDER.map((k) => (
                    <li key={k}>
                      {p.limits[k] == null ? "Unlimited" : p.limits[k]} {LIMIT_LABEL[k].toLowerCase()}
                    </li>
                  ))}
                  <li>{retention(p.retention_days)}</li>
                  {p.features
                    .filter((f) => f !== "basic_reports")
                    .map((f) => (
                      <li key={f} className="flex items-center gap-1 text-foreground">
                        <Check className="size-3 text-pay" aria-hidden /> {FEATURE_LABEL[f] ?? f}
                      </li>
                    ))}
                </ul>
                <Button className="mt-4" variant={current ? "outline" : "default"} disabled={a.disabled} onClick={() => setConfirm(p)}>
                  {a.label}
                </Button>
              </div>
            );
          })}
        </div>
      </section>

      <section aria-labelledby="inv-h" className="rounded-xl border bg-card">
        <h2 id="inv-h" className="border-b px-4 py-3 text-sm font-semibold">
          Billing history
        </h2>
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="text-left text-xs text-muted-foreground">
              <tr>
                <th className="px-4 py-2 font-medium">Invoice</th>
                <th className="px-4 py-2 font-medium">Plan</th>
                <th className="px-4 py-2 font-medium">Period</th>
                <th className="px-4 py-2 text-right font-medium">Amount</th>
                <th className="px-4 py-2 font-medium">Status</th>
              </tr>
            </thead>
            <tbody>
              {s.invoices.map((i) => (
                <tr key={i.invoice_id} className="border-t">
                  <td className="px-4 py-2 font-medium">{i.invoice_number}</td>
                  <td className="px-4 py-2">{i.plan_code}</td>
                  <td className="px-4 py-2">
                    {d(i.period_start)} – {d(i.period_end)}
                  </td>
                  <td className="tabular px-4 py-2 text-right">
                    {rupees(i.amount)}
                    <span className="block text-xs text-muted-foreground">
                      {i.pos_count} × {rupees(i.unit_price)}
                    </span>
                  </td>
                  <td className="px-4 py-2">
                    <StatusChip status={i.status} label={i.status === "due" ? "Due" : i.status === "paid" ? `Paid ${d(i.paid_at)}` : "Void"} />
                  </td>
                </tr>
              ))}
              {s.invoices.length === 0 && (
                <tr>
                  <td colSpan={5} className="px-4 py-6 text-center text-muted-foreground">
                    No invoices yet.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>

      {confirm && (
        <Modal
          open
          onOpenChange={(o) => !o && setConfirm(null)}
          title={actionFor(confirm).label}
          footer={
            <>
              <Button variant="outline" onClick={() => setConfirm(null)}>
                Cancel
              </Button>
              <Button disabled={busy === confirm.code} onClick={() => change(confirm)}>
                {busy === confirm.code && <Loader2 className="size-4 animate-spin" aria-hidden />}
                Confirm
              </Button>
            </>
          }
        >
          <p className="text-sm">
            {Number(confirm.price_per_pos_month) === 0
              ? `You'll move to ${confirm.name} now. Nothing is deactivated; you won't be able to add more than its limits allow.`
              : actionFor(confirm).label.startsWith("Start")
                ? `${confirm.name} free for ${confirm.trial_days} days. Pay before it ends to keep it.`
                : `An invoice for ${rupees(Number(confirm.price_per_pos_month) * s.billable_pos)} (${s.billable_pos} POS × ${rupees(confirm.price_per_pos_month)}) is raised; ${confirm.name} starts when it's paid.`}
          </p>
        </Modal>
      )}
    </div>
  );
}
