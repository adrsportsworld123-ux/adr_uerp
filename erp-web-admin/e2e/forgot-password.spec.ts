import { test, expect } from "@playwright/test";

// End-to-end coverage for Phase 1's last named-but-never-built roadmap
// item — a real forgot-password flow — closed 2026-09-22. The reset
// code is emailed in this environment's dev SMTP setup; this test reads
// it back the same way a real admin would need SMTP access to (there's
// no UI for "read my email" here), via a direct API call to the seeded
// merchant's real notification log, which is exactly what the backend
// actually sent.
const API_BASE = "http://localhost:8080/api/v1";

test("forgot password: request a code, reset, and confirm the old password stops working", async ({ page, request }) => {
  // A dedicated walk-in-style test user would need real signup, which
  // this system doesn't have — so this test resets Ravi's own password
  // and restores it via the dev-only set-password tool afterward, the
  // same restore-after-test discipline other specs already use for
  // shared seed state (e.g. inventory.spec.ts's matching -2 adjustment).
  const newPassword = `E2ENewPass${Date.now() % 1000000}!`;

  await test.step("request a reset code", async () => {
    await page.goto("/forgot-password");
    await page.fill("#merchantCode", "acme-sports");
    await page.fill("#email", "ravi@acme-sports.test");
    await page.click('button[type="submit"]');
    await expect(page.getByText(/Enter the code we emailed you/)).toBeVisible();
  });

  let code = "";
  await test.step("read the real code the backend actually emailed", async () => {
    const adminLogin = await request.post(`${API_BASE}/auth/login`, {
      data: { merchant_code: "acme-sports", email: "arjun@acme-sports.test", password: "Passw0rd!" },
    });
    const { access_token } = await adminLogin.json();
    const logs = await request.get(`${API_BASE}/notifications?category=password_reset&limit=1`, {
      headers: { Authorization: `Bearer ${access_token}` },
    });
    const body = await logs.json();
    const bodyText: string = body.notifications[0].body;
    const match = bodyText.match(/reset your password \(expires in 1 hour\): (\S+)/);
    expect(match).not.toBeNull();
    code = match![1];
  });

  await test.step("submit the code and a new password", async () => {
    await page.fill("#code", code);
    await page.fill("#newPassword", newPassword);
    await page.click('button[type="submit"]');
    await expect(page.getByText("Your password has been changed")).toBeVisible();
  });

  await test.step("the old password no longer works, the new one does", async () => {
    const oldLogin = await request.post(`${API_BASE}/auth/login`, {
      data: { merchant_code: "acme-sports", email: "ravi@acme-sports.test", password: "Passw0rd!" },
    });
    expect(oldLogin.status()).toBe(401);

    const newLogin = await request.post(`${API_BASE}/auth/login`, {
      data: { merchant_code: "acme-sports", email: "ravi@acme-sports.test", password: newPassword },
    });
    expect(newLogin.ok()).toBeTruthy();
  });

  await test.step("restore the seed password for every other spec in this suite", async () => {
    const resp = await request.post("http://localhost:8080/dev/set-password", {
      data: { merchant_code: "acme-sports", email: "ravi@acme-sports.test", new_password: "Passw0rd!" },
    });
    expect(resp.ok()).toBeTruthy();
  });
});
