"use client";

// One sale (Phase 14, GET /sales/orders/{id}/detail): items with each
// line's tax rate, the GST summary, payments (with any correction),
// discounts, returns, the ledger entries it caused and its audit trail.
// The back office's after-sale actions run on the same rules as the till:
// - Void: sales.void, a reason, only a completed sale with no returns.
// - Return / refund: the merchant's return policy (manager approval when
//   set, the return window), proportional refund, optional restock.
// - Correct a payment method (FRD §3 "Bill Modification"): a manager's
//   code + PIN and a reason; the ledger is reclassed and audited.
// Prices and quantities are corrected with a return and a new sale.
import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { useParams } from "next/navigation";
import { ArrowLeft, Ban, RotateCcw, Shuffle } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { ReturnableOrder, SaleDetail, SaleDetailPayment } from "@/lib/types";
import { CHANNEL_LABEL, METHOD_LABEL, STATUS_LABEL, dateTime, qty, rupees } from "@/lib/format";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import { Button } from "@/components/ui/button";
import { Modal } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { PageHeader } from "@/components/data/page-header";
import { StatusChip } from "@/components/data/status-chip";

const DISCOUNT_LABEL: Record<string, string> = { manual: "Manual", coupon: "Coupon", promotion: "Promotion", loyalty: "Loyalty points" };

