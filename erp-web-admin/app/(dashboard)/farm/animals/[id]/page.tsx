"use client";

// One animal (erp-core-go §3.82): its details and parents, where it is in
// its cycle, open withdrawals, the last 30 days' milk, its breeding record
// (a birth's young join the register), its own entries in the herd's log,
// and its young. Breeding and how it left: farm.record; moving it,
// editing and cancelling a breeding entry: farm.manage.
import { FormEvent, useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { useParams } from "next/navigation";
import { Loader2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api-client";
import { usePermissions } from "@/lib/permissions";
import type { FarmAnimal, FarmAnimalDetail, FarmBreedingEvent } from "@/lib/types";
import { localDate, shortDate } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Modal } from "@/components/ui/dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { PageHeader } from "@/components/data/page-header";
import { Field, Panel, Stat } from "@/components/data/panel";
import { StatusChip } from "@/components/data/status-chip";
import { EVENT_LABEL, SEX_LABEL, SPECIES_LABEL, message, withdrawalText } from "@/components/farm/farm";
import { ANIMAL_STATUS, AnimalTable, BREEDING_LABEL, BreedingDialog, ExitDialog, MoveDialog, ageText, breedText } from "@/components/farm/animals";

export default function AnimalPage() {
  const { id } = useParams<{ id: string }>();
  const { can } = usePermissions();
  const [a, setA] = useState<FarmAnimalDetail | null>(null);
  const [males, setMales] = useState<FarmAnimal[]>([]);
  const [dialog, setDialog] = useState<"breeding" | "exit" | "move" | "edit" | null>(null);
  const [cancelling, setCancelling] = useState<FarmBreedingEvent | null>(null);
  const load = useCallback(() => {
    api
      .get<FarmAnimalDetail>(`/api/v1/farm/animals/${id}`)
      .then((d) => {
        setA(d);
        if (d.sex === "female") {
          api
            .get<{ animals: FarmAnimal[] }>(`/api/v1/farm/animals?branch_id=${d.branch_id}&species=${d.species}`)
            .then((m) => setMales(m.animals.filter((x) => x.sex === "male")))
            .catch(() => setMales([]));
        }
      })
      .catch((e) => toast.error(message(e, "Could not load the animal")));
  }, [id]);
  useEffect(load, [load]);
  if (!a) return <p className="text-sm text-muted-foreground">Loading…</p>;
  const active = a.status === "active";
  const w = withdrawalText(a.withdrawal, shortDate);
  return (
    <div className="max-w-5xl space-y-5">
      <Link href="/farm/animals" className="text-sm text-muted-foreground hover:underline">
        ← Animals
      </Link>
      <PageHeader
        title={`${a.tag}${a.name ? ` · ${a.name}` : ""}`}
        description={`${SPECIES_LABEL[a.species]} · ${SEX_LABEL[a.sex]}${a.breed ? ` · ${a.breed}` : ""} · ${a.batch_number}${a.house ? `, ${a.house}` : ""} · ${a.branch}`}
        actions={
          active && (
            <div className="flex flex-wrap gap-2">
              {a.sex === "female" && (
                <Button onClick={() => setDialog("breeding")} disabled={!can("farm.record")}>
                  Breeding
                </Button>
              )}
              <Button variant="outline" onClick={() => setDialog("exit")} disabled={!can("farm.record")}>
                Left the farm
              </Button>
              <Button variant="outline" onClick={() => setDialog("move")} disabled={!can("farm.manage")}>
                Move
              </Button>
              <Button variant="ghost" onClick={() => setDialog("edit")} disabled={!can("farm.manage")}>
                Edit
              </Button>
            </div>
          )
        }
      />
      {w.length > 0 && (
        <div role="status" className="rounded-md border border-warn/40 bg-warn-soft px-3 py-2 text-sm" data-testid="animal-withdrawal">
          Withdrawal: {w.join(" · ")}
        </div>
      )}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4" data-testid="animal-kpis">
        <Stat label="Status" value={ANIMAL_STATUS[a.status].label + (a.exit_on ? ` (${shortDate(a.exit_on)})` : "")} />
        <Stat label="Age" value={ageText(a.age_days)} />
        <Stat label="Breeding" value={breedText(a)} strong />
        <Stat label="Milk" value={a.milk_7d_avg_l != null ? `${a.milk_7d_avg_l} L/day (7 days)` : "—"} />
        <Stat label="Weight" value={a.latest_weight_kg != null ? `${a.latest_weight_kg} kg (${a.weighed_on ? shortDate(a.weighed_on) : ""})` : "—"} />
        <Stat label="Mother" value={a.dam_tag ?? "—"} />
        <Stat label="Father" value={a.sire_tag ?? a.sire_note ?? "—"} />
        <Stat label="Births" value={a.sex === "female" ? a.breeding.births : "—"} />
      </div>
      {a.sex === "female" && (
        <Panel title="Breeding">
          <Table data-testid="animal-breeding">
            <TableHeader>
              <TableRow>
                <TableHead>Date</TableHead>
                <TableHead>What</TableHead>
                <TableHead>Details</TableHead>
                <TableHead className="text-right">Action</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {a.breeding_events.length === 0 && (
                <TableRow>
                  <TableCell colSpan={4} className="py-4 text-center text-sm text-muted-foreground">
                    Nothing recorded yet.
                  </TableCell>
                </TableRow>
              )}
              {a.breeding_events.map((b) => (
                <TableRow key={b.breeding_id} className={b.status === "cancelled" ? "opacity-60" : undefined}>
                  <TableCell className="text-sm">{shortDate(b.event_date)}</TableCell>
                  <TableCell className="text-sm">
                    {BREEDING_LABEL[b.event_type]}
                    {b.status === "cancelled" && <span className="block text-xs text-destructive">Cancelled</span>}
                  </TableCell>
                  <TableCell className="text-sm">
                    {[
                      b.method === "ai" ? "AI" : b.method === "natural" ? "Natural" : "",
                      b.sire_tag ?? b.sire_note ?? "",
                      b.result === "positive" ? "Pregnant" : b.result === "negative" ? "Not pregnant" : "",
                      b.born_alive != null ? `${b.born_alive} alive${b.born_dead ? `, ${b.born_dead} dead` : ""}` : "",
                      b.notes ?? "",
                    ]
                      .filter(Boolean)
                      .join(" · ") || "—"}
                  </TableCell>
                  <TableCell className="text-right">
                    {b.status === "posted" && can("farm.manage") && (
                      <Button size="sm" variant="ghost" className="text-destructive hover:text-destructive" onClick={() => setCancelling(b)}>
                        Cancel
                      </Button>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Panel>
      )}
      {a.milk.length > 0 && (
        <Panel title="Milk, last 30 days">
          <Table data-testid="animal-milk">
            <TableHeader>
              <TableRow>
                <TableHead>Date</TableHead>
                <TableHead className="text-right">Morning</TableHead>
                <TableHead className="text-right">Evening</TableHead>
                <TableHead className="text-right">Day</TableHead>
                <TableHead className="text-right">Litres</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {a.milk.map((m) => (
                <TableRow key={m.date}>
                  <TableCell className="text-sm">{shortDate(m.date)}</TableCell>
                  <TableCell className="text-right tabular">{m.am || "—"}</TableCell>
                  <TableCell className="text-right tabular">{m.pm || "—"}</TableCell>
                  <TableCell className="text-right tabular">{m.day || "—"}</TableCell>
                  <TableCell className="text-right tabular font-medium">{m.litres}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Panel>
      )}
      <Panel title="Health and log" description="Its entries in the herd's log: treatments, weights, how it left.">
        <Table data-testid="animal-log">
          <TableHeader>
            <TableRow>
              <TableHead>Date</TableHead>
              <TableHead>Event</TableHead>
              <TableHead>Details</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {a.events.length === 0 && (
              <TableRow>
                <TableCell colSpan={3} className="py-4 text-center text-sm text-muted-foreground">
                  Nothing recorded for this animal.
                </TableCell>
              </TableRow>
            )}
            {a.events.map((e) => (
              <TableRow key={e.event_id} className={e.status === "cancelled" ? "opacity-60" : undefined}>
                <TableCell className="text-sm">{shortDate(e.event_date)}</TableCell>
                <TableCell className="text-sm">{EVENT_LABEL[e.event_type]}</TableCell>
                <TableCell className="text-sm">
                  {[
                    e.product ? `${e.product} ${e.quantity ?? ""} ${e.unit ?? ""}`.trim() : "",
                    e.vaccine ?? "",
                    e.dose ? `dose ${e.dose}` : "",
                    e.cause ?? "",
                    e.avg_weight_kg ? `${e.avg_weight_kg} kg` : "",
                    e.related_batch ? `with ${e.related_batch}` : "",
                    ...withdrawalText({ meat_until: e.withdrawal_meat_until, milk_until: e.withdrawal_milk_until, eggs_until: e.withdrawal_eggs_until }, shortDate),
                  ]
                    .filter(Boolean)
                    .join(" · ") || "—"}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </Panel>
      {a.young.length > 0 && (
        <Panel title={`Young (${a.young.length})`}>
          <AnimalTable animals={a.young} />
        </Panel>
      )}
      {dialog === "breeding" && <BreedingDialog animal={a} males={males} onClose={() => setDialog(null)} onSaved={load} />}
      {dialog === "exit" && <ExitDialog animal={a} onClose={() => setDialog(null)} onSaved={load} />}
      {dialog === "move" && <MoveDialog animal={a} onClose={() => setDialog(null)} onSaved={load} />}
      {dialog === "edit" && <EditDialog animal={a} onClose={() => setDialog(null)} onSaved={load} />}
      {cancelling && <CancelBreeding entry={cancelling} onClose={() => setCancelling(null)} onDone={load} />}
      <p className="text-xs text-muted-foreground">
        <StatusChip status="" tone="muted" label={a.origin === "born" ? "Born here" : a.origin === "purchased" ? "Bought" : "From the herd"} /> Joined {shortDate(a.joined_on)}
      </p>
    </div>
  );
}

function EditDialog({ animal, onClose, onSaved }: { animal: FarmAnimal; onClose: () => void; onSaved: () => void }) {
  const [tag, setTag] = useState(animal.tag);
  const [name, setName] = useState(animal.name ?? "");
  const [breed, setBreed] = useState(animal.breed ?? "");
  const [bornOn, setBornOn] = useState(animal.born_on ?? "");
  const [notes, setNotes] = useState(animal.notes ?? "");
  const [busy, setBusy] = useState(false);
  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      await api.patch(`/api/v1/farm/animals/${animal.animal_id}`, { tag, name, breed, born_on: bornOn, notes });
      toast.success(`${tag} saved`);
      onSaved();
      onClose();
    } catch (err) {
      toast.error(message(err, "Could not save"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={`Edit ${animal.tag}`}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="farm-animal-edit" disabled={busy || !tag.trim()}>
            {busy && <Loader2 className="animate-spin" aria-hidden />} Save
          </Button>
        </>
      }
    >
      <form id="farm-animal-edit" onSubmit={save} className="grid grid-cols-2 gap-3">
        <Field label="Tag *" htmlFor="fae-tag">
          <Input id="fae-tag" value={tag} onChange={(e) => setTag(e.target.value)} />
        </Field>
        <Field label="Name" htmlFor="fae-name">
          <Input id="fae-name" value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        <Field label="Breed" htmlFor="fae-breed">
          <Input id="fae-breed" value={breed} onChange={(e) => setBreed(e.target.value)} />
        </Field>
        <Field label="Born on" htmlFor="fae-born">
          <Input id="fae-born" type="date" value={bornOn} max={localDate()} onChange={(e) => setBornOn(e.target.value)} />
        </Field>
        <Field label="Notes" htmlFor="fae-notes" className="col-span-2">
          <Input id="fae-notes" value={notes} onChange={(e) => setNotes(e.target.value)} />
        </Field>
      </form>
    </Modal>
  );
}

function CancelBreeding({ entry, onClose, onDone }: { entry: FarmBreedingEvent; onClose: () => void; onDone: () => void }) {
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  async function go() {
    setBusy(true);
    try {
      await api.post(`/api/v1/farm/breeding/${entry.breeding_id}/cancel`, { reason });
      toast.success(`${BREEDING_LABEL[entry.event_type]} of ${shortDate(entry.event_date)} cancelled`);
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
      title={`Cancel ${BREEDING_LABEL[entry.event_type].toLowerCase()} of ${shortDate(entry.event_date)}`}
      description={entry.event_type === "birth" ? "The young it added to the herd come off again (not if any are on the register)." : undefined}
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
      <Field label="Why *" htmlFor="fbc-reason">
        <Input id="fbc-reason" value={reason} onChange={(e) => setReason(e.target.value)} autoFocus />
      </Field>
    </Modal>
  );
}
