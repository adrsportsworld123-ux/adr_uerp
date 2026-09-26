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

export default function RBACPage() {
  const [roles, setRoles] = useState<RBACRole[]>([]);
  const [permissions, setPermissions] = useState<RBACPermission[]>([]);
  const [employees, setEmployees] = useState<Employee[]>([]);
  const [error, setError] = useState<string | null>(null);

  const [newRoleName, setNewRoleName] = useState("");
  const [selectedRole, setSelectedRole] = useState<RBACRole | null>(null);
  const [checkedIds, setCheckedIds] = useState<Set<string>>(new Set());

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
      toast.success("Role revoked");
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
      <h1 className="text-2xl font-semibold">User Access Control (Roles &amp; Permissions)</h1>
      {error && <p className="text-sm text-red-600">{error}</p>}

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
                <TableHead>Users</TableHead>
                <TableHead></TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {roles.map((role) => (
                <TableRow key={role.id} className="cursor-pointer" onClick={() => selectRole(role)}>
                  <TableCell>{role.name}</TableCell>
                  <TableCell className="text-xs">{role.permission_codes.length} permission(s)</TableCell>
                  <TableCell>{role.user_count}</TableCell>
                  <TableCell>
                    <Button
                      size="sm"
                      variant="destructive"
                      disabled={role.user_count > 0}
                      onClick={async (e) => {
                        e.stopPropagation();
                        try {
                          await api.delete(`/api/v1/rbac/roles/${role.id}`);
                          toast.success("Role deleted");
                          if (selectedRole?.id === role.id) setSelectedRole(null);
                          load();
                        } catch (err) {
                          toast.error(err instanceof ApiError ? err.message : "Could not delete role");
                        }
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
            <div className="grid grid-cols-2 gap-2 max-h-96 overflow-auto">
              {permissions.map((p) => (
                <label key={p.id} className="flex items-start gap-2 text-sm">
                  <input
                    type="checkbox"
                    className="mt-1"
                    checked={checkedIds.has(p.id)}
                    onChange={() => togglePermission(p.id)}
                  />
                  <span>
                    <span className="font-mono text-xs">{p.code}</span>
                    <br />
                    <span className="text-zinc-500 text-xs">{p.description}</span>
                  </span>
                </label>
              ))}
            </div>
            <Button onClick={savePermissions} className="w-fit">Save permissions</Button>
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
                <SelectTrigger><SelectValue placeholder="Select employee" /></SelectTrigger>
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
                <SelectTrigger><SelectValue placeholder="Select role" /></SelectTrigger>
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
                              className="ml-1 text-zinc-400 hover:text-red-600"
                              onClick={() => revokeRole(e.id, rid)}
                              aria-label={`Revoke ${roleName}`}
                            >
                              ×
                            </button>
                          )}
                        </Badge>
                      );
                    })}
                    {e.roles.length === 0 && <span className="text-zinc-400 text-xs">No roles</span>}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
    </div>
  );
}
