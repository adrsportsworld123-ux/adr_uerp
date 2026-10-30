"use client";

// Health & Medicine (erp-core-go §3.82): treatments still in withdrawal
// (no meat, milk or eggs until …), each farm's medicines and vaccines with
// what is expired or expiring, recent treatments, and the farm items —
// which products are feed, medicine or vaccine, with a medicine's
// withdrawal days (a treatment starts from them). farm.record; farm items
// and settings: farm.manage.
import { FormEvent, Suspense, useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { Loader2, Plus } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api-client";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import type { FarmEvent, FarmItem, FarmMedicineRow, FarmSpecies, FarmWithdrawalRow } from "@/lib/types";
import { localDate, shortDate } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Modal } from "@/components/ui/dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { PageHeader } from "@/components/data/page-header";
import { Field, FilterBar, Panel, selectClass } from "@/components/data/panel";
import { StatusChip } from "@/components/data/status-chip";
import { ProductPicker, VariantOption } from "@/components/data/product-picker";
import { EVENT_LABEL, FarmBranchSelect, SPECIES_LABEL, message } from "@/components/farm/farm";
import { TabStrip, useTab } from "@/components/farm/tabs";

const TABS = ["withdrawals", "stock", "treatments", "items"] as const;
const TAB_LABEL = { withdrawals: "In withdrawal", stock: "Medicine stock", treatments: "Treatments", items: "Farm items" };
const ROLE_LABEL: Record<FarmItem["role"], string> = { feed: "Feed", medicine: "Medicine", vaccine: "Vaccine" };

function HealthInner() {
  const { can } = usePermissions();
  const [tab, setTab] = useTab(TABS, "/farm/health");
  const [branchId, setBranchId] = useState("");
  if (!can("farm.record")) return <p className="text-sm text-muted-foreground">{missingPermissionHint("farm.record")}</p>;
  return (
    <div className="max-w-5xl space-y-5">
      <PageHeader title="Health & Medicine" description="Withdrawal periods, medicine stock and expiry, treatments, and which products are feed, medicine or vaccine." />
      <TabStrip tabs={TABS} labels={TAB_LABEL} value={tab} onChange={setTab} label="Health" />
      {tab !== "items" && (
        <FilterBar>
          <Field label="Farm" htmlFor="hl-branch">
            <FarmBranchSelect id="hl-branch" value={branchId} onChange={setBranchId} allLabel="Every farm" />
          </Field>
        </FilterBar>
      )}
      {tab === "withdrawals" ? <Withdrawals branchId={branchId} /> : tab === "stock" ? <Stock branchId={branchId} /> : tab === "treatments" ? <Treatments branchId={branchId} /> : <Items />}
    </div>
  );
}

export default function HealthPage() {
  return (
    <Suspense>
      <HealthInner />
    </Suspense>
  );
}

