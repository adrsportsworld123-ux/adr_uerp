"use client";

// Phase 5's statutory-rate configuration. Gated by payroll.manage — more
// sensitive than plain hr.manage, same tier as running payroll itself.
// Rates/ceilings shown here are editable per-merchant defaults, not
// authoritative — verify against the current EPFO/ESIC/state PT
// notification before relying on this for a real filing (see
// migrations/023_hr_payroll.sql's header comment).
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { StatutoryConfig, PTSlab } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

export default function StatutoryConfigPage() {
  const [cfg, setCfg] = useState<StatutoryConfig | null>(null);
  const [ptSlabs, setPtSlabs] = useState<PTSlab[]>([]);
  const [newSlabMin, setNewSlabMin] = useState("");
  const [newSlabMax, setNewSlabMax] = useState("");
  const [newSlabAmount, setNewSlabAmount] = useState("");

  useEffect(() => {
    api.get<StatutoryConfig>("/api/v1/hr/statutory-config").then((d) => {
      setCfg(d);
      setPtSlabs(d.pt_slabs);
    });
  }, []);

  async function save(patch: Partial<StatutoryConfig>) {
    try {
      const updated = await api.patch<StatutoryConfig>("/api/v1/hr/statutory-config", patch);
      setCfg(updated);
      setPtSlabs(updated.pt_slabs);
      toast.success("Statutory config updated");
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not update statutory config");
    }
  }

  function addSlab() {
    const slab: PTSlab = { min: Number(newSlabMin), max: newSlabMax ? Number(newSlabMax) : null, amount: Number(newSlabAmount) };
    const next = [...ptSlabs, slab].sort((a, b) => a.min - b.min);
    save({ pt_slabs: next });
    setNewSlabMin("");
    setNewSlabMax("");
    setNewSlabAmount("");
  }

  if (!cfg) return <p className="text-sm text-zinc-500">Loading…</p>;

  return (
    <div className="flex flex-col gap-6 max-w-3xl">
      <h1 className="text-2xl font-semibold">Statutory Configuration</h1>
      <p className="text-sm text-zinc-500">
        Editable defaults for this merchant. Verify against the current EPFO/ESIC/state PT notification before relying
        on these for a real filing — rates change over time and PT/LWF vary by state.
      </p>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">EPF</CardTitle>
        </CardHeader>
        <CardContent className="flex items-end gap-4 flex-wrap">
          <div className="flex flex-col gap-2 w-36">
            <Label htmlFor="pfEmp">Employee rate %</Label>
            <Input id="pfEmp" type="number" defaultValue={cfg.pf_employee_rate} onBlur={(e) => save({ pf_employee_rate: Number(e.target.value) })} />
          </div>
          <div className="flex flex-col gap-2 w-36">
            <Label htmlFor="pfEmpr">Employer rate %</Label>
            <Input id="pfEmpr" type="number" defaultValue={cfg.pf_employer_rate} onBlur={(e) => save({ pf_employer_rate: Number(e.target.value) })} />
          </div>
          <div className="flex flex-col gap-2 w-40">
            <Label htmlFor="pfCeil">Wage ceiling ₹</Label>
            <Input id="pfCeil" type="number" defaultValue={cfg.pf_wage_ceiling} onBlur={(e) => save({ pf_wage_ceiling: Number(e.target.value) })} />
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">ESI</CardTitle>
        </CardHeader>
        <CardContent className="flex items-end gap-4 flex-wrap">
          <div className="flex flex-col gap-2 w-36">
            <Label htmlFor="esiEmp">Employee rate %</Label>
            <Input id="esiEmp" type="number" step="0.01" defaultValue={cfg.esi_employee_rate} onBlur={(e) => save({ esi_employee_rate: Number(e.target.value) })} />
          </div>
          <div className="flex flex-col gap-2 w-36">
            <Label htmlFor="esiEmpr">Employer rate %</Label>
            <Input id="esiEmpr" type="number" step="0.01" defaultValue={cfg.esi_employer_rate} onBlur={(e) => save({ esi_employer_rate: Number(e.target.value) })} />
          </div>
          <div className="flex flex-col gap-2 w-40">
            <Label htmlFor="esiCeil">Wage ceiling ₹</Label>
            <Input id="esiCeil" type="number" defaultValue={cfg.esi_wage_ceiling} onBlur={(e) => save({ esi_wage_ceiling: Number(e.target.value) })} />
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Professional Tax slabs</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Min gross ₹</TableHead>
                <TableHead>Max gross ₹</TableHead>
                <TableHead>Amount ₹</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {ptSlabs.map((s, i) => (
                <TableRow key={i}>
                  <TableCell>{s.min}</TableCell>
                  <TableCell>{s.max ?? "∞"}</TableCell>
                  <TableCell>{s.amount}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          <div className="flex items-end gap-4">
            <div className="flex flex-col gap-2 w-32">
              <Label htmlFor="slabMin">Min</Label>
              <Input id="slabMin" type="number" value={newSlabMin} onChange={(e) => setNewSlabMin(e.target.value)} />
            </div>
            <div className="flex flex-col gap-2 w-32">
              <Label htmlFor="slabMax">Max (blank = ∞)</Label>
              <Input id="slabMax" type="number" value={newSlabMax} onChange={(e) => setNewSlabMax(e.target.value)} />
            </div>
            <div className="flex flex-col gap-2 w-32">
              <Label htmlFor="slabAmount">Amount</Label>
              <Input id="slabAmount" type="number" value={newSlabAmount} onChange={(e) => setNewSlabAmount(e.target.value)} />
            </div>
            <Button onClick={addSlab} disabled={!newSlabMin || !newSlabAmount}>Add slab</Button>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">LWF &amp; TDS</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <p className="text-xs text-amber-700">
            LWF is due annually or half-yearly, not monthly, and whatever you set here is added to every payroll run
            that follows. Leave both at 0 except for the specific run LWF is actually due — set the real amount right
            before running that one payroll, then reset to 0 afterward.
          </p>
          <div className="flex items-end gap-4 flex-wrap">
            <div className="flex flex-col gap-2 w-44">
              <Label htmlFor="lwfEmp">LWF employee ₹ (this run only)</Label>
              <Input id="lwfEmp" type="number" defaultValue={cfg.lwf_employee_amount} onBlur={(e) => save({ lwf_employee_amount: Number(e.target.value) })} />
            </div>
            <div className="flex flex-col gap-2 w-44">
              <Label htmlFor="lwfEmpr">LWF employer ₹ (this run only)</Label>
              <Input id="lwfEmpr" type="number" defaultValue={cfg.lwf_employer_amount} onBlur={(e) => save({ lwf_employer_amount: Number(e.target.value) })} />
            </div>
            <div className="flex flex-col gap-2 w-40">
              <Label htmlFor="tds">TDS rate % (flat)</Label>
              <Input id="tds" type="number" step="0.01" defaultValue={cfg.tds_rate_percent} onBlur={(e) => save({ tds_rate_percent: Number(e.target.value) })} />
            </div>
          </div>
        </CardContent>
      </Card>
      <p className="text-xs text-zinc-500">
        TDS here is a flat percentage of gross pay, not a real income-tax slab/regime computation — it defaults to 0
        (disabled) and needs a real TDS tool or a CA for an actual deployment above the exemption threshold.
      </p>
    </div>
  );
}
