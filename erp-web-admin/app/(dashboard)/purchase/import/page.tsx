"use client";

// Import a supplier's purchase invoice from the CSV template
// (erp-core-go internal/purchase/import.go): fill the template, upload,
// check the preview — which lines restock an existing product, which create
// a new one, and that the lines add up to the printed total — then import.
// One click books the GRN (stock in, on the invoice date) and the bill
// (payable, GST input) together, or nothing at all.
import { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { Download, FileUp, Loader2 } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Branch, Supplier } from "@/lib/types";
import { rupees } from "@/lib/format";
import { saveBlob } from "@/components/data/document-actions";
import { PageHeader } from "@/components/data/page-header";
import { SearchSelect } from "@/components/data/search-select";
import { StatusChip } from "@/components/data/status-chip";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

import { currencySymbol } from "@/lib/currency";
type Suggestion = { variant_id: string; name: string; sku: string; mrp: string; score: number };
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
};
type Preview = {
  supplier: string;
  branch: string;
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
};

const today = () => new Date().toLocaleDateString("en-CA");

export default function ImportInvoicePage() {
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
  }, []);

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
    }),
    [supplierId, branchId, invoiceNumber, invoiceDate, dueDate, freight, other, total, csv, matches]
  );

  // A preview is only shown while the inputs it was made from are unchanged.
  const inputKey = JSON.stringify([supplierId, branchId, invoiceNumber, invoiceDate, dueDate, freight, other, total, csv]);
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

  const canPreview = supplierId && branchId && invoiceNumber.trim() && invoiceDate && csv;
  const shown = preview && previewKey === inputKey ? preview : null;
  const s = shown?.summary;

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
          </CardContent>
        </Card>
      )}

      <Card>
        <CardHeader>
          <CardTitle>Invoice</CardTitle>
          <CardDescription>As printed on the supplier&apos;s invoice. The total is checked against the lines; a difference under {currencySymbol()}1 is booked as round-off.</CardDescription>
        </CardHeader>
        <CardContent className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
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
                            <option value="">{l.action === "create" ? "Create as a new product" : "Automatic match"}</option>
                            {l.suggestions.map((sg) => (
                              <option key={sg.variant_id} value={sg.variant_id}>
                                Same as {sg.name} (MRP {sg.mrp})
                              </option>
                            ))}
                            {matches[String(l.row)] && matches[String(l.row)] !== "new" && !l.suggestions.some((sg) => sg.variant_id === matches[String(l.row)]) && (
                              <option value={matches[String(l.row)]}>{l.product_name}</option>
                            )}
                          </select>
                        )}
                      </TableCell>
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
        </CardContent>
      </Card>
    </div>
  );
}
