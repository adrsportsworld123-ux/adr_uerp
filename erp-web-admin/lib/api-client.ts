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

function getToken(): string | null {
  if (typeof window === "undefined") return null;
  return window.localStorage.getItem("access_token");
}

export function setToken(token: string | null) {
  if (typeof window === "undefined") return;
  if (token) window.localStorage.setItem("access_token", token);
  else window.localStorage.removeItem("access_token");
}

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
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
