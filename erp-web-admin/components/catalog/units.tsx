"use client";

// Units of measure (erp-core-go §3.79, migrations/076): the unit list, a
// unit picker for the product forms, and each barcode's pack — what one
// scan adds (a tray of 30 eggs, a 25 kg bag, a 500 g packet). Stock, cost
// and price stay per base unit; a pack only sets the quantity.
import { FormEvent, useCallback, useEffect, useState } from "react";
import { Loader2, Package, Plus } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import type { BarcodeInfo, Unit, UnitRef } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Modal } from "@/components/ui/dialog";
import { Field, selectClass } from "@/components/data/panel";

export const KIND_LABEL: Record<Unit["kind"], string> = {
  count: "Count",
  weight: "Weight",
  volume: "Volume",
  length: "Length",
  area: "Area",
  pack: "Pack",
};

export function useUnits(all = false) {
  const [units, setUnits] = useState<Unit[]>([]);
  const load = useCallback(() => {
    api
      .get<{ units: Unit[] }>("/api/v1/units")
      .then((d) => setUnits(all ? d.units : d.units.filter((u) => u.status === "active")))
      .catch(() => {});
  }, [all]);
  useEffect(load, [load]);
  return { units, load };
}

/**
 * A product's unit: the measurable units (not pack units), grouped by kind.
 * `current` is the product's saved unit: it shows (and stays chosen) while
 * the list loads, or when it isn't in the list (switched off), so the field
 * never falls back to the first unit on its own.
 */
export function UnitSelect({ id, value, onChange, units, current }: { id: string; value: string; onChange: (v: string) => void; units: Unit[]; current?: UnitRef | null }) {
  const kinds = (["count", "weight", "volume", "length", "area"] as const).filter((k) => units.some((u) => u.kind === k));
  const missing = current && value === current.unit_id && !units.some((u) => u.unit_id === current.unit_id) ? current : null;
  return (
    <select id={id} value={value} onChange={(e) => onChange(e.target.value)} className={`${selectClass} w-full`} disabled={units.length === 0 && !missing}>
      {units.length === 0 && !missing && <option value={value}>Loading units…</option>}
      {missing && (
        <option value={missing.unit_id}>
          {missing.name} ({missing.symbol})
        </option>
      )}
      {kinds.map((k) => (
        <optgroup key={k} label={KIND_LABEL[k]}>
          {units
            .filter((u) => u.kind === k)
            .map((u) => (
              <option key={u.unit_id} value={u.unit_id}>
                {u.name} ({u.symbol})
              </option>
            ))}
        </optgroup>
      ))}
    </select>
  );
}

/** A quantity with its unit: "2.5 kg", "30 pcs". */
export function withUnit(qty: string | number, symbol?: string | null): string {
  const n = typeof qty === "number" ? qty : Number(qty);
  const shown = Number.isNaN(n) ? String(qty) : String(Number(n.toFixed(3)));
  return symbol ? `${shown} ${symbol}` : shown;
}

/**
 * A variant's barcodes and their packs, with "Pack…" to change what a scan
 * adds and "Add a pack barcode" for another code (a tray, a bag).
 */
export function BarcodePacks({ variantId, barcodes, unit, onChanged }: { variantId: string; barcodes: BarcodeInfo[]; unit: UnitRef | null | undefined; onChanged: () => void }) {
  const [editing, setEditing] = useState<BarcodeInfo | "new" | null>(null);
  return (
    <div className="flex flex-col gap-1.5" data-testid={`barcodes-${variantId}`}>
      <span className="text-xs font-medium text-muted-foreground">Barcodes{unit ? ` · counted in ${unit.name.toLowerCase()} (${unit.symbol})` : ""}</span>
      {barcodes.length === 0 && <span className="text-xs text-muted-foreground">No barcode yet.</span>}
      {barcodes.map((b) => (
        <div key={b.code} className="flex flex-wrap items-center gap-2 text-sm">
          <span className="font-mono">{b.code}</span>
          <span className="text-xs text-muted-foreground">
            {b.source === "generated" ? "system" : "original"}
            {b.is_primary ? " · primary" : ""}
          </span>
          <span className="rounded bg-muted px-1.5 py-0.5 text-xs">{b.pack_label ? `${b.pack_label} = ${withUnit(b.pack_qty, unit?.symbol)}` : `1 ${unit?.symbol ?? ""}`.trim()}</span>
          <Button type="button" size="sm" variant="ghost" onClick={() => setEditing(b)} aria-label={`Pack of ${b.code}`}>
            <Package aria-hidden /> Pack…
          </Button>
        </div>
      ))}
      <Button type="button" size="sm" variant="outline" className="w-fit" onClick={() => setEditing("new")}>
        <Plus aria-hidden /> Add a pack barcode
      </Button>
      {editing && <PackDialog variantId={variantId} barcode={editing === "new" ? null : editing} unit={unit} onClose={() => setEditing(null)} onSaved={onChanged} />}
    </div>
  );
}

