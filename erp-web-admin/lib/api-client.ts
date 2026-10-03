// Talks to erp-core-go's /api/v1 surface — same Bearer-token contract the
// Flutter POS app's ApiClient (erp-pos-flutter/lib/api/api_client.dart)
// already uses, so both clients speak to the same backend the same way.
// Money fields stay as strings end-to-end, same reasoning as the Flutter
// client: the server already settled them to NUMERIC(14,2)-correct values,
// and re-parsing to a JS number here would reintroduce float rounding risk
// for no benefit (this app only displays them, never does arithmetic).

const API_BASE_URL = process.env.NEXT_PUBLIC_API_BASE_URL ?? "http://localhost:8080";

export class ApiError extends Error {
  code: string;
  status: number;
  constructor(status: number, code: string, message: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

// ---------------------------------------------------------------------
// Session lifecycle
//
// The backend issues short-lived access tokens whose lifetime IS the
// FRD's role-based inactivity timeout (erp-core-go's
// internal/authn/session_tiers.go: POS User 15 min, Branch Manager 30,
// Merchant Admin 60), plus a 30-day rotating refresh token. So:
//
//   - active user, token about to expire          -> silent POST /auth/refresh
//   - no user activity for a full session window  -> expire, back to /login
//   - refresh rejected (revoked/expired/locked)   -> expire, back to /login
//
// Refreshing blindly on every 401 would quietly turn a 15-minute
// inactivity timeout into a 30-day session, so idleness is always checked
// first. A network failure or 5xx during refresh is NOT treated as expiry —
// the request just fails like any other unreachable-server request.
// ---------------------------------------------------------------------

const K = {
  access: "access_token",
  refresh: "refresh_token",
  expiresAt: "access_expires_at", // epoch ms
  window: "session_window_ms", // the role tier's inactivity window
  activity: "last_activity_at", // epoch ms, shared across tabs
} as const;

export const SESSION_EXPIRED_EVENT = "erp:session-expired";
export type SessionEndReason = "idle" | "revoked";
const EXPIRED_REASON_KEY = "session_expired_reason"; // sessionStorage, read once by /login
const POST_LOGIN_REDIRECT_KEY = "post_login_redirect"; // sessionStorage, read once by /login

const REFRESH_SKEW_MS = 60_000; // refresh this long before the access token actually expires

export interface TokenBundle {
  access_token: string;
  refresh_token: string;
  expires_in: number; // seconds
}

function ls(): Storage | null {
  return typeof window === "undefined" ? null : window.localStorage;
}

function num(key: string): number {
  const v = Number(ls()?.getItem(key));
  return Number.isFinite(v) ? v : 0;
}

function getToken(): string | null {
  return ls()?.getItem(K.access) ?? null;
}

export function hasSession(): boolean {
  return !!getToken();
}

export function storeTokens(t: TokenBundle, countsAsActivity: boolean) {
  const s = ls();
  if (!s) return;
  const windowMs = t.expires_in * 1000;
  s.setItem(K.access, t.access_token);
  s.setItem(K.refresh, t.refresh_token);
  s.setItem(K.expiresAt, String(Date.now() + windowMs));
  s.setItem(K.window, String(windowMs));
  if (countsAsActivity) markActivity(true);
}

export function clearTokens() {
  const s = ls();
  if (!s) return;
  Object.values(K).forEach((k) => s.removeItem(k));
}

let lastActivityWrite = 0;
// Called on real user input (see AuthProvider). Throttled: a localStorage
// write per mousemove would be wasteful, and 10s granularity is plenty
// against a 15-60 minute window.
export function markActivity(force = false) {
  const now = Date.now();
  if (!force && now - lastActivityWrite < 10_000) return;
  lastActivityWrite = now;
  ls()?.setItem(K.activity, String(now));
}

export function isIdle(): boolean {
  const windowMs = num(K.window);
  const last = num(K.activity);
  if (!windowMs || !last) return false; // session from before this bookkeeping existed — let the refresh path decide
  return Date.now() - last >= windowMs;
}

function accessTokenNeedsRefresh(): boolean {
  const exp = num(K.expiresAt);
  return !exp || exp - Date.now() < REFRESH_SKEW_MS;
}

export function expireSession(reason: SessionEndReason) {
  if (typeof window === "undefined" || !hasSession()) return;
  clearTokens();
  window.localStorage.removeItem("user_id");
  window.localStorage.removeItem("roles");
  try {
    window.sessionStorage.setItem(EXPIRED_REASON_KEY, reason);
    const here = window.location.pathname + window.location.search;
    if (!here.startsWith("/login")) window.sessionStorage.setItem(POST_LOGIN_REDIRECT_KEY, here);
  } catch {
    // sessionStorage unavailable (privacy mode) — the redirect still happens, just without the message
  }
  window.dispatchEvent(new CustomEvent<SessionEndReason>(SESSION_EXPIRED_EVENT, { detail: reason }));
}

// Read-once helpers for the login page.
export function takeSessionExpiredReason(): SessionEndReason | null {
  try {
    const r = window.sessionStorage.getItem(EXPIRED_REASON_KEY);
    window.sessionStorage.removeItem(EXPIRED_REASON_KEY);
    return r === "idle" || r === "revoked" ? r : null;
  } catch {
    return null;
  }
}

export function takePostLoginRedirect(): string | null {
  try {
    const r = window.sessionStorage.getItem(POST_LOGIN_REDIRECT_KEY);
    window.sessionStorage.removeItem(POST_LOGIN_REDIRECT_KEY);
    // Same-origin paths only — never an open redirect.
    return r && r.startsWith("/") && !r.startsWith("//") ? r : null;
  } catch {
    return null;
  }
}

type RefreshOutcome = "ok" | "rejected" | "unreachable";
let refreshInFlight: Promise<RefreshOutcome> | null = null;

// Single-flight within a tab, and serialized across tabs via the Web Locks
// API where available. The backend ROTATES refresh tokens (the old one is
// revoked on use), so two tabs refreshing with the same token at once would
// make the loser look "revoked" and log the user out. Inside the lock, a
// tab first checks whether another tab already refreshed while it waited.
export function refreshSession(): Promise<RefreshOutcome> {
  if (!refreshInFlight) {
    const tokenAtStart = getToken();
    const run = (): Promise<RefreshOutcome> => doRefresh(tokenAtStart);
    const locks = typeof navigator !== "undefined" ? navigator.locks : undefined;
    // locks.request() resolves to the callback's resolved value at runtime;
    // the DOM typings nest the promise, hence the cast.
    const p = locks ? (locks.request("erp-auth-refresh", run) as unknown as Promise<RefreshOutcome>) : run();
    refreshInFlight = p.finally(() => {
      refreshInFlight = null;
    });
  }
  return refreshInFlight as Promise<RefreshOutcome>;
}

async function doRefresh(tokenAtStart: string | null): Promise<RefreshOutcome> {
  const current = getToken();
  if (current && current !== tokenAtStart && !accessTokenNeedsRefresh()) return "ok"; // another tab won the race
  const refreshToken = ls()?.getItem(K.refresh);
  if (!refreshToken) return "rejected";
  let resp: Response;
  try {
    resp = await fetch(`${API_BASE_URL}/api/v1/auth/refresh`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ refresh_token: refreshToken }),
    });
  } catch {
    return "unreachable";
  }
  if (resp.status === 401 || resp.status === 403) return "rejected";
  if (!resp.ok) return "unreachable"; // 5xx — transient, don't log the user out over it
  storeTokens((await resp.json()) as TokenBundle, false);
  return "ok";
}

