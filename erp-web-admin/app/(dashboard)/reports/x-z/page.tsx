"use client";

// X and Z reports (erp-core-go internal/sales shifts.go and day_report.go,
// phase0_1_design.md §3.60). The X report is one till's shift: live while
// the shift is open, final once it's closed with its counted drawer. The Z
// report is one branch's whole business day, every till and shift; it stays
// live until the day is closed (reports.day_close), which freezes it with
// the branch's next Z number, so a reprint is always the same document.
// Filter, open, and print either (Print gives paper or a PDF; the till
// prints the same reports on its receipt printer).
import { useCallback, useEffect, useState } from "react";
import { Lock, Printer, RefreshCw } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Branch } from "@/lib/types";
import { METHOD_LABEL, dateTime, rupees } from "@/lib/format";
import { usePermissions, missingPermissionHint } from "@/lib/permissions";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Modal } from "@/components/ui/dialog";
import { PageHeader } from "@/components/data/page-header";
import { StatusChip } from "@/components/data/status-chip";

type ShiftRow = {
  shift_id: string;
  shift_number: number;
  status: "open" | "closed";
  branch_id: string;
  branch_name: string;
  pos_terminal_id: string;
  terminal_name: string;
  opening_float: string;
  opened_by: string;
  opened_at: string;
  closed_by: string | null;
  closed_at: string | null;
  variance: string | null;
  order_count: number | null;
  net_sales: string | null;
};
type Movement = { movement_id: string; kind: string; amount: string; reason: string; performed_by: string; approved_by: string | null; created_at: string };
type ShiftReport = {
  kind: "X" | "Z";
  generated_at: string;
  order_count: number;
  voided_count: number;
  net_sales: string;
  /** Checkout round-off waivers in the shift (signed sum); absent before migrations/059. */
  round_off_total?: string;
  by_payment_method: Record<string, string>;
  return_count: number;
  refund_total: string;
  refunds_by_method: Record<string, string>;
  cash: { opening_float: string; cash_sales: string; cash_refunds: string; float_in: string; drops: string; paid_in: string; paid_out: string; expected: string };
  movements: Movement[];
  close?: { counted_total: string; variance: string; reason: string; approved_by: string | null };
};
type Shift = ShiftRow & { report?: ShiftReport };
type DayShift = {
  shift_id: string;
  shift_number: number;
  terminal_name: string;
  status: string;
  opened_by: string;
  opened_at: string;
  closed_at: string | null;
  order_count: number;
  net_sales: string;
  expected_cash: string;
  counted_cash: string | null;
  variance: string | null;
};
type DayReport = {
  branch_id: string;
  branch_name: string;
  date: string;
  status: "open" | "closed";
  z_number: number | null;
  closed_by: string | null;
  closed_at: string | null;
  generated_at: string;
  first_order_number: string | null;
  last_order_number: string | null;
  order_count: number;
  subtotal: string;
  discount_total: string;
  tax_total: string;
  /** The day's checkout round-off waivers (signed sum); absent on a day frozen before migrations/059. */
  round_off_total?: string;
  /** Tax by head and the branch's regime (migrations/060); absent on a day frozen before it. */
  tax_regime?: "gst_split" | "single";
  tax_label?: string;
  cgst_total?: string;
  sgst_total?: string;
  igst_total?: string;
  cess_total?: string;
  net_sales: string;
  credit_sales: string;
  average_bill: string;
  voided_count: number;
  voided_total: string;
  released_count: number;
  by_payment_method: Record<string, string>;
  by_channel: Record<string, string>;
  return_count: number;
  refund_total: string;
  refunds_by_method: Record<string, string>;
  tax_rates: { rate: string; taxable_value: string; tax: string }[];
  cashiers: { cashier: string; orders: number; net_sales: string }[];
  cash: { cash_sales: string; cash_refunds: string; float_in: string; drops: string; paid_in: string; paid_out: string; net_cash: string; counted: string; variance: string };
  shifts: DayShift[];
  open_shifts: number;
  changed_since_close?: { order_count: number; net_sales: string; refund_total: string };
};
type ZRow = { z_number: number; branch_id: string; branch_name: string; date: string; order_count: number; net_sales: string; refund_total: string; cash_variance: string; closed_by: string; closed_at: string };
type Terminal = { terminal_id: string; name: string; branch_id: string };

