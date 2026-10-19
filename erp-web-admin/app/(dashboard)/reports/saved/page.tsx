"use client";

// Saved and generated reports (erp-core-go reportbuilder library.go). Saved
// reports are the ones kept from the Report Library (with their filters and
// period) or built in the Report Builder, the caller's own and everyone's
// shared ones: run any again, for its own period or other dates, print it,
// or export it. Generated reports are the caller's last 50 exports: download
// one again, or regenerate it, with the same dates, for its period as of
// today, or other dates.
import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { Download, FileSpreadsheet, Loader2, Play, Printer, RefreshCw, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { dateTime } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Modal } from "@/components/ui/dialog";
import { PageHeader } from "@/components/data/page-header";
import { StatusChip } from "@/components/data/status-chip";
import { ReportResult, ReportTable, Run, downloadRun } from "@/components/reports/report-result";

type Saved = {
  template_id: string;
  name: string;
  description: string | null;
  spec: { dataset: string; date_from?: string; date_to?: string; period?: string };
  shared: boolean;
  mine: boolean;
  created_by_name: string;
  updated_at: string;
  library_key: string | null;
};
type Period = { key: string; label: string };
const selectClass = "h-9 rounded-lg border bg-card px-2 text-sm";

export default function SavedReportsPage() {
  const [saved, setSaved] = useState<Saved[]>([]);
  const [runs, setRuns] = useState<Run[]>([]);
  const [periods, setPeriods] = useState<Period[]>([]);
  const [names, setNames] = useState<Record<string, string>>({});
  const [open, setOpen] = useState<Saved | null>(null);
  const [period, setPeriod] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [result, setResult] = useState<ReportResult | null>(null);
  const [running, setRunning] = useState(false);
  const [regen, setRegen] = useState<Run | null>(null);
  const [error, setError] = useState<string | null>(null);

  const loadSaved = useCallback(() => api.get<{ templates: Saved[] }>("/api/v1/reports/saved").then((d) => setSaved(d.templates)), []);
  const loadRuns = useCallback(() => api.get<{ runs: Run[] }>("/api/v1/reports/runs").then((d) => setRuns(d.runs)), []);
  useEffect(() => {
    loadSaved().catch((e) => setError(e instanceof ApiError ? e.message : "Could not load saved reports"));
    loadRuns().catch(() => {});
    api
      .get<{ reports: { key: string; name: string }[]; periods: Period[] }>("/api/v1/reports/library")
      .then((d) => {
        setPeriods(d.periods);
        setNames(Object.fromEntries(d.reports.map((r) => [r.key, r.name])));
      })
      .catch(() => {});
  }, [loadSaved, loadRuns]);
  // Poll while an export is still being produced.
  useEffect(() => {
    if (!runs.some((r) => r.status === "queued" || r.status === "running")) return;
    const t = setInterval(() => loadRuns().catch(() => {}), 2000);
    return () => clearInterval(t);
  }, [runs, loadRuns]);

  const periodLabel = (k?: string | null) => periods.find((p) => p.key === k)?.label ?? k ?? "";
  const covers = (s: Saved["spec"]) => (s.period ? periodLabel(s.period) : s.date_from ? `${s.date_from} to ${s.date_to}` : "Now");
  const body = () => (period === "custom" ? { date_from: from, date_to: to } : period ? { period } : {});

  async function runSaved(t: Saved, b = body()) {
    setRunning(true);
    setError(null);
    try {
      setResult(await api.post<ReportResult>(`/api/v1/reports/saved/${t.template_id}/run`, b));
    } catch (e) {
      setResult(null);
      setError(e instanceof ApiError ? e.message : "Could not run the report");
    } finally {
      setRunning(false);
    }
  }
  function openSaved(t: Saved) {
    setOpen(t);
    setPeriod("");
    setFrom("");
    setTo("");
    setResult(null);
    void runSaved(t, {});
  }
  async function exportSaved(t: Saved, format: "csv" | "xlsx") {
    try {
      await api.post(`/api/v1/reports/saved/${t.template_id}/export`, { format, ...(open?.template_id === t.template_id ? body() : {}) });
      toast.success("Export started: it appears under Generated reports");
      loadRuns();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not start the export");
    }
  }
  async function remove(t: Saved) {
    if (!window.confirm(`Delete the saved report “${t.name}”? Its generated files stay under Generated reports.`)) return;
    try {
      await api.delete(`/api/v1/reports/saved/${t.template_id}`);
      toast.success(`Deleted “${t.name}”`);
      if (open?.template_id === t.template_id) setOpen(null);
      loadSaved();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not delete");
    }
  }

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Saved Reports"
        description="Reports kept to run again, and the files you've generated. Regenerate any of them for the same dates, for today's period, or other dates."
        actions={
          <Link href="/reports/library" className="text-sm text-primary hover:underline">
            Report Library →
          </Link>
        }
      />
      {error && (
        <p className="text-sm text-destructive" role="alert">
          {error}
        </p>
      )}

      <section className="rounded-xl border bg-card print:hidden" aria-labelledby="saved-h">
        <h2 id="saved-h" className="border-b px-4 py-2 text-sm font-semibold">
          Saved reports
        </h2>
        <div className="overflow-x-auto">
          <table className="w-full min-w-[760px] text-sm">
            <thead className="text-left text-xs text-muted-foreground">
              <tr>
                <th className="px-4 py-2 font-medium">Name</th>
                <th className="px-4 py-2 font-medium">Report</th>
                <th className="px-4 py-2 font-medium">Covers</th>
                <th className="px-4 py-2 font-medium">Saved by</th>
                <th className="px-4 py-2" />
              </tr>
            </thead>
            <tbody>
              {saved.map((t) => (
                <tr key={t.template_id} className={`border-t ${open?.template_id === t.template_id ? "bg-accent/40" : ""}`}>
                  <td className="px-4 py-2 font-medium">
                    {t.name}
                    {t.shared && <StatusChip status="active" tone="info" label="Shared" className="ml-2" />}
                    {t.description && <span className="block text-xs font-normal text-muted-foreground">{t.description}</span>}
                  </td>
                  <td className="px-4 py-2 text-muted-foreground">{t.library_key ? (names[t.library_key] ?? t.library_key) : "Custom (Report Builder)"}</td>
                  <td className="px-4 py-2">{covers(t.spec)}</td>
                  <td className="px-4 py-2 text-muted-foreground">{t.mine ? "You" : t.created_by_name}</td>
                  <td className="px-4 py-2">
                    <div className="flex justify-end gap-1">
                      <Button size="sm" onClick={() => openSaved(t)} aria-label={`Run ${t.name}`}>
                        <Play /> Run
                      </Button>
                      <Button size="sm" variant="outline" onClick={() => exportSaved(t, "xlsx")} aria-label={`Export ${t.name} to Excel`}>
                        <FileSpreadsheet />
                      </Button>
                      <Button size="sm" variant="outline" onClick={() => exportSaved(t, "csv")} aria-label={`Export ${t.name} to CSV`}>
                        <Download />
                      </Button>
                      {t.mine && (
                        <Button size="sm" variant="ghost" onClick={() => remove(t)} aria-label={`Delete ${t.name}`}>
                          <Trash2 />
                        </Button>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
              {saved.length === 0 && (
                <tr>
                  <td colSpan={5} className="px-4 py-8 text-center text-muted-foreground">
                    Nothing saved yet. Open a report in the{" "}
                    <Link href="/reports/library" className="text-primary hover:underline">
                      Report Library
                    </Link>{" "}
                    and choose “Save to my reports”.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>

      {open && (
        <section className="rounded-xl border bg-card print:border-0" aria-label={`${open.name} results`}>
          <div className="flex flex-wrap items-end gap-3 border-b px-4 py-3 print:hidden">
            <h2 className="mr-auto text-base font-semibold">{open.name}</h2>
            <div className="flex flex-col gap-1">
              <Label htmlFor="sv-period">Run for</Label>
              <select id="sv-period" value={period} onChange={(e) => setPeriod(e.target.value)} className={`${selectClass} w-48`}>
                <option value="">Its own: {covers(open.spec)}</option>
                {periods.map((p) => (
                  <option key={p.key} value={p.key}>
                    {p.label}
                  </option>
                ))}
                <option value="custom">Other dates</option>
              </select>
            </div>
            {period === "custom" && (
              <>
                <div className="flex flex-col gap-1">
                  <Label htmlFor="sv-from">From</Label>
                  <Input id="sv-from" type="date" value={from} onChange={(e) => setFrom(e.target.value)} className="w-40" />
                </div>
                <div className="flex flex-col gap-1">
                  <Label htmlFor="sv-to">To</Label>
                  <Input id="sv-to" type="date" value={to} onChange={(e) => setTo(e.target.value)} className="w-40" />
                </div>
              </>
            )}
            <Button onClick={() => runSaved(open)} disabled={running || (period === "custom" && (!from || !to))}>
              {running ? <Loader2 className="animate-spin" /> : <Play />} Run
            </Button>
            <Button variant="outline" onClick={() => window.print()} disabled={!result}>
              <Printer /> Print / PDF
            </Button>
          </div>
          {result ? (
            <>
              <p className="px-4 pt-2 text-sm text-muted-foreground print:hidden">
                {result.rows.length} row{result.rows.length === 1 ? "" : "s"}
                {result.date_from && ` · ${result.date_from} to ${result.date_to}`}
              </p>
              <ReportTable result={result} title={open.name} totalLabel={open.library_key?.startsWith("profit_loss") ? "Profit (loss when negative)" : "Total"} />
            </>
          ) : (
            running && <p className="px-4 py-6 text-sm text-muted-foreground">Running…</p>
          )}
        </section>
      )}

      <section className="rounded-xl border bg-card print:hidden" aria-labelledby="runs-h">
        <div className="flex items-center justify-between border-b px-4 py-2">
          <h2 id="runs-h" className="text-sm font-semibold">
            Generated reports
          </h2>
          <Button size="sm" variant="ghost" onClick={() => loadRuns()} aria-label="Refresh generated reports">
            <RefreshCw />
          </Button>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full min-w-[760px] text-sm">
            <thead className="text-left text-xs text-muted-foreground">
              <tr>
                <th className="px-4 py-2 font-medium">Report</th>
                <th className="px-4 py-2 font-medium">Covers</th>
                <th className="px-4 py-2 font-medium">Made</th>
                <th className="px-4 py-2 font-medium">Status</th>
                <th className="px-4 py-2" />
              </tr>
            </thead>
            <tbody>
              {runs.map((r) => (
                <tr key={r.run_id} className="border-t">
                  <td className="px-4 py-2">
                    {r.name} <span className="text-xs uppercase text-muted-foreground">{r.format}</span>
                  </td>
                  <td className="px-4 py-2">
                    {r.date_from ? `${r.date_from} to ${r.date_to}` : "At the time"}
                    {r.period && <span className="block text-xs text-muted-foreground">{periodLabel(r.period)}</span>}
                  </td>
                  <td className="px-4 py-2 text-muted-foreground">{dateTime(r.created_at)}</td>
                  <td className="px-4 py-2">
                    {r.status === "done" ? (
                      <span>
                        {r.row_count} row{r.row_count === 1 ? "" : "s"}
                        {r.truncated && " (cut at 100,000)"}
                      </span>
                    ) : r.status === "failed" ? (
                      <span className="text-destructive">{r.error ?? "Failed"}</span>
                    ) : (
                      <span className="inline-flex items-center gap-1 text-muted-foreground">
                        <Loader2 className="size-3.5 animate-spin" /> {r.status === "queued" ? "Waiting" : "Producing"}
                      </span>
                    )}
                  </td>
                  <td className="px-4 py-2">
                    <div className="flex justify-end gap-1">
                      <Button size="sm" variant="outline" disabled={r.status !== "done"} onClick={() => downloadRun(r)} aria-label={`Download ${r.name}`}>
                        <Download /> Download
                      </Button>
                      <Button size="sm" variant="outline" disabled={r.status === "queued" || r.status === "running"} onClick={() => setRegen(r)} aria-label={`Regenerate ${r.name}`}>
                        <RefreshCw /> Regenerate
                      </Button>
                    </div>
                  </td>
                </tr>
              ))}
              {runs.length === 0 && (
                <tr>
                  <td colSpan={5} className="px-4 py-8 text-center text-muted-foreground">
                    Exports you make appear here.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>

      {regen && <RegenerateDialog run={regen} periodLabel={periodLabel(regen.period)} onClose={() => setRegen(null)} onDone={() => (setRegen(null), loadRuns())} />}
    </div>
  );
}

function RegenerateDialog({ run, periodLabel, onClose, onDone }: { run: Run; periodLabel: string; onClose: () => void; onDone: () => void }) {
  const [mode, setMode] = useState<"same" | "refresh" | "dates">("same");
  const [from, setFrom] = useState(run.date_from ?? "");
  const [to, setTo] = useState(run.date_to ?? "");
  const [format, setFormat] = useState(run.format);
  const [busy, setBusy] = useState(false);
  async function go() {
    setBusy(true);
    try {
      await api.post(`/api/v1/reports/runs/${run.run_id}/regenerate`, {
        format,
        ...(mode === "refresh" ? { refresh: true } : mode === "dates" ? { date_from: from, date_to: to } : {}),
      });
      toast.success(`Regenerating “${run.name}”`);
      onDone();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not regenerate");
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={`Regenerate “${run.name}”`}
      description="The report is run again from today's data: anything recorded since appears in it."
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={busy || (mode === "dates" && (!from || !to))} onClick={go}>
            <RefreshCw /> Regenerate
          </Button>
        </>
      }
    >
      <fieldset className="space-y-2 text-sm">
        <legend className="sr-only">Dates</legend>
        <label className="flex items-center gap-2">
          <input type="radio" name="rg" checked={mode === "same"} onChange={() => setMode("same")} />
          The same dates{run.date_from ? ` (${run.date_from} to ${run.date_to})` : ""}
        </label>
        {run.period && (
          <label className="flex items-center gap-2">
            <input type="radio" name="rg" checked={mode === "refresh"} onChange={() => setMode("refresh")} />
            {periodLabel}, as of today
          </label>
        )}
        {run.date_from && (
          <label className="flex items-center gap-2">
            <input type="radio" name="rg" checked={mode === "dates"} onChange={() => setMode("dates")} />
            Other dates
          </label>
        )}
        {mode === "dates" && (
          <div className="flex gap-2 pl-6">
            <Input type="date" aria-label="From" value={from} onChange={(e) => setFrom(e.target.value)} className="w-40" />
            <Input type="date" aria-label="To" value={to} onChange={(e) => setTo(e.target.value)} className="w-40" />
          </div>
        )}
        <div className="flex items-center gap-2 pt-2">
          <Label htmlFor="rg-format">File</Label>
          <select id="rg-format" value={format} onChange={(e) => setFormat(e.target.value as "csv" | "xlsx")} className={`${selectClass} w-32`}>
            <option value="xlsx">Excel</option>
            <option value="csv">CSV</option>
          </select>
        </div>
      </fieldset>
    </Modal>
  );
}
