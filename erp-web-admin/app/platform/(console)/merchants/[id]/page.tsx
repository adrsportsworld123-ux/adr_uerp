"use client";

// One merchant in the console: subscription, usage, invoices, and the
// lifecycle controls (plan, status, trial / period / grace dates). Every
// change needs a reason and is written to both the platform audit log and
// the merchant's own audit log.
import { FormEvent, useCallback, useEffect, useState } from "react";
import { useParams } from "next/navigation";
import { Loader2 } from "lucide-react";
import { toast } from "sonner";
import { ApiError } from "@/lib/api-client";
import { rupees } from "@/lib/format";
import { Invoice, Plan, platformApi, SUB_STATUS, Usage } from "@/lib/platform-api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Modal } from "@/components/ui/dialog";
import { PageHeader } from "@/components/data/page-header";
import { StatusChip, Tone } from "@/components/data/status-chip";

type Detail = {
  merchant: { merchant_id: string; code: string; legal_name: string; trade_name: string | null; created_at: string };
  plan: Plan;
  status: string;
  stored_status: string;
  read_only: boolean;
  read_only_since: string | null;
  trial_ends_at: string | null;
  current_period_end: string | null;
  grace_ends_at: string | null;
  notes: string | null;
  usage: Usage;
  billable_pos: number;
  invoices: Invoice[];
};

const LIMIT_LABEL: Record<string, string> = {
  branches: "Active branches",
  pos_terminals: "Active tills",
  products: "Active products",
  transactions_per_month: "Sales this month",
};

const d = (iso: string | null) => (iso ? new Date(iso).toLocaleString("en-IN", { dateStyle: "medium", timeStyle: "short" }) : "—");
// <input type="date"> value from an ISO time, and back (end of that day, IST).
const toDate = (iso: string | null) => (iso ? new Date(new Date(iso).getTime() + 5.5 * 3600_000).toISOString().slice(0, 10) : "");
const fromDate = (v: string) => (v ? new Date(`${v}T23:59:59+05:30`).toISOString() : null);

