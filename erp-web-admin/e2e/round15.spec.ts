import { test, expect, APIRequestContext, Page } from "@playwright/test";

// erp-core-go §3.82–§3.83 (migrations/079–080): selling price suggestions
// (switched on by the Merchant Admin, filled into the Edit pricing dialog,
// which also edits the original barcode, cost and MRP); a dairy herd's
// animal register, a birth, milk recording refused in a withdrawal, and
// mixing feed. The API flows (price_flow, farm2_flow) cover the
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

async function pick(page: Page, trigger: string, text: string) {
  await page.locator(trigger).click();
  await page.getByLabel("Search products").fill(text);
  await page.getByRole("option", { name: new RegExp(text) }).first().click();
}

test.describe.configure({ mode: "serial" });

test("price suggestions: switched on, filled into the pricing dialog with the barcode, cost and MRP editable", async ({ page, request }) => {
  const auth = { Authorization: `Bearer ${await token(request)}` };
  const tag = Date.now().toString().slice(-6);
  const prod = await (
    await request.post(`${API}/products`, {
      headers: auth,
      data: { name: `E2E Test Ball ${tag}`, price_includes_tax: true, variants: [{ sku: `E2E-TB-${tag}`, mrp: 575, selling_price: 500, cost_price: 360 }] },
    })
  ).json();
  const variant = prod.variants[0].variant_id as string;
  try {
    await login(page);
    await page.goto("/pricing/suggestions");
    await page.locator("#sg-enabled").check();
    await expect(page.getByText("Price suggestions are on")).toBeVisible();
    await expect(page.getByTestId("suggestion-rules")).toContainText("All products");
    await page.locator("#sg-cost-tax").check();
    await expect(page.locator("#sg-cost-tax")).toBeChecked();

    // Try it: ₹360 at the 15% margin → ₹423.53 → ₹429.
    await page.fill("#try-cost", "360");
    await page.fill("#try-mrp", "575");
    await page.getByRole("button", { name: "Suggest" }).click();
    await expect(page.getByTestId("try-result")).toContainText("429");

    await page.goto(`/pricing?q=E2E-TB-${tag}`);
    await page.getByRole("button", { name: `Edit price of E2E-TB-${tag}` }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.locator("#sellingPrice")).toHaveValue("429");
    await expect(dialog.getByTestId("price-suggestion")).toContainText("All products");
    await dialog.locator("#costPrice").fill("300");
    await expect(dialog.locator("#sellingPrice")).toHaveValue("359"); // 300 / 0.85 = 352.94 → 359
    await dialog.locator("#mrpPrice").fill("599");
    await dialog.locator("#originalBarcode").fill(`OB${tag}`);
    await dialog.getByRole("button", { name: "Save" }).click();
    await expect(page.getByText("Pricing updated")).toBeVisible();

    const scan = await (await request.get(`${API}/products/barcode/OB${tag}`, { headers: auth })).json();
    expect(scan.variant_id).toBe(variant);
    const detail = await (await request.get(`${API}/products/${prod.product_id}`, { headers: auth })).json();
    const v = detail.variants[0];
    expect([v.cost_price, v.mrp, v.selling_price]).toEqual(["300.00", "599.00", "359.00"]);
  } finally {
    await request.put(`${API}/pricing/suggestion-settings`, { headers: auth, data: { enabled: false, cost_includes_tax: false } });
  }
});

