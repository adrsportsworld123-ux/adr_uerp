"use client";

// The animal register (erp-core-go §3.82): cattle, goats, pigs and
// breeding rabbits one by one, each a head of its herd (a batch). The
// table and the dialogs the Animals pages and a herd's page share: add an
// animal, record breeding (a birth adds the young), how it left, move it
// to another herd.
import { FormEvent, useEffect, useState } from "react";
import Link from "next/link";
import { Loader2, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { usePermissions } from "@/lib/permissions";
import type { FarmAnimal, FarmBatch, FarmBreedingType } from "@/lib/types";
import { localDate, shortDate } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Modal } from "@/components/ui/dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Field, selectClass } from "@/components/data/panel";
import { StatusChip, Tone } from "@/components/data/status-chip";
import { ProductPicker, VariantOption } from "@/components/data/product-picker";
import { SEX_LABEL, SPECIES_LABEL, message, withdrawalText } from "@/components/farm/farm";

export const ANIMAL_STATUS: Record<FarmAnimal["status"], { label: string; tone: Tone }> = {
  active: { label: "On the farm", tone: "ok" },
  dead: { label: "Died", tone: "bad" },
  culled: { label: "Culled", tone: "muted" },
  harvested: { label: "Sold / harvested", tone: "info" },
};
export const BREEDING_LABEL: Record<FarmBreedingType, string> = {
  heat: "Heat",
  service: "Served",
  pregnancy_check: "Pregnancy check",
  birth: "Birth",
  abortion: "Abortion",
  dry_off: "Dried off",
};

export function ageText(days: number | null): string {
  if (days == null) return "—";
  if (days < 60) return `${days} days`;
  if (days < 730) return `${Math.floor(days / 30.4)} months`;
  return `${(days / 365).toFixed(1)} years`;
}

/** Where a female is: open, served (due …), pregnant (due …), in milk. */
export function breedText(a: FarmAnimal): string {
  const b = a.breeding;
  if (a.sex === "male") return "—";
  const parts: string[] = [];
  if (b.status === "pregnant") parts.push(`Pregnant${b.expected_birth_on ? `, due ${shortDate(b.expected_birth_on)}` : ""}`);
  else if (b.status === "served") parts.push(`Served${b.expected_birth_on ? `, due ${shortDate(b.expected_birth_on)}` : ""}`);
  else parts.push("Open");
  if (b.lactating) parts.push(`in milk ${b.days_in_milk ?? 0} days`);
  return parts.join(" · ");
}

export function AnimalTable({ animals, showBatch }: { animals: FarmAnimal[]; showBatch?: boolean }) {
  return (
    <Table data-testid="animal-table">
      <TableHeader>
        <TableRow>
          <TableHead>Tag</TableHead>
          <TableHead>Sex · breed</TableHead>
          <TableHead>Age</TableHead>
          {showBatch && <TableHead>Herd</TableHead>}
          <TableHead>Breeding</TableHead>
          <TableHead className="text-right">Milk (7 days)</TableHead>
          <TableHead className="text-right">Weight</TableHead>
          <TableHead>Status</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {animals.length === 0 && (
          <TableRow>
            <TableCell colSpan={showBatch ? 8 : 7} className="py-5 text-center text-sm text-muted-foreground">
              No animals on the register.
            </TableCell>
          </TableRow>
        )}
        {animals.map((a) => {
          const w = withdrawalText(a.withdrawal, shortDate);
          return (
            <TableRow key={a.animal_id} className={a.status !== "active" ? "opacity-60" : undefined}>
              <TableCell>
                <Link href={`/farm/animals/${a.animal_id}`} className="font-medium hover:underline">
                  {a.tag}
                </Link>
                {a.name && <span className="block text-xs text-muted-foreground">{a.name}</span>}
              </TableCell>
              <TableCell className="text-sm">
                {SEX_LABEL[a.sex]}
                {a.breed && <span className="block text-xs text-muted-foreground">{a.breed}</span>}
              </TableCell>
              <TableCell className="text-sm">{ageText(a.age_days)}</TableCell>
              {showBatch && (
                <TableCell className="text-sm">
                  <Link href={`/farm/batches/${a.batch_id}`} className="hover:underline">
                    {a.batch_number}
                  </Link>
                  <span className="block text-xs text-muted-foreground">{[SPECIES_LABEL[a.species], a.house].filter(Boolean).join(" · ")}</span>
                </TableCell>
              )}
              <TableCell className="text-sm">
                {breedText(a)}
                {w.length > 0 && <span className="block text-xs text-warn">{w.join(" · ")}</span>}
              </TableCell>
              <TableCell className="text-right tabular text-sm">{a.milk_7d_avg_l != null ? `${a.milk_7d_avg_l} L/day` : "—"}</TableCell>
              <TableCell className="text-right tabular text-sm">{a.latest_weight_kg != null ? `${a.latest_weight_kg} kg` : "—"}</TableCell>
              <TableCell>
                <StatusChip status="" tone={ANIMAL_STATUS[a.status].tone} label={ANIMAL_STATUS[a.status].label} />
              </TableCell>
            </TableRow>
          );
        })}
      </TableBody>
    </Table>
  );
}

