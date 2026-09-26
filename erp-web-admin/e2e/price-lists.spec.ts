import { test, expect } from "@playwright/test";

// End-to-end coverage for Phase 7's wholesale price lists (erp-core-go's
// phased_roadmap.md; internal/pricing/price_lists.go, resolve.go). Seeds a
// real product via the API, creates a price list and sets an override
// price through the actual browser UI, then proves the price actually
// applies at checkout (not just that the UI shows a number) by adding the
// variant to a real cart for a customer assigned to that list and reading
// back the resulting line's unit_price via the API — the same "seed via
// API, assert via UI, verify the real effect via API" split this suite
// already uses elsewhere (see ai-insights.spec.ts).
const API_BASE = "http://localhost:8080/api/v1";
const BRANCH_ID = "22222222-2222-2222-2222-222222222222";
const TERMINAL_ID = "33333333-3333-3333-3333-333333333333";

test("wholesale price list: create, set an item price via the UI, and confirm it resolves at checkout", async ({ page, request }) => {
  const unique = Date.now() % 1000000;
  const productName = `E2E Price List Bat ${unique}`;
  const listName = `E2E Wholesale ${unique}`;
  let variantId = "";
  let customerId = "";
  let adminToken = "";

  await test.step("seed a product and a B2B customer via the API", async () => {
    const login = await request.post(`${API_BASE}/auth/login`, {
      data: { merchant_code: "acme-sports", email: "admin@adrsw.com", password: "Idontsay3#" },
    });
    adminToken = (await login.json()).access_token;
    const headers = { Authorization: `Bearer ${adminToken}` };

    const catRes = await request.get(`${API_BASE}/categories`, { headers });
    const { categories } = await catRes.json();
    const categoryId = categories[0]?.category_id;

    const prod = await request.post(`${API_BASE}/products`, {
      headers,
      data: { name: productName, hsn_code: "9506", category_id: categoryId, variants: [{ sku: `E2E-PL-${unique}`, cost_price: 300, mrp: 999, selling_price: 799 }] },
    });
    variantId = (await prod.json()).variants[0].variant_id;
    await request.post(`${API_BASE}/inventory/adjustments`, {
      headers,
      data: { variant_id: variantId, branch_id: BRANCH_ID, quantity_delta: 10, reason: `e2e price-lists seed ${unique}` },
    });

    const customer = await request.post(`${API_BASE}/customers`, {
      headers,
      data: { name: `E2E Wholesale Customer ${unique}`, phone: `9${unique}`.padEnd(10, "0"), email: `wholesale${unique}@e2e.test`, customer_type: "b2b", gstin: "29ABCDE1234F1Z5" },
    });
    customerId = (await customer.json()).customer_id;
  });

  await test.step("login as Merchant Admin and create a price list through the UI", async () => {
    await page.goto("/login");
    await page.fill("#email", "admin@adrsw.com");
    await page.fill("#password", "Idontsay3#");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");

    await page.goto("/pricing/price-lists");
    await page.getByLabel("New price list name").fill(listName);
    await page.getByRole("button", { name: "Create" }).click();
    await expect(page.getByRole("cell", { name: listName })).toBeVisible();
  });

  await test.step("set an override price (retail 799 -> wholesale 550) through the UI", async () => {
    await page.getByRole("row", { name: new RegExp(listName) }).getByRole("button", { name: "View / edit" }).click();
    await page.locator("button", { hasText: "Select a product" }).click();
    await page.getByRole("option", { name: new RegExp(productName) }).click();
    await page.locator('input[type="number"]').last().fill("550");
    await page.getByRole("button", { name: "Set price" }).click();
    await expect(page.getByText("₹550.00")).toBeVisible();
  });

  await test.step("assign the B2B customer to this price list via the API, then confirm it resolves at checkout", async () => {
    const headers = { Authorization: `Bearer ${adminToken}` };
    const listsResp = await request.get(`${API_BASE}/pricing/price-lists`, { headers });
    const list = (await listsResp.json()).price_lists.find((l: { name: string }) => l.name === listName);
    expect(list).toBeTruthy();

    await request.patch(`${API_BASE}/customers/${customerId}`, { headers, data: { price_list_id: list.price_list_id } });

    const order = await request.post(`${API_BASE}/sales/orders`, {
      headers,
      data: { branch_id: BRANCH_ID, pos_terminal_id: TERMINAL_ID, idempotency_key: `e2e-pl-${unique}` },
    });
    const { order_id } = await order.json();
    await request.post(`${API_BASE}/sales/orders/${order_id}/customer`, { headers, data: { customer_id: customerId } });
    const lineResp = await request.post(`${API_BASE}/sales/orders/${order_id}/lines`, { headers, data: { variant_id: variantId, quantity: 1 } });
    const line = (await lineResp.json()).lines[0];
    expect(line.unit_price).toBe("550.00");
  });

  await test.step("a POS User (no pricing.manage) cannot create a price list", async () => {
    const posLogin = await request.post(`${API_BASE}/auth/login`, {
      data: { merchant_code: "acme-sports", email: "ravi@acme-sports.test", password: "Passw0rd!" },
    });
    const posToken = (await posLogin.json()).access_token;
    const resp = await request.post(`${API_BASE}/pricing/price-lists`, {
      headers: { Authorization: `Bearer ${posToken}` },
      data: { name: `should-be-denied-${unique}` },
    });
    expect(resp.status()).toBe(403);
  });

  await test.step("deleting the list while a customer is still assigned is refused, through the real UI (found missing during a completeness audit)", async () => {
    await page.reload();
    await page.getByRole("row", { name: new RegExp(listName) }).getByRole("button", { name: "Delete" }).click();
    await expect(page.getByText(/assigned to this price list/i)).toBeVisible();
    // Still there — the refusal didn't silently drop the row.
    await expect(page.getByRole("cell", { name: listName })).toBeVisible();
  });

  await test.step("after unassigning the customer, the same delete succeeds", async () => {
    const headers = { Authorization: `Bearer ${adminToken}` };
    await request.patch(`${API_BASE}/customers/${customerId}`, { headers, data: { price_list_id: "" } });

    await page.getByRole("row", { name: new RegExp(listName) }).getByRole("button", { name: "Delete" }).click();
    await expect(page.getByText("Price list deleted")).toBeVisible();
    await expect(page.getByRole("cell", { name: listName })).toHaveCount(0);
  });
});
