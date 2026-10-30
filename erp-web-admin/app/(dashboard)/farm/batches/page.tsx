"use client";

// Flocks & Herds (erp-core-go §3.81): every batch, active or closed, with
// its figures; place a new one. farm.record (placing: farm.manage).
import { useCallback, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { Plus } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api-client";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import type { FarmBatch } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { PageHeader } from "@/components/data/page-header";
import { Field, FilterBar, Panel, selectClass } from "@/components/data/panel";
import { BatchDialog, FarmBranchSelect, SPECIES_LABEL, message } from "@/components/farm/farm";
import { BatchTable } from "@/components/farm/batch-table";

export default function BatchesPage() {
  const router = useRouter();
  const { can } = usePermissions();
  const [branchId, setBranchId] = useState("");
  const [status, setStatus] = useState("active");
  const [species, setSpecies] = useState("");
  const [batches, setBatches] = useState<FarmBatch[] | null>(null);
  const [placing, setPlacing] = useState(false);
  const load = useCallback(() => {
    const q = new URLSearchParams();
    if (branchId) q.set("branch_id", branchId);
    if (status) q.set("status", status);
    if (species) q.set("species", species);
    api
      .get<{ batches: FarmBatch[] }>(`/api/v1/farm/batches?${q.toString()}`)
      .then((d) => setBatches(d.batches))
      .catch((e) => toast.error(message(e, "Could not load the batches")));
  }, [branchId, status, species]);
  useEffect(load, [load]);

  if (!can("farm.record")) return <p className="text-sm text-muted-foreground">{missingPermissionHint("farm.record")}</p>;
  return (
    <div className="space-y-5">
      <PageHeader
        title="Flocks & Herds"
        description="Every flock, herd and pond stocking: age, alive, mortality, weight, FCR, eggs and cost per head."
        actions={
          <Button onClick={() => setPlacing(true)} disabled={!can("farm.manage")}>
            <Plus aria-hidden /> New batch
          </Button>
        }
      />
      <FilterBar>
        <Field label="Farm" htmlFor="fl-branch">
          <FarmBranchSelect id="fl-branch" value={branchId} onChange={setBranchId} allLabel="Every farm" />
        </Field>
        <Field label="Status" htmlFor="fl-status">
          <select id="fl-status" value={status} onChange={(e) => setStatus(e.target.value)} className={`${selectClass} w-32`}>
            <option value="active">Active</option>
            <option value="closed">Closed</option>
            <option value="">Any</option>
          </select>
        </Field>
        <Field label="Species" htmlFor="fl-species">
          <select id="fl-species" value={species} onChange={(e) => setSpecies(e.target.value)} className={`${selectClass} w-32`}>
            <option value="">Any</option>
            {Object.entries(SPECIES_LABEL).map(([k, v]) => (
              <option key={k} value={k}>
                {v}
              </option>
            ))}
          </select>
        </Field>
      </FilterBar>
      <Panel>{batches && <BatchTable batches={batches} />}</Panel>
      {placing && <BatchDialog onClose={() => setPlacing(false)} onSaved={(id) => router.push(`/farm/batches/${id}`)} />}
    </div>
  );
}
