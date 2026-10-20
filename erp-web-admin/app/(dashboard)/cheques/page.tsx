"use client";

// Cheques (erp-core-go migrations/066, internal/banking): the two registers.
//   Issued — every leaf we write: supplier bill payments by cheque (from
//   the bill), employee loans paid by cheque, and any other payee written
//   here (rent, a deposit). Print on the CTS-2010 leaf, then clear, bounce
//   or cancel; bouncing or cancelling a bill payment's cheque makes the
//   bill owed again. A spoilt leaf is cancelled so it is never reused.
//   Received — customers' cheques, post-dated or not. Nothing is posted
//   until the deposit (not before the cheque's date): the receipt goes
//   against the invoice, or on account. A bounce reverses it.
import { FormEvent, useCallback, useEffect, useMemo, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import Link from "next/link";
import { Ban, Loader2, Plus } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import type { BankAccount, IssuedCheque, ReceivedCheque } from "@/lib/types";
import { localDate, rupees } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Modal } from "@/components/ui/dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { PageHeader } from "@/components/data/page-header";
import { Field, FilterBar, Panel, selectClass } from "@/components/data/panel";
import { StatusChip, Tone } from "@/components/data/status-chip";
import { PrintPdfButton } from "@/components/data/document-actions";
import { PartyPicker } from "@/components/data/party-picker";
import { BranchSelect, useMyBranches } from "@/components/data/branch-select";
import { BankAccountSelect, defaultAccount, useBankAccounts } from "@/components/banking/bank-account-select";

const message = (e: unknown, fallback: string) => (e instanceof ApiError ? e.message : fallback);

const ISSUED_TONE: Record<IssuedCheque["status"], Tone> = { issued: "info", cleared: "ok", bounced: "bad", cancelled: "muted" };
const RECEIVED_TONE: Record<ReceivedCheque["status"], Tone> = { received: "warn", deposited: "info", cleared: "ok", bounced: "bad", returned: "muted" };

export default function ChequesPage() {
  const params = useSearchParams();
  const router = useRouter();
  const tab = params.get("tab") === "received" ? "received" : "issued";
  const { can } = usePermissions();
  const allowed = can("cheques.manage");
  return (
    <div className="space-y-5">
      <PageHeader
        title="Cheques"
        description="Cheques written and received: print, clear and bounce them, and deposit customers' cheques (post-dated ones on or after their date)."
      />
      <div role="tablist" aria-label="Cheque registers" className="flex gap-1 rounded-lg bg-muted p-1 w-fit">
        {(["issued", "received"] as const).map((t) => (
          <button
            key={t}
            role="tab"
            aria-selected={tab === t}
            onClick={() => router.replace(`/cheques?tab=${t}`)}
            className={`rounded-md px-3 py-1.5 text-sm ${tab === t ? "bg-card font-medium shadow-sm" : "text-muted-foreground hover:text-foreground"}`}
          >
            {t === "issued" ? "Issued" : "Received"}
          </button>
        ))}
      </div>
      {!allowed && <p className="text-sm text-muted-foreground">{missingPermissionHint("cheques.manage")}</p>}
      {tab === "issued" ? <IssuedRegister allowed={allowed} /> : <ReceivedRegister allowed={allowed} />}
    </div>
  );
}

// ------------------------------------------------------------------ actions

type ActionSpec = { title: string; path: string; date?: boolean; reason?: boolean; bank?: boolean; danger?: boolean; confirm: string; note?: string };

