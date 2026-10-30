"use client";

// Every payslip of a payroll run, one to a page, to print together (or
// save as one PDF from the print dialog) — erp-core-go §3.78,
// GET /payroll/runs/{id}/payslips. A single payslip's PDF comes from the
// API (the payroll page's download button); this is the same content.
import { useEffect, useState } from "react";
import { useParams } from "next/navigation";
import { Printer } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import type { PayslipDocument } from "@/lib/types";
import { rupees, shortDate } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { PageHeader } from "@/components/data/page-header";

const nonZero = (v?: string | null) => Number(v ?? 0) !== 0;

export default function PrintPayslipsPage() {
  const { id } = useParams<{ id: string }>();
  const [slips, setSlips] = useState<PayslipDocument[] | null>(null);
  useEffect(() => {
    api
      .get<{ payslips: PayslipDocument[] }>(`/api/v1/payroll/runs/${id}/payslips`)
      .then((d) => setSlips(d.payslips))
      .catch((e) => toast.error(e instanceof ApiError ? e.message : "Could not load the payslips"));
  }, [id]);
  if (!slips) return <p className="text-sm text-muted-foreground">Loading…</p>;
  return (
    <div className="max-w-4xl space-y-6">
      <div className="print:hidden">
        <PageHeader
          title={`Payslips · ${slips[0]?.period ?? ""}`}
          description={`${slips.length} payslip(s), one to a page.`}
          actions={
            <Button onClick={() => window.print()}>
              <Printer aria-hidden /> Print
            </Button>
          }
        />
      </div>
      {slips.map((p) => (
        <PayslipSheet key={p.payslip_id} p={p} />
      ))}
    </div>
  );
}

function PayslipSheet({ p }: { p: PayslipDocument }) {
  const earn: [string, string][] = (
    [
      ["Basic", p.basic],
      ["HRA", p.hra],
      ["Special allowance", p.special_allowance],
      ["Other allowances", p.other_allowances],
      ["Commission", p.commission_amount],
    ] as [string, string][]
  ).filter(([, v], i) => i === 0 || nonZero(v));
  const ded: [string, string][] = (
    [
      ["Provident fund", p.pf_employee],
      ["ESI", p.esi_employee],
      ["Professional tax", p.pt_amount],
      ["Income tax (TDS)", p.tds_amount],
      ["Labour welfare fund", p.lwf_employee],
      ["Loan / advance recovery", p.loan_deduction ?? "0"],
    ] as [string, string][]
  ).filter(([, v]) => nonZero(v));
  const e = p.employee;
  const rows = Math.max(earn.length, ded.length);
  const days = [
    nonZero(p.leave_paid_days) && `paid leave ${Number(p.leave_paid_days)} day(s)`,
    nonZero(p.leave_unpaid_days) && `unpaid leave ${Number(p.leave_unpaid_days)} day(s)`,
    nonZero(p.absent_days) && `absent ${Number(p.absent_days)} day(s)`,
    p.permission_unpaid_minutes ? `unpaid permission ${p.permission_unpaid_minutes} min` : "",
  ].filter(Boolean);
  return (
    <section className="break-after-page rounded-xl border bg-card p-6 text-sm print:rounded-none print:border-0 print:p-0" data-testid="payslip-sheet">
      <header className="flex items-start justify-between gap-4 border-b pb-3">
        <div>
          <h2 className="text-lg font-semibold">{p.company.name}</h2>
          {(p.company.address ?? []).map((l) => (
            <p key={l} className="text-xs text-muted-foreground">
              {l}
            </p>
          ))}
        </div>
        <div className="text-right">
          <p className="text-base font-semibold">PAYSLIP</p>
          <p className="text-xs">For {p.period}</p>
          {p.run_status !== "finalized" && <p className="text-xs font-semibold text-destructive">DRAFT</p>}
        </div>
      </header>
      <div className="grid grid-cols-2 gap-4 py-3">
        <div>
          <p className="font-medium">{p.name}</p>
          <p className="text-xs text-muted-foreground">{[e.employee_code, e.designation, e.department].filter(Boolean).join(" · ")}</p>
          {e.branch && <p className="text-xs text-muted-foreground">Branch: {e.branch}</p>}
          {e.date_of_joining && <p className="text-xs text-muted-foreground">Joined: {shortDate(e.date_of_joining)}</p>}
        </div>
        <dl className="grid grid-cols-2 gap-x-3 text-xs">
          {(
            [
              ["PAN", e.pan_number],
              ["UAN", e.uan_number],
              ["ESI no.", e.esi_number],
              ["Bank a/c", e.bank_account_no ? `XXXX${e.bank_account_no.slice(-4)}` : null],
              ["Days in month", String(Number(p.days_in_period))],
              ["Days paid", String(Number(p.days_present))],
            ] as [string, string | null][]
          )
            .filter(([, v]) => v)
            .map(([k, v]) => (
              <div key={k} className="contents">
                <dt className="text-muted-foreground">{k}</dt>
                <dd className="text-right">{v}</dd>
              </div>
            ))}
        </dl>
      </div>
      <table className="w-full border-collapse text-sm">
        <thead>
          <tr className="bg-muted text-left">
            <th className="px-2 py-1.5">Earnings</th>
            <th className="px-2 py-1.5 text-right">Amount</th>
            <th className="px-2 py-1.5">Deductions</th>
            <th className="px-2 py-1.5 text-right">Amount</th>
          </tr>
        </thead>
        <tbody>
          {Array.from({ length: rows }, (_, i) => (
            <tr key={i} className="border-b">
              <td className="px-2 py-1.5">{earn[i]?.[0]}</td>
              <td className="tabular px-2 py-1.5 text-right">{earn[i] ? rupees(earn[i][1]) : ""}</td>
              <td className="px-2 py-1.5">{ded[i]?.[0]}</td>
              <td className="tabular px-2 py-1.5 text-right">{ded[i] ? rupees(ded[i][1]) : ""}</td>
            </tr>
          ))}
          <tr className="font-medium">
            <td className="px-2 py-1.5">Gross earnings</td>
            <td className="tabular px-2 py-1.5 text-right">{rupees(p.gross_earnings)}</td>
            <td className="px-2 py-1.5">Total deductions</td>
            <td className="tabular px-2 py-1.5 text-right">{rupees(p.total_deductions)}</td>
          </tr>
        </tbody>
      </table>
      <div className="mt-3 flex items-baseline justify-between border-t pt-3">
        <p className="text-xs text-muted-foreground">Net pay in words: {p.net_pay_words}</p>
        <p className="text-base font-semibold" data-testid="payslip-net">
          Net pay {rupees(p.net_pay)}
        </p>
      </div>
      {days.length > 0 && <p className="mt-2 text-xs text-muted-foreground">This month: {days.join(", ")}.</p>}
    </section>
  );
}
