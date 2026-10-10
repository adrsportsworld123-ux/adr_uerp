"use client";

// UACL — user/role/permission administration (erp-core-go's
// internal/rbac, migrations/025_rbac.sql). Gated by rbac.manage,
// Merchant Admin only. `roles`/`permissions`/`role_permissions`/
// `user_roles` have existed since Phase 0, but this is the first UI (and
// first API) anywhere in this system that can manage them directly —
// before this, every role/permission grant was seeded by a developer
// editing a migration file.
import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { RBACPermission, RBACRole, Employee } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { AlertModal } from "@/components/ui/dialog";
import { PageHeader } from "@/components/data/page-header";

// Permission codes are "<module>.<action>"; grouping by module turns a
// flat list of 25+ codes into something an admin can scan.
function groupPermissions(perms: RBACPermission[]) {
  const groups = new Map<string, RBACPermission[]>();
  for (const p of perms) {
    const mod = p.code.split(".")[0];
    groups.set(mod, [...(groups.get(mod) ?? []), p]);
  }
  return Array.from(groups.entries()).sort(([a], [b]) => a.localeCompare(b));
}

function moduleLabel(mod: string) {
  const s = mod.replace(/_/g, " ");
  return s.charAt(0).toUpperCase() + s.slice(1);
}

