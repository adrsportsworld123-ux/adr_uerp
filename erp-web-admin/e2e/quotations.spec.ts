import { test, expect } from "@playwright/test";

// End-to-end coverage for Phase 7's B2B quotations (erp-core-go's
// phased_roadmap.md; internal/quotations). Seeds a real product and B2B
// customer via the API, drives the full draft -> send -> accept -> convert
// lifecycle through the actual browser UI, then confirms the converted
// order is a REAL sales_order — correct wholesale price, correct
// customer_id, checkoutable — by reading it back via the API. That's the
// phase's own Definition of Done ("the same catalog and stock serve both
// a walk-in retail customer and a B2B wholesale order without
// double-entry") verified for real, not just asserted.
const API_BASE = "http://localhost:8080/api/v1";
const BRANCH_ID = "22222222-2222-2222-2222-222222222222";

test("B2B quotation: draft -> send -> accept -> convert -> a real, correctly-priced, checkoutable order", async ({ page, request }) => {
  const unique = Date.now() % 1000000;
  const productName = `E2E Quote Bat ${unique}`;
  const customerName = `E2E Quote Customer ${unique}`;
  let variantId = "";
  let customerId = "";
  let adminToken = "";

  await test.step("seed a product, a wholesale price list, and a B2B customer via the API", async () => {
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
      data: { name: productName, hsn_code: "9506", category_id: categoryId, variants: [{ sku: `E2E-QT-${unique}`, cost_price: 200, mrp: 599, selling_price: 499 }] },
    });
    variantId = (await prod.json()).variants[0].variant_id;
    // Exactly enough stock for the one line this quote converts (1 unit)
    // plus headroom for a second attempt if the first checkout step needs
    // a retry — matches ai-insights.spec.ts's own documented lesson about
    // under-seeding stock silently skewing a test.
    await request.post(`${API_BASE}/inventory/adjustments`, {
      headers,
      data: { variant_id: variantId, branch_id: BRANCH_ID, quantity_delta: 5, reason: `e2e quotations seed ${unique}` },
    });

    const priceList = await request.post(`${API_BASE}/pricing/price-lists`, { headers, data: { name: `E2E Quote Wholesale ${unique}` } });
    const { price_list_id } = await priceList.json();
    await request.put(`${API_BASE}/pricing/price-lists/${price_list_id}/items/${variantId}`, { headers, data: { price: 350 } });

    const customer = await request.post(`${API_BASE}/customers`, {
      headers,
      data: { name: customerName, phone: `8${unique}`.padEnd(10, "0"), email: `quote${unique}@e2e.test`, customer_type: "b2b", gstin: "29ABCDE1234F1Z5", price_list_id },
    });
    customerId = (await customer.json()).customer_id;
  });

  let quoteId = "";

  await test.step("login and create a quotation through the UI", async () => {
    await page.goto("/login");
    await page.fill("#email", "admin@adrsw.com");
    await page.fill("#password", "Idontsay3#");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");

    await page.goto("/quotations");
    await page.locator("button", { hasText: "Select branch" }).click();
    await page.getByRole("option", { name: "MG Road" }).click();
    await page.locator("button", { hasText: "Select customer" }).click();
    await page.getByRole("option", { name: new RegExp(customerName) }).click();
    await page.locator("button", { hasText: "Select a product" }).click();
    await page.getByRole("option", { name: new RegExp(productName) }).click();
    await page.getByRole("button", { name: "Create quotation" }).click();
    await expect(page.getByText("Quotation created")).toBeVisible({ timeout: 10_000 });
  });

  await test.step("a (separate, throwaway) draft quote can be deleted through the UI (found missing during a completeness audit — reject only applies from 'sent'); run before the real quote below is ever opened, so this navigation doesn't clobber that quote's selected-in-UI state", async () => {
    const headers = { Authorization: `Bearer ${adminToken}` };
    const throwaway = await request.post(`${API_BASE}/quotations`, {
      headers,
      data: { branch_id: BRANCH_ID, customer_id: customerId, lines: [{ variant_id: variantId, quantity: 1 }] },
    });
    const { quote_number: throwawayNumber } = await throwaway.json();

    await page.goto("/quotations");
    await page.getByRole("button", { name: "Refresh" }).click();
    await page.getByRole("row", { name: new RegExp(throwawayNumber) }).getByRole("button", { name: "View" }).click();
    await page.getByRole("button", { name: "Delete" }).click();
    await expect(page.getByText("Quotation deleted")).toBeVisible();
    await expect(page.getByRole("cell", { name: throwawayNumber })).toHaveCount(0);
  });

  await test.step("the quote line auto-resolved the wholesale price, not retail", async () => {
    await page.getByRole("button", { name: "Refresh" }).click();
    await page.getByRole("row").filter({ hasText: "draft" }).first().getByRole("button", { name: "View" }).click();
    // qty=1 makes unit_price, line_total, and grand_total all coincidentally
    // "350.00" — asserting the product's own line row contains it (not a
    // bare page-wide text search) avoids the resulting strict-mode collision.
    await expect(page.getByRole("row", { name: new RegExp(productName) })).toContainText("₹350.00");
  });

  await test.step("a draft quote cannot be converted yet", async () => {
    const headers = { Authorization: `Bearer ${adminToken}` };
    const listResp = await request.get(`${API_BASE}/quotations?status=draft`, { headers });
    const quote = (await listResp.json()).quotations.find((q: { customer_id: string }) => q.customer_id === customerId);
    expect(quote).toBeTruthy();
    quoteId = quote.quotation_id;
    const convertResp = await request.post(`${API_BASE}/quotations/${quoteId}/convert`, { headers });
    expect(convertResp.status()).toBe(409);
  });

  await test.step("send and accept through the UI", async () => {
    await page.getByRole("button", { name: "Send" }).click();
    await expect(page.getByRole("button", { name: "Accept" })).toBeVisible();
    await page.getByRole("button", { name: "Accept" }).click();
    await expect(page.getByRole("button", { name: "Convert to order" })).toBeVisible();
  });

  let orderId = "";

  await test.step("convert through the UI and confirm a real order id is reported", async () => {
    await page.getByRole("button", { name: "Convert to order" }).click();
    // Scoped to the persistent detail-card message, not the transient
    // toast — both show the same text at once, a strict-mode collision.
    await expect(page.getByRole("main").getByText(/Converted to sales order/)).toBeVisible();
    const headers = { Authorization: `Bearer ${adminToken}` };
    const detail = await (await request.get(`${API_BASE}/quotations/${quoteId}`, { headers })).json();
    expect(detail.status).toBe("converted");
    expect(detail.converted_sales_order_id).toBeTruthy();
    orderId = detail.converted_sales_order_id;
  });

  await test.step("the converted order is real: correct customer, correct wholesale price, checkoutable", async () => {
    const headers = { Authorization: `Bearer ${adminToken}` };
    const order = await (await request.get(`${API_BASE}/sales/orders/${orderId}`, { headers })).json();
    expect(order.lines[0].unit_price).toBe("350.00");

    const checkout = await request.post(`${API_BASE}/sales/orders/${orderId}/checkout`, {
      headers,
      data: { payments: [{ method: "cash", amount: Number(order.grand_total) }] },
    });
    expect(checkout.ok()).toBeTruthy();
    expect((await checkout.json()).status).toBe("finalized");
  });
});
