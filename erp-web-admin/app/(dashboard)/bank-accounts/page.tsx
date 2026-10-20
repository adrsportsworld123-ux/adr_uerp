"use client";

// Bank Accounts (erp-core-go migrations/066, internal/banking): the
// business's accounts at merchant level and each branch's own, with their
// ledger account, balance and cheque books. The first account posts to the
// existing Bank ledger (1002), so balances and reconciliation stay where
// they were; others get their own sub-account (1002-02 …). bank.manage
// adds and edits; anyone can see the list (payment forms choose from it).
import { FormEvent, useCallback, useEffect, useState } from "react";
import { BookOpen, Loader2, Pencil, Plus } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import type { BankAccount, ChequeBook } from "@/lib/types";
import { rupees } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Modal } from "@/components/ui/dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { PageHeader } from "@/components/data/page-header";
import { Panel, selectClass } from "@/components/data/panel";
import { StatusChip } from "@/components/data/status-chip";
import { BranchSelect, useMyBranches } from "@/components/data/branch-select";

const TYPE_LABEL: Record<BankAccount["account_type"], string> = { current: "Current", savings: "Savings", overdraft: "Overdraft", cash_credit: "Cash credit" };
const message = (e: unknown, fallback: string) => (e instanceof ApiError ? e.message : fallback);

export default function BankAccountsPage() {
  const { can } = usePermissions();
  const allowed = can("bank.manage");
  const [accounts, setAccounts] = useState<BankAccount[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<BankAccount | "new" | null>(null);
  const [books, setBooks] = useState<BankAccount | null>(null);
  const [status, setStatus] = useState("active");

  const load = useCallback(() => {
    api
      .get<{ bank_accounts: BankAccount[] }>(`/api/v1/bank-accounts${status ? `?status=${status}` : ""}`)
      .then((d) => {
        setAccounts(d.bank_accounts);
        setError(null);
      })
      .catch((e) => setError(message(e, "Could not load the bank accounts")))
      .finally(() => setLoading(false));
  }, [status]);
  useEffect(load, [load]);

  return (
    <div className="space-y-5">
      <PageHeader
        title="Bank Accounts"
        description="The business's bank accounts, merchant-wide or a branch's own. Payments by transfer or cheque name the account they went through."
        actions={
          <Button onClick={() => setEditing("new")} disabled={!allowed} title={allowed ? undefined : missingPermissionHint("bank.manage")}>
            <Plus /> New bank account
          </Button>
        }
      />
      <Panel
        actions={
          <select aria-label="Bank account status" value={status} onChange={(e) => setStatus(e.target.value)} className={selectClass}>
            <option value="active">Active</option>
            <option value="inactive">Inactive</option>
            <option value="">All</option>
          </select>
        }
        title={`Accounts (${accounts.length})`}
      >
        {error && <p className="text-sm text-destructive">{error}</p>}
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Account</TableHead>
              <TableHead>Scope</TableHead>
              <TableHead>IFSC</TableHead>
              <TableHead>Ledger</TableHead>
              <TableHead className="text-right">Balance</TableHead>
              <TableHead>Cheques</TableHead>
              <TableHead>Status</TableHead>
              <TableHead className="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {accounts.map((a) => (
              <TableRow key={a.bank_account_id} data-testid="bank-account-row">
                <TableCell className="font-medium">
                  {a.display_name}
                  {a.is_default && <span className="ms-2 rounded bg-primary-soft px-1.5 py-0.5 text-xs text-primary">Default</span>}
                  <span className="block text-xs font-normal text-muted-foreground">
                    {a.account_name} · {TYPE_LABEL[a.account_type]} · {a.account_number}
                    {a.bank_branch && ` · ${a.bank_branch}`}
                  </span>
                </TableCell>
                <TableCell className="text-sm">{a.branch_name ?? "Merchant-wide"}</TableCell>
                <TableCell className="font-mono text-xs">{a.ifsc || "—"}</TableCell>
                <TableCell className="text-xs">
                  {a.ledger_code}
                  <span className="block text-muted-foreground">{a.ledger_name}</span>
                </TableCell>
                <TableCell className="tabular text-right">{rupees(a.balance)}</TableCell>
                <TableCell className="text-xs">
                  {a.cheque_books > 0 ? `${a.leaves_left} leaves in ${a.cheque_books} book${a.cheque_books === 1 ? "" : "s"}` : "No cheque book"}
                </TableCell>
                <TableCell>
                  <StatusChip status={a.status} />
                </TableCell>
                <TableCell>
                  <div className="flex justify-end gap-1.5">
                    <Button size="sm" variant="outline" onClick={() => setBooks(a)} aria-label={`Cheque books of ${a.display_name}`}>
                      <BookOpen /> Cheque books
                    </Button>
                    <Button size="sm" variant="outline" disabled={!allowed} onClick={() => setEditing(a)} aria-label={`Edit ${a.display_name}`}>
                      <Pencil /> Edit
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ))}
            {!loading && accounts.length === 0 && (
              <TableRow>
                <TableCell colSpan={8} className="py-6 text-center text-sm text-muted-foreground">
                  No bank accounts yet. Payments by transfer or cheque post to the main Bank ledger until you add one.
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </Panel>
      {editing && <AccountDialog account={editing === "new" ? null : editing} first={accounts.length === 0} onClose={() => setEditing(null)} onSaved={load} />}
      {books && <BooksDialog account={books} allowed={allowed} onClose={() => setBooks(null)} onChanged={load} />}
    </div>
  );
}

