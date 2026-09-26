"use client";

// Phase 5's Shift Management sub-area. Gated by hr.manage.
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

export default function ShiftsPage() {
  const [shifts, setShifts] = useState<Shift[]>([]);
  const [branches, setBranches] = useState<Branch[]>([]);
  const [name, setName] = useState("");
  const [branchId, setBranchId] = useState("");
  const [startTime, setStartTime] = useState("09:00");
  const [endTime, setEndTime] = useState("18:00");

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
              </TableRow>
            </TableHeader>
            <TableBody>
              {shifts.map((s) => (
                <TableRow key={s.id}>
                  <TableCell>{s.name}</TableCell>
                  <TableCell>{s.start_time}</TableCell>
                  <TableCell>{s.end_time}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
    </div>
  );
}
