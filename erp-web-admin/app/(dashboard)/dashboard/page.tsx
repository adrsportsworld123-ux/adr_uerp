"use client";

// The sales dashboard (erp-core-go internal/dashboard, phase0_1_design.md
// §3.62): daily, weekly and monthly sales, sales tracking against the
// month's target, tenders, top products and categories, branches and the
// hour of day, filtered by dates, branch and channel. Which widgets show is
// per role (dashboard.configure, under Customize); seeing it at all needs
// dashboard.view (Merchant Admin by default). Without it, this page keeps
// its shortcuts. It refreshes itself every two minutes while open.
import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { Loader2, RefreshCw, Settings2 } from "lucide-react";
import { api, ApiError } from "@/lib/api-client";
import { Branch } from "@/lib/types";
import { usePermissions } from "@/lib/permissions";
import { Button, buttonVariants } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { PageHeader } from "@/components/data/page-header";
import { useLiveRefresh } from "@/components/data/use-live-refresh";
import { BranchSales, HourlySales, Kpi, KpiCards, PaymentMix, Point, Ranking, SalesTracking, SalesTrend, Share, Tracking } from "@/components/dashboard/widgets";

const SHORTCUTS = [
  { href: "/customers", title: "Customers", description: "Profiles, auto-segmentation, and purchase history" },
  { href: "/suppliers/new", title: "New supplier", description: "Register a supplier before receiving stock from them" },
  { href: "/purchase/grn/new", title: "New GRN", description: "Record goods received from a supplier" },
  { href: "/purchase/bills", title: "Purchase bills", description: "Bill a completed GRN and track payments" },
  { href: "/accounting/day-book", title: "Day book", description: "Every journal entry posted today" },
  { href: "/accounting/party-ledger", title: "Party ledger", description: "What you owe each supplier" },
  { href: "/transfers/new", title: "New branch transfer", description: "Move stock from one branch to another" },
  { href: "/branches", title: "Branches", description: "Manage branches" },
  { href: "/pricing", title: "Pricing", description: "Margins, calculator, and bulk price updates" },
  { href: "/search", title: "Product Search", description: "Fast, typo-tolerant catalog search" },
];

type DashboardData = {
  widgets: string[];
  data: {
    kpis?: Kpi[];
    sales_tracking?: Tracking;
    daily_sales?: Point[];
    weekly_sales?: Point[];
    monthly_sales?: Point[];
    payment_mix?: Share[];
    top_products?: Share[];
    top_categories?: Share[];
    branch_sales?: Share[];
    hourly_sales?: Point[];
  };
};

const iso = (d: Date) => new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0, 10);
const PRESETS: { key: string; label: string; range: () => [string, string] }[] = [
  { key: "7", label: "Last 7 days", range: () => [iso(new Date(Date.now() - 6 * 864e5)), iso(new Date())] },
  { key: "30", label: "Last 30 days", range: () => [iso(new Date(Date.now() - 29 * 864e5)), iso(new Date())] },
  {
    key: "month",
    label: "This month",
    range: () => {
      const n = new Date();
      return [iso(new Date(n.getFullYear(), n.getMonth(), 1)), iso(n)];
    },
  },
  {
    key: "last_month",
    label: "Last month",
    range: () => {
      const n = new Date();
      return [iso(new Date(n.getFullYear(), n.getMonth() - 1, 1)), iso(new Date(n.getFullYear(), n.getMonth(), 0))];
    },
  },
  {
    key: "fy",
    label: "This financial year",
    range: () => {
      const n = new Date();
      const y = n.getMonth() >= 3 ? n.getFullYear() : n.getFullYear() - 1;
      return [iso(new Date(y, 3, 1)), iso(n)];
    },
  },
  { key: "90", label: "Last 90 days", range: () => [iso(new Date(Date.now() - 89 * 864e5)), iso(new Date())] },
];
const selectClass = "h-9 rounded-lg border bg-card px-2 text-sm";

