"use client";

// Label Studio (Phase 14, pos_frd_complete.md §12; erp-core-go
// POST /labels/preview and /labels/print).
// - What to print: products with a number of copies each, or a whole GRN
//   (each line's received quantity and its batch).
// - Size: §12's presets or a custom size; which fields; 203 or 300 dpi.
// - A live preview that marks any label whose barcode would be too fine to
//   scan at that size and resolution.
// - Three print paths:
//   1. a print file for a thermal label printer (ZPL, TSPL or ESC/POS);
//   2. the system print dialog at the exact label size;
//   3. an A4 sticker sheet, saved as PDF from the print dialog.
// Every path draws from the same preview data, so they print the same.
// A drag-and-drop label designer is a follow-up, as the roadmap scopes it.
import { useEffect, useMemo, useState } from "react";
import { toSVG } from "bwip-js/browser";
import { Download, Printer, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError, SEED_BRANCH_ID } from "@/lib/api-client";
import { Branch } from "@/lib/types";
import { rupees } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { PageHeader } from "@/components/data/page-header";
import { StatusChip } from "@/components/data/status-chip";
import { ProductPicker, VariantOption } from "@/components/data/product-picker";

type Fields = { name: boolean; sku: boolean; price: boolean; mrp: boolean; batch: boolean; code: "barcode" | "qr" | "none" };
type Spec = { width_mm: number; height_mm: number; dpi: number; fields: Fields };
type Item = { variant_id: string; label: string; copies: number; batch_no?: string; expiry_date?: string };
type Scan = { level: "ok" | "small" | "fail" | "none"; module_mm: number; message: string };
type PreviewLabel = {
  variant_id: string;
  copies: number;
  product_name: string;
  sku: string;
  mrp: string;
  selling_price: string;
  barcode: string;
  symbology: string;
  batch_no: string;
  expiry_date: string;
  scan: Scan;
};
type GRNSummary = { grn_id: string; grn_number: string; status: string; created_at: string };
type GRNDetail = { grn_number: string; lines: { variant_id: string; sku: string; product_name: string; quantity: string; batch_no: string; expiry_date: string | null }[] };

const PRESETS: Record<string, { label: string; spec: Spec }> = {
  standard: { label: "Standard · 50×30 mm", spec: { width_mm: 50, height_mm: 30, dpi: 203, fields: { name: true, sku: true, price: true, mrp: true, batch: false, code: "barcode" } } },
  compact: { label: "Compact · 40×20 mm", spec: { width_mm: 40, height_mm: 20, dpi: 203, fields: { name: true, sku: false, price: false, mrp: true, batch: false, code: "barcode" } } },
  detailed: { label: "Detailed · 100×50 mm", spec: { width_mm: 100, height_mm: 50, dpi: 203, fields: { name: true, sku: true, price: true, mrp: true, batch: true, code: "barcode" } } },
  qr: { label: "QR · 50×50 mm", spec: { width_mm: 50, height_mm: 50, dpi: 203, fields: { name: true, sku: false, price: true, mrp: false, batch: false, code: "qr" } } },
};

const selectClass = "h-9 rounded-lg border bg-card px-2 text-sm";
const SCAN_TONE = { ok: "ok", small: "warn", fail: "bad", none: "muted" } as const;

// The code as SVG, the same symbology the printer gets.
function codeSVG(l: PreviewLabel, kind: Fields["code"]): string | null {
  if (kind === "none" || !l.barcode) return null;
  try {
    if (kind === "qr") return toSVG({ bcid: "qrcode", text: l.barcode }); // error correction M by default, as the printer files use
    if (l.symbology === "EAN13" && /^\d{12,13}$/.test(l.barcode)) return toSVG({ bcid: "ean13", text: l.barcode, includetext: true, textxalign: "center" });
    return toSVG({ bcid: "code128", text: l.barcode, includetext: true, textxalign: "center" });
  } catch {
    return null;
  }
}

