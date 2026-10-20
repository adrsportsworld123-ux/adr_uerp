"use client";

// Merchant Management in the SaaS console (erp-core-go migrations/065):
// the company profile, the account's status (a suspended or cancelled
// merchant can't sign in — password, PIN or a refreshed session — and its
// data is untouched), and its Merchant Admins' passwords for a merchant
// locked out of its own account. Every change needs a reason and goes to
// the platform audit log.
import { useCallback, useEffect, useState } from "react";
import { KeyRound, Loader2 } from "lucide-react";
import { toast } from "sonner";
import { ApiError } from "@/lib/api-client";
import { platformApi } from "@/lib/platform-api";
import { dateTime } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Modal } from "@/components/ui/dialog";
import { StatusChip } from "@/components/data/status-chip";
import { selectClass } from "@/components/data/panel";
import { MerchantProfile, ProfileForm } from "./profile-form";

type Admin = { user_id: string; name: string; email: string; status: string; locked_until: string | null };
type Overview = { profile: MerchantProfile; branches: number; users: number; terminals: number; admins: Admin[] };

const STATUS_LABEL: Record<string, string> = { active: "Active", suspended: "Suspended", cancelled: "Cancelled" };
const STATUS_TONE = { active: "ok", suspended: "warn", cancelled: "bad" } as const;

const message = (e: unknown, fallback: string) => (e instanceof ApiError ? e.message : fallback);

