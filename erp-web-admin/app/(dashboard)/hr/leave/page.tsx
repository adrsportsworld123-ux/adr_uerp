"use client";

// Leave & Requests (erp-core-go §3.78, migrations/075), hr.manage, four tabs:
//   Requests — every employee's leave, permission and attendance-correction
//   requests: approve, reject (with why) or cancel (an approved leave too,
//   until that month's payroll is finalized); ask on an employee's behalf.
//   Balances — an employee's leave for a year; set their own allowance
//   (a late joiner, a carried-forward balance).
//   Leave types — casual, sick, loss of pay… paid or not, the yearly quota.
//   Rules — when the leave year starts, the free permission each month, how
//   long one permission may be, and how far back a correction may go.
// Nobody approves their own request.
import { FormEvent, useCallback, useEffect, useMemo, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { Loader2, Plus } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api-client";
import { useAuth } from "@/lib/auth";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import type { AttendanceSettings, Employee, LeaveBalances, LeaveType } from "@/lib/types";
import { localDate } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Modal } from "@/components/ui/dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { PageHeader } from "@/components/data/page-header";
import { Field, FilterBar, Panel, selectClass } from "@/components/data/panel";
import { SearchSelect } from "@/components/data/search-select";
import { StatusChip } from "@/components/data/status-chip";
import {
  AnyRequest,
  BalancesCard,
  DecideDialog,
  KIND_LABEL,
  LeaveDialog,
  PermissionDialog,
  RegularizeDialog,
  RequestKind,
  RequestsTable,
  message,
  useLeaveTypes,
  useRequests,
} from "@/components/hr/requests";

const TABS = [
  ["requests", "Requests"],
  ["balances", "Balances"],
  ["types", "Leave types"],
  ["rules", "Rules"],
] as const;

function useEmployees() {
  const [list, setList] = useState<Employee[]>([]);
  useEffect(() => {
    api
      .get<{ employees: Employee[] }>("/api/v1/hr/employees")
      .then((d) => setList(d.employees))
      .catch(() => {});
  }, []);
  return list;
}

export default function LeavePage() {
  const params = useSearchParams();
  const router = useRouter();
  const tab = (TABS.find(([k]) => k === params.get("tab"))?.[0] ?? "requests") as (typeof TABS)[number][0];
  const { can } = usePermissions();
  return (
    <div className="space-y-5">
      <PageHeader
        title="Leave & Requests"
        description="Employees' leave, permission and attendance corrections — asked from the POS or My Leave & Payslips — with balances, leave types and the rules. Unpaid leave and permission beyond the free limit come off the salary."
      />
      <div role="tablist" aria-label="Leave" className="flex w-fit gap-1 rounded-lg bg-muted p-1">
        {TABS.map(([k, label]) => (
          <button
            key={k}
            role="tab"
            aria-selected={tab === k}
            onClick={() => router.replace(`/hr/leave?tab=${k}`)}
            className={`rounded-md px-3 py-1.5 text-sm ${tab === k ? "bg-card font-medium shadow-sm" : "text-muted-foreground hover:text-foreground"}`}
          >
            {label}
          </button>
        ))}
      </div>
      {!can("hr.manage") ? (
        <p className="text-sm text-muted-foreground">{missingPermissionHint("hr.manage")}</p>
      ) : tab === "requests" ? (
        <Requests />
      ) : tab === "balances" ? (
        <Balances />
      ) : tab === "types" ? (
        <Types />
      ) : (
        <Rules />
      )}
    </div>
  );
}

// ------------------------------------------------------------------ requests

