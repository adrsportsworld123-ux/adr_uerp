import { test, expect, Page } from "@playwright/test";

// Phase 13: the merchant's return policy (GET/PUT /sales/settings). The
// policy is what the POS return screen and POST /sales/orders/{id}/returns
// enforce; this spec checks the admin side saves it, the API reflects it,
// and a user without sales.settings can read but not change it.

const API = "http://localhost:8080/api/v1";
const MERCHANT = process.env.E2E_MERCHANT_CODE ?? "acme-sports";

async function login(page: Page, email: string, password: string) {
  await page.goto("/login");
  await page.fill("#email", email);
  await page.fill("#password", password);
  await page.click('button[type="submit"]');
  await page.waitForURL("/dashboard");
}

async function token(request: import("@playwright/test").APIRequestContext, email: string) {
  const r = await request.post(`${API}/auth/login`, { data: { merchant_code: MERCHANT, email, password: "Passw0rd!" } });
  expect(r.ok()).toBeTruthy();
  return (await r.json()).access_token as string;
}

test("Phase 13: a Merchant Admin sets the return policy; a POS user can't", async ({ page, request }) => {
  const adminToken = await token(request, "arjun@acme-sports.test");
  const headers = { Authorization: `Bearer ${adminToken}` };
  // Known starting point: approval on, no window.
  await request.put(`${API}/sales/settings`, { headers, data: { returns_require_approval: true, return_window_days: null } });

  try {
    await login(page, "arjun@acme-sports.test", "Passw0rd!");
    await page.goto("/sales-settings");
    await expect(page.getByRole("heading", { name: "Return Policy" })).toBeVisible();
    await expect(page.getByRole("radio", { name: /Manager approval/ })).toBeChecked();
    const save = page.getByRole("button", { name: "Save" });
    await expect(save).toBeDisabled(); // nothing changed yet

    await page.getByRole("radio", { name: /Any cashier/ }).check();
    await page.getByRole("checkbox", { name: /within a number of days/ }).check();
    await page.fill("#return-days", "0");
    await expect(page.getByText("Enter a whole number of days, 1 or more")).toBeVisible();
    await expect(save).toBeDisabled();
    await page.fill("#return-days", "14");
    await save.click();
    await expect(page.getByText("Return policy saved")).toBeVisible();

    const saved = await (await request.get(`${API}/sales/settings`, { headers })).json();
    expect(saved).toEqual({ returns_require_approval: false, return_window_days: 14 });

    await page.reload();
    await expect(page.getByRole("radio", { name: /Any cashier/ })).toBeChecked();
    await expect(page.locator("#return-days")).toHaveValue("14");

    // A POS user sees the policy but can't change it.
    await page.evaluate(() => localStorage.clear());
    await login(page, "ravi@acme-sports.test", "Passw0rd!");
    await page.goto("/sales-settings");
    await expect(page.getByRole("radio", { name: /Any cashier/ })).toBeChecked();
    await expect(page.getByRole("radio", { name: /Manager approval/ })).toBeDisabled();
    await expect(page.getByRole("button", { name: "Save" })).toBeDisabled();
    await expect(page.getByText(/Needs the sales.settings permission/).first()).toBeVisible();
  } finally {
    await request.put(`${API}/sales/settings`, { headers, data: { returns_require_approval: true, return_window_days: null } });
  }
});