const selectClass = "h-9 rounded-lg border bg-card px-2 text-sm";
const istToday = () => new Date(Date.now() + 5.5 * 3600e3).toISOString().slice(0, 10);
const daysAgo = (n: number) => new Date(Date.now() + 5.5 * 3600e3 - n * 864e5).toISOString().slice(0, 10);
const CHANNEL: Record<string, string> = { pos: "Till", backoffice: "Back office", online: "Online" };
const MOVE: Record<string, string> = { float_in: "Float in", drop: "Cash drop", paid_in: "Paid in", paid_out: "Paid out" };
const method = (k: string) => METHOD_LABEL[k] ?? k.replace(/_/g, " ").replace(/^./, (c) => c.toUpperCase());

export default function XZReportsPage() {
  const [tab, setTab] = useState<"x" | "z">("x");
  const [branches, setBranches] = useState<Branch[]>([]);
  useEffect(() => {
    api.get<{ branches: Branch[] }>("/api/v1/branches?scope=mine").then((d) => setBranches(d.branches)).catch(() => {});
  }, []);
  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="X & Z reports"
        description="X: one till's shift, live while it's open. Z: a branch's whole business day, every till; closing the day freezes it with its Z number."
      />
      <div role="tablist" aria-label="Report" className="flex w-fit gap-1 rounded-lg border bg-card p-1 print:hidden">
        {(
          [
            ["x", "X report · by shift"],
            ["z", "Z report · by day"],
          ] as const
        ).map(([k, l]) => (
          <button
            key={k}
            role="tab"
            aria-selected={tab === k}
            onClick={() => setTab(k)}
            className={`rounded-md px-3 py-1.5 text-sm ${tab === k ? "bg-primary text-primary-foreground" : "hover:bg-muted"}`}
          >
            {l}
          </button>
        ))}
      </div>
      {tab === "x" ? <XTab branches={branches} /> : <ZTab branches={branches} />}
    </div>
  );
}

// ------------------------------------------------------------------ X

