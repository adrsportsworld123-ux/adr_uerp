import { test, expect, Page, APIRequestContext } from "@playwright/test";

// Phase 16 report builder in the browser: the Merchant Admin builds "sales
// by branch" (total and count), runs it, saves it shared; the Branch
// Manager (own branch only) opens the shared report and sees only MG Road,
// exports it to Excel in the background and downloads it. The server
// applies each person's branch scope; the screen just shows what comes back.

const API = "http://localhost:8080/api/v1";
const MERCHANT = process.env.E2E_MERCHANT_CODE ?? "acme-sports";

async function login(page: Page, email: string, password = "Passw0rd!") {
  await page.evaluate(() => localStorage.clear()).catch(() => {});
  await page.goto("/login");
  await page.fill("#email", email);
  await page.fill("#password", password);
  await page.click('button[type="submit"]');
  await page.waitForURL("/dashboard");
}

async function token(request: APIRequestContext, email: string, password = "Passw0rd!") {
  const r = await request.post(`${API}/auth/login`, { data: { merchant_code: MERCHANT, email, password } });
  return (await r.json()).access_token as string;
}

// A finalized sale today in a branch, so the report has both branches.
async function sale(request: APIRequestContext, auth: Record<string, string>, branch: string) {
  const bat = "99999999-9999-9999-9999-999999999999";
  await request.post(`${API}/inventory/adjustments`, { headers: auth, data: { variant_id: bat, branch_id: branch, quantity_delta: 2, reason: "e2e report builder" } });
  const till = await (await request.post(`${API}/pos-terminals`, { headers: auth, data: { branch_id: branch, name: `E2E RB ${Date.now()}` } })).json();
  const o = await (await request.post(`${API}/sales/orders`, { headers: auth, data: { branch_id: branch, pos_terminal_id: till.terminal_id, idempotency_key: crypto.randomUUID() } })).json();
  const l = await (await request.post(`${API}/sales/orders/${o.order_id}/lines`, { headers: auth, data: { variant_id: bat, quantity: 1 } })).json();
  await request.post(`${API}/sales/orders/${o.order_id}/checkout`, { headers: auth, data: { payments: [{ method: "cash", amount: Number(l.grand_total) }] } });
}

test("Report builder: build, save shared, and each person sees only their branches", async ({ page, request }) => {
  test.setTimeout(90_000);
  const name = `E2E sales by branch ${Date.now() % 100000}`;
  const admin = { Authorization: `Bearer ${await token(request, "arjun@acme-sports.test")}` };
  await sale(request, admin, "22222222-2222-2222-2222-222222222222");
  await sale(request, admin, "33333333-1111-2222-3333-444444444444");

  await test.step("the admin builds sales by branch and runs it", async () => {
    await login(page, "arjun@acme-sports.test");
    await page.goto("/reports/builder");
    await page.getByLabel("Report on").selectOption("sales");
    await page.getByRole("button", { name: "This month" }).click();
    await page.getByLabel("Column 1 field").selectOption("branch");
    await page.getByRole("button", { name: "Add column" }).click();
    await page.getByLabel("Column 2 field").selectOption("grand_total");
    await page.getByLabel("Column 2 summary").selectOption("sum");
    await page.getByRole("button", { name: "Add column" }).click();
    await page.getByLabel("Column 3 field").selectOption("order_number");
    await page.getByLabel("Column 3 summary").selectOption("count");
    await page.getByLabel("Sort by").selectOption("1:desc");
    await page.getByRole("button", { name: "Run" }).click();
    const results = page.getByRole("region", { name: "Results" });
    await expect(results.getByRole("columnheader", { name: "Total total" })).toBeVisible();
    await expect(results.getByRole("cell", { name: "MG Road" })).toBeVisible();
    await expect(results.getByRole("cell", { name: "Koramangala" })).toBeVisible();
  });

  await test.step("save it, shared", async () => {
    await page.getByRole("button", { name: "Save", exact: true }).click();
    await page.getByLabel("Name").fill(name);
    await page.getByLabel(/Share with everyone/).check();
    await page.getByRole("dialog").getByRole("button", { name: "Save" }).click();
    await expect(page.getByText(`Saved "${name}"`)).toBeVisible();
    await expect(page.getByRole("button", { name: new RegExp(name) })).toContainText("Mine · shared");
  });

  await test.step("the Branch Manager runs the shared report: MG Road only", async () => {
    await login(page, "meera@acme-sports.test", "9999");
    await page.goto("/reports/builder");
    await page.getByRole("button", { name: new RegExp(name) }).click();
    await expect(page.getByText("read-only; duplicate it to change it", { exact: false })).toBeVisible();
    await page.getByRole("button", { name: "This month" }).click();
    await page.getByRole("button", { name: "Run" }).click();
    const results = page.getByRole("region", { name: "Results" });
    await expect(results.getByRole("cell", { name: "MG Road" })).toBeVisible();
    await expect(results.getByRole("cell", { name: "Koramangala" })).toHaveCount(0);
  });

  await test.step("export to Excel in the background and download it", async () => {
    await page.getByRole("button", { name: "Excel" }).click();
    await expect(page.getByText("Export started")).toBeVisible();
    const dl = page.getByRole("button", { name: `Download ${name}.xlsx` });
    await expect(dl).toBeVisible({ timeout: 20_000 });
    const [file] = await Promise.all([page.waitForEvent("download"), dl.click()]);
    expect(file.suggestedFilename()).toBe(`${name}.xlsx`);
  });

  await test.step("clean up", async () => {
    const list = await (await request.get(`${API}/report-builder/templates`, { headers: admin })).json();
    for (const t of list.templates.filter((x: { name: string }) => x.name === name)) {
      await request.delete(`${API}/report-builder/templates/${t.template_id}`, { headers: admin });
    }
  });
});
