"use client";

// Custom report builder (Phase 16, erp-core-go internal/reportbuilder; FRD
// §15 "Custom Builder"). Pick a dataset, columns (with totals, averages or
// counts), filters, sort and dates; run it here (first 1,000 rows), save it
// as a template (optionally shared), or export it to CSV / Excel in the
// background. Printing the preview gives a PDF. The server only accepts
// fields from its allow-list and always applies the person's own branch
// scope, so a shared template shows each person their own branches.
import { useCallback, useEffect, useMemo, useState } from "react";
import { ArrowDown, ArrowUp, Copy, Download, FileSpreadsheet, Loader2, Play, Plus, Printer, Save, Trash2, X } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { rupees } from "@/lib/format";
import { usePermissions } from "@/lib/permissions";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Modal } from "@/components/ui/dialog";
import { PageHeader } from "@/components/data/page-header";
import { StatusChip } from "@/components/data/status-chip";

type FieldInfo = { key: string; label: string; type: "text" | "number" | "money" | "date" | "bool"; aggs: string[]; ops: string[] };
type DatasetInfo = { key: string; label: string; description: string; has_date_range: boolean; fields: FieldInfo[] };
type Column = { field: string; agg?: string };
type Filter = { field: string; op: string; value?: unknown };
type Spec = {
  dataset: string;
  columns: Column[];
  filters: Filter[];
  sort: { column: number; desc: boolean }[];
  date_from?: string;
  date_to?: string;
};
type Template = { template_id: string; name: string; description: string | null; spec: Spec; shared: boolean; mine: boolean; created_by_name: string };
type Result = { columns: { key: string; label: string; type: string; agg?: string }[]; rows: (string | null)[][]; truncated: boolean };
type Run = { run_id: string; name: string; format: "csv" | "xlsx"; status: "queued" | "running" | "done" | "failed"; row_count: number | null; truncated: boolean; error: string | null; created_at: string };

const AGG_LABEL: Record<string, string> = { sum: "Total", avg: "Average", min: "Lowest", max: "Highest", count: "Count", count_distinct: "Distinct count" };
const OP_LABEL: Record<string, string> = {
  eq: "is",
  ne: "is not",
  in: "is one of",
  not_in: "is none of",
  contains: "contains",
  empty: "is empty",
  not_empty: "is not empty",
  gt: ">",
  gte: "≥",
  lt: "<",
  lte: "≤",
  between: "between",
  is_true: "is yes",
  is_false: "is no",
};
const NO_VALUE = ["empty", "not_empty", "is_true", "is_false"];

const iso = (d: Date) => new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0, 10);
function presetRange(p: "this_month" | "last_30" | "last_month"): [string, string] {
  const now = new Date();
  if (p === "this_month") return [iso(new Date(now.getFullYear(), now.getMonth(), 1)), iso(now)];
  if (p === "last_month") return [iso(new Date(now.getFullYear(), now.getMonth() - 1, 1)), iso(new Date(now.getFullYear(), now.getMonth(), 0))];
  return [iso(new Date(now.getTime() - 29 * 86400000)), iso(now)];
}

function blankSpec(ds: DatasetInfo): Spec {
  const [from, to] = presetRange("this_month");
  return { dataset: ds.key, columns: [{ field: ds.fields[0].key }], filters: [], sort: [], ...(ds.has_date_range ? { date_from: from, date_to: to } : {}) };
}

function cell(v: string | null, type: string) {
  if (v == null) return "—";
  if (type === "money") return rupees(v);
  if (type === "bool") return v === "true" ? "Yes" : "No";
  if (type === "number") return Number(v).toLocaleString("en-IN", { maximumFractionDigits: 3 });
  return v;
}

