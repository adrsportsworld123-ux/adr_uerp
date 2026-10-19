"use client";

// Sales policy (Phase 13, erp-core-go internal/sales/returns.go and
// shifts.go): whether a POS return needs a Branch Manager / Merchant Admin
// to approve it with their employee code and PIN, how long after a sale it
// can be returned, whether a till must have an open shift to take
// payment, and the round-off waiver at checkout (migrations/059: a bill of
// ₹105.75 settled for ₹105, at most ₹5). Applies to every branch of the
// merchant.
import { useEffect, useState } from "react";
import { Loader2 } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { PageHeader } from "@/components/data/page-header";

import { rupees } from "@/lib/format";
import { currencySymbol } from "@/lib/currency";
type SalesSettings = {
  returns_require_approval: boolean;
  return_window_days: number | null;
  shifts_required: boolean;
  round_off_max: string;
  round_off_auto: boolean;
  /** migrations/062: whether open-price items take discounts and promotions (the default for every branch). */
  open_price_discounts?: boolean;
  /** migrations/063: the POS grid lists open-price items first (the default for every branch). */
  open_price_first?: boolean;
  branches?: {
    branch_id: string;
    branch_name: string;
    open_price_discounts: boolean | null;
    effective_open_price_discounts: boolean;
    open_price_first?: boolean | null;
    effective_open_price_first?: boolean;
  }[];
};
/** The most a merchant may allow (erp-core-go sales.MaxRoundOff). */
const MAX_ROUND_OFF = 5;

