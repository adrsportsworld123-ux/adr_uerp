"use client";

// Flocks, herds and ponds with their figures (erp-core-go §3.81): the farm
// dashboard's and the batch list's table.
import Link from "next/link";
import type { FarmBatch } from "@/lib/types";
import { rupees, shortDate } from "@/lib/format";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { PURPOSE_LABEL, SPECIES_LABEL } from "@/components/farm/farm";

/** Flocks, herds and ponds with their figures. */
export function BatchTable({ batches }: { batches: FarmBatch[] }) {
  return (
    <Table data-testid="farm-batches">
      <TableHeader>
        <TableRow>
          <TableHead>Batch</TableHead>
          <TableHead>Kind</TableHead>
          <TableHead className="text-right">Age</TableHead>
          <TableHead className="text-right">Alive</TableHead>
          <TableHead className="text-right">Mortality</TableHead>
          <TableHead className="text-right">Weight / FCR</TableHead>
          <TableHead className="text-right">Eggs today</TableHead>
          <TableHead className="text-right">Cost / head</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {batches.map((b) => (
          <TableRow key={b.batch_id}>
            <TableCell className="text-sm">
              <Link href={`/farm/batches/${b.batch_id}`} className="font-medium hover:underline">
                {b.batch_number}
              </Link>
              {b.name ? ` · ${b.name}` : ""}
              <span className="block text-xs text-muted-foreground">
                {[b.house, b.branch].filter(Boolean).join(" · ")}
                {b.kpi.vaccines_due > 0 ? ` · ${b.kpi.vaccines_due} due` : ""}
              </span>
            </TableCell>
            <TableCell className="text-sm">
              {SPECIES_LABEL[b.species]} · {PURPOSE_LABEL[b.purpose] ?? b.purpose}
              {b.status === "closed" && <span className="block text-xs text-muted-foreground">Closed {b.closed_on ? shortDate(b.closed_on) : ""}</span>}
            </TableCell>
            <TableCell className="tabular text-right">{b.kpi.age_days} d</TableCell>
            <TableCell className="tabular text-right">
              {b.kpi.alive.toLocaleString("en-IN")}
              <span className="block text-xs text-muted-foreground">of {b.initial_count.toLocaleString("en-IN")}</span>
            </TableCell>
            <TableCell className="tabular text-right">{b.kpi.mortality_pct}%</TableCell>
            <TableCell className="tabular text-right">
              {b.kpi.latest_avg_weight_kg != null ? `${b.kpi.latest_avg_weight_kg} kg` : "—"}
              <span className="block text-xs text-muted-foreground">{b.kpi.fcr != null ? `FCR ${b.kpi.fcr}` : ""}</span>
            </TableCell>
            <TableCell className="tabular text-right">
              {b.purpose === "layer" || b.purpose === "breeder" ? b.kpi.eggs_today.toLocaleString("en-IN") : "—"}
              <span className="block text-xs text-muted-foreground">{b.kpi.laying_7d_pct != null ? `${b.kpi.laying_7d_pct}% laying` : ""}</span>
            </TableCell>
            <TableCell className="tabular text-right">{b.kpi.cost_per_head != null ? rupees(b.kpi.cost_per_head) : "—"}</TableCell>
          </TableRow>
        ))}
        {batches.length === 0 && (
          <TableRow>
            <TableCell colSpan={8} className="py-6 text-center text-sm text-muted-foreground">
              No batches.
            </TableCell>
          </TableRow>
        )}
      </TableBody>
    </Table>
  );
}
