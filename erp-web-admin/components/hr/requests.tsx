"use client";

// Leave, permission and attendance regularization (erp-core-go §3.78,
// migrations/075): the dialogs an employee (or HR, for an employee) asks
// with, the balance card, and the request table both My Leave & Payslips
// and HR's Leave & Requests use. Approving is hr.manage, never your own.
import { FormEvent, useCallback, useEffect, useState } from "react";
import { Loader2 } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import type { AttendanceRecord, LeaveBalances, LeaveRequest, LeaveType, PermissionRequest, Regularization, RequestStatus } from "@/lib/types";
import { localDate, merchantStamp, merchantTime, shortDate } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Modal } from "@/components/ui/dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Field, selectClass } from "@/components/data/panel";
import { StatusChip, Tone } from "@/components/data/status-chip";

export const message = (e: unknown, fallback: string) => (e instanceof ApiError ? e.message : fallback);

export const REQUEST_TONE: Record<RequestStatus, Tone> = { pending: "warn", approved: "ok", rejected: "bad", cancelled: "muted" };
export const REQUEST_LABEL: Record<RequestStatus, string> = { pending: "Waiting", approved: "Approved", rejected: "Rejected", cancelled: "Cancelled" };

export type RequestKind = "leave" | "permission" | "regularization";
export const KIND_LABEL: Record<RequestKind, string> = { leave: "Leave", permission: "Permission", regularization: "Attendance correction" };
export const KIND_PATH: Record<RequestKind, string> = {
  leave: "/api/v1/hr/leave-requests",
  permission: "/api/v1/hr/permission-requests",
  regularization: "/api/v1/hr/regularizations",
};

/** One row of any kind, as the tables show it. */
export type AnyRequest = { kind: RequestKind; id: string; what: string; when: string; outcome: string; r: LeaveRequest | PermissionRequest | Regularization };

export function toRows(leave: LeaveRequest[], perm: PermissionRequest[], reg: Regularization[]): AnyRequest[] {
  const days = (n: number) => `${n} day${n === 1 ? "" : "s"}`;
  return [
    ...leave.map((l) => ({
      kind: "leave" as const,
      id: l.leave_request_id,
      what: `${l.leave_type}${l.half_day ? " (half day)" : ""}`,
      when: l.from_date === l.to_date ? shortDate(l.from_date) : `${shortDate(l.from_date)} – ${shortDate(l.to_date)} · ${days(l.days)}`,
      outcome:
        l.status === "approved" && l.unpaid_days ? `${days(l.paid_days ?? 0)} paid, ${days(l.unpaid_days)} unpaid` : l.status === "approved" && !l.paid_type ? "Unpaid" : "",
      r: l,
    })),
    ...perm.map((p) => ({
      kind: "permission" as const,
      id: p.permission_request_id,
      what: "Permission",
      when: `${shortDate(p.work_date)}, ${p.from_time}–${p.to_time} (${p.minutes} min)`,
      outcome: p.status === "approved" && p.unpaid_minutes ? `${p.unpaid_minutes} min unpaid` : "",
      r: p,
    })),
    ...reg.map((g) => ({
      kind: "regularization" as const,
      id: g.regularization_id,
      what: g.session_id ? "Correct a check-in/out" : "Add a missed day",
      when: `${shortDate(g.work_date)}${g.clock_in ? `, in ${merchantTime(g.clock_in)}` : ""}${g.clock_out ? `, out ${merchantTime(g.clock_out)}` : ""}`,
      outcome: "",
      r: g,
    })),
  ].sort((a, b) => (a.r.status === "pending" ? 0 : 1) - (b.r.status === "pending" ? 0 : 1) || b.r.requested_at.localeCompare(a.r.requested_at));
}

/** Loads the three kinds of request; `query` is appended to each list call. */
export function useRequests(query: string) {
  const [rows, setRows] = useState<AnyRequest[] | null>(null);
  const load = useCallback(() => {
    Promise.all([
      api.get<{ leave_requests: LeaveRequest[] }>(`${KIND_PATH.leave}?${query}`),
      api.get<{ permission_requests: PermissionRequest[] }>(`${KIND_PATH.permission}?${query}`),
      api.get<{ regularizations: Regularization[] }>(`${KIND_PATH.regularization}?${query}`),
    ])
      .then(([l, p, g]) => setRows(toRows(l.leave_requests, p.permission_requests, g.regularizations)))
      .catch((e) => toast.error(message(e, "Could not load the requests")));
  }, [query]);
  useEffect(load, [load]);
  return { rows, load };
}

