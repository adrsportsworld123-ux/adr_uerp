import { test, expect, Page } from "@playwright/test";

// Phase 11 (erp-core-go/docs/phased_roadmap.md): the shared list/form kit
// and master-data lifecycle, driven through the real browser against the
// real API — not mocked. Covers the Definition of Done items that are
// behaviour rather than looks: server paging/search, duplicate-name
// refusal, dependency-aware delete that offers deactivate instead, the
// unsaved-changes guard, the permission hint (disabled before a 403), the
// command palette, and the theme switch.

const API = "http://localhost:8080/api/v1";
const unique = Date.now();

async function login(page: Page, email: string, password: string) {
  await page.goto("/login");
  await page.fill("#email", email);
  await page.fill("#password", password);
  await page.click('button[type="submit"]');
  await page.waitForURL("/dashboard");
}

test("Phase 11: brand lifecycle — create, duplicate refused, delete blocked by a product, deactivate instead", async ({ page, request }) => {
  const brand = `E2E Brand ${unique}`;

  await test.step("login as Merchant Admin", () => login(page, "admin@adrsw.com", "Idontsay3#"));

  await test.step("create a brand through the dialog", async () => {
    await page.goto("/catalog/brands");
    await expect(page.getByRole("heading", { name: "Brands" })).toBeVisible();
    await page.getByRole("button", { name: "New brand" }).first().click();
    await page.fill("#master-name", brand);
    await page.getByRole("button", { name: "Create brand" }).click();
    await expect(page.getByText(`Created brand “${brand}”`)).toBeVisible();
  });

  await test.step("server-side search finds it and shows a record count", async () => {
    await page.getByPlaceholder("Search brands").fill(String(unique));
    await expect(page).toHaveURL(new RegExp(`q=${unique}`));
    await expect(page.getByRole("cell", { name: brand, exact: true })).toBeVisible();
    await expect(page.getByText(/Showing 1–1 of 1/)).toBeVisible();
  });

  await test.step("a duplicate name in a different case is refused inline", async () => {
    await page.getByRole("button", { name: "New brand" }).first().click();
    await page.fill("#master-name", brand.toUpperCase());
    await page.getByRole("button", { name: "Create brand" }).click();
    await expect(page.getByRole("alert")).toContainText("Another brand already uses this name");
  });

  await test.step("closing with unsaved input asks before discarding", async () => {
    await page.keyboard.press("Escape");
    await expect(page.getByRole("alertdialog", { name: "Discard unsaved changes?" })).toBeVisible();
    await page.getByRole("button", { name: "Discard" }).click();
    await expect(page.getByRole("dialog")).toHaveCount(0);
  });

  await test.step("give the brand a product (direct API call)", async () => {
    const token = (await (await request.post(`${API}/auth/login`, {
      data: { merchant_code: "acme-sports", email: "admin@adrsw.com", password: "Idontsay3#" },
    })).json()).access_token;
    const brands = await (await request.get(`${API}/brands?q=${unique}`, { headers: { Authorization: `Bearer ${token}` } })).json();
    const resp = await request.post(`${API}/products`, {
      headers: { Authorization: `Bearer ${token}` },
      data: {
        name: `E2E Product ${unique}`,
        brand_id: brands.brands[0].brand_id,
        variants: [{ sku: `E2E-P11-${unique}`, mrp: 200, selling_price: 180, cost_price: 100 }],
      },
    });
    expect(resp.status()).toBe(201);
  });

  await test.step("delete explains the dependency and offers deactivate instead", async () => {
    await page.reload();
    await page.getByRole("button", { name: `Delete ${brand}` }).click();
    const dialog = page.getByRole("alertdialog");
    await expect(dialog).toContainText("can't be deleted");
    await expect(dialog.getByText("Products")).toBeVisible();
    await expect(dialog.getByRole("button", { name: "Delete permanently" })).toHaveCount(0);
    await dialog.getByRole("button", { name: "Deactivate instead" }).click();
    await expect(page.getByText(`Deactivated “${brand}”`)).toBeVisible();
  });

  await test.step("it now lists under Inactive, not Active", async () => {
    await expect(page.getByRole("cell", { name: brand, exact: true })).toHaveCount(0);
    await page.getByLabel("Status").selectOption("inactive");
    await expect(page.getByRole("cell", { name: brand, exact: true })).toBeVisible();
  });
});

test("Phase 11: an unused brand deletes cleanly", async ({ page }) => {
  const brand = `E2E Temp ${unique}`;
  await login(page, "admin@adrsw.com", "Idontsay3#");
  await page.goto("/catalog/brands");
  await page.getByRole("button", { name: "New brand" }).first().click();
  await page.fill("#master-name", brand);
  await page.getByRole("button", { name: "Create brand" }).click();
  await page.getByPlaceholder("Search brands").fill(brand);
  await page.getByRole("button", { name: `Delete ${brand}` }).click();
  const dialog = page.getByRole("alertdialog");
  await expect(dialog).toContainText("Nothing uses this brand");
  await dialog.getByRole("button", { name: "Delete permanently" }).click();
  await expect(page.getByText(`Deleted brand “${brand}”`)).toBeVisible();
  await expect(page.getByText("Nothing matches these filters")).toBeVisible();
});

test("Phase 11: a POS User sees catalog actions disabled with the reason, before any 403", async ({ page }) => {
  await login(page, "ravi@acme-sports.test", "Passw0rd!");
  await page.goto("/catalog/categories");
  const create = page.getByRole("button", { name: "New category" }).first();
  await expect(create).toBeDisabled();
  await expect(create).toHaveAttribute("title", /catalog\.manage/);
});

test("Phase 11: shell — grouped nav, breadcrumb, command palette, theme", async ({ page }) => {
  await login(page, "admin@adrsw.com", "Idontsay3#");

  await test.step("breadcrumb names the group and screen", async () => {
    await page.goto("/catalog/tax-slabs");
    await expect(page.getByRole("navigation", { name: "Breadcrumb" })).toHaveText(/Catalog\s*\/\s*Tax Slabs/);
  });

  await test.step("Ctrl+K jumps to a screen by name", async () => {
    await page.keyboard.press("Control+k");
    await page.getByLabel("Jump to a screen").fill("supplier");
    await page.keyboard.press("Enter");
    await page.waitForURL("/suppliers");
    await expect(page.getByRole("heading", { name: "Suppliers" })).toBeVisible();
  });

  await test.step("dark theme applies and persists across reload", async () => {
    await page.getByRole("button", { name: "Dark theme" }).click();
    await expect(page.locator("html")).toHaveClass(/dark/);
    await page.reload();
    await expect(page.locator("html")).toHaveClass(/dark/);
    await page.getByRole("button", { name: "Light theme" }).click();
    await expect(page.locator("html")).not.toHaveClass(/dark/);
  });
});
