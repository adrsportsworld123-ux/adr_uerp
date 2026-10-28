"use client";

// The business's partners (erp-core-go migrations/074), for the forms that
// let a partner pay from their own money: a supplier bill, an expense, a
// purchase order's funding. GET /partners is open to any signed-in user
// (capital balances only come with partners.manage). No partners = the
// forms look as they always did.
import { useEffect, useState } from "react";
import { api } from "@/lib/api-client";
import { Partner } from "@/lib/types";
import { selectClass } from "@/components/data/panel";

export function usePartners(includeRetired = false) {
  const [partners, setPartners] = useState<Partner[]>([]);
  useEffect(() => {
    api
      .get<{ partners: Partner[] }>("/api/v1/partners")
      .then((d) => setPartners(includeRetired ? d.partners : d.partners.filter((p) => p.status === "active")))
      .catch(() => setPartners([]));
  }, [includeRetired]);
  return partners;
}

export function PartnerSelect({
  id,
  partners,
  value,
  onChange,
  noneLabel,
  className = "w-full",
}: {
  id?: string;
  partners: Partner[];
  value: string;
  onChange: (id: string) => void;
  /** Offer "none" with this label. */
  noneLabel?: string;
  className?: string;
}) {
  return (
    <select id={id} value={value} onChange={(e) => onChange(e.target.value)} className={`${selectClass} ${className}`}>
      {noneLabel != null ? <option value="">{noneLabel}</option> : !value && <option value="">Choose a partner</option>}
      {partners.map((p) => (
        <option key={p.partner_id} value={p.partner_id}>
          {p.name}
          {p.status !== "active" ? " (retired)" : ""}
        </option>
      ))}
    </select>
  );
}
