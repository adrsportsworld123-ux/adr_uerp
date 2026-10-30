"use client";

// Selling price suggestions (erp-core-go §3.83): a suggested selling price
// from the cost and MRP by the Merchant Admin's rules. Plan feature
// price_suggestion; switched on per merchant. A suggestion only pre-fills
// the price field — the user still saves (or changes) it.
import { useCallback, useEffect, useState } from "react";
import { api, ApiError } from "@/lib/api-client";
import type { PriceSuggestion, SuggestionSettings, SuggestionStatus } from "@/lib/types";
import { rupees } from "@/lib/format";
import { currencySymbol } from "@/lib/currency";
import { StatusChip } from "@/components/data/status-chip";
import { Button } from "@/components/ui/button";

export const SUGGESTION_STATUS: Record<SuggestionStatus, { label: string; tone: "ok" | "warn" | "bad" | "muted" }> = {
  ok: { label: "Suggested", tone: "ok" },
  at_mrp: { label: "At MRP", tone: "warn" },
  check: { label: "Check MRP", tone: "warn" },
  hold: { label: "Hold", tone: "bad" },
  no_cost: { label: "No cost", tone: "muted" },
  no_rule: { label: "No rule", tone: "muted" },
};

/** How a rule rounds, in the merchant's currency. */
export function roundingLabel(code: string): string {
  const c = currencySymbol();
  return (
    {
      none: "No rounding",
      up_1: `Up to ${c}1`,
      up_5: `Up to ${c}5`,
      up_10: `Up to ${c}10`,
      end_9: "Up to a price ending in 9",
      end_99: `Up to ${c}X.99`,
      charm: `${c}5 up to ${c}100, then ending in 9`,
    } as Record<string, string>
  )[code] ?? code;
}

export const ROUNDINGS = ["none", "up_1", "up_5", "up_10", "end_9", "end_99", "charm"] as const;

/** The merchant's suggestion settings; `locked` when the plan doesn't include them. */
export function useSuggestionSettings() {
  const [settings, setSettings] = useState<SuggestionSettings | null>(null);
  const [locked, setLocked] = useState(false);
  const reload = useCallback(() => {
    api
      .get<SuggestionSettings>("/api/v1/pricing/suggestion-settings")
      .then((s) => {
        setSettings(s);
        setLocked(false);
      })
      .catch((e) => {
        if (e instanceof ApiError && e.code === "FEATURE_NOT_IN_PLAN") setLocked(true);
        setSettings(null);
      });
  }, []);
  useEffect(reload, [reload]);
  return { settings, locked, reload, enabled: !!settings?.enabled };
}

export type SuggestInput = {
  key?: string;
  variant_id?: string;
  cost_price?: number;
  mrp?: number;
  category_id?: string;
  brand_id?: string;
  tax_slab_id?: string;
  price_includes_tax?: boolean;
};

export async function fetchSuggestions(items: SuggestInput[]): Promise<PriceSuggestion[]> {
  if (items.length === 0) return [];
  const d = await api.post<{ enabled: boolean; items: PriceSuggestion[] }>("/api/v1/pricing/suggest", { items });
  return d.enabled ? d.items : [];
}

/** One suggestion for figures being typed (debounced); null while off or unknown. */
export function useLiveSuggestion(input: SuggestInput | null, enabled: boolean) {
  const [result, setResult] = useState<PriceSuggestion | null>(null);
  const key = input ? JSON.stringify(input) : "";
  useEffect(() => {
    if (!enabled || !key) return;
    let live = true;
    const t = setTimeout(() => {
      fetchSuggestions([JSON.parse(key) as SuggestInput])
        .then((r) => live && setResult(r[0] ?? null))
        .catch(() => live && setResult(null));
    }, 300);
    return () => {
      live = false;
      clearTimeout(t);
    };
  }, [key, enabled]);
  return enabled && key ? result : null;
}

export function SuggestionChip({ s }: { s: PriceSuggestion }) {
  const st = SUGGESTION_STATUS[s.status];
  return <StatusChip status="" tone={st.tone} label={st.label} />;
}

/** "Suggested ₹449 · Balls rule · 19.8% margin", with a button to use it. */
export function SuggestionHint({ s, onUse, current }: { s: PriceSuggestion; onUse?: (price: number) => void; current?: string }) {
  return (
    <div className="flex flex-wrap items-center gap-2 rounded-md border border-dashed px-3 py-2 text-xs" data-testid="price-suggestion">
      <SuggestionChip s={s} />
      {s.suggested_price != null ? (
        <span>
          Suggested <span className="font-semibold tabular">{rupees(s.suggested_price)}</span>
          {s.rule && <span className="text-muted-foreground"> · {s.rule}</span>}
          {s.margin_pct != null && <span className="text-muted-foreground"> · {s.margin_pct}% margin</span>}
        </span>
      ) : (
        <span className="text-muted-foreground">No suggestion</span>
      )}
      {s.note && <span className="w-full text-muted-foreground">{s.note}</span>}
      {onUse && s.suggested_price != null && current !== String(s.suggested_price) && (
        <Button type="button" size="sm" variant="outline" className="h-6 px-2 text-xs" onClick={() => onUse(s.suggested_price!)}>
          Use {rupees(s.suggested_price)}
        </Button>
      )}
    </div>
  );
}
