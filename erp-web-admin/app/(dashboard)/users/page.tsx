"use client";

// User Management (erp-core-go internal/users, migrations/062, users.manage):
// every login in one list, and per user — reset the password, set or remove
// the POS / approval PIN, unlock after failed sign-ins, deactivate a leaver
// (signed out everywhere) or bring them back, the home branch, extra branch
// access and roles. Logins are created with an employee (HR → Employees);
// roles and branch access use the same endpoints as Roles & Permissions and
// Data Access, so all three screens stay in step.
import { useCallback, useEffect, useState } from "react";
import { KeyRound, Loader2, Search, ShieldCheck } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import type { Branch } from "@/lib/types";
import { dateTime } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Modal } from "@/components/ui/dialog";
import { PageHeader } from "@/components/data/page-header";
import { Panel, selectClass } from "@/components/data/panel";
import { StatusChip } from "@/components/data/status-chip";

type Ref = { branch_id: string; name: string };
type User = {
  user_id: string;
  name: string;
  email: string | null;
  phone: string | null;
  employee_code: string | null;
  status: "active" | "inactive" | "locked";
  locked: boolean;
  failed_login_attempts: number;
  has_password: boolean;
  has_pin: boolean;
  password_changed_at: string | null;
  created_at: string;
  home_branch: Ref | null;
  extra_branches: Ref[];
  roles: { role_id: string; name: string }[];
};
type Role = { id: string; name: string };

const errMsg = (e: unknown, fallback: string) => (e instanceof ApiError ? e.message : fallback);

