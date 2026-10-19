"use client";

// One posted journal entry (Phase 14): its lines and the document that
// made it. Entries are read-only: a sale is undone by voiding or returning
// it, a voucher by reversing the voucher, and so on, from that document.
import { useEffect, useState } from "react";
import Link from "next/link";
import { useParams } from "next/navigation";
import { ArrowLeft } from "lucide-react";
import { api, ApiError } from "@/lib/api-client";
import { LedgerEntry } from "@/lib/types";
import { SOURCE_LABEL, rupees } from "@/lib/format";
import { PageHeader } from "@/components/data/page-header";
import { PartyCell } from "@/components/accounting/party-cell";

// Where each source document lives in this app (null: no page of its own).
function sourceHref(e: LedgerEntry): string | null {
  if (!e.source_id) return null;
  switch (e.source_type) {
    case "sale":
    case "sale_void":
      return `/sales/${e.source_id}`;
    case "manual":
    case "manual_reversal":
      return `/accounting/vouchers/${e.source_id}`;
    case "purchase_bill":
      return `/purchase/bills/${e.source_id}`;
    default:
      return null;
  }
}

export default function JournalEntryPage() {
  const { id } = useParams<{ id: string }>();
  const [e, setE] = useState<LedgerEntry | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    api
      .get<LedgerEntry>(`/api/v1/accounting/journal-entries/${id}`)
      .then(setE)
      .catch((err) => setError(err instanceof ApiError ? err.message : "Could not load the entry"));
  }, [id]);

  if (error) return <p className="text-sm text-destructive">{error}</p>;
  if (!e) return <p className="text-sm text-muted-foreground">Loading…</p>;
  const href = sourceHref(e);
  const manual = e.source_type.startsWith("manual");

  return (
    <div className="flex max-w-4xl flex-col gap-5">
      <Link href="/accounting/journal-entries" className="flex w-fit items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
        <ArrowLeft className="size-4" /> Journal
      </Link>
      <PageHeader
        title={`Entry ${e.entry_number}`}
        description={`${e.entry_date} · ${SOURCE_LABEL[e.source_type] ?? e.source_type}${e.branch_name ? ` · ${e.branch_name}` : ""}${e.created_by ? ` · by ${e.created_by}` : ""}`}
      />
      <div className="rounded-xl border bg-card p-4">
        <p className="mb-3 text-sm">{e.description}</p>
        <table className="w-full text-sm">
          <thead className="text-left text-xs text-muted-foreground">
            <tr>
              <th className="pb-1 font-medium">Account</th>
              <th className="pb-1 font-medium">Party</th>
              <th className="pb-1 text-right font-medium">Debit</th>
              <th className="pb-1 text-right font-medium">Credit</th>
            </tr>
          </thead>
          <tbody>
            {e.lines?.map((l, i) => (
              <tr key={i} className="border-t">
                <td className="py-1.5">
                  {l.account_code} · {l.account_name}
                </td>
                <td className="py-1.5">
                  <PartyCell type={l.party_type} id={l.party_id} name={l.party_name} />
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
              <td className="tabular pt-2 text-right">{rupees(e.amount)}</td>
              <td className="tabular pt-2 text-right">{rupees(e.amount)}</td>
            </tr>
          </tfoot>
        </table>
      </div>
      <p className="text-sm text-muted-foreground">
        {manual
          ? "Posted entries are final. To correct this one, reverse its voucher."
          : "Made automatically and read-only. To undo it, use its source document (a void or a return for a sale, for example)."}{" "}
        {href && (
          <Link className="text-foreground underline" href={href}>
            Open the {manual ? "voucher" : "source document"}
          </Link>
        )}
      </p>
    </div>
  );
}
