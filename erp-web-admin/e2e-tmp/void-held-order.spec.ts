import { test, expect } from "@playwright/test";

// Voiding a held order from its sale page (erp-core-go void_held.go): a
// cart is parked through the API, then dropped in the browser with a
// reason; its items go back on sale and it shows under "Released".
const API = "http://localhost:8081/api/v1";
const BRANCH = "22222222-2222-2222-2222-222222222222";
const TERMINAL = "33333333-3333-3333-3333-333333333333";

test("void a held order with a reason", async ({ page, request }) => {
  const login = await request.post(`${API}/auth/login`, {
    data: { merchant_code: "acme-sports", email: "arjun@acme-sports.test", password: "Passw0rd!" },
  });
  const headers = { Authorization: `Bearer ${(await login.json()).access_token}` };
  const order = await (await request.post(`${API}/sales/orders`, { headers, data: { branch_id: BRANCH, pos_terminal_id: TERMINAL, idempotency_key: `e2e-vh-${Date.now()}` } })).json();
  await request.post(`${API}/sales/orders/${order.order_id}/lines`, { headers, data: { variant_id: "99999999-9999-9999-9999-999999999999", quantity: 1 } });
  const held = await request.post(`${API}/sales/orders/${order.order_id}/hold`, { headers, data: { note: "e2e" } });
  expect(held.status()).toBe(200);

  await page.goto("/login");
  await page.fill("#email", "arjun@acme-sports.test");
  await page.fill("#password", "Passw0rd!");
  await page.click('button[type="submit"]');
  await page.waitForURL("/dashboard");
  await page.goto(`/sales/${order.order_id}`);
  await page.getByRole("button", { name: "Void held order" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("go back on sale");
  await expect(dialog.getByRole("button", { name: "Void held order" })).toBeDisabled();
  await dialog.getByLabel("Reason").fill("Customer left without paying");
  await dialog.getByRole("button", { name: "Void held order" }).click();
  await expect(page.getByText(/voided — its items are back on sale/)).toBeVisible();
  await expect(page.getByText("Released").first()).toBeVisible();

  await page.goto("/sales?status=held_voided");
  await expect(page.getByText(order.order_number)).toBeVisible();
});
