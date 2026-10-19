import { test, expect, Page, APIRequestContext } from "@playwright/test";

// The reporting round (erp-core-go phase0_1_design.md §3.60–§3.63):
// - a journal voucher records its party: a receivables line must name the
//   customer, the voucher and journal show it, and the party ledger has it;
// - X reports by shift and the day's Z report, with closing a day;
// - the sales dashboard, its per-role widgets and the sales target;
// - the report library, saving a report, and regenerating an export.
// Each test creates what it needs and puts shared settings back.

const API = "http://localhost:8081/api/v1";
const MERCHANT = process.env.E2E_MERCHANT_CODE ?? "acme-sports";
const BRANCH = "22222222-2222-2222-2222-222222222222";

async function login(page: Page, email: string) {
  await page.evaluate(() => localStorage.clear()).catch(() => {});
  await page.goto("/login");
  await page.fill("#email", email);
  await page.fill("#password", "Passw0rd!");
  await page.click('button[type="submit"]');
  await page.waitForURL("/dashboard");
}

async function auth(request: APIRequestContext, email: string) {
  const r = await request.post(`${API}/auth/login`, { data: { merchant_code: MERCHANT, email, password: "Passw0rd!" } });
  return { Authorization: `Bearer ${(await r.json()).access_token}` };
}

test("a journal voucher records its party, and the party ledger shows it", async ({ page, request }) => {
  const admin = await auth(request, "arjun@acme-sports.test");
  await request.put(`${API}/accounting/settings`, { headers: admin, data: { jv_approval_required: false, locked_through: null } });
  const name = `E2E Party ${Date.now()}`;
  const customer = await (await request.post(`${API}/customers`, { headers: admin, data: { name, phone: `9${String(Date.now()).slice(-9)}` } })).json();

  await login(page, "arjun@acme-sports.test");
  await page.goto("/accounting/vouchers/new");
  await page.getByLabel("Description").fill(`Opening balance ${name}`);
  await page.getByLabel("Line 1 account").selectOption("1100");
  // Receivables picks the customer type and needs the customer itself.
  await expect(page.getByLabel("Line 1 party type")).toHaveValue("customer");
  await page.getByLabel("Line 1 debit").fill("250");
  await page.getByLabel("Line 2 account").selectOption("4002");
  await page.getByLabel("Line 2 credit").fill("250");
  await expect(page.getByTestId("jv-balance")).toHaveText("Balanced");
  await expect(page.getByTestId("jv-party-error")).toHaveText("Choose the customer");
  await expect(page.getByRole("button", { name: "Post now" })).toBeDisabled();

  await page.getByLabel("Line 1 party", { exact: true }).click();
  await page.getByLabel("Name, phone or GSTIN").fill(name);
  await page.getByRole("option", { name: new RegExp(name) }).click();
  await expect(page.getByTestId("jv-party-error")).toHaveCount(0);
  await page.getByRole("button", { name: "Post now" }).click();

  await page.waitForURL(/\/accounting\/vouchers\/[0-9a-f-]{36}$/);
  const partyCell = page.getByTestId("jv-line-party").first();
  await expect(partyCell).toContainText(name);
  await expect(partyCell).toContainText("Customer");

  const voucherId = page.url().split("/").pop()!;
  const v = await (await request.get(`${API}/accounting/vouchers/${voucherId}`, { headers: admin })).json();
  const entry = await (await request.get(`${API}/accounting/journal-entries/${v.entry_number}`, { headers: admin })).json();
  expect(entry.lines.find((l: { party_id: string | null }) => l.party_id === customer.customer_id)?.party_name).toBe(name);

  await page.goto(`/accounting/journal-entries/${entry.entry_id}`);
  await page.getByRole("link", { name: new RegExp(name) }).click();
  await page.waitForURL(/party-ledger/);
  await expect(page.getByTestId("party-balance")).toHaveText("₹250.00");
  await expect(page.getByRole("link", { name: v.entry_number })).toBeVisible();
});

