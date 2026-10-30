"use client";

// Sheds & Ponds (erp-core-go §3.81): where each farm keeps its animals —
// sheds, pens, cage houses, paddocks, ponds, tanks — with what each holds
// and what is in it now. farm.record (adding / editing: farm.manage).
import { useCallback, useEffect, useState } from "react";
import { Plus } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api-client";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import type { FarmHouse } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { PageHeader } from "@/components/data/page-header";
import { Field, FilterBar, Panel } from "@/components/data/panel";
import { StatusChip } from "@/components/data/status-chip";
import { FarmBranchSelect, HOUSE_KIND_LABEL, HouseDialog, SPECIES_LABEL, message } from "@/components/farm/farm";

export default function HousesPage() {
  const { can } = usePermissions();
  const [branchId, setBranchId] = useState("");
  const [houses, setHouses] = useState<FarmHouse[]>([]);
  const [editing, setEditing] = useState<FarmHouse | "new" | null>(null);
  const load = useCallback(() => {
    api
      .get<{ houses: FarmHouse[] }>(`/api/v1/farm/houses${branchId ? `?branch_id=${branchId}` : ""}`)
      .then((d) => setHouses(d.houses))
      .catch((e) => toast.error(message(e, "Could not load the sheds")));
  }, [branchId]);
  useEffect(load, [load]);

  async function toggle(h: FarmHouse) {
    try {
      await api.patch(`/api/v1/farm/houses/${h.house_id}`, { status: h.status === "active" ? "inactive" : "active" });
      toast.success(`${h.name} ${h.status === "active" ? "switched off" : "switched on"}`);
      load();
    } catch (e) {
      toast.error(message(e, "Could not change it"));
    }
  }

  if (!can("farm.record")) return <p className="text-sm text-muted-foreground">{missingPermissionHint("farm.record")}</p>;
  return (
    <div className="space-y-5">
      <PageHeader
        title="Sheds & Ponds"
        description="Where each farm keeps its animals, what each holds, and what is in it now."
        actions={
          <Button onClick={() => setEditing("new")} disabled={!can("farm.manage")}>
            <Plus aria-hidden /> New shed or pond
          </Button>
        }
      />
      <FilterBar>
        <Field label="Farm" htmlFor="fh-filter">
          <FarmBranchSelect id="fh-filter" value={branchId} onChange={setBranchId} allLabel="Every farm" />
        </Field>
      </FilterBar>
      <Panel>
        <Table data-testid="farm-houses">
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Kind</TableHead>
              <TableHead>For</TableHead>
              <TableHead className="text-right">In it / holds</TableHead>
              <TableHead>Batch</TableHead>
              <TableHead>Status</TableHead>
              <TableHead className="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {houses.map((h) => (
              <TableRow key={h.house_id}>
                <TableCell className="text-sm">
                  <span className="font-medium">{h.name}</span>
                  <span className="block text-xs text-muted-foreground">{h.branch}</span>
                </TableCell>
                <TableCell className="text-sm">{HOUSE_KIND_LABEL[h.kind]}</TableCell>
                <TableCell className="text-sm">{SPECIES_LABEL[h.species]}</TableCell>
                <TableCell className="tabular text-right">
                  {h.occupied.toLocaleString("en-IN")} / {h.capacity != null ? h.capacity.toLocaleString("en-IN") : "—"}
                </TableCell>
                <TableCell className="text-sm">{h.active_batch ?? "Empty"}</TableCell>
                <TableCell>
                  <StatusChip status="" tone={h.status === "active" ? "ok" : "muted"} label={h.status === "active" ? "Active" : "Inactive"} />
                </TableCell>
                <TableCell>
                  <div className="flex justify-end gap-1.5">
                    <Button size="sm" variant="ghost" disabled={!can("farm.manage")} onClick={() => setEditing(h)} aria-label={`Edit ${h.name}`}>
                      Edit
                    </Button>
                    <Button size="sm" variant="ghost" disabled={!can("farm.manage")} onClick={() => toggle(h)} aria-label={`${h.status === "active" ? "Deactivate" : "Activate"} ${h.name}`}>
                      {h.status === "active" ? "Deactivate" : "Activate"}
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ))}
            {houses.length === 0 && (
              <TableRow>
                <TableCell colSpan={7} className="py-6 text-center text-sm text-muted-foreground">
                  No sheds or ponds yet.
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </Panel>
      {editing && <HouseDialog house={editing === "new" ? null : editing} onClose={() => setEditing(null)} onSaved={load} />}
    </div>
  );
}
