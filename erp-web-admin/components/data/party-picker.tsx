"use client";

// Choose a customer, a supplier, an employee or one of the business's bank
// accounts (erp-core-go migrations/067) for a ledger line. Searches the server
// as you type (a business can have thousands of customers, more than one
// page of /customers). The caller keeps the chosen party's name (onChange
// hands it over) and passes it back as initialLabel, so it still shows once
// the search results move on.
import { useEffect, useState } from "react";
import { api } from "@/lib/api-client";
import { Customer, Supplier } from "@/lib/types";
import { SearchSelect, SearchOption } from "@/components/data/search-select";

export type PartyType = "customer" | "supplier" | "employee" | "bank_account";

export const PARTY_LABEL: Record<PartyType, string> = { customer: "Customer", supplier: "Supplier", employee: "Employee", bank_account: "Bank account" };

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
          : type === "supplier"
            ? api.get<{ suppliers: Supplier[] }>(`/api/v1/suppliers?q=${q}&limit=50&status=active`).then((d) =>
                d.suppliers.map((s) => ({ value: s.supplier_id, label: s.name, hint: s.gstin ?? undefined })),
              )
            : api.get<{ parties: { id: string; name: string; hint: string }[] }>(`/api/v1/accounting/parties?type=${type}&q=${q}`).then((d) =>
                d.parties.map((p) => ({ value: p.id, label: p.name, hint: p.hint || undefined })),
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
      placeholder={`Choose ${type === "employee" ? "an employee" : `a ${PARTY_LABEL[type].toLowerCase()}`}`}
      searchPlaceholder={
        { customer: "Name, phone or GSTIN", supplier: "Name or GSTIN", employee: "Name or employee code", bank_account: "Bank, name or account number" }[type]
      }
      ariaLabel={ariaLabel}
    />
  );
}