// One label, drawn at its real size in mm (screen and print alike).
function LabelFace({ l, spec }: { l: PreviewLabel; spec: Spec }) {
  const svg = codeSVG(l, spec.fields.code);
  const f = spec.fields;
  const price = [f.mrp ? `MRP ${l.mrp}` : "", f.price ? `Price ${l.selling_price}` : ""].filter(Boolean).join("  ");
  return (
    <div
      className="label-face flex flex-col items-center overflow-hidden bg-white text-black"
      style={{ width: `${spec.width_mm}mm`, height: `${spec.height_mm}mm`, padding: "2mm", fontSize: "2.6mm", lineHeight: 1.15 }}
    >
      {f.name && <div className="w-full truncate text-center font-semibold">{l.product_name}</div>}
      {svg && (
        <div
          className="flex min-h-0 w-full flex-1 items-center justify-center [&>svg]:max-h-full [&>svg]:max-w-full"
          // bwip-js output: an SVG we generated from our own data.
          dangerouslySetInnerHTML={{ __html: svg }}
        />
      )}
      {price && <div className="w-full truncate text-center">{price}</div>}
      {f.sku && <div className="w-full truncate text-center">SKU {l.sku}</div>}
      {f.batch && l.batch_no && (
        <div className="w-full truncate text-center">
          Batch {l.batch_no}
          {l.expiry_date ? `  Exp ${l.expiry_date}` : ""}
        </div>
      )}
    </div>
  );
}

