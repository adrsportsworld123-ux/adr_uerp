import type { LucideIcon } from "lucide-react";
import {
  BookOpen,
  Boxes,
  Building2,
  ChartColumn,
  LayoutDashboard,
  Package,
  Receipt,
  ShieldCheck,
  Sparkles,
  Tags,
  Truck,
  Users,
} from "lucide-react";

// Sidebar structure (Phase 11, erp-core-go/docs/phased_roadmap.md). The
// labels and hrefs are the same ones the old flat list used, so bookmarks,
// e2e `goto`s and any text a spec matches on are unchanged. Only the
// grouping is new. `match` lists extra path prefixes that should highlight
// an item (e.g. /products/[id]/edit highlights Products).
export type NavItem = { href: string; label: string; match?: string[] };
export type NavGroup = { id: string; label: string; icon: LucideIcon; items: NavItem[] };

export const NAV_GROUPS: NavGroup[] = [
  {
    id: "overview",
    label: "Overview",
    icon: LayoutDashboard,
    items: [
      { href: "/dashboard", label: "Dashboard" },
      { href: "/ai-insights", label: "AI Insights" },
      { href: "/ai-assistant", label: "Ask AI / Copilot" },
    ],
  },
  {
    id: "sales",
    label: "Sales",
    icon: Receipt,
    items: [
      { href: "/quotations", label: "B2B Quotations" },
      { href: "/prescriptions", label: "Prescriptions" },
    ],
  },
  {
    id: "catalog",
    label: "Catalog",
    icon: Package,
    items: [
      { href: "/products", label: "Products", match: ["/products/"] },
      { href: "/catalog/categories", label: "Categories" },
      { href: "/catalog/brands", label: "Brands" },
      { href: "/catalog/tax-slabs", label: "Tax Slabs" },
      { href: "/attributes", label: "Product Attributes" },
      { href: "/search", label: "Product Search" },
    ],
  },
  {
    id: "pricing",
    label: "Pricing & Promotions",
    icon: Tags,
    items: [
      { href: "/pricing", label: "Pricing" },
      { href: "/pricing/price-lists", label: "Wholesale Price Lists" },
      { href: "/pricing/scheduled-changes", label: "Scheduled Price Changes" },
      { href: "/pricing/rules", label: "Price Rules" },
      { href: "/promotions", label: "Promotions & Loyalty" },
    ],
  },
  {
    id: "inventory",
    label: "Inventory",
    icon: Boxes,
    items: [
      { href: "/inventory", label: "Inventory" },
      { href: "/inventory/expiring-batches", label: "Expiring Batches" },
      { href: "/transfers", label: "Branch Transfers", match: ["/transfers/"] },
      { href: "/inventory-reconciliation", label: "Inventory Reconciliation" },
    ],
  },
  {
    id: "purchasing",
    label: "Purchasing",
    icon: Truck,
    items: [
      { href: "/suppliers", label: "Suppliers", match: ["/suppliers/"] },
      { href: "/purchase/grn", label: "Goods Receipt (GRN)", match: ["/purchase/grn/"] },
      { href: "/purchase/bills", label: "Purchase Bills", match: ["/purchase/bills/"] },
      { href: "/purchase/returns", label: "Purchase Returns", match: ["/purchase/returns/"] },
    ],
  },
  {
    id: "customers",
    label: "Customers",
    icon: Users,
    items: [
      { href: "/customers", label: "Customers", match: ["/customers/"] },
      { href: "/notifications", label: "Notifications" },
    ],
  },
  {
    id: "finance",
    label: "Finance",
    icon: BookOpen,
    items: [
      { href: "/accounting/chart-of-accounts", label: "Chart of Accounts" },
      { href: "/accounting/journal-entries/new", label: "Journal Entry" },
      { href: "/accounting/party-ledger", label: "Party Ledger" },
      { href: "/accounting/day-book", label: "Day Book" },
      { href: "/accounting/cash-book", label: "Cash Book" },
      { href: "/gst", label: "GST Returns" },
      { href: "/e-invoicing", label: "E-Invoicing & E-Way Bill" },
      { href: "/bank-reconciliation", label: "Bank Reconciliation" },
      { href: "/cash-reconciliation", label: "Cash Reconciliation" },
      { href: "/payment-gateway-reconciliation", label: "Payment Gateway Reconciliation" },
    ],
  },
  {
    id: "reports",
    label: "Reports",
    icon: ChartColumn,
    items: [{ href: "/reports", label: "Reports" }],
  },
  {
    id: "organization",
    label: "Organization",
    icon: Building2,
    items: [
      { href: "/branches", label: "Branches" },
      { href: "/hr/employees", label: "Employees" },
      { href: "/hr/shifts", label: "Shifts" },
      { href: "/hr/statutory-config", label: "Statutory Config" },
      { href: "/payroll", label: "Payroll" },
    ],
  },
  {
    id: "access",
    label: "Access & Audit",
    icon: ShieldCheck,
    items: [
      { href: "/rbac", label: "Roles & Permissions" },
      { href: "/audit-logs", label: "Audit Log" },
    ],
  },
];

// Extra destinations the command palette can jump to that aren't sidebar
// entries of their own (create screens reached from a page action).
export const EXTRA_DESTINATIONS: (NavItem & { group: string })[] = [
  { href: "/products/new", label: "New product", group: "Catalog" },
  { href: "/customers/new", label: "New customer", group: "Customers" },
  { href: "/suppliers/new", label: "New supplier", group: "Purchasing" },
  { href: "/purchase/grn/new", label: "New goods receipt", group: "Purchasing" },
  { href: "/purchase/bills/new", label: "New purchase bill", group: "Purchasing" },
  { href: "/purchase/returns/new", label: "New purchase return", group: "Purchasing" },
  { href: "/transfers/new", label: "New branch transfer", group: "Inventory" },
];

export const ASSISTANT_ICON = Sparkles;

function itemMatches(item: NavItem, pathname: string): boolean {
  if (pathname === item.href) return true;
  return (item.match ?? []).some((prefix) => pathname.startsWith(prefix));
}

// The most specific sidebar item for a path: an exact match wins over a
// prefix match, so /pricing/rules highlights "Price Rules", not "Pricing".
export function findActive(pathname: string): { group: NavGroup; item: NavItem } | null {
  for (const group of NAV_GROUPS) {
    for (const item of group.items) {
      if (pathname === item.href) return { group, item };
    }
  }
  for (const group of NAV_GROUPS) {
    for (const item of group.items) {
      if (itemMatches(item, pathname)) return { group, item };
    }
  }
  return null;
}