export default function SalesSettingsPage() {
  const { can } = usePermissions();
  const allowed = can("sales.settings");
  const [saved, setSaved] = useState<SalesSettings | null>(null);
  const [approval, setApproval] = useState(true);
  const [limitWindow, setLimitWindow] = useState(false);
  const [days, setDays] = useState("30");
  const [shifts, setShifts] = useState(false);
  const [roundOff, setRoundOff] = useState(false);
  const [roundOffMax, setRoundOffMax] = useState("1");
  const [roundOffAuto, setRoundOffAuto] = useState(false);
  const [openPriceDiscounts, setOpenPriceDiscounts] = useState(true);
  const [openPriceFirst, setOpenPriceFirst] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api
      .get<SalesSettings>("/api/v1/sales/settings")
      .then((s) => {
        setSaved(s);
        setApproval(s.returns_require_approval);
        setLimitWindow(s.return_window_days != null);
        if (s.return_window_days != null) setDays(String(s.return_window_days));
        setShifts(s.shifts_required);
        setRoundOff(Number(s.round_off_max) > 0);
        if (Number(s.round_off_max) > 0) setRoundOffMax(String(Number(s.round_off_max)));
        setRoundOffAuto(s.round_off_auto);
        setOpenPriceDiscounts(s.open_price_discounts ?? true);
        setOpenPriceFirst(s.open_price_first ?? false);
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load the sales policy"));
  }, []);

  const daysValue = Number(days);
  const daysInvalid = limitWindow && (!Number.isInteger(daysValue) || daysValue < 1 || daysValue > 3650);
  const maxValue = Number(roundOffMax);
  const maxInvalid = roundOff && (!(maxValue > 0) || maxValue > MAX_ROUND_OFF || !/^\d+(\.\d{1,2})?$/.test(roundOffMax.trim()));
  const next = {
    returns_require_approval: approval,
    return_window_days: limitWindow ? daysValue : null,
    shifts_required: shifts,
    round_off_max: roundOff ? maxValue : 0,
    round_off_auto: roundOff && roundOffAuto,
    open_price_discounts: openPriceDiscounts,
    open_price_first: openPriceFirst,
  };
  const dirty =
    saved != null &&
    (saved.returns_require_approval !== next.returns_require_approval ||
      saved.return_window_days !== next.return_window_days ||
      saved.shifts_required !== next.shifts_required ||
      Number(saved.round_off_max) !== next.round_off_max ||
      saved.round_off_auto !== next.round_off_auto ||
      (saved.open_price_discounts ?? true) !== next.open_price_discounts ||
      (saved.open_price_first ?? false) !== next.open_price_first);

  // A branch's own rule for open-price discounts: saved at once.
  async function setBranchRule(branchId: string, value: boolean | null, field: "open_price_discounts" | "open_price_first" = "open_price_discounts") {
    try {
      const s = await api.put<SalesSettings>(`/api/v1/sales/settings/branches/${branchId}`, { [field]: value });
      setSaved(s);
      toast.success("Branch rule saved");
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not save the branch rule");
    }
  }

  async function save() {
    setSaving(true);
    try {
      const s = await api.put<SalesSettings>("/api/v1/sales/settings", next);
      setSaved(s);
      toast.success("Sales policy saved");
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not save the sales policy");
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="Sales Policy"
        description="How returns, shifts and round-off work on every till. Cashiers start a return from the POS's sales history; the refund and restock follow the original sale."
      />
      {error && <p className="text-sm text-destructive">{error}</p>}
      {saved == null && !error ? (
        <p className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="size-4 animate-spin" aria-hidden /> Loading…
        </p>
      ) : saved != null ? (
        <Card className="max-w-2xl">
          <CardHeader>
            <CardTitle>Returns, refunds, shifts and round-off</CardTitle>
            <CardDescription>Applies to all branches.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-6">
            <fieldset className="space-y-3" disabled={!allowed}>
              <legend className="text-sm font-medium">Who can process a return</legend>
              <label className="flex items-start gap-3 text-sm">
                <input type="radio" name="approval" className="mt-1" checked={approval} onChange={() => setApproval(true)} />
                <span>
                  <span className="font-medium">Manager approval</span> (recommended)
                  <span className="block text-muted-foreground">
                    A Branch Manager or Merchant Admin enters their employee code and PIN on the till. Who approves, and above what refund amount, is set under Approval Policies.
                  </span>
                </span>
              </label>
              <label className="flex items-start gap-3 text-sm">
                <input type="radio" name="approval" className="mt-1" checked={!approval} onChange={() => setApproval(false)} />
                <span>
                  <span className="font-medium">Any cashier</span>
                  <span className="block text-muted-foreground">Cashiers refund on their own. Every return still records who processed it.</span>
                </span>
              </label>
            </fieldset>

            <fieldset className="space-y-3" disabled={!allowed}>
              <legend className="text-sm font-medium">Return window</legend>
              <label className="flex items-center gap-3 text-sm">
                <input type="checkbox" checked={limitWindow} onChange={(e) => setLimitWindow(e.target.checked)} />
                Only accept returns within a number of days of the sale
              </label>
              {limitWindow && (
                <div className="flex items-center gap-2 pl-7">
                  <Label htmlFor="return-days" className="sr-only">
                    Days
                  </Label>
                  <Input
                    id="return-days"
                    type="number"
                    min={1}
                    max={3650}
                    className="w-24"
                    value={days}
                    aria-invalid={daysInvalid}
                    aria-describedby="return-days-help"
                    onChange={(e) => setDays(e.target.value)}
                  />
                  <span id="return-days-help" className={daysInvalid ? "text-sm text-destructive" : "text-sm text-muted-foreground"}>
                    {daysInvalid ? "Enter a whole number of days, 1 or more" : "days after the sale"}
                  </span>
                </div>
              )}
            </fieldset>

            <fieldset className="space-y-3" disabled={!allowed}>
              <legend className="text-sm font-medium">Shifts</legend>
              <label className="flex items-start gap-3 text-sm">
                <input type="checkbox" className="mt-1" checked={shifts} onChange={(e) => setShifts(e.target.checked)} />
                <span>
                  <span className="font-medium">Require an open shift to take payment</span>
                  <span className="block text-muted-foreground">
                    Each till opens a shift with its cash float and closes it with a counted drawer. The close is that till&apos;s cash
                    reconciliation, so a branch that runs shifts doesn&apos;t also do a whole-branch daily count. Offline sales are never
                    blocked.
                  </span>
                </span>
              </label>
            </fieldset>

            <fieldset className="space-y-3" disabled={!allowed}>
              <legend className="text-sm font-medium">Round-off at checkout</legend>
              <label className="flex items-start gap-3 text-sm">
                <input type="checkbox" className="mt-1" checked={roundOff} onChange={(e) => setRoundOff(e.target.checked)} />
                <span>
                  <span className="font-medium">Let the cashier waive a small balance</span>
                  <span className="block text-muted-foreground">
                    A bill of {currencySymbol()}105.75 can be settled for {currencySymbol()}105.00. The invoice and its GST stay as billed; the amount waived is printed on the
                    receipt, booked to Round Off, and shown in every sales report.
                  </span>
                </span>
              </label>
              {roundOff && (
                <div className="space-y-3 pl-7">
                  <div className="flex flex-wrap items-center gap-2">
                    <Label htmlFor="round-off-max">Most waived on one sale</Label>
                    <span className="text-sm">{currencySymbol()}</span>
                    <Input
                      id="round-off-max"
                      type="number"
                      min={0.01}
                      max={MAX_ROUND_OFF}
                      step="0.01"
                      className="w-24"
                      value={roundOffMax}
                      aria-invalid={maxInvalid}
                      aria-describedby="round-off-max-help"
                      onChange={(e) => setRoundOffMax(e.target.value)}
                    />
                    <span id="round-off-max-help" className={maxInvalid ? "text-sm text-destructive" : "text-sm text-muted-foreground"}>
                      {maxInvalid ? `Enter an amount above ${currencySymbol()}0 and up to ${rupees(MAX_ROUND_OFF)}` : `up to ${rupees(MAX_ROUND_OFF)}`}
                    </span>
                  </div>
                  <label className="flex items-start gap-3 text-sm">
                    <input type="checkbox" className="mt-1" checked={roundOffAuto} onChange={(e) => setRoundOffAuto(e.target.checked)} />
                    <span>
                      <span className="font-medium">Round down to the rupee by default</span>
                      <span className="block text-muted-foreground">
                        The payment screen starts with the paise waived (within the limit above); the cashier can still take the exact amount.
                      </span>
                    </span>
                  </label>
                </div>
              )}
            </fieldset>

            <fieldset className="space-y-3" disabled={!allowed}>
              <legend className="text-sm font-medium">Open-price items</legend>
              <label className="flex items-start gap-3 text-sm">
                <input
                  type="checkbox"
                  className="mt-1"
                  checked={!openPriceDiscounts}
                  onChange={(e) => setOpenPriceDiscounts(!e.target.checked)}
                />
                <span>
                  <span className="font-medium">Don&apos;t apply promotions or discounts to open-price items</span>
                  <span className="block text-muted-foreground">
                    The price typed at the till is final: promotions, coupons, loyalty points and manual discounts skip those lines, and a
                    percent discount or a coupon&apos;s minimum spend is worked out on the other items. This is the default for every branch;
                    a branch can choose differently below.
                  </span>
                </span>
              </label>
              {(saved.branches ?? []).length > 0 && (
                <ul className="space-y-2 pl-7" aria-label="Open-price items by branch">
                  {(saved.branches ?? []).map((b) => (
                    <li key={b.branch_id} className="flex flex-wrap items-center gap-2 text-sm">
                      <span className="min-w-40 font-medium">{b.branch_name}</span>
                      <select
                        aria-label={`Open-price discounts at ${b.branch_name}`}
                        value={b.open_price_discounts == null ? "" : b.open_price_discounts ? "allow" : "block"}
                        onChange={(e) => setBranchRule(b.branch_id, e.target.value === "" ? null : e.target.value === "allow")}
                        className="h-9 rounded-lg border bg-card px-2 text-sm"
                      >
                        <option value="">Same as the default</option>
                        <option value="block">No promotions or discounts</option>
                        <option value="allow">Promotions and discounts apply</option>
                      </select>
                      <span className="text-xs text-muted-foreground" data-testid="branch-open-price-rule">
                        {b.effective_open_price_discounts ? "discounts apply" : "no discounts"}
                      </span>
                    </li>
                  ))}
                </ul>
              )}
              <label className="flex items-start gap-3 text-sm">
                <input type="checkbox" className="mt-1" checked={openPriceFirst} onChange={(e) => setOpenPriceFirst(e.target.checked)} />
                <span>
                  <span className="font-medium">Show open-price items first on the POS</span>
                  <span className="block text-muted-foreground">
                    The till&apos;s product grid lists open-price items before the rest, on the full list and inside every category. Off:
                    the order the grid has today. Each open-price tile is marked and coloured — the product&apos;s own colour (set on the
                    product), else the standard open-price colour.
                  </span>
                </span>
              </label>
              {(saved.branches ?? []).length > 0 && (
                <ul className="space-y-2 pl-7" aria-label="Grid order by branch">
                  {(saved.branches ?? []).map((b) => (
                    <li key={b.branch_id} className="flex flex-wrap items-center gap-2 text-sm">
                      <span className="min-w-40 font-medium">{b.branch_name}</span>
                      <select
                        aria-label={`Grid order at ${b.branch_name}`}
                        value={b.open_price_first == null ? "" : b.open_price_first ? "first" : "normal"}
                        onChange={(e) => setBranchRule(b.branch_id, e.target.value === "" ? null : e.target.value === "first", "open_price_first")}
                        className="h-9 rounded-lg border bg-card px-2 text-sm"
                      >
                        <option value="">Same as the default</option>
                        <option value="first">Open-price items first</option>
                        <option value="normal">The usual order</option>
                      </select>
                      <span className="text-xs text-muted-foreground" data-testid="branch-grid-order">
                        {b.effective_open_price_first ? "open-price first" : "usual order"}
                      </span>
                    </li>
                  ))}
                </ul>
              )}
            </fieldset>

            <div className="flex items-center gap-3">
              <Button onClick={save} disabled={!allowed || !dirty || daysInvalid || maxInvalid || saving} title={allowed ? undefined : missingPermissionHint("sales.settings")}>
                {saving && <Loader2 className="size-4 animate-spin" aria-hidden />}
                Save
              </Button>
              {!allowed && <span className="text-sm text-muted-foreground">{missingPermissionHint("sales.settings")}</span>}
            </div>
          </CardContent>
        </Card>
      ) : null}
    </div>
  );
}
