import { test, expect, APIRequestContext } from "@playwright/test";

// A fixed rupee discount on the whole sale (₹200 off), as opposed to a
// percent: rung up in the back office, it must take exactly that amount off
// the pre-tax value and total to the paisa what the till gets for the same
// cart through the API. A larger amount asks for a manager the same way the
// percent it comes to would. The sale's detail lists it without a percent.

const API = "http://localhost:8081/api/v1";
const MERCHANT = process.env.E2E_MERCHANT_CODE ?? "acme-sports";
const BRANCH = "22222222-2222-2222-2222-222222222222";
const TILL = "33333333-3333-3333-3333-333333333333";
const BAT = "99999999-9999-9999-9999-999999999999"; // ₹2,299, GST 18% on top

async function token(request: APIRequestContext, email: string) {
  const r = await request.post(`${API}/auth/login`, { data: { merchant_code: MERCHANT, email, password: "Passw0rd!" } });
  return (await r.json()).access_token as string;
}

const inr = (v: string | number) => `₹${Number(v).toLocaleString("en-IN", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;

test("a fixed ₹ discount takes exactly that amount off, matching the till", async ({ page, request }) => {
  const admin = { Authorization: `Bearer ${await token(request, "arjun@acme-sports.test")}` };
  const cashier = { Authorization: `Bearer ${await token(request, "ravi@acme-sports.test")}` };
  const stock = await (await request.get(`${API}/inventory?branch_id=${BRANCH}&variant_id=${BAT}`, { headers: admin })).json();
  if (Number(stock.available) < 4) {
    await request.post(`${API}/inventory/adjustments`, { headers: admin, data: { variant_id: BAT, branch_id: BRANCH, quantity_delta: 20, reason: "e2e stock" } });
  }

  // The till's cart: 2 bats, ₹200 off.
  const pos = await (await request.post(`${API}/sales/orders`, { headers: cashier, data: { branch_id: BRANCH, pos_terminal_id: TILL, idempotency_key: `e2e-fixed-${Date.now()}` } })).json();
  await request.post(`${API}/sales/orders/${pos.order_id}/lines`, { headers: cashier, data: { variant_id: BAT, quantity: 2 } });
  const posCart = await (
    await request.post(`${API}/sales/orders/${pos.order_id}/discounts`, { headers: cashier, data: { type: "manual", mode: "amount", amount: 200, reason: "fixture" } })
  ).json();
  expect(posCart.discount_total).toBe("200.00");
  await request.delete(`${API}/sales/orders/${pos.order_id}/lines/${posCart.lines[0].line_id}`, { headers: cashier }); // release the stock hold

  await page.goto("/login");
  await page.fill("#email", "arjun@acme-sports.test");
  await page.fill("#password", "Passw0rd!");
  await page.click('button[type="submit"]');
  await page.waitForURL("/dashboard");

  await page.goto("/sales/new");
  await page.getByRole("button", { name: /Search by name, SKU or barcode/ }).click();
  await page.getByLabel("Search products").fill("SG-BAT-SH");
  await page.getByRole("option", { name: /SG-BAT-SH/ }).first().click();
  await page.fill("#sale-qty", "2");
  await page.getByRole("button", { name: "Add", exact: true }).click();
  await expect(page.getByText("SG-BAT-SH").first()).toBeVisible();

  await test.step("an amount above the free tier asks for a manager", async () => {
    await page.getByRole("group", { name: "Discount type" }).getByRole("button", { name: "₹" }).click();
    await page.getByLabel("Discount amount").fill("500"); // 10.87% of ₹4,598
    await expect(page.getByTestId("discount-equivalent")).toHaveText("10.87% of the sale");
    await expect(page.getByLabel("Approver employee code")).toBeVisible();
    await page.getByLabel("Discount reason").fill("fixture");
    await expect(page.getByRole("button", { name: "Apply discount" })).toBeDisabled();
  });

  await test.step("₹200 applies without approval and matches the till", async () => {
    await page.getByLabel("Discount amount").fill("200"); // 4.35%
    await expect(page.getByLabel("Approver employee code")).toBeHidden();
    await page.getByRole("button", { name: "Apply discount" }).click();
    await expect(page.getByText("Discount of ₹200.00 applied")).toBeVisible();
    await expect(page.getByText("−₹200.00")).toBeVisible();
    expect(await page.getByTestId("sale-total").textContent()).toBe(inr(posCart.grand_total));
  });

  await test.step("the completed sale lists the discount without a percent", async () => {
    await page.getByRole("button", { name: /Fill remaining/ }).click();
    await page.getByRole("button", { name: /Complete sale/ }).click();
    await page.waitForURL(/\/sales\/[0-9a-f-]{36}$/);
    const discounts = page.locator("section").filter({ has: page.getByRole("heading", { name: "Discounts" }) });
    await expect(discounts).toContainText("Manual · ₹200.00");
    await expect(discounts).not.toContainText("0%");
  });
});
