"use client";

// Collecting a credit sale from the sale itself (erp-core-go migrations/082):
// the sales list's payment status and "Record payment", and the sale page's
// credit panel. Payments go through POST /sales/orders/{id}/credit-payments,
// the same ledger posting as the customer page's "Record payment".
import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { PaymentStatus, SaleCreditState } from "@/lib/types";
import { PAYMENT_STATUS_LABEL, RECEIVABLE_METHOD_LABEL, dateTime, rupees } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Modal } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { selectClass } from "@/components/data/panel";
import { StatusChip } from "@/components/data/status-chip";
import { BankAccountSelect, defaultAccount, useBankAccounts } from "@/components/banking/bank-account-select";

/** The list's Payment cell: the status, and what is still owed. */
export function PaymentStatusCell({ status, pending }: { status?: PaymentStatus; pending?: string }) {
  if (!status) return <span className="text-muted-foreground">—</span>;
  return (
    <div className="flex flex-col items-start gap-0.5">
      <StatusChip status={status} label={PAYMENT_STATUS_LABEL[status] ?? status} />
      {Number(pending ?? 0) > 0 && <span className="tabular text-xs text-muted-foreground">Due {rupees(pending!)}</span>}
    </div>
  );
}

type Method = "cash" | "card" | "upi" | "bank_transfer" | "cheque";

type DialogProps = {
  orderId: string;
  orderNumber: string;
  pending: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onRecorded: (state: SaleCreditState) => void;
};

/** Record what a customer pays against one credit sale, up to what is
 * owed. Mounted only while open, so each opening starts fresh (amount =
 * what is due) with its own idempotency key. */
export function CreditPaymentDialog(props: DialogProps) {
  return props.open ? <CreditPaymentModal {...props} /> : null;
}

function CreditPaymentModal({
  orderId,
  orderNumber,
  pending,
  open,
  onOpenChange,
  onRecorded,
}: DialogProps) {
  const accounts = useBankAccounts();
  const [amount, setAmount] = useState(pending);
  const [method, setMethod] = useState<Method>("cash");
  const [reference, setReference] = useState("");
  const [bankAccountId, setBankAccountId] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // One key per dialog opening: a double click or a retry records once.
  const [key] = useState(() => `admin-credit-${Date.now()}-${Math.random().toString(16).slice(2)}`);
  const banked = method === "bank_transfer" || method === "cheque";
  const bankAccount = bankAccountId ?? defaultAccount(accounts)?.bank_account_id ?? "";
  const value = Number(amount);
  const valid = value > 0 && value <= Number(pending) + 0.001;

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!valid) return;
    setBusy(true);
    try {
      const st = await api.post<SaleCreditState>(`/api/v1/sales/orders/${orderId}/credit-payments`, {
        amount: value,
        method,
        reference_no: reference.trim(),
        ...(banked && bankAccount ? { bank_account_id: bankAccount } : {}),
        idempotency_key: key,
      });
      toast.success(Number(st.pending_amount) > 0 ? `Payment recorded · ${rupees(st.pending_amount)} still due` : "Payment recorded · fully paid");
      onRecorded(st);
      onOpenChange(false);
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : "Could not record the payment");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title="Record payment"
      description={`${orderNumber} · ${rupees(pending)} due`}
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button type="submit" form="credit-payment-form" disabled={busy || !valid}>
            {busy ? "Recording…" : "Record payment"}
          </Button>
        </>
      }
    >
      <form id="credit-payment-form" onSubmit={submit} className="flex flex-col gap-4">
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div className="flex flex-col gap-2">
            <Label htmlFor="credit-amount">Amount</Label>
            <Input
              id="credit-amount"
              type="number"
              min="0.01"
              step="0.01"
              max={pending}
              value={amount}
              onChange={(e) => setAmount(e.target.value)}
              autoFocus
            />
            {!valid && amount !== "" && <span className="text-xs text-destructive">Between {rupees("0.01")} and {rupees(pending)}</span>}
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="credit-method">Method</Label>
            <select id="credit-method" value={method} onChange={(e) => setMethod(e.target.value as Method)} className={selectClass}>
              {Object.entries(RECEIVABLE_METHOD_LABEL).map(([k, v]) => (
                <option key={k} value={k}>
                  {v}
                </option>
              ))}
            </select>
          </div>
        </div>
        {banked && accounts.length > 0 && (
          <div className="flex flex-col gap-2">
            <Label htmlFor="credit-bank">Into</Label>
            <BankAccountSelect id="credit-bank" accounts={accounts} value={bankAccount} onChange={setBankAccountId} allowNone className="w-full" />
          </div>
        )}
        <div className="flex flex-col gap-2">
          <Label htmlFor="credit-reference">Reference (optional)</Label>
          <Input
            id="credit-reference"
            value={reference}
            onChange={(e) => setReference(e.target.value)}
            placeholder={method === "cheque" ? "Cheque number" : method === "cash" ? "Receipt number" : "Transaction ID"}
          />
        </div>
      </form>
    </Modal>
  );
}

