"use client";

// A report's rows (erp-core-go reportbuilder Result), with a totals row for
// summed and counted columns, and a print header naming the report and its
// dates. Shared by the report library and saved reports.
import { api, ApiError } from "@/lib/api-client";
import { rupees } from "@/lib/format";
import { toast } from "sonner";

export type ReportColumn = { key: string; label: string; type: "text" | "number" | "money" | "date" | "bool"; agg?: string };
export type ReportResult = { columns: ReportColumn[]; rows: (string | null)[][]; truncated: boolean; date_from?: string; date_to?: string };

export type Run = {
  run_id: string;
  template_id: string | null;
  name: string;
  format: "csv" | "xlsx";
  status: "queued" | "running" | "done" | "failed";
  row_count: number | null;
  truncated: boolean;
  error: string | null;
  created_at: string;
  finished_at: string | null;
  library_key: string | null;
  date_from: string | null;
  date_to: string | null;
  period: string | null;
};

export function formatCell(v: string | null, type: string) {
  if (v == null) return "—";
  if (type === "money") return rupees(v);
  if (type === "bool") return v === "true" ? "Yes" : "No";
  if (type === "number") return Number(v).toLocaleString("en-IN", { maximumFractionDigits: 3 });
  return v;
}

const numeric = (t: string) => t === "money" || t === "number";

export function ReportTable({ result, title, totalLabel = "Total" }: { result: ReportResult; title: string; totalLabel?: string }) {
  // Totals only for columns that add up: sums and counts (not averages,
  // highs or lows), and only when the report groups rows.
  const grouped = result.columns.some((c) => c.agg);
  const totals = result.columns.map((c, i) =>
    grouped && (c.agg === "sum" || c.agg === "count") ? result.rows.reduce((s, r) => s + (r[i] == null ? 0 : Number(r[i])), 0) : null,
  );
  const hasTotals = result.rows.length > 1 && totals.some((t) => t != null);
  const firstText = result.columns.findIndex((c) => !numeric(c.type));
  return (
    <div>
      <h2 className="hidden px-4 pt-4 text-lg font-semibold print:block">
        {title}
        {result.date_from && ` · ${result.date_from} to ${result.date_to}`}
      </h2>
      <div className="overflow-x-auto">
        <table className="w-full text-sm" data-testid="report-table">
          <thead className="text-left text-xs text-muted-foreground">
            <tr>
              {result.columns.map((c, i) => (
                <th key={i} scope="col" className={`px-4 py-2 font-medium ${numeric(c.type) ? "text-right" : ""}`}>
                  {c.label}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {result.rows.map((row, ri) => (
              <tr key={ri} className="border-t">
                {row.map((v, ci) => {
                  const t = result.columns[ci].type;
                  return (
                    <td key={ci} className={`px-4 py-1.5 ${numeric(t) ? "tabular text-right" : ""} ${t === "money" && v != null && Number(v) < 0 ? "text-destructive" : ""}`}>
                      {formatCell(v, t)}
                    </td>
                  );
                })}
              </tr>
            ))}
            {result.rows.length === 0 && (
              <tr>
                <td colSpan={result.columns.length} className="px-4 py-8 text-center text-muted-foreground">
                  Nothing in these dates and filters.
                </td>
              </tr>
            )}
          </tbody>
          {hasTotals && (
            <tfoot>
              <tr className="border-t-2 font-semibold" data-testid="report-total">
                {result.columns.map((c, i) => (
                  <td key={i} className={`px-4 py-2 ${numeric(c.type) ? "tabular text-right" : ""}`}>
                    {totals[i] != null ? (c.type === "money" ? rupees(totals[i]!) : totals[i]!.toLocaleString("en-IN", { maximumFractionDigits: 3 })) : i === Math.max(firstText, 0) ? totalLabel : ""}
                  </td>
                ))}
              </tr>
            </tfoot>
          )}
        </table>
      </div>
      {result.truncated && <p className="px-4 py-2 text-xs text-muted-foreground print:hidden">The first 1,000 rows are shown (and totalled). Export to get them all.</p>}
    </div>
  );
}

export async function downloadRun(r: Run) {
  try {
    const { blob, filename } = await api.download(`/api/v1/reports/runs/${r.run_id}/download`);
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = filename;
    a.click();
    URL.revokeObjectURL(url);
  } catch (e) {
    toast.error(e instanceof ApiError ? e.message : "Could not download");
  }
}
