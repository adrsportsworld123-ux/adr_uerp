"use client";

// The tab strip the farm's Feed and Health pages use (the Approvals
// page's pattern): one view at a time, kept in ?tab= so a link opens it.
import { useRouter, useSearchParams } from "next/navigation";

export function useTab<T extends string>(tabs: readonly T[], path: string): [T, (t: T) => void] {
  const router = useRouter();
  const params = useSearchParams();
  const raw = params.get("tab") as T | null;
  const tab = raw && tabs.includes(raw) ? raw : tabs[0];
  return [tab, (t: T) => router.replace(`${path}?tab=${t}`)];
}

export function TabStrip<T extends string>({ tabs, labels, value, onChange, label }: { tabs: readonly T[]; labels: Record<T, string>; value: T; onChange: (t: T) => void; label: string }) {
  return (
    <div role="tablist" aria-label={label} className="flex w-fit gap-1 rounded-lg bg-muted p-1">
      {tabs.map((t) => (
        <button
          key={t}
          role="tab"
          aria-selected={value === t}
          onClick={() => onChange(t)}
          className={`rounded-md px-3 py-1.5 text-sm ${value === t ? "bg-card font-medium shadow-sm" : "text-muted-foreground hover:text-foreground"}`}
        >
          {labels[t]}
        </button>
      ))}
    </div>
  );
}