function Withdrawals({ branchId }: { branchId: string }) {
  const [rows, setRows] = useState<FarmWithdrawalRow[] | null>(null);
  useEffect(() => {
    api
      .get<{ withdrawals: FarmWithdrawalRow[] }>(`/api/v1/farm/health/withdrawals${branchId ? `?branch_id=${branchId}` : ""}`)
      .then((d) => setRows(d.withdrawals))
      .catch((e) => toast.error(message(e, "Could not load the withdrawals")));
  }, [branchId]);
  return (
    <Panel title="In withdrawal" description="Produce from these is refused until the date shown (a manager can override it with a reason).">
      <Table data-testid="withdrawals">
        <TableHeader>
          <TableRow>
            <TableHead>Batch / animal</TableHead>
            <TableHead>Treatment</TableHead>
            <TableHead>No milk until</TableHead>
            <TableHead>No eggs until</TableHead>
            <TableHead>No meat until</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows?.length === 0 && (
            <TableRow>
              <TableCell colSpan={5} className="py-5 text-center text-sm text-muted-foreground">
                Nothing in withdrawal.
              </TableCell>
            </TableRow>
          )}
          {rows?.map((w) => (
            <TableRow key={w.event_id}>
              <TableCell className="text-sm">
                <Link href={`/farm/batches/${w.batch_id}`} className="font-medium hover:underline">
                  {w.batch_number}
                </Link>
                {w.animal_id && (
                  <Link href={`/farm/animals/${w.animal_id}`} className="ms-1 hover:underline">
                    · {w.animal_tag}
                  </Link>
                )}
                <span className="block text-xs text-muted-foreground">{[SPECIES_LABEL[w.species], w.house].filter(Boolean).join(" · ")}</span>
              </TableCell>
              <TableCell className="text-sm">
                {w.treatment}
                <span className="block text-xs text-muted-foreground">{shortDate(w.treated_on)}</span>
              </TableCell>
              {[w.milk_until, w.eggs_until, w.meat_until].map((d, i) => (
                <TableCell key={i} className="text-sm">
                  {d ? <StatusChip status="" tone="warn" label={shortDate(d)} /> : "—"}
                </TableCell>
              ))}
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Panel>
  );
}

function Stock({ branchId }: { branchId: string }) {
  const { can } = usePermissions();
  const [rows, setRows] = useState<FarmMedicineRow[] | null>(null);
  const [warn, setWarn] = useState("");
  const load = useCallback(() => {
    api
      .get<{ items: FarmMedicineRow[]; expiry_warn_days: number }>(`/api/v1/farm/health/stock${branchId ? `?branch_id=${branchId}` : ""}`)
      .then((d) => {
        setRows(d.items);
        setWarn(String(d.expiry_warn_days));
      })
      .catch((e) => toast.error(message(e, "Could not load the medicine stock")));
  }, [branchId]);
  useEffect(load, [load]);
  async function saveWarn() {
    try {
      await api.put("/api/v1/farm/settings", { expiry_warn_days: Number(warn) });
      toast.success("Saved");
      load();
    } catch (e) {
      toast.error(message(e, "Could not save"));
    }
  }
  return (
    <Panel
      title="Medicine stock"
      description="Medicines and vaccines (Farm items) in each farm's stock. Expiry comes from stock bought with a batch and expiry date."
      actions={
        <div className="flex items-end gap-2">
          <Field label="Warn when expiring within (days)" htmlFor="ms-warn">
            <Input id="ms-warn" type="number" min="1" className="w-20" value={warn} disabled={!can("farm.manage")} onChange={(e) => setWarn(e.target.value)} />
          </Field>
          {can("farm.manage") && (
            <Button size="sm" variant="outline" onClick={saveWarn}>
              Save
            </Button>
          )}
        </div>
      }
    >
      <Table data-testid="medicine-stock">
        <TableHeader>
          <TableRow>
            <TableHead>Item</TableHead>
            <TableHead>Farm</TableHead>
            <TableHead className="text-right">In stock</TableHead>
            <TableHead className="text-right">Used (30 days)</TableHead>
            <TableHead>Next expiry</TableHead>
            <TableHead>Expiry</TableHead>
            <TableHead>Withdrawal (milk · eggs · meat)</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows?.length === 0 && (
            <TableRow>
              <TableCell colSpan={7} className="py-5 text-center text-sm text-muted-foreground">
                No medicines yet. Mark products as medicine or vaccine under Farm items.
              </TableCell>
            </TableRow>
          )}
          {rows?.map((m) => (
            <TableRow key={`${m.branch_id}-${m.product_id}`}>
              <TableCell className="font-medium">
                {m.product}
                <span className="block text-xs text-muted-foreground">{ROLE_LABEL[m.role]}</span>
              </TableCell>
              <TableCell className="text-sm">{m.branch}</TableCell>
              <TableCell className="text-right tabular">
                {m.free} {m.unit}
              </TableCell>
              <TableCell className="text-right tabular">{m.used_30d || "—"}</TableCell>
              <TableCell className="text-sm">{m.next_expiry ? shortDate(m.next_expiry) : "—"}</TableCell>
              <TableCell>
                {m.expired_qty > 0 ? (
                  <StatusChip status="" tone="bad" label={`${m.expired_qty} expired`} />
                ) : m.expiring_qty > 0 ? (
                  <StatusChip status="" tone="warn" label={`${m.expiring_qty} expiring`} />
                ) : (
                  <StatusChip status="" tone="ok" label="OK" />
                )}
              </TableCell>
              <TableCell className="text-sm tabular">
                {[m.withdrawal_milk_days, m.withdrawal_eggs_days, m.withdrawal_meat_days].map((d) => (d == null ? "—" : `${d}d`)).join(" · ")}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Panel>
  );
}

function Treatments({ branchId }: { branchId: string }) {
  const [from, setFrom] = useState(() => localDate(-30));
  const [rows, setRows] = useState<FarmEvent[] | null>(null);
  useEffect(() => {
    const qs = new URLSearchParams({ type: "medicine,vaccination", from, ...(branchId && { branch_id: branchId }) });
    api
      .get<{ events: FarmEvent[] }>(`/api/v1/farm/events?${qs}`)
      .then((d) => setRows(d.events.filter((e) => e.status === "posted")))
      .catch((e) => toast.error(message(e, "Could not load the treatments")));
  }, [branchId, from]);
  return (
    <Panel
      title="Treatments"
      actions={
        <Field label="Since" htmlFor="tr-from">
          <Input id="tr-from" type="date" value={from} max={localDate()} onChange={(e) => setFrom(e.target.value)} />
        </Field>
      }
    >
      <Table data-testid="treatments">
        <TableHeader>
          <TableRow>
            <TableHead>Date</TableHead>
            <TableHead>Batch / animal</TableHead>
            <TableHead>What</TableHead>
            <TableHead>Dose · vet</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows?.length === 0 && (
            <TableRow>
              <TableCell colSpan={4} className="py-5 text-center text-sm text-muted-foreground">
                No treatments in this time.
              </TableCell>
            </TableRow>
          )}
          {rows?.map((e) => (
            <TableRow key={e.event_id}>
              <TableCell className="text-sm">{shortDate(e.event_date)}</TableCell>
              <TableCell className="text-sm">
                <Link href={`/farm/batches/${e.batch_id}`} className="hover:underline">
                  {e.batch_number}
                </Link>
                {e.animal_tag && <span className="ms-1">· {e.animal_tag}</span>}
              </TableCell>
              <TableCell className="text-sm">
                {EVENT_LABEL[e.event_type]}: {e.vaccine ?? e.product ?? "—"}
                {e.cause && <span className="block text-xs text-muted-foreground">{e.cause}</span>}
              </TableCell>
              <TableCell className="text-sm">{[e.dose, e.course_days ? `${e.course_days} days` : "", e.vet].filter(Boolean).join(" · ") || "—"}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Panel>
  );
}

function Items() {
  const { can } = usePermissions();
  const [items, setItems] = useState<FarmItem[]>([]);
  const [editing, setEditing] = useState<FarmItem | "new" | null>(null);
  const load = useCallback(() => {
    api
      .get<{ items: FarmItem[] }>("/api/v1/farm/items")
      .then((d) => setItems(d.items))
      .catch((e) => toast.error(message(e, "Could not load the farm items")));
  }, []);
  useEffect(load, [load]);
  async function remove(f: FarmItem) {
    if (!window.confirm(`Stop treating ${f.product} as a farm ${ROLE_LABEL[f.role].toLowerCase()}? The product itself stays.`)) return;
    try {
      await api.delete(`/api/v1/farm/items/${f.farm_item_id}`);
      load();
    } catch (e) {
      toast.error(message(e, "Could not remove it"));
    }
  }
  return (
    <Panel
      title="Farm items"
      description="Which products are feed, medicine or vaccine. A medicine's withdrawal days fill in a treatment's (and can be changed there)."
      actions={
        <Button size="sm" onClick={() => setEditing("new")} disabled={!can("farm.manage")}>
          <Plus aria-hidden /> Add an item
        </Button>
      }
    >
      <Table data-testid="farm-items">
        <TableHeader>
          <TableRow>
            <TableHead>Product</TableHead>
            <TableHead>Is</TableHead>
            <TableHead>For</TableHead>
            <TableHead>Withdrawal (milk · eggs · meat)</TableHead>
            <TableHead>Dose</TableHead>
            <TableHead className="text-right">Action</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {items.length === 0 && (
            <TableRow>
              <TableCell colSpan={6} className="py-5 text-center text-sm text-muted-foreground">
                No farm items yet.
              </TableCell>
            </TableRow>
          )}
          {items.map((f) => (
            <TableRow key={f.farm_item_id}>
              <TableCell className="font-medium">{f.product}</TableCell>
              <TableCell className="text-sm">{ROLE_LABEL[f.role]}</TableCell>
              <TableCell className="text-sm">{f.species.length ? f.species.map((s) => SPECIES_LABEL[s]).join(", ") : "Any"}</TableCell>
              <TableCell className="text-sm tabular">
                {f.role === "feed" ? "—" : [f.withdrawal_milk_days, f.withdrawal_eggs_days, f.withdrawal_meat_days].map((d) => (d == null ? "—" : `${d}d`)).join(" · ")}
              </TableCell>
              <TableCell className="text-sm">{f.dose_note ?? "—"}</TableCell>
              <TableCell className="text-right">
                <div className="flex justify-end gap-1">
                  <Button size="sm" variant="ghost" disabled={!can("farm.manage")} onClick={() => setEditing(f)} aria-label={`Edit ${f.product}`}>
                    Edit
                  </Button>
                  <Button size="sm" variant="ghost" disabled={!can("farm.manage")} onClick={() => remove(f)} aria-label={`Remove ${f.product}`}>
                    Remove
                  </Button>
                </div>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      {editing && <ItemDialog item={editing === "new" ? null : editing} onClose={() => setEditing(null)} onSaved={load} />}
    </Panel>
  );
}

const ALL_SPECIES: FarmSpecies[] = ["chicken", "goat", "rabbit", "cow", "pig", "fish"];

function ItemDialog({ item, onClose, onSaved }: { item: FarmItem | null; onClose: () => void; onSaved: () => void }) {
  const [product, setProduct] = useState<VariantOption | null>(
    item ? { variantId: item.product_id, productId: item.product_id, productName: item.product, sku: "", sellingPrice: "", label: item.product, trackBatch: false } : null,
  );
  const [role, setRole] = useState<FarmItem["role"]>(item?.role ?? "medicine");
  const [species, setSpecies] = useState<FarmSpecies[]>(item?.species ?? []);
  const [milk, setMilk] = useState(item?.withdrawal_milk_days != null ? String(item.withdrawal_milk_days) : "");
  const [eggs, setEggs] = useState(item?.withdrawal_eggs_days != null ? String(item.withdrawal_eggs_days) : "");
  const [meat, setMeat] = useState(item?.withdrawal_meat_days != null ? String(item.withdrawal_meat_days) : "");
  const [dose, setDose] = useState(item?.dose_note ?? "");
  const [busy, setBusy] = useState(false);
  const n = (v: string) => (v.trim() === "" ? undefined : Number(v));
  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    const body = { product_id: product?.productId, role, species, withdrawal_milk_days: n(milk), withdrawal_eggs_days: n(eggs), withdrawal_meat_days: n(meat), dose_note: dose };
    try {
      if (item) await api.put(`/api/v1/farm/items/${item.farm_item_id}`, body);
      else await api.post("/api/v1/farm/items", body);
      toast.success(`${product?.productName ?? "Item"} saved`);
      onSaved();
      onClose();
    } catch (err) {
      toast.error(message(err, "Could not save it"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={item ? `Edit ${item.product}` : "Add a farm item"}
      width={600}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="farm-item" disabled={busy || !product}>
            {busy && <Loader2 className="animate-spin" aria-hidden />} Save
          </Button>
        </>
      }
    >
      <form id="farm-item" onSubmit={save} className="grid grid-cols-2 gap-3">
        <Field label="Product *" htmlFor="fi-product" className="col-span-2">
          {item ? <p className="text-sm font-medium">{item.product}</p> : <ProductPicker id="fi-product" mode="product" value={product} onChange={setProduct} placeholder="Search the product" />}
        </Field>
        <Field label="It is" htmlFor="fi-role">
          <select id="fi-role" value={role} onChange={(e) => setRole(e.target.value as FarmItem["role"])} className={`${selectClass} w-full`}>
            <option value="feed">Feed</option>
            <option value="medicine">Medicine</option>
            <option value="vaccine">Vaccine</option>
          </select>
        </Field>
        <Field label="Dose" htmlFor="fi-dose">
          <Input id="fi-dose" value={dose} onChange={(e) => setDose(e.target.value)} placeholder="1 ml / 10 kg" />
        </Field>
        <fieldset className="col-span-2 flex flex-wrap gap-3 text-sm">
          <legend className="mb-1 text-xs text-muted-foreground">For (none = any)</legend>
          {ALL_SPECIES.map((s) => (
            <label key={s} className="flex items-center gap-1">
              <input type="checkbox" checked={species.includes(s)} onChange={(e) => setSpecies((x) => (e.target.checked ? [...x, s] : x.filter((y) => y !== s)))} />
              {SPECIES_LABEL[s]}
            </label>
          ))}
        </fieldset>
        {role !== "feed" && (
          <fieldset className="col-span-2 grid grid-cols-3 gap-3 rounded-md border p-3">
            <legend className="px-1 text-xs text-muted-foreground">Withdrawal days — no produce for this long after a dose</legend>
            <Field label="Milk" htmlFor="fi-milk">
              <Input id="fi-milk" type="number" min="0" step="1" value={milk} onChange={(e) => setMilk(e.target.value)} />
            </Field>
            <Field label="Eggs" htmlFor="fi-eggs">
              <Input id="fi-eggs" type="number" min="0" step="1" value={eggs} onChange={(e) => setEggs(e.target.value)} />
            </Field>
            <Field label="Meat" htmlFor="fi-meat">
              <Input id="fi-meat" type="number" min="0" step="1" value={meat} onChange={(e) => setMeat(e.target.value)} />
            </Field>
          </fieldset>
        )}
      </form>
    </Modal>
  );
}
