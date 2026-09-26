"use client";

// Phase 6 v1 — AI Platform (erp-core-go's phased_roadmap.md; internal/ai).
// Both features here are real SQL over this merchant's own transaction
// history, not a trained model: reorder suggestions (sales velocity vs.
// a caller-supplied lead time) and a market-basket "customers who bought
// this also bought" recommendation engine. Open to any authenticated
// user on the backend (not permission-gated) since neither exposes
// anything sensitive — no route-level restriction is enforced here
// either.
import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Branch, Product, ReorderSuggestion, Recommendation } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

export default function AIInsightsPage() {
  const [branches, setBranches] = useState<Branch[]>([]);
  const [branchId, setBranchId] = useState("");
  const [leadTimeDays, setLeadTimeDays] = useState("7");
  const [windowDays, setWindowDays] = useState("30");
  const [suggestions, setSuggestions] = useState<ReorderSuggestion[]>([]);
  const [loadingSuggestions, setLoadingSuggestions] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const [products, setProducts] = useState<Product[]>([]);
  const [variantId, setVariantId] = useState("");
  const [recommendations, setRecommendations] = useState<Recommendation[]>([]);
  const [sourceOrderCount, setSourceOrderCount] = useState<number | null>(null);
  const [loadingRecs, setLoadingRecs] = useState(false);

  // A plain fetch with no setState of its own — reused by the initial
  // effect (which must not call setState synchronously from an effect
  // body, including via a function it calls) and by the "Refresh"
  // button's onClick (which does manage its own loading/error state,
  // fine there since it's an event handler, not an effect).
  const fetchSuggestions = useCallback(() => {
    const params = new URLSearchParams();
    if (branchId) params.set("branch_id", branchId);
    if (leadTimeDays) params.set("lead_time_days", leadTimeDays);
    if (windowDays) params.set("velocity_window_days", windowDays);
    return api.get<{ suggestions: ReorderSuggestion[] }>(`/api/v1/ai/reorder-suggestions?${params.toString()}`);
  }, [branchId, leadTimeDays, windowDays]);

  function loadReorderSuggestions() {
    setLoadingSuggestions(true);
    fetchSuggestions()
      .then((data) => {
        setSuggestions(data.suggestions);
        setError(null);
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load reorder suggestions"))
      .finally(() => setLoadingSuggestions(false));
  }

  useEffect(() => {
    api.get<{ branches: Branch[] }>("/api/v1/branches").then((d) => setBranches(d.branches)).catch(() => {});
    api.get<{ products: Product[] }>("/api/v1/products?limit=200").then((d) => setProducts(d.products)).catch(() => {});
    fetchSuggestions()
      .then((data) => setSuggestions(data.suggestions))
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load reorder suggestions"));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function getRecommendations() {
    if (!variantId) return;
    setLoadingRecs(true);
    try {
      const data = await api.get<{ recommendations: Recommendation[]; source_order_count: number }>(
        `/api/v1/ai/recommendations/${variantId}?limit=10`,
      );
      setRecommendations(data.recommendations);
      setSourceOrderCount(data.source_order_count);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not load recommendations");
    } finally {
      setLoadingRecs(false);
    }
  }

  const variantOptions = products.flatMap((p) => p.variants.map((v) => ({ id: v.variant_id, label: `${p.name} (${v.sku})` })));

  return (
    <div className="flex flex-col gap-6 max-w-5xl">
      <h1 className="text-2xl font-semibold">AI Insights</h1>
      <p className="text-sm text-zinc-500">
        Both sections below compute real numbers from this merchant&apos;s own sales history — no trained model,
        just SQL over what&apos;s already been sold.
      </p>
      {error && <p className="text-sm text-red-600">{error}</p>}

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Reorder suggestions</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <p className="text-xs text-zinc-500">
            Lead time isn&apos;t stored anywhere yet (no product↔supplier link exists in Purchase Management) — enter
            what you know about your supplier&apos;s typical delivery time.
          </p>
          <div className="flex items-end gap-4 flex-wrap">
            <div className="flex flex-col gap-2 w-48">
              <Label>Branch</Label>
              <Select value={branchId} onValueChange={(v) => setBranchId(v ?? "")} items={Object.fromEntries(branches.map((b) => [b.branch_id, b.name]))}>
                <SelectTrigger><SelectValue placeholder="All branches" /></SelectTrigger>
                <SelectContent>
                  {branches.map((b) => (
                    <SelectItem key={b.branch_id} value={b.branch_id}>{b.name}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-2 w-36">
              <Label htmlFor="leadTime">Lead time (days)</Label>
              <Input id="leadTime" type="number" value={leadTimeDays} onChange={(e) => setLeadTimeDays(e.target.value)} />
            </div>
            <div className="flex flex-col gap-2 w-44">
              <Label htmlFor="windowDays">Velocity window (days)</Label>
              <Input id="windowDays" type="number" value={windowDays} onChange={(e) => setWindowDays(e.target.value)} />
            </div>
            <Button onClick={loadReorderSuggestions} disabled={loadingSuggestions}>
              {loadingSuggestions ? "Loading..." : "Refresh"}
            </Button>
          </div>

          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Product</TableHead>
                <TableHead>Available</TableHead>
                <TableHead>Reorder pt.</TableHead>
                <TableHead>Daily velocity</TableHead>
                <TableHead>Days of stock left</TableHead>
                <TableHead>Suggested qty</TableHead>
                <TableHead>Reason</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {suggestions.map((s) => (
                <TableRow key={`${s.branch_id}-${s.variant_id}`}>
                  <TableCell>{s.product_name} <span className="text-xs text-zinc-500">({s.sku})</span></TableCell>
                  <TableCell>{s.available}</TableCell>
                  <TableCell>{s.reorder_point}</TableCell>
                  <TableCell>{s.daily_velocity}</TableCell>
                  <TableCell>{s.days_of_stock_remaining ?? "—"}</TableCell>
                  <TableCell className="font-medium">{s.suggested_reorder_qty}</TableCell>
                  <TableCell><Badge variant="destructive">{s.reason}</Badge></TableCell>
                </TableRow>
              ))}
              {suggestions.length === 0 && !loadingSuggestions && (
                <TableRow>
                  <TableCell colSpan={7} className="text-center text-sm text-zinc-500 py-6">
                    Nothing is at risk of stocking out right now
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Frequently bought together</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <div className="flex items-end gap-4 flex-wrap">
            <div className="flex flex-col gap-2 w-64">
              <Label>Product</Label>
              <Select value={variantId} onValueChange={(v) => setVariantId(v ?? "")} items={Object.fromEntries(variantOptions.map((o) => [o.id, o.label]))}>
                <SelectTrigger><SelectValue placeholder="Select a product" /></SelectTrigger>
                <SelectContent>
                  {variantOptions.map((o) => (
                    <SelectItem key={o.id} value={o.id}>{o.label}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <Button onClick={getRecommendations} disabled={!variantId || loadingRecs}>
              {loadingRecs ? "Loading..." : "Get recommendations"}
            </Button>
          </div>

          {sourceOrderCount !== null && (
            <p className="text-xs text-zinc-500">Based on {sourceOrderCount} finalized order(s) containing this product.</p>
          )}

          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Product</TableHead>
                <TableHead>Price</TableHead>
                <TableHead>Bought together</TableHead>
                <TableHead>Confidence</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {recommendations.map((r) => (
                <TableRow key={r.variant_id}>
                  <TableCell>{r.product_name} <span className="text-xs text-zinc-500">({r.sku})</span></TableCell>
                  <TableCell>₹{r.selling_price}</TableCell>
                  <TableCell>{r.co_occurrence_count}x</TableCell>
                  <TableCell>{(Number(r.confidence) * 100).toFixed(0)}%</TableCell>
                </TableRow>
              ))}
              {recommendations.length === 0 && sourceOrderCount !== null && (
                <TableRow>
                  <TableCell colSpan={4} className="text-center text-sm text-zinc-500 py-6">
                    No other product has ever been bought alongside this one yet
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
