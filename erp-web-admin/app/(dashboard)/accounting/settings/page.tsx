"use client";

// Accounting settings (Phase 14, accounting.manage): whether journal
// vouchers need approval, and the period lock. Nothing posts on or before
// the lock date, from any source; moving it back reopens the period.
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { AccountingSettings } from "@/lib/types";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { PageHeader } from "@/components/data/page-header";

export default function AccountingSettingsPage() {
  const { can } = usePermissions();
  const allowed = can("accounting.manage");
  const [saved, setSaved] = useState<AccountingSettings | null>(null);
  const [approval, setApproval] = useState(false);
  const [lock, setLock] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api
      .get<AccountingSettings>("/api/v1/accounting/settings")
      .then((s) => {
        setSaved(s);
        setApproval(s.jv_approval_required);
        setLock(s.locked_through ?? "");
      })
      .catch(() => {});
  }, []);

  const dirty = saved != null && (saved.jv_approval_required !== approval || (saved.locked_through ?? "") !== lock);

  async function save() {
    setBusy(true);
    try {
      const s = await api.put<AccountingSettings>("/api/v1/accounting/settings", { jv_approval_required: approval, locked_through: lock || null });
      setSaved(s);
      toast.success("Accounting settings saved");
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not save");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex max-w-2xl flex-col gap-5">
      <PageHeader title="Accounting Settings" description="Approval for journal vouchers, and locking past periods once they're closed." />
      {saved && (
        <div className="flex flex-col gap-6 rounded-xl border bg-card p-4">
          <fieldset className="flex flex-col gap-2" disabled={!allowed}>
            <legend className="text-sm font-semibold">Journal vouchers</legend>
            <label className="flex items-start gap-3 text-sm">
              <input type="checkbox" className="mt-1" checked={approval} onChange={(e) => setApproval(e.target.checked)} />
              <span>
                <span className="font-medium">Require approval before a voucher posts</span>
                <span className="block text-muted-foreground">
                  A voucher is submitted, then a different person with JV approval rights approves it before it posts. Off, vouchers post as
                  they&apos;re saved.
                </span>
              </span>
            </label>
          </fieldset>
          <fieldset className="flex flex-col gap-2" disabled={!allowed}>
            <legend className="text-sm font-semibold">Period lock</legend>
            <div className="flex items-end gap-2">
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="lock">Books locked through</Label>
                <Input id="lock" type="date" value={lock} onChange={(e) => setLock(e.target.value)} className="w-44" />
              </div>
              {lock && (
                <Button variant="ghost" onClick={() => setLock("")}>
                  Unlock
                </Button>
              )}
            </div>
            <p className="text-sm text-muted-foreground">
              Nothing can be posted on or before this date: no vouchers, reversals or back-dated counts. Sales and other day-to-day postings are
              dated today, so they carry on.
            </p>
          </fieldset>
          <div className="flex items-center gap-3">
            <Button disabled={!allowed || !dirty || busy} onClick={save}>
              Save
            </Button>
            {!allowed && <span className="text-sm text-muted-foreground">{missingPermissionHint("accounting.manage")}</span>}
          </div>
        </div>
      )}
    </div>
  );
}
