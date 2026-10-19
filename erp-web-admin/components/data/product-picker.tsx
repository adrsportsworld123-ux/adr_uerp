"use client";

import { useEffect, useRef, useState } from "react";
import { Popover } from "@base-ui/react/popover";
import { Check, ChevronDown, Loader2, Search } from "lucide-react";
import { api } from "@/lib/api-client";
import { Product } from "@/lib/types";

import { rupees } from "@/lib/format";
export type VariantOption = { variantId: string; productId: string; productName: string; sku: string; sellingPrice: string; label: string; trackBatch: boolean };

function toOptions(products: Product[], mode: "variant" | "product"): VariantOption[] {
  if (mode === "product") {
    return products.map((p) => ({
      variantId: p.product_id,
      productId: p.product_id,
      productName: p.name,
      sku: p.variants.length === 1 ? p.variants[0].sku : `${p.variants.length} variants`,
      sellingPrice: p.variants[0]?.selling_price ?? "",
      label: p.name,
      trackBatch: false,
    }));
  }
  return products.flatMap((p) =>
    p.variants.map((v) => ({
      variantId: v.variant_id,
      productId: p.product_id,
      productName: p.name,
      sku: v.sku,
      sellingPrice: v.selling_price,
      label: `${p.name} (${v.sku})`,
      trackBatch: v.track_batch,
    })),
  );
}

// Product/variant picker backed by server search (GET /products?q=, which
// matches name, SKU or barcode). Replaces "load the first N products into
// a dropdown", which silently hides everything past N on a real catalog.
// With an empty query it shows recently updated products first, so a
// product created a moment ago is right at the top.
export function ProductPicker({
  value,
  onChange,
  placeholder = "Select a product",
  status = "active",
  mode = "variant",
  id,
  ariaLabel,
}: {
  value: VariantOption | null;
  onChange: (option: VariantOption) => void;
  placeholder?: string;
  status?: "active" | "all";
  /** "product" lists each product once (for a choice that applies to the whole product). */
  mode?: "variant" | "product";
  id?: string;
  ariaLabel?: string;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [options, setOptions] = useState<VariantOption[]>([]);
  const [loading, setLoading] = useState(false);
  const [active, setActive] = useState(0);
  const listRef = useRef<HTMLUListElement>(null);

  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    const t = setTimeout(() => {
      setLoading(true);
      const params = new URLSearchParams({ status, limit: "20", page: "1" });
      if (query.trim()) params.set("q", query.trim());
      else {
        params.set("sort", "updated");
        params.set("dir", "desc");
      }
      api
        .get<{ products: Product[] }>(`/api/v1/products?${params.toString()}`)
        .then((d) => {
          if (cancelled) return;
          setOptions(toOptions(d.products, mode));
          setActive(0);
        })
        .catch(() => !cancelled && setOptions([]))
        .finally(() => !cancelled && setLoading(false));
    }, 200);
    return () => {
      cancelled = true;
      clearTimeout(t);
    };
  }, [open, query, status, mode]);

  function choose(o: VariantOption | undefined) {
    if (!o) return;
    onChange(o);
    setOpen(false);
    setQuery("");
  }

  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Popover.Trigger id={id} aria-label={ariaLabel} className="flex h-9 w-full min-w-0 items-center justify-between gap-2 rounded-lg border bg-card px-3 text-left text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/30">
        <span className={`truncate ${value ? "" : "text-muted-foreground"}`}>{value ? value.label : placeholder}</span>
        <ChevronDown className="size-4 shrink-0 text-muted-foreground" aria-hidden />
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Positioner sideOffset={4} align="start" className="z-50">
          <Popover.Popup className="w-[min(420px,calc(100vw-32px))] overflow-hidden rounded-lg border bg-popover text-popover-foreground shadow-xl outline-none">
            <div className="flex items-center gap-2 border-b px-3">
              <Search className="size-4 text-muted-foreground" aria-hidden />
              <input
                autoFocus
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "ArrowDown") {
                    e.preventDefault();
                    setActive((a) => Math.min(a + 1, options.length - 1));
                  } else if (e.key === "ArrowUp") {
                    e.preventDefault();
                    setActive((a) => Math.max(a - 1, 0));
                  } else if (e.key === "Enter") {
                    e.preventDefault();
                    choose(options[active]);
                  }
                }}
                placeholder="Search name, SKU or barcode"
                aria-label="Search products"
                className="h-10 flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
              />
              {loading && <Loader2 className="size-4 animate-spin text-muted-foreground" aria-hidden />}
            </div>
            <ul ref={listRef} role="listbox" aria-label="Products" className="max-h-72 overflow-y-auto p-1">
              {!loading && options.length === 0 && (
                <li className="px-3 py-6 text-center text-sm text-muted-foreground">
                  {query ? `No active product matches “${query}”.` : "No products yet."}
                </li>
              )}
              {options.map((o, i) => (
                <li
                  key={o.variantId}
                  role="option"
                  aria-selected={value?.variantId === o.variantId}
                  onMouseEnter={() => setActive(i)}
                  onClick={() => choose(o)}
                  className={`flex cursor-pointer items-center justify-between gap-3 rounded-md px-2.5 py-2 text-sm ${i === active ? "bg-accent text-accent-foreground" : ""}`}
                >
                  <span className="min-w-0">
                    <span className="block truncate">{o.label}</span>
                    {mode === "product" && <span className="block truncate text-xs text-muted-foreground">{o.sku}</span>}
                  </span>
                  <span className="flex shrink-0 items-center gap-2 text-xs text-muted-foreground">
                    <span className="tabular">{rupees(o.sellingPrice)}</span>
                    {value?.variantId === o.variantId && <Check className="size-3.5 text-primary" aria-hidden />}
                  </span>
                </li>
              ))}
            </ul>
          </Popover.Popup>
        </Popover.Positioner>
      </Popover.Portal>
    </Popover.Root>
  );
}
