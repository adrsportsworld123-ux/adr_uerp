"use client";

// Employee Loans (erp-core-go migrations/067, internal/loans): loans and
// salary advances to employees, with optional flat interest, repaid in
// monthly instalments. Each payroll run deducts the instalments due (shown
// on the payslip) and finalizing it recovers them; repayments by hand and
// foreclosure are recorded here. Paying out by cheque writes the cheque on
// the register. loans.manage.
import { FormEvent, useCallback, useEffect, useState } from "react";
import { Loader2, Plus } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import type { EmployeeLoan, LoanInstallment } from "@/lib/types";
import { localDate, rupees } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Modal } from "@/components/ui/dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { PageHeader } from "@/components/data/page-header";
import { Field, FilterBar, Panel, selectClass } from "@/components/data/panel";
import { StatusChip, Tone } from "@/components/data/status-chip";
import { PartyPicker } from "@/components/data/party-picker";
import { BankAccountSelect, defaultAccount, useBankAccounts } from "@/components/banking/bank-account-select";

const message = (e: unknown, fallback: string) => (e instanceof ApiError ? e.message : fallback);
const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
const STATUS_TONE: Record<EmployeeLoan["status"], Tone> = { active: "info", closed: "ok", foreclosed: "plum" };
const METHOD_LABEL: Record<string, string> = { cash: "Cash", bank_transfer: "Bank transfer", cheque: "Cheque", payroll: "Payroll" };
const month = (m: number, y: number) => `${MONTHS[m - 1]} ${y}`;

