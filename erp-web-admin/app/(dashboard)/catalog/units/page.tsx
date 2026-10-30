"use client";

// Units of measure (erp-core-go §3.79, migrations/076): the standard set
// (Pieces, Dozen, Kg, g, L, ml, m … and pack units Box, Bag, Tray …) and
// any the business adds. A product is counted in one unit; a barcode can
// be a pack of it. A unit's kind and size never change once made; its
// name, symbol, decimals and status can. units.manage.
import { FormEvent, useState } from "react";
import { Loader2, Plus } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import type { Unit } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Modal } from "@/components/ui/dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { PageHeader } from "@/components/data/page-header";
import { Field, Panel, selectClass } from "@/components/data/panel";
import { StatusChip } from "@/components/data/status-chip";
import { KIND_LABEL, useUnits } from "@/components/catalog/units";

const REFERENCE: Record<Unit["kind"], string> = { count: "pcs", weight: "kg", volume: "L", length: "m", area: "sq m", pack: "" };

export default function UnitsPage() {
  const { can } = usePermissions();
  const { units, load } = useUnits(true);
  const [editing, setEditing] = useState<Unit | "new" | null>(null);
  const allowed = can("units.manage");

  async function toggle(u: Unit) {
    try {
      await api.patch(`/api/v1/units/${u.unit_id}`, { status: u.status === "active" ? "inactive" : "active" });
      toast.success(`${u.name} ${u.status === "active" ? "switched off" : "switched on"}`);
      load();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not change it");
    }
  }

  return (
    <div className="space-y-5">
      <PageHeader
        title="Units"
        description="What products are counted in — pieces, kilograms, litres, metres … — and the packs a barcode can sell (a tray, a bag, a box). Stock, cost and price stay per unit; a pack barcode just adds its contents."
        actions={
          <Button onClick={() => setEditing("new")} disabled={!allowed} title={allowed ? undefined : missingPermissionHint("units.manage")}>
            <Plus aria-hidden /> New unit
          </Button>
        }
      />
      <Panel>
        <Table data-testid="units-table">
          <TableHeader>
            <TableRow>
              <TableHead>Unit</TableHead>
              <TableHead>Kind</TableHead>
              <TableHead>Size</TableHead>
              <TableHead className="text-right">Decimals</TableHead>
              <TableHead className="text-right">Products</TableHead>
              <TableHead>Status</TableHead>
              <TableHead className="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {units.map((u) => (
              <TableRow key={u.unit_id}>
                <TableCell className="text-sm">
                  <span className="font-medium">{u.name}</span> <span className="text-muted-foreground">({u.symbol})</span>
                  <span className="block font-mono text-xs text-muted-foreground">
                    {u.code}
                    {u.is_system ? " · standard" : ""}
                  </span>
                </TableCell>
                <TableCell className="text-sm">{KIND_LABEL[u.kind]}</TableCell>
                <TableCell className="text-sm">{u.factor != null ? `1 ${u.symbol} = ${Number(u.factor.toFixed(6))} ${REFERENCE[u.kind]}` : "Set on each barcode"}</TableCell>
                <TableCell className="tabular text-right">{u.decimals === 0 ? "Whole" : u.decimals}</TableCell>
                <TableCell className="tabular text-right">{u.products}</TableCell>
                <TableCell>
                  <StatusChip status="" tone={u.status === "active" ? "ok" : "muted"} label={u.status === "active" ? "Active" : "Inactive"} />
                </TableCell>
                <TableCell>
                  <div className="flex justify-end gap-1.5">
                    <Button size="sm" variant="ghost" disabled={!allowed} onClick={() => setEditing(u)} aria-label={`Edit ${u.name}`}>
                      Edit
                    </Button>
                    {u.code !== "PCS" && (
                      <Button size="sm" variant="ghost" disabled={!allowed} onClick={() => toggle(u)} aria-label={`${u.status === "active" ? "Deactivate" : "Activate"} ${u.name}`}>
                        {u.status === "active" ? "Deactivate" : "Activate"}
                      </Button>
                    )}
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </Panel>
      {editing && <UnitDialog unit={editing === "new" ? null : editing} onClose={() => setEditing(null)} onSaved={load} />}
    </div>
  );
}

function UnitDialog({ unit, onClose, onSaved }: { unit: Unit | null; onClose: () => void; onSaved: () => void }) {
  const [code, setCode] = useState(unit?.code ?? "");
  const [name, setName] = useState(unit?.name ?? "");
  const [symbol, setSymbol] = useState(unit?.symbol ?? "");
  const [kind, setKind] = useState<Unit["kind"]>(unit?.kind ?? "pack");
  const [factor, setFactor] = useState(unit?.factor != null ? String(unit.factor) : "");
  const [decimals, setDecimals] = useState(String(unit?.decimals ?? 0));
  const [busy, setBusy] = useState(false);
  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      if (unit) await api.patch(`/api/v1/units/${unit.unit_id}`, { ...(unit.is_system ? {} : { code }), name, symbol, decimals: Number(decimals) });
      else
        await api.post("/api/v1/units", { code, name, symbol, kind, decimals: Number(decimals), ...(kind === "pack" ? {} : { factor: Number(factor) }) });
      toast.success(`${name} saved`);
      onSaved();
      onClose();
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : "Could not save the unit");
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={unit ? `Edit ${unit.name}` : "New unit"}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="unit-form" disabled={busy || !code.trim() || !name.trim() || !symbol.trim() || (!unit && kind !== "pack" && !(Number(factor) > 0))}>
            {busy && <Loader2 className="animate-spin" aria-hidden />} Save
          </Button>
        </>
      }
    >
      <form id="unit-form" onSubmit={save} className="grid grid-cols-2 gap-3">
        <Field label="Code *" htmlFor="un-code">
          <Input id="un-code" value={code} maxLength={10} disabled={unit?.is_system} onChange={(e) => setCode(e.target.value.toUpperCase())} placeholder="SACK" />
        </Field>
        <Field label="Symbol *" htmlFor="un-symbol">
          <Input id="un-symbol" value={symbol} maxLength={10} onChange={(e) => setSymbol(e.target.value)} placeholder="sack" />
        </Field>
        <Field label="Name *" htmlFor="un-name" className="col-span-2">
          <Input id="un-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="Sack" />
        </Field>
        <Field label="Kind" htmlFor="un-kind">
          <select id="un-kind" value={kind} disabled={!!unit} onChange={(e) => setKind(e.target.value as Unit["kind"])} className={`${selectClass} w-full`}>
            {(Object.keys(KIND_LABEL) as Unit["kind"][]).map((k) => (
              <option key={k} value={k}>
                {KIND_LABEL[k]}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Decimals" htmlFor="un-dec">
          <select id="un-dec" value={decimals} onChange={(e) => setDecimals(e.target.value)} className={`${selectClass} w-full`}>
            <option value="0">Whole numbers</option>
            <option value="1">1</option>
            <option value="2">2</option>
            <option value="3">3</option>
          </select>
        </Field>
        {kind !== "pack" && (
          <Field label={`1 of it is how many ${REFERENCE[kind]} *`} htmlFor="un-factor" className="col-span-2">
            <Input id="un-factor" type="number" step="any" min="0" value={factor} disabled={!!unit} onChange={(e) => setFactor(e.target.value)} />
          </Field>
        )}
        {kind === "pack" && <p className="col-span-2 text-xs text-muted-foreground">A pack has no fixed size: each barcode says how much is in it (a tray of 30, a bag of 25 kg).</p>}
      </form>
    </Modal>
  );
}
