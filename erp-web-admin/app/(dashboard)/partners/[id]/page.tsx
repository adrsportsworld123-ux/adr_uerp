"use client";

// One partner's capital statement (erp-core-go GET /partners/{id}/statement):
// opening capital, every entry on their capital account — pay-ins, pay-outs,
// bills and expenses they paid, any voucher — with a running balance, the
// closing capital, and their share of the period's profit. partners.manage.
import { useEffect, useState } from "react";
import Link from "next/link";
import { useParams, useRouter, useSearchParams } from "next/navigation";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Partner } from "@/lib/types";
import { localDate, rupees } from "@/lib/format";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableFooter, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { PageHeader } from "@/components/data/page-header";
import { Field, FilterBar, Panel, Stat } from "@/components/data/panel";
import { StatusChip, Tone } from "@/components/data/status-chip";
import { Button } from "@/components/ui/button";
import { Printer } from "lucide-react";

type Line = { date: string; entry_number: string; type: string; description: string; reference: string | null; in: string; out: string; balance: string };
type Statement = {
  partner: Partner;
  from: string;
  to: string;
  lines: Line[];
  opening_capital: string;
  paid_in: string;
  taken_out: string;
  closing_capital: string;
  net_profit: string;
  share_ratio: string;
  profit_share: string;
  closing_with_profit: string;
};

const TYPE: Record<string, { label: string; tone: Tone }> = {
  pay_in: { label: "Paid in", tone: "ok" },
  pay_out: { label: "Taken out", tone: "warn" },
  paid_expense: { label: "Paid an expense", tone: "info" },
  paid_bill: { label: "Paid a supplier", tone: "info" },
  reversal: { label: "Reversed", tone: "muted" },
  journal: { label: "Journal", tone: "plum" },
};

function fyStart(): string {
  const [y, m] = localDate().split("-").map(Number);
  return `${m >= 4 ? y : y - 1}-04-01`;
}

export default function PartnerStatementPage() {
  const { id } = useParams<{ id: string }>();
  const params = useSearchParams();
  const router = useRouter();
  const from = params.get("from") || fyStart();
  const to = params.get("to") || localDate();
  const [st, setSt] = useState<Statement | null>(null);

  useEffect(() => {
    api
      .get<Statement>(`/api/v1/partners/${id}/statement?from=${from}&to=${to}`)
      .then(setSt)
      .catch((e) => toast.error(e instanceof ApiError ? e.message : "Could not load the statement"));
  }, [id, from, to]);

  const setRange = (f: string, t: string) => router.replace(`/partners/${id}?from=${f}&to=${t}`);

  return (
    <div className="flex max-w-5xl flex-col gap-5">
      <PageHeader
        title={st ? `${st.partner.name} — capital statement` : "Capital statement"}
        description={st ? `${st.partner.account_code} · profit share ${st.share_ratio}%` : undefined}
        actions={
          <Button variant="outline" onClick={() => window.print()}>
            <Printer aria-hidden /> Print
          </Button>
        }
      />
      <FilterBar>
        <Field label="From" htmlFor="ps-from">
          <Input id="ps-from" type="date" value={from} max={to} onChange={(e) => setRange(e.target.value, to)} />
        </Field>
        <Field label="To" htmlFor="ps-to">
          <Input id="ps-to" type="date" value={to} min={from} onChange={(e) => setRange(from, e.target.value)} />
        </Field>
        <Link href="/partners" className="ms-auto self-center text-sm text-primary hover:underline">
          All partners
        </Link>
      </FilterBar>
      {st && (
        <>
          <Panel>
            <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
              <Stat label="Opening capital" value={rupees(st.opening_capital)} />
              <Stat label="Put in / paid" value={rupees(st.paid_in)} />
              <Stat label="Taken out" value={rupees(st.taken_out)} />
              <Stat label="Closing capital" value={rupees(st.closing_capital)} strong testId="ps-closing" />
            </div>
            <div className="grid grid-cols-2 gap-4 border-t pt-4 sm:grid-cols-4">
              <Stat label="Net profit in the period" value={rupees(st.net_profit)} />
              <Stat label={`Share (${st.share_ratio}%)`} value={rupees(st.profit_share)} testId="ps-share" />
              <Stat label="Capital with the share" value={rupees(st.closing_with_profit)} strong />
            </div>
          </Panel>
          <Panel title="Entries">
            <Table data-testid="ps-lines">
              <TableHeader>
                <TableRow>
                  <TableHead>Date</TableHead>
                  <TableHead>What</TableHead>
                  <TableHead>Entry</TableHead>
                  <TableHead className="text-right">In</TableHead>
                  <TableHead className="text-right">Out</TableHead>
                  <TableHead className="text-right">Balance</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                <TableRow>
                  <TableCell className="text-sm">{st.from}</TableCell>
                  <TableCell colSpan={4} className="text-sm text-muted-foreground">
                    Opening capital
                  </TableCell>
                  <TableCell className="tabular text-right">{rupees(st.opening_capital)}</TableCell>
                </TableRow>
                {st.lines.map((l) => (
                  <TableRow key={l.entry_number}>
                    <TableCell className="whitespace-nowrap text-sm">{l.date}</TableCell>
                    <TableCell className="max-w-80 whitespace-normal text-sm">
                      <StatusChip status="" tone={(TYPE[l.type] ?? TYPE.journal).tone} label={(TYPE[l.type] ?? TYPE.journal).label} />
                      <span className="block">{l.description}</span>
                      {l.reference && <span className="block text-xs text-muted-foreground">{l.reference}</span>}
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground">{l.entry_number}</TableCell>
                    <TableCell className="tabular text-right">{Number(l.in) ? rupees(l.in) : ""}</TableCell>
                    <TableCell className="tabular text-right">{Number(l.out) ? rupees(l.out) : ""}</TableCell>
                    <TableCell className="tabular text-right">{rupees(l.balance)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
              <TableFooter>
                <TableRow>
                  <TableCell colSpan={3}>Closing capital</TableCell>
                  <TableCell className="tabular text-right">{rupees(st.paid_in)}</TableCell>
                  <TableCell className="tabular text-right">{rupees(st.taken_out)}</TableCell>
                  <TableCell className="tabular text-right font-semibold">{rupees(st.closing_capital)}</TableCell>
                </TableRow>
              </TableFooter>
            </Table>
          </Panel>
        </>
      )}
    </div>
  );
}
