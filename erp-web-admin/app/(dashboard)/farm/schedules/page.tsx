"use client";

// Vaccination Schedules (erp-core-go §3.81): plans by age — the standard
// broiler and layer chicken plans (a common practice; confirm with your
// vet) and any the farm makes. A batch follows one; its due dates come
// from its placement and age. farm.record (editing: farm.manage).
import { FormEvent, useCallback, useEffect, useState } from "react";
import { Loader2, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api-client";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import type { FarmSchedule, FarmSpecies } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Modal } from "@/components/ui/dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { PageHeader } from "@/components/data/page-header";
import { Field, Panel, selectClass } from "@/components/data/panel";
import { StatusChip } from "@/components/data/status-chip";
import { PURPOSES, PURPOSE_LABEL, SPECIES_LABEL, message } from "@/components/farm/farm";

export default function SchedulesPage() {
  const { can } = usePermissions();
  const [list, setList] = useState<FarmSchedule[]>([]);
  const [editing, setEditing] = useState<FarmSchedule | "new" | null>(null);
  const load = useCallback(() => {
    api
      .get<{ schedules: FarmSchedule[] }>("/api/v1/farm/schedules")
      .then((d) => setList(d.schedules))
      .catch((e) => toast.error(message(e, "Could not load the schedules")));
  }, []);
  useEffect(load, [load]);
  if (!can("farm.record")) return <p className="text-sm text-muted-foreground">{missingPermissionHint("farm.record")}</p>;
  return (
    <div className="space-y-5">
      <PageHeader
        title="Vaccination Schedules"
        description="Vaccinations and medicines by age. The standard chicken plans are common practice — confirm them with your vet."
        actions={
          <Button onClick={() => setEditing("new")} disabled={!can("farm.manage")}>
            <Plus aria-hidden /> New schedule
          </Button>
        }
      />
      {list.map((s) => (
        <Panel
          key={s.schedule_id}
          title={`${s.name}`}
          description={`${SPECIES_LABEL[s.species]}${s.purpose ? ` · ${PURPOSE_LABEL[s.purpose] ?? s.purpose}` : ""}${s.is_system ? " · standard" : ""}`}
          actions={
            <div className="flex items-center gap-2">
              <StatusChip status="" tone={s.status === "active" ? "ok" : "muted"} label={s.status === "active" ? "Active" : "Inactive"} />
              <Button size="sm" variant="ghost" disabled={!can("farm.manage")} onClick={() => setEditing(s)} aria-label={`Edit ${s.name}`}>
                Edit
              </Button>
            </div>
          }
        >
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-20">Day</TableHead>
                <TableHead>What</TableHead>
                <TableHead>How</TableHead>
                <TableHead>Repeats</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {s.items.map((it) => (
                <TableRow key={it.schedule_item_id}>
                  <TableCell className="tabular">{it.day_of_age}</TableCell>
                  <TableCell className="text-sm">{it.name}</TableCell>
                  <TableCell className="text-sm text-muted-foreground">{it.method ?? "—"}</TableCell>
                  <TableCell className="text-sm text-muted-foreground">{it.repeat_every_days ? `Every ${it.repeat_every_days} days` : "Once"}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Panel>
      ))}
      {editing && <ScheduleDialog schedule={editing === "new" ? null : editing} onClose={() => setEditing(null)} onSaved={load} />}
    </div>
  );
}

type Row = { day: string; name: string; method: string; repeat: string };

function ScheduleDialog({ schedule, onClose, onSaved }: { schedule: FarmSchedule | null; onClose: () => void; onSaved: () => void }) {
  const [species, setSpecies] = useState<FarmSpecies>(schedule?.species ?? "chicken");
  const [purpose, setPurpose] = useState(schedule?.purpose ?? "");
  const [name, setName] = useState(schedule?.name ?? "");
  const [status, setStatus] = useState(schedule?.status ?? "active");
  const [rows, setRows] = useState<Row[]>(
    schedule?.items.map((i) => ({ day: String(i.day_of_age), name: i.name, method: i.method ?? "", repeat: i.repeat_every_days ? String(i.repeat_every_days) : "" })) ?? [
      { day: "", name: "", method: "", repeat: "" },
    ],
  );
  const [busy, setBusy] = useState(false);
  const set = (i: number, k: keyof Row, v: string) => setRows((r) => r.map((x, j) => (j === i ? { ...x, [k]: v } : x)));
  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    const items = rows
      .filter((r) => r.name.trim())
      .map((r) => ({ day_of_age: Number(r.day), name: r.name, method: r.method || undefined, repeat_every_days: r.repeat.trim() ? Number(r.repeat) : undefined }));
    try {
      if (schedule) await api.put(`/api/v1/farm/schedules/${schedule.schedule_id}`, { name, purpose, status, items });
      else await api.post("/api/v1/farm/schedules", { species, purpose, name, items });
      toast.success(`${name} saved`);
      onSaved();
      onClose();
    } catch (err) {
      toast.error(message(err, "Could not save the schedule"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={schedule ? `Edit ${schedule.name}` : "New schedule"}
      width={640}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="farm-schedule" disabled={busy || !name.trim()}>
            {busy && <Loader2 className="animate-spin" aria-hidden />} Save
          </Button>
        </>
      }
    >
      <form id="farm-schedule" onSubmit={save} className="space-y-3">
        <div className="grid grid-cols-3 gap-3">
          <Field label="Species" htmlFor="fs-species">
            <select id="fs-species" value={species} disabled={!!schedule} onChange={(e) => setSpecies(e.target.value as FarmSpecies)} className={`${selectClass} w-full`}>
              {Object.entries(SPECIES_LABEL).map(([k, v]) => (
                <option key={k} value={k}>
                  {v}
                </option>
              ))}
            </select>
          </Field>
          <Field label="For" htmlFor="fs-purpose">
            <select id="fs-purpose" value={purpose} onChange={(e) => setPurpose(e.target.value)} className={`${selectClass} w-full`}>
              <option value="">Any purpose</option>
              {PURPOSES[species].map((p) => (
                <option key={p} value={p}>
                  {PURPOSE_LABEL[p] ?? p}
                </option>
              ))}
            </select>
          </Field>
          {schedule && (
            <Field label="Status" htmlFor="fs-status">
              <select id="fs-status" value={status} onChange={(e) => setStatus(e.target.value as "active" | "inactive")} className={`${selectClass} w-full`}>
                <option value="active">Active</option>
                <option value="inactive">Inactive</option>
              </select>
            </Field>
          )}
        </div>
        <Field label="Name *" htmlFor="fs-name">
          <Input id="fs-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="Our broiler plan" />
        </Field>
        <div className="space-y-2">
          {rows.map((r, i) => (
            <div key={i} className="grid grid-cols-[80px_1fr_1fr_96px_auto] items-end gap-2">
              <Field label={i === 0 ? "Day" : ""} htmlFor={`fs-day-${i}`}>
                <Input id={`fs-day-${i}`} type="number" min="0" value={r.day} onChange={(e) => set(i, "day", e.target.value)} aria-label={`Day of item ${i + 1}`} />
              </Field>
              <Field label={i === 0 ? "Vaccine / medicine" : ""} htmlFor={`fs-name-${i}`}>
                <Input id={`fs-name-${i}`} value={r.name} onChange={(e) => set(i, "name", e.target.value)} aria-label={`Name of item ${i + 1}`} />
              </Field>
              <Field label={i === 0 ? "How" : ""} htmlFor={`fs-method-${i}`}>
                <Input id={`fs-method-${i}`} value={r.method} onChange={(e) => set(i, "method", e.target.value)} aria-label={`Method of item ${i + 1}`} />
              </Field>
              <Field label={i === 0 ? "Every (days)" : ""} htmlFor={`fs-repeat-${i}`}>
                <Input
                  id={`fs-repeat-${i}`}
                  type="number"
                  min="1"
                  value={r.repeat}
                  placeholder="once"
                  onChange={(e) => set(i, "repeat", e.target.value)}
                  aria-label={`Repeat of item ${i + 1}`}
                />
              </Field>
              <Button type="button" size="icon-sm" variant="ghost" aria-label={`Remove item ${i + 1}`} onClick={() => setRows((x) => x.filter((_, j) => j !== i))}>
                <Trash2 />
              </Button>
            </div>
          ))}
          <Button type="button" size="sm" variant="outline" onClick={() => setRows((x) => [...x, { day: "", name: "", method: "", repeat: "" }])}>
            <Plus aria-hidden /> Add an item
          </Button>
        </div>
      </form>
    </Modal>
  );
}
