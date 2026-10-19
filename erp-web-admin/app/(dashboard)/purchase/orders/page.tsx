"use client";

// Purchase orders (erp-core-go migrations/057): what has been ordered from
// each supplier and how much of it has arrived. A PO is received by one or
// more GRNs; each GRN is billed as usual (Purchase Bills).
import { Suspense } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { Plus } from "lucide-react";
import { PurchaseOrderSummary } from "@/lib/types";
import { qty, rupees } from "@/lib/format";
import { buttonVariants } from "@/components/ui/button";
import { Column, DataTable } from "@/components/data/data-table";
import { PageHeader } from "@/components/data/page-header";
import { selectClass } from "@/components/data/panel";
import { PO_TONE, StatusChip } from "@/components/data/status-chip";
import { useListState } from "@/components/data/use-list-state";
import { useServerList } from "@/components/data/use-server-list";

const columns: Column<PurchaseOrderSummary>[] = [
  {
    key: "number",
    header: "PO",
    cell: (p) => (
      <div>
        <div className="font-medium">{p.po_number}</div>
        <div className="text-xs text-muted-foreground">{p.order_date}</div>
      </div>
    ),
  },
  { key: "supplier", header: "Supplier", cell: (p) => p.supplier_name },
  { key: "status", header: "Status", cell: (p) => <StatusChip status={p.status} tone={PO_TONE[p.status]} /> },
  { key: "expected", header: "Expected", sort: "expected", cell: (p) => p.expected_date ?? "—" },
  {
    key: "received",
    header: "Received",
    align: "right",
    cell: (p) => (
      <span className="tabular">
        {qty(p.received_qty)} / {qty(p.ordered_qty)}
      </span>
    ),
  },
  { key: "total", header: "Value", align: "right", sort: "total", cell: (p) => <span className="tabular">{rupees(p.subtotal)}</span> },
];

function PurchaseOrdersView() {
  const router = useRouter();
  const { state, update, apiQuery, activeFilterCount } = useListState({ sort: "created", dir: "desc", status: "all" });
  const list = useServerList<PurchaseOrderSummary>("/api/v1/purchase/orders", "purchase_orders", apiQuery);
  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Purchase Orders"
        description="Orders placed with suppliers. Receive each delivery as a GRN against the PO; it completes once everything ordered has arrived."
        actions={
          <Link href="/purchase/orders/new" className={buttonVariants()}>
            <Plus /> New purchase order
          </Link>
        }
      />
      <DataTable<PurchaseOrderSummary>
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
        rowKey={(p) => p.po_id}
        searchPlaceholder="PO number or supplier"
        searchLabel="Search purchase orders"
        filters={
          <select aria-label="Status" value={state.status || "all"} onChange={(e) => update({ status: e.target.value })} className={selectClass}>
            <option value="all">All statuses</option>
            <option value="pending">Waiting for goods</option>
            <option value="draft">Draft</option>
            <option value="open">Issued, nothing received</option>
            <option value="partially_received">Partially received</option>
            <option value="completed">Completed</option>
            <option value="closed">Closed short</option>
            <option value="cancelled">Cancelled</option>
          </select>
        }
        activeFilterCount={activeFilterCount}
        onClearFilters={() => update({ q: "", status: "all" })}
        empty={{ title: "No purchase orders yet", body: "Raise one to order stock from a supplier, then receive it with a GRN." }}
        onRowClick={(p) => router.push(`/purchase/orders/${p.po_id}`)}
      />
    </div>
  );
}

export default function PurchaseOrdersPage() {
  return (
    <Suspense>
      <PurchaseOrdersView />
    </Suspense>
  );
}
