import { test, expect, Page, APIRequestContext } from "@playwright/test";
import { readFileSync } from "node:fs";

// Phase 14 Label Studio: a throwaway product with a generated barcode is
// printed three ways. The thermal print file is downloaded and read back;
// the two browser paths are checked by what they lay out (window.print is
// stubbed, since a headless browser has no print dialog). Printing on a
// real 203-dpi printer is the hardware item the roadmap records separately.

const API = "http://localhost:8080/api/v1";
const MERCHANT = process.env.E2E_MERCHANT_CODE ?? "acme-sports";
const GST18 = "66666666-6666-6666-6666-666666666666";

async function login(page: Page, email: string) {
  await page.goto("/login");
  await page.fill("#email", email);
  await page.fill("#password", "Passw0rd!");
  await page.click('button[type="submit"]');
  await page.waitForURL("/dashboard");
}

async function token(request: APIRequestContext) {
  const r = await request.post(`${API}/auth/login`, { data: { merchant_code: MERCHANT, email: "arjun@acme-sports.test", password: "Passw0rd!" } });
  return (await r.json()).access_token as string;
}

test("Phase 14: label studio previews, flags an unscannable size, and prints three ways", async ({ page, request }) => {
  const headers = { Authorization: `Bearer ${await token(request)}` };
  const sku = `E2E-LBL-${Date.now()}`;
  const created = await (
    await request.post(`${API}/products`, {
      headers,
      data: { name: `Label ${sku}`, tax_slab_id: GST18, variants: [{ sku, cost_price: 40, mrp: 120, selling_price: 99, generate_barcode: true }] },
    })
  ).json();
  const variantId = created.variants[0].variant_id as string;

  try {
    await login(page, "arjun@acme-sports.test");
    await page.goto("/labels");
    await page.getByRole("button", { name: "Select a product" }).click();
    await page.getByLabel("Search products").fill(sku);
    await page.getByRole("option", { name: new RegExp(sku) }).click();
    await page.getByLabel("Copies", { exact: true }).fill("3");
    await page.getByRole("button", { name: "Add", exact: true }).click();

    const previewPane = page.getByRole("region", { name: "Preview" });
    await expect(previewPane.getByText(`SKU ${sku}`)).toBeVisible();
    await expect(previewPane.getByText("Scannable", { exact: true })).toBeVisible();
    await expect(previewPane.locator(".label-face svg")).toHaveCount(1); // the barcode, drawn

    await test.step("a too-small custom size is flagged and blocks the print file", async () => {
      await page.getByLabel("Size").selectOption("custom");
      await page.getByLabel("Width (mm)").fill("20");
      await page.getByLabel("Height (mm)").fill("15");
      await expect(previewPane.getByText(/Some won't scan|Small barcodes/)).toBeVisible();
      await page.getByLabel("Size").selectOption("standard");
      await expect(previewPane.getByText("Scannable", { exact: true })).toBeVisible();
    });

    await test.step("thermal print file: TSPL, read back", async () => {
      await page.getByLabel("Thermal printer language").selectOption("tspl");
      const [download] = await Promise.all([page.waitForEvent("download"), page.getByRole("button", { name: "Print file" }).click()]);
      expect(download.suggestedFilename()).toMatch(/\.prn$/);
      const body = readFileSync((await download.path())!, "utf8");
      expect(body).toContain("SIZE 50 mm,30 mm");
      expect(body).toContain("PRINT 1,3");
      expect(body).toContain(created.variants[0].generated_barcode);
    });

    await test.step("browser paths: exact label size, then an A4 sheet", async () => {
      await page.evaluate(() => {
        window.print = () => {}; // no dialog in a headless browser; keep the print layout up
      });
      await page.getByRole("button", { name: "Print at label size" }).click();
      await expect(page.locator("#label-print .label-face")).toHaveCount(3);
      const pageRule = await page.evaluate(() => Array.from(document.head.querySelectorAll("style")).map((s) => s.textContent).join("\n"));
      expect(pageRule).toContain("size: 50mm 30mm");

      await page.getByRole("button", { name: "A4 sticker sheet (PDF)" }).click();
      await expect(page.locator('#label-print[data-mode="sheet"]')).toHaveCount(1);
      const sheetRule = await page.evaluate(() => Array.from(document.head.querySelectorAll("style")).map((s) => s.textContent).join("\n"));
      expect(sheetRule).toContain("size: A4");
      const cols = await page.locator("#label-print").evaluate((el) => (el as HTMLElement).style.gridTemplateColumns);
      expect(cols).toContain("repeat(3"); // 50 mm labels: 3 across an A4 page
    });
  } finally {
    await request.patch(`${API}/products/variants/${variantId}`, { headers, data: { status: "discontinued" } });
  }
});
