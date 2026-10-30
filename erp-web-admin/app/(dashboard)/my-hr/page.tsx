"use client";

// My Leave & Payslips (erp-core-go §3.78): anyone signed in — their leave
// balance and this month's permission use, asking for leave, permission or
// an attendance correction, their requests (cancel one still waiting), and
// their payslips once the month's payroll is finalized (PDF).
import { useCallback, useEffect, useState } from "react";
import { CalendarClock, CalendarPlus, Clock } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api-client";
import { useAuth } from "@/lib/auth";
import type { LeaveBalances, Payslip } from "@/lib/types";
import { rupees } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { PageHeader } from "@/components/data/page-header";
import { Panel } from "@/components/data/panel";
import { DownloadPdfButton } from "@/components/data/document-actions";
import {
  AnyRequest,
  BalancesCard,
  DecideDialog,
  LeaveDialog,
  PermissionDialog,
  RegularizeDialog,
  RequestsTable,
  message,
  useRequests,
} from "@/components/hr/requests";

const MONTHS = ["January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"];

export default function MyHrPage() {
  const { userId } = useAuth();
  const [balances, setBalances] = useState<LeaveBalances | null>(null);
  const [payslips, setPayslips] = useState<Payslip[] | null>(null);
  const [asking, setAsking] = useState<"leave" | "permission" | "regularization" | null>(null);
  const [cancelling, setCancelling] = useState<AnyRequest | null>(null);
  const { rows, load } = useRequests("mine=1");

  const loadBalances = useCallback(() => {
    api
      .get<LeaveBalances>("/api/v1/hr/leave-balances")
      .then(setBalances)
      .catch((e) => toast.error(message(e, "Could not load your leave balance")));
  }, []);
  useEffect(loadBalances, [loadBalances]);
  useEffect(() => {
    if (!userId) return;
    api
      .get<{ payslips: Payslip[] }>(`/api/v1/payroll/employees/${userId}/payslips`)
      .then((d) => setPayslips(d.payslips))
      .catch(() => setPayslips([]));
  }, [userId]);

  const refresh = () => {
    load();
    loadBalances();
  };

  return (
    <div className="max-w-4xl space-y-5">
      <PageHeader
        title="My Leave & Payslips"
        description="Your leave balance, permission and attendance corrections, and your payslips. A manager approves what you ask for."
        actions={
          <>
            <Button onClick={() => setAsking("leave")}>
              <CalendarPlus aria-hidden /> Ask for leave
            </Button>
            <Button variant="outline" onClick={() => setAsking("permission")}>
              <Clock aria-hidden /> Permission
            </Button>
            <Button variant="outline" onClick={() => setAsking("regularization")}>
              <CalendarClock aria-hidden /> Correct attendance
            </Button>
          </>
        }
      />
      {balances && (
        <Panel title={`Leave ${balances.year_from.slice(0, 4)}${balances.year_from.slice(0, 4) !== balances.year_to.slice(0, 4) ? `–${balances.year_to.slice(2, 4)}` : ""}`}>
          <BalancesCard data={balances} />
        </Panel>
      )}
      <Panel title="My requests">
        {rows && (
          <RequestsTable
            rows={rows}
            testId="my-requests"
            empty="You haven't asked for anything yet."
            actions={(row) => (row.r.status === "pending" ? ["cancel"] : [])}
            onAct={(row) => setCancelling(row)}
          />
        )}
      </Panel>
      <Panel title="My payslips" description="A month's payslip appears here once its payroll is finalized.">
        <Table data-testid="my-payslips">
          <TableHeader>
            <TableRow>
              <TableHead>Month</TableHead>
              <TableHead className="text-right">Days paid</TableHead>
              <TableHead className="text-right">Gross</TableHead>
              <TableHead className="text-right">Deductions</TableHead>
              <TableHead className="text-right">Net pay</TableHead>
              <TableHead className="text-right">PDF</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {(payslips ?? []).map((p) => (
              <TableRow key={p.payslip_id}>
                <TableCell className="text-sm">
                  {MONTHS[(p.period_month ?? 1) - 1]} {p.period_year}
                </TableCell>
                <TableCell className="tabular text-right">
                  {Number(p.days_present)} / {Number(p.days_in_period)}
                </TableCell>
                <TableCell className="tabular text-right">{rupees(p.gross_earnings)}</TableCell>
                <TableCell className="tabular text-right">{rupees(p.total_deductions)}</TableCell>
                <TableCell className="tabular text-right font-medium">{rupees(p.net_pay)}</TableCell>
                <TableCell className="text-right">
                  <DownloadPdfButton path={`/api/v1/payroll/payslips/${p.payslip_id}/pdf`} label="Download" />
                </TableCell>
              </TableRow>
            ))}
            {payslips?.length === 0 && (
              <TableRow>
                <TableCell colSpan={6} className="py-6 text-center text-sm text-muted-foreground">
                  No payslips yet.
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </Panel>
      {asking === "leave" && <LeaveDialog onClose={() => setAsking(null)} onSaved={refresh} />}
      {asking === "permission" && <PermissionDialog onClose={() => setAsking(null)} onSaved={refresh} />}
      {asking === "regularization" && <RegularizeDialog onClose={() => setAsking(null)} onSaved={refresh} />}
      {cancelling && <DecideDialog row={cancelling} action="cancel" onClose={() => setCancelling(null)} onDone={refresh} />}
    </div>
  );
}