export default function MerchantDetailPage() {
  const { id } = useParams<{ id: string }>();
  const [m, setM] = useState<Detail | null>(null);
  const [plans, setPlans] = useState<Plan[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [form, setForm] = useState({ plan_code: "", status: "", trial_ends_at: "", current_period_end: "", no_expiry: false, notes: "", reason: "" });
  const [saving, setSaving] = useState(false);
  const [invAction, setInvAction] = useState<{ inv: Invoice; action: "mark-paid" | "void" } | null>(null);
  const [invRef, setInvRef] = useState("");
  const [invReason, setInvReason] = useState("");

  const load = useCallback(() => {
    platformApi
      .get<Detail>(`/platform/merchants/${id}`)
      .then((x) => {
        setM(x);
        setForm({
          plan_code: x.plan.code,
          status: x.stored_status,
          trial_ends_at: toDate(x.trial_ends_at),
          current_period_end: toDate(x.current_period_end),
          no_expiry: x.current_period_end == null,
          notes: x.notes ?? "",
          reason: "",
        });
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load the merchant"));
  }, [id]);

  useEffect(() => {
    load();
    platformApi
      .get<{ plans: Plan[] }>("/platform/plans")
      .then((x) => setPlans(x.plans))
      .catch(() => {});
  }, [load]);

  async function save(e: FormEvent) {
    e.preventDefault();
    if (!m) return;
    setSaving(true);
    const body: Record<string, unknown> = { reason: form.reason, notes: form.notes };
    if (form.plan_code !== m.plan.code) body.plan_code = form.plan_code;
    if (form.status !== m.stored_status) body.status = form.status;
    if (form.trial_ends_at !== toDate(m.trial_ends_at) && form.trial_ends_at) body.trial_ends_at = fromDate(form.trial_ends_at);
    if (form.no_expiry && m.current_period_end) body.clear_period_end = true;
    else if (!form.no_expiry && form.current_period_end && form.current_period_end !== toDate(m.current_period_end))
      body.current_period_end = fromDate(form.current_period_end);
    try {
      await platformApi.patch(`/platform/merchants/${id}/subscription`, body);
      toast.success("Subscription updated");
      load();
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : "Could not update");
    } finally {
      setSaving(false);
    }
  }

  async function runInvoiceAction() {
    if (!invAction) return;
    try {
      await platformApi.post(`/platform/merchants/${id}/invoices/${invAction.inv.invoice_id}/${invAction.action}`, {
        reference: invRef,
        reason: invReason,
      });
      toast.success(invAction.action === "void" ? "Invoice voided" : "Payment recorded");
      setInvAction(null);
      setInvRef("");
      setInvReason("");
      load();
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : "Could not update the invoice");
    }
  }

  if (!m) return <p className="text-sm text-muted-foreground">{error ?? "Loading…"}</p>;
  const st = SUB_STATUS[m.status] ?? { label: m.status, tone: "muted" as Tone };

  return (
    <div className="space-y-6">
      <PageHeader
        title={m.merchant.trade_name || m.merchant.legal_name}
        description={
          <>
            {m.merchant.code} · {m.plan.name} · <StatusChip status={m.status} tone={st.tone} label={st.label} />
            {m.read_only && <> · read-only since {d(m.read_only_since)}</>}
          </>
        }
      />

      <section className="grid gap-4 lg:grid-cols-2">
        <div className="rounded-xl border bg-card p-5">
          <h2 className="text-sm font-semibold">Lifecycle</h2>
          <dl className="mt-3 grid grid-cols-2 gap-y-1.5 text-sm">
            <dt className="text-muted-foreground">Trial ends</dt>
            <dd>{d(m.trial_ends_at)}</dd>
            <dt className="text-muted-foreground">Period ends</dt>
            <dd>{m.current_period_end ? d(m.current_period_end) : "No expiry"}</dd>
            <dt className="text-muted-foreground">Grace ends</dt>
            <dd>{d(m.grace_ends_at)}</dd>
            <dt className="text-muted-foreground">Billable tills</dt>
            <dd>
              {m.billable_pos} × {rupees(m.plan.price_per_pos_month)}
            </dd>
          </dl>
        </div>
        <div className="rounded-xl border bg-card p-5">
          <h2 className="text-sm font-semibold">Usage</h2>
          <ul className="mt-3 space-y-1.5 text-sm">
            {Object.entries(LIMIT_LABEL).map(([k, label]) => (
              <li key={k} className="flex justify-between">
                <span>{label}</span>
                <span className="tabular">
                  {m.usage[k]?.used ?? 0} {m.usage[k]?.limit == null ? "· unlimited" : `of ${m.usage[k]?.limit}`}
                </span>
              </li>
            ))}
          </ul>
        </div>
      </section>

      <form onSubmit={save} className="grid gap-4 rounded-xl border bg-card p-5 sm:grid-cols-2 lg:grid-cols-3">
        <h2 className="text-sm font-semibold sm:col-span-2 lg:col-span-3">Change the subscription</h2>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="sub-plan">Plan</Label>
          <select id="sub-plan" value={form.plan_code} onChange={(e) => setForm({ ...form, plan_code: e.target.value })} className="h-9 rounded-lg border bg-card px-2 text-sm">
            {plans.map((p) => (
              <option key={p.code} value={p.code}>
                {p.name}
                {p.active ? "" : " (retired)"}
              </option>
            ))}
          </select>
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="sub-status">Status</Label>
          <select id="sub-status" value={form.status} onChange={(e) => setForm({ ...form, status: e.target.value })} className="h-9 rounded-lg border bg-card px-2 text-sm">
            {Object.entries(SUB_STATUS).map(([k, v]) => (
              <option key={k} value={k}>
                {v.label}
              </option>
            ))}
          </select>
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="sub-trial">Trial ends</Label>
          <Input id="sub-trial" type="date" value={form.trial_ends_at} onChange={(e) => setForm({ ...form, trial_ends_at: e.target.value })} />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="sub-period">Period ends</Label>
          <Input
            id="sub-period"
            type="date"
            disabled={form.no_expiry}
            value={form.current_period_end}
            onChange={(e) => setForm({ ...form, current_period_end: e.target.value })}
          />
          <label className="flex items-center gap-2 text-xs">
            <input type="checkbox" checked={form.no_expiry} onChange={(e) => setForm({ ...form, no_expiry: e.target.checked })} /> No expiry
          </label>
        </div>
        <div className="flex flex-col gap-1.5 lg:col-span-2">
          <Label htmlFor="sub-notes">Notes</Label>
          <Input id="sub-notes" value={form.notes} onChange={(e) => setForm({ ...form, notes: e.target.value })} />
        </div>
        <div className="flex flex-col gap-1.5 sm:col-span-2">
          <Label htmlFor="sub-reason">Reason (required, audited)</Label>
          <Input id="sub-reason" required value={form.reason} onChange={(e) => setForm({ ...form, reason: e.target.value })} />
        </div>
        <div className="flex items-end">
          <Button type="submit" disabled={saving || !form.reason.trim()}>
            {saving && <Loader2 className="size-4 animate-spin" aria-hidden />}
            Save changes
          </Button>
        </div>
      </form>

      <section className="rounded-xl border bg-card">
        <h2 className="border-b px-4 py-3 text-sm font-semibold">Invoices</h2>
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="text-left text-xs text-muted-foreground">
              <tr>
                <th className="px-4 py-2 font-medium">Invoice</th>
                <th className="px-4 py-2 font-medium">Kind</th>
                <th className="px-4 py-2 font-medium">Plan</th>
                <th className="px-4 py-2 text-right font-medium">Amount</th>
                <th className="px-4 py-2 font-medium">Status</th>
                <th className="px-4 py-2" />
              </tr>
            </thead>
            <tbody>
              {m.invoices.map((i) => (
                <tr key={i.invoice_id} className="border-t">
                  <td className="px-4 py-2 font-medium">{i.invoice_number}</td>
                  <td className="px-4 py-2">{i.kind.replace("_", " ")}</td>
                  <td className="px-4 py-2">{i.plan_code}</td>
                  <td className="tabular px-4 py-2 text-right">{rupees(i.amount)}</td>
                  <td className="px-4 py-2">
                    <StatusChip status={i.status} label={i.status === "paid" ? `Paid (${i.provider_ref ?? i.provider})` : i.status === "due" ? "Due" : "Void"} />
                  </td>
                  <td className="px-4 py-2 text-right">
                    {i.status === "due" && (
                      <div className="flex justify-end gap-2">
                        <Button size="sm" variant="outline" onClick={() => setInvAction({ inv: i, action: "mark-paid" })}>
                          Record payment
                        </Button>
                        <Button size="sm" variant="ghost" onClick={() => setInvAction({ inv: i, action: "void" })}>
                          Void
                        </Button>
                      </div>
                    )}
                  </td>
                </tr>
              ))}
              {m.invoices.length === 0 && (
                <tr>
                  <td colSpan={6} className="px-4 py-6 text-center text-muted-foreground">
                    No invoices.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>

      {invAction && (
        <Modal
          open
          onOpenChange={(o) => !o && setInvAction(null)}
          title={invAction.action === "void" ? `Void ${invAction.inv.invoice_number}` : `Record payment for ${invAction.inv.invoice_number}`}
          footer={
            <>
              <Button variant="outline" onClick={() => setInvAction(null)}>
                Cancel
              </Button>
              <Button disabled={!invReason.trim() || (invAction.action === "mark-paid" && !invRef.trim())} onClick={runInvoiceAction}>
                {invAction.action === "void" ? "Void" : "Record payment"}
              </Button>
            </>
          }
        >
          <div className="space-y-3">
            {invAction.action === "mark-paid" && (
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="inv-ref">Payment reference (UTR, cheque no.)</Label>
                <Input id="inv-ref" value={invRef} onChange={(e) => setInvRef(e.target.value)} />
              </div>
            )}
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="inv-reason">Reason</Label>
              <Input id="inv-reason" value={invReason} onChange={(e) => setInvReason(e.target.value)} />
            </div>
          </div>
        </Modal>
      )}
    </div>
  );
}