test("X reports by shift and the day's Z report, with closing a day", async ({ page, request }) => {
  const admin = await auth(request, "arjun@acme-sports.test");
  const cashier = await auth(request, "ravi@acme-sports.test");
  const till = `E2E XZ Till ${Date.now()}`;
  const term = await (await request.post(`${API}/pos-terminals`, { headers: admin, data: { branch_id: BRANCH, name: till } })).json();
  try {
    const opened = await (
      await request.post(`${API}/pos-shifts`, { headers: cashier, data: { pos_terminal_id: term.terminal_id, opening_float: 1000, idempotency_key: `e2e-xz-${Date.now()}` } })
    ).json();
    expect(opened.status).toBe("open");

    await login(page, "arjun@acme-sports.test");
    await page.goto("/reports/x-z");
    await page.getByLabel("Till").selectOption({ label: till });
    await page.getByRole("button", { name: `${till} · shift 1` }).click();
    const x = page.getByTestId("x-report");
    await expect(x).toContainText("Shift open: figures so far");
    await expect(x).toContainText("₹1,000.00"); // the opening float
    await expect(page.getByRole("button", { name: "Print X report" })).toBeVisible();

    await request.post(`${API}/pos-shifts/${opened.shift_id}/close`, { headers: cashier, data: { denominations: [{ denomination: 500, count: 2 }] } });
    await page.getByRole("button", { name: "← All shifts" }).click();
    await page.getByRole("button", { name: `${till} · shift 1` }).click();
    await expect(x).toContainText("Shift closed: final figures");
    await expect(x).toContainText("Variance");

    // Z: today is live and can't be closed yet; an old day closes.
    await page.getByRole("tab", { name: "Z report · by day" }).click();
    await expect(page.getByTestId("z-status")).toContainText("Day not closed");
    await expect(page.getByRole("button", { name: "Close day" })).toBeDisabled();
    await page.getByLabel("Business day").fill("2024-01-15");
    await expect(page.getByRole("heading", { name: "Z report · 2024-01-15" })).toBeVisible();
    const status = page.getByTestId("z-status");
    if ((await status.textContent())?.includes("Day not closed")) {
      await page.getByRole("button", { name: "Close day" }).click();
      await page.getByRole("dialog").getByRole("button", { name: "Close day" }).click();
    }
    await expect(status).toContainText(/Z no\. \d+ · closed by/);
    await expect(page.getByRole("button", { name: "Close day" })).toHaveCount(0);
    await page.getByLabel("From").fill("2024-01-01");
    await expect(page.getByRole("row", { name: /2024-01-15/ })).toBeVisible();

    // Closing needs reports.day_close: the cashier is refused.
    const refused = await request.post(`${API}/reports/z-report/close`, { headers: cashier, data: { branch_id: BRANCH, date: "2024-01-16" } });
    expect(refused.status()).toBe(403);
  } finally {
    await request.patch(`${API}/pos-terminals/${term.terminal_id}`, { headers: admin, data: { status: "inactive" } });
  }
});

test("the sales dashboard: widgets per role, the sales target, and no dashboard without access", async ({ page, request }) => {
  const admin = await auth(request, "arjun@acme-sports.test");
  const roles = (await (await request.get(`${API}/dashboard/roles`, { headers: admin })).json()).roles as { role_id: string; role_name: string }[];
  const adminRole = roles.find((r) => r.role_name === "Merchant Admin")!;
  try {
    await login(page, "arjun@acme-sports.test");
    await expect(page.getByTestId("kpi-day")).toBeVisible();
    await expect(page.getByTestId("widget-daily_sales")).toBeVisible();
    await expect(page.getByTestId("widget-hourly_sales")).toBeVisible();
    await expect(page.getByTestId("widget-sales_tracking")).toContainText("Not set");

    await page.getByRole("link", { name: "Customize" }).click();
    await page.getByRole("option", { name: /Merchant Admin/ }).click();
    await page.getByLabel(/^Sales by hour/).uncheck();
    await page.getByRole("button", { name: "Save Merchant Admin" }).click();
    await expect(page.getByText("Saved Merchant Admin's dashboard")).toBeVisible();

    await page.getByRole("button", { name: "Add a target" }).click();
    await page.getByLabel("Target 1 amount").fill("100000");
    await page.getByRole("button", { name: "Save targets" }).click();
    await expect(page.getByText("Sales targets saved")).toBeVisible();

    await page.goto("/dashboard");
    await expect(page.getByTestId("widget-daily_sales")).toBeVisible();
    await expect(page.getByTestId("widget-hourly_sales")).toHaveCount(0);
    await expect(page.getByTestId("widget-sales_tracking")).toContainText("₹1,00,000.00");
    await expect(page.getByTestId("widget-sales_tracking")).toContainText("of target");

    await page.getByLabel("Channel").selectOption("backoffice");
    await expect(page.getByTestId("widget-daily_sales")).toBeVisible();

    await login(page, "ravi@acme-sports.test");
    await expect(page.getByText("Your role doesn't include the sales dashboard")).toBeVisible();
    expect((await request.get(`${API}/dashboard`, { headers: await auth(request, "ravi@acme-sports.test") })).status()).toBe(403);
  } finally {
    await request.put(`${API}/dashboard/roles/${adminRole.role_id}`, { headers: admin, data: { widgets: null } });
    await request.put(`${API}/dashboard/targets`, { headers: admin, data: { targets: [] } });
  }
});