export function useLeaveTypes(all = false) {
  const [types, setTypes] = useState<LeaveType[]>([]);
  const load = useCallback(() => {
    api
      .get<{ leave_types: LeaveType[] }>("/api/v1/hr/leave-types")
      .then((d) => setTypes(all ? d.leave_types : d.leave_types.filter((t) => t.status === "active")))
      .catch(() => {});
  }, [all]);
  useEffect(load, [load]);
  return { types, load };
}

/** The request table. `onAct` gets approve / reject / cancel; which buttons show is up to `actions`. */
export function RequestsTable({
  rows,
  showEmployee,
  actions,
  onAct,
  testId,
  empty = "No requests.",
}: {
  rows: AnyRequest[];
  showEmployee?: boolean;
  actions: (row: AnyRequest) => ("approve" | "reject" | "cancel")[];
  onAct: (row: AnyRequest, action: "approve" | "reject" | "cancel") => void;
  testId?: string;
  empty?: string;
}) {
  return (
    <Table data-testid={testId}>
      <TableHeader>
        <TableRow>
          <TableHead>Request</TableHead>
          {showEmployee && <TableHead>Employee</TableHead>}
          <TableHead>When</TableHead>
          <TableHead>Reason</TableHead>
          <TableHead>Status</TableHead>
          <TableHead className="text-right">Actions</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((row) => {
          const r = row.r;
          const acts = actions(row);
          return (
            <TableRow key={`${row.kind}-${row.id}`}>
              <TableCell className="text-sm">
                <span className="font-medium">{row.what}</span>
                <span className="block text-xs text-muted-foreground">
                  {r.request_number}
                  {r.source === "pos" ? " · from the POS" : ""}
                </span>
              </TableCell>
              {showEmployee && (
                <TableCell className="text-sm">
                  {r.employee}
                  <span className="block text-xs text-muted-foreground">{[r.employee_code, r.branch].filter(Boolean).join(" · ")}</span>
                </TableCell>
              )}
              <TableCell className="whitespace-nowrap text-sm">{row.when}</TableCell>
              <TableCell className="max-w-64 whitespace-normal text-sm">
                {r.reason}
                {r.decision_note && <span className="block text-xs text-muted-foreground">Note: {r.decision_note}</span>}
                {r.cancel_reason && <span className="block text-xs text-muted-foreground">Cancelled: {r.cancel_reason}</span>}
              </TableCell>
              <TableCell className="text-sm">
                <StatusChip status="" tone={REQUEST_TONE[r.status]} label={REQUEST_LABEL[r.status]} />
                <span className="block text-xs text-muted-foreground">
                  {[row.outcome, r.decided_by ? `by ${r.decided_by}` : "", r.cancelled_by && r.status === "cancelled" ? `by ${r.cancelled_by}` : ""].filter(Boolean).join(" · ")}
                </span>
              </TableCell>
              <TableCell>
                <div className="flex justify-end gap-1.5">
                  {acts.includes("approve") && (
                    <Button size="sm" onClick={() => onAct(row, "approve")} aria-label={`Approve ${r.request_number}`}>
                      Approve
                    </Button>
                  )}
                  {acts.includes("reject") && (
                    <Button size="sm" variant="outline" onClick={() => onAct(row, "reject")} aria-label={`Reject ${r.request_number}`}>
                      Reject
                    </Button>
                  )}
                  {acts.includes("cancel") && (
                    <Button
                      size="sm"
                      variant="ghost"
                      className="text-destructive hover:text-destructive"
                      onClick={() => onAct(row, "cancel")}
                      aria-label={`Cancel ${r.request_number}`}
                    >
                      Cancel
                    </Button>
                  )}
                </div>
              </TableCell>
            </TableRow>
          );
        })}
        {rows.length === 0 && (
          <TableRow>
            <TableCell colSpan={showEmployee ? 6 : 5} className="py-6 text-center text-sm text-muted-foreground">
              {empty}
            </TableCell>
          </TableRow>
        )}
      </TableBody>
    </Table>
  );
}

