import { test, expect, APIRequestContext } from "@playwright/test";

// End-to-end coverage for Promotions & Loyalty's admin surface
// (erp-core-go's phased_roadmap.md Phase 3, sub-area 2) against a real
// erp-core-go backend. Requires the same setup as
// purchase-and-accounting.spec.ts (backend running, seed passwords set —
// Ravi "Passw0rd!", Meera "9999"). Promotion/coupon/loyalty-config writes
// are gated by promotions.manage/loyalty.manage, so most of this runs as
// Branch Manager (Meera), matching pricing.spec.ts's precedent.
//
// This app has no cart/checkout UI (that's erp-pos-flutter's job — see
// this repo's own CLAUDE.md), so verifying the read-only loyalty
// balance/ledger on a customer's detail page needs a real finalized sale
// to exist first. Rather than skip that check, this test drives the real
// erp-core-go API directly (bypassing the browser) to create one, then
// switches back to the browser to confirm the UI renders what that sale
// produced — the same "seed via API, assert via UI" split
// purchase-and-accounting.spec.ts's own backend setup already implies is
// fine for this codebase.
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

test("promotion/coupon CRUD, permission gate, and a customer's loyalty ledger", async ({ page, request }) => {
  const unique = Date.now() % 1000000;

  await test.step("login as Branch Manager (Meera)", async () => {
    await page.goto("/login");
    await page.fill("#email", "meera@acme-sports.test");
    await page.fill("#password", "9999");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");
  });

  const promoName = `E2E Promo ${unique}`;
  await test.step("create a percent promotion", async () => {
    await page.goto("/promotions");
    await page.fill("#promoName", promoName);
    await page.fill("#valuePercent", "10");
    await page.click('button[type="submit"]:has-text("Create promotion")');
    await expect(page.getByText("Promotion created")).toBeVisible();
    await expect(page.getByRole("cell", { name: promoName })).toBeVisible();
  });

  await test.step("deactivate the promotion", async () => {
    const row = page.getByRole("row", { name: new RegExp(promoName) });
    await row.getByRole("button", { name: "Deactivate" }).click();
    await expect(page.getByText("Promotion deactivated")).toBeVisible();
    await expect(row.getByText("inactive")).toBeVisible();
  });

  const couponCode = `E2E${unique}`;
  await test.step("create a flat-amount coupon", async () => {
    await page.fill("#code", couponCode);
    await page.fill("#couponValue", "50");
    await page.click('button[type="submit"]:has-text("Create coupon")');
    await expect(page.getByText("Coupon created")).toBeVisible();
    await expect(page.getByRole("cell", { name: couponCode })).toBeVisible();
  });

  await test.step("update loyalty settings", async () => {
    // earn_rupees_per_point deliberately set to 1: other specs in this
    // suite (pricing.spec.ts) intentionally reprice the seed catalog
    // variant down to near-zero as part of their own negative-margin
    // test, and specs share one live database — a higher rate here would
    // make the "seed a sale, expect >0 points earned" step below flaky
    // depending on suite run order.
    await page.fill("#earnRate", "1");
    await page.fill("#redeemRate", "5");
    await page.fill("#expiryMonths", "6");
    await page.getByRole("button", { name: "Save" }).click();
    await expect(page.getByText("Loyalty settings saved")).toBeVisible();
  });

  await test.step("POS User (Ravi) is denied creating a promotion", async () => {
    await page.goto("/dashboard");
    await page.click("text=Log out");
    await page.waitForURL("/login");
    await page.fill("#password", "Passw0rd!");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");

    await page.goto("/promotions");
    await page.fill("#promoName", `Denied ${unique}`);
    await page.fill("#valuePercent", "5");
    await page.click('button[type="submit"]:has-text("Create promotion")');
    // .text-red-600 scopes to the actual API error, not the sidebar's
    // "Roles & Permissions" nav link, which a bare text search also
    // matches now that that screen exists.
    await expect(page.locator(".text-red-600", { hasText: /permission|forbidden/i })).toBeVisible();
  });

  let customerId = "";
  await test.step("register a customer via the UI", async () => {
    await page.goto("/customers/new");
    await page.fill("#name", `E2E Loyalty ${unique}`);
    await page.fill("#phone", `9${unique}`);
    await page.fill("#email", `loyalty${unique}@example.com`);
    await page.click('button[type="submit"]');
    await page.waitForURL(/\/customers\/[0-9a-f-]+/);
    customerId = page.url().split("/customers/")[1];
  });

  await test.step("seed a real finalized sale for this customer via the API directly", async () => {
    const token = await apiLogin(request, "ravi@acme-sports.test", "Passw0rd!");
    const auth = { Authorization: `Bearer ${token}` };

    const orderResp = await request.post(`${API_BASE}/sales/orders`, {
      headers: auth,
      data: { branch_id: BRANCH_ID, pos_terminal_id: TERMINAL_ID, idempotency_key: `e2e-loyalty-${unique}` },
    });
    expect(orderResp.ok()).toBeTruthy();
    const order = await orderResp.json();

    await request.post(`${API_BASE}/sales/orders/${order.order_id}/customer`, {
      headers: auth,
      data: { customer_id: customerId },
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

  await test.step("the customer's detail page shows the earned loyalty points", async () => {
    await page.goto(`/customers/${customerId}`);
    await expect(page.getByText(/\d+ points/)).toBeVisible();
    await expect(page.getByRole("cell", { name: "earn", exact: true })).toBeVisible();
  });
});