export default function DashboardHome() {
  const { can } = usePermissions();
  const [branches, setBranches] = useState<Branch[]>([]);
  const [preset, setPreset] = useState("30");
  const [[from, to], setRange] = useState<[string, string]>(PRESETS[1].range());
  const [branchId, setBranchId] = useState("");
  const [channel, setChannel] = useState("");
  const [dash, setDash] = useState<DashboardData | null>(null);
  const [denied, setDenied] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    api.get<{ branches: Branch[] }>("/api/v1/branches?scope=mine").then((d) => setBranches(d.branches)).catch(() => {});
  }, []);

  const load = useCallback(() => {
    const qs = new URLSearchParams({ from, to, branch_id: branchId, channel });
    api
      .get<DashboardData>(`/api/v1/dashboard?${qs}`)
      .then((d) => {
        setDash(d);
        setError(null);
        setDenied(false);
      })
      .catch((e) => {
        if (e instanceof ApiError && e.status === 403) setDenied(true);
        else setError(e instanceof ApiError ? e.message : "Could not load the dashboard");
      })
      .finally(() => setLoading(false));
  }, [from, to, branchId, channel]);
  useEffect(load, [load]);
  useLiveRefresh(load, { intervalMs: 120000, enabled: !denied });

  if (denied) return <Shortcuts />;

  const d = dash?.data ?? {};
  const show = (k: string) => !!dash?.widgets.includes(k);
  const order = dash?.widgets ?? [];

  const render = (k: string) => {
    switch (k) {
      case "sales_tracking":
        return d.sales_tracking && <SalesTracking key={k} t={d.sales_tracking} />;
      case "daily_sales":
        return d.daily_sales && <SalesTrend key={k} title="Daily sales" points={d.daily_sales} wide testId="widget-daily_sales" />;
      case "weekly_sales":
        return d.weekly_sales && <SalesTrend key={k} title="Weekly sales" points={d.weekly_sales} kind="bar" testId="widget-weekly_sales" />;
      case "monthly_sales":
        return d.monthly_sales && <SalesTrend key={k} title="Monthly sales" points={d.monthly_sales} kind="bar" testId="widget-monthly_sales" />;
      case "payment_mix":
        return d.payment_mix && <PaymentMix key={k} shares={d.payment_mix} />;
      case "top_products":
        return d.top_products && <Ranking key={k} title="Top products" shares={d.top_products} testId="widget-top_products" />;
      case "top_categories":
        return d.top_categories && <Ranking key={k} title="Top categories" shares={d.top_categories} testId="widget-top_categories" />;
      case "branch_sales":
        return d.branch_sales && <BranchSales key={k} shares={d.branch_sales} />;
      case "hourly_sales":
        return d.hourly_sales && <HourlySales key={k} points={d.hourly_sales} />;
      default:
        return null;
    }
  };

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Dashboard"
        description="Sales at a glance. Completed sales at their bill total, counted on the day they were paid (India time)."
        actions={
          <>
            <Button variant="outline" size="sm" onClick={load} disabled={loading} aria-label="Refresh">
              {loading ? <Loader2 className="animate-spin" /> : <RefreshCw />} Refresh
            </Button>
            {can("dashboard.configure") && (
              <Link href="/dashboard/settings" className={buttonVariants({ variant: "outline", size: "sm" })}>
                <Settings2 /> Customize
              </Link>
            )}
          </>
        }
      />

      <div className="flex flex-wrap items-end gap-3 rounded-xl border bg-card p-3" role="group" aria-label="Dashboard filters">
        <div className="flex flex-col gap-1">
          <Label htmlFor="d-range">Dates</Label>
          <select
            id="d-range"
            value={preset}
            onChange={(e) => {
              setPreset(e.target.value);
              const p = PRESETS.find((x) => x.key === e.target.value);
              if (p) setRange(p.range());
            }}
            className={`${selectClass} w-44`}
          >
            {PRESETS.map((p) => (
              <option key={p.key} value={p.key}>
                {p.label}
              </option>
            ))}
            <option value="custom">Custom dates</option>
          </select>
        </div>
        <div className="flex flex-col gap-1">
          <Label htmlFor="d-from">From</Label>
          <Input id="d-from" type="date" value={from} max={to} onChange={(e) => (setPreset("custom"), setRange([e.target.value, to]))} className="w-40" />
        </div>
        <div className="flex flex-col gap-1">
          <Label htmlFor="d-to">To</Label>
          <Input id="d-to" type="date" value={to} min={from} onChange={(e) => (setPreset("custom"), setRange([from, e.target.value]))} className="w-40" />
        </div>
        <div className="flex flex-col gap-1">
          <Label htmlFor="d-branch">Branch</Label>
          <select id="d-branch" value={branchId} onChange={(e) => setBranchId(e.target.value)} className={`${selectClass} w-48`}>
            <option value="">All my branches</option>
            {branches.map((b) => (
              <option key={b.branch_id} value={b.branch_id}>
                {b.name}
              </option>
            ))}
          </select>
        </div>
        <div className="flex flex-col gap-1">
          <Label htmlFor="d-channel">Channel</Label>
          <select id="d-channel" value={channel} onChange={(e) => setChannel(e.target.value)} className={`${selectClass} w-36`}>
            <option value="">All channels</option>
            <option value="pos">Till</option>
            <option value="backoffice">Back office</option>
            <option value="online">Online</option>
          </select>
        </div>
        <p className="ml-auto text-xs text-muted-foreground">Weekly and monthly charts end at the “To” date; the headline figures and tracking are as of it.</p>
      </div>

      {error && (
        <p className="text-sm text-destructive" role="alert">
          {error}
        </p>
      )}
      {!dash && !error && <p className="text-sm text-muted-foreground">Loading the dashboard…</p>}

      {dash && (
        <>
          {show("kpis") && d.kpis && <KpiCards kpis={d.kpis} />}
          <div className="grid gap-4 lg:grid-cols-2">{order.filter((k) => k !== "kpis").map(render)}</div>
          {dash.widgets.length === 0 && (
            <p className="rounded-xl border bg-card p-6 text-center text-sm text-muted-foreground">Your role has no dashboard widgets chosen. An administrator can pick them under Customize.</p>
          )}
        </>
      )}
    </div>
  );
}

function Shortcuts() {
  return (
    <div className="flex max-w-3xl flex-col gap-6">
      <div>
        <h1 className="text-2xl font-semibold">Dashboard</h1>
        <p className="text-sm text-muted-foreground">Your role doesn&apos;t include the sales dashboard. These shortcuts get you to the everyday screens.</p>
      </div>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        {SHORTCUTS.map((s) => (
          <Link key={s.href} href={s.href}>
            <Card className="h-full transition-colors hover:border-primary/40">
              <CardHeader>
                <CardTitle className="text-base">{s.title}</CardTitle>
                <CardDescription>{s.description}</CardDescription>
              </CardHeader>
            </Card>
          </Link>
        ))}
      </div>
    </div>
  );
}
