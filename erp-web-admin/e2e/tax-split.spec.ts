import { test, expect, APIRequestContext, Page } from "@playwright/test";

// GST split (erp-core-go migrations/060): Tax Settings (merchant default,
// branch override), CGST + SGST on a walk-in sale, IGST on a sale to a
// customer in another state, and the split on a purchase bill.

const API = process.env.E2E_API_URL ?? "http://localhost:8080/api/v1";
const MERCHANT = process.env.E2E_MERCHANT_CODE ?? "acme-sports";
const BRANCH = "22222222-2222-2222-2222-222222222222";
const TILL = "33333333-3333-3333-3333-333333333333";
const SUPPLIER = "11111111-2222-3333-4444-555555555555";
// The seed bat, SG-BAT-SH: ₹2,299 + 18% GST.
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

test("tax split: settings, CGST + SGST and IGST sales, purchase bill", async ({ page, request }) => {
  const auth = { Authorization: `Bearer ${await token(request)}` };
  await login(page);

  await test.step("Tax Settings: GST by default; a branch can switch to a single VAT and back", async () => {
    await page.goto("/tax-settings");
    await expect(page.getByRole("heading", { name: "Tax Settings" })).toBeVisible();
    const branch = page.getByRole("region", { name: "Tax for MG Road" });
    await expect(branch.getByTestId("branch-effective")).toContainText("CGST + SGST / IGST");
    await branch.getByLabel(/Single tax/).check();
    await branch.getByLabel("Tax name").fill("VAT");
    await branch.getByRole("button", { name: "Save MG Road" }).click();
    await expect(page.getByText("MG Road saved")).toBeVisible();
    await expect(branch.getByTestId("branch-effective")).toContainText("VAT");
    await branch.getByLabel("Same as the merchant").check();
    await branch.getByLabel("Tax name").fill("");
    await branch.getByRole("button", { name: "Save MG Road" }).click();
    await expect(branch.getByTestId("branch-effective")).toContainText("CGST + SGST / IGST");
  });

  await test.step("a walk-in sale shows CGST and SGST", async () => {
    await page.goto("/sales/new");
    await page.getByRole("button", { name: /Search by name, SKU or barcode/ }).click();
    await page.getByLabel("Search products").fill("SG-BAT-SH");
    await page.getByRole("option", { name: /SG-BAT-SH/ }).first().click();
    await page.fill("#sale-qty", "1");
    await page.getByRole("button", { name: "Add", exact: true }).click();
    await expect(page.getByTestId("sale-total")).toHaveText("₹2,712.82");
    const rows = page.getByTestId("tax-row");
    await expect(rows).toHaveCount(2);
    await expect(rows.nth(0)).toContainText("CGST @ 9%");
    await expect(rows.nth(0)).toContainText("₹206.91");
    await expect(rows.nth(1)).toContainText("SGST @ 9%");
  });

  await test.step("a sale to a Tamil Nadu customer is IGST", async () => {
    const stamp = Date.now();
    const cust = await (
      await request.post(`${API}/customers`, {
        headers: auth,
        data: { name: `TN Traders ${stamp}`, phone: `96${String(stamp).slice(-8)}`, customer_type: "b2b", company_name: "TN Traders", gstin: "33AABCT1234B1Z2" },
      })
    ).json();
    const o = await (await request.post(`${API}/sales/orders`, { headers: auth, data: { branch_id: BRANCH, pos_terminal_id: TILL, idempotency_key: `e2e-tax-${stamp}` } })).json();
    await request.post(`${API}/sales/orders/${o.order_id}/customer`, { headers: auth, data: { customer_id: cust.customer_id } });
    await request.post(`${API}/sales/orders/${o.order_id}/lines`, { headers: auth, data: { variant_id: BAT, quantity: 1 } });
    const done = await request.post(`${API}/sales/orders/${o.order_id}/checkout`, { headers: auth, data: { payments: [{ method: "upi", amount: 2712.82 }] } });
    expect(done.status()).toBe(200);

    await page.goto(`/sales/${o.order_id}`);
    await expect(page.getByTestId("place-of-supply")).toContainText("33 – Tamil Nadu · inter-state (IGST)");
    await expect(page.getByRole("columnheader", { name: "IGST" }).first()).toBeVisible();
    await expect(page.getByRole("columnheader", { name: "CGST" })).toHaveCount(0);
    await expect(page.getByTestId("tax-row")).toHaveCount(1);
    await expect(page.getByTestId("tax-row")).toContainText("IGST @ 18%");
    await expect(page.getByRole("table", { name: "GST by rate" })).toContainText("413.82");
  });

  await test.step("goods delivered to another state: place of supply on the sale makes it IGST", async () => {
    await page.goto("/sales/new");
    await page.getByRole("button", { name: /Search by name, SKU or barcode/ }).click();
    await page.getByLabel("Search products").fill("SG-BAT-SH");
    await page.getByRole("option", { name: /SG-BAT-SH/ }).first().click();
    await page.fill("#sale-qty", "1");
    await page.getByRole("button", { name: "Add", exact: true }).click();
    await expect(page.getByTestId("tax-row").first()).toContainText("CGST @ 9%");
    await page.getByLabel("Place of supply").selectOption("27");
    await expect(page.getByTestId("tax-row")).toHaveCount(1);
    await expect(page.getByTestId("tax-row")).toContainText("IGST @ 18%");
    await expect(page.getByTestId("place-of-supply")).toContainText("27 – Maharashtra · inter-state (IGST)");
    await page.getByLabel("Place of supply").selectOption("");
    await expect(page.getByTestId("tax-row")).toHaveCount(2);
  });

  await test.step("a return is a credit note with its tax split and a PDF", async () => {
    const stamp = Date.now();
    const o = await (await request.post(`${API}/sales/orders`, { headers: auth, data: { branch_id: BRANCH, pos_terminal_id: TILL, idempotency_key: `e2e-cn-${stamp}` } })).json();
    const c = await (await request.post(`${API}/sales/orders/${o.order_id}/lines`, { headers: auth, data: { variant_id: BAT, quantity: 2 } })).json();
    await request.post(`${API}/sales/orders/${o.order_id}/checkout`, { headers: auth, data: { payments: [{ method: "cash", amount: Number(c.grand_total) }] } });
    const rt = await (await request.get(`${API}/sales/orders/${o.order_id}/returnable`, { headers: auth })).json();
    const body: Record<string, unknown> = { lines: [{ line_id: rt.lines[0].line_id, quantity: 1 }], reason: "Damaged", refund_method: "cash", idempotency_key: `e2e-cn-r-${stamp}`, pos_terminal_id: TILL };
    if (rt.requires_approval) Object.assign(body, { approver_code: "EMP003", approver_pin: "1234" });
    const r = await request.post(`${API}/sales/orders/${o.order_id}/returns`, { headers: auth, data: body });
    expect(r.status()).toBe(201);
    await page.goto(`/sales/${o.order_id}`);
    await expect(page.getByTestId("credit-note-tax")).toContainText("CGST @ 9% ₹206.91 · SGST @ 9% ₹206.91");
    await expect(page.getByRole("button", { name: /Credit note/ })).toBeVisible();
  });

  await test.step("Tax Settings: a branch in another currency", async () => {
    await page.goto("/tax-settings");
    const branch = page.getByRole("region", { name: "Tax for MG Road" });
    await branch.getByLabel("Currency").selectOption("USD");
    await branch.getByRole("button", { name: "Save MG Road" }).click();
    await expect(branch.getByTestId("branch-effective")).toContainText("USD $");
    await branch.getByLabel("Currency").selectOption("");
    await branch.getByRole("button", { name: "Save MG Road" }).click();
    await expect(branch.getByTestId("branch-effective")).toContainText("INR ₹");
  });

  await test.step("a purchase bill shows the supplier's CGST and SGST", async () => {
    const g = await (await request.post(`${API}/purchase/grn`, { headers: auth, data: { supplier_id: SUPPLIER, branch_id: BRANCH } })).json();
    await request.post(`${API}/purchase/grn/${g.grn_id}/lines`, { headers: auth, data: { variant_id: BAT, quantity: 1, unit_cost: 1000 } });
    await request.post(`${API}/purchase/grn/${g.grn_id}/complete`, { headers: auth });
    await page.goto(`/purchase/bills/new?grn_id=${g.grn_id}`);
    await page.getByLabel("Supplier's tax is").selectOption("intra");
    await page.getByLabel("GST on this bill (₹)").fill("180");
    await page.getByRole("button", { name: "Create bill" }).click();
    await page.waitForURL(/\/purchase\/bills\/[0-9a-f-]{36}$/);
    const rows = page.getByTestId("bill-tax-row");
    await expect(rows).toHaveCount(2);
    await expect(rows.nth(0)).toContainText("CGST");
    await expect(rows.nth(0)).toContainText("₹90.00");
    await expect(rows.nth(1)).toContainText("SGST");
  });
});
