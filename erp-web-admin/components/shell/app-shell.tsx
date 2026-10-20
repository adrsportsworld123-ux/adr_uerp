"use client";

import { ReactNode, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useTheme } from "next-themes";
import { ChevronDown, ChevronRight, Lock, LogOut, Menu, Monitor, Moon, Search, Sun, X } from "lucide-react";
import { NAV_GROUPS, findActive } from "@/lib/nav";
import { t } from "@/lib/i18n";
import { useAuth } from "@/lib/auth";
import { Button } from "@/components/ui/button";
import { usePermissions } from "@/lib/permissions";
import { CommandPalette } from "./command-palette";
import { AttendanceButton } from "./attendance-button";
import { PlanLocked, SubscriptionBanner } from "./plan";

const COLLAPSED_KEY = "nav.collapsed";

function readCollapsed(): string[] {
  try {
    return JSON.parse(window.localStorage.getItem(COLLAPSED_KEY) ?? "[]");
  } catch {
    return [];
  }
}

function Sidebar({ onNavigate, onOpenPalette }: { onNavigate?: () => void; onOpenPalette: () => void }) {
  const pathname = usePathname();
  const active = findActive(pathname);
  const { has } = usePermissions();
  // Every group starts expanded: the sidebar shows exactly the same links
  // the old flat list did until someone chooses to collapse a group.
  const [collapsed, setCollapsed] = useState<string[]>([]);
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- localStorage only exists after mount; reading it during render would break hydration
    setCollapsed(readCollapsed());
  }, []);

  function toggle(id: string) {
    setCollapsed((prev) => {
      const next = prev.includes(id) ? prev.filter((g) => g !== id) : [...prev, id];
      try {
        window.localStorage.setItem(COLLAPSED_KEY, JSON.stringify(next));
      } catch {
        // per-browser convenience only
      }
      return next;
    });
  }

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center gap-2.5 px-3 pb-3 pt-1">
        <div className="grid size-8 place-items-center rounded-lg bg-primary text-[11px] font-bold tracking-wide text-primary-foreground">
          ADR
        </div>
        <div className="leading-tight">
          <p className="text-sm font-semibold text-foreground">{t("ERP Admin")}</p>
          <p className="text-xs text-muted-foreground">{t("Back office")}</p>
        </div>
      </div>
      <button
        type="button"
        onClick={onOpenPalette}
        className="mx-1 mb-3 flex h-9 items-center gap-2 rounded-lg border bg-background px-2.5 text-sm text-muted-foreground hover:border-primary/40"
      >
        <Search className="size-4" aria-hidden />
        <span className="flex-1 text-start">{t("Jump to…")}</span>
        <kbd className="rounded border bg-muted px-1.5 font-mono text-[11px]">Ctrl K</kbd>
      </button>
      <nav aria-label="Main" className="-mx-1 flex-1 overflow-y-auto px-1 pb-4">
        {NAV_GROUPS.map((group) => {
          const isCollapsed = collapsed.includes(group.id) && active?.group.id !== group.id;
          const Icon = group.icon;
          return (
            <div key={group.id} className="mb-1">
              <button
                type="button"
                onClick={() => toggle(group.id)}
                aria-expanded={!isCollapsed}
                className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground hover:text-foreground"
              >
                <Icon className="size-3.5" aria-hidden />
                <span className="flex-1 text-start">{t(group.label)}</span>
                {isCollapsed ? <ChevronRight className="size-3.5 rtl:rotate-180" aria-hidden /> : <ChevronDown className="size-3.5" aria-hidden />}
              </button>
              {!isCollapsed && (
                <div className="flex flex-col gap-px pb-1">
                  {group.items.map((item) => {
                    const isActive = active?.item.href === item.href;
                    const locked = item.feature != null && !has(item.feature);
                    return (
                      <Link
                        key={item.href}
                        href={item.href}
                        onClick={onNavigate}
                        aria-current={isActive ? "page" : undefined}
                        className={`flex items-center gap-1.5 rounded-md py-1.5 ps-7 pe-2 text-sm transition-colors ${
                          isActive
                            ? "bg-sidebar-accent font-medium text-sidebar-accent-foreground"
                            : "text-sidebar-foreground hover:bg-muted hover:text-foreground"
                        }`}
                      >
                        <span className="flex-1">{t(item.label)}</span>
                        {locked && (
                          <>
                            <Lock className="size-3 text-muted-foreground" aria-hidden />
                            <span className="sr-only">{t("(not in your plan)")}</span>
                          </>
                        )}
                      </Link>
                    );
                  })}
                </div>
              )}
            </div>
          );
        })}
      </nav>
    </div>
  );
}