function num(v: string): number | undefined {
  return v.trim() === "" ? undefined : Number(v);
}

/** Put an animal on the register: one of its herd's heads, or one bought. */
export function AnimalDialog({ batches, batchId: initialBatch, onClose, onSaved }: { batches: FarmBatch[]; batchId?: string; onClose: () => void; onSaved: (id: string) => void }) {
  const [batchId, setBatchId] = useState(initialBatch ?? batches[0]?.batch_id ?? "");
  const batch = batches.find((b) => b.batch_id === batchId);
  const [tag, setTag] = useState("");
  const [name, setName] = useState("");
  const [sex, setSex] = useState<"female" | "male">("female");
  const [breed, setBreed] = useState("");
  const [bornOn, setBornOn] = useState("");
  const [origin, setOrigin] = useState<"existing" | "purchased">("existing");
  const [joinedOn, setJoinedOn] = useState("");
  const [fromStock, setFromStock] = useState<VariantOption | null>(null);
  const [busy, setBusy] = useState(false);
  const full = batch ? batch.kpi.on_register >= batch.kpi.alive : false;
  const how = full ? "purchased" : origin;
  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      const a = await api.post<FarmAnimal>("/api/v1/farm/animals", {
        batch_id: batchId,
        tag,
        name: name || undefined,
        sex,
        breed: breed || undefined,
        born_on: bornOn || undefined,
        origin: how,
        joined_on: joinedOn || undefined,
        stock_variant_id: how === "purchased" ? fromStock?.variantId : undefined,
      });
      toast.success(`${a.tag} is on the register`);
      onSaved(a.animal_id);
      onClose();
    } catch (err) {
      toast.error(message(err, "Could not add the animal"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title="Add an animal"
      description="An animal of a herd by its ear tag. One already counted in the herd, or one bought now (a new head)."
      width={600}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="farm-animal" disabled={busy || !batchId || !tag.trim()}>
            {busy && <Loader2 className="animate-spin" aria-hidden />} Add
          </Button>
        </>
      }
    >
      {batches.length === 0 ? (
        <p className="text-sm text-muted-foreground">Place a cattle, goat, pig or rabbit herd first (Flocks & Herds).</p>
      ) : (
        <form id="farm-animal" onSubmit={save} className="grid grid-cols-2 gap-3">
          <Field label="Herd" htmlFor="fa-batch" className="col-span-2">
            <select id="fa-batch" value={batchId} onChange={(e) => setBatchId(e.target.value)} className={`${selectClass} w-full`}>
              {batches.map((b) => (
                <option key={b.batch_id} value={b.batch_id}>
                  {b.batch_number} · {SPECIES_LABEL[b.species]} · {b.kpi.on_register}/{b.kpi.alive} on the register
                </option>
              ))}
            </select>
          </Field>
          <Field label="Tag *" htmlFor="fa-tag">
            <Input id="fa-tag" value={tag} onChange={(e) => setTag(e.target.value)} placeholder="Ear tag number" />
          </Field>
          <Field label="Name" htmlFor="fa-name">
            <Input id="fa-name" value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Field label="Sex" htmlFor="fa-sex">
            <select id="fa-sex" value={sex} onChange={(e) => setSex(e.target.value as "female" | "male")} className={`${selectClass} w-full`}>
              <option value="female">Female</option>
              <option value="male">Male</option>
            </select>
          </Field>
          <Field label="Breed (empty = the herd's)" htmlFor="fa-breed">
            <Input id="fa-breed" value={breed} onChange={(e) => setBreed(e.target.value)} placeholder={batch?.breed ?? ""} />
          </Field>
          <Field label="Born on" htmlFor="fa-born">
            <Input id="fa-born" type="date" value={bornOn} max={localDate()} onChange={(e) => setBornOn(e.target.value)} />
          </Field>
          <Field label="It is" htmlFor="fa-origin">
            <select id="fa-origin" value={how} disabled={full} onChange={(e) => setOrigin(e.target.value as "existing" | "purchased")} className={`${selectClass} w-full`}>
              <option value="existing">One of the herd&apos;s heads</option>
              <option value="purchased">Bought (a new head)</option>
            </select>
          </Field>
          {how === "purchased" && (
            <>
              <Field label="Joined on" htmlFor="fa-joined">
                <Input id="fa-joined" type="date" value={joinedOn} min={batch?.placed_on} max={localDate()} onChange={(e) => setJoinedOn(e.target.value)} />
              </Field>
              <Field label="From stock (optional)" htmlFor="fa-stock" className="col-span-2">
                <ProductPicker id="fa-stock" value={fromStock} onChange={setFromStock} placeholder="The animal bought on a goods receipt" />
              </Field>
            </>
          )}
          {full && <p className="col-span-2 text-xs text-muted-foreground">Every head of this herd is on the register, so a new animal is a purchase.</p>}
        </form>
      )}
    </Modal>
  );
}

