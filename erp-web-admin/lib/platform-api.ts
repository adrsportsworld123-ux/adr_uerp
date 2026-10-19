// The SaaS console's client (Phase 16, erp-core-go internal/subscriptions
// platform.go). Platform staff are not merchant users: their session is a
// separate token, kept under its own key, and only ever sent to
// /api/v1/platform/*. A merchant session in the same browser is untouched.
import { ApiError } from "@/lib/api-client";
import type { Tone } from "@/components/data/status-chip";

import { apiBase } from "@/lib/api-base";
const KEY = "platform.session";

export type PlatformSession = { token: string; expires_at: string; name: string };

export function getPlatformSession(): PlatformSession | null {
  try {
    const raw = window.localStorage.getItem(KEY);
    if (!raw) return null;
    const s = JSON.parse(raw) as PlatformSession;
    return new Date(s.expires_at).getTime() > Date.now() ? s : null;
  } catch {
    return null;
  }
}

export function clearPlatformSession() {
  try {
    window.localStorage.removeItem(KEY);
  } catch {
    // nothing to clear
  }
}

export async function platformLogin(email: string, password: string): Promise<PlatformSession> {
  const res = await fetch(`${apiBase()}/api/v1/platform/auth/login`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email, password }),
  });
  const body = await res.json().catch(() => null);
  if (!res.ok) throw new ApiError(res.status, body?.error?.code ?? "ERROR", body?.error?.message ?? "Sign-in failed");
  const s = { token: body.access_token, expires_at: body.expires_at, name: body.staff?.name ?? "" };
  window.localStorage.setItem(KEY, JSON.stringify(s));
  return s;
}

export const PLATFORM_SESSION_ENDED = "platform:session-ended";

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const s = getPlatformSession();
  const res = await fetch(`${apiBase()}/api/v1${path}`, {
    method,
    headers: { "Content-Type": "application/json", ...(s ? { Authorization: `Bearer ${s.token}` } : {}) },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await res.json().catch(() => null);
  if (res.status === 401) {
    clearPlatformSession();
    window.dispatchEvent(new Event(PLATFORM_SESSION_ENDED));
  }
  if (!res.ok) {
    throw new ApiError(res.status, data?.error?.code ?? "ERROR", data?.error?.message ?? "Request failed", data?.error?.details);
  }
  return data as T;
}

export const platformApi = {
  get: <T>(path: string) => request<T>("GET", path),
  post: <T>(path: string, body: unknown) => request<T>("POST", path, body),
  patch: <T>(path: string, body: unknown) => request<T>("PATCH", path, body),
};

// Shapes shared by the console pages.
export type Usage = Record<string, { used: number; limit: number | null }>;
export type MerchantRow = {
  merchant: { merchant_id: string; code: string; legal_name: string; trade_name: string | null; merchant_status: string; created_at: string };
  plan: { code: string; name: string };
  status: string;
  read_only: boolean;
  current_period_end: string | null;
  trial_ends_at: string | null;
  grace_ends_at: string | null;
  usage: Usage;
  invoices_due: number;
  amount_due: string;
  last_sale_at: string | null;
};
export type Plan = {
  code: string;
  name: string;
  price_per_pos_month: string;
  limits: Record<string, number | null>;
  features: string[];
  retention_days: number | null;
  trial_days: number;
  grace_days: number;
  sort_order: number;
  active: boolean;
};
export type Invoice = {
  invoice_id: string;
  invoice_number: string;
  kind: string;
  plan_code: string;
  period_start: string;
  period_end: string;
  pos_count: number;
  unit_price: string;
  amount: string;
  status: "due" | "paid" | "void";
  paid_at: string | null;
  provider: string | null;
  provider_ref: string | null;
};

export const SUB_STATUS: Record<string, { label: string; tone: Tone }> = {
  active: { label: "Active", tone: "ok" },
  trial: { label: "Trial", tone: "info" },
  grace: { label: "Payment due", tone: "warn" },
  expired: { label: "Expired", tone: "bad" },
  cancelled: { label: "Cancelled", tone: "muted" },
};