function newKey() {
  return `admin-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

// A manager's employee code + PIN, the same pair the till's PIN pad asks for.
function ApproverFields({ code, pin, onCode, onPin, idPrefix }: { code: string; pin: string; onCode: (v: string) => void; onPin: (v: string) => void; idPrefix: string }) {
  return (
    <div className="grid grid-cols-2 gap-3">
      <div className="flex flex-col gap-1.5">
        <Label htmlFor={`${idPrefix}-code`}>Manager&apos;s employee code</Label>
        <Input id={`${idPrefix}-code`} value={code} onChange={(e) => onCode(e.target.value.toUpperCase())} autoComplete="off" />
      </div>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor={`${idPrefix}-pin`}>Manager&apos;s PIN</Label>
        <Input id={`${idPrefix}-pin`} type="password" inputMode="numeric" value={pin} onChange={(e) => onPin(e.target.value.replace(/\D/g, ""))} autoComplete="off" />
      </div>
    </div>
  );
}

function Section({ title, children, aside }: { title: string; children: React.ReactNode; aside?: React.ReactNode }) {
  return (
    <section className="rounded-xl border bg-card">
      <div className="flex items-center justify-between border-b px-4 py-2.5">
        <h2 className="text-sm font-semibold">{title}</h2>
        {aside}
      </div>
      <div className="p-4">{children}</div>
    </section>
  );
}

export default function SaleDetailPage() {
  const { id } = useParams<{ id: string }>();
  const { can } = usePermissions();
  const [sale, setSale] = useState<SaleDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [voiding, setVoiding] = useState(false);
  const [returning, setReturning] = useState(false);
  const [correcting, setCorrecting] = useState<SaleDetailPayment | null>(null);

  const load = useCallback(() => {
    api
      .get<SaleDetail>(`/api/v1/sales/orders/${id}/detail`)
      .then((d) => {
        setSale(d);
        setError(null);
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load the sale"));
  }, [id]);
  useEffect(load, [load]);

  if (error) return <p className="text-sm text-destructive">{error}</p>;
  if (!sale) return <p className="text-sm text-muted-foreground">Loading…</p>;

  const completed = sale.status === "finalized" || sale.status === "partially_refunded" || sale.status === "refunded";
  const canVoid = sale.status === "finalized" && sale.returns.length === 0;
  const canReturn = sale.status === "finalized" || sale.status === "partially_refunded";

  return (
    <div className="flex flex-col gap-5">
      <Link href="/sales" className="flex w-fit items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
        <ArrowLeft className="size-4" /> Sales
      </Link>
      <PageHeader
        title={`Sale ${sale.order_number}`}
        description={
          <span className="flex flex-wrap items-center gap-2">
            <StatusChip status={sale.status} label={STATUS_LABEL[sale.status] ?? sale.status} />
            {sale.branch_name} · {CHANNEL_LABEL[sale.channel] ?? sale.channel} · {sale.terminal_name} · {sale.cashier_name} · {dateTime(sale.finalized_at ?? sale.created_at)}
          </span>
        }
        actions={
          <>
            <Button variant="outline" disabled={!canReturn} onClick={() => setReturning(true)}>
              <RotateCcw /> Return / refund
            </Button>
            <Button
              variant="outline"
              disabled={!canVoid || !can("sales.void")}
              title={!can("sales.void") ? missingPermissionHint("sales.void") : !canVoid ? "Only a completed sale with no returns can be voided" : undefined}
              onClick={() => setVoiding(true)}
            >
              <Ban /> Void
            </Button>
          </>
        }
      />

      <div className="grid gap-5 xl:grid-cols-[minmax(0,1fr)_340px]">
        <div className="flex min-w-0 flex-col gap-5">
          <Section title="Items">
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead className="text-left text-xs text-muted-foreground">
                  <tr>
                    <th className="pb-2 font-medium">Product</th>
                    <th className="pb-2 text-right font-medium">Qty</th>
                    <th className="pb-2 text-right font-medium">Price</th>
                    <th className="pb-2 text-right font-medium">Discount</th>
                    <th className="pb-2 text-right font-medium">Taxable</th>
                    <th className="pb-2 text-right font-medium">GST</th>
                    <th className="pb-2 text-right font-medium">Total</th>
                  </tr>
                </thead>
                <tbody>
                  {sale.lines.map((l) => (
                    <tr key={l.line_id} className="border-t align-top">
                      <td className="py-2">
                        <div className="font-medium">{l.product_name}</div>
                        <div className="text-xs text-muted-foreground">
                          {l.sku}
                          {Number(l.returned_quantity) > 0 && ` · ${qty(l.returned_quantity)} returned`}
                          {l.open_price && ` · open price${l.price_approved_by ? `, approved by ${l.price_approved_by}` : ""}`}
                        </div>
                      </td>
                      <td className="tabular py-2 text-right">{qty(l.quantity)}</td>
                      <td className="tabular py-2 text-right">{rupees(l.unit_price)}</td>
                      <td className="tabular py-2 text-right">{Number(l.discount_amount) ? rupees(l.discount_amount) : "—"}</td>
                      <td className="tabular py-2 text-right">{rupees(l.taxable_value)}</td>
                      <td className="tabular py-2 text-right">
                        {rupees(l.tax_amount)}
                        <div className="text-xs text-muted-foreground">{Number(l.tax_rate)}%</div>
                      </td>
                      <td className="tabular py-2 text-right font-medium">{rupees(l.line_total)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </Section>

          <Section title="GST summary">
            <table className="w-full text-sm" aria-label="GST by rate">
              <thead className="text-left text-xs text-muted-foreground">
                <tr>
                  <th className="pb-2 font-medium">Rate</th>
                  <th className="pb-2 text-right font-medium">Taxable value</th>
                  <th className="pb-2 text-right font-medium">CGST</th>
                  <th className="pb-2 text-right font-medium">SGST</th>
                  <th className="pb-2 text-right font-medium">Total tax</th>
                </tr>
              </thead>
              <tbody>
                {sale.tax_summary.map((t) => (
                  <tr key={t.rate} className="border-t">
                    <td className="py-1.5">{Number(t.rate)}%</td>
                    <td className="tabular py-1.5 text-right">{rupees(t.taxable_value)}</td>
                    <td className="tabular py-1.5 text-right">{rupees(t.cgst)}</td>
                    <td className="tabular py-1.5 text-right">{rupees(t.sgst)}</td>
                    <td className="tabular py-1.5 text-right">{rupees(t.tax_amount)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </Section>

          {sale.returns.length > 0 && (
            <Section title="Returns">
              <ul className="flex flex-col gap-2 text-sm">
                {sale.returns.map((r) => (
                  <li key={r.return_id} className="flex flex-wrap items-center justify-between gap-2">
                    <span>
                      {r.return_number} · {dateTime(r.created_at)} · {METHOD_LABEL[r.refund_method] ?? r.refund_method}
                      {r.restocked ? " · restocked" : " · not restocked"}
                      {r.approved_by ? " · approved" : ""}
                    </span>
                    <span className="tabular font-medium">−{rupees(r.refund_amount)}</span>
                  </li>
                ))}
              </ul>
            </Section>
          )}

          <Section title="Ledger entries">
            <ul className="flex flex-col gap-1.5 text-sm">
              {sale.journal.map((j) => (
                <li key={j.entry_number} className="flex justify-between gap-2">
                  <span>
                    <span className="font-mono text-xs">{j.entry_number}</span> · {j.entry_date} · {j.description || j.source_type}
                  </span>
                  <span className="tabular">{rupees(j.amount)}</span>
                </li>
              ))}
              {sale.journal.length === 0 && <li className="text-muted-foreground">Nothing posted yet.</li>}
            </ul>
          </Section>

          {sale.audit.length > 0 && (
            <Section title="Audit trail">
              <ul className="flex flex-col gap-1.5 text-sm">
                {sale.audit.map((a, i) => (
                  <li key={i}>
                    {dateTime(a.created_at)} · {a.entity_type.replace(/_/g, " ")} {a.action}
                    {a.performed_by ? ` by ${a.performed_by}` : ""}
                    {a.reason ? ` · ${a.reason}` : ""}
                  </li>
                ))}
              </ul>
            </Section>
          )}
        </div>

        <div className="flex flex-col gap-5">
          <Section title="Totals">
            <dl className="grid grid-cols-2 gap-y-1.5 text-sm">
              <dt className="text-muted-foreground">Subtotal</dt>
              <dd className="tabular text-right">{rupees(sale.subtotal)}</dd>
              <dt className="text-muted-foreground">Discount</dt>
              <dd className="tabular text-right">{Number(sale.discount_total) ? `−${rupees(sale.discount_total)}` : "—"}</dd>
              <dt className="text-muted-foreground">Tax</dt>
              <dd className="tabular text-right">{rupees(sale.tax_total)}</dd>
              <dt className="font-semibold">Total</dt>
              <dd className="tabular text-right text-lg font-semibold">{rupees(sale.grand_total)}</dd>
              {Number(sale.credit_amount) > 0 && (
                <>
                  <dt className="text-muted-foreground">On credit · paid</dt>
                  <dd className="tabular text-right">
                    {rupees(sale.credit_amount)} · {rupees(sale.credit_paid)}
                  </dd>
                </>
              )}
            </dl>
            <p className="mt-3 text-sm">
              Customer: {sale.customer_name ? `${sale.customer_name}${sale.customer_phone ? ` · ${sale.customer_phone}` : ""}` : "Walk-in"}
            </p>
          </Section>

          <Section title="Payments">
            <ul className="flex flex-col gap-3 text-sm">
              {sale.payments.map((p) => (
                <li key={p.payment_id}>
                  <div className="flex items-center justify-between gap-2">
                    <span>
                      <span className="font-medium">{METHOD_LABEL[p.method] ?? p.method}</span>
                      {p.reference && <span className="text-muted-foreground"> · {p.reference}</span>}
                    </span>
                    <span className="tabular">{rupees(p.amount)}</span>
                  </div>
                  {p.corrections.map((c, i) => (
                    <div key={i} className="text-xs text-muted-foreground">
                      Corrected {METHOD_LABEL[c.from_method]} → {METHOD_LABEL[c.to_method]} by {c.performed_by}, approved by {c.approved_by}: {c.reason}
                    </div>
                  ))}
                  {completed && p.status === "captured" && p.method !== "credit" && (
                    <Button
                      variant="ghost"
                      size="sm"
                      className="mt-1 h-7 px-2"
                      disabled={!can("sales.backoffice")}
                      title={can("sales.backoffice") ? undefined : missingPermissionHint("sales.backoffice")}
                      onClick={() => setCorrecting(p)}
                    >
                      <Shuffle /> Correct method
                    </Button>
                  )}
                </li>
              ))}
            </ul>
          </Section>

          {sale.discounts.length > 0 && (
            <Section title="Discounts">
              <ul className="flex flex-col gap-1.5 text-sm">
                {sale.discounts.map((d, i) => (
                  <li key={i}>
                    {DISCOUNT_LABEL[d.type] ?? d.type}
                    {d.value_percent != null ? ` ${Number(d.value_percent)}%` : ""} · {rupees(d.discount_amount)} · by {d.applied_by}
                    {d.authorized_by ? `, approved by ${d.authorized_by}` : ""}
                    {d.reason ? ` · ${d.reason}` : ""}
                  </li>
                ))}
              </ul>
            </Section>
          )}
        </div>
      </div>

      {voiding && <VoidDialog sale={sale} onClose={() => setVoiding(false)} onDone={load} />}
      {returning && <ReturnDialog sale={sale} onClose={() => setReturning(false)} onDone={load} />}
      {correcting && <CorrectDialog sale={sale} payment={correcting} onClose={() => setCorrecting(null)} onDone={setSale} />}
    </div>
  );
}

function VoidDialog({ sale, onClose, onDone }: { sale: SaleDetail; onClose: () => void; onDone: () => void }) {
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  async function submit() {
    setBusy(true);
    try {
      await api.post(`/api/v1/sales/orders/${sale.order_id}/void`, { reason: reason.trim() });
      toast.success(`Voided ${sale.order_number}`);
      onClose();
      onDone();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not void");
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={`Void ${sale.order_number}?`}
      description="Voiding reverses the whole sale: stock goes back and the ledger entry is reversed. Use a return for part of a sale."
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="destructive" disabled={busy || !reason.trim()} onClick={submit}>
            Void sale
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="void-reason">Reason</Label>
        <Textarea id="void-reason" value={reason} onChange={(e) => setReason(e.target.value)} />
      </div>
    </Modal>
  );
}

function ReturnDialog({ sale, onClose, onDone }: { sale: SaleDetail; onClose: () => void; onDone: () => void }) {
  const [info, setInfo] = useState<ReturnableOrder | null>(null);
  const [quantities, setQuantities] = useState<Record<string, string>>({});
  const [reason, setReason] = useState("");
  const [method, setMethod] = useState("cash");
  const [reference, setReference] = useState("");
  const [restock, setRestock] = useState(true);
  const [code, setCode] = useState("");
  const [pin, setPin] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Set when the server asks for an approver this dialog's estimate didn't expect.
  const [approvalDemanded, setApprovalDemanded] = useState(false);
  const [key] = useState(newKey); // one key per dialog: a retry never refunds twice

  useEffect(() => {
    api
      .get<ReturnableOrder>(`/api/v1/sales/orders/${sale.order_id}/returnable`)
      .then(setInfo)
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load the sale"));
  }, [sale.order_id]);

  const lines = (info?.lines ?? []).filter((l) => Number(l.returnable_quantity) > 0);
  const chosen = lines
    .map((l) => ({ l, q: Number(quantities[l.line_id] || 0) }))
    .filter((x) => x.q > 0);
  const tooMany = chosen.some((x) => x.q > Number(x.l.returnable_quantity));
  const estimate = chosen.reduce((s, x) => s + x.q * Number(x.l.unit_refund), 0);
  // Approval (Sales Policy) applies above the Approval Policies' refund
  // threshold only; null means every refund.
  const above = info?.approval_above ?? null;
  const needsApproval = approvalDemanded || ((info?.requires_approval ?? true) && (above == null || estimate > above));
  const ready = chosen.length > 0 && !tooMany && reason.trim() !== "" && (!needsApproval || (code.trim() !== "" && pin.length >= 4));

  async function submit() {
    setBusy(true);
    setError(null);
    try {
      const r = await api.post<{ return_number: string; refund_amount: string }>(`/api/v1/sales/orders/${sale.order_id}/returns`, {
        lines: chosen.map((x) => ({ line_id: x.l.line_id, quantity: x.q })),
        reason: reason.trim(),
        refund_method: method,
        refund_reference: reference.trim(),
        restock,
        approver_code: needsApproval ? code.trim() : undefined,
        approver_pin: needsApproval ? pin : undefined,
        idempotency_key: key,
      });
      toast.success(`${r.return_number}: refund ${rupees(r.refund_amount)} by ${METHOD_LABEL[method]}`);
      onClose();
      onDone();
    } catch (e) {
      if (e instanceof ApiError && e.code === "RETURN_NOT_APPROVED") setApprovalDemanded(true);
      setError(e instanceof ApiError ? e.message : "Could not record the return");
      setBusy(false);
    }
  }

  return (
    <Modal
      open
      width={620}
      onOpenChange={(o) => !o && onClose()}
      title={`Return items from ${sale.order_number}`}
      description={info && !info.returnable ? (info.window_open ? "This sale can't be returned." : "This sale is past the return window.") : "The refund is each line's share of what was paid."}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={busy || !ready || !info?.returnable} onClick={submit}>
            Refund about {rupees(estimate)}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <table className="w-full text-sm">
          <thead className="text-left text-xs text-muted-foreground">
            <tr>
              <th className="pb-1 font-medium">Item</th>
              <th className="pb-1 text-right font-medium">Can return</th>
              <th className="pb-1 text-right font-medium">Each</th>
              <th className="pb-1 text-right font-medium">Return</th>
            </tr>
          </thead>
          <tbody>
            {lines.map((l) => (
              <tr key={l.line_id} className="border-t">
                <td className="py-1.5">
                  {l.product_name}
                  <div className="text-xs text-muted-foreground">{l.sku}</div>
                </td>
                <td className="tabular py-1.5 text-right">{qty(l.returnable_quantity)}</td>
                <td className="tabular py-1.5 text-right">{rupees(l.unit_refund)}</td>
                <td className="py-1.5 text-right">
                  <Input
                    aria-label={`Quantity to return of ${l.product_name}`}
                    className="ml-auto w-20 text-right"
                    type="number"
                    min={0}
                    max={Number(l.returnable_quantity)}
                    step="1"
                    value={quantities[l.line_id] ?? ""}
                    placeholder="0"
                    onChange={(e) => setQuantities((q) => ({ ...q, [l.line_id]: e.target.value }))}
                  />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {tooMany && <p className="text-sm text-destructive">That&apos;s more than can still be returned.</p>}
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="ret-reason">Reason</Label>
          <Input id="ret-reason" value={reason} onChange={(e) => setReason(e.target.value)} placeholder="Wrong size, defective …" />
        </div>
        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ret-method">Refund by</Label>
            <select id="ret-method" value={method} onChange={(e) => setMethod(e.target.value)} className="h-9 rounded-lg border bg-card px-2 text-sm">
              <option value="cash">Cash</option>
              <option value="card">Card</option>
              <option value="upi">UPI</option>
              <option value="credit" disabled={!info || Number(info.credit_outstanding) <= 0}>
                Credit (only up to unpaid credit)
              </option>
            </select>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ret-ref">Reference (card/UPI)</Label>
            <Input id="ret-ref" value={reference} onChange={(e) => setReference(e.target.value)} />
          </div>
        </div>
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={restock} onChange={(e) => setRestock(e.target.checked)} />
          Put the items back in stock (turn off for damaged goods)
        </label>
        {needsApproval && <ApproverFields idPrefix="ret" code={code} pin={pin} onCode={setCode} onPin={setPin} />}
        {error && (
          <p className="text-sm text-destructive" role="alert">
            {error}
          </p>
        )}
      </div>
    </Modal>
  );
}

function CorrectDialog({ sale, payment, onClose, onDone }: { sale: SaleDetail; payment: SaleDetailPayment; onClose: () => void; onDone: (d: SaleDetail) => void }) {
  const options = ["cash", "card", "upi"].filter((m) => m !== payment.method);
  const [to, setTo] = useState(options[0]);
  const [reference, setReference] = useState("");
  const [reason, setReason] = useState("");
  const [code, setCode] = useState("");
  const [pin, setPin] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit() {
    setBusy(true);
    setError(null);
    try {
      const d = await api.post<SaleDetail>(`/api/v1/sales/orders/${sale.order_id}/payments/${payment.payment_id}/correct`, {
        to_method: to,
        to_reference: reference.trim(),
        reason: reason.trim(),
        approver_code: code.trim(),
        approver_pin: pin,
      });
      toast.success(`Payment moved to ${METHOD_LABEL[to]}`);
      onDone(d);
      onClose();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not correct the payment");
      setBusy(false);
    }
  }

  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={`Correct a ${METHOD_LABEL[payment.method]} payment of ${rupees(payment.amount)}`}
      description="For a payment recorded against the wrong method. The ledger moves the amount between the two accounts, and the change is kept on the sale and in the audit log."
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={busy || !reason.trim() || !code.trim() || pin.length < 4} onClick={submit}>
            Correct payment
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="cor-to">Actually paid by</Label>
            <select id="cor-to" value={to} onChange={(e) => setTo(e.target.value)} className="h-9 rounded-lg border bg-card px-2 text-sm">
              {options.map((m) => (
                <option key={m} value={m}>
                  {METHOD_LABEL[m]}
                </option>
              ))}
            </select>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="cor-ref">Reference (optional)</Label>
            <Input id="cor-ref" value={reference} onChange={(e) => setReference(e.target.value)} />
          </div>
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="cor-reason">Reason</Label>
          <Input id="cor-reason" value={reason} onChange={(e) => setReason(e.target.value)} placeholder="Rung up as cash, customer paid by card" />
        </div>
        <ApproverFields idPrefix="cor" code={code} pin={pin} onCode={setCode} onPin={setPin} />
        {error && (
          <p className="text-sm text-destructive" role="alert">
            {error}
          </p>
        )}
      </div>
    </Modal>
  );
}
