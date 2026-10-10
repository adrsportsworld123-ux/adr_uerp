"use client";

// Data access (Phase 15, FRD §18 "own branch vs all branches"): who sees
// which branches' data. A user sees every branch when any of their roles
// is set to "All branches" (on Roles & Permissions); otherwise their home
// branch (from their employee record) plus any extra branches given here.
// The database enforces it for every list, report, export and write.
import { useEffect, useState } from "react";
import Link from "next/link";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Branch } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Modal } from "@/components/ui/dialog";
import { PageHeader } from "@/components/data/page-header";
import { StatusChip } from "@/components/data/status-chip";

type Ref = { branch_id: string; name: string };
type UserAccess = {
  user_id: string;
  name: string;
  employee_code: string | null;
  email: string | null;
  roles: string[];
  home_branch: Ref | null;
  extra_branches: Ref[];
  all_branches: boolean;
};

export default function DataAccessPage() {
  const [users, setUsers] = useState<UserAccess[] | null>(null);
  const [branches, setBranches] = useState<Branch[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<UserAccess | null>(null);
  const [chosen, setChosen] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState(false);

  const load = () =>
    api
      .get<{ users: UserAccess[] }>("/api/v1/rbac/data-access")
      .then((d) => setUsers(d.users))
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load data access"));

  useEffect(() => {
    load();
    api.get<{ branches: Branch[] }>("/api/v1/branches?status=active").then((d) => setBranches(d.branches)).catch(() => {});
  }, []);

  function open(u: UserAccess) {
    setEditing(u);
    setChosen(new Set(u.extra_branches.map((b) => b.branch_id)));
  }

  async function save() {
    if (!editing) return;
    setBusy(true);
    try {
      await api.put(`/api/v1/rbac/users/${editing.user_id}/branches`, { branch_ids: [...chosen] });
      toast.success(`Saved ${editing.name}'s branches`);
      setEditing(null);
      await load();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not save");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Data Access"
        description={
          <>
            Which branches&apos; sales, stock, cash and books each person sees. Set a role to &ldquo;All branches&rdquo; or &ldquo;Own branch
            only&rdquo; on <Link href="/rbac" className="underline">Roles &amp; Permissions</Link>; give extra branches to someone who covers several.
          </>
        }
      />
      {error && <p className="text-sm text-destructive">{error}</p>}
      <div className="overflow-x-auto rounded-xl border bg-card">
        <table className="w-full text-sm">
          <thead className="border-b text-left text-xs text-muted-foreground">
            <tr>
              <th className="px-4 py-2 font-medium">Person</th>
              <th className="px-4 py-2 font-medium">Roles</th>
              <th className="px-4 py-2 font-medium">Sees</th>
              <th className="px-4 py-2 font-medium">Home branch</th>
              <th className="px-4 py-2 font-medium">Extra branches</th>
              <th className="px-4 py-2" />
            </tr>
          </thead>
          <tbody>
            {users?.map((u) => (
              <tr key={u.user_id} className="border-b last:border-0">
                <td className="px-4 py-2">
                  <div className="font-medium">{u.name}</div>
                  <div className="text-xs text-muted-foreground">{u.employee_code ?? u.email}</div>
                </td>
                <td className="px-4 py-2 text-xs">{u.roles.join(", ") || "—"}</td>
                <td className="px-4 py-2">
                  {u.all_branches ? <StatusChip status="all" tone="info" label="All branches" /> : <StatusChip status="branch" tone="muted" label="Own branch(es)" />}
                </td>
                <td className="px-4 py-2">{u.home_branch?.name ?? <span className="text-muted-foreground">None</span>}</td>
                <td className="px-4 py-2 text-xs">{u.extra_branches.map((b) => b.name).join(", ") || "—"}</td>
                <td className="px-4 py-2 text-right">
                  <Button variant="outline" size="sm" onClick={() => open(u)} aria-label={`Edit branches for ${u.name}`}>
                    Branches
                  </Button>
                </td>
              </tr>
            ))}
            {users?.length === 0 && (
              <tr>
                <td colSpan={6} className="px-4 py-8 text-center text-muted-foreground">
                  No active users.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
      {users?.some((u) => !u.all_branches && !u.home_branch && u.extra_branches.length === 0) && (
        <p className="text-sm text-warn" role="status">
          Someone limited to their own branch has no branch at all, so they see no branch data. Give them a home branch or an extra branch.
        </p>
      )}

      {editing && (
        <Modal
          open
          onOpenChange={(o) => !o && setEditing(null)}
          title={`Extra branches for ${editing.name}`}
          description={
            editing.all_branches
              ? "One of their roles already sees all branches, so this changes nothing until that role is limited."
              : `They always see their home branch${editing.home_branch ? ` (${editing.home_branch.name})` : ""}. Tick any others they cover.`
          }
          footer={
            <>
              <Button variant="outline" onClick={() => setEditing(null)}>
                Cancel
              </Button>
              <Button disabled={busy} onClick={save}>
                Save
              </Button>
            </>
          }
        >
          <fieldset className="flex flex-col gap-2 text-sm">
            <legend className="sr-only">Branches</legend>
            {branches
              .filter((b) => b.branch_id !== editing.home_branch?.branch_id)
              .map((b) => (
                <label key={b.branch_id} className="flex items-center gap-2">
                  <input
                    type="checkbox"
                    checked={chosen.has(b.branch_id)}
                    onChange={(e) =>
                      setChosen((c) => {
                        const n = new Set(c);
                        if (e.target.checked) n.add(b.branch_id);
                        else n.delete(b.branch_id);
                        return n;
                      })
                    }
                  />
                  {b.name}
                </label>
              ))}
          </fieldset>
        </Modal>
      )}
    </div>
  );
}
