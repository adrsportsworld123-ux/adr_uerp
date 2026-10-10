import { test, expect, Page, APIRequestContext } from "@playwright/test";

// Phase 15 approval policies in the browser: the Merchant Admin edits the
// discount tiers and the refund threshold, the form refuses an incoherent
// tier, the saved policy is what the server now enforces (an 8% discount
// needs no approver once the first tier is 10%), and a POS User can read
// the page but not save it.

const API = "http://localhost:8080/api/v1";
const MERCHANT = process.env.E2E_MERCHANT_CODE ?? "acme-sports";

async function login(page: Page, email: string) {
  await page.evaluate(() => localStorage.clear()).catch(() => {});
  await page.goto("/login");
  await page.fill("#email", email);
  await page.fill("#password", "Passw0rd!");
  await page.click('button[type="submit"]');
  await page.waitForURL("/dashboard");
}

async function token(request: APIRequestContext, email: string) {
  const r = await request.post(`${API}/auth/login`, { data: { merchant_code: MERCHANT, email, password: "Passw0rd!" } });
  expect(r.ok()).toBeTruthy();
  return (await r.json()).access_token as string;
}

test("Approval policies: edit tiers and refund threshold; the server enforces them", async ({ page, request }) => {
  const admin = await token(request, "arjun@acme-sports.test");
  const auth = { Authorization: `Bearer ${admin}` };
  const original = await (await request.get(`${API}/settings/approval-policies`, { headers: auth })).json();
  const defaults = {
    discount_tiers: [
      { up_to: 5, approver: "none" },
      { up_to: 15, approver: "branch_manager" },
      { up_to: 25, approver: "merchant_admin" },
    ],
    refund_approval_above: null,
    refund_approver: "branch_manager",
    below_cost_price: "any",
    stock_adjust_approval_above: null,
    stock_adjust_approver: "branch_manager",
    jv_approval_above: null,
  };
  await request.put(`${API}/settings/approval-policies`, { headers: auth, data: defaults });

  try {
    await login(page, "arjun@acme-sports.test");
    await page.goto("/rbac/approval-policies");
    await expect(page.getByLabel("Up to").first()).toHaveValue("5");
    const save = page.getByRole("button", { name: "Save" });
    await expect(save).toBeDisabled(); // nothing changed yet

    await test.step("a tier that doesn't rise is refused in the form", async () => {
      await page.locator("#tier-1-upto").fill("3");
      await expect(page.getByText(/Discount tier 2: each tier/)).toBeVisible();
      await expect(save).toBeDisabled();
      await page.locator("#tier-1-upto").fill("15");
    });

    await test.step("raise the free tier to 10% and limit refund approval to above ₹3000", async () => {
      await page.locator("#tier-0-upto").fill("10");
      await page.getByLabel("Only refunds above ₹").check();
      await page.getByLabel("Refund approval threshold in rupees").fill("3000");
      await page.locator("#refund-approver").selectOption("merchant_admin");
      await save.click();
      await expect(page.getByText("Approval policies saved")).toBeVisible();
      const p = await (await request.get(`${API}/settings/approval-policies`, { headers: auth })).json();
      expect(p.discount_tiers[0]).toEqual({ up_to: 10, approver: "none" });
      expect(p.refund_approval_above).toBe(3000);
      expect(p.refund_approver).toBe("merchant_admin");
    });

    await test.step("the server now allows an 8% discount with no approver", async () => {
      const ravi = await token(request, "ravi@acme-sports.test");
      const h = { Authorization: `Bearer ${ravi}` };
      const till = await (
        await request.post(`${API}/pos-terminals`, { headers: auth, data: { branch_id: "22222222-2222-2222-2222-222222222222", name: `E2E P15 ${Date.now()}` } })
      ).json();
      const order = await (
        await request.post(`${API}/sales/orders`, {
          headers: h,
          data: { branch_id: "22222222-2222-2222-2222-222222222222", pos_terminal_id: till.terminal_id, idempotency_key: `e2e-p15-${Date.now()}` },
        })
      ).json();
      await request.post(`${API}/inventory/adjustments`, {
        headers: auth,
        data: { variant_id: "99999999-9999-9999-9999-999999999999", branch_id: "22222222-2222-2222-2222-222222222222", quantity_delta: 2, reason: "e2e p15 top-up" },
      });
      const line = await request.post(`${API}/sales/orders/${order.order_id}/lines`, {
        headers: h,
        data: { variant_id: "99999999-9999-9999-9999-999999999999", quantity: 1 },
      });
      expect(line.ok()).toBeTruthy();
      const d = await request.post(`${API}/sales/orders/${order.order_id}/discounts`, { headers: h, data: { type: "manual", value: 8, reason: "e2e" } });
      expect(d.ok()).toBeTruthy();
      // The open cart's reservation lapses on its own (15-minute sweeper).
    });

    await test.step("a POS User sees the policy but can't save it", async () => {
      await login(page, "ravi@acme-sports.test");
      await page.goto("/rbac/approval-policies");
      await expect(page.locator("#tier-0-upto")).toHaveValue("10");
      await expect(page.locator("#tier-0-upto")).toBeDisabled();
      await expect(page.getByRole("button", { name: "Save" })).toBeDisabled();
      await expect(page.getByText("Needs the approvals.manage permission").first()).toBeVisible();
      const ravi = await token(request, "ravi@acme-sports.test");
      const r = await request.put(`${API}/settings/approval-policies`, { headers: { Authorization: `Bearer ${ravi}` }, data: defaults });
      expect(r.status()).toBe(403);
    });
  } finally {
    await request.put(`${API}/settings/approval-policies`, { headers: auth, data: original });
  }
});

