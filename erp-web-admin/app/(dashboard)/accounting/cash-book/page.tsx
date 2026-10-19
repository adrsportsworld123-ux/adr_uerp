"use client";

// Cash Book: one branch's movements on the Cash, Bank, Card Clearing and
// UPI Clearing accounts for a day, and the day's net movement.
import { useEffect, useState } from "react";
import { Printer } from "lucide-react";
import { api, ApiError } from "@/lib/api-client";
import { CashBook } from "@/lib/types";
import { SOURCE_LABEL, localDate, rupees } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/data/page-header";
import { Field, FilterBar, Panel, Stat } from "@/components/data/panel";
import { BranchSelect, useMyBranches } from "@/components/data/branch-select";
import { StatusChip } from "@/components/data/status-chip";

export default function CashBookPage() {
  const { branches, defaultBranchId } = useMyBranches();
  const [branchId, setBranchId] = useState("");
  const [date, setDate] = useState(localDate());
  const [book, setBook] = useState<CashBook | null>(null);
  const [error, setError] = useState<string | null>(null);
  const branch = branchId || defaultBranchId;

  useEffect(() => {
    if (!branch || !date) return;
    api
      .get<CashBook>(`/api/v1/accounting/cash-book?${new URLSearchParams({ branch_id: branch, date })}`)
      .then((d) => {
        setBook(d);
        setError(null);
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not reach the server"));
  }, [branch, date]);

  return (
    <div className="flex max-w-5xl flex-col gap-5">
      <PageHeader
        title="Cash Book"
        description="Cash, Bank, Card Clearing and UPI Clearing movements for one branch and day."
        actions={
          book && book.lines.length > 0 ? (
            <Button variant="outline" className="print:hidden" onClick={() => window.print()}>
              <Printer /> Print / PDF
            </Button>
          ) : undefined
        }
      />
      <FilterBar>
        <Field label="Branch" htmlFor="cb-branch">
          <BranchSelect id="cb-branch" branches={branches} value={branch} onChange={setBranchId} />
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

      {book && (
        <Panel>
          <div className="grid grid-cols-3 gap-4 border-b pb-4">
            <Stat label="Money in (debit)" value={rupees(book.total_debit)} testId="cash-total-debit" />
            <Stat label="Money out (credit)" value={rupees(book.total_credit)} testId="cash-total-credit" />
            <Stat label="Net movement" value={rupees(book.net_movement)} strong testId="cash-net" />
          </div>
          {book.lines.length === 0 ? (
            <p className="text-sm text-muted-foreground">No cash or bank movement on this date.</p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full min-w-[600px] text-sm">
                <thead className="text-left text-xs text-muted-foreground">
                  <tr>
                    <th className="pb-1 font-medium">Description</th>
                    <th className="pb-1 font-medium">Source</th>
                    <th className="w-36 pb-1 text-right font-medium">Debit</th>
                    <th className="w-36 pb-1 text-right font-medium">Credit</th>
                  </tr>
                </thead>
                <tbody>
                  {book.lines.map((l, i) => (
                    <tr key={i} className="border-t">
                      <td className="py-1.5">{l.description}</td>
                      <td className="py-1.5">
                        <StatusChip status={l.source_type} tone="muted" label={SOURCE_LABEL[l.source_type] ?? l.source_type} />
                      </td>
                      <td className="tabular py-1.5 text-right">{Number(l.debit) ? rupees(l.debit) : ""}</td>
                      <td className="tabular py-1.5 text-right">{Number(l.credit) ? rupees(l.credit) : ""}</td>
                    </tr>
                  ))}
                </tbody>
                <tfoot>
                  <tr className="border-t font-medium">
                    <td className="pt-2" colSpan={2}>
                      Total
                    </td>
                    <td className="tabular pt-2 text-right">{rupees(book.total_debit)}</td>
                    <td className="tabular pt-2 text-right">{rupees(book.total_credit)}</td>
                  </tr>
                </tfoot>
              </table>
            </div>
          )}
        </Panel>
      )}
    </div>
  );
}
