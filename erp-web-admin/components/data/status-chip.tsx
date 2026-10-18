import { cn } from "cn";

// One status vocabulary for every screen (design system §03, the same
// mapping the Flutter POS uses): the color is fixed per meaning, and the
// label is always written out, so color is never the only signal.
export type Tone = "ok" | "warn" | "bad" | "info" | "plum" | "teal" | "muted";

const toneClass: Record<Tone, string> = {
  ok: "bg-pay-soft text-pay",
  warn: "bg-warn-soft text-warn",
  bad: "bg-destructive-soft text-destructive",
  info: "bg-primary-soft text-primary",
  plum: "bg-plum-soft text-plum",
  teal: "bg-teal-soft text-teal",
  muted: "bg-muted text-muted-foreground",
};

const statusTone: Record<string, Tone> = {
  active: "ok",
  completed: "ok",
  finalized: "ok",
  paid: "ok",
  synced: "ok",
  inactive: "muted",
  cancelled: "muted",
  draft: "muted",
  discontinued: "muted",
  pending: "warn",
  held: "info",
  held_voided: "muted",
  cart: "info",
  partially_paid: "warn",
  unpaid: "warn",
  submitted: "warn",
  approved: "info",
  posted: "teal",
  refunded: "teal",
  partially_refunded: "teal",
  returned: "plum",
  reversed: "plum",
  voided: "bad",
  failed: "bad",
  rejected: "bad",
};

function labelFor(status: string) {
  const s = status.replace(/_/g, " ");
  return s.charAt(0).toUpperCase() + s.slice(1);
}

export function StatusChip({ status, tone, label, className }: { status: string; tone?: Tone; label?: string; className?: string }) {
  const t = tone ?? statusTone[status] ?? "muted";
  return (
    <span
      className={cn(
        "inline-flex h-6 items-center gap-1.5 whitespace-nowrap rounded-full px-2.5 text-xs font-medium",
        toneClass[t],
        className,
      )}
    >
      <span className="size-1.5 rounded-full bg-current" aria-hidden />
      {label ?? labelFor(status)}
    </span>
  );
}
