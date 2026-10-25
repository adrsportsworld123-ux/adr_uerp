import { test, expect, APIRequestContext } from "@playwright/test";

// Volume tiers with a fixed discount, and Buy X Get Y across products
// (erp-core-go migrations/070). The cart arithmetic is covered by the API
// flow; here the dialog saves both, the list describes them, and they
// reopen as saved.

const API = process.env.E2E_API_URL ?? "http://localhost:8080/api/v1";
const MERCHANT = process.env.E2E_MERCHANT_CODE ?? "acme-sports";

async function token(request: APIRequestContext) {
  const r = await request.post(`${API}/auth/login`, { data: { merchant_code: MERCHANT, email: "arjun@acme-sports.test", password: "Passw0rd!" } });
  return (await r.json()).access_token as string;
}

test("volume tier ₹ off each piece, and Buy ABC get LKG free", async ({ page, request }) => {
  const auth = { Authorization: `Bearer ${await token(request)}` };
  const tag = Date.now().toString().slice(-7);
  const abc = `E2E ABC ${tag}`;
  const lkg = `E2E LKG ${tag}`;
  const imp = await request.post(`${API}/purchase/imports/commit`, {
    headers: auth,
    data: {
      supplier_id: "11111111-2222-3333-4444-555555555555",
      branch_id: "22222222-2222-2222-2222-222222222222",
      invoice_number: `E2E-BX-${tag}`,
      invoice_date: "2026-10-01",
      csv: `item_name,quantity,unit_cost,gst_rate,mrp,sale_gst_rate\n${abc},2,100,18,500,18\n${lkg},2,20,18,100,18\n`,
    },
  });
  expect(imp.status()).toBe(201);
  const ids: string[] = [];
  page.on("response", async (r) => {
    if (r.url().endsWith("/api/v1/promotions") && r.request().method() === "POST" && r.status() === 201) ids.push((await r.json()).promotion_id);
  });

  try {
    await page.goto("/login");
    await page.fill("#email", "meera@acme-sports.test");
    await page.fill("#password", "9999");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");
    await page.goto("/promotions");

    await test.step("volume: 6+ pieces, ₹10 off each", async () => {
      const name = `E2E Vol ${tag}`;
      await page.getByRole("button", { name: "New promotion" }).click();
      await page.fill("#promoName", name);
      await page.getByRole("combobox", { name: "Promotion type" }).click();
      await page.getByRole("option", { name: "Volume tiers" }).click();
      await page.locator("#promoProduct").click();
      await page.getByLabel("Search products").fill(abc);
      await page.getByRole("option", { name: new RegExp(abc) }).click();
      await page.getByLabel("Min qty").fill("6");
      await page.getByLabel("Max qty (0 = no limit)").fill("0");
      await page.getByLabel("Tier 1 discount as").selectOption("amount_per_unit");
      await page.getByLabel(/^Amount/).fill("10");
      await page.click('button[type="submit"]:has-text("Create promotion")');
      await expect(page.getByText("Promotion created")).toBeVisible();
      await expect(page.getByRole("row", { name: new RegExp(name) })).toContainText("6+: ₹10.00 off each");
      await page.getByRole("row", { name: new RegExp(name) }).getByRole("button", { name: "Edit" }).click();
      await expect(page.getByLabel("Tier 1 discount as")).toHaveValue("amount_per_unit");
      await expect(page.getByLabel(/^Amount/)).toHaveValue("10");
      await page.keyboard.press("Escape");
    });

    await test.step("Buy 2 ABC get 1 LKG free", async () => {
      const name = `E2E BXGY ${tag}`;
      await page.getByRole("button", { name: "New promotion" }).click();
      await page.fill("#promoName", name);
      await page.getByRole("combobox", { name: "Promotion type" }).click();
      await page.getByRole("option", { name: "Buy X get Y — different products" }).click();
      await expect(page.getByRole("combobox", { name: "Applies to" })).toHaveCount(0);
      await expect(page.locator("#promoApplyOn")).toBeDisabled();
      await page.fill("#buyQty", "2");
      await page.locator("#bxgyBuy").click();
      await page.getByLabel("Search products").fill(abc);
      await page.getByRole("option", { name: new RegExp(abc) }).click();
      await page.fill("#getQty", "1");
      await page.locator("#bxgyGet").click();
      await page.getByLabel("Search products").fill(lkg);
      await page.getByRole("option", { name: new RegExp(lkg) }).click();
      await expect(page.locator("#getDiscountPct")).toHaveValue("100");
      await page.click('button[type="submit"]:has-text("Create promotion")');
      await expect(page.getByText("Promotion created")).toBeVisible();
      const row = page.getByRole("row", { name: new RegExp(name) });
      await expect(row).toContainText("Buy 2 get 1 free");
      await expect(row).toContainText(`Buy ${abc} → get ${lkg}`);
      await row.getByRole("button", { name: "Edit" }).click();
      await expect(page.getByTestId("promo-bxgy").getByRole("list", { name: "Chosen products" }).first()).toContainText(abc);
      await page.keyboard.press("Escape");
    });
  } finally {
    // Never leave running promotions behind for other specs' carts.
    for (const id of ids) await request.patch(`${API}/promotions/${id}`, { headers: auth, data: { active: false } });
  }
});