test("a dairy herd: an animal added, a calving, milk refused in a withdrawal, and feed mixed", async ({ page, request }) => {
  const auth = { Authorization: `Bearer ${await token(request)}` };
  const tag = Date.now().toString().slice(-6);
  const branch = await (
    await request.post(`${API}/branches`, { headers: auth, data: { name: `E2E Dairy ${tag}`, code: `ED${tag}`, business_types: ["dairy_farm"] } })
  ).json();
  const units = (await (await request.get(`${API}/units`, { headers: auth })).json()).units as { code: string; unit_id: string }[];
  const unit = (c: string) => units.find((u) => u.code === c)!.unit_id;
  const product = async (name: string, u: string, cost: number) =>
    (
      await (
        await request.post(`${API}/products`, {
          headers: auth,
          data: { name: `${name} ${tag}`, unit_id: unit(u), variants: [{ sku: `${name.slice(0, 5).replace(/ /g, "")}-${tag}`, mrp: cost * 2 || 60, selling_price: cost * 2 || 60, cost_price: cost }] },
        })
      ).json()
    ) as { product_id: string; variants: { variant_id: string }[] };
  await product("E2E Raw milk", "L", 0); // picked by name on the milk sheet
  const med = await product("E2E Oxytet", "PCS", 50);
  const maize = await product("E2E Maize", "KG", 20);
  const mash = await product("E2E Cattle mash", "KG", 0);
  for (const [v, q] of [
    [med.variants[0].variant_id, 10],
    [maize.variants[0].variant_id, 500],
  ] as const) {
    await request.post(`${API}/inventory/adjustments`, { headers: auth, data: { variant_id: v, branch_id: branch.branch_id, quantity_delta: q, reason: "opening" } });
  }
  await request.post(`${API}/farm/items`, { headers: auth, data: { product_id: med.product_id, role: "medicine", withdrawal_milk_days: 3 } });
  const tenDaysAgo = new Date(Date.now() + 5.5 * 3600_000 - 10 * 86_400_000).toISOString().slice(0, 10);
  const herd = await (
    await request.post(`${API}/farm/batches`, {
      headers: auth,
      data: { branch_id: branch.branch_id, species: "cow", purpose: "dairy", placed_on: tenDaysAgo, age_at_placement_days: 1500, initial_count: 2 },
    })
  ).json();
  await request.post(`${API}/farm/feed/recipes`, {
      headers: auth,
    data: { name: `E2E mash ${tag}`, output_variant_id: mash.variants[0].variant_id, batch_size: 100, items: [{ variant_id: maize.variants[0].variant_id, quantity: 100 }] },
  });
  try {
    await login(page);

    // The register: one of the herd's two cows.
    await page.goto(`/farm/animals?batch=${herd.batch_id}`);
    await page.getByRole("button", { name: "Add an animal" }).click();
    await page.fill("#fa-tag", `LAK-${tag}`);
    await page.fill("#fa-name", "Lakshmi");
    await page.getByRole("dialog").getByRole("button", { name: "Add" }).click();
    await page.waitForURL(/\/farm\/animals\/[0-9a-f-]+$/);
    await expect(page.getByText(`LAK-${tag} is on the register`)).toBeVisible();

    // She calves; the calf (tagged) joins the register.
    await page.getByRole("button", { name: "Breeding" }).click();
    await page.locator("#fbr-type").selectOption("birth");
    await page.fill("#fbr-alive", "1");
    await page.getByRole("button", { name: "Tag a young one" }).click();
    await page.getByLabel("Tag of young 1").fill(`CALF-${tag}`);
    await page.getByRole("dialog").getByRole("button", { name: "Record" }).click();
    await expect(page.getByText(`Birth recorded for LAK-${tag}`)).toBeVisible();
    await expect(page.getByTestId("animal-kpis")).toContainText("in milk");
    await expect(page.getByText(`CALF-${tag}`)).toBeVisible();
    const animalId = page.url().split("/").pop()!;

    // Treated: no milk for 3 days.
    await request.post(`${API}/farm/batches/${herd.batch_id}/events`, {
      headers: auth,
      data: { event_type: "medicine", event_date: new Date(Date.now() + 5.5 * 3600_000).toISOString().slice(0, 10), animal_id: animalId, variant_id: med.variants[0].variant_id, quantity: 1 },
    });
    await page.reload();
    await expect(page.getByTestId("animal-withdrawal")).toContainText("No milk until");

    await page.goto(`/farm/milk?batch=${herd.batch_id}`);
    await pick(page, "#mk-item", `E2E Raw milk ${tag}`);
    await page.getByLabel(`Litres from LAK-${tag}`).fill("9");
    await page.getByRole("button", { name: /Save 9 L/ }).click();
    await expect(page.getByRole("alert")).toContainText("no milk until");
    await page.fill("#mk-override", "Recorded, then discarded");
    await page.getByRole("button", { name: "Record anyway" }).click();
    await expect(page.getByText(/Morning milking: 9 L into stock/)).toBeVisible();

    // Mix 50 kg of the farm's own mash.
    await page.goto("/farm/feed?tab=mixing");
    await page.getByRole("button", { name: `Mix E2E mash ${tag}` }).click();
    await page.locator("#mx-branch").selectOption({ label: `E2E Dairy ${tag}` });
    await page.fill("#mx-qty", "50");
    await expect(page.getByTestId("mix-preview")).toContainText("50 kg");
    await page.getByRole("dialog").getByRole("button", { name: "Mix" }).click();
    await expect(page.getByText(/FM-\d+: 50 kg/)).toBeVisible();
    await expect(page.getByTestId("feed-mixes")).toContainText(`E2E Cattle mash ${tag}`);

    await page.goto("/farm");
    await expect(page.getByTestId("farm-alerts")).toContainText(`LAK-${tag}`);
  } finally {
    // Leave nothing that blocks other specs: the cows leave, the herd closes.
    const animals = (await (await request.get(`${API}/farm/animals?batch_id=${herd.batch_id}`, { headers: auth })).json()).animals as { animal_id: string }[];
    for (const a of animals) await request.post(`${API}/farm/animals/${a.animal_id}/exit`, { headers: auth, data: { how: "culled", date: new Date(Date.now() + 5.5 * 3600_000).toISOString().slice(0, 10) } });
    await request.post(`${API}/farm/batches/${herd.batch_id}/close`, { headers: auth, data: { note: "e2e" } });
  }
});
