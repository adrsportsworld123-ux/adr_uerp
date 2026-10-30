import { test, expect, APIRequestContext, Page } from "@playwright/test";

// erp-core-go §3.78 (migrations/075): an employee asks for leave, a
// permission and an attendance correction on My Leave & Payslips; HR
// approves on Leave & Requests and Approvals; leave types, balances and
// rules; the expense reports; printing a run's payslips and an employee
// seeing their own. The API flow (hr_flow) covers the arithmetic; this
// checks the screens drive it. Requests a test leaves behind are cancelled
// in finally. Leave and permission use next month, so a finalized payroll
// for this month doesn't get in the way.

const API = process.env.E2E_API_URL ?? "http://localhost:8080/api/v1";
const MERCHANT = process.env.E2E_MERCHANT_CODE ?? "acme-sports";
const BRANCH = "22222222-2222-2222-2222-222222222222";
const RAVI = "55555555-5555-5555-5555-555555555555";
const USERS = {
  arjun: ["arjun@acme-sports.test", "Passw0rd!"],
  meera: ["meera@acme-sports.test", "9999"],
  ravi: ["ravi@acme-sports.test", "Passw0rd!"],
} as const;

async function token(request: APIRequestContext, who: keyof typeof USERS = "arjun") {
  const [email, password] = USERS[who];
  const r = await request.post(`${API}/auth/login`, { data: { merchant_code: MERCHANT, email, password } });
  return (await r.json()).access_token as string;
}

async function login(page: Page, who: keyof typeof USERS) {
  await page.goto("/login");
  await page.fill("#email", USERS[who][0]);
  await page.fill("#password", USERS[who][1]);
  await page.click('button[type="submit"]');
  await page.waitForURL("/dashboard");
}

/** A day of next month, YYYY-MM-DD. */
function nextMonth(day: number) {
  const d = new Date();
  const y = d.getMonth() === 11 ? d.getFullYear() + 1 : d.getFullYear();
  const m = d.getMonth() === 11 ? 1 : d.getMonth() + 2;
  return `${y}-${String(m).padStart(2, "0")}-${String(day).padStart(2, "0")}`;
}

function istDay(offset: number) {
  const d = new Date(Date.now() + 5.5 * 3600_000 + offset * 86_400_000);
  return d.toISOString().slice(0, 10);
}

test.describe.configure({ mode: "serial" });

test("an employee asks for leave and a permission; HR approves both", async ({ page, request }) => {
  const auth = { Authorization: `Bearer ${await token(request)}` };
  const day = 3 + (Number(Date.now().toString().slice(-3)) % 20);
  const made: { path: string; id: string }[] = [];
  try {
    await login(page, "ravi");
    await page.goto("/my-hr");
    await expect(page.getByTestId("leave-balances")).toContainText("Casual leave");

    await page.getByRole("button", { name: "Ask for leave" }).click();
    await page.locator("#lv-type").selectOption({ label: "Casual leave" });
    await page.fill("#lv-from", nextMonth(day));
    await page.fill("#lv-to", nextMonth(day));
    await page.fill("#lv-reason", "E2E family function");
    const leaveResp = page.waitForResponse((r) => r.url().endsWith("/hr/leave-requests") && r.request().method() === "POST");
    await page.getByRole("button", { name: "Ask for leave" }).last().click();
    const leave = await (await leaveResp).json();
    made.push({ path: "leave-requests", id: leave.leave_request_id });
    await expect(page.getByText(`Leave asked (${leave.request_number})`)).toBeVisible();
    await expect(page.getByTestId("my-requests").getByRole("row", { name: new RegExp(leave.request_number) })).toContainText("Waiting");

    await page.getByRole("button", { name: "Permission" }).click();
    await page.fill("#pr-date", nextMonth(day + 1));
    await page.fill("#pr-from", "10:00");
    await page.fill("#pr-to", "11:30");
    await page.fill("#pr-reason", "E2E bank visit");
    const permResp = page.waitForResponse((r) => r.url().endsWith("/hr/permission-requests") && r.request().method() === "POST");
    await page.getByRole("button", { name: "Ask for permission" }).click();
    const perm = await (await permResp).json();
    made.push({ path: "permission-requests", id: perm.permission_request_id });
    await expect(page.getByText(`Permission asked (${perm.request_number})`)).toBeVisible();

    // HR (Meera) approves the leave on Leave & Requests …
    await login(page, "meera");
    await page.goto("/hr/leave");
    const row = page.getByTestId("hr-requests").getByRole("row", { name: new RegExp(leave.request_number) });
    await expect(row).toContainText("Ravi Kumar");
    await row.getByRole("button", { name: `Approve ${leave.request_number}` }).click();
    await page.getByRole("dialog").getByRole("button", { name: "Approve" }).click();
    await expect(page.getByText(`${leave.request_number} approved`)).toBeVisible();
    await page.locator("#lr-status").selectOption("approved");
    await expect(page.getByTestId("hr-requests").getByRole("row", { name: new RegExp(leave.request_number) })).toContainText("by Meera Nair");

    // … and the permission from Approvals.
    await page.goto("/approvals");
    const prow = page.getByTestId("approvals-waiting").getByRole("row", { name: new RegExp(perm.request_number) });
    await expect(prow).toContainText("Permission");
    await prow.getByRole("button", { name: `Approve ${perm.request_number}` }).click();
    await expect(page.getByText(`Permission ${perm.request_number} approved`)).toBeVisible();
    await page.getByRole("tab", { name: "Given" }).click();
    await page.locator("#given-kind").selectOption("permission");
    await expect(page.getByTestId("approvals-given").getByRole("row", { name: new RegExp(perm.request_number) })).toContainText("Meera");
  } finally {
    for (const m of made) await request.post(`${API}/hr/${m.path}/${m.id}/cancel`, { headers: auth, data: { reason: "e2e cleanup" } });
  }
});

