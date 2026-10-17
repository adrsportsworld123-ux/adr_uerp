"use client";

// The activity log (erp-core-go internal/activity, migrations/051): every
// POS and web-admin request — who, from which app and terminal, from what
// IP, what it did and whether it succeeded — and the merchant's switches
// for it. The record-change trail next to it is statutory and always on.
import { Fragment, useCallback, useEffect, useState } from "react";
import { ChevronDown, ChevronRight, Loader2 } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { ActivityEntry, AuditSettings } from "@/lib/types";
import { dateTime } from "@/lib/format";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import { StatusChip } from "@/components/data/status-chip";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

const PAGE = 50;
const CLIENT_LABEL: Record<string, string> = { pos: "POS", web: "Web admin", api: "API" };

function Select({ id, value, onChange, options }: { id: string; value: string; onChange: (v: string) => void; options: [string, string][] }) {
  return (
    <select id={id} className="h-9 rounded-md border bg-background px-2 text-sm" value={value} onChange={(e) => onChange(e.target.value)}>
      {options.map(([v, l]) => (
        <option key={v} value={v}>
          {l}
        </option>
      ))}
    </select>
  );
}

export function ActivityLog() {
  const [client, setClient] = useState("");
  const [module, setModule] = useState("");
  const [outcome, setOutcome] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [q, setQ] = useState("");
  const [page, setPage] = useState(1);
  const [modules, setModules] = useState<string[]>([]);
  const [rows, setRows] = useState<ActivityEntry[] | null>(null);
  const [total, setTotal] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const [expanded, setExpanded] = useState<number | null>(null);

  useEffect(() => {
    api.get<{ modules: string[] }>("/api/v1/audit/modules").then((d) => setModules(d.modules)).catch(() => {});
  }, []);

  const load = useCallback(() => {
    const p = new URLSearchParams({ page: String(page), limit: String(PAGE) });
    for (const [k, v] of Object.entries({ client, module, outcome, from, to, q: q.trim() })) if (v) p.set(k, v);
    api
      .get<{ activity: ActivityEntry[]; total: number }>(`/api/v1/audit/activity?${p}`)
      .then((d) => {
        setRows(d.activity);
        setTotal(d.total);
        setError(null);
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load the activity log"));
  }, [client, module, outcome, from, to, q, page]);
  useEffect(load, [load]);

  const reset = <T,>(set: (v: T) => void) => (v: T) => {
    set(v);
    setPage(1);
  };

  return (
    <div className="flex flex-col gap-4">
      <Card>
        <CardContent className="flex flex-wrap items-end gap-4 pt-6">
          <div className="flex flex-col gap-2">
            <Label htmlFor="act-client">App</Label>
            <Select id="act-client" value={client} onChange={reset(setClient)} options={[["", "All"], ["pos", "POS"], ["web", "Web admin"], ["api", "API"]]} />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="act-module">Area</Label>
            <Select id="act-module" value={module} onChange={reset(setModule)} options={[["", "All"], ...modules.map((m) => [m, m] as [string, string])]} />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="act-outcome">Outcome</Label>
            <Select id="act-outcome" value={outcome} onChange={reset(setOutcome)} options={[["", "All"], ["success", "Succeeded"], ["failure", "Refused or failed"]]} />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="act-from">From</Label>
            <Input id="act-from" type="date" value={from} onChange={(e) => reset(setFrom)(e.target.value)} className="w-40" />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="act-to">To</Label>
            <Input id="act-to" type="date" value={to} onChange={(e) => reset(setTo)(e.target.value)} className="w-40" />
          </div>
          <div className="flex min-w-56 flex-1 flex-col gap-2">
            <Label htmlFor="act-q">Path or IP</Label>
            <Input id="act-q" value={q} onChange={(e) => reset(setQ)(e.target.value)} placeholder="e.g. checkout, /pricing, 10.0.0.12" />
          </div>
        </CardContent>
      </Card>

      {error && <p className="text-sm text-destructive">{error}</p>}

      <Card>
        <CardContent className="pt-6">
          {rows == null && !error ? (
            <p className="flex items-center gap-2 text-sm text-muted-foreground">
              <Loader2 className="size-4 animate-spin" aria-hidden /> Loading…
            </p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-6" />
                  <TableHead>When</TableHead>
                  <TableHead>Who</TableHead>
                  <TableHead>App</TableHead>
                  <TableHead>Action</TableHead>
                  <TableHead>Result</TableHead>
                  <TableHead>IP</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(rows ?? []).map((a) => (
                  <Fragment key={a.id}>
                    <TableRow>
                      <TableCell>
                        <button
                          type="button"
                          aria-label={expanded === a.id ? "Hide details" : "Show details"}
                          aria-expanded={expanded === a.id}
                          onClick={() => setExpanded(expanded === a.id ? null : a.id)}
                          className="grid size-6 place-items-center rounded hover:bg-muted"
                        >
                          {expanded === a.id ? <ChevronDown className="size-4" /> : <ChevronRight className="size-4" />}
                        </button>
                      </TableCell>
                      <TableCell className="whitespace-nowrap text-xs">{dateTime(a.occurred_at)}</TableCell>
                      <TableCell className="text-sm">
                        {a.user_name ?? "—"}
                        {a.branch_name && <span className="block text-xs text-muted-foreground">{a.branch_name}</span>}
                      </TableCell>
                      <TableCell className="text-sm">
                        {CLIENT_LABEL[a.client] ?? a.client}
                        {a.terminal_name && <span className="block text-xs text-muted-foreground">{a.terminal_name}</span>}
                      </TableCell>
                      <TableCell className="font-mono text-xs">
                        <span className="font-semibold">{a.method}</span> {a.route}
                      </TableCell>
                      <TableCell>
                        <StatusChip status="" tone={a.status_code < 400 ? "ok" : a.status_code < 500 ? "warn" : "bad"} label={String(a.status_code)} />
                      </TableCell>
                      <TableCell className="font-mono text-xs">{a.ip ?? "—"}</TableCell>
                    </TableRow>
                    {expanded === a.id && (
                      <TableRow>
                        <TableCell />
                        <TableCell colSpan={6}>
                          <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 text-xs">
                            <dt className="text-muted-foreground">Path</dt>
                            <dd className="font-mono">{a.path}</dd>
                            {a.entity_id && (
                              <>
                                <dt className="text-muted-foreground">Record</dt>
                                <dd className="font-mono">{a.entity_id}</dd>
                              </>
                            )}
                            <dt className="text-muted-foreground">Took</dt>
                            <dd>{a.duration_ms} ms</dd>
                            <dt className="text-muted-foreground">Device</dt>
                            <dd className="break-all">{a.user_agent ?? "—"}</dd>
                            <dt className="text-muted-foreground">Request id</dt>
                            <dd className="font-mono">{a.request_id ?? "—"}</dd>
                          </dl>
                          {a.request_body != null && (
                            <pre className="mt-2 max-h-64 overflow-auto rounded-md bg-muted p-2 text-xs">{JSON.stringify(a.request_body, null, 2)}</pre>
                          )}
                        </TableCell>
                      </TableRow>
                    )}
                  </Fragment>
                ))}
                {rows?.length === 0 && (
                  <TableRow>
                    <TableCell colSpan={7} className="py-6 text-center text-sm text-muted-foreground">
                      No activity matches these filters
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          )}
          <div className="mt-4 flex items-center justify-between text-sm text-muted-foreground">
            <span>{total} action(s)</span>
            <div className="flex gap-2">
              <Button variant="outline" size="sm" disabled={page <= 1} onClick={() => setPage(page - 1)}>
                Previous
              </Button>
              <Button variant="outline" size="sm" disabled={page * PAGE >= total} onClick={() => setPage(page + 1)}>
                Next
              </Button>
            </div>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

export function AuditSettingsPanel() {
  const { can } = usePermissions();
  const allowed = can("audit.configure");
  const [saved, setSaved] = useState<AuditSettings | null>(null);
  const [draft, setDraft] = useState<AuditSettings | null>(null);
  const [modules, setModules] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api
      .get<AuditSettings>("/api/v1/audit/settings")
      .then((s) => {
        setSaved(s);
        setDraft(s);
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load the audit settings"));
    api.get<{ modules: string[] }>("/api/v1/audit/modules").then((d) => setModules(d.modules.filter((m) => m !== "audit"))).catch(() => {});
  }, []);

  if (error) return <p className="text-sm text-destructive">{error}</p>;
  if (!draft || !saved) {
    return (
      <p className="flex items-center gap-2 text-sm text-muted-foreground">
        <Loader2 className="size-4 animate-spin" aria-hidden /> Loading…
      </p>
    );
  }
  const dirty =
    draft.activity_log_enabled !== saved.activity_log_enabled ||
    draft.log_read_requests !== saved.log_read_requests ||
    draft.capture_request_body !== saved.capture_request_body ||
    [...draft.disabled_modules].sort().join() !== [...saved.disabled_modules].sort().join();
  const allModules = Array.from(new Set([...modules, ...draft.disabled_modules])).sort();

  async function save() {
    if (!draft) return;
    setSaving(true);
    try {
      const s = await api.put<AuditSettings>("/api/v1/audit/settings", {
        activity_log_enabled: draft.activity_log_enabled,
        log_read_requests: draft.log_read_requests,
        capture_request_body: draft.capture_request_body,
        disabled_modules: draft.disabled_modules,
      });
      setSaved(s);
      setDraft(s);
      toast.success("Audit settings saved");
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not save the audit settings");
    } finally {
      setSaving(false);
    }
  }

  function toggleModule(m: string, off: boolean) {
    if (!draft) return;
    setDraft({ ...draft, disabled_modules: off ? [...draft.disabled_modules, m] : draft.disabled_modules.filter((x) => x !== m) });
  }

  return (
    <div className="flex max-w-2xl flex-col gap-4">
      <Card>
        <CardHeader>
          <CardTitle>Activity log</CardTitle>
          <CardDescription>What this merchant records about each action on the POS and in this app. Changes take effect within 30 seconds and are themselves recorded.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-5 text-sm">
          <fieldset className="space-y-3" disabled={!allowed}>
            <label className="flex items-start gap-3">
              <input type="checkbox" className="mt-1" checked={draft.activity_log_enabled} onChange={(e) => setDraft({ ...draft, activity_log_enabled: e.target.checked })} />
              <span>
                <span className="font-medium">Record activity</span>
                <span className="block text-muted-foreground">Who did what, from which app, terminal and IP, and whether it succeeded — including refused attempts.</span>
              </span>
            </label>
            <label className="flex items-start gap-3">
              <input
                type="checkbox"
                className="mt-1"
                disabled={!draft.activity_log_enabled}
                checked={draft.capture_request_body}
                onChange={(e) => setDraft({ ...draft, capture_request_body: e.target.checked })}
              />
              <span>
                <span className="font-medium">Keep what was submitted</span>
                <span className="block text-muted-foreground">The form or cart data sent with each action. Passwords, PINs and tokens are always masked.</span>
              </span>
            </label>
            <label className="flex items-start gap-3">
              <input
                type="checkbox"
                className="mt-1"
                disabled={!draft.activity_log_enabled}
                checked={draft.log_read_requests}
                onChange={(e) => setDraft({ ...draft, log_read_requests: e.target.checked })}
              />
              <span>
                <span className="font-medium">Also record views</span>
                <span className="block text-muted-foreground">Every screen opened and report run. Much more data — live screens refresh every 30 seconds.</span>
              </span>
            </label>
          </fieldset>
          {allModules.length > 0 && (
            <fieldset className="space-y-2" disabled={!allowed || !draft.activity_log_enabled}>
              <legend className="mb-1 font-medium">Areas not to record</legend>
              <div className="grid grid-cols-2 gap-x-6 gap-y-1 sm:grid-cols-3">
                {allModules.map((m) => (
                  <label key={m} className="flex items-center gap-2">
                    <input type="checkbox" checked={draft.disabled_modules.includes(m)} onChange={(e) => toggleModule(m, e.target.checked)} />
                    {m}
                  </label>
                ))}
              </div>
            </fieldset>
          )}
          <Button onClick={save} disabled={!allowed || !dirty || saving} title={allowed ? undefined : missingPermissionHint("audit.configure")}>
            {saving && <Loader2 className="animate-spin" aria-hidden />}
            Save settings
          </Button>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Record changes</CardTitle>
          <CardDescription>
            The before-and-after trail of every change to a record is always on and can&apos;t be switched off: India&apos;s Companies (Accounts) Rules require
            accounting software to keep an audit trail that can&apos;t be disabled.
          </CardDescription>
        </CardHeader>
      </Card>
    </div>
  );
}