export default function EmployeeLoansPage() {
  const { can } = usePermissions();
  const allowed = can("loans.manage");
  const [loans, setLoans] = useState<EmployeeLoan[]>([]);
  const [status, setStatus] = useState("active");
  const [q, setQ] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [open, setOpen] = useState<string | null>(null);

  const load = useCallback(() => {
    if (!allowed) return;
    const qs = new URLSearchParams({ ...(status ? { status } : {}), ...(q.trim() ? { q: q.trim() } : {}) });
    api
      .get<{ loans: EmployeeLoan[] }>(`/api/v1/employee-loans?${qs}`)
      .then((d) => {
        setLoans(d.loans);
        setError(null);
      })
      .catch((e) => setError(message(e, "Could not load the loans")))
      .finally(() => setLoading(false));
  }, [status, q, allowed]);
  useEffect(load, [load]);

  const outstanding = loans.filter((l) => l.status === "active").reduce((s, l) => s + Number(l.outstanding_principal), 0);

  return (
    <div className="space-y-5">
      <PageHeader
        title="Employee Loans"
        description="Loans and salary advances to employees, recovered from their pay each month or repaid by hand."
        actions={
          <Button disabled={!allowed} onClick={() => setCreating(true)}>
            <Plus /> New loan or advance
          </Button>
        }
      />
      {!allowed && <p className="text-sm text-muted-foreground">{missingPermissionHint("loans.manage")}</p>}
      <FilterBar>
        <Field label="Status" htmlFor="ln-status">
          <select id="ln-status" value={status} onChange={(e) => setStatus(e.target.value)} className={selectClass}>
            <option value="active">Active</option>
            <option value="closed">Closed</option>
            <option value="foreclosed">Foreclosed</option>
            <option value="">All</option>
          </select>
        </Field>
        <Field label="Search" htmlFor="ln-q">
          <Input id="ln-q" type="search" placeholder="Employee, code or loan no." value={q} onChange={(e) => setQ(e.target.value)} className="w-56" />
        </Field>
      </FilterBar>
      <Panel title={`Loans (${loans.length})`} description={outstanding > 0 ? `${rupees(outstanding)} principal outstanding` : undefined}>
        {error && <p className="text-sm text-destructive">{error}</p>}
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Loan</TableHead>
              <TableHead>Employee</TableHead>
              <TableHead className="text-right">Amount</TableHead>
              <TableHead className="text-right">EMI</TableHead>
              <TableHead>Instalments left</TableHead>
              <TableHead className="text-right">Outstanding</TableHead>
              <TableHead>Status</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loans.map((l) => (
              <TableRow key={l.loan_id} className="cursor-pointer" onClick={() => setOpen(l.loan_id)} data-testid="loan-row">
                <TableCell className="font-medium">
                  {l.loan_number}
                  <span className="block text-xs font-normal text-muted-foreground">
                    {l.loan_type === "advance" ? "Salary advance" : `Loan · ${Number(l.interest_rate)}% flat`} · from {month(l.start_month, l.start_year)}
                  </span>
                </TableCell>
                <TableCell>
                  {l.employee_name}
                  {l.employee_code && <span className="block text-xs text-muted-foreground">{l.employee_code}</span>}
                </TableCell>
                <TableCell className="tabular text-right">{rupees(l.principal)}</TableCell>
                <TableCell className="tabular text-right">{rupees(l.emi_amount)}</TableCell>
                <TableCell className="tabular">
                  {l.installments_left} of {l.tenure_months}
                </TableCell>
                <TableCell className="tabular text-right">{rupees(Number(l.outstanding_principal) + Number(l.outstanding_interest))}</TableCell>
                <TableCell>
                  <StatusChip status={l.status} tone={STATUS_TONE[l.status]} />
                </TableCell>
              </TableRow>
            ))}
            {!loading && loans.length === 0 && allowed && (
              <TableRow>
                <TableCell colSpan={7} className="py-6 text-center text-sm text-muted-foreground">
                  No loans match.
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </Panel>
      {creating && (
        <NewLoanDialog
          onClose={() => setCreating(false)}
          onDone={(id) => {
            load();
            setOpen(id);
          }}
        />
      )}
      {open && <LoanDialog loanId={open} onClose={() => setOpen(null)} onChanged={load} />}
    </div>
  );
}

function ScheduleTable({ schedule }: { schedule: LoanInstallment[] }) {
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>#</TableHead>
          <TableHead>Month</TableHead>
          <TableHead className="text-right">Principal</TableHead>
          <TableHead className="text-right">Interest</TableHead>
          <TableHead className="text-right">Instalment</TableHead>
          <TableHead>Status</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {schedule.map((i) => (
          <TableRow key={i.seq}>
            <TableCell className="tabular">{i.seq}</TableCell>
            <TableCell>{month(i.due_month, i.due_year)}</TableCell>
            <TableCell className="tabular text-right">{rupees(i.principal_part)}</TableCell>
            <TableCell className="tabular text-right">{rupees(i.interest_part)}</TableCell>
            <TableCell className="tabular text-right">{rupees(i.amount)}</TableCell>
            <TableCell className="text-xs">
              {i.status === "recovered" ? (
                <StatusChip status="completed" label={`Recovered${i.recovered_via ? ` · ${METHOD_LABEL[i.recovered_via] ?? i.recovered_via}` : ""}`} />
              ) : (
                <StatusChip status="pending" label="Due" />
              )}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function NewLoanDialog({ onClose, onDone }: { onClose: () => void; onDone: (id: string) => void }) {
  const accounts = useBankAccounts();
  const now = new Date();
  const nextMonth = now.getMonth() + 2 > 12 ? 1 : now.getMonth() + 2;
  const [employee, setEmployee] = useState({ id: "", name: "" });
  const [f, setF] = useState({
    loan_type: "loan",
    purpose: "",
    principal: "",
    interest_rate: "0",
    tenure_months: "12",
    start_month: String(nextMonth),
    start_year: String(nextMonth === 1 ? now.getFullYear() + 1 : now.getFullYear()),
    disbursed_on: localDate(),
    disbursement_method: "bank_transfer",
    cheque_number: "",
  });
  const [bank, setBank] = useState<string | null>(null);
  const bankAccount = bank ?? defaultAccount(accounts)?.bank_account_id ?? "";
  const [preview, setPreview] = useState<{ total_interest: string; emi_amount: string; total_payable: string; schedule: LoanInstallment[] } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const set = (k: keyof typeof f, v: string) => setF({ ...f, [k]: v });
  const terms = {
    principal: Number(f.principal),
    interest_rate: f.loan_type === "advance" ? 0 : Number(f.interest_rate) || 0,
    tenure_months: Number(f.tenure_months),
    start_month: Number(f.start_month),
    start_year: Number(f.start_year),
  };

  const valid = terms.principal > 0 && terms.tenure_months > 0 && terms.tenure_months <= 120;
  useEffect(() => {
    if (!valid) return;
    const t = setTimeout(() => {
      api
        .post<NonNullable<typeof preview>>("/api/v1/employee-loans/preview", terms)
        .then(setPreview)
        .catch(() => setPreview(null));
    }, 300);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- the terms are derived from f
  }, [f.principal, f.interest_rate, f.tenure_months, f.start_month, f.start_year, f.loan_type, valid]);

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const l = await api.post<EmployeeLoan>("/api/v1/employee-loans", {
        ...terms,
        user_id: employee.id,
        loan_type: f.loan_type,
        purpose: f.purpose,
        disbursed_on: f.disbursed_on,
        disbursement_method: f.disbursement_method,
        ...(f.disbursement_method !== "cash" && bankAccount ? { bank_account_id: bankAccount } : {}),
        ...(f.disbursement_method === "cheque" ? { cheque_number: f.cheque_number } : {}),
      });
      toast.success(`${l.loan_number} paid out${l.issued_cheque_id ? " — the cheque is on the register to print" : ""}`);
      onDone(l.loan_id);
      onClose();
    } catch (err) {
      setError(message(err, "Could not create the loan"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title="New loan or advance"
      width={720}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="new-loan" disabled={busy || !employee.id || !(terms.principal > 0)}>
            {busy && <Loader2 className="animate-spin" aria-hidden />}
            Pay out
          </Button>
        </>
      }
    >
      <form id="new-loan" onSubmit={save} className="space-y-4">
        <div className="grid gap-3 sm:grid-cols-3">
          <div className="flex flex-col gap-1.5 sm:col-span-2">
            <Label htmlFor="nl-employee">Employee</Label>
            <PartyPicker type="employee" value={employee.id} initialLabel={employee.name} ariaLabel="Employee" onChange={(id, name) => setEmployee({ id, name })} />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="nl-type">Kind</Label>
            <select id="nl-type" value={f.loan_type} onChange={(e) => set("loan_type", e.target.value)} className={selectClass}>
              <option value="loan">Loan</option>
              <option value="advance">Salary advance (no interest)</option>
            </select>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="nl-amount">Amount</Label>
            <Input id="nl-amount" type="number" min="1" step="0.01" required value={f.principal} onChange={(e) => set("principal", e.target.value)} />
          </div>
          {f.loan_type === "loan" && (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="nl-rate">Interest (% a year, flat)</Label>
              <Input id="nl-rate" type="number" min="0" max="60" step="0.01" value={f.interest_rate} onChange={(e) => set("interest_rate", e.target.value)} />
            </div>
          )}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="nl-tenure">Months</Label>
            <Input id="nl-tenure" type="number" min="1" max="120" required value={f.tenure_months} onChange={(e) => set("tenure_months", e.target.value)} />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="nl-start">First instalment</Label>
            <div className="flex gap-1.5">
              <select id="nl-start" aria-label="First instalment month" value={f.start_month} onChange={(e) => set("start_month", e.target.value)} className={selectClass}>
                {MONTHS.map((m, i) => (
                  <option key={m} value={i + 1}>
                    {m}
                  </option>
                ))}
              </select>
              <Input aria-label="First instalment year" type="number" min="2000" max="2100" value={f.start_year} onChange={(e) => set("start_year", e.target.value)} className="w-24" />
            </div>
          </div>
          <div className="flex flex-col gap-1.5 sm:col-span-2">
            <Label htmlFor="nl-purpose">Purpose</Label>
            <Input id="nl-purpose" maxLength={200} value={f.purpose} onChange={(e) => set("purpose", e.target.value)} placeholder="Medical, education, festival advance…" />
          </div>
        </div>
        <fieldset className="grid gap-3 rounded-lg border p-3 sm:grid-cols-3">
          <legend className="px-1 text-sm font-medium">Paid out</legend>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="nl-method">By</Label>
            <select id="nl-method" value={f.disbursement_method} onChange={(e) => set("disbursement_method", e.target.value)} className={selectClass}>
              <option value="bank_transfer">Bank transfer</option>
              <option value="cheque">Cheque</option>
              <option value="cash">Cash</option>
            </select>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="nl-on">On</Label>
            <Input id="nl-on" type="date" value={f.disbursed_on} max={localDate()} onChange={(e) => set("disbursed_on", e.target.value)} />
          </div>
          {f.disbursement_method !== "cash" && (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="nl-bank">From account</Label>
              <BankAccountSelect id="nl-bank" accounts={accounts} value={bankAccount} onChange={setBank} allowNone className="w-full" />
            </div>
          )}
          {f.disbursement_method === "cheque" && bankAccount && (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="nl-cheque">Cheque number</Label>
              <Input id="nl-cheque" inputMode="numeric" value={f.cheque_number} onChange={(e) => set("cheque_number", e.target.value.replace(/\D/g, ""))} placeholder="The next leaf" />
            </div>
          )}
        </fieldset>
        {valid && preview && (
          <div className="space-y-2">
            <p className="text-sm">
              EMI <span className="font-semibold tabular">{rupees(preview.emi_amount)}</span> · interest {rupees(preview.total_interest)} · repaid in all{" "}
              <span className="tabular">{rupees(preview.total_payable)}</span>
            </p>
            <div className="max-h-56 overflow-y-auto rounded-lg border">
              <ScheduleTable schedule={preview.schedule} />
            </div>
          </div>
        )}
        {error && (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        )}
      </form>
    </Modal>
  );
}

function LoanDialog({ loanId, onClose, onChanged }: { loanId: string; onClose: () => void; onChanged: () => void }) {
  const accounts = useBankAccounts();
  const [l, setL] = useState<EmployeeLoan | null>(null);
  const [mode, setMode] = useState<"repay" | "foreclose" | null>(null);
  const [amount, setAmount] = useState("");
  const [method, setMethod] = useState("cash");
  const [bank, setBank] = useState<string | null>(null);
  const bankAccount = bank ?? defaultAccount(accounts)?.bank_account_id ?? "";
  const [paidOn, setPaidOn] = useState(localDate());
  const [reference, setReference] = useState("");
  const [waive, setWaive] = useState(true);
  const [busy, setBusy] = useState(false);

  const load = useCallback(() => {
    api
      .get<EmployeeLoan>(`/api/v1/employee-loans/${loanId}`)
      .then(setL)
      .catch((e) => toast.error(message(e, "Could not load the loan")));
  }, [loanId]);
  useEffect(load, [load]);

  async function submit() {
    if (!l || !mode) return;
    setBusy(true);
    try {
      const body = {
        method,
        paid_on: paidOn,
        reference_no: reference,
        ...(method !== "cash" && bankAccount ? { bank_account_id: bankAccount } : {}),
        ...(mode === "repay" ? { amount: Number(amount) } : { waive_interest: waive }),
      };
      const next = await api.post<EmployeeLoan>(`/api/v1/employee-loans/${l.loan_id}/${mode === "repay" ? "repayments" : "foreclose"}`, body);
      setL(next);
      setMode(null);
      setAmount("");
      toast.success(mode === "repay" ? "Repayment recorded" : "Loan foreclosed");
      onChanged();
    } catch (e) {
      toast.error(message(e, "Could not record it"));
    } finally {
      setBusy(false);
    }
  }

  const left = l ? Number(l.outstanding_principal) + Number(l.outstanding_interest) : 0;

  return (
    <Modal open onOpenChange={(o) => !o && onClose()} title={l ? `${l.loan_number} · ${l.employee_name}` : "Loan"} width={760} footer={<Button onClick={onClose}>Done</Button>}>
      {!l ? (
        <p className="text-sm text-muted-foreground">Loading…</p>
      ) : (
        <div className="space-y-4">
          <div className="grid gap-2 text-sm sm:grid-cols-4">
            <div>
              <span className="block text-xs text-muted-foreground">Amount</span>
              <span className="tabular">{rupees(l.principal)}</span>
            </div>
            <div>
              <span className="block text-xs text-muted-foreground">EMI</span>
              <span className="tabular">{rupees(l.emi_amount)}</span>
            </div>
            <div>
              <span className="block text-xs text-muted-foreground">Repaid</span>
              <span className="tabular">{rupees(l.recovered)}</span>
            </div>
            <div>
              <span className="block text-xs text-muted-foreground">Left</span>
              <span className="tabular font-semibold">{rupees(left)}</span>
            </div>
          </div>
          <p className="text-xs text-muted-foreground">
            {l.loan_type === "advance" ? "Salary advance" : `${Number(l.interest_rate)}% a year, flat`} · paid out {l.disbursed_on} by {METHOD_LABEL[l.disbursement_method]}
            {l.purpose && ` · ${l.purpose}`} · <StatusChip status={l.status} tone={STATUS_TONE[l.status]} />
          </p>
          <div className="max-h-64 overflow-y-auto rounded-lg border">
            <ScheduleTable schedule={l.schedule ?? []} />
          </div>
          {(l.repayments ?? []).length > 0 && (
            <div>
              <h3 className="mb-1 text-sm font-semibold">Repayments</h3>
              <ul className="space-y-1 text-sm">
                {(l.repayments ?? []).map((r) => (
                  <li key={r.repayment_id} className="flex justify-between gap-2">
                    <span>
                      {r.paid_on} · {METHOD_LABEL[r.method] ?? r.method}
                      {r.foreclosure && " · foreclosure"}
                      {r.reference_no && ` · ${r.reference_no}`}
                    </span>
                    <span className="tabular">
                      {rupees(r.amount)}
                      {Number(r.interest_part) > 0 && <span className="text-xs text-muted-foreground"> (interest {rupees(r.interest_part)})</span>}
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          )}
          {l.status === "active" && !mode && (
            <div className="flex gap-2">
              <Button variant="outline" onClick={() => setMode("repay")}>
                Record a repayment
              </Button>
              <Button variant="outline" onClick={() => setMode("foreclose")}>
                Foreclose
              </Button>
            </div>
          )}
          {mode && (
            <div className="grid gap-3 rounded-lg border p-3 sm:grid-cols-4 sm:items-end">
              {mode === "repay" ? (
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="lr-amount">Amount</Label>
                  <Input id="lr-amount" type="number" min="0.01" max={left} step="0.01" value={amount} onChange={(e) => setAmount(e.target.value)} />
                </div>
              ) : (
                <label className="flex items-center gap-2 text-sm sm:col-span-1">
                  <input type="checkbox" checked={waive} onChange={(e) => setWaive(e.target.checked)} /> Waive the interest left
                </label>
              )}
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="lr-method">By</Label>
                <select id="lr-method" value={method} onChange={(e) => setMethod(e.target.value)} className={selectClass}>
                  <option value="cash">Cash</option>
                  <option value="bank_transfer">Bank transfer</option>
                  <option value="cheque">Cheque</option>
                </select>
              </div>
              {method !== "cash" && (
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="lr-bank">Into account</Label>
                  <BankAccountSelect id="lr-bank" accounts={accounts} value={bankAccount} onChange={setBank} allowNone className="w-full" />
                </div>
              )}
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="lr-on">On</Label>
                <Input id="lr-on" type="date" value={paidOn} max={localDate()} onChange={(e) => setPaidOn(e.target.value)} />
              </div>
              <div className="flex flex-col gap-1.5 sm:col-span-2">
                <Label htmlFor="lr-ref">Reference</Label>
                <Input id="lr-ref" value={reference} onChange={(e) => setReference(e.target.value)} />
              </div>
              <div className="flex gap-2 sm:col-span-2 sm:justify-end">
                <Button variant="ghost" onClick={() => setMode(null)}>
                  Back
                </Button>
                <Button disabled={busy || (mode === "repay" && !(Number(amount) > 0))} onClick={submit}>
                  {busy && <Loader2 className="animate-spin" aria-hidden />}
                  {mode === "repay" ? "Record repayment" : `Foreclose (${rupees(waive ? Number(l.outstanding_principal) : left)})`}
                </Button>
              </div>
            </div>
          )}
        </div>
      )}
    </Modal>
  );
}
