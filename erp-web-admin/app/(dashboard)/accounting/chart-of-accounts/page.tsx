"use client";

// Chart of accounts with the uniform master lifecycle (erp-core-go
// migrations/054): add, edit, activate/deactivate and delete. System
// accounts — the ones sales, purchases and payments post to on their own —
// can be renamed but never deactivated, re-coded or deleted. An inactive
// account can't be used in a new journal voucher; an account with postings
// can't be deleted (deactivate it instead) and keeps its code and type.
import { useCallback, useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Account } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { PageHeader } from "@/components/data/page-header";
import { StatusChip } from "@/components/data/status-chip";
import { EditDialog, EditField } from "@/components/data/edit-dialog";
import { MasterRowActions } from "@/components/data/master-actions";

const TYPES = ["asset", "liability", "equity", "income", "expense"];

const EDIT_FIELDS: EditField[] = [
  { key: "code", label: "Code", required: true, half: true, hint: "Can't change once entries are posted to it" },
  { key: "name", label: "Name", required: true },
];
const SYSTEM_EDIT_FIELDS: EditField[] = [{ key: "name", label: "Name", required: true, hint: "A system account keeps its code and type" }];

export default function ChartOfAccountsPage() {
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [q, setQ] = useState("");
  const [status, setStatus] = useState("all");
  const [editing, setEditing] = useState<Account | null>(null);

  const [code, setCode] = useState("");
  const [name, setName] = useState("");
  const [accountType, setAccountType] = useState("expense");
  const [submitting, setSubmitting] = useState(false);

  const load = useCallback(() => {
    api
      .get<{ accounts: Account[] }>("/api/v1/accounting/accounts")
      .then((d) => {
        setAccounts(d.accounts);
        setError(null);
      })
      .catch((e) => setError(e.message));
  }, []);
  useEffect(load, [load]);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    try {
      await api.post("/api/v1/accounting/accounts", { code, name, account_type: accountType });
      toast.success("Account created");
      setCode("");
      setName("");
      load();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not create account");
    } finally {
      setSubmitting(false);
    }
  }

  const shown = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return accounts.filter(
      (a) => (status === "all" || a.status === status) && (!needle || a.code.toLowerCase().includes(needle) || a.name.toLowerCase().includes(needle))
    );
  }, [accounts, q, status]);
  const grouped = TYPES.map((t) => ({ type: t, items: shown.filter((a) => a.account_type === t) }));

  return (
    <div className="flex max-w-4xl flex-col gap-6">
      <PageHeader
        title="Chart of Accounts"
        description="Inactive accounts can't be used in new journal vouchers; their history is untouched. System accounts are posted to automatically and can only be renamed."
      />
      <div className="flex flex-wrap gap-2">
        <Input aria-label="Search accounts" placeholder="Search code or name" value={q} onChange={(e) => setQ(e.target.value)} className="w-64" />
        <select aria-label="Status" value={status} onChange={(e) => setStatus(e.target.value)} className="h-9 rounded-lg border bg-card px-2 text-sm">
          <option value="all">All statuses</option>
          <option value="active">Active</option>
          <option value="inactive">Inactive</option>
        </select>
      </div>
      {error && <p className="text-sm text-destructive">{error}</p>}

      {grouped.map(
        (g) =>
          g.items.length > 0 && (
            <div key={g.type}>
              <h2 className="mb-2 text-sm font-semibold uppercase text-muted-foreground">{g.type}</h2>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-24">Code</TableHead>
                    <TableHead>Name</TableHead>
                    <TableHead className="w-28">Status</TableHead>
                    <TableHead className="w-20"></TableHead>
                    <TableHead className="w-32 text-right">Actions</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {g.items.map((a) => (
                    <TableRow key={a.account_id}>
                      <TableCell className="font-mono">{a.code}</TableCell>
                      <TableCell>{a.name}</TableCell>
                      <TableCell>
                        <StatusChip status={a.status} />
                      </TableCell>
                      <TableCell>{a.is_system && <span className="text-xs text-muted-foreground">system</span>}</TableCell>
                      <TableCell className="text-right">
                        <div className="flex justify-end">
                          <MasterRowActions
                            name={`${a.code} ${a.name}`}
                            noun="account"
                            status={a.status}
                            onEdit={() => setEditing(a)}
                            setStatus={(next) => api.patch(`/api/v1/accounting/accounts/${a.account_id}`, { status: next })}
                            deactivateNote="It stays on every entry already posted, but can't be used in a new journal voucher until reactivated."
                            remove={{ entity: "account", id: a.account_id, path: `/api/v1/accounting/accounts/${a.account_id}`, permission: "accounting.manage" }}
                            locked={a.is_system ? "A system account can't be deactivated or deleted" : undefined}
                            onChanged={load}
                          />
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )
      )}
      {shown.length === 0 && !error && <p className="text-sm text-muted-foreground">No accounts match these filters.</p>}

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Add a custom account</CardTitle>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleSubmit} className="flex flex-col gap-4">
            <div className="grid grid-cols-3 gap-4">
              <div className="flex flex-col gap-2">
                <Label htmlFor="code">Code *</Label>
                <Input id="code" required value={code} onChange={(e) => setCode(e.target.value)} placeholder="5010" />
              </div>
              <div className="col-span-2 flex flex-col gap-2">
                <Label htmlFor="name">Name *</Label>
                <Input id="name" required value={name} onChange={(e) => setName(e.target.value)} placeholder="Rent Expense" />
              </div>
            </div>
            <div className="flex flex-col gap-2">
              <Label>Type *</Label>
              <Select value={accountType} onValueChange={(v) => setAccountType(v ?? "expense")}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {TYPES.map((t) => (
                    <SelectItem key={t} value={t}>
                      {t}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <Button type="submit" disabled={submitting}>
              {submitting ? "Creating..." : "Add account"}
            </Button>
          </form>
        </CardContent>
      </Card>

      {editing && (
        <EditDialog
          open
          onOpenChange={(o) => !o && setEditing(null)}
          title="Edit account"
          description={`${editing.code} · ${editing.account_type}`}
          fields={editing.is_system ? SYSTEM_EDIT_FIELDS : EDIT_FIELDS}
          initial={editing.is_system ? { name: editing.name } : { code: editing.code, name: editing.name }}
          onSave={async (changes) => {
            await api.patch(`/api/v1/accounting/accounts/${editing.account_id}`, changes);
            toast.success(`Saved account “${changes.name ?? editing.name}”`);
            load();
          }}
        />
      )}
    </div>
  );
}
