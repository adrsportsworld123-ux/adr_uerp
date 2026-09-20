"use client";

import Link from "next/link";
import { useEffect, useRef, useState } from "react";
import { api, ApiError } from "@/lib/api-client";
import { SearchHit, SearchResponse } from "@/lib/types";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { buttonVariants } from "@/components/ui/button";

function marginPct(sellingPrice: number, costPrice: number): number | null {
  if (sellingPrice <= 0) return null;
  return Math.round(((sellingPrice - costPrice) / sellingPrice) * 100 * 100) / 100;
}

// Product Search (internal/search — OpenSearch-backed), distinct from the
// Pricing screen's GET /products browse: this one is free-text and
// typo-tolerant (fuzzy multi_match server-side), and degrades to a plain
// "search unavailable" message rather than crashing when OpenSearch isn't
// configured/reachable — SEARCH_UNAVAILABLE is an expected, not
// exceptional, response shape for this one screen.
export default function SearchPage() {
  const [q, setQ] = useState("");
  const [minPrice, setMinPrice] = useState("");
  const [maxPrice, setMaxPrice] = useState("");
  const [results, setResults] = useState<SearchHit[] | null>(null);
  const [total, setTotal] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const [unavailable, setUnavailable] = useState(false);
  const [loading, setLoading] = useState(false);
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  function runSearch() {
    setLoading(true);
    setError(null);
    setUnavailable(false);
    const params = new URLSearchParams();
    if (q) params.set("q", q);
    if (minPrice) params.set("min_price", minPrice);
    if (maxPrice) params.set("max_price", maxPrice);
    api
      .get<SearchResponse>(`/api/v1/products/search?${params.toString()}`)
      .then((d) => {
        setResults(d.results ?? []);
        setTotal(d.total);
      })
      .catch((e) => {
        if (e instanceof ApiError && e.code === "SEARCH_UNAVAILABLE") {
          setUnavailable(true);
        } else {
          setError(e instanceof ApiError ? e.message : "Could not reach the server");
        }
      })
      .finally(() => setLoading(false));
  }

  // Debounced live search as the user types/adjusts filters — this is a
  // typeahead-style screen (FRD: "<=50ms autocomplete"), not a
  // submit-a-form one.
  useEffect(() => {
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(runSearch, 300);
    return () => {
      if (debounceRef.current) clearTimeout(debounceRef.current);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [q, minPrice, maxPrice]);

  return (
    <div className="flex flex-col gap-6 max-w-4xl">
      <h1 className="text-2xl font-semibold">Product Search</h1>

      <Card>
        <CardContent className="flex flex-col gap-4 pt-6">
          <div className="flex gap-2 items-end flex-wrap">
            <div className="flex flex-col gap-2 flex-1 min-w-48">
              <Label htmlFor="q">Search</Label>
              <Input id="q" placeholder="Name, SKU, or HSN code" value={q} onChange={(e) => setQ(e.target.value)} />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="minPrice">Min price (₹)</Label>
              <Input id="minPrice" type="number" step="0.01" className="w-32" value={minPrice} onChange={(e) => setMinPrice(e.target.value)} />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="maxPrice">Max price (₹)</Label>
              <Input id="maxPrice" type="number" step="0.01" className="w-32" value={maxPrice} onChange={(e) => setMaxPrice(e.target.value)} />
            </div>
          </div>
          <p className="text-xs text-zinc-500">
            No category/brand filter here yet — there&apos;s no category/brand list endpoint to populate a picker from (same gap as the Pricing screen&apos;s bulk-update filter).
          </p>
        </CardContent>
      </Card>

      {unavailable && (
        <Card>
          <CardContent className="pt-6 text-sm text-amber-700">
            Product search is temporarily unavailable. The rest of the app is unaffected — try again shortly, or use the Pricing screen&apos;s catalog browse instead.
          </CardContent>
        </Card>
      )}
      {error && <p className="text-sm text-red-600">{error}</p>}

      {!unavailable && results && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">
              {total} result{total === 1 ? "" : "s"}
              {loading && <span className="text-zinc-400 font-normal"> · searching…</span>}
            </CardTitle>
          </CardHeader>
          <CardContent>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Product</TableHead>
                  <TableHead>SKU</TableHead>
                  <TableHead>Category</TableHead>
                  <TableHead>Brand</TableHead>
                  <TableHead>Cost</TableHead>
                  <TableHead>Selling</TableHead>
                  <TableHead>Margin</TableHead>
                  <TableHead></TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {results.map((hit) => {
                  const margin = marginPct(hit.selling_price, hit.cost_price);
                  return (
                    <TableRow key={hit.variant_id}>
                      <TableCell>{hit.name}</TableCell>
                      <TableCell className="font-mono text-xs">{hit.sku}</TableCell>
                      <TableCell>{hit.category_name || "—"}</TableCell>
                      <TableCell>{hit.brand_name || "—"}</TableCell>
                      <TableCell>₹{hit.cost_price}</TableCell>
                      <TableCell>₹{hit.selling_price}</TableCell>
                      <TableCell className={margin !== null && margin < 0 ? "text-red-600 font-medium" : ""}>{margin ?? "—"}%</TableCell>
                      <TableCell>
                        <Link href="/pricing" className={buttonVariants({ variant: "outline", size: "sm" })}>
                          Edit price
                        </Link>
                      </TableCell>
                    </TableRow>
                  );
                })}
                {results.length === 0 && !loading && (
                  <TableRow>
                    <TableCell colSpan={8} className="text-center text-sm text-zinc-500 py-6">
                      No matching products
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      )}
    </div>
  );
}
