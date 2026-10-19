import { test, expect, APIRequestContext, Page } from "@playwright/test";

// erp-core-go migrations/062: User Management (password, PIN, deactivate,
// home branch) on a user the spec creates itself; open-price items and
// discounts per branch on Sales Policy; the SKU print setting.

const API = process.env.E2E_API_URL ?? "http://localhost:8080/api/v1";
const MERCHANT = process.env.E2E_MERCHANT_CODE ?? "acme-sports";
const BRANCH = "22222222-2222-2222-2222-222222222222";

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

test("User Management, open-price discounts per branch, SKU on receipts", async ({ page, request }) => {
  const auth = { Authorization: `Bearer ${await token(request)}` };
  const stamp = Date.now().toString().slice(-6);
  const name = `E2E User ${stamp}`;
  const email = `e2e-user-${stamp}@acme.test`;
  const created = await request.post(`${API}/hr/employees`, {
    headers: auth,
    data: { name, email, employee_code: `E2E${stamp}`, branch_id: BRANCH, password: "Start#2026" },
  });
  expect(created.status()).toBe(201);
  await login(page);

  await test.step("Users: set a password and a PIN, deactivate and reactivate", async () => {
    await page.goto("/users");
    await page.getByLabel("Search users").fill(name);
    const row = page.getByTestId("user-row").filter({ hasText: name });
    await expect(row).toHaveCount(1);
    await row.getByRole("button", { name: "Manage" }).click();
    const dialog = page.getByRole("dialog");
    await dialog.getByLabel("New password").fill("short");
    await expect(dialog.getByText("At least 8 characters, typed the same twice")).toBeVisible();
    await dialog.getByLabel("New password").fill("Fresh#2026x");
    await dialog.getByLabel("Confirm").fill("Fresh#2026x");
    await dialog.getByRole("button", { name: "Set password" }).click();
    await expect(page.getByText("Password set")).toBeVisible();
    const signIn = await request.post(`${API}/auth/login`, { data: { merchant_code: MERCHANT, email, password: "Fresh#2026x" } });
    expect(signIn.status()).toBe(200);

    await dialog.getByLabel("New PIN").fill("4831");
    await dialog.getByRole("button", { name: "Set PIN" }).click();
    await expect(page.getByText("PIN set")).toBeVisible();
    await expect(dialog.getByRole("button", { name: "Remove PIN" })).toBeVisible();

    await dialog.getByRole("button", { name: "Deactivate" }).click();
    await expect(page.getByText(/is deactivated and signed out/)).toBeVisible();
    const refused = await request.post(`${API}/auth/login`, { data: { merchant_code: MERCHANT, email, password: "Fresh#2026x" } });
    expect(refused.status()).not.toBe(200);
    await dialog.getByRole("button", { name: "Activate" }).click();
    await expect(page.getByText(/can sign in again/)).toBeVisible();

    await dialog.getByLabel("Home branch").selectOption("");
    await dialog.getByRole("button", { name: "Save home branch" }).click();
    await expect(page.getByText("Home branch saved")).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(row).toContainText("No home branch");
  });

  await test.step("Sales Policy: a branch keeps open-price items out of discounts", async () => {
    await page.goto("/sales-settings");
    const select = page.getByLabel("Open-price discounts at MG Road");
    await select.selectOption("block");
    await expect(page.getByText("Branch rule saved")).toBeVisible();
    const rule = page.getByRole("list", { name: "Open-price items by branch" }).getByRole("listitem").filter({ hasText: "MG Road" });
    await expect(rule.getByTestId("branch-open-price-rule")).toHaveText("no discounts");
    await select.selectOption("");
    await expect(rule.getByTestId("branch-open-price-rule")).toHaveText("discounts apply");
  });

  await test.step("Print settings: the SKU can be left off receipts", async () => {
    await page.goto(`/branches/${BRANCH}/print-settings`);
    const box = page.getByLabel(/Print the SKU under each item/);
    await expect(box).toBeChecked();
  });

  await test.step("My Account: a wrong current password is refused", async () => {
    await page.goto("/account");
    await page.getByLabel("Current password").first().fill("not-my-password");
    await page.getByLabel("New password").fill("Whatever#2026");
    await page.getByLabel("Confirm").fill("Whatever#2026");
    await page.getByRole("button", { name: "Change password" }).click();
    await expect(page.getByText("your current password is not correct")).toBeVisible();
  });

  // Leave nothing behind that can sign in.
  const list = await (await request.get(`${API}/users?q=${encodeURIComponent(name)}`, { headers: auth })).json();
  await request.patch(`${API}/users/${list.users[0].user_id}`, { headers: auth, data: { status: "inactive" } });
});