export default function LabelStudioPage() {
  const [items, setItems] = useState<Item[]>([]);
  const [picked, setPicked] = useState<VariantOption | null>(null);
  const [copies, setCopies] = useState("1");
  const [grns, setGrns] = useState<GRNSummary[]>([]);
  const [grnId, setGrnId] = useState("");
  const [branches, setBranches] = useState<Branch[]>([]);
  const [branchId, setBranchId] = useState(SEED_BRANCH_ID);
  const [preset, setPreset] = useState("standard");
  const [custom, setCustom] = useState<Spec>(PRESETS.standard.spec);
  const [dpi, setDpi] = useState(203);
  const [language, setLanguage] = useState("zpl");
  const [preview, setPreview] = useState<{ labels: PreviewLabel[]; worst: string; spec: Spec } | null>(null);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const [printMode, setPrintMode] = useState<"label" | "sheet" | null>(null);

  const spec: Spec = useMemo(() => (preset === "custom" ? { ...custom, dpi } : { ...PRESETS[preset].spec, dpi }), [preset, custom, dpi]);

  useEffect(() => {
    api.get<{ branches: Branch[] }>("/api/v1/branches?status=active&scope=mine").then((d) => setBranches(d.branches)).catch(() => {});
    api.get<{ grns: GRNSummary[] }>("/api/v1/purchase/grn?status=completed").then((d) => setGrns(d.grns.slice(0, 50))).catch(() => {});
  }, []);

  // Live preview, debounced; the server is the one source of the data and
  // the scannability verdict.
  useEffect(() => {
    if (!items.length) return;
    const t = setTimeout(() => {
      api
        .post<{ labels: PreviewLabel[]; worst_scan: string; spec: Spec }>("/api/v1/labels/preview", {
          preset: preset === "custom" ? "custom" : preset,
          spec: preset === "custom" ? spec : { dpi },
          branch_id: branchId,
          items: items.map(({ variant_id, copies, batch_no, expiry_date }) => ({ variant_id, copies, batch_no, expiry_date })),
        })
        .then((d) => {
          setPreview({ labels: d.labels, worst: d.worst_scan, spec: d.spec });
          setPreviewError(null);
        })
        .catch((e) => setPreviewError(e instanceof ApiError ? e.message : "Could not preview"));
    }, 300);
    return () => clearTimeout(t);
  }, [items, preset, spec, dpi, branchId]);

  // The browser print paths: the page size goes into an @page rule for
  // the print, and is removed afterwards.
  useEffect(() => {
    if (!printMode || !preview) return;
    const style = document.createElement("style");
    style.textContent =
      printMode === "label"
        ? `@page { size: ${preview.spec.width_mm}mm ${preview.spec.height_mm}mm; margin: 0 }`
        : `@page { size: A4; margin: 8mm }`;
    document.head.appendChild(style);
    const done = () => setPrintMode(null);
    window.addEventListener("afterprint", done, { once: true });
    const t = setTimeout(() => window.print(), 50);
    return () => {
      clearTimeout(t);
      style.remove();
      window.removeEventListener("afterprint", done);
    };
  }, [printMode, preview]);

  function addProduct() {
    const n = Math.floor(Number(copies));
    if (!picked || !(n >= 1 && n <= 500)) return;
    setItems((xs) => [...xs, { variant_id: picked.variantId, label: picked.label, copies: n }]);
    setPicked(null);
    setCopies("1");
  }

  async function loadGRN(id: string) {
    setGrnId(id);
    if (!id) return;
    try {
      const g = await api.get<GRNDetail>(`/api/v1/purchase/grn/${id}`);
      setItems(
        g.lines.map((l) => ({
          variant_id: l.variant_id,
          label: `${l.product_name} (${l.sku}) · ${g.grn_number}`,
          copies: Math.min(Math.max(Math.ceil(Number(l.quantity)), 1), 500),
          batch_no: l.batch_no || undefined,
          expiry_date: l.expiry_date ?? undefined,
        })),
      );
      toast.success(`Loaded ${g.lines.length} line(s) from ${g.grn_number}`);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not load the GRN");
    }
  }

  async function downloadPrinterFile() {
    try {
      const { blob, filename } = await api.download("/api/v1/labels/print", {
        preset: preset === "custom" ? "custom" : preset,
        spec: preset === "custom" ? spec : { dpi },
        branch_id: branchId,
        language,
        items: items.map(({ variant_id, copies, batch_no, expiry_date }) => ({ variant_id, copies, batch_no, expiry_date })),
      });
      const a = document.createElement("a");
      a.href = URL.createObjectURL(blob);
      a.download = filename;
      a.click();
      URL.revokeObjectURL(a.href);
      toast.success(`Saved ${filename}. Send it to the printer's raw port or driver.`);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not build the print file");
    }
  }

  const totalLabels = items.reduce((s, i) => s + i.copies, 0);
  // Every copy, for the browser print paths.
  const copiesToPrint = (preview?.labels ?? []).flatMap((l) => Array.from({ length: l.copies }, (_, i) => ({ l, key: `${l.variant_id}-${i}` })));
  const sheet = preview ? { cols: Math.max(Math.floor((210 - 16 + 2) / (preview.spec.width_mm + 2)), 1), rows: Math.max(Math.floor((297 - 16 + 2) / (preview.spec.height_mm + 2)), 1) } : null;

  return (
    <>
      <style>{`@media print {
        body * { visibility: hidden !important; }
        #label-print, #label-print * { visibility: visible !important; }
        #label-print { position: absolute; left: 0; top: 0; }
        #label-print .label-face { break-inside: avoid; }
        #label-print[data-mode="label"] .label-face { break-after: page; }
      }`}</style>

      <div className="flex flex-col gap-5 print:hidden">
        <PageHeader
          title="Label Studio"
          description="Print shelf and product labels in bulk: pick products or a whole GRN, choose a size and fields, check the preview, then print."
        />

        <div className="grid gap-5 xl:grid-cols-[420px_minmax(0,1fr)]">
          <div className="flex flex-col gap-4">
            <section className="flex flex-col gap-3 rounded-xl border bg-card p-4">
              <h2 className="text-sm font-semibold">What to print</h2>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="grn">From a goods receipt</Label>
                <select id="grn" value={grnId} onChange={(e) => loadGRN(e.target.value)} className={selectClass}>
                  <option value="">Choose a completed GRN…</option>
                  {grns.map((g) => (
                    <option key={g.grn_id} value={g.grn_id}>
                      {g.grn_number}
                    </option>
                  ))}
                </select>
              </div>
              <div className="flex items-end gap-2">
                <div className="flex min-w-0 flex-1 flex-col gap-1.5">
                  <span className="text-sm font-medium">Or a product</span>
                  <ProductPicker value={picked} onChange={setPicked} />
                </div>
                <Input aria-label="Copies" className="w-20" type="number" min={1} max={500} value={copies} onChange={(e) => setCopies(e.target.value)} />
                <Button variant="outline" disabled={!picked} onClick={addProduct}>
                  Add
                </Button>
              </div>
              <ul className="flex flex-col gap-1 text-sm" aria-label="Labels to print">
                {items.map((it, i) => (
                  <li key={i} className="flex items-center gap-2">
                    <span className="min-w-0 flex-1 truncate" title={it.label}>
                      {it.label}
                      {it.batch_no ? ` · batch ${it.batch_no}` : ""}
                    </span>
                    <Input
                      aria-label={`Copies of ${it.label}`}
                      className="h-8 w-16"
                      type="number"
                      min={1}
                      max={500}
                      value={it.copies}
                      onChange={(e) => setItems((xs) => xs.map((x, j) => (j === i ? { ...x, copies: Math.min(Math.max(Math.floor(Number(e.target.value) || 1), 1), 500) } : x)))}
                    />
                    <Button variant="ghost" size="icon-sm" aria-label={`Remove ${it.label}`} onClick={() => setItems((xs) => xs.filter((_, j) => j !== i))}>
                      <Trash2 />
                    </Button>
                  </li>
                ))}
                {!items.length && <li className="text-muted-foreground">Nothing yet. Add products or load a GRN.</li>}
              </ul>
              {items.length > 0 && <p className="text-xs text-muted-foreground">{totalLabels} label(s) in this job.</p>}
            </section>

            <section className="flex flex-col gap-3 rounded-xl border bg-card p-4">
              <h2 className="text-sm font-semibold">Size and fields</h2>
              <div className="grid grid-cols-2 gap-3">
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="preset">Size</Label>
                  <select id="preset" value={preset} onChange={(e) => setPreset(e.target.value)} className={selectClass}>
                    {Object.entries(PRESETS).map(([k, p]) => (
                      <option key={k} value={k}>
                        {p.label}
                      </option>
                    ))}
                    <option value="custom">Custom size…</option>
                  </select>
                </div>
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="dpi">Printer resolution</Label>
                  <select id="dpi" value={dpi} onChange={(e) => setDpi(Number(e.target.value))} className={selectClass}>
                    <option value={203}>203 dpi (most thermal)</option>
                    <option value={300}>300 dpi</option>
                  </select>
                </div>
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="branch">Branch (for batches)</Label>
                  <select id="branch" value={branchId} onChange={(e) => setBranchId(e.target.value)} className={selectClass}>
                    {branches.map((b) => (
                      <option key={b.branch_id} value={b.branch_id}>
                        {b.name}
                      </option>
                    ))}
                  </select>
                </div>
              </div>
              {preset === "custom" && (
                <div className="flex flex-col gap-3 rounded-lg bg-muted/40 p-3">
                  <div className="grid grid-cols-2 gap-3">
                    <div className="flex flex-col gap-1.5">
                      <Label htmlFor="w">Width (mm)</Label>
                      <Input id="w" type="number" min={20} max={120} value={custom.width_mm} onChange={(e) => setCustom((c) => ({ ...c, width_mm: Number(e.target.value) }))} />
                    </div>
                    <div className="flex flex-col gap-1.5">
                      <Label htmlFor="h">Height (mm)</Label>
                      <Input id="h" type="number" min={15} max={150} value={custom.height_mm} onChange={(e) => setCustom((c) => ({ ...c, height_mm: Number(e.target.value) }))} />
                    </div>
                  </div>
                  <fieldset className="flex flex-wrap gap-x-4 gap-y-2 text-sm">
                    <legend className="sr-only">Fields</legend>
                    {(["name", "sku", "price", "mrp", "batch"] as const).map((k) => (
                      <label key={k} className="flex items-center gap-1.5">
                        <input type="checkbox" checked={custom.fields[k]} onChange={(e) => setCustom((c) => ({ ...c, fields: { ...c.fields, [k]: e.target.checked } }))} />
                        {{ name: "Name", sku: "SKU", price: "Price", mrp: "MRP", batch: "Batch & expiry" }[k]}
                      </label>
                    ))}
                    <label className="flex items-center gap-1.5">
                      Code
                      <select aria-label="Code" value={custom.fields.code} onChange={(e) => setCustom((c) => ({ ...c, fields: { ...c.fields, code: e.target.value as Fields["code"] } }))} className="h-8 rounded-md border bg-card px-1 text-sm">
                        <option value="barcode">Barcode</option>
                        <option value="qr">QR code</option>
                        <option value="none">None</option>
                      </select>
                    </label>
                  </fieldset>
                </div>
              )}
            </section>

            <section className="flex flex-col gap-3 rounded-xl border bg-card p-4">
              <h2 className="text-sm font-semibold">Print</h2>
              <div className="flex items-end gap-2">
                <div className="flex flex-1 flex-col gap-1.5">
                  <Label htmlFor="lang">Thermal printer language</Label>
                  <select id="lang" value={language} onChange={(e) => setLanguage(e.target.value)} className={selectClass}>
                    <option value="zpl">ZPL · Zebra</option>
                    <option value="tspl">TSPL · TSC and most label printers</option>
                    <option value="escpos">ESC/POS · receipt printers</option>
                  </select>
                </div>
                <Button disabled={!preview || preview.worst === "fail"} onClick={downloadPrinterFile}>
                  <Download /> Print file
                </Button>
              </div>
              <div className="flex flex-wrap gap-2">
                <Button variant="outline" disabled={!preview} onClick={() => setPrintMode("label")}>
                  <Printer /> Print at label size
                </Button>
                <Button variant="outline" disabled={!preview} onClick={() => setPrintMode("sheet")}>
                  <Printer /> A4 sticker sheet (PDF)
                </Button>
              </div>
              <p className="text-xs text-muted-foreground">
                The print file goes to the printer&apos;s raw port (9100) or its driver. &ldquo;Print at label size&rdquo; uses the system dialog
                with the page set to the label; the A4 sheet fits {sheet ? `${sheet.cols} × ${sheet.rows}` : "as many as fit"} per page. Choose
                &ldquo;Save as PDF&rdquo; there for a PDF.
              </p>
            </section>
          </div>

          <section className="flex min-w-0 flex-col gap-3 rounded-xl border bg-card p-4" aria-label="Preview">
            <div className="flex items-center justify-between gap-2">
              <h2 className="text-sm font-semibold">Preview · actual size</h2>
              {preview && (
                <StatusChip
                  status={preview.worst}
                  tone={SCAN_TONE[preview.worst as Scan["level"]] ?? "muted"}
                  label={preview.worst === "fail" ? "Some won't scan" : preview.worst === "small" ? "Small barcodes" : "Scannable"}
                />
              )}
            </div>
            {previewError && <p className="text-sm text-destructive">{previewError}</p>}
            {!items.length && <p className="text-sm text-muted-foreground">Add something to print to see it here.</p>}
            <div className="flex flex-wrap gap-4">
              {items.length > 0 &&
                preview?.labels.map((l, i) => (
                  <figure key={i} className="flex flex-col gap-1.5">
                    <div className="rounded-sm shadow-[0_0_0_1px_var(--border)]">
                      <LabelFace l={l} spec={preview.spec} />
                    </div>
                    <figcaption className="max-w-60 text-xs">
                      <StatusChip status={l.scan.level} tone={SCAN_TONE[l.scan.level]} label={`× ${l.copies}`} /> <span className="text-muted-foreground">{l.scan.message}</span>
                      {l.selling_price && <span className="sr-only"> Price {rupees(l.selling_price)}</span>}
                    </figcaption>
                  </figure>
                ))}
            </div>
          </section>
        </div>
      </div>

      {/* Only shown when printing (see the @media print rule above). */}
      {printMode && preview && (
        <div
          id="label-print"
          data-mode={printMode}
          className="hidden print:grid"
          style={printMode === "sheet" ? { gridTemplateColumns: `repeat(${sheet!.cols}, ${preview.spec.width_mm}mm)`, gap: "2mm" } : undefined}
        >
          {copiesToPrint.map(({ l, key }) => (
            <LabelFace key={key} l={l} spec={preview.spec} />
          ))}
        </div>
      )}
    </>
  );
}
