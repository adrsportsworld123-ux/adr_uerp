import { test, expect, APIRequestContext, Page } from "@playwright/test";

// Sale round-off waiver (erp-core-go migrations/059): Sales Policy sets the
// limit; a back-office sale of ₹2,712.82 is settled for ₹2,712.00; the
// sale shows the round-off and what was paid; a full return refunds
// exactly what was paid, not the invoice value.

const API = process.env.E2E_API_URL ?? "http://localhost:8080/api/v1";
const MERCHANT = process.env.E2E_MERCHANT_CODE ?? "acme-sports";

async function token(request: APIRequestContext) {
  const r = await request.post(`${API}/auth/login`, { data: { merchant_code: MERCHANT, email: "arjun@acme-sports.test", password: "Passw0rd!" } });
  return (await r.json()).access_token as string;
}

async function login(page: Page) {
  await page.goto("/login");
  await page.fill("#email", "arjun@acme-sports.test");
  await page.fill("#password", "Passw0rd!");
  await page.click('button[type="submit"]');
  await page.waitForURL("/dashboard");
}

test("round-off: set in Sales Policy, taken on a back-office sale, given back on return", async ({ page, request }) => {
  const auth = { Authorization: `Bearer ${await token(request)}` };
  // Only the round-off fields change; the return policy other specs rely on stays as it is.
  const before = await (await request.get(`${API}/sales/settings`, { headers: auth })).json();
  const restore = { returns_require_approval: before.returns_require_approval, return_window_days: before.return_window_days };
  await request.put(`${API}/sales/settings`, { headers: auth, data: { ...restore, round_off_max: 0, round_off_auto: false } });
  await login(page);

  await test.step("switch it on in Sales Policy", async () => {
    await page.goto("/sales-settings");
    await page.getByLabel("Let the cashier waive a small balance").check();
    await page.fill("#round-off-max", "6");
    await expect(page.getByText("Enter an amount above ₹0 and up to ₹5")).toBeVisible();
    await page.fill("#round-off-max", "2");
    await page.getByRole("button", { name: "Save" }).click();
    await expect(page.getByText("Sales policy saved")).toBeVisible();
    const s = await (await request.get(`${API}/sales/settings`, { headers: auth })).json();
    expect(s.round_off_max).toBe("2.00");
  });

  let saleUrl = "";
  await test.step("a ₹2,712.82 sale settled for ₹2,712.00", async () => {
    await page.goto("/sales/new");
    await page.getByRole("button", { name: /Search by name, SKU or barcode/ }).click();
    await page.getByLabel("Search products").fill("SG-BAT-SH");
    await page.getByRole("option", { name: /SG-BAT-SH/ }).first().click();
    await page.fill("#sale-qty", "1");
    await page.getByRole("button", { name: "Add", exact: true }).click();
    await expect(page.getByTestId("sale-total")).toHaveText("₹2,712.82");

    await page.getByLabel("Round off").check();
    await expect(page.getByLabel("Amount waived")).toHaveValue("0.82");
    await expect(page.getByTestId("sale-payable")).toHaveText("₹2,712.00");
    await page.getByLabel("Amount waived").fill("2.50");
    await expect(page.getByText("Up to ₹2.00")).toBeVisible();
    await expect(page.getByRole("button", { name: /Complete sale/ })).toBeDisabled();
    await page.getByLabel("Amount waived").fill("0.82");

    await page.getByRole("button", { name: /Fill remaining/ }).click();
    await page.getByRole("button", { name: "Complete sale · ₹2,712.00" }).click();
    await page.waitForURL(/\/sales\/[0-9a-f-]{36}$/);
    saleUrl = page.url();
    await expect(page.getByTestId("sale-round-off")).toHaveText("−₹0.82");
    await expect(page.getByTestId("sale-payable")).toHaveText("₹2,712.00");
  });

  await test.step("the full return refunds what was paid", async () => {
    await page.getByRole("button", { name: "Return / refund" }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByTestId("return-round-off-note")).toBeVisible();
    await dialog.getByLabel(/Quantity to return of/).fill("1");
    await dialog.getByLabel("Reason").fill("Changed mind");
    if (before.returns_require_approval) {
      await dialog.getByLabel("Manager's employee code").fill("EMP003");
      await dialog.getByLabel("Manager's PIN").fill("1234");
    }
    await dialog.getByRole("button", { name: /Refund about/ }).click();
    await expect(page.getByText(/refund ₹2,712\.00 by Cash/)).toBeVisible();
  });

  await test.step("the sales list shows what was paid, and the round-off", async () => {
    const id = saleUrl.split("/").pop()!;
    const detail = await (await request.get(`${API}/sales/orders/${id}/detail`, { headers: auth })).json();
    await page.goto(`/sales?q=${detail.order_number}`);
    const row = page.getByRole("row", { name: new RegExp(detail.order_number) });
    await expect(row).toContainText("₹2,712.00");
    await expect(row).toContainText("round-off −₹0.82");
  });

  await request.put(`${API}/sales/settings`, {
    headers: auth,
    data: { ...restore, round_off_max: Number(before.round_off_max), round_off_auto: before.round_off_auto },
  });
});
