import { test, expect } from "@playwright/test";

// End-to-end coverage for Phase 10 (erp-core-go's phased_roadmap.md;
// internal/pricing/rules.go, schedule.go, internal/quotations/lines.go):
// a dynamic price rule created and checked through the Price Rules
// screen, a scheduled price change created and cancelled through its
// screen, and a draft quotation edited through the B2B Quotations screen
// — then converted, confirming via the API that the order bills the
// NEGOTIATED price (the conversion used to re-price at retail).
//
// The time-window logic itself (days, hours, overnight windows, branch
// timezone, seasons, precedence, MRP cap) is verified against the API
// directly; this spec proves the screens drive it. The rule here is
// active every day at all hours so the result doesn't depend on when the
// suite runs.
const API_BASE = "http://localhost:8080/api/v1";
const BRANCH_ID = "22222222-2222-2222-2222-222222222222";

test("Phase 10: price rule, scheduled change, and draft quotation editing through the real UI", async ({ page, request }) => {
  const unique = Date.now() % 1000000;
  const productName = `E2E P10 Bat ${unique}`;
  const ruleName = `E2E P10 Clearance ${unique}`;
  const customerName = `E2E P10 Customer ${unique}`;
  let headers: Record<string, string> = {};
  let variantId = "";
  let customerId = "";

  await test.step("seed a product with stock and a customer via the API", async () => {
    const login = await request.post(`${API_BASE}/auth/login`, {
      data: { merchant_code: "acme-sports", email: "admin@adrsw.com", password: "Idontsay3#" },
    });
    headers = { Authorization: `Bearer ${(await login.json()).access_token}` };

    const product = await request.post(`${API_BASE}/products`, {
      headers,
      data: { name: productName, variants: [{ sku: `E2E-P10-${unique}`, cost_price: 500, mrp: 1200, selling_price: 1000 }] },
    });
    expect(product.ok()).toBeTruthy();
    variantId = (await product.json()).variants[0].variant_id;
    await request.post(`${API_BASE}/inventory/adjustments`, {
      headers,
      data: { variant_id: variantId, branch_id: BRANCH_ID, quantity_delta: 10, reason: "E2E P10 stock" },
    });
    const customer = await request.post(`${API_BASE}/customers`, {
      headers,
      data: { name: customerName, phone: `7${String(unique).padStart(9, "0")}`, email: `e2e-p10-${unique}@test.invalid` },
    });
    expect(customer.ok()).toBeTruthy();
    customerId = (await customer.json()).customer_id;
  });

  await test.step("login", async () => {
    await page.goto("/login");
    await page.fill("#email", "admin@adrsw.com");
    await page.fill("#password", "Idontsay3#");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");
  });

  await test.step("create a 20%-off rule for the product through the Price Rules screen", async () => {
    await page.goto("/pricing/rules");
    await page.fill("#ruleName", ruleName);
    await page.locator("button", { hasText: "A whole category" }).click();
    await page.getByRole("option", { name: "One product variant" }).click();
    await page.locator("button", { hasText: "Choose a variant" }).first().click();
    await page.getByRole("option", { name: new RegExp(productName) }).click();
    await page.fill("#ruleValue", "-20");
    for (const day of ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"]) {
      await page.getByRole("button", { name: day, exact: true }).click();
    }
    await page.getByRole("button", { name: "Create rule" }).click();
    await expect(page.getByRole("row", { name: new RegExp(ruleName) })).toBeVisible();
    await expect(page.getByRole("row", { name: new RegExp(ruleName) })).toContainText("-20%");
  });

  await test.step("the price check shows the rule pricing the item at ₹800", async () => {
    await page.locator("button", { hasText: "Choose a variant" }).last().click();
    await page.getByRole("option", { name: new RegExp(productName) }).click();
    await page.locator("button", { hasText: "Choose a branch" }).click();
    await page.getByRole("option", { name: "MG Road" }).click();
    await page.getByRole("button", { name: "Check price" }).click();
    await expect(page.getByTestId("price-check-result")).toContainText("₹800.00");
    await expect(page.getByTestId("price-check-result")).toContainText(ruleName);
  });

  await test.step("deactivating the rule puts the item back at its base price, and an unused rule deletes", async () => {
    const row = page.getByRole("row", { name: new RegExp(ruleName) });
    await row.getByRole("button", { name: "Deactivate" }).click();
    await expect(row).toContainText("inactive");
    await page.getByRole("button", { name: "Check price" }).click();
    await expect(page.getByTestId("price-check-result")).toContainText("₹1000.00");
    await expect(page.getByTestId("price-check-result")).toContainText("base price");
    await row.getByRole("button", { name: "Delete" }).click();
    await expect(page.getByRole("row", { name: new RegExp(ruleName) })).toHaveCount(0);
  });

  await test.step("schedule a price change for tomorrow, see it pending, then cancel it", async () => {
    await page.goto("/pricing/scheduled-changes");
    await page.locator("button", { hasText: "Choose a product variant" }).click();
    await page.getByRole("option", { name: new RegExp(productName) }).click();
    await page.fill("#newSelling", "1100");
    const tomorrow = new Date(Date.now() + 24 * 3600 * 1000);
    const pad = (n: number) => String(n).padStart(2, "0");
    await page.fill("#effectiveAt", `${tomorrow.getFullYear()}-${pad(tomorrow.getMonth() + 1)}-${pad(tomorrow.getDate())}T09:00`);
    await page.fill("#reason", `E2E P10 revision ${unique}`);
    await page.getByRole("button", { name: "Schedule", exact: true }).click();

    const row = page.getByRole("row", { name: new RegExp(productName) });
    await expect(row).toContainText("Selling ₹1100.00");
    await expect(row).toContainText("pending");
    await row.getByRole("button", { name: "Cancel" }).click();
    await expect(page.getByRole("row", { name: new RegExp(productName) })).toHaveCount(0); // list is filtered to pending
  });

  let quotationId = "";
  await test.step("edit a draft quotation: negotiate the price, change quantity, edit notes", async () => {
    const created = await request.post(`${API_BASE}/quotations`, {
      headers,
      data: { branch_id: BRANCH_ID, customer_id: customerId, lines: [{ variant_id: variantId, quantity: 2 }] },
    });
    expect(created.ok()).toBeTruthy();
    const quote = await created.json();
    quotationId = quote.quotation_id;
    expect(quote.grand_total).toBe("2000.00");

    await page.goto("/quotations");
    await page.getByRole("row", { name: new RegExp(quote.quote_number) }).getByRole("button", { name: "View" }).click();

    await page.getByLabel("Unit price", { exact: true }).fill("950");
    await page.getByRole("button", { name: "Save", exact: true }).click();
    await expect(page.getByText("Grand total: ₹1900.00")).toBeVisible();

    await page.getByLabel("Quantity", { exact: true }).fill("3");
    await page.getByRole("button", { name: "Save", exact: true }).click();
    await expect(page.getByText("Grand total: ₹2850.00")).toBeVisible();

    await page.fill("#editNotes", "Net 30, delivery Friday");
    await page.getByRole("button", { name: "Save details" }).click();
    await expect(page.locator("#editNotes")).toHaveValue("Net 30, delivery Friday");
  });

  await test.step("once sent, the quote is read-only; converted, it bills the negotiated price", async () => {
    await page.getByRole("button", { name: "Send", exact: true }).click();
    await expect(page.getByLabel("Unit price", { exact: true })).toHaveCount(0);
    await expect(page.getByText("Net 30, delivery Friday")).toBeVisible();

    await request.post(`${API_BASE}/quotations/${quotationId}/accept`, { headers });
    const converted = await request.post(`${API_BASE}/quotations/${quotationId}/convert`, { headers });
    expect(converted.ok()).toBeTruthy();
    const orderId = (await converted.json()).order_id;
    const order = await (await request.get(`${API_BASE}/sales/orders/${orderId}`, { headers })).json();
    expect(order.lines).toHaveLength(1);
    expect(order.lines[0].unit_price).toBe("950.00");
    expect(order.lines[0].quantity).toBe("3.000");
    expect(order.grand_total).toBe("2850.00");
  });
});
