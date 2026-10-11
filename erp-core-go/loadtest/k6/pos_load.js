// Phase 16 load-test gate (phased_roadmap.md Phase 16; tech_stack_decision.md
// §9: k6). FRD Part A's targets as pass/fail thresholds:
//   - a POS transaction (cart, scan, two lines, payment) completes in ≤ 6 s;
//   - a product lookup at the till answers in ≤ 100 ms;
//   - a dashboard report loads in ≤ 3 s;
//   - 10,000 transactions an hour, across several tenants and terminals.
//
// setup() onboards TENANTS fresh Enterprise merchants through the SaaS
// console (so plan limits never interfere and each run is isolated), each
// with TILLS tills, two products and plenty of stock. Every iteration is a
// whole sale on a random tenant and till.
//
// Run (from erp-core-go, against a stack with DEV_AUTH_TOOLS / a real
// platform operator password):
//   docker run --rm -i --network erp-core-go_default -e BASE=http://<api>:8080 \
//     -e OPS_EMAIL=ops@adrsw.com -e OPS_PASSWORD=... grafana/k6 run - < loadtest/k6/pos_load.js
// Knobs: TENANTS (3), TILLS (4), RATE (txn/s, default 2.78 = 10,000/h),
// DURATION (5m), PEAK (2: then RAMP, 1m, to RATE × PEAK and hold it PEAK_HOLD, 2m).
import http from "k6/http";
import { check, fail } from "k6";
import { Counter, Trend } from "k6/metrics";

const BASE = (__ENV.BASE || "http://localhost:8080") + "/api/v1";
const TENANTS = Number(__ENV.TENANTS || 3);
const TILLS = Number(__ENV.TILLS || 4);
const RATE = Number(__ENV.RATE || 10000 / 3600);
const DURATION = __ENV.DURATION || "5m";
const PEAK = Number(__ENV.PEAK || 2);
const RAMP = __ENV.RAMP || "1m";
const PEAK_HOLD = __ENV.PEAK_HOLD || "2m";

const txn = new Trend("pos_transaction_ms", true);
const lookup = new Trend("pos_lookup_ms", true);
const dashboard = new Trend("dashboard_ms", true);
const search = new Trend("product_search_ms", true);
const sales = new Counter("sales_completed");
const failedSales = new Counter("sales_failed");

// k6 arrival rates are per time unit; express fractional rates per minute.
const perMinute = (r) => Math.max(1, Math.round(r * 60));

export const options = {
  setupTimeout: "5m",
  scenarios: {
    checkout: {
      executor: "ramping-arrival-rate",
      exec: "sale",
      timeUnit: "1m",
      startRate: perMinute(RATE),
      preAllocatedVUs: 20,
      maxVUs: 200,
      stages: [
        { target: perMinute(RATE), duration: DURATION }, // sustained target
        { target: perMinute(RATE * PEAK), duration: RAMP }, // ramp to peak
        { target: perMinute(RATE * PEAK), duration: PEAK_HOLD }, // hold peak
      ],
    },
    lookups: { executor: "constant-arrival-rate", exec: "lookupOnly", rate: 120, timeUnit: "1m", duration: DURATION, preAllocatedVUs: 5, maxVUs: 30 },
    dashboards: { executor: "constant-arrival-rate", exec: "dashboardOnly", rate: 12, timeUnit: "1m", duration: DURATION, preAllocatedVUs: 2, maxVUs: 10 },
  },
  thresholds: {
    pos_transaction_ms: ["p(95)<6000"],
    pos_lookup_ms: ["p(95)<100"],
    dashboard_ms: ["p(95)<3000"],
    http_req_failed: ["rate<0.01"],
    "checks{kind:sale}": ["rate>0.99"],
  },
};

function json(method, path, body, token) {
  const params = { headers: { "Content-Type": "application/json" }, tags: { name: path.replace(/[0-9a-f-]{36}/g, ":id").replace(/\/products\/barcode\/\d+/, "/products/barcode/:code") } };
  if (token) params.headers.Authorization = `Bearer ${token}`;
  const r = http.request(method, BASE + path, body === undefined ? null : JSON.stringify(body), params);
  return r;
}

function must(r, what) {
  if (r.status < 200 || r.status >= 300) fail(`${what}: ${r.status} ${r.body}`);
  return r.json();
}

