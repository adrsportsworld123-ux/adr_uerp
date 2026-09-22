import { test, expect, APIRequestContext } from "@playwright/test";

// End-to-end coverage for Phase 4's first sub-area, B2B Credit Facility
// (erp-core-go's phased_roadmap.md), against a real erp-core-go backend.
// Requires the same setup as purchase-and-accounting.spec.ts (backend
// running, seed passwords set). credit.manage gates the settings write,
// so that part runs as Branch Manager (Meera), matching pricing.spec.ts's
// precedent — recording a payment isn't gated (mirrors
// POST /purchase/bills/{id}/payments' own precedent), so that part runs
// as the default POS User.
//
// This app has no checkout UI — a credit sale is created via a direct API
// call (same "seed via API, assert via UI" split promotions.spec.ts and
// notifications.spec.ts already established), then the credit card's
// outstanding/aging/payment-recording is driven for real through the
// browser.
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

test("credit limit configuration, a credit sale's outstanding/aging, and recording a payment", async ({ page, request }) => {
  const unique = Date.now() % 1000000;
  let customerId = "";
  let orderId = "";
  let grandTotal = "";

  await test.step("register a B2B customer and a credit sale for them via the API directly", async () => {
    const token = await apiLogin(request, "ravi@acme-sports.test", "Passw0rd!");
    const auth = { Authorization: `Bearer ${token}` };

    const custResp = await request.post(`${API_BASE}/customers`, {
      headers: auth,
      data: {
        name: `E2E Credit ${unique}`, phone: `9${unique}`, email: `credit${unique}@example.com`,
        customer_type: "b2b", gstin: "29ABCDE1234F1Z5",
      },
    });
    expect(custResp.ok()).toBeTruthy();
    customerId = (await custResp.json()).customer_id;

    // credit_limit defaults to 0 — raise it first (as Merchant Admin via
    // the API here; the UI's own write path is exercised later in this
    // test) so the credit sale below is actually within limit.
    const adminToken = await apiLogin(request, "arjun@acme-sports.test", "Passw0rd!");
    await request.patch(`${API_BASE}/customers/${customerId}/credit`, {
      headers: { Authorization: `Bearer ${adminToken}` },
      data: { credit_limit: 100000, payment_terms: "net_30" },
    });

    const orderResp = await request.post(`${API_BASE}/sales/orders`, {
      headers: auth,
      data: { branch_id: BRANCH_ID, pos_terminal_id: TERMINAL_ID, idempotency_key: `e2e-credit-${unique}` },
    });
    expect(orderResp.ok()).toBeTruthy();
    orderId = (await orderResp.json()).order_id;

    await request.post(`${API_BASE}/sales/orders/${orderId}/customer`, { headers: auth, data: { customer_id: customerId } });
    const lineResp = await request.post(`${API_BASE}/sales/orders/${orderId}/lines`, {
      headers: auth,
      data: { variant_id: VARIANT_ID, quantity: 1 },
    });
    expect(lineResp.ok()).toBeTruthy();
    grandTotal = (await lineResp.json()).grand_total;

    const checkoutResp = await request.post(`${API_BASE}/sales/orders/${orderId}/checkout`, {
      headers: auth,
      data: { payments: [{ method: "credit", amount: Number(grandTotal) }] },
    });
    expect(checkoutResp.ok()).toBeTruthy();
  });

  await test.step("login as Branch Manager (Meera) and see the outstanding balance", async () => {
    await page.goto("/login");
    await page.fill("#email", "meera@acme-sports.test");
    await page.fill("#password", "9999");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");

    await page.goto(`/customers/${customerId}`);
    await expect(page.getByText(`₹${grandTotal}`).first()).toBeVisible();
  });

  await test.step("edit credit settings through the UI", async () => {
    await page.getByRole("button", { name: "Edit" }).nth(1).click(); // Profile's own Edit is nth(0)
    await page.fill("#creditLimit", "50000");
    await page.getByRole("button", { name: "Save" }).click();
    await expect(page.getByText("Credit settings saved")).toBeVisible();
    await expect(page.getByText("₹50000.00")).toBeVisible();
  });

  await test.step("POS User (Ravi) can view the credit card but is denied editing it", async () => {
    await page.goto("/dashboard");
    await page.click("text=Log out");
    await page.waitForURL("/login");
    await page.fill("#password", "Passw0rd!");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");

    await page.goto(`/customers/${customerId}`);
    await page.getByRole("button", { name: "Edit" }).nth(1).click();
    await page.fill("#creditLimit", "999999");
    await page.getByRole("button", { name: "Save" }).click();
    await expect(page.getByText(/credit\.manage/)).toBeVisible();
  });

  await test.step("record a payment against the open invoice", async () => {
    await page.goto(`/customers/${customerId}`);
    await page.getByRole("button", { name: "Record payment" }).click();
    await page.locator("input[type='number']").last().fill(grandTotal);
    await page.getByRole("button", { name: "Record" }).click();
    await expect(page.getByText("Payment recorded")).toBeVisible();
    await expect(page.getByText("No outstanding credit sales")).toBeVisible();
  });
});