function PackDialog({
  variantId,
  barcode,
  unit,
  onClose,
  onSaved,
}: {
  variantId: string;
  barcode: BarcodeInfo | null;
  unit: UnitRef | null | undefined;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { units } = useUnits();
  const [code, setCode] = useState("");
  const [packUnit, setPackUnit] = useState(barcode?.pack_unit_id ?? "");
  const [size, setSize] = useState(barcode?.pack_size ?? (barcode && barcode.pack_qty !== "1.000" ? String(Number(barcode.pack_qty)) : ""));
  const [busy, setBusy] = useState(false);
  // A pack can be a Box / Bag / Tray … of the base unit, or another unit of the same kind (500 g of a kg item).
  const choices = units.filter((u) => u.kind === "pack" || (unit && u.kind === unit.kind && u.unit_id !== unit.unit_id));
  const effectiveUnit = packUnit;
  const effective = units.find((u) => u.unit_id === packUnit);

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    const body = effectiveUnit ? { pack_unit_id: effectiveUnit, pack_size: Number(size) } : size ? { pack_size: Number(size) } : {};
    try {
      if (barcode) await api.put(`/api/v1/products/variants/${variantId}/barcodes/${encodeURIComponent(barcode.code)}/pack`, body);
      else await api.post(`/api/v1/products/variants/${variantId}/barcodes`, { ...(code.trim() ? { code: code.trim() } : {}), ...body });
      toast.success(barcode ? `Pack of ${barcode.code} saved` : "Pack barcode added");
      onSaved();
      onClose();
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : "Could not save the pack");
    } finally {
      setBusy(false);
    }
  }
  const sizeLabel = effective
    ? effective.kind === "pack"
      ? `${unit?.symbol ?? "Units"} in one ${effective.name.toLowerCase()} *`
      : `How many ${effective.symbol} *`
    : `${unit?.symbol ?? "Units"} per scan (empty = 1)`;
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={barcode ? `Pack of ${barcode.code}` : "Add a pack barcode"}
      description="What one scan of this barcode adds to a sale or a receipt. The price stays per unit."
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="pack-form" disabled={busy || (!!effectiveUnit && !(Number(size) > 0))}>
            {busy && <Loader2 className="animate-spin" aria-hidden />} Save
          </Button>
        </>
      }
    >
      <form id="pack-form" onSubmit={save} className="grid grid-cols-2 gap-3">
        {!barcode && (
          <Field label="Barcode" htmlFor="pk-code" className="col-span-2">
            <Input id="pk-code" value={code} onChange={(e) => setCode(e.target.value)} placeholder="Empty = generate one" />
          </Field>
        )}
        <Field label="Pack" htmlFor="pk-unit">
          <select
            id="pk-unit"
            value={packUnit}
            onChange={(e) => setPackUnit(e.target.value)}
            className={`${selectClass} w-full`}
          >
            <option value="">One {unit?.symbol ?? "unit"} (no pack)</option>
            {choices.map((u) => (
              <option key={u.unit_id} value={u.unit_id}>
                {u.name}
                {u.kind !== "pack" ? ` (${u.symbol})` : ""}
              </option>
            ))}
          </select>
        </Field>
        <Field label={sizeLabel} htmlFor="pk-size">
          <Input id="pk-size" type="number" min="0" step="any" value={size} onChange={(e) => setSize(e.target.value)} />
        </Field>
        {effective && effective.kind !== "pack" && effective.factor && unit && Number(size) > 0 && (
          <p className="col-span-2 text-xs text-muted-foreground">
            {size} {effective.symbol} = {withUnit((Number(size) * effective.factor) / (units.find((u) => u.unit_id === unit.unit_id)?.factor ?? 1), unit.symbol)} per scan.
          </p>
        )}
      </form>
    </Modal>
  );
}
