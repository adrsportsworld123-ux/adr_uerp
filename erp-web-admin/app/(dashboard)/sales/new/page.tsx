"use client";

// New sale from the back office (Phase 14). Not a second sales engine: it
// opens a cart with channel "backoffice" and then calls exactly the till's
// endpoints (add line, quantity, discount, customer, checkout), so pricing,
// tax, promotions, credit limits, open-price rules and stock reservation are
// the same code and give the same totals to the paisa. Needs
// sales.backoffice; shifts don't apply to the back office.
//
// Promotions apply on the server as items go in (erp-core-go
// promotions.Reapply, best price per line), so each line shows its offer.
// "Discard sale" releases the cart (POST .../release): its stock goes back
// on sale at once instead of sitting reserved; leaving the page with items
// in the cart asks first.
import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { Ban, Loader2, Minus, Plus, Trash2, UserRound, X } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError, SEED_BRANCH_ID } from "@/lib/api-client";
import { Branch, CartOrder, Customer } from "@/lib/types";
import { METHOD_LABEL, qty, rupees } from "@/lib/format";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import { Button } from "@/components/ui/button";
import { Modal } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { PageHeader } from "@/components/data/page-header";
import { ProductPicker, VariantOption } from "@/components/data/product-picker";

type OpenPrice = { min: string; max: string; approval_above: string | null };
type Payment = { method: string; amount: string; reference: string };

const selectClass = "h-9 rounded-lg border bg-card px-2 text-sm";

