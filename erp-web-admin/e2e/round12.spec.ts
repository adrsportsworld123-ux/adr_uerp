import { test, expect, APIRequestContext, Page } from "@playwright/test";

// erp-core-go §3.77 (migrations/071–074): markup rules and the branch's
// customer price; credit outstanding by customer; the till's on-screen
// keyboard; approvals waiting / given; POs from reorder suggestions;
// importing an expense list; partners' capital. The API flows cover the
// arithmetic; this checks the screens drive it. Everything a step switches
// on is put back in finally.

const API = process.env.E2E_API_URL ?? "http://localhost:8080/api/v1";
const MERCHANT = process.env.E2E_MERCHANT_CODE ?? "acme-sports";
const BRANCH = "22222222-2222-2222-2222-222222222222";
const TILL = "33333333-3333-3333-3333-333333333333";
const SUPPLIER = "11111111-2222-3333-4444-555555555555";
const BAT = "99999999-9999-9999-9999-999999999999";

async function token(request: APIRequestContext) {
  const r = await request.post(`${API}/auth/login`, { data: { merchant_code: MERCHANT, email: "arjun@acme-sports.test", password: "Passw0rd!" } });
  return (await r.json()).access_token as string;
}

async function login(page: Page) {
  await page.goto("/login");
  await page.fill("#email", "arjun@acme-sports.test");
  await page.fill("#password", "Passw0rd!");
  await page.click('button[type="submit"]');
  await page.waitForURL("/dashboard");
}

test.describe.configure({ mode: "serial" });

test("markup rule and the branch's customer price", async ({ page, request }) => {
  const auth = { Authorization: `Bearer ${await token(request)}` };
  const tag = Date.now().toString().slice(-7);
  const created: string[] = [];
  page.on("response", async (r) => {
    if (r.url().endsWith("/api/v1/pricing/markup-rules") && r.request().method() === "POST" && r.status() === 201) created.push((await r.json()).markup_rule_id);
  });
  try {
    await login(page);
    await page.goto("/pricing/markup-rules");
    await page.getByRole("button", { name: "New markup rule" }).click();
    await page.fill("#markupName", `E2E markup ${tag}`);
    await page.locator("#markupScope").selectOption("all");
    await page.fill("#markupValue", "40");
    await expect(page.getByTestId("markup-example")).toContainText("140.00");
    await page.getByRole("button", { name: "Create rule" }).click();
    await expect(page.getByText("Markup rule created")).toBeVisible();
    const row = page.getByRole("row", { name: new RegExp(`E2E markup ${tag}`) });
    await expect(row).toContainText("Every product");
    await expect(row).toContainText("Cost + 40%");
    await row.getByRole("button", { name: "Deactivate" }).click();
    await expect(page.getByText("Markup rule switched off")).toBeVisible();

    await page.goto("/sales-settings");
    const sel = page.getByRole("combobox", { name: /Customer price at/ }).first();
    await sel.selectOption("customer");
    await expect(page.getByText("Branch rule saved")).toBeVisible();
    await expect(page.getByTestId("branch-price-benefit").first()).toHaveText("Customer benefit");
    await sel.selectOption("");
    await expect(page.getByTestId("branch-price-benefit").first()).toHaveText("As today");
  } finally {
    for (const id of created) await request.patch(`${API}/pricing/markup-rules/${id}`, { headers: auth, data: { active: false } });
    await request.put(`${API}/sales/settings/branches/${BRANCH}`, { headers: auth, data: { price_benefit: null } });
  }
});

test("credit outstanding by customer in the report library", async ({ page }) => {
  await login(page);
  await page.goto("/reports/library");
  await page.getByRole("button", { name: "Credit outstanding by customer" }).click();
  await page.getByRole("button", { name: "Run report" }).click();
  await expect(page.getByRole("columnheader", { name: "Over 90 days" })).toBeVisible();
});

test("the till's on-screen keyboard switch", async ({ page, request }) => {
  const auth = { Authorization: `Bearer ${await token(request)}` };
  try {
    await login(page);
    await page.goto("/terminals");
    await page.getByRole("button", { name: /Screen layout of/ }).first().click();
    await page.getByLabel(/On-screen keyboard/).check();
    await page.getByRole("button", { name: "Save" }).click();
    await expect(page.getByText(/Layout saved for/)).toBeVisible();
    await expect(page.getByText("On-screen keyboard").first()).toBeVisible();
    const t = await (await request.get(`${API}/settings/localization/terminal?pos_terminal_id=${TILL}`, { headers: auth })).json();
    expect(typeof t.virtual_keyboard).toBe("boolean");
  } finally {
    const list = await (await request.get(`${API}/pos-terminals`, { headers: auth })).json();
    for (const t of list.terminals) if (t.virtual_keyboard) await request.patch(`${API}/pos-terminals/${t.terminal_id}`, { headers: auth, data: { virtual_keyboard: false } });
  }
});

