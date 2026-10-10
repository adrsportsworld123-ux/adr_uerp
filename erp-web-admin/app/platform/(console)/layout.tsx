"use client";

// The SaaS console shell (Phase 16): merchants, plans and the platform
// audit log, for platform staff. Its own session (lib/platform-api.ts).
import { ReactNode, useEffect, useState } from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { LogOut } from "lucide-react";
import { clearPlatformSession, getPlatformSession, PLATFORM_SESSION_ENDED, PlatformSession } from "@/lib/platform-api";
import { Button } from "@/components/ui/button";

const LINKS = [
  { href: "/platform", label: "Merchants" },
  { href: "/platform/plans", label: "Plans" },
  { href: "/platform/audit", label: "Audit log" },
];

export default function ConsoleLayout({ children }: { children: ReactNode }) {
  const router = useRouter();
  const pathname = usePathname();
  const [session, setSession] = useState<PlatformSession | null>(null);
  const [ready, setReady] = useState(false);

  useEffect(() => {
    const s = getPlatformSession();
    if (!s) {
      router.replace("/platform/login");
      return;
    }
    // eslint-disable-next-line react-hooks/set-state-in-effect -- the session lives in localStorage, readable only after mount
    setSession(s);
    setReady(true);
    const ended = () => router.replace("/platform/login");
    window.addEventListener(PLATFORM_SESSION_ENDED, ended);
    return () => window.removeEventListener(PLATFORM_SESSION_ENDED, ended);
  }, [router]);

  if (!ready) return <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">Loading…</div>;

  const isActive = (href: string) => (href === "/platform" ? pathname === "/platform" || pathname.startsWith("/platform/merchants") : pathname.startsWith(href));

  return (
    <div className="flex flex-1 flex-col">
      <header className="sticky top-0 z-30 flex h-14 flex-wrap items-center gap-4 border-b bg-card px-4 md:px-6">
        <span className="text-sm font-semibold">ADR ERP · SaaS console</span>
        <nav aria-label="Console" className="flex flex-1 gap-1">
          {LINKS.map((l) => (
            <Link
              key={l.href}
              href={l.href}
              aria-current={isActive(l.href) ? "page" : undefined}
              className={`rounded-md px-2.5 py-1.5 text-sm ${isActive(l.href) ? "bg-muted font-medium" : "text-muted-foreground hover:text-foreground"}`}
            >
              {l.label}
            </Link>
          ))}
        </nav>
        <span className="hidden text-xs text-muted-foreground sm:inline">{session?.name}</span>
        <Button
          variant="outline"
          size="sm"
          onClick={() => {
            clearPlatformSession();
            router.replace("/platform/login");
          }}
        >
          <LogOut aria-hidden /> Sign out
        </Button>
      </header>
      <main id="main-content" className="min-w-0 flex-1 p-4 md:p-6">
        {children}
      </main>
    </div>
  );
}