/** Heat, service, pregnancy check, birth (the young join), abortion, drying off. */
export function BreedingDialog({ animal, males, onClose, onSaved }: { animal: FarmAnimal; males: FarmAnimal[]; onClose: () => void; onSaved: () => void }) {
  const [type, setType] = useState<FarmBreedingType>(animal.breeding.status === "served" ? "pregnancy_check" : animal.breeding.status === "pregnant" ? "birth" : "service");
  const [date, setDate] = useState(() => localDate());
  const [method, setMethod] = useState<"natural" | "ai">("ai");
  const [sireId, setSireId] = useState("");
  const [sireNote, setSireNote] = useState("");
  const [result, setResult] = useState<"positive" | "negative">("positive");
  const [alive, setAlive] = useState("1");
  const [dead, setDead] = useState("0");
  const [young, setYoung] = useState<{ tag: string; sex: "female" | "male" }[]>([]);
  const [notes, setNotes] = useState("");
  const [busy, setBusy] = useState(false);
  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      await api.post(`/api/v1/farm/animals/${animal.animal_id}/breeding`, {
        event_type: type,
        event_date: date,
        method: type === "service" ? method : undefined,
        sire_id: type === "service" && sireId ? sireId : undefined,
        sire_note: type === "service" ? sireNote || undefined : undefined,
        result: type === "pregnancy_check" ? result : undefined,
        born_alive: type === "birth" ? num(alive) : undefined,
        born_dead: type === "birth" ? num(dead) : undefined,
        young: type === "birth" ? young.filter((y) => y.tag.trim()) : undefined,
        notes: notes || undefined,
      });
      toast.success(`${BREEDING_LABEL[type]} recorded for ${animal.tag}`);
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
      title={`${animal.tag} · breeding`}
      description={breedText(animal)}
      width={600}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="farm-breeding" disabled={busy || (type === "birth" && !(Number(alive) > 0 || Number(dead) > 0))}>
            {busy && <Loader2 className="animate-spin" aria-hidden />} Record
          </Button>
        </>
      }
    >
      <form id="farm-breeding" onSubmit={save} className="grid grid-cols-2 gap-3">
        <Field label="What" htmlFor="fbr-type">
          <select id="fbr-type" value={type} onChange={(e) => setType(e.target.value as FarmBreedingType)} className={`${selectClass} w-full`}>
            {(Object.keys(BREEDING_LABEL) as FarmBreedingType[]).map((t) => (
              <option key={t} value={t}>
                {BREEDING_LABEL[t]}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Date" htmlFor="fbr-date">
          <Input id="fbr-date" type="date" value={date} min={animal.joined_on} max={localDate()} onChange={(e) => setDate(e.target.value)} />
        </Field>
        {type === "service" && (
          <>
            <Field label="How" htmlFor="fbr-method">
              <select id="fbr-method" value={method} onChange={(e) => setMethod(e.target.value as "natural" | "ai")} className={`${selectClass} w-full`}>
                <option value="ai">Artificial insemination</option>
                <option value="natural">Natural</option>
              </select>
            </Field>
            <Field label="Father on the farm" htmlFor="fbr-sire">
              <select id="fbr-sire" value={sireId} onChange={(e) => setSireId(e.target.value)} className={`${selectClass} w-full`}>
                <option value="">Not on the farm</option>
                {males.map((m) => (
                  <option key={m.animal_id} value={m.animal_id}>
                    {m.tag}
                    {m.name ? ` · ${m.name}` : ""}
                  </option>
                ))}
              </select>
            </Field>
            <Field label="Bull / buck / semen straw" htmlFor="fbr-sire-note" className="col-span-2">
              <Input id="fbr-sire-note" value={sireNote} onChange={(e) => setSireNote(e.target.value)} placeholder="HF straw 1123" />
            </Field>
          </>
        )}
        {type === "pregnancy_check" && (
          <Field label="Result" htmlFor="fbr-result">
            <select id="fbr-result" value={result} onChange={(e) => setResult(e.target.value as "positive" | "negative")} className={`${selectClass} w-full`}>
              <option value="positive">Pregnant</option>
              <option value="negative">Not pregnant</option>
            </select>
          </Field>
        )}
        {type === "birth" && (
          <>
            <Field label="Born alive" htmlFor="fbr-alive">
              <Input id="fbr-alive" type="number" min="0" step="1" value={alive} onChange={(e) => setAlive(e.target.value)} />
            </Field>
            <Field label="Born dead" htmlFor="fbr-dead">
              <Input id="fbr-dead" type="number" min="0" step="1" value={dead} onChange={(e) => setDead(e.target.value)} />
            </Field>
            <div className="col-span-2 space-y-2">
              <p className="text-xs text-muted-foreground">The live young are added to the herd. Tag the ones to keep on the register (the rest are counted only).</p>
              {young.map((y, i) => (
                <div key={i} className="flex items-end gap-2">
                  <Field label={i === 0 ? "Tag" : ""} htmlFor={`fbr-y-tag-${i}`} className="flex-1">
                    <Input
                      id={`fbr-y-tag-${i}`}
                      value={y.tag}
                      aria-label={`Tag of young ${i + 1}`}
                      onChange={(e) => setYoung((ys) => ys.map((x, j) => (j === i ? { ...x, tag: e.target.value } : x)))}
                    />
                  </Field>
                  <Field label={i === 0 ? "Sex" : ""} htmlFor={`fbr-y-sex-${i}`}>
                    <select
                      id={`fbr-y-sex-${i}`}
                      value={y.sex}
                      aria-label={`Sex of young ${i + 1}`}
                      onChange={(e) => setYoung((ys) => ys.map((x, j) => (j === i ? { ...x, sex: e.target.value as "female" | "male" } : x)))}
                      className={selectClass}
                    >
                      <option value="female">Female</option>
                      <option value="male">Male</option>
                    </select>
                  </Field>
                  <Button type="button" size="icon-sm" variant="ghost" aria-label={`Remove young ${i + 1}`} onClick={() => setYoung((ys) => ys.filter((_, j) => j !== i))}>
                    <Trash2 />
                  </Button>
                </div>
              ))}
              {young.length < (Number(alive) || 0) && (
                <Button type="button" size="sm" variant="outline" onClick={() => setYoung((ys) => [...ys, { tag: "", sex: "female" }])}>
                  <Plus aria-hidden /> Tag a young one
                </Button>
              )}
            </div>
          </>
        )}
        <Field label="Notes" htmlFor="fbr-notes" className="col-span-2">
          <Input id="fbr-notes" value={notes} onChange={(e) => setNotes(e.target.value)} />
        </Field>
      </form>
    </Modal>
  );
}

/** How an animal left: died, culled, or harvested (into stock: live or meat). */
export function ExitDialog({ animal, onClose, onSaved }: { animal: FarmAnimal; onClose: () => void; onSaved: () => void }) {
  const { can } = usePermissions();
  const [how, setHow] = useState<"died" | "culled" | "harvested">("died");
  const [date, setDate] = useState(() => localDate());
  const [cause, setCause] = useState("");
  const [item, setItem] = useState<VariantOption | null>(null);
  const [qty, setQty] = useState("");
  const [unitCost, setUnitCost] = useState("");
  const [blocked, setBlocked] = useState<string | null>(null);
  const [overrideReason, setOverrideReason] = useState("");
  const [busy, setBusy] = useState(false);
  async function go(override: boolean) {
    setBusy(true);
    try {
      await api.post(`/api/v1/farm/animals/${animal.animal_id}/exit`, {
        how,
        date,
        cause: cause || undefined,
        variant_id: how === "harvested" ? item?.variantId : undefined,
        quantity: how === "harvested" && item ? num(qty) : undefined,
        unit_cost: how === "harvested" ? num(unitCost) : undefined,
        override_withdrawal: override || undefined,
        override_reason: override ? overrideReason : undefined,
      });
      toast.success(`${animal.tag}: recorded`);
      onSaved();
      onClose();
    } catch (err) {
      if (err instanceof ApiError && err.code === "WITHDRAWAL_PERIOD") setBlocked(err.message);
      else toast.error(message(err, "Could not record it"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={`${animal.tag} left the farm`}
      description="It stays on the register, marked how it left; cancelling the entry in the herd's log brings it back."
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={() => go(false)} disabled={busy || (how === "harvested" && !!item && !(Number(qty) > 0))}>
            {busy && <Loader2 className="animate-spin" aria-hidden />} Record
          </Button>
        </>
      }
    >
      <div className="grid grid-cols-2 gap-3">
        <Field label="How" htmlFor="fx-how">
          <select id="fx-how" value={how} onChange={(e) => setHow(e.target.value as "died" | "culled" | "harvested")} className={`${selectClass} w-full`}>
            <option value="died">Died</option>
            <option value="culled">Culled</option>
            <option value="harvested">Sold or slaughtered (into stock)</option>
          </select>
        </Field>
        <Field label="Date" htmlFor="fx-date">
          <Input id="fx-date" type="date" value={date} max={localDate()} onChange={(e) => setDate(e.target.value)} />
        </Field>
        <Field label="Cause / note" htmlFor="fx-cause" className="col-span-2">
          <Input id="fx-cause" value={cause} onChange={(e) => setCause(e.target.value)} />
        </Field>
        {how === "harvested" && (
          <>
            <Field label="Into stock as (optional)" htmlFor="fx-item" className="col-span-2">
              <ProductPicker id="fx-item" value={item} onChange={setItem} placeholder="Live goat, mutton…" />
            </Field>
            {item && (
              <>
                <Field label="Quantity *" htmlFor="fx-qty">
                  <Input id="fx-qty" type="number" min="0" step="any" value={qty} onChange={(e) => setQty(e.target.value)} />
                </Field>
                <Field label="Cost per unit (empty = its cost price)" htmlFor="fx-cost">
                  <Input id="fx-cost" type="number" min="0" step="0.01" value={unitCost} onChange={(e) => setUnitCost(e.target.value)} />
                </Field>
              </>
            )}
          </>
        )}
        {blocked && (
          <div role="alert" className="col-span-2 space-y-2 rounded-md border border-destructive/40 bg-destructive-soft p-3 text-sm">
            <p>{blocked}</p>
            {can("farm.manage") && (
              <div className="flex items-end gap-2">
                <Field label="Why record it anyway *" htmlFor="fx-override" className="flex-1">
                  <Input id="fx-override" value={overrideReason} onChange={(e) => setOverrideReason(e.target.value)} />
                </Field>
                <Button type="button" variant="destructive" disabled={busy || !overrideReason.trim()} onClick={() => go(true)}>
                  Record anyway
                </Button>
              </div>
            )}
          </div>
        )}
      </div>
    </Modal>
  );
}

/** To another active herd of its species on the same farm. */
export function MoveDialog({ animal, onClose, onSaved }: { animal: FarmAnimal; onClose: () => void; onSaved: () => void }) {
  const [batches, setBatches] = useState<FarmBatch[]>([]);
  const [batchId, setBatchId] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    api
      .get<{ batches: FarmBatch[] }>(`/api/v1/farm/batches?branch_id=${animal.branch_id}&status=active&species=${animal.species}`)
      .then((d) => setBatches(d.batches.filter((b) => b.batch_id !== animal.batch_id)))
      .catch(() => setBatches([]));
  }, [animal]);
  const target = batchId || batches[0]?.batch_id || "";
  async function go() {
    setBusy(true);
    try {
      await api.post(`/api/v1/farm/animals/${animal.animal_id}/move`, { batch_id: target });
      toast.success(`${animal.tag} moved`);
      onSaved();
      onClose();
    } catch (err) {
      toast.error(message(err, "Could not move it"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={`Move ${animal.tag}`}
      description={`From ${animal.batch_number}. A head moves out of one herd and into the other; its costs so far stay where they were.`}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={go} disabled={busy || !target}>
            {busy && <Loader2 className="animate-spin" aria-hidden />} Move
          </Button>
        </>
      }
    >
      {batches.length === 0 ? (
        <p className="text-sm text-muted-foreground">No other active {SPECIES_LABEL[animal.species].toLowerCase()} herd on this farm.</p>
      ) : (
        <Field label="To" htmlFor="fm-batch">
          <select id="fm-batch" value={target} onChange={(e) => setBatchId(e.target.value)} className={`${selectClass} w-full`}>
            {batches.map((b) => (
              <option key={b.batch_id} value={b.batch_id}>
                {b.batch_number}
                {b.house ? ` · ${b.house}` : ""} ({b.kpi.alive})
              </option>
            ))}
          </select>
        </Field>
      )}
    </Modal>
  );
}
