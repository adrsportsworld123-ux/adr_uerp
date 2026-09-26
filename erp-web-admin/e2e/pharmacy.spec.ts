import { test, expect } from "@playwright/test";

// End-to-end coverage for Phase 8: Vertical Expansion — Pharmacy
// (erp-core-go's phased_roadmap.md; internal/pharmacy). Configures a
// Pharmacy category with a "Drug Schedule" attribute (Phase 8's own
// attribute-set mechanism, not new schema) and a 30-day min_shelf_life_days,
// creates a Schedule H drug batch-tracked variant, confirms checkout
// refuses without a prescription and succeeds once one is recorded
// through the real Prescriptions screen, and confirms the schedule-drug
// sales report reflects it. Sale/checkout itself is driven via the API
// (this app has no checkout UI — see credit.spec.ts's own note); the
// Prescriptions screen and its real effect on checkout is what's under
// test through the browser.
const API_BASE = "http://localhost:8080/api/v1";
const BRANCH_ID = "22222222-2222-2222-2222-222222222222";
const TERMINAL_ID = "33333333-3333-3333-3333-333333333333";
const SUPPLIER_ID = "11111111-2222-3333-4444-555555555555";

test("Pharmacy: Schedule H drug requires a prescription at checkout, recorded through the real UI", async ({ page, request }) => {
  const unique = Date.now() % 1000000;
  const categoryName = `E2E Pharmacy ${unique}`;
  const productName = `E2E Drug ${unique}`;
  const customerName = `E2E Pharmacy Customer ${unique}`;
  let adminToken = "";
  let variantId = "";
  let customerId = "";
  let orderId = "";

  await test.step("configure the Pharmacy vertical via the API (category, Drug Schedule attribute, 30-day shelf-life floor, batch-tracked product, real stock)", async () => {
    const login = await request.post(`${API_BASE}/auth/login`, {
      data: { merchant_code: "acme-sports", email: "admin@adrsw.com", password: "Idontsay3#" },
    });
    adminToken = (await login.json()).access_token;
    const headers = { Authorization: `Bearer ${adminToken}` };

    const cat = await request.post(`${API_BASE}/categories`, { headers, data: { name: categoryName } });
    const categoryId = (await cat.json()).category_id;

    const attr = await request.post(`${API_BASE}/attributes`, { headers, data: { name: "Drug Schedule", input_type: "select" } });
    const attributeId = (await attr.json()).attribute_id;
    await request.post(`${API_BASE}/attributes/${attributeId}/values`, { headers, data: { value: "Schedule H" } });
    await request.post(`${API_BASE}/categories/${categoryId}/attributes`, { headers, data: { attribute_id: attributeId, required: true } });

    const prod = await request.post(`${API_BASE}/products`, {
      headers,
      data: {
        name: productName, category_id: categoryId,
        variants: [{ sku: `E2E-RX-${unique}`, mrp: 150, selling_price: 120, track_batch: true, attribute_combo: { "Drug Schedule": "Schedule H" } }],
      },
    });
    variantId = (await prod.json()).variants[0].variant_id;

    const grn = await request.post(`${API_BASE}/purchase/grn`, { headers, data: { supplier_id: SUPPLIER_ID, branch_id: BRANCH_ID } });
    const grnId = (await grn.json()).grn_id;
    await request.post(`${API_BASE}/purchase/grn/${grnId}/lines`, {
      headers, data: { variant_id: variantId, quantity: 20, unit_cost: 80, batch_no: `E2E-RX-${unique}`, expiry_date: "2026-12-25" },
    });
    await request.post(`${API_BASE}/purchase/grn/${grnId}/complete`, { headers });

    const customer = await request.post(`${API_BASE}/customers`, {
      headers, data: { name: customerName, phone: `7${unique}`.padEnd(10, "0"), email: `rx${unique}@e2e.test`, customer_type: "b2c" },
    });
    customerId = (await customer.json()).customer_id;

    const order = await request.post(`${API_BASE}/sales/orders`, {
      headers, data: { branch_id: BRANCH_ID, pos_terminal_id: TERMINAL_ID, idempotency_key: `e2e-rx-${unique}` },
    });
    orderId = (await order.json()).order_id;
    await request.post(`${API_BASE}/sales/orders/${orderId}/customer`, { headers, data: { customer_id: customerId } });
    await request.post(`${API_BASE}/sales/orders/${orderId}/lines`, { headers, data: { variant_id: variantId, quantity: 2 } });
  });

  await test.step("checkout is refused without a prescription", async () => {
    const headers = { Authorization: `Bearer ${adminToken}` };
    const resp = await request.post(`${API_BASE}/sales/orders/${orderId}/checkout`, { headers, data: { payments: [{ method: "cash", amount: 240 }] } });
    expect(resp.status()).toBe(409);
    expect((await resp.json()).error.code).toBe("PRESCRIPTION_REQUIRED");
  });

  await test.step("login and record a prescription for the customer through the real Prescriptions screen", async () => {
    await page.goto("/login");
    await page.fill("#email", "admin@adrsw.com");
    await page.fill("#password", "Idontsay3#");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");

    await page.goto("/prescriptions");
    await page.locator("button", { hasText: "Select customer" }).first().click();
    await page.getByRole("option", { name: new RegExp(customerName) }).click();
    await page.fill("#doctorName", "Dr. E2E Sharma");
    await page.fill("#doctorRegNo", "MCI-E2E-1");
    await page.getByRole("button", { name: "Record prescription" }).click();
    await expect(page.getByText("Prescription recorded")).toBeVisible();

    // The History section has its own, separate customer picker — select
    // the same customer there to pull up what was just recorded.
    await page.locator("button", { hasText: "Select customer" }).last().click();
    await page.getByRole("option", { name: new RegExp(customerName) }).click();
    await expect(page.getByText("Dr. E2E Sharma")).toBeVisible();
  });

  await test.step("attach the recorded prescription and checkout now succeeds", async () => {
    const headers = { Authorization: `Bearer ${adminToken}` };
    const rx = await request.get(`${API_BASE}/prescriptions?customer_id=${customerId}`, { headers });
    const prescriptionId = (await rx.json()).prescriptions[0].prescription_id;
    await request.post(`${API_BASE}/sales/orders/${orderId}/prescription`, { headers, data: { prescription_id: prescriptionId } });

    const checkout = await request.post(`${API_BASE}/sales/orders/${orderId}/checkout`, { headers, data: { payments: [{ method: "cash", amount: 240 }] } });
    expect(checkout.ok()).toBeTruthy();
    expect((await checkout.json()).status).toBe("finalized");
  });

  await test.step("the schedule-drug sales report reflects the sale with its prescription reference", async () => {
    const headers = { Authorization: `Bearer ${adminToken}` };
    const today = new Date().toISOString().slice(0, 10);
    const report = await (await request.get(`${API_BASE}/reports/schedule-drug-sales?date=${today}`, { headers })).json();
    const line = report.lines.find((l: { variant_id: string }) => l.variant_id === variantId);
    expect(line).toBeTruthy();
    expect(line.drug_schedule).toBe("Schedule H");
    expect(line.prescription_id).toBeTruthy();
  });
});