function AccountDialog({ account, first, onClose, onSaved }: { account: BankAccount | null; first: boolean; onClose: () => void; onSaved: () => void }) {
  const { branches } = useMyBranches();
  const a = account;
  const [f, setF] = useState({
    account_name: a?.account_name ?? "",
    bank_name: a?.bank_name ?? "",
    bank_branch: a?.bank_branch ?? "",
    account_number: a?.account_number ?? "",
    ifsc: a?.ifsc ?? "",
    account_type: a?.account_type ?? "current",
    notes: a?.notes ?? "",
    status: a?.status ?? "active",
  });
  const [scope, setScope] = useState<"merchant" | "branch">(a?.branch_id ? "branch" : "merchant");
  const [branchId, setBranchId] = useState(a?.branch_id ?? "");
  const [isDefault, setIsDefault] = useState(a?.is_default ?? first);
  const [ledger, setLedger] = useState<"main" | "own">(first ? "main" : "own");
  const [offsetX, setOffsetX] = useState(String(a?.cheque_layout.offset_x_mm ?? 0));
  const [offsetY, setOffsetY] = useState(String(a?.cheque_layout.offset_y_mm ?? 0));
  const [signatory, setSignatory] = useState(a?.cheque_layout.signatory_line ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const set = (k: keyof typeof f, v: string) => setF({ ...f, [k]: v });

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    const body = {
      ...f,
      is_default: isDefault,
      cheque_layout: { offset_x_mm: Number(offsetX) || 0, offset_y_mm: Number(offsetY) || 0, signatory_line: signatory },
      ...(a ? {} : { branch_id: scope === "branch" ? branchId : null, ledger }),
    };
    try {
      if (a) await api.patch(`/api/v1/bank-accounts/${a.bank_account_id}`, body);
      else await api.post("/api/v1/bank-accounts", body);
      toast.success(a ? "Bank account saved" : "Bank account added");
      onSaved();
      onClose();
    } catch (err) {
      setError(message(err, "Could not save the bank account"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={a ? `Edit ${a.display_name}` : "New bank account"}
      width={620}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="bank-account-form" disabled={busy || (scope === "branch" && !branchId && !a)}>
            {busy && <Loader2 className="animate-spin" aria-hidden />}
            Save
          </Button>
        </>
      }
    >
      <form id="bank-account-form" onSubmit={save} className="grid gap-4 sm:grid-cols-2">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="ba-bank">Bank</Label>
          <Input id="ba-bank" required value={f.bank_name} onChange={(e) => set("bank_name", e.target.value)} placeholder="HDFC Bank" />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="ba-bank-branch">Bank branch</Label>
          <Input id="ba-bank-branch" value={f.bank_branch} onChange={(e) => set("bank_branch", e.target.value)} />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="ba-name">Name on the account</Label>
          <Input id="ba-name" required value={f.account_name} onChange={(e) => set("account_name", e.target.value)} />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="ba-type">Account type</Label>
          <select id="ba-type" value={f.account_type} onChange={(e) => set("account_type", e.target.value)} className={selectClass}>
            {Object.entries(TYPE_LABEL).map(([k, v]) => (
              <option key={k} value={k}>
                {v}
              </option>
            ))}
          </select>
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="ba-number">Account number</Label>
          <Input id="ba-number" required value={f.account_number} onChange={(e) => set("account_number", e.target.value)} />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="ba-ifsc">IFSC</Label>
          <Input id="ba-ifsc" className="uppercase" maxLength={11} value={f.ifsc} onChange={(e) => set("ifsc", e.target.value)} placeholder="HDFC0001234" />
        </div>
        {!a && (
          <>
            <fieldset className="flex flex-col gap-1.5 sm:col-span-2">
              <legend className="text-sm font-medium">Belongs to</legend>
              <div className="flex flex-wrap items-center gap-4 text-sm">
                <label className="flex items-center gap-2">
                  <input type="radio" name="ba-scope" checked={scope === "merchant"} onChange={() => setScope("merchant")} /> The whole business
                </label>
                <label className="flex items-center gap-2">
                  <input type="radio" name="ba-scope" checked={scope === "branch"} onChange={() => setScope("branch")} /> One branch
                </label>
                {scope === "branch" && <BranchSelect id="ba-branch" branches={branches} value={branchId} onChange={setBranchId} />}
              </div>
            </fieldset>
            <fieldset className="flex flex-col gap-1.5 sm:col-span-2">
              <legend className="text-sm font-medium">Ledger</legend>
              <label className="flex items-start gap-2 text-sm">
                <input type="radio" name="ba-ledger" className="mt-1" checked={ledger === "main"} onChange={() => setLedger("main")} />
                <span>
                  The main Bank account (1002)
                  <span className="block text-xs text-muted-foreground">Keeps what is already posted to Bank in this account.</span>
                </span>
              </label>
              <label className="flex items-start gap-2 text-sm">
                <input type="radio" name="ba-ledger" className="mt-1" checked={ledger === "own"} onChange={() => setLedger("own")} />
                <span>
                  Its own ledger account (1002-NN, under Bank)
                  <span className="block text-xs text-muted-foreground">A separate balance, reconciled on its own.</span>
                </span>
              </label>
            </fieldset>
          </>
        )}
        <label className="flex items-center gap-2 text-sm sm:col-span-2">
          <input type="checkbox" checked={isDefault} onChange={(e) => setIsDefault(e.target.checked)} /> The default for {scope === "branch" ? "this branch" : "the business"}
        </label>
        {a && (
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ba-status">Status</Label>
            <select id="ba-status" value={f.status} onChange={(e) => set("status", e.target.value)} className={selectClass}>
              <option value="active">Active</option>
              <option value="inactive">Inactive</option>
            </select>
          </div>
        )}
        <fieldset className="grid gap-3 rounded-lg border p-3 sm:col-span-2 sm:grid-cols-3">
          <legend className="px-1 text-sm font-medium">Cheque printing</legend>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ba-ox">Move right (mm)</Label>
            <Input id="ba-ox" type="number" step="0.5" min={-20} max={20} value={offsetX} onChange={(e) => setOffsetX(e.target.value)} />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ba-oy">Move down (mm)</Label>
            <Input id="ba-oy" type="number" step="0.5" min={-20} max={20} value={offsetY} onChange={(e) => setOffsetY(e.target.value)} />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ba-sign">Above the signature</Label>
            <Input id="ba-sign" maxLength={60} value={signatory} onChange={(e) => setSignatory(e.target.value)} placeholder="For Acme Sports" />
          </div>
          <p className="text-xs text-muted-foreground sm:col-span-3">
            Cheques print on the standard CTS-2010 leaf (202 × 92 mm). If your printer or this bank&apos;s leaf sits differently, move the print here.
          </p>
        </fieldset>
        <div className="flex flex-col gap-1.5 sm:col-span-2">
          <Label htmlFor="ba-notes">Notes</Label>
          <Input id="ba-notes" value={f.notes} onChange={(e) => set("notes", e.target.value)} />
        </div>
        {error && (
          <p role="alert" className="text-sm text-destructive sm:col-span-2">
            {error}
          </p>
        )}
      </form>
    </Modal>
  );
}

function BooksDialog({ account, allowed, onClose, onChanged }: { account: BankAccount; allowed: boolean; onClose: () => void; onChanged: () => void }) {
  const [books, setBooks] = useState<ChequeBook[]>([]);
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [digits, setDigits] = useState("6");
  const [receivedOn, setReceivedOn] = useState("");
  const [busy, setBusy] = useState(false);

  const load = useCallback(() => {
    api
      .get<{ cheque_books: ChequeBook[] }>(`/api/v1/bank-accounts/${account.bank_account_id}/cheque-books`)
      .then((d) => setBooks(d.cheque_books))
      .catch((e) => toast.error(message(e, "Could not load the cheque books")));
  }, [account.bank_account_id]);
  useEffect(load, [load]);

  async function add(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      await api.post(`/api/v1/bank-accounts/${account.bank_account_id}/cheque-books`, {
        series_from: Number(from),
        series_to: Number(to),
        leaf_digits: Number(digits) || 6,
        received_on: receivedOn,
      });
      toast.success("Cheque book added");
      setFrom("");
      setTo("");
      load();
      onChanged();
    } catch (err) {
      toast.error(message(err, "Could not add the cheque book"));
    } finally {
      setBusy(false);
    }
  }

  async function close(b: ChequeBook) {
    try {
      await api.post(`/api/v1/cheque-books/${b.cheque_book_id}/close`, {});
      toast.success("Cheque book closed");
      load();
      onChanged();
    } catch (err) {
      toast.error(message(err, "Could not close the cheque book"));
    }
  }

  return (
    <Modal open onOpenChange={(o) => !o && onClose()} title={`Cheque books · ${account.display_name}`} width={640} footer={<Button onClick={onClose}>Done</Button>}>
      <div className="space-y-4">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Series</TableHead>
              <TableHead>Used</TableHead>
              <TableHead>Next leaf</TableHead>
              <TableHead>Status</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {books.map((b) => (
              <TableRow key={b.cheque_book_id}>
                <TableCell className="font-mono text-sm">
                  {String(b.series_from).padStart(b.leaf_digits, "0")} – {String(b.series_to).padStart(b.leaf_digits, "0")}
                  {b.received_on && <span className="block font-sans text-xs text-muted-foreground">received {b.received_on}</span>}
                </TableCell>
                <TableCell className="tabular">
                  {b.used} of {b.series_to - b.series_from + 1}
                </TableCell>
                <TableCell className="font-mono">{b.next_leaf || "used up"}</TableCell>
                <TableCell>
                  <StatusChip status={b.status === "active" ? "active" : "inactive"} label={b.status === "active" ? "Active" : "Closed"} />
                </TableCell>
                <TableCell className="text-right">
                  {b.status === "active" && allowed && (
                    <Button size="sm" variant="ghost" onClick={() => close(b)}>
                      Close
                    </Button>
                  )}
                </TableCell>
              </TableRow>
            ))}
            {books.length === 0 && (
              <TableRow>
                <TableCell colSpan={5} className="py-4 text-center text-sm text-muted-foreground">
                  No cheque books. A cheque can still be written with its number typed in.
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
        {allowed && (
          <form onSubmit={add} className="grid gap-3 rounded-lg border p-3 sm:grid-cols-5 sm:items-end">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="cb-from">First leaf</Label>
              <Input id="cb-from" inputMode="numeric" required value={from} onChange={(e) => setFrom(e.target.value.replace(/\D/g, ""))} />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="cb-to">Last leaf</Label>
              <Input id="cb-to" inputMode="numeric" required value={to} onChange={(e) => setTo(e.target.value.replace(/\D/g, ""))} />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="cb-digits">Digits</Label>
              <Input id="cb-digits" type="number" min={4} max={10} value={digits} onChange={(e) => setDigits(e.target.value)} />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="cb-received">Received on</Label>
              <Input id="cb-received" type="date" value={receivedOn} onChange={(e) => setReceivedOn(e.target.value)} />
            </div>
            <Button type="submit" disabled={busy || !from || !to}>
              <Plus /> Add book
            </Button>
          </form>
        )}
      </div>
    </Modal>
  );
}
