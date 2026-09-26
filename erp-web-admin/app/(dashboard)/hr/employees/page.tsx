"use client";

// Phase 5's Employee Management (erp-core-go's phased_roadmap.md;
// pos_frd_complete.md's HR section). Gated by hr.manage. There is no
// separate "employees" concept on the backend — POST /hr/employees is
// the first user-creation endpoint this codebase has ever had (see
// migrations/023_hr_payroll.sql's header comment); this page is
// therefore also, incidentally, the first UI anywhere in this app that
// can add a staff login.
import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Employee, Shift, Branch, SalaryStructure } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

interface Role {
  id: string;
  name: string;
}

export default function EmployeesPage() {
  const [employees, setEmployees] = useState<Employee[]>([]);
  const [roles, setRoles] = useState<Role[]>([]);
  const [shifts, setShifts] = useState<Shift[]>([]);
  const [branches, setBranches] = useState<Branch[]>([]);
  const [error, setError] = useState<string | null>(null);

  const [name, setName] = useState("");
  const [employeeCode, setEmployeeCode] = useState("");
  const [email, setEmail] = useState("");
  const [phone, setPhone] = useState("");
  const [designation, setDesignation] = useState("");
  const [department, setDepartment] = useState("");
  const [branchId, setBranchId] = useState("");
  const [roleId, setRoleId] = useState("");
  const [dateOfJoining, setDateOfJoining] = useState("");
  const [pin, setPin] = useState("");

  const [selected, setSelected] = useState<Employee | null>(null);
  const [salaryHistory, setSalaryHistory] = useState<SalaryStructure[]>([]);
  const [newBasic, setNewBasic] = useState("");
  const [newHRA, setNewHRA] = useState("");
  const [newSpecial, setNewSpecial] = useState("");
  const [newEffectiveFrom, setNewEffectiveFrom] = useState("");

  const load = useCallback(() => {
    api
      .get<{ employees: Employee[] }>("/api/v1/hr/employees")
      .then((d) => {
        setEmployees(d.employees);
        setError(null);
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load employees"));
    api.get<{ roles: Role[] }>("/api/v1/hr/roles").then((d) => setRoles(d.roles)).catch(() => {});
    api.get<{ shifts: Shift[] }>("/api/v1/hr/shifts").then((d) => setShifts(d.shifts)).catch(() => {});
    api.get<{ branches: Branch[] }>("/api/v1/branches").then((d) => setBranches(d.branches)).catch(() => {});
  }, []);
  useEffect(load, [load]);

  async function createEmployee() {
    try {
      await api.post("/api/v1/hr/employees", {
        name,
        employee_code: employeeCode,
        email: email || undefined,
        phone: phone || undefined,
        designation: designation || undefined,
        department: department || undefined,
        branch_id: branchId || undefined,
        role_id: roleId || undefined,
        date_of_joining: dateOfJoining || undefined,
        pin: pin || undefined,
      });
      toast.success("Employee created");
      setName("");
      setEmployeeCode("");
      setEmail("");
      setPhone("");
      setDesignation("");
      setDepartment("");
      setBranchId("");
      setRoleId("");
      setDateOfJoining("");
      setPin("");
      load();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not create employee");
    }
  }

  function selectEmployee(emp: Employee) {
    setSelected(emp);
    api.get<{ salary_structure_history: SalaryStructure[] }>(`/api/v1/hr/employees/${emp.id}/salary-structure`).then((d) => setSalaryHistory(d.salary_structure_history));
  }

  async function assignShift(shiftId: string) {
    if (!selected) return;
    try {
      const updated = await api.patch<Employee>(`/api/v1/hr/employees/${selected.id}`, { shift_id: shiftId });
      setSelected(updated);
      load();
      toast.success("Shift assigned");
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not assign shift");
    }
  }

  async function markExited() {
    if (!selected) return;
    try {
      const today = new Date().toISOString().slice(0, 10);
      const updated = await api.patch<Employee>(`/api/v1/hr/employees/${selected.id}`, { date_of_exit: today });
      setSelected(updated);
      load();
      toast.success("Employee marked as exited");
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not update employee");
    }
  }

  async function addSalaryStructure() {
    if (!selected) return;
    try {
      await api.post(`/api/v1/hr/employees/${selected.id}/salary-structure`, {
        effective_from: newEffectiveFrom,
        basic: Number(newBasic),
        hra: Number(newHRA || 0),
        special_allowance: Number(newSpecial || 0),
        other_allowances: 0,
      });
      toast.success("Salary structure saved");
      setNewBasic("");
      setNewHRA("");
      setNewSpecial("");
      setNewEffectiveFrom("");
      selectEmployee(selected);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not save salary structure");
    }
  }

  return (
    <div className="flex flex-col gap-6 max-w-5xl">
      <h1 className="text-2xl font-semibold">Employees</h1>
      {error && <p className="text-sm text-red-600">{error}</p>}

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Add employee</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-wrap items-end gap-4">
          <div className="flex flex-col gap-2 w-48">
            <Label htmlFor="name">Name</Label>
            <Input id="name" value={name} onChange={(e) => setName(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2 w-36">
            <Label htmlFor="employeeCode">Employee code</Label>
            <Input id="employeeCode" value={employeeCode} onChange={(e) => setEmployeeCode(e.target.value)} placeholder="EMP004" />
          </div>
          <div className="flex flex-col gap-2 w-56">
            <Label htmlFor="email">Email</Label>
            <Input id="email" value={email} onChange={(e) => setEmail(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2 w-40">
            <Label htmlFor="phone">Phone</Label>
            <Input id="phone" value={phone} onChange={(e) => setPhone(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2 w-40">
            <Label htmlFor="designation">Designation</Label>
            <Input id="designation" value={designation} onChange={(e) => setDesignation(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2 w-40">
            <Label htmlFor="department">Department</Label>
            <Input id="department" value={department} onChange={(e) => setDepartment(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2 w-44">
            <Label>Branch</Label>
            <Select value={branchId} onValueChange={(v) => setBranchId(v ?? "")} items={Object.fromEntries(branches.map((b) => [b.branch_id, b.name]))}>
              <SelectTrigger><SelectValue placeholder="None" /></SelectTrigger>
              <SelectContent>
                {branches.map((b) => (
                  <SelectItem key={b.branch_id} value={b.branch_id}>{b.name}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-2 w-44">
            <Label>Role</Label>
            <Select value={roleId} onValueChange={(v) => setRoleId(v ?? "")} items={Object.fromEntries(roles.map((r) => [r.id, r.name]))}>
              <SelectTrigger><SelectValue placeholder="None" /></SelectTrigger>
              <SelectContent>
                {roles.map((r) => (
                  <SelectItem key={r.id} value={r.id}>{r.name}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-2 w-40">
            <Label htmlFor="doj">Date of joining</Label>
            <Input id="doj" type="date" value={dateOfJoining} onChange={(e) => setDateOfJoining(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2 w-28">
            <Label htmlFor="pin">PIN (optional)</Label>
            <Input id="pin" value={pin} onChange={(e) => setPin(e.target.value)} placeholder="4-6 digits" />
          </div>
          <Button onClick={createEmployee} disabled={!name || !employeeCode}>Add employee</Button>
        </CardContent>
      </Card>

      <Card>
        <CardContent className="pt-6">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Code</TableHead>
                <TableHead>Designation</TableHead>
                <TableHead>Department</TableHead>
                <TableHead>Roles</TableHead>
                <TableHead>Status</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {employees.map((e) => (
                <TableRow key={e.id} className="cursor-pointer" onClick={() => selectEmployee(e)}>
                  <TableCell>{e.name}</TableCell>
                  <TableCell className="font-mono text-xs">{e.employee_code}</TableCell>
                  <TableCell>{e.designation || "—"}</TableCell>
                  <TableCell>{e.department || "—"}</TableCell>
                  <TableCell>{e.roles.join(", ") || "—"}</TableCell>
                  <TableCell>
                    <Badge variant={e.date_of_exit ? "outline" : "default"}>{e.date_of_exit ? "exited" : "active"}</Badge>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      {selected && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">{selected.name} — details</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            <div className="flex items-end gap-4 flex-wrap">
              <div className="flex flex-col gap-2 w-48">
                <Label>Shift</Label>
                <Select value={selected.shift_id ?? ""} onValueChange={(v) => v && assignShift(v)} items={Object.fromEntries(shifts.map((s) => [s.id, `${s.name} (${s.start_time}-${s.end_time})`]))}>
                  <SelectTrigger><SelectValue placeholder="None" /></SelectTrigger>
                  <SelectContent>
                    {shifts.map((s) => (
                      <SelectItem key={s.id} value={s.id}>{s.name} ({s.start_time}-{s.end_time})</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              {!selected.date_of_exit && (
                <Button variant="destructive" onClick={markExited}>Mark as exited</Button>
              )}
            </div>

            <div>
              <p className="text-sm font-medium mb-2">Salary structure history</p>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Effective from</TableHead>
                    <TableHead>Basic</TableHead>
                    <TableHead>HRA</TableHead>
                    <TableHead>Special</TableHead>
                    <TableHead>PF applicable</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {salaryHistory.map((s) => (
                    <TableRow key={s.effective_from}>
                      <TableCell>{s.effective_from}</TableCell>
                      <TableCell>{s.basic}</TableCell>
                      <TableCell>{s.hra}</TableCell>
                      <TableCell>{s.special_allowance}</TableCell>
                      <TableCell>{s.pf_applicable ? "Yes" : "No"}</TableCell>
                    </TableRow>
                  ))}
                  {salaryHistory.length === 0 && (
                    <TableRow>
                      <TableCell colSpan={5} className="text-center text-sm text-zinc-500 py-4">No salary structure on file</TableCell>
                    </TableRow>
                  )}
                </TableBody>
              </Table>
            </div>

            <div className="flex items-end gap-4 flex-wrap">
              <div className="flex flex-col gap-2 w-40">
                <Label htmlFor="newEffectiveFrom">Effective from</Label>
                <Input id="newEffectiveFrom" type="date" value={newEffectiveFrom} onChange={(e) => setNewEffectiveFrom(e.target.value)} />
              </div>
              <div className="flex flex-col gap-2 w-32">
                <Label htmlFor="newBasic">Basic</Label>
                <Input id="newBasic" type="number" value={newBasic} onChange={(e) => setNewBasic(e.target.value)} />
              </div>
              <div className="flex flex-col gap-2 w-32">
                <Label htmlFor="newHRA">HRA</Label>
                <Input id="newHRA" type="number" value={newHRA} onChange={(e) => setNewHRA(e.target.value)} />
              </div>
              <div className="flex flex-col gap-2 w-32">
                <Label htmlFor="newSpecial">Special allowance</Label>
                <Input id="newSpecial" type="number" value={newSpecial} onChange={(e) => setNewSpecial(e.target.value)} />
              </div>
              <Button onClick={addSalaryStructure} disabled={!newEffectiveFrom || !newBasic}>Save salary structure</Button>
            </div>
          </CardContent>
        </Card>
      )}
    </div>
  );
}
