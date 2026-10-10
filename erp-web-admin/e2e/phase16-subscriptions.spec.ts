import { test, expect, Page, APIRequestContext } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

// Phase 16 in the browser: platform staff onboard a merchant from the SaaS
// console; the new Freemium merchant sees GST locked (lock icon + preview,
// never a 404), starts a Starter trial from Subscription & Billing and gets
// it; the console expires the account and the merchant sees the read-only
// banner and the server refuses a write; acme-sports stays on Enterprise
// with nothing locked. Needs the platform operator's dev password
// (POST /dev/set-platform-password, ops@adrsw.com / Platf0rm!).

const API = "http://localhost:8080/api/v1";
const OPS = { email: "ops@adrsw.com", password: "Platf0rm!" };

async function merchantLogin(page: Page, code: string, email: string) {
  await page.evaluate(() => localStorage.clear()).catch(() => {});
  await page.goto("/login");
  await page.fill("#merchantCode", code);
  await page.fill("#email", email);
  await page.fill("#password", "Passw0rd!");
  await page.click('button[type="submit"]');
  await page.waitForURL("/dashboard");
}

async function consoleLogin(page: Page) {
  await page.goto("/platform/login");
  await page.getByLabel("Email").fill(OPS.email);
  await page.getByLabel("Password").fill(OPS.password);
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("/platform");
}

async function opsToken(request: APIRequestContext) {
  const r = await request.post(`${API}/platform/auth/login`, { data: OPS });
  expect(r.ok()).toBeTruthy();
  return (await r.json()).access_token as string;
}

test("SaaS console onboards a merchant; plan locks, trial and read-only expiry", async ({ page, request }) => {
  test.setTimeout(90_000);
  const code = `e2e-p16-${Date.now() % 1_000_000}`;
  const email = `owner@${code}.test`;

  await test.step("platform staff sign in and see acme-sports on Enterprise", async () => {
    await consoleLogin(page);
    await expect(page.getByRole("row", { name: /acme-sports/ })).toContainText("Enterprise");
  });

  await test.step("onboard a Freemium merchant", async () => {
    await page.getByRole("link", { name: "Onboard merchant" }).click();
    await page.getByLabel("Merchant code").fill(code);
    await page.getByLabel("Legal name").fill(`E2E ${code}`);
    await page.getByLabel("Name", { exact: true }).fill("E2E Owner");
    await page.getByLabel("Email").fill(email);
    await page.getByLabel("First password").fill("Passw0rd!");
    await page.getByLabel("Plan", { exact: true }).selectOption("freemium");
    await page.getByLabel("Reason (for the audit log)").fill("e2e onboarding");
    await page.getByRole("button", { name: "Onboard" }).click();
    await page.waitForURL(/\/platform\/merchants\/[0-9a-f-]{36}$/);
    await expect(page.getByRole("heading", { name: `E2E ${code}` })).toBeVisible();
    await expect(page.getByText("Freemium").first()).toBeVisible();
  });
  const merchantUrl = page.url();
  const merchantId = merchantUrl.split("/").pop()!;

  await test.step("the merchant sees GST locked, with a preview instead of the page", async () => {
    await merchantLogin(page, code, email);
    const gstLink = page.getByRole("link", { name: /GST Returns/ });
    await expect(gstLink).toContainText("(not in your plan)");
    await page.goto("/gst");
    await expect(page.getByRole("heading", { name: "GST Returns" })).toBeVisible();
    await expect(page.getByText(/isn't part of your Freemium plan; it comes with Starter/)).toBeVisible();
  });

  await test.step("start the Starter trial from Subscription & Billing", async () => {
    await page.getByRole("link", { name: "Compare plans" }).click();
    await page.waitForURL("/subscription");
    await expect(page.getByRole("progressbar", { name: "Active branches" })).toHaveAttribute("aria-valuemax", "1");
    await page.getByRole("button", { name: "Start 14-day trial" }).first().click();
    await page.getByRole("dialog").getByRole("button", { name: "Confirm" }).click();
    await expect(page.getByText("Starter trial started")).toBeVisible();
    await expect(page.getByRole("heading", { name: "Starter", level: 2 })).toBeVisible();
    await expect(page.getByRole("link", { name: /GST Returns/ })).not.toContainText("(not in your plan)");
  });

  await test.step("the console expires the account: read-only banner, writes refused", async () => {
    const ops = await opsToken(request);
    const r = await request.patch(`${API}/platform/merchants/${merchantId}/subscription`, {
      headers: { Authorization: `Bearer ${ops}` },
      data: { status: "expired", reason: "e2e: unpaid" },
    });
    expect(r.ok()).toBeTruthy();
    await page.waitForTimeout(500);
    await merchantLogin(page, code, email);
    await expect(page.getByRole("status").filter({ hasText: "read-only" })).toBeVisible();
    const login = await request.post(`${API}/auth/login`, { data: { merchant_code: code, email, password: "Passw0rd!" } });
    const token = (await login.json()).access_token as string;
    const w = await request.post(`${API}/branches`, { headers: { Authorization: `Bearer ${token}` }, data: { name: "X", code: "X" } });
    expect(w.status()).toBe(402);
    const read = await request.get(`${API}/products`, { headers: { Authorization: `Bearer ${token}` } });
    expect(read.ok()).toBeTruthy();
  });

  await test.step("the console records it, and can set it right", async () => {
    await consoleLogin(page); // the merchant sign-in above cleared this browser's storage
    await page.goto(merchantUrl);
    await expect(page.getByRole("heading", { name: `E2E ${code}` })).toBeVisible();
    await expect(page.getByText("Expired").first()).toBeVisible();
    await page.getByLabel("Status").selectOption("active");
    await page.getByLabel("Reason (required, audited)").fill("e2e: paid by transfer");
    await page.getByRole("button", { name: "Save changes" }).click();
    await expect(page.getByText("Subscription updated")).toBeVisible();
    await page.getByRole("link", { name: "Audit log" }).click();
    await expect(page.getByRole("row", { name: /subscription\.update/ }).first()).toContainText("e2e: paid by transfer");
  });
});

test("acme-sports is grandfathered on Enterprise: nothing locked, no expiry", async ({ page }) => {
  await merchantLogin(page, process.env.E2E_MERCHANT_CODE ?? "acme-sports", "arjun@acme-sports.test");
  await expect(page.getByText("(not in your plan)")).toHaveCount(0);
  await page.goto("/subscription");
  await expect(page.getByRole("heading", { name: "Enterprise", level: 2 })).toBeVisible();
  await expect(page.getByText("No expiry")).toBeVisible();
  await expect(page.getByRole("button", { name: "Current plan" })).toBeDisabled();
});

test("SaaS console pages have no WCAG A/AA violations", async ({ page }) => {
  test.setTimeout(60_000);
  await consoleLogin(page);
  const failures: string[] = [];
  for (const path of ["/platform/login", "/platform", "/platform/merchants/new", "/platform/plans", "/platform/audit"]) {
    await page.goto(path);
    await expect(page.locator("h1").first()).toBeVisible();
    await page.waitForLoadState("networkidle");
    const results = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze();
    for (const v of results.violations) {
      failures.push(`${path} [${v.id}] ${v.help} — ${v.nodes.slice(0, 3).map((n) => n.target.join(" ")).join(" | ")}`);
    }
  }
  expect(failures, failures.join("\n")).toEqual([]);
});