/** Approve (optional note), reject (note required) or cancel (reason) one request. */
export function DecideDialog({
  row,
  action,
  onClose,
  onDone,
}: {
  row: AnyRequest;
  action: "approve" | "reject" | "cancel";
  onClose: () => void;
  onDone: () => void;
}) {
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const r = row.r;
  const verb = { approve: "Approve", reject: "Reject", cancel: "Cancel" }[action];
  async function go(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      await api.post(`${KIND_PATH[row.kind]}/${row.id}/${action}`, action === "cancel" ? { reason: note.trim() } : { note: note.trim() });
      toast.success(`${r.request_number} ${{ approve: "approved", reject: "rejected", cancel: "cancelled" }[action]}`);
      onDone();
      onClose();
    } catch (err) {
      toast.error(message(err, `Could not ${action} it`));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={`${verb} ${r.request_number}`}
      description={`${r.employee} · ${row.what} · ${row.when}`}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Back
          </Button>
          <Button type="submit" form="decide-form" variant={action === "approve" ? "default" : "destructive"} disabled={busy || (action === "reject" && !note.trim())}>
            {busy && <Loader2 className="animate-spin" aria-hidden />} {verb}
          </Button>
        </>
      }
    >
      <form id="decide-form" onSubmit={go} className="space-y-3">
        {action === "approve" && row.kind === "leave" && (
          <p className="text-sm text-muted-foreground">Days within the employee&apos;s balance are paid; any beyond it are unpaid and come off the salary.</p>
        )}
        {action === "approve" && row.kind === "permission" && (
          <p className="text-sm text-muted-foreground">Free while this month&apos;s permissions are within the limit; minutes beyond it are unpaid.</p>
        )}
        {action === "approve" && row.kind === "regularization" && (
          <p className="text-sm text-muted-foreground">The check-in/out is corrected as asked, with this request named in the attendance.</p>
        )}
        {action === "cancel" && r.status === "approved" && (
          <p className="text-sm text-muted-foreground">The leave comes off the attendance, and the days go back to the balance.</p>
        )}
        <Field label={action === "approve" ? "Note (optional)" : action === "reject" ? "Why *" : "Reason"} htmlFor="decide-note">
          <Input id="decide-note" value={note} onChange={(e) => setNote(e.target.value)} autoFocus />
        </Field>
      </form>
    </Modal>
  );
}

/** The leave balances and this month's permission use. */
export function BalancesCard({ data, onSetAllowance }: { data: LeaveBalances; onSetAllowance?: (typeId: string) => void }) {
  const p = data.permission;
  const n = (v: number | null) => (v == null ? "No limit" : String(v));
  return (
    <div className="space-y-3">
      <Table data-testid="leave-balances">
        <TableHeader>
          <TableRow>
            <TableHead>Leave</TableHead>
            <TableHead className="text-right">For the year</TableHead>
            <TableHead className="text-right">Taken</TableHead>
            <TableHead className="text-right">Waiting</TableHead>
            <TableHead className="text-right">Left</TableHead>
            {onSetAllowance && <TableHead className="text-right">Allowance</TableHead>}
          </TableRow>
        </TableHeader>
        <TableBody>
          {data.balances
            .filter((b) => b.status === "active" || b.taken > 0 || b.pending > 0)
            .map((b) => (
              <TableRow key={b.leave_type_id}>
                <TableCell className="text-sm">
                  {b.name}
                  <span className="block text-xs text-muted-foreground">
                    {b.paid ? "Paid" : "Unpaid"}
                    {b.override ? " · own allowance" : ""}
                    {b.unpaid > 0 ? ` · ${b.unpaid} day(s) beyond the balance, unpaid` : ""}
                  </span>
                </TableCell>
                <TableCell className="tabular text-right">{b.paid ? n(b.entitled) : "—"}</TableCell>
                <TableCell className="tabular text-right">{b.taken}</TableCell>
                <TableCell className="tabular text-right">{b.pending || "—"}</TableCell>
                <TableCell className="tabular text-right font-medium">{b.paid ? n(b.available) : "—"}</TableCell>
                {onSetAllowance && (
                  <TableCell className="text-right">
                    {b.paid && (
                      <Button size="sm" variant="ghost" onClick={() => onSetAllowance(b.leave_type_id)} aria-label={`Set ${b.name} allowance`}>
                        Set
                      </Button>
                    )}
                  </TableCell>
                )}
              </TableRow>
            ))}
        </TableBody>
      </Table>
      <p className="text-sm text-muted-foreground" data-testid="permission-usage">
        Permission in {p.month}: {p.used_count} of {p.free_count} free used ({p.used_free_minutes} of {p.free_minutes} min)
        {p.unpaid_minutes > 0 ? `, ${p.unpaid_minutes} min unpaid` : ""}
        {p.pending_count > 0 ? `, ${p.pending_count} waiting` : ""}. One permission is at most {p.max_minutes} min.
      </p>
    </div>
  );
}

// ------------------------------------------------------------------ asking