test("an employee asks to add a missed day; HR's approval puts it on the attendance", async ({ page, request }) => {
  const auth = { Authorization: `Bearer ${await token(request)}` };
  const day = istDay(-(2 + (Number(Date.now().toString().slice(-4)) % 20)));
  await login(page, "ravi");
  await page.goto("/my-hr");
  await page.getByRole("button", { name: "Correct attendance" }).click();
  await page.fill("#rg-date", day);
  const missed = page.getByRole("radio", { name: "Add a check-in and check-out I missed" });
  await missed.check();
  await page.fill("#rg-in", "09:15");
  await page.fill("#rg-out", "18:15");
  await page.fill("#rg-reason", "E2E phone was dead");
  const resp = page.waitForResponse((r) => r.url().endsWith("/hr/regularizations") && r.request().method() === "POST");
  await page.getByRole("button", { name: "Ask for the correction" }).click();
  const reg = await (await resp).json();
  await expect(page.getByText(`Correction asked (${reg.request_number})`)).toBeVisible();
  await expect(page.getByTestId("my-requests")).toContainText("Add a missed day");

  const ok = await request.post(`${API}/hr/regularizations/${reg.regularization_id}/approve`, { headers: auth, data: {} });
  expect(ok.status()).toBe(200);
  const att = await (await request.get(`${API}/hr/attendance?user_id=${RAVI}&month=${day.slice(0, 7)}`, { headers: auth })).json();
  const rec = att.attendance.find((a: { work_date: string }) => a.work_date === day);
  expect(rec.sessions.some((s: { correction_reason?: string }) => s.correction_reason?.includes(reg.request_number))).toBe(true);
  await page.reload();
  await expect(page.getByTestId("my-requests").getByRole("row", { name: new RegExp(reg.request_number) })).toContainText("Approved");
});

