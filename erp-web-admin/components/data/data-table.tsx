"use client";

import { ReactNode, useEffect, useState } from "react";
import { ArrowDown, ArrowUp, ArrowUpDown, ChevronLeft, ChevronRight, Inbox, RotateCw, Search, SearchX } from "lucide-react";
import { cn } from "cn";
import { Button } from "@/components/ui/button";
import { ListState, PAGE_SIZES } from "./use-list-state";

export type Column<T> = {
  key: string;
  header: string;
  cell: (row: T) => ReactNode;
  /** Server sort key; omit for an unsortable column. */
  sort?: string;
  align?: "left" | "right";
  className?: string;
};

export type DataTableProps<T> = {
  columns: Column<T>[];
  rows: T[] | null;
  total: number;
  loading: boolean;
  error: string | null;
  onRetry: () => void;
  state: ListState;
  update: (patch: Partial<Omit<ListState, "filters">> & { filters?: Record<string, string> }) => void;
  rowKey: (row: T) => string;
  searchPlaceholder: string;
  /** Accessible name for the search box (defaults to "Search"). */
  searchLabel?: string;
  /** DOM id for the search box, for pages whose tests address it directly. */
  searchId?: string;
  /** Extra filter controls rendered after the search box. */
  filters?: ReactNode;
  activeFilterCount: number;
  onClearFilters: () => void;
  empty: { title: string; body: string; action?: ReactNode };
  onRowClick?: (row: T) => void;
  rowActions?: (row: T) => ReactNode;
};

function useDebounced(value: string, ms: number) {
  const [v, setV] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setV(value), ms);
    return () => clearTimeout(t);
  }, [value, ms]);
  return v;
}

