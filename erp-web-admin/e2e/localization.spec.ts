import { test, expect, APIRequestContext, Page } from "@playwright/test";

// erp-core-go migrations/064: Organization → Localization. The merchant's
// language turns the back office's shell into that language (Spanish
// here), Arabic turns the page right-to-left, the denominations follow
// the merchant's own list in Cash Reconciliation, and a POS terminal takes
// its own text direction and cart side. Everything is put back to English
// at the end, so later specs read the screens they expect.

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

test("Localization: language, right-to-left, denominations, a terminal's own layout", async ({ page, request }) => {
  const auth = { Authorization: `Bearer ${await token(request)}` };
  const reset = () =>
    request.put(`${API}/settings/localization`, {
      headers: auth,
      data: { language: "en", timezone: "Asia/Kolkata", text_direction: "auto", cart_side: "right", denominations: null },
    });
  await reset();
  const terminals = await (await request.get(`${API}/pos-terminals`, { headers: auth })).json();
  const till = (terminals.terminals ?? terminals)[0];

  try {
    await login(page);

    await test.step("English by default: the screens read as before", async () => {
      await page.goto("/localization");
      await expect(page.getByRole("heading", { name: "Localization" })).toBeVisible();
      await expect(page.locator("html")).toHaveAttribute("dir", "ltr");
      await expect(page.getByTestId("standard-denominations")).toContainText("₹2,000.00");
    });

    await test.step("Spanish, with the merchant's own notes and coins", async () => {
      await page.getByLabel("Language").first().selectOption("es");
      await page.getByLabel("Our own list").check();
      await page.getByLabel("Denominations").fill("500, 100, 0.5");
      await page.getByRole("button", { name: "Save" }).click();
      await expect(page.getByRole("heading", { name: "Localización" })).toBeVisible();
      await expect(page.getByRole("navigation", { name: "Main" })).toContainText("Ventas");
      await expect(page.getByRole("button", { name: "Cerrar sesión" })).toBeVisible();
      await expect(page.locator("html")).toHaveAttribute("lang", "es");

      await page.goto("/cash-reconciliation");
      await expect(page.getByText("₹0.5", { exact: true })).toBeVisible();
      await expect(page.getByText("₹2000", { exact: true })).toHaveCount(0);
    });

    await test.step("Arabic turns the back office right-to-left", async () => {
      const r = await request.put(`${API}/settings/localization`, { headers: auth, data: { language: "ar" } });
      expect(r.status()).toBe(200);
      await page.goto("/dashboard");
      await expect(page.locator("html")).toHaveAttribute("dir", "rtl");
      await expect(page.getByRole("navigation", { name: "Main" })).toContainText("المبيعات");
      // The sidebar sits on the right in a right-to-left page.
      const aside = await page.locator("aside").first().boundingBox();
      const main = await page.locator("#main-content").boundingBox();
      expect(aside!.x).toBeGreaterThan(main!.x);
    });

    await test.step("a terminal's own text direction and cart side", async () => {
      await reset();
      await page.goto("/terminals");
      await page.getByRole("button", { name: `Screen layout of ${till.name}` }).click();
      await page.getByLabel("Text direction").selectOption("rtl");
      await page.getByLabel("Cart side").selectOption("left");
      await page.getByRole("button", { name: "Save", exact: true }).click();
      await expect(page.getByText("Right to left · Cart on the left")).toBeVisible();
      const eff = await (await request.get(`${API}/settings/localization/terminal?pos_terminal_id=${till.terminal_id}`, { headers: auth })).json();
      expect(eff.direction).toBe("rtl");
      expect(eff.cart_side).toBe("left");
    });
  } finally {
    await reset();
    if (till) await request.patch(`${API}/pos-terminals/${till.terminal_id}`, { headers: auth, data: { text_direction: "", cart_side: "" } });
  }
});
