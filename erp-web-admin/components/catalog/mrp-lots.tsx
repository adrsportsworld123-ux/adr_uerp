"use client";

// One original barcode, several things on the shelf (erp-core-go
// migrations/083, §3.86):
//   - MRP lots: the same item at a new MRP while old stock is still on the
//     shelf. A lot is its own variant (stock, MRP, price) that answers to
//     its parent's barcode. A GRN line with a new MRP makes one on its own;
//     "Add MRP lot" makes one ahead of the goods.
//   - A printed code shared by several colours/sizes of one product.
// Scanning such a code returns `options`; ScanChoice lets a person pick.
import { useState } from "react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { ProductDetail, ProductDetailVariant } from "@/lib/types";
import { rupees } from "@/lib/format";
import { currencySymbol } from "@/lib/currency";
import { usePermissions, missingPermissionHint } from "@/lib/permissions";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

/** One variant a scanned code may mean (GET /products/barcode/{code}). */
export interface ScanOption {
  variant_id: string;
  sku: string;
  /** The variant's attribute values ("Red · L"); "" for a product with one variant. */
  label: string;
  mrp: string;
  selling_price: string;
  /** The variant this is an MRP lot of; null for the variant itself. */
  mrp_lot_of: string | null;
  /** What the asked branch can sell now; null when no branch was asked. */
  available: string | null;
}

/** The fields every barcode lookup shares, plus the choice (migrations/083). */
export interface ScanResult {
  product_id: string;
  product_name: string;
  variant_id: string;
  sku: string;
  mrp?: string;
  selling_price?: string;
  choice_required?: boolean;
  options?: ScanOption[];
}

export function optionText(o: ScanOption): string {
  return [o.label, `MRP ${rupees(o.mrp)}`].filter(Boolean).join(" · ");
}

/** "Which one is it?" — every option of a scanned code, to pick from. */
export function ScanChoice({ result, onPick, onCancel }: { result: ScanResult; onPick: (o: ScanOption) => void; onCancel: () => void }) {
  const opts = result.options ?? [];
  const colours = new Set(opts.map((o) => o.mrp_lot_of ?? o.variant_id)).size > 1;
  return (
    <div className="flex flex-col gap-2 rounded-md border bg-muted/50 p-3" data-testid="scan-choice" role="group" aria-label="Choose the item">
      <p className="text-sm font-medium">
        {result.product_name}: {colours ? "this code is on more than one item — which one is it?" : "which MRP is printed on the pack?"}
      </p>
      <div className="flex flex-wrap gap-2">
        {opts.map((o) => (
          <Button key={o.variant_id} type="button" variant="outline" className="h-auto flex-col items-start py-1.5" onClick={() => onPick(o)}>
            <span className="font-medium">{optionText(o)}</span>
            <span className="text-xs font-normal text-muted-foreground">
              {o.sku} · sells at {rupees(o.selling_price)}
              {o.available != null ? ` · ${Number(o.available)} in stock` : ""}
            </span>
          </Button>
        ))}
        <Button type="button" variant="ghost" onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </div>
  );
}

/** Add a lot for a new MRP of this variant (pricing.manage). */
export function AddMrpLot({ variant, onSaved }: { variant: ProductDetailVariant; onSaved: (p: ProductDetail) => void }) {
  const { can } = usePermissions();
  const allowed = can("pricing.manage");
  const [open, setOpen] = useState(false);
  const [mrp, setMrp] = useState("");
  const [price, setPrice] = useState("");
  const [busy, setBusy] = useState(false);
  const mrpN = Number(mrp);
  const priceN = Number(price);
  const problem =
    mrp === "" || price === ""
      ? null
      : !(mrpN > 0) || !(priceN > 0) || priceN > mrpN
        ? "The selling price must be above 0 and not above the MRP"
        : Math.abs(mrpN - Number(variant.mrp)) < 0.005
          ? "That is this item's own MRP"
          : null;

  async function add(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      const p = await api.post<ProductDetail>(`/api/v1/products/variants/${variant.variant_id}/mrp-lots`, { mrp: mrpN, selling_price: priceN });
      toast.success(`MRP lot at ${rupees(mrp)} ready — receive its stock on a GRN with that MRP`);
      onSaved(p);
      setOpen(false);
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : "Could not add the MRP lot");
    } finally {
      setBusy(false);
    }
  }

  if (!open) {
    return (
      <Button
        type="button"
        variant="ghost"
        size="sm"
        className="w-fit"
        disabled={!allowed}
        title={allowed ? undefined : missingPermissionHint("pricing.manage")}
        onClick={() => setOpen(true)}
        data-testid={`add-mrp-lot-${variant.sku}`}
      >
        + Add MRP lot (new MRP, same barcode)
      </Button>
    );
  }
  return (
    <form onSubmit={add} className="flex flex-col gap-3 rounded-md border bg-muted/40 p-3" data-testid={`mrp-lot-form-${variant.sku}`}>
      <p className="text-xs text-muted-foreground">
        For stock arriving at a new MRP while the old stock is still on the shelf. Both sell under the same barcode: the till takes the one in stock, or
        asks which MRP is on the pack when both are. A GRN line with a new MRP does this on its own.
      </p>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
        <div className="flex flex-col gap-2">
          <Label htmlFor={`lot-mrp-${variant.variant_id}`}>New MRP ({currencySymbol()})</Label>
          <Input id={`lot-mrp-${variant.variant_id}`} type="number" min="0.01" step="0.01" required value={mrp} onChange={(e) => setMrp(e.target.value)} />
        </div>
        <div className="flex flex-col gap-2">
          <Label htmlFor={`lot-price-${variant.variant_id}`}>Selling price ({currencySymbol()})</Label>
          <Input
            id={`lot-price-${variant.variant_id}`}
            type="number"
            min="0.01"
            step="0.01"
            required
            value={price}
            onChange={(e) => setPrice(e.target.value)}
          />
        </div>
        <div className="flex items-end gap-2">
          <Button type="submit" disabled={busy || problem != null || mrp === "" || price === ""}>
            Add lot
          </Button>
          <Button type="button" variant="ghost" onClick={() => setOpen(false)}>
            Cancel
          </Button>
        </div>
      </div>
      {problem && <p className="text-sm text-destructive">{problem}</p>}
    </form>
  );
}
