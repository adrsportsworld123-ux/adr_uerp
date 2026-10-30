"use client";

// Farm management (erp-core-go §3.81–§3.82, internal/farm): labels, the
// farm branch picker, and the dialogs the farm screens share — record an
// event (mortality, feed, medicine, vaccination, weight, production,
// expense, note, birth, animals added), place a new batch, add or edit a
// shed or pond. Feed, medicine and placed animals come out of the branch's
// stock; produce goes into it. A treatment keeps its withdrawal days;
// produce during them is refused unless farm.manage overrides it.
import { FormEvent, useEffect, useState } from "react";
import { Loader2 } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { usePermissions } from "@/lib/permissions";
import type { FarmAnimal, FarmBatch, FarmDue, FarmEventType, FarmFeedPlan, FarmHouse, FarmItem, FarmProduceKind, FarmSchedule, FarmSpecies } from "@/lib/types";
import { localDate } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Modal } from "@/components/ui/dialog";
import { Field, selectClass } from "@/components/data/panel";
import { ProductPicker, VariantOption } from "@/components/data/product-picker";

export const message = (e: unknown, fallback: string) => (e instanceof ApiError ? e.message : fallback);

export const SPECIES_LABEL: Record<FarmSpecies, string> = { chicken: "Chicken", goat: "Goat", rabbit: "Rabbit", cow: "Cattle", pig: "Pig", fish: "Fish" };
export const PURPOSE_LABEL: Record<string, string> = {
  broiler: "Broiler",
  layer: "Layer",
  breeder: "Breeder",
  country: "Country chicken",
  meat: "Meat",
  breeding: "Breeding",
  dairy: "Dairy",
  calf_rearing: "Calf rearing",
  fattening: "Fattening",
  grow_out: "Grow-out",
  nursery: "Nursery",
};
export const PURPOSES: Record<FarmSpecies, string[]> = {
  chicken: ["broiler", "layer", "breeder", "country"],
  goat: ["meat", "breeding", "dairy"],
  rabbit: ["meat", "breeding"],
  cow: ["dairy", "breeding", "calf_rearing"],
  pig: ["fattening", "breeding"],
  fish: ["grow_out", "nursery"],
};
export const HOUSE_KIND_LABEL: Record<FarmHouse["kind"], string> = {
  shed: "Shed",
  pen: "Pen",
  cage: "Cage house",
  paddock: "Paddock",
  pond: "Pond",
  tank: "Tank",
  other: "Other",
};
export const EVENT_LABEL: Record<FarmEventType, string> = {
  placement: "Placed",
  mortality: "Mortality",
  cull: "Culled",
  feed: "Feed",
  medicine: "Medicine",
  vaccination: "Vaccination",
  weight: "Weight",
  production: "Production",
  expense: "Expense",
  note: "Note",
  birth: "Birth",
  addition: "Animals added",
  transfer_in: "Moved in",
  transfer_out: "Moved out",
};
/** What a farm worker records, in the order the buttons show. */
export const RECORDABLE: FarmEventType[] = ["mortality", "feed", "vaccination", "medicine", "weight", "production", "cull", "birth", "addition", "expense", "note"];
/** The species kept one by one on the animal register. */
export const REGISTER_SPECIES: FarmSpecies[] = ["cow", "goat", "pig", "rabbit"];
export const PRODUCE_LABEL: Record<FarmProduceKind, string> = { meat: "Meat / live animals", milk: "Milk", eggs: "Eggs", other: "Other" };
export const SEX_LABEL = { female: "Female", male: "Male" } as const;

/** "No milk until 2 Nov" lines for a set of withdrawal dates. */
export function withdrawalText(w: { meat_until: string | null; milk_until: string | null; eggs_until: string | null }, fmt: (d: string) => string): string[] {
  const out: string[] = [];
  if (w.milk_until) out.push(`No milk until ${fmt(w.milk_until)}`);
  if (w.eggs_until) out.push(`No eggs until ${fmt(w.eggs_until)}`);
  if (w.meat_until) out.push(`No meat until ${fmt(w.meat_until)}`);
  return out;
}

