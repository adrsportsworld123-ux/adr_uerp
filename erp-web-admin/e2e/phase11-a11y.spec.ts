import { test, expect, Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

// Phase 11 Definition of Done: an axe accessibility scan of the screens on
// the new kit, in both themes, at WCAG 2.1 A/AA. Runs against the real
// app and API like every other spec here. A failure lists each violated
// rule with the offending elements, so it's actionable from the report.

const SCREENS = [
  "/dashboard",
  "/products",
  "/customers",
  "/suppliers",
  "/branches",
  "/catalog/categories",
  "/catalog/brands",
  "/catalog/tax-slabs",
  "/terminals",
  "/sales-settings",
  "/inventory/review",
  "/sales",
  "/labels",
  "/accounting/journal-entries",
  "/accounting/vouchers",
  "/rbac/data-access",
  "/rbac/approval-policies",
  "/subscription",
  "/reports/builder",
];

async function login(page: Page) {
  await page.goto("/login");
  await page.fill("#email", "admin@adrsw.com");
  await page.fill("#password", "Idontsay3#");
  await page.click('button[type="submit"]');
  await page.waitForURL("/dashboard");
}

for (const theme of ["light", "dark"] as const) {
  test(`Phase 11 a11y: no WCAG A/AA violations on kit screens (${theme})`, async ({ page }) => {
    test.setTimeout(90_000);
    await login(page);
    await page.evaluate((t) => localStorage.setItem("theme", t), theme);

    const failures: string[] = [];
    for (const path of SCREENS) {
      await page.goto(path);
      await expect(page.locator("h1").first()).toBeVisible();
      // Let the list finish loading so the scan sees real rows, not skeletons.
      await page.waitForLoadState("networkidle");
      const results = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze();
      for (const v of results.violations) {
        failures.push(`${path} [${v.id}] ${v.help} — ${v.nodes.slice(0, 3).map((n) => n.target.join(" ")).join(" | ")}`);
      }
    }
    expect(failures, failures.join("\n")).toEqual([]);
  });
}

// Tab until the focused element's accessible label matches, like a
// keyboard user would. Fails rather than looping forever.
async function tabTo(page: Page, label: RegExp, max = 80) {
  for (let i = 0; i < max; i++) {
    await page.keyboard.press("Tab");
    const name = await page.evaluate(() => {
      // A rough accessible name: aria-label, else an associated <label>, else text.
      const el = document.activeElement as (HTMLElement & { labels?: NodeListOf<HTMLLabelElement> }) | null;
      if (!el) return "";
      return el.getAttribute("aria-label") ?? el.labels?.[0]?.textContent?.trim() ?? el.textContent?.trim() ?? "";
    });
    if (label.test(name)) return;
  }
  throw new Error(`never reached ${label} by Tab`);
}

test("Phase 11 a11y: create, edit and deactivate a brand with the keyboard only", async ({ page }) => {
  const brand = `E2E Keyboard ${Date.now()}`;
  await login(page);

  // Assertions only (no clicks): wait until the shell is interactive and
  // the palette's input actually has focus before typing into it.
  await expect(page.getByRole("button", { name: /Jump to/ })).toBeVisible();
  await page.keyboard.press("Control+k");
  await expect(page.getByLabel("Jump to a screen")).toBeFocused();
  await page.keyboard.type("Brands");
  await page.keyboard.press("Enter");
  await page.waitForURL("/catalog/brands");

  // The skip link is the first tab stop on every page, and lands focus in
  // the page content, so the sidebar's ~75 links are one keypress away.
  await tabTo(page, /^Skip to main content$/, 1);
  await page.keyboard.press("Enter");
  await tabTo(page, /^New brand$/, 5);
  await page.keyboard.press("Enter");
  await page.keyboard.type(brand); // the name field takes focus when the dialog opens
  await page.keyboard.press("Enter");
  await expect(page.getByText(`Created brand “${brand}”`)).toBeVisible();
  await expect(page.getByRole("dialog")).toHaveCount(0); // closing animation done

  // Focus returns to "New brand" when the dialog closes, so the search box
  // is the very next stop rather than a trip back through the sidebar.
  await tabTo(page, /^Search$/, 3);
  await page.keyboard.type(brand);
  await expect(page.getByRole("cell", { name: brand, exact: true })).toBeVisible();

  await tabTo(page, new RegExp(`^Edit ${brand}$`));
  await page.keyboard.press("Enter");
  await page.keyboard.press("End");
  await page.keyboard.type(" Pro");
  await page.keyboard.press("Enter");
  await expect(page.getByText(`Saved brand “${brand} Pro”`)).toBeVisible();
  await expect(page.getByRole("dialog")).toHaveCount(0);

  await page.getByPlaceholder("Search brands").fill(`${brand} Pro`); // re-scope the list to the renamed row
  await tabTo(page, new RegExp(`^Deactivate ${brand} Pro$`));
  await page.keyboard.press("Enter");
  await expect(page.getByText(`Deactivated “${brand} Pro”`)).toBeVisible();
});