function ActionDialog({ spec, accounts, onClose, onDone }: { spec: ActionSpec; accounts: BankAccount[]; onClose: () => void; onDone: () => void }) {
  const [date, setDate] = useState(localDate());
  const [reason, setReason] = useState("");
  const [bank, setBank] = useState(defaultAccount(accounts)?.bank_account_id ?? "");
  const [busy, setBusy] = useState(false);
  async function run() {
    setBusy(true);
    try {
      await api.post(spec.path, { ...(spec.date ? { date } : {}), ...(spec.reason ? { reason } : {}), ...(spec.bank ? { bank_account_id: bank } : {}) });
      toast.success(spec.title.replace(/^(\w+).*/, "$1") + " done");
      onDone();
      onClose();
    } catch (e) {
      toast.error(message(e, "Could not save"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={spec.title}
      description={spec.note}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Back
          </Button>
          <Button variant={spec.danger ? "destructive" : "default"} disabled={busy || (spec.reason && !reason.trim()) || (spec.bank && !bank)} onClick={run}>
            {busy && <Loader2 className="animate-spin" aria-hidden />}
            {spec.confirm}
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        {spec.bank && (
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ca-bank">Into account</Label>
            <BankAccountSelect id="ca-bank" accounts={accounts} value={bank} onChange={setBank} className="w-full" />
          </div>
        )}
        {spec.date && (
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ca-date">Date</Label>
            <Input id="ca-date" type="date" value={date} onChange={(e) => setDate(e.target.value)} />
          </div>
        )}
        {spec.reason && (
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ca-reason">Reason</Label>
            <Input id="ca-reason" value={reason} onChange={(e) => setReason(e.target.value)} placeholder="e.g. insufficient funds" />
          </div>
        )}
      </div>
    </Modal>
  );
}

// ------------------------------------------------------------------ issued

function IssuedRegister({ allowed }: { allowed: boolean }) {
  const accounts = useBankAccounts();
  const [filters, setFilters] = useState({ bank_account_id: "", status: "", from: "", to: "", q: "" });
  const [rows, setRows] = useState<IssuedCheque[]>([]);
  const [loading, setLoading] = useState(true);
  const [action, setAction] = useState<ActionSpec | null>(null);
  const [writing, setWriting] = useState(false);
  const [spoiling, setSpoiling] = useState(false);

  const load = useCallback(() => {
    const qs = new URLSearchParams(Object.entries(filters).filter(([, v]) => v));
    api
      .get<{ cheques: IssuedCheque[] }>(`/api/v1/cheques/issued?${qs}`)
      .then((d) => setRows(d.cheques))
      .catch((e) => toast.error(message(e, "Could not load the cheques")))
      .finally(() => setLoading(false));
  }, [filters]);
  useEffect(load, [load]);

  const total = useMemo(() => rows.filter((r) => r.status === "issued").reduce((s, r) => s + Number(r.amount), 0), [rows]);

  return (
    <>
      <FilterBar>
        <Field label="Account" htmlFor="ci-bank">
          <BankAccountSelect id="ci-bank" accounts={accounts} value={filters.bank_account_id} onChange={(v) => setFilters({ ...filters, bank_account_id: v })} allowNone noneLabel="All accounts" />
        </Field>
        <Field label="Status" htmlFor="ci-status">
          <select id="ci-status" value={filters.status} onChange={(e) => setFilters({ ...filters, status: e.target.value })} className={selectClass}>
            <option value="">All</option>
            <option value="issued">Issued (not cleared)</option>
            <option value="cleared">Cleared</option>
            <option value="bounced">Bounced</option>
            <option value="cancelled">Cancelled</option>
          </select>
        </Field>
        <Field label="From" htmlFor="ci-from">
          <Input id="ci-from" type="date" value={filters.from} onChange={(e) => setFilters({ ...filters, from: e.target.value })} className="w-40" />
        </Field>
        <Field label="To" htmlFor="ci-to">
          <Input id="ci-to" type="date" value={filters.to} onChange={(e) => setFilters({ ...filters, to: e.target.value })} className="w-40" />
        </Field>
        <Field label="Search" htmlFor="ci-q">
          <Input id="ci-q" type="search" placeholder="Number or payee" value={filters.q} onChange={(e) => setFilters({ ...filters, q: e.target.value })} className="w-48" />
        </Field>
      </FilterBar>
      <Panel
        title={`Issued cheques (${rows.length})`}
        description={total > 0 ? `${rupees(total)} written and not yet cleared` : undefined}
        actions={
          <>
            <Button variant="outline" disabled={!allowed || accounts.length === 0} onClick={() => setSpoiling(true)}>
              <Ban /> Cancel a leaf
            </Button>
            <Button disabled={!allowed || accounts.length === 0} onClick={() => setWriting(true)} title={accounts.length === 0 ? "Add a bank account first" : undefined}>
              <Plus /> Write a cheque
            </Button>
          </>
        }
      >
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Cheque</TableHead>
              <TableHead>Date</TableHead>
              <TableHead>Payee</TableHead>
              <TableHead className="text-right">Amount</TableHead>
              <TableHead>For</TableHead>
              <TableHead>Status</TableHead>
              <TableHead className="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((c) => (
              <TableRow key={c.cheque_id} data-testid="issued-cheque-row">
                <TableCell className="font-mono">
                  {c.cheque_number}
                  <span className="block font-sans text-xs text-muted-foreground">{c.bank_account}</span>
                </TableCell>
                <TableCell className="tabular">{c.cheque_date}</TableCell>
                <TableCell>
                  {c.payee_name || "—"}
                  {c.account_payee && c.status !== "cancelled" && <span className="block text-xs text-muted-foreground">A/c payee</span>}
                </TableCell>
                <TableCell className="tabular text-right">{c.status === "cancelled" && Number(c.amount) === 0 ? "—" : rupees(c.amount)}</TableCell>
                <TableCell className="text-sm">
                  {c.purchase_bill_id ? (
                    <Link href={`/purchase/bills/${c.purchase_bill_id}`} className="hover:underline">
                      Bill {c.bill_number}
                    </Link>
                  ) : (
                    c.memo || "—"
                  )}
                </TableCell>
                <TableCell>
                  <StatusChip status={c.status} tone={ISSUED_TONE[c.status]} />
                  {(c.status_reason || c.cleared_on || c.bounced_on) && (
                    <span className="block text-xs text-muted-foreground">{[c.cleared_on && `on ${c.cleared_on}`, c.bounced_on && `on ${c.bounced_on}`, c.status_reason].filter(Boolean).join(" · ")}</span>
                  )}
                </TableCell>
                <TableCell>
                  <div className="flex justify-end gap-1.5">
                    {c.status !== "cancelled" && <PrintPdfButton path={`/api/v1/cheques/issued/${c.cheque_id}/print`} label={c.print_count > 0 ? `Print (${c.print_count})` : "Print"} />}
                    {c.status === "issued" && allowed && (
                      <>
                        <Button size="sm" variant="outline" onClick={() => setAction({ title: `Clear cheque ${c.cheque_number}`, path: `/api/v1/cheques/issued/${c.cheque_id}/clear`, date: true, confirm: "Mark cleared", note: "The bank has paid it." })}>
                          Clear
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          onClick={() =>
                            setAction({
                              title: `Bounce cheque ${c.cheque_number}`,
                              path: `/api/v1/cheques/issued/${c.cheque_id}/bounce`,
                              date: true,
                              reason: true,
                              danger: true,
                              confirm: "Mark bounced",
                              note: c.bill_payment_id ? `The bill payment it made is reversed, and bill ${c.bill_number} is owed again.` : "Record that the bank returned it.",
                            })
                          }
                        >
                          Bounce
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          onClick={() =>
                            setAction({
                              title: `Cancel cheque ${c.cheque_number}`,
                              path: `/api/v1/cheques/issued/${c.cheque_id}/cancel`,
                              reason: true,
                              danger: true,
                              confirm: "Cancel cheque",
                              note: c.bill_payment_id ? `Stopped before it was presented: bill ${c.bill_number} is owed again.` : "Stopped or voided before it was presented.",
                            })
                          }
                        >
                          Cancel
                        </Button>
                      </>
                    )}
                  </div>
                </TableCell>
              </TableRow>
            ))}
            {!loading && rows.length === 0 && (
              <TableRow>
                <TableCell colSpan={7} className="py-6 text-center text-sm text-muted-foreground">
                  No cheques match. Pay a supplier bill by cheque from the bill, or write one here.
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </Panel>
      {action && <ActionDialog spec={action} accounts={accounts} onClose={() => setAction(null)} onDone={load} />}
      {writing && <WriteChequeDialog accounts={accounts} onClose={() => setWriting(false)} onDone={load} />}
      {spoiling && <CancelLeafDialog accounts={accounts} onClose={() => setSpoiling(false)} onDone={load} />}
    </>
  );
}

function WriteChequeDialog({ accounts, onClose, onDone }: { accounts: BankAccount[]; onClose: () => void; onDone: () => void }) {
  const [f, setF] = useState({
    bank_account_id: defaultAccount(accounts)?.bank_account_id ?? "",
    cheque_number: "",
    cheque_date: localDate(),
    payee_name: "",
    amount: "",
    memo: "",
  });
  const [accountPayee, setAccountPayee] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const c = await api.post<IssuedCheque>("/api/v1/cheques/issued", { ...f, amount: Number(f.amount), account_payee: accountPayee });
      toast.success(`Cheque ${c.cheque_number} written — print it from the register`);
      onDone();
      onClose();
    } catch (err) {
      setError(message(err, "Could not write the cheque"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title="Write a cheque"
      description="For any payee — rent, a deposit, a refund. Its ledger entry is the voucher that records the payment; supplier bills are paid by cheque from the bill."
      width={560}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="write-cheque" disabled={busy}>
            {busy && <Loader2 className="animate-spin" aria-hidden />}
            Write cheque
          </Button>
        </>
      }
    >
      <form id="write-cheque" onSubmit={save} className="grid gap-3 sm:grid-cols-2">
        <div className="flex flex-col gap-1.5 sm:col-span-2">
          <Label htmlFor="wc-bank">From account</Label>
          <BankAccountSelect id="wc-bank" accounts={accounts} value={f.bank_account_id} onChange={(v) => setF({ ...f, bank_account_id: v })} className="w-full" />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="wc-number">Cheque number</Label>
          <Input id="wc-number" inputMode="numeric" value={f.cheque_number} onChange={(e) => setF({ ...f, cheque_number: e.target.value.replace(/\D/g, "") })} placeholder="The next leaf" />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="wc-date">Cheque date</Label>
          <Input id="wc-date" type="date" required value={f.cheque_date} onChange={(e) => setF({ ...f, cheque_date: e.target.value })} />
        </div>
        <div className="flex flex-col gap-1.5 sm:col-span-2">
          <Label htmlFor="wc-payee">Pay</Label>
          <Input id="wc-payee" required maxLength={120} value={f.payee_name} onChange={(e) => setF({ ...f, payee_name: e.target.value })} />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="wc-amount">Amount</Label>
          <Input id="wc-amount" type="number" min="0.01" step="0.01" required value={f.amount} onChange={(e) => setF({ ...f, amount: e.target.value })} />
        </div>
        <label className="flex items-center gap-2 self-end pb-2 text-sm">
          <input type="checkbox" checked={accountPayee} onChange={(e) => setAccountPayee(e.target.checked)} /> Cross &quot;A/c Payee&quot;
        </label>
        <div className="flex flex-col gap-1.5 sm:col-span-2">
          <Label htmlFor="wc-memo">For (on the register)</Label>
          <Input id="wc-memo" value={f.memo} onChange={(e) => setF({ ...f, memo: e.target.value })} placeholder="October rent" />
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

function CancelLeafDialog({ accounts, onClose, onDone }: { accounts: BankAccount[]; onClose: () => void; onDone: () => void }) {
  const [bank, setBank] = useState(defaultAccount(accounts)?.bank_account_id ?? "");
  const [number, setNumber] = useState("");
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  async function run() {
    setBusy(true);
    try {
      await api.post("/api/v1/cheques/issued/cancel-leaf", { bank_account_id: bank, cheque_number: number, reason });
      toast.success(`Leaf ${number} cancelled`);
      onDone();
      onClose();
    } catch (e) {
      toast.error(message(e, "Could not cancel the leaf"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title="Cancel a cheque leaf"
      description="A spoilt, torn or lost leaf that was never given to anyone. It is kept on the register so the number is never used."
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Back
          </Button>
          <Button variant="destructive" disabled={busy || !bank || !number || !reason.trim()} onClick={run}>
            Cancel leaf
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="cl-bank">Account</Label>
          <BankAccountSelect id="cl-bank" accounts={accounts} value={bank} onChange={setBank} className="w-full" />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="cl-number">Cheque number</Label>
          <Input id="cl-number" inputMode="numeric" value={number} onChange={(e) => setNumber(e.target.value.replace(/\D/g, ""))} />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="cl-reason">Reason</Label>
          <Input id="cl-reason" value={reason} onChange={(e) => setReason(e.target.value)} placeholder="misprinted" />
        </div>
      </div>
    </Modal>
  );
}

// ------------------------------------------------------------------ received

function ReceivedRegister({ allowed }: { allowed: boolean }) {
  const accounts = useBankAccounts();
  const [filters, setFilters] = useState({ status: "", q: "", post_dated: "" });
  const [rows, setRows] = useState<ReceivedCheque[]>([]);
  const [loading, setLoading] = useState(true);
  const [action, setAction] = useState<ActionSpec | null>(null);
  const [recording, setRecording] = useState(false);

  const load = useCallback(() => {
    const qs = new URLSearchParams(Object.entries(filters).filter(([, v]) => v));
    api
      .get<{ cheques: ReceivedCheque[] }>(`/api/v1/cheques/received?${qs}`)
      .then((d) => setRows(d.cheques))
      .catch((e) => toast.error(message(e, "Could not load the cheques")))
      .finally(() => setLoading(false));
  }, [filters]);
  useEffect(load, [load]);

  const today = localDate();
  const held = rows.filter((r) => r.status === "received");

  return (
    <>
      <FilterBar>
        <Field label="Status" htmlFor="cr-status">
          <select id="cr-status" value={filters.status} onChange={(e) => setFilters({ ...filters, status: e.target.value })} className={selectClass}>
            <option value="">All</option>
            <option value="received">In hand (not deposited)</option>
            <option value="deposited">Deposited</option>
            <option value="cleared">Cleared</option>
            <option value="bounced">Bounced</option>
            <option value="returned">Returned</option>
          </select>
        </Field>
        <label className="flex items-center gap-2 pb-2 text-sm">
          <input type="checkbox" checked={filters.post_dated === "true"} onChange={(e) => setFilters({ ...filters, post_dated: e.target.checked ? "true" : "" })} /> Post-dated only
        </label>
        <Field label="Search" htmlFor="cr-q">
          <Input id="cr-q" type="search" placeholder="Number, drawer or customer" value={filters.q} onChange={(e) => setFilters({ ...filters, q: e.target.value })} className="w-56" />
        </Field>
      </FilterBar>
      <Panel
        title={`Received cheques (${rows.length})`}
        description={held.length > 0 ? `${held.length} in hand, ${rupees(held.reduce((s, r) => s + Number(r.amount), 0))}` : undefined}
        actions={
          <Button disabled={!allowed} onClick={() => setRecording(true)}>
            <Plus /> Record a cheque
          </Button>
        }
      >
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Cheque</TableHead>
              <TableHead>Date</TableHead>
              <TableHead>From</TableHead>
              <TableHead className="text-right">Amount</TableHead>
              <TableHead>Against</TableHead>
              <TableHead>Status</TableHead>
              <TableHead className="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((c) => (
              <TableRow key={c.cheque_id} data-testid="received-cheque-row">
                <TableCell className="font-mono">
                  {c.cheque_number}
                  <span className="block font-sans text-xs text-muted-foreground">{c.drawer_bank || "—"}</span>
                </TableCell>
                <TableCell className="tabular">
                  {c.cheque_date}
                  {c.post_dated && <span className="block text-xs text-warn">post-dated</span>}
                </TableCell>
                <TableCell>
                  {c.drawer_name}
                  {c.customer_name && c.customer_name !== c.drawer_name && <span className="block text-xs text-muted-foreground">{c.customer_name}</span>}
                </TableCell>
                <TableCell className="tabular text-right">{rupees(c.amount)}</TableCell>
                <TableCell className="text-sm">
                  {c.sales_order_id ? (
                    <Link href={`/sales/${c.sales_order_id}`} className="hover:underline">
                      Invoice {c.order_number}
                    </Link>
                  ) : (
                    "On account"
                  )}
                </TableCell>
                <TableCell>
                  <StatusChip status={c.status} tone={RECEIVED_TONE[c.status]} label={c.status === "received" ? "In hand" : undefined} />
                  {(c.deposit_bank_account || c.status_reason) && (
                    <span className="block text-xs text-muted-foreground">
                      {[c.deposit_bank_account && `${c.deposit_bank_account}${c.deposited_on ? ` on ${c.deposited_on}` : ""}`, c.status_reason].filter(Boolean).join(" · ")}
                    </span>
                  )}
                </TableCell>
                <TableCell>
                  {allowed && (
                    <div className="flex justify-end gap-1.5">
                      {c.status === "received" && (
                        <>
                          <Button
                            size="sm"
                            variant="outline"
                            disabled={c.cheque_date > today || accounts.length === 0}
                            title={c.cheque_date > today ? `Can be deposited from ${c.cheque_date}` : undefined}
                            onClick={() =>
                              setAction({
                                title: `Deposit cheque ${c.cheque_number}`,
                                path: `/api/v1/cheques/received/${c.cheque_id}/deposit`,
                                bank: true,
                                date: true,
                                confirm: "Deposit",
                                note: c.sales_order_id ? `The receipt is recorded against invoice ${c.order_number}.` : "The receipt is recorded on the customer's account.",
                              })
                            }
                          >
                            Deposit
                          </Button>
                          <Button
                            size="sm"
                            variant="ghost"
                            onClick={() => setAction({ title: `Return cheque ${c.cheque_number}`, path: `/api/v1/cheques/received/${c.cheque_id}/return`, reason: true, confirm: "Return to customer" })}
                          >
                            Return
                          </Button>
                        </>
                      )}
                      {c.status === "deposited" && (
                        <>
                          <Button size="sm" variant="outline" onClick={() => setAction({ title: `Clear cheque ${c.cheque_number}`, path: `/api/v1/cheques/received/${c.cheque_id}/clear`, date: true, confirm: "Mark cleared" })}>
                            Clear
                          </Button>
                          <Button
                            size="sm"
                            variant="ghost"
                            onClick={() =>
                              setAction({
                                title: `Bounce cheque ${c.cheque_number}`,
                                path: `/api/v1/cheques/received/${c.cheque_id}/bounce`,
                                date: true,
                                reason: true,
                                danger: true,
                                confirm: "Mark bounced",
                                note: c.sales_order_id ? `The receipt is reversed and invoice ${c.order_number} is owed again.` : "The receipt is reversed on the customer's account.",
                              })
                            }
                          >
                            Bounce
                          </Button>
                        </>
                      )}
                    </div>
                  )}
                </TableCell>
              </TableRow>
            ))}
            {!loading && rows.length === 0 && (
              <TableRow>
                <TableCell colSpan={7} className="py-6 text-center text-sm text-muted-foreground">
                  No cheques match.
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </Panel>
      {action && <ActionDialog spec={action} accounts={accounts} onClose={() => setAction(null)} onDone={load} />}
      {recording && <RecordChequeDialog onClose={() => setRecording(false)} onDone={load} />}
    </>
  );
}

type OpenInvoice = { order_id: string; order_number: string; outstanding: string; due_date: string };

function RecordChequeDialog({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const { branches, defaultBranchId } = useMyBranches();
  const [branch, setBranch] = useState("");
  const [customer, setCustomer] = useState({ id: "", name: "" });
  const [invoices, setInvoices] = useState<OpenInvoice[]>([]);
  const [f, setF] = useState({ sales_order_id: "", drawer_name: "", drawer_bank: "", drawer_branch: "", cheque_number: "", cheque_date: localDate(), amount: "", notes: "" });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!customer.id) return;
    api
      .get<{ open_invoices: OpenInvoice[] }>(`/api/v1/customers/${customer.id}/credit`)
      .then((d) => setInvoices(d.open_invoices ?? []))
      .catch(() => setInvoices([]));
  }, [customer.id]);

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api.post("/api/v1/cheques/received", { ...f, branch_id: branch || defaultBranchId, customer_id: customer.id, amount: Number(f.amount) });
      toast.success(f.cheque_date > localDate() ? "Post-dated cheque recorded — deposit it on or after its date" : "Cheque recorded — deposit it when it goes to the bank");
      onDone();
      onClose();
    } catch (err) {
      setError(message(err, "Could not record the cheque"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title="Record a customer's cheque"
      description="Nothing is posted until it is deposited."
      width={600}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="record-cheque" disabled={busy || !customer.id}>
            {busy && <Loader2 className="animate-spin" aria-hidden />}
            Record cheque
          </Button>
        </>
      }
    >
      <form id="record-cheque" onSubmit={save} className="grid gap-3 sm:grid-cols-2">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="rc-customer">Customer</Label>
          <PartyPicker
            type="customer"
            value={customer.id}
            initialLabel={customer.name}
            ariaLabel="Customer"
            onChange={(id, name) => {
              setCustomer({ id, name });
              setF({ ...f, sales_order_id: "", drawer_name: f.drawer_name || name });
            }}
          />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="rc-branch">Branch</Label>
          <BranchSelect id="rc-branch" branches={branches} value={branch || defaultBranchId} onChange={setBranch} className="w-full" />
        </div>
        <div className="flex flex-col gap-1.5 sm:col-span-2">
          <Label htmlFor="rc-invoice">Against</Label>
          <select
            id="rc-invoice"
            value={f.sales_order_id}
            onChange={(e) => {
              const inv = invoices.find((i) => i.order_id === e.target.value);
              setF({ ...f, sales_order_id: e.target.value, amount: inv ? inv.outstanding : f.amount });
            }}
            className={selectClass}
          >
            <option value="">On account (no invoice)</option>
            {invoices.map((i) => (
              <option key={i.order_id} value={i.order_id}>
                Invoice {i.order_number} · {rupees(i.outstanding)} owed{i.due_date ? ` · due ${i.due_date}` : ""}
              </option>
            ))}
          </select>
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="rc-drawer">Drawn by (name on the cheque)</Label>
          <Input id="rc-drawer" value={f.drawer_name} onChange={(e) => setF({ ...f, drawer_name: e.target.value })} />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="rc-bank">Their bank</Label>
          <Input id="rc-bank" value={f.drawer_bank} onChange={(e) => setF({ ...f, drawer_bank: e.target.value })} placeholder="Canara Bank" />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="rc-number">Cheque number</Label>
          <Input id="rc-number" required inputMode="numeric" value={f.cheque_number} onChange={(e) => setF({ ...f, cheque_number: e.target.value })} />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="rc-date">Cheque date</Label>
          <Input id="rc-date" type="date" required value={f.cheque_date} onChange={(e) => setF({ ...f, cheque_date: e.target.value })} />
          {f.cheque_date > localDate() && <span className="text-xs text-warn">Post-dated: it can be deposited from {f.cheque_date}.</span>}
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="rc-amount">Amount</Label>
          <Input id="rc-amount" type="number" min="0.01" step="0.01" required value={f.amount} onChange={(e) => setF({ ...f, amount: e.target.value })} />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="rc-notes">Notes</Label>
          <Input id="rc-notes" value={f.notes} onChange={(e) => setF({ ...f, notes: e.target.value })} />
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