test("approvals: a transfer waits, is approved, and shows as given", async ({ page, request }) => {
  const auth = { Authorization: `Bearer ${await token(request)}` };
  const branches = (await (await request.get(`${API}/branches`, { headers: auth })).json()).branches;
  const other = branches.find((b: { branch_id: string }) => b.branch_id !== BRANCH);
  const tr = await (
    await request.post(`${API}/branch-transfers`, {
      headers: auth,
      data: { from_branch_id: BRANCH, to_branch_id: other.branch_id, notes: "E2E approvals", lines: [{ variant_id: BAT, quantity: 1 }] },
    })
  ).json();
  await login(page);
  await page.goto("/approvals");
  const row = page.getByTestId("approvals-waiting").getByRole("row", { name: new RegExp(tr.transfer_number) });
  await expect(row).toContainText("Branch transfer");
  await row.getByRole("button", { name: `Approve ${tr.transfer_number}` }).click();
  await expect(page.getByText(`Branch transfer ${tr.transfer_number} approved`)).toBeVisible();
  await expect(page.getByTestId("approvals-waiting").getByRole("row", { name: new RegExp(tr.transfer_number) })).toHaveCount(0);
  await page.getByRole("tab", { name: "Given" }).click();
  await expect(page.getByTestId("approvals-given").getByRole("row", { name: new RegExp(tr.transfer_number) })).toContainText("Arjun");
  await request.post(`${API}/branch-transfers/${tr.transfer_id}/cancel`, { headers: auth, data: { reason: "e2e" } });
});

test("a draft PO from reorder suggestions", async ({ page, request }) => {
  const auth = { Authorization: `Bearer ${await token(request)}` };
  const tag = Date.now().toString().slice(-7);
  const name = `E2E REORDER ${tag}`;
  const imp = await request.post(`${API}/purchase/imports/commit`, {
    headers: auth,
    data: {
      supplier_id: SUPPLIER,
      branch_id: BRANCH,
      invoice_number: `E2E-RO-${tag}`,
      invoice_date: "2026-10-01",
      csv: `item_name,quantity,unit_cost,gst_rate,mrp,sale_gst_rate\n${name},1,50,18,100,18\n`,
    },
  });
  expect(imp.status()).toBe(201);
  const prods = await (await request.get(`${API}/products?q=${encodeURIComponent(name)}`, { headers: auth })).json();
  const variant = (prods.products ?? prods.items)[0].variants[0].variant_id;
  await request.patch(`${API}/inventory/reorder-point`, { headers: auth, data: { branch_id: BRANCH, variant_id: variant, reorder_point: 5 } });

  await login(page);
  await page.goto("/ai-insights");
  await page.getByRole("checkbox", { name: `Order ${name}` }).check();
  await expect(page.getByRole("combobox", { name: `Supplier for ${name}` })).toHaveValue(SUPPLIER);
  await page.getByRole("button", { name: /Create purchase orders/ }).click();
  await expect(page.getByText(/draft purchase order\(s\) created/)).toBeVisible();
  const link = page.getByTestId("reorder-created").getByRole("link").first();
  await link.click();
  await page.waitForURL(/\/purchase\/orders\//);
  await expect(page.getByTestId("po-lines")).toContainText(name);
  await request.patch(`${API}/inventory/reorder-point`, { headers: auth, data: { branch_id: BRANCH, variant_id: variant, reorder_point: 0 } });
});

test("import an expense list, then a partner's statement", async ({ page, request }) => {
  const auth = { Authorization: `Bearer ${await token(request)}` };
  const tag = Date.now().toString().slice(-7);
  const partner = `E2E Partner ${tag}`;
  await login(page);

  // A partner, added on the screen
  await page.goto("/partners");
  await page.getByRole("button", { name: "Add partner" }).click();
  await page.fill("#pt-name", partner);
  await page.fill("#pt-share", "0");
  await page.getByRole("button", { name: "Save" }).click();
  await expect(page.getByText(`${partner} added`)).toBeVisible();
  await page.getByRole("button", { name: `Pay in for ${partner}` }).click();
  await page.fill("#pm-amount", "10000");
  await page.locator("#pm-method").selectOption("cash");
  await page.getByRole("button", { name: "Record pay-in" }).click();
  await expect(page.getByText(new RegExp(`paid in by ${partner}`))).toBeVisible();

  // Expenses from a CSV: one paid by that partner
  await page.goto("/expenses?tab=import");
  const csv =
    "date,head,amount,paid_by,description,vendor,reference\n" +
    `2026-04-10,Flex & Banner,1500,partner: ${partner},Banner ${tag},,\n` +
    `2026-04-11,Transport,700,cash,Freight ${tag},,\n`;
  await page.setInputFiles("#ei-file", { name: "expenses.csv", mimeType: "text/csv", buffer: Buffer.from(csv) });
  await page.getByRole("button", { name: "Preview" }).click();
  await expect(page.getByTestId("ei-total")).toContainText("2,200.00");
  await page.getByRole("button", { name: "Import 2 expense(s)" }).click();
  await expect(page.getByText("2 expense(s) imported").first()).toBeVisible();
  await page.goto("/expenses?tab=list");
  await page.fill("#ex-from", "2026-04-01");
  await page.fill("#ex-q", tag);
  await expect(page.getByTestId("expenses-table")).toContainText(`Banner ${tag}`);

  // The statement: pay-in + the banner they paid
  await page.goto("/partners");
  await page.getByRole("link", { name: partner }).click();
  await page.fill("#ps-from", "2026-04-01");
  await expect(page.getByTestId("ps-closing")).toContainText("11,500.00");
  await expect(page.getByTestId("ps-lines")).toContainText("Paid an expense");

  // Retire the test partner so it doesn't skew shares for other tests.
  const list = await (await request.get(`${API}/partners`, { headers: auth })).json();
  const p = list.partners.find((x: { name: string }) => x.name === partner);
  await request.patch(`${API}/partners/${p.partner_id}`, { headers: auth, data: { status: "retired" } });
});