function Requests() {
  const { userId } = useAuth();
  const employees = useEmployees();
  const [status, setStatus] = useState("pending");
  const [kind, setKind] = useState<"" | RequestKind>("");
  const [who, setWho] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [acting, setActing] = useState<{ row: AnyRequest; action: "approve" | "reject" | "cancel" } | null>(null);
  const [asking, setAsking] = useState<RequestKind | null>(null);
  const [askFor, setAskFor] = useState("");
  const query = useMemo(() => {
    const q = new URLSearchParams();
    if (status) q.set("status", status);
    if (who) q.set("user_id", who);
    if (from) q.set("from", from);
    if (to) q.set("to", to);
    return q.toString();
  }, [status, who, from, to]);
  const { rows, load } = useRequests(query);
  const shown = (rows ?? []).filter((r) => !kind || r.kind === kind);
  const options = employees.map((e) => ({ value: e.id, label: e.name, hint: e.employee_code }));
  const forUser = employees.find((e) => e.id === askFor);

  return (
    <div className="space-y-4">
      <FilterBar>
        <Field label="Status" htmlFor="lr-status">
          <select id="lr-status" value={status} onChange={(e) => setStatus(e.target.value)} className={`${selectClass} w-36`}>
            <option value="pending">Waiting</option>
            <option value="approved">Approved</option>
            <option value="rejected">Rejected</option>
            <option value="cancelled">Cancelled</option>
            <option value="">Any</option>
          </select>
        </Field>
        <Field label="Kind" htmlFor="lr-kind">
          <select id="lr-kind" value={kind} onChange={(e) => setKind(e.target.value as "" | RequestKind)} className={`${selectClass} w-48`}>
            <option value="">Every kind</option>
            {(Object.keys(KIND_LABEL) as RequestKind[]).map((k) => (
              <option key={k} value={k}>
                {KIND_LABEL[k]}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Employee" htmlFor="lr-who">
          <SearchSelect id="lr-who" value={who} onChange={setWho} options={options} noneLabel="Everyone" placeholder="Everyone" className="w-52" />
        </Field>
        <Field label="From" htmlFor="lr-from">
          <Input id="lr-from" type="date" value={from} onChange={(e) => setFrom(e.target.value)} />
        </Field>
        <Field label="To" htmlFor="lr-to">
          <Input id="lr-to" type="date" value={to} onChange={(e) => setTo(e.target.value)} />
        </Field>
      </FilterBar>
      <Panel
        title={status === "pending" ? "Waiting for a decision" : "Requests"}
        actions={
          <div className="flex items-end gap-2">
            <SearchSelect
              id="lr-askfor"
              ariaLabel="Ask on behalf of"
              value={askFor}
              onChange={setAskFor}
              options={options.filter((o) => o.value !== userId)}
              placeholder="On behalf of…"
              className="w-48"
            />
            <Button size="sm" variant="outline" disabled={!askFor} onClick={() => setAsking("leave")}>
              <Plus aria-hidden /> Leave
            </Button>
            <Button size="sm" variant="outline" disabled={!askFor} onClick={() => setAsking("permission")}>
              <Plus aria-hidden /> Permission
            </Button>
            <Button size="sm" variant="outline" disabled={!askFor} onClick={() => setAsking("regularization")}>
              <Plus aria-hidden /> Correction
            </Button>
          </div>
        }
      >
        {rows && (
          <RequestsTable
            rows={shown}
            showEmployee
            testId="hr-requests"
            empty={status === "pending" ? "Nothing is waiting." : "No requests."}
            actions={(row) =>
              row.r.status === "pending"
                ? row.r.user_id === userId
                  ? ["cancel"]
                  : ["approve", "reject", "cancel"]
                : row.r.status === "approved" && row.kind !== "regularization"
                  ? ["cancel"]
                  : []
            }
            onAct={(row, action) => setActing({ row, action })}
          />
        )}
      </Panel>
      {acting && <DecideDialog row={acting.row} action={acting.action} onClose={() => setActing(null)} onDone={load} />}
      {asking && forUser && (
        <>
          {asking === "leave" && <LeaveDialog forUser={{ userId: forUser.id, name: forUser.name }} onClose={() => setAsking(null)} onSaved={load} />}
          {asking === "permission" && <PermissionDialog forUser={{ userId: forUser.id, name: forUser.name }} onClose={() => setAsking(null)} onSaved={load} />}
          {asking === "regularization" && <RegularizeDialog forUser={{ userId: forUser.id, name: forUser.name }} onClose={() => setAsking(null)} onSaved={load} />}
        </>
      )}
    </div>
  );
}

// ------------------------------------------------------------------ balances

function Balances() {
  const employees = useEmployees();
  const [who, setWho] = useState("");
  const [year, setYear] = useState(() => Number(localDate().slice(0, 4)));
  const [data, setData] = useState<LeaveBalances | null>(null);
  const [setting, setSetting] = useState<string | null>(null);
  const load = useCallback(() => {
    if (!who) return;
    api
      .get<LeaveBalances>(`/api/v1/hr/leave-balances?user_id=${who}&year=${year}`)
      .then(setData)
      .catch((e) => toast.error(message(e, "Could not load the balance")));
  }, [who, year]);
  useEffect(load, [load]);
  const shown = data && data.user_id === who ? data : null;
  const type = shown?.balances.find((b) => b.leave_type_id === setting);
  return (
    <div className="space-y-4">
      <FilterBar>
        <Field label="Employee" htmlFor="lb-who">
          <SearchSelect
            id="lb-who"
            value={who}
            onChange={setWho}
            options={employees.map((e) => ({ value: e.id, label: e.name, hint: e.employee_code }))}
            placeholder="Choose an employee"
            className="w-60"
          />
        </Field>
        <Field label="Leave year" htmlFor="lb-year">
          <Input id="lb-year" type="number" value={year} onChange={(e) => setYear(Number(e.target.value))} className="w-28" />
        </Field>
      </FilterBar>
      {shown ? (
        <Panel title={`${shown.name} · ${shown.year_from} to ${shown.year_to}`}>
          <BalancesCard data={shown} onSetAllowance={setSetting} />
        </Panel>
      ) : (
        <p className="text-sm text-muted-foreground">Choose an employee to see their leave.</p>
      )}
      {setting && type && shown && <AllowanceDialog data={shown} typeId={setting} typeName={type.name} current={type.entitled} override={type.override} onClose={() => setSetting(null)} onSaved={load} />}
    </div>
  );
}

function AllowanceDialog({
  data,
  typeId,
  typeName,
  current,
  override,
  onClose,
  onSaved,
}: {
  data: LeaveBalances;
  typeId: string;
  typeName: string;
  current: number | null;
  override: boolean;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [days, setDays] = useState(current == null ? "" : String(current));
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  async function save(reset: boolean) {
    setBusy(true);
    try {
      await api.put("/api/v1/hr/leave-balances", { user_id: data.user_id, leave_type_id: typeId, leave_year: data.leave_year, days: reset ? null : Number(days), note });
      toast.success(reset ? `${typeName}: back to the usual quota` : `${typeName} allowance set to ${days} day(s)`);
      onSaved();
      onClose();
    } catch (err) {
      toast.error(message(err, "Could not set the allowance"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={`${typeName} for ${data.name}, ${data.leave_year}`}
      description="Their own allowance for this leave year instead of the type's quota."
      footer={
        <>
          {override && (
            <Button variant="ghost" className="me-auto" disabled={busy} onClick={() => save(true)}>
              Use the usual quota
            </Button>
          )}
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={busy || days === "" || Number(days) < 0} onClick={() => save(false)}>
            {busy && <Loader2 className="animate-spin" aria-hidden />} Save
          </Button>
        </>
      }
    >
      <div className="grid grid-cols-2 gap-3">
        <Field label="Days *" htmlFor="al-days">
          <Input id="al-days" type="number" step="0.5" min="0" value={days} onChange={(e) => setDays(e.target.value)} />
        </Field>
        <Field label="Note" htmlFor="al-note">
          <Input id="al-note" value={note} onChange={(e) => setNote(e.target.value)} placeholder="Joined in July" />
        </Field>
      </div>
    </Modal>
  );
}

// ------------------------------------------------------------------ types

function Types() {
  const { types, load } = useLeaveTypes(true);
  const [editing, setEditing] = useState<LeaveType | "new" | null>(null);
  async function toggle(t: LeaveType) {
    try {
      await api.patch(`/api/v1/hr/leave-types/${t.leave_type_id}`, { status: t.status === "active" ? "inactive" : "active" });
      toast.success(`${t.name} ${t.status === "active" ? "switched off" : "switched on"}`);
      load();
    } catch (err) {
      toast.error(message(err, "Could not change it"));
    }
  }
  return (
    <Panel
      title="Leave types"
      description="A paid type with a yearly quota is paid while the balance lasts; days beyond it, and an unpaid type, come off the salary."
      actions={
        <Button size="sm" onClick={() => setEditing("new")}>
          <Plus aria-hidden /> New leave type
        </Button>
      }
    >
      <Table data-testid="leave-types">
        <TableHeader>
          <TableRow>
            <TableHead>Code</TableHead>
            <TableHead>Name</TableHead>
            <TableHead>Pay</TableHead>
            <TableHead className="text-right">Days a year</TableHead>
            <TableHead>Half day</TableHead>
            <TableHead>Status</TableHead>
            <TableHead className="text-right">Actions</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {types.map((t) => (
            <TableRow key={t.leave_type_id}>
              <TableCell className="font-mono text-sm">{t.code}</TableCell>
              <TableCell className="text-sm">{t.name}</TableCell>
              <TableCell className="text-sm">{t.paid ? "Paid" : "Unpaid"}</TableCell>
              <TableCell className="tabular text-right">{t.annual_quota ?? "No limit"}</TableCell>
              <TableCell className="text-sm">{t.allow_half_day ? "Yes" : "No"}</TableCell>
              <TableCell>
                <StatusChip status="" tone={t.status === "active" ? "ok" : "muted"} label={t.status === "active" ? "Active" : "Inactive"} />
              </TableCell>
              <TableCell>
                <div className="flex justify-end gap-1.5">
                  <Button size="sm" variant="ghost" onClick={() => setEditing(t)} aria-label={`Edit ${t.name}`}>
                    Edit
                  </Button>
                  <Button size="sm" variant="ghost" onClick={() => toggle(t)} aria-label={`${t.status === "active" ? "Deactivate" : "Activate"} ${t.name}`}>
                    {t.status === "active" ? "Deactivate" : "Activate"}
                  </Button>
                </div>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      {editing && <TypeDialog type={editing === "new" ? null : editing} onClose={() => setEditing(null)} onSaved={load} />}
    </Panel>
  );
}

function TypeDialog({ type, onClose, onSaved }: { type: LeaveType | null; onClose: () => void; onSaved: () => void }) {
  const [code, setCode] = useState(type?.code ?? "");
  const [name, setName] = useState(type?.name ?? "");
  const [paid, setPaid] = useState(type?.paid ?? true);
  const [quota, setQuota] = useState(type?.annual_quota == null ? "" : String(type.annual_quota));
  const [half, setHalf] = useState(type?.allow_half_day ?? true);
  const [busy, setBusy] = useState(false);
  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    const body = { code, name, paid, allow_half_day: half, ...(quota === "" ? { no_limit: true } : { annual_quota: Number(quota) }) };
    try {
      if (type) await api.patch(`/api/v1/hr/leave-types/${type.leave_type_id}`, body);
      else await api.post("/api/v1/hr/leave-types", body);
      toast.success(`${name} saved`);
      onSaved();
      onClose();
    } catch (err) {
      toast.error(message(err, "Could not save the leave type"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={type ? `Edit ${type.name}` : "New leave type"}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="lt-form" disabled={busy || !code.trim() || !name.trim()}>
            {busy && <Loader2 className="animate-spin" aria-hidden />} Save
          </Button>
        </>
      }
    >
      <form id="lt-form" onSubmit={save} className="grid grid-cols-2 gap-3">
        <Field label="Code *" htmlFor="lt-code">
          <Input id="lt-code" value={code} maxLength={10} onChange={(e) => setCode(e.target.value.toUpperCase())} placeholder="EL" />
        </Field>
        <Field label="Name *" htmlFor="lt-name">
          <Input id="lt-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="Earned leave" />
        </Field>
        <Field label="Pay" htmlFor="lt-paid">
          <select id="lt-paid" value={paid ? "paid" : "unpaid"} onChange={(e) => setPaid(e.target.value === "paid")} className={`${selectClass} w-full`}>
            <option value="paid">Paid</option>
            <option value="unpaid">Unpaid (loss of pay)</option>
          </select>
        </Field>
        <Field label="Days a year" htmlFor="lt-quota">
          <Input id="lt-quota" type="number" step="0.5" min="0" value={quota} onChange={(e) => setQuota(e.target.value)} placeholder="No limit" />
        </Field>
        <label className="col-span-2 flex items-center gap-2 text-sm">
          <input type="checkbox" checked={half} onChange={(e) => setHalf(e.target.checked)} /> Can be taken as a half day
        </label>
      </form>
    </Modal>
  );
}

// ------------------------------------------------------------------ rules

const MONTHS = ["January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"];

function Rules() {
  const [s, setS] = useState<AttendanceSettings | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    api
      .get<AttendanceSettings>("/api/v1/hr/attendance/settings")
      .then(setS)
      .catch((e) => toast.error(message(e, "Could not load the rules")));
  }, []);
  if (!s) return null;
  const num = (k: keyof AttendanceSettings) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => setS({ ...s, [k]: Number(e.target.value) });
  async function save(e: FormEvent) {
    e.preventDefault();
    if (!s) return;
    setBusy(true);
    try {
      setS(
        await api.put<AttendanceSettings>("/api/v1/hr/attendance/settings", {
          leave_year_start_month: s.leave_year_start_month,
          permission_free_count: s.permission_free_count,
          permission_free_minutes: s.permission_free_minutes,
          permission_max_minutes: s.permission_max_minutes,
          standard_day_hours: s.standard_day_hours,
          regularize_within_days: s.regularize_within_days,
        }),
      );
      toast.success("Rules saved");
    } catch (err) {
      toast.error(message(err, "Could not save the rules"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Panel title="Rules" className="max-w-2xl">
      <form onSubmit={save} className="grid grid-cols-2 gap-4">
        <Field label="Leave year starts in" htmlFor="ru-start">
          <select id="ru-start" value={s.leave_year_start_month} onChange={num("leave_year_start_month")} className={`${selectClass} w-full`}>
            {MONTHS.map((m, i) => (
              <option key={m} value={i + 1}>
                {m}
              </option>
            ))}
          </select>
        </Field>
        <Field label="A working day is (hours)" htmlFor="ru-hours">
          <Input id="ru-hours" type="number" step="0.5" min="1" max="24" value={s.standard_day_hours} onChange={num("standard_day_hours")} />
        </Field>
        <Field label="Free permissions a month" htmlFor="ru-count">
          <Input id="ru-count" type="number" min="0" max="31" value={s.permission_free_count} onChange={num("permission_free_count")} />
        </Field>
        <Field label="Free permission minutes a month" htmlFor="ru-free">
          <Input id="ru-free" type="number" min="0" step="15" value={s.permission_free_minutes} onChange={num("permission_free_minutes")} />
        </Field>
        <Field label="One permission at most (minutes)" htmlFor="ru-max">
          <Input id="ru-max" type="number" min="15" max="720" step="15" value={s.permission_max_minutes} onChange={num("permission_max_minutes")} />
        </Field>
        <Field label="Corrections allowed for the last (days)" htmlFor="ru-window">
          <Input id="ru-window" type="number" min="1" max="366" value={s.regularize_within_days} onChange={num("regularize_within_days")} />
        </Field>
        <p className="col-span-2 text-xs text-muted-foreground">
          Permission beyond the free count or minutes is unpaid, as a part of the employee&apos;s shift (or the working day above when they have no shift).
        </p>
        <div className="col-span-2">
          <Button type="submit" disabled={busy}>
            {busy && <Loader2 className="animate-spin" aria-hidden />} Save rules
          </Button>
        </div>
      </form>
    </Panel>
  );
}
