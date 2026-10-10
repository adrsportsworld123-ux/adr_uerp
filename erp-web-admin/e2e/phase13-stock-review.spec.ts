import { test, expect, Page, APIRequestContext } from "@playwright/test";

// Phase 13: Stock Review. An offline sale that oversells is pushed through
// POST /sync/push exactly as the POS sends it (a throwaway product with no
// stock, so the seed's stock is untouched); the admin finds it by SKU,
// resolves it with a note, and a POS user sees it but can't resolve.

const API = "http://localhost:8080/api/v1";
const MERCHANT = process.env.E2E_MERCHANT_CODE ?? "acme-sports";
const BRANCH = "22222222-2222-2222-2222-222222222222";
const TILL = "33333333-3333-3333-3333-333333333333";
const GST18 = "66666666-6666-6666-6666-666666666666";

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

test("Phase 13: an oversold offline sale is reviewed and resolved in the back office", async ({ page, request }) => {
  const admin = { Authorization: `Bearer ${await token(request, "arjun@acme-sports.test")}` };
  const cashier = { Authorization: `Bearer ${await token(request, "ravi@acme-sports.test")}` };
  const sku = `E2E-RVW-${Date.now()}`;
  const product = await (
    await request.post(`${API}/products`, {
      headers: admin,
      data: { name: `Review Item ${sku}`, tax_slab_id: GST18, variants: [{ sku, cost_price: 40, mrp: 100, selling_price: 80 }] },
    })
  ).json();
  const variantId = product.variants[0].variant_id;

  try {
    const push = await (
      await request.post(`${API}/sync/push`, {
        headers: cashier,
        data: {
          orders: [
            {
              idempotency_key: `e2e-rvw-${Date.now()}`,
              branch_id: BRANCH,
              pos_terminal_id: TILL,
              device_created_at: new Date().toISOString().replace(/\.\d+Z$/, "Z"),
              lines: [{ variant_id: variantId, quantity: 3, unit_price: 80, discount_amount: 0, tax_amount: 43.2, line_total: 283.2 }],
              payments: [{ method: "cash", amount: 283.2 }],
            },
          ],
        },
      })
    ).json();
    expect(push.results[0].status).toBe("ok");
    expect(push.results[0].negative_stock).toBe(true);

    await test.step("a POS user sees it but can't resolve", async () => {
      await login(page, "ravi@acme-sports.test");
      await page.goto(`/inventory/review?q=${sku}`);
      const row = page.getByRole("row", { name: new RegExp(sku) });
      await expect(row).toContainText("Needs review");
      await expect(row.getByRole("button", { name: /Resolve/ })).toBeDisabled();
    });

    await test.step("the admin resolves it with a note", async () => {
      await page.evaluate(() => localStorage.clear());
      await login(page, "arjun@acme-sports.test");
      await page.goto("/inventory/review");
      await page.getByRole("searchbox", { name: "Search review items" }).fill(sku);
      const row = page.getByRole("row", { name: new RegExp(sku) });
      await expect(row).toContainText("-3 / -3");
      await row.getByRole("button", { name: /Resolve/ }).click();
      const dialog = page.getByRole("dialog");
      await expect(dialog.getByRole("button", { name: "Mark resolved" })).toBeDisabled();
      await dialog.getByLabel("What did you find?").fill("Counted 3 in the back room, adjusted +3");
      await dialog.getByRole("button", { name: "Mark resolved" }).click();
      await expect(page.getByText(`Resolved Review Item ${sku}`)).toBeVisible();
      await expect(page.getByRole("row", { name: new RegExp(sku) })).toHaveCount(0); // the list shows open items

      await page.getByRole("combobox", { name: "Status" }).selectOption("resolved");
      const done = page.getByRole("row", { name: new RegExp(sku) });
      await expect(done).toContainText("Resolved");
      await expect(done).toContainText("Counted 3 in the back room");
    });
  } finally {
    await request.patch(`${API}/products/variants/${variantId}`, { headers: admin, data: { status: "discontinued" } });
  }
});
