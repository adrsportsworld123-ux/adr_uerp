"use client";

// Expenses (erp-core-go migrations/074, internal/expenses), three tabs:
//   Expenses — record one (date, head, amount, paid by cash / bank / a
//   partner / unpaid), the list with totals by head, pay an unpaid one,
//   cancel one (a reversing entry; nothing is deleted).
//   Import — book a whole list never recorded (shop advance, rent, interior,
//   flex and banner, transport …) from a CSV: preview each row, then all or
//   nothing. The same file can't go in twice.
//   Heads — what an expense is for and the account it posts to: a deposit
//   or interior work to an asset (balance sheet), the rest to expenses (P&L).
// expenses.manage.
import { FormEvent, useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Download, FileUp, Loader2, Plus } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import { Expense, ExpenseHead } from "@/lib/types";
import { localDate, rupees } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Modal } from "@/components/ui/dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { PageHeader } from "@/components/data/page-header";
import { Field, FilterBar, Panel, Stat, selectClass } from "@/components/data/panel";
import { StatusChip, Tone } from "@/components/data/status-chip";
import { saveBlob } from "@/components/data/document-actions";
import { BranchSelect, useMyBranches } from "@/components/data/branch-select";
import { BankAccountSelect, defaultAccount, useBankAccounts } from "@/components/banking/bank-account-select";
import { PartnerSelect, usePartners } from "@/components/partners/use-partners";

const message = (e: unknown, fallback: string) => (e instanceof ApiError ? e.message : fallback);
const PAID_BY: Record<string, { label: string; tone: Tone }> = {
  cash: { label: "Cash", tone: "ok" },
  bank: { label: "Bank", tone: "info" },
  partner: { label: "Partner", tone: "plum" },
  unpaid: { label: "Unpaid", tone: "warn" },
};
const TABS = [
  ["list", "Expenses"],
  ["import", "Import a list"],
  ["heads", "Expense heads"],
] as const;

export default function ExpensesPage() {
  const params = useSearchParams();
  const router = useRouter();
  const tab = (TABS.find(([k]) => k === params.get("tab"))?.[0] ?? "list") as (typeof TABS)[number][0];
  const { can } = usePermissions();
  const allowed = can("expenses.manage");
  const [heads, setHeads] = useState<ExpenseHead[]>([]);
  const loadHeads = useCallback(() => {
    api
      .get<{ heads: ExpenseHead[] }>("/api/v1/expense-heads")
      .then((d) => setHeads(d.heads))
      .catch(() => {});
  }, []);
  useEffect(loadHeads, [loadHeads]);

  return (
    <div className="space-y-5">
      <PageHeader
        title="Expenses"
        description="Rent, advertising, transport, a shop advance, interior work… each posted to its account: deposits and fixtures to the balance sheet, the rest to profit and loss. Paid by cash, bank, a partner, or unpaid until paid."
      />
      <div role="tablist" aria-label="Expenses" className="flex w-fit gap-1 rounded-lg bg-muted p-1">
        {TABS.map(([k, label]) => (
          <button
            key={k}
            role="tab"
            aria-selected={tab === k}
            onClick={() => router.replace(`/expenses?tab=${k}`)}
            className={`rounded-md px-3 py-1.5 text-sm ${tab === k ? "bg-card font-medium shadow-sm" : "text-muted-foreground hover:text-foreground"}`}
          >
            {label}
          </button>
        ))}
      </div>
      {!allowed ? (
        <p className="text-sm text-muted-foreground">{missingPermissionHint("expenses.manage")}</p>
      ) : tab === "list" ? (
        <ExpenseList heads={heads} />
      ) : tab === "import" ? (
        <ImportExpenses />
      ) : (
        <Heads heads={heads} reload={loadHeads} />
      )}
    </div>
  );
}

// ------------------------------------------------------------------ list