function SubmitFooter({ form, busy, disabled, label, onClose }: { form: string; busy: boolean; disabled: boolean; label: string; onClose: () => void }) {
  return (
    <>
      <Button variant="outline" onClick={onClose}>
        Cancel
      </Button>
      <Button type="submit" form={form} disabled={busy || disabled}>
        {busy && <Loader2 className="animate-spin" aria-hidden />} {label}
      </Button>
    </>
  );
}

/** For: the employee asked for (HR on their behalf); none = the signed-in user. */
type For = { userId?: string; name?: string };

export function LeaveDialog({ forUser, onClose, onSaved }: { forUser?: For; onClose: () => void; onSaved: () => void }) {
  const { types } = useLeaveTypes();
  const [typeId, setTypeId] = useState("");
  const [from, setFrom] = useState(() => localDate(1));
  const [to, setTo] = useState(() => localDate(1));
  const [half, setHalf] = useState(false);
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const type = types.find((t) => t.leave_type_id === typeId);
  const canHalf = from === to && (type?.allow_half_day ?? true);

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      const r = await api.post<LeaveRequest>(KIND_PATH.leave, {
        user_id: forUser?.userId,
        leave_type_id: typeId,
        from_date: from,
        to_date: to,
        half_day: canHalf && half,
        reason,
      });
      toast.success(`Leave asked (${r.request_number})`);
      onSaved();
      onClose();
    } catch (err) {
      toast.error(message(err, "Could not ask for the leave"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={forUser?.name ? `Leave for ${forUser.name}` : "Ask for leave"}
      footer={<SubmitFooter form="leave-form" busy={busy} disabled={!typeId || !reason.trim() || to < from} label="Ask for leave" onClose={onClose} />}
    >
      <form id="leave-form" onSubmit={save} className="grid grid-cols-2 gap-3">
        <Field label="Leave type *" htmlFor="lv-type" className="col-span-2">
          <select id="lv-type" value={typeId} onChange={(e) => setTypeId(e.target.value)} className={`${selectClass} w-full`}>
            <option value="">Choose…</option>
            {types.map((t) => (
              <option key={t.leave_type_id} value={t.leave_type_id}>
                {t.name}
                {t.paid ? "" : " (unpaid)"}
              </option>
            ))}
          </select>
        </Field>
        <Field label="From *" htmlFor="lv-from">
          <Input
            id="lv-from"
            type="date"
            value={from}
            onChange={(e) => {
              setFrom(e.target.value);
              if (to < e.target.value) setTo(e.target.value);
            }}
          />
        </Field>
        <Field label="To *" htmlFor="lv-to">
          <Input id="lv-to" type="date" value={to} min={from} onChange={(e) => setTo(e.target.value)} />
        </Field>
        {canHalf && (
          <label className="col-span-2 flex items-center gap-2 text-sm">
            <input type="checkbox" checked={half} onChange={(e) => setHalf(e.target.checked)} /> Half day
          </label>
        )}
        <Field label="Reason *" htmlFor="lv-reason" className="col-span-2">
          <Input id="lv-reason" value={reason} onChange={(e) => setReason(e.target.value)} />
        </Field>
      </form>
    </Modal>
  );
}

export function PermissionDialog({ forUser, onClose, onSaved }: { forUser?: For; onClose: () => void; onSaved: () => void }) {
  const [date, setDate] = useState(() => localDate());
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      const r = await api.post<PermissionRequest>(KIND_PATH.permission, { user_id: forUser?.userId, work_date: date, from_time: from, to_time: to, reason });
      toast.success(`Permission asked (${r.request_number})`);
      onSaved();
      onClose();
    } catch (err) {
      toast.error(message(err, "Could not ask for the permission"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={forUser?.name ? `Permission for ${forUser.name}` : "Ask for permission"}
      description="A few hours off in a working day: free up to the month's limit, unpaid beyond it."
      footer={<SubmitFooter form="perm-form" busy={busy} disabled={!from || !to || to <= from || !reason.trim()} label="Ask for permission" onClose={onClose} />}
    >
      <form id="perm-form" onSubmit={save} className="grid grid-cols-3 gap-3">
        <Field label="Date *" htmlFor="pr-date">
          <Input id="pr-date" type="date" value={date} onChange={(e) => setDate(e.target.value)} />
        </Field>
        <Field label="From *" htmlFor="pr-from">
          <Input id="pr-from" type="time" value={from} onChange={(e) => setFrom(e.target.value)} />
        </Field>
        <Field label="To *" htmlFor="pr-to">
          <Input id="pr-to" type="time" value={to} onChange={(e) => setTo(e.target.value)} />
        </Field>
        <Field label="Reason *" htmlFor="pr-reason" className="col-span-3">
          <Input id="pr-reason" value={reason} onChange={(e) => setReason(e.target.value)} />
        </Field>
      </form>
    </Modal>
  );
}

/** Correct a check-in/out (a missed check-out, a wrong time) or add a forgotten day. */
export function RegularizeDialog({ forUser, onClose, onSaved }: { forUser?: For; onClose: () => void; onSaved: () => void }) {
  const [date, setDate] = useState(() => localDate(-1));
  const [day, setDay] = useState<AttendanceRecord | null | undefined>(undefined);
  const [sessionId, setSessionId] = useState(""); // "" = add a missed check-in/out
  const [inTime, setInTime] = useState("");
  const [outTime, setOutTime] = useState("");
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let live = true;
    const q = new URLSearchParams({ month: date.slice(0, 7) });
    if (forUser?.userId) q.set("user_id", forUser.userId);
    api
      .get<{ attendance: AttendanceRecord[] }>(`/api/v1/hr/attendance?${q.toString()}`)
      .then((d) => {
        if (!live) return;
        const rec = d.attendance.find((a) => a.work_date === date) ?? null;
        setDay(rec);
        const open = rec?.sessions.find((s) => !s.clock_out);
        setSessionId(open?.id ?? "");
      })
      .catch(() => live && setDay(null));
    return () => {
      live = false;
    };
  }, [date, forUser?.userId]);

  const session = day?.sessions.find((s) => s.id === sessionId);
  const ready = sessionId ? !!(inTime || outTime) : !!(inTime && outTime);

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      const inAt = inTime ? merchantStamp(date, inTime) : undefined;
      const startHm = inTime || (session ? merchantTime(session.clock_in) : "");
      const outAt = outTime ? merchantStamp(date, outTime, !!startHm && outTime < startHm) : undefined;
      const r = await api.post<Regularization>(KIND_PATH.regularization, {
        user_id: forUser?.userId,
        work_date: date,
        session_id: sessionId || undefined,
        clock_in: inAt,
        clock_out: outAt,
        reason,
      });
      toast.success(`Correction asked (${r.request_number})`);
      onSaved();
      onClose();
    } catch (err) {
      toast.error(message(err, "Could not ask for the correction"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={forUser?.name ? `Correct attendance for ${forUser.name}` : "Correct my attendance"}
      description="A missed check-out, a wrong time, or a day you forgot to check in. A manager approves it."
      width={560}
      footer={<SubmitFooter form="reg-form" busy={busy} disabled={!ready || !reason.trim()} label="Ask for the correction" onClose={onClose} />}
    >
      <form id="reg-form" onSubmit={save} className="grid grid-cols-2 gap-3">
        <Field label="Day *" htmlFor="rg-date" className="col-span-2">
          <Input
            id="rg-date"
            type="date"
            value={date}
            max={localDate()}
            onChange={(e) => {
              setDate(e.target.value);
              setInTime("");
              setOutTime("");
            }}
          />
        </Field>
        <fieldset className="col-span-2 space-y-1.5" aria-label="What to correct">
          <span className="text-xs font-medium text-muted-foreground">What to correct</span>
          {(day?.sessions ?? []).map((s) => (
            <label key={s.id} className="flex items-center gap-2 text-sm">
              <input type="radio" name="rg-session" checked={sessionId === s.id} onChange={() => setSessionId(s.id)} />
              Checked in {merchantTime(s.clock_in)}, {s.clock_out ? `out ${merchantTime(s.clock_out)}` : "no check-out"}
            </label>
          ))}
          <label className="flex items-center gap-2 text-sm">
            <input type="radio" name="rg-session" checked={sessionId === ""} onChange={() => setSessionId("")} />
            Add a check-in and check-out I missed
          </label>
          {day === null && <p className="text-xs text-muted-foreground">No check-in on this day.</p>}
        </fieldset>
        <Field label={sessionId ? "Correct check-in" : "Check-in *"} htmlFor="rg-in">
          <Input id="rg-in" type="time" value={inTime} onChange={(e) => setInTime(e.target.value)} />
        </Field>
        <Field label={sessionId ? "Correct check-out" : "Check-out *"} htmlFor="rg-out">
          <Input id="rg-out" type="time" value={outTime} onChange={(e) => setOutTime(e.target.value)} />
        </Field>
        {sessionId && <p className="col-span-2 -mt-1 text-xs text-muted-foreground">Leave a time empty to keep it as it is.</p>}
        <Field label="Reason *" htmlFor="rg-reason" className="col-span-2">
          <Input id="rg-reason" value={reason} onChange={(e) => setReason(e.target.value)} />
        </Field>
      </form>
    </Modal>
  );
}
