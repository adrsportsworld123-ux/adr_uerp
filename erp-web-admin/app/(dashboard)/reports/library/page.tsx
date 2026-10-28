"use client";

// The standard report library (erp-core-go reportbuilder library.go,
// phase0_1_design.md §3.63): sales, items, payments, stock, products,
// purchases, accounts (journal, ledgers, trial balance, profit and loss)
// and audit (the audit trail and the sales audit). Pick a report, a period
// or dates and a few filters; run it here (first 1,000 rows, with totals),
// print it, export it to CSV or Excel, or save it to Saved Reports to run
// again later. A saved "this month" always means the current month. Every
// plan has these; each person sees their own branches' data.
import { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { Download, FileSpreadsheet, Loader2, Play, Printer, Save, Search } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Branch } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Modal } from "@/components/ui/dialog";
import { PageHeader } from "@/components/data/page-header";
import { ReportResult, ReportTable } from "@/components/reports/report-result";

type FilterField = { key: string; label: string; type: "text" | "number" | "money" | "date" | "bool"; ops: string[] };
type LibraryReport = {
  key: string;
  group: string;
  name: string;
  description: string;
  default_period?: string;
  dataset_label: string;
  has_date_range: boolean;
  filter_fields: FilterField[];
};
type Period = { key: string; label: string };
type Filter = { field: string; op: string; value?: unknown };

// Outstanding: credit owed by customers and to suppliers, party-wise (erp-core-go §3.77).
const GROUP_ORDER = ["Sales", "Items", "Payments", "Stock", "Products", "Purchases", "Outstanding", "Expenses", "Accounts", "Audit"];
const selectClass = "h-9 rounded-lg border bg-card px-2 text-sm";
// Fixed choices for some filters (the values the server stores).
const CHOICES: Record<string, { value: string; label: string }[]> = {
  channel: [
    { value: "pos", label: "Till" },
    { value: "backoffice", label: "Back office" },
    { value: "online", label: "Online" },
  ],
  method: ["cash", "card", "upi", "bank_transfer", "cheque"].map((m) => ({ value: m, label: m.replace("_", " ").replace(/^./, (c) => c.toUpperCase()) })),
  party_type: [
    { value: "customer", label: "Customer" },
    { value: "supplier", label: "Supplier" },
  ],
  action: ["create", "update", "delete"].map((a) => ({ value: a, label: a.replace(/^./, (c) => c.toUpperCase()) })),
  event: ["Sale voided", "Cart released", "Return", "Manual discount", "Open price"].map((e) => ({ value: e, label: e })),
  customer_type: [
    { value: "b2c", label: "B2C" },
    { value: "b2b", label: "B2B" },
  ],
};