function ExpenseList({ heads }: { heads: ExpenseHead[] }) {
  const [from, setFrom] = useState(() => localDate(-89));
  const [to, setTo] = useState(() => localDate());
  const [headId, setHeadId] = useState("");
  const [paidBy, setPaidBy] = useState("");
  const [q, setQ] = useState("");
  const [data, setData] = useState<{ expenses: Expense[]; by_head: { head: string; count: number; amount: string }[]; total: string } | null>(null);
  const [adding, setAdding] = useState(false);
  const [paying, setPaying] = useState<Expense | null>(null);
  const [cancelling, setCancelling] = useState<Expense | null>(null);

  const load = useCallback(() => {
    const p = new URLSearchParams({ from, to });
    if (headId) p.set("head_id", headId);
    if (paidBy) p.set("paid_by", paidBy);
    if (q.trim()) p.set("q", q.trim());
    api
      .get<typeof data>(`/api/v1/expenses?${p.toString()}`)
      .then(setData)
      .catch((e) => toast.error(message(e, "Could not load expenses")));
  }, [from, to, headId, paidBy, q]);
  useEffect(load, [load]);

  return (
    <div className="space-y-4">
      <FilterBar>
        <Field label="From" htmlFor="ex-from">
          <Input id="ex-from" type="date" value={from} max={to} onChange={(e) => setFrom(e.target.value)} />
        </Field>
        <Field label="To" htmlFor="ex-to">
          <Input id="ex-to" type="date" value={to} min={from} onChange={(e) => setTo(e.target.value)} />
        </Field>
        <Field label="Head" htmlFor="ex-head">
          <select id="ex-head" value={headId} onChange={(e) => setHeadId(e.target.value)} className={`${selectClass} w-44`}>
            <option value="">Every head</option>
            {heads.map((h) => (
              <option key={h.head_id} value={h.head_id}>
                {h.name}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Paid by" htmlFor="ex-paid">
          <select id="ex-paid" value={paidBy} onChange={(e) => setPaidBy(e.target.value)} className={`${selectClass} w-32`}>
            <option value="">Anyone</option>
            {Object.entries(PAID_BY).map(([k, v]) => (
              <option key={k} value={k}>
                {v.label}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Search" htmlFor="ex-q">
          <Input id="ex-q" value={q} onChange={(e) => setQ(e.target.value)} placeholder="Number, vendor, details" />
        </Field>
        <Button className="ms-auto" onClick={() => setAdding(true)}>
          <Plus aria-hidden /> New expense
        </Button>
      </FilterBar>
      {data && (
        <Panel title={`Total ${rupees(data.total)}`} description={data.by_head.map((b) => `${b.head} ${rupees(b.amount)}`).join(" · ") || "Nothing in these dates."}>
          <Table data-testid="expenses-table">
            <TableHeader>
              <TableRow>
                <TableHead>Date</TableHead>
                <TableHead>Head</TableHead>
                <TableHead>Details</TableHead>
                <TableHead>Paid by</TableHead>
                <TableHead className="text-right">Amount</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.expenses.map((e) => (
                <TableRow key={e.expense_id} className={e.status === "cancelled" ? "opacity-60" : undefined}>
                  <TableCell className="whitespace-nowrap text-sm">
                    {e.expense_date}
                    <span className="block text-xs text-muted-foreground">{e.expense_number}</span>
                  </TableCell>
                  <TableCell className="text-sm">
                    {e.head}
                    <span className="block text-xs text-muted-foreground">
                      {e.account_code} {e.account_name}
                    </span>
                  </TableCell>
                  <TableCell className="max-w-72 whitespace-normal text-sm">
                    {[e.description, e.vendor, e.reference].filter(Boolean).join(" · ") || "—"}
                    {e.status === "cancelled" && <span className="block text-xs text-destructive">Cancelled: {e.cancel_reason}</span>}
                  </TableCell>
                  <TableCell className="text-sm">
                    <StatusChip status="" tone={PAID_BY[e.paid_by].tone} label={PAID_BY[e.paid_by].label} />
                    <span className="block text-xs text-muted-foreground">
                      {e.paid_by === "bank" ? e.bank_account ?? "Bank" : e.paid_by === "partner" ? e.partner : e.paid_by === "unpaid" && e.paid_at ? `paid ${e.paid_at} (${e.paid_with})` : ""}
                    </span>
                  </TableCell>
                  <TableCell className="tabular text-right">{rupees(e.amount)}</TableCell>
                  <TableCell>
                    <div className="flex justify-end gap-1.5">
                      {e.status === "posted" && e.paid_by === "unpaid" && !e.paid_at && (
                        <Button size="sm" variant="outline" onClick={() => setPaying(e)}>
                          Pay
                        </Button>
                      )}
                      {e.status === "posted" && (
                        <Button size="sm" variant="ghost" className="text-destructive hover:text-destructive" onClick={() => setCancelling(e)}>
                          Cancel
                        </Button>
                      )}
                    </div>
                  </TableCell>
                </TableRow>
              ))}
              {data.expenses.length === 0 && (
                <TableRow>
                  <TableCell colSpan={6} className="py-6 text-center text-sm text-muted-foreground">
                    No expenses in these dates.{" "}
                    <Link href="/expenses?tab=import" className="text-primary hover:underline">
                      Import a list
                    </Link>
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </Panel>
      )}
      {adding && <ExpenseDialog heads={heads} onClose={() => setAdding(false)} onSaved={load} />}
      {paying && <PayDialog expense={paying} onClose={() => setPaying(null)} onSaved={load} />}
      {cancelling && <CancelDialog expense={cancelling} onClose={() => setCancelling(null)} onSaved={load} />}
    </div>
  );
}

/** Cash, bank (an account), a partner, or (for a new expense) unpaid. */
function PayerFields({
  paidBy,
  setPaidBy,
  bankId,
  setBankId,
  partnerId,
  setPartnerId,
  allowUnpaid,
}: {
  paidBy: string;
  setPaidBy: (v: string) => void;
  bankId: string;
  setBankId: (v: string) => void;
  partnerId: string;
  setPartnerId: (v: string) => void;
  allowUnpaid: boolean;
}) {
  const accounts = useBankAccounts();
  const partners = usePartners();
  const bank = bankId || defaultAccount(accounts)?.bank_account_id || "";
  return (
    <>
      <Field label="Paid by *" htmlFor="ex-paidby">
        <select id="ex-paidby" value={paidBy} onChange={(e) => setPaidBy(e.target.value)} className={`${selectClass} w-full`}>
          <option value="cash">Cash</option>
          <option value="bank">Bank</option>
          {partners.length > 0 && <option value="partner">A partner (their own money)</option>}
          {allowUnpaid && <option value="unpaid">Unpaid — pay later</option>}
        </select>
      </Field>
      {paidBy === "bank" && accounts.length > 0 ? (
        <Field label="Bank account" htmlFor="ex-bank">
          <BankAccountSelect id="ex-bank" accounts={accounts} value={bank} onChange={(v) => setBankId(v)} allowNone className="w-full" />
        </Field>
      ) : paidBy === "partner" ? (
        <Field label="Partner *" htmlFor="ex-partner">
          <PartnerSelect id="ex-partner" partners={partners} value={partnerId} onChange={setPartnerId} />
        </Field>
      ) : (
        <span />
      )}
    </>
  );
}

function ExpenseDialog({ heads, onClose, onSaved }: { heads: ExpenseHead[]; onClose: () => void; onSaved: () => void }) {
  const { branches, defaultBranchId } = useMyBranches();
  const accounts = useBankAccounts();
  const [branchId, setBranchId] = useState("");
  const [date, setDate] = useState(() => localDate());
  const [headId, setHeadId] = useState("");
  const [amount, setAmount] = useState("");
  const [paidBy, setPaidBy] = useState("cash");
  const [bankId, setBankId] = useState("");
  const [partnerId, setPartnerId] = useState("");
  const [description, setDescription] = useState("");
  const [vendor, setVendor] = useState("");
  const [reference, setReference] = useState("");
  const [busy, setBusy] = useState(false);
  const head = heads.find((h) => h.head_id === headId);

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      await api.post("/api/v1/expenses", {
        branch_id: branchId || defaultBranchId,
        expense_date: date,
        head_id: headId,
        amount: Number(amount),
        paid_by: paidBy,
        ...(paidBy === "bank" ? { bank_account_id: bankId || defaultAccount(accounts)?.bank_account_id || "" } : {}),
        ...(paidBy === "partner" ? { partner_id: partnerId } : {}),
        description,
        vendor,
        reference,
      });
      toast.success("Expense recorded");
      onSaved();
      onClose();
    } catch (err) {
      toast.error(message(err, "Could not record the expense"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title="New expense"
      width={640}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="expense-form" disabled={busy || !headId || !(Number(amount) > 0) || (paidBy === "partner" && !partnerId)}>
            {busy && <Loader2 className="animate-spin" aria-hidden />} Record expense
          </Button>
        </>
      }
    >
      <form id="expense-form" onSubmit={save} className="grid gap-3 sm:grid-cols-2">
        <Field label="Date *" htmlFor="ex-date">
          <Input id="ex-date" type="date" max={localDate()} value={date} onChange={(e) => setDate(e.target.value)} />
        </Field>
        <Field label="Head *" htmlFor="ex-headsel">
          <select id="ex-headsel" value={headId} onChange={(e) => setHeadId(e.target.value)} className={`${selectClass} w-full`}>
            <option value="">Choose a head</option>
            {heads
              .filter((h) => h.status === "active")
              .map((h) => (
                <option key={h.head_id} value={h.head_id}>
                  {h.name}
                </option>
              ))}
          </select>
          {head && (
            <span className="text-xs text-muted-foreground">
              Posts to {head.account_code} {head.account_name} ({head.account_type === "asset" ? "an asset, not profit and loss" : "an expense"})
            </span>
          )}
        </Field>
        <Field label="Amount *" htmlFor="ex-amount">
          <Input id="ex-amount" type="number" min="0.01" step="0.01" value={amount} onChange={(e) => setAmount(e.target.value)} />
        </Field>
        <Field label="Branch (whose books)" htmlFor="ex-branch">
          <BranchSelect id="ex-branch" branches={branches} value={branchId || defaultBranchId} onChange={setBranchId} className="w-full" />
        </Field>
        <PayerFields paidBy={paidBy} setPaidBy={setPaidBy} bankId={bankId} setBankId={setBankId} partnerId={partnerId} setPartnerId={setPartnerId} allowUnpaid />
        <Field label="Details" htmlFor="ex-desc">
          <Input id="ex-desc" value={description} onChange={(e) => setDescription(e.target.value)} placeholder="April rent" />
        </Field>
        <Field label="Paid to" htmlFor="ex-vendor">
          <Input id="ex-vendor" value={vendor} onChange={(e) => setVendor(e.target.value)} />
        </Field>
        <Field label="Bill / reference" htmlFor="ex-ref">
          <Input id="ex-ref" value={reference} onChange={(e) => setReference(e.target.value)} />
        </Field>
      </form>
    </Modal>
  );
}

function PayDialog({ expense, onClose, onSaved }: { expense: Expense; onClose: () => void; onSaved: () => void }) {
  const accounts = useBankAccounts();
  const [paidWith, setPaidWith] = useState("cash");
  const [bankId, setBankId] = useState("");
  const [partnerId, setPartnerId] = useState("");
  const [date, setDate] = useState(() => localDate());
  const [busy, setBusy] = useState(false);
  async function pay() {
    setBusy(true);
    try {
      await api.post(`/api/v1/expenses/${expense.expense_id}/pay`, {
        date,
        paid_with: paidWith,
        ...(paidWith === "bank" ? { bank_account_id: bankId || defaultAccount(accounts)?.bank_account_id || "" } : {}),
        ...(paidWith === "partner" ? { partner_id: partnerId } : {}),
      });
      toast.success(`${expense.expense_number} paid`);
      onSaved();
      onClose();
    } catch (err) {
      toast.error(message(err, "Could not pay it"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={`Pay ${expense.expense_number}`}
      description={`${expense.head} · ${rupees(expense.amount)}`}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={pay} disabled={busy || (paidWith === "partner" && !partnerId)}>
            Pay {rupees(expense.amount)}
          </Button>
        </>
      }
    >
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Date" htmlFor="pay-date">
          <Input id="pay-date" type="date" min={expense.expense_date} max={localDate()} value={date} onChange={(e) => setDate(e.target.value)} />
        </Field>
        <span />
        <PayerFields paidBy={paidWith} setPaidBy={setPaidWith} bankId={bankId} setBankId={setBankId} partnerId={partnerId} setPartnerId={setPartnerId} allowUnpaid={false} />
      </div>
    </Modal>
  );
}

function CancelDialog({ expense, onClose, onSaved }: { expense: Expense; onClose: () => void; onSaved: () => void }) {
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  async function cancel() {
    setBusy(true);
    try {
      await api.post(`/api/v1/expenses/${expense.expense_id}/cancel`, { reason });
      toast.success(`${expense.expense_number} cancelled`);
      onSaved();
      onClose();
    } catch (err) {
      toast.error(message(err, "Could not cancel it"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={`Cancel ${expense.expense_number}?`}
      description="A reversing entry is posted today (and of its payment, if it was paid later); a partner's capital comes back down. The expense stays on the list, marked cancelled."
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Keep it
          </Button>
          <Button variant="destructive" onClick={cancel} disabled={busy || !reason.trim()}>
            Cancel expense
          </Button>
        </>
      }
    >
      <Field label="Why *" htmlFor="ex-cancel-reason">
        <Input id="ex-cancel-reason" autoFocus value={reason} onChange={(e) => setReason(e.target.value)} />
      </Field>
    </Modal>
  );
}

// ------------------------------------------------------------------ import

type ImportRow = {
  row: number;
  date: string;
  head: string;
  account?: string;
  account_type?: string;
  amount: string;
  paid_by: string;
  paid_by_label: string;
  branch?: string;
  description: string;
  vendor: string;
  reference: string;
  warnings: string[];
  errors: string[];
};
type ImportPreview = {
  rows: ImportRow[];
  errors: string[];
  ready: boolean;
  count: number;
  total: string;
  by_head: Record<string, string>;
  by_payer: Record<string, string>;
  to_assets: string;
  to_expenses: string;
};

function ImportExpenses() {
  const { branches, defaultBranchId } = useMyBranches();
  const [branchId, setBranchId] = useState("");
  const [csv, setCsv] = useState("");
  const [fileName, setFileName] = useState("");
  const [preview, setPreview] = useState<ImportPreview | null>(null);
  const [busy, setBusy] = useState<"preview" | "commit" | null>(null);
  const [done, setDone] = useState<{ imported: number; total: string } | null>(null);
  const body = { branch_id: branchId || defaultBranchId, csv };

  async function template() {
    try {
      const { blob, filename } = await api.download("/api/v1/expenses/import/template");
      saveBlob(blob, filename);
    } catch (e) {
      toast.error(message(e, "Could not download the template"));
    }
  }
  function readFile(f: File | undefined) {
    if (!f) return;
    const r = new FileReader();
    r.onload = () => {
      setCsv(String(r.result ?? ""));
      setFileName(f.name);
      setPreview(null);
      setDone(null);
    };
    r.readAsText(f);
  }
  async function runPreview() {
    setBusy("preview");
    try {
      setPreview(await api.post<ImportPreview>("/api/v1/expenses/import/preview", body));
    } catch (e) {
      toast.error(message(e, "Could not read the file"));
    } finally {
      setBusy(null);
    }
  }
  async function commit() {
    setBusy("commit");
    try {
      const r = await api.post<{ imported: number; total: string }>("/api/v1/expenses/import/commit", body);
      setDone(r);
      setPreview(null);
      toast.success(`${r.imported} expense(s) imported`);
    } catch (e) {
      const details = e instanceof ApiError ? (e.details as { preview?: ImportPreview } | undefined) : undefined;
      if (details?.preview) setPreview(details.preview);
      toast.error(message(e, "Could not import the expenses"));
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className="space-y-4">
      {done && (
        <Panel className="border-pay/40">
          <p className="text-sm">
            <span className="font-medium">{done.imported} expense(s) imported</span>, {rupees(done.total)} in all.{" "}
            <Link href="/expenses?tab=list" className="text-primary hover:underline">
              See the list
            </Link>
          </p>
        </Panel>
      )}
      <Panel
        title="Import a list of expenses"
        description="One row per expense: date, head, amount and paid_by; description, vendor, reference and branch are optional. paid_by is cash, bank (or “bank: ICICI”), a partner's name (or “partner: Ravi”), or unpaid."
        actions={
          <Button variant="outline" onClick={template}>
            <Download aria-hidden /> Template
          </Button>
        }
      >
        <div className="grid gap-4 sm:grid-cols-3">
          <Field label="Default branch" htmlFor="ei-branch">
            <BranchSelect id="ei-branch" branches={branches} value={branchId || defaultBranchId} onChange={setBranchId} className="w-full" />
          </Field>
          <Field label="CSV file *" htmlFor="ei-file" className="sm:col-span-2">
            <Input id="ei-file" type="file" accept=".csv,text/csv" onChange={(e) => readFile(e.target.files?.[0])} />
            {fileName && <span className="text-xs text-muted-foreground">{fileName}</span>}
          </Field>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button onClick={runPreview} disabled={!csv || busy != null}>
            {busy === "preview" ? <Loader2 className="animate-spin" aria-hidden /> : <FileUp aria-hidden />} Preview
          </Button>
          {preview && (
            <Button onClick={commit} disabled={!preview.ready || busy != null}>
              {busy === "commit" && <Loader2 className="animate-spin" aria-hidden />} Import {preview.count} expense(s)
            </Button>
          )}
        </div>
      </Panel>
      {preview && (
        <>
          <Panel>
            <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
              <Stat label="Rows" value={preview.count} />
              <Stat label="Total" value={rupees(preview.total)} strong testId="ei-total" />
              <Stat label="To assets (deposits, fixtures)" value={rupees(preview.to_assets)} />
              <Stat label="To expenses (profit & loss)" value={rupees(preview.to_expenses)} />
            </div>
            <p className="text-xs text-muted-foreground">
              Paid by: {Object.entries(preview.by_payer).map(([k, v]) => `${k} ${rupees(v)}`).join(" · ") || "—"}
            </p>
            {preview.errors.length > 0 && (
              <ul className="list-disc ps-5 text-sm text-destructive">
                {preview.errors.map((e) => (
                  <li key={e}>{e}</li>
                ))}
              </ul>
            )}
          </Panel>
          <Panel title="Rows">
            <Table data-testid="ei-rows">
              <TableHeader>
                <TableRow>
                  <TableHead>Row</TableHead>
                  <TableHead>Date</TableHead>
                  <TableHead>Head → account</TableHead>
                  <TableHead>Paid by</TableHead>
                  <TableHead className="text-right">Amount</TableHead>
                  <TableHead>Details</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {preview.rows.map((r) => (
                  <TableRow key={r.row} className={r.errors.length ? "bg-destructive-soft/40" : undefined}>
                    <TableCell className="font-mono text-xs">{r.row}</TableCell>
                    <TableCell className="whitespace-nowrap text-sm">{r.date || "—"}</TableCell>
                    <TableCell className="text-sm">
                      {r.head}
                      {r.account && <span className="block text-xs text-muted-foreground">{r.account}</span>}
                    </TableCell>
                    <TableCell className="text-sm">{r.paid_by_label || "—"}</TableCell>
                    <TableCell className="tabular text-right">{r.amount ? rupees(r.amount) : "—"}</TableCell>
                    <TableCell className="max-w-80 whitespace-normal text-sm">
                      {[r.description, r.vendor, r.reference].filter(Boolean).join(" · ")}
                      {r.errors.map((e) => (
                        <span key={e} className="block text-xs text-destructive">
                          {e}
                        </span>
                      ))}
                      {r.warnings.map((w) => (
                        <span key={w} className="block text-xs text-warn">
                          {w}
                        </span>
                      ))}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </Panel>
        </>
      )}
    </div>
  );
}

// ------------------------------------------------------------------ heads

function Heads({ heads, reload }: { heads: ExpenseHead[]; reload: () => void }) {
  const [accounts, setAccounts] = useState<{ code: string; name: string; account_type: string }[]>([]);
  const [name, setName] = useState("");
  const [code, setCode] = useState("");
  useEffect(() => {
    api
      .get<{ accounts: { code: string; name: string; account_type: string }[] }>("/api/v1/accounting/accounts")
      .then((d) => setAccounts(d.accounts.filter((a) => a.account_type === "expense" || a.account_type === "asset")))
      .catch(() => {});
  }, []);
  async function save(id: string | null, patch: { name?: string; account_code?: string; status?: string }) {
    try {
      if (id) await api.patch(`/api/v1/expense-heads/${id}`, patch);
      else await api.post("/api/v1/expense-heads", patch);
      toast.success(id ? "Head saved" : "Head added");
      if (!id) {
        setName("");
        setCode("");
      }
      reload();
    } catch (e) {
      toast.error(message(e, "Could not save the head"));
    }
  }
  const accountSelect = (value: string, onChange: (v: string) => void, label: string) => (
    <select aria-label={label} value={value} onChange={(e) => onChange(e.target.value)} className={`${selectClass} w-64`}>
      <option value="">Choose an account</option>
      {accounts.map((a) => (
        <option key={a.code} value={a.code}>
          {a.code} {a.name} ({a.account_type})
        </option>
      ))}
    </select>
  );
  return (
    <Panel
      title="Expense heads"
      description="Each head posts to one account. An asset account (deposits, furniture & fixtures) keeps it off profit and loss; an expense account puts it there. A change applies to expenses entered from then on."
    >
      <Table data-testid="heads-table">
        <TableHeader>
          <TableRow>
            <TableHead>Head</TableHead>
            <TableHead>Account</TableHead>
            <TableHead>Status</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {heads.map((h) => (
            <TableRow key={h.head_id}>
              <TableCell className="font-medium">{h.name}</TableCell>
              <TableCell>{accountSelect(h.account_code, (v) => v && save(h.head_id, { account_code: v }), `Account for ${h.name}`)}</TableCell>
              <TableCell>
                <Button size="sm" variant="outline" onClick={() => save(h.head_id, { status: h.status === "active" ? "inactive" : "active" })}>
                  {h.status === "active" ? "Deactivate" : "Activate"}
                </Button>
              </TableCell>
            </TableRow>
          ))}
          <TableRow>
            <TableCell>
              <Input aria-label="New head name" value={name} onChange={(e) => setName(e.target.value)} placeholder="New head, e.g. Security" />
            </TableCell>
            <TableCell>{accountSelect(code, setCode, "Account for the new head")}</TableCell>
            <TableCell>
              <Button size="sm" disabled={!name.trim() || !code} onClick={() => save(null, { name: name.trim(), account_code: code })}>
                <Plus aria-hidden /> Add
              </Button>
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
      <p className="text-xs text-muted-foreground">
        Need another account? Add it under{" "}
        <Link href="/accounting/chart-of-accounts" className="text-primary hover:underline">
          Chart of Accounts
        </Link>
        .
      </p>
    </Panel>
  );
}
