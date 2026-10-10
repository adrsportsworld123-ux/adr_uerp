"use client";

import { useCallback, useEffect, useState } from "react";
import { api } from "@/lib/api-client";

// Fetches one page of a Phase 11 list endpoint ({<key>: [...], total}).
// Keeps the previous rows visible (dimmed by DataTable) while the next page
// loads, instead of flashing back to skeletons on every keystroke.
export function useServerList<T>(path: string, key: string, query: string) {
  const [rows, setRows] = useState<T[] | null>(null);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [nonce, setNonce] = useState(0);

  useEffect(() => {
    let cancelled = false;
    api
      .get<Record<string, unknown> & { total?: number }>(`${path}?${query}`)
      .then((d) => {
        if (cancelled) return;
        const list = (d[key] as T[]) ?? [];
        setRows(list);
        setTotal(typeof d.total === "number" ? d.total : list.length);
        setError(null);
      })
      .catch((e: Error) => !cancelled && setError(e.message))
      .finally(() => !cancelled && setLoading(false));
    return () => {
      cancelled = true;
    };
  }, [path, key, query, nonce]);

  const reload = useCallback(() => {
    setLoading(true);
    setError(null);
    setNonce((n) => n + 1);
  }, []);

  return { rows, total, loading, error, reload, setLoading };
}
