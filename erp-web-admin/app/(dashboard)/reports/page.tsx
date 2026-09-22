"use client";

// Phase 1's "Basic reports: daily sales, stock summary, EOD cash
// reconciliation" and Phase 2's Multi-Branch "consolidated cross-branch
// reporting" (erp-core-go's phased_roadmap.md) — both backend items had
// been marked done since their own phases, but neither erp-web-admin nor
// erp-pos-flutter (whose job is the transactional flow, not reporting)
// ever actually surfaced them anywhere. Found during an explicit
// "review previous phases for anything missing" pass. All five reports
// are read-only and not permission-gated (matches internal/reports'
// own handlers, which gate nothing beyond authentication), so this
// screen has no denial path to test.
import { useCallback, useEffect, useState } from "react";
import { api, ApiError, SEED_BRANCH_ID } from "@/lib/api-client";
import {
  Branch,
  ConsolidatedSalesReport,
  ConsolidatedStockEntry,
  DailySalesReport,
  EODCashReport,
  StockSummaryLine,
} from "@/lib/types";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

function today() {
  return new Date().toISOString().slice(0, 10);
}

export default function ReportsPage() {
  return (
    <div className="flex flex-col gap-6 max-w-4xl">
      <h1 className="text-2xl font-semibold">Reports</h1>
      <BranchDateReports />
      <ConsolidatedSalesSection />
      <ConsolidatedStockSection />
    </div>
  );
}

