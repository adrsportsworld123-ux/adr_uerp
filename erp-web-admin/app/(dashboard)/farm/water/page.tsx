"use client";

// Water Quality (erp-core-go §3.82): a fish pond or tank's readings —
// dissolved oxygen, pH, temperature, ammonia, nitrite, transparency — with
// readings outside the usual warm-water range flagged. farm.record;
// removing a wrong reading: farm.manage.
import { FormEvent, useCallback, useEffect, useState } from "react";
import { Loader2, Plus } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api-client";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import type { FarmHouse, FarmWaterLog } from "@/lib/types";
import { localDate, shortDate } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Modal } from "@/components/ui/dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { PageHeader } from "@/components/data/page-header";
import { Field, FilterBar, Panel, selectClass } from "@/components/data/panel";
import { message } from "@/components/farm/farm";

const READINGS = [
  ["do_mg_l", "Dissolved oxygen (mg/L)"],
  ["ph", "pH"],
  ["temp_c", "Temperature (°C)"],
  ["ammonia_mg_l", "Ammonia (mg/L)"],
  ["nitrite_mg_l", "Nitrite (mg/L)"],
  ["transparency_cm", "Transparency (cm)"],
] as const;
type ReadingKey = (typeof READINGS)[number][0];

export default function WaterPage() {
  const { can } = usePermissions();
  const [ponds, setPonds] = useState<FarmHouse[]>([]);
  const [houseId, setHouseId] = useState("");
  const [from, setFrom] = useState(() => localDate(-14));
  const [logs, setLogs] = useState<FarmWaterLog[] | null>(null);
  const [adding, setAdding] = useState(false);
  useEffect(() => {
    api
      .get<{ houses: FarmHouse[] }>("/api/v1/farm/houses?status=active")
      .then((d) => setPonds(d.houses.filter((h) => h.kind === "pond" || h.kind === "tank" || h.species === "fish")))
      .catch(() => setPonds([]));
  }, []);
  const load = useCallback(() => {
    const qs = new URLSearchParams({ from, ...(houseId && { house_id: houseId }) });
    api
      .get<{ logs: FarmWaterLog[] }>(`/api/v1/farm/water?${qs}`)
      .then((d) => setLogs(d.logs))
      .catch((e) => toast.error(message(e, "Could not load the water log")));
  }, [houseId, from]);
  useEffect(load, [load]);
  async function remove(l: FarmWaterLog) {
    if (!window.confirm(`Remove the ${shortDate(l.logged_on)} reading of ${l.house}?`)) return;
    try {
      await api.delete(`/api/v1/farm/water/${l.water_log_id}`);
      load();
    } catch (e) {
      toast.error(message(e, "Could not remove it"));
    }
  }
  if (!can("farm.record")) return <p className="text-sm text-muted-foreground">{missingPermissionHint("farm.record")}</p>;
  return (
    <div className="max-w-5xl space-y-5">
      <PageHeader
        title="Water Quality"
        description="Pond and tank readings. Flagged: oxygen under 4 mg/L, pH outside 6.5–8.5, ammonia over 0.5 mg/L and other readings outside the usual warm-water range."
        actions={
          <Button onClick={() => setAdding(true)} disabled={ponds.length === 0}>
            <Plus aria-hidden /> New reading
          </Button>
        }
      />
      <FilterBar>
        <Field label="Pond or tank" htmlFor="wq-house">
          <select id="wq-house" value={houseId} onChange={(e) => setHouseId(e.target.value)} className={`${selectClass} w-56`}>
            <option value="">Every pond</option>
            {ponds.map((p) => (
              <option key={p.house_id} value={p.house_id}>
                {p.name} · {p.branch}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Since" htmlFor="wq-from">
          <Input id="wq-from" type="date" value={from} max={localDate()} onChange={(e) => setFrom(e.target.value)} />
        </Field>
      </FilterBar>
      <Panel title="Readings">
        <Table data-testid="water-log">
          <TableHeader>
            <TableRow>
              <TableHead>When</TableHead>
              <TableHead>Pond</TableHead>
              {READINGS.map(([k, l]) => (
                <TableHead key={k} className="text-right">
                  {l.replace(/ \(.*\)/, "")}
                </TableHead>
              ))}
              <TableHead>Flags</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {logs?.length === 0 && (
              <TableRow>
                <TableCell colSpan={READINGS.length + 4} className="py-5 text-center text-sm text-muted-foreground">
                  No readings in this time.
                </TableCell>
              </TableRow>
            )}
            {logs?.map((l) => (
              <TableRow key={l.water_log_id}>
                <TableCell className="whitespace-nowrap text-sm">
                  {shortDate(l.logged_on)}
                  {l.logged_time && <span className="block text-xs text-muted-foreground">{l.logged_time}</span>}
                </TableCell>
                <TableCell className="text-sm">{l.house}</TableCell>
                {READINGS.map(([k]) => (
                  <TableCell key={k} className="text-right tabular">
                    {l[k] ?? "—"}
                  </TableCell>
                ))}
                <TableCell className="text-xs">{l.alerts.length ? <span className="text-destructive">{l.alerts.join(" · ")}</span> : <span className="text-pay">In range</span>}</TableCell>
                <TableCell className="text-right">
                  {can("farm.manage") && (
                    <Button size="sm" variant="ghost" onClick={() => remove(l)} aria-label={`Remove reading of ${l.logged_on}`}>
                      Remove
                    </Button>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </Panel>
      {adding && <ReadingDialog ponds={ponds} houseId={houseId} onClose={() => setAdding(false)} onSaved={load} />}
    </div>
  );
}

function ReadingDialog({ ponds, houseId, onClose, onSaved }: { ponds: FarmHouse[]; houseId: string; onClose: () => void; onSaved: () => void }) {
  const [pond, setPond] = useState(houseId || ponds[0]?.house_id || "");
  const [date, setDate] = useState(() => localDate());
  const [time, setTime] = useState("");
  const [values, setValues] = useState<Partial<Record<ReadingKey, string>>>({});
  const [notes, setNotes] = useState("");
  const [busy, setBusy] = useState(false);
  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    const body: Record<string, unknown> = { house_id: pond, logged_on: date, logged_time: time || undefined, notes: notes || undefined };
    for (const [k] of READINGS) if (values[k]?.trim()) body[k] = Number(values[k]);
    try {
      const l = await api.post<FarmWaterLog>("/api/v1/farm/water", body);
      if (l.alerts.length) toast.warning(`Recorded — ${l.alerts.join(" · ")}`);
      else toast.success("Recorded — in range");
      onSaved();
      onClose();
    } catch (err) {
      toast.error(message(err, "Could not record it"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title="New water reading"
      width={560}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="water-reading" disabled={busy || !pond}>
            {busy && <Loader2 className="animate-spin" aria-hidden />} Record
          </Button>
        </>
      }
    >
      <form id="water-reading" onSubmit={save} className="grid grid-cols-3 gap-3">
        <Field label="Pond" htmlFor="wr-pond">
          <select id="wr-pond" value={pond} onChange={(e) => setPond(e.target.value)} className={`${selectClass} w-full`}>
            {ponds.map((p) => (
              <option key={p.house_id} value={p.house_id}>
                {p.name}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Date" htmlFor="wr-date">
          <Input id="wr-date" type="date" value={date} max={localDate()} onChange={(e) => setDate(e.target.value)} />
        </Field>
        <Field label="Time" htmlFor="wr-time">
          <Input id="wr-time" type="time" value={time} onChange={(e) => setTime(e.target.value)} />
        </Field>
        {READINGS.map(([k, l]) => (
          <Field key={k} label={l} htmlFor={`wr-${k}`}>
            <Input id={`wr-${k}`} type="number" min="0" step="any" value={values[k] ?? ""} onChange={(e) => setValues((v) => ({ ...v, [k]: e.target.value }))} />
          </Field>
        ))}
        <Field label="Notes" htmlFor="wr-notes" className="col-span-3">
          <Input id="wr-notes" value={notes} onChange={(e) => setNotes(e.target.value)} placeholder="Aerator on, water exchanged…" />
        </Field>
      </form>
    </Modal>
  );
}
