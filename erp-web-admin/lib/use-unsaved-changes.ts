"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";

// Warns before unsaved edits are lost (design system principle 4).
// - Closing the tab / reloading: the browser's own beforeunload prompt.
// - Clicking an in-app link (sidebar, breadcrumb, any <a>): the App Router
//   has no route-blocking API, so link clicks are intercepted in the
//   capture phase and confirmed with `confirmLeave`, which the caller
//   renders as a real dialog (window.confirm is avoided on purpose).
export function useUnsavedChanges(dirty: boolean, confirmLeave: (proceed: () => void) => void) {
  const router = useRouter();
  useEffect(() => {
    if (!dirty) return;
    const onBeforeUnload = (e: BeforeUnloadEvent) => {
      e.preventDefault();
    };
    const onClick = (e: MouseEvent) => {
      if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
      const anchor = (e.target as HTMLElement | null)?.closest("a");
      if (!anchor || anchor.target === "_blank" || !anchor.href) return;
      const url = new URL(anchor.href);
      if (url.origin !== window.location.origin || url.href === window.location.href) return;
      e.preventDefault();
      e.stopPropagation();
      confirmLeave(() => router.push(url.pathname + url.search + url.hash));
    };
    window.addEventListener("beforeunload", onBeforeUnload);
    document.addEventListener("click", onClick, true);
    return () => {
      window.removeEventListener("beforeunload", onBeforeUnload);
      document.removeEventListener("click", onClick, true);
    };
  }, [dirty, confirmLeave, router]);
}
