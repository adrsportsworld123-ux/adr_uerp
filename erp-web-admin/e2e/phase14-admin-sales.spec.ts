import { test, expect, Page, APIRequestContext } from "@playwright/test";

// Phase 14: a sale rung up in the back office through the browser, on the
// till's own code path. Checks the Definition of Done's parity (the same
// cart built on the POS through the API totals identically), then the
// after-sale actions on the detail page: payment-method correction with a
// manager's PIN, and a partial return. A POS user can't sell here.

const API = process.env.E2E_API_URL ?? "http://localhost:8080/api/v1";
const MERCHANT = process.env.E2E_MERCHANT_CODE ?? "acme-sports";
const BRANCH = "22222222-2222-2222-2222-222222222222";
const TILL = "33333333-3333-3333-3333-333333333333";
const BAT = "99999999-9999-9999-9999-999999999999";

async function login(page: Page, email: string) {
  await page.goto("/login");
  await page.fill("#email", email);
  await page.fill("#password", "Passw0rd!");
  await page.click('button[type="submit"]');
  await page.waitForURL("/dashboard");
}

async function token(request: APIRequestContext, email: string) {
  const r = await request.post(`${API}/auth/login`, { data: { merchant_code: MERCHANT, email, password: "Passw0rd!" } });
  return (await r.json()).access_token as string;
}

test("Phase 14: a back-office sale matches the till to the paisa, then is corrected and partly returned", async ({ page, request }) => {
  const admin = { Authorization: `Bearer ${await token(request, "arjun@acme-sports.test")}` };
  const cashier = { Authorization: `Bearer ${await token(request, "ravi@acme-sports.test")}` };
  const stock = await (await request.get(`${API}/inventory?branch_id=${BRANCH}&variant_id=${BAT}`, { headers: admin })).json();
  if (Number(stock.available) < 6) {
    await request.post(`${API}/inventory/adjustments`, { headers: admin, data: { variant_id: BAT, branch_id: BRANCH, quantity_delta: 20, reason: "e2e stock" } });
  }

  // The fixture cart on the till: 2 bats and a 5% discount.
  const pos = await (await request.post(`${API}/sales/orders`, { headers: cashier, data: { branch_id: BRANCH, pos_terminal_id: TILL, idempotency_key: `e2e-p14-${Date.now()}` } })).json();
  await request.post(`${API}/sales/orders/${pos.order_id}/lines`, { headers: cashier, data: { variant_id: BAT, quantity: 2 } });
  const posCart = await (await request.post(`${API}/sales/orders/${pos.order_id}/discounts`, { headers: cashier, data: { type: "manual", value: 5, reason: "fixture" } })).json();
  await request.delete(`${API}/sales/orders/${pos.order_id}/lines/${posCart.lines[0].line_id}`, { headers: cashier }); // release the stock hold

  await login(page, "arjun@acme-sports.test");
  let saleId = "";
  await test.step("ring up the same cart in the back office", async () => {
    await page.goto("/sales/new");
    await page.getByRole("button", { name: /Search by name, SKU or barcode/ }).click();
    await page.getByLabel("Search products").fill("SG-BAT-SH");
    await page.getByRole("option", { name: /SG-BAT-SH/ }).first().click();
    await page.fill("#sale-qty", "2");
    await page.getByRole("button", { name: "Add", exact: true }).click();
    await expect(page.getByText("SG-BAT-SH").first()).toBeVisible();
    await page.getByLabel("Discount percent").fill("5");
    await page.getByLabel("Discount reason").fill("fixture");
    await page.getByRole("button", { name: "Apply discount" }).click();
    await expect(page.getByText("Discount of 5% applied")).toBeVisible();

    const shown = await page.getByTestId("sale-total").textContent();
    const expected = `₹${Number(posCart.grand_total).toLocaleString("en-IN", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
    expect(shown).toBe(expected); // the Definition of Done's parity, to the paisa

    await page.getByRole("button", { name: /Fill remaining/ }).click();
    await page.getByRole("button", { name: /Complete sale/ }).click();
    await page.waitForURL(/\/sales\/[0-9a-f-]{36}$/);
    saleId = page.url().split("/").pop()!;
    await expect(page.getByText("Completed").first()).toBeVisible();
    await expect(page.getByText(/Back office/).first()).toBeVisible();
    await expect(page.getByRole("table", { name: "GST by rate" })).toContainText("18%");
  });

  await test.step("correct the payment method with a manager's PIN", async () => {
    await page.getByRole("button", { name: "Correct method" }).click();
    const dialog = page.getByRole("dialog");
    await dialog.getByLabel("Actually paid by").selectOption("card");
    await dialog.getByLabel("Reason").fill("Customer paid by card");
    await dialog.getByLabel("Manager's employee code").fill("EMP003");
    await dialog.getByLabel("Manager's PIN").fill("1234");
    await dialog.getByRole("button", { name: "Correct payment" }).click();
    await expect(page.getByText("Payment moved to Card")).toBeVisible();
    await expect(page.getByText(/Corrected Cash → Card/)).toBeVisible();
  });

  await test.step("return one bat", async () => {
    await page.getByRole("button", { name: "Return / refund" }).click();
    const dialog = page.getByRole("dialog");
    await dialog.getByLabel(/Quantity to return of/).fill("1");
    await dialog.getByLabel("Reason").fill("Wrong size");
    await dialog.getByLabel("Manager's employee code").fill("EMP003");
    await dialog.getByLabel("Manager's PIN").fill("1234");
    await dialog.getByRole("button", { name: /Refund about/ }).click();
    await expect(page.getByText(/refund ₹.* by Cash/)).toBeVisible();
    await expect(page.getByText("Partly refunded").first()).toBeVisible();
    await expect(page.getByRole("heading", { name: "Returns" })).toBeVisible();
  });

  await test.step("the sales list finds it under Back office", async () => {
    await page.goto("/sales");
    await page.getByLabel("Channel").selectOption("backoffice");
    await expect(page.locator(`a[href="/sales/${saleId}"], tr`).filter({ hasText: "Partly refunded" }).first()).toBeVisible();
  });

  await test.step("a POS user can't sell from the back office", async () => {
    await page.evaluate(() => localStorage.clear());
    await login(page, "ravi@acme-sports.test");
    await page.goto("/sales/new");
    await expect(page.getByText(/Needs the sales.backoffice permission/)).toBeVisible();
    await expect(page.getByRole("button", { name: /Complete sale/ })).toBeDisabled();
  });
});
