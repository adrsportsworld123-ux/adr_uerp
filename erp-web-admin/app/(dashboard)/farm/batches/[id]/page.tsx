"use client";

// One flock, herd or pond (erp-core-go §3.81–§3.82): its figures, today's
// ration and feed against plan, open withdrawals, its animals on the
// register, its schedule (record a vaccination from it; a repeating item
// shows its next date), its log (cancel an entry: its stock and its
// ledger entry go back), and closing it (what is left in Livestock WIP
// goes to Farm Losses). farm.record; cancel and close: farm.manage.
import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { useParams } from "next/navigation";
import { Loader2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api-client";
import { usePermissions } from "@/lib/permissions";
import type { FarmAnimal, FarmBatch, FarmDue, FarmEvent, FarmEventType } from "@/lib/types";
import { localDate, rupees, shortDate } from "@/lib/format";
import { Button, buttonVariants } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Modal } from "@/components/ui/dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { PageHeader } from "@/components/data/page-header";
import { Field, Panel, Stat } from "@/components/data/panel";
import { StatusChip, Tone } from "@/components/data/status-chip";
import { EVENT_LABEL, EventDialog, PURPOSE_LABEL, RECORDABLE, REGISTER_SPECIES, SPECIES_LABEL, message, withdrawalText } from "@/components/farm/farm";
import { AnimalTable } from "@/components/farm/animals";

type Detail = FarmBatch & { schedule_items: FarmDue[]; events: FarmEvent[] };

const DUE_TONE: Record<FarmDue["state"], Tone> = {
  done: "ok",
  overdue: "bad",
  due_today: "warn",
  upcoming: "info",
  before_placement: "muted",
  not_done: "muted",
};
const DUE_LABEL: Record<FarmDue["state"], string> = {
  done: "Done",
  overdue: "Overdue",
  due_today: "Today",
  upcoming: "Coming",
  before_placement: "Before placement",
  not_done: "Not done",
};

