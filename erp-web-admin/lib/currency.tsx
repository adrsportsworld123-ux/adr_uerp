"use client";

// The merchant's currency (erp-core-go migrations/061, Tax Settings): how
// rupees() names and groups money everywhere in the back office. INR keeps
// Indian grouping (₹1,23,456.50); any other currency groups in thousands
// ($123,456.50). Amounts are never converted — this only names them. A
// branch's own currency applies to its printed documents and its POS; the
// back office, which adds branches up, shows the merchant's.
import { ReactNode, useEffect, useState } from "react";
import { api } from "@/lib/api-client";

export type Currency = { code: string; symbol: string };

let current: Currency = { code: "INR", symbol: "₹" };
let grouping = new Intl.NumberFormat("en-IN", { minimumFractionDigits: 2, maximumFractionDigits: 2 });

export function setCurrency(c: Currency) {
  current = c;
  grouping = new Intl.NumberFormat(c.code === "INR" ? "en-IN" : "en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}

/** The ISO 4217 code, e.g. "INR". */
export function currencyCode(): string {
  return current.code;
}

/** The printed symbol, e.g. "₹", "$", "AED". */
export function currencySymbol(): string {
  return current.symbol;
}

/** The symbol as it prefixes an amount: a letter symbol gets a space ("AED 10.00"). */
export function currencyPrefix(): string {
  return /\p{L}$/u.test(current.symbol) ? `${current.symbol} ` : current.symbol;
}

export function groupMoney(n: number): string {
  return grouping.format(n);
}

/** Loads the merchant's currency once signed in; children re-render with it. */
export function CurrencyProvider({ children }: { children: ReactNode }) {
  const [code, setCode] = useState(current.code + current.symbol);
  useEffect(() => {
    api
      .get<{ merchant: { currency_code?: string; currency_symbol?: string } }>("/api/v1/tax/settings")
      .then((s) => {
        const { currency_code: c, currency_symbol: sym } = s.merchant;
        // Only a change re-renders the screens (INR / ₹ is already in place).
        if (c && sym && c + sym !== current.code + current.symbol) {
          setCurrency({ code: c, symbol: sym });
          setCode(c + sym);
        }
      })
      .catch(() => {}); // an older API: stay on INR
  }, []);
  return <div key={code} className="contents">{children}</div>;
}
