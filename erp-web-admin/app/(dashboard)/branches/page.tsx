"use client";

// Branches (Phase 2's Multi-Branch, moved onto the Phase 11 list kit).
// Newest first, so a branch you just created is at the top of the list.
// Deactivating a branch (Phase 12) closes its tills: its terminals refuse
// PIN logins and new pairing codes until it's reactivated. Stock, sales
// and reports are untouched, and it still appears in history and reports.
import { Suspense, useState } from "react";
import Link from "next/link";
import { FileText, Pencil, Power } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Branch } from "@/lib/types";
import { Button, buttonVariants } from "@/components/ui/button";
import { AlertModal } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Column, DataTable } from "@/components/data/data-table";
import { EditDialog, EditField } from "@/components/data/edit-dialog";
import { PageHeader } from "@/components/data/page-header";
import { StatusChip } from "@/components/data/status-chip";
import { useListState } from "@/components/data/use-list-state";
import { useServerList } from "@/components/data/use-server-list";

const columns: Column<Branch>[] = [
  { key: "name", header: "Branch", sort: "name", cell: (b) => <span className="font-medium">{b.name}</span> },
  { key: "code", header: "Code", sort: "code", cell: (b) => <span className="font-mono text-xs">{b.code}</span> },
  { key: "gstin", header: "GSTIN", cell: (b) => <span className="font-mono text-xs">{b.gstin || "—"}</span> },
  { key: "tz", header: "Timezone", cell: (b) => b.timezone },
  { key: "status", header: "Status", cell: (b) => <StatusChip status={b.status} /> },
];

const EDIT_FIELDS: EditField[] = [
  { key: "name", label: "Name", required: true },
  { key: "code", label: "Code", required: true, upper: true, half: true, hint: "Short code printed on documents" },
  { key: "timezone", label: "Timezone", half: true, placeholder: "Asia/Kolkata", hint: "Price rules use this branch's local time" },
  { key: "gstin", label: "GSTIN", upper: true, hint: "A branch in another state can carry its own GSTIN" },
];

function BranchesView() {
  const { state, update, apiQuery, activeFilterCount } = useListState({ sort: "created", dir: "desc", status: "all" });
  const list = useServerList<Branch>("/api/v1/branches", "branches", apiQuery);
  const [editing, setEditing] = useState<Branch | null>(null);
  const [deactivating, setDeactivating] = useState<Branch | null>(null);

  async function setStatus(b: Branch, status: "active" | "inactive") {
    try {
      await api.patch(`/api/v1/branches/${b.branch_id}`, { status });
      toast.success(`${status === "active" ? "Activated" : "Deactivated"} “${b.name}”`);
      list.reload();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not update branch");
    }
  }

  const [name, setName] = useState("");
  const [code, setCode] = useState("");
  const [gstin, setGstin] = useState("");
  const [submitting, setSubmitting] = useState(false);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    try {
      await api.post("/api/v1/branches", { name, code, gstin });
      toast.success(`Created branch “${name}”`);
      setName("");
      setCode("");
      setGstin("");
      list.reload();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not create branch");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="flex flex-col gap-5">
      <PageHeader title="Branches" description="Stores and warehouses. Stock, sales, terminals and transfers all belong to a branch." />

      <div className="grid gap-5 xl:grid-cols-[minmax(0,1fr)_320px]">
        <div className="min-w-0">
          <DataTable<Branch>
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
            rowKey={(b) => b.branch_id}
            searchPlaceholder="Name, code or GSTIN"
            searchLabel="Search branches"
            activeFilterCount={activeFilterCount}
            onClearFilters={() => update({ q: "", status: "all" })}
            empty={{ title: "No branches yet", body: "Create your first branch with the form alongside." }}
            onRowClick={setEditing}
            rowActions={(b) => (
              <>
                <Button variant="ghost" size="icon-sm" aria-label={`Edit ${b.name}`} title="Edit" onClick={() => setEditing(b)}>
                  <Pencil />
                </Button>
                <Link
                  href={`/branches/${b.branch_id}/print-settings`}
                  className={buttonVariants({ variant: "ghost", size: "icon-sm" })}
                  aria-label={`Print settings for ${b.name}`}
                  title="Letterhead & print settings"
                  onClick={(e) => e.stopPropagation()}
                >
                  <FileText />
                </Link>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label={`${b.status === "active" ? "Deactivate" : "Activate"} ${b.name}`}
                  title={b.status === "active" ? "Deactivate" : "Activate"}
                  onClick={() => (b.status === "active" ? setDeactivating(b) : setStatus(b, "active"))}
                >
                  <Power className={b.status === "active" ? "" : "text-pay"} />
                </Button>
              </>
            )}
          />
        </div>

        <form onSubmit={handleSubmit} className="flex h-fit flex-col gap-4 rounded-xl border bg-card p-4">
          <div>
            <h2 className="text-sm font-semibold">New branch</h2>
            <p className="text-xs text-muted-foreground">Code must be unique, e.g. BLR02.</p>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="name">Name *</Label>
            <Input id="name" required value={name} onChange={(e) => setName(e.target.value)} />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="code">Code *</Label>
            <Input id="code" required value={code} onChange={(e) => setCode(e.target.value.toUpperCase())} placeholder="BLR02" />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="gstin">GSTIN</Label>
            <Input id="gstin" value={gstin} onChange={(e) => setGstin(e.target.value.toUpperCase())} />
          </div>
          <Button type="submit" disabled={submitting}>
            {submitting ? "Creating…" : "Create branch"}
          </Button>
        </form>
      </div>

      <AlertModal
        open={!!deactivating}
        onOpenChange={(o) => !o && setDeactivating(null)}
        title={`Deactivate “${deactivating?.name ?? ""}”?`}
        actions={
          <>
            <Button variant="outline" onClick={() => setDeactivating(null)}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              onClick={() => {
                const b = deactivating!;
                setDeactivating(null);
                setStatus(b, "inactive");
              }}
            >
              Deactivate branch
            </Button>
          </>
        }
      >
        Its POS terminals stop accepting sign-ins and can&rsquo;t be paired until you reactivate it. Stock, sales, transfers and reports
        are untouched.
      </AlertModal>

      {editing && (
        <EditDialog
          open
          onOpenChange={(o) => !o && setEditing(null)}
          title="Edit branch"
          description={editing.name}
          fields={EDIT_FIELDS}
          initial={{ name: editing.name, code: editing.code, timezone: editing.timezone, gstin: editing.gstin }}
          onSave={async (changes) => {
            await api.patch(`/api/v1/branches/${editing.branch_id}`, changes);
            toast.success(`Saved branch “${changes.name ?? editing.name}”`);
            list.reload();
          }}
        />
      )}
    </div>
  );
}

export default function BranchesPage() {
  return (
    <Suspense>
      <BranchesView />
    </Suspense>
  );
}
