"use client";

import { ReactNode } from "react";
import { Dialog as DialogPrimitive } from "@base-ui/react/dialog";
import { AlertDialog as AlertDialogPrimitive } from "@base-ui/react/alert-dialog";
import { X } from "lucide-react";
import { cn } from "cn";

// Our dialogs are opened from code (a row action, a page button), not a
// Base UI <Trigger>, so Base UI has nothing to return focus to on close and
// focus would fall back to the top of the page — a keyboard user would
// have to Tab through the whole sidebar again. Track the last element
// focused outside any dialog and hand it back as finalFocus.
let lastFocusedOutside: HTMLElement | null = null;
if (typeof document !== "undefined") {
  document.addEventListener("focusin", (e) => {
    const t = e.target as HTMLElement | null;
    if (t && !t.closest('[role="dialog"],[role="alertdialog"]')) lastFocusedOutside = t;
  });
}
function restoreFocus(): HTMLElement | boolean {
  return lastFocusedOutside?.isConnected ? lastFocusedOutside : true;
}

const backdrop =
  "fixed inset-0 z-50 bg-black/35 transition-opacity data-[ending-style]:opacity-0 data-[starting-style]:opacity-0";
const popup =
  "fixed left-1/2 top-1/2 z-50 flex max-h-[calc(100vh-32px)] w-[min(var(--dialog-width,480px),calc(100vw-32px))] -translate-x-1/2 -translate-y-1/2 flex-col overflow-hidden rounded-xl border bg-popover text-popover-foreground shadow-2xl outline-none transition-all data-[ending-style]:scale-95 data-[ending-style]:opacity-0 data-[starting-style]:scale-95 data-[starting-style]:opacity-0";

// Form/content dialog. `onOpenChange(false)` fires on Esc, backdrop click
// and the close button — callers with unsaved input intercept it there.
export function Modal({
  open,
  onOpenChange,
  title,
  description,
  children,
  footer,
  width = 480,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description?: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
  width?: number;
}) {
  return (
    <DialogPrimitive.Root open={open} onOpenChange={(next) => onOpenChange(next)}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Backdrop className={backdrop} />
        <DialogPrimitive.Popup finalFocus={restoreFocus} className={popup} style={{ ["--dialog-width" as string]: `${width}px` }}>
          <div className="flex items-start justify-between gap-4 border-b px-5 py-4">
            <div className="min-w-0">
              <DialogPrimitive.Title className="text-base font-semibold">{title}</DialogPrimitive.Title>
              {description && (
                <DialogPrimitive.Description className="mt-0.5 text-sm text-muted-foreground">
                  {description}
                </DialogPrimitive.Description>
              )}
            </div>
            <DialogPrimitive.Close
              aria-label="Close"
              className="grid size-8 shrink-0 place-items-center rounded-md text-muted-foreground hover:bg-muted"
            >
              <X className="size-4" />
            </DialogPrimitive.Close>
          </div>
          <div className="overflow-y-auto px-5 py-4">{children}</div>
          {footer && <div className="flex flex-wrap justify-end gap-2 border-t bg-muted/40 px-5 py-3">{footer}</div>}
        </DialogPrimitive.Popup>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}

// Blocking confirmation (alertdialog role: no backdrop dismiss).
export function AlertModal({
  open,
  onOpenChange,
  title,
  children,
  actions,
  className,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  children?: ReactNode;
  actions: ReactNode;
  className?: string;
}) {
  return (
    <AlertDialogPrimitive.Root open={open} onOpenChange={(next) => onOpenChange(next)}>
      <AlertDialogPrimitive.Portal>
        <AlertDialogPrimitive.Backdrop className={backdrop} />
        <AlertDialogPrimitive.Popup finalFocus={restoreFocus} className={cn(popup, "gap-3 p-5", className)}>
          <AlertDialogPrimitive.Title className="text-base font-semibold">{title}</AlertDialogPrimitive.Title>
          {children && <div className="text-sm text-muted-foreground">{children}</div>}
          <div className="mt-1 flex flex-wrap justify-end gap-2">{actions}</div>
        </AlertDialogPrimitive.Popup>
      </AlertDialogPrimitive.Portal>
    </AlertDialogPrimitive.Root>
  );
}
