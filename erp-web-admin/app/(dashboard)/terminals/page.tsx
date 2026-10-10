"use client";

// POS terminals (Phase 12, erp-core-go internal/terminals): one row per
// till. An admin issues a short-lived pairing code here and types it into
// the tablet's pairing screen once; that binds the device to the terminal.
// Deactivating a terminal (or its branch) stops new PIN logins on it at
// once; its sales history is untouched.
import { useCallback, useEffect, useMemo, useState } from "react";
import { Copy, KeyRound, Link2Off, Loader2, MonitorSmartphone, Pencil, Plus, Power } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Branch } from "@/lib/types";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import { Button } from "@/components/ui/button";
import { AlertModal, Modal } from "@/components/ui/dialog";
import { EditDialog } from "@/components/data/edit-dialog";
import { PageHeader } from "@/components/data/page-header";
import { StatusChip } from "@/components/data/status-chip";

type Terminal = {
  terminal_id: string;
  name: string;
  branch_id: string;
  branch_name: string;
  branch_status: string;
  status: string;
  paired: boolean;
  paired_at: string | null;
  pairing_pending: boolean;
  last_synced_at: string | null;
  created_at: string;
};

function when(iso: string | null) {
  if (!iso) return "—";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "—" : d.toLocaleString("en-IN", { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });
}

function useCountdown(expiresAt: string | null) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!expiresAt) return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [expiresAt]);
  if (!expiresAt) return null;
  const left = Math.max(0, Math.floor((new Date(expiresAt).getTime() - now) / 1000));
  return { left, label: `${Math.floor(left / 60)}:${String(left % 60).padStart(2, "0")}` };
}

function PairingCodeDialog({ terminal, code, expiresAt, onClose }: { terminal: Terminal; code: string; expiresAt: string; onClose: () => void }) {
  const countdown = useCountdown(expiresAt);
  const expired = countdown?.left === 0;
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={`Pair a device to ${terminal.name}`}
      description={terminal.branch_name}
      footer={<Button onClick={onClose}>Done</Button>}
    >
      <div className="flex flex-col items-center gap-4 py-2 text-center">
        <p className="text-sm text-muted-foreground">On the tablet, open the POS app and enter this code on the pairing screen.</p>
        <div
          className={`flex items-center gap-3 rounded-xl border bg-muted px-5 py-3 font-mono text-3xl font-semibold tracking-[0.3em] ${expired ? "opacity-40 line-through" : ""}`}
          aria-label={`Pairing code ${code.split("").join(" ")}`}
        >
          {code}
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="Copy pairing code"
            onClick={() => navigator.clipboard?.writeText(code).then(() => toast.success("Code copied")).catch(() => {})}
          >
            <Copy />
          </Button>
        </div>
        <p className={`text-sm ${expired ? "text-destructive" : "text-muted-foreground"}`} aria-live="polite">
          {expired ? "This code has expired. Close and issue a new one." : `Expires in ${countdown?.label ?? "15:00"} · works once`}
        </p>
        <p className="text-xs text-muted-foreground">
          Pairing binds this terminal to that device. A device already paired to this terminal will need a new code to pair again.
        </p>
      </div>
    </Modal>
  );
}

