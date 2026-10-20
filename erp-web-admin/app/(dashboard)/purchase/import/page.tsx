"use client";

// Import a supplier's purchase invoice from the CSV template
// (erp-core-go internal/purchase/import.go): fill the template, upload,
// check the preview — which lines restock an existing product, which create
// a new one, and that the lines add up to the printed total — then import.
// One click books the GRN (stock in, on the invoice date) and the bill
// (payable, GST input) together, or nothing at all.
//
// Against a purchase order (?po= from the PO page, or the picker;
// erp-core-go §3.74) each line is compared with the PO: on the order, over
// what is pending, or not on it at all. Differences are received as
// invoiced once "Accept the differences" is ticked, and what is still
// pending can be closed short in the same step.
import { Suspense, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { Download, FileUp, Loader2 } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Branch, PurchaseOrderSummary, Supplier } from "@/lib/types";
import { rupees } from "@/lib/format";
import { saveBlob } from "@/components/data/document-actions";
import { PageHeader } from "@/components/data/page-header";
import { SearchSelect } from "@/components/data/search-select";
import { PO_TONE, StatusChip } from "@/components/data/status-chip";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

import { currencySymbol } from "@/lib/currency";
type Suggestion = { variant_id: string; name: string; sku: string; mrp: string; score: number; on_po?: boolean };
type PreviewLine = {
  row: number;
  item_name: string;
  quantity: string;
  unit_cost: string;
  gst_rate: string;
  line_cost: string;
  line_tax: string;
  action: "add_stock" | "create";
  matched_by?: string;
  product_name?: string;
  sku?: string;
  on_hand?: string;
  current_mrp?: string;
  new_product?: { name: string; sku: string; category: string; brand: string; tax_slab: string; mrp: string; selling_price: string };
  suggestions: Suggestion[];
  warnings: string[];
  errors: string[];
  po_status?: "on_po" | "over" | "unplanned";
  po_ordered?: string;
  po_pending?: string;
};
type POLine = {
  po_line_id: string;
  variant_id: string;
  product_name: string;
  sku: string;
  ordered: string;
  received: string;
  pending: string;
  invoiced: string;
  excess: string;
  pending_after: string;
  status: "full" | "short" | "over" | "not_delivered" | "done";
};
type POPreview = {
  po_id: string;
  po_number: string;
  status: string;
  lines: POLine[];
  unplanned: number;
  over: number;
  short: number;
  variances: boolean;
  close_short: boolean;
};
type Preview = {
  supplier: string;
  branch: string;
  purchase_order?: POPreview;
  lines: PreviewLine[];
  new_brands: string[];
  errors: string[];
  ready: boolean;
  summary: {
    lines: number;
    quantity: string;
    subtotal: string;
    tax: string;
    freight: string;
    other_charges: string;
    computed_total: string;
    invoice_total: string | null;
    round_off: string;
    add_stock: number;
    create: number;
  };
};
type Result = {
  grn_id: string;
  grn_number: string;
  bill_id: string;
  bill_number: string;
  grand_total: string;
  stock_added: number;
  products_created: number;
  brands_created: string[] | null;
  po_number?: string;
  po_status?: string;
};

const today = () => new Date().toLocaleDateString("en-CA");

const LINE_PO: Record<string, { label: string; tone: "ok" | "warn" | "info" }> = {
  on_po: { label: "On the PO", tone: "ok" },
  over: { label: "Over the PO", tone: "warn" },
  unplanned: { label: "Not on the PO", tone: "info" },
};
const PO_LINE_STATUS: Record<POLine["status"], { label: string; tone: "ok" | "warn" | "bad" | "muted" | "info" }> = {
  full: { label: "Received in full", tone: "ok" },
  short: { label: "Short", tone: "warn" },
  over: { label: "Over", tone: "warn" },
  not_delivered: { label: "Not delivered", tone: "bad" },
  done: { label: "Nothing pending", tone: "muted" },
};

export default function ImportInvoicePage() {
  return (
    <Suspense>
      <ImportInvoice />
    </Suspense>
  );
}

