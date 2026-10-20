"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { Dialog } from "@base-ui/react/dialog";
import { CornerDownLeft, Search } from "lucide-react";
import { EXTRA_DESTINATIONS, NAV_GROUPS } from "@/lib/nav";
import { t } from "@/lib/i18n";

type Entry = { href: string; label: string; group: string };

const ENTRIES: Entry[] = [
  ...NAV_GROUPS.flatMap((g) => g.items.map((i) => ({ href: i.href, label: i.label, group: g.label }))),
  ...EXTRA_DESTINATIONS,
];

// Ctrl/⌘+K jump-to-screen palette. Screens only for now; record search
// (an invoice number, a product) needs a cross-entity search endpoint that
// doesn't exist yet, so it isn't faked here.
export function CommandPalette({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const router = useRouter();
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const listRef = useRef<HTMLUListElement>(null);

  const results = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return ENTRIES;
    // The English name finds a screen in any language, as does the translated one.
    return ENTRIES.filter((e) => [e.label, e.group, t(e.label), t(e.group)].some((s) => s.toLowerCase().includes(q)));
  }, [query]);

  useEffect(() => {
    listRef.current?.querySelector(`[data-index="${active}"]`)?.scrollIntoView({ block: "nearest" });
  }, [active]);

  function go(entry: Entry | undefined) {
    if (!entry) return;
    onOpenChange(false);
    router.push(entry.href);
  }

  function changeOpen(next: boolean) {
    if (next) {
      setQuery("");
      setActive(0);
    }
    onOpenChange(next);
  }

  return (
    <Dialog.Root open={open} onOpenChange={changeOpen}>
      <Dialog.Portal>
        <Dialog.Backdrop className="fixed inset-0 z-50 bg-black/30 backdrop-blur-[1px]" />
        <Dialog.Popup className="fixed left-1/2 top-[12vh] z-50 w-[min(560px,calc(100vw-32px))] -translate-x-1/2 overflow-hidden rounded-xl border bg-popover text-popover-foreground shadow-2xl outline-none">
          <Dialog.Title className="sr-only">{t("Command palette")}</Dialog.Title>
          <div className="flex items-center gap-2 border-b px-4">
            <Search className="size-4 text-muted-foreground" aria-hidden />
            <input
              autoFocus
              value={query}
              onChange={(e) => {
                setQuery(e.target.value);
                setActive(0);
              }}
              onKeyDown={(e) => {
                if (e.key === "ArrowDown") {
                  e.preventDefault();
                  setActive((a) => Math.min(a + 1, results.length - 1));
                } else if (e.key === "ArrowUp") {
                  e.preventDefault();
                  setActive((a) => Math.max(a - 1, 0));
                } else if (e.key === "Enter") {
                  e.preventDefault();
                  go(results[active]);
                }
              }}
              placeholder={t("Jump to a screen…")}
              aria-label={t("Jump to a screen")}
              className="h-12 flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
            />
            <kbd className="rounded border bg-muted px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground">Esc</kbd>
          </div>
          <ul ref={listRef} role="listbox" aria-label={t("Screens")} className="max-h-[50vh] overflow-y-auto p-2">
            {results.length === 0 && (
              <li className="px-3 py-8 text-center text-sm text-muted-foreground">{t("No screen matches “{0}”.", [query])}</li>
            )}
            {results.map((e, i) => (
              <li
                key={e.href}
                data-index={i}
                role="option"
                aria-selected={i === active}
                onMouseEnter={() => setActive(i)}
                onClick={() => go(e)}
                className={`flex cursor-pointer items-center justify-between gap-3 rounded-md px-3 py-2 text-sm ${
                  i === active ? "bg-accent text-accent-foreground" : ""
                }`}
              >
                <span>{t(e.label)}</span>
                <span className="flex items-center gap-2 text-xs text-muted-foreground">
                  {t(e.group)}
                  {i === active && <CornerDownLeft className="size-3.5 rtl:-scale-x-100" aria-hidden />}
                </span>
              </li>
            ))}
          </ul>
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
