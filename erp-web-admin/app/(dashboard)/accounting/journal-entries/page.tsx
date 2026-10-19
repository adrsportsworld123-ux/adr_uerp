"use client";

// The journal (Phase 14): every posted entry, manual and automatic, newest
// first, with search, source and date filters, CSV export, and a print
// layout (save it as PDF from the print dialog). An entry opens its lines
// and the document that made it.
import { Suspense, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { Download, Plus, Printer } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { LedgerEntry } from "@/lib/types";
import { SOURCE_LABEL, rupees } from "@/lib/format";
import { Button, buttonVariants } from "@/components/ui/button";
import { Column, DataTable } from "@/components/data/data-table";
import { PageHeader } from "@/components/data/page-header";
import { selectClass } from "@/components/data/panel";
import { StatusChip } from "@/components/data/status-chip";
import { useListState } from "@/components/data/use-list-state";
import { useServerList } from "@/components/data/use-server-list";

const FILTER_KEYS = ["source_type", "from", "to"];
const columns: Column<LedgerEntry>[] = [
  {
    key: "entry",
    header: "Entry",
    sort: "date",
    cell: (e) => (
      <div>
        <div className="font-mono text-xs font-medium">{e.entry_number}</div>
        <div className="text-xs text-muted-foreground">{e.entry_date}</div>
      </div>
    ),
  },
  {
    key: "source",
    header: "Source",
    cell: (e) => <StatusChip status={e.source_type} tone={e.source_type.startsWith("manual") ? "info" : "muted"} label={SOURCE_LABEL[e.source_type] ?? e.source_type} />,
  },
  { key: "description", header: "Description", cell: (e) => <span className="line-clamp-2">{e.description}</span> },
  { key: "branch", header: "Branch", cell: (e) => e.branch_name ?? "—" },
  { key: "amount", header: "Amount", align: "right", cell: (e) => <span className="tabular">{rupees(e.amount)}</span> },
];

function JournalView() {
  const router = useRouter();
  const { state, update, apiQuery, activeFilterCount } = useListState({ sort: "date", dir: "desc", filterKeys: FILTER_KEYS });
  const list = useServerList<LedgerEntry>("/api/v1/accounting/journal-entries", "entries", apiQuery);
  const [exporting, setExporting] = useState(false);
  const f = state.filters;
  const setFilter = (k: string, v: string) => {
    list.setLoading(true);
    update({ filters: { [k]: v } });
  };

  async function exportCSV() {
    setExporting(true);
    try {
      const p = new URLSearchParams({ format: "csv" });
      if (state.q) p.set("q", state.q);
      for (const k of FILTER_KEYS) if (f[k]) p.set(k, f[k]);
      const { blob, filename } = await api.download(`/api/v1/accounting/journal-entries?${p.toString()}`);
      const a = document.createElement("a");
      a.href = URL.createObjectURL(blob);
      a.download = filename;
      a.click();
      URL.revokeObjectURL(a.href);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not export");
    } finally {
      setExporting(false);
    }
  }

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Journal"
        description="Every posted entry. Automatic ones are read-only and open the document that made them; manual ones are journal vouchers."
        actions={
          <>
            <Button variant="outline" disabled={exporting} onClick={exportCSV}>
              <Download /> CSV
            </Button>
            <Button variant="outline" className="print:hidden" onClick={() => window.print()}>
              <Printer /> Print / PDF
            </Button>
            <Link href="/accounting/vouchers/new" className={buttonVariants()}>
              <Plus /> New voucher
            </Link>
          </>
        }
      />
      <DataTable<LedgerEntry>
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
        rowKey={(e) => e.entry_id}
        searchPlaceholder="Entry number or description"
        searchLabel="Search the journal"
        filters={
          <>
            <select aria-label="Source" value={f.source_type} onChange={(e) => setFilter("source_type", e.target.value)} className={selectClass}>
              <option value="">All sources</option>
              {Object.entries(SOURCE_LABEL).map(([k, v]) => (
                <option key={k} value={k}>
                  {v}
                </option>
              ))}
            </select>
            <label className="flex items-center gap-1 text-sm">
              From
              <input type="date" aria-label="From date" value={f.from} onChange={(e) => setFilter("from", e.target.value)} className={selectClass} />
            </label>
            <label className="flex items-center gap-1 text-sm">
              To
              <input type="date" aria-label="To date" value={f.to} onChange={(e) => setFilter("to", e.target.value)} className={selectClass} />
            </label>
          </>
        }
        activeFilterCount={activeFilterCount}
        onClearFilters={() => update({ q: "", filters: { source_type: "", from: "", to: "" } })}
        empty={{ title: "Nothing posted yet", body: "Sales, purchases and vouchers appear here as they post." }}
        onRowClick={(e) => router.push(`/accounting/journal-entries/${e.entry_number}`)}
      />
    </div>
  );
}

export default function JournalPage() {
  return (
    <Suspense>
      <JournalView />
    </Suspense>
  );
}
