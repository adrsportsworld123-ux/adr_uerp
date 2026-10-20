"use client";

// Choose one of the business's bank accounts (erp-core-go migrations/066):
// merchant-level ones and the branch's own. "" = Bank (1002) when
// allowNone, the account every payment used before bank accounts existed.
import { useEffect, useState } from "react";
import { api } from "@/lib/api-client";
import type { BankAccount } from "@/lib/types";
import { selectClass } from "@/components/data/panel";

export function useBankAccounts(branchId?: string) {
  const [accounts, setAccounts] = useState<BankAccount[]>([]);
  useEffect(() => {
    const qs = new URLSearchParams({ status: "active", ...(branchId ? { branch_id: branchId } : {}) });
    api
      .get<{ bank_accounts: BankAccount[] }>(`/api/v1/bank-accounts?${qs}`)
      .then((d) => setAccounts(d.bank_accounts))
      .catch(() => setAccounts([]));
  }, [branchId]);
  return accounts;
}

/** The default account for a branch: its own default, else the merchant's, else the first. */
export function defaultAccount(accounts: BankAccount[], branchId?: string): BankAccount | undefined {
  return (
    accounts.find((a) => a.is_default && branchId && a.branch_id === branchId) ??
    accounts.find((a) => a.is_default && !a.branch_id) ??
    accounts[0]
  );
}

export function BankAccountSelect({
  id,
  accounts,
  value,
  onChange,
  allowNone,
  noneLabel = "Bank (main ledger)",
  className = "w-64",
}: {
  id?: string;
  accounts: BankAccount[];
  value: string;
  onChange: (id: string) => void;
  allowNone?: boolean;
  noneLabel?: string;
  className?: string;
}) {
  return (
    <select id={id} value={value} onChange={(e) => onChange(e.target.value)} className={`${selectClass} ${className}`}>
      {(allowNone || accounts.length === 0) && <option value="">{noneLabel}</option>}
      {accounts.map((a) => (
        <option key={a.bank_account_id} value={a.bank_account_id}>
          {a.display_name}
          {a.branch_name ? ` · ${a.branch_name}` : ""}
          {a.is_default ? " (default)" : ""}
        </option>
      ))}
    </select>
  );
}
