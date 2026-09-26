import { test, expect } from "@playwright/test";

// End-to-end coverage for UACL — role/permission administration
// (erp-core-go's internal/rbac, migrations/025_rbac.sql). `roles`/
// `permissions`/`role_permissions`/`user_roles` have existed since
// Phase 0, but this is the first UI (and first API) that can manage them
// directly rather than requiring a developer to edit a migration file.
// Drives the real /rbac screen: create a custom role, assign it real
// permissions, grant it to an employee alongside their existing role
// (multi-role, not a replace), revoke it, then delete the now-unused
// role — all against the live backend.
const API_BASE = "http://localhost:8080/api/v1";

test("UACL: create a custom role, assign permissions, grant/revoke on a user, and confirm permission gating", async ({ page, request }) => {
  const unique = Date.now() % 1000000;
  const roleName = `E2E Role ${unique}`;

  await test.step("login as Merchant Admin", async () => {
    await page.goto("/login");
    await page.fill("#email", "arjun@acme-sports.test");
    await page.fill("#password", "Passw0rd!");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");
  });

  await test.step("create a custom role", async () => {
    await page.goto("/rbac");
    await page.fill("#newRoleName", roleName);
    await page.getByRole("button", { name: "Add role" }).click();
    await expect(page.getByText(roleName)).toBeVisible();
  });

  await test.step("select it and assign two real permissions", async () => {
    await page.getByRole("row", { name: new RegExp(roleName) }).click();
    await expect(page.getByText(`Permissions for "${roleName}"`)).toBeVisible();
    await page.locator("label", { hasText: "notifications.view" }).locator("input[type=checkbox]").check();
    await page.locator("label", { hasText: "search.reindex" }).locator("input[type=checkbox]").check();
    await page.getByRole("button", { name: "Save permissions" }).click();
    await expect(page.getByRole("row", { name: new RegExp(roleName) })).toContainText("2 permission(s)");
  });

  await test.step("assign the role to Ravi alongside his existing role", async () => {
    await page.getByRole("combobox").nth(0).click();
    await page.getByRole("option", { name: "Ravi Kumar" }).click();
    await page.getByRole("combobox").nth(1).click();
    await page.getByRole("option", { name: roleName, exact: true }).click();
    await page.getByRole("button", { name: "Assign" }).click();

    const raviRow = page.getByRole("row", { name: /Ravi Kumar/ });
    await expect(raviRow.getByText(roleName)).toBeVisible();
    await expect(raviRow.getByText("POS User")).toBeVisible(); // his original role is untouched, not replaced
  });

  await test.step("Ravi now actually has the new permission via the real backend", async () => {
    const login = await request.post(`${API_BASE}/auth/login`, {
      data: { merchant_code: "acme-sports", email: "ravi@acme-sports.test", password: "Passw0rd!" },
    });
    const { access_token } = await login.json();
    const resp = await request.get(`${API_BASE}/notifications`, { headers: { Authorization: `Bearer ${access_token}` } });
    expect(resp.ok()).toBeTruthy(); // notifications.view is now real, not just a UI checkbox
  });

  await test.step("revoke the role from Ravi", async () => {
    const raviRow = page.getByRole("row", { name: /Ravi Kumar/ });
    await raviRow.getByRole("button", { name: `Revoke ${roleName}` }).click();
    await expect(raviRow.getByText(roleName)).not.toBeVisible();
  });

  await test.step("Ravi's access is actually revoked on the real backend", async () => {
    const login = await request.post(`${API_BASE}/auth/login`, {
      data: { merchant_code: "acme-sports", email: "ravi@acme-sports.test", password: "Passw0rd!" },
    });
    const { access_token } = await login.json();
    const resp = await request.get(`${API_BASE}/notifications`, { headers: { Authorization: `Bearer ${access_token}` } });
    expect(resp.status()).toBe(403);
  });

  await test.step("delete the now-unused role", async () => {
    const roleRow = page.getByRole("row", { name: new RegExp(roleName) });
    await roleRow.getByRole("button", { name: "Delete" }).click();
    // Scoped to the roles table's own rows, not a bare text search — a
    // closed base-ui Select can leave its last-rendered listbox content
    // sitting in the DOM (an exit-animation artifact), and a loose
    // getByText(roleName) also matches that stale portal content and the
    // still-mounted "Permissions for ..." panel heading, neither of
    // which reflects real app state (confirmed live: the role is gone
    // from a direct API call the instant this click resolves).
    await expect(page.getByRole("row", { name: new RegExp(roleName) })).toHaveCount(0);
  });

  await test.step("a POS User is denied the whole /rbac screen", async () => {
    await page.goto("/dashboard");
    await page.click("text=Log out");
    await page.waitForURL("/login");
    await page.fill("#email", "ravi@acme-sports.test");
    await page.fill("#password", "Passw0rd!");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");

    await page.goto("/rbac");
    await expect(page.getByText(/rbac\.manage/)).toBeVisible();
  });
});
