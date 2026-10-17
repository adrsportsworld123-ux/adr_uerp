"use client";

// Phase 5's Employee Management (erp-core-go's phased_roadmap.md;
// pos_frd_complete.md's HR section). Gated by hr.manage. There is no
// separate "employees" concept on the backend — POST /hr/employees is
// the first user-creation endpoint this codebase has ever had (see
// migrations/023_hr_payroll.sql's header comment); this page is
// therefore also, incidentally, the first UI anywhere in this app that
// can add a staff login.
import { Suspense, useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Employee, Shift, Branch, SalaryStructure } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { AlertModal } from "@/components/ui/dialog";
import { Column, DataTable } from "@/components/data/data-table";
import { EditDialog, EditField } from "@/components/data/edit-dialog";
import { MasterRowActions } from "@/components/data/master-actions";
import { PageHeader } from "@/components/data/page-header";
import { StatusChip } from "@/components/data/status-chip";
import { useListState } from "@/components/data/use-list-state";
import { useServerList } from "@/components/data/use-server-list";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

interface Role {
  id: string;
  name: string;
}

const columns: Column<Employee>[] = [
  { key: "name", header: "Name", sort: "name", cell: (e) => <span className="font-medium">{e.name}</span> },
  { key: "code", header: "Code", sort: "code", cell: (e) => <span className="font-mono text-xs">{e.employee_code}</span> },
  { key: "designation", header: "Designation", cell: (e) => e.designation || "—" },
  { key: "department", header: "Department", cell: (e) => e.department || "—" },
  { key: "roles", header: "Roles", cell: (e) => e.roles.join(", ") || "—" },
  { key: "joined", header: "Joined", sort: "joined", cell: (e) => e.date_of_joining || "—" },
  {
    key: "status",
    header: "Status",
    cell: (e) =>
      e.date_of_exit ? <StatusChip status="exited" tone="muted" label={`Exited ${e.date_of_exit}`} /> : <StatusChip status={e.status} />,
  },
];

const EDIT_FIELDS: EditField[] = [
  { key: "name", label: "Name", required: true },
  { key: "email", label: "Email (sign-in)", type: "email", required: true, half: true },
  { key: "phone", label: "Phone", type: "tel", half: true },
  { key: "designation", label: "Designation", half: true },
  { key: "department", label: "Department", half: true },
  { key: "pan_number", label: "PAN", upper: true, half: true },
  { key: "uan_number", label: "UAN", half: true },
  { key: "esi_number", label: "ESI number", half: true },
  { key: "bank_account_no", label: "Bank account", half: true },
  { key: "bank_ifsc", label: "IFSC", upper: true, half: true },
];

function EmployeesView() {
  const [roles, setRoles] = useState<Role[]>([]);
  const [shifts, setShifts] = useState<Shift[]>([]);
  const [branches, setBranches] = useState<Branch[]>([]);
  // Newest first by default, so someone you just added is at the top.
  const { state, update, apiQuery, activeFilterCount } = useListState({ sort: "created", dir: "desc", status: "all" });
  const list = useServerList<Employee>("/api/v1/hr/employees", "employees", apiQuery);
  const [confirmExit, setConfirmExit] = useState(false);
  const [editing, setEditing] = useState<Employee | null>(null);

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

  const reloadList = list.reload;
  const load = useCallback(() => {
    reloadList();
  }, [reloadList]);
  useEffect(() => {
    api.get<{ roles: Role[] }>("/api/v1/hr/roles").then((d) => setRoles(d.roles)).catch(() => {});
    api.get<{ shifts: Shift[] }>("/api/v1/hr/shifts?status=active").then((d) => setShifts(d.shifts)).catch(() => {});
    api.get<{ branches: Branch[] }>("/api/v1/branches").then((d) => setBranches(d.branches)).catch(() => {});
  }, []);

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
    setConfirmExit(false);
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
    <div className="flex flex-col gap-6">
      <PageHeader title="Employees" description="Staff records and logins. Click a row to manage shift, salary structure and exit." />

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

      <DataTable<Employee>
        columns={columns}
        rows={list.rows}
        total={list.total}
        loading={list.loading}
        error={list.error}
        onRetry={list.reload}
        state={state}
        update={(p) => {
          list.setLoading(true);
          update(p);
        }}
        rowKey={(e) => e.id}
        searchPlaceholder="Name, code, email, phone, designation"
        searchLabel="Search employees"
        activeFilterCount={activeFilterCount}
        onClearFilters={() => update({ q: "", status: "all" })}
        filters={
          <select aria-label="Status" value={state.status || "all"} onChange={(e) => update({ status: e.target.value })} className="h-9 rounded-lg border bg-card px-2 text-sm">
            <option value="all">All employees</option>
            <option value="active">Active</option>
            <option value="exited">Exited</option>
          </select>
        }
        empty={{ title: "No employees yet", body: "Add staff above to give them a login and include them in payroll." }}
        onRowClick={selectEmployee}
        rowActions={(e) => (
          <MasterRowActions
            name={e.name}
            noun="employee"
            status={e.status}
            onEdit={() => setEditing(e)}
            setStatus={(next) => api.patch(`/api/v1/hr/employees/${e.id}`, { status: next })}
            deactivateNote="They can't sign in to the POS or this app until reactivated. Their sales, attendance and payslips stay as they are."
            remove={{ entity: "employee", id: e.id, path: `/api/v1/hr/employees/${e.id}`, permission: "hr.manage" }}
            onChanged={list.reload}
          />
        )}
      />

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
                <Button variant="destructive" onClick={() => setConfirmExit(true)}>Mark as exited</Button>
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
                      <TableCell colSpan={5} className="text-center text-sm text-muted-foreground py-4">No salary structure on file</TableCell>
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
      {editing && (
        <EditDialog
          open
          onOpenChange={(o) => !o && setEditing(null)}
          title="Edit employee"
          description={editing.name}
          fields={EDIT_FIELDS}
          initial={{
            name: editing.name,
            email: editing.email ?? "",
            phone: editing.phone ?? "",
            designation: editing.designation ?? "",
            department: editing.department ?? "",
            pan_number: editing.pan_number ?? "",
            uan_number: editing.uan_number ?? "",
            esi_number: editing.esi_number ?? "",
            bank_account_no: editing.bank_account_no ?? "",
            bank_ifsc: editing.bank_ifsc ?? "",
          }}
          onSave={async (changes) => {
            await api.patch(`/api/v1/hr/employees/${editing.id}`, changes);
            toast.success(`Saved employee “${changes.name ?? editing.name}”`);
            list.reload();
          }}
        />
      )}
      <AlertModal
        open={confirmExit}
        onOpenChange={setConfirmExit}
        title={`Mark ${selected?.name ?? "this employee"} as exited today?`}
        actions={
          <>
            <Button variant="outline" onClick={() => setConfirmExit(false)}>
              Cancel
            </Button>
            <Button variant="destructive" onClick={markExited}>
              Mark as exited
            </Button>
          </>
        }
      >
        Their login is disabled immediately. Payroll still covers days up to today, and their records stay. This can&rsquo;t be
        undone from this screen.
      </AlertModal>
    </div>
  );
}

export default function EmployeesPage() {
  return (
    <Suspense>
      <EmployeesView />
    </Suspense>
  );
}