/** The sale page's credit panel: owed, paid, pending, and each payment. */
export function CreditCollection({ orderId, onChanged }: { orderId: string; onChanged?: () => void }) {
  const [state, setState] = useState<SaleCreditState | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [paying, setPaying] = useState(false);
  const load = useCallback(() => {
    api
      .get<SaleCreditState>(`/api/v1/sales/orders/${orderId}/credit-payments`)
      .then((s) => {
        setState(s);
        setError(null);
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load the credit payments"));
  }, [orderId]);
  useEffect(load, [load]);

  if (error) return <p className="text-sm text-destructive">{error}</p>;
  if (!state) return <p className="text-sm text-muted-foreground">Loading…</p>;
  const due = Number(state.pending_amount) > 0;
  return (
    <div className="flex flex-col gap-3 text-sm" data-testid="credit-collection">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <PaymentStatusCell status={state.payment_status} />
        {due && (
          <Button size="sm" onClick={() => setPaying(true)}>
            Record payment
          </Button>
        )}
      </div>
      <dl className="grid grid-cols-2 gap-x-4 gap-y-1">
        <dt className="text-muted-foreground">On credit</dt>
        <dd className="tabular text-right">{rupees(state.credit_amount)}</dd>
        <dt className="text-muted-foreground">Paid</dt>
        <dd className="tabular text-right">{rupees(state.credit_paid)}</dd>
        <dt className="font-semibold">Pending</dt>
        <dd className="tabular text-right font-semibold" data-testid="credit-pending">
          {rupees(state.pending_amount)}
        </dd>
        {state.due_date && (
          <>
            <dt className="text-muted-foreground">Due date</dt>
            <dd className="text-right">{state.due_date}</dd>
          </>
        )}
      </dl>
      {state.payments.length > 0 && (
        <ul className="flex flex-col gap-2 border-t pt-3">
          {state.payments.map((p) => (
            <li key={p.payment_id} className="flex items-start justify-between gap-2">
              <span>
                <span className={`font-medium ${p.reversed ? "line-through" : ""}`}>{RECEIVABLE_METHOD_LABEL[p.method] ?? p.method}</span>
                {p.reference_no && <span className="text-muted-foreground"> · {p.reference_no}</span>}
                {p.reversed && <span className="text-destructive"> · reversed</span>}
                <span className="block text-xs text-muted-foreground">
                  {dateTime(p.received_at)} · {p.recorded_by}
                  {p.terminal_name ? ` · ${p.terminal_name}` : " · back office"}
                </span>
              </span>
              <span className={`tabular ${p.reversed ? "line-through text-muted-foreground" : ""}`}>{rupees(p.amount)}</span>
            </li>
          ))}
        </ul>
      )}
      <CreditPaymentDialog
        orderId={state.order_id}
        orderNumber={state.order_number}
        pending={state.pending_amount}
        open={paying}
        onOpenChange={setPaying}
        onRecorded={(s) => {
          setState(s);
          onChanged?.();
        }}
      />
    </div>
  );
}