export default function ReportLibraryPage() {
  const [reports, setReports] = useState<LibraryReport[]>([]);
  const [periods, setPeriods] = useState<Period[]>([]);
  const [branches, setBranches] = useState<Branch[]>([]);
  const [q, setQ] = useState("");
  const [key, setKey] = useState("");
  const [period, setPeriod] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [values, setValues] = useState<Record<string, string>>({});
  const [result, setResult] = useState<ReportResult | null>(null);
  const [running, setRunning] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [saveName, setSaveName] = useState("");
  const [saveShared, setSaveShared] = useState(false);

  useEffect(() => {
    api
      .get<{ reports: LibraryReport[]; periods: Period[] }>("/api/v1/reports/library")
      .then((d) => {
        setReports(d.reports);
        setPeriods(d.periods);
        const first = new URLSearchParams(window.location.search).get("report");
        const r = d.reports.find((x) => x.key === first) ?? d.reports[0];
        if (r) {
          setKey(r.key);
          setPeriod(r.default_period ?? "");
        }
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load the report library"));
    api.get<{ branches: Branch[] }>("/api/v1/branches?scope=mine").then((d) => setBranches(d.branches)).catch(() => {});
  }, []);

  const report = reports.find((r) => r.key === key);
  const groups = useMemo(() => {
    const words = q.toLowerCase().split(/\s+/).filter(Boolean);
    const hit = (r: LibraryReport) => words.every((w) => `${r.name} ${r.description} ${r.group}`.toLowerCase().includes(w));
    // A group the server adds later still shows, after the known ones.
    const order = [...GROUP_ORDER, ...reports.map((r) => r.group).filter((g, i, all) => !GROUP_ORDER.includes(g) && all.indexOf(g) === i)];
    return order.map((g) => ({ g, items: reports.filter((r) => r.group === g && hit(r)) })).filter((x) => x.items.length);
  }, [reports, q]);

  const choose = (r: LibraryReport) => {
    setKey(r.key);
    setPeriod(r.default_period ?? "");
    setFrom("");
    setTo("");
    setValues({});
    setResult(null);
    setError(null);
  };

  const filters = (): Filter[] =>
    (report?.filter_fields ?? []).flatMap((f) => {
      const v = (values[f.key] ?? "").trim();
      if (!v) return [];
      if (f.type === "bool") return [{ field: f.key, op: v === "yes" ? "is_true" : "is_false" }];
      if (f.key === "branch" || CHOICES[f.key]) return [{ field: f.key, op: "eq", value: v }];
      return [{ field: f.key, op: "contains", value: v }];
    });
  const custom = period === "custom";
  const body = () => ({ ...(custom ? { date_from: from, date_to: to } : { period }), filters: filters() });
  const datesOk = !report?.has_date_range || !custom || (from && to && from <= to);

  async function run() {
    if (!report) return;
    setRunning(true);
    setError(null);
    try {
      setResult(await api.post<ReportResult>(`/api/v1/reports/library/${report.key}/run`, body()));
    } catch (e) {
      setResult(null);
      setError(e instanceof ApiError ? e.message : "Could not run the report");
    } finally {
      setRunning(false);
    }
  }
  async function exportAs(format: "csv" | "xlsx") {
    if (!report) return;
    try {
      await api.post(`/api/v1/reports/library/${report.key}/export`, { ...body(), format });
      toast.success("Export started: download it from Saved Reports → Generated reports when it's ready");
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not start the export");
    }
  }
  async function save() {
    if (!report) return;
    try {
      await api.post("/api/v1/reports/saved", { library_key: report.key, name: saveName.trim(), shared: saveShared, ...body() });
      toast.success(`Saved “${saveName.trim()}” to Saved Reports`);
      setSaving(false);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not save the report");
    }
  }

  const periodLabel = periods.find((p) => p.key === period)?.label;
  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Report Library"
        description="Ready-made reports on sales, items, payments, stock, products, purchases, accounts and audit. Run, print, export or save any of them."
        actions={
          <Link href="/reports/saved" className="text-sm text-primary hover:underline">
            Saved &amp; generated reports →
          </Link>
        }
      />
      <div className="grid gap-4 lg:grid-cols-[280px_1fr]">
        <aside className="flex flex-col gap-3 rounded-xl border bg-card p-3 print:hidden" aria-label="Reports">
          <div className="flex items-center gap-2 rounded-lg border px-2">
            <Search className="size-4 text-muted-foreground" aria-hidden />
            <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Find a report" aria-label="Find a report" className="h-9 flex-1 bg-transparent text-sm outline-none" />
          </div>
          <nav className="flex max-h-[70vh] flex-col gap-3 overflow-y-auto">
            {groups.map(({ g, items }) => (
              <div key={g}>
                <p className="px-2 pb-1 text-xs font-semibold uppercase tracking-wide text-muted-foreground">{g}</p>
                <ul>
                  {items.map((r) => (
                    <li key={r.key}>
                      <button
                        type="button"
                        onClick={() => choose(r)}
                        aria-current={r.key === key ? "true" : undefined}
                        className={`w-full rounded-md px-2 py-1.5 text-left text-sm ${r.key === key ? "bg-accent font-medium text-accent-foreground" : "hover:bg-muted"}`}
                      >
                        {r.name}
                      </button>
                    </li>
                  ))}
                </ul>
              </div>
            ))}
          </nav>
        </aside>

        <div className="flex min-w-0 flex-col gap-4">
          {report && (
            <section className="flex flex-col gap-3 rounded-xl border bg-card p-4 print:hidden" aria-label="Report options">
              <div>
                <h2 className="text-base font-semibold">{report.name}</h2>
                <p className="text-sm text-muted-foreground">{report.description}</p>
              </div>
              <div className="flex flex-wrap items-end gap-3">
                {report.has_date_range && (
                  <>
                    <div className="flex flex-col gap-1">
                      <Label htmlFor="lib-period">Period</Label>
                      <select id="lib-period" value={period} onChange={(e) => setPeriod(e.target.value)} className={`${selectClass} w-48`}>
                        {periods.map((p) => (
                          <option key={p.key} value={p.key}>
                            {p.label}
                          </option>
                        ))}
                        <option value="custom">Custom dates</option>
                      </select>
                    </div>
                    {custom && (
                      <>
                        <div className="flex flex-col gap-1">
                          <Label htmlFor="lib-from">From</Label>
                          <Input id="lib-from" type="date" value={from} onChange={(e) => setFrom(e.target.value)} className="w-40" />
                        </div>
                        <div className="flex flex-col gap-1">
                          <Label htmlFor="lib-to">To</Label>
                          <Input id="lib-to" type="date" value={to} onChange={(e) => setTo(e.target.value)} className="w-40" />
                        </div>
                      </>
                    )}
                  </>
                )}
                {report.filter_fields.map((f) => (
                  <div key={f.key} className="flex flex-col gap-1">
                    <Label htmlFor={`lib-f-${f.key}`}>{f.label}</Label>
                    {f.key === "branch" ? (
                      <select id={`lib-f-${f.key}`} value={values.branch ?? ""} onChange={(e) => setValues({ ...values, branch: e.target.value })} className={`${selectClass} w-44`}>
                        <option value="">All my branches</option>
                        {branches.map((b) => (
                          <option key={b.branch_id} value={b.name}>
                            {b.name}
                          </option>
                        ))}
                      </select>
                    ) : CHOICES[f.key] || f.type === "bool" ? (
                      <select id={`lib-f-${f.key}`} value={values[f.key] ?? ""} onChange={(e) => setValues({ ...values, [f.key]: e.target.value })} className={`${selectClass} w-40`}>
                        <option value="">Any</option>
                        {(CHOICES[f.key] ?? [
                          { value: "yes", label: "Yes" },
                          { value: "no", label: "No" },
                        ]).map((c) => (
                          <option key={c.value} value={c.value}>
                            {c.label}
                          </option>
                        ))}
                      </select>
                    ) : (
                      <Input
                        id={`lib-f-${f.key}`}
                        value={values[f.key] ?? ""}
                        onChange={(e) => setValues({ ...values, [f.key]: e.target.value })}
                        placeholder="contains…"
                        className="w-40"
                        onKeyDown={(e) => e.key === "Enter" && run()}
                      />
                    )}
                  </div>
                ))}
              </div>
              <div className="flex flex-wrap gap-2">
                <Button onClick={run} disabled={running || !datesOk}>
                  {running ? <Loader2 className="animate-spin" /> : <Play />} Run report
                </Button>
                <Button variant="outline" onClick={() => window.print()} disabled={!result}>
                  <Printer /> Print / PDF
                </Button>
                <Button variant="outline" onClick={() => exportAs("csv")} disabled={!datesOk}>
                  <Download /> CSV
                </Button>
                <Button variant="outline" onClick={() => exportAs("xlsx")} disabled={!datesOk}>
                  <FileSpreadsheet /> Excel
                </Button>
                <Button
                  variant="outline"
                  disabled={!datesOk}
                  onClick={() => {
                    setSaveName(`${report.name}${custom ? ` ${from} to ${to}` : periodLabel ? ` (${periodLabel.toLowerCase()})` : ""}`);
                    setSaving(true);
                  }}
                >
                  <Save /> Save to my reports
                </Button>
              </div>
            </section>
          )}
          {error && (
            <p className="text-sm text-destructive" role="alert">
              {error}
            </p>
          )}
          {result && report && (
            <section aria-label="Results" className="rounded-xl border bg-card print:border-0">
              <div className="flex items-center justify-between border-b px-4 py-2 text-sm text-muted-foreground print:hidden">
                <span>
                  {result.rows.length} row{result.rows.length === 1 ? "" : "s"}
                  {result.date_from && ` · ${result.date_from} to ${result.date_to}`}
                </span>
              </div>
              <ReportTable result={result} title={report.name} totalLabel={report.key.startsWith("profit_loss") ? "Profit (loss when negative)" : "Total"} />
            </section>
          )}
        </div>
      </div>

      {saving && report && (
        <Modal
          open
          onOpenChange={(o) => !o && setSaving(false)}
          title="Save to my reports"
          description={custom ? "Saved with these dates." : `Saved with “${periodLabel}”: each run covers that period as of the day it runs.`}
          footer={
            <>
              <Button variant="outline" onClick={() => setSaving(false)}>
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
              <Label htmlFor="lib-save-name">Name</Label>
              <Input id="lib-save-name" value={saveName} onChange={(e) => setSaveName(e.target.value)} maxLength={120} />
            </div>
            <label className="flex items-start gap-2 text-sm">
              <input type="checkbox" className="mt-1" checked={saveShared} onChange={(e) => setSaveShared(e.target.checked)} />
              <span>
                Share with everyone
                <span className="block text-xs text-muted-foreground">Each person sees only their own branches&apos; data when they run it.</span>
              </span>
            </label>
          </div>
        </Modal>
      )}
    </div>
  );
}
