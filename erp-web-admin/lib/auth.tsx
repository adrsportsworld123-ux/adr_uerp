"use client";

import { createContext, useContext, useEffect, useState, ReactNode } from "react";
import { useRouter } from "next/navigation";
import { api, setToken, ApiError } from "./api-client";

interface LoginResponse {
  access_token: string;
  refresh_token: string;
  expires_in: number;
  user_id: string;
  roles: string[];
}

interface AuthState {
  userId: string | null;
  roles: string[];
  ready: boolean; // false until the initial localStorage check completes — avoids a login-page flash on refresh
  login: (merchantCode: string, email: string, password: string) => Promise<string | null>; // returns an error message, or null on success
  logout: () => void;
}

const AuthContext = createContext<AuthState | null>(null);

// Session is memory + localStorage only (no server-side cookie/session) —
// same Bearer-token model the Flutter client uses, deliberately, so this
// admin app and the POS app are consistent about how they authenticate
// against the same backend rather than inventing a second auth pattern.
interface SessionState {
  userId: string | null;
  roles: string[];
  ready: boolean;
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [session, setSession] = useState<SessionState>({ userId: null, roles: [], ready: false });
  const { userId, roles, ready } = session;

  // Deliberately a useEffect, not a lazy useState initializer: this app is
  // server-rendered first (no `window`), so reading localStorage during
  // render would either crash on the server or, if guarded, make the
  // client's first render diverge from the server's — a hydration
  // mismatch. Restoring the session AFTER hydration, in an effect, is the
  // standard fix for exactly this "client-only persisted state" case, even
  // though it's a single setState call some lint configs still flag.
  useEffect(() => {
    const storedUserId = window.localStorage.getItem("user_id");
    const storedToken = window.localStorage.getItem("access_token");
    const storedRoles = window.localStorage.getItem("roles");
    // Reading localStorage (an external system) once after mount and
    // syncing it into React state is exactly this rule's own documented
    // exception; there's no lazy-useState alternative here without a
    // hydration mismatch (see the comment above this effect).
    if (storedUserId && storedToken) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setSession({ userId: storedUserId, roles: storedRoles ? JSON.parse(storedRoles) : [], ready: true });
    } else {
      setSession((s) => ({ ...s, ready: true }));
    }
  }, []);

  async function login(merchantCode: string, email: string, password: string) {
    try {
      const result = await api.post<LoginResponse>("/api/v1/auth/login", {
        merchant_code: merchantCode,
        email,
        password,
      });
      setToken(result.access_token);
      window.localStorage.setItem("user_id", result.user_id);
      window.localStorage.setItem("roles", JSON.stringify(result.roles));
      window.localStorage.setItem("refresh_token", result.refresh_token);
      setSession({ userId: result.user_id, roles: result.roles, ready: true });
      return null;
    } catch (e) {
      return e instanceof ApiError ? e.message : "Could not reach the server";
    }
  }

  function logout() {
    setToken(null);
    window.localStorage.removeItem("user_id");
    window.localStorage.removeItem("roles");
    window.localStorage.removeItem("refresh_token");
    setSession({ userId: null, roles: [], ready: true });
  }

  return <AuthContext.Provider value={{ userId, roles, ready, login, logout }}>{children}</AuthContext.Provider>;
}

export function useAuth() {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used inside AuthProvider");
  return ctx;
}

// Client-side route guard for the (dashboard) layout — redirects to /login
// if there's no session once the initial localStorage check has finished.
// A middleware-based guard would need the token in a cookie, not
// localStorage; kept consistent with the Bearer-token model above instead.
export function useRequireAuth() {
  const { userId, ready } = useAuth();
  const router = useRouter();
  useEffect(() => {
    if (ready && !userId) router.replace("/login");
  }, [ready, userId, router]);
  return { userId, ready };
}
