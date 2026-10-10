"use client";

import { useCallback, useMemo } from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";

// List state lives in the URL (?q=&status=&sort=&dir=&page=&limit=&<filter>=),
// so a filtered view survives a reload and can be bookmarked or shared,
// and Back steps through it. Defaults are left out of the URL to keep it short.
export type ListState = {
  q: string;
  status: string;
  sort: string;
  dir: "asc" | "desc";
  page: number;
  limit: number;
  filters: Record<string, string>;
};

export const PAGE_SIZES = [10, 25, 50, 100];

export function useListState(defaults: { sort: string; dir?: "asc" | "desc"; status?: string; limit?: number; filterKeys?: string[] }) {
  const router = useRouter();
  const pathname = usePathname();
  const params = useSearchParams();
  const filterKeys = useMemo(() => defaults.filterKeys ?? [], [defaults.filterKeys]);

  const state: ListState = useMemo(() => {
    const limit = Number(params.get("limit"));
    const page = Number(params.get("page"));
    const filters: Record<string, string> = {};
    for (const k of filterKeys) filters[k] = params.get(k) ?? "";
    return {
      q: params.get("q") ?? "",
      status: params.get("status") ?? defaults.status ?? "",
      sort: params.get("sort") ?? defaults.sort,
      dir: params.get("dir") === "desc" ? "desc" : params.get("dir") === "asc" ? "asc" : (defaults.dir ?? "asc"),
      page: page > 0 ? page : 1,
      limit: PAGE_SIZES.includes(limit) ? limit : (defaults.limit ?? 25),
      filters,
    };
  }, [params, defaults.sort, defaults.dir, defaults.status, defaults.limit, filterKeys]);

  const update = useCallback(
    (patch: Partial<Omit<ListState, "filters">> & { filters?: Record<string, string> }) => {
      const next = new URLSearchParams(params.toString());
      const set = (key: string, value: string, fallback: string) => {
        if (value === fallback || value === "") next.delete(key);
        else next.set(key, value);
      };
      if (patch.q !== undefined) set("q", patch.q, "");
      if (patch.status !== undefined) set("status", patch.status, defaults.status ?? "");
      if (patch.sort !== undefined) set("sort", patch.sort, defaults.sort);
      if (patch.dir !== undefined) set("dir", patch.dir, defaults.dir ?? "asc");
      if (patch.limit !== undefined) set("limit", String(patch.limit), String(defaults.limit ?? 25));
      for (const [k, v] of Object.entries(patch.filters ?? {})) set(k, v, "");
      // Any change other than paging itself goes back to page 1.
      const page = patch.page ?? 1;
      set("page", String(page), "1");
      const qs = next.toString();
      router.replace(qs ? `${pathname}?${qs}` : pathname, { scroll: false });
    },
    [params, pathname, router, defaults.status, defaults.sort, defaults.dir, defaults.limit],
  );

  // Query string to send to the API for this state (always paged).
  const apiQuery = useMemo(() => {
    const p = new URLSearchParams();
    if (state.q) p.set("q", state.q);
    if (state.status) p.set("status", state.status);
    p.set("sort", state.sort);
    p.set("dir", state.dir);
    p.set("page", String(state.page));
    p.set("limit", String(state.limit));
    for (const [k, v] of Object.entries(state.filters)) if (v) p.set(k, v);
    return p.toString();
  }, [state]);

  const activeFilterCount = (state.q ? 1 : 0) + (state.status && state.status !== (defaults.status ?? "") ? 1 : 0) +
    Object.values(state.filters).filter(Boolean).length;

  return { state, update, apiQuery, activeFilterCount };
}