export default function ReportBuilderPage() {
  const { can } = usePermissions();
  const builder = can("reports.build");
  const [datasets, setDatasets] = useState<DatasetInfo[]>([]);
  const [templates, setTemplates] = useState<Template[]>([]);
  const [current, setCurrent] = useState<Template | null>(null);
  const [spec, setSpec] = useState<Spec | null>(null);
  const [result, setResult] = useState<Result | null>(null);
  const [running, setRunning] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saveOpen, setSaveOpen] = useState<null | "save" | "save_as">(null);
  const [saveName, setSaveName] = useState("");
  const [saveShared, setSaveShared] = useState(false);
  const [runs, setRuns] = useState<Run[]>([]);

  const ds = useMemo(() => datasets.find((d) => d.key === spec?.dataset), [datasets, spec?.dataset]);
  const field = (k: string) => ds?.fields.find((f) => f.key === k);

  const loadTemplates = useCallback(() => api.get<{ templates: Template[] }>("/api/v1/report-builder/templates").then((d) => setTemplates(d.templates)), []);
  const loadRuns = useCallback(() => api.get<{ runs: Run[] }>("/api/v1/report-builder/runs").then((d) => setRuns(d.runs)), []);

  useEffect(() => {
    api
      .get<{ datasets: DatasetInfo[] }>("/api/v1/report-builder/datasets")
      .then((d) => {
        setDatasets(d.datasets);
        setSpec((s) => s ?? blankSpec(d.datasets[0]));
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load the report builder"));
    loadTemplates().catch(() => {});
    loadRuns().catch(() => {});
  }, [loadTemplates, loadRuns]);

  // Poll while an export is still being produced.
  useEffect(() => {
    if (!runs.some((r) => r.status === "queued" || r.status === "running")) return;
    const t = setInterval(() => loadRuns().catch(() => {}), 2000);
    return () => clearInterval(t);
  }, [runs, loadRuns]);

  const update = (patch: Partial<Spec>) => {
    setSpec((s) => (s ? { ...s, ...patch } : s));
    setResult(null);
  };

  async function run() {
    if (!spec) return;
    setRunning(true);
    setError(null);
    try {
      const r =
        builder || !current
          ? await api.post<Result>("/api/v1/report-builder/query", { spec })
          : await api.post<Result>(`/api/v1/report-builder/templates/${current.template_id}/run`, { date_from: spec.date_from, date_to: spec.date_to });
      setResult(r);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not run the report");
      setResult(null);
    } finally {
      setRunning(false);
    }
  }

  async function exportAs(format: "csv" | "xlsx") {
    if (!spec) return;
    try {
      const body =
        current && !builder
          ? { template_id: current.template_id, format, date_from: spec.date_from, date_to: spec.date_to }
          : { spec, format, name: current?.name ?? ds?.label ?? "Report" };
      await api.post("/api/v1/report-builder/runs", body);
      toast.success("Export started: it appears under Exports when ready");
      loadRuns();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not start the export");
    }
  }

  async function downloadRun(r: Run) {
    try {
      const { blob, filename } = await api.download(`/api/v1/report-builder/runs/${r.run_id}/download`);
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

  async function save() {
    if (!spec) return;
    try {
      const body = { name: saveName, spec, shared: saveShared, description: current?.description ?? "" };
      const t =
        saveOpen === "save" && current?.mine
          ? await api.put<Template>(`/api/v1/report-builder/templates/${current.template_id}`, body)
          : await api.post<Template>("/api/v1/report-builder/templates", body);
      setCurrent(t);
      setSaveOpen(null);
      toast.success(`Saved "${t.name}"`);
      loadTemplates();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not save");
    }
  }

  async function duplicate() {
    if (!current) return;
    try {
      const t = await api.post<Template>(`/api/v1/report-builder/templates/${current.template_id}/duplicate`, {});
      open(t);
      loadTemplates();
      toast.success(`Made "${t.name}"`);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not duplicate");
    }
  }

  async function remove() {
    if (!current || !window.confirm(`Delete "${current.name}"?`)) return;
    try {
      await api.delete(`/api/v1/report-builder/templates/${current.template_id}`);
      setCurrent(null);
      if (datasets[0]) setSpec(blankSpec(datasets[0]));
      setResult(null);
      loadTemplates();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not delete");
    }
  }

  function open(t: Template) {
    setCurrent(t);
    const [from, to] = presetRange("this_month");
    setSpec({ ...t.spec, filters: t.spec.filters ?? [], sort: t.spec.sort ?? [], date_from: t.spec.date_from || from, date_to: t.spec.date_to || to });
    setResult(null);
  }

  if (error && !spec) return <p className="text-sm text-destructive">{error}</p>;
  if (!spec || !ds) {
    return (
      <p className="flex items-center gap-2 text-sm text-muted-foreground">
        <Loader2 className="size-4 animate-spin" aria-hidden /> Loading…
      </p>
    );
  }

  const editable = builder;
  const moveCol = (i: number, d: -1 | 1) => {
    const cols = [...spec.columns];
    [cols[i], cols[i + d]] = [cols[i + d], cols[i]];
    update({ columns: cols, sort: [] });
  };

  return (
    <div className="space-y-5">
      <PageHeader
        title="Report Builder"
        description="Build your own reports from sales, items, payments, stock, products and the ledger. You only ever see your own branches' data."
        actions={
          <>
            {editable && (
              <Button
                variant="outline"
                onClick={() => {
                  setCurrent(null);
                  setSpec(blankSpec(ds));
                  setResult(null);
                }}
              >
                <Plus aria-hidden /> New report
              </Button>
            )}
          </>
        }
      />

      <div className="grid gap-5 lg:grid-cols-[16rem_minmax(0,1fr)]">
        <aside className="space-y-4 print:hidden">
          <section aria-labelledby="tpl-h" className="rounded-xl border bg-card p-3">
            <h2 id="tpl-h" className="mb-2 text-sm font-semibold">
              Saved reports
            </h2>
            <ul className="space-y-0.5">
              {templates.map((t) => (
                <li key={t.template_id}>
                  <button
                    type="button"
                    onClick={() => open(t)}
                    aria-current={current?.template_id === t.template_id ? "true" : undefined}
                    className={`w-full rounded-md px-2 py-1.5 text-left text-sm hover:bg-muted ${current?.template_id === t.template_id ? "bg-muted font-medium" : ""}`}
                  >
                    {t.name}
                    <span className="block text-xs text-muted-foreground">{t.mine ? (t.shared ? "Mine · shared" : "Mine") : `Shared by ${t.created_by_name}`}</span>
                  </button>
                </li>
              ))}
              {templates.length === 0 && <li className="px-2 text-xs text-muted-foreground">None yet.</li>}
            </ul>
          </section>
          <section aria-labelledby="runs-h" className="rounded-xl border bg-card p-3">
            <h2 id="runs-h" className="mb-2 text-sm font-semibold">
              Exports
            </h2>
            <ul className="space-y-2">
              {runs.slice(0, 8).map((r) => (
                <li key={r.run_id} className="text-xs">
                  <div className="flex items-center gap-1.5">
                    <span className="flex-1 truncate font-medium" title={r.name}>
                      {r.name}.{r.format}
                    </span>
                    {r.status === "done" ? (
                      <Button size="icon-xs" variant="ghost" aria-label={`Download ${r.name}.${r.format}`} onClick={() => downloadRun(r)}>
                        <Download aria-hidden />
                      </Button>
                    ) : (
                      <StatusChip status={r.status} tone={r.status === "failed" ? "bad" : "info"} label={r.status === "failed" ? "Failed" : "Working…"} />
                    )}
                  </div>
                  <span className="text-muted-foreground">
                    {r.status === "done" ? `${r.row_count} rows${r.truncated ? " (first 100,000)" : ""}` : (r.error ?? new Date(r.created_at).toLocaleTimeString("en-IN"))}
                  </span>
                </li>
              ))}
              {runs.length === 0 && <li className="text-xs text-muted-foreground">Exports you start appear here.</li>}
            </ul>
          </section>
        </aside>

        <div className="min-w-0 space-y-4">
          <section aria-label="Report definition" className="space-y-4 rounded-xl border bg-card p-4 print:hidden">
            {current && (
              <p className="text-sm">
                <span className="font-semibold">{current.name}</span>{" "}
                <span className="text-muted-foreground">{current.mine ? "" : `· shared by ${current.created_by_name} (read-only; duplicate it to change it)`}</span>
              </p>
            )}
            <div className="flex flex-wrap items-end gap-3">
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="rb-dataset">Report on</Label>
                <select
                  id="rb-dataset"
                  disabled={!editable}
                  value={spec.dataset}
                  onChange={(e) => {
                    const next = datasets.find((d) => d.key === e.target.value)!;
                    setSpec(blankSpec(next));
                    setResult(null);
                  }}
                  className="h-9 rounded-lg border bg-card px-2 text-sm"
                >
                  {datasets.map((d) => (
                    <option key={d.key} value={d.key}>
                      {d.label}
                    </option>
                  ))}
                </select>
              </div>
              {ds.has_date_range && (
                <>
                  <div className="flex flex-col gap-1.5">
                    <Label htmlFor="rb-from">From</Label>
                    <Input id="rb-from" type="date" className="w-40" value={spec.date_from ?? ""} onChange={(e) => update({ date_from: e.target.value })} />
                  </div>
                  <div className="flex flex-col gap-1.5">
                    <Label htmlFor="rb-to">To</Label>
                    <Input id="rb-to" type="date" className="w-40" value={spec.date_to ?? ""} onChange={(e) => update({ date_to: e.target.value })} />
                  </div>
                  <div className="flex gap-1">
                    {(
                      [
                        ["this_month", "This month"],
                        ["last_month", "Last month"],
                        ["last_30", "Last 30 days"],
                      ] as const
                    ).map(([k, l]) => (
                      <Button
                        key={k}
                        variant="ghost"
                        size="sm"
                        onClick={() => {
                          const [f, t] = presetRange(k);
                          update({ date_from: f, date_to: t });
                        }}
                      >
                        {l}
                      </Button>
                    ))}
                  </div>
                </>
              )}
            </div>
            <p className="text-xs text-muted-foreground">{ds.description}</p>

            <fieldset className="space-y-2" disabled={!editable}>
              <legend className="text-sm font-medium">Columns</legend>
              {spec.columns.map((c, i) => {
                const f = field(c.field);
                return (
                  <div key={i} className="flex flex-wrap items-center gap-2">
                    <select
                      aria-label={`Column ${i + 1} field`}
                      value={c.field}
                      onChange={(e) => {
                        const cols = [...spec.columns];
                        cols[i] = { field: e.target.value };
                        update({ columns: cols });
                      }}
                      className="h-8 rounded-md border bg-card px-1.5 text-sm"
                    >
                      {ds.fields.map((x) => (
                        <option key={x.key} value={x.key}>
                          {x.label}
                        </option>
                      ))}
                    </select>
                    <select
                      aria-label={`Column ${i + 1} summary`}
                      value={c.agg ?? ""}
                      onChange={(e) => {
                        const cols = [...spec.columns];
                        cols[i] = { field: c.field, ...(e.target.value ? { agg: e.target.value } : {}) };
                        update({ columns: cols });
                      }}
                      className="h-8 rounded-md border bg-card px-1.5 text-sm"
                    >
                      <option value="">Each value (group by)</option>
                      {f?.aggs.map((a) => (
                        <option key={a} value={a}>
                          {AGG_LABEL[a]}
                        </option>
                      ))}
                    </select>
                    <Button size="icon-sm" variant="ghost" aria-label={`Move column ${i + 1} up`} disabled={i === 0} onClick={() => moveCol(i, -1)}>
                      <ArrowUp aria-hidden />
                    </Button>
                    <Button
                      size="icon-sm"
                      variant="ghost"
                      aria-label={`Move column ${i + 1} down`}
                      disabled={i === spec.columns.length - 1}
                      onClick={() => moveCol(i, 1)}
                    >
                      <ArrowDown aria-hidden />
                    </Button>
                    <Button
                      size="icon-sm"
                      variant="ghost"
                      aria-label={`Remove column ${i + 1}`}
                      disabled={spec.columns.length === 1}
                      onClick={() => update({ columns: spec.columns.filter((_, j) => j !== i), sort: [] })}
                    >
                      <X aria-hidden />
                    </Button>
                  </div>
                );
              })}
              <Button size="sm" variant="outline" disabled={spec.columns.length >= 20} onClick={() => update({ columns: [...spec.columns, { field: ds.fields[0].key }] })}>
                <Plus aria-hidden /> Add column
              </Button>
            </fieldset>

            <fieldset className="space-y-2" disabled={!editable}>
              <legend className="text-sm font-medium">Filters</legend>
              {spec.filters.map((fl, i) => {
                const f = field(fl.field);
                const setF = (patch: Partial<Filter>) => {
                  const fs = [...spec.filters];
                  fs[i] = { ...fs[i], ...patch };
                  update({ filters: fs });
                };
                const inputType = f?.type === "date" ? "date" : f?.type === "number" || f?.type === "money" ? "number" : "text";
                return (
                  <div key={i} className="flex flex-wrap items-center gap-2">
                    <select
                      aria-label={`Filter ${i + 1} field`}
                      value={fl.field}
                      onChange={(e) => {
                        const nf = field(e.target.value)!;
                        setF({ field: nf.key, op: nf.ops[0], value: undefined });
                      }}
                      className="h-8 rounded-md border bg-card px-1.5 text-sm"
                    >
                      {ds.fields.map((x) => (
                        <option key={x.key} value={x.key}>
                          {x.label}
                        </option>
                      ))}
                    </select>
                    <select
                      aria-label={`Filter ${i + 1} condition`}
                      value={fl.op}
                      onChange={(e) => setF({ op: e.target.value, value: undefined })}
                      className="h-8 rounded-md border bg-card px-1.5 text-sm"
                    >
                      {f?.ops.map((o) => (
                        <option key={o} value={o}>
                          {OP_LABEL[o]}
                        </option>
                      ))}
                    </select>
                    {!NO_VALUE.includes(fl.op) &&
                      (fl.op === "between" ? (
                        <>
                          <Input
                            aria-label={`Filter ${i + 1} from`}
                            type={inputType}
                            className="h-8 w-32"
                            value={String((fl.value as unknown[] | undefined)?.[0] ?? "")}
                            onChange={(e) => setF({ value: [e.target.value, (fl.value as unknown[] | undefined)?.[1] ?? ""] })}
                          />
                          <span className="text-sm">and</span>
                          <Input
                            aria-label={`Filter ${i + 1} to`}
                            type={inputType}
                            className="h-8 w-32"
                            value={String((fl.value as unknown[] | undefined)?.[1] ?? "")}
                            onChange={(e) => setF({ value: [(fl.value as unknown[] | undefined)?.[0] ?? "", e.target.value] })}
                          />
                        </>
                      ) : (
                        <Input
                          aria-label={`Filter ${i + 1} value`}
                          type={fl.op === "in" || fl.op === "not_in" ? "text" : inputType}
                          placeholder={fl.op === "in" || fl.op === "not_in" ? "Comma-separated" : ""}
                          className="h-8 w-48"
                          value={Array.isArray(fl.value) ? fl.value.join(", ") : String(fl.value ?? "")}
                          onChange={(e) =>
                            setF({
                              value:
                                fl.op === "in" || fl.op === "not_in"
                                  ? e.target.value.split(",").map((x) => x.trim()).filter(Boolean)
                                  : e.target.value,
                            })
                          }
                        />
                      ))}
                    <Button size="icon-sm" variant="ghost" aria-label={`Remove filter ${i + 1}`} onClick={() => update({ filters: spec.filters.filter((_, j) => j !== i) })}>
                      <X aria-hidden />
                    </Button>
                  </div>
                );
              })}
              <Button
                size="sm"
                variant="outline"
                onClick={() => update({ filters: [...spec.filters, { field: ds.fields[0].key, op: ds.fields[0].ops[0] }] })}
              >
                <Plus aria-hidden /> Add filter
              </Button>
            </fieldset>

            <div className="flex flex-wrap items-end gap-3">
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="rb-sort">Sort by</Label>
                <select
                  id="rb-sort"
                  disabled={!editable}
                  value={spec.sort[0] ? `${spec.sort[0].column}:${spec.sort[0].desc ? "desc" : "asc"}` : ""}
                  onChange={(e) => {
                    if (!e.target.value) return update({ sort: [] });
                    const [col, dir] = e.target.value.split(":");
                    update({ sort: [{ column: Number(col), desc: dir === "desc" }] });
                  }}
                  className="h-9 rounded-lg border bg-card px-2 text-sm"
                >
                  <option value="">First column</option>
                  {spec.columns.flatMap((c, i) => {
                    const label = `${c.agg ? AGG_LABEL[c.agg] + " " : ""}${field(c.field)?.label ?? c.field}`;
                    return [
                      <option key={`${i}a`} value={`${i}:asc`}>
                        {label} ↑
                      </option>,
                      <option key={`${i}d`} value={`${i}:desc`}>
                        {label} ↓
                      </option>,
                    ];
                  })}
                </select>
              </div>
              <div className="flex flex-1 flex-wrap justify-end gap-2">
                <Button onClick={run} disabled={running}>
                  {running ? <Loader2 className="animate-spin" aria-hidden /> : <Play aria-hidden />} Run
                </Button>
                {editable && (
                  <>
                    {current?.mine && (
                      <Button
                        variant="outline"
                        onClick={() => {
                          setSaveName(current.name);
                          setSaveShared(current.shared);
                          setSaveOpen("save");
                        }}
                      >
                        <Save aria-hidden /> Save
                      </Button>
                    )}
                    <Button
                      variant="outline"
                      onClick={() => {
                        setSaveName(current ? `${current.name} (copy)` : "");
                        setSaveShared(false);
                        setSaveOpen("save_as");
                      }}
                    >
                      <Save aria-hidden /> {current ? "Save as new" : "Save"}
                    </Button>
                    {current && !current.mine && (
                      <Button variant="outline" onClick={duplicate}>
                        <Copy aria-hidden /> Duplicate
                      </Button>
                    )}
                    {current?.mine && (
                      <Button variant="ghost" onClick={remove} aria-label="Delete this report">
                        <Trash2 aria-hidden />
                      </Button>
                    )}
                  </>
                )}
              </div>
            </div>
          </section>

          {error && (
            <p role="alert" className="text-sm text-destructive print:hidden">
              {error}
            </p>
          )}

          {result && (
            <section aria-label="Results" className="rounded-xl border bg-card">
              <div className="flex flex-wrap items-center gap-2 border-b px-4 py-2 print:hidden">
                <span className="flex-1 text-sm text-muted-foreground">
                  {result.rows.length} row{result.rows.length === 1 ? "" : "s"}
                  {result.truncated ? " shown (the first 1,000). Export to get them all." : ""}
                </span>
                <Button size="sm" variant="outline" onClick={() => exportAs("csv")}>
                  <Download aria-hidden /> CSV
                </Button>
                <Button size="sm" variant="outline" onClick={() => exportAs("xlsx")}>
                  <FileSpreadsheet aria-hidden /> Excel
                </Button>
                <Button size="sm" variant="outline" onClick={() => window.print()}>
                  <Printer aria-hidden /> Print / PDF
                </Button>
              </div>
              <h2 className="hidden px-4 pt-4 text-lg font-semibold print:block">
                {current?.name ?? ds.label}
                {ds.has_date_range && ` · ${spec.date_from} to ${spec.date_to}`}
              </h2>
              <div className="overflow-x-auto">
                <table className="w-full text-sm">
                  <thead className="text-left text-xs text-muted-foreground">
                    <tr>
                      {result.columns.map((c, i) => (
                        <th key={i} scope="col" className={`px-4 py-2 font-medium ${c.type === "money" || c.type === "number" ? "text-right" : ""}`}>
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
                            <td key={ci} className={`px-4 py-1.5 ${t === "money" || t === "number" ? "tabular text-right" : ""}`}>
                              {cell(v, t)}
                            </td>
                          );
                        })}
                      </tr>
                    ))}
                    {result.rows.length === 0 && (
                      <tr>
                        <td colSpan={result.columns.length} className="px-4 py-8 text-center text-muted-foreground">
                          Nothing matches.
                        </td>
                      </tr>
                    )}
                  </tbody>
                </table>
              </div>
            </section>
          )}
        </div>
      </div>

      {saveOpen && (
        <Modal
          open
          onOpenChange={(o) => !o && setSaveOpen(null)}
          title={saveOpen === "save" ? "Save report" : "Save as a new report"}
          footer={
            <>
              <Button variant="outline" onClick={() => setSaveOpen(null)}>
                Cancel
              </Button>
              <Button disabled={!saveName.trim()} onClick={save}>
                Save
              </Button>
            </>
          }
        >
          <div className="space-y-3">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="rb-name">Name</Label>
              <Input id="rb-name" value={saveName} onChange={(e) => setSaveName(e.target.value)} />
            </div>
            <label className="flex items-start gap-2 text-sm">
              <input type="checkbox" className="mt-1" checked={saveShared} onChange={(e) => setSaveShared(e.target.checked)} />
              <span>
                Share with everyone who can run reports
                <span className="block text-xs text-muted-foreground">Each person sees only their own branches&apos; data when they run it.</span>
              </span>
            </label>
          </div>
        </Modal>
      )}
    </div>
  );
}
