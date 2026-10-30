"use client";

// Farm Dashboard (erp-core-go §3.81–§3.82): today on the farm — birds and
// animals alive, deaths, feed, eggs and milk today; the vaccinations due
// today or overdue (record one from here) and the next 7 days'; feed
// running low, treatments in withdrawal, births due in the next 14 days,
// medicine expired or expiring; every active flock, herd and pond with
// its figures. farm.record.
import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { Plus, Syringe } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api-client";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import type { FarmBatch, FarmBirthDue, FarmDue, FarmFeedStockRow, FarmMedicineRow, FarmWithdrawalRow } from "@/lib/types";
import { shortDate } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { PageHeader } from "@/components/data/page-header";
import { Field, FilterBar, Panel, Stat } from "@/components/data/panel";
import { StatusChip } from "@/components/data/status-chip";
import { BatchDialog, EventDialog, FarmBranchSelect, message } from "@/components/farm/farm";
import { BatchTable } from "@/components/farm/batch-table";

type DueRow = FarmDue & { batch_id: string; batch_number: string; house: string };
type Dashboard = {
  today: string;
  batches: FarmBatch[];
  due: DueRow[];
  upcoming: DueRow[];
  totals: { alive: number; mortality_today: number; feed_today_kg: number; eggs_today: number; milk_today_l: number };
  feed_low: FarmFeedStockRow[];
  withdrawals: FarmWithdrawalRow[];
  births_due: FarmBirthDue[];
  medicine_alerts: FarmMedicineRow[];
};

