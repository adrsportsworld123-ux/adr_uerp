"use client";

// What platform staff did: onboarding, plan edits, subscription changes,
// invoice payments and voids (newest 200).
import { useEffect, useState } from "react";
import { ApiError } from "@/lib/api-client";
import { platformApi } from "@/lib/platform-api";
import { PageHeader } from "@/components/data/page-header";

type Entry = {
  id: number;
  staff: string;
  action: string;
  target_type: string;
  target_id: string;
  before: unknown;
  after: unknown;
  reason: string | null;
  created_at: string;
};

export default function PlatformAuditPage() {
  const [rows, setRows] = useState<Entry[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    platformApi
      .get<{ entries: Entry[] }>("/platform/audit-log")
      .then((d) => setRows(d.entries))
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load the log"));
  }, []);

  return (
    <div className="space-y-4">
      <PageHeader title="Platform audit log" description="Every change platform staff made, with the reason they gave." />
      {error && <p className="text-sm text-destructive">{error}</p>}
      <div className="overflow-x-auto rounded-xl border bg-card">
        <table className="w-full text-sm">
          <thead className="border-b text-left text-xs text-muted-foreground">
            <tr>
              <th className="px-4 py-2 font-medium">When</th>
              <th className="px-4 py-2 font-medium">Who</th>
              <th className="px-4 py-2 font-medium">Action</th>
              <th className="px-4 py-2 font-medium">Target</th>
              <th className="px-4 py-2 font-medium">Reason</th>
              <th className="px-4 py-2 font-medium">Change</th>
            </tr>
          </thead>
          <tbody>
            {rows?.map((r) => (
              <tr key={r.id} className="border-b align-top last:border-0">
                <td className="whitespace-nowrap px-4 py-2 text-xs">{new Date(r.created_at).toLocaleString("en-IN")}</td>
                <td className="px-4 py-2">{r.staff}</td>
                <td className="px-4 py-2 font-medium">{r.action}</td>
                <td className="px-4 py-2 text-xs">
                  {r.target_type} <span className="font-mono text-muted-foreground">{r.target_id.slice(0, 8)}</span>
                </td>
                <td className="px-4 py-2">{r.reason ?? "—"}</td>
                <td className="px-4 py-2">
                  <details>
                    <summary className="cursor-pointer text-xs text-muted-foreground">Before / after</summary>
                    <pre className="mt-1 max-w-md overflow-auto whitespace-pre-wrap text-xs">{JSON.stringify({ before: r.before, after: r.after }, null, 2)}</pre>
                  </details>
                </td>
              </tr>
            ))}
            {rows?.length === 0 && (
              <tr>
                <td colSpan={6} className="px-4 py-8 text-center text-muted-foreground">
                  Nothing yet.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