// Server-paged table with the full set of list states the design system
// requires (§08): skeleton loading, first-use empty, no-results naming the
// active filters, inline error with retry, and "Showing x–y of N" paging.
export function DataTable<T>(props: DataTableProps<T>) {
  const { columns, rows, total, loading, error, state, update } = props;
  const [search, setSearch] = useState(state.q);
  const debounced = useDebounced(search, 300);

  useEffect(() => {
    if (debounced !== state.q) update({ q: debounced });
    // eslint-disable-next-line react-hooks/exhaustive-deps -- only react to the debounced text
  }, [debounced]);

  const pages = Math.max(1, Math.ceil(total / state.limit));
  const from = total === 0 ? 0 : (state.page - 1) * state.limit + 1;
  const to = Math.min(total, state.page * state.limit);
  const colSpan = columns.length + (props.rowActions ? 1 : 0);

  function toggleSort(key: string) {
    if (state.sort === key) update({ dir: state.dir === "asc" ? "desc" : "asc" });
    else update({ sort: key, dir: "asc" });
  }

  const body = (() => {
    if (error) {
      return (
        <tr>
          <td colSpan={colSpan} className="px-4 py-10 text-center">
            <p className="text-sm font-medium text-destructive">{error}</p>
            <Button variant="outline" size="sm" className="mt-3" onClick={props.onRetry}>
              <RotateCw aria-hidden />
              Try again
            </Button>
          </td>
        </tr>
      );
    }
    if (rows === null || (loading && rows.length === 0)) {
      return Array.from({ length: Math.min(state.limit, 6) }, (_, i) => (
        <tr key={`sk-${i}`} className="border-t">
          {columns.map((c) => (
            <td key={c.key} className="px-3 py-3">
              <div className="h-3 animate-pulse rounded bg-muted motion-reduce:animate-none" style={{ width: `${45 + ((i * 17 + c.key.length * 7) % 45)}%` }} />
            </td>
          ))}
          {props.rowActions && <td />}
        </tr>
      ));
    }
    if (rows.length === 0) {
      const filtered = props.activeFilterCount > 0;
      return (
        <tr>
          <td colSpan={colSpan} className="px-4 py-12">
            <div className="mx-auto flex max-w-sm flex-col items-center gap-2 text-center">
              <div className="grid size-11 place-items-center rounded-xl bg-primary-soft text-primary">
                {filtered ? <SearchX className="size-5" /> : <Inbox className="size-5" />}
              </div>
              <p className="font-medium">{filtered ? "Nothing matches these filters" : props.empty.title}</p>
              <p className="text-sm text-muted-foreground">
                {filtered
                  ? `${state.q ? `No results for “${state.q}”. ` : ""}Clear the filters to see everything.`
                  : props.empty.body}
              </p>
              <div className="mt-1">
                {filtered ? (
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => {
                      setSearch("");
                      props.onClearFilters();
                    }}
                  >
                    Clear filters
                  </Button>
                ) : (
                  props.empty.action
                )}
              </div>
            </div>
          </td>
        </tr>
      );
    }
    return rows.map((row) => (
      <tr
        key={props.rowKey(row)}
        onClick={props.onRowClick ? () => props.onRowClick!(row) : undefined}
        className={cn("border-t transition-colors hover:bg-muted/50", props.onRowClick && "cursor-pointer", loading && "opacity-60")}
      >
        {columns.map((c) => (
          <td key={c.key} className={cn("px-3 py-2.5 align-middle", c.align === "right" && "text-right", c.className)}>
            {c.cell(row)}
          </td>
        ))}
        {props.rowActions && (
          <td className="px-2 py-1.5 text-right" onClick={(e) => e.stopPropagation()}>
            <div className="flex justify-end gap-1">{props.rowActions(row)}</div>
          </td>
        )}
      </tr>
    ));
  })();

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <label className="relative w-full sm:w-72">
          <span className="sr-only">{props.searchLabel ?? "Search"}</span>
          <Search className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" aria-hidden />
          <input
            type="search"
            id={props.searchId}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder={props.searchPlaceholder}
            className="h-9 w-full rounded-lg border bg-card pl-8 pr-3 text-sm outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/30"
          />
        </label>
        {props.filters}
        {props.activeFilterCount > 0 && (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => {
              setSearch("");
              props.onClearFilters();
            }}
          >
            Clear filters
          </Button>
        )}
      </div>

      <div className="overflow-hidden rounded-xl border bg-card">
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="bg-muted/60">
              <tr>
                {columns.map((c) => {
                  const active = c.sort && state.sort === c.sort;
                  return (
                    <th
                      key={c.key}
                      scope="col"
                      aria-sort={active ? (state.dir === "asc" ? "ascending" : "descending") : undefined}
                      className={cn(
                        "whitespace-nowrap px-3 py-2.5 text-left text-xs font-semibold text-muted-foreground",
                        c.align === "right" && "text-right",
                      )}
                    >
                      {c.sort ? (
                        <button
                          type="button"
                          onClick={() => toggleSort(c.sort!)}
                          className={cn("inline-flex items-center gap-1 hover:text-foreground", active && "text-foreground")}
                        >
                          {c.header}
                          {active ? (
                            state.dir === "asc" ? <ArrowUp className="size-3.5" /> : <ArrowDown className="size-3.5" />
                          ) : (
                            <ArrowUpDown className="size-3.5 opacity-40" />
                          )}
                        </button>
                      ) : (
                        c.header
                      )}
                    </th>
                  );
                })}
                {props.rowActions && <th className="w-px" aria-label="Actions" />}
              </tr>
            </thead>
            <tbody>{body}</tbody>
          </table>
        </div>
        <div className="flex flex-wrap items-center justify-between gap-3 border-t px-3 py-2.5 text-sm text-muted-foreground">
          <span aria-live="polite">
            {total > 0 ? `Showing ${from.toLocaleString("en-IN")}–${to.toLocaleString("en-IN")} of ${total.toLocaleString("en-IN")}` : rows ? "No records" : "Loading…"}
          </span>
          <div className="flex items-center gap-2">
            <label className="flex items-center gap-1.5">
              <span>Rows</span>
              <select
                value={state.limit}
                onChange={(e) => update({ limit: Number(e.target.value) })}
                className="h-8 rounded-md border bg-card px-1.5 text-foreground"
                aria-label="Rows per page"
              >
                {PAGE_SIZES.map((n) => (
                  <option key={n} value={n}>
                    {n}
                  </option>
                ))}
              </select>
            </label>
            <Button variant="outline" size="icon-sm" aria-label="Previous page" disabled={state.page <= 1} onClick={() => update({ page: state.page - 1 })}>
              <ChevronLeft />
            </Button>
            <span className="tabular min-w-16 text-center">
              {state.page} / {pages}
            </span>
            <Button variant="outline" size="icon-sm" aria-label="Next page" disabled={state.page >= pages} onClick={() => update({ page: state.page + 1 })}>
              <ChevronRight />
            </Button>
          </div>
        </div>
      </div>
    </div>
  );
}