function ImportInvoice() {
  const initialPo = useSearchParams().get("po") ?? "";
  const [poId, setPoId] = useState(initialPo);
  const [orders, setOrders] = useState<PurchaseOrderSummary[]>([]);
  const [acceptVariances, setAcceptVariances] = useState(false);
  const [closeShort, setCloseShort] = useState(false);
  const [closeReason, setCloseReason] = useState("");
  const [suppliers, setSuppliers] = useState<Supplier[]>([]);
  const [branches, setBranches] = useState<Branch[]>([]);
  const [supplierId, setSupplierId] = useState("");
  const [branchId, setBranchId] = useState("");
  const [invoiceNumber, setInvoiceNumber] = useState("");
  const [invoiceDate, setInvoiceDate] = useState(today());
  const [dueDate, setDueDate] = useState("");
  const [freight, setFreight] = useState("");
  const [other, setOther] = useState("");
  const [total, setTotal] = useState("");
  const [csv, setCsv] = useState("");
  const [fileName, setFileName] = useState("");
  const [matches, setMatches] = useState<Record<string, string>>({});
  const [preview, setPreview] = useState<Preview | null>(null);
  const [busy, setBusy] = useState<"preview" | "commit" | null>(null);
  const [result, setResult] = useState<Result | null>(null);

  useEffect(() => {
    api.get<{ suppliers: Supplier[] }>("/api/v1/suppliers").then((d) => setSuppliers(d.suppliers)).catch(() => {});
    api
      .get<{ branches: Branch[] }>("/api/v1/branches?scope=mine")
      .then((d) => {
        setBranches(d.branches);
        if (d.branches.length === 1) setBranchId(d.branches[0].branch_id);
      })
      .catch(() => {});
    api
      .get<{ purchase_orders: PurchaseOrderSummary[] }>("/api/v1/purchase/orders?status=pending&limit=200")
      .then((d) => {
        setOrders(d.purchase_orders);
        const linked = d.purchase_orders.find((o) => o.po_id === initialPo);
        if (linked) pickPO(linked);
      })
      .catch(() => {});
    // eslint-disable-next-line react-hooks/exhaustive-deps -- once, for the ?po= link
  }, []);

  // A PO brings its supplier and branch with it.
  const po = orders.find((o) => o.po_id === poId);
  function pickPO(o: PurchaseOrderSummary | undefined) {
    if (!o) return;
    setSupplierId(o.supplier_id);
    setBranchId(o.branch_id);
  }

  const body = useMemo(
    () => ({
      supplier_id: supplierId,
      branch_id: branchId,
      invoice_number: invoiceNumber.trim(),
      invoice_date: invoiceDate,
      due_date: dueDate || undefined,
      freight_amount: Number(freight) || 0,
      other_charges: Number(other) || 0,
      invoice_total: total.trim() === "" ? undefined : Number(total),
      csv,
      matches,
      purchase_order_id: poId || undefined,
      accept_variances: poId ? acceptVariances : undefined,
      close_short: poId ? closeShort : undefined,
      close_reason: poId && closeShort ? closeReason.trim() : undefined,
    }),
    [supplierId, branchId, invoiceNumber, invoiceDate, dueDate, freight, other, total, csv, matches, poId, acceptVariances, closeShort, closeReason]
  );

  // A preview is only shown while the inputs it was made from are unchanged
  // (the PO's accept / close choices re-run it instead, see below).
  const inputKey = JSON.stringify([supplierId, branchId, invoiceNumber, invoiceDate, dueDate, freight, other, total, csv, poId]);
  const [previewKey, setPreviewKey] = useState("");

  async function template(sample: boolean) {
    try {
      const { blob, filename } = await api.download(`/api/v1/purchase/imports/template${sample ? "?sample=1" : ""}`);
      saveBlob(blob, filename);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not download the template");
    }
  }

  function readFile(f: File | undefined) {
    if (!f) return;
    if (f.size > 1024 * 1024) {
      toast.error("The file is larger than 1 MB — split the invoice");
      return;
    }
    const reader = new FileReader();
    reader.onload = () => {
      setCsv(String(reader.result ?? ""));
      setFileName(f.name);
      setMatches({});
      setResult(null);
    };
    reader.readAsText(f);
  }

  async function runPreview(nextMatches = matches) {
    setBusy("preview");
    try {
      setPreview(await api.post<Preview>("/api/v1/purchase/imports/preview", { ...body, matches: nextMatches }));
      setPreviewKey(inputKey);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not read the invoice");
    } finally {
      setBusy(null);
    }
  }

  // Ticking a PO choice previews again with it, so "ready" follows.
  async function poChoice(next: { accept?: boolean; close?: boolean }) {
    if (next.accept !== undefined) setAcceptVariances(next.accept);
    if (next.close !== undefined) setCloseShort(next.close);
    if (!preview) return;
    setBusy("preview");
    try {
      setPreview(
        await api.post<Preview>("/api/v1/purchase/imports/preview", {
          ...body,
          accept_variances: next.accept ?? acceptVariances,
          close_short: next.close ?? closeShort,
          close_reason: (next.close ?? closeShort) ? closeReason.trim() : undefined,
        })
      );
      setPreviewKey(inputKey);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not read the invoice");
    } finally {
      setBusy(null);
    }
  }

  function choose(row: number, value: string) {
    const next = { ...matches };
    if (value) next[String(row)] = value;
    else delete next[String(row)];
    setMatches(next);
    void runPreview(next);
  }

  async function commit() {
    setBusy("commit");
    try {
      const r = await api.post<Result>("/api/v1/purchase/imports/commit", body);
      setResult(r);
      setPreview(null);
      toast.success(`Invoice ${invoiceNumber} imported — ${r.stock_added} restocked, ${r.products_created} new product(s)`);
      if (r.po_number) setOrders((os) => os.filter((o) => o.po_id !== poId || r.po_status === "partially_received"));
    } catch (e) {
      const details = e instanceof ApiError ? (e.details as { preview?: Preview } | undefined) : undefined;
      if (details?.preview) {
        setPreview(details.preview);
        setPreviewKey(inputKey);
      }
      toast.error(e instanceof ApiError ? e.message : "Could not import the invoice");
    } finally {
      setBusy(null);
    }
  }

  const canPreview = (poId || (supplierId && branchId)) && invoiceNumber.trim() && invoiceDate && csv;
  const shown = preview && previewKey === inputKey ? preview : null;
  const s = shown?.summary;
  const sp = shown?.purchase_order;

  return (
    <div className="space-y-6">
      <PageHeader
        title="Import Purchase Invoice"
        description="Fill the template with the invoice's lines, upload it, check the preview and import. Existing products are restocked; new ones are created with a barcode."
        actions={
          <>
            <Button variant="outline" onClick={() => template(false)}>
              <Download aria-hidden /> Template
            </Button>
            <Button variant="outline" onClick={() => template(true)}>
              <Download aria-hidden /> Filled example
            </Button>
          </>
        }
      />

      {result && (
        <Card className="border-pay/40">
          <CardHeader>
            <CardTitle>Imported</CardTitle>
            <CardDescription>
              {result.stock_added} line(s) restocked existing products, {result.products_created} new product(s) created
              {result.brands_created?.length ? `, new brands: ${result.brands_created.join(", ")}` : ""}. Bill total {rupees(result.grand_total)}.
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-wrap gap-2">
            <Link className="text-primary hover:underline" href={`/purchase/grn/${result.grn_id}`}>
              Goods receipt {result.grn_number}
            </Link>
            <span aria-hidden>·</span>
            <Link className="text-primary hover:underline" href={`/purchase/bills/${result.bill_id}`}>
              Purchase bill {result.bill_number}
            </Link>
            {result.po_number && (
              <>
                <span aria-hidden>·</span>
                <Link className="text-primary hover:underline" href={`/purchase/orders/${poId}`}>
                  Purchase order {result.po_number}
                </Link>
                {result.po_status && <StatusChip status={result.po_status} tone={PO_TONE[result.po_status as keyof typeof PO_TONE]} />}
              </>
            )}
          </CardContent>
        </Card>
      )}

      <Card>
        <CardHeader>
          <CardTitle>Invoice</CardTitle>
          <CardDescription>As printed on the supplier&apos;s invoice. The total is checked against the lines; a difference under {currencySymbol()}1 is booked as round-off.</CardDescription>
        </CardHeader>
        <CardContent className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <div className="flex flex-col gap-2 sm:col-span-2">
            <Label htmlFor="imp-po">Against purchase order</Label>
            <SearchSelect
              id="imp-po"
              value={poId}
              onChange={(v) => {
                setPoId(v);
                pickPO(orders.find((o) => o.po_id === v));
                setAcceptVariances(false);
                setCloseShort(false);
                setMatches({});
              }}
              noneLabel="None — a direct purchase"
              placeholder="None — a direct purchase"
              options={[
                ...orders.map((o) => ({ value: o.po_id, label: `${o.po_number} · ${o.supplier_name}`, hint: `${o.received_qty} of ${o.ordered_qty} received` })),
                ...(poId && !po ? [{ value: poId, label: "The purchase order from the link" }] : []),
              ]}
            />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="imp-supplier">Supplier *</Label>
            <SearchSelect id="imp-supplier" value={supplierId} onChange={setSupplierId} placeholder="Choose a supplier" options={suppliers.map((x) => ({ value: x.supplier_id, label: x.name, hint: x.gstin }))} />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="imp-branch">Receiving branch *</Label>
            <SearchSelect id="imp-branch" value={branchId} onChange={setBranchId} placeholder="Choose a branch" options={branches.map((b) => ({ value: b.branch_id, label: b.name }))} />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="imp-no">Invoice number *</Label>
            <Input id="imp-no" value={invoiceNumber} onChange={(e) => setInvoiceNumber(e.target.value)} placeholder="SC/SL/26-27/4900" />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="imp-date">Invoice date *</Label>
            <Input id="imp-date" type="date" max={today()} value={invoiceDate} onChange={(e) => setInvoiceDate(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="imp-total">Invoice total ({currencySymbol()})</Label>
            <Input id="imp-total" inputMode="decimal" value={total} onChange={(e) => setTotal(e.target.value)} placeholder="26903" />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="imp-freight">Freight ({currencySymbol()})</Label>
            <Input id="imp-freight" inputMode="decimal" value={freight} onChange={(e) => setFreight(e.target.value)} placeholder="0" />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="imp-other">Other charges ({currencySymbol()})</Label>
            <Input id="imp-other" inputMode="decimal" value={other} onChange={(e) => setOther(e.target.value)} placeholder="0" />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="imp-due">Due date</Label>
            <Input id="imp-due" type="date" value={dueDate} onChange={(e) => setDueDate(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2 sm:col-span-2">
            <Label htmlFor="imp-file">Invoice lines (CSV from the template) *</Label>
            <Input id="imp-file" type="file" accept=".csv,text/csv" onChange={(e) => readFile(e.target.files?.[0])} />
            {fileName && <span className="text-xs text-muted-foreground">{fileName}</span>}
          </div>
          <div className="flex items-end sm:col-span-2">
            <Button onClick={() => runPreview()} disabled={!canPreview || busy != null}>
              {busy === "preview" ? <Loader2 className="animate-spin" aria-hidden /> : <FileUp aria-hidden />}
              Preview
            </Button>
          </div>
        </CardContent>
      </Card>

      {shown && s && (
        <>
          <Card>
            <CardContent className="flex flex-wrap items-center gap-x-8 gap-y-3 pt-6 text-sm">
              <div>
                <div className="text-muted-foreground">Lines</div>
                <div className="font-medium">
                  {s.lines} ({s.quantity} pcs) · {s.add_stock} restock · {s.create} new
                </div>
              </div>
              <div>
                <div className="text-muted-foreground">Taxable + GST</div>
                <div className="font-mono">
                  {rupees(s.subtotal)} + {rupees(s.tax)}
                </div>
              </div>
              <div>
                <div className="text-muted-foreground">Lines total</div>
                <div className="font-mono font-semibold">{rupees(s.computed_total)}</div>
              </div>
              {s.invoice_total && (
                <div>
                  <div className="text-muted-foreground">Invoice total · round-off</div>
                  <div className="font-mono">
                    {rupees(s.invoice_total)} · {rupees(s.round_off)}
                  </div>
                </div>
              )}
              {shown.new_brands.length > 0 && (
                <div>
                  <div className="text-muted-foreground">New brands</div>
                  <div>{shown.new_brands.join(", ")}</div>
                </div>
              )}
              <div className="ml-auto">
                <Button onClick={commit} disabled={!shown.ready || busy != null}>
                  {busy === "commit" && <Loader2 className="animate-spin" aria-hidden />}
                  Import invoice
                </Button>
              </div>
            </CardContent>
            {shown.errors.length > 0 && (
              <CardContent>
                <ul className="list-disc space-y-1 pl-5 text-sm text-destructive">
                  {shown.errors.map((e) => (
                    <li key={e}>{e}</li>
                  ))}
                </ul>
              </CardContent>
            )}
          </Card>

          {sp && (
            <Card>
              <CardHeader>
                <CardTitle>Against {sp.po_number}</CardTitle>
                <CardDescription>
                  What was ordered and what this invoice brings.
                  {sp.unplanned > 0 && ` ${sp.unplanned} item(s) aren't on the order.`}
                  {sp.over > 0 && ` ${sp.over} line(s) are more than is pending.`}
                  {sp.short > 0 && ` ${sp.short} item(s) will still be pending.`}
                </CardDescription>
              </CardHeader>
              <CardContent className="space-y-4">
                <div className="overflow-x-auto">
                  <table className="w-full min-w-[560px] text-sm" data-testid="import-po-lines">
                    <thead className="text-left text-xs text-muted-foreground">
                      <tr>
                        <th className="pb-1 font-medium">Ordered item</th>
                        <th className="pb-1 text-right font-medium">Ordered</th>
                        <th className="pb-1 text-right font-medium">Pending</th>
                        <th className="pb-1 text-right font-medium">On this invoice</th>
                        <th className="pb-1 text-right font-medium">Pending after</th>
                        <th className="pb-1 pl-4 font-medium">Status</th>
                      </tr>
                    </thead>
                    <tbody>
                      {sp.lines.map((l) => (
                        <tr key={l.po_line_id} className="border-t">
                          <td className="py-1.5">
                            {l.product_name} <span className="text-xs text-muted-foreground">({l.sku})</span>
                          </td>
                          <td className="tabular py-1.5 text-right">{l.ordered}</td>
                          <td className="tabular py-1.5 text-right">{l.pending}</td>
                          <td className="tabular py-1.5 text-right">
                            {l.invoiced}
                            {Number(l.excess) > 0 && <span className="block text-xs text-warn">+{l.excess} extra</span>}
                          </td>
                          <td className="tabular py-1.5 text-right">{l.pending_after}</td>
                          <td className="py-1.5 pl-4">
                            <StatusChip status="" tone={PO_LINE_STATUS[l.status].tone} label={PO_LINE_STATUS[l.status].label} />
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                {(sp.variances || acceptVariances) && (
                  <label className="flex items-start gap-2 text-sm">
                    <input type="checkbox" className="mt-1" checked={acceptVariances} disabled={busy != null} onChange={(e) => poChoice({ accept: e.target.checked })} />
                    <span>
                      <span className="font-medium">Accept the differences</span>
                      <span className="block text-muted-foreground">
                        Receive what was invoiced: items not on the order go into stock (new ones are created), and quantity over the order is recorded as extra on
                        the PO line.
                      </span>
                    </span>
                  </label>
                )}
                {(sp.short > 0 || closeShort) && (
                  <div className="space-y-2">
                    <label className="flex items-start gap-2 text-sm">
                      <input type="checkbox" className="mt-1" checked={closeShort} disabled={busy != null} onChange={(e) => poChoice({ close: e.target.checked })} />
                      <span>
                        <span className="font-medium">Close the PO short</span>
                        <span className="block text-muted-foreground">Nothing more is coming for the pending items. Leave unticked to keep them pending for a later delivery.</span>
                      </span>
                    </label>
                    {closeShort && (
                      <div className="flex flex-col gap-1.5 sm:max-w-md">
                        <Label htmlFor="imp-close-reason">Why it is closed short *</Label>
                        <div className="flex gap-2">
                          <Input id="imp-close-reason" value={closeReason} onChange={(e) => setCloseReason(e.target.value)} placeholder="e.g. item discontinued by the supplier" />
                          <Button variant="outline" disabled={busy != null || !closeReason.trim()} onClick={() => poChoice({ close: true })}>
                            Apply
                          </Button>
                        </div>
                      </div>
                    )}
                  </div>
                )}
              </CardContent>
            </Card>
          )}

          <Card>
            <CardContent className="pt-6">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Row</TableHead>
                    <TableHead>Item</TableHead>
                    <TableHead className="text-right">Qty</TableHead>
                    <TableHead className="text-right">Rate</TableHead>
                    <TableHead className="text-right">GST</TableHead>
                    <TableHead>What happens</TableHead>
                    {sp && <TableHead>Purchase order</TableHead>}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {shown.lines.map((l) => (
                    <TableRow key={l.row} className={l.errors.length ? "bg-destructive-soft/40" : undefined}>
                      <TableCell className="font-mono text-xs">{l.row}</TableCell>
                      <TableCell className="max-w-72 whitespace-normal">
                        <div className="font-medium">{l.item_name}</div>
                        {l.errors.map((e) => (
                          <div key={e} className="text-xs text-destructive">
                            {e}
                          </div>
                        ))}
                        {l.warnings.map((w) => (
                          <div key={w} className="text-xs text-warn">
                            {w}
                          </div>
                        ))}
                      </TableCell>
                      <TableCell className="text-right font-mono">{l.quantity}</TableCell>
                      <TableCell className="text-right font-mono">{rupees(l.unit_cost)}</TableCell>
                      <TableCell className="text-right font-mono text-xs">
                        {l.gst_rate}%{Number(l.line_tax) > 0 && <div>{rupees(l.line_tax)}</div>}
                      </TableCell>
                      <TableCell className="min-w-64 whitespace-normal text-sm">
                        {l.action === "add_stock" ? (
                          <div className="flex flex-col gap-1">
                            <StatusChip status="" tone="ok" label="Restock" />
                            <span className="text-xs text-muted-foreground">
                              {l.product_name} · {l.on_hand} → {Number(l.on_hand) + Number(l.quantity)} in stock
                              {l.matched_by === "chosen" ? " · your choice" : ` · matched by ${l.matched_by}`}
                            </span>
                          </div>
                        ) : l.action === "create" && l.new_product ? (
                          <div className="flex flex-col gap-1">
                            <StatusChip status="" tone="info" label="New product" />
                            <span className="text-xs text-muted-foreground">
                              {[l.new_product.category || "uncategorised", l.new_product.brand, l.new_product.tax_slab, `MRP ${rupees(l.new_product.mrp)}`, `sells at ${rupees(l.new_product.selling_price)}`]
                                .filter(Boolean)
                                .join(" · ")}
                            </span>
                          </div>
                        ) : null}
                        {(l.suggestions.length > 0 || matches[String(l.row)]) && (
                          <select
                            aria-label={`Which product is row ${l.row}?`}
                            className="mt-1 h-8 w-full rounded-md border bg-background px-2 text-xs"
                            value={matches[String(l.row)] ?? ""}
                            onChange={(e) => choose(l.row, e.target.value)}
                          >
                            <option value="">{l.action === "create" && matches[String(l.row)] !== "new" ? "Create as a new product" : "Automatic match"}</option>
                            {l.suggestions.map((sg) => (
                              <option key={sg.variant_id} value={sg.variant_id}>
                                Same as {sg.name} (MRP {sg.mrp}){sg.on_po ? " · on the PO" : ""}
                              </option>
                            ))}
                            {matches[String(l.row)] && matches[String(l.row)] !== "new" && !l.suggestions.some((sg) => sg.variant_id === matches[String(l.row)]) && (
                              <option value={matches[String(l.row)]}>{l.product_name}</option>
                            )}
                            {(l.action === "add_stock" || matches[String(l.row)] === "new") && <option value="new">Create as a new product</option>}
                          </select>
                        )}
                      </TableCell>
                      {sp && (
                        <TableCell className="whitespace-normal text-xs">
                          {l.po_status && <StatusChip status="" tone={LINE_PO[l.po_status].tone} label={LINE_PO[l.po_status].label} />}
                          {l.po_ordered && (
                            <span className="mt-1 block text-muted-foreground">
                              ordered {l.po_ordered}, pending {l.po_pending}
                            </span>
                          )}
                        </TableCell>
                      )}
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </CardContent>
          </Card>
        </>
      )}

      <Card className="max-w-3xl">
        <CardHeader>
          <CardTitle>Filling the template</CardTitle>
        </CardHeader>
        <CardContent className="space-y-2 text-sm text-muted-foreground">
          <p>
            One row per invoice line. <b>item_name</b>, <b>quantity</b> and <b>unit_cost</b> (the rate before GST) are always required; <b>gst_rate</b> is
            the GST the supplier charged on the line.
          </p>
          <p>
            A line is matched to an existing product by <b>barcode</b>, then <b>sku</b>, then the exact name. Anything else creates a new product, which
            also needs <b>mrp</b> and <b>sale_gst_rate</b> (the GST you sell it at); <b>selling_price</b> defaults to the MRP, and <b>category</b> and{" "}
            <b>brand</b> are optional (a new brand is created, a category must already exist).
          </p>
          <p>
            Put the printed <b>line_total</b> in too: the preview flags any line where quantity × rate + GST doesn&apos;t match it. An invoice can only be
            imported once per supplier.
          </p>
          <p>
            Against a purchase order, each line is matched to the ordered item for its product. A name that only nearly matches an ordered item is
            suggested first (&quot;on the PO&quot;): pick it if it is the same item. Less than ordered stays pending unless you close the PO short; more than
            ordered and items not on the order need &quot;Accept the differences&quot;.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
