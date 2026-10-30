import { test, expect, APIRequestContext, Page } from "@playwright/test";

// erp-core-go §3.79–§3.81 (migrations/076–078): units of measure and pack
// barcodes; what a branch does (business types); a poultry farm — a shed,
// a broiler flock placed, mortality and a vaccination from its schedule,
// the farm report. The API flows (units_flow, farm_flow) cover the
// arithmetic; this checks the screens drive it.

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

test.describe.configure({ mode: "serial" });

test("units: a new pack unit, a product counted in kg, and a 25 kg bag barcode", async ({ page, request }) => {
  const auth = { Authorization: `Bearer ${await token(request)}` };
  const tag = Date.now().toString().slice(-6);
  const units = (await (await request.get(`${API}/units`, { headers: auth })).json()).units;
  const kg = units.find((u: { code: string }) => u.code === "KG");
  const prod = await (
    await request.post(`${API}/products`, {
      headers: auth,
      data: { name: `E2E Rice ${tag}`, unit_id: kg.unit_id, variants: [{ sku: `E2E-RICE-${tag}`, mrp: 60, selling_price: 60, cost_price: 45 }] },
    })
  ).json();

  await login(page);
  await page.goto("/catalog/units");
  await expect(page.getByTestId("units-table")).toContainText("Kilogram");
  await page.getByRole("button", { name: "New unit" }).click();
  await page.fill("#un-code", `SK${tag.slice(-4)}`);
  await page.fill("#un-symbol", "sack");
  await page.fill("#un-name", `Sack ${tag}`);
  await page.getByRole("dialog").getByRole("button", { name: "Save" }).click();
  await expect(page.getByText(`Sack ${tag} saved`)).toBeVisible();
  await expect(page.getByTestId("units-table").getByRole("row", { name: new RegExp(`Sack ${tag}`) })).toContainText("Set on each barcode");

  await page.goto(`/products/${prod.product_id}/edit`);
  await expect(page.locator("#unit")).toHaveValue(kg.unit_id);
  const bags = page.getByTestId(`barcodes-${prod.variants[0].variant_id}`);
  await bags.getByRole("button", { name: "Add a pack barcode" }).click();
  await page.fill("#pk-code", `E2EBAG${tag}`);
  await page.locator("#pk-unit").selectOption({ label: "Bag" });
  await page.fill("#pk-size", "25");
  await page.getByRole("dialog").getByRole("button", { name: "Save" }).click();
  await expect(page.getByText("Pack barcode added")).toBeVisible();
  await expect(bags).toContainText("Bag of 25 kg");
  const scan = await (await request.get(`${API}/products/barcode/E2EBAG${tag}`, { headers: auth })).json();
  expect(scan.pack_qty).toBe("25.000");
});

test("a branch becomes a poultry farm, and the Farm screens appear", async ({ page, request }) => {
  const auth = { Authorization: `Bearer ${await token(request)}` };
  const tag = Date.now().toString().slice(-6);
  const branch = await (await request.post(`${API}/branches`, { headers: auth, data: { name: `E2E Farm ${tag}`, code: `EF${tag}` } })).json();
  expect(branch.business_types).toEqual(["retail"]);

  await login(page);
  await page.goto("/branches");
  await page.getByRole("button", { name: `What E2E Farm ${tag} does` }).click();
  await page.getByRole("dialog").getByLabel("Poultry farm").check();
  await page.getByRole("dialog").getByLabel("Retail shop").uncheck();
  await page.getByRole("dialog").getByRole("button", { name: "Save" }).click();
  await expect(page.getByText(`E2E Farm ${tag}: saved what it does`)).toBeVisible();
  await expect(page.getByRole("link", { name: "Farm Dashboard" })).toBeVisible();

  // A shed, then a flock of 100 broilers placed 5 days ago.
  await page.goto("/farm/houses");
  await page.getByRole("button", { name: "New shed or pond" }).click();
  await page.locator("#fh-branch").selectOption({ label: `E2E Farm ${tag}` });
  await page.fill("#fh-name", `Shed ${tag}`);
  await page.fill("#fh-cap", "500");
  await page.getByRole("dialog").getByRole("button", { name: "Save" }).click();
  await expect(page.getByText(`Shed ${tag} saved`)).toBeVisible();

  await page.goto("/farm/batches");
  await page.getByRole("button", { name: "New batch" }).click();
  await page.locator("#fb-branch").selectOption({ label: `E2E Farm ${tag}` });
  await page.locator("#fb-house").selectOption({ label: `Shed ${tag} (0/500)` });
  const fiveDaysAgo = new Date(Date.now() + 5.5 * 3600_000 - 5 * 86_400_000).toISOString().slice(0, 10);
  await page.fill("#fb-placed", fiveDaysAgo);
  await page.fill("#fb-count", "100");
  await page.fill("#fb-breed", "Cobb 430");
  await page.getByRole("dialog").getByRole("button", { name: "Place" }).click();
  await page.waitForURL(/\/farm\/batches\/[0-9a-f-]+$/);
  await expect(page.getByTestId("batch-kpis")).toContainText("100 of 100");

  await page.getByRole("group", { name: "Record" }).getByRole("button", { name: "Mortality" }).click();
  await page.fill("#fe-heads", "3");
  await page.fill("#fe-cause", "Weak chicks");
  await page.getByRole("dialog").getByRole("button", { name: "Record" }).click();
  await expect(page.getByTestId("kpi-mortality")).toContainText("3%");
  await expect(page.getByTestId("batch-log")).toContainText("Weak chicks");

  // The Newcastle vaccine (day 6) is due tomorrow: record it from the schedule.
  const nd = page.getByTestId("batch-schedule").getByRole("row", { name: /Newcastle disease \(F1/ });
  await nd.getByRole("button", { name: /Record/ }).click();
  await expect(page.locator("#fe-vaccine")).toHaveValue(/Newcastle/);
  await page.getByRole("dialog").getByRole("button", { name: "Record" }).click();
  await expect(page.getByTestId("batch-schedule").getByRole("row", { name: /Newcastle disease \(F1/ })).toContainText("Done");

  await page.goto("/farm");
  await expect(page.getByTestId("farm-batches")).toContainText("Shed " + tag);

  await page.goto("/reports/library");
  await page.getByRole("button", { name: "Flock and herd performance" }).click();
  await page.getByRole("button", { name: "Run report" }).click();
  await expect(page.getByRole("columnheader", { name: "Mortality %" })).toBeVisible();

  // Close the flock so the branch can be put back.
  const list = (await (await request.get(`${API}/farm/batches?branch_id=${branch.branch_id}`, { headers: auth })).json()).batches;
  for (const b of list) await request.post(`${API}/farm/batches/${b.batch_id}/close`, { headers: auth, data: { note: "e2e" } });
});
