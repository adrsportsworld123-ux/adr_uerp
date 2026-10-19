"use client";

import { ReactNode } from "react";
import { useRequireAuth } from "@/lib/auth";
import { AppShell } from "@/components/shell/app-shell";
import { PermissionsProvider } from "@/lib/permissions";
import { CurrencyProvider } from "@/lib/currency";

export default function DashboardLayout({ children }: { children: ReactNode }) {
  const { ready, userId } = useRequireAuth();

  if (!ready || !userId) {
    return <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">Loading…</div>;
  }

  return (
    <PermissionsProvider>
      <CurrencyProvider>
        <AppShell>{children}</AppShell>
      </CurrencyProvider>
    </PermissionsProvider>
  );
}
