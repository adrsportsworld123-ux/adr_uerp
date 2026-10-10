"use client";

// One journal voucher (Phase 14): its lines, its history, and the next
// step its state and your permissions allow. The server enforces every
// rule (maker-checker, the period lock, post-only-once); the buttons only
// spare a round trip.
import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { ArrowLeft } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { AccountingSettings, Voucher } from "@/lib/types";
import { rupees } from "@/lib/format";
import { usePermissions } from "@/lib/permissions";
import { useAuth } from "@/lib/auth";
import { Button } from "@/components/ui/button";
import { Modal } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { PageHeader } from "@/components/data/page-header";
import { StatusChip } from "@/components/data/status-chip";
import { VoucherForm } from "@/components/accounting/voucher-form";

const TONE = { draft: "muted", submitted: "warn", approved: "info", posted: "ok", reversed: "plum" } as const;

export default function VoucherPage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  const { can } = usePermissions();
  const { userId } = useAuth();
  const [v, setV] = useState<Voucher | null>(null);
  const [settings, setSettings] = useState<AccountingSettings | null>(null);
  const [editing, setEditing] = useState(false);
  const [dialog, setDialog] = useState<"reject" | "reverse" | null>(null);
  const [text, setText] = useState("");
  const [date, setDate] = useState(new Date().toISOString().slice(0, 10));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(() => {
    api.get<Voucher>(`/api/v1/accounting/vouchers/${id}`).then(setV).catch((e) => setError(e instanceof ApiError ? e.message : "Could not load the voucher"));
    api.get<AccountingSettings>("/api/v1/accounting/settings").then(setSettings).catch(() => {});
  }, [id]);
  useEffect(load, [load]);

  async function act(action: string, body: object = {}) {
    setBusy(true);
    try {
      const next = await api.post<Voucher>(`/api/v1/accounting/vouchers/${id}/${action}`, body);
      setV(next);
      setDialog(null);
      setText("");
      toast.success(
        { submit: "Submitted for approval", approve: "Approved", reject: "Sent back to draft", post: `Posted as ${next.entry_number}`, reverse: `Reversed by ${next.reversal_entry_number}` }[action] ?? "Done",
      );
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not do that");
    } finally {
      setBusy(false);
    }
  }

  async function remove() {
    try {
      await api.delete(`/api/v1/accounting/vouchers/${id}`);
      toast.success("Draft deleted");
      router.push("/accounting/vouchers");
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not delete");
    }
  }

  if (error) return <p className="text-sm text-destructive">{error}</p>;
  if (!v) return <p className="text-sm text-muted-foreground">Loading…</p>;

  const approval = settings?.jv_approval_required ?? false;
  const approver = can("accounting.jv_approve");
  const mine = v.created_by_id === userId;

  return (
    <div className="flex max-w-4xl flex-col gap-5">
      <Link href="/accounting/vouchers" className="flex w-fit items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
        <ArrowLeft className="size-4" /> Journal vouchers
      </Link>
      <PageHeader
        title={`Voucher ${v.voucher_number}`}
        description={
          <span className="flex flex-wrap items-center gap-2">
            <StatusChip status={v.status} tone={TONE[v.status]} /> {v.entry_date} · made by {v.created_by}
          </span>
        }
        actions={
          <>
            {v.status === "draft" && !editing && (
              <>
                <Button variant="outline" onClick={() => setEditing(true)}>
                  Edit
                </Button>
                <Button variant="outline" onClick={remove}>
                  Delete
                </Button>
                {approval ? (
                  <Button disabled={busy} onClick={() => act("submit")}>
                    Submit for approval
                  </Button>
                ) : (
                  <Button disabled={busy} onClick={() => act("post")}>
                    Post
                  </Button>
                )}
              </>
            )}
            {v.status === "submitted" && approver && (
              <>
                <Button variant="outline" disabled={busy} onClick={() => setDialog("reject")}>
                  Send back
                </Button>
                <Button disabled={busy || mine} title={mine ? "Someone else has to approve your own voucher" : undefined} onClick={() => act("approve")}>
                  Approve
                </Button>
              </>
            )}
            {v.status === "approved" && (
              <Button disabled={busy} onClick={() => act("post")}>
                Post
              </Button>
            )}
            {v.status === "posted" && (!approval || approver) && (
              <Button variant="outline" disabled={busy} onClick={() => setDialog("reverse")}>
                Reverse
              </Button>
            )}
          </>
        }
      />

      {v.rejection_note && v.status === "draft" && (
        <p className="rounded-lg border border-warn/40 bg-warn-soft p-3 text-sm" role="status">
          Sent back: {v.rejection_note}
        </p>
      )}
      {v.status === "submitted" && !approver && <p className="text-sm text-muted-foreground">Waiting for someone with JV approval rights.</p>}

      <div className="rounded-xl border bg-card p-4">
        {editing ? (
          <VoucherForm
            voucher={v}
            onSaved={(next) => {
              setV(next);
              setEditing(false);
              toast.success(next.status === "draft" ? "Saved" : next.status === "posted" ? `Posted as ${next.entry_number}` : "Submitted for approval");
            }}
          />
        ) : (
          <>
            <p className="mb-3 text-sm">{v.description}</p>
            <table className="w-full text-sm">
              <thead className="text-left text-xs text-muted-foreground">
                <tr>
                  <th className="pb-1 font-medium">Account</th>
                  <th className="pb-1 text-right font-medium">Debit</th>
                  <th className="pb-1 text-right font-medium">Credit</th>
                  <th className="pb-1 font-medium">Memo</th>
                </tr>
              </thead>
              <tbody>
                {v.lines?.map((l) => (
                  <tr key={l.line_no} className="border-t">
                    <td className="py-1.5">
                      {l.account_code} · {l.account_name}
                    </td>
                    <td className="tabular py-1.5 text-right">{Number(l.debit) ? rupees(l.debit) : ""}</td>
                    <td className="tabular py-1.5 text-right">{Number(l.credit) ? rupees(l.credit) : ""}</td>
                    <td className="py-1.5 text-muted-foreground">{l.memo}</td>
                  </tr>
                ))}
              </tbody>
              <tfoot>
                <tr className="border-t font-medium">
                  <td className="pt-2">Total</td>
                  <td className="tabular pt-2 text-right">{rupees(v.total)}</td>
                  <td className="tabular pt-2 text-right">{rupees(v.total)}</td>
                  <td />
                </tr>
              </tfoot>
            </table>
          </>
        )}
      </div>

      <section className="rounded-xl border bg-card p-4 text-sm">
        <h2 className="mb-2 font-semibold">History</h2>
        <ul className="flex flex-col gap-1">
          <li>Made by {v.created_by}</li>
          {v.submitted_by && <li>Submitted by {v.submitted_by}</li>}
          {v.approved_by && <li>Approved by {v.approved_by}</li>}
          {v.posted_by && (
            <li>
              Posted by {v.posted_by} as{" "}
              <Link className="font-mono underline" href={`/accounting/journal-entries/${v.entry_number}`}>
                {v.entry_number}
              </Link>
            </li>
          )}
          {v.reversed_by && (
            <li>
              Reversed by {v.reversed_by} ({v.reversal_reason}) as{" "}
              <Link className="font-mono underline" href={`/accounting/journal-entries/${v.reversal_entry_number}`}>
                {v.reversal_entry_number}
              </Link>
            </li>
          )}
        </ul>
      </section>

      {dialog && (
        <Modal
          open
          onOpenChange={(o) => !o && setDialog(null)}
          title={dialog === "reject" ? "Send back to draft" : `Reverse ${v.voucher_number}`}
          description={
            dialog === "reject"
              ? "The maker can fix it and submit again."
              : "Posts a mirror entry (every debit becomes a credit) on the date you choose. The original stays in the ledger."
          }
          footer={
            <>
              <Button variant="outline" onClick={() => setDialog(null)}>
                Cancel
              </Button>
              <Button
                disabled={busy || !text.trim()}
                onClick={() => (dialog === "reject" ? act("reject", { note: text.trim() }) : act("reverse", { reason: text.trim(), date }))}
              >
                {dialog === "reject" ? "Send back" : "Reverse"}
              </Button>
            </>
          }
        >
          <div className="flex flex-col gap-3">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="jv-text">{dialog === "reject" ? "What needs fixing" : "Reason"}</Label>
              <Textarea id="jv-text" value={text} onChange={(e) => setText(e.target.value)} />
            </div>
            {dialog === "reverse" && (
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="jv-rev-date">Reversal date</Label>
                <Input id="jv-rev-date" type="date" value={date} onChange={(e) => setDate(e.target.value)} />
              </div>
            )}
          </div>
        </Modal>
      )}
    </div>
  );
}
