"use client";

import { useMemo, useState } from "react";
import { Popover } from "@base-ui/react/popover";
import { Check, ChevronDown, Search, X } from "lucide-react";

export type SearchOption = { value: string; label: string; hint?: string };

// Shown at most; typing narrows the rest. A category tree or brand list
// can run to hundreds, and a long unfiltered list is what this replaces.
const MAX_SHOWN = 60;

// A dropdown you can type into (design system §04 "search and select"):
// the same value/options contract as a plain select, so a screen swaps it
// in without changing its state. Every word typed must appear in the label
// or hint, in any order ("bat eng" finds "Cricket › Bats › English
// Willow"). For products and variants use ProductPicker instead, which
// searches the server rather than a list already in memory.
export function SearchSelect({
  value,
  onChange,
  options,
  placeholder = "Select…",
  searchPlaceholder = "Type to search",
  noneLabel,
  id,
  ariaLabel,
  disabled,
  className = "",
}: {
  value: string;
  onChange: (value: string) => void;
  options: SearchOption[];
  placeholder?: string;
  searchPlaceholder?: string;
  /** Offer an explicit "none" choice (clears the value) with this label. */
  noneLabel?: string;
  id?: string;
  ariaLabel?: string;
  disabled?: boolean;
  className?: string;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);

  const selected = options.find((o) => o.value === value);
  const matches = useMemo(() => {
    const words = query.toLowerCase().split(/\s+/).filter(Boolean);
    const all = words.length
      ? options.filter((o) => {
          const hay = `${o.label} ${o.hint ?? ""}`.toLowerCase();
          return words.every((w) => hay.includes(w));
        })
      : options;
    const list: SearchOption[] = noneLabel && !words.length ? [{ value: "", label: noneLabel }, ...all] : all;
    return { shown: list.slice(0, MAX_SHOWN), more: Math.max(list.length - MAX_SHOWN, 0) };
  }, [options, query, noneLabel]);

  function choose(o: SearchOption | undefined) {
    if (!o) return;
    onChange(o.value);
    setOpen(false);
    setQuery("");
  }

  return (
    <Popover.Root
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        if (o) setActive(0);
      }}
    >
      <Popover.Trigger
        id={id}
        aria-label={ariaLabel}
        disabled={disabled}
        className={`flex h-9 w-full min-w-0 items-center justify-between gap-2 rounded-lg border bg-card px-3 text-left text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/30 disabled:opacity-50 ${className}`}
      >
        <span className={`truncate ${selected ? "" : "text-muted-foreground"}`}>{selected ? selected.label : value ? "…" : placeholder}</span>
        <ChevronDown className="size-4 shrink-0 text-muted-foreground" aria-hidden />
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Positioner sideOffset={4} align="start" className="z-50">
          <Popover.Popup className="w-[min(max(var(--anchor-width),320px),calc(100vw-32px))] overflow-hidden rounded-lg border bg-popover text-popover-foreground shadow-xl outline-none">
            <div className="flex items-center gap-2 border-b px-3">
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
                    setActive((a) => Math.min(a + 1, matches.shown.length - 1));
                  } else if (e.key === "ArrowUp") {
                    e.preventDefault();
                    setActive((a) => Math.max(a - 1, 0));
                  } else if (e.key === "Enter") {
                    e.preventDefault();
                    choose(matches.shown[active]);
                  }
                }}
                placeholder={searchPlaceholder}
                aria-label={searchPlaceholder}
                className="h-10 flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
              />
              {query && (
                <button type="button" aria-label="Clear search" onClick={() => setQuery("")} className="text-muted-foreground hover:text-foreground">
                  <X className="size-4" />
                </button>
              )}
            </div>
            <ul role="listbox" aria-label={ariaLabel ?? placeholder} className="max-h-72 overflow-y-auto p-1">
              {matches.shown.length === 0 && (
                <li className="px-3 py-6 text-center text-sm text-muted-foreground">{query ? `Nothing matches “${query}”.` : "Nothing to choose yet."}</li>
              )}
              {matches.shown.map((o, i) => (
                <li
                  key={o.value || "__none"}
                  role="option"
                  aria-selected={o.value === value}
                  onMouseEnter={() => setActive(i)}
                  onClick={() => choose(o)}
                  className={`flex cursor-pointer items-center justify-between gap-3 rounded-md px-2.5 py-2 text-sm ${i === active ? "bg-accent text-accent-foreground" : ""} ${o.value === "" ? "text-muted-foreground" : ""}`}
                >
                  <span className="min-w-0">
                    <span className="block truncate">{o.label}</span>
                    {o.hint && <span className="block truncate text-xs text-muted-foreground">{o.hint}</span>}
                  </span>
                  {o.value === value && <Check className="size-3.5 shrink-0 text-primary" aria-hidden />}
                </li>
              ))}
              {matches.more > 0 && <li className="px-3 py-2 text-center text-xs text-muted-foreground">{matches.more} more — keep typing to narrow</li>}
            </ul>
          </Popover.Popup>
        </Popover.Positioner>
      </Popover.Portal>
    </Popover.Root>
  );
}

/** A category row as an option, labelled by its full path ("/Cricket/Bats" → "Cricket › Bats") so same-named sub-categories stay distinct. */
export function categoryOption(c: { category_id: string; name: string; path?: string }): SearchOption {
  const label = c.path ? c.path.replace(/^\//, "").replace(/\//g, " › ") : c.name;
  return { value: c.category_id, label: label || c.name };
}
