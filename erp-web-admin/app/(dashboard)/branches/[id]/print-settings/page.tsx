"use client";

// A branch's letterhead (erp-core-go internal/documents, migrations/049):
// logo, display name, address, contact lines, a header and two footers —
// one for customer-facing sale receipts, one for supplier-facing purchase
// documents (GRNs, bills, returns). Every downloadable PDF of the branch
// carries it, and the thermal ESC/POS slip carries its text lines. The
// preview alongside is the server's own render of the saved settings, so it
// is exactly what a download will look like.
import { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { useParams } from "next/navigation";
import { ArrowLeft, ImageUp, RefreshCw, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Branch, PrintSettings } from "@/lib/types";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { PageHeader } from "@/components/data/page-header";

const DEFAULT_RECEIPT_FOOTER = "Thank you, visit again!";
const MAX_UPLOAD = 2 * 1024 * 1024;

type Form = {
  display_name: string;
  address: string;
  phone: string;
  email: string;
  header_text: string;
  receipt_footer: string;
  purchase_footer: string;
  show_logo: boolean;
  show_gstin: boolean;
  show_sku: boolean;
  receipt_format: "a4" | "thermal";
};

function toForm(s: PrintSettings): Form {
  return {
    display_name: s.display_name,
    address: s.address,
    phone: s.phone,
    email: s.email,
    header_text: s.header_text,
    receipt_footer: s.receipt_footer ?? DEFAULT_RECEIPT_FOOTER,
    purchase_footer: s.purchase_footer,
    show_logo: s.show_logo,
    show_gstin: s.show_gstin,
    show_sku: s.show_sku ?? true,
    receipt_format: s.receipt_format,
  };
}

const PREVIEWS = [
  { key: "receipt-a4", label: "Receipt · A4", query: "kind=receipt&format=a4" },
  { key: "receipt-thermal", label: "Receipt · 80 mm", query: "kind=receipt&format=thermal" },
  { key: "purchase", label: "Purchase documents", query: "kind=purchase" },
] as const;
type PreviewKey = (typeof PREVIEWS)[number]["key"];

function Field({ id, label, hint, children }: { id: string; label: string; hint?: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      {children}
      {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
    </div>
  );
}

export default function BranchPrintSettingsPage() {
  const { id } = useParams<{ id: string }>();
  const { can } = usePermissions();
  const allowed = can("branch.print_settings");

  const [branch, setBranch] = useState<Branch | null>(null);
  const [saved, setSaved] = useState<PrintSettings | null>(null);
  const [form, setForm] = useState<Form | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const [logoUrl, setLogoUrl] = useState<string | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  const [preview, setPreview] = useState<PreviewKey>("receipt-a4");
  const [previewUrl, setPreviewUrl] = useState<string | null>(null);
  const [previewBusy, setPreviewBusy] = useState(false);

  const loadLogo = useCallback(
    async (has: boolean) => {
      setLogoUrl((old) => {
        if (old) URL.revokeObjectURL(old);
        return null;
      });
      if (!has) return;
      try {
        const { blob } = await api.download(`/api/v1/branches/${id}/print-settings/logo`);
        setLogoUrl(URL.createObjectURL(blob));
      } catch {
        // the preview still shows it; a missing thumbnail isn't worth a toast
      }
    },
    [id],
  );

  const loadPreview = useCallback(
    async (key: PreviewKey) => {
      setPreviewBusy(true);
      try {
        const q = PREVIEWS.find((p) => p.key === key)!.query;
        const { blob } = await api.download(`/api/v1/branches/${id}/print-settings/preview?${q}`);
        setPreviewUrl((old) => {
          if (old) URL.revokeObjectURL(old);
          return URL.createObjectURL(blob);
        });
      } catch (e) {
        toast.error(e instanceof ApiError ? e.message : "Could not render the preview");
      } finally {
        setPreviewBusy(false);
      }
    },
    [id],
  );

  useEffect(() => {
    api
      .get<{ branches: Branch[] }>("/api/v1/branches")
      .then((d) => setBranch(d.branches.find((b) => b.branch_id === id) ?? null))
      .catch(() => {});
    api
      .get<PrintSettings>(`/api/v1/branches/${id}/print-settings`)
      .then((s) => {
        setSaved(s);
        setForm(toForm(s));
        loadLogo(s.has_logo);
        loadPreview("receipt-a4");
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load print settings"));
  }, [id, loadLogo, loadPreview]);

  const dirty = saved != null && form != null && JSON.stringify(toForm(saved)) !== JSON.stringify(form);
  const set = <K extends keyof Form>(k: K, v: Form[K]) => setForm((f) => (f ? { ...f, [k]: v } : f));

  async function save(e: React.FormEvent) {
    e.preventDefault();
    if (!form) return;
    setBusy(true);
    try {
      const s = await api.put<PrintSettings>(`/api/v1/branches/${id}/print-settings`, {
        ...form,
        // Untouched default stays "use the default" (null), so a later change
        // to the default reaches this branch too.
        receipt_footer: saved?.receipt_footer == null && form.receipt_footer === DEFAULT_RECEIPT_FOOTER ? null : form.receipt_footer,
      });
      setSaved(s);
      setForm(toForm(s));
      toast.success("Print settings saved");
      loadPreview(preview);
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : "Could not save");
    } finally {
      setBusy(false);
    }
  }

  async function uploadLogo(file: File) {
    if (!["image/png", "image/jpeg"].includes(file.type)) {
      toast.error("Choose a PNG or JPEG image");
      return;
    }
    if (file.size > MAX_UPLOAD) {
      toast.error("The logo must be under 2 MB");
      return;
    }
    setBusy(true);
    try {
      const s = await api.upload<PrintSettings>(`/api/v1/branches/${id}/print-settings/logo`, file);
      setSaved((prev) => (prev ? { ...prev, has_logo: s.has_logo, logo_updated_at: s.logo_updated_at } : s));
      toast.success("Logo uploaded");
      loadLogo(true);
      loadPreview(preview);
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : "Could not upload the logo");
    } finally {
      setBusy(false);
      if (fileRef.current) fileRef.current.value = "";
    }
  }

  async function removeLogo() {
    setBusy(true);
    try {
      const s = await api.delete<PrintSettings>(`/api/v1/branches/${id}/print-settings/logo`);
      setSaved((prev) => (prev ? { ...prev, has_logo: s.has_logo, logo_updated_at: s.logo_updated_at } : s));
      toast.success("Logo removed");
      loadLogo(false);
      loadPreview(preview);
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : "Could not remove the logo");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex flex-col gap-5">
      <Link href="/branches" className="flex w-fit items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
        <ArrowLeft className="size-4" /> Branches
      </Link>
      <PageHeader
        title="Letterhead & print settings"
        description={
          <>
            {branch ? `${branch.name} (${branch.code}). ` : ""}Printed on this branch&rsquo;s receipts, GRNs, purchase bills and purchase
            returns.
          </>
        }
      />
      {error && <p className="text-sm text-destructive">{error}</p>}

      {form && saved && (
        <div className="grid gap-5 xl:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
          <form onSubmit={save} className="flex min-w-0 flex-col gap-5">
            <section className="flex flex-col gap-4 rounded-xl border bg-card p-4">
              <h2 className="text-sm font-semibold">Logo</h2>
              <div className="flex flex-wrap items-center gap-4">
                <div className="flex h-20 w-40 items-center justify-center overflow-hidden rounded-lg border bg-white">
                  {logoUrl ? (
                    // eslint-disable-next-line @next/next/no-img-element -- a blob: URL, nothing for next/image to optimise
                    <img src={logoUrl} alt={`${branch?.name ?? "Branch"} logo`} className="max-h-full max-w-full object-contain" />
                  ) : (
                    <span className="text-xs text-muted-foreground">No logo</span>
                  )}
                </div>
                <div className="flex flex-col gap-2">
                  <input
                    ref={fileRef}
                    type="file"
                    accept="image/png,image/jpeg"
                    className="hidden"
                    aria-label="Logo file"
                    onChange={(e) => e.target.files?.[0] && uploadLogo(e.target.files[0])}
                  />
                  <div className="flex gap-2">
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      disabled={!allowed || busy}
                      title={!allowed ? missingPermissionHint("branch.print_settings") : undefined}
                      onClick={() => fileRef.current?.click()}
                    >
                      <ImageUp /> {saved.has_logo ? "Replace" : "Upload"}
                    </Button>
                    {saved.has_logo && (
                      <Button type="button" variant="ghost" size="sm" disabled={!allowed || busy} onClick={removeLogo}>
                        <Trash2 /> Remove
                      </Button>
                    )}
                  </div>
                  <p className="text-xs text-muted-foreground">PNG or JPEG, under 2 MB. A wide logo on a white or transparent background prints best.</p>
                </div>
              </div>
              <label className="flex items-center gap-2 text-sm">
                <input type="checkbox" checked={form.show_logo} disabled={!allowed} onChange={(e) => set("show_logo", e.target.checked)} />
                Print the logo on documents
              </label>
            </section>

            <fieldset disabled={!allowed} className="flex flex-col gap-4 rounded-xl border bg-card p-4">
              <legend className="sr-only">Header</legend>
              <h2 className="text-sm font-semibold">Header</h2>
              <Field id="display_name" label="Name on documents" hint="Leave blank to use the business's trade name.">
                <Input id="display_name" maxLength={120} value={form.display_name} onChange={(e) => set("display_name", e.target.value)} />
              </Field>
              <Field id="address" label="Address" hint="One printed line per line, up to 6.">
                <Textarea id="address" rows={3} maxLength={400} value={form.address} onChange={(e) => set("address", e.target.value)} />
              </Field>
              <div className="grid gap-4 sm:grid-cols-2">
                <Field id="phone" label="Phone">
                  <Input id="phone" maxLength={60} value={form.phone} onChange={(e) => set("phone", e.target.value)} />
                </Field>
                <Field id="email" label="Email">
                  <Input id="email" type="email" maxLength={120} value={form.email} onChange={(e) => set("email", e.target.value)} />
                </Field>
              </div>
              <Field id="header_text" label="Header lines" hint="Under the letterhead: a tagline, licence or FSSAI number.">
                <Textarea id="header_text" rows={2} maxLength={400} value={form.header_text} onChange={(e) => set("header_text", e.target.value)} />
              </Field>
              <label className="flex items-center gap-2 text-sm">
                <input type="checkbox" checked={form.show_gstin} onChange={(e) => set("show_gstin", e.target.checked)} />
                Print the GSTIN (a sale receipt with a GSTIN is titled &ldquo;Tax invoice&rdquo;)
              </label>
              <label className="flex items-center gap-2 text-sm">
                <input type="checkbox" checked={form.show_sku} onChange={(e) => set("show_sku", e.target.checked)} />
                Print the SKU under each item on receipts, invoices and quotations (off: the product name only)
              </label>
            </fieldset>

            <fieldset disabled={!allowed} className="flex flex-col gap-4 rounded-xl border bg-card p-4">
              <legend className="sr-only">Footers</legend>
              <h2 className="text-sm font-semibold">Footers</h2>
              <Field id="receipt_footer" label="Sale receipt footer" hint="Return policy, thank-you note. Clear it to print none.">
                <Textarea
                  id="receipt_footer"
                  rows={3}
                  maxLength={600}
                  value={form.receipt_footer}
                  onChange={(e) => set("receipt_footer", e.target.value)}
                />
              </Field>
              <Field id="purchase_footer" label="Purchase document footer" hint="On GRNs, purchase bills and returns sent to suppliers.">
                <Textarea
                  id="purchase_footer"
                  rows={2}
                  maxLength={600}
                  value={form.purchase_footer}
                  onChange={(e) => set("purchase_footer", e.target.value)}
                />
              </Field>
              <Field id="receipt_format" label="Default receipt paper" hint="What a plain “Download receipt” gives, on the web and at the till.">
                <select
                  id="receipt_format"
                  className="h-8 w-48 rounded-lg border border-input bg-transparent px-2 text-sm"
                  value={form.receipt_format}
                  onChange={(e) => set("receipt_format", e.target.value as Form["receipt_format"])}
                >
                  <option value="a4">A4</option>
                  <option value="thermal">80 mm thermal roll</option>
                </select>
              </Field>
            </fieldset>

            <div className="flex flex-wrap items-center gap-3">
              <Button type="submit" disabled={!allowed || !dirty || busy}>
                {busy ? "Saving…" : "Save"}
              </Button>
              {dirty && (
                <Button type="button" variant="ghost" onClick={() => setForm(toForm(saved))}>
                  Discard changes
                </Button>
              )}
              {!allowed && <span className="text-sm text-muted-foreground">{missingPermissionHint("branch.print_settings")}</span>}
            </div>
          </form>

          <section className="flex min-w-0 flex-col gap-3 rounded-xl border bg-card p-4 xl:sticky xl:top-4 xl:h-fit">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div role="tablist" aria-label="Preview" className="flex flex-wrap gap-1">
                {PREVIEWS.map((p) => (
                  <Button
                    key={p.key}
                    role="tab"
                    aria-selected={preview === p.key}
                    variant={preview === p.key ? "secondary" : "ghost"}
                    size="sm"
                    onClick={() => {
                      setPreview(p.key);
                      loadPreview(p.key);
                    }}
                  >
                    {p.label}
                  </Button>
                ))}
              </div>
              <Button variant="ghost" size="sm" disabled={previewBusy} onClick={() => loadPreview(preview)}>
                <RefreshCw /> Refresh
              </Button>
            </div>
            {dirty && <p className="text-xs text-muted-foreground">The preview shows the saved settings. Save to see your changes.</p>}
            <div className="h-[70vh] min-h-96 overflow-hidden rounded-lg border bg-muted">
              {previewUrl ? (
                <iframe title="Document preview" src={previewUrl} className="size-full" />
              ) : (
                <p className="p-4 text-sm text-muted-foreground">Rendering preview…</p>
              )}
            </div>
          </section>
        </div>
      )}
    </div>
  );
}