function BranchDateReports() {
  const [branchId, setBranchId] = useState(SEED_BRANCH_ID);
  const [branches, setBranches] = useState<Branch[]>([]);
  const [date, setDate] = useState(today());
  const [daily, setDaily] = useState<DailySalesReport | null>(null);
  const [eod, setEod] = useState<EODCashReport | null>(null);
  const [stock, setStock] = useState<StockSummaryLine[]>([]);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api
      .get<{ branches: Branch[] }>("/api/v1/branches")
      .then((d) => setBranches(d.branches))
      .catch(() => {});
  }, []);

  const load = useCallback(() => {
    Promise.all([
      api.get<DailySalesReport>(`/api/v1/reports/daily-sales?branch_id=${branchId}&date=${date}`),
      api.get<EODCashReport>(`/api/v1/reports/eod-cash?branch_id=${branchId}&date=${date}`),
      api.get<{ branch_id: string; lines: StockSummaryLine[] }>(`/api/v1/reports/stock-summary?branch_id=${branchId}`),
    ])
      .then(([d, e, s]) => {
        setDaily(d);
        setEod(e);
        setStock(s.lines);
        setError(null);
      })
      .catch((err) => setError(err instanceof ApiError ? err.message : "Could not load reports"));
  }, [branchId, date]);
  useEffect(load, [load]);

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Daily sales, EOD cash, and stock summary</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <div className="flex items-end gap-4 flex-wrap">
          <div className="flex flex-col gap-2 w-48">
            <Label>Branch</Label>
            <Select
              value={branchId}
              onValueChange={(v) => setBranchId(v ?? SEED_BRANCH_ID)}
              items={Object.fromEntries(branches.map((b) => [b.branch_id, b.name]))}
            >
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {branches.map((b) => (
                  <SelectItem key={b.branch_id} value={b.branch_id}>
                    {b.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="date">Date</Label>
            <Input id="date" type="date" value={date} onChange={(e) => setDate(e.target.value)} className="w-40" />
          </div>
        </div>

        {error && <p className="text-sm text-red-600">{error}</p>}

        {daily && (
          <div className="flex flex-col gap-2">
            <p className="text-xs font-medium text-zinc-600">Daily sales</p>
            <div className="grid grid-cols-2 sm:grid-cols-4 gap-3 text-sm">
              <div>
                Orders: <span className="font-semibold">{daily.order_count}</span>
              </div>
              <div>
                Subtotal: <span className="font-semibold">₹{daily.subtotal}</span>
              </div>
              <div>
                Tax: <span className="font-semibold">₹{daily.tax_total}</span>
              </div>
              <div>
                Grand total: <span className="font-semibold">₹{daily.grand_total}</span>
              </div>
            </div>
            {daily.by_payment_method.length > 0 && (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Method</TableHead>
                    <TableHead>Count</TableHead>
                    <TableHead>Amount</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {daily.by_payment_method.map((p) => (
                    <TableRow key={p.method}>
                      <TableCell className="uppercase">{p.method}</TableCell>
                      <TableCell>{p.count}</TableCell>
                      <TableCell>₹{p.amount}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </div>
        )}

        {eod && (
          <p className="text-sm">
            EOD cash: <span className="font-semibold">₹{eod.cash_total}</span> across {eod.cash_order_count} cash payment(s)
          </p>
        )}

        {stock.length > 0 && (
          <div className="flex flex-col gap-2">
            <p className="text-xs font-medium text-zinc-600">Stock summary ({stock.length} variant(s))</p>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Product</TableHead>
                  <TableHead>SKU</TableHead>
                  <TableHead>On hand</TableHead>
                  <TableHead>Reserved</TableHead>
                  <TableHead>Available</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {stock.map((s) => (
                  <TableRow key={s.variant_id}>
                    <TableCell>{s.product_name}</TableCell>
                    <TableCell className="font-mono text-xs">{s.sku}</TableCell>
                    <TableCell>{s.on_hand}</TableCell>
                    <TableCell>{s.reserved}</TableCell>
                    <TableCell>{s.available}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function ConsolidatedSalesSection() {
  const [date, setDate] = useState(today());
  const [report, setReport] = useState<ConsolidatedSalesReport | null>(null);

  const load = useCallback(() => {
    api
      .get<ConsolidatedSalesReport>(`/api/v1/reports/consolidated-sales?date=${date}`)
      .then(setReport)
      .catch(() => setReport(null));
  }, [date]);
  useEffect(load, [load]);

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Consolidated sales (all branches)</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <div className="flex items-center gap-2">
          <Label htmlFor="consDate" className="text-sm">
            Date
          </Label>
          <Input id="consDate" type="date" value={date} onChange={(e) => setDate(e.target.value)} className="w-40" />
        </div>
        {report && (
          <>
            <p className="text-sm">
              Total: <span className="font-semibold">{report.total_order_count}</span> order(s), ₹
              <span className="font-semibold">{report.total_grand_total}</span>
            </p>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Branch</TableHead>
                  <TableHead>Orders</TableHead>
                  <TableHead>Grand total</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {report.branches.map((b) => (
                  <TableRow key={b.branch_id}>
                    <TableCell>{b.branch_name}</TableCell>
                    <TableCell>{b.order_count}</TableCell>
                    <TableCell>₹{b.grand_total}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </>
        )}
      </CardContent>
    </Card>
  );
}

function ConsolidatedStockSection() {
  const [entries, setEntries] = useState<ConsolidatedStockEntry[]>([]);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api
      .get<{ entries: ConsolidatedStockEntry[] }>("/api/v1/reports/consolidated-stock")
      .then((d) => setEntries(d.entries))
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load consolidated stock"));
  }, []);

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Consolidated stock (all branches)</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {error && <p className="text-sm text-red-600">{error}</p>}
        {entries.map((e) => (
          <div key={e.variant_id} className="border-b pb-3 last:border-b-0">
            <p className="text-sm font-medium">
              {e.product_name} <span className="font-mono text-xs text-zinc-500">({e.sku})</span> — total {e.total_on_hand}
            </p>
            <div className="flex gap-4 text-xs text-zinc-600 mt-1 flex-wrap">
              {e.by_branch.map((b) => (
                <span key={b.branch_id}>
                  {b.branch_id.slice(0, 8)}…: {b.available} available
                </span>
              ))}
            </div>
          </div>
        ))}
        {entries.length === 0 && !error && <p className="text-sm text-zinc-500">No stock recorded anywhere yet.</p>}
      </CardContent>
    </Card>
  );
}