function newKey() {
  return `admin-sale-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

export default function NewSalePage() {
  const router = useRouter();
  const { can } = usePermissions();
  const [branches, setBranches] = useState<Branch[]>([]);
  const [branchId, setBranchId] = useState(SEED_BRANCH_ID);
  const [cartKey, setCartKey] = useState(newKey); // a retried "open cart" reuses the same cart; a new one after a discard
  const [discarding, setDiscarding] = useState(false);
  const [order, setOrder] = useState<CartOrder | null>(null);
  const [picked, setPicked] = useState<VariantOption | null>(null);
  const [addQty, setAddQty] = useState("1");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Open price: the server answers OPEN_PRICE_REQUIRED with the range.
  const [openPrice, setOpenPrice] = useState<{ range: OpenPrice; suggested: string } | null>(null);
  const [price, setPrice] = useState("");
  const [approverCode, setApproverCode] = useState("");
  const [approverPin, setApproverPin] = useState("");
  // Customer, discount, payment.
  const [customerQuery, setCustomerQuery] = useState("");
  const [customerHits, setCustomerHits] = useState<Customer[]>([]);
  const [customer, setCustomer] = useState<Customer | null>(null);
  // A percent off, or a fixed rupee amount off the sale (₹100 off).
  const [discountMode, setDiscountMode] = useState<"percent" | "amount">("percent");
  const [discount, setDiscount] = useState("");
  const [discountReason, setDiscountReason] = useState("");
  const [payments, setPayments] = useState<Payment[]>([{ method: "cash", amount: "", reference: "" }]);

  useEffect(() => {
    api
      .get<{ branches: Branch[] }>("/api/v1/branches?status=active&scope=mine")
      .then((d) => {
        setBranches(d.branches);
        if (d.branches.length && !d.branches.some((b) => b.branch_id === SEED_BRANCH_ID)) setBranchId(d.branches[0].branch_id);
      })
      .catch(() => {});
  }, []);

  useEffect(() => {
    if (customerQuery.trim().length < 2) return; // shorter queries show no list (see render)
    const t = setTimeout(() => {
      api
        .get<{ customers: Customer[] }>(`/api/v1/customers?q=${encodeURIComponent(customerQuery.trim())}&limit=8&page=1`)
        .then((d) => setCustomerHits(d.customers))
        .catch(() => setCustomerHits([]));
    }, 250);
    return () => clearTimeout(t);
  }, [customerQuery]);

  const allowed = can("sales.backoffice");
  const oid = order?.order_id;
  const unfinished = !!order?.lines?.length;

  // Leaving with items in the cart would leave their stock reserved.
  useEffect(() => {
    if (!unfinished) return;
    const warn = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [unfinished]);

  const discard = () =>
    run(async () => {
      if (!oid) return;
      await api.post(`/api/v1/sales/orders/${oid}/release`, { reason: "Discarded from the back-office New sale page" });
      setDiscarding(false);
      setOrder(null);
      setCustomer(null);
      setPayments([{ method: "cash", amount: "", reference: "" }]);
      setDiscount("");
      setDiscountReason("");
      setCartKey(newKey());
      toast.success("Sale discarded — its items are back on sale");
    });

  async function run<T>(fn: () => Promise<T>): Promise<T | undefined> {
    setBusy(true);
    setError(null);
    try {
      return await fn();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Something went wrong");
      return undefined;
    } finally {
      setBusy(false);
    }
  }

  async function ensureCart(): Promise<string> {
    if (oid) return oid;
    const o = await api.post<CartOrder>("/api/v1/sales/orders", { branch_id: branchId, channel: "backoffice", idempotency_key: cartKey });
    setOrder(o);
    return o.order_id;
  }

  async function addItem() {
    if (!picked) return;
    const q = Number(addQty);
    if (!(q > 0)) {
      setError("Enter a quantity above zero");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const id = await ensureCart();
      const body: Record<string, unknown> = { variant_id: picked.variantId, quantity: q };
      if (openPrice) {
        body.unit_price = Number(price);
        if (approverCode) {
          body.approver_code = approverCode;
          body.approver_pin = approverPin;
        }
      }
      const o = await api.post<CartOrder>(`/api/v1/sales/orders/${id}/lines`, body);
      setOrder(o);
      setPicked(null);
      setAddQty("1");
      setOpenPrice(null);
      setPrice("");
      setApproverCode("");
      setApproverPin("");
    } catch (e) {
      if (e instanceof ApiError && e.code === "OPEN_PRICE_REQUIRED" && e.details) {
        const d = e.details as { open_price: OpenPrice; suggested_price: string };
        setOpenPrice({ range: d.open_price, suggested: d.suggested_price });
        setPrice(d.suggested_price);
      } else {
        setError(e instanceof ApiError ? e.message : "Could not add the item");
      }
    } finally {
      setBusy(false);
    }
  }

  const step = (lineId: string, quantity: number) =>
    run(async () => {
      if (!oid) return;
      setOrder(
        quantity <= 0
          ? await api.delete<CartOrder>(`/api/v1/sales/orders/${oid}/lines/${lineId}`)
          : await api.patch<CartOrder>(`/api/v1/sales/orders/${oid}/lines/${lineId}`, { quantity }),
      );
    });

  const attach = (c: Customer) =>
    run(async () => {
      const id = await ensureCart();
      await api.post(`/api/v1/sales/orders/${id}/customer`, { customer_id: c.customer_id });
      setCustomer(c);
      setCustomerQuery("");
      setCustomerHits([]);
      setOrder(await api.get<CartOrder>(`/api/v1/sales/orders/${id}`));
    });

  const applyDiscount = () =>
    run(async () => {
      if (!oid) return;
      const value = Number(discount);
      const body: Record<string, unknown> =
        discountMode === "amount"
          ? { type: "manual", mode: "amount", amount: value, reason: discountReason.trim() }
          : { type: "manual", value, reason: discountReason.trim() };
      if (needsDiscountApproval) {
        body.authorized_by_code = approverCode;
        body.authorized_pin = approverPin;
      }
      setOrder(await api.post<CartOrder>(`/api/v1/sales/orders/${oid}/discounts`, body));
      setDiscount("");
      setDiscountReason("");
      toast.success(discountMode === "amount" ? `Discount of ${rupees(value)} applied` : `Discount of ${value}% applied`);
    });

  const total = Number(order?.grand_total ?? 0);
  const paid = payments.reduce((s, p) => s + (Number(p.amount) || 0), 0);
  const remaining = Math.round((total - paid) * 100) / 100;
  const creditWithoutCustomer = payments.some((p) => p.method === "credit" && Number(p.amount) > 0) && !customer;

  const checkout = () =>
    run(async () => {
      if (!oid) return;
      const body = {
        payments: payments
          .filter((p) => Number(p.amount) > 0)
          .map((p) => ({ method: p.method, amount: Number(p.amount), reference: p.reference.trim() || undefined })),
      };
      const done = await api.post<CartOrder>(`/api/v1/sales/orders/${oid}/checkout`, body);
      toast.success(`Sale ${done.order_number} completed`);
      router.push(`/sales/${oid}`);
    });

  const discountValue = Number(discount);
  // What the server measures a discount against (and the approval tier by):
  // the lines' price × quantity before any discount. Display math only.
  const discountBase = (order?.lines ?? []).reduce((s, l) => s + Number(l.unit_price) * Number(l.quantity), 0);
  const discountPercent = discountMode === "amount" ? (discountBase > 0 ? (discountValue / discountBase) * 100 : 0) : discountValue;
  const needsDiscountApproval = discountPercent > 5;
  const openPriceNeedsApproval = openPrice?.range.approval_above != null && Number(price) > Number(openPrice.range.approval_above);

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="New sale"
        description="Rung up in the back office with the till's own rules: prices, tax, promotions, credit limits and stock are the same code."
        actions={
          oid ? (
            <Button variant="outline" disabled={busy} onClick={() => setDiscarding(true)}>
              <Ban /> Discard sale
            </Button>
          ) : undefined
        }
      />
      {discarding && (
        <Modal
          open
          onOpenChange={(o) => !o && setDiscarding(false)}
          title="Discard this sale?"
          description="Nothing is sold: the items go back on sale and the cart is kept only as a released record."
          footer={
            <>
              <Button variant="outline" onClick={() => setDiscarding(false)}>
                Keep selling
              </Button>
              <Button variant="destructive" disabled={busy} onClick={discard}>
                Discard sale
              </Button>
            </>
          }
        >
          {null}
        </Modal>
      )}
      {!allowed && <p className="text-sm text-muted-foreground">{missingPermissionHint("sales.backoffice")}</p>}

      <div className="grid gap-5 xl:grid-cols-[minmax(0,1fr)_380px]">
        <div className="flex min-w-0 flex-col gap-4">
          <div className="flex flex-wrap items-end gap-3 rounded-xl border bg-card p-4">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="sale-branch">Branch</Label>
              <select id="sale-branch" value={branchId} disabled={!!order} onChange={(e) => setBranchId(e.target.value)} className={selectClass}>
                {branches.map((b) => (
                  <option key={b.branch_id} value={b.branch_id}>
                    {b.name}
                  </option>
                ))}
              </select>
            </div>
            <div className="flex min-w-72 flex-1 flex-col gap-1.5">
              <span className="text-sm font-medium">Product</span>
              <ProductPicker
                value={picked}
                onChange={(o) => {
                  setPicked(o);
                  setOpenPrice(null);
                }}
                placeholder="Search by name, SKU or barcode"
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="sale-qty">Qty</Label>
              <Input id="sale-qty" className="w-20" type="number" min={0} step="1" value={addQty} onChange={(e) => setAddQty(e.target.value)} />
            </div>
            <Button disabled={!allowed || !picked || busy || (!!openPrice && (!price || (openPriceNeedsApproval && (!approverCode || approverPin.length < 4))))} onClick={addItem}>
              {busy ? <Loader2 className="animate-spin" /> : <Plus />} Add
            </Button>
            {openPrice && (
              <div className="flex w-full flex-wrap items-end gap-3 rounded-lg bg-muted/50 p-3">
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="sale-open-price">
                    Price ({rupees(openPrice.range.min)} to {rupees(openPrice.range.max)})
                  </Label>
                  <Input id="sale-open-price" className="w-32" type="number" step="0.01" value={price} onChange={(e) => setPrice(e.target.value)} />
                </div>
                {openPriceNeedsApproval && (
                  <>
                    <div className="flex flex-col gap-1.5">
                      <Label htmlFor="op-code">Manager&apos;s code</Label>
                      <Input id="op-code" className="w-32" value={approverCode} onChange={(e) => setApproverCode(e.target.value.toUpperCase())} />
                    </div>
                    <div className="flex flex-col gap-1.5">
                      <Label htmlFor="op-pin">PIN</Label>
                      <Input id="op-pin" className="w-24" type="password" inputMode="numeric" value={approverPin} onChange={(e) => setApproverPin(e.target.value.replace(/\D/g, ""))} />
                    </div>
                  </>
                )}
                <p className="text-xs text-muted-foreground">
                  Open-price item{openPrice.range.approval_above ? `; above ${rupees(openPrice.range.approval_above)} a manager approves` : ""}.
                </p>
              </div>
            )}
          </div>

          <div className="rounded-xl border bg-card">
            <table className="w-full text-sm">
              <thead className="border-b text-left text-xs text-muted-foreground">
                <tr>
                  <th className="px-4 py-2 font-medium">Item</th>
                  <th className="px-2 py-2 text-center font-medium">Qty</th>
                  <th className="px-2 py-2 text-right font-medium">Price</th>
                  <th className="px-2 py-2 text-right font-medium">Tax</th>
                  <th className="px-4 py-2 text-right font-medium">Total</th>
                </tr>
              </thead>
              <tbody>
                {(order?.lines ?? []).map((l) => (
                  <tr key={l.line_id} className="border-b last:border-0">
                    <td className="px-4 py-2">
                      <div className="font-medium">{l.product_name}</div>
                      <div className="text-xs text-muted-foreground">
                        {l.sku}
                        {l.open_price ? " · open price" : ""}
                      </div>
                      {Number(l.discount_amount) > 0 && (
                        <div className="text-xs font-medium text-pay" data-testid="line-discount">
                          Offer −{rupees(l.discount_amount)}
                        </div>
                      )}
                    </td>
                    <td className="px-2 py-2">
                      <div className="flex items-center justify-center gap-1">
                        <Button variant="ghost" size="icon-sm" aria-label={`One less ${l.product_name}`} onClick={() => step(l.line_id, Number(l.quantity) - 1)}>
                          {Number(l.quantity) <= 1 ? <Trash2 /> : <Minus />}
                        </Button>
                        <span className="tabular w-8 text-center">{qty(l.quantity)}</span>
                        <Button variant="ghost" size="icon-sm" aria-label={`One more ${l.product_name}`} onClick={() => step(l.line_id, Number(l.quantity) + 1)}>
                          <Plus />
                        </Button>
                      </div>
                    </td>
                    <td className="tabular px-2 py-2 text-right">{rupees(l.unit_price)}</td>
                    <td className="tabular px-2 py-2 text-right">{rupees(l.tax_amount)}</td>
                    <td className="tabular px-4 py-2 text-right font-medium">{rupees(l.line_total)}</td>
                  </tr>
                ))}
                {!order?.lines?.length && (
                  <tr>
                    <td colSpan={5} className="px-4 py-8 text-center text-muted-foreground">
                      Add a product to start the sale.
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
          {error && (
            <p className="text-sm text-destructive" role="alert">
              {error}
            </p>
          )}
        </div>

        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-2 rounded-xl border bg-card p-4">
            <span className="text-sm font-semibold">Customer</span>
            {customer ? (
              <div className="flex items-center justify-between gap-2 text-sm">
                <span className="flex items-center gap-2">
                  <UserRound className="size-4" /> {customer.name} · {customer.phone}
                </span>
              </div>
            ) : (
              <>
                <Input
                  aria-label="Find a customer"
                  placeholder="Name or phone (needed for credit)"
                  value={customerQuery}
                  disabled={!allowed}
                  onChange={(e) => setCustomerQuery(e.target.value)}
                />
                {customerQuery.trim().length >= 2 && customerHits.length > 0 && (
                  <ul className="rounded-lg border text-sm">
                    {customerHits.map((c) => (
                      <li key={c.customer_id}>
                        <button type="button" className="w-full px-3 py-1.5 text-left hover:bg-muted" onClick={() => attach(c)}>
                          {c.name} · {c.phone}
                        </button>
                      </li>
                    ))}
                  </ul>
                )}
              </>
            )}
          </div>

          <div className="flex flex-col gap-2 rounded-xl border bg-card p-4">
            <div className="flex items-center justify-between gap-2">
              <span className="text-sm font-semibold">Discount</span>
              <div className="flex gap-1" role="group" aria-label="Discount type">
                {(["percent", "amount"] as const).map((m) => (
                  <Button
                    key={m}
                    type="button"
                    size="sm"
                    variant={discountMode === m ? "default" : "outline"}
                    aria-pressed={discountMode === m}
                    onClick={() => {
                      setDiscountMode(m);
                      setDiscount("");
                    }}
                  >
                    {m === "percent" ? "%" : "₹"}
                  </Button>
                ))}
              </div>
            </div>
            <div className="flex gap-2">
              {discountMode === "percent" ? (
                <Input aria-label="Discount percent" className="w-24" type="number" min={0} max={25} step="0.5" placeholder="%" value={discount} onChange={(e) => setDiscount(e.target.value)} />
              ) : (
                <Input aria-label="Discount amount" className="w-28" type="number" min={0} step="0.01" placeholder="₹" value={discount} onChange={(e) => setDiscount(e.target.value)} />
              )}
              <Input aria-label="Discount reason" placeholder="Reason" value={discountReason} onChange={(e) => setDiscountReason(e.target.value)} />
            </div>
            {discountMode === "amount" && discountValue > 0 && discountBase > 0 && (
              <p className="text-xs text-muted-foreground" data-testid="discount-equivalent">
                {discountPercent.toFixed(2)}% of the sale
              </p>
            )}
            {needsDiscountApproval && (
              <div className="grid grid-cols-2 gap-2">
                <Input aria-label="Approver employee code" placeholder="Manager's code" value={approverCode} onChange={(e) => setApproverCode(e.target.value.toUpperCase())} />
                <Input aria-label="Approver PIN" placeholder="PIN" type="password" inputMode="numeric" value={approverPin} onChange={(e) => setApproverPin(e.target.value.replace(/\D/g, ""))} />
              </div>
            )}
            <Button variant="outline" disabled={!oid || !(discountValue > 0) || busy || (needsDiscountApproval && (!approverCode || approverPin.length < 4))} onClick={applyDiscount}>
              Apply discount
            </Button>
            <p className="text-xs text-muted-foreground">
              Up to 5% needs no approval; above that a manager approves, up to 25%. A fixed amount counts as the percent of the sale it comes to.
            </p>
          </div>

          <div className="flex flex-col gap-3 rounded-xl border bg-card p-4">
            <dl className="grid grid-cols-2 gap-y-1 text-sm">
              <dt className="text-muted-foreground">Subtotal</dt>
              <dd className="tabular text-right">{rupees(order?.subtotal ?? 0)}</dd>
              <dt className="text-muted-foreground">Discount</dt>
              <dd className="tabular text-right">{Number(order?.discount_total ?? 0) ? `−${rupees(order!.discount_total)}` : "—"}</dd>
              <dt className="text-muted-foreground">Tax</dt>
              <dd className="tabular text-right">{rupees(order?.tax_total ?? 0)}</dd>
              <dt className="font-semibold">Total</dt>
              <dd className="tabular text-right text-xl font-semibold" data-testid="sale-total">
                {rupees(total)}
              </dd>
            </dl>
            <span className="text-sm font-semibold">Payment</span>
            {payments.map((p, i) => (
              <div key={i} className="flex flex-wrap gap-2">
                <select aria-label={`Payment ${i + 1} method`} value={p.method} onChange={(e) => setPayments((ps) => ps.map((x, j) => (j === i ? { ...x, method: e.target.value } : x)))} className={selectClass}>
                  {Object.entries(METHOD_LABEL).map(([k, v]) => (
                    <option key={k} value={k}>
                      {v}
                    </option>
                  ))}
                </select>
                <Input
                  aria-label={`Payment ${i + 1} amount`}
                  className="w-28"
                  type="number"
                  step="0.01"
                  value={p.amount}
                  onChange={(e) => setPayments((ps) => ps.map((x, j) => (j === i ? { ...x, amount: e.target.value } : x)))}
                />
                {(p.method === "card" || p.method === "upi") && (
                  <Input
                    aria-label={`Payment ${i + 1} reference`}
                    className="w-32"
                    placeholder="Reference"
                    value={p.reference}
                    onChange={(e) => setPayments((ps) => ps.map((x, j) => (j === i ? { ...x, reference: e.target.value } : x)))}
                  />
                )}
                {payments.length > 1 && (
                  <Button variant="ghost" size="icon-sm" aria-label={`Remove payment ${i + 1}`} onClick={() => setPayments((ps) => ps.filter((_, j) => j !== i))}>
                    <X />
                  </Button>
                )}
              </div>
            ))}
            <div className="flex flex-wrap gap-2">
              <Button
                variant="outline"
                size="sm"
                disabled={remaining <= 0}
                onClick={() =>
                  setPayments((ps) => {
                    const empty = ps.findIndex((x) => !Number(x.amount));
                    if (empty >= 0) return ps.map((x, j) => (j === empty ? { ...x, amount: remaining.toFixed(2) } : x));
                    return [...ps, { method: "card", amount: remaining.toFixed(2), reference: "" }];
                  })
                }
              >
                Fill remaining {rupees(Math.max(remaining, 0))}
              </Button>
              <Button variant="ghost" size="sm" onClick={() => setPayments((ps) => [...ps, { method: "upi", amount: "", reference: "" }])}>
                <Plus /> Split
              </Button>
            </div>
            {creditWithoutCustomer && <p className="text-xs text-destructive">A credit payment needs a customer.</p>}
            <Button size="lg" disabled={!allowed || !order?.lines?.length || busy || Math.abs(remaining) > 0.004 || creditWithoutCustomer} onClick={checkout}>
              Complete sale · {rupees(total)}
            </Button>
          </div>
        </div>
      </div>
    </div>
  );
}
