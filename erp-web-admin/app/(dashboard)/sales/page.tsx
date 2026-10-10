"use client";

// Sales (Phase 14, erp-core-go GET /sales/orders and /sales/summary): every
// completed, voided and refunded sale from the tills, the online store and
// the back office, newest first, with the period's summary on top. A row
// opens the full sale (items, tax, payments, returns, ledger and audit).
import { Suspense, useEffect, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { Plus } from "lucide-react";
import { api } from "@/lib/api-client";
import { Branch, SaleSummaryRow, SalesSummary } from "@/lib/types";
import { CHANNEL_LABEL, METHOD_LABEL, STATUS_LABEL, dateTime, qty, rupees } from "@/lib/format";
import { usePermissions } from "@/lib/permissions";
import { buttonVariants } from "@/components/ui/button";
import { Column, DataTable } from "@/components/data/data-table";
import { PageHeader } from "@/components/data/page-header";
import { StatusChip } from "@/components/data/status-chip";
import { useListState } from "@/components/data/use-list-state";
import { useServerList } from "@/components/data/use-server-list";

const columns: Column<SaleSummaryRow>[] = [
  {
    key: "number",
    header: "Sale",
    sort: "number",
    cell: (o) => (
      <div>
        <div className="font-medium">{o.order_number}</div>
        <div className="text-xs text-muted-foreground">{dateTime(o.finalized_at)}</div>
      </div>
    ),
  },
  { key: "status", header: "Status", cell: (o) => <StatusChip status={o.status} label={STATUS_LABEL[o.status] ?? o.status} /> },
  {
    key: "where",
    header: "Where",
    cell: (o) => (
      <div className="text-xs">
        <div>
          {o.branch_name} · {CHANNEL_LABEL[o.channel] ?? o.channel}
        </div>
        <div className="text-muted-foreground">
          {o.terminal_name} · {o.cashier_name}
        </div>
      </div>
    ),
  },
  { key: "customer", header: "Customer", cell: (o) => o.customer_name ?? <span className="text-muted-foreground">Walk-in</span> },
  { key: "items", header: "Items", align: "right", cell: (o) => <span className="tabular">{qty(o.item_count)}</span> },
  { key: "paid", header: "Paid by", cell: (o) => o.payment_methods.map((m) => METHOD_LABEL[m] ?? m).join(" + ") || "—" },
  {
    key: "total",
    header: "Total",
    sort: "total",
    align: "right",
    cell: (o) => <span className={`tabular font-medium ${o.status === "voided" ? "line-through text-muted-foreground" : ""}`}>{rupees(o.grand_total)}</span>,
  },
];

const selectClass = "h-9 rounded-lg border bg-card px-2 text-sm";

// Module-level: useListState memoises on this array, so a fresh literal on
// every render would re-run every effect that depends on the filters.
const FILTER_KEYS = ["channel", "branch_id", "payment_method", "from", "to"];

function SalesView() {
  const router = useRouter();
  const { can } = usePermissions();
  const { state, update, apiQuery, activeFilterCount } = useListState({
    sort: "recent",
    dir: "desc",
    status: "",
    filterKeys: FILTER_KEYS,
  });
  const list = useServerList<SaleSummaryRow>("/api/v1/sales/orders", "orders", apiQuery);
  const [summary, setSummary] = useState<SalesSummary | null>(null);
  const [branches, setBranches] = useState<Branch[]>([]);

  useEffect(() => {
    api.get<{ branches: Branch[] }>("/api/v1/branches").then((d) => setBranches(d.branches)).catch(() => {});
  }, []);
  // The summary covers the same filters as the list (not the page).
  const f = state.filters;
  useEffect(() => {
    const p = new URLSearchParams();
    for (const k of FILTER_KEYS) if (f[k]) p.set(k, f[k]);
    if (state.q) p.set("q", state.q);
    api.get<SalesSummary>(`/api/v1/sales/summary?${p.toString()}`).then(setSummary).catch(() => setSummary(null));
  }, [f, state.q]);

  const setFilter = (k: string, v: string) => {
    list.setLoading(true);
    update({ filters: { [k]: v } });
  };

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Sales"
        description="Every sale from the tills, the online store and the back office. Open one to see items, tax, payments, returns and its ledger entries."
        actions={
          can("sales.backoffice") ? (
            <Link href="/sales/new" className={buttonVariants()}>
              <Plus /> New sale
            </Link>
          ) : undefined
        }
      />

      {summary && (
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4" aria-label="Summary for these filters">
          {[
            ["Sales", summary.order_count.toLocaleString("en-IN")],
            ["Net sales", rupees(summary.net_sales)],
            ["Tax", rupees(summary.tax_total)],
            ["Refunded · voided", `${summary.refunded_count} · ${summary.voided_count}`],
          ].map(([label, value]) => (
            <div key={label} className="rounded-xl border bg-card p-3">
              <div className="text-xs text-muted-foreground">{label}</div>
              <div className="tabular text-lg font-semibold">{value}</div>
            </div>
          ))}
        </div>
      )}

      <DataTable<SaleSummaryRow>
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
        rowKey={(o) => o.order_id}
        searchPlaceholder="Sale number, customer or phone"
        searchLabel="Search sales"
        filters={
          <>
            <select aria-label="Status" value={state.status} onChange={(e) => update({ status: e.target.value })} className={selectClass}>
              <option value="">All statuses</option>
              <option value="finalized">Completed</option>
              <option value="partially_refunded">Partly refunded</option>
              <option value="refunded">Refunded</option>
              <option value="voided">Voided</option>
            </select>
            <select aria-label="Channel" value={f.channel} onChange={(e) => setFilter("channel", e.target.value)} className={selectClass}>
              <option value="">All channels</option>
              <option value="pos">Till</option>
              <option value="backoffice">Back office</option>
              <option value="online">Online</option>
            </select>
            <select aria-label="Branch" value={f.branch_id} onChange={(e) => setFilter("branch_id", e.target.value)} className={selectClass}>
              <option value="">All branches</option>
              {branches.map((b) => (
                <option key={b.branch_id} value={b.branch_id}>
                  {b.name}
                </option>
              ))}
            </select>
            <select aria-label="Paid by" value={f.payment_method} onChange={(e) => setFilter("payment_method", e.target.value)} className={selectClass}>
              <option value="">Any payment</option>
              {Object.entries(METHOD_LABEL).map(([k, v]) => (
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
        onClearFilters={() => update({ q: "", status: "", filters: { channel: "", branch_id: "", payment_method: "", from: "", to: "" } })}
        empty={{ title: "No sales yet", body: "Sales appear here as soon as a till, the online store or the back office completes one." }}
        onRowClick={(o) => router.push(`/sales/${o.order_id}`)}
      />
    </div>
  );
}

export default function SalesPage() {
  return (
    <Suspense>
      <SalesView />
    </Suspense>
  );
}
