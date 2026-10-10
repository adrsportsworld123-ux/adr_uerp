"use client";

// Console home: every merchant with plan, status, usage and money due.
import { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { Plus } from "lucide-react";
import { ApiError } from "@/lib/api-client";
import { rupees } from "@/lib/format";
import { MerchantRow, platformApi, SUB_STATUS } from "@/lib/platform-api";
import { buttonVariants } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/data/page-header";
import { StatusChip, Tone } from "@/components/data/status-chip";


function usage(u: { used: number; limit: number | null } | undefined) {
  if (!u) return "—";
  return u.limit == null ? `${u.used}` : `${u.used}/${u.limit}`;
}

export default function MerchantsPage() {
  const [rows, setRows] = useState<MerchantRow[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [q, setQ] = useState("");
  const [status, setStatus] = useState("");

  useEffect(() => {
    platformApi
      .get<{ merchants: MerchantRow[] }>("/platform/merchants")
      .then((d) => setRows(d.merchants))
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load merchants"));
  }, []);

  const shown = useMemo(
    () =>
      (rows ?? []).filter(
        (r) =>
          (!status || r.status === status) &&
          (!q || `${r.merchant.code} ${r.merchant.legal_name} ${r.merchant.trade_name ?? ""}`.toLowerCase().includes(q.toLowerCase())),
      ),
    [rows, q, status],
  );

  return (
    <div className="space-y-4">
      <PageHeader
        title="Merchants"
        description="Every tenant, its plan and lifecycle, usage against its limits, and what it owes."
        actions={
          <Link href="/platform/merchants/new" className={buttonVariants()}>
            <Plus aria-hidden /> Onboard merchant
          </Link>
        }
      />
      <div className="flex flex-wrap gap-2">
        <Input aria-label="Search merchants" placeholder="Search code or name" className="max-w-xs" value={q} onChange={(e) => setQ(e.target.value)} />
        <select
          aria-label="Status"
          value={status}
          onChange={(e) => setStatus(e.target.value)}
          className="h-9 rounded-lg border bg-card px-2 text-sm"
        >
          <option value="">All statuses</option>
          {Object.entries(SUB_STATUS).map(([k, v]) => (
            <option key={k} value={k}>
              {v.label}
            </option>
          ))}
        </select>
      </div>
      {error && <p className="text-sm text-destructive">{error}</p>}
      <div className="overflow-x-auto rounded-xl border bg-card">
        <table className="w-full text-sm">
          <thead className="border-b text-left text-xs text-muted-foreground">
            <tr>
              <th className="px-4 py-2 font-medium">Merchant</th>
              <th className="px-4 py-2 font-medium">Plan</th>
              <th className="px-4 py-2 font-medium">Status</th>
              <th className="px-4 py-2 font-medium">Branches</th>
              <th className="px-4 py-2 font-medium">Tills</th>
              <th className="px-4 py-2 font-medium">Products</th>
              <th className="px-4 py-2 font-medium">Sales this month</th>
              <th className="px-4 py-2 text-right font-medium">Due</th>
              <th className="px-4 py-2 font-medium">Last sale</th>
            </tr>
          </thead>
          <tbody>
            {shown.map((r) => {
              const st = SUB_STATUS[r.status] ?? { label: r.status, tone: "muted" as Tone };
              return (
                <tr key={r.merchant.merchant_id} className="border-b last:border-0">
                  <td className="px-4 py-2">
                    <Link href={`/platform/merchants/${r.merchant.merchant_id}`} className="font-medium underline-offset-2 hover:underline">
                      {r.merchant.trade_name || r.merchant.legal_name}
                    </Link>
                    <div className="text-xs text-muted-foreground">{r.merchant.code}</div>
                  </td>
                  <td className="px-4 py-2">{r.plan.name}</td>
                  <td className="px-4 py-2">
                    <StatusChip status={r.status} tone={st.tone} label={st.label} />
                  </td>
                  <td className="tabular px-4 py-2">{usage(r.usage.branches)}</td>
                  <td className="tabular px-4 py-2">{usage(r.usage.pos_terminals)}</td>
                  <td className="tabular px-4 py-2">{usage(r.usage.products)}</td>
                  <td className="tabular px-4 py-2">{usage(r.usage.transactions_per_month)}</td>
                  <td className="tabular px-4 py-2 text-right">{r.invoices_due > 0 ? rupees(r.amount_due) : "—"}</td>
                  <td className="px-4 py-2 text-xs text-muted-foreground">
                    {r.last_sale_at ? new Date(r.last_sale_at).toLocaleDateString("en-IN") : "Never"}
                  </td>
                </tr>
              );
            })}
            {rows && shown.length === 0 && (
              <tr>
                <td colSpan={9} className="px-4 py-8 text-center text-muted-foreground">
                  No merchants match.
                </td>
              </tr>
            )}
            {!rows && !error && (
              <tr>
                <td colSpan={9} className="px-4 py-8 text-center text-muted-foreground">
                  Loading…
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
