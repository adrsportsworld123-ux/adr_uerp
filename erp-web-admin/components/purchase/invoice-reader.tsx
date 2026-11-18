"use client";

// Read the supplier's own invoice — a PDF or photos — into the import
// (erp-core-go internal/purchase/invoice_read.go, §3.90). A typed PDF is
// read from its text with the supplier's reading rules (their "template");
// photos and scanned PDFs by the AI reader when the server has one. The
// lines come back into an editable table, and the table is the import's
// CSV: the page's Preview and Import stay exactly as they were.
//
// The reading rules are per supplier: what the BATCH NO. / size column
// holds, how sizes are written, the cost basis, the brand, the sale GST
// and category of new items, the supplier's own codes, the column
// headings, and notes for the AI. "Read again" tries changed rules on the
// same document; "Save for <supplier>" keeps them.
import { useEffect, useRef, useState } from "react";
import { FileSearch, Loader2, RotateCcw, Save, Sparkles, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { asFile, asPhoto, DocPage, isPdf, isPhoto, pdfPages, ReadFile } from "@/lib/invoice-reader";
import { rupees } from "@/lib/format";
import { saveBlob } from "@/components/data/document-actions";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";

export type ReadLine = {
  row: number;
  item_name: string;
  printed_name: string;
  variant: string;
  size: string;
  batch_no: string;
  hsn_code: string;
  quantity: string;
  unit: string;
  mrp: string;
  rate: string;
  unit_cost: string;
  gst_rate: string;
  tax: string;
  /** With GST: what the import checks quantity × cost + GST against. */
  line_total: string;
  /** The amount as printed when it was before GST (line_total adds the GST). */
  printed_amount?: string;
  selling_price: string;
  category: string;
  brand: string;
  sale_gst_rate: string;
  notes: string[];
};

// A read line's cost is worked back from its printed amount (erp-core-go
// buildLines). When the quantity or GST % is corrected, the printed amount
// stays what the supplier billed: the cost follows it — unless the cost
// was typed over — and a before-GST amount's line total takes the new GST.
// The import re-checks every figure on Preview; this only keeps the
// suggested inputs consistent.
const num = (s: string | undefined) => (s && s.trim() !== "" && Number.isFinite(Number(s)) ? Number(s) : null);
const costText = (v: number) => String(Math.round(v * 1e6) / 1e6);

function workedCost(l: ReadLine): number | null {
  const qty = num(l.quantity);
  const gst = num(l.gst_rate) ?? 0;
  if (!qty || qty <= 0) return null;
  const printed = num(l.printed_amount);
  if (printed != null) return printed / qty;
  const total = num(l.line_total);
  return total == null ? null : total / (1 + gst / 100) / qty;
}

function reworked(l: ReadLine, key: keyof ReadLine, value: string): ReadLine {
  const next = { ...l, [key]: value };
  if (key !== "quantity" && key !== "gst_rate") return next;
  const printed = num(l.printed_amount);
  if (printed != null) {
    const gst = num(next.gst_rate) ?? 0;
    next.line_total = (printed + Math.round(printed * gst) / 100).toFixed(2);
  }
  const was = workedCost(l);
  const cost = num(l.unit_cost);
  const now = workedCost(next);
  if (was != null && cost != null && Math.abs(was - cost) < 5e-6 && now != null) next.unit_cost = costText(now);
  return next;
}

const FIELDS = [
  ["item_name", "Item"],
  ["variant", "Size / batch (often “BATCH NO.”)"],
  ["quantity", "Quantity"],
  ["mrp", "MRP"],
  ["rate", "Rate"],
  ["tax", "Tax amount"],
  ["gst_rate", "GST %"],
  ["line_total", "Line total"],
  ["hsn_code", "HSN"],
  ["serial", "Line number"],
] as const;
type Field = (typeof FIELDS)[number][0];

type Options = {
  variant_use: "size" | "batch" | "ignore";
  size_style: "words" | "letters" | "as_printed";
  name_format: string;
  cost_basis: "from_total" | "rate";
  line_amount: "auto" | "with_gst" | "before_gst";
  brand_rule: "known" | "first_word" | "none";
  sale_gst_rate: number | null;
  category: string;
  drop_words: string[];
  size_aliases: Record<string, string>;
  keep_mrp_in_name: boolean;
  ai_hints: string;
};
type Template = {
  supplier_id: string;
  supplier: string;
  saved: boolean;
  columns: Record<Field, string[]>;
  options: Options;
  uses: number;
  last_used_at: string | null;
};
type ReadResult = {
  source: "pdf_text" | "ai";
  invoice_number: string;
  invoice_date: string;
  invoice_total: string;
  supplier_gstin: string;
  supplier_id: string;
  supplier_note?: string;
  lines: ReadLine[];
  checks: {
    lines: number;
    quantity: string;
    lines_total: string;
    printed_quantity?: string;
    printed_subtotal?: string;
    invoice_total?: string;
    difference?: string;
    adds_up: boolean;
    need_attention: number;
  };
  warnings: string[];
  csv: string;
  template: Template;
  ai_available: boolean;
};

// The rules as the form edits them (lists and numbers as text).
type RulesForm = {
  columns: Record<Field, string>;
  variant_use: Options["variant_use"];
  size_style: Options["size_style"];
  name_format: string;
  cost_basis: Options["cost_basis"];
  line_amount: Options["line_amount"];
  brand_rule: Options["brand_rule"];
  sale_gst_rate: string;
  category: string;
  drop_words: string;
  size_aliases: string;
  keep_mrp_in_name: boolean;
  ai_hints: string;
};

function toForm(t: Template): RulesForm {
  const columns = {} as Record<Field, string>;
  for (const [f] of FIELDS) columns[f] = (t.columns[f] ?? []).join(", ");
  const o = t.options;
  return {
    columns,
    variant_use: o.variant_use,
    size_style: o.size_style,
    name_format: o.name_format,
    cost_basis: o.cost_basis,
    line_amount: o.line_amount ?? "auto",
    brand_rule: o.brand_rule,
    sale_gst_rate: o.sale_gst_rate == null ? "" : String(o.sale_gst_rate),
    category: o.category ?? "",
    drop_words: (o.drop_words ?? []).join(", "),
    size_aliases: Object.entries(o.size_aliases ?? {})
      .map(([k, v]) => `${k} = ${v}`)
      .join("\n"),
    keep_mrp_in_name: o.keep_mrp_in_name,
    ai_hints: o.ai_hints ?? "",
  };
}

function fromForm(f: RulesForm): { columns: Record<string, string[]>; options: Record<string, unknown> } {
  const list = (s: string) =>
    s
      .split(",")
      .map((x) => x.trim())
      .filter(Boolean);
  const columns: Record<string, string[]> = {};
  for (const [k] of FIELDS) columns[k] = list(f.columns[k]);
  const aliases: Record<string, string> = {};
  for (const line of f.size_aliases.split("\n")) {
    const [code, ...rest] = line.split("=");
    if (code?.trim()) aliases[code.trim()] = rest.join("=").trim();
  }
  return {
    columns,
    options: {
      variant_use: f.variant_use,
      size_style: f.size_style,
      name_format: f.name_format,
      cost_basis: f.cost_basis,
      line_amount: f.line_amount,
      brand_rule: f.brand_rule,
      sale_gst_rate: f.sale_gst_rate.trim() === "" ? null : Number(f.sale_gst_rate),
      category: f.category,
      drop_words: list(f.drop_words),
      size_aliases: aliases,
      keep_mrp_in_name: f.keep_mrp_in_name,
      ai_hints: f.ai_hints,
    },
  };
}

const CSV_COLUMNS = [
  "item_name", "quantity", "unit_cost", "gst_rate", "mrp", "selling_price", "line_total",
  "sku", "barcode", "hsn_code", "category", "brand", "sale_gst_rate", "batch_no", "expiry_date",
] as const;

/** The lines in the import template's columns. */
export function linesToCsv(lines: ReadLine[]): string {
  const cell = (v: string) => (/[",\n]/.test(v) ? `"${v.replace(/"/g, '""')}"` : v);
  const rows = lines.map((l) => CSV_COLUMNS.map((c) => cell(String((l as unknown as Record<string, string>)[c] ?? ""))).join(","));
  return [CSV_COLUMNS.join(","), ...rows].join("\n") + "\n";
}

type Source = { pages?: DocPage[]; files?: ReadFile[]; pdf?: File; label: string };

const EDITABLE: { key: keyof ReadLine; label: string; width: string; numeric?: boolean }[] = [
  { key: "quantity", label: "Qty", width: "w-16", numeric: true },
  { key: "unit_cost", label: "Cost before GST", width: "w-28", numeric: true },
  { key: "gst_rate", label: "GST %", width: "w-14", numeric: true },
  { key: "mrp", label: "MRP", width: "w-20", numeric: true },
  { key: "selling_price", label: "Sale price", width: "w-20", numeric: true },
  { key: "category", label: "Category", width: "w-28" },
  { key: "brand", label: "Brand", width: "w-24" },
  { key: "sale_gst_rate", label: "Sale GST %", width: "w-14", numeric: true },
];

export function InvoiceReader({
  supplierId,
  onRead,
  onLines,
}: {
  supplierId: string;
  /** The invoice's own details, as read (only what was found). */
  onRead: (r: { supplierId?: string; invoiceNumber?: string; invoiceDate?: string; invoiceTotal?: string }) => void;
  /** The lines as CSV, every time they change (label: where they came from). */
  onLines: (csv: string, label: string) => void;
}) {
  const [busy, setBusy] = useState<"read" | "save" | null>(null);
  const [result, setResult] = useState<ReadResult | null>(null);
  const [lines, setLines] = useState<ReadLine[]>([]);
  const [template, setTemplate] = useState<Template | null>(null);
  const [rules, setRules] = useState<RulesForm | null>(null);
  const [dirty, setDirty] = useState(false);
  const [showRules, setShowRules] = useState(false);
  const [failure, setFailure] = useState<{ message: string; ai: boolean } | null>(null);
  const source = useRef<Source | null>(null);
  // Whether a document has been read, for render (the source itself is a ref).
  const [hasSource, setHasSource] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);
  // The supplier the reader resolved (chosen, or found by the GSTIN).
  const readFor = result?.supplier_id || supplierId;

  // The chosen supplier's rules, unless changed and not yet saved.
  useEffect(() => {
    if (!supplierId || dirty) return;
    let live = true;
    api
      .get<Template>(`/api/v1/purchase/import-templates/${supplierId}`)
      .then((t) => {
        if (!live) return;
        setTemplate(t);
        setRules(toForm(t));
      })
      .catch(() => {});
    return () => {
      live = false;
    };
  }, [supplierId, dirty]);

  function emit(next: ReadLine[]) {
    setLines(next);
    onLines(next.length ? linesToCsv(next) : "", source.current?.label ?? "");
  }

  async function read(withRules: boolean, src = source.current) {
    if (!src) return;
    setBusy("read");
    setFailure(null);
    try {
      const body: Record<string, unknown> = { supplier_id: supplierId || undefined };
      if (src.pages) body.pages = src.pages;
      else body.files = src.files;
      if (withRules && rules) Object.assign(body, fromForm(rules));
      const r = await api.post<ReadResult>("/api/v1/purchase/imports/read", body);
      setResult(r);
      setTemplate(r.template);
      if (!withRules) {
        setRules(toForm(r.template));
        setDirty(false);
      }
      emit(r.lines);
      onRead({
        supplierId: r.supplier_id || undefined,
        invoiceNumber: r.invoice_number || undefined,
        invoiceDate: r.invoice_date || undefined,
        invoiceTotal: r.invoice_total || undefined,
      });
      toast.success(`${r.lines.length} line(s) read from ${src.label}${r.source === "ai" ? " by the AI reader" : ""}`);
    } catch (e) {
      const err = e instanceof ApiError ? e : null;
      const details = (err?.details ?? {}) as { ai_available?: boolean; template?: Template };
      if (details.template && !withRules) {
        setTemplate(details.template);
        setRules(toForm(details.template));
      }
      if (err?.code === "NO_TABLE_FOUND") setShowRules(true);
      setFailure({ message: err?.message ?? "Could not read the invoice", ai: !!details.ai_available && !!src.pdf && !!src.pages });
      setResult(null);
      emit([]);
    } finally {
      setBusy(null);
    }
  }

  async function pick(list: FileList | null) {
    const files = Array.from(list ?? []);
    if (!files.length) return;
    try {
      let src: Source;
      if (files.length === 1 && isPdf(files[0])) {
        const f = files[0];
        const pages = await pdfPages(f);
        if (pages) src = { pages, pdf: f, label: f.name };
        else {
          if (f.size > 8 * 1024 * 1024) throw new Error("This scanned PDF is larger than 8 MB — photograph the pages instead");
          src = { files: [await asFile(f)], label: f.name };
        }
      } else if (files.every(isPhoto)) {
        if (files.length > 6) throw new Error("Up to 6 photos (pages) at a time");
        src = { files: await Promise.all(files.map(asPhoto)), label: files.length === 1 ? files[0].name : `${files.length} photos` };
      } else {
        throw new Error("Choose one PDF, or one or more photos (JPEG, PNG, WebP)");
      }
      source.current = src;
      setHasSource(true);
      await read(dirty);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Could not open the file");
    } finally {
      if (fileInput.current) fileInput.current.value = "";
    }
  }

  async function readWithAI() {
    const pdf = source.current?.pdf;
    if (!pdf) return;
    const src: Source = { files: [await asFile(pdf)], label: pdf.name };
    source.current = src;
    await read(dirty, src);
  }

  function edit(i: number, key: keyof ReadLine, value: string) {
    emit(lines.map((l, j) => (j === i ? reworked(l, key, value) : l)));
  }

  function setRule<K extends keyof RulesForm>(k: K, v: RulesForm[K]) {
    if (!rules) return;
    setRules({ ...rules, [k]: v });
    setDirty(true);
  }

  async function saveRules() {
    if (!rules || !readFor) return;
    setBusy("save");
    try {
      const t = await api.put<Template>(`/api/v1/purchase/import-templates/${readFor}`, fromForm(rules));
      setTemplate(t);
      setRules(toForm(t));
      setDirty(false);
      toast.success(`Reading rules saved for ${t.supplier}`);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not save the rules");
    } finally {
      setBusy(null);
    }
  }

  async function resetRules() {
    if (!readFor) return;
    setBusy("save");
    try {
      const t = await api.delete<Template>(`/api/v1/purchase/import-templates/${readFor}`);
      setTemplate(t);
      setRules(toForm(t));
      setDirty(false);
      toast.success("Back to the standard reading rules");
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not reset the rules");
    } finally {
      setBusy(null);
    }
  }

  const c = result?.checks;
  return (
    <Card>
      <CardHeader>
        <CardTitle>Read the supplier&apos;s invoice</CardTitle>
        <CardDescription>
          Choose the invoice PDF, or photos of it. The lines are read into the table below for you to check and correct, then Preview and Import as usual. Nothing is saved until you import.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex flex-wrap items-end gap-3">
          <div className="flex flex-col gap-2">
            <Label htmlFor="imp-doc">Invoice PDF or photos</Label>
            <Input
              id="imp-doc"
              ref={fileInput}
              type="file"
              multiple
              accept="application/pdf,.pdf,image/jpeg,image/png,image/webp"
              disabled={busy != null}
              onChange={(e) => void pick(e.target.files)}
              className="max-w-xs"
            />
          </div>
          {busy === "read" && (
            <span className="flex items-center gap-2 text-sm text-muted-foreground">
              <Loader2 className="size-4 animate-spin" aria-hidden /> Reading…
            </span>
          )}
          <Button variant="outline" onClick={() => setShowRules((v) => !v)} disabled={!rules}>
            <FileSearch aria-hidden /> Reading rules{template?.saved ? ` (${template.supplier})` : ""}
          </Button>
        </div>

        {failure && (
          <div role="alert" className="flex flex-wrap items-center gap-3 rounded-lg border border-destructive/40 bg-destructive/5 p-3 text-sm">
            <span>{failure.message}</span>
            {failure.ai && (
              <Button size="sm" variant="outline" onClick={() => void readWithAI()} disabled={busy != null}>
                <Sparkles aria-hidden /> Read it with AI
              </Button>
            )}
          </div>
        )}

        {showRules && rules && (
          <div className="space-y-4 rounded-lg border p-4" data-testid="reading-rules">
            <div className="text-sm">
              <span className="font-semibold">How {template?.supplier || "this supplier"}&apos;s invoices are read</span>{" "}
              <span className="text-muted-foreground">
                {template?.saved ? `Saved rules${template.uses ? `, used ${template.uses} time(s)` : ""}.` : "Standard rules — nothing saved for this supplier yet."}
                {dirty && " Changed, not saved."}
              </span>
            </div>
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
              <div className="flex flex-col gap-2">
                <Label htmlFor="rr-variant">The size / “BATCH NO.” column holds</Label>
                <select id="rr-variant" className="h-9 rounded-md border bg-background px-2 text-sm" value={rules.variant_use} onChange={(e) => setRule("variant_use", e.target.value as Options["variant_use"])}>
                  <option value="size">The size — each size is its own item</option>
                  <option value="batch">A real batch number</option>
                  <option value="ignore">Nothing useful — ignore it</option>
                </select>
              </div>
              <div className="flex flex-col gap-2">
                <Label htmlFor="rr-style">Write sizes as</Label>
                <select id="rr-style" className="h-9 rounded-md border bg-background px-2 text-sm" value={rules.size_style} onChange={(e) => setRule("size_style", e.target.value as Options["size_style"])}>
                  <option value="words">SMALL, MEDIUM, XLARGE, 2XLARGE</option>
                  <option value="letters">S, M, XL, XXL</option>
                  <option value="as_printed">As printed</option>
                </select>
              </div>
              <div className="flex flex-col gap-2">
                <Label htmlFor="rr-name">Item name with a size</Label>
                <Input id="rr-name" value={rules.name_format} onChange={(e) => setRule("name_format", e.target.value)} placeholder="{item} - {size}" />
              </div>
              <div className="flex flex-col gap-2">
                <Label htmlFor="rr-cost">Cost before GST</Label>
                <select id="rr-cost" className="h-9 rounded-md border bg-background px-2 text-sm" value={rules.cost_basis} onChange={(e) => setRule("cost_basis", e.target.value as Options["cost_basis"])}>
                  <option value="from_total">Worked back from the line total (matches the printed total to the paisa)</option>
                  <option value="rate">The printed rate</option>
                </select>
              </div>
              <div className="flex flex-col gap-2">
                <Label htmlFor="rr-amount">Line amount printed</Label>
                <select id="rr-amount" className="h-9 rounded-md border bg-background px-2 text-sm" value={rules.line_amount} onChange={(e) => setRule("line_amount", e.target.value as Options["line_amount"])}>
                  <option value="auto">Work it out from the tax printed (default)</option>
                  <option value="with_gst">Includes GST</option>
                  <option value="before_gst">Before GST (the taxable value)</option>
                </select>
              </div>
              <div className="flex flex-col gap-2">
                <Label htmlFor="rr-brand">Brand of new items</Label>
                <select id="rr-brand" className="h-9 rounded-md border bg-background px-2 text-sm" value={rules.brand_rule} onChange={(e) => setRule("brand_rule", e.target.value as Options["brand_rule"])}>
                  <option value="known">A brand you already have, named in the item</option>
                  <option value="first_word">The item&apos;s first word</option>
                  <option value="none">Leave empty</option>
                </select>
              </div>
              <div className="flex flex-col gap-2">
                <Label htmlFor="rr-gst">Sale GST % of new items</Label>
                <Input id="rr-gst" inputMode="decimal" value={rules.sale_gst_rate} onChange={(e) => setRule("sale_gst_rate", e.target.value)} placeholder="Same as the purchase GST" />
              </div>
              <div className="flex flex-col gap-2">
                <Label htmlFor="rr-cat">Category of new items</Label>
                <Input id="rr-cat" value={rules.category} onChange={(e) => setRule("category", e.target.value)} placeholder="Uncategorised" />
              </div>
              <div className="flex flex-col gap-2">
                <Label htmlFor="rr-drop">Words to drop from the size column</Label>
                <Input id="rr-drop" value={rules.drop_words} onChange={(e) => setRule("drop_words", e.target.value)} placeholder="NEW, OLD, RATE" />
              </div>
              <label className="flex items-center gap-2 self-end pb-2 text-sm">
                <input type="checkbox" checked={rules.keep_mrp_in_name} onChange={(e) => setRule("keep_mrp_in_name", e.target.checked)} />
                Keep “MRP 220” in item names
              </label>
              <div className="flex flex-col gap-2 sm:col-span-2 lg:col-span-1">
                <Label htmlFor="rr-alias">The supplier&apos;s own codes (one per line: CODE = SIZE)</Label>
                <Textarea id="rr-alias" rows={3} value={rules.size_aliases} onChange={(e) => setRule("size_aliases", e.target.value)} placeholder={"MITIGSIZE-XL = XLARGE\nDIPKAGLNS ="} />
              </div>
              <div className="flex flex-col gap-2 sm:col-span-2">
                <Label htmlFor="rr-ai">Notes for the AI reader (photos and scans)</Label>
                <Textarea id="rr-ai" rows={3} value={rules.ai_hints} onChange={(e) => setRule("ai_hints", e.target.value)} placeholder="e.g. The BATCH NO. column holds the size or the MRP." />
              </div>
            </div>
            <details>
              <summary className="cursor-pointer text-sm font-medium">Column headings (when this supplier prints different ones)</summary>
              <div className="mt-3 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
                {FIELDS.map(([f, label]) => (
                  <div key={f} className="flex flex-col gap-1">
                    <Label htmlFor={`rr-col-${f}`} className="text-xs">
                      {label}
                    </Label>
                    <Input
                      id={`rr-col-${f}`}
                      value={rules.columns[f]}
                      onChange={(e) => setRule("columns", { ...rules.columns, [f]: e.target.value })}
                      className="h-8 text-xs"
                    />
                  </div>
                ))}
              </div>
              <p className="mt-2 text-xs text-muted-foreground">Comma-separated, as printed. An empty box uses the standard headings.</p>
            </details>
            <div className="flex flex-wrap gap-2">
              <Button variant="outline" onClick={() => void read(true)} disabled={!hasSource || busy != null}>
                <RotateCcw aria-hidden /> Read again with these rules
              </Button>
              <Button onClick={() => void saveRules()} disabled={!readFor || busy != null}>
                <Save aria-hidden /> Save for {template?.supplier || "the supplier"}
              </Button>
              {template?.saved && (
                <Button variant="ghost" onClick={() => void resetRules()} disabled={busy != null}>
                  Back to the standard rules
                </Button>
              )}
              {!readFor && <span className="self-center text-xs text-muted-foreground">Choose the supplier to save their rules.</span>}
            </div>
          </div>
        )}

        {result && c && (
          <div className="space-y-3">
            <div className="flex flex-wrap gap-x-8 gap-y-2 text-sm">
              <div>
                <div className="text-muted-foreground">Read</div>
                <div className="font-medium">
                  {c.lines} line(s), {c.quantity} pcs {result.source === "ai" ? "· by the AI reader" : "· from the PDF's text"}
                </div>
              </div>
              <div>
                <div className="text-muted-foreground">Lines total</div>
                <div className="font-mono">{rupees(c.lines_total)}</div>
              </div>
              {c.invoice_total && (
                <div>
                  <div className="text-muted-foreground">Invoice total</div>
                  <div className={`font-mono ${c.adds_up ? "text-pay" : "text-destructive"}`}>
                    {rupees(c.invoice_total)} {c.adds_up ? "✓" : `(${rupees(c.difference ?? "0")} apart)`}
                  </div>
                </div>
              )}
              {result.supplier_gstin && (
                <div>
                  <div className="text-muted-foreground">Supplier GSTIN</div>
                  <div className="font-mono">{result.supplier_gstin}</div>
                </div>
              )}
            </div>
            {(result.supplier_note || result.warnings.length > 0 || c.need_attention > 0) && (
              <ul className="space-y-1 text-sm">
                {result.supplier_note && <li className="text-warn">{result.supplier_note}</li>}
                {result.warnings.map((w) => (
                  <li key={w} className="text-warn">
                    {w}
                  </li>
                ))}
                {c.need_attention > 0 && <li className="text-warn">{c.need_attention} line(s) have a note — check them below.</li>}
              </ul>
            )}
          </div>
        )}

        {lines.length > 0 && (
          <>
            <div className="overflow-x-auto rounded-lg border">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-8">#</TableHead>
                    <TableHead className="min-w-64">Item (as it will be in the catalogue)</TableHead>
                    {EDITABLE.map((col) => (
                      <TableHead key={col.key} className={col.numeric ? "text-end" : undefined}>
                        {col.label}
                      </TableHead>
                    ))}
                    <TableHead className="text-end">Line total (with GST)</TableHead>
                    <TableHead className="w-8">
                      <span className="sr-only">Remove</span>
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {lines.map((l, i) => (
                    <TableRow key={`${l.row}-${i}`}>
                      <TableCell className="align-top text-xs text-muted-foreground">{l.row}</TableCell>
                      <TableCell className="align-top">
                        <Input aria-label={`Line ${l.row} item`} value={l.item_name} onChange={(e) => edit(i, "item_name", e.target.value)} className="h-8 text-xs" />
                        <div className="mt-1 text-[11px] text-muted-foreground">
                          {l.variant && l.variant !== "-" ? `Printed: ${l.printed_name} · ${l.variant}` : l.printed_name !== l.item_name ? `Printed: ${l.printed_name}` : ""}
                          {l.hsn_code && ` · HSN ${l.hsn_code}`}
                        </div>
                        {l.notes.map((n) => (
                          <div key={n} className="text-[11px] text-warn">
                            {n}
                          </div>
                        ))}
                      </TableCell>
                      {EDITABLE.map((col) => (
                        <TableCell key={col.key} className="align-top">
                          <Input
                            aria-label={`Line ${l.row} ${col.label}`}
                            value={String(l[col.key] ?? "")}
                            inputMode={col.numeric ? "decimal" : undefined}
                            onChange={(e) => edit(i, col.key, e.target.value)}
                            className={`h-8 text-xs ${col.width} ${col.numeric ? "text-end" : ""}`}
                          />
                        </TableCell>
                      ))}
                      <TableCell className="align-top text-end font-mono text-xs tabular">
                        {l.line_total ? rupees(l.line_total) : "—"}
                        {l.printed_amount && <div className="text-[11px] text-muted-foreground">{rupees(l.printed_amount)} + GST</div>}
                      </TableCell>
                      <TableCell className="align-top">
                        <Button size="icon" variant="ghost" aria-label={`Remove line ${l.row}`} onClick={() => emit(lines.filter((_, j) => j !== i))}>
                          <Trash2 aria-hidden />
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
            <div className="flex flex-wrap gap-2 text-xs text-muted-foreground">
              <span>The cost is before GST. Category, brand and sale GST matter only for items not in the catalogue yet; Preview shows which those are.</span>
              <Button
                size="sm"
                variant="outline"
                onClick={() => saveBlob(new Blob([linesToCsv(lines)], { type: "text/csv" }), `${(source.current?.label ?? "invoice").replace(/\.[^.]+$/, "")}.csv`)}
              >
                Download as CSV
              </Button>
            </div>
          </>
        )}
      </CardContent>
    </Card>
  );
}
