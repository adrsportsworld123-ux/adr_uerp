import { test, expect, APIRequestContext } from "@playwright/test";

// End-to-end coverage for Notifications (expanded)'s admin surface
// (erp-core-go's phased_roadmap.md Phase 3, sub-area 3) against a real
// erp-core-go backend. Requires the same setup as
// purchase-and-accounting.spec.ts (backend running, seed passwords set).
//
// GET /notifications is the first endpoint in this app whose READ, not
// just its writes, is permission-gated (notifications.view) — this test
// covers both sides of that: Branch Manager sees the log, POS User sees
// the API's own permission-denied message instead of a blank/broken page.
const API_BASE = "http://localhost:8080/api/v1";
const BRANCH_ID = "22222222-2222-2222-2222-222222222222";
const TERMINAL_ID = "33333333-3333-3333-3333-333333333333";
const VARIANT_ID = "99999999-9999-9999-9999-999999999999";

async function apiLogin(request: APIRequestContext, email: string, password: string): Promise<string> {
  const resp = await request.post(`${API_BASE}/auth/login`, {
    data: { merchant_code: "acme-sports", email, password },
  });
  expect(resp.ok()).toBeTruthy();
  const body = await resp.json();
  return body.access_token as string;
}

test("reorder-point configuration, permission gate, and the sent-notification log", async ({ page, request }) => {
  const unique = Date.now() % 1000000;

  await test.step("seed a real finalized sale with an emailed receipt via the API directly", async () => {
    // Same "seed via API, assert via UI" split promotions.spec.ts already
    // established — this app has no cart/checkout UI, but a receipt
    // notification needs a real finalized order with a customer email to
    // exist before the log has anything meaningful to show.
    const token = await apiLogin(request, "ravi@acme-sports.test", "Passw0rd!");
    const auth = { Authorization: `Bearer ${token}` };

    const custResp = await request.post(`${API_BASE}/customers`, {
      headers: auth,
      data: { name: `E2E Notify ${unique}`, phone: `9${unique}`, email: `notify${unique}@example.com` },
    });
    expect(custResp.ok()).toBeTruthy();
    const customer = await custResp.json();

    const orderResp = await request.post(`${API_BASE}/sales/orders`, {
      headers: auth,
      data: { branch_id: BRANCH_ID, pos_terminal_id: TERMINAL_ID, idempotency_key: `e2e-notify-${unique}` },
    });
    expect(orderResp.ok()).toBeTruthy();
    const order = await orderResp.json();

    await request.post(`${API_BASE}/sales/orders/${order.order_id}/customer`, {
      headers: auth,
      data: { customer_id: customer.customer_id },
    });
    const lineResp = await request.post(`${API_BASE}/sales/orders/${order.order_id}/lines`, {
      headers: auth,
      data: { variant_id: VARIANT_ID, quantity: 1 },
    });
    expect(lineResp.ok()).toBeTruthy();
    const cart = await lineResp.json();

    const checkoutResp = await request.post(`${API_BASE}/sales/orders/${order.order_id}/checkout`, {
      headers: auth,
      data: { payments: [{ method: "cash", amount: Number(cart.grand_total) }] },
    });
    expect(checkoutResp.ok()).toBeTruthy();
  });

  await test.step("login as Branch Manager (Meera)", async () => {
    await page.goto("/login");
    await page.fill("#email", "meera@acme-sports.test");
    await page.fill("#password", "9999");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");
  });

  await test.step("the sent-notification log shows the seeded receipt email", async () => {
    await page.goto("/notifications");
    await page.getByRole("combobox").first().click();
    await page.getByRole("option", { name: "Receipt" }).click();
    await expect(page.getByRole("cell", { name: `notify${unique}@example.com` })).toBeVisible();
  });

  await test.step("set a reorder point for the seed product and see stock levels", async () => {
    // Page has two Select groups in DOM order: the log's 3 filters
    // (category/channel/status), then this card's 2 (branch/product) —
    // indices 3 and 4 are unambiguous rather than scoping by ancestor.
    const comboboxes = page.getByRole("combobox");
    await comboboxes.nth(3).click();
    await page.getByRole("option", { name: "MG Road" }).click();
    await comboboxes.nth(4).click();
    await page.getByRole("option", { name: /SG Cricket Bat/ }).click();

    await expect(page.getByText("On hand")).toBeVisible();
    await page.fill("#reorderPoint", "3");
    await page.getByRole("button", { name: "Save" }).click();
    await expect(page.getByText("Reorder point saved")).toBeVisible();
  });

  await test.step("POS User (Ravi) sees the permission-denied message on the notification log", async () => {
    await page.goto("/dashboard");
    await page.click("text=Log out");
    await page.waitForURL("/login");
    await page.fill("#password", "Passw0rd!");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");

    await page.goto("/notifications");
    await expect(page.getByText(/notifications\.view/)).toBeVisible();
  });

  await test.step("POS User can still look up stock but is denied saving a reorder point", async () => {
    const comboboxes = page.getByRole("combobox");
    await comboboxes.nth(3).click();
    await page.getByRole("option", { name: "MG Road" }).click();
    await comboboxes.nth(4).click();
    await page.getByRole("option", { name: /SG Cricket Bat/ }).click();
    await expect(page.getByText("On hand")).toBeVisible();

    await page.fill("#reorderPoint", "7");
    await page.getByRole("button", { name: "Save" }).click();
    await expect(page.getByText(/inventory\.adjust/)).toBeVisible();
  });
});