test("leave types, an employee's balance and the rules", async ({ page, request }) => {
  const auth = { Authorization: `Bearer ${await token(request)}` };
  const code = `E${Date.now().toString().slice(-4)}`;
  try {
    await login(page, "arjun");
    await page.goto("/hr/leave?tab=types");
    await page.getByRole("button", { name: "New leave type" }).click();
    await page.fill("#lt-code", code);
    await page.fill("#lt-name", `E2E leave ${code}`);
    await page.fill("#lt-quota", "5");
    await page.getByRole("dialog").getByRole("button", { name: "Save" }).click();
    await expect(page.getByText(`E2E leave ${code} saved`)).toBeVisible();
    const row = page.getByTestId("leave-types").getByRole("row", { name: new RegExp(code) });
    await expect(row).toContainText("5");
    await row.getByRole("button", { name: `Deactivate E2E leave ${code}` }).click();
    await expect(row).toContainText("Inactive");

    await page.getByRole("tab", { name: "Balances" }).click();
    await page.locator("#lb-who").click();
    await page.getByRole("option", { name: /Ravi Kumar/ }).click();
    await expect(page.getByTestId("leave-balances")).toContainText("Sick leave");
    await page.getByRole("button", { name: "Set Casual leave allowance" }).click();
    await page.fill("#al-days", "10");
    await page.getByRole("dialog").getByRole("button", { name: "Save" }).click();
    await expect(page.getByText("Casual leave allowance set to 10 day(s)")).toBeVisible();
    await expect(page.getByTestId("leave-balances").getByRole("row", { name: /Casual leave/ })).toContainText("own allowance");

    await page.getByRole("tab", { name: "Rules" }).click();
    await page.fill("#ru-count", "3");
    await page.getByRole("button", { name: "Save rules" }).click();
    await expect(page.getByText("Rules saved")).toBeVisible();
    const s = await (await request.get(`${API}/hr/attendance/settings`, { headers: auth })).json();
    expect(s.permission_free_count).toBe(3);
  } finally {
    await request.put(`${API}/hr/attendance/settings`, { headers: auth, data: { permission_free_count: 2 } });
    const year = Number(istDay(0).slice(0, 4));
    const types = (await (await request.get(`${API}/hr/leave-types`, { headers: auth })).json()).leave_types;
    const cl = types.find((t: { code: string }) => t.code === "CL");
    await request.put(`${API}/hr/leave-balances`, { headers: auth, data: { user_id: RAVI, leave_type_id: cl.leave_type_id, leave_year: year, days: null } });
  }
});

test("the expense reports, and a run's payslips printed and seen by the employee", async ({ page, request }) => {
  const auth = { Authorization: `Bearer ${await token(request)}` };
  const heads = (await (await request.get(`${API}/expense-heads`, { headers: auth })).json()).heads;
  const rent = heads.find((h: { name: string }) => h.name === "Shop Rent");
  const ex = await request.post(`${API}/expenses`, {
    headers: auth,
    data: { branch_id: BRANCH, expense_date: istDay(0), head_id: rent.head_id, amount: 1234, paid_by: "cash", vendor: "E2E landlord" },
  });
  expect(ex.status()).toBe(201);

  await login(page, "arjun");
  await page.goto("/reports/library");
  await page.getByRole("button", { name: "Expenses by head and month" }).click();
  await page.getByRole("button", { name: "Run report" }).click();
  await expect(page.getByRole("columnheader", { name: "Amount" })).toBeVisible();
  await expect(page.getByRole("cell", { name: "Shop Rent" }).first()).toBeVisible();

  // A payroll run far ahead, so it never meets a real month's.
  const month = 1 + (Number(Date.now().toString().slice(-2)) % 12);
  await request.post(`${API}/hr/employees/${RAVI}/salary-structure`, {
    headers: auth,
    data: { effective_from: "2024-01-01", basic: 25000, hra: 5000, special_allowance: 0, other_allowances: 0, pf_applicable: true },
  });
  const run = await (await request.post(`${API}/payroll/runs`, { headers: auth, data: { period_month: month, period_year: 2031 } })).json();
  await page.goto(`/payroll/runs/${run.id}/print`);
  const sheet = page.getByTestId("payslip-sheet").filter({ hasText: "Ravi Kumar" });
  await expect(sheet).toContainText("PAYSLIP");
  await expect(sheet).toContainText("Net pay in words");
  await expect(sheet).toContainText("Provident fund");
  const pdf = await request.get(`${API}/payroll/payslips/${run.payslips.find((p: { user_id: string }) => p.user_id === RAVI).payslip_id}/pdf`, { headers: auth });
  expect((await pdf.body()).subarray(0, 4).toString()).toBe("%PDF");

  if (run.status !== "finalized") await request.post(`${API}/payroll/runs/${run.id}/finalize`, { headers: auth, data: {} });
  await login(page, "ravi");
  await page.goto("/my-hr");
  const mine = page.getByTestId("my-payslips").getByRole("row", { name: new RegExp(`2031`) }).first();
  await expect(mine).toBeVisible();
  const download = page.waitForEvent("download");
  await mine.getByRole("button", { name: "Download" }).click();
  expect((await download).suggestedFilename()).toMatch(/^payslip-EMP001-2031-\d+\.pdf$/);
});
