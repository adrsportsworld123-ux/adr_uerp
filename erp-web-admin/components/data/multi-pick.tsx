"use client";

// Several choices from one list, as removable chips: the picker (a
// SearchSelect, or a ProductPicker for products) adds one at a time and is
// ready for the next. Empty means "no limit" wherever it is used, so the
// caller says what empty means in `emptyText`.
import { X } from "lucide-react";
import { SearchOption, SearchSelect } from "@/components/data/search-select";
import { ProductPicker } from "@/components/data/product-picker";

export type PickedRef = { id: string; name: string };

function Chips({ value, onChange, emptyText, what }: { value: PickedRef[]; onChange: (next: PickedRef[]) => void; emptyText: string; what: string }) {
  if (value.length === 0) return <p className="text-xs text-muted-foreground">{emptyText}</p>;
  return (
    <ul className="flex flex-wrap gap-1.5" aria-label={`Chosen ${what}`}>
      {value.map((v) => (
        <li key={v.id} className="flex items-center gap-1 rounded-full border bg-muted/50 py-0.5 ps-2.5 pe-1 text-xs">
          {v.name}
          <button
            type="button"
            className="rounded-full p-0.5 hover:bg-muted"
            aria-label={`Remove ${v.name}`}
            onClick={() => onChange(value.filter((x) => x.id !== v.id))}
          >
            <X className="size-3" aria-hidden />
          </button>
        </li>
      ))}
    </ul>
  );
}

export function MultiPick({
  id,
  what,
  options,
  value,
  onChange,
  placeholder,
  emptyText,
}: {
  id: string;
  /** Plural, lower case: "categories". */
  what: string;
  options: SearchOption[];
  value: PickedRef[];
  onChange: (next: PickedRef[]) => void;
  placeholder: string;
  emptyText: string;
}) {
  const left = options.filter((o) => !value.some((v) => v.id === o.value));
  return (
    <div className="flex flex-col gap-2">
      <SearchSelect
        id={id}
        ariaLabel={`Add ${what}`}
        value=""
        onChange={(v) => {
          const o = options.find((x) => x.value === v);
          if (o) onChange([...value, { id: o.value, name: o.label }]);
        }}
        options={left}
        placeholder={placeholder}
      />
      <Chips value={value} onChange={onChange} emptyText={emptyText} what={what} />
    </div>
  );
}

export function MultiProductPick({
  id,
  value,
  onChange,
  emptyText,
}: {
  id: string;
  value: PickedRef[];
  onChange: (next: PickedRef[]) => void;
  emptyText: string;
}) {
  return (
    <div className="flex flex-col gap-2">
      <ProductPicker
        id={id}
        ariaLabel="Add products"
        mode="product"
        value={null}
        onChange={(o) => {
          if (!value.some((v) => v.id === o.productId)) onChange([...value, { id: o.productId, name: o.productName || o.label }]);
        }}
        placeholder="Search a product to add"
      />
      <Chips value={value} onChange={onChange} emptyText={emptyText} what="products" />
    </div>
  );
}
