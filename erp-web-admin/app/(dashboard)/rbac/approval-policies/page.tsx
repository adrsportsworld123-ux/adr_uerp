"use client";

// Approval policies (Phase 15, erp-core-go internal/approvals): who has to
// approve, with their employee code and PIN, a manual discount, a refund, a
// below-cost price, a stock adjustment and a journal voucher. One policy per
// merchant; the defaults are what the system did before policies existed.
// An approver can only approve inside their own data scope, so a Branch
// Manager of one store can't approve a refund in another.
import { useEffect, useState } from "react";
import { Loader2, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/data/page-header";

import { currencySymbol } from "@/lib/currency";
type Level = "none" | "any" | "branch_manager" | "merchant_admin";
type Tier = { up_to: number; approver: Level };
type Policy = {
  discount_tiers: Tier[];
  refund_approval_above: number | null;
  refund_approver: Level;
  below_cost_price: Level;
  stock_adjust_approval_above: number | null;
  stock_adjust_approver: Level;
  jv_approval_above: number | null;
};

const LEVEL_LABEL: Record<Level, string> = {
  none: "No approval",
  any: "Anyone who can manage prices",
  branch_manager: "Branch Manager or Merchant Admin",
  merchant_admin: "Merchant Admin only",
};

// Form state keeps numbers as strings so a half-typed value isn't lost.
type TierDraft = { up_to: string; approver: Level };
type Draft = {
  tiers: TierDraft[];
  refundAll: boolean;
  refundAbove: string;
  refundApprover: Level;
  belowCost: Level;
  stockOn: boolean;
  stockAbove: string;
  stockApprover: Level;
  jvAll: boolean;
  jvAbove: string;
};

function toDraft(p: Policy): Draft {
  return {
    tiers: p.discount_tiers.map((t) => ({ up_to: String(t.up_to), approver: t.approver })),
    refundAll: p.refund_approval_above == null,
    refundAbove: p.refund_approval_above == null ? "1000" : String(p.refund_approval_above),
    refundApprover: p.refund_approver,
    belowCost: p.below_cost_price,
    stockOn: p.stock_adjust_approval_above != null,
    stockAbove: p.stock_adjust_approval_above == null ? "5000" : String(p.stock_adjust_approval_above),
    stockApprover: p.stock_adjust_approver,
    jvAll: p.jv_approval_above == null,
    jvAbove: p.jv_approval_above == null ? "10000" : String(p.jv_approval_above),
  };
}

function amount(v: string) {
  const n = Number(v);
  return v.trim() !== "" && Number.isFinite(n) && n >= 0 ? n : null;
}

// fromDraft returns the policy, or a message saying what's wrong with the form.
function fromDraft(d: Draft): Policy | string {
  if (d.tiers.length === 0) return "Add at least one discount tier";
  let prev = 0;
  const tiers: Tier[] = [];
  for (const [i, t] of d.tiers.entries()) {
    const n = Number(t.up_to);
    if (t.up_to.trim() === "" || !Number.isFinite(n) || n <= prev || n > 100)
      return `Discount tier ${i + 1}: each tier must be above the one before and at most 100%`;
    tiers.push({ up_to: n, approver: t.approver });
    prev = n;
  }
  const refund = d.refundAll ? null : amount(d.refundAbove);
  if (!d.refundAll && refund == null) return "Enter the refund amount above which approval is needed";
  const stock = d.stockOn ? amount(d.stockAbove) : null;
  if (d.stockOn && stock == null) return "Enter the stock adjustment value above which approval is needed";
  const jv = d.jvAll ? null : amount(d.jvAbove);
  if (!d.jvAll && jv == null) return "Enter the voucher total above which approval is needed";
  return {
    discount_tiers: tiers,
    refund_approval_above: refund,
    refund_approver: d.refundApprover,
    below_cost_price: d.belowCost,
    stock_adjust_approval_above: stock,
    stock_adjust_approver: d.stockApprover,
    jv_approval_above: jv,
  };
}

function LevelSelect({ id, value, onChange, levels }: { id: string; value: Level; onChange: (l: Level) => void; levels: Level[] }) {
  return (
    <select id={id} value={value} onChange={(e) => onChange(e.target.value as Level)} className="h-8 rounded-md border bg-card px-1.5 text-sm">
      {levels.map((l) => (
        <option key={l} value={l}>
          {LEVEL_LABEL[l]}
        </option>
      ))}
    </select>
  );
}

const APPROVERS: Level[] = ["branch_manager", "merchant_admin"];

export default function ApprovalPoliciesPage() {
  const { can } = usePermissions();
  const allowed = can("approvals.manage");
  const [saved, setSaved] = useState<Policy | null>(null);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api
      .get<Policy>("/api/v1/settings/approval-policies")
      .then((p) => {
        setSaved(p);
        setDraft(toDraft(p));
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load the approval policies"));
  }, []);

  const set = (patch: Partial<Draft>) => setDraft((d) => (d ? { ...d, ...patch } : d));
  const setTier = (i: number, patch: Partial<TierDraft>) =>
    setDraft((d) => (d ? { ...d, tiers: d.tiers.map((t, j) => (j === i ? { ...t, ...patch } : t)) } : d));

  const next = draft ? fromDraft(draft) : null;
  const invalid = typeof next === "string" ? next : null;
  const dirty = saved != null && next != null && typeof next !== "string" && JSON.stringify(next) !== JSON.stringify(saved);

  async function save() {
    if (!next || typeof next === "string") return;
    setSaving(true);
    try {
      const p = await api.put<Policy>("/api/v1/settings/approval-policies", next);
      setSaved(p);
      setDraft(toDraft(p));
      toast.success("Approval policies saved");
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not save the approval policies");
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="Approval Policies"
        description="Who approves discounts, refunds, below-cost prices, stock adjustments and journal vouchers. The approver enters their employee code and PIN, and can only approve in branches they have access to."
      />
      {error && <p className="text-sm text-destructive">{error}</p>}
      {draft == null && !error ? (
        <p className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="size-4 animate-spin" aria-hidden /> Loading…
        </p>
      ) : draft != null ? (
        <div className="max-w-3xl space-y-6">
          <fieldset disabled={!allowed} className="space-y-6">
            <Card>
              <CardHeader>
                <CardTitle>Manual discounts</CardTitle>
                <CardDescription>
                  A discount up to each limit needs that tier&apos;s approver. Anything above the last tier is refused.
                </CardDescription>
              </CardHeader>
              <CardContent className="space-y-3">
                <ol className="space-y-2" aria-label="Discount tiers">
                  {draft.tiers.map((t, i) => (
                    <li key={i} className="flex flex-wrap items-center gap-2 text-sm">
                      <label htmlFor={`tier-${i}-upto`}>Up to</label>
                      <Input
                        id={`tier-${i}-upto`}
                        type="number"
                        min={0}
                        max={100}
                        step="0.5"
                        className="h-8 w-20"
                        value={t.up_to}
                        onChange={(e) => setTier(i, { up_to: e.target.value })}
                      />
                      <span>%:</span>
                      <label htmlFor={`tier-${i}-approver`} className="sr-only">
                        Approver for tier {i + 1}
                      </label>
                      <LevelSelect
                        id={`tier-${i}-approver`}
                        value={t.approver}
                        onChange={(l) => setTier(i, { approver: l })}
                        levels={["none", ...APPROVERS]}
                      />
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label={`Remove tier ${i + 1}`}
                        disabled={draft.tiers.length === 1}
                        onClick={() => set({ tiers: draft.tiers.filter((_, j) => j !== i) })}
                      >
                        <Trash2 className="size-4" aria-hidden />
                      </Button>
                    </li>
                  ))}
                </ol>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={draft.tiers.length >= 6}
                  onClick={() => {
                    const last = Number(draft.tiers[draft.tiers.length - 1]?.up_to) || 0;
                    set({ tiers: [...draft.tiers, { up_to: String(Math.min(100, last + 10)), approver: "merchant_admin" }] });
                  }}
                >
                  <Plus className="size-4" aria-hidden /> Add tier
                </Button>
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle>Refunds</CardTitle>
                <CardDescription>Applies when the Sales Policy asks for manager approval on returns.</CardDescription>
              </CardHeader>
              <CardContent className="space-y-3 text-sm">
                <label className="flex items-center gap-3">
                  <input type="radio" name="refund" checked={draft.refundAll} onChange={() => set({ refundAll: true })} />
                  Every refund needs approval
                </label>
                <div className="flex flex-wrap items-center gap-2">
                  <label className="flex items-center gap-3">
                    <input type="radio" name="refund" checked={!draft.refundAll} onChange={() => set({ refundAll: false })} />
                    Only refunds above {currencySymbol()}
                  </label>
                  <Input
                    aria-label="Refund approval threshold in rupees"
                    type="number"
                    min={0}
                    className="h-8 w-28"
                    disabled={draft.refundAll}
                    value={draft.refundAbove}
                    onChange={(e) => set({ refundAbove: e.target.value })}
                  />
                </div>
                <div className="flex flex-wrap items-center gap-2">
                  <label htmlFor="refund-approver">Approved by</label>
                  <LevelSelect id="refund-approver" value={draft.refundApprover} onChange={(l) => set({ refundApprover: l })} levels={APPROVERS} />
                </div>
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle>Prices below cost</CardTitle>
                <CardDescription>Who may save a selling price below the cost price.</CardDescription>
              </CardHeader>
              <CardContent className="flex flex-wrap items-center gap-2 text-sm">
                <label htmlFor="below-cost">Allowed for</label>
                <LevelSelect id="below-cost" value={draft.belowCost} onChange={(l) => set({ belowCost: l })} levels={["any", ...APPROVERS]} />
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle>Stock adjustments</CardTitle>
                <CardDescription>Value is the quantity changed times the cost price.</CardDescription>
              </CardHeader>
              <CardContent className="space-y-3 text-sm">
                <div className="flex flex-wrap items-center gap-2">
                  <label className="flex items-center gap-3">
                    <input type="checkbox" checked={draft.stockOn} onChange={(e) => set({ stockOn: e.target.checked })} />
                    Adjustments worth more than {currencySymbol()}
                  </label>
                  <Input
                    aria-label="Stock adjustment approval threshold in rupees"
                    type="number"
                    min={0}
                    className="h-8 w-28"
                    disabled={!draft.stockOn}
                    value={draft.stockAbove}
                    onChange={(e) => set({ stockAbove: e.target.value })}
                  />
                  <span>need approval</span>
                </div>
                <div className="flex flex-wrap items-center gap-2">
                  <label htmlFor="stock-approver">Approved by</label>
                  <LevelSelect
                    id="stock-approver"
                    value={draft.stockApprover}
                    onChange={(l) => set({ stockApprover: l })}
                    levels={APPROVERS}
                  />
                </div>
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle>Journal vouchers</CardTitle>
                <CardDescription>Applies when Accounting Settings turns on voucher approval.</CardDescription>
              </CardHeader>
              <CardContent className="space-y-3 text-sm">
                <label className="flex items-center gap-3">
                  <input type="radio" name="jv" checked={draft.jvAll} onChange={() => set({ jvAll: true })} />
                  Every voucher needs approval
                </label>
                <div className="flex flex-wrap items-center gap-2">
                  <label className="flex items-center gap-3">
                    <input type="radio" name="jv" checked={!draft.jvAll} onChange={() => set({ jvAll: false })} />
                    Only vouchers totalling more than {currencySymbol()}
                  </label>
                  <Input
                    aria-label="Voucher approval threshold in rupees"
                    type="number"
                    min={0}
                    className="h-8 w-28"
                    disabled={draft.jvAll}
                    value={draft.jvAbove}
                    onChange={(e) => set({ jvAbove: e.target.value })}
                  />
                </div>
              </CardContent>
            </Card>
          </fieldset>

          <div className="flex flex-wrap items-center gap-3">
            <Button onClick={save} disabled={!allowed || !dirty || saving} title={allowed ? undefined : missingPermissionHint("approvals.manage")}>
              {saving && <Loader2 className="size-4 animate-spin" aria-hidden />}
              Save
            </Button>
            {invalid && (
              <span role="alert" className="text-sm text-destructive">
                {invalid}
              </span>
            )}
            {!allowed && <span className="text-sm text-muted-foreground">{missingPermissionHint("approvals.manage")}</span>}
          </div>
        </div>
      ) : null}
    </div>
  );
}
