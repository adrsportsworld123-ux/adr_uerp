"use client";

import { useEffect, useState } from "react";
import { Link2, Loader2 } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Button } from "@/components/ui/button";
import { AlertModal } from "@/components/ui/dialog";

export type Dependency = { table: string; label: string; count: number; capped: boolean; blocking: boolean };
type DependencyReport = { dependencies: Dependency[]; deletable: boolean };

export function DependencyList({ deps }: { deps: Dependency[] }) {
  return (
    <ul className="divide-y rounded-lg border text-sm">
      {deps.map((d) => (
        <li key={d.table} className="flex items-center justify-between gap-4 px-3 py-2 text-foreground">
          <span>{d.label}</span>
          <span className="tabular font-semibold">
            {d.count.toLocaleString("en-IN")}
            {d.capped ? "+" : ""}
          </span>
        </li>
      ))}
    </ul>
  );
}

// "Delete" for a master record (design system §04). Asks the server what
// uses the record first; if anything does, explains it and offers
// Deactivate instead, so history is never broken and the user never hits
// a bare foreign-key error. The server re-checks on delete (409 IN_USE),
// so a race between this dialog and someone else's new sale is safe too.
export function DeleteWithDependencies({
  open,
  onOpenChange,
  entity,
  id,
  name,
  noun,
  deletePath,
  canDeactivate,
  onDeactivate,
  onDeleted,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  entity: string;
  id: string;
  name: string;
  noun: string;
  deletePath: string;
  canDeactivate: boolean;
  onDeactivate: () => Promise<void>;
  onDeleted: () => void;
}) {
  const [report, setReport] = useState<DependencyReport | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // The server's own reason when it refuses for a rule rather than a
  // reference it can list (a branch still holding stock).
  const [reason, setReason] = useState<string | null>(null);

  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    api
      .get<DependencyReport>(`/api/v1/dependencies/${entity}/${id}`)
      .then((r) => !cancelled && setReport(r))
      .catch((e: Error) => !cancelled && setError(e.message));
    return () => {
      cancelled = true;
      setReport(null);
      setError(null);
      setReason(null);
    };
  }, [open, entity, id]);

  async function remove() {
    setBusy(true);
    try {
      await api.delete(deletePath);
      toast.success(`Deleted ${noun} “${name}”`);
      onOpenChange(false);
      onDeleted();
    } catch (e) {
      if (e instanceof ApiError && e.code === "IN_USE") {
        const deps = (e.details as { dependencies?: Dependency[] } | undefined)?.dependencies ?? [];
        setReport({ dependencies: deps, deletable: false });
        if (deps.length === 0) setReason(e.message);
      } else {
        toast.error(e instanceof Error ? e.message : "Delete failed");
      }
    } finally {
      setBusy(false);
    }
  }

  async function deactivate() {
    setBusy(true);
    try {
      await onDeactivate();
      onOpenChange(false);
    } finally {
      setBusy(false);
    }
  }

  const blocked = report && !report.deletable;
  const title = !report && !error ? `Delete ${noun}?` : blocked ? `“${name}” can't be deleted` : `Delete ${noun} “${name}”?`;

  return (
    <AlertModal
      open={open}
      onOpenChange={onOpenChange}
      title={title}
      actions={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          {blocked ? (
            <Button onClick={deactivate} disabled={busy || !canDeactivate}>
              {busy && <Loader2 className="animate-spin" aria-hidden />}
              Deactivate instead
            </Button>
          ) : (
            <Button variant="destructive" onClick={remove} disabled={busy || !report}>
              {busy && <Loader2 className="animate-spin" aria-hidden />}
              Move to Recycle Bin
            </Button>
          )}
        </>
      }
    >
      {error && <p className="text-destructive">{error}</p>}
      {!report && !error && (
        <p className="flex items-center gap-2">
          <Loader2 className="size-4 animate-spin" aria-hidden /> Checking where this {noun} is used…
        </p>
      )}
      {report && blocked && (
        <div className="flex flex-col gap-3">
          <p>
            It&rsquo;s used by the records below, which must keep it. Deactivating hides it from pickers for new records and
            leaves all history unchanged. You can reactivate it any time.
          </p>
          {reason ? <p className="text-foreground">{reason}</p> : <DependencyList deps={report.dependencies.filter((d) => d.blocking)} />}
        </div>
      )}
      {report && !blocked && (
        <div className="flex flex-col gap-2">
          <p className="flex items-center gap-2">
            <Link2 className="size-4" aria-hidden /> Nothing uses this {noun}. It moves to the Recycle Bin, where an admin can restore it.
          </p>
        </div>
      )}
    </AlertModal>
  );
}
