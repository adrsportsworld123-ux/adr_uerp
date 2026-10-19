"use client";

// Customer list (Phase 3's CRM, moved onto the Phase 11 list kit):
// server-side search across name, phone, email, GSTIN and company;
// type/segment/status filters; sort by name, spend, orders or recency;
// paging in the URL. A row opens the customer's detail page.
import { Suspense, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { Plus } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api-client";
import { Customer } from "@/lib/types";
import { buttonVariants } from "@/components/ui/button";
import { Column, DataTable } from "@/components/data/data-table";
import { EditDialog, EditField } from "@/components/data/edit-dialog";
import { MasterRowActions } from "@/components/data/master-actions";
import { PageHeader } from "@/components/data/page-header";
import { StatusChip, Tone } from "@/components/data/status-chip";
import { useListState } from "@/components/data/use-list-state";
import { useServerList } from "@/components/data/use-server-list";

import { rupees as money } from "@/lib/format";
const SEGMENT: Record<Customer["segment"], { label: string; tone: Tone }> = {
  vip: { label: "VIP", tone: "plum" },
  regular: { label: "Regular", tone: "info" },
  new: { label: "New", tone: "teal" },
  dormant: { label: "Dormant", tone: "muted" },
};

const EDIT_FIELDS: EditField[] = [
  { key: "name", label: "Name", required: true },
  { key: "phone", label: "Phone", type: "tel", half: true, required: true },
  { key: "email", label: "Email (optional)", type: "email", half: true },
  { key: "company_name", label: "Company", half: true },
  { key: "gstin", label: "GSTIN", upper: true, half: true, hint: "Required for a B2B customer" },
  { key: "address", label: "Address" },
];

function rupees(v: string) {
  return Number.isFinite(Number(v)) ? money(v) : v;
}

function shortDate(iso: string | null) {
  if (!iso) return "—";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "—" : d.toLocaleDateString("en-IN", { day: "numeric", month: "short", year: "numeric" });
}

const columns: Column<Customer>[] = [
  {
    key: "name",
    header: "Customer",
    sort: "name",
    cell: (c) => (
      <div className="flex flex-col leading-tight">
        <span className="font-medium">{c.name}</span>
        {c.company_name && <span className="text-xs text-muted-foreground">{c.company_name}</span>}
      </div>
    ),
  },
  {
    key: "contact",
    header: "Contact",
    cell: (c) => (
      <div className="flex flex-col leading-tight">
        <span className="tabular">{c.phone || "—"}</span>
        {c.email && <span className="text-xs text-muted-foreground">{c.email}</span>}
      </div>
    ),
  },
  {
    key: "type",
    header: "Type",
    cell: (c) => (
      <span className="rounded-md border px-1.5 py-0.5 text-xs font-medium">{c.customer_type === "b2b" ? "B2B" : "B2C"}</span>
    ),
  },
  { key: "segment", header: "Segment", cell: (c) => <StatusChip status={c.segment} tone={SEGMENT[c.segment]?.tone} label={SEGMENT[c.segment]?.label} /> },
  { key: "orders", header: "Orders", sort: "orders", align: "right", cell: (c) => <span className="tabular">{c.transaction_count}</span> },
  { key: "spend", header: "Total spend", sort: "spend", align: "right", cell: (c) => <span className="tabular whitespace-nowrap">{rupees(c.total_spend)}</span> },
  { key: "last", header: "Last purchase", sort: "last_purchase", cell: (c) => <span className="whitespace-nowrap">{shortDate(c.last_purchase_at)}</span> },
  {
    key: "status",
    header: "Status",
    cell: (c) => (
      <div className="flex items-center gap-1.5">
        <StatusChip status={c.status} />
        {c.credit_hold && <StatusChip status="credit_hold" tone="bad" label="Credit hold" />}
      </div>
    ),
  },
];

function CustomersList() {
  const router = useRouter();
  const { state, update, apiQuery, activeFilterCount } = useListState({
    sort: "name",
    status: "all",
    filterKeys: ["customer_type", "segment"],
  });
  const list = useServerList<Customer>("/api/v1/customers", "customers", apiQuery);
  const [editing, setEditing] = useState<Customer | null>(null);
  const selectClass = "h-9 rounded-lg border bg-card px-2 text-sm";

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Customers"
        description="Profiles are shared across every branch, with purchase history, loyalty and credit on the detail page."
        actions={
          <Link href="/customers/new" className={buttonVariants()}>
            <Plus aria-hidden />
            New customer
          </Link>
        }
      />
      <DataTable<Customer>
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
        rowKey={(c) => c.customer_id}
        searchPlaceholder="Name, phone, email or GSTIN"
        searchLabel="Search customers"
        searchId="q"
        activeFilterCount={activeFilterCount}
        onClearFilters={() => update({ q: "", status: "all", filters: { customer_type: "", segment: "" } })}
        filters={
          <>
            <select aria-label="Customer type" value={state.filters.customer_type} onChange={(e) => update({ filters: { customer_type: e.target.value } })} className={selectClass}>
              <option value="">All types</option>
              <option value="b2c">B2C</option>
              <option value="b2b">B2B</option>
            </select>
            <select aria-label="Segment" value={state.filters.segment} onChange={(e) => update({ filters: { segment: e.target.value } })} className={selectClass}>
              <option value="">All segments</option>
              <option value="vip">VIP</option>
              <option value="regular">Regular</option>
              <option value="new">New</option>
              <option value="dormant">Dormant</option>
            </select>
            <select aria-label="Status" value={state.status || "all"} onChange={(e) => update({ status: e.target.value })} className={selectClass}>
              <option value="all">All statuses</option>
              <option value="active">Active</option>
              <option value="inactive">Inactive</option>
            </select>
          </>
        }
        empty={{
          title: "No customers yet",
          body: "Register customers here or at the POS to track purchases, loyalty points and credit.",
          action: (
            <Link href="/customers/new" className={buttonVariants({ size: "sm" })}>
              <Plus aria-hidden />
              New customer
            </Link>
          ),
        }}
        onRowClick={(c) => router.push(`/customers/${c.customer_id}`)}
        rowActions={(c) => (
          <MasterRowActions
            name={c.name || c.phone}
            noun="customer"
            status={c.status}
            onEdit={() => setEditing(c)}
            setStatus={(next) => api.patch(`/api/v1/customers/${c.customer_id}`, { status: next })}
            deactivateNote="Their purchase history, loyalty points and credit stay as they are; they can't be picked for new sales until reactivated."
            remove={{ entity: "customer", id: c.customer_id, path: `/api/v1/customers/${c.customer_id}`, permission: "master_data.delete" }}
            onChanged={list.reload}
          />
        )}
      />
      {editing && (
        <EditDialog
          open
          onOpenChange={(o) => !o && setEditing(null)}
          title="Edit customer"
          description={editing.name}
          fields={EDIT_FIELDS}
          initial={{
            name: editing.name,
            phone: editing.phone,
            email: editing.email,
            company_name: editing.company_name,
            gstin: editing.gstin,
            address: editing.address,
          }}
          onSave={async (changes) => {
            await api.patch(`/api/v1/customers/${editing.customer_id}`, changes);
            toast.success(`Saved customer “${changes.name ?? editing.name}”`);
            list.reload();
          }}
        />
      )}
    </div>
  );
}

export default function CustomersPage() {
  return (
    <Suspense>
      <CustomersList />
    </Suspense>
  );
}
