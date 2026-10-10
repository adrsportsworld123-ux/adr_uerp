"use client";

// The product master list — the entry point to editing a product
// (products/[id]/edit). Until this existed the only product table was the
// Pricing page, and an inactive or discontinued product disappeared from
// every screen, so it could never be found again to reactivate. The status
// filter here asks GET /products for exactly that.
import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Product } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, CardContent } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

const STATUS_BADGE: Record<string, "default" | "secondary" | "outline" | "destructive"> = {
  active: "default",
  inactive: "secondary",
  discontinued: "destructive",
  out_of_stock: "outline",
};

export default function ProductsPage() {
  const [products, setProducts] = useState<Product[]>([]);
  const [status, setStatus] = useState("all");
  const [q, setQ] = useState("");

  const load = useCallback(() => {
    const params = new URLSearchParams({ limit: "200", status });
    if (q.trim()) params.set("q", q.trim());
    api
      .get<{ products: Product[] }>(`/api/v1/products?${params.toString()}`)
      .then((d) => setProducts(d.products))
      .catch((e) => toast.error(e instanceof ApiError ? e.message : "Could not load products"));
  }, [status, q]);

  useEffect(() => {
    const t = setTimeout(load, 250); // debounce typing in the search box
    return () => clearTimeout(t);
  }, [load]);

  return (
    <div className="flex flex-col gap-6 max-w-5xl">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Products</h1>
        <Link href="/products/new">
          <Button>New product</Button>
        </Link>
      </div>

      <div className="flex gap-3 flex-wrap">
        <Input className="w-72" placeholder="Search by name" value={q} onChange={(e) => setQ(e.target.value)} aria-label="Search products" />
        <div className="w-48">
          <Select
            value={status}
            onValueChange={(v) => setStatus(v ?? "all")}
            items={{ all: "All statuses", active: "Active", inactive: "Inactive", discontinued: "Discontinued" }}
          >
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All statuses</SelectItem>
              <SelectItem value="active">Active</SelectItem>
              <SelectItem value="inactive">Inactive</SelectItem>
              <SelectItem value="discontinued">Discontinued</SelectItem>
            </SelectContent>
          </Select>
        </div>
      </div>

      <Card>
        <CardContent className="pt-6">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Product</TableHead>
                <TableHead>Variants</TableHead>
                <TableHead>Selling price</TableHead>
                <TableHead>Status</TableHead>
                <TableHead></TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {products.map((p) => {
                const prices = p.variants.map((v) => Number(v.selling_price));
                const lo = Math.min(...prices);
                const hi = Math.max(...prices);
                return (
                  <TableRow key={p.product_id}>
                    <TableCell className="font-medium">{p.name}</TableCell>
                    <TableCell className="text-sm text-muted-foreground">
                      {p.variants.map((v) => (
                        <div key={v.variant_id}>
                          {v.sku}
                          {v.status !== "active" && <span className="text-xs text-destructive"> · {v.status}</span>}
                        </div>
                      ))}
                    </TableCell>
                    <TableCell>{prices.length ? (lo === hi ? `₹${lo.toFixed(2)}` : `₹${lo.toFixed(2)} – ₹${hi.toFixed(2)}`) : "—"}</TableCell>
                    <TableCell>
                      <Badge variant={STATUS_BADGE[p.status] ?? "outline"}>{p.status}</Badge>
                    </TableCell>
                    <TableCell>
                      <Link href={`/products/${p.product_id}/edit`}>
                        <Button size="sm" variant="outline">
                          Edit
                        </Button>
                      </Link>
                    </TableCell>
                  </TableRow>
                );
              })}
              {products.length === 0 && (
                <TableRow>
                  <TableCell colSpan={5} className="text-center text-sm text-muted-foreground py-6">
                    No products match
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
    </div>
  );
}