export default function FarmDashboardPage() {
  const { can } = usePermissions();
  const [branchId, setBranchId] = useState("");
  const [data, setData] = useState<Dashboard | null>(null);
  const [placing, setPlacing] = useState(false);
  const [recording, setRecording] = useState<{ batch: FarmBatch; due: DueRow } | null>(null);
  const load = useCallback(() => {
    api
      .get<Dashboard>(`/api/v1/farm/dashboard${branchId ? `?branch_id=${branchId}` : ""}`)
      .then(setData)
      .catch((e) => toast.error(message(e, "Could not load the farm")));
  }, [branchId]);
  useEffect(load, [load]);

  if (!can("farm.record")) return <p className="text-sm text-muted-foreground">{missingPermissionHint("farm.record")}</p>;
  return (
    <div className="space-y-5">
      <PageHeader
        title="Farm Dashboard"
        description="Today on the farm: animals alive, deaths, feed, eggs and milk, the vaccinations due, and every active flock, herd and pond."
        actions={
          <Button onClick={() => setPlacing(true)} disabled={!can("farm.manage")}>
            <Plus aria-hidden /> New batch
          </Button>
        }
      />
      <FilterBar>
        <Field label="Farm" htmlFor="fd-branch">
          <FarmBranchSelect id="fd-branch" value={branchId} onChange={setBranchId} allLabel="Every farm" />
        </Field>
      </FilterBar>
      {data && (
        <>
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-5" data-testid="farm-totals">
            <Stat label="Alive" value={data.totals.alive.toLocaleString("en-IN")} strong />
            <Stat label="Died today" value={data.totals.mortality_today} />
            <Stat label="Feed today" value={`${data.totals.feed_today_kg} kg`} />
            <Stat label="Eggs today" value={data.totals.eggs_today.toLocaleString("en-IN")} />
            <Stat label="Milk today" value={`${data.totals.milk_today_l} L`} />
          </div>
          <Panel title={`Due now (${data.due.length})`} description="Vaccinations and medicines due today or overdue.">
            <Table data-testid="farm-due">
              <TableHeader>
                <TableRow>
                  <TableHead>Batch</TableHead>
                  <TableHead>What</TableHead>
                  <TableHead>Due</TableHead>
                  <TableHead className="text-right">Action</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {data.due.map((d) => (
                  <TableRow key={`${d.batch_id}-${d.schedule_item_id}`}>
                    <TableCell className="text-sm">
                      <Link href={`/farm/batches/${d.batch_id}`} className="font-medium hover:underline">
                        {d.batch_number}
                      </Link>
                      <span className="block text-xs text-muted-foreground">{d.house}</span>
                    </TableCell>
                    <TableCell className="text-sm">
                      {d.name}
                      <span className="block text-xs text-muted-foreground">
                        Day {d.day_of_age}
                        {d.method ? ` · ${d.method}` : ""}
                      </span>
                    </TableCell>
                    <TableCell>
                      <StatusChip status="" tone={d.state === "overdue" ? "bad" : "warn"} label={d.state === "overdue" ? `Overdue since ${shortDate(d.due_on)}` : "Today"} />
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        size="sm"
                        onClick={() => {
                          const b = data.batches.find((x) => x.batch_id === d.batch_id);
                          if (b) setRecording({ batch: b, due: d });
                        }}
                        aria-label={`Record ${d.name} for ${d.batch_number}`}
                      >
                        <Syringe aria-hidden /> Record
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
                {data.due.length === 0 && (
                  <TableRow>
                    <TableCell colSpan={4} className="py-5 text-center text-sm text-muted-foreground">
                      Nothing due. {data.upcoming.length ? `Next: ${data.upcoming[0].name} for ${data.upcoming[0].batch_number} on ${shortDate(data.upcoming[0].due_on)}.` : ""}
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          </Panel>
          {(data.feed_low.length > 0 || data.withdrawals.length > 0 || data.births_due.length > 0 || data.medicine_alerts.length > 0) && (
            <div className="grid gap-4 md:grid-cols-2" data-testid="farm-alerts">
              {data.feed_low.length > 0 && (
                <Panel title={`Feed running low (${data.feed_low.length})`} actions={<Link href="/farm/feed" className="text-sm hover:underline">Feed</Link>}>
                  <ul className="space-y-1 text-sm">
                    {data.feed_low.map((f) => (
                      <li key={`${f.branch_id}-${f.product_id}`} className="flex justify-between gap-3">
                        <span>
                          {f.product} <span className="text-xs text-muted-foreground">{f.branch}</span>
                        </span>
                        <span className="tabular text-destructive">
                          {f.free} {f.unit} · {f.days_left} days
                        </span>
                      </li>
                    ))}
                  </ul>
                </Panel>
              )}
              {data.withdrawals.length > 0 && (
                <Panel title={`In withdrawal (${data.withdrawals.length})`} actions={<Link href="/farm/health" className="text-sm hover:underline">Health</Link>}>
                  <ul className="space-y-1 text-sm">
                    {data.withdrawals.slice(0, 8).map((w) => (
                      <li key={w.event_id} className="flex justify-between gap-3">
                        <span>
                          {w.batch_number}
                          {w.animal_tag ? ` · ${w.animal_tag}` : ""} <span className="text-xs text-muted-foreground">{w.treatment}</span>
                        </span>
                        <span className="text-xs text-warn">
                          {[w.milk_until && `milk ${shortDate(w.milk_until)}`, w.eggs_until && `eggs ${shortDate(w.eggs_until)}`, w.meat_until && `meat ${shortDate(w.meat_until)}`]
                            .filter(Boolean)
                            .join(" · ")}
                        </span>
                      </li>
                    ))}
                  </ul>
                </Panel>
              )}
              {data.births_due.length > 0 && (
                <Panel title={`Births due (${data.births_due.length})`} description="Expected in the next 14 days, from the last service.">
                  <ul className="space-y-1 text-sm">
                    {data.births_due.map((b) => (
                      <li key={b.animal_id} className="flex justify-between gap-3">
                        <Link href={`/farm/animals/${b.animal_id}`} className="hover:underline">
                          {b.tag}
                          {b.name ? ` · ${b.name}` : ""} <span className="text-xs text-muted-foreground">{b.batch_number}</span>
                        </Link>
                        <span className="text-xs">
                          {shortDate(b.expected_on)} {b.status === "served" && <span className="text-muted-foreground">(not confirmed)</span>}
                        </span>
                      </li>
                    ))}
                  </ul>
                </Panel>
              )}
              {data.medicine_alerts.length > 0 && (
                <Panel title={`Medicine expiring (${data.medicine_alerts.length})`} actions={<Link href="/farm/health?tab=stock" className="text-sm hover:underline">Stock</Link>}>
                  <ul className="space-y-1 text-sm">
                    {data.medicine_alerts.map((m) => (
                      <li key={`${m.branch_id}-${m.product_id}`} className="flex justify-between gap-3">
                        <span>
                          {m.product} <span className="text-xs text-muted-foreground">{m.branch}</span>
                        </span>
                        <span className={`text-xs ${m.expired_qty > 0 ? "text-destructive" : "text-warn"}`}>
                          {m.expired_qty > 0 ? `${m.expired_qty} expired` : `${m.expiring_qty} by ${m.next_expiry ? shortDate(m.next_expiry) : ""}`}
                        </span>
                      </li>
                    ))}
                  </ul>
                </Panel>
              )}
            </div>
          )}
          <Panel title={`Active batches (${data.batches.length})`}>
            <BatchTable batches={data.batches} />
          </Panel>
        </>
      )}
      {placing && <BatchDialog onClose={() => setPlacing(false)} onSaved={load} />}
      {recording && <EventDialog batch={recording.batch} type="vaccination" due={recording.due} onClose={() => setRecording(null)} onSaved={load} />}
    </div>
  );
}
