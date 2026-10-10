"use client";

import { ReactNode, useCallback, useState } from "react";
import { Loader2, Pencil, Plus, Power, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import { Button } from "@/components/ui/button";
import { AlertModal, Modal } from "@/components/ui/dialog";
import { Column, DataTable } from "@/components/data/data-table";
import { DeleteWithDependencies } from "@/components/data/dependency-dialog";
import { PageHeader } from "@/components/data/page-header";
import { StatusChip } from "@/components/data/status-chip";
import { useListState } from "@/components/data/use-list-state";
import { useServerList } from "@/components/data/use-server-list";

export type MasterRecord = { name: string; status: string; product_count: number };

export type MasterConfig<T extends MasterRecord, F extends Record<string, string>> = {
  /** Entity name for GET /dependencies/{entity}/{id}. */
  entity: string;
  noun: string;
  nounPlural: string;
  description: string;
  path: string; // e.g. /api/v1/brands
  listKey: string; // e.g. "brands"
  idOf: (row: T) => string;
  permission: string;
  defaultSort: string;
  columns: Column<T>[];
  searchPlaceholder: string;
  emptyBody: string;
  /** Blank form for "New". */
  blank: F;
  /** Form for "Edit" — only name is editable after creation. */
  fromRow: (row: T) => F;
  /** Fields shown only when creating (e.g. parent category, tax rates). */
  createOnlyFields?: (form: F, set: (patch: Partial<F>) => void) => ReactNode;
  /** Explains why some fields can't change after creation. */
  editNote?: ReactNode;
  validate: (form: F) => string | null;
  toCreateBody: (form: F) => unknown;
};

// One template for the small catalog masters (categories, brands, tax
// slabs): server-paged list with search/status/sort, create & edit in a
// dialog that guards unsaved input, activate/deactivate, and delete that
// explains dependencies and offers deactivate instead (Phase 11).
export function SimpleMasterPage<T extends MasterRecord, F extends Record<string, string>>({ config }: { config: MasterConfig<T, F> }) {
  const { can } = usePermissions();
  const allowed = can(config.permission);
  const { state, update, apiQuery, activeFilterCount } = useListState({ sort: config.defaultSort, status: "active" });
  const list = useServerList<T>(config.path, config.listKey, apiQuery);

  const [editing, setEditing] = useState<{ row: T | null; form: F; initial: F } | null>(null);
  const [saving, setSaving] = useState(false);
  const [fieldError, setFieldError] = useState<string | null>(null);
  const [confirmDiscard, setConfirmDiscard] = useState(false);
  const [deleting, setDeleting] = useState<T | null>(null);

  const dirty = !!editing && JSON.stringify(editing.form) !== JSON.stringify(editing.initial);

  function openCreate() {
    setFieldError(null);
    setEditing({ row: null, form: { ...config.blank }, initial: { ...config.blank } });
  }
  function openEdit(row: T) {
    const f = config.fromRow(row);
    setFieldError(null);
    setEditing({ row, form: { ...f }, initial: { ...f } });
  }
  const closeEditor = useCallback(() => {
    if (dirty) setConfirmDiscard(true);
    else setEditing(null);
  }, [dirty]);

  async function save() {
    if (!editing) return;
    const problem = config.validate(editing.form);
    if (problem) {
      setFieldError(problem);
      return;
    }
    setSaving(true);
    setFieldError(null);
    try {
      if (editing.row) {
        await api.patch(`${config.path}/${config.idOf(editing.row)}`, { name: editing.form.name.trim() });
        toast.success(`Saved ${config.noun} “${editing.form.name.trim()}”`);
      } else {
        await api.post(config.path, config.toCreateBody(editing.form));
        toast.success(`Created ${config.noun} “${editing.form.name.trim()}”`);
      }
      setEditing(null);
      list.reload();
    } catch (e) {
      if (e instanceof ApiError && e.code === "DUPLICATE_NAME") setFieldError(`Another ${config.noun} already uses this name.`);
      else setFieldError(e instanceof Error ? e.message : "Save failed");
    } finally {
      setSaving(false);
    }
  }

  async function setStatus(row: T, status: "active" | "inactive") {
    try {
      await api.patch(`${config.path}/${config.idOf(row)}`, { status });
      toast.success(`${status === "active" ? "Activated" : "Deactivated"} “${row.name}”`, {
        description: status === "inactive" ? `Hidden from pickers for new records. Existing records keep it.` : undefined,
      });
      list.reload();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Update failed");
      throw e;
    }
  }

  const denied = allowed ? undefined : missingPermissionHint(config.permission);

  const columns: Column<T>[] = [
    ...config.columns,
    {
      key: "products",
      header: "Products",
      sort: "products",
      align: "right",
      cell: (r) => <span className="tabular">{r.product_count.toLocaleString("en-IN")}</span>,
    },
    { key: "status", header: "Status", cell: (r) => <StatusChip status={r.status} /> },
  ];

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title={config.nounPlural}
        description={config.description}
        actions={
          <Button onClick={openCreate} disabled={!allowed} title={denied}>
            <Plus aria-hidden />
            New {config.noun}
          </Button>
        }
      />

      <DataTable<T>
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
        rowKey={config.idOf}
        searchPlaceholder={config.searchPlaceholder}
        activeFilterCount={activeFilterCount}
        onClearFilters={() => update({ q: "", status: "active" })}
        filters={
          <select
            aria-label="Status"
            value={state.status || "all"}
            onChange={(e) => update({ status: e.target.value === "all" ? "all" : e.target.value })}
            className="h-9 rounded-lg border bg-card px-2 text-sm"
          >
            <option value="active">Active</option>
            <option value="inactive">Inactive</option>
            <option value="all">All statuses</option>
          </select>
        }
        empty={{
          title: `No ${config.nounPlural.toLowerCase()} yet`,
          body: config.emptyBody,
          action: allowed ? (
            <Button size="sm" onClick={openCreate}>
              <Plus aria-hidden />
              New {config.noun}
            </Button>
          ) : undefined,
        }}
        onRowClick={allowed ? openEdit : undefined}
        rowActions={(row) => (
          <>
            <Button variant="ghost" size="icon-sm" aria-label={`Edit ${row.name}`} title={denied ?? "Edit"} disabled={!allowed} onClick={() => openEdit(row)}>
              <Pencil />
            </Button>
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label={`${row.status === "active" ? "Deactivate" : "Activate"} ${row.name}`}
              title={denied ?? (row.status === "active" ? "Deactivate" : "Activate")}
              disabled={!allowed}
              onClick={() => setStatus(row, row.status === "active" ? "inactive" : "active").catch(() => {})}
            >
              <Power className={row.status === "active" ? "" : "text-pay"} />
            </Button>
            <Button variant="ghost" size="icon-sm" aria-label={`Delete ${row.name}`} title={denied ?? "Delete"} disabled={!allowed} onClick={() => setDeleting(row)}>
              <Trash2 className="text-destructive" />
            </Button>
          </>
        )}
      />

      <Modal
        open={!!editing}
        onOpenChange={(o) => (o ? null : closeEditor())}
        title={editing?.row ? `Edit ${config.noun}` : `New ${config.noun}`}
        description={editing?.row ? editing.row.name : undefined}
        footer={
          <>
            <Button variant="outline" onClick={closeEditor} disabled={saving}>
              Cancel
            </Button>
            <Button onClick={save} disabled={saving || !dirty}>
              {saving && <Loader2 className="animate-spin" aria-hidden />}
              {editing?.row ? "Save changes" : `Create ${config.noun}`}
            </Button>
          </>
        }
      >
        {editing && (
          <form
            className="flex flex-col gap-4"
            onSubmit={(e) => {
              e.preventDefault();
              save();
            }}
          >
            <div className="flex flex-col gap-1.5">
              <label htmlFor="master-name" className="text-sm font-medium">
                Name <span className="text-destructive">*</span>
              </label>
              <input
                id="master-name"
                autoFocus
                value={editing.form.name}
                aria-invalid={!!fieldError}
                onChange={(e) => setEditing({ ...editing, form: { ...editing.form, name: e.target.value } })}
                className="h-9 rounded-lg border bg-card px-3 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/30 aria-invalid:border-destructive"
              />
            </div>
            {!editing.row &&
              config.createOnlyFields?.(editing.form, (patch) => setEditing({ ...editing, form: { ...editing.form, ...patch } as F }))}
            {editing.row && config.editNote && <p className="text-xs text-muted-foreground">{config.editNote}</p>}
            {fieldError && (
              <p role="alert" className="text-sm text-destructive">
                {fieldError}
              </p>
            )}
            <button type="submit" hidden />
          </form>
        )}
      </Modal>

      <AlertModal
        open={confirmDiscard}
        onOpenChange={setConfirmDiscard}
        title="Discard unsaved changes?"
        actions={
          <>
            <Button variant="outline" onClick={() => setConfirmDiscard(false)}>
              Keep editing
            </Button>
            <Button
              variant="destructive"
              onClick={() => {
                setConfirmDiscard(false);
                setEditing(null);
              }}
            >
              Discard
            </Button>
          </>
        }
      >
        Your changes to this {config.noun} haven&rsquo;t been saved.
      </AlertModal>

      {deleting && (
        <DeleteWithDependencies
          open={!!deleting}
          onOpenChange={(o) => !o && setDeleting(null)}
          entity={config.entity}
          id={config.idOf(deleting)}
          name={deleting.name}
          noun={config.noun}
          deletePath={`${config.path}/${config.idOf(deleting)}`}
          canDeactivate={deleting.status === "active"}
          onDeactivate={() => setStatus(deleting, "inactive")}
          onDeleted={list.reload}
        />
      )}
    </div>
  );
}
