"use client";

// Phase 5's Shift Management sub-area. Gated by hr.manage. Edit,
// activate/deactivate and delete follow the uniform master lifecycle
// (erp-core-go migrations/054): an inactive shift stays on the employees
// already on it but isn't offered for new assignments; a shift anyone is
// assigned to can't be deleted.
import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Shift, Branch } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { StatusChip } from "@/components/data/status-chip";
import { EditDialog, EditField } from "@/components/data/edit-dialog";
import { MasterRowActions } from "@/components/data/master-actions";

const EDIT_FIELDS: EditField[] = [
  { key: "name", label: "Name", required: true },
  { key: "start_time", label: "Start (HH:MM)", required: true, half: true, placeholder: "09:00" },
  { key: "end_time", label: "End (HH:MM)", required: true, half: true, placeholder: "18:00" },
];

export default function ShiftsPage() {
  const [shifts, setShifts] = useState<Shift[]>([]);
  const [branches, setBranches] = useState<Branch[]>([]);
  const [name, setName] = useState("");
  const [branchId, setBranchId] = useState("");
  const [startTime, setStartTime] = useState("09:00");
  const [endTime, setEndTime] = useState("18:00");
  const [editing, setEditing] = useState<Shift | null>(null);

  const load = useCallback(() => {
    api.get<{ shifts: Shift[] }>("/api/v1/hr/shifts").then((d) => setShifts(d.shifts));
    api.get<{ branches: Branch[] }>("/api/v1/branches").then((d) => setBranches(d.branches));
  }, []);
  useEffect(load, [load]);

  async function createShift() {
    try {
      await api.post("/api/v1/hr/shifts", { name, branch_id: branchId || undefined, start_time: startTime, end_time: endTime });
      toast.success("Shift created");
      setName("");
      load();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not create shift");
    }
  }

  return (
    <div className="flex flex-col gap-6 max-w-3xl">
      <h1 className="text-2xl font-semibold">Shifts</h1>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Add shift</CardTitle>
        </CardHeader>
        <CardContent className="flex items-end gap-4 flex-wrap">
          <div className="flex flex-col gap-2 w-40">
            <Label htmlFor="name">Name</Label>
            <Input id="name" value={name} onChange={(e) => setName(e.target.value)} placeholder="Morning" />
          </div>
          <div className="flex flex-col gap-2 w-44">
            <Label>Branch</Label>
            <Select value={branchId} onValueChange={(v) => setBranchId(v ?? "")} items={Object.fromEntries(branches.map((b) => [b.branch_id, b.name]))}>
              <SelectTrigger><SelectValue placeholder="All branches" /></SelectTrigger>
              <SelectContent>
                {branches.map((b) => (
                  <SelectItem key={b.branch_id} value={b.branch_id}>{b.name}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-2 w-32">
            <Label htmlFor="start">Start time</Label>
            <Input id="start" type="time" value={startTime} onChange={(e) => setStartTime(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2 w-32">
            <Label htmlFor="end">End time</Label>
            <Input id="end" type="time" value={endTime} onChange={(e) => setEndTime(e.target.value)} />
          </div>
          <Button onClick={createShift} disabled={!name}>Add shift</Button>
        </CardContent>
      </Card>

      <Card>
        <CardContent className="pt-6">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Start</TableHead>
                <TableHead>End</TableHead>
                <TableHead>Status</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {shifts.map((s) => (
                <TableRow key={s.id}>
                  <TableCell>{s.name}</TableCell>
                  <TableCell>{s.start_time}</TableCell>
                  <TableCell>{s.end_time}</TableCell>
                  <TableCell>
                    <StatusChip status={s.status ?? "active"} />
                  </TableCell>
                  <TableCell className="text-right">
                    <div className="flex justify-end">
                      <MasterRowActions
                        name={s.name}
                        noun="shift"
                        status={s.status ?? "active"}
                        onEdit={() => setEditing(s)}
                        setStatus={(next) => api.patch(`/api/v1/hr/shifts/${s.id}`, { status: next })}
                        deactivateNote="Employees already on this shift keep it; it won't be offered for new assignments."
                        remove={{ entity: "shift", id: s.id, path: `/api/v1/hr/shifts/${s.id}`, permission: "hr.manage" }}
                        onChanged={load}
                      />
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
      {editing && (
        <EditDialog
          open
          onOpenChange={(o) => !o && setEditing(null)}
          title="Edit shift"
          description={editing.name}
          fields={EDIT_FIELDS}
          initial={{ name: editing.name, start_time: editing.start_time.slice(0, 5), end_time: editing.end_time.slice(0, 5) }}
          onSave={async (changes) => {
            await api.patch(`/api/v1/hr/shifts/${editing.id}`, changes);
            toast.success(`Saved shift “${changes.name ?? editing.name}”`);
            load();
          }}
        />
      )}
    </div>
  );
}
