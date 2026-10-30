"use client";

// Approvals (erp-core-go §3.77, internal/approvals/requests.go), two tabs:
//   Waiting — everything waiting for someone: branch transfers to approve,
//   journal vouchers submitted for approval, oversold offline sales to
//   review, employees' leave, permission and attendance corrections
//   (§3.78; nobody decides their own). Approve / reject (or resolve) here; each goes through the
//   item's own endpoint and permission, exactly as on its own screen.
//   Given — what was approved, mostly by a manager's PIN at the till:
//   manual discounts, returns, open prices, cash and stock-count variances,
//   stock adjustments, cash paid in/out, payment corrections, transfers,
//   vouchers, employee loans and employees' requests — who asked, who approved, how much, why.
// Both need approvals.view (Branch Manager and Merchant Admin).
import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Check, Loader2, X } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import { dateTime, localDate, rupees } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Modal } from "@/components/ui/dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { PageHeader } from "@/components/data/page-header";
import { Field, FilterBar, Panel, selectClass } from "@/components/data/panel";
import { StatusChip, Tone } from "@/components/data/status-chip";
import { useMyBranches } from "@/components/data/branch-select";

const message = (e: unknown, fallback: string) => (e instanceof ApiError ? e.message : fallback);

type WaitingKind = "transfer" | "voucher" | "stock_review" | "leave" | "permission" | "regularization";
type WaitingItem = {
  kind: WaitingKind;
  id: string;
  reference: string;
  summary: string;
  detail: string;
  branch: string | null;
  requested_by: string | null;
  requested_at: string;
  amount: string | null; // null for an HR request
  can_act: boolean;
  why?: string;
};
type GivenItem = {
  kind: string;
  at: string;
  reference: string;
  branch: string | null;
  done_by: string | null;
  approved_by: string | null;
  amount: string | null;
  reason: string | null;
};

const WAITING_LABEL: Record<WaitingKind, string> = {
  transfer: "Branch transfer",
  voucher: "Journal voucher",
  stock_review: "Oversold offline sale",
  leave: "Leave",
  permission: "Permission",
  regularization: "Attendance correction",
};
const WAITING_TONE: Record<WaitingKind, Tone> = { transfer: "info", voucher: "plum", stock_review: "warn", leave: "teal", permission: "teal", regularization: "teal" };
const WAITING_LINK: Record<WaitingKind, (id: string) => string> = {
  transfer: (id) => `/transfers/${id}`,
  voucher: (id) => `/accounting/vouchers/${id}`,
  stock_review: () => "/inventory/review",
  leave: () => "/hr/leave",
  permission: () => "/hr/leave",
  regularization: () => "/hr/leave",
};
// Employees' requests (erp-core-go §3.78) are decided through their own endpoints.
const HR_PATH: Partial<Record<WaitingKind, string>> = {
  leave: "/api/v1/hr/leave-requests",
  permission: "/api/v1/hr/permission-requests",
  regularization: "/api/v1/hr/regularizations",
};
const GIVEN_LABEL: Record<string, string> = {
  manual_discount: "Manual discount",
  return: "Return / refund",
  open_price: "Open price",
  cash_variance: "Cash count variance",
  stock_count: "Stock count variance",
  stock_adjustment: "Stock adjustment",
  cash_movement: "Cash in / out",
  payment_correction: "Payment correction",
  transfer: "Branch transfer",
  voucher: "Journal voucher",
  loan: "Employee loan",
  leave: "Leave",
  permission: "Permission",
  regularization: "Attendance correction",
};

export default function ApprovalsPage() {
  const params = useSearchParams();
  const router = useRouter();
  const tab = params.get("tab") === "given" ? "given" : "waiting";
  const { can } = usePermissions();
  const allowed = can("approvals.view");
  return (
    <div className="space-y-5">
      <PageHeader
        title="Approvals"
        description="What is waiting for an approval, and every approval already given — at the till by PIN or here — with who asked, who approved, how much and why."
      />
      <div role="tablist" aria-label="Approvals" className="flex w-fit gap-1 rounded-lg bg-muted p-1">
        {(["waiting", "given"] as const).map((t) => (
          <button
            key={t}
            role="tab"
            aria-selected={tab === t}
            onClick={() => router.replace(`/approvals?tab=${t}`)}
            className={`rounded-md px-3 py-1.5 text-sm ${tab === t ? "bg-card font-medium shadow-sm" : "text-muted-foreground hover:text-foreground"}`}
          >
            {t === "waiting" ? "Waiting" : "Given"}
          </button>
        ))}
      </div>
      {!allowed ? (
        <p className="text-sm text-muted-foreground">{missingPermissionHint("approvals.view")}</p>
      ) : tab === "waiting" ? (
        <Waiting />
      ) : (
        <Given />
      )}
    </div>
  );
}

// ------------------------------------------------------------------ waiting

