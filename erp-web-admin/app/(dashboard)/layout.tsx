"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { ReactNode } from "react";
import { useRequireAuth, useAuth } from "@/lib/auth";
import { Button } from "@/components/ui/button";

const NAV = [
  { href: "/dashboard", label: "Dashboard" },
  { href: "/customers", label: "Customers" },
  { href: "/promotions", label: "Promotions & Loyalty" },
  { href: "/branches", label: "Branches" },
  { href: "/transfers", label: "Branch Transfers" },
  { href: "/suppliers", label: "Suppliers" },
  { href: "/purchase/grn", label: "Goods Receipt (GRN)" },
  { href: "/purchase/bills", label: "Purchase Bills" },
  { href: "/purchase/returns", label: "Purchase Returns" },
  { href: "/pricing", label: "Pricing" },
  { href: "/search", label: "Product Search" },
  { href: "/accounting/chart-of-accounts", label: "Chart of Accounts" },
  { href: "/accounting/journal-entries/new", label: "Journal Entry" },
  { href: "/accounting/party-ledger", label: "Party Ledger" },
  { href: "/accounting/day-book", label: "Day Book" },
  { href: "/accounting/cash-book", label: "Cash Book" },
];

export default function DashboardLayout({ children }: { children: ReactNode }) {
  const { ready, userId } = useRequireAuth();
  const { logout } = useAuth();
  const pathname = usePathname();

  if (!ready || !userId) {
    return <div className="flex flex-1 items-center justify-center text-sm text-zinc-500">Loading…</div>;
  }

  return (
    <div className="flex flex-1">
      <aside className="w-64 shrink-0 border-r bg-white p-4 flex flex-col gap-1">
        <div className="px-2 py-2 mb-2">
          <p className="font-semibold">ERP Admin</p>
          <p className="text-xs text-zinc-500">Purchase & Accounting</p>
        </div>
        {NAV.map((item) => (
          <Link
            key={item.href}
            href={item.href}
            className={`rounded px-2 py-1.5 text-sm ${
              pathname === item.href ? "bg-zinc-900 text-white" : "text-zinc-700 hover:bg-zinc-100"
            }`}
          >
            {item.label}
          </Link>
        ))}
        <div className="mt-auto pt-4">
          <Button variant="outline" size="sm" className="w-full" onClick={logout}>
            Log out
          </Button>
        </div>
      </aside>
      <main className="flex-1 p-6 overflow-auto">{children}</main>
    </div>
  );
}
