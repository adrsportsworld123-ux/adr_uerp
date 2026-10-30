"use client";

// Milk Recording (erp-core-go §3.82): one milking of a dairy herd — each
// female's litres; the total goes into stock as one production entry. An
// animal in its milk withdrawal is refused unless a manager (farm.manage)
// records it with a reason (the milk was discarded). ?batch= starts on a
// herd. farm.record.
import { Suspense, useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { Loader2 } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import type { FarmBatch, FarmMilkSheet } from "@/lib/types";
import { localDate, shortDate } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { PageHeader } from "@/components/data/page-header";
import { Field, FilterBar, Panel, selectClass } from "@/components/data/panel";
import { ProductPicker, VariantOption } from "@/components/data/product-picker";
import { message } from "@/components/farm/farm";

type Session = "am" | "pm" | "day";
const SESSION_LABEL: Record<Session, string> = { am: "Morning", pm: "Evening", day: "Whole day" };

function MilkSheetPage() {
  const { can } = usePermissions();
  const params = useSearchParams();
  const [herds, setHerds] = useState<FarmBatch[]>([]);
  const [batchId, setBatchId] = useState(params.get("batch") ?? "");
  const [date, setDate] = useState(() => localDate());
  const [session, setSession] = useState<Session>("am");
  const [item, setItem] = useState<VariantOption | null>(null);
  const [sheet, setSheet] = useState<FarmMilkSheet | null>(null);
  const [litres, setLitres] = useState<Record<string, string>>({});
  const [blocked, setBlocked] = useState<string | null>(null);
  const [overrideReason, setOverrideReason] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    api
      .get<{ batches: FarmBatch[] }>("/api/v1/farm/batches?status=active")
      .then((d) => setHerds(d.batches.filter((b) => (b.species === "cow" || b.species === "goat") && b.kpi.on_register > 0)))
      .catch(() => setHerds([]));
  }, []);
  const herd = batchId || herds[0]?.batch_id || "";
  const load = useCallback(() => {
    if (!herd) return;
    api
      .get<FarmMilkSheet>(`/api/v1/farm/batches/${herd}/milk?date=${date}`)
      .then((s) => {
        setSheet(s);
        setLitres({});
        setBlocked(null);
      })
      .catch((e) => toast.error(message(e, "Could not load the milk sheet")));
  }, [herd, date]);
  useEffect(load, [load]);
  const recorded = (a: FarmMilkSheet["animals"][number]) => a[session];
  const total = useMemo(() => Object.values(litres).reduce((t, v) => t + (Number(v) || 0), 0), [litres]);

  async function save(override: boolean) {
    if (!sheet || !item) return;
    setBusy(true);
    try {
      const entries = Object.entries(litres)
        .filter(([, v]) => v.trim() !== "")
        .map(([animal_id, v]) => ({ animal_id, litres: Number(v) }));
      await api.post(`/api/v1/farm/batches/${sheet.batch_id}/milk`, {
        date,
        session,
        variant_id: item.variantId,
        entries,
        override_withdrawal: override || undefined,
        override_reason: override ? overrideReason : undefined,
      });
      toast.success(`${SESSION_LABEL[session]} milking: ${Math.round(total * 1000) / 1000} L into stock`);
      load();
    } catch (e) {
      if (e instanceof ApiError && e.code === "WITHDRAWAL_PERIOD") setBlocked(e.message);
      else toast.error(message(e, "Could not record the milking"));
    } finally {
      setBusy(false);
    }
  }

  if (!can("farm.record")) return <p className="text-sm text-muted-foreground">{missingPermissionHint("farm.record")}</p>;
  return (
    <div className="max-w-4xl space-y-5">
      <PageHeader title="Milk Recording" description="Each cow's or goat's litres for a milking. The total goes into stock as the milk item." />
      <FilterBar>
        <Field label="Herd" htmlFor="mk-herd">
          <select id="mk-herd" value={herd} onChange={(e) => setBatchId(e.target.value)} className={`${selectClass} w-56`}>
            {herds.length === 0 && <option value="">No dairy herd with animals on the register</option>}
            {herds.map((b) => (
              <option key={b.batch_id} value={b.batch_id}>
                {b.batch_number}
                {b.house ? ` · ${b.house}` : ""} · {b.branch}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Date" htmlFor="mk-date">
          <Input id="mk-date" type="date" value={date} max={localDate()} onChange={(e) => setDate(e.target.value)} />
        </Field>
        <Field label="Milking" htmlFor="mk-session">
          <select id="mk-session" value={session} onChange={(e) => setSession(e.target.value as Session)} className={`${selectClass} w-36`}>
            {(Object.keys(SESSION_LABEL) as Session[]).map((s) => (
              <option key={s} value={s}>
                {SESSION_LABEL[s]}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Milk item (in litres) *" htmlFor="mk-item">
          <div className="w-64">
            <ProductPicker id="mk-item" value={item} onChange={setItem} placeholder="Raw milk" />
          </div>
        </Field>
      </FilterBar>
      {sheet && (
        <Panel
          title={`${sheet.batch_number} · ${shortDate(sheet.date)} · ${SESSION_LABEL[session]}`}
          description={sheet.batch_withdrawal_milk_until ? `The whole herd is in a milk withdrawal until ${shortDate(sheet.batch_withdrawal_milk_until)}.` : undefined}
          actions={
            <Button onClick={() => save(false)} disabled={busy || !item || total <= 0}>
              {busy && <Loader2 className="animate-spin" aria-hidden />} Save {Math.round(total * 1000) / 1000} L
            </Button>
          }
        >
          <Table data-testid="milk-sheet">
            <TableHeader>
              <TableRow>
                <TableHead>Animal</TableHead>
                <TableHead>In milk</TableHead>
                <TableHead className="text-right">Already recorded</TableHead>
                <TableHead className="w-36 text-right">Litres</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {sheet.animals.length === 0 && (
                <TableRow>
                  <TableCell colSpan={4} className="py-5 text-center text-sm text-muted-foreground">
                    No females of this herd on the register.
                  </TableCell>
                </TableRow>
              )}
              {sheet.animals.map((a) => (
                <TableRow key={a.animal_id}>
                  <TableCell>
                    <Link href={`/farm/animals/${a.animal_id}`} className="font-medium hover:underline">
                      {a.tag}
                    </Link>
                    {a.name && <span className="ms-1 text-xs text-muted-foreground">{a.name}</span>}
                    {a.withdrawal_milk_until && <span className="block text-xs text-warn">No milk until {shortDate(a.withdrawal_milk_until)}</span>}
                  </TableCell>
                  <TableCell className="text-sm">{a.lactating ? `${a.days_in_milk ?? 0} days` : "Dry / not calved"}</TableCell>
                  <TableCell className="text-right tabular">{recorded(a) != null ? `${recorded(a)} L` : "—"}</TableCell>
                  <TableCell className="text-right">
                    <Input
                      aria-label={`Litres from ${a.tag}`}
                      type="number"
                      min="0"
                      step="0.1"
                      className="ms-auto w-28 text-right"
                      disabled={recorded(a) != null}
                      value={litres[a.animal_id] ?? ""}
                      onChange={(e) => setLitres((l) => ({ ...l, [a.animal_id]: e.target.value }))}
                    />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          {blocked && (
            <div role="alert" className="mt-3 space-y-2 rounded-md border border-destructive/40 bg-destructive-soft p-3 text-sm">
              <p>{blocked}</p>
              {can("farm.manage") ? (
                <div className="flex items-end gap-2">
                  <Field label="Why record it anyway *" htmlFor="mk-override" className="flex-1">
                    <Input id="mk-override" value={overrideReason} onChange={(e) => setOverrideReason(e.target.value)} placeholder="Recorded, then discarded" />
                  </Field>
                  <Button variant="destructive" disabled={busy || !overrideReason.trim()} onClick={() => save(true)}>
                    Record anyway
                  </Button>
                </div>
              ) : (
                <p className="text-xs">Leave that animal out, or ask a manager (farm.manage) to record it with a reason.</p>
              )}
            </div>
          )}
        </Panel>
      )}
    </div>
  );
}

export default function MilkPage() {
  return (
    <Suspense>
      <MilkSheetPage />
    </Suspense>
  );
}
