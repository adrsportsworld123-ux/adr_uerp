"use client";

// Attendance (erp-core-go internal/hr/attendance.go, migrations/052):
// the day register — every employee with their check-in and check-out,
// where each punch came from (location, IP, POS or web) and whether it
// was inside the branch geofence — plus HR marks and corrections, the
// merchant's check-in rules and each branch's geofence. Gated by hr.manage.
import { useCallback, useEffect, useMemo, useState } from "react";
import { Loader2, MapPin } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import {
  AttendanceRecord,
  AttendanceRegisterRow,
  AttendanceSession,
  AttendanceSettings,
  Branch,
  Geofence,
  PunchEvidence,
} from "@/lib/types";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import { getFix, mapsLink } from "@/lib/geolocation";
import { PageHeader } from "@/components/data/page-header";
import { SearchSelect } from "@/components/data/search-select";
import { StatusChip } from "@/components/data/status-chip";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Modal } from "@/components/ui/dialog";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";

const today = () => new Date().toLocaleDateString("en-CA"); // YYYY-MM-DD, local

function time(iso?: string) {
  if (!iso) return "—";
  const d = new Date(iso);
  return Number.isNaN(d.getTime())
    ? "—"
    : d.toLocaleTimeString("en-IN", { hour: "2-digit", minute: "2-digit" });
}

// "2026-10-17" + "09:30" in the browser's zone → RFC 3339 with offset.
function toRFC3339(date: string, hhmm: string): string | undefined {
  if (!hhmm) return undefined;
  const d = new Date(`${date}T${hhmm}:00`);
  if (Number.isNaN(d.getTime())) return undefined;
  const off = -d.getTimezoneOffset();
  const sign = off >= 0 ? "+" : "-";
  const pad = (n: number) => String(Math.floor(Math.abs(n))).padStart(2, "0");
  return `${date}T${hhmm}:00${sign}${pad(off / 60)}:${pad(off % 60)}`;
}

function hhmmOf(iso?: string) {
  if (!iso) return "";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "" : d.toTimeString().slice(0, 5);
}

const STATUS_LABEL: Record<string, string> = {
  present: "Present",
  half_day: "Half day",
  leave: "Leave",
  absent: "Absent",
};

function Evidence({ ev }: { ev: PunchEvidence }) {
  if (!ev.source) return null;
  const fence =
    ev.geofence === "outside" ? (
      <StatusChip
        status=""
        tone="bad"
        label={`Outside${ev.distance_m != null ? ` · ${ev.distance_m} m` : ""}`}
      />
    ) : ev.geofence === "inside" ? (
      <StatusChip status="" tone="ok" label="In area" />
    ) : ev.geofence === "unknown" ? (
      <StatusChip status="" tone="warn" label="No location" />
    ) : null;
  return (
    <div className="flex flex-col gap-0.5 text-xs text-muted-foreground">
      <div className="flex flex-wrap items-center gap-1.5">
        <span className="font-medium uppercase">
          {ev.source === "pos" ? "POS" : ev.source}
        </span>
        {fence}
      </div>
      {ev.latitude != null && ev.longitude != null && (
        <a
          className="inline-flex items-center gap-1 text-primary hover:underline"
          href={mapsLink(ev.latitude, ev.longitude)}
          target="_blank"
          rel="noreferrer"
        >
          <MapPin className="size-3" aria-hidden />
          {ev.latitude.toFixed(5)}, {ev.longitude.toFixed(5)}
          {ev.accuracy_m != null && ` ±${Math.round(ev.accuracy_m)} m`}
        </a>
      )}
      {ev.ip && <span>IP {ev.ip}</span>}
    </div>
  );
}

type EditTarget = {
  row: AttendanceRegisterRow;
  record: AttendanceRecord | null;
};
type SessionTarget = {
  row: AttendanceRegisterRow;
  record: AttendanceRecord;
  session: AttendanceSession | null;
};

