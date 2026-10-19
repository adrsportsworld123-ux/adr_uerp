import { test, expect, APIRequestContext, Page } from "@playwright/test";

// erp-core-go migrations/063: Sales Policy decides per branch whether the
// POS grid lists open-price items first; a product carries its own POS
// tile colour.

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

test("POS grid: open-price items first per branch, and a product's tile colour", async ({ page, request }) => {
  const auth = { Authorization: `Bearer ${await token(request)}` };
  const list = await (await request.get(`${API}/products?q=SG-BAT-SH&limit=1`, { headers: auth })).json();
  const productId = list.products[0].product_id as string;
  await login(page);

  await test.step("Sales Policy: MG Road lists open-price items first", async () => {
    await page.goto("/sales-settings");
    const select = page.getByLabel("Grid order at MG Road");
    await select.selectOption("first");
    await expect(page.getByText("Branch rule saved")).toBeVisible();
    const row = page.getByRole("list", { name: "Grid order by branch" }).getByRole("listitem").filter({ hasText: "MG Road" });
    await expect(row.getByTestId("branch-grid-order")).toHaveText("open-price first");
    // The discount rule is a separate setting and stays as it was.
    const disc = page.getByRole("list", { name: "Open-price items by branch" }).getByRole("listitem").filter({ hasText: "MG Road" });
    await expect(disc.getByTestId("branch-open-price-rule")).toHaveText("discounts apply");
    await select.selectOption("");
    await expect(row.getByTestId("branch-grid-order")).toHaveText("usual order");
  });

  await test.step("Product: pick a POS tile colour, then clear it", async () => {
    await page.goto(`/products/${productId}/edit`);
    await page.getByRole("button", { name: "Amber" }).click();
    await expect(page.getByTestId("pos-color-value")).toHaveText("#F59E0B");
    await page.getByRole("button", { name: "Save product" }).click();
    await expect.poll(async () => (await (await request.get(`${API}/products/${productId}`, { headers: auth })).json()).pos_color).toBe("#F59E0B");
    await page.getByRole("button", { name: "No colour" }).click();
    await page.getByRole("button", { name: "Save product" }).click();
    await expect.poll(async () => (await (await request.get(`${API}/products/${productId}`, { headers: auth })).json()).pos_color).toBeNull();
  });
});
