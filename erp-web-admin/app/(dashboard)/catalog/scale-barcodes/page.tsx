"use client";

// Weighing-scale label formats (erp-core-go migrations/084, §3.87). A scale
// prints a 13-digit label: a 2-digit prefix, the item's PLU, then its
// weight or its price, then a check digit. With nothing saved the system
// reads one convention (21 + 5-digit PLU + weight in grams); list the
// formats your scales print and only those are read. catalog.manage.
import { useEffect, useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import type { ScaleFormat } from "@/lib/types";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { PageHeader } from "@/components/data/page-header";
import { Panel, selectClass } from "@/components/data/panel";

const PREFIXES = ["02", "21", "22", "23", "24", "25", "26", "27", "28", "29"];

/** A sample label for the format, so the layout can be checked at a glance. */
function sampleLabel(f: ScaleFormat): string {
  const plu = "1234567".slice(0, f.plu_digits);
  const value = f.value_kind === "weight" ? "1250" : "12550";
  return `${f.prefix} ${plu} ${value.padStart(f.value_digits, "0").slice(-f.value_digits)} ✓`;
}

function sampleMeaning(f: ScaleFormat): string {
  const raw = Number((f.value_kind === "weight" ? "1250" : "12550").padStart(f.value_digits, "0").slice(-f.value_digits));
  const v = raw / 10 ** f.value_decimals;
  return f.value_kind === "weight" ? `${v.toFixed(3)} kg of PLU ${"1234567".slice(0, f.plu_digits)}` : `₹${v.toFixed(2)} of PLU ${"1234567".slice(0, f.plu_digits)}`;
}

export default function ScaleBarcodesPage() {
  const { can } = usePermissions();
  const allowed = can("catalog.manage");
  const [formats, setFormats] = useState<ScaleFormat[] | null>(null);
  const [configured, setConfigured] = useState(false);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api
      .get<{ formats: ScaleFormat[]; configured: boolean }>("/api/v1/settings/scale-barcodes")
      .then((d) => {
        setFormats(d.formats);
        setConfigured(d.configured);
      })
      .catch(() => setFormats([]));
  }, []);

  const set = (i: number, patch: Partial<ScaleFormat>) =>
    setFormats((fs) =>
      (fs ?? []).map((f, j) => {
        if (j !== i) return f;
        const next = { ...f, ...patch };
        // PLU + value are always 10 digits.
        if (patch.plu_digits != null) next.value_digits = 10 - patch.plu_digits;
        if (patch.value_kind === "weight") next.value_decimals = 3;
        if (patch.value_kind === "price") next.value_decimals = 2;
        return next;
      }),
    );
  const prefixes = (formats ?? []).map((f) => f.prefix);
  const duplicate = prefixes.find((p, i) => prefixes.indexOf(p) !== i);

  async function save(list: ScaleFormat[]) {
    setBusy(true);
    try {
      const d = await api.put<{ formats: ScaleFormat[]; configured: boolean }>("/api/v1/settings/scale-barcodes", { formats: list });
      setFormats(d.formats);
      setConfigured(d.configured);
      toast.success(d.configured ? "Scale formats saved" : "Back to the standard format");
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not save the formats");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="max-w-4xl space-y-5">
      <PageHeader
        title="Scale Barcodes"
        description="How your weighing scales lay out their labels: a prefix, the item's PLU (set on the product), then its weight or its price. Scanning a label adds that weight — or, for a price label, the weight that price buys."
      />
      {!configured && formats && (
        <p className="text-sm text-muted-foreground" data-testid="scale-default-note">
          Nothing saved yet, so the standard format below is read. Change it or add your scale&apos;s formats and save.
        </p>
      )}
      <Panel>
        <Table data-testid="scale-formats">
          <TableHeader>
            <TableRow>
              <TableHead>Prefix</TableHead>
              <TableHead>PLU digits</TableHead>
              <TableHead>The rest is</TableHead>
              <TableHead>Example label</TableHead>
              <TableHead>On</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {(formats ?? []).map((f, i) => (
              <TableRow key={i}>
                <TableCell>
                  <select aria-label="Prefix" value={f.prefix} disabled={!allowed} onChange={(e) => set(i, { prefix: e.target.value })} className={selectClass}>
                    {PREFIXES.map((p) => (
                      <option key={p} value={p}>
                        {p}
                      </option>
                    ))}
                  </select>
                </TableCell>
                <TableCell>
                  <select
                    aria-label="PLU digits"
                    value={f.plu_digits}
                    disabled={!allowed}
                    onChange={(e) => set(i, { plu_digits: Number(e.target.value) })}
                    className={selectClass}
                  >
                    {[4, 5, 6].map((n) => (
                      <option key={n} value={n}>
                        {n}
                      </option>
                    ))}
                  </select>
                </TableCell>
                <TableCell>
                  <select
                    aria-label="Value"
                    value={`${f.value_kind}:${f.value_decimals}`}
                    disabled={!allowed}
                    onChange={(e) => {
                      const [kind, dec] = e.target.value.split(":");
                      set(i, { value_kind: kind as ScaleFormat["value_kind"] });
                      set(i, { value_decimals: Number(dec) });
                    }}
                    className={selectClass}
                  >
                    <option value="weight:3">Weight in grams</option>
                    <option value="weight:2">Weight in 10 g</option>
                    <option value="price:2">Price in paise</option>
                    <option value="price:0">Price in rupees</option>
                  </select>
                </TableCell>
                <TableCell className="text-sm">
                  <span className="font-mono">{sampleLabel(f)}</span>
                  <div className="text-xs text-muted-foreground">{sampleMeaning(f)}</div>
                </TableCell>
                <TableCell>
                  <input type="checkbox" aria-label="Read this format" checked={f.active} disabled={!allowed} onChange={(e) => set(i, { active: e.target.checked })} />
                </TableCell>
                <TableCell>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    aria-label="Remove"
                    disabled={!allowed}
                    onClick={() => setFormats((fs) => (fs ?? []).filter((_, j) => j !== i))}
                  >
                    <Trash2 />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
        {duplicate && <p className="text-sm text-destructive">Prefix {duplicate} is listed twice.</p>}
        <div className="flex flex-wrap gap-2">
          <Button
            variant="outline"
            disabled={!allowed || (formats ?? []).length >= 9}
            onClick={() =>
              setFormats((fs) => [
                ...(fs ?? []),
                {
                  prefix: PREFIXES.find((p) => p !== "02" && !(fs ?? []).some((f) => f.prefix === p)) ?? "29",
                  plu_digits: 5,
                  value_kind: "price",
                  value_digits: 5,
                  value_decimals: 2,
                  active: true,
                },
              ])
            }
          >
            <Plus /> Add a format
          </Button>
          <Button disabled={!allowed || busy || !!duplicate || !formats} onClick={() => save(formats ?? [])} title={allowed ? undefined : missingPermissionHint("catalog.manage")}>
            Save formats
          </Button>
          {configured && (
            <Button variant="ghost" disabled={!allowed || busy} onClick={() => save([])}>
              Back to the standard format
            </Button>
          )}
        </div>
        <p className="text-xs text-muted-foreground">
          Prefix 20 is this system&apos;s own barcodes, so a scale can&apos;t use it. A price label sells the weight that price buys at the item&apos;s price
          per kg, to the nearest gram.
        </p>
      </Panel>
    </div>
  );
}