test("Approval policies: a stock adjustment above the threshold asks for an approver on the Inventory screen", async ({ page, request }) => {
  const admin = await token(request, "arjun@acme-sports.test");
  const auth = { Authorization: `Bearer ${admin}` };
  const original = await (await request.get(`${API}/settings/approval-policies`, { headers: auth })).json();
  // The seed bat costs ₹1200, so a one-unit adjustment is above ₹100.
  await request.put(`${API}/settings/approval-policies`, { headers: auth, data: { ...original, stock_adjust_approval_above: 100, stock_adjust_approver: "branch_manager" } });
  try {
    await login(page, "arjun@acme-sports.test");
    await page.goto("/inventory");
    await page.getByRole("combobox").nth(1).click();
    await page.getByRole("option", { name: /SG Cricket Bat/ }).click();
    await page.fill("#delta", "1");
    await page.fill("#reason", "E2E approval threshold");
    await page.getByRole("button", { name: "Apply" }).click();
    await expect(page.getByLabel("Approver's employee code")).toBeVisible();
    await expect(page.getByRole("button", { name: "Apply" })).toBeDisabled(); // until code and PIN are in

    await page.getByLabel("Approver's employee code").fill("emp003");
    await page.getByLabel("Approver's PIN").fill("1234");
    await page.getByRole("button", { name: "Apply" }).click();
    await expect(page.getByText("Stock adjusted")).toBeVisible();
    await expect(page.getByLabel("Approver's employee code")).toHaveCount(0); // asked again only when needed

    // Undo it the same way.
    await page.fill("#delta", "-1");
    await page.fill("#reason", "E2E cleanup");
    await page.getByRole("button", { name: "Apply" }).click();
    await page.getByLabel("Approver's employee code").fill("EMP003");
    await page.getByLabel("Approver's PIN").fill("1234");
    await page.getByRole("button", { name: "Apply" }).click();
    await expect(page.getByText("Stock adjusted").first()).toBeVisible();
  } finally {
    await request.put(`${API}/settings/approval-policies`, { headers: auth, data: original });
  }
});
