"use client";

// Onboard a merchant: one request sets up the merchant, its chart of
// accounts and roles, the first branch, the owner's Merchant Admin login
// and the subscription (POST /platform/merchants).
import { FormEvent, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { Loader2 } from "lucide-react";
import { toast } from "sonner";
import { ApiError } from "@/lib/api-client";
import { Plan, platformApi } from "@/lib/platform-api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { PageHeader } from "@/components/data/page-header";

function Field({ id, label, children, hint }: { id: string; label: string; children: React.ReactNode; hint?: string }) {
  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      {children}
      {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
    </div>
  );
}

export default function NewMerchantPage() {
  const router = useRouter();
  const [plans, setPlans] = useState<Plan[]>([]);
  const [f, setF] = useState({
    code: "",
    legal_name: "",
    trade_name: "",
    gstin: "",
    plan_code: "freemium",
    start_trial: false,
    branch_name: "Main",
    branch_code: "MAIN",
    admin_name: "",
    admin_email: "",
    admin_code: "",
    admin_password: "",
    reason: "",
  });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    platformApi
      .get<{ plans: Plan[] }>("/platform/plans")
      .then((d) => setPlans(d.plans.filter((p) => p.active)))
      .catch(() => {});
  }, []);

  const set = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
    setF((x) => ({ ...x, [k]: e.target.type === "checkbox" ? (e.target as HTMLInputElement).checked : e.target.value }));
  const plan = plans.find((p) => p.code === f.plan_code);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const r = await platformApi.post<{ merchant_id: string }>("/platform/merchants", {
        code: f.code,
        legal_name: f.legal_name,
        trade_name: f.trade_name,
        gstin: f.gstin,
        plan_code: f.plan_code,
        start_trial: f.start_trial,
        branch: { name: f.branch_name, code: f.branch_code },
        admin: { name: f.admin_name, email: f.admin_email, employee_code: f.admin_code, password: f.admin_password },
        reason: f.reason,
      });
      toast.success(`${f.code} onboarded`);
      router.push(`/platform/merchants/${r.merchant_id}`);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Could not onboard the merchant");
      setBusy(false);
    }
  }

  return (
    <div className="max-w-3xl space-y-4">
      <PageHeader title="Onboard a merchant" description="Sets up the account, its first branch and its owner's sign-in in one step." />
      <form onSubmit={submit} className="space-y-6">
        <fieldset className="grid gap-4 rounded-xl border bg-card p-5 sm:grid-cols-2">
          <legend className="px-1 text-sm font-semibold">Business</legend>
          <Field id="m-code" label="Merchant code" hint="What staff type at sign-in: lower-case letters, digits, hyphens.">
            <Input id="m-code" required value={f.code} onChange={set("code")} />
          </Field>
          <Field id="m-legal" label="Legal name">
            <Input id="m-legal" required value={f.legal_name} onChange={set("legal_name")} />
          </Field>
          <Field id="m-trade" label="Trade name">
            <Input id="m-trade" value={f.trade_name} onChange={set("trade_name")} />
          </Field>
          <Field id="m-gstin" label="GSTIN">
            <Input id="m-gstin" value={f.gstin} onChange={set("gstin")} />
          </Field>
          <Field id="m-branch" label="First branch name">
            <Input id="m-branch" required value={f.branch_name} onChange={set("branch_name")} />
          </Field>
          <Field id="m-branch-code" label="First branch code">
            <Input id="m-branch-code" required value={f.branch_code} onChange={set("branch_code")} />
          </Field>
        </fieldset>
        <fieldset className="grid gap-4 rounded-xl border bg-card p-5 sm:grid-cols-2">
          <legend className="px-1 text-sm font-semibold">Owner (Merchant Admin)</legend>
          <Field id="a-name" label="Name">
            <Input id="a-name" required value={f.admin_name} onChange={set("admin_name")} />
          </Field>
          <Field id="a-email" label="Email">
            <Input id="a-email" type="email" required value={f.admin_email} onChange={set("admin_email")} />
          </Field>
          <Field id="a-code" label="Employee code" hint="Used to approve at the till.">
            <Input id="a-code" value={f.admin_code} onChange={set("admin_code")} />
          </Field>
          <Field id="a-pw" label="First password" hint="8 to 72 characters; they change it after signing in.">
            <Input id="a-pw" type="password" autoComplete="new-password" required minLength={8} value={f.admin_password} onChange={set("admin_password")} />
          </Field>
        </fieldset>
        <fieldset className="grid gap-4 rounded-xl border bg-card p-5 sm:grid-cols-2">
          <legend className="px-1 text-sm font-semibold">Subscription</legend>
          <Field id="s-plan" label="Plan">
            <select id="s-plan" value={f.plan_code} onChange={set("plan_code")} className="h-9 rounded-lg border bg-card px-2 text-sm">
              {plans.map((p) => (
                <option key={p.code} value={p.code}>
                  {p.name}
                </option>
              ))}
            </select>
          </Field>
          <label className="flex items-center gap-2 self-end pb-2 text-sm">
            <input type="checkbox" checked={f.start_trial} disabled={!plan || plan.trial_days === 0} onChange={set("start_trial")} />
            {plan && plan.trial_days > 0 ? `Start with the ${plan.trial_days}-day trial` : "No trial on this plan"}
          </label>
          <p className="text-xs text-muted-foreground sm:col-span-2">
            Without a trial the plan is active with no expiry (a sales-led deal); set a period end on the merchant page to bill monthly.
          </p>
          <Field id="s-reason" label="Reason (for the audit log)">
            <Input id="s-reason" required value={f.reason} onChange={set("reason")} />
          </Field>
        </fieldset>
        {error && (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        )}
        <Button type="submit" disabled={busy}>
          {busy && <Loader2 className="size-4 animate-spin" aria-hidden />}
          Onboard
        </Button>
      </form>
    </div>
  );
}
