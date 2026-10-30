"use client";

// Phase 16 plan surfaces in the shell: the trial/grace/expired banner and
// the plan-locked preview a screen shows instead of its content when the
// merchant's plan doesn't include it (phased_roadmap.md: "lock icon +
// preview page, never a 404"). The server enforces both on its own.
import Link from "next/link";
import { Lock } from "lucide-react";
import { usePermissions } from "@/lib/permissions";
import { buttonVariants } from "@/components/ui/button";

export const FEATURE_LABEL: Record<string, string> = {
  basic_reports: "Basic reports",
  gst_compliance: "GST compliance and e-invoicing",
  advanced_reports: "Advanced reports and AI insights",
  custom_report_builder: "Custom report builder",
  loyalty_crm: "Full loyalty and CRM",
  api_access: "API access",
  payroll: "Payroll",
  multi_currency: "Multi-currency",
  white_label: "White-label",
  custom_integrations: "Custom integrations",
  price_suggestion: "Selling price suggestions",
};

const FEATURE_BLURB: Record<string, string> = {
  gst_compliance: "GSTR-1 and GSTR-3B returns, e-invoices (IRN) and e-way bills, prepared from your sales.",
  advanced_reports: "AI insights, anomaly alerts, demand forecasts and asking questions of your data in plain words.",
  custom_report_builder: "Build your own reports: pick fields, filters and groupings, save them and export.",
  payroll: "Salary structures, statutory deductions, payroll runs, payslips and challans.",
  price_suggestion: "A suggested selling price from each item's cost and MRP, by your own margin and rounding rules.",
};

// The plan that first includes a feature, for the preview's call to action.
const FIRST_PLAN: Record<string, string> = {
  gst_compliance: "Starter",
  advanced_reports: "Professional",
  custom_report_builder: "Professional",
  loyalty_crm: "Professional",
  api_access: "Professional",
  payroll: "Enterprise",
  price_suggestion: "Professional",
};

function days(iso: string | null) {
  if (!iso) return null;
  return Math.ceil((new Date(iso).getTime() - Date.now()) / 86_400_000);
}

function fmt(iso: string | null) {
  return iso ? new Date(iso).toLocaleDateString("en-IN", { day: "numeric", month: "short", year: "numeric" }) : "";
}

export function SubscriptionBanner() {
  const { entitlements: e, can } = usePermissions();
  if (!e) return null;
  const manage = can("subscription.manage");
  const action = manage ? (
    <Link href="/subscription" className="font-medium underline underline-offset-2">
      {e.read_only ? "Renew now" : "View subscription"}
    </Link>
  ) : (
    <span>Ask a Merchant Admin to renew.</span>
  );
  let text: string | null = null;
  let tone = "border-primary/30 bg-primary-soft text-foreground";
  if (e.status === "trial") {
    const d = days(e.trial_ends_at);
    if (d != null && d <= 7) text = `Your ${e.plan.name} trial ends in ${d} day${d === 1 ? "" : "s"} (${fmt(e.trial_ends_at)}).`;
  } else if (e.status === "grace") {
    tone = "border-warn/40 bg-warn-soft text-foreground";
    text = `Your ${e.plan.name} subscription is unpaid. The account becomes read-only on ${fmt(e.grace_ends_at)}.`;
  } else if (e.read_only) {
    tone = "border-destructive/40 bg-destructive-soft text-foreground";
    text = `Your subscription ${e.status === "cancelled" ? "was cancelled" : "has expired"}, so the account is read-only. Everything can still be viewed and exported.`;
  }
  if (!text) return null;
  return (
    <div role="status" className={`border-b px-4 py-2 text-sm md:px-6 ${tone}`}>
      {text} {action}
    </div>
  );
}

export function PlanLocked({ feature, title }: { feature: string; title: string }) {
  const { entitlements: e, can } = usePermissions();
  const plan = FIRST_PLAN[feature];
  return (
    <div className="mx-auto mt-10 max-w-xl rounded-xl border bg-card p-8 text-center">
      <div className="mx-auto mb-4 grid size-12 place-items-center rounded-full bg-muted">
        <Lock className="size-5 text-muted-foreground" aria-hidden />
      </div>
      <h1 className="text-lg font-semibold">{title}</h1>
      <p className="mt-2 text-sm text-muted-foreground">
        {FEATURE_BLURB[feature] ?? FEATURE_LABEL[feature] ?? feature}
      </p>
      <p className="mt-4 text-sm">
        {FEATURE_LABEL[feature] ?? feature} isn&apos;t part of your {e?.plan.name ?? "current"} plan
        {plan ? <>; it comes with {plan} and above.</> : "."}
      </p>
      <div className="mt-6">
        {can("subscription.manage") ? (
          <Link href="/subscription" className={buttonVariants()}>
            Compare plans
          </Link>
        ) : (
          <p className="text-sm text-muted-foreground">Ask a Merchant Admin to upgrade the plan.</p>
        )}
      </div>
    </div>
  );
}