function ThemeSwitch() {
  const { theme, setTheme } = useTheme();
  const options = [
    { value: "light", label: t("Light"), Icon: Sun },
    { value: "dark", label: t("Dark"), Icon: Moon },
    { value: "system", label: t("System"), Icon: Monitor },
  ];
  return (
    <div role="group" aria-label="Theme" className="flex rounded-lg bg-muted p-0.5">
      {options.map(({ value, label, Icon }) => (
        <button
          key={value}
          type="button"
          title={label}
          aria-label={t("{0} theme", [label])}
          aria-pressed={theme === value}
          onClick={() => setTheme(value)}
          className={`grid size-7 place-items-center rounded-md ${
            theme === value ? "bg-card text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground"
          }`}
        >
          <Icon className="size-3.5" aria-hidden />
        </button>
      ))}
    </div>
  );
}

export function AppShell({ children }: { children: ReactNode }) {
  const pathname = usePathname();
  const { roles, logout } = useAuth();
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const active = findActive(pathname);
  const { has } = usePermissions();
  const lockedFeature = active?.item.feature != null && !has(active.item.feature) ? active.item.feature : null;

  // After a client-side navigation that leaves nothing focused (the
  // focused element went with the old page), put focus back at the top, so
  // the skip link is the next Tab stop on every page, as on a full load.
  // Otherwise the browser resumes from wherever the old page's focus was
  // (a chart, a dialog's portal), possibly past the end of the new page.
  const top = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const a = document.activeElement;
    if (!a || a === document.body || !a.isConnected) top.current?.focus({ preventScroll: true });
  }, [pathname]);

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setPaletteOpen((o) => !o);
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const roleLabel = roles.length > 0 ? roles.join(", ") : t("Signed in");

  return (
    <div ref={top} tabIndex={-1} className="flex min-h-0 flex-1 outline-none">
      {/* First tab stop on every page: jump past ~75 sidebar links. */}
      <a
        href="#main-content"
        className="sr-only z-50 rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground focus:not-sr-only focus:fixed focus:start-3 focus:top-3"
      >
        {t("Skip to main content")}
      </a>
      <aside className="sticky top-0 hidden h-screen w-64 shrink-0 border-e bg-sidebar p-3 lg:block print:hidden">
        <Sidebar onOpenPalette={() => setPaletteOpen(true)} />
      </aside>

      {drawerOpen && (
        <div className="fixed inset-0 z-40 lg:hidden">
          <div className="absolute inset-0 bg-black/30" onClick={() => setDrawerOpen(false)} aria-hidden />
          <aside className="absolute inset-y-0 start-0 w-72 max-w-[85vw] border-e bg-sidebar p-3 shadow-xl">
            <button
              type="button"
              onClick={() => setDrawerOpen(false)}
              aria-label={t("Close menu")}
              className="absolute end-2 top-2 grid size-8 place-items-center rounded-md text-muted-foreground hover:bg-muted"
            >
              <X className="size-4" />
            </button>
            <Sidebar onNavigate={() => setDrawerOpen(false)} onOpenPalette={() => setPaletteOpen(true)} />
          </aside>
        </div>
      )}

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="sticky top-0 z-30 flex h-14 items-center gap-3 border-b bg-card/95 px-4 backdrop-blur supports-[backdrop-filter]:bg-card/80 md:px-6 print:hidden">
          <button
            type="button"
            onClick={() => setDrawerOpen(true)}
            aria-label={t("Open menu")}
            className="grid size-8 place-items-center rounded-md text-muted-foreground hover:bg-muted lg:hidden"
          >
            <Menu className="size-4" />
          </button>
          <nav aria-label="Breadcrumb" className="min-w-0 flex-1 truncate text-sm text-muted-foreground">
            {active ? (
              <>
                <span>{t(active.group.label)}</span>
                <span className="px-1.5" aria-hidden>
                  /
                </span>
                <span className="font-medium text-foreground">{t(active.item.label)}</span>
              </>
            ) : null}
          </nav>
          <AttendanceButton />
          <ThemeSwitch />
          <span className="hidden max-w-48 truncate text-xs text-muted-foreground md:inline" title={roleLabel}>
            {roleLabel}
          </span>
          <Button variant="outline" size="sm" onClick={logout}>
            <LogOut className="rtl:rotate-180" aria-hidden />
            {t("Log out")}
          </Button>
        </header>
        <SubscriptionBanner />
        <main id="main-content" tabIndex={-1} className="min-w-0 flex-1 p-4 outline-none md:p-6">
          {lockedFeature && active ? <PlanLocked feature={lockedFeature} title={t(active.item.label)} /> : children}
        </main>
      </div>

      <CommandPalette open={paletteOpen} onOpenChange={setPaletteOpen} />
    </div>
  );
}