function XTab({ branches }: { branches: Branch[] }) {
  const [terminals, setTerminals] = useState<Terminal[]>([]);
  const [branchId, setBranchId] = useState("");
  const [terminalId, setTerminalId] = useState("");
  const [status, setStatus] = useState("");
  const [from, setFrom] = useState(daysAgo(6));
  const [to, setTo] = useState(istToday());
  const [page, setPage] = useState(1);
  const [list, setList] = useState<{ shifts: ShiftRow[]; total: number; limit: number } | null>(null);
  const [open, setOpen] = useState<Shift | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api.get<{ terminals: Terminal[] }>("/api/v1/pos-terminals").then((d) => setTerminals(d.terminals)).catch(() => {});
  }, []);
  const load = useCallback(() => {
    const qs = new URLSearchParams({ branch_id: branchId, terminal_id: terminalId, status, from, to, page: String(page), limit: "25" });
    api
      .get<{ shifts: ShiftRow[]; total: number; limit: number }>(`/api/v1/pos-shifts?${qs}`)
      .then((d) => {
        setList(d);
        setError(null);
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load shifts"));
  }, [branchId, terminalId, status, from, to, page]);
  useEffect(load, [load]);

  const show = (id: string) =>
    api
      .get<Shift>(`/api/v1/pos-shifts/${id}`)
      .then(setOpen)
      .catch((e) => toast.error(e instanceof ApiError ? e.message : "Could not load the shift"));

  if (open) return <ShiftView shift={open} onBack={() => setOpen(null)} onRefresh={() => show(open.shift_id)} />;

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-end gap-3 rounded-xl border bg-card p-3" role="group" aria-label="Shift filters">
        <Field label="Branch" id="x-branch">
          <select
            id="x-branch"
            value={branchId}
            onChange={(e) => {
              setBranchId(e.target.value);
              setTerminalId("");
              setPage(1);
            }}
            className={`${selectClass} w-44`}
          >
            <option value="">All my branches</option>
            {branches.map((b) => (
              <option key={b.branch_id} value={b.branch_id}>
                {b.name}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Till" id="x-till">
          <select id="x-till" value={terminalId} onChange={(e) => (setTerminalId(e.target.value), setPage(1))} className={`${selectClass} w-40`}>
            <option value="">All tills</option>
            {terminals
              .filter((t) => !branchId || t.branch_id === branchId)
              .map((t) => (
                <option key={t.terminal_id} value={t.terminal_id}>
                  {t.name}
                </option>
              ))}
          </select>
        </Field>
        <Field label="Status" id="x-status">
          <select id="x-status" value={status} onChange={(e) => (setStatus(e.target.value), setPage(1))} className={`${selectClass} w-32`}>
            <option value="">Open and closed</option>
            <option value="open">Open</option>
            <option value="closed">Closed</option>
          </select>
        </Field>
        <Field label="Opened from" id="x-from">
          <Input id="x-from" type="date" value={from} max={to} onChange={(e) => (setFrom(e.target.value), setPage(1))} className="w-40" />
        </Field>
        <Field label="To" id="x-to">
          <Input id="x-to" type="date" value={to} min={from} onChange={(e) => (setTo(e.target.value), setPage(1))} className="w-40" />
        </Field>
        <Button variant="outline" size="sm" onClick={load}>
          <RefreshCw /> Refresh
        </Button>
      </div>
      {error && (
        <p className="text-sm text-destructive" role="alert">
          {error}
        </p>
      )}
      <div className="overflow-x-auto rounded-xl border bg-card">
        <table className="w-full min-w-[820px] text-sm">
          <thead className="border-b text-left text-xs text-muted-foreground">
            <tr>
              <th className="px-3 py-2 font-medium">Shift</th>
              <th className="px-3 py-2 font-medium">Branch</th>
              <th className="px-3 py-2 font-medium">Opened</th>
              <th className="px-3 py-2 font-medium">Closed</th>
              <th className="px-3 py-2 text-right font-medium">Bills</th>
              <th className="px-3 py-2 text-right font-medium">Net sales</th>
              <th className="px-3 py-2 text-right font-medium">Variance</th>
              <th className="px-3 py-2 font-medium">Status</th>
            </tr>
          </thead>
          <tbody>
            {list?.shifts.map((s) => (
              <tr key={s.shift_id} className="cursor-pointer border-b last:border-0 hover:bg-muted/50" onClick={() => show(s.shift_id)}>
                <td className="px-3 py-2">
                  <button type="button" className="font-medium hover:underline" onClick={(e) => (e.stopPropagation(), show(s.shift_id))}>
                    {s.terminal_name} · shift {s.shift_number}
                  </button>
                </td>
                <td className="px-3 py-2 text-muted-foreground">{s.branch_name}</td>
                <td className="px-3 py-2">
                  {dateTime(s.opened_at)}
                  <span className="block text-xs text-muted-foreground">{s.opened_by}</span>
                </td>
                <td className="px-3 py-2">
                  {s.closed_at ? dateTime(s.closed_at) : "—"}
                  {s.closed_by && <span className="block text-xs text-muted-foreground">{s.closed_by}</span>}
                </td>
                <td className="tabular px-3 py-2 text-right">{s.order_count ?? "—"}</td>
                <td className="tabular px-3 py-2 text-right">{s.net_sales ? rupees(s.net_sales) : <span className="text-xs text-muted-foreground">open it</span>}</td>
                <td className={`tabular px-3 py-2 text-right ${s.variance && Number(s.variance) !== 0 ? "text-destructive" : ""}`}>{s.variance ? rupees(s.variance) : "—"}</td>
                <td className="px-3 py-2">
                  <StatusChip status={s.status === "open" ? "active" : "inactive"} label={s.status === "open" ? "Open" : "Closed"} />
                </td>
              </tr>
            ))}
            {list && list.shifts.length === 0 && (
              <tr>
                <td colSpan={8} className="px-3 py-8 text-center text-muted-foreground">
                  No shifts opened in these dates.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
      {list && list.total > list.limit && (
        <div className="flex items-center gap-2 text-sm">
          <Button variant="outline" size="sm" disabled={page === 1} onClick={() => setPage(page - 1)}>
            Previous
          </Button>
          <span>
            Page {page} of {Math.ceil(list.total / list.limit)}
          </span>
          <Button variant="outline" size="sm" disabled={page * list.limit >= list.total} onClick={() => setPage(page + 1)}>
            Next
          </Button>
        </div>
      )}
    </div>
  );
}

function ShiftView({ shift, onBack, onRefresh }: { shift: Shift; onBack: () => void; onRefresh: () => void }) {
  const r = shift.report;
  const live = shift.status === "open";
  return (
    <div className="flex max-w-3xl flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2 print:hidden">
        <Button variant="outline" size="sm" onClick={onBack}>
          ← All shifts
        </Button>
        {live && (
          <Button variant="outline" size="sm" onClick={onRefresh}>
            <RefreshCw /> Refresh
          </Button>
        )}
        <Button size="sm" onClick={() => window.print()}>
          <Printer /> Print X report
        </Button>
      </div>
      <article className="rounded-xl border bg-card p-5 print:border-0 print:p-0" data-testid="x-report">
        <header className="mb-4 border-b pb-3 text-center">
          <p className="text-xs uppercase tracking-widest text-muted-foreground">{shift.branch_name ?? ""}</p>
          <h2 className="text-xl font-semibold">X report</h2>
          <p className="text-sm">
            {shift.terminal_name} · shift {shift.shift_number}
          </p>
          <p className="text-xs text-muted-foreground">
            Opened {dateTime(shift.opened_at)} by {shift.opened_by}
            {shift.closed_at ? ` · closed ${dateTime(shift.closed_at)} by ${shift.closed_by}` : ""}
          </p>
          <p className={`mt-1 text-xs font-medium ${live ? "text-warn" : "text-pay"}`}>{live ? `Shift open: figures so far (as of ${dateTime(r?.generated_at)})` : "Shift closed: final figures"}</p>
        </header>
        {r && (
          <div className="grid gap-5 sm:grid-cols-2">
            <Section title="Sales">
              <Row label="Bills" value={String(r.order_count)} />
              {r.voided_count > 0 && <Row label="Voided" value={String(r.voided_count)} />}
              {Number(r.round_off_total ?? 0) !== 0 && <Row label="Round off" value={rupees(r.round_off_total!)} />}
              <Row label="Net sales" value={rupees(r.net_sales)} strong />
            </Section>
            <Section title="Tenders">
              {Object.entries(r.by_payment_method).map(([k, v]) => (
                <Row key={k} label={method(k)} value={rupees(v)} />
              ))}
              {Object.keys(r.by_payment_method).length === 0 && <Row label="No payments yet" value="" />}
            </Section>
            <Section title="Returns">
              <Row label="Returns" value={String(r.return_count)} />
              <Row label="Refunded" value={rupees(r.refund_total)} />
              {Object.entries(r.refunds_by_method).map(([k, v]) => (
                <Row key={k} label={`· ${method(k)}`} value={rupees(v)} />
              ))}
            </Section>
            <Section title="Drawer cash">
              <Row label="Opening float" value={rupees(r.cash.opening_float)} />
              <Row label="Cash sales" value={rupees(r.cash.cash_sales)} />
              <Row label="Cash refunds" value={`−${rupees(r.cash.cash_refunds)}`} />
              <Row label="Float in" value={rupees(r.cash.float_in)} />
              <Row label="Drops" value={`−${rupees(r.cash.drops)}`} />
              <Row label="Paid in" value={rupees(r.cash.paid_in)} />
              <Row label="Paid out" value={`−${rupees(r.cash.paid_out)}`} />
              <Row label="Expected in drawer" value={rupees(r.cash.expected)} strong />
              {r.close && (
                <>
                  <Row label="Counted" value={rupees(r.close.counted_total)} />
                  <Row label="Variance" value={rupees(r.close.variance)} strong />
                  {r.close.reason && <Row label={`Reason: ${r.close.reason}`} value="" />}
                </>
              )}
            </Section>
            {r.movements.length > 0 && (
              <Section title="Cash movements" className="sm:col-span-2">
                {r.movements.map((m) => (
                  <Row key={m.movement_id} label={`${MOVE[m.kind] ?? m.kind} · ${dateTime(m.created_at)} · ${m.performed_by}${m.reason ? ` · ${m.reason}` : ""}`} value={rupees(m.amount)} />
                ))}
              </Section>
            )}
          </div>
        )}
      </article>
    </div>
  );
}

// ------------------------------------------------------------------ Z

function ZTab({ branches }: { branches: Branch[] }) {
  const { can } = usePermissions();
  const [branchId, setBranchId] = useState("");
  const [date, setDate] = useState(istToday());
  const [rep, setRep] = useState<DayReport | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [closing, setClosing] = useState(false);
  const [busy, setBusy] = useState(false);
  const [hist, setHist] = useState<ZRow[]>([]);
  const [hFrom, setHFrom] = useState(daysAgo(30));
  const [hTo, setHTo] = useState(istToday());
  const branch = branchId || branches[0]?.branch_id || "";

  const load = useCallback(() => {
    if (!branch) return;
    api
      .get<DayReport>(`/api/v1/reports/z-report?branch_id=${branch}&date=${date}`)
      .then((d) => {
        setRep(d);
        setError(null);
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not build the Z report"));
  }, [branch, date]);
  useEffect(load, [load]);
  const loadHist = useCallback(() => {
    api
      .get<{ z_reports: ZRow[] }>(`/api/v1/reports/z-reports?${new URLSearchParams({ branch_id: branchId, from: hFrom, to: hTo })}`)
      .then((d) => setHist(d.z_reports))
      .catch(() => {});
  }, [branchId, hFrom, hTo]);
  useEffect(loadHist, [loadHist]);

  async function closeDay() {
    setBusy(true);
    try {
      const d = await api.post<{ report: DayReport; replayed: boolean }>("/api/v1/reports/z-report/close", { branch_id: branch, date });
      setRep(d.report);
      setClosing(false);
      toast.success(`Day closed: Z no. ${d.report.z_number}`);
      loadHist();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not close the day");
    } finally {
      setBusy(false);
    }
  }

  const ended = date < istToday();
  const canClose = can("reports.day_close");
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-end gap-3 rounded-xl border bg-card p-3 print:hidden" role="group" aria-label="Z report filters">
        <Field label="Branch" id="z-branch">
          <select id="z-branch" value={branch} onChange={(e) => setBranchId(e.target.value)} className={`${selectClass} w-48`}>
            {branches.map((b) => (
              <option key={b.branch_id} value={b.branch_id}>
                {b.name}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Business day" id="z-date">
          <Input id="z-date" type="date" value={date} max={istToday()} onChange={(e) => setDate(e.target.value)} className="w-40" />
        </Field>
        <Button variant="outline" size="sm" onClick={load}>
          <RefreshCw /> Refresh
        </Button>
        <Button size="sm" onClick={() => window.print()} disabled={!rep}>
          <Printer /> Print Z report
        </Button>
        {rep && rep.status === "open" && (
          <Button
            size="sm"
            variant="outline"
            disabled={!canClose || !ended}
            title={!canClose ? missingPermissionHint("reports.day_close") : !ended ? "A day can be closed once it has ended" : undefined}
            onClick={() => setClosing(true)}
          >
            <Lock /> Close day
          </Button>
        )}
      </div>
      {error && (
        <p className="text-sm text-destructive" role="alert">
          {error}
        </p>
      )}
      {rep && <DayView r={rep} />}

      <section className="rounded-xl border bg-card p-4 print:hidden" aria-labelledby="zh-h">
        <div className="mb-3 flex flex-wrap items-end justify-between gap-3">
          <h2 id="zh-h" className="text-sm font-semibold">
            Closed days
          </h2>
          <div className="flex flex-wrap items-end gap-2">
            <Field label="From" id="zh-from">
              <Input id="zh-from" type="date" value={hFrom} onChange={(e) => setHFrom(e.target.value)} className="w-40" />
            </Field>
            <Field label="To" id="zh-to">
              <Input id="zh-to" type="date" value={hTo} onChange={(e) => setHTo(e.target.value)} className="w-40" />
            </Field>
          </div>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full min-w-[640px] text-sm">
            <thead className="text-left text-xs text-muted-foreground">
              <tr>
                <th className="pb-1 font-medium">Z no.</th>
                <th className="pb-1 font-medium">Day</th>
                <th className="pb-1 font-medium">Branch</th>
                <th className="pb-1 text-right font-medium">Bills</th>
                <th className="pb-1 text-right font-medium">Net sales</th>
                <th className="pb-1 text-right font-medium">Refunds</th>
                <th className="pb-1 text-right font-medium">Cash variance</th>
                <th className="pb-1 font-medium">Closed by</th>
              </tr>
            </thead>
            <tbody>
              {hist.map((z) => (
                <tr
                  key={`${z.branch_id}-${z.z_number}`}
                  className="cursor-pointer border-t hover:bg-muted/50"
                  onClick={() => {
                    setBranchId(z.branch_id);
                    setDate(z.date);
                    window.scrollTo({ top: 0, behavior: "smooth" });
                  }}
                >
                  <td className="py-1.5 font-medium">{z.z_number}</td>
                  <td className="py-1.5">{z.date}</td>
                  <td className="py-1.5 text-muted-foreground">{z.branch_name}</td>
                  <td className="tabular py-1.5 text-right">{z.order_count}</td>
                  <td className="tabular py-1.5 text-right">{rupees(z.net_sales)}</td>
                  <td className="tabular py-1.5 text-right">{rupees(z.refund_total)}</td>
                  <td className={`tabular py-1.5 text-right ${Number(z.cash_variance) !== 0 ? "text-destructive" : ""}`}>{rupees(z.cash_variance)}</td>
                  <td className="py-1.5">
                    {z.closed_by}
                    <span className="block text-xs text-muted-foreground">{dateTime(z.closed_at)}</span>
                  </td>
                </tr>
              ))}
              {hist.length === 0 && (
                <tr>
                  <td colSpan={8} className="py-6 text-center text-muted-foreground">
                    No days closed in these dates.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>

      {closing && rep && (
        <Modal
          open
          onOpenChange={(o) => !o && setClosing(false)}
          title={`Close ${rep.branch_name} for ${rep.date}?`}
          description="The Z report is frozen with the branch's next Z number. Sales that sync for this day afterwards are shown beside it as a difference; the frozen figures don't change."
          footer={
            <>
              <Button variant="outline" onClick={() => setClosing(false)}>
                Cancel
              </Button>
              <Button disabled={busy} onClick={closeDay}>
                <Lock /> Close day
              </Button>
            </>
          }
        >
          <p className="text-sm">
            {rep.order_count} bills · {rupees(rep.net_sales)} net sales · cash variance {rupees(rep.cash.variance)}
            {rep.open_shifts > 0 && <span className="mt-2 block text-destructive">{rep.open_shifts} shift(s) are still open: close them on their tills first.</span>}
          </p>
        </Modal>
      )}
    </div>
  );
}

function DayView({ r }: { r: DayReport }) {
  return (
    <article className="max-w-4xl rounded-xl border bg-card p-5 print:max-w-none print:border-0 print:p-0" data-testid="z-report">
      <header className="mb-4 border-b pb-3 text-center">
        <p className="text-xs uppercase tracking-widest text-muted-foreground">{r.branch_name}</p>
        <h2 className="text-xl font-semibold">Z report · {r.date}</h2>
        {r.status === "closed" ? (
          <p className="text-sm font-medium text-pay" data-testid="z-status">
            Z no. {r.z_number} · closed by {r.closed_by} on {dateTime(r.closed_at)}
          </p>
        ) : (
          <p className="text-sm font-medium text-warn" data-testid="z-status">
            Day not closed: figures so far (as of {dateTime(r.generated_at)})
          </p>
        )}
        {r.first_order_number && (
          <p className="text-xs text-muted-foreground">
            Bills {r.first_order_number} to {r.last_order_number}
          </p>
        )}
      </header>
      {r.changed_since_close && (
        <p className="mb-4 rounded-lg bg-warn-soft px-3 py-2 text-sm text-warn">
          Changed since the day was closed: {r.changed_since_close.order_count >= 0 ? "+" : ""}
          {r.changed_since_close.order_count} bills, {rupees(r.changed_since_close.net_sales)} net sales, {rupees(r.changed_since_close.refund_total)} refunds (a sale
          synced late, or a later void). The figures below are as closed.
        </p>
      )}
      {r.open_shifts > 0 && (
        <p className="mb-4 rounded-lg bg-warn-soft px-3 py-2 text-sm text-warn print:hidden">
          {r.open_shifts} shift(s) still open, so cash counts are still to come.
        </p>
      )}
      <div className="grid gap-5 sm:grid-cols-2 lg:grid-cols-3">
        <Section title="Sales">
          <Row label="Bills" value={String(r.order_count)} />
          <Row label="Subtotal" value={rupees(r.subtotal)} />
          <Row label="Discounts" value={`−${rupees(r.discount_total)}`} />
          <Row label="Tax" value={rupees(r.tax_total)} />
          {r.tax_regime === "gst_split" &&
            (
              [
                ["CGST", r.cgst_total],
                ["SGST", r.sgst_total],
                ["IGST", r.igst_total],
                ["Cess", r.cess_total],
              ] as const
            )
              .filter(([, v]) => Number(v ?? 0) !== 0)
              .map(([k, v]) => <Row key={k} label={`· ${k}`} value={rupees(v!)} />)}
          {Number(r.round_off_total ?? 0) !== 0 && <Row label="Round off" value={rupees(r.round_off_total!)} />}
          <Row label="Net sales" value={rupees(r.net_sales)} strong />
          <Row label="Average bill" value={rupees(r.average_bill)} />
          {Number(r.credit_sales) > 0 && <Row label="On credit" value={rupees(r.credit_sales)} />}
        </Section>
        <Section title="Tenders">
          {Object.entries(r.by_payment_method).map(([k, v]) => (
            <Row key={k} label={method(k)} value={rupees(v)} />
          ))}
          {Object.keys(r.by_payment_method).length === 0 && <Row label="No payments" value="" />}
          <p className="pt-2 text-xs font-medium text-muted-foreground">Channels</p>
          {Object.entries(r.by_channel).map(([k, v]) => (
            <Row key={k} label={CHANNEL[k] ?? k} value={rupees(v)} />
          ))}
        </Section>
        <Section title="Tax by rate">
          {r.tax_rates.map((t) => (
            <Row key={t.rate} label={`${r.tax_label ?? "GST"} ${t.rate}% on ${rupees(t.taxable_value)}`} value={rupees(t.tax)} />
          ))}
          {r.tax_rates.length === 0 && <Row label="No taxable sales" value="" />}
        </Section>
        <Section title="Returns & voids">
          <Row label="Returns" value={String(r.return_count)} />
          <Row label="Refunded" value={rupees(r.refund_total)} />
          {Object.entries(r.refunds_by_method).map(([k, v]) => (
            <Row key={k} label={`· ${method(k)}`} value={rupees(v)} />
          ))}
          <Row label="Voided sales" value={`${r.voided_count} (${rupees(r.voided_total)})`} />
          <Row label="Released carts" value={String(r.released_count)} />
        </Section>
        <Section title="Cash">
          <Row label="Cash sales" value={rupees(r.cash.cash_sales)} />
          <Row label="Cash refunds" value={`−${rupees(r.cash.cash_refunds)}`} />
          <Row label="Float in" value={rupees(r.cash.float_in)} />
          <Row label="Drops" value={`−${rupees(r.cash.drops)}`} />
          <Row label="Paid in" value={rupees(r.cash.paid_in)} />
          <Row label="Paid out" value={`−${rupees(r.cash.paid_out)}`} />
          <Row label="Net cash" value={rupees(r.cash.net_cash)} strong />
          <Row label="Counted (closed shifts)" value={rupees(r.cash.counted)} />
          <Row label="Variance" value={rupees(r.cash.variance)} strong />
        </Section>
        <Section title="Cashiers">
          {r.cashiers.map((c) => (
            <Row key={c.cashier} label={`${c.cashier} (${c.orders})`} value={rupees(c.net_sales)} />
          ))}
          {r.cashiers.length === 0 && <Row label="No sales" value="" />}
        </Section>
      </div>
      <Section title="Shifts" className="mt-5">
        <table className="w-full text-sm">
          <thead className="text-left text-xs text-muted-foreground">
            <tr>
              <th className="pb-1 font-medium">Till</th>
              <th className="pb-1 font-medium">Opened</th>
              <th className="pb-1 font-medium">Closed</th>
              <th className="pb-1 text-right font-medium">Bills</th>
              <th className="pb-1 text-right font-medium">Net sales</th>
              <th className="pb-1 text-right font-medium">Expected cash</th>
              <th className="pb-1 text-right font-medium">Counted</th>
              <th className="pb-1 text-right font-medium">Variance</th>
            </tr>
          </thead>
          <tbody>
            {r.shifts.map((s) => (
              <tr key={s.shift_id} className="border-t">
                <td className="py-1">
                  {s.terminal_name} #{s.shift_number}
                </td>
                <td className="py-1 text-xs">
                  {dateTime(s.opened_at)} · {s.opened_by}
                </td>
                <td className="py-1 text-xs">{s.closed_at ? dateTime(s.closed_at) : <span className="font-medium text-warn">Open</span>}</td>
                <td className="tabular py-1 text-right">{s.order_count}</td>
                <td className="tabular py-1 text-right">{rupees(s.net_sales)}</td>
                <td className="tabular py-1 text-right">{rupees(s.expected_cash)}</td>
                <td className="tabular py-1 text-right">{s.counted_cash ? rupees(s.counted_cash) : "—"}</td>
                <td className="tabular py-1 text-right">{s.variance ? rupees(s.variance) : "—"}</td>
              </tr>
            ))}
            {r.shifts.length === 0 && (
              <tr>
                <td colSpan={8} className="py-3 text-muted-foreground">
                  No till shifts this day (back-office and online sales need none).
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </Section>
    </article>
  );
}

function Section({ title, children, className = "" }: { title: string; children: React.ReactNode; className?: string }) {
  return (
    <section className={`break-inside-avoid ${className}`}>
      <h3 className="mb-1.5 border-b pb-1 text-xs font-semibold uppercase tracking-wide text-muted-foreground">{title}</h3>
      <div className="flex flex-col gap-0.5">{children}</div>
    </section>
  );
}

function Row({ label, value, strong }: { label: string; value: string; strong?: boolean }) {
  return (
    <div className={`flex items-baseline justify-between gap-3 text-sm ${strong ? "font-semibold" : ""}`}>
      <span className="min-w-0">{label}</span>
      <span className="tabular shrink-0">{value}</span>
    </div>
  );
}

function Field({ label, id, children }: { label: string; id: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1">
      <Label htmlFor={id}>{label}</Label>
      {children}
    </div>
  );
}
