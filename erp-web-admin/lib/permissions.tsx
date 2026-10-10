"use client";

import { createContext, ReactNode, useCallback, useContext, useEffect, useState } from "react";
import { api } from "@/lib/api-client";
import { useAuth } from "@/lib/auth";

// The caller's permission codes from GET /me/permissions (Phase 11), so a
// screen can disable an action and say why BEFORE the user tries it,
// instead of only toasting a 403 afterwards. A hint for the interface:
// the backend still enforces every gate on its own, so a stale or missing
// list can make a button look available, never make an action succeed.
type PermissionsState = {
  loaded: boolean;
  permissions: string[];
  can: (code: string) => boolean;
};

const PermissionsContext = createContext<PermissionsState>({ loaded: false, permissions: [], can: () => true });

export function PermissionsProvider({ children }: { children: ReactNode }) {
  const { userId } = useAuth();
  const [state, setState] = useState<{ loaded: boolean; permissions: string[] }>({ loaded: false, permissions: [] });

  useEffect(() => {
    if (!userId) return;
    let cancelled = false;
    api
      .get<{ permissions: string[] }>("/api/v1/me/permissions")
      .then((d) => !cancelled && setState({ loaded: true, permissions: d.permissions }))
      // An older backend without this endpoint: fall back to "unknown",
      // which leaves buttons enabled and the server's 403 as the answer —
      // exactly how every screen behaved before.
      .catch(() => !cancelled && setState({ loaded: false, permissions: [] }));
    return () => {
      cancelled = true;
    };
  }, [userId]);

  const can = useCallback(
    (code: string) => !state.loaded || state.permissions.includes(code),
    [state],
  );

  return <PermissionsContext.Provider value={{ ...state, can }}>{children}</PermissionsContext.Provider>;
}

export function usePermissions() {
  return useContext(PermissionsContext);
}

// Tooltip text for an action the user can't take.
export function missingPermissionHint(code: string) {
  return `Needs the ${code} permission. Ask a Merchant Admin to grant it under Roles & Permissions.`;
}
