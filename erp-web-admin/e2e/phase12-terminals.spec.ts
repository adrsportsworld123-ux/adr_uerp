import { test, expect, Page } from "@playwright/test";

// Phase 12: POS terminal management through the real browser and API.
// The device side of pairing is a direct API call — exactly what the
// Flutter pairing screen sends (POST /pos-terminals/pair).

const API = "http://localhost:8080/api/v1";
const unique = Date.now();
const SEED_BRANCH = "22222222-2222-2222-2222-222222222222";

async function login(page: Page, email: string, password: string) {
  await page.goto("/login");
  await page.fill("#email", email);
  await page.fill("#password", password);
  await page.click('button[type="submit"]');
  await page.waitForURL("/dashboard");
}

test("Phase 12: create a terminal, pair a device with a one-time code, unbind and deactivate it", async ({ page, request }) => {
  const name = `E2E Counter ${unique}`;
  await login(page, "admin@adrsw.com", "Idontsay3#");

  await test.step("create the terminal", async () => {
    await page.goto("/terminals");
    await page.getByRole("button", { name: "New terminal" }).click();
    await page.locator("#terminal-branch").selectOption(SEED_BRANCH);
    await page.fill("#terminal-name", name);
    await page.getByRole("button", { name: "Create terminal" }).click();
    await expect(page.getByText(`Created terminal “${name}”`)).toBeVisible();
    await expect(page.getByRole("row", { name: new RegExp(name) })).toContainText("Not paired");
  });

  let code = "";
  await test.step("issue a pairing code", async () => {
    await page.getByRole("button", { name: `Pair a device to ${name}` }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toContainText("Expires in");
    code = ((await dialog.getByLabel(/^Pairing code /).getAttribute("aria-label")) ?? "").replace("Pairing code ", "").replace(/ /g, "");
    expect(code).toMatch(/^[2-9A-HJ-NP-Z]{8}$/);
    await dialog.getByRole("button", { name: "Done" }).click();
    await expect(page.getByRole("row", { name: new RegExp(name) })).toContainText("Code issued");
  });

  await test.step("the device pairs with it once; a second use is refused", async () => {
    const pair = (fp: string) =>
      request.post(`${API}/pos-terminals/pair`, { data: { merchant_code: "acme-sports", pairing_code: code, device_fingerprint: fp } });
    const first = await pair(`e2e-device-${unique}`);
    expect(first.status()).toBe(200);
    expect((await first.json()).terminal_name).toBe(name);
    expect((await pair(`e2e-device-2-${unique}`)).status()).toBe(401);
    await page.reload();
    await expect(page.getByRole("row", { name: new RegExp(name) })).toContainText("Paired");
  });

  await test.step("unbind the device (lost tablet)", async () => {
    await page.getByRole("button", { name: `Reset device on ${name}` }).click();
    await page.getByRole("button", { name: "Unbind device" }).click();
    await expect(page.getByText(`Device unbound from “${name}”`)).toBeVisible();
    await expect(page.getByRole("row", { name: new RegExp(name) })).toContainText("Not paired");
  });

  await test.step("deactivate it: pairing is no longer offered", async () => {
    await page.getByRole("button", { name: `Deactivate ${name}` }).click();
    await page.getByRole("alertdialog").getByRole("button", { name: "Deactivate" }).click();
    await expect(page.getByText(`Deactivated “${name}”`)).toBeVisible();
    await expect(page.getByRole("button", { name: `Pair a device to ${name}` })).toBeDisabled();
  });
});

test("Phase 12: a POS User can view terminals but not manage them", async ({ page }) => {
  await login(page, "ravi@acme-sports.test", "Passw0rd!");
  await page.goto("/terminals");
  await expect(page.getByRole("heading", { name: "POS Terminals" })).toBeVisible();
  await expect(page.getByRole("button", { name: "New terminal" })).toBeDisabled();
});

test("Phase 12: deactivating a branch explains that its tills close", async ({ page }) => {
  const code = `P12B${unique % 100000}`;
  await login(page, "admin@adrsw.com", "Idontsay3#");
  await page.goto("/branches");
  await page.fill("#name", `E2E P12 Branch ${code}`);
  await page.fill("#code", code);
  await page.click('button[type="submit"]');
  await page.getByRole("button", { name: `Deactivate E2E P12 Branch ${code}` }).click();
  const dialog = page.getByRole("alertdialog");
  await expect(dialog).toContainText("POS terminals stop accepting sign-ins");
  await dialog.getByRole("button", { name: "Deactivate branch" }).click();
  await expect(page.getByRole("row", { name: new RegExp(code) })).toContainText(/Inactive/);
});
