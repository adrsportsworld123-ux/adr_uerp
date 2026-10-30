"use client";

// Animals (erp-core-go §3.82): the register of cattle, goats, pigs and
// breeding rabbits — tag, breed, age, where each female is in her cycle,
// milk, withdrawal. ?batch= starts on one herd. farm.record; adding an
// animal: farm.manage.
import { Suspense, useCallback, useEffect, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { Plus } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api-client";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import type { FarmAnimal, FarmBatch } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/data/page-header";
import { Field, FilterBar, Panel, selectClass } from "@/components/data/panel";
import { FarmBranchSelect, REGISTER_SPECIES, SPECIES_LABEL, message } from "@/components/farm/farm";
import { AnimalDialog, AnimalTable } from "@/components/farm/animals";

function AnimalsList() {
  const { can } = usePermissions();
  const router = useRouter();
  const params = useSearchParams();
  const [branchId, setBranchId] = useState("");
  const [batchId, setBatchId] = useState(params.get("batch") ?? "");
  const [species, setSpecies] = useState("");
  const [status, setStatus] = useState("active");
  const [q, setQ] = useState("");
  const [animals, setAnimals] = useState<FarmAnimal[] | null>(null);
  const [herds, setHerds] = useState<FarmBatch[]>([]);
  const [adding, setAdding] = useState(false);
  const load = useCallback(() => {
    const qs = new URLSearchParams({ status, ...(branchId && { branch_id: branchId }), ...(batchId && { batch_id: batchId }), ...(species && { species }), ...(q && { q }) });
    api
      .get<{ animals: FarmAnimal[] }>(`/api/v1/farm/animals?${qs}`)
      .then((d) => setAnimals(d.animals))
      .catch((e) => toast.error(message(e, "Could not load the animals")));
  }, [branchId, batchId, species, status, q]);
  useEffect(load, [load]);
  useEffect(() => {
    api
      .get<{ batches: FarmBatch[] }>("/api/v1/farm/batches?status=active")
      .then((d) => setHerds(d.batches.filter((b) => REGISTER_SPECIES.includes(b.species))))
      .catch(() => setHerds([]));
  }, [animals]);
  if (!can("farm.record")) return <p className="text-sm text-muted-foreground">{missingPermissionHint("farm.record")}</p>;
  return (
    <div className="space-y-5">
      <PageHeader
        title="Animals"
        description="Cattle, goats, pigs and breeding rabbits by their tag: breeding, milk, health and how each left."
        actions={
          <Button onClick={() => setAdding(true)} disabled={!can("farm.manage")}>
            <Plus aria-hidden /> Add an animal
          </Button>
        }
      />
      <FilterBar>
        <Field label="Farm" htmlFor="an-branch">
          <FarmBranchSelect id="an-branch" value={branchId} onChange={setBranchId} allLabel="Every farm" />
        </Field>
        <Field label="Herd" htmlFor="an-batch">
          <select id="an-batch" value={batchId} onChange={(e) => setBatchId(e.target.value)} className={`${selectClass} w-48`}>
            <option value="">Every herd</option>
            {herds.map((b) => (
              <option key={b.batch_id} value={b.batch_id}>
                {b.batch_number} · {SPECIES_LABEL[b.species]}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Species" htmlFor="an-species">
          <select id="an-species" value={species} onChange={(e) => setSpecies(e.target.value)} className={`${selectClass} w-32`}>
            <option value="">All</option>
            {REGISTER_SPECIES.map((s) => (
              <option key={s} value={s}>
                {SPECIES_LABEL[s]}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Status" htmlFor="an-status">
          <select id="an-status" value={status} onChange={(e) => setStatus(e.target.value)} className={`${selectClass} w-36`}>
            <option value="active">On the farm</option>
            <option value="all">All</option>
            <option value="dead">Died</option>
            <option value="culled">Culled</option>
            <option value="harvested">Sold / harvested</option>
          </select>
        </Field>
        <Field label="Tag or name" htmlFor="an-q">
          <Input id="an-q" value={q} onChange={(e) => setQ(e.target.value)} className="w-40" />
        </Field>
      </FilterBar>
      <Panel title={`Register (${animals?.length ?? 0})`}>{animals ? <AnimalTable animals={animals} showBatch /> : <p className="text-sm text-muted-foreground">Loading…</p>}</Panel>
      {adding && <AnimalDialog batches={herds} batchId={batchId || undefined} onClose={() => setAdding(false)} onSaved={(id) => router.push(`/farm/animals/${id}`)} />}
    </div>
  );
}

export default function AnimalsPage() {
  return (
    <Suspense>
      <AnimalsList />
    </Suspense>
  );
}
