import { test, expect, Page, APIRequestContext } from "@playwright/test";

// Phase 15 data access in the browser: a Merchant Admin limits the Branch
// Manager role to its own branch on Roles & Permissions, gives Meera (MG
// Road) a second branch on Data Access and takes it away again. Each step is
// checked against what the server actually lets Meera see, not just the
// screen: the database enforces the scope.

const API = "http://localhost:8080/api/v1";
const MERCHANT = process.env.E2E_MERCHANT_CODE ?? "acme-sports";
const KORAMANGALA = "33333333-1111-2222-3333-444444444444";

async function login(page: Page, email: string) {
  await page.evaluate(() => localStorage.clear()).catch(() => {});
  await page.goto("/login");
  await page.fill("#email", email);
  await page.fill("#password", "Passw0rd!");
  await page.click('button[type="submit"]');
  await page.waitForURL("/dashboard");
}

async function token(request: APIRequestContext, email: string, password = "Passw0rd!") {
  const r = await request.post(`${API}/auth/login`, { data: { merchant_code: MERCHANT, email, password } });
  expect(r.ok()).toBeTruthy();
  return (await r.json()).access_token as string;
}

async function meeraSees(request: APIRequestContext) {
  const t = await token(request, "meera@acme-sports.test", "9999");
  const me = await (await request.get(`${API}/me/permissions`, { headers: { Authorization: `Bearer ${t}` } })).json();
  const stock = await request.get(`${API}/inventory/expiring-batches?branch_id=${KORAMANGALA}`, { headers: { Authorization: `Bearer ${t}` } });
  return { all: me.data_scope.all as boolean, branches: (me.data_scope.branches as { name: string }[]).map((b) => b.name), korStatus: stock.status() };
}

test("Data access: own-branch role, extra branches, enforced by the server", async ({ page, request }) => {
  const admin = await token(request, "arjun@acme-sports.test");
  const auth = { Authorization: `Bearer ${admin}` };
  const roles = (await (await request.get(`${API}/rbac/roles`, { headers: auth })).json()).roles as { id: string; name: string }[];
  const bm = roles.find((r) => r.name === "Branch Manager")!;
  const meeraId = (await (await request.get(`${API}/rbac/data-access`, { headers: auth })).json()).users.find(
    (u: { email: string | null }) => u.email === "meera@acme-sports.test",
  ).user_id as string;
  await request.put(`${API}/rbac/users/${meeraId}/branches`, { headers: auth, data: { branch_ids: [] } });
  // Start from "all branches" so the screen has something to change.
  await request.patch(`${API}/rbac/roles/${bm.id}`, { headers: auth, data: { data_scope: "all" } });
  expect((await meeraSees(request)).all).toBe(true);

  try {
    await login(page, "arjun@acme-sports.test");

    await test.step("Branch Manager role set to own branch on Roles & Permissions", async () => {
      await page.goto("/rbac");
      await page.getByRole("row", { name: /Branch Manager/ }).first().click();
      await page.locator("#role-data-scope").selectOption("branch");
      await expect(page.getByRole("row", { name: /Branch Manager/ }).first()).toContainText("Own branch");
      const s = await meeraSees(request);
      expect(s.all).toBe(false);
      expect(s.branches).toEqual(["MG Road"]);
      expect(s.korStatus).toBe(403);
    });

    const meeraRow = () => page.getByRole("row", { name: /Meera Nair/ });

    await test.step("give Meera Koramangala as an extra branch", async () => {
      await page.goto("/rbac/data-access");
      await expect(meeraRow()).toContainText("Own branch(es)");
      await expect(meeraRow()).toContainText("MG Road");
      await page.getByRole("button", { name: "Edit branches for Meera Nair" }).click();
      const dialog = page.getByRole("dialog");
      await expect(dialog.getByText("MG Road", { exact: true })).toHaveCount(0); // her home branch isn't offered
      await dialog.getByLabel("Koramangala").check();
      await dialog.getByRole("button", { name: "Save" }).click();
      await expect(meeraRow()).toContainText("Koramangala");
      const s = await meeraSees(request);
      expect(s.branches.sort()).toEqual(["Koramangala", "MG Road"]);
      expect(s.korStatus).toBe(200);
    });

    await test.step("take it away again", async () => {
      await page.getByRole("button", { name: "Edit branches for Meera Nair" }).click();
      await page.getByRole("dialog").getByLabel("Koramangala").uncheck();
      await page.getByRole("dialog").getByRole("button", { name: "Save" }).click();
      await expect(meeraRow().getByRole("cell").nth(4)).toHaveText("—");
      const s = await meeraSees(request);
      expect(s.branches).toEqual(["MG Road"]);
      expect(s.korStatus).toBe(403);
    });

    await test.step("a Branch Manager can't open Data Access", async () => {
      const t = await token(request, "meera@acme-sports.test", "9999");
      const r = await request.get(`${API}/rbac/data-access`, { headers: { Authorization: `Bearer ${t}` } });
      expect(r.status()).toBe(403);
    });
  } finally {
    await request.put(`${API}/rbac/users/${meeraId}/branches`, { headers: auth, data: { branch_ids: [] } });
    await request.patch(`${API}/rbac/roles/${bm.id}`, { headers: auth, data: { data_scope: "branch" } });
  }
});
