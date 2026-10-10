"use client";

// Return policy (Phase 13, erp-core-go internal/sales/returns.go): whether a
// POS return needs a Branch Manager / Merchant Admin to approve it with
// their employee code and PIN, and how long after a sale it can be
// returned. Applies to every branch of the merchant.
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

type SalesSettings = { returns_require_approval: boolean; return_window_days: number | null };

export default function SalesSettingsPage() {
  const { can } = usePermissions();
  const allowed = can("sales.settings");
  const [saved, setSaved] = useState<SalesSettings | null>(null);
  const [approval, setApproval] = useState(true);
  const [limitWindow, setLimitWindow] = useState(false);
  const [days, setDays] = useState("30");
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
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load the return policy"));
  }, []);

  const daysValue = Number(days);
  const daysInvalid = limitWindow && (!Number.isInteger(daysValue) || daysValue < 1 || daysValue > 3650);
  const next: SalesSettings = { returns_require_approval: approval, return_window_days: limitWindow ? daysValue : null };
  const dirty = saved != null && (saved.returns_require_approval !== next.returns_require_approval || saved.return_window_days !== next.return_window_days);

  async function save() {
    setSaving(true);
    try {
      const s = await api.put<SalesSettings>("/api/v1/sales/settings", next);
      setSaved(s);
      toast.success("Return policy saved");
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not save the return policy");
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="Return Policy"
        description="How returns work on every till. Cashiers start a return from the POS's sales history; the refund and restock follow the original sale."
      />
      {error && <p className="text-sm text-destructive">{error}</p>}
      {saved == null && !error ? (
        <p className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="size-4 animate-spin" aria-hidden /> Loading…
        </p>
      ) : saved != null ? (
        <Card className="max-w-2xl">
          <CardHeader>
            <CardTitle>Returns and refunds</CardTitle>
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
                    A Branch Manager or Merchant Admin enters their employee code and PIN on the till for every return.
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

            <div className="flex items-center gap-3">
              <Button onClick={save} disabled={!allowed || !dirty || daysInvalid || saving} title={allowed ? undefined : missingPermissionHint("sales.settings")}>
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
