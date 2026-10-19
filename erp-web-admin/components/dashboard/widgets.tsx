"use client";

// The sales dashboard's widgets (erp-core-go internal/dashboard). Each one
// takes its own slice of GET /dashboard's data. Money arrives as NUMERIC
// strings; it's turned into numbers here only to draw it, never sent back.
import { ReactNode } from "react";
import {
  Area,
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  ComposedChart,
  Legend,
  Line,
  LineChart,
  Pie,
  PieChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { ArrowDownRight, ArrowUpRight, Minus, Target } from "lucide-react";
import { METHOD_LABEL, rupees } from "@/lib/format";

export type Point = { period: string; label: string; sales: string; orders: number; prior?: string };
export type Kpi = { key: string; label: string; value: string; previous: string; change_pct: number | null; orders: number; hint: string };
export type Share = { key: string; label: string; value: string; count: number; quantity?: string };
export type Tracking = {
  month: string;
  target: string | null;
  month_to_date: string;
  last_month_same_days: string;
  projected: string;
  achieved_pct: number | null;
  projected_pct: number | null;
  required_per_day: string | null;
  days_left: number;
  fy_label: string;
  fy_to_date: string;
  fy_prev_same_period: string;
  fy_change_pct: number | null;
  cumulative: { day: number; this_month: string | null; last_month: string | null; target: string | null }[];
};

const C = ["var(--chart-1)", "var(--chart-2)", "var(--chart-3)", "var(--chart-4)", "var(--chart-5)"];

/** ₹ in lakhs and crores for chart axes: 1250000 → "₹12.5L". */
export function compactINR(v: number): string {
  const a = Math.abs(v);
  const s = v < 0 ? "−" : "";
  if (a >= 1e7) return `${s}₹${+(a / 1e7).toFixed(2)}Cr`;
  if (a >= 1e5) return `${s}₹${+(a / 1e5).toFixed(1)}L`;
  if (a >= 1e3) return `${s}₹${+(a / 1e3).toFixed(1)}k`;
  return `${s}₹${Math.round(a)}`;
}

const num = (s: string | null | undefined) => (s == null ? null : Number(s));

export function Card({ title, subtitle, children, className = "", testId }: { title: string; subtitle?: ReactNode; children: ReactNode; className?: string; testId?: string }) {
  return (
    <section className={`flex min-w-0 flex-col gap-3 rounded-xl border bg-card p-4 ${className}`} data-testid={testId} aria-label={title}>
      <div>
        <h2 className="text-sm font-semibold">{title}</h2>
        {subtitle && <p className="text-xs text-muted-foreground">{subtitle}</p>}
      </div>
      {children}
    </section>
  );
}

function Change({ pct }: { pct: number | null }) {
  if (pct == null) return <span className="text-xs text-muted-foreground">no earlier sales to compare</span>;
  const up = pct > 0;
  const flat = pct === 0;
  const Icon = flat ? Minus : up ? ArrowUpRight : ArrowDownRight;
  return (
    <span className={`inline-flex items-center gap-0.5 rounded-full px-1.5 py-0.5 text-xs font-medium ${flat ? "bg-muted text-muted-foreground" : up ? "bg-pay-soft text-pay" : "bg-destructive/10 text-destructive"}`}>
      <Icon className="size-3" aria-hidden />
      {up ? "+" : ""}
      {pct}%
    </span>
  );
}

const tooltipStyle = { borderRadius: 8, border: "1px solid var(--border)", background: "var(--popover)", color: "var(--popover-foreground)", fontSize: 12 };
const axisTick = { fontSize: 11, fill: "var(--muted-foreground)" };
const moneyTip = (v: unknown) => rupees(Number(v));

export function KpiCards({ kpis }: { kpis: Kpi[] }) {
  return (
    <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4" data-testid="widget-kpis">
      {kpis.map((k) => (
        <div key={k.key} className="rounded-xl border bg-card p-4" data-testid={`kpi-${k.key}`}>
          <p className="text-xs font-medium text-muted-foreground">{k.label}</p>
          <p className="tabular mt-1 text-2xl font-semibold tracking-tight">{rupees(k.value)}</p>
          <div className="mt-1 flex flex-wrap items-center gap-2">
            <Change pct={k.change_pct} />
            <span className="text-xs text-muted-foreground">{k.key === "avg_bill" ? `${k.orders} bills` : `${k.orders} bill${k.orders === 1 ? "" : "s"}`}</span>
          </div>
          <p className="mt-1 text-xs text-muted-foreground" title={`Before: ${rupees(k.previous)}`}>
            {k.hint} ({rupees(k.previous)})
          </p>
        </div>
      ))}
    </div>
  );
}

export function SalesTracking({ t }: { t: Tracking }) {
  const achieved = Math.min(t.achieved_pct ?? 0, 100);
  const projected = Math.min(t.projected_pct ?? 0, 100);
  const data = t.cumulative.map((c) => ({ day: c.day, "This month": num(c.this_month), "Last month": num(c.last_month), Target: num(c.target) }));
  return (
    <Card title="Sales tracking" subtitle={`${t.month} so far, against ${t.target ? "its target and " : ""}last month`} className="lg:col-span-2" testId="widget-sales_tracking">
      <div className="grid gap-3 sm:grid-cols-4">
        <Stat label="Month to date" value={rupees(t.month_to_date)} sub={`last month, same days: ${rupees(t.last_month_same_days)}`} />
        <Stat label="Projected month" value={rupees(t.projected)} sub={t.projected_pct != null ? `${t.projected_pct}% of target` : "at the current pace"} />
        <Stat
          label="Target"
          value={t.target ? rupees(t.target) : "Not set"}
          sub={t.required_per_day ? `${rupees(t.required_per_day)} a day for the ${t.days_left} day${t.days_left === 1 ? "" : "s"} left` : t.target ? "the month is over" : "set one in Customize"}
        />
        <Stat label={t.fy_label} value={rupees(t.fy_to_date)} sub={<span className="inline-flex items-center gap-1">vs last year <Change pct={t.fy_change_pct} /></span>} />
      </div>
      {t.target && (
        <div>
          <div className="mb-1 flex items-center justify-between text-xs">
            <span className="inline-flex items-center gap-1 font-medium">
              <Target className="size-3.5" aria-hidden /> {t.achieved_pct}% of target
            </span>
            <span className="text-muted-foreground">projected {t.projected_pct}%</span>
          </div>
          <div className="relative h-2.5 overflow-hidden rounded-full bg-muted" role="progressbar" aria-valuenow={t.achieved_pct ?? 0} aria-valuemin={0} aria-valuemax={100} aria-label="Share of the month's target reached">
            <div className="absolute inset-y-0 left-0 rounded-full bg-primary/25" style={{ width: `${projected}%` }} />
            <div className="absolute inset-y-0 left-0 rounded-full bg-primary" style={{ width: `${achieved}%` }} />
          </div>
        </div>
      )}
      <div className="h-56">
        <ResponsiveContainer width="100%" height="100%">
          <LineChart data={data} margin={{ top: 8, right: 8, left: 0, bottom: 0 }}>
            <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
            <XAxis dataKey="day" tick={axisTick} tickLine={false} axisLine={false} />
            <YAxis tickFormatter={compactINR} tick={axisTick} tickLine={false} axisLine={false} width={56} />
            <Tooltip formatter={moneyTip} labelFormatter={(d) => `Day ${d}`} contentStyle={tooltipStyle} />
            <Legend wrapperStyle={{ fontSize: 12 }} />
            <Line type="monotone" dataKey="This month" stroke={C[0]} strokeWidth={2.5} dot={false} connectNulls={false} />
            <Line type="monotone" dataKey="Last month" stroke={C[4]} strokeWidth={1.5} strokeDasharray="4 3" dot={false} />
            {t.target && <Line type="linear" dataKey="Target" stroke={C[2]} strokeWidth={1.5} dot={false} />}
          </LineChart>
        </ResponsiveContainer>
      </div>
    </Card>
  );
}

function Stat({ label, value, sub }: { label: string; value: string; sub?: ReactNode }) {
  return (
    <div className="rounded-lg bg-muted/50 p-3">
      <p className="text-xs text-muted-foreground">{label}</p>
      <p className="tabular text-lg font-semibold">{value}</p>
      {sub && <p className="text-xs text-muted-foreground">{sub}</p>}
    </div>
  );
}

function total(points: Point[]) {
  return points.reduce((s, p) => s + Number(p.sales), 0);
}

export function SalesTrend({
  title,
  points,
  subtitle,
  kind = "area",
  wide = false,
  testId,
}: {
  title: string;
  points: Point[];
  subtitle?: string;
  kind?: "area" | "bar";
  wide?: boolean;
  testId?: string;
}) {
  const data = points.map((p) => ({ label: p.label, Sales: Number(p.sales), Bills: p.orders, "A year before": p.prior != null ? Number(p.prior) : undefined }));
  const hasPrior = points.some((p) => p.prior != null && Number(p.prior) > 0);
  return (
    <Card title={title} subtitle={subtitle ?? `${rupees(total(points))} in all · ${points.reduce((s, p) => s + p.orders, 0)} bills`} className={wide ? "lg:col-span-2" : ""} testId={testId}>
      <div className="h-60">
        <ResponsiveContainer width="100%" height="100%">
          <ComposedChart data={data} margin={{ top: 8, right: 8, left: 0, bottom: 0 }}>
            <defs>
              <linearGradient id={`fill-${testId}`} x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor={C[0]} stopOpacity={0.35} />
                <stop offset="100%" stopColor={C[0]} stopOpacity={0.02} />
              </linearGradient>
            </defs>
            <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
            <XAxis dataKey="label" tick={axisTick} tickLine={false} axisLine={false} minTickGap={12} />
            <YAxis yAxisId="m" tickFormatter={compactINR} tick={axisTick} tickLine={false} axisLine={false} width={56} />
            <YAxis yAxisId="n" orientation="right" tick={axisTick} tickLine={false} axisLine={false} width={32} allowDecimals={false} />
            <Tooltip formatter={(v, n) => (n === "Bills" ? v : rupees(Number(v)))} contentStyle={tooltipStyle} />
            <Legend wrapperStyle={{ fontSize: 12 }} />
            {kind === "area" ? (
              <Area yAxisId="m" type="monotone" dataKey="Sales" stroke={C[0]} strokeWidth={2} fill={`url(#fill-${testId})`} />
            ) : (
              <Bar yAxisId="m" dataKey="Sales" fill={C[0]} radius={[4, 4, 0, 0]} maxBarSize={36} />
            )}
            {hasPrior && <Bar yAxisId="m" dataKey="A year before" fill={C[4]} fillOpacity={0.45} radius={[4, 4, 0, 0]} maxBarSize={36} />}
            <Line yAxisId="n" type="monotone" dataKey="Bills" stroke={C[2]} strokeWidth={1.5} dot={false} />
          </ComposedChart>
        </ResponsiveContainer>
      </div>
    </Card>
  );
}

export function PaymentMix({ shares }: { shares: Share[] }) {
  const sum = shares.reduce((s, x) => s + Number(x.value), 0);
  const data = shares.map((s) => ({ name: METHOD_LABEL[s.key] ?? s.label, value: Number(s.value), count: s.count }));
  return (
    <Card title="Payment mix" subtitle={`${rupees(sum)} taken`} testId="widget-payment_mix">
      {data.length === 0 ? (
        <Empty />
      ) : (
        <div className="flex flex-wrap items-center gap-4">
          <div className="h-44 w-44">
            <ResponsiveContainer width="100%" height="100%">
              <PieChart>
                <Pie data={data} dataKey="value" nameKey="name" innerRadius="58%" outerRadius="95%" paddingAngle={2} stroke="none">
                  {data.map((_, i) => (
                    <Cell key={i} fill={C[i % C.length]} />
                  ))}
                </Pie>
                <Tooltip formatter={moneyTip} contentStyle={tooltipStyle} />
              </PieChart>
            </ResponsiveContainer>
          </div>
          <ul className="flex min-w-40 flex-1 flex-col gap-1.5 text-sm">
            {data.map((d, i) => (
              <li key={d.name} className="flex items-center justify-between gap-3">
                <span className="flex items-center gap-2">
                  <span className="size-2.5 rounded-full" style={{ background: C[i % C.length] }} aria-hidden />
                  {d.name}
                </span>
                <span className="tabular text-right">
                  {rupees(d.value)} <span className="text-xs text-muted-foreground">{sum ? Math.round((d.value / sum) * 100) : 0}%</span>
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </Card>
  );
}

export function Ranking({ title, shares, unit, testId }: { title: string; shares: Share[]; unit?: string; testId?: string }) {
  const top = Math.max(...shares.map((s) => Number(s.value)), 1);
  return (
    <Card title={title} subtitle={shares.length ? `Top ${shares.length} by value` : undefined} testId={testId}>
      {shares.length === 0 ? (
        <Empty />
      ) : (
        <ol className="flex flex-col gap-2.5">
          {shares.map((s, i) => (
            <li key={s.key || s.label} className="text-sm">
              <div className="flex items-baseline justify-between gap-3">
                <span className="min-w-0 truncate">
                  <span className="mr-1.5 text-xs text-muted-foreground">{i + 1}</span>
                  {s.label}
                </span>
                <span className="tabular shrink-0 font-medium">{rupees(s.value)}</span>
              </div>
              <div className="mt-1 flex items-center gap-2">
                <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-muted">
                  <div className="h-full rounded-full" style={{ width: `${(Number(s.value) / top) * 100}%`, background: C[i % 2 === 0 ? 0 : 4] }} />
                </div>
                <span className="w-24 shrink-0 text-right text-xs text-muted-foreground">
                  {s.quantity != null ? `${Number(s.quantity)} ${unit ?? "sold"}` : `${s.count} bills`}
                </span>
              </div>
            </li>
          ))}
        </ol>
      )}
    </Card>
  );
}

export function BranchSales({ shares }: { shares: Share[] }) {
  const data = shares.map((s) => ({ name: s.label, Sales: Number(s.value), Bills: s.count }));
  return (
    <Card title="Sales by branch" subtitle={`${shares.length} branch${shares.length === 1 ? "" : "es"}`} testId="widget-branch_sales">
      {data.length === 0 ? (
        <Empty />
      ) : (
        <div style={{ height: Math.max(120, data.length * 44) }}>
          <ResponsiveContainer width="100%" height="100%">
            <BarChart data={data} layout="vertical" margin={{ top: 0, right: 16, left: 0, bottom: 0 }}>
              <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" horizontal={false} />
              <XAxis type="number" tickFormatter={compactINR} tick={axisTick} tickLine={false} axisLine={false} />
              <YAxis type="category" dataKey="name" tick={axisTick} tickLine={false} axisLine={false} width={110} />
              <Tooltip formatter={(v, n) => (n === "Bills" ? v : rupees(Number(v)))} contentStyle={tooltipStyle} />
              <Bar dataKey="Sales" fill={C[1]} radius={[0, 4, 4, 0]} maxBarSize={26} />
            </BarChart>
          </ResponsiveContainer>
        </div>
      )}
    </Card>
  );
}

export function HourlySales({ points }: { points: Point[] }) {
  const data = points.map((p) => ({ label: p.label.slice(0, 2), Sales: Number(p.sales), Bills: p.orders }));
  const busiest = points.reduce<Point | null>((b, p) => (!b || Number(p.sales) > Number(b.sales) ? p : b), null);
  return (
    <Card title="Sales by hour" subtitle={busiest && Number(busiest.sales) > 0 ? `Busiest: ${busiest.label}` : undefined} testId="widget-hourly_sales">
      <div className="h-52">
        <ResponsiveContainer width="100%" height="100%">
          <BarChart data={data} margin={{ top: 8, right: 8, left: 0, bottom: 0 }}>
            <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
            <XAxis dataKey="label" tick={axisTick} tickLine={false} axisLine={false} interval={1} />
            <YAxis tickFormatter={compactINR} tick={axisTick} tickLine={false} axisLine={false} width={56} />
            <Tooltip formatter={(v, n) => (n === "Bills" ? v : rupees(Number(v)))} labelFormatter={(h) => `${h}:00`} contentStyle={tooltipStyle} />
            <Bar dataKey="Sales" fill={C[3]} radius={[3, 3, 0, 0]} />
          </BarChart>
        </ResponsiveContainer>
      </div>
    </Card>
  );
}

function Empty() {
  return <p className="py-8 text-center text-sm text-muted-foreground">No sales in these dates.</p>;
}
