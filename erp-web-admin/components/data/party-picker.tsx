"use client";

// Choose a customer or a supplier for a ledger line. Searches the server
// as you type (a business can have thousands of customers, more than one
// page of /customers). The caller keeps the chosen party's name (onChange
// hands it over) and passes it back as initialLabel, so it still shows once
// the search results move on.
import { useEffect, useState } from "react";
import { api } from "@/lib/api-client";
import { Customer, Supplier } from "@/lib/types";
import { SearchSelect, SearchOption } from "@/components/data/search-select";

export type PartyType = "customer" | "supplier";

export function PartyPicker({
  type,
  value,
  onChange,
  initialLabel,
  ariaLabel,
}: {
  type: PartyType;
  value: string;
  onChange: (id: string, label: string) => void;
  initialLabel?: string | null;
  ariaLabel?: string;
}) {
  const [options, setOptions] = useState<SearchOption[]>([]);
  const [query, setQuery] = useState("");

  useEffect(() => {
    let live = true;
    const t = setTimeout(() => {
      const q = encodeURIComponent(query.trim());
      const load =
        type === "customer"
          ? api.get<{ customers: Customer[] }>(`/api/v1/customers?q=${q}&limit=50&status=active`).then((d) =>
              d.customers.map((c) => ({ value: c.customer_id, label: c.name, hint: c.phone ?? undefined })),
            )
          : api.get<{ suppliers: Supplier[] }>(`/api/v1/suppliers?q=${q}&limit=50&status=active`).then((d) =>
              d.suppliers.map((s) => ({ value: s.supplier_id, label: s.name, hint: s.gstin ?? undefined })),
            );
      load
        .then((opts) => {
          if (live) setOptions(opts);
        })
        .catch(() => {});
    }, 250);
    return () => {
      live = false;
      clearTimeout(t);
    };
  }, [type, query]);

  return (
    <SearchSelect
      value={value}
      options={options}
      onChange={(id) => onChange(id, options.find((o) => o.value === id)?.label ?? "")}
      onQueryChange={setQuery}
      selectedLabel={initialLabel ?? undefined}
      placeholder={type === "customer" ? "Choose a customer" : "Choose a supplier"}
      searchPlaceholder={type === "customer" ? "Name, phone or GSTIN" : "Name or GSTIN"}
      ariaLabel={ariaLabel}
    />
  );
}
