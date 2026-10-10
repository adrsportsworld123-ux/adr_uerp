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

async function adminToken(request: import("@playwright/test").APIRequestContext) {
  const resp = await request.post(`${API}/auth/login`, {
    data: { merchant_code: "acme-sports", email: "admin@adrsw.com", password: "Idontsay3#" },
  });
  return (await resp.json()).access_token as string;
}

test("Phase 11: supplier edit clears a field and sets the credit limit to zero", async ({ page, request }) => {
  const name = `E2E Edit Supplier ${unique}`;
  const token = await adminToken(request);
  const created = await request.post(`${API}/suppliers`, {
    headers: { Authorization: `Bearer ${token}` },
    data: { name, gstin: "29AAAAA0000A1Z5", credit_limit: 50000, payment_terms: "Net 30" },
  });
  expect(created.status()).toBe(201);

  await login(page, "admin@adrsw.com", "Idontsay3#");
  await page.goto(`/suppliers?q=${encodeURIComponent(name)}`);
  const row = page.getByRole("row", { name: new RegExp(name) });
  await expect(row).toContainText("₹50,000.00");
  await row.getByRole("button", { name: `Edit ${name}` }).click();

  await page.fill("#edit-gstin", "");
  await page.fill("#edit-credit_limit", "0");
  await page.fill("#edit-contact_name", "Ramesh");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByText(`Saved supplier “${name}”`)).toBeVisible();

  await expect(row).toContainText("₹0.00");
  await expect(row).toContainText("Ramesh");
  await expect(row).not.toContainText("29AAAAA0000A1Z5");
  await expect(row).toContainText("Net 30"); // untouched fields are kept
});

test("Phase 11: branch edit rejects an unknown timezone, then saves", async ({ page }) => {
  const code = `P11${unique % 100000}`;
  await login(page, "admin@adrsw.com", "Idontsay3#");
  await page.goto("/branches");
  await page.fill("#name", `E2E P11 Branch ${code}`);
  await page.fill("#code", code);
  await page.click('button[type="submit"]');
  await expect(page.getByText(`Created branch “E2E P11 Branch ${code}”`)).toBeVisible();

  await page.getByRole("button", { name: `Edit E2E P11 Branch ${code}` }).click();
  await page.fill("#edit-timezone", "Mars/Olympus");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByRole("alert")).toContainText("unknown timezone");

  await page.fill("#edit-timezone", "Asia/Dubai");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByRole("row", { name: new RegExp(code) })).toContainText("Asia/Dubai");
});

test("Phase 11: products list searches by SKU and keeps filters in the URL", async ({ page, request }) => {
  const token = await adminToken(request);
  const sku = `E2E-P11-SKU-${unique}`;
  const resp = await request.post(`${API}/products`, {
    headers: { Authorization: `Bearer ${token}` },
    data: { name: `E2E SKU Search ${unique}`, variants: [{ sku, mrp: 300, selling_price: 250, cost_price: 100 }] },
  });
  expect(resp.status()).toBe(201);

  await login(page, "admin@adrsw.com", "Idontsay3#");
  await page.goto("/products");
  await page.getByLabel("Search products").fill(sku);
  await expect(page).toHaveURL(new RegExp(`q=${sku}`));
  await expect(page.getByRole("row", { name: new RegExp(`E2E SKU Search ${unique}`) })).toBeVisible();
  await expect(page.getByText(/Showing 1–1 of 1/)).toBeVisible();

  await page.reload(); // state survives a reload because it lives in the URL
  await expect(page.getByLabel("Search products")).toHaveValue(sku);
  await expect(page.getByRole("row", { name: new RegExp(`E2E SKU Search ${unique}`) })).toBeVisible();
});

test("Phase 11: customers list filters by type", async ({ page }) => {
  await login(page, "admin@adrsw.com", "Idontsay3#");
  await page.goto("/customers");
  await page.getByLabel("Customer type").selectOption("b2b");
  await expect(page).toHaveURL(/customer_type=b2b/);
  const cells = page.getByRole("cell", { name: "B2C", exact: true });
  await expect(cells).toHaveCount(0);
});

test("Phase 11: a category-scoped promotion is created from the category picker (no pasted UUID)", async ({ page }) => {
  const promo = `E2E Cat Promo ${unique}`;
  await login(page, "meera@acme-sports.test", "9999");
  await page.goto("/promotions");
  await page.fill("#promoName", promo);
  await page.fill("#valuePercent", "5");
  // Comboboxes in the form, in order: Type, Applies to.
  await page.getByRole("combobox").nth(1).click();
  await page.getByRole("option", { name: "One category" }).click();
  const category = page.locator("#categoryId");
  const firstReal = await category.locator("option").nth(1).getAttribute("value");
  expect(firstReal).toBeTruthy();
  await category.selectOption(firstReal!);
  await page.click('button[type="submit"]:has-text("Create promotion")');
  await expect(page.getByText("Promotion created")).toBeVisible();
  await expect(page.getByRole("row", { name: new RegExp(promo) })).toContainText("category");
});

test("Phase 11: RBAC groups permissions and guards unsaved ticks when switching roles", async ({ page }) => {
  const roleA = `E2E Guard A ${unique}`;
  const roleB = `E2E Guard B ${unique}`;
  await login(page, "arjun@acme-sports.test", "Passw0rd!");
  await page.goto("/rbac");
  for (const r of [roleA, roleB]) {
    await page.fill("#newRoleName", r);
    await page.getByRole("button", { name: "Add role" }).click();
    await expect(page.getByRole("row", { name: new RegExp(r) })).toBeVisible();
  }

  await page.getByRole("row", { name: new RegExp(roleA) }).click();
  await expect(page.getByRole("group", { name: "Catalog" })).toBeVisible(); // fieldset per module
  await page.getByRole("group", { name: "Catalog" }).getByRole("button", { name: "Select all" }).click();
  await expect(page.getByText(/unsaved change/)).toBeVisible();

  await page.getByRole("row", { name: new RegExp(roleB) }).click();
  const guard = page.getByRole("alertdialog", { name: "Discard unsaved permission changes?" });
  await expect(guard).toBeVisible();
  await guard.getByRole("button", { name: "Keep editing" }).click();
  await expect(page.getByText(`Permissions for "${roleA}"`)).toBeVisible();

  // Clean up both throwaway roles (nobody holds them).
  for (const r of [roleA, roleB]) {
    await page.getByRole("row", { name: new RegExp(r) }).getByRole("button", { name: "Delete" }).click();
    const confirm = page.getByRole("alertdialog");
    if (await confirm.getByRole("button", { name: "Discard" }).isVisible().catch(() => false)) {
      await confirm.getByRole("button", { name: "Discard" }).click();
    }
    await page.getByRole("button", { name: "Delete role" }).click();
    await expect(page.getByRole("row", { name: new RegExp(r) })).toHaveCount(0);
  }
});

test("Phase 11: marking an employee exited asks first and explains the effect", async ({ page }) => {
  await login(page, "admin@adrsw.com", "Idontsay3#");
  await page.goto("/hr/employees?status=active");
  await page.getByRole("row", { name: /Ravi Kumar/ }).click();
  await page.getByRole("button", { name: "Mark as exited" }).click();
  const dialog = page.getByRole("alertdialog");
  await expect(dialog).toContainText("login is disabled immediately");
  await dialog.getByRole("button", { name: "Cancel" }).click(); // never actually exit the seed POS user
  await expect(page.getByRole("row", { name: /Ravi Kumar/ })).toContainText(/Active/);
});
