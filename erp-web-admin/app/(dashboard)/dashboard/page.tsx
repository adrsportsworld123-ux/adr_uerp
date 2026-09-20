import Link from "next/link";
import { Card, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";

const SHORTCUTS = [
  { href: "/customers", title: "Customers", description: "Profiles, auto-segmentation, and purchase history" },
  { href: "/suppliers/new", title: "New supplier", description: "Register a supplier before receiving stock from them" },
  { href: "/purchase/grn/new", title: "New GRN", description: "Record goods received from a supplier" },
  { href: "/purchase/bills", title: "Purchase bills", description: "Bill a completed GRN and track payments" },
  { href: "/accounting/day-book", title: "Day book", description: "Every journal entry posted today" },
  { href: "/accounting/party-ledger", title: "Party ledger", description: "What you owe each supplier" },
  { href: "/transfers/new", title: "New branch transfer", description: "Move stock from one branch to another" },
  { href: "/branches", title: "Branches", description: "Manage branches" },
  { href: "/pricing", title: "Pricing", description: "Margins, calculator, and bulk price updates" },
  { href: "/search", title: "Product Search", description: "Fast, typo-tolerant catalog search" },
];

export default function DashboardHome() {
  return (
    <div className="flex flex-col gap-6 max-w-3xl">
      <div>
        <h1 className="text-2xl font-semibold">Dashboard</h1>
        <p className="text-sm text-zinc-500">Purchase Management, Ledger & Accounting, Multi-Branch, and Pricing for the ERP backend.</p>
      </div>
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
        {SHORTCUTS.map((s) => (
          <Link key={s.href} href={s.href}>
            <Card className="hover:border-zinc-400 transition-colors h-full">
              <CardHeader>
                <CardTitle className="text-base">{s.title}</CardTitle>
                <CardDescription>{s.description}</CardDescription>
              </CardHeader>
            </Card>
          </Link>
        ))}
      </div>
    </div>
  );
}
