"use client";

// One purchase order: its items with ordered / received / pending, the
// GRNs that received it (each with its purchase bill), and the next step
// for its status. Draft → Issue; issued → Receive goods (any quantity up
// to what is pending, as a draft GRN), until everything has arrived and
// the PO completes by itself, or it is closed short.
import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { PackageCheck, Plus, Send, Trash2, X } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { GRN, PurchaseOrder, PurchaseOrderLine } from "@/lib/types";
import { qty, rupees } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Modal } from "@/components/ui/dialog";
import { PageHeader } from "@/components/data/page-header";
import { Field, Panel, Stat } from "@/components/data/panel";
import { PO_TONE, StatusChip } from "@/components/data/status-chip";
import { ProductPicker, VariantOption } from "@/components/data/product-picker";
import { DownloadPdfButton, PrintPdfButton } from "@/components/data/document-actions";
import { DeleteDocumentButton } from "@/components/purchase/delete-document-dialog";

import { currencySymbol } from "@/lib/currency";
type ReceiveRow = { quantity: string; unitCost: string; batchNo: string; expiryDate: string };

export default function PurchaseOrderPage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  const [po, setPo] = useState<PurchaseOrder | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [receiving, setReceiving] = useState(false);
  const [ending, setEnding] = useState<"cancel" | "close" | null>(null);

  const load = useCallback(() => {
    api
      .get<PurchaseOrder>(`/api/v1/purchase/orders/${id}`)
      .then((d) => {
        setPo(d);
        setError(null);
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load the purchase order"));
  }, [id]);
  useEffect(load, [load]);

  async function run(call: () => Promise<PurchaseOrder>, ok: string): Promise<boolean> {
    setBusy(true);
    try {
      setPo(await call());
      toast.success(ok);
      return true;
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not update the purchase order");
      return false;
    } finally {
      setBusy(false);
    }
  }

  if (error) {
    return (
      <p className="text-sm text-destructive" role="alert">
        {error}
      </p>
    );
  }
  if (!po) return <p className="text-sm text-muted-foreground">Loading…</p>;

  const base = `/api/v1/purchase/orders/${po.po_id}`;
  const isDraft = po.status === "draft";
  const receivable = po.status === "open" || po.status === "partially_received";
  const hasOpenGRN = po.grns.some((g) => g.status === "draft");

  return (
    <div className="flex max-w-5xl flex-col gap-5">
      <PageHeader
        title={po.po_number}
        description={`${po.supplier_name} · ordered ${po.order_date}`}
        actions={
          <>
            <DownloadPdfButton path={`${base}/pdf`} label={isDraft ? "Draft PDF" : "Download PDF"} size="default" />
            <PrintPdfButton path={`${base}/pdf`} />
            {isDraft && (
              <Button disabled={busy || po.lines.length === 0} onClick={() => run(() => api.post<PurchaseOrder>(`${base}/issue`), "Purchase order issued")}>
                <Send /> Issue to supplier
              </Button>
            )}
            {receivable && (
              <Button disabled={busy} onClick={() => setReceiving(true)}>
                <PackageCheck /> Receive goods
              </Button>
            )}
            {(isDraft || po.status === "open") && po.grns.length === 0 && (
              <>
                {po.status === "open" && (
                  <Button variant="outline" disabled={busy} onClick={() => setEnding("cancel")}>
                    Cancel PO
                  </Button>
                )}
                <DeleteDocumentButton
                  path={base}
                  what="purchase order"
                  number={po.po_number}
                  disabled={busy}
                  note={
                    isDraft
                      ? "The draft is removed from every list. You can restore it from the Recycle Bin."
                      : "Use this only if it was raised by mistake. If the supplier has the order, cancel it instead so the record stays. A deleted PO can be restored from the Recycle Bin."
                  }
                  onDeleted={() => router.push("/purchase/orders")}
                />
              </>
            )}
            {po.status === "partially_received" && (
              <Button variant="outline" disabled={busy || hasOpenGRN} title={hasOpenGRN ? "Complete or finish the open GRN first" : undefined} onClick={() => setEnding("close")}>
                Close short
              </Button>
            )}
          </>
        }
      />

      <Panel>
        <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
          <Stat label="Status" value={<StatusChip status={po.status} tone={PO_TONE[po.status]} />} />
          <Stat label="Expected delivery" value={po.expected_date ?? "—"} />
          <Stat label="GRNs" value={po.grns.length} />
          <Stat label="Order value (before GST)" value={rupees(po.subtotal)} strong testId="po-subtotal" />
        </div>
        {po.notes && <p className="text-sm text-muted-foreground">{po.notes}</p>}
        {po.close_reason && (
          <p className="text-sm">
            <span className="font-medium">{po.status === "closed" ? "Closed short" : "Cancelled"}:</span> {po.close_reason}
          </p>
        )}
      </Panel>

      <Panel title="Items">
        <ItemsTable po={po} busy={busy} onRemove={(lineId) => run(() => api.delete<PurchaseOrder>(`${base}/lines/${lineId}`), "Item removed")} />
        {isDraft && <AddLine disabled={busy} onAdd={(body) => run(() => api.post<PurchaseOrder>(`${base}/lines`, body), "Item added")} />}
      </Panel>

      <Panel title="Goods receipts" description="Each delivery is a GRN against this PO; bill each one when the supplier's invoice arrives.">
        {po.grns.length === 0 ? (
          <p className="text-sm text-muted-foreground">Nothing received yet.</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[560px] text-sm" data-testid="po-grns">
              <thead className="text-left text-xs text-muted-foreground">
                <tr>
                  <th className="pb-1 font-medium">GRN</th>
                  <th className="pb-1 font-medium">Status</th>
                  <th className="pb-1 text-right font-medium">Value</th>
                  <th className="pb-1 pl-4 font-medium">Purchase bill</th>
                </tr>
              </thead>
              <tbody>
                {po.grns.map((g) => (
                  <tr key={g.grn_id} className="border-t">
                    <td className="py-1.5">
                      <Link href={`/purchase/grn/${g.grn_id}`} className="font-medium underline-offset-2 hover:underline">
                        {g.grn_number}
                      </Link>
                    </td>
                    <td className="py-1.5">
                      <StatusChip status={g.status} />
                    </td>
                    <td className="tabular py-1.5 text-right">{rupees(g.grand_total)}</td>
                    <td className="py-1.5 pl-4">
                      {g.bill_id ? (
                        <Link href={`/purchase/bills/${g.bill_id}`} className="underline-offset-2 hover:underline">
                          {g.bill_number}
                        </Link>
                      ) : g.status === "completed" ? (
                        <Link href={`/purchase/bills/new?grn_id=${g.grn_id}`} className="text-primary underline-offset-2 hover:underline">
                          Enter bill
                        </Link>
                      ) : (
                        <span className="text-muted-foreground">Complete the GRN first</span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Panel>

      {receiving && (
        <ReceiveDialog
          po={po}
          onClose={() => setReceiving(false)}
          onCreated={(grn) => {
            toast.success(`${grn.grn_number} drafted — check it and complete it to bring the stock in`);
            router.push(`/purchase/grn/${grn.grn_id}`);
          }}
        />
      )}
      {ending && (
        <EndDialog
          kind={ending}
          busy={busy}
          onClose={() => setEnding(null)}
          onConfirm={(reason) =>
            run(() => api.post<PurchaseOrder>(`${base}/${ending}`, { reason }), ending === "cancel" ? "Purchase order cancelled" : "Purchase order closed").then((done) =>
              done ? setEnding(null) : undefined,
            )
          }
        />
      )}
    </div>
  );
}

function ItemsTable({ po, busy, onRemove }: { po: PurchaseOrder; busy: boolean; onRemove: (lineId: string) => void }) {
  if (po.lines.length === 0) return <p className="text-sm text-muted-foreground">No items yet.</p>;
  const isDraft = po.status === "draft";
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[640px] text-sm" data-testid="po-lines">
        <thead className="text-left text-xs text-muted-foreground">
          <tr>
            <th className="pb-1 font-medium">Product</th>
            <th className="pb-1 text-right font-medium">Ordered</th>
            <th className="pb-1 text-right font-medium">Received</th>
            <th className="pb-1 text-right font-medium">Pending</th>
            <th className="pb-1 text-right font-medium">Unit cost</th>
            <th className="pb-1 text-right font-medium">Amount</th>
            {isDraft && <th className="w-10" />}
          </tr>
        </thead>
        <tbody>
          {po.lines.map((l) => (
            <tr key={l.line_id} className="border-t">
              <td className="py-1.5">
                {l.product_name} <span className="text-xs text-muted-foreground">({l.sku})</span>
              </td>
              <td className="tabular py-1.5 text-right">{qty(l.quantity)}</td>
              <td className="tabular py-1.5 text-right">{qty(l.received_qty)}</td>
              <td className={`tabular py-1.5 text-right ${Number(l.pending_qty) > 0 && !isDraft ? "font-medium text-warn" : ""}`}>{qty(l.pending_qty)}</td>
              <td className="tabular py-1.5 text-right">{rupees(l.unit_cost)}</td>
              <td className="tabular py-1.5 text-right">{rupees(l.line_total)}</td>
              {isDraft && (
                <td className="py-1.5 text-right">
                  <Button variant="ghost" size="icon-sm" aria-label={`Remove ${l.product_name}`} disabled={busy} onClick={() => onRemove(l.line_id)}>
                    <Trash2 />
                  </Button>
                </td>
              )}
            </tr>
          ))}
        </tbody>
        <tfoot>
          <tr className="border-t font-medium">
            <td className="pt-2" colSpan={5}>
              Total (before GST)
            </td>
            <td className="tabular pt-2 text-right">{rupees(po.subtotal)}</td>
            {isDraft && <td />}
          </tr>
        </tfoot>
      </table>
    </div>
  );
}

function AddLine({ disabled, onAdd }: { disabled: boolean; onAdd: (body: { variant_id: string; quantity: number; unit_cost: number }) => Promise<boolean> }) {
  const [option, setOption] = useState<VariantOption | null>(null);
  const [quantity, setQuantity] = useState("1");
  const [unitCost, setUnitCost] = useState("");
  const ok = option && Number(quantity) > 0 && unitCost !== "" && Number(unitCost) >= 0;
  return (
    <div className="flex flex-wrap items-end gap-2 rounded-lg border bg-muted/40 p-3">
      <Field label="Add an item" htmlFor="poAddProduct" className="min-w-64 flex-1">
        <ProductPicker id="poAddProduct" ariaLabel="Add an item: product" value={option} onChange={setOption} />
      </Field>
      <Field label="Quantity" htmlFor="poAddQty" className="w-28">
        <Input id="poAddQty" type="number" min="0.001" step="0.001" value={quantity} onChange={(e) => setQuantity(e.target.value)} />
      </Field>
      <Field label={`Unit cost (${currencySymbol()})`} htmlFor="poAddCost" className="w-32">
        <Input id="poAddCost" type="number" min="0" step="0.01" value={unitCost} onChange={(e) => setUnitCost(e.target.value)} />
      </Field>
      <Button
        variant="outline"
        disabled={disabled || !ok}
        onClick={() =>
          onAdd({ variant_id: option!.variantId, quantity: Number(quantity), unit_cost: Number(unitCost) }).then((done) => {
            if (!done) return;
            setOption(null);
            setQuantity("1");
            setUnitCost("");
          })
        }
      >
        <Plus /> Add
      </Button>
    </div>
  );
}

// Receive goods: one row per item still pending, prefilled with what is
// pending and the PO's agreed cost. Whatever arrived goes in; 0 skips the
// item this time. The server refuses more than is pending.
function ReceiveDialog({ po, onClose, onCreated }: { po: PurchaseOrder; onClose: () => void; onCreated: (grn: GRN) => void }) {
  const pending = po.lines.filter((l) => Number(l.pending_qty) > 0);
  const [rows, setRows] = useState<Record<string, ReceiveRow>>(() =>
    Object.fromEntries(pending.map((l) => [l.line_id, { quantity: String(Number(l.pending_qty)), unitCost: l.unit_cost, batchNo: "", expiryDate: "" }])),
  );
  const [freight, setFreight] = useState("");
  const [other, setOther] = useState("");
  const [notes, setNotes] = useState("");
  const [busy, setBusy] = useState(false);
  const set = (l: PurchaseOrderLine, patch: Partial<ReceiveRow>) => setRows({ ...rows, [l.line_id]: { ...rows[l.line_id], ...patch } });
  const any = pending.some((l) => Number(rows[l.line_id]?.quantity) > 0);

  async function submit() {
    setBusy(true);
    try {
      const grn = await api.post<GRN>(`/api/v1/purchase/orders/${po.po_id}/grn`, {
        freight_amount: Number(freight) || 0,
        other_charges: Number(other) || 0,
        notes,
        lines: pending
          .filter((l) => Number(rows[l.line_id].quantity) > 0)
          .map((l) => {
            const r = rows[l.line_id];
            return {
              po_line_id: l.line_id,
              quantity: Number(r.quantity),
              unit_cost: Number(r.unitCost),
              batch_no: r.batchNo || undefined,
              expiry_date: r.expiryDate || undefined,
            };
          }),
      });
      onCreated(grn);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not create the GRN");
      setBusy(false);
    }
  }

  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title="Receive goods"
      description="Enter what actually arrived. It becomes a draft GRN you complete to bring the stock in."
      width={760}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={busy || !any}>
            {busy ? "Creating…" : "Create GRN"}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <div className="overflow-x-auto">
          <table className="w-full min-w-[620px] text-sm">
            <thead className="text-left text-xs text-muted-foreground">
              <tr>
                <th className="pb-1 font-medium">Product</th>
                <th className="pb-1 text-right font-medium">Pending</th>
                <th className="pb-1 pl-3 font-medium">Received now</th>
                <th className="pb-1 pl-3 font-medium">Unit cost ({currencySymbol()})</th>
                <th className="pb-1 pl-3 font-medium">Batch / expiry</th>
              </tr>
            </thead>
            <tbody>
              {pending.map((l) => {
                const r = rows[l.line_id];
                return (
                  <tr key={l.line_id} className="border-t align-top">
                    <td className="py-2">
                      {l.product_name} <span className="text-xs text-muted-foreground">({l.sku})</span>
                    </td>
                    <td className="tabular py-2 text-right">{qty(l.pending_qty)}</td>
                    <td className="py-2 pl-3">
                      <Input
                        aria-label={`Received quantity of ${l.product_name}`}
                        className="w-24"
                        type="number"
                        min="0"
                        max={Number(l.pending_qty)}
                        step="0.001"
                        value={r.quantity}
                        onChange={(e) => set(l, { quantity: e.target.value })}
                      />
                    </td>
                    <td className="py-2 pl-3">
                      <Input aria-label={`Unit cost of ${l.product_name}`} className="w-28" type="number" min="0" step="0.01" value={r.unitCost} onChange={(e) => set(l, { unitCost: e.target.value })} />
                    </td>
                    <td className="py-2 pl-3">
                      {l.track_batch ? (
                        <div className="flex gap-1">
                          <Input aria-label={`Batch of ${l.product_name}`} className="w-28" placeholder="Batch *" value={r.batchNo} onChange={(e) => set(l, { batchNo: e.target.value })} />
                          <Input aria-label={`Expiry of ${l.product_name}`} className="w-36" type="date" value={r.expiryDate} onChange={(e) => set(l, { expiryDate: e.target.value })} />
                        </div>
                      ) : (
                        <span className="text-xs text-muted-foreground">—</span>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
        <div className="grid gap-3 sm:grid-cols-3">
          <Field label={`Freight (${currencySymbol()})`} htmlFor="rcvFreight">
            <Input id="rcvFreight" type="number" min="0" step="0.01" value={freight} onChange={(e) => setFreight(e.target.value)} />
          </Field>
          <Field label={`Other charges (${currencySymbol()})`} htmlFor="rcvOther">
            <Input id="rcvOther" type="number" min="0" step="0.01" value={other} onChange={(e) => setOther(e.target.value)} />
          </Field>
          <Field label="Delivery note / challan" htmlFor="rcvNotes">
            <Input id="rcvNotes" value={notes} onChange={(e) => setNotes(e.target.value)} />
          </Field>
        </div>
      </div>
    </Modal>
  );
}

function EndDialog({ kind, busy, onClose, onConfirm }: { kind: "cancel" | "close"; busy: boolean; onClose: () => void; onConfirm: (reason: string) => void }) {
  const [reason, setReason] = useState("");
  const close = kind === "close";
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={close ? "Close this PO short?" : "Cancel this PO?"}
      description={
        close
          ? "What was received stays received and billed as usual. The pending quantity is no longer expected, and no more GRNs can be made against this PO."
          : "Nothing has been received against it. A cancelled PO can't be reopened."
      }
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            <X /> Keep it
          </Button>
          <Button variant="destructive" disabled={busy || (close && !reason.trim())} onClick={() => onConfirm(reason.trim())}>
            {close ? "Close short" : "Cancel PO"}
          </Button>
        </>
      }
    >
      <Field label={close ? "Reason *" : "Reason"} htmlFor="poEndReason">
        <Textarea id="poEndReason" rows={2} value={reason} onChange={(e) => setReason(e.target.value)} placeholder={close ? "e.g. Supplier discontinued the item" : ""} />
      </Field>
    </Modal>
  );
}