/** The farm's feed, medicine and vaccine items (with withdrawal days). */
export function useFarmItems() {
  const [items, setItems] = useState<FarmItem[]>([]);
  useEffect(() => {
    api
      .get<{ items: FarmItem[] }>("/api/v1/farm/items")
      .then((d) => setItems(d.items))
      .catch(() => setItems([]));
  }, []);
  return items;
}

/** The farm branches the user sees (from /me/features). */
export function useFarmBranches() {
  const { branchFeatures } = usePermissions();
  return branchFeatures.filter((b) => b.features.includes("farm"));
}

export function FarmBranchSelect({ id, value, onChange, allLabel }: { id: string; value: string; onChange: (v: string) => void; allLabel?: string }) {
  const branches = useFarmBranches();
  return (
    <select id={id} value={value} onChange={(e) => onChange(e.target.value)} className={`${selectClass} w-48`}>
      {allLabel && <option value="">{allLabel}</option>}
      {branches.map((b) => (
        <option key={b.branch_id} value={b.branch_id}>
          {b.name}
        </option>
      ))}
    </select>
  );
}

function num(v: string): number | undefined {
  return v.trim() === "" ? undefined : Number(v);
}

/** Record one event of a batch. `due` pre-fills a vaccination from its schedule. */
export function EventDialog({
  batch,
  type: initialType,
  due,
  onClose,
  onSaved,
}: {
  batch: FarmBatch;
  type: FarmEventType;
  due?: FarmDue;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [type, setType] = useState<FarmEventType>(initialType);
  const [date, setDate] = useState(() => localDate());
  const [heads, setHeads] = useState("");
  const [item, setItem] = useState<VariantOption | null>(null);
  const [qty, setQty] = useState("");
  const [unitCost, setUnitCost] = useState("");
  const [amount, setAmount] = useState("");
  const [weight, setWeight] = useState("");
  const [sample, setSample] = useState("");
  const [cause, setCause] = useState("");
  const [vaccine, setVaccine] = useState(due?.name ?? "");
  const [method, setMethod] = useState(due?.method ?? "");
  const [notes, setNotes] = useState("");
  const [busy, setBusy] = useState(false);
  // §3.82: one animal of the register; a treatment's details; produce kind.
  const { can } = usePermissions();
  const register = REGISTER_SPECIES.includes(batch.species);
  const [animals, setAnimals] = useState<FarmAnimal[]>([]);
  const [animalId, setAnimalId] = useState("");
  const [dose, setDose] = useState("");
  const [course, setCourse] = useState("");
  const [vet, setVet] = useState("");
  const [wMeat, setWMeat] = useState("");
  const [wMilk, setWMilk] = useState("");
  const [wEggs, setWEggs] = useState("");
  const [produceKind, setProduceKind] = useState<FarmProduceKind | "">("");
  const [blocked, setBlocked] = useState<string | null>(null);
  const [overrideReason, setOverrideReason] = useState("");
  const farmItems = useFarmItems();
  useEffect(() => {
    if (!register) return;
    api
      .get<{ animals: FarmAnimal[] }>(`/api/v1/farm/animals?batch_id=${batch.batch_id}`)
      .then((d) => setAnimals(d.animals))
      .catch(() => setAnimals([]));
  }, [register, batch.batch_id]);
  const usesItem = type === "feed" || type === "medicine" || type === "production" || type === "vaccination" || type === "addition";
  const needsItem = type === "feed" || type === "medicine" || type === "production";
  const treatment = type === "medicine" || type === "vaccination";
  const forAnimal = register && animals.length > 0 && ["mortality", "cull", "production", "medicine", "vaccination", "weight", "note"].includes(type);
  const oneHead = !!animalId && (type === "mortality" || type === "cull" || type === "production");

  function pickItem(o: VariantOption) {
    setItem(o);
    // A medicine's withdrawal days come from its farm item (editable here).
    const fi = farmItems.find((f) => f.product_id === o.productId);
    if (fi) {
      setWMeat(fi.withdrawal_meat_days != null ? String(fi.withdrawal_meat_days) : "");
      setWMilk(fi.withdrawal_milk_days != null ? String(fi.withdrawal_milk_days) : "");
      setWEggs(fi.withdrawal_eggs_days != null ? String(fi.withdrawal_eggs_days) : "");
      if (fi.dose_note && !dose) setDose(fi.dose_note);
    }
  }

  async function save(e: FormEvent, override = false) {
    e.preventDefault();
    setBusy(true);
    try {
      await api.post(`/api/v1/farm/batches/${batch.batch_id}/events`, {
        event_type: type,
        event_date: date,
        head_count: oneHead ? 1 : num(heads),
        animal_id: forAnimal && animalId ? animalId : undefined,
        dose: treatment ? dose || undefined : undefined,
        course_days: treatment ? num(course) : undefined,
        vet: treatment ? vet || undefined : undefined,
        withdrawal_meat_days: treatment ? num(wMeat) : undefined,
        withdrawal_milk_days: treatment ? num(wMilk) : undefined,
        withdrawal_eggs_days: treatment ? num(wEggs) : undefined,
        produce_kind: type === "production" ? produceKind || undefined : undefined,
        override_withdrawal: override || undefined,
        override_reason: override ? overrideReason : undefined,
        variant_id: usesItem && item ? item.variantId : undefined,
        quantity: usesItem && item ? num(qty) : undefined,
        unit_cost: type === "production" ? num(unitCost) : undefined,
        amount: type === "expense" ? num(amount) : undefined,
        avg_weight_kg: type === "weight" ? num(weight) : undefined,
        sample_size: type === "weight" ? num(sample) : undefined,
        cause: cause || undefined,
        vaccine: type === "vaccination" ? vaccine || undefined : undefined,
        method: method || undefined,
        schedule_item_id: type === "vaccination" ? due?.schedule_item_id : undefined,
        notes: notes || undefined,
      });
      toast.success(`${EVENT_LABEL[type]} recorded for ${batch.batch_number}`);
      onSaved();
      onClose();
    } catch (err) {
      if (err instanceof ApiError && err.code === "WITHDRAWAL_PERIOD") setBlocked(err.message);
      else toast.error(message(err, "Could not record it"));
    } finally {
      setBusy(false);
    }
  }
  const ready =
    !["mortality", "cull", "birth", "addition"].includes(type) || oneHead || Number(heads) > 0
      ? (!needsItem || (item && Number(qty) > 0)) &&
        (type !== "weight" || Number(weight) > 0) &&
        (type !== "expense" || (Number(amount) > 0 && notes.trim())) &&
        (type !== "note" || notes.trim()) &&
        (type !== "vaccination" || vaccine.trim())
      : false;
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={`${batch.batch_number} · ${EVENT_LABEL[type]}`}
      description={`${SPECIES_LABEL[batch.species]} ${PURPOSE_LABEL[batch.purpose] ?? batch.purpose} · ${batch.kpi.alive} alive · day ${batch.kpi.age_days}`}
      width={560}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="farm-event" disabled={busy || !ready}>
            {busy && <Loader2 className="animate-spin" aria-hidden />} Record
          </Button>
        </>
      }
    >
      <form id="farm-event" onSubmit={save} className="grid grid-cols-2 gap-3">
        <Field label="What" htmlFor="fe-type">
          <select id="fe-type" value={type} onChange={(e) => setType(e.target.value as FarmEventType)} className={`${selectClass} w-full`}>
            {RECORDABLE.map((t) => (
              <option key={t} value={t}>
                {EVENT_LABEL[t]}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Date" htmlFor="fe-date">
          <Input id="fe-date" type="date" value={date} min={batch.placed_on} max={localDate()} onChange={(e) => setDate(e.target.value)} />
        </Field>
        {forAnimal && (
          <Field label={treatment || type === "weight" || type === "note" ? "Animal (empty = the whole batch)" : "Which animal"} htmlFor="fe-animal" className="col-span-2">
            <select id="fe-animal" value={animalId} onChange={(e) => setAnimalId(e.target.value)} className={`${selectClass} w-full`}>
              <option value="">{type === "mortality" || type === "cull" || type === "production" ? "Not on the register" : "The whole batch"}</option>
              {animals.map((a) => (
                <option key={a.animal_id} value={a.animal_id}>
                  {a.tag}
                  {a.name ? ` · ${a.name}` : ""} ({SEX_LABEL[a.sex]})
                </option>
              ))}
            </select>
          </Field>
        )}
        {!oneHead && ["mortality", "cull", "production", "vaccination", "birth", "addition"].includes(type) && (
          <Field
            label={
              type === "production"
                ? "Heads that left (a harvest)"
                : type === "vaccination"
                  ? "Heads vaccinated"
                  : type === "birth"
                    ? "How many born alive *"
                    : type === "addition"
                      ? "How many added *"
                      : "How many *"
            }
            htmlFor="fe-heads"
          >
            <Input id="fe-heads" type="number" min="1" step="1" value={heads} onChange={(e) => setHeads(e.target.value)} />
          </Field>
        )}
        {type === "production" && (
          <Field label="Produce" htmlFor="fe-kind">
            <select id="fe-kind" value={produceKind} onChange={(e) => setProduceKind(e.target.value as FarmProduceKind | "")} className={`${selectClass} w-full`}>
              <option value="">From the item (litres = milk …)</option>
              {(Object.keys(PRODUCE_LABEL) as FarmProduceKind[]).map((k) => (
                <option key={k} value={k}>
                  {PRODUCE_LABEL[k]}
                </option>
              ))}
            </select>
          </Field>
        )}
        {(type === "mortality" || type === "cull") && (
          <Field label="Cause" htmlFor="fe-cause">
            <Input id="fe-cause" value={cause} onChange={(e) => setCause(e.target.value)} placeholder="Heat stress, disease…" />
          </Field>
        )}
        {type === "vaccination" && (
          <>
            <Field label="Vaccine *" htmlFor="fe-vaccine">
              <Input id="fe-vaccine" value={vaccine} onChange={(e) => setVaccine(e.target.value)} />
            </Field>
            <Field label="How given" htmlFor="fe-method">
              <Input id="fe-method" value={method} onChange={(e) => setMethod(e.target.value)} placeholder="Eye drop, drinking water…" />
            </Field>
          </>
        )}
        {usesItem && (
          <>
            <Field
              label={
                type === "production"
                  ? "Produce (into stock) *"
                  : type === "vaccination"
                    ? "Vaccine from stock (optional)"
                    : type === "addition"
                      ? "Animals from stock (optional)"
                      : `${EVENT_LABEL[type]} from stock *`
              }
              htmlFor="fe-item"
              className="col-span-2"
            >
              <ProductPicker id="fe-item" value={item} onChange={pickItem} placeholder="Search the item" />
            </Field>
            {item && (
              <Field label="Quantity *" htmlFor="fe-qty">
                <Input id="fe-qty" type="number" min="0" step="any" value={qty} onChange={(e) => setQty(e.target.value)} />
              </Field>
            )}
            {item && type === "production" && (
              <Field label="Cost per unit (empty = its cost price)" htmlFor="fe-cost">
                <Input id="fe-cost" type="number" min="0" step="0.01" value={unitCost} onChange={(e) => setUnitCost(e.target.value)} />
              </Field>
            )}
          </>
        )}
        {type === "weight" && (
          <>
            <Field label="Average weight (kg) *" htmlFor="fe-weight">
              <Input id="fe-weight" type="number" min="0" step="0.001" value={weight} onChange={(e) => setWeight(e.target.value)} />
            </Field>
            <Field label="Weighed (sample)" htmlFor="fe-sample">
              <Input id="fe-sample" type="number" min="1" step="1" value={sample} onChange={(e) => setSample(e.target.value)} />
            </Field>
          </>
        )}
        {treatment && (
          <>
            <Field label="Dose" htmlFor="fe-dose">
              <Input id="fe-dose" value={dose} onChange={(e) => setDose(e.target.value)} placeholder="20 ml IM" />
            </Field>
            <Field label="Course (days)" htmlFor="fe-course">
              <Input id="fe-course" type="number" min="1" step="1" value={course} onChange={(e) => setCourse(e.target.value)} />
            </Field>
            <Field label="Vet" htmlFor="fe-vet">
              <Input id="fe-vet" value={vet} onChange={(e) => setVet(e.target.value)} />
            </Field>
            {type === "medicine" && (
              <Field label="Diagnosis" htmlFor="fe-diagnosis">
                <Input id="fe-diagnosis" value={cause} onChange={(e) => setCause(e.target.value)} placeholder="Mastitis, coccidiosis…" />
              </Field>
            )}
            <fieldset className="col-span-2 grid grid-cols-3 gap-3 rounded-md border p-3">
              <legend className="px-1 text-xs text-muted-foreground">Withdrawal — days before produce may be taken again</legend>
              <Field label="Milk" htmlFor="fe-w-milk">
                <Input id="fe-w-milk" type="number" min="0" step="1" value={wMilk} onChange={(e) => setWMilk(e.target.value)} />
              </Field>
              <Field label="Eggs" htmlFor="fe-w-eggs">
                <Input id="fe-w-eggs" type="number" min="0" step="1" value={wEggs} onChange={(e) => setWEggs(e.target.value)} />
              </Field>
              <Field label="Meat" htmlFor="fe-w-meat">
                <Input id="fe-w-meat" type="number" min="0" step="1" value={wMeat} onChange={(e) => setWMeat(e.target.value)} />
              </Field>
            </fieldset>
          </>
        )}
        {type === "expense" && (
          <Field label="Amount *" htmlFor="fe-amount">
            <Input id="fe-amount" type="number" min="0" step="0.01" value={amount} onChange={(e) => setAmount(e.target.value)} />
          </Field>
        )}
        <Field label={type === "expense" ? "What it was for *" : type === "note" ? "Note *" : "Notes"} htmlFor="fe-notes" className="col-span-2">
          <Input id="fe-notes" value={notes} onChange={(e) => setNotes(e.target.value)} />
        </Field>
        {type === "expense" && (
          <p className="col-span-2 text-xs text-muted-foreground">Counts in the batch&apos;s cost only; pay it in Finance › Expenses.</p>
        )}
        {blocked && (
          <div role="alert" className="col-span-2 space-y-2 rounded-md border border-destructive/40 bg-destructive-soft p-3 text-sm">
            <p>{blocked}</p>
            {can("farm.manage") ? (
              <div className="flex items-end gap-2">
                <Field label="Why record it anyway *" htmlFor="fe-override" className="flex-1">
                  <Input id="fe-override" value={overrideReason} onChange={(e) => setOverrideReason(e.target.value)} placeholder="It was discarded" />
                </Field>
                <Button type="button" variant="destructive" disabled={busy || !overrideReason.trim()} onClick={(e) => save(e as unknown as FormEvent, true)}>
                  Record anyway
                </Button>
              </div>
            ) : (
              <p className="text-xs">A manager (farm.manage) can record it with a reason.</p>
            )}
          </div>
        )}
      </form>
    </Modal>
  );
}

/** Place a new flock, herd or pond stocking. */
export function BatchDialog({ onClose, onSaved }: { onClose: () => void; onSaved: (id: string) => void }) {
  const branches = useFarmBranches();
  const [branchId, setBranchId] = useState(branches[0]?.branch_id ?? "");
  const branch = branches.find((b) => b.branch_id === branchId);
  const speciesList = (branch?.species ?? []) as FarmSpecies[];
  const [species, setSpecies] = useState<FarmSpecies | "">(speciesList[0] ?? "");
  const [purpose, setPurpose] = useState("");
  const [houses, setHouses] = useState<FarmHouse[]>([]);
  const [houseId, setHouseId] = useState("");
  const [schedules, setSchedules] = useState<FarmSchedule[]>([]);
  const [scheduleId, setScheduleId] = useState("");
  const [feedPlans, setFeedPlans] = useState<FarmFeedPlan[]>([]);
  const [feedPlanId, setFeedPlanId] = useState("");
  const [placedOn, setPlacedOn] = useState(() => localDate());
  const [count, setCount] = useState("");
  const [age, setAge] = useState("0");
  const [avgWeight, setAvgWeight] = useState("");
  const [breed, setBreed] = useState("");
  const [name, setName] = useState("");
  const [source, setSource] = useState("");
  const [fromStock, setFromStock] = useState<VariantOption | null>(null);
  const [busy, setBusy] = useState(false);
  const sp = species || speciesList[0] || "";
  const pp = purpose || (sp ? PURPOSES[sp][0] : "");

  useEffect(() => {
    if (!branchId) return;
    api
      .get<{ houses: FarmHouse[] }>(`/api/v1/farm/houses?branch_id=${branchId}&status=active`)
      .then((d) => setHouses(d.houses))
      .catch(() => setHouses([]));
  }, [branchId]);
  useEffect(() => {
    if (!sp) return;
    api
      .get<{ schedules: FarmSchedule[] }>(`/api/v1/farm/schedules?species=${sp}`)
      .then((d) => setSchedules(d.schedules.filter((s) => s.status === "active")))
      .catch(() => setSchedules([]));
    api
      .get<{ feed_plans: FarmFeedPlan[] }>(`/api/v1/farm/feed-plans?species=${sp}`)
      .then((d) => setFeedPlans(d.feed_plans.filter((p) => p.status === "active")))
      .catch(() => setFeedPlans([]));
  }, [sp]);

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      const b = await api.post<FarmBatch>("/api/v1/farm/batches", {
        branch_id: branchId,
        house_id: houseId || undefined,
        species: sp,
        purpose: pp,
        placed_on: placedOn,
        initial_count: Number(count),
        age_at_placement_days: Number(age) || 0,
        initial_avg_weight_kg: num(avgWeight),
        breed: breed || undefined,
        name: name || undefined,
        source: source || undefined,
        schedule_id: scheduleId,
        feed_plan_id: feedPlanId,
        stock_variant_id: fromStock?.variantId,
      });
      toast.success(`${b.batch_number} placed`);
      onSaved(b.batch_id);
      onClose();
    } catch (err) {
      toast.error(message(err, "Could not place the batch"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title="New batch"
      description="A flock, herd or pond stocking. The animals can come out of this branch's stock (chicks bought on a goods receipt)."
      width={640}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="farm-batch" disabled={busy || !branchId || !sp || !(Number(count) > 0)}>
            {busy && <Loader2 className="animate-spin" aria-hidden />} Place
          </Button>
        </>
      }
    >
      {branches.length === 0 ? (
        <p className="text-sm text-muted-foreground">No branch is set up as a farm yet. Give one a farm business type in Organization › Branches.</p>
      ) : (
        <form id="farm-batch" onSubmit={save} className="grid grid-cols-2 gap-3">
          <Field label="Farm" htmlFor="fb-branch">
            <select
              id="fb-branch"
              value={branchId}
              onChange={(e) => {
                setBranchId(e.target.value);
                setSpecies("");
                setHouseId("");
              }}
              className={`${selectClass} w-full`}
            >
              {branches.map((b) => (
                <option key={b.branch_id} value={b.branch_id}>
                  {b.name}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Species" htmlFor="fb-species">
            <select
              id="fb-species"
              value={sp}
              onChange={(e) => {
                setSpecies(e.target.value as FarmSpecies);
                setPurpose("");
                setScheduleId("");
                setFeedPlanId("");
              }}
              className={`${selectClass} w-full`}
            >
              {speciesList.map((s) => (
                <option key={s} value={s}>
                  {SPECIES_LABEL[s]}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Purpose" htmlFor="fb-purpose">
            <select id="fb-purpose" value={pp} onChange={(e) => setPurpose(e.target.value)} className={`${selectClass} w-full`}>
              {(sp ? PURPOSES[sp] : []).map((p) => (
                <option key={p} value={p}>
                  {PURPOSE_LABEL[p] ?? p}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Shed / pond" htmlFor="fb-house">
            <select id="fb-house" value={houseId} onChange={(e) => setHouseId(e.target.value)} className={`${selectClass} w-full`}>
              <option value="">None</option>
              {houses
                .filter((h) => h.species === sp)
                .map((h) => (
                  <option key={h.house_id} value={h.house_id}>
                    {h.name}
                    {h.capacity ? ` (${h.occupied}/${h.capacity})` : ""}
                  </option>
                ))}
            </select>
          </Field>
          <Field label="Placed on *" htmlFor="fb-placed">
            <Input id="fb-placed" type="date" value={placedOn} max={localDate()} onChange={(e) => setPlacedOn(e.target.value)} />
          </Field>
          <Field label="How many *" htmlFor="fb-count">
            <Input id="fb-count" type="number" min="1" step="1" value={count} onChange={(e) => setCount(e.target.value)} />
          </Field>
          <Field label="Age when placed (days)" htmlFor="fb-age">
            <Input id="fb-age" type="number" min="0" step="1" value={age} onChange={(e) => setAge(e.target.value)} />
          </Field>
          <Field label="Average weight (kg)" htmlFor="fb-weight">
            <Input id="fb-weight" type="number" min="0" step="0.001" value={avgWeight} onChange={(e) => setAvgWeight(e.target.value)} placeholder="0.040" />
          </Field>
          <Field label="Breed" htmlFor="fb-breed">
            <Input id="fb-breed" value={breed} onChange={(e) => setBreed(e.target.value)} placeholder="Cobb 430, BV-300…" />
          </Field>
          <Field label="Schedule" htmlFor="fb-schedule">
            <select id="fb-schedule" value={scheduleId} onChange={(e) => setScheduleId(e.target.value)} className={`${selectClass} w-full`}>
              <option value="">The standard one for the purpose</option>
              <option value="none">No schedule</option>
              {schedules.map((s) => (
                <option key={s.schedule_id} value={s.schedule_id}>
                  {s.name}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Feed plan" htmlFor="fb-feed-plan">
            <select id="fb-feed-plan" value={feedPlanId} onChange={(e) => setFeedPlanId(e.target.value)} className={`${selectClass} w-full`}>
              <option value="">The standard one for the purpose</option>
              <option value="none">No feed plan</option>
              {feedPlans.map((p) => (
                <option key={p.feed_plan_id} value={p.feed_plan_id}>
                  {p.name}
                </option>
              ))}
            </select>
          </Field>
          <Field label="From stock (optional)" htmlFor="fb-stock" className="col-span-2">
            <ProductPicker id="fb-stock" value={fromStock} onChange={setFromStock} placeholder="Day-old chicks, kids, fingerlings… in this branch's stock" />
          </Field>
          <Field label="Source" htmlFor="fb-source">
            <Input id="fb-source" value={source} onChange={(e) => setSource(e.target.value)} placeholder="The hatchery" />
          </Field>
          <Field label="Name" htmlFor="fb-name">
            <Input id="fb-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="Optional" />
          </Field>
        </form>
      )}
    </Modal>
  );
}

/** Add or edit a shed, pen or pond. */
export function HouseDialog({ house, onClose, onSaved }: { house: FarmHouse | null; onClose: () => void; onSaved: () => void }) {
  const branches = useFarmBranches();
  const [branchId, setBranchId] = useState(house?.branch_id ?? branches[0]?.branch_id ?? "");
  const branch = branches.find((b) => b.branch_id === branchId);
  const [name, setName] = useState(house?.name ?? "");
  const [kind, setKind] = useState<FarmHouse["kind"]>(house?.kind ?? "shed");
  const [species, setSpecies] = useState<string>(house?.species ?? branch?.species[0] ?? "");
  const [capacity, setCapacity] = useState(house?.capacity != null ? String(house.capacity) : "");
  const [area, setArea] = useState(house?.area_sqft != null ? String(house.area_sqft) : "");
  const [notes, setNotes] = useState(house?.notes ?? "");
  const [busy, setBusy] = useState(false);
  const sp = species || branch?.species[0] || "";
  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    const body = { name, kind, species: sp, capacity: num(capacity), area_sqft: num(area), notes };
    try {
      if (house) await api.patch(`/api/v1/farm/houses/${house.house_id}`, body);
      else await api.post("/api/v1/farm/houses", { ...body, branch_id: branchId });
      toast.success(`${name} saved`);
      onSaved();
      onClose();
    } catch (err) {
      toast.error(message(err, "Could not save it"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={house ? `Edit ${house.name}` : "New shed or pond"}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="farm-house" disabled={busy || !name.trim() || !sp}>
            {busy && <Loader2 className="animate-spin" aria-hidden />} Save
          </Button>
        </>
      }
    >
      <form id="farm-house" onSubmit={save} className="grid grid-cols-2 gap-3">
        {!house && (
          <Field label="Farm" htmlFor="fh-branch">
            <select id="fh-branch" value={branchId} onChange={(e) => setBranchId(e.target.value)} className={`${selectClass} w-full`}>
              {branches.map((b) => (
                <option key={b.branch_id} value={b.branch_id}>
                  {b.name}
                </option>
              ))}
            </select>
          </Field>
        )}
        <Field label="Name *" htmlFor="fh-name">
          <Input id="fh-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="Shed A" />
        </Field>
        <Field label="Kind" htmlFor="fh-kind">
          <select id="fh-kind" value={kind} onChange={(e) => setKind(e.target.value as FarmHouse["kind"])} className={`${selectClass} w-full`}>
            {(Object.keys(HOUSE_KIND_LABEL) as FarmHouse["kind"][]).map((k) => (
              <option key={k} value={k}>
                {HOUSE_KIND_LABEL[k]}
              </option>
            ))}
          </select>
        </Field>
        <Field label="For" htmlFor="fh-species">
          <select id="fh-species" value={sp} onChange={(e) => setSpecies(e.target.value)} className={`${selectClass} w-full`}>
            {(house ? [house.species, ...(branch?.species ?? []).filter((s) => s !== house.species)] : branch?.species ?? []).map((s) => (
              <option key={s} value={s}>
                {SPECIES_LABEL[s as FarmSpecies]}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Holds (animals)" htmlFor="fh-cap">
          <Input id="fh-cap" type="number" min="1" step="1" value={capacity} onChange={(e) => setCapacity(e.target.value)} />
        </Field>
        <Field label="Area (sq ft)" htmlFor="fh-area">
          <Input id="fh-area" type="number" min="0" step="any" value={area} onChange={(e) => setArea(e.target.value)} />
        </Field>
        <Field label="Notes" htmlFor="fh-notes" className="col-span-2">
          <Input id="fh-notes" value={notes} onChange={(e) => setNotes(e.target.value)} />
        </Field>
      </form>
    </Modal>
  );
}
