import { test, expect, Page } from "@playwright/test";

// Session expiry handling (lib/api-client.ts "Session lifecycle"):
//   - idle for a whole role-tier window  -> back to /login with a notice,
//     and signing in again returns to the page the user was on
//   - active user with a dead access token -> silent refresh, no interruption
//   - refresh token rejected too           -> back to /login with a notice
// Waiting out a real 15-60 minute window isn't practical, so each test
// rewinds the client-side session bookkeeping in localStorage instead —
// the server-side half (rejecting a bad access/refresh token) is real.

async function login(page: Page) {
  await page.goto("/login");
  await page.fill("#email", "admin@adrsw.com");
  await page.fill("#password", "Idontsay3#");
  await page.click('button[type="submit"]');
  await page.waitForURL("/dashboard");
}

test("an idle session expires to /login, and signing back in returns to the same page", async ({ page }) => {
  await login(page);
  await page.goto("/pricing");
  await expect(page.getByRole("heading", { name: "Pricing" })).toBeVisible();

  // Last activity a full session window (+1s) ago.
  await page.evaluate(() => {
    const windowMs = Number(localStorage.getItem("session_window_ms"));
    localStorage.setItem("last_activity_at", String(Date.now() - windowMs - 1000));
  });
  // The provider checks on an interval and whenever the window regains focus.
  await page.evaluate(() => window.dispatchEvent(new Event("focus")));

  await page.waitForURL("/login");
  await expect(page.getByTestId("session-expired-notice")).toContainText("inactivity");
  expect(await page.evaluate(() => localStorage.getItem("access_token"))).toBeNull();

  await page.fill("#email", "admin@adrsw.com");
  await page.fill("#password", "Idontsay3#");
  await page.click('button[type="submit"]');
  await page.waitForURL("/pricing");
});

test("an active user's expired access token is refreshed silently", async ({ page }) => {
  await login(page);
  const before = await page.evaluate(() => localStorage.getItem("refresh_token"));

  // A token the server rejects (401 INVALID_TOKEN), with recent activity.
  await page.evaluate(() => {
    localStorage.setItem("access_token", "expired.or.tampered.token");
    localStorage.setItem("last_activity_at", String(Date.now()));
  });

  await page.goto("/pricing");
  await expect(page.getByRole("heading", { name: "Pricing" })).toBeVisible();
  // Real data loaded through the retried request, and no bounce to /login.
  await expect(page.getByRole("button", { name: "Edit" }).first()).toBeVisible();
  expect(page.url()).toContain("/pricing");

  const after = await page.evaluate(() => ({
    access: localStorage.getItem("access_token"),
    refresh: localStorage.getItem("refresh_token"),
  }));
  expect(after.access).not.toBe("expired.or.tampered.token");
  expect(after.refresh).not.toBe(before); // rotated by POST /auth/refresh
});

test("a session whose refresh token is rejected ends at /login", async ({ page }) => {
  await login(page);
  await page.evaluate(() => {
    localStorage.setItem("access_token", "expired.or.tampered.token");
    localStorage.setItem("refresh_token", "revoked-refresh-token");
    localStorage.setItem("last_activity_at", String(Date.now()));
  });

  await page.goto("/pricing");
  await page.waitForURL("/login");
  await expect(page.getByTestId("session-expired-notice")).toContainText("Your session has ended");
});

test("signing out in one tab signs out the other", async ({ page, context }) => {
  await login(page);
  const other = await context.newPage();
  await other.goto("/pricing");
  await expect(other.getByRole("heading", { name: "Pricing" })).toBeVisible();

  await page.getByRole("button", { name: "Log out" }).click();
  await other.waitForURL("/login");
});