export default function UsersPage() {
  const { can } = usePermissions();
  const allowed = can("users.manage");
  const [users, setUsers] = useState<User[] | null>(null);
  const [q, setQ] = useState("");
  const [status, setStatus] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [branches, setBranches] = useState<Branch[]>([]);
  const [roles, setRoles] = useState<Role[]>([]);
  const [editing, setEditing] = useState<User | null>(null);

  const load = useCallback(() => {
    const p = new URLSearchParams();
    if (q.trim()) p.set("q", q.trim());
    if (status) p.set("status", status);
    api
      .get<{ users: User[] }>(`/api/v1/users?${p}`)
      .then((d) => {
        setUsers(d.users);
        setError(null);
      })
      .catch((e) => setError(errMsg(e, "Could not load users")));
  }, [q, status]);

  useEffect(() => {
    const t = setTimeout(load, 250);
    return () => clearTimeout(t);
  }, [load]);
  useEffect(() => {
    api.get<{ branches: Branch[] }>("/api/v1/branches?status=active").then((d) => setBranches(d.branches)).catch(() => {});
    api.get<{ roles: Role[] }>("/api/v1/rbac/roles").then((d) => setRoles(d.roles)).catch(() => {});
  }, []);

  const replace = (u: User) => {
    setUsers((list) => list?.map((x) => (x.user_id === u.user_id ? u : x)) ?? null);
    setEditing((cur) => (cur?.user_id === u.user_id ? u : cur));
  };

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Users"
        description="Passwords, PINs, branch access and roles for everyone who signs in. New logins are added with an employee under HR."
      />
      {!allowed && <p className="text-sm text-muted-foreground">{missingPermissionHint("users.manage")}</p>}
      <div className="flex flex-wrap items-end gap-3">
        <div className="relative w-72">
          <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" aria-hidden />
          <Input aria-label="Search users" className="pl-8" placeholder="Name, email, phone or code" value={q} onChange={(e) => setQ(e.target.value)} />
        </div>
        <select aria-label="Status" value={status} onChange={(e) => setStatus(e.target.value)} className={selectClass}>
          <option value="">All statuses</option>
          <option value="active">Active</option>
          <option value="locked">Locked</option>
          <option value="inactive">Inactive</option>
        </select>
      </div>
      {error && <p className="text-sm text-destructive">{error}</p>}
      <Panel title="Users">
        {users == null ? (
          <p className="flex items-center gap-2 text-sm text-muted-foreground">
            <Loader2 className="size-4 animate-spin" aria-hidden /> Loading…
          </p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="text-left text-xs text-muted-foreground">
                <tr>
                  <th className="pb-2 font-medium">User</th>
                  <th className="pb-2 font-medium">Roles</th>
                  <th className="pb-2 font-medium">Branches</th>
                  <th className="pb-2 font-medium">Sign-in</th>
                  <th className="pb-2 font-medium">Status</th>
                  <th className="pb-2" />
                </tr>
              </thead>
              <tbody>
                {users.map((u) => (
                  <tr key={u.user_id} className="border-t align-top" data-testid="user-row">
                    <td className="py-2">
                      <div className="font-medium">{u.name}</div>
                      <div className="text-xs text-muted-foreground">{[u.employee_code, u.email, u.phone].filter(Boolean).join(" · ")}</div>
                    </td>
                    <td className="py-2">{u.roles.map((r) => r.name).join(", ") || "—"}</td>
                    <td className="py-2">
                      {u.home_branch?.name ?? "No home branch"}
                      {u.extra_branches.length > 0 && <div className="text-xs text-muted-foreground">+ {u.extra_branches.map((b) => b.name).join(", ")}</div>}
                    </td>
                    <td className="py-2 text-xs">
                      {u.has_password ? "Password" : "No password"} · {u.has_pin ? "PIN" : "No PIN"}
                      {u.failed_login_attempts > 0 && <div className="text-warn">{u.failed_login_attempts} failed attempt(s)</div>}
                    </td>
                    <td className="py-2">
                      <StatusChip
                        status={u.locked ? "locked" : u.status}
                        tone={u.locked ? "bad" : u.status === "active" ? "ok" : "muted"}
                        label={u.locked ? "Locked" : u.status === "active" ? "Active" : "Inactive"}
                      />
                    </td>
                    <td className="py-2 text-right">
                      <Button size="sm" variant="outline" disabled={!allowed} onClick={() => setEditing(u)}>
                        Manage
                      </Button>
                    </td>
                  </tr>
                ))}
                {users.length === 0 && (
                  <tr>
                    <td colSpan={6} className="py-6 text-center text-muted-foreground">
                      No users match.
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        )}
      </Panel>
      {editing && <ManageUser user={editing} branches={branches} roles={roles} onChange={replace} onClose={() => setEditing(null)} />}
    </div>
  );
}

function ManageUser({
  user,
  branches,
  roles,
  onChange,
  onClose,
}: {
  user: User;
  branches: Branch[];
  roles: Role[];
  onChange: (u: User) => void;
  onClose: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [pin, setPin] = useState("");
  const [home, setHome] = useState(user.home_branch?.branch_id ?? "");
  const [extra, setExtra] = useState<Set<string>>(new Set(user.extra_branches.map((b) => b.branch_id)));

  async function act(fn: () => Promise<User | void>, done: string) {
    setBusy(true);
    try {
      const u = await fn();
      if (u) onChange(u);
      toast.success(done);
      return true;
    } catch (e) {
      toast.error(errMsg(e, "That didn't work"));
      return false;
    } finally {
      setBusy(false);
    }
  }
  const reload = () => api.get<User>(`/api/v1/users/${user.user_id}`);

  const passwordInvalid = password.length > 0 && (password.length < 8 || password !== confirm);
  const pinInvalid = pin.length > 0 && !/^\d{4,6}$/.test(pin);
  const extraChanged =
    extra.size !== user.extra_branches.length || user.extra_branches.some((b) => !extra.has(b.branch_id));

  return (
    <Modal open onOpenChange={(o) => !o && onClose()} title={user.name} description={[user.employee_code, user.email].filter(Boolean).join(" · ")} width={620}>
      <div className="flex flex-col gap-5 text-sm">
        <section className="flex flex-wrap items-center gap-2" aria-label="Account status">
          <StatusChip status={user.status} tone={user.locked ? "bad" : user.status === "active" ? "ok" : "muted"} label={user.locked ? "Locked" : user.status === "active" ? "Active" : "Inactive"} />
          {user.password_changed_at && <span className="text-xs text-muted-foreground">Password changed {dateTime(user.password_changed_at)}</span>}
          <span className="flex-1" />
          {user.locked && (
            <Button size="sm" variant="outline" disabled={busy} onClick={() => act(() => api.patch<User>(`/api/v1/users/${user.user_id}`, { unlock: true }), "Unlocked")}>
              Unlock
            </Button>
          )}
          {user.status === "inactive" ? (
            <Button size="sm" disabled={busy} onClick={() => act(() => api.patch<User>(`/api/v1/users/${user.user_id}`, { status: "active" }), `${user.name} can sign in again`)}>
              Activate
            </Button>
          ) : (
            <Button
              size="sm"
              variant="destructive"
              disabled={busy}
              onClick={() => act(() => api.patch<User>(`/api/v1/users/${user.user_id}`, { status: "inactive" }), `${user.name} is deactivated and signed out`)}
            >
              Deactivate
            </Button>
          )}
        </section>

        <section className="space-y-2" aria-label="Password">
          <h3 className="flex items-center gap-1.5 font-semibold">
            <KeyRound className="size-4" aria-hidden /> Password
          </h3>
          <div className="grid gap-2 sm:grid-cols-2">
            <div className="space-y-1">
              <Label htmlFor="new-password">New password</Label>
              <Input id="new-password" type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} />
            </div>
            <div className="space-y-1">
              <Label htmlFor="confirm-password">Confirm</Label>
              <Input id="confirm-password" type="password" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} />
            </div>
          </div>
          <p className={passwordInvalid ? "text-xs text-destructive" : "text-xs text-muted-foreground"}>
            {passwordInvalid ? "At least 8 characters, typed the same twice" : "8 characters or more. Their other sessions are signed out."}
          </p>
          <Button
            size="sm"
            disabled={busy || !password || passwordInvalid}
            onClick={async () => {
              if (await act(() => api.put<User>(`/api/v1/users/${user.user_id}/password`, { new_password: password }), "Password set")) {
                setPassword("");
                setConfirm("");
              }
            }}
          >
            Set password
          </Button>
        </section>

        <section className="space-y-2" aria-label="PIN">
          <h3 className="font-semibold">PIN (POS sign-in and manager approvals)</h3>
          {!user.employee_code ? (
            <p className="text-xs text-muted-foreground">This user has no employee code; PIN sign-in is code + PIN. Add a code under HR → Employees.</p>
          ) : (
            <div className="flex flex-wrap items-end gap-2">
              <div className="space-y-1">
                <Label htmlFor="new-pin">New PIN</Label>
                <Input id="new-pin" className="w-32" type="password" inputMode="numeric" maxLength={6} value={pin} onChange={(e) => setPin(e.target.value.replace(/\D/g, ""))} />
              </div>
              <Button
                size="sm"
                disabled={busy || !pin || pinInvalid}
                onClick={async () => {
                  if (await act(() => api.put<User>(`/api/v1/users/${user.user_id}/pin`, { pin }), "PIN set")) setPin("");
                }}
              >
                Set PIN
              </Button>
              {user.has_pin && (
                <Button size="sm" variant="outline" disabled={busy} onClick={() => act(() => api.delete<User>(`/api/v1/users/${user.user_id}/pin`), "PIN removed")}>
                  Remove PIN
                </Button>
              )}
              <span className={pinInvalid ? "text-xs text-destructive" : "text-xs text-muted-foreground"}>4 to 6 digits, not all the same</span>
            </div>
          )}
        </section>

        <section className="space-y-2" aria-label="Branch access">
          <h3 className="font-semibold">Branch access</h3>
          <div className="flex flex-wrap items-end gap-2">
            <div className="space-y-1">
              <Label htmlFor="home-branch">Home branch</Label>
              <select id="home-branch" value={home} onChange={(e) => setHome(e.target.value)} className={`${selectClass} w-56`}>
                <option value="">No home branch</option>
                {branches.map((b) => (
                  <option key={b.branch_id} value={b.branch_id}>
                    {b.name}
                  </option>
                ))}
              </select>
            </div>
            <Button
              size="sm"
              variant="outline"
              disabled={busy || home === (user.home_branch?.branch_id ?? "")}
              onClick={() => act(() => api.patch<User>(`/api/v1/users/${user.user_id}`, { home_branch_id: home || null }), "Home branch saved")}
            >
              Save home branch
            </Button>
          </div>
          <fieldset className="space-y-1">
            <legend className="text-xs text-muted-foreground">Also sees these branches (when their role is limited to its own branch)</legend>
            <div className="flex flex-wrap gap-x-4 gap-y-1">
              {branches
                .filter((b) => b.branch_id !== home)
                .map((b) => (
                  <label key={b.branch_id} className="flex items-center gap-1.5">
                    <input
                      type="checkbox"
                      checked={extra.has(b.branch_id)}
                      onChange={(e) =>
                        setExtra((s) => {
                          const n = new Set(s);
                          if (e.target.checked) n.add(b.branch_id);
                          else n.delete(b.branch_id);
                          return n;
                        })
                      }
                    />
                    {b.name}
                  </label>
                ))}
            </div>
          </fieldset>
          <Button
            size="sm"
            variant="outline"
            disabled={busy || !extraChanged}
            onClick={() =>
              act(async () => {
                await api.put(`/api/v1/rbac/users/${user.user_id}/branches`, { branch_ids: [...extra] });
                return reload();
              }, "Branch access saved")
            }
          >
            Save branch access
          </Button>
        </section>

        <section className="space-y-2" aria-label="Roles">
          <h3 className="flex items-center gap-1.5 font-semibold">
            <ShieldCheck className="size-4" aria-hidden /> Roles
          </h3>
          <div className="flex flex-wrap gap-x-4 gap-y-1">
            {roles.map((r) => {
              const has = user.roles.some((x) => x.role_id === r.id);
              return (
                <label key={r.id} className="flex items-center gap-1.5">
                  <input
                    type="checkbox"
                    checked={has}
                    disabled={busy}
                    onChange={() =>
                      act(async () => {
                        if (has) await api.delete(`/api/v1/rbac/users/${user.user_id}/roles/${r.id}`);
                        else await api.post(`/api/v1/rbac/users/${user.user_id}/roles`, { role_id: r.id });
                        return reload();
                      }, has ? `${r.name} removed` : `${r.name} given`)
                    }
                  />
                  {r.name}
                </label>
              );
            })}
            {roles.length === 0 && <span className="text-xs text-muted-foreground">Roles need the Roles &amp; Permissions access.</span>}
          </div>
        </section>
      </div>
    </Modal>
  );
}