export function setup() {
  const ops = must(json("POST", "/platform/auth/login", { email: __ENV.OPS_EMAIL || "ops@adrsw.com", password: __ENV.OPS_PASSWORD || "" }), "platform login");
  const run = Date.now() % 1000000;
  const tenants = [];
  for (let i = 0; i < TENANTS; i++) {
    const code = `lt-${run}-${i}`;
    const m = must(
      json("POST", "/platform/merchants", {
        code, legal_name: `Load test ${code}`, plan_code: "enterprise",
        branch: { name: "Main", code: "MAIN" },
        admin: { name: "Load Owner", email: `owner@${code}.test`, employee_code: "LT1", password: "Passw0rd!" },
        reason: "k6 load test",
      }, ops.access_token),
      "onboard",
    );
    const t = must(json("POST", "/auth/login", { merchant_code: code, email: `owner@${code}.test`, password: "Passw0rd!" }), "owner login").access_token;
    const slab = must(json("POST", "/tax-slabs", { name: "GST 18", cgst_rate: 9, sgst_rate: 9, igst_rate: 0, cess_rate: 0 }, t), "slab");
    const items = [];
    for (let p = 0; p < 2; p++) {
      const prod = must(json("POST", "/products", { name: `Load item ${p}`, tax_slab_id: slab.tax_slab_id,
        variants: [{ sku: `LT-${code}-${p}`, cost_price: 40, mrp: 120, selling_price: 100, generate_barcode: true }] }, t), "product");
      const v = prod.variants[0];
      must(json("POST", "/inventory/adjustments", { variant_id: v.variant_id, branch_id: m.branch_id, quantity_delta: 100000, reason: "load test stock" }, t), "stock");
      items.push({ variant: v.variant_id, barcode: v.generated_barcode });
    }
    const tills = [];
    for (let k = 0; k < TILLS; k++) tills.push(must(json("POST", "/pos-terminals", { branch_id: m.branch_id, name: `Till ${k + 1}` }, t), "till").terminal_id);
    tenants.push({ code, token: t, branch: m.branch_id, tills, items });
  }
  return { tenants };
}

const pick = (a) => a[Math.floor(Math.random() * a.length)];

export function sale(data) {
  const ten = pick(data.tenants);
  const till = pick(ten.tills);
  const start = Date.now();
  let ok = false;
  const order = json("POST", "/sales/orders", { branch_id: ten.branch, pos_terminal_id: till, idempotency_key: `${__VU}-${__ITER}-${Date.now()}-${Math.random()}` }, ten.token);
  if (order.status === 201 || order.status === 200) {
    const id = order.json().order_id;
    let total = 0;
    let lines = true;
    for (const it of ten.items) {
      const scan = json("GET", `/products/barcode/${it.barcode}`, undefined, ten.token);
      lookup.add(scan.timings.duration);
      const l = json("POST", `/sales/orders/${id}/lines`, { variant_id: it.variant, quantity: 1 }, ten.token);
      if (l.status !== 201 && l.status !== 200) lines = false;
      else total = Number(l.json().grand_total);
    }
    if (lines) {
      const c = json("POST", `/sales/orders/${id}/checkout`, { payments: [{ method: "cash", amount: total }] }, ten.token);
      ok = c.status === 200;
    }
  }
  txn.add(Date.now() - start);
  check(ok, { "sale completed": (v) => v }, { kind: "sale" });
  if (ok) sales.add(1);
  else failedSales.add(1);
}

export function lookupOnly(data) {
  const ten = pick(data.tenants);
  const r = json("GET", `/products/barcode/${pick(ten.items).barcode}`, undefined, ten.token);
  lookup.add(r.timings.duration);
  const s = json("GET", "/products/search?q=load", undefined, ten.token);
  if (s.status === 200) search.add(s.timings.duration);
}

export function dashboardOnly(data) {
  const ten = pick(data.tenants);
  const today = new Date(Date.now() + 5.5 * 3600 * 1000).toISOString().slice(0, 10);
  const r = json("GET", `/reports/daily-sales?branch_id=${ten.branch}&date=${today}`, undefined, ten.token);
  dashboard.add(r.timings.duration);
  check(r, { "dashboard ok": (x) => x.status === 200 });
}

export function handleSummary(data) {
  const m = data.metrics;
  const v = (k, s) => (m[k] && m[k].values[s] != null ? m[k].values[s].toFixed(1) : "n/a");
  const lines = [
    `POS transactions: ${m.sales_completed ? m.sales_completed.values.count : 0} completed, ${m.sales_failed ? m.sales_failed.values.count : 0} failed`,
    `  transaction ms  p50 ${v("pos_transaction_ms", "med")}  p95 ${v("pos_transaction_ms", "p(95)")}  max ${v("pos_transaction_ms", "max")}  (target p95 < 6000)`,
    `  barcode lookup ms  p50 ${v("pos_lookup_ms", "med")}  p95 ${v("pos_lookup_ms", "p(95)")}  (target p95 < 100)`,
    `  product search ms  p50 ${v("product_search_ms", "med")}  p95 ${v("product_search_ms", "p(95)")}`,
    `  dashboard ms  p50 ${v("dashboard_ms", "med")}  p95 ${v("dashboard_ms", "p(95)")}  (target p95 < 3000)`,
    `  http errors ${(m.http_req_failed.values.rate * 100).toFixed(2)}%  requests ${m.http_reqs.values.count} (${m.http_reqs.values.rate.toFixed(1)}/s)`,
    `  thresholds: ${Object.entries(data.metrics).filter(([, x]) => x.thresholds).map(([k, x]) => `${k} ${Object.values(x.thresholds).every((t) => t.ok) ? "ok" : "FAILED"}`).join(", ")}`,
  ];
  return { stdout: lines.join("\n") + "\n", "/tmp/k6-summary.json": JSON.stringify(data, null, 2) };
}
