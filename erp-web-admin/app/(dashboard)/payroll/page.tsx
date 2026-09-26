"use client";

// Phase 5's Payroll Run and Commission Engine sub-areas. Gated by
// payroll.manage. Challan generation is scoped honestly: this computes
// the correct total payable per statutory head from a run's payslips —
// the number that goes on a real EPFO/ESIC/PT challan — it does not
// produce or submit an actual government e-challan file (see
// internal/payroll/payroll.go's GetChallanSummary doc comment).
import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { CommissionRule, PayrollRun, ChallanSummary } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";

export default function PayrollPage() {
  const [rules, setRules] = useState<CommissionRule[]>([]);
  const [ruleName, setRuleName] = useState("");
  const [tier1Rate, setTier1Rate] = useState("1");
  const [tier2Min, setTier2Min] = useState("50000");
  const [tier2Rate, setTier2Rate] = useState("2");

  const [periodMonth, setPeriodMonth] = useState(String(new Date().getMonth() + 1));
  const [periodYear, setPeriodYear] = useState(String(new Date().getFullYear()));
  const [runs, setRuns] = useState<PayrollRun[]>([]);
  const [selectedRun, setSelectedRun] = useState<PayrollRun | null>(null);
  const [challan, setChallan] = useState<ChallanSummary | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(() => {
    api
      .get<{ commission_rules: CommissionRule[] }>("/api/v1/payroll/commission-rules")
      .then((d) => {
        setRules(d.commission_rules);
        setError(null);
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load commission rules"));
    api.get<{ payroll_runs: PayrollRun[] }>("/api/v1/payroll/runs").then((d) => setRuns(d.payroll_runs)).catch(() => {});
  }, []);
  useEffect(load, [load]);

  async function createRule() {
    try {
      await api.post("/api/v1/payroll/commission-rules", {
        name: ruleName,
        tiers: [
          { min_net_sales: 0, rate_percent: Number(tier1Rate) },
          { min_net_sales: Number(tier2Min), rate_percent: Number(tier2Rate) },
        ],
      });
      toast.success("Commission rule created");
      setRuleName("");
      load();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not create commission rule");
    }
  }

  async function computeCommission() {
    try {
      const resp = await api.post<{ commission_earnings: unknown[] }>("/api/v1/payroll/commission/compute", {
        period_month: Number(periodMonth),
        period_year: Number(periodYear),
      });
      toast.success(`Computed commission for ${resp.commission_earnings.length} cashier(s)`);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not compute commission");
    }
  }

  async function runPayroll() {
    try {
      const run = await api.post<PayrollRun>("/api/v1/payroll/runs", { period_month: Number(periodMonth), period_year: Number(periodYear) });
      toast.success("Payroll run computed");
      setSelectedRun(run);
      setChallan(null);
      load();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not run payroll");
    }
  }

  async function viewRun(id: string) {
    const run = await api.get<PayrollRun>(`/api/v1/payroll/runs/${id}`);
    setSelectedRun(run);
    setChallan(null);
  }

  async function finalizeRun(id: string) {
    try {
      const run = await api.post<PayrollRun>(`/api/v1/payroll/runs/${id}/finalize`);
      toast.success("Payroll run finalized");
      setSelectedRun((prev) => (prev ? { ...prev, status: run.status, finalized_at: run.finalized_at } : prev));
      load();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not finalize payroll run");
    }
  }

  async function loadChallan(id: string) {
    try {
      const summary = await api.get<ChallanSummary>(`/api/v1/payroll/runs/${id}/challan`);
      setChallan(summary);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not load challan summary");
    }
  }

  return (
    <div className="flex flex-col gap-6 max-w-5xl">
      <h1 className="text-2xl font-semibold">Payroll</h1>
      {error && <p className="text-sm text-red-600">{error}</p>}

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Commission rules</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <div className="flex items-end gap-4 flex-wrap">
            <div className="flex flex-col gap-2 w-56">
              <Label htmlFor="ruleName">Name</Label>
              <Input id="ruleName" value={ruleName} onChange={(e) => setRuleName(e.target.value)} placeholder="Standard cashier commission" />
            </div>
            <div className="flex flex-col gap-2 w-28">
              <Label htmlFor="tier1Rate">Base rate %</Label>
              <Input id="tier1Rate" type="number" value={tier1Rate} onChange={(e) => setTier1Rate(e.target.value)} />
            </div>
            <div className="flex flex-col gap-2 w-32">
              <Label htmlFor="tier2Min">Tier-2 min sales ₹</Label>
              <Input id="tier2Min" type="number" value={tier2Min} onChange={(e) => setTier2Min(e.target.value)} />
            </div>
            <div className="flex flex-col gap-2 w-28">
              <Label htmlFor="tier2Rate">Tier-2 rate %</Label>
              <Input id="tier2Rate" type="number" value={tier2Rate} onChange={(e) => setTier2Rate(e.target.value)} />
            </div>
            <Button onClick={createRule} disabled={!ruleName}>Add rule</Button>
          </div>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Tiers</TableHead>
                <TableHead>Status</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rules.map((r) => (
                <TableRow key={r.id}>
                  <TableCell>{r.name}</TableCell>
                  <TableCell className="text-xs">{r.tiers.map((t) => `${t.rate_percent}% ≥₹${t.min_net_sales}`).join(", ")}</TableCell>
                  <TableCell><Badge variant="outline">{r.status}</Badge></TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Run payroll</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <div className="flex items-end gap-4">
            <div className="flex flex-col gap-2 w-24">
              <Label htmlFor="month">Month</Label>
              <Input id="month" type="number" min={1} max={12} value={periodMonth} onChange={(e) => setPeriodMonth(e.target.value)} />
            </div>
            <div className="flex flex-col gap-2 w-28">
              <Label htmlFor="year">Year</Label>
              <Input id="year" type="number" value={periodYear} onChange={(e) => setPeriodYear(e.target.value)} />
            </div>
            <Button variant="outline" onClick={computeCommission}>Compute commission first</Button>
            <Button onClick={runPayroll}>Run / recompute payroll</Button>
          </div>
          <p className="text-xs text-zinc-500">
            Compute commission after the return window on this period&apos;s sales has closed, not right at period-end —
            computing early can pay commission on a sale that&apos;s later returned or voided.
          </p>

          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Period</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Gross</TableHead>
                <TableHead>Deductions</TableHead>
                <TableHead>Net</TableHead>
                <TableHead></TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {runs.map((r) => (
                <TableRow key={r.id} className="cursor-pointer" onClick={() => viewRun(r.id)}>
                  <TableCell>{r.period_month}/{r.period_year}</TableCell>
                  <TableCell><Badge variant={r.status === "finalized" ? "default" : "outline"}>{r.status}</Badge></TableCell>
                  <TableCell>{r.total_gross}</TableCell>
                  <TableCell>{r.total_deductions}</TableCell>
                  <TableCell>{r.total_net}</TableCell>
                  <TableCell>
                    {r.status === "draft" && (
                      <Button size="sm" variant="outline" onClick={(e) => { e.stopPropagation(); finalizeRun(r.id); }}>Finalize</Button>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      {selectedRun && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base flex items-center gap-2">
              Payslips — {selectedRun.period_month}/{selectedRun.period_year}
              <Badge variant={selectedRun.status === "finalized" ? "default" : "outline"}>{selectedRun.status}</Badge>
              <Button size="sm" variant="outline" onClick={() => loadChallan(selectedRun.id)}>Challan summary</Button>
            </CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            {selectedRun.skipped_employees && selectedRun.skipped_employees.length > 0 && (
              <p className="text-xs text-zinc-500">Skipped: {selectedRun.skipped_employees.join("; ")}</p>
            )}
            {challan && (
              <div className="text-sm border rounded-lg p-3 flex flex-col gap-1">
                <div className="flex gap-6 flex-wrap">
                  <span>PF payable: <strong>₹{challan.pf_total}</strong></span>
                  <span>ESI payable: <strong>₹{challan.esi_total}</strong></span>
                  <span>PT payable: <strong>₹{challan.pt_total}</strong></span>
                  <span>TDS payable: <strong>₹{challan.tds_total}</strong></span>
                  <span>LWF payable: <strong>₹{challan.lwf_total}</strong></span>
                </div>
                <p className="text-xs text-zinc-500">
                  PF employer split for the EPFO challan: EPS ₹{challan.pf_employer_eps} + EPF ₹{challan.pf_employer_epf}
                </p>
              </div>
            )}
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Employee</TableHead>
                  <TableHead>Gross</TableHead>
                  <TableHead>Days present</TableHead>
                  <TableHead>PF</TableHead>
                  <TableHead>ESI</TableHead>
                  <TableHead>PT</TableHead>
                  <TableHead>TDS</TableHead>
                  <TableHead>Net pay</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(selectedRun.payslips ?? []).map((p) => (
                  <TableRow key={p.user_id}>
                    <TableCell>{p.name}</TableCell>
                    <TableCell>{p.gross_earnings}</TableCell>
                    <TableCell>{p.days_present}/{p.days_in_period}</TableCell>
                    <TableCell>{p.pf_employee}</TableCell>
                    <TableCell>{p.esi_employee}</TableCell>
                    <TableCell>{p.pt_amount}</TableCell>
                    <TableCell>{p.tds_amount}</TableCell>
                    <TableCell className="font-medium">{p.net_pay}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      )}
    </div>
  );
}
