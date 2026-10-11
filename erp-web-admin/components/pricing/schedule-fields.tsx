"use client";

import { Button } from "@/components/ui/button";

export const DAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

// The API returns timestamps as Postgres text ("2026-11-01 00:00:00+05:30",
// sometimes "+00" with no minutes) and takes RFC3339 back. A
// <input type="datetime-local"> wants "2026-11-01T00:00" in the browser's
// own zone. These convert both ways; "" means "not set".
export function toLocalInput(ts: string | null | undefined): string {
  if (!ts) return "";
  let iso = ts.trim().replace(" ", "T");
  if (/[+-]\d\d$/.test(iso)) iso += ":00";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;
}

export function fromLocalInput(local: string): string {
  return local ? new Date(local).toISOString() : "";
}

/** A short "when" summary for a list row: days · hours · date range, or "Always". */
export function describeWhen(x: {
  days_of_week?: number[] | null;
  time_start?: string | null;
  time_end?: string | null;
  starts_at?: string | null;
  ends_at?: string | null;
}): string {
  const parts: string[] = [];
  if (x.days_of_week?.length) parts.push(x.days_of_week.map((d) => DAYS[d]).join(", "));
  if (x.time_start) parts.push(`${x.time_start}–${x.time_end}`);
  const day = (ts: string | null | undefined) => {
    const v = toLocalInput(ts);
    return v ? new Date(v).toLocaleDateString("en-IN", { day: "numeric", month: "short", year: "numeric" }) : "…";
  };
  if (x.starts_at || x.ends_at) parts.push(`${day(x.starts_at)} to ${day(x.ends_at)}`);
  return parts.length ? parts.join(" · ") : "Always";
}

/** Toggle buttons for days of the week; none selected = every day. */
export function DaysOfWeekPicker({ value, onChange, label = "Days (none = every day)" }: { value: number[]; onChange: (days: number[]) => void; label?: string }) {
  return (
    <fieldset className="flex flex-col gap-2">
      <legend className="mb-2 text-sm font-medium">{label}</legend>
      <div className="flex flex-wrap gap-1.5">
        {DAYS.map((d, i) => {
          const on = value.includes(i);
          return (
            <Button
              key={d}
              type="button"
              size="sm"
              variant={on ? "default" : "outline"}
              aria-pressed={on}
              onClick={() => onChange(on ? value.filter((x) => x !== i) : [...value, i].sort())}
            >
              {d}
            </Button>
          );
        })}
      </div>
    </fieldset>
  );
}
