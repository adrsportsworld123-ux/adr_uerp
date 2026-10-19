"use client";

// Customize the sales dashboard (dashboard.configure; erp-core-go
// internal/dashboard): which widgets each role sees, in what order, and the
// monthly sales targets the "Sales tracking" widget measures against. A
// role sees the dashboard at all only with the dashboard.view permission,
// given under Roles & Permissions (Merchant Admin has it by default); a role
// left on "Default" sees every widget.
import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { ArrowDown, ArrowLeft, ArrowUp, Loader2, Plus, RotateCcw, Save, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Branch } from "@/lib/types";
import { usePermissions, missingPermissionHint } from "@/lib/permissions";
import { rupees } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/data/page-header";

type Widget = { key: string; label: string; description: string };
type RoleConfig = { role_id: string; role_name: string; can_view: boolean; widgets: string[]; customized: boolean; updated_by: string | null; updated_at: string | null };
type TargetRow = { branch_id: string | null; monthly_target: string };

export default function DashboardSettingsPage() {
  const { can } = usePermissions();
  const [widgets, setWidgets] = useState<Widget[]>([]);
  const [roles, setRoles] = useState<RoleConfig[]>([]);
  const [roleId, setRoleId] = useState("");
  const [draft, setDraft] = useState<string[]>([]);
  const [branches, setBranches] = useState<Branch[]>([]);
  const [targets, setTargets] = useState<TargetRow[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(() => {
    api
      .get<{ roles: RoleConfig[]; widgets: Widget[] }>("/api/v1/dashboard/roles")
      .then((d) => {
        setRoles(d.roles);
        setWidgets(d.widgets);
        setRoleId((cur) => {
          const id = cur || d.roles.find((r) => r.can_view)?.role_id || d.roles[0]?.role_id || "";
          setDraft(d.roles.find((r) => r.role_id === id)?.widgets ?? []);
          return id;
        });
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load the dashboard settings"));
    api
      .get<{ targets: TargetRow[] }>("/api/v1/dashboard/targets")
      .then((d) => setTargets(d.targets.map((t) => ({ branch_id: t.branch_id, monthly_target: t.monthly_target }))))
      .catch(() => {});
  }, []);
  useEffect(load, [load]);
  useEffect(() => {
    api.get<{ branches: Branch[] }>("/api/v1/branches?status=active").then((d) => setBranches(d.branches)).catch(() => {});
  }, []);

  const role = roles.find((r) => r.role_id === roleId);
  const pick = (id: string) => {
    setRoleId(id);
    setDraft(roles.find((r) => r.role_id === id)?.widgets ?? []);
  };
  const toggle = (k: string) => setDraft((d) => (d.includes(k) ? d.filter((x) => x !== k) : [...d, k]));
  const move = (k: string, by: number) =>
    setDraft((d) => {
      const i = d.indexOf(k);
      const j = i + by;
      if (i < 0 || j < 0 || j >= d.length) return d;
      const n = [...d];
      [n[i], n[j]] = [n[j], n[i]];
      return n;
    });
  const dirty = role && JSON.stringify(role.widgets) !== JSON.stringify(draft);

  async function saveRole(reset = false) {
    if (!role) return;
    setBusy(true);
    try {
      await api.put(`/api/v1/dashboard/roles/${role.role_id}`, { widgets: reset ? null : draft });
      toast.success(reset ? `${role.role_name} is back on the default dashboard` : `Saved ${role.role_name}'s dashboard`);
      load();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not save");
    } finally {
      setBusy(false);
    }
  }

  async function saveTargets() {
    setBusy(true);
    try {
      const body = { targets: targets.filter((t) => Number(t.monthly_target) > 0).map((t) => ({ branch_id: t.branch_id, monthly_target: Number(t.monthly_target) })) };
      const d = await api.put<{ targets: TargetRow[] }>("/api/v1/dashboard/targets", body);
      setTargets(d.targets.map((t) => ({ branch_id: t.branch_id, monthly_target: t.monthly_target })));
      toast.success("Sales targets saved");
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not save the targets");
    } finally {
      setBusy(false);
    }
  }

  if (!can("dashboard.configure"))
    return (
      <div className="flex flex-col gap-3">
        <PageHeader title="Customize dashboard" />
        <p className="text-sm text-muted-foreground">{missingPermissionHint("dashboard.configure")}</p>
      </div>
    );

  const usedBranches = new Set(targets.map((t) => t.branch_id ?? ""));
  const ordered = [...draft.map((k) => widgets.find((w) => w.key === k)!).filter(Boolean), ...widgets.filter((w) => !draft.includes(w.key))];

  return (
    <div className="flex max-w-5xl flex-col gap-5">
      <Link href="/dashboard" className="flex w-fit items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
        <ArrowLeft className="size-4" /> Dashboard
      </Link>
      <PageHeader title="Customize dashboard" description="Choose what each role sees on the sales dashboard, and set the monthly targets it tracks." />
      {error && (
        <p className="text-sm text-destructive" role="alert">
          {error}
        </p>
      )}

      <section className="grid gap-4 rounded-xl border bg-card p-4 md:grid-cols-[220px_1fr]" aria-labelledby="roles-h">
        <div>
          <h2 id="roles-h" className="mb-2 text-sm font-semibold">
            Roles
          </h2>
          <ul className="flex flex-col gap-1" role="listbox" aria-label="Roles">
            {roles.map((r) => (
              <li key={r.role_id}>
                <button
                  type="button"
                  role="option"
                  aria-selected={r.role_id === roleId}
                  onClick={() => pick(r.role_id)}
                  className={`w-full rounded-lg px-3 py-2 text-left text-sm ${r.role_id === roleId ? "bg-accent font-medium" : "hover:bg-muted"}`}
                >
                  {r.role_name}
                  <span className="block text-xs text-muted-foreground">
                    {!r.can_view ? "No dashboard access" : r.customized ? `${r.widgets.length} widget${r.widgets.length === 1 ? "" : "s"}` : "Default · every widget"}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        </div>
        {role && (
          <div className="flex min-w-0 flex-col gap-3">
            <div className="flex flex-wrap items-baseline justify-between gap-2">
              <h3 className="text-base font-semibold">{role.role_name}</h3>
              {role.updated_by && <span className="text-xs text-muted-foreground">Last changed by {role.updated_by}</span>}
            </div>
            {!role.can_view && (
              <p className="rounded-lg bg-warn-soft px-3 py-2 text-sm text-warn">
                This role can&apos;t open the dashboard yet. Give it the <span className="font-mono text-xs">dashboard.view</span> permission under{" "}
                <Link href="/rbac" className="underline">
                  Roles &amp; Permissions
                </Link>
                ; the widgets chosen here apply from then.
              </p>
            )}
            <ul className="flex flex-col gap-1.5" aria-label={`${role.role_name}'s widgets`}>
              {ordered.map((w) => {
                const on = draft.includes(w.key);
                const i = draft.indexOf(w.key);
                return (
                  <li key={w.key} className={`flex items-start gap-3 rounded-lg border px-3 py-2 ${on ? "" : "opacity-70"}`}>
                    <input type="checkbox" id={`w-${w.key}`} checked={on} onChange={() => toggle(w.key)} className="mt-1 size-4" />
                    <label htmlFor={`w-${w.key}`} className="min-w-0 flex-1 cursor-pointer">
                      <span className="text-sm font-medium">{w.label}</span>
                      <span className="block text-xs text-muted-foreground">{w.description}</span>
                    </label>
                    {on && (
                      <span className="flex shrink-0 gap-1">
                        <Button variant="ghost" size="icon-sm" aria-label={`Move ${w.label} up`} disabled={i === 0} onClick={() => move(w.key, -1)}>
                          <ArrowUp />
                        </Button>
                        <Button variant="ghost" size="icon-sm" aria-label={`Move ${w.label} down`} disabled={i === draft.length - 1} onClick={() => move(w.key, 1)}>
                          <ArrowDown />
                        </Button>
                      </span>
                    )}
                  </li>
                );
              })}
            </ul>
            <div className="flex flex-wrap gap-2">
              <Button disabled={busy || !dirty} onClick={() => saveRole()}>
                {busy ? <Loader2 className="animate-spin" /> : <Save />} Save {role.role_name}
              </Button>
              {role.customized && (
                <Button variant="outline" disabled={busy} onClick={() => saveRole(true)}>
                  <RotateCcw /> Back to default
                </Button>
              )}
            </div>
            <p className="text-xs text-muted-foreground">Someone with more than one role sees every widget any of their roles may. Branch Managers see only their own branches, as everywhere.</p>
          </div>
        )}
      </section>

      <section className="flex flex-col gap-3 rounded-xl border bg-card p-4" aria-labelledby="targets-h">
        <div>
          <h2 id="targets-h" className="text-sm font-semibold">
            Monthly sales targets
          </h2>
          <p className="text-xs text-muted-foreground">
            “Sales tracking” compares each month with these. The whole-business target is used when no branch is picked; without one, the branch targets are added up.
          </p>
        </div>
        <table className="w-full max-w-xl text-sm">
          <thead className="text-left text-xs text-muted-foreground">
            <tr>
              <th className="pb-1 font-medium">For</th>
              <th className="pb-1 font-medium">Target per month (₹)</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {targets.map((t, i) => (
              <tr key={i}>
                <td className="py-1 pr-2">
                  <select
                    aria-label={`Target ${i + 1} branch`}
                    value={t.branch_id ?? ""}
                    onChange={(e) => setTargets((ts) => ts.map((x, j) => (j === i ? { ...x, branch_id: e.target.value || null } : x)))}
                    className="h-9 w-full rounded-lg border bg-card px-2 text-sm"
                  >
                    <option value="" disabled={usedBranches.has("") && (t.branch_id ?? "") !== ""}>
                      Whole business
                    </option>
                    {branches.map((b) => (
                      <option key={b.branch_id} value={b.branch_id} disabled={usedBranches.has(b.branch_id) && t.branch_id !== b.branch_id}>
                        {b.name}
                      </option>
                    ))}
                  </select>
                </td>
                <td className="py-1 pr-2">
                  <Input
                    aria-label={`Target ${i + 1} amount`}
                    type="number"
                    min={1}
                    step="1000"
                    className="tabular w-44 text-right"
                    value={t.monthly_target}
                    onChange={(e) => setTargets((ts) => ts.map((x, j) => (j === i ? { ...x, monthly_target: e.target.value } : x)))}
                  />
                  {Number(t.monthly_target) > 0 && <span className="ml-2 text-xs text-muted-foreground">{rupees(t.monthly_target)}</span>}
                </td>
                <td className="py-1">
                  <Button variant="ghost" size="icon-sm" aria-label={`Remove target ${i + 1}`} onClick={() => setTargets((ts) => ts.filter((_, j) => j !== i))}>
                    <Trash2 />
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        <div className="flex flex-wrap gap-2">
          <Button
            variant="outline"
            size="sm"
            disabled={targets.length > branches.length}
            onClick={() => {
              const free = !usedBranches.has("") ? null : (branches.find((b) => !usedBranches.has(b.branch_id))?.branch_id ?? null);
              setTargets((ts) => [...ts, { branch_id: free, monthly_target: "" }]);
            }}
          >
            <Plus /> Add a target
          </Button>
          <Button size="sm" disabled={busy} onClick={saveTargets}>
            <Save /> Save targets
          </Button>
        </div>
      </section>
    </div>
  );
}