test("the report library runs, saves and exports; a generated report regenerates", async ({ page, request }) => {
  const admin = await auth(request, "arjun@acme-sports.test");
  const saveName = `E2E saved ${Date.now()}`;
  try {
    await login(page, "arjun@acme-sports.test");
    await page.goto("/reports/library");
    await page.getByRole("button", { name: "Profit and loss", exact: true }).click();
    await page.getByLabel("Period").selectOption("this_fy");
    await page.getByRole("button", { name: "Run report" }).click();
    await expect(page.getByTestId("report-table")).toBeVisible();
    await expect(page.getByTestId("report-table")).toContainText("1 Income");

    await page.getByLabel("Find a report").fill("audit");
    await page.getByRole("button", { name: "Sales audit report" }).click();
    await page.getByLabel("Period").selectOption("this_month");
    await page.getByRole("button", { name: "Run report" }).click();
    await expect(page.getByTestId("report-table")).toBeVisible();

    await page.getByLabel("Find a report").fill("");
    await page.getByRole("button", { name: "Daily sales summary" }).click();
    await page.getByRole("button", { name: "Run report" }).click();
    await expect(page.getByTestId("report-table")).toBeVisible();
    await page.getByRole("button", { name: "Save to my reports" }).click();
    await page.getByLabel("Name").fill(saveName);
    await page.getByRole("dialog").getByRole("button", { name: "Save" }).click();
    await expect(page.getByText(`Saved “${saveName}” to Saved Reports`)).toBeVisible();

    await page.goto("/reports/saved");
    const row = page.getByRole("row", { name: new RegExp(saveName) });
    await expect(row).toContainText("This month");
    await row.getByRole("button", { name: `Run ${saveName}` }).click();
    await expect(page.getByTestId("report-table")).toBeVisible();
    await row.getByRole("button", { name: `Export ${saveName} to CSV` }).click();
    await expect(page.getByText("Export started")).toBeVisible();

    const download = page.getByRole("button", { name: `Download ${saveName}` }).first();
    await expect(download).toBeEnabled({ timeout: 20000 });
    const [file] = await Promise.all([page.waitForEvent("download"), download.click()]);
    expect(file.suggestedFilename()).toBe(`${saveName}.csv`);

    await page.getByRole("button", { name: `Regenerate ${saveName}` }).first().click();
    const dialog = page.getByRole("dialog");
    await dialog.getByText("This month, as of today").click();
    await dialog.getByLabel("File").selectOption("xlsx");
    await dialog.getByRole("button", { name: "Regenerate" }).click();
    await expect(page.getByText(`Regenerating “${saveName}”`)).toBeVisible();
    await expect(page.getByRole("row", { name: new RegExp(`${saveName} xlsx`) }).first()).toBeVisible();
  } finally {
    const saved = (await (await request.get(`${API}/reports/saved`, { headers: admin })).json()).templates as { template_id: string; name: string }[];
    for (const t of saved.filter((x) => x.name === saveName)) await request.delete(`${API}/reports/saved/${t.template_id}`, { headers: admin });
  }
});
