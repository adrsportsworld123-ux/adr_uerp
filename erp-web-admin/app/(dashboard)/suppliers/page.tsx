"use client";

import { Suspense, useState } from "react";
import Link from "next/link";
import { Plus, Power } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api-client";
import { Supplier } from "@/lib/types";
import { Button, buttonVariants } from "@/components/ui/button";
import { AlertModal } from "@/components/ui/dialog";
import { Column, DataTable } from "@/components/data/data-table";
import { PageHeader } from "@/components/data/page-header";
import { StatusChip } from "@/components/data/status-chip";
import { useListState } from "@/components/data/use-list-state";
import { useServerList } from "@/components/data/use-server-list";

function money(v: string) {
  const n = Number(v);
  return Number.isFinite(n) ? `₹${n.toLocaleString("en-IN", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}` : v;
}

const columns: Column<Supplier>[] = [
  {
    key: "name",
    header: "Supplier",
    sort: "name",
    cell: (s) => (
      <div className="flex flex-col leading-tight">
        <span className="font-medium">{s.name}</span>
        {s.legal_name && s.legal_name !== s.name && <span className="text-xs text-muted-foreground">{s.legal_name}</span>}
      </div>
    ),
  },
  { key: "gstin", header: "GSTIN", sort: "gstin", cell: (s) => <span className="font-mono text-xs">{s.gstin || "—"}</span> },
  {
    key: "contact",
    header: "Contact",
    cell: (s) => (
      <div className="flex flex-col leading-tight">
        <span>{s.contact_name || "—"}</span>
        {(s.phone || s.email) && <span className="text-xs text-muted-foreground">{[s.phone, s.email].filter(Boolean).join(" · ")}</span>}
      </div>
    ),
  },
  { key: "terms", header: "Payment terms", cell: (s) => s.payment_terms || "—" },
  { key: "credit", header: "Credit limit", sort: "credit_limit", align: "right", cell: (s) => <span className="tabular">{money(s.credit_limit)}</span> },
  { key: "status", header: "Status", cell: (s) => <StatusChip status={s.status} /> },
];

function SuppliersList() {
  // Newest first by default, so a supplier you just added is right at the top.
  const { state, update, apiQuery, activeFilterCount } = useListState({ sort: "updated", dir: "desc", status: "all" });
  const list = useServerList<Supplier>("/api/v1/suppliers", "suppliers", apiQuery);
  const [confirming, setConfirming] = useState<Supplier | null>(null);
  const [busy, setBusy] = useState(false);

  async function toggleStatus(s: Supplier) {
    const next = s.status === "active" ? "inactive" : "active";
    setBusy(true);
    try {
      await api.patch(`/api/v1/suppliers/${s.supplier_id}`, { status: next });
      toast.success(`${next === "active" ? "Activated" : "Deactivated"} “${s.name}”`);
      setConfirming(null);
      list.reload();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Update failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Suppliers"
        description="Vendors you receive goods from. Inactive suppliers stay on past GRNs, bills and returns."
        actions={
          <Link href="/suppliers/new" className={buttonVariants()}>
            <Plus aria-hidden />
            New supplier
          </Link>
        }
      />
      <DataTable<Supplier>
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
        rowKey={(s) => s.supplier_id}
        searchPlaceholder="Search name, GSTIN, phone, email"
        activeFilterCount={activeFilterCount}
        onClearFilters={() => update({ q: "", status: "all" })}
        filters={
          <select
            aria-label="Status"
            value={state.status || "all"}
            onChange={(e) => update({ status: e.target.value })}
            className="h-9 rounded-lg border bg-card px-2 text-sm"
          >
            <option value="all">All statuses</option>
            <option value="active">Active</option>
            <option value="inactive">Inactive</option>
          </select>
        }
        empty={{
          title: "No suppliers yet",
          body: "Add the vendors you buy from to receive goods and record purchase bills against them.",
          action: (
            <Link href="/suppliers/new" className={buttonVariants({ size: "sm" })}>
              <Plus aria-hidden />
              Add supplier
            </Link>
          ),
        }}
        rowActions={(s) => (
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={`${s.status === "active" ? "Deactivate" : "Activate"} ${s.name}`}
            title={s.status === "active" ? "Deactivate" : "Activate"}
            onClick={() => (s.status === "active" ? setConfirming(s) : toggleStatus(s))}
          >
            <Power className={s.status === "active" ? "" : "text-pay"} />
          </Button>
        )}
      />
      <AlertModal
        open={!!confirming}
        onOpenChange={(o) => !o && setConfirming(null)}
        title={`Deactivate “${confirming?.name ?? ""}”?`}
        actions={
          <>
            <Button variant="outline" onClick={() => setConfirming(null)} disabled={busy}>
              Cancel
            </Button>
            <Button onClick={() => confirming && toggleStatus(confirming)} disabled={busy}>
              Deactivate
            </Button>
          </>
        }
      >
        They&rsquo;ll stay on every past goods receipt, bill and return. You can reactivate them any time.
      </AlertModal>
    </div>
  );
}

export default function SuppliersPage() {
  return (
    <Suspense>
      <SuppliersList />
    </Suspense>
  );
}
