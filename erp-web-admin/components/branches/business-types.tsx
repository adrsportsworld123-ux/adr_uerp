"use client";

// What a branch does (erp-core-go §3.80, migrations/077): one or more
// business types — a retail shop, wholesale, grocery, pharmacy, a
// warehouse, or a farm of one species. A farm type switches on the Farm
// screens for that branch; the existing screens never depend on a type.
import { useEffect, useState } from "react";
import { Loader2 } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { usePermissions } from "@/lib/permissions";
import { Button } from "@/components/ui/button";
import { Modal } from "@/components/ui/dialog";

export type BusinessType = { code: string; name: string; description: string; features: string[]; species: string | null };

let cache: BusinessType[] | null = null;

export function useBusinessTypes() {
  const [types, setTypes] = useState<BusinessType[]>(cache ?? []);
  useEffect(() => {
    if (cache) return;
    api
      .get<{ business_types: BusinessType[] }>("/api/v1/business-types")
      .then((d) => {
        cache = d.business_types;
        setTypes(d.business_types);
      })
      .catch(() => {});
  }, []);
  return types;
}

/** The type names of a branch, as small chips. */
export function BusinessTypeChips({ codes, types }: { codes: string[] | undefined; types: BusinessType[] }) {
  if (!codes?.length) return <span className="text-xs text-muted-foreground">—</span>;
  return (
    <span className="flex flex-wrap gap-1">
      {codes.map((c) => {
        const t = types.find((x) => x.code === c);
        return (
          <span key={c} className={`rounded px-1.5 py-0.5 text-xs ${t?.species ? "bg-pay-soft text-pay" : "bg-muted"}`}>
            {t?.name ?? c}
          </span>
        );
      })}
    </span>
  );
}

export function BusinessTypesDialog({
  branchId,
  branchName,
  current,
  onClose,
  onSaved,
}: {
  branchId: string;
  branchName: string;
  current: string[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const types = useBusinessTypes();
  const { refreshFeatures } = usePermissions();
  const [chosen, setChosen] = useState<string[]>(current.length ? current : ["retail"]);
  const [busy, setBusy] = useState(false);
  const toggle = (c: string) => setChosen((prev) => (prev.includes(c) ? prev.filter((x) => x !== c) : [...prev, c]));

  async function save() {
    setBusy(true);
    try {
      await api.put(`/api/v1/branches/${branchId}/business-types`, { business_types: chosen });
      toast.success(`${branchName}: saved what it does`);
      refreshFeatures();
      onSaved();
      onClose();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not save");
    } finally {
      setBusy(false);
    }
  }
  const group = (farm: boolean) => types.filter((t) => !!t.species === farm);
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={`What ${branchName} does`}
      description="Choose every business this branch runs. A farm type turns on the Farm screens for it; nothing a shop uses today changes."
      width={560}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={save} disabled={busy || chosen.length === 0}>
            {busy && <Loader2 className="animate-spin" aria-hidden />} Save
          </Button>
        </>
      }
    >
      <div className="grid gap-4 sm:grid-cols-2">
        {[false, true].map((farm) => (
          <fieldset key={String(farm)} className="space-y-2">
            <legend className="mb-1 text-xs font-semibold uppercase tracking-wider text-muted-foreground">{farm ? "Farms" : "Shops & stores"}</legend>
            {group(farm).map((t) => (
              <label key={t.code} className="flex items-start gap-2 text-sm">
                <input type="checkbox" className="mt-1" checked={chosen.includes(t.code)} onChange={() => toggle(t.code)} aria-label={t.name} />
                <span>
                  {t.name}
                  <span className="block text-xs text-muted-foreground">{t.description}</span>
                </span>
              </label>
            ))}
          </fieldset>
        ))}
      </div>
    </Modal>
  );
}