export function ConsoleMerchantManagement({ merchantId }: { merchantId: string }) {
  const [o, setO] = useState<Overview | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [reason, setReason] = useState("");
  const [status, setStatus] = useState("active");
  const [statusReason, setStatusReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [resetFor, setResetFor] = useState<Admin | null>(null);
  const [pw, setPw] = useState("");
  const [pwReason, setPwReason] = useState("");

  const load = useCallback(() => {
    platformApi
      .get<Overview>(`/platform/merchants/${merchantId}/profile`)
      .then((x) => {
        setO(x);
        setStatus(x.profile.status);
      })
      .catch((e) => setError(message(e, "Could not load the merchant's profile")));
  }, [merchantId]);
  useEffect(load, [load]);

  async function changeStatus() {
    setBusy(true);
    try {
      setO(await platformApi.post<Overview>(`/platform/merchants/${merchantId}/status`, { status, reason: statusReason }));
      setStatusReason("");
      toast.success(`Merchant ${STATUS_LABEL[status].toLowerCase()}`);
    } catch (e) {
      toast.error(message(e, "Could not change the status"));
    } finally {
      setBusy(false);
    }
  }

  async function resetPassword() {
    if (!resetFor) return;
    setBusy(true);
    try {
      await platformApi.post(`/platform/merchants/${merchantId}/admins/${resetFor.user_id}/password`, { new_password: pw, reason: pwReason });
      toast.success(`Password reset for ${resetFor.name}; their other sessions were signed out`);
      setResetFor(null);
      setPw("");
      setPwReason("");
      load();
    } catch (e) {
      toast.error(message(e, "Could not reset the password"));
    } finally {
      setBusy(false);
    }
  }

  if (error) return <p className="text-sm text-destructive">{error}</p>;
  if (!o) return <p className="text-sm text-muted-foreground">Loading the profile…</p>;
  const p = o.profile;

  return (
    <div className="space-y-6">
      <section className="grid gap-4 lg:grid-cols-2">
        <div className="rounded-xl border bg-card p-5">
          <h2 className="flex items-center gap-2 text-sm font-semibold">
            Account <StatusChip status={p.status} tone={STATUS_TONE[p.status as keyof typeof STATUS_TONE]} label={STATUS_LABEL[p.status] ?? p.status} />
          </h2>
          <dl className="mt-3 grid grid-cols-2 gap-y-1.5 text-sm">
            <dt className="text-muted-foreground">Active branches</dt>
            <dd className="tabular">{o.branches}</dd>
            <dt className="text-muted-foreground">Active users</dt>
            <dd className="tabular">{o.users}</dd>
            <dt className="text-muted-foreground">Active tills</dt>
            <dd className="tabular">{o.terminals}</dd>
            {p.status_changed_at && (
              <>
                <dt className="text-muted-foreground">Status changed</dt>
                <dd>
                  {dateTime(p.status_changed_at)}
                  {p.status_reason && <span className="block text-xs text-muted-foreground">{p.status_reason}</span>}
                </dd>
              </>
            )}
          </dl>
          <div className="mt-4 grid gap-2 sm:grid-cols-[10rem_1fr_auto] sm:items-end">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="mm-status">Access</Label>
              <select id="mm-status" value={status} onChange={(e) => setStatus(e.target.value)} className={selectClass}>
                {Object.entries(STATUS_LABEL).map(([k, v]) => (
                  <option key={k} value={k}>
                    {v}
                  </option>
                ))}
              </select>
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="mm-status-reason">Why (audited)</Label>
              <Input id="mm-status-reason" value={statusReason} onChange={(e) => setStatusReason(e.target.value)} />
            </div>
            <Button onClick={changeStatus} disabled={busy || status === p.status || !statusReason.trim()} variant={status === "active" ? "default" : "destructive"}>
              {busy && <Loader2 className="animate-spin" aria-hidden />}
              {status === "active" ? "Reactivate" : status === "suspended" ? "Suspend" : "Cancel account"}
            </Button>
          </div>
          <p className="mt-2 text-xs text-muted-foreground">
            A suspended or cancelled merchant can&apos;t sign in at the back office or a till, and open sessions end at their next refresh. Nothing is
            deleted; reactivating restores access.
          </p>
        </div>
        <div className="rounded-xl border bg-card p-5">
          <h2 className="text-sm font-semibold">Merchant Admins</h2>
          <ul className="mt-3 divide-y text-sm">
            {o.admins.map((a) => (
              <li key={a.user_id} className="flex items-center justify-between gap-3 py-2">
                <span>
                  <span className="font-medium">{a.name}</span>
                  <span className="block text-xs text-muted-foreground">
                    {a.email || "no e-mail"} · {a.status}
                    {a.locked_until && new Date(a.locked_until) > new Date() && " · locked"}
                  </span>
                </span>
                <Button size="sm" variant="outline" onClick={() => setResetFor(a)} aria-label={`Reset ${a.name}'s password`}>
                  <KeyRound /> Reset password
                </Button>
              </li>
            ))}
            {o.admins.length === 0 && <li className="py-2 text-muted-foreground">No Merchant Admin.</li>}
          </ul>
        </div>
      </section>

      <section className="rounded-xl border bg-card p-5">
        <h2 className="mb-4 text-sm font-semibold">Company profile</h2>
        <ProfileForm
          key={JSON.stringify(p)}
          profile={p}
          saveLabel="Save profile"
          extra={
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="mm-reason">Why the profile changed (audited)</Label>
              <Input id="mm-reason" value={reason} onChange={(e) => setReason(e.target.value)} placeholder="e.g. customer asked by e-mail" />
            </div>
          }
          onSave={async (f) => {
            try {
              setO(await platformApi.patch<Overview>(`/platform/merchants/${merchantId}/profile`, { ...f, reason }));
              setReason("");
              toast.success("Profile saved");
            } catch (e) {
              throw new Error(message(e, "Could not save the profile"));
            }
          }}
        />
      </section>

      {resetFor && (
        <Modal
          open
          onOpenChange={(v) => !v && setResetFor(null)}
          title={`Reset ${resetFor.name}'s password`}
          description="Sets a new password, clears a lockout and signs the user out everywhere. Tell them the password over a safe channel."
          footer={
            <>
              <Button variant="outline" onClick={() => setResetFor(null)}>
                Cancel
              </Button>
              <Button onClick={resetPassword} disabled={busy || pw.length < 8 || !pwReason.trim()}>
                Reset password
              </Button>
            </>
          }
        >
          <div className="space-y-3">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="mm-pw">New password (8 or more characters)</Label>
              <Input id="mm-pw" type="password" autoComplete="new-password" value={pw} onChange={(e) => setPw(e.target.value)} />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="mm-pw-reason">Why (audited)</Label>
              <Input id="mm-pw-reason" value={pwReason} onChange={(e) => setPwReason(e.target.value)} />
            </div>
          </div>
        </Modal>
      )}
    </div>
  );
}