export default function RBACPage() {
  const [roles, setRoles] = useState<RBACRole[]>([]);
  const [permissions, setPermissions] = useState<RBACPermission[]>([]);
  const [employees, setEmployees] = useState<Employee[]>([]);
  const [error, setError] = useState<string | null>(null);

  const [newRoleName, setNewRoleName] = useState("");
  const [selectedRole, setSelectedRole] = useState<RBACRole | null>(null);
  const [checkedIds, setCheckedIds] = useState<Set<string>>(new Set());
  const [deletingRole, setDeletingRole] = useState<RBACRole | null>(null);
  const [pendingRole, setPendingRole] = useState<RBACRole | null>(null);

  const [assignEmployeeId, setAssignEmployeeId] = useState("");
  const [assignRoleId, setAssignRoleId] = useState("");

  const load = useCallback(() => {
    api
      .get<{ roles: RBACRole[] }>("/api/v1/rbac/roles")
      .then((d) => {
        setRoles(d.roles);
        setError(null);
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load roles"));
    api.get<{ permissions: RBACPermission[] }>("/api/v1/rbac/permissions").then((d) => setPermissions(d.permissions)).catch(() => {});
    api.get<{ employees: Employee[] }>("/api/v1/hr/employees").then((d) => setEmployees(d.employees)).catch(() => {});
  }, []);
  useEffect(load, [load]);

  // Phase 15: which branches' data the role's holders see. Takes effect on
  // their next request; the database enforces it for every list and report.
  async function setDataScope(role: RBACRole, scope: RBACRole["data_scope"]) {
    try {
      const updated = await api.patch<RBACRole>(`/api/v1/rbac/roles/${role.id}`, { data_scope: scope });
      setRoles((rs) => rs.map((r) => (r.id === updated.id ? updated : r)));
      setSelectedRole((sel) => (sel && sel.id === updated.id ? { ...sel, data_scope: updated.data_scope } : sel));
      toast.success(`${role.name}: ${scope === "all" ? "all branches" : "own branch only"}`);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not change data access");
    }
  }

  async function createRole() {
    try {
      await api.post("/api/v1/rbac/roles", { name: newRoleName });
      toast.success("Role created");
      setNewRoleName("");
      load();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not create role");
    }
  }

  const savedIds = new Set(selectedRole ? permissions.filter((p) => selectedRole.permission_codes.includes(p.code)).map((p) => p.id) : []);
  const changedCount =
    Array.from(checkedIds).filter((id) => !savedIds.has(id)).length + Array.from(savedIds).filter((id) => !checkedIds.has(id)).length;

  // Switching to another role with unsaved ticks would silently drop them.
  function requestRole(role: RBACRole) {
    if (selectedRole && role.id !== selectedRole.id && changedCount > 0) setPendingRole(role);
    else selectRole(role);
  }

  function toggleGroup(ids: string[], on: boolean) {
    setCheckedIds((prev) => {
      const next = new Set(prev);
      for (const id of ids) {
        if (on) next.add(id);
        else next.delete(id);
      }
      return next;
    });
  }

  async function deleteRole(role: RBACRole) {
    setDeletingRole(null);
    try {
      await api.delete(`/api/v1/rbac/roles/${role.id}`);
      toast.success(`Deleted role “${role.name}”`);
      if (selectedRole?.id === role.id) setSelectedRole(null);
      load();
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : "Could not delete role");
    }
  }

  function selectRole(role: RBACRole) {
    setSelectedRole(role);
    setCheckedIds(new Set(permissions.filter((p) => role.permission_codes.includes(p.code)).map((p) => p.id)));
  }

  function togglePermission(id: string) {
    setCheckedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  async function savePermissions() {
    if (!selectedRole) return;
    try {
      const updated = await api.patch<RBACRole>(`/api/v1/rbac/roles/${selectedRole.id}/permissions`, {
        permission_ids: Array.from(checkedIds),
      });
      toast.success("Permissions updated");
      setSelectedRole(updated);
      load();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not update permissions");
    }
  }

  async function assignRole() {
    if (!assignEmployeeId || !assignRoleId) return;
    try {
      await api.post(`/api/v1/rbac/users/${assignEmployeeId}/roles`, { role_id: assignRoleId });
      toast.success("Role assigned");
      setAssignRoleId("");
      load();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not assign role");
    }
  }

  async function revokeRole(employeeId: string, roleId: string) {
    try {
      await api.delete(`/api/v1/rbac/users/${employeeId}/roles/${roleId}`);
      const roleName = roles.find((r) => r.id === roleId)?.name ?? "Role";
      // Revoking is one click, so make it one click to put back.
      toast.success(`${roleName} revoked`, {
        action: {
          label: "Undo",
          onClick: () => {
            api
              .post(`/api/v1/rbac/users/${employeeId}/roles`, { role_id: roleId })
              .then(() => {
                toast.success(`${roleName} restored`);
                load();
              })
              .catch((e) => toast.error(e instanceof ApiError ? e.message : "Could not restore role"));
          },
        },
      });
      load();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not revoke role");
    }
  }

  function roleIdByName(name: string): string | undefined {
    return roles.find((r) => r.name === name)?.id;
  }

  return (
    <div className="flex flex-col gap-6 max-w-5xl">
      <PageHeader
        title="Roles & Permissions"
        description="Who can do what. Every permission is enforced by the server; this screen changes what each role is granted."
      />
      {error && <p className="text-sm text-destructive">{error}</p>}

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Roles</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <div className="flex items-end gap-4">
            <div className="flex flex-col gap-2 w-56">
              <Label htmlFor="newRoleName">New role name</Label>
              <Input id="newRoleName" value={newRoleName} onChange={(e) => setNewRoleName(e.target.value)} placeholder="Accountant" />
            </div>
            <Button onClick={createRole} disabled={!newRoleName}>Add role</Button>
          </div>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Permissions</TableHead>
                <TableHead>Data access</TableHead>
                <TableHead>Users</TableHead>
                <TableHead></TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {roles.map((role) => (
                <TableRow
                  key={role.id}
                  className={`cursor-pointer ${selectedRole?.id === role.id ? "bg-primary-soft" : ""}`}
                  onClick={() => requestRole(role)}
                >
                  <TableCell>{role.name}</TableCell>
                  <TableCell className="text-xs">{role.permission_codes.length} permission(s)</TableCell>
                  <TableCell className="text-xs">{role.data_scope === "all" ? "All branches" : "Own branch only"}</TableCell>
                  <TableCell>{role.user_count}</TableCell>
                  <TableCell>
                    <Button
                      size="sm"
                      variant="destructive"
                      disabled={role.user_count > 0}
                      title={
                        role.user_count > 0
                          ? `Assigned to ${role.user_count} user${role.user_count === 1 ? "" : "s"} — revoke it from them first`
                          : "Delete role"
                      }
                      onClick={(e) => {
                        e.stopPropagation();
                        setDeletingRole(role);
                      }}
                    >
                      Delete
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      {selectedRole && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Permissions for &quot;{selectedRole.name}&quot;</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            <div className="flex flex-wrap items-center gap-2 text-sm">
              <label htmlFor="role-data-scope" className="font-medium">
                Data access
              </label>
              <select
                id="role-data-scope"
                value={selectedRole.data_scope}
                onChange={(e) => setDataScope(selectedRole, e.target.value as RBACRole["data_scope"])}
                className="h-8 rounded-md border bg-card px-1.5 text-sm"
              >
                <option value="all">All branches</option>
                <option value="branch">Own branch only</option>
              </select>
              <span className="text-muted-foreground">
                Own branch: sales, stock, cash and books of their home branch (and any extra ones on Data Access) only. Saved at once.
              </span>
            </div>
            <div className="flex max-h-[28rem] flex-col gap-4 overflow-auto pr-1">
              {groupPermissions(permissions).map(([mod, perms]) => {
                const ids = perms.map((p) => p.id);
                const allOn = ids.every((id) => checkedIds.has(id));
                return (
                  <fieldset key={mod} className="rounded-lg border p-3">
                    <legend className="px-1 text-sm font-semibold">{moduleLabel(mod)}</legend>
                    <div className="mb-2 flex justify-end">
                      <button type="button" className="text-xs text-primary hover:underline" onClick={() => toggleGroup(ids, !allOn)}>
                        {allOn ? "Clear all" : "Select all"}
                      </button>
                    </div>
                    <div className="grid gap-2 sm:grid-cols-2">
                      {perms.map((p) => (
                        <label key={p.id} className="flex items-start gap-2 text-sm">
                          <input
                            type="checkbox"
                            className="mt-1 accent-[var(--primary)]"
                            checked={checkedIds.has(p.id)}
                            onChange={() => togglePermission(p.id)}
                          />
                          <span>
                            <span className="font-mono text-xs">{p.code}</span>
                            <br />
                            <span className="text-xs text-muted-foreground">{p.description}</span>
                          </span>
                        </label>
                      ))}
                    </div>
                  </fieldset>
                );
              })}
            </div>
            <div className="flex items-center gap-3">
              <Button onClick={savePermissions} disabled={changedCount === 0}>
                Save permissions
              </Button>
              {changedCount > 0 && (
                <span className="text-sm text-warn">
                  {changedCount} unsaved change{changedCount === 1 ? "" : "s"}
                </span>
              )}
            </div>
          </CardContent>
        </Card>
      )}

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Assign a role to a user</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <div className="flex items-end gap-4 flex-wrap">
            <div className="flex flex-col gap-2 w-56">
              <Label>Employee</Label>
              <Select value={assignEmployeeId} onValueChange={(v) => setAssignEmployeeId(v ?? "")} items={Object.fromEntries(employees.map((e) => [e.id, e.name]))}>
                <SelectTrigger aria-label="Employee"><SelectValue placeholder="Select employee" /></SelectTrigger>
                <SelectContent>
                  {employees.map((e) => (
                    <SelectItem key={e.id} value={e.id}>{e.name}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-2 w-44">
              <Label>Role</Label>
              <Select value={assignRoleId} onValueChange={(v) => setAssignRoleId(v ?? "")} items={Object.fromEntries(roles.map((r) => [r.id, r.name]))}>
                <SelectTrigger aria-label="Role"><SelectValue placeholder="Select role" /></SelectTrigger>
                <SelectContent>
                  {roles.map((r) => (
                    <SelectItem key={r.id} value={r.id}>{r.name}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <Button onClick={assignRole} disabled={!assignEmployeeId || !assignRoleId}>Assign</Button>
          </div>

          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Employee</TableHead>
                <TableHead>Roles</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {employees.map((e) => (
                <TableRow key={e.id}>
                  <TableCell>{e.name}</TableCell>
                  <TableCell className="flex gap-1 flex-wrap">
                    {e.roles.map((roleName) => {
                      const rid = roleIdByName(roleName);
                      return (
                        <Badge key={roleName} variant="outline" className="gap-1">
                          {roleName}
                          {rid && (
                            <button
                              type="button"
                              className="ml-1 text-muted-foreground hover:text-destructive"
                              onClick={() => revokeRole(e.id, rid)}
                              aria-label={`Revoke ${roleName}`}
                            >
                              ×
                            </button>
                          )}
                        </Badge>
                      );
                    })}
                    {e.roles.length === 0 && <span className="text-muted-foreground text-xs">No roles</span>}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
      <AlertModal
        open={!!deletingRole}
        onOpenChange={(o) => !o && setDeletingRole(null)}
        title={`Delete role “${deletingRole?.name ?? ""}”?`}
        actions={
          <>
            <Button variant="outline" onClick={() => setDeletingRole(null)}>
              Cancel
            </Button>
            <Button variant="destructive" onClick={() => deletingRole && deleteRole(deletingRole)}>
              Delete role
            </Button>
          </>
        }
      >
        No one holds this role, so no one loses access. Its permission set is removed and can&rsquo;t be recovered.
      </AlertModal>
      <AlertModal
        open={!!pendingRole}
        onOpenChange={(o) => !o && setPendingRole(null)}
        title="Discard unsaved permission changes?"
        actions={
          <>
            <Button variant="outline" onClick={() => setPendingRole(null)}>
              Keep editing
            </Button>
            <Button
              variant="destructive"
              onClick={() => {
                if (pendingRole) selectRole(pendingRole);
                setPendingRole(null);
              }}
            >
              Discard
            </Button>
          </>
        }
      >
        You have {changedCount} unsaved change{changedCount === 1 ? "" : "s"} to &ldquo;{selectedRole?.name}&rdquo;.
      </AlertModal>
    </div>
  );
}
