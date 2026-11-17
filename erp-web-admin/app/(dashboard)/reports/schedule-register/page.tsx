"use client";

// The Schedule H1 register (erp-core-go migrations/084, §3.87): every
// Schedule H1 / H / X sale with the drug, quantity, batch, patient and
// prescriber with their addresses — from the sales themselves, by the
// merchant's day — as a list, a CSV and an A4 PDF to keep or show an
// inspector. Rows missing something the register needs say what.
// audit.view.
import { useCallback, useEffect, useState } from "react";
import { api, ApiError } from "@/lib/api-client";
import type { ScheduleRegisterLine } from "@/lib/types";
import { localDate, qty } from "@/lib/format";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { PageHeader } from "@/components/data/page-header";
import { Field, FilterBar, Panel, selectClass } from "@/components/data/panel";
import { BranchSelect, useMyBranches } from "@/components/data/branch-select";
import { DownloadPdfButton } from "@/components/data/document-actions";
import { StatusChip } from "@/components/data/status-chip";

const SCHEDULES = [
  { value: "H1", label: "Schedule H1" },
  { value: "H", label: "Schedule H" },
  { value: "X", label: "Schedule X" },
  { value: "", label: "Every schedule (not OTC)" },
];

export default function ScheduleRegisterPage() {
  const { can } = usePermissions();
  const allowed = can("audit.view");
  const { branches, defaultBranchId } = useMyBranches();
  const [branchId, setBranchId] = useState("");
  const [from, setFrom] = useState(localDate(-30));
  const [to, setTo] = useState(localDate());
  const [schedule, setSchedule] = useState("H1");
  const [lines, setLines] = useState<ScheduleRegisterLine[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const branch = branchId || defaultBranchId;

  const query = `branch_id=${branch}&from=${from}&to=${to}&schedule=${encodeURIComponent(schedule)}`;
  const load = useCallback(() => {
    if (!branch || !allowed) return;
    api
      .get<{ lines: ScheduleRegisterLine[] }>(`/api/v1/reports/schedule-register?${query}`)
      .then((d) => {
        setLines(d.lines);
        setError(null);
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load the register"));
  }, [branch, allowed, query]);
  useEffect(load, [load]);

  const gaps = (lines ?? []).filter((l) => l.missing.length > 0).length;
  return (
    <div className="max-w-5xl space-y-5">
      <PageHeader
        title="Schedule H1 Register"
        description="Each scheduled-drug sale with its batch, the patient and the prescriber, as the Drugs Rules ask a chemist to record. Keep the PDF, or hand the CSV to your consultant."
        actions={
          allowed && branch ? (
            <>
              <DownloadPdfButton path={`/api/v1/reports/schedule-register?${query}&format=csv`} label="CSV" />
              <DownloadPdfButton path={`/api/v1/reports/schedule-register?${query}&format=pdf`} label="PDF register" />
            </>
          ) : undefined
        }
      />
      {!allowed ? (
        <p className="text-sm text-muted-foreground">{missingPermissionHint("audit.view")}</p>
      ) : (
        <>
          <FilterBar onSubmit={load}>
            <Field label="Branch" htmlFor="reg-branch">
              <BranchSelect id="reg-branch" branches={branches} value={branch} onChange={setBranchId} />
            </Field>
            <Field label="From" htmlFor="reg-from">
              <Input id="reg-from" type="date" value={from} onChange={(e) => setFrom(e.target.value)} className="w-40" />
            </Field>
            <Field label="To" htmlFor="reg-to">
              <Input id="reg-to" type="date" value={to} onChange={(e) => setTo(e.target.value)} className="w-40" />
            </Field>
            <Field label="Schedule" htmlFor="reg-schedule">
              <select id="reg-schedule" value={schedule} onChange={(e) => setSchedule(e.target.value)} className={selectClass}>
                {SCHEDULES.map((s) => (
                  <option key={s.value} value={s.value}>
                    {s.label}
                  </option>
                ))}
              </select>
            </Field>
            <Button type="submit" variant="outline">
              Show
            </Button>
          </FilterBar>
          {error && <p className="text-sm text-destructive">{error}</p>}
          {gaps > 0 && (
            <p className="rounded-lg border border-warn/40 bg-warn-soft p-3 text-sm text-warn" data-testid="register-gaps">
              {gaps} {gaps === 1 ? "entry lacks" : "entries lack"} something the register needs. Add the prescription, or the patient&apos;s address (on the
              customer) or the prescriber&apos;s address (on the prescription).
            </p>
          )}
          <Panel>
            <Table data-testid="schedule-register">
              <TableHeader>
                <TableRow>
                  <TableHead>Date · bill</TableHead>
                  <TableHead>Drug · batch</TableHead>
                  <TableHead className="text-right">Qty</TableHead>
                  <TableHead>Patient</TableHead>
                  <TableHead>Prescriber</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(lines ?? []).map((l) => (
                  <TableRow key={`${l.order_id}-${l.sku}`}>
                    <TableCell className="text-sm">
                      {l.date}
                      <div className="font-mono text-xs text-muted-foreground">{l.order_number}</div>
                    </TableCell>
                    <TableCell className="text-sm">
                      {l.product_name} <span className="text-muted-foreground">({l.drug_schedule})</span>
                      {l.batches && <div className="text-xs text-muted-foreground">Batch {l.batches}</div>}
                    </TableCell>
                    <TableCell className="tabular text-right">{qty(l.quantity)}</TableCell>
                    <TableCell className="text-sm">
                      {l.patient_name ?? "—"}
                      <div className="text-xs text-muted-foreground">{[l.patient_phone, l.patient_address].filter(Boolean).join(" · ")}</div>
                    </TableCell>
                    <TableCell className="text-sm">
                      {l.doctor_name ?? <StatusChip status="pending" label="No prescription" />}
                      <div className="text-xs text-muted-foreground">
                        {[l.doctor_reg_no && `Reg. ${l.doctor_reg_no}`, l.doctor_address].filter(Boolean).join(" · ")}
                      </div>
                      {l.missing.length > 0 && <div className="text-xs text-warn">Missing: {l.missing.join(", ")}</div>}
                    </TableCell>
                  </TableRow>
                ))}
                {lines && lines.length === 0 && (
                  <TableRow>
                    <TableCell colSpan={5} className="py-6 text-center text-sm text-muted-foreground">
                      No scheduled-drug sales in this period.
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          </Panel>
        </>
      )}
    </div>
  );
}
