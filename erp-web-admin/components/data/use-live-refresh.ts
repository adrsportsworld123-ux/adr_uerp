"use client";

import { useEffect, useRef } from "react";

// Keeps a monitoring screen current without a manual reload: calls
// `refresh` every `intervalMs` while the tab is visible, and straight away
// when the tab or window comes back into view (so a screen left open in the
// background catches up the moment someone looks at it). Nothing runs while
// the tab is hidden or `enabled` is false.
export function useLiveRefresh(refresh: () => void, { intervalMs = 15000, enabled = true }: { intervalMs?: number; enabled?: boolean } = {}) {
  const fn = useRef(refresh);
  useEffect(() => {
    fn.current = refresh;
  }, [refresh]);

  useEffect(() => {
    if (!enabled) return;
    const tick = () => {
      if (document.visibilityState === "visible") fn.current();
    };
    const id = window.setInterval(tick, intervalMs);
    document.addEventListener("visibilitychange", tick);
    window.addEventListener("focus", tick);
    return () => {
      window.clearInterval(id);
      document.removeEventListener("visibilitychange", tick);
      window.removeEventListener("focus", tick);
    };
  }, [enabled, intervalMs]);
}