export default function TerminalsPage() {
  const { can } = usePermissions();
  const allowed = can("terminal.manage");
  const denied = allowed ? undefined : missingPermissionHint("terminal.manage");

  const [terminals, setTerminals] = useState<Terminal[] | null>(null);
  const [branches, setBranches] = useState<Branch[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [branchFilter, setBranchFilter] = useState("");

  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState("");
  const [newBranch, setNewBranch] = useState("");
  const [busy, setBusy] = useState(false);

  const [renaming, setRenaming] = useState<Terminal | null>(null);
  const [confirming, setConfirming] = useState<{ terminal: Terminal; action: "deactivate" | "reset" } | null>(null);
  const [pairing, setPairing] = useState<{ terminal: Terminal; code: string; expiresAt: string } | null>(null);

  const load = useCallback(() => {
    api
      .get<{ terminals: Terminal[] }>("/api/v1/pos-terminals")
      .then((d) => {
        setTerminals(d.terminals);
        setError(null);
      })
      .catch((e) => setError(e instanceof Error ? e.message : "Could not load terminals"));
  }, []);

  useEffect(() => {
    load();
    api
      .get<{ branches: Branch[] }>("/api/v1/branches?scope=mine")
      .then((d) => setBranches(d.branches))
      .catch(() => {});
  }, [load]);

  const visible = useMemo(
    () => (terminals ?? []).filter((t) => !branchFilter || t.branch_id === branchFilter),
    [terminals, branchFilter],
  );

  async function run<T>(fn: () => Promise<T>, success: string) {
    setBusy(true);
    try {
      const r = await fn();
      toast.success(success);
      load();
      return r;
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Something went wrong");
      return undefined;
    } finally {
      setBusy(false);
    }
  }

  async function create() {
    const ok = await run(() => api.post("/api/v1/pos-terminals", { branch_id: newBranch, name: newName.trim() }), `Created terminal “${newName.trim()}”`);
    if (ok) {
      setCreating(false);
      setNewName("");
    }
  }

  async function issueCode(t: Terminal) {
    setBusy(true);
    try {
      const d = await api.post<{ code: string; expires_at: string }>(`/api/v1/pos-terminals/${t.terminal_id}/pairing-code`);
      setPairing({ terminal: t, code: d.code, expiresAt: d.expires_at });
      load();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not issue a pairing code");
    } finally {
      setBusy(false);
    }
  }

  const activeBranches = branches.filter((b) => b.status === "active");

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="POS Terminals"
        description="Each till that can take sales. Pair a tablet to a terminal with a one-time code; deactivating a terminal or its branch stops new logins on it."
        actions={
          <Button
            onClick={() => {
              setNewBranch(activeBranches[0]?.branch_id ?? "");
              setCreating(true);
            }}
            disabled={!allowed}
            title={denied}
          >
            <Plus aria-hidden />
            New terminal
          </Button>
        }
      />

      <div className="flex flex-wrap items-center gap-2">
        <select aria-label="Branch" value={branchFilter} onChange={(e) => setBranchFilter(e.target.value)} className="h-9 rounded-lg border bg-card px-2 text-sm">
          <option value="">All branches</option>
          {branches.map((b) => (
            <option key={b.branch_id} value={b.branch_id}>
              {b.name}
              {b.status !== "active" ? " (inactive)" : ""}
            </option>
          ))}
        </select>
        <span className="text-sm text-muted-foreground">{terminals ? `${visible.length} terminal${visible.length === 1 ? "" : "s"}` : "Loading…"}</span>
      </div>

      {error && (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      )}

      <div className="overflow-hidden rounded-xl border bg-card">
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="bg-muted/60 text-left text-xs font-semibold text-muted-foreground">
              <tr>
                <th className="px-3 py-2.5">Terminal</th>
                <th className="px-3 py-2.5">Branch</th>
                <th className="px-3 py-2.5">Device</th>
                <th className="px-3 py-2.5">Last sync</th>
                <th className="px-3 py-2.5">Status</th>
                <th className="w-px px-3 py-2.5" aria-label="Actions" />
              </tr>
            </thead>
            <tbody>
              {terminals === null && !error && (
                <tr>
                  <td colSpan={6} className="px-3 py-8 text-center text-muted-foreground">
                    <Loader2 className="mx-auto size-5 animate-spin" aria-label="Loading" />
                  </td>
                </tr>
              )}
              {terminals !== null && visible.length === 0 && (
                <tr>
                  <td colSpan={6} className="px-3 py-10 text-center">
                    <MonitorSmartphone className="mx-auto mb-2 size-6 text-primary" aria-hidden />
                    <p className="font-medium">No terminals here yet</p>
                    <p className="text-sm text-muted-foreground">Create one for each till, then pair a tablet to it.</p>
                  </td>
                </tr>
              )}
              {visible.map((t) => (
                <tr key={t.terminal_id} className="border-t">
                  <td className="px-3 py-2.5 font-medium">{t.name}</td>
                  <td className="px-3 py-2.5">
                    {t.branch_name}
                    {t.branch_status !== "active" && <StatusChip status="inactive" className="ml-2" label="Branch inactive" />}
                  </td>
                  <td className="px-3 py-2.5">
                    {t.paired ? (
                      <StatusChip status="paired" tone="ok" label={`Paired ${when(t.paired_at)}`} />
                    ) : t.pairing_pending ? (
                      <StatusChip status="pending" tone="warn" label="Code issued" />
                    ) : (
                      <StatusChip status="unpaired" tone="muted" label="Not paired" />
                    )}
                  </td>
                  <td className="px-3 py-2.5 tabular text-muted-foreground">{when(t.last_synced_at)}</td>
                  <td className="px-3 py-2.5">
                    <StatusChip status={t.status} />
                  </td>
                  <td className="px-2 py-1.5">
                    <div className="flex justify-end gap-1">
                      <Button
                        variant="outline"
                        size="sm"
                        disabled={!allowed || busy || t.status !== "active" || t.branch_status !== "active"}
                        title={denied ?? (t.status !== "active" || t.branch_status !== "active" ? "Reactivate the terminal and its branch first" : undefined)}
                        onClick={() => issueCode(t)}
                        aria-label={`Pair a device to ${t.name}`}
                      >
                        <KeyRound aria-hidden />
                        Pair device
                      </Button>
                      <Button variant="ghost" size="icon-sm" aria-label={`Rename ${t.name}`} title={denied ?? "Rename"} disabled={!allowed} onClick={() => setRenaming(t)}>
                        <Pencil />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label={`Reset device on ${t.name}`}
                        title={denied ?? (t.paired ? "Unbind the paired device" : "No device is paired")}
                        disabled={!allowed || !t.paired}
                        onClick={() => setConfirming({ terminal: t, action: "reset" })}
                      >
                        <Link2Off />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label={`${t.status === "active" ? "Deactivate" : "Activate"} ${t.name}`}
                        title={denied ?? (t.status === "active" ? "Deactivate" : "Activate")}
                        disabled={!allowed}
                        onClick={() =>
                          t.status === "active"
                            ? setConfirming({ terminal: t, action: "deactivate" })
                            : run(() => api.patch(`/api/v1/pos-terminals/${t.terminal_id}`, { status: "active" }), `Activated “${t.name}”`)
                        }
                      >
                        <Power className={t.status === "active" ? "" : "text-pay"} />
                      </Button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      <Modal
        open={creating}
        onOpenChange={setCreating}
        title="New terminal"
        footer={
          <>
            <Button variant="outline" onClick={() => setCreating(false)} disabled={busy}>
              Cancel
            </Button>
            <Button onClick={create} disabled={busy || !newName.trim() || !newBranch}>
              {busy && <Loader2 className="animate-spin" aria-hidden />}
              Create terminal
            </Button>
          </>
        }
      >
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            create();
          }}
        >
          <div className="flex flex-col gap-1.5">
            <label htmlFor="terminal-branch" className="text-sm font-medium">
              Branch
            </label>
            <select id="terminal-branch" value={newBranch} onChange={(e) => setNewBranch(e.target.value)} className="h-9 rounded-lg border bg-card px-2 text-sm">
              {activeBranches.map((b) => (
                <option key={b.branch_id} value={b.branch_id}>
                  {b.name}
                </option>
              ))}
            </select>
          </div>
          <div className="flex flex-col gap-1.5">
            <label htmlFor="terminal-name" className="text-sm font-medium">
              Name
            </label>
            <input
              id="terminal-name"
              autoFocus
              value={newName}
              placeholder="Counter 2"
              onChange={(e) => setNewName(e.target.value)}
              className="h-9 rounded-lg border bg-card px-3 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/30"
            />
          </div>
          <button type="submit" hidden />
        </form>
      </Modal>

      {renaming && (
        <EditDialog
          open
          onOpenChange={(o) => !o && setRenaming(null)}
          title="Rename terminal"
          description={renaming.branch_name}
          fields={[{ key: "name", label: "Name", required: true }]}
          initial={{ name: renaming.name }}
          onSave={async (changes) => {
            await api.patch(`/api/v1/pos-terminals/${renaming.terminal_id}`, changes);
            toast.success(`Renamed to “${changes.name}”`);
            load();
          }}
        />
      )}

      <AlertModal
        open={!!confirming}
        onOpenChange={(o) => !o && setConfirming(null)}
        title={
          confirming?.action === "reset"
            ? `Unbind the device from “${confirming.terminal.name}”?`
            : `Deactivate “${confirming?.terminal.name ?? ""}”?`
        }
        actions={
          <>
            <Button variant="outline" onClick={() => setConfirming(null)}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              disabled={busy}
              onClick={async () => {
                const c = confirming!;
                setConfirming(null);
                if (c.action === "reset") await run(() => api.post(`/api/v1/pos-terminals/${c.terminal.terminal_id}/reset-device`), `Device unbound from “${c.terminal.name}”`);
                else await run(() => api.patch(`/api/v1/pos-terminals/${c.terminal.terminal_id}`, { status: "inactive" }), `Deactivated “${c.terminal.name}”`);
              }}
            >
              {confirming?.action === "reset" ? "Unbind device" : "Deactivate"}
            </Button>
          </>
        }
      >
        {confirming?.action === "reset"
          ? "Use this when a tablet is lost or replaced. The old device can no longer sign in here; pair the new one with a fresh code."
          : "Cashiers can't sign in on this terminal until it's reactivated. Its sales and reports stay as they are."}
      </AlertModal>

      {pairing && <PairingCodeDialog terminal={pairing.terminal} code={pairing.code} expiresAt={pairing.expiresAt} onClose={() => setPairing(null)} />}
    </div>
  );
}