export default function AttendancePage() {
  const { can } = usePermissions();
  const allowed = can("hr.manage");
  const [date, setDate] = useState(today());
  const [branchId, setBranchId] = useState("");
  const [branches, setBranches] = useState<Branch[]>([]);
  const [rows, setRows] = useState<AttendanceRegisterRow[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<EditTarget | null>(null);
  const [editingSession, setEditingSession] = useState<SessionTarget | null>(
    null,
  );

  useEffect(() => {
    api
      .get<{ branches: Branch[] }>("/api/v1/branches?scope=mine")
      .then((d) => setBranches(d.branches))
      .catch(() => {});
  }, []);

  const load = useCallback(() => {
    const p = new URLSearchParams({ date });
    if (branchId) p.set("branch_id", branchId);
    api
      .get<{ employees: AttendanceRegisterRow[] }>(
        `/api/v1/hr/attendance/register?${p}`,
      )
      .then((d) => {
        setRows(d.employees);
        setError(null);
      })
      .catch((e) =>
        setError(
          e instanceof ApiError ? e.message : "Could not load the register",
        ),
      );
  }, [date, branchId]);
  useEffect(load, [load]);

  const summary = useMemo(() => {
    const list = rows ?? [];
    return {
      in: list.filter((r) => r.attendance?.clock_in && !r.attendance.clock_out)
        .length,
      out: list.filter((r) => r.attendance?.clock_out).length,
      leave: list.filter(
        (r) =>
          r.attendance &&
          (r.attendance.status === "leave" || r.attendance.status === "absent"),
      ).length,
      missing: list.filter((r) => !r.attendance).length,
      flagged: list.filter(
        (r) =>
          r.attendance?.clock_in_evidence.geofence === "outside" ||
          r.attendance?.clock_out_evidence.geofence === "outside",
      ).length,
    };
  }, [rows]);

  return (
    <div className="space-y-6">
      <PageHeader
        title="Attendance"
        description="Who checked in and out, from where. Employees check in from the POS or from the Check in button at the top of this app."
      />

      <Card>
        <CardContent className="flex flex-wrap items-end gap-4 pt-6">
          <div className="flex flex-col gap-2">
            <Label htmlFor="att-date">Date</Label>
            <Input
              id="att-date"
              type="date"
              value={date}
              max={today()}
              onChange={(e) => setDate(e.target.value || today())}
              className="w-44"
            />
          </div>
          <div className="flex w-56 flex-col gap-2">
            <Label htmlFor="att-branch">Branch</Label>
            <SearchSelect
              id="att-branch"
              value={branchId}
              onChange={setBranchId}
              noneLabel="All branches"
              placeholder="All branches"
              options={branches.map((b) => ({
                value: b.branch_id,
                label: b.name,
              }))}
            />
          </div>
          <div className="flex flex-wrap gap-2 text-sm">
            <StatusChip status="" tone="ok" label={`${summary.in} in`} />
            <StatusChip
              status=""
              tone="muted"
              label={`${summary.out} checked out`}
            />
            <StatusChip
              status=""
              tone="info"
              label={`${summary.leave} leave/absent`}
            />
            <StatusChip
              status=""
              tone="warn"
              label={`${summary.missing} not checked in`}
            />
            {summary.flagged > 0 && (
              <StatusChip
                status=""
                tone="bad"
                label={`${summary.flagged} outside area`}
              />
            )}
          </div>
        </CardContent>
      </Card>

      {error && <p className="text-sm text-destructive">{error}</p>}

      <Card>
        <CardContent className="pt-6">
          {rows == null && !error ? (
            <p className="flex items-center gap-2 text-sm text-muted-foreground">
              <Loader2 className="size-4 animate-spin" aria-hidden /> Loading…
            </p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Employee</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Check-in sessions</TableHead>
                  <TableHead>Hours</TableHead>
                  <TableHead className="text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(rows ?? []).map((r) => {
                  const a = r.attendance;
                  return (
                    <TableRow key={r.user_id}>
                      <TableCell>
                        <div className="font-medium">{r.name}</div>
                        <div className="text-xs text-muted-foreground">
                          {[r.employee_code, r.designation]
                            .filter(Boolean)
                            .join(" · ")}
                          {a?.branch_name && ` · ${a.branch_name}`}
                        </div>
                      </TableCell>
                      <TableCell>
                        {a ? (
                          <div className="flex flex-col gap-1">
                            <StatusChip
                              status=""
                              tone={
                                a.status === "present"
                                  ? "ok"
                                  : a.status === "half_day"
                                    ? "info"
                                    : "muted"
                              }
                              label={STATUS_LABEL[a.status] ?? a.status}
                            />
                            {a.late_minutes ? (
                              <span className="text-xs text-warn">
                                {a.late_minutes} min late
                              </span>
                            ) : null}
                            {a.correction_reason && (
                              <span
                                className="text-xs text-muted-foreground"
                                title={a.correction_reason}
                              >
                                Edited by HR
                              </span>
                            )}
                          </div>
                        ) : (
                          <StatusChip
                            status=""
                            tone="warn"
                            label="Not checked in"
                          />
                        )}
                      </TableCell>
                      <TableCell className="min-w-72">
                        {a && a.sessions.length > 0 ? (
                          <ol className="flex flex-col gap-2">
                            {a.sessions.map((sn, i) => (
                              <li
                                key={sn.id}
                                className="flex flex-col gap-0.5 border-l-2 pl-2"
                              >
                                <div className="flex items-center gap-2 font-mono text-sm">
                                  <span className="text-xs text-muted-foreground">
                                    #{i + 1}
                                  </span>
                                  {time(sn.clock_in)} →{" "}
                                  {sn.clock_out ? (
                                    time(sn.clock_out)
                                  ) : (
                                    <span className="text-muted-foreground">
                                      still in
                                    </span>
                                  )}
                                  {sn.minutes != null && (
                                    <span className="text-xs text-muted-foreground">
                                      ({Math.floor(sn.minutes / 60)}h{" "}
                                      {sn.minutes % 60}m)
                                    </span>
                                  )}
                                  {allowed && (
                                    <button
                                      type="button"
                                      className="text-xs text-primary hover:underline"
                                      onClick={() =>
                                        setEditingSession({
                                          row: r,
                                          record: a,
                                          session: sn,
                                        })
                                      }
                                      aria-label={`Edit session ${i + 1} of ${r.name}`}
                                    >
                                      Edit
                                    </button>
                                  )}
                                </div>
                                <div className="flex flex-wrap gap-x-4">
                                  <Evidence ev={sn.clock_in_evidence} />
                                  {sn.clock_out && (
                                    <Evidence ev={sn.clock_out_evidence} />
                                  )}
                                </div>
                                {sn.correction_reason && (
                                  <span className="text-xs text-muted-foreground">
                                    Edited by HR: {sn.correction_reason}
                                  </span>
                                )}
                              </li>
                            ))}
                          </ol>
                        ) : (
                          <span className="text-sm text-muted-foreground">
                            —
                          </span>
                        )}
                        {a && allowed && (
                          <button
                            type="button"
                            className="mt-1 text-xs text-primary hover:underline"
                            onClick={() =>
                              setEditingSession({
                                row: r,
                                record: a,
                                session: null,
                              })
                            }
                          >
                            + Add session
                          </button>
                        )}
                      </TableCell>
                      <TableCell className="font-mono text-sm">
                        {a?.hours_worked ?? "—"}
                      </TableCell>
                      <TableCell className="text-right">
                        <Button
                          variant="outline"
                          size="sm"
                          disabled={!allowed}
                          title={
                            allowed
                              ? undefined
                              : missingPermissionHint("hr.manage")
                          }
                          onClick={() => setEditing({ row: r, record: a })}
                          aria-label={`${a ? "Correct" : "Mark"} attendance for ${r.name}`}
                        >
                          {a ? "Correct" : "Mark"}
                        </Button>
                      </TableCell>
                    </TableRow>
                  );
                })}
                {rows?.length === 0 && (
                  <TableRow>
                    <TableCell
                      colSpan={5}
                      className="py-6 text-center text-sm text-muted-foreground"
                    >
                      No employees for this branch
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      {editing && (
        <MarkDialog
          target={editing}
          date={date}
          onClose={() => setEditing(null)}
          onSaved={load}
        />
      )}
      {editingSession && (
        <SessionDialog
          target={editingSession}
          onClose={() => setEditingSession(null)}
          onSaved={load}
        />
      )}

      <SettingsCard allowed={allowed} />
      <GeofenceCard allowed={allowed} branches={branches} />
    </div>
  );
}

function MarkDialog({
  target,
  date,
  onClose,
  onSaved,
}: {
  target: EditTarget;
  date: string;
  onClose: () => void;
  onSaved: () => void;
}) {
  const rec = target.record;
  const workDate = rec?.work_date ?? date;
  const [status, setStatus] = useState<string>(rec?.status ?? "leave");
  const [inAt, setInAt] = useState(hhmmOf(rec?.clock_in));
  const [outAt, setOutAt] = useState(hhmmOf(rec?.clock_out));
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const perSession = (rec?.sessions.length ?? 0) > 1;

  async function save() {
    if (!reason.trim()) {
      setError("A reason is required — it's kept with the record.");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      if (rec) {
        const body: Record<string, string> = { status, reason: reason.trim() };
        if (!perSession && inAt !== hhmmOf(rec.clock_in))
          body.clock_in = toRFC3339(workDate, inAt) ?? "";
        if (!perSession && outAt !== hhmmOf(rec.clock_out))
          body.clock_out = toRFC3339(workDate, outAt) ?? "";
        await api.patch(`/api/v1/hr/attendance/${rec.id}`, body);
      } else {
        await api.post("/api/v1/hr/attendance/mark", {
          user_id: target.row.user_id,
          work_date: workDate,
          status,
          clock_in: toRFC3339(workDate, inAt),
          clock_out: toRFC3339(workDate, outAt),
          reason: reason.trim(),
        });
      }
      toast.success(`Attendance saved for ${target.row.name}`);
      onSaved();
      onClose();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not save attendance");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={`${rec ? "Correct" : "Mark"} attendance — ${target.row.name}`}
      description={`${workDate}. The original punches stay on record; this edit is logged with your name and reason.`}
      footer={
        <>
          <Button variant="outline" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={save} disabled={busy}>
            {busy && <Loader2 className="animate-spin" aria-hidden />}
            Save
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <fieldset className="flex flex-wrap gap-4 text-sm">
          <legend className="mb-2 text-sm font-medium">Status</legend>
          {Object.entries(STATUS_LABEL).map(([v, l]) => (
            <label key={v} className="flex items-center gap-2">
              <input
                type="radio"
                name="att-status"
                checked={status === v}
                onChange={() => setStatus(v)}
              />
              {l}
            </label>
          ))}
        </fieldset>
        {perSession ? (
          <p className="text-sm text-muted-foreground">
            This day has {rec?.sessions.length} check-in sessions — correct
            their times with Edit next to each session.
          </p>
        ) : (
          <div className="flex gap-4">
            <div className="flex flex-col gap-2">
              <Label htmlFor="att-in">Check in</Label>
              <Input
                id="att-in"
                type="time"
                value={inAt}
                onChange={(e) => setInAt(e.target.value)}
                className="w-32"
              />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="att-out">Check out</Label>
              <Input
                id="att-out"
                type="time"
                value={outAt}
                onChange={(e) => setOutAt(e.target.value)}
                className="w-32"
              />
            </div>
          </div>
        )}
        <div className="flex flex-col gap-2">
          <Label htmlFor="att-reason">Reason</Label>
          <Input
            id="att-reason"
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            placeholder="e.g. Forgot to check out; confirmed by manager"
          />
        </div>
        {error && <p className="text-sm text-destructive">{error}</p>}
      </div>
    </Modal>
  );
}

function SettingsCard({ allowed }: { allowed: boolean }) {
  const [saved, setSaved] = useState<AttendanceSettings | null>(null);
  const [draft, setDraft] = useState<AttendanceSettings | null>(null);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    api
      .get<AttendanceSettings>("/api/v1/hr/attendance/settings")
      .then((s) => {
        setSaved(s);
        setDraft(s);
      })
      .catch(() => {});
  }, []);

  if (!draft || !saved) return null;
  const dirty =
    JSON.stringify({ ...draft, updated_at: null }) !==
    JSON.stringify({ ...saved, updated_at: null });

  async function save() {
    if (!draft) return;
    setSaving(true);
    try {
      const s = await api.put<AttendanceSettings>(
        "/api/v1/hr/attendance/settings",
        draft,
      );
      setSaved(s);
      setDraft(s);
      toast.success("Attendance rules saved");
    } catch (e) {
      toast.error(
        e instanceof ApiError ? e.message : "Could not save the rules",
      );
    } finally {
      setSaving(false);
    }
  }

  return (
    <Card className="max-w-2xl">
      <CardHeader>
        <CardTitle>Check-in rules</CardTitle>
        <CardDescription>
          Apply to every branch. The IP address and device are always recorded.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-5">
        <fieldset className="space-y-2 text-sm" disabled={!allowed}>
          <legend className="mb-1 font-medium">
            Where employees can check in from
          </legend>
          <label className="flex items-center gap-3">
            <input
              type="checkbox"
              checked={draft.allow_pos}
              onChange={(e) =>
                setDraft({ ...draft, allow_pos: e.target.checked })
              }
            />
            POS terminals
          </label>
          <label className="flex items-center gap-3">
            <input
              type="checkbox"
              checked={draft.allow_web}
              onChange={(e) =>
                setDraft({ ...draft, allow_web: e.target.checked })
              }
            />
            This web app
          </label>
        </fieldset>
        <fieldset className="space-y-2 text-sm" disabled={!allowed}>
          <legend className="mb-1 font-medium">Location</legend>
          <label className="flex items-start gap-3">
            <input
              type="checkbox"
              className="mt-1"
              checked={draft.require_location}
              onChange={(e) =>
                setDraft({ ...draft, require_location: e.target.checked })
              }
            />
            <span>
              Require the device&apos;s location to check in or out
              <span className="block text-muted-foreground">
                A paired POS terminal counts as a location: it belongs to one
                branch.
              </span>
            </span>
          </label>
        </fieldset>
        <fieldset className="space-y-2 text-sm" disabled={!allowed}>
          <legend className="mb-1 font-medium">Outside the branch area</legend>
          {(
            [
              [
                "flag",
                "Allow, and flag it here (recommended)",
                "Indoor GPS is often off by a few hundred metres; the manager sees the distance and decides.",
              ],
              [
                "block",
                "Refuse the check-in",
                "The employee must be within the branch's area, with location on.",
              ],
              ["off", "Don't check the area", "Locations are still recorded."],
            ] as const
          ).map(([v, title, help]) => (
            <label key={v} className="flex items-start gap-3">
              <input
                type="radio"
                name="geofence-mode"
                className="mt-1"
                checked={draft.geofence_mode === v}
                onChange={() => setDraft({ ...draft, geofence_mode: v })}
              />
              <span>
                <span className="font-medium">{title}</span>
                <span className="block text-muted-foreground">{help}</span>
              </span>
            </label>
          ))}
        </fieldset>
        <Button onClick={save} disabled={!allowed || !dirty || saving}>
          {saving && <Loader2 className="animate-spin" aria-hidden />}
          Save rules
        </Button>
      </CardContent>
    </Card>
  );
}

function GeofenceCard({
  allowed,
  branches,
}: {
  allowed: boolean;
  branches: Branch[];
}) {
  const [fences, setFences] = useState<Geofence[]>([]);
  const [branchId, setBranchId] = useState("");
  const [lat, setLat] = useState("");
  const [lng, setLng] = useState("");
  const [radius, setRadius] = useState("200");
  const [enabled, setEnabled] = useState(true);
  const [busy, setBusy] = useState(false);
  const [locating, setLocating] = useState(false);

  const load = useCallback(() => {
    api
      .get<{ geofences: Geofence[] }>("/api/v1/hr/attendance/geofences")
      .then((d) => setFences(d.geofences))
      .catch(() => {});
  }, []);
  useEffect(load, [load]);

  function pick(id: string) {
    setBranchId(id);
    const g = fences.find((f) => f.branch_id === id);
    setLat(g ? String(g.latitude) : "");
    setLng(g ? String(g.longitude) : "");
    setRadius(g ? String(g.radius_m) : "200");
    setEnabled(g ? g.enabled : true);
  }

  async function useHere() {
    setLocating(true);
    const r = await getFix();
    setLocating(false);
    if (!r.fix) {
      toast.error(r.reason);
      return;
    }
    setLat(r.fix.latitude.toFixed(6));
    setLng(r.fix.longitude.toFixed(6));
  }

  async function save() {
    setBusy(true);
    try {
      await api.put(`/api/v1/hr/attendance/geofences/${branchId}`, {
        latitude: Number(lat),
        longitude: Number(lng),
        radius_m: Number(radius),
        enabled,
      });
      toast.success("Branch area saved");
      load();
    } catch (e) {
      toast.error(
        e instanceof ApiError ? e.message : "Could not save the branch area",
      );
    } finally {
      setBusy(false);
    }
  }

  const valid =
    branchId &&
    lat !== "" &&
    lng !== "" &&
    Number(radius) >= 10 &&
    Number(radius) <= 5000;

  return (
    <Card className="max-w-3xl">
      <CardHeader>
        <CardTitle>Branch areas (geofences)</CardTitle>
        <CardDescription>
          The point and radius a check-in is measured against. Stand at the
          branch and use “My current location”, or paste coordinates from a map.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex flex-wrap items-end gap-3">
          <div className="flex w-56 flex-col gap-2">
            <Label htmlFor="gf-branch">Branch</Label>
            <SearchSelect
              id="gf-branch"
              value={branchId}
              onChange={pick}
              placeholder="Choose a branch"
              options={branches.map((b) => ({
                value: b.branch_id,
                label: b.name,
              }))}
              disabled={!allowed}
            />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="gf-lat">Latitude</Label>
            <Input
              id="gf-lat"
              inputMode="decimal"
              value={lat}
              onChange={(e) => setLat(e.target.value)}
              className="w-32"
              disabled={!allowed}
            />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="gf-lng">Longitude</Label>
            <Input
              id="gf-lng"
              inputMode="decimal"
              value={lng}
              onChange={(e) => setLng(e.target.value)}
              className="w-32"
              disabled={!allowed}
            />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="gf-radius">Radius (m)</Label>
            <Input
              id="gf-radius"
              type="number"
              min={10}
              max={5000}
              value={radius}
              onChange={(e) => setRadius(e.target.value)}
              className="w-24"
              disabled={!allowed}
            />
          </div>
          <label className="flex items-center gap-2 pb-2 text-sm">
            <input
              type="checkbox"
              checked={enabled}
              onChange={(e) => setEnabled(e.target.checked)}
              disabled={!allowed}
            />
            On
          </label>
        </div>
        <div className="flex gap-2">
          <Button
            variant="outline"
            onClick={useHere}
            disabled={!allowed || locating}
          >
            {locating ? (
              <Loader2 className="animate-spin" aria-hidden />
            ) : (
              <MapPin aria-hidden />
            )}
            My current location
          </Button>
          <Button onClick={save} disabled={!allowed || !valid || busy}>
            {busy && <Loader2 className="animate-spin" aria-hidden />}
            Save area
          </Button>
        </div>
        {fences.length > 0 && (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Branch</TableHead>
                <TableHead>Point</TableHead>
                <TableHead>Radius</TableHead>
                <TableHead>Status</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {fences.map((g) => (
                <TableRow key={g.branch_id}>
                  <TableCell>{g.branch_name}</TableCell>
                  <TableCell>
                    <a
                      className="text-primary hover:underline"
                      href={mapsLink(g.latitude, g.longitude)}
                      target="_blank"
                      rel="noreferrer"
                    >
                      {g.latitude.toFixed(5)}, {g.longitude.toFixed(5)}
                    </a>
                  </TableCell>
                  <TableCell>{g.radius_m} m</TableCell>
                  <TableCell>
                    <StatusChip
                      status=""
                      tone={g.enabled ? "ok" : "muted"}
                      label={g.enabled ? "On" : "Off"}
                    />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}

// Correct one check-in session, or add one the employee forgot to punch.
function SessionDialog({
  target,
  onClose,
  onSaved,
}: {
  target: SessionTarget;
  onClose: () => void;
  onSaved: () => void;
}) {
  const sn = target.session;
  const day = target.record.work_date;
  const [inAt, setInAt] = useState(hhmmOf(sn?.clock_in));
  const [outAt, setOutAt] = useState(hhmmOf(sn?.clock_out));
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function save() {
    if (!inAt) {
      setError("The check-in time is required.");
      return;
    }
    if (!reason.trim()) {
      setError("A reason is required — it's kept with the session.");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const body = {
        clock_in: toRFC3339(day, inAt),
        clock_out: outAt ? toRFC3339(day, outAt) : "",
        reason: reason.trim(),
      };
      if (sn) await api.patch(`/api/v1/hr/attendance/sessions/${sn.id}`, body);
      else
        await api.post(
          `/api/v1/hr/attendance/${target.record.id}/sessions`,
          body,
        );
      toast.success(`Session saved for ${target.row.name}`);
      onSaved();
      onClose();
    } catch (e) {
      setError(
        e instanceof ApiError ? e.message : "Could not save the session",
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={`${sn ? "Correct session" : "Add session"} — ${target.row.name}`}
      description={`${day}. Recorded locations and IPs stay on the session; this edit is logged with your name and reason.`}
      footer={
        <>
          <Button variant="outline" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={save} disabled={busy}>
            {busy && <Loader2 className="animate-spin" aria-hidden />}
            Save
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <div className="flex gap-4">
          <div className="flex flex-col gap-2">
            <Label htmlFor="sess-in">Check in</Label>
            <Input
              id="sess-in"
              type="time"
              value={inAt}
              onChange={(e) => setInAt(e.target.value)}
              className="w-32"
            />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="sess-out">Check out</Label>
            <Input
              id="sess-out"
              type="time"
              value={outAt}
              onChange={(e) => setOutAt(e.target.value)}
              className="w-32"
            />
          </div>
        </div>
        <div className="flex flex-col gap-2">
          <Label htmlFor="sess-reason">Reason</Label>
          <Input
            id="sess-reason"
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            placeholder="e.g. Went out for lunch without checking out"
          />
        </div>
        {error && <p className="text-sm text-destructive">{error}</p>}
      </div>
    </Modal>
  );
}
