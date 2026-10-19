import { FormEvent, ReactNode } from "react";
import { cn } from "cn";

// The page building blocks the Finance screens share (and any report or
// workbench page can): a bordered section with an optional title row, a
// filter bar above the results, and a label-over-control field. One look,
// so a page made in Phase 4 reads like one made in Phase 14.

export function Panel({
  title,
  description,
  actions,
  children,
  className,
  ...rest
}: {
  title?: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  className?: string;
  "data-testid"?: string;
}) {
  return (
    <section className={cn("flex flex-col gap-4 rounded-xl border bg-card p-4", className)} {...rest}>
      {(title || actions) && (
        <div className="flex flex-wrap items-start justify-between gap-2">
          <div className="min-w-0">
            {title && <h2 className="flex flex-wrap items-center gap-2 text-base font-semibold">{title}</h2>}
            {description && <p className="mt-0.5 text-sm text-muted-foreground">{description}</p>}
          </div>
          {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
        </div>
      )}
      {children}
    </section>
  );
}

/** The controls that choose what a page shows. A form when `onSubmit` is given (a Load button), a plain bar otherwise. */
export function FilterBar({ children, onSubmit, className }: { children: ReactNode; onSubmit?: () => void; className?: string }) {
  const cls = cn("flex flex-wrap items-end gap-3 rounded-xl border bg-card p-4 print:hidden", className);
  if (!onSubmit) return <div className={cls}>{children}</div>;
  return (
    <form
      className={cls}
      onSubmit={(e: FormEvent) => {
        e.preventDefault();
        onSubmit();
      }}
    >
      {children}
    </form>
  );
}

export function Field({ label, htmlFor, children, className }: { label: ReactNode; htmlFor?: string; children: ReactNode; className?: string }) {
  return (
    <div className={cn("flex flex-col gap-1.5", className)}>
      <label htmlFor={htmlFor} className="text-sm font-medium leading-none">
        {label}
      </label>
      {children}
    </div>
  );
}

/** A small label-over-figure tile for a report's headline numbers. */
export function Stat({ label, value, strong, testId }: { label: ReactNode; value: ReactNode; strong?: boolean; testId?: string }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span className={cn("tabular", strong ? "text-lg font-semibold" : "text-base")} data-testid={testId}>
        {value}
      </span>
    </div>
  );
}

/** The native select style the list filters use. */
export const selectClass = "h-9 rounded-lg border bg-card px-2 text-sm";