export default function BatchPage() {
  const { id } = useParams<{ id: string }>();
  const { can } = usePermissions();
  const [b, setB] = useState<Detail | null>(null);
  const [recording, setRecording] = useState<{ type: FarmEventType; due?: FarmDue } | null>(null);
  const [cancelling, setCancelling] = useState<FarmEvent | null>(null);
  const [closing, setClosing] = useState(false);
  const [animals, setAnimals] = useState<FarmAnimal[]>([]);
  const load = useCallback(() => {
    api
      .get<Detail>(`/api/v1/farm/batches/${id}`)
      .then((d) => {
        setB(d);
        if (REGISTER_SPECIES.includes(d.species)) {
          api
            .get<{ animals: FarmAnimal[] }>(`/api/v1/farm/animals?batch_id=${d.batch_id}&status=all`)
            .then((a) => setAnimals(a.animals))
            .catch(() => setAnimals([]));
        }
      })
      .catch((e) => toast.error(message(e, "Could not load the batch")));
  }, [id]);
  useEffect(load, [load]);
  if (!b) return <p className="text-sm text-muted-foreground">Loading…</p>;
  const k = b.kpi;
  const active = b.status === "active";
  const layer = b.purpose === "layer" || b.purpose === "breeder";
  const register = REGISTER_SPECIES.includes(b.species);
  const withdrawals = withdrawalText(k.withdrawal, shortDate);
  const dairy = (b.species === "cow" || b.species === "goat") && register;

  return (
    <div className="max-w-5xl space-y-5">
      <Link href="/farm/batches" className="text-sm text-muted-foreground hover:underline">
        ← Flocks & Herds
      </Link>
      <PageHeader
        title={`${b.batch_number}${b.name ? ` · ${b.name}` : ""}`}
        description={`${SPECIES_LABEL[b.species]} · ${PURPOSE_LABEL[b.purpose] ?? b.purpose}${b.breed ? ` · ${b.breed}` : ""} · ${[b.house, b.branch].filter(Boolean).join(", ")} · placed ${shortDate(b.placed_on)}${
          b.status === "closed" ? ` · closed ${b.closed_on ? shortDate(b.closed_on) : ""}` : ""
        }`}
        actions={
          active && (
            <div className="flex gap-2">
              {dairy && (
                <Link href={`/farm/milk?batch=${b.batch_id}`} className={buttonVariants({ variant: "outline" })}>
                  Milk recording
                </Link>
              )}
              <Button variant="outline" disabled={!can("farm.manage")} onClick={() => setClosing(true)}>
                Close batch
              </Button>
            </div>
          )
        }
      />
      {(withdrawals.length > 0 || k.animals_in_withdrawal > 0) && (
        <div role="status" className="rounded-md border border-warn/40 bg-warn-soft px-3 py-2 text-sm" data-testid="batch-withdrawal">
          Withdrawal: {[...withdrawals, ...(k.animals_in_withdrawal > 0 ? [`${k.animals_in_withdrawal} animal(s) treated`] : [])].join(" · ")}
        </div>
      )}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4" data-testid="batch-kpis">
        <Stat label="Age" value={`${k.age_days} days`} />
        <Stat
          label="Alive"
          value={`${k.alive.toLocaleString("en-IN")} of ${(b.initial_count + k.born + k.added).toLocaleString("en-IN")}`}
          strong
        />
        <Stat label="Mortality" value={`${k.mortality_pct}% (${k.dead})`} testId="kpi-mortality" />
        <Stat label="Feed" value={`${k.feed_kg} kg${k.feed_per_head_kg != null ? ` · ${k.feed_per_head_kg} kg/head` : ""}`} />
        <Stat label="Average weight" value={k.latest_avg_weight_kg != null ? `${k.latest_avg_weight_kg} kg (${k.latest_weighed_on ? shortDate(k.latest_weighed_on) : ""})` : "—"} />
        <Stat label="FCR" value={k.fcr ?? "—"} testId="kpi-fcr" />
        {layer ? (
          <Stat label="Eggs" value={`${k.eggs_today} today · ${k.laying_7d_pct ?? 0}% laying`} testId="kpi-eggs" />
        ) : (
          <Stat label="Harvested" value={`${k.harvested} · ${k.harvested_kg} kg`} />
        )}
        <Stat label="Cost per head" value={k.cost_per_head != null ? rupees(k.cost_per_head) : "—"} />
        {(k.born > 0 || k.added > 0 || k.moved_out > 0) && <Stat label="Born · added · moved out" value={`${k.born} · ${k.added} · ${k.moved_out}`} />}
        {register && <Stat label="On the register" value={k.on_register} testId="kpi-register" />}
        {b.species === "fish" && <Stat label="Biomass" value={k.biomass_kg != null ? `${k.biomass_kg} kg` : "—"} />}
        {k.milk_total_l > 0 && <Stat label="Milk" value={`${k.milk_total_l} L in all`} />}
      </div>
      {b.feed_plan && (
        <Panel title={`Feed · ${b.feed_plan}`} description={active ? "Today's ration from the plan, and what was fed against it so far." : "What was fed against the plan."}>
          <div className="grid gap-4 sm:grid-cols-2" data-testid="batch-feed">
            <ul className="space-y-1 text-sm">
              {k.feed_today.length === 0 && <li className="text-muted-foreground">Nothing planned for day {k.age_days}.</li>}
              {k.feed_today.map((r, i) => (
                <li key={i} className="flex justify-between gap-3">
                  <span>
                    {r.feed_name}
                    {r.product && <span className="text-xs text-muted-foreground"> · {r.product}</span>}
                  </span>
                  <span className="tabular">{r.kg != null ? `${r.kg} kg` : "weigh a sample first"}</span>
                </li>
              ))}
            </ul>
            {k.feed_planned_kg != null && (
              <p className="text-sm">
                Fed <span className="tabular font-medium">{k.feed_kg} kg</span> of <span className="tabular">{k.feed_planned_kg} kg</span> planned so far
                {k.feed_vs_plan_pct != null && (
                  <span className={k.feed_vs_plan_pct < 85 || k.feed_vs_plan_pct > 115 ? " text-warn" : ""}> ({k.feed_vs_plan_pct}%)</span>
                )}
                .
              </p>
            )}
          </div>
        </Panel>
      )}
      <Panel title="Cost" description="Animals, feed and health from stock, other costs; less what went into stock as produce.">
        <dl className="grid grid-cols-2 gap-x-6 gap-y-1 text-sm sm:grid-cols-3" data-testid="batch-cost">
          {(
            [
              ["Animals placed", k.stock_cost],
              ["Feed", k.feed_cost],
              ["Medicine & vaccines", k.health_cost],
              ["Other costs", k.other_cost],
              ["Produce out", -k.output_value],
              ["Net cost", k.net_cost],
            ] as [string, number][]
          ).map(([l, v]) => (
            <div key={l} className="flex justify-between gap-3">
              <dt className="text-muted-foreground">{l}</dt>
              <dd className={`tabular ${l === "Net cost" ? "font-semibold" : ""}`}>{rupees(v)}</dd>
            </div>
          ))}
        </dl>
        {k.outputs.length > 0 && (
          <p className="mt-2 text-xs text-muted-foreground">Produce: {k.outputs.map((o) => `${o.product} ${o.quantity} ${o.unit}`).join(" · ")}</p>
        )}
      </Panel>
      {active && (
        <div className="flex flex-wrap gap-2" role="group" aria-label="Record">
          {RECORDABLE.map((t) => (
            <Button key={t} size="sm" variant={t === "mortality" || t === "feed" ? "default" : "outline"} disabled={!can("farm.record")} onClick={() => setRecording({ type: t })}>
              {EVENT_LABEL[t]}
            </Button>
          ))}
        </div>
      )}
      {b.schedule_items.length > 0 && (
        <Panel title={`Schedule · ${b.schedule ?? ""}`}>
          <Table data-testid="batch-schedule">
            <TableHeader>
              <TableRow>
                <TableHead>Day</TableHead>
                <TableHead>What</TableHead>
                <TableHead>Due</TableHead>
                <TableHead>State</TableHead>
                <TableHead className="text-right">Action</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {b.schedule_items.map((d) => (
                <TableRow key={d.schedule_item_id}>
                  <TableCell className="tabular">{d.day_of_age}</TableCell>
                  <TableCell className="text-sm">
                    {d.name}
                    {(d.method || d.repeat_every_days) && (
                      <span className="block text-xs text-muted-foreground">
                        {[d.method, d.repeat_every_days ? `every ${d.repeat_every_days} days` : ""].filter(Boolean).join(" · ")}
                      </span>
                    )}
                  </TableCell>
                  <TableCell className="text-sm">{shortDate(d.due_on)}</TableCell>
                  <TableCell>
                    <StatusChip
                      status=""
                      tone={DUE_TONE[d.state]}
                      label={d.done_on && !d.repeat_every_days ? `Done ${shortDate(d.done_on)}` : DUE_LABEL[d.state]}
                    />
                    {d.repeat_every_days && d.done_on && <span className="block text-xs text-muted-foreground">Last {shortDate(d.done_on)}</span>}
                  </TableCell>
                  <TableCell className="text-right">
                    {active && (d.state === "overdue" || d.state === "due_today" || d.state === "upcoming") && (
                      <Button size="sm" variant="outline" onClick={() => setRecording({ type: "vaccination", due: d })} aria-label={`Record ${d.name}`}>
                        Record
                      </Button>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Panel>
      )}
      {register && (
        <Panel
          title={`Animals (${animals.filter((a) => a.status === "active").length})`}
          description="The herd's animals on the register; open one to record breeding, milk or how it left."
          actions={
            <Link href={`/farm/animals?batch=${b.batch_id}`} className={buttonVariants({ size: "sm", variant: "outline" })}>
              Add or find animals
            </Link>
          }
        >
          <AnimalTable animals={animals} />
        </Panel>
      )}
      <Panel title="Log">
        <Table data-testid="batch-log">
          <TableHeader>
            <TableRow>
              <TableHead>Date</TableHead>
              <TableHead>Event</TableHead>
              <TableHead>Details</TableHead>
              <TableHead className="text-right">Cost / value</TableHead>
              <TableHead className="text-right">Action</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {b.events.map((e) => (
              <TableRow key={e.event_id} className={e.status === "cancelled" ? "opacity-60" : undefined}>
                <TableCell className="whitespace-nowrap text-sm">{shortDate(e.event_date)}</TableCell>
                <TableCell className="text-sm">
                  {EVENT_LABEL[e.event_type]}
                  {e.animal_tag && <span className="block text-xs font-medium">{e.animal_tag}</span>}
                  {e.status === "cancelled" && <span className="block text-xs text-destructive">Cancelled: {e.cancel_reason}</span>}
                </TableCell>
                <TableCell className="max-w-96 whitespace-normal text-sm">{describe(e)}</TableCell>
                <TableCell className="tabular text-right">{e.amount != null ? rupees(e.amount) : "—"}</TableCell>
                <TableCell className="text-right">
                  {active && e.status === "posted" && !["placement", "transfer_in", "transfer_out"].includes(e.event_type) && can("farm.manage") && (
                    <Button size="sm" variant="ghost" className="text-destructive hover:text-destructive" onClick={() => setCancelling(e)} aria-label={`Cancel ${EVENT_LABEL[e.event_type]} of ${e.event_date}`}>
                      Cancel
                    </Button>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </Panel>
      {recording && <EventDialog batch={b} type={recording.type} due={recording.due} onClose={() => setRecording(null)} onSaved={load} />}
      {cancelling && <CancelDialog event={cancelling} onClose={() => setCancelling(null)} onDone={load} />}
      {closing && <CloseDialog batch={b} onClose={() => setClosing(false)} onDone={load} />}
    </div>
  );
}

function describe(e: FarmEvent): string {
  const parts: string[] = [];
  if (e.head_count) parts.push(`${e.head_count.toLocaleString("en-IN")} head`);
  if (e.product) parts.push(`${e.product} ${e.quantity ?? ""} ${e.unit ?? ""}`.trim());
  if (e.vaccine) parts.push(e.vaccine);
  if (e.method) parts.push(e.method);
  if (e.avg_weight_kg) parts.push(`avg ${e.avg_weight_kg} kg${e.sample_size ? ` (${e.sample_size} weighed)` : ""}`);
  if (e.cause) parts.push(e.cause);
  if (e.dose) parts.push(`dose ${e.dose}${e.course_days ? ` for ${e.course_days} days` : ""}`);
  if (e.vet) parts.push(e.vet);
  parts.push(...withdrawalText({ meat_until: e.withdrawal_meat_until, milk_until: e.withdrawal_milk_until, eggs_until: e.withdrawal_eggs_until }, shortDate));
  if (e.withdrawal_override) parts.push(`recorded during a withdrawal: ${e.withdrawal_override}`);
  if (e.notes) parts.push(e.notes);
  return parts.join(" · ") || "—";
}

function CancelDialog({ event, onClose, onDone }: { event: FarmEvent; onClose: () => void; onDone: () => void }) {
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  async function go() {
    setBusy(true);
    try {
      await api.post(`/api/v1/farm/events/${event.event_id}/cancel`, { reason });
      toast.success(`${EVENT_LABEL[event.event_type]} of ${shortDate(event.event_date)} cancelled`);
      onDone();
      onClose();
    } catch (e) {
      toast.error(message(e, "Could not cancel it"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={`Cancel ${EVENT_LABEL[event.event_type].toLowerCase()} of ${shortDate(event.event_date)}`}
      description={event.variant_id ? "Its stock goes back, and its ledger entry is reversed." : "It stays in the log, marked cancelled."}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Back
          </Button>
          <Button variant="destructive" onClick={go} disabled={busy || !reason.trim()}>
            {busy && <Loader2 className="animate-spin" aria-hidden />} Cancel it
          </Button>
        </>
      }
    >
      <Field label="Why *" htmlFor="fc-reason">
        <Input id="fc-reason" value={reason} onChange={(e) => setReason(e.target.value)} autoFocus />
      </Field>
    </Modal>
  );
}

function CloseDialog({ batch, onClose, onDone }: { batch: FarmBatch; onClose: () => void; onDone: () => void }) {
  const [date, setDate] = useState(() => localDate());
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  async function go() {
    setBusy(true);
    try {
      await api.post(`/api/v1/farm/batches/${batch.batch_id}/close`, { closed_on: date, note });
      toast.success(`${batch.batch_number} closed`);
      onDone();
      onClose();
    } catch (e) {
      toast.error(message(e, "Could not close it"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={`Close ${batch.batch_number}`}
      description={`${batch.kpi.alive} still counted alive. What is left of its cost (${rupees(
        batch.kpi.stock_cost + batch.kpi.feed_cost + batch.kpi.health_cost - batch.kpi.output_value,
      )}) goes from Livestock WIP to Farm Losses (or Farm Gains). No more entries after this.`}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Back
          </Button>
          <Button onClick={go} disabled={busy}>
            {busy && <Loader2 className="animate-spin" aria-hidden />} Close batch
          </Button>
        </>
      }
    >
      <div className="grid grid-cols-2 gap-3">
        <Field label="Closed on" htmlFor="fx-date">
          <Input id="fx-date" type="date" value={date} min={batch.placed_on} max={localDate()} onChange={(e) => setDate(e.target.value)} />
        </Field>
        <Field label="Note" htmlFor="fx-note">
          <Input id="fx-note" value={note} onChange={(e) => setNote(e.target.value)} placeholder="Sold out, shed cleaned" />
        </Field>
      </div>
    </Modal>
  );
}
