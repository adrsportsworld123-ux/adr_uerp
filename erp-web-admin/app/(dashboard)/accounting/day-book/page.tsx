"use client";

// Day Book: every journal entry a branch posted on one day, with its lines.
// An entry number opens the entry and the document that made it.
import { useEffect, useState } from "react";
import Link from "next/link";
import { Printer } from "lucide-react";
import { api, ApiError } from "@/lib/api-client";
import { DayBook, DayBookEntry } from "@/lib/types";
import { SOURCE_LABEL, localDate, rupees } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/data/page-header";
import { Field, FilterBar, Panel, Stat } from "@/components/data/panel";
import { BranchSelect, useMyBranches } from "@/components/data/branch-select";
import { StatusChip } from "@/components/data/status-chip";

export default function DayBookPage() {
  const { branches, defaultBranchId } = useMyBranches();
  const [branchId, setBranchId] = useState("");
  const [date, setDate] = useState(localDate());
  const [book, setBook] = useState<DayBook | null>(null);
  const [error, setError] = useState<string | null>(null);
  const branch = branchId || defaultBranchId;

  useEffect(() => {
    if (!branch || !date) return;
    api
      .get<DayBook>(`/api/v1/accounting/day-book?${new URLSearchParams({ branch_id: branch, date })}`)
      .then((d) => {
        setBook(d);
        setError(null);
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not reach the server"));
  }, [branch, date]);
  const entries = book?.entries;

  return (
    <div className="flex max-w-5xl flex-col gap-5">
      <PageHeader
        title="Day Book"
        description="Every journal entry a branch posted on one day, with its debit and credit lines."
        actions={
          entries && entries.length > 0 ? (
            <Button variant="outline" className="print:hidden" onClick={() => window.print()}>
              <Printer /> Print / PDF
            </Button>
          ) : undefined
        }
      />
      <FilterBar>
        <Field label="Branch" htmlFor="db-branch">
          <BranchSelect id="db-branch" branches={branches} value={branch} onChange={setBranchId} />
        </Field>
        <Field label="Date" htmlFor="date">
          <Input id="date" type="date" value={date} onChange={(e) => setDate(e.target.value)} className="w-40" />
        </Field>
      </FilterBar>

      {error && (
        <p className="text-sm text-destructive" role="alert">
          {error}
        </p>
      )}
      {book && book.entries.length > 0 && (
        <Panel>
          <div className="grid grid-cols-3 gap-4">
            <Stat label="Entries" value={book.entries.length} />
            <Stat label="Total debit" value={rupees(book.total_debit)} testId="day-total-debit" />
            <Stat label="Total credit" value={rupees(book.total_credit)} testId="day-total-credit" />
          </div>
        </Panel>
      )}
      {entries && entries.length === 0 && (
        <Panel>
          <p className="text-sm text-muted-foreground">No journal entries posted for this date.</p>
        </Panel>
      )}
      {entries?.map((entry) => (
          <Panel
            key={entry.entry_number}
            title={
              <>
                <Link href={`/accounting/journal-entries/${entry.entry_number}`} className="font-mono text-sm hover:underline">
                  {entry.entry_number}
                </Link>
                <StatusChip status={entry.source_type} tone={entry.source_type.startsWith("manual") ? "info" : "muted"} label={SOURCE_LABEL[entry.source_type] ?? entry.source_type} />
              </>
            }
            description={entry.description}
          >
            <LinesTable entry={entry} />
          </Panel>
      ))}
    </div>
  );
}

function LinesTable({ entry }: { entry: DayBookEntry }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[480px] text-sm">
        <thead className="text-left text-xs text-muted-foreground">
          <tr>
            <th className="pb-1 font-medium">Account</th>
            <th className="w-36 pb-1 text-right font-medium">Debit</th>
            <th className="w-36 pb-1 text-right font-medium">Credit</th>
          </tr>
        </thead>
        <tbody>
          {entry.lines.map((l, i) => (
            <tr key={i} className="border-t">
              <td className="py-1.5">
                {l.account_code} · {l.account_name}
              </td>
              <td className="tabular py-1.5 text-right">{Number(l.debit) ? rupees(l.debit) : ""}</td>
              <td className="tabular py-1.5 text-right">{Number(l.credit) ? rupees(l.credit) : ""}</td>
            </tr>
          ))}
        </tbody>
        <tfoot>
          <tr className="border-t font-medium">
            <td className="pt-2">Total</td>
            <td className="tabular pt-2 text-right">{rupees(entry.total_debit)}</td>
            <td className="tabular pt-2 text-right">{rupees(entry.total_credit)}</td>
          </tr>
        </tfoot>
      </table>
    </div>
  );
}
