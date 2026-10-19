"use client";

// Journal vouchers (Phase 14): manual entries and where each one stands:
// draft, submitted, approved, posted or reversed. Drafts never reach the
// ledger; posting does, and a posted voucher is only ever reversed.
import { Suspense } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { Plus } from "lucide-react";
import { Voucher } from "@/lib/types";
import { rupees } from "@/lib/format";
import { buttonVariants } from "@/components/ui/button";
import { Column, DataTable } from "@/components/data/data-table";
import { PageHeader } from "@/components/data/page-header";
import { selectClass } from "@/components/data/panel";
import { StatusChip, VOUCHER_TONE } from "@/components/data/status-chip";
import { useListState } from "@/components/data/use-list-state";
import { useServerList } from "@/components/data/use-server-list";

const columns: Column<Voucher>[] = [
  {
    key: "number",
    header: "Voucher",
    cell: (v) => (
      <div>
        <div className="font-medium">{v.voucher_number}</div>
        <div className="text-xs text-muted-foreground">{v.entry_date}</div>
      </div>
    ),
  },
  { key: "description", header: "Description", cell: (v) => <span className="line-clamp-2">{v.description}</span> },
  { key: "status", header: "Status", cell: (v) => <StatusChip status={v.status} tone={VOUCHER_TONE[v.status]} /> },
  { key: "by", header: "Made by", cell: (v) => v.created_by },
  { key: "entry", header: "Ledger entry", cell: (v) => <span className="font-mono text-xs">{v.entry_number ?? "—"}</span> },
  { key: "total", header: "Amount", align: "right", cell: (v) => <span className="tabular">{rupees(v.total)}</span> },
];

function VouchersView() {
  const router = useRouter();
  const { state, update, apiQuery, activeFilterCount } = useListState({ sort: "created", dir: "desc", status: "all" });
  const list = useServerList<Voucher>("/api/v1/accounting/vouchers", "vouchers", apiQuery);
  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Journal Vouchers"
        description="Manual entries, from draft to posted. Drafts never reach the ledger; a posted voucher is corrected by reversing it."
        actions={
          <Link href="/accounting/vouchers/new" className={buttonVariants()}>
            <Plus /> New voucher
          </Link>
        }
      />
      <DataTable<Voucher>
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
        rowKey={(v) => v.voucher_id}
        searchPlaceholder="Voucher number or description"
        searchLabel="Search vouchers"
        filters={
          <select aria-label="Status" value={state.status || "all"} onChange={(e) => update({ status: e.target.value })} className={selectClass}>
            <option value="all">All statuses</option>
            <option value="draft">Draft</option>
            <option value="submitted">Waiting for approval</option>
            <option value="approved">Approved, not posted</option>
            <option value="posted">Posted</option>
            <option value="reversed">Reversed</option>
          </select>
        }
        activeFilterCount={activeFilterCount}
        onClearFilters={() => update({ q: "", status: "all" })}
        empty={{ title: "No vouchers yet", body: "Make one for an accrual, a correction or anything the system doesn't post by itself." }}
        onRowClick={(v) => router.push(`/accounting/vouchers/${v.voucher_id}`)}
      />
    </div>
  );
}

export default function VouchersPage() {
  return (
    <Suspense>
      <VouchersView />
    </Suspense>
  );
}