function sessionExpiredError(reason: SessionEndReason): ApiError {
  return new ApiError(
    401,
    "SESSION_EXPIRED",
    reason === "idle" ? "Your session expired due to inactivity. Please sign in again." : "Your session has ended. Please sign in again."
  );
}

// Proactive check, run before each request and on AuthProvider's timer:
// end an idle session, or refresh an active one that's about to lapse.
export async function ensureFreshSession(): Promise<void> {
  if (!hasSession()) return;
  if (isIdle()) {
    expireSession("idle");
    throw sessionExpiredError("idle");
  }
  if (accessTokenNeedsRefresh() && (await refreshSession()) === "rejected") {
    expireSession("revoked");
    throw sessionExpiredError("revoked");
  }
}

async function request<T>(path: string, options: RequestInit = {}, retried = false): Promise<T> {
  const isAuthEndpoint = path.startsWith("/api/v1/auth/");
  if (!isAuthEndpoint && !retried) await ensureFreshSession();

  const token = getToken();
  const resp = await fetch(`${API_BASE_URL}${path}`, {
    ...options,
    headers: {
      "Content-Type": "application/json",
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...options.headers,
    },
  });

  const text = await resp.text();
  const body = text ? JSON.parse(text) : {};

  // Reactive path: the token was rejected anyway (clock skew, server-side
  // revocation, a laptop asleep through the proactive timer). Same rules —
  // idle sessions end, active ones get one silent refresh-and-retry. Keyed
  // on the auth middleware's own error codes, so a business-level 401
  // (none exist today) could never be mistaken for an expired session.
  const tokenRejected = resp.status === 401 && ["INVALID_TOKEN", "MISSING_TOKEN"].includes(body?.error?.code);
  if (tokenRejected && token && !isAuthEndpoint) {
    if (isIdle()) {
      expireSession("idle");
      throw sessionExpiredError("idle");
    }
    const outcome = retried ? "rejected" : await refreshSession();
    if (outcome === "ok") return request<T>(path, options, true);
    if (outcome === "rejected") {
      expireSession("revoked");
      throw sessionExpiredError("revoked");
    }
  }

  if (!resp.ok) {
    const err = body?.error ?? {};
    throw new ApiError(resp.status, err.code ?? "UNKNOWN_ERROR", err.message ?? `request failed with status ${resp.status}`);
  }
  return body as T;
}

export const api = {
  get: <T>(path: string) => request<T>(path, { method: "GET" }),
  post: <T>(path: string, body?: unknown) => request<T>(path, { method: "POST", body: body ? JSON.stringify(body) : undefined }),
  patch: <T>(path: string, body?: unknown) => request<T>(path, { method: "PATCH", body: body ? JSON.stringify(body) : undefined }),
  put: <T>(path: string, body?: unknown) => request<T>(path, { method: "PUT", body: body ? JSON.stringify(body) : undefined }),
  delete: <T>(path: string) => request<T>(path, { method: "DELETE" }),
};

// Matches migrations/002_seed.sql — the same seed branch/terminal the
// Flutter app's main.dart hardcodes today. A real "pick a branch" flow
// needs a GET /branches endpoint that doesn't exist yet; this is the same
// interim simplification, not a new one introduced here.
export const SEED_BRANCH_ID = "22222222-2222-2222-2222-222222222222";
