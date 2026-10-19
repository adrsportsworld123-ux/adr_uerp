// Where the API is. NEXT_PUBLIC_API_BASE_URL in production; in development
// it is usually http://localhost:8080 — which, opened from another device
// on the LAN (a tablet at http://10.x.x.x:3000), would point at that device
// itself. So when the configured host is localhost but the page was loaded
// from another host, the API is reached on the page's host instead (same
// port and scheme as configured). A real, non-localhost URL is used as is.
const CONFIGURED = process.env.NEXT_PUBLIC_API_BASE_URL ?? "http://localhost:8080";
const LOCAL = new Set(["localhost", "127.0.0.1", "::1", "[::1]"]);

export function apiBase(): string {
  if (typeof window === "undefined") return CONFIGURED;
  try {
    const u = new URL(CONFIGURED);
    const page = window.location.hostname;
    if (LOCAL.has(u.hostname) && !LOCAL.has(page)) {
      u.hostname = page;
      return u.toString().replace(/\/$/, "");
    }
  } catch {
    // not a URL we can reason about: use it as given
  }
  return CONFIGURED;
}
