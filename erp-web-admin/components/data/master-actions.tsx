"use client";

// The row actions every master list shares (erp-core-go migrations/054):
// Edit, Activate/Deactivate (deactivating asks first) and Delete through
// DeleteWithDependencies, which checks what uses the record and offers
// "deactivate instead" when something does. Deleting moves the record to
// the Recycle Bin. One component, so every master behaves the same.
import { useState } from "react";
import { Pencil, Power, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { ApiError } from "@/lib/api-client";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import { Button } from "@/components/ui/button";
import { AlertModal } from "@/components/ui/dialog";
import { DeleteWithDependencies } from "@/components/data/dependency-dialog";

export function MasterRowActions({
  name,
  noun,
  status,
  onEdit,
  setStatus,
  deactivateNote,
  remove,
  onChanged,
  locked,
}: {
  name: string;
  noun: string; // "customer", "account"
  status: string; // "active" | "inactive"
  onEdit?: () => void;
  /** PATCHes the status; omit for a master with no status. */
  setStatus?: (next: "active" | "inactive") => Promise<void>;
  /** What deactivating means for this master, shown in the confirmation. */
  deactivateNote?: string;
  remove?: { entity: string; id: string; path: string; permission?: string };
  onChanged: () => void;
  /** Why status and delete are unavailable (a system account), if they are. */
  locked?: string;
}) {
  const { can } = usePermissions();
  const [confirming, setConfirming] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [busy, setBusy] = useState(false);
  const active = status === "active";
  const canDelete = !remove?.permission || can(remove.permission);

  async function apply(next: "active" | "inactive") {
    if (!setStatus) return;
    setBusy(true);
    try {
      await setStatus(next);
      toast.success(`${next === "active" ? "Activated" : "Deactivated"} “${name}”`);
      setConfirming(false);
      onChanged();
    } catch (e) {
      toast.error(e instanceof ApiError || e instanceof Error ? e.message : "Update failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      {onEdit && (
        <Button variant="ghost" size="icon-sm" aria-label={`Edit ${name}`} title="Edit" onClick={onEdit}>
          <Pencil />
        </Button>
      )}
      {setStatus && (
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label={`${active ? "Deactivate" : "Activate"} ${name}`}
          title={locked ?? (active ? "Deactivate" : "Activate")}
          disabled={!!locked}
          onClick={() => (active ? setConfirming(true) : apply("active"))}
        >
          <Power className={active ? "" : "text-pay"} />
        </Button>
      )}
      {remove && (
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label={`Delete ${name}`}
          title={locked ?? (canDelete ? "Delete" : missingPermissionHint(remove.permission!))}
          disabled={!!locked || !canDelete}
          onClick={() => setDeleting(true)}
        >
          <Trash2 />
        </Button>
      )}
      <AlertModal
        open={confirming}
        onOpenChange={setConfirming}
        title={`Deactivate “${name}”?`}
        actions={
          <>
            <Button variant="outline" onClick={() => setConfirming(false)} disabled={busy}>
              Cancel
            </Button>
            <Button onClick={() => apply("inactive")} disabled={busy}>
              Deactivate
            </Button>
          </>
        }
      >
        {deactivateNote ?? `The ${noun} stays on everything it was used on, but can't be picked for anything new. You can reactivate it any time.`}
      </AlertModal>
      {remove && deleting && (
        <DeleteWithDependencies
          open
          onOpenChange={setDeleting}
          entity={remove.entity}
          id={remove.id}
          name={name}
          noun={noun}
          deletePath={remove.path}
          canDeactivate={!!setStatus && active}
          onDeactivate={async () => {
            await apply("inactive");
          }}
          onDeleted={onChanged}
        />
      )}
    </>
  );
}