function Waiting() {
  const [items, setItems] = useState<WaitingItem[] | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [rejecting, setRejecting] = useState<WaitingItem | null>(null);
  const [note, setNote] = useState("");

  const load = useCallback(() => {
    api
      .get<{ items: WaitingItem[] }>("/api/v1/approvals/waiting")
      .then((d) => setItems(d.items))
      .catch((e) => toast.error(message(e, "Could not load the approvals waiting")));
  }, []);
  useEffect(load, [load]);

  async function approve(it: WaitingItem) {
    setBusy(it.id);
    try {
      if (it.kind === "transfer") await api.post(`/api/v1/branch-transfers/${it.id}/approve`, {});
      else if (it.kind === "voucher") await api.post(`/api/v1/accounting/vouchers/${it.id}/approve`, {});
      else if (HR_PATH[it.kind]) await api.post(`${HR_PATH[it.kind]}/${it.id}/approve`, {});
      toast.success(`${WAITING_LABEL[it.kind]} ${it.reference} approved`);
      load();
    } catch (e) {
      toast.error(message(e, "Could not approve it"));
    } finally {
      setBusy(null);
    }
  }

  async function reject() {
    if (!rejecting) return;
    const it = rejecting;
    setBusy(it.id);
    try {
      if (it.kind === "transfer") await api.post(`/api/v1/branch-transfers/${it.id}/reject`, { reason: note.trim() });
      else if (it.kind === "voucher") await api.post(`/api/v1/accounting/vouchers/${it.id}/reject`, { note: note.trim() });
      else if (HR_PATH[it.kind]) await api.post(`${HR_PATH[it.kind]}/${it.id}/reject`, { note: note.trim() });
      else await api.post(`/api/v1/inventory/review/${it.id}/resolve`, { resolution: note.trim() });
      toast.success(it.kind === "stock_review" ? `Review of ${it.reference} resolved` : `${WAITING_LABEL[it.kind]} ${it.reference} sent back`);
      setRejecting(null);
      setNote("");
      load();
    } catch (e) {
      toast.error(message(e, "Could not do that"));
    } finally {
      setBusy(null);
    }
  }

  if (!items) return <p className="text-sm text-muted-foreground">Loading…</p>;
  const counts = (k: WaitingKind) => items.filter((i) => i.kind === k).length;
  return (
    <Panel
      title={`Waiting (${items.length})`}
      description={`${counts("transfer")} transfer(s), ${counts("voucher")} voucher(s), ${counts("stock_review")} oversold sale(s) to review, ${counts("leave") + counts("permission") + counts("regularization")} employee request(s). Oldest first.`}
    >
      <Table data-testid="approvals-waiting">
        <TableHeader>
          <TableRow>
            <TableHead>What</TableHead>
            <TableHead>Details</TableHead>
            <TableHead>Branch</TableHead>
            <TableHead>Asked by</TableHead>
            <TableHead className="text-right">Value</TableHead>
            <TableHead className="text-right">Action</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {items.map((it) => (
            <TableRow key={`${it.kind}-${it.id}`}>
              <TableCell>
                <StatusChip status="" tone={WAITING_TONE[it.kind]} label={WAITING_LABEL[it.kind]} />
                <Link href={WAITING_LINK[it.kind](it.id)} className="mt-1 block text-sm font-medium hover:underline">
                  {it.reference}
                </Link>
              </TableCell>
              <TableCell className="max-w-80 whitespace-normal text-sm">
                {it.summary}
                <span className="block text-xs text-muted-foreground">{it.detail}</span>
              </TableCell>
              <TableCell className="text-sm">{it.branch ?? "—"}</TableCell>
              <TableCell className="text-sm">
                {it.requested_by ?? "—"}
                <span className="block text-xs text-muted-foreground">{dateTime(it.requested_at)}</span>
              </TableCell>
              <TableCell className="tabular text-right">{it.amount != null ? rupees(it.amount) : "—"}</TableCell>
              <TableCell>
                <div className="flex justify-end gap-1.5" title={it.can_act ? undefined : it.why}>
                  {it.kind === "stock_review" ? (
                    <Button size="sm" variant="outline" disabled={!it.can_act || busy === it.id} onClick={() => setRejecting(it)}>
                      Resolve
                    </Button>
                  ) : (
                    <>
                      <Button size="sm" disabled={!it.can_act || busy === it.id} onClick={() => approve(it)} aria-label={`Approve ${it.reference}`}>
                        {busy === it.id ? <Loader2 className="animate-spin" aria-hidden /> : <Check aria-hidden />} Approve
                      </Button>
                      <Button size="sm" variant="outline" disabled={!it.can_act || busy === it.id} onClick={() => setRejecting(it)} aria-label={`Reject ${it.reference}`}>
                        <X aria-hidden /> Reject
                      </Button>
                    </>
                  )}
                </div>
                {!it.can_act && it.why && <span className="mt-1 block text-right text-xs text-muted-foreground">{it.why}</span>}
              </TableCell>
            </TableRow>
          ))}
          {items.length === 0 && (
            <TableRow>
              <TableCell colSpan={6} className="py-6 text-center text-sm text-muted-foreground">
                Nothing is waiting for approval.
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>

      {rejecting && (
        <Modal
          open
          onOpenChange={(o) => !o && setRejecting(null)}
          title={rejecting.kind === "stock_review" ? `Resolve ${rejecting.reference}` : `Reject ${rejecting.reference}`}
          description={
            rejecting.kind === "voucher"
              ? "The voucher goes back to its author as a draft, with your note."
              : rejecting.kind === "transfer"
                ? "The transfer is rejected; the reason is kept on it."
                : HR_PATH[rejecting.kind]
                  ? "The employee sees the request rejected, with your reason."
                  : "Say what was done about it (a count, an adjustment)."
          }
          footer={
            <>
              <Button variant="outline" onClick={() => setRejecting(null)}>
                Cancel
              </Button>
              <Button onClick={reject} disabled={!note.trim() || busy != null} variant={rejecting.kind === "stock_review" ? "default" : "destructive"}>
                {rejecting.kind === "stock_review" ? "Resolve" : "Reject"}
              </Button>
            </>
          }
        >
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="appr-note">{rejecting.kind === "stock_review" ? "What was done *" : "Reason *"}</Label>
            <Input id="appr-note" autoFocus value={note} onChange={(e) => setNote(e.target.value)} />
          </div>
        </Modal>
      )}
    </Panel>
  );
}

// ------------------------------------------------------------------ given

function Given() {
  const branches = useMyBranches();
  const [from, setFrom] = useState(() => localDate(-29));
  const [to, setTo] = useState(() => localDate());
  const [kind, setKind] = useState("");
  const [branchId, setBranchId] = useState("");
  const [items, setItems] = useState<GivenItem[] | null>(null);
  const [truncated, setTruncated] = useState(false);

  useEffect(() => {
    const q = new URLSearchParams({ from, to });
    if (kind) q.set("kind", kind);
    if (branchId) q.set("branch_id", branchId);
    api
      .get<{ items: GivenItem[]; truncated: boolean }>(`/api/v1/approvals/given?${q.toString()}`)
      .then((d) => {
        setItems(d.items);
        setTruncated(d.truncated);
      })
      .catch((e) => toast.error(message(e, "Could not load the approvals given")));
  }, [from, to, kind, branchId]);

  return (
    <div className="space-y-4">
      <FilterBar>
        <Field label="From" htmlFor="given-from">
          <Input id="given-from" type="date" value={from} max={to} onChange={(e) => setFrom(e.target.value)} />
        </Field>
        <Field label="To" htmlFor="given-to">
          <Input id="given-to" type="date" value={to} min={from} onChange={(e) => setTo(e.target.value)} />
        </Field>
        <Field label="Kind" htmlFor="given-kind">
          <select id="given-kind" value={kind} onChange={(e) => setKind(e.target.value)} className={selectClass}>
            <option value="">Every kind</option>
            {Object.entries(GIVEN_LABEL).map(([k, v]) => (
              <option key={k} value={k}>
                {v}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Branch" htmlFor="given-branch">
          <select id="given-branch" value={branchId} onChange={(e) => setBranchId(e.target.value)} className={`${selectClass} w-48`}>
            <option value="">All branches</option>
            {branches.branches.map((b) => (
              <option key={b.branch_id} value={b.branch_id}>
                {b.name}
              </option>
            ))}
          </select>
        </Field>
      </FilterBar>
      <Panel title={items ? `Approvals given (${items.length}${truncated ? "+" : ""})` : "Approvals given"} description="Newest first. Amounts are the discount, refund, line value, variance or document total.">
        {!items ? (
          <p className="text-sm text-muted-foreground">Loading…</p>
        ) : (
          <Table data-testid="approvals-given">
            <TableHeader>
              <TableRow>
                <TableHead>When</TableHead>
                <TableHead>What</TableHead>
                <TableHead>Reference</TableHead>
                <TableHead>Branch</TableHead>
                <TableHead>Done by</TableHead>
                <TableHead>Approved by</TableHead>
                <TableHead className="text-right">Amount</TableHead>
                <TableHead>Reason</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((it, i) => (
                <TableRow key={`${it.kind}-${it.reference}-${it.at}-${i}`}>
                  <TableCell className="whitespace-nowrap text-sm">{dateTime(it.at)}</TableCell>
                  <TableCell className="text-sm">{GIVEN_LABEL[it.kind] ?? it.kind}</TableCell>
                  <TableCell className="text-sm">{it.reference}</TableCell>
                  <TableCell className="text-sm">{it.branch ?? "—"}</TableCell>
                  <TableCell className="text-sm">{it.done_by ?? "—"}</TableCell>
                  <TableCell className="text-sm font-medium">{it.approved_by ?? "—"}</TableCell>
                  <TableCell className="tabular text-right">{it.amount != null ? rupees(it.amount) : "—"}</TableCell>
                  <TableCell className="max-w-72 whitespace-normal text-sm text-muted-foreground">{it.reason ?? ""}</TableCell>
                </TableRow>
              ))}
              {items.length === 0 && (
                <TableRow>
                  <TableCell colSpan={8} className="py-6 text-center text-sm text-muted-foreground">
                    No approvals in these dates.
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        )}
      </Panel>
    </div>
  );
}
