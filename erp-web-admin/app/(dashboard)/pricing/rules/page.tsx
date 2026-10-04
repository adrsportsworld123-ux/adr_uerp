"use client";

// Phase 10 (erp-core-go's phased_roadmap.md; internal/pricing/rules.go):
// dynamic pricing — happy hours, weekend specials, seasonal prices. A rule
// changes a POS line's unit price itself (not a discount line like a
// Promotion), up or down, never above MRP. Precedence at the till: a
// customer's wholesale price-list price, then the best live rule (a
// product rule beats a category rule, then priority, then newest), then
// the base price. Hours and days are the BRANCH's local time. Quotations
// never use rules — a quote valid for weeks mustn't lock in a 3-hour
// price.
import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Branch, Category, EffectivePrice, PriceRule, Product } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

const DAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

function describeRule(r: PriceRule) {
  const what = r.rule_type === "fixed_price" ? `₹${r.value}` : `${Number(r.value) > 0 ? "+" : ""}${Number(r.value)}%`;
  const when: string[] = [];
  if (r.days_of_week?.length) when.push(r.days_of_week.map((d) => DAYS[d]).join(", "));
  if (r.time_start) when.push(`${r.time_start}–${r.time_end}`);
  if (r.starts_at || r.ends_at)
    when.push(`${r.starts_at ? new Date(r.starts_at).toLocaleDateString() : "…"} to ${r.ends_at ? new Date(r.ends_at).toLocaleDateString() : "…"}`);
  return { what, when: when.length ? when.join(" · ") : "Always" };
}

export default function PriceRulesPage() {
  const [rules, setRules] = useState<PriceRule[]>([]);
  const [products, setProducts] = useState<Product[]>([]);
  const [categories, setCategories] = useState<Category[]>([]);
  const [branches, setBranches] = useState<Branch[]>([]);

  const [name, setName] = useState("");
  const [scope, setScope] = useState<"variant" | "category">("category");
  const [variantId, setVariantId] = useState("");
  const [categoryId, setCategoryId] = useState("");
  const [branchId, setBranchId] = useState("");
  const [ruleType, setRuleType] = useState<"percent" | "fixed_price">("percent");
  const [value, setValue] = useState("");
  const [priority, setPriority] = useState("0");
  const [days, setDays] = useState<number[]>([]);
  const [timeStart, setTimeStart] = useState("");
  const [timeEnd, setTimeEnd] = useState("");
  const [startsAt, setStartsAt] = useState("");
  const [endsAt, setEndsAt] = useState("");
  const [override, setOverride] = useState(false);
  const [saving, setSaving] = useState(false);

  const [checkVariant, setCheckVariant] = useState("");
  const [checkBranch, setCheckBranch] = useState("");
  const [checkAt, setCheckAt] = useState("");
  const [checkResult, setCheckResult] = useState<EffectivePrice | null>(null);

  const fetchRules = useCallback(() => api.get<{ rules: PriceRule[] }>("/api/v1/pricing/rules"), []);

  useEffect(() => {
    fetchRules().then((d) => setRules(d.rules)).catch((e) => toast.error(e instanceof ApiError ? e.message : "Could not load price rules"));
    api.get<{ products: Product[] }>("/api/v1/products?limit=200").then((d) => setProducts(d.products)).catch(() => {});
    api.get<{ categories: Category[] }>("/api/v1/categories").then((d) => setCategories(d.categories)).catch(() => {});
    api.get<{ branches: Branch[] }>("/api/v1/branches").then((d) => setBranches(d.branches)).catch(() => {});
  }, [fetchRules]);

  function refresh() {
    fetchRules().then((d) => setRules(d.rules)).catch((e) => toast.error(e instanceof ApiError ? e.message : "Could not load price rules"));
  }

  const variantOptions = products.flatMap((p) => p.variants.map((v) => ({ id: v.variant_id, label: `${p.name} (${v.sku}) — ₹${v.selling_price}` })));
  const branchName = (id: string | null) => (id ? branches.find((b) => b.branch_id === id)?.name ?? "one branch" : "All branches");

  function toggleDay(d: number) {
    setDays(days.includes(d) ? days.filter((x) => x !== d) : [...days, d].sort());
  }

  async function createRule() {
    if (!name.trim() || !value || (scope === "variant" ? !variantId : !categoryId)) {
      toast.error("Name, a product or category, and a value are required");
      return;
    }
    setSaving(true);
    try {
      await api.post("/api/v1/pricing/rules", {
        name: name.trim(),
        ...(scope === "variant" ? { variant_id: variantId } : { category_id: categoryId }),
        ...(branchId ? { branch_id: branchId } : {}),
        rule_type: ruleType,
        value: Number(value),
        priority: Number(priority) || 0,
        ...(days.length ? { days_of_week: days } : {}),
        ...(timeStart && timeEnd ? { time_start: timeStart, time_end: timeEnd } : {}),
        ...(startsAt ? { starts_at: new Date(startsAt).toISOString() } : {}),
        ...(endsAt ? { ends_at: new Date(endsAt).toISOString() } : {}),
        override,
      });
      toast.success("Price rule created");
      setName("");
      setValue("");
      setDays([]);
      setTimeStart("");
      setTimeEnd("");
      setStartsAt("");
      setEndsAt("");
      setOverride(false);
      refresh();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not create price rule");
    } finally {
      setSaving(false);
    }
  }

  async function setActive(id: string, active: boolean) {
    try {
      await api.patch(`/api/v1/pricing/rules/${id}`, { active });
      refresh();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not update rule");
    }
  }

  async function remove(id: string) {
    try {
      await api.delete(`/api/v1/pricing/rules/${id}`);
      toast.success("Price rule deleted");
      refresh();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not delete rule");
    }
  }

  async function checkPrice() {
    if (!checkVariant) return;
    const params = new URLSearchParams({ branch_id: checkBranch });
    if (checkAt) params.set("at", new Date(checkAt).toISOString());
    try {
      setCheckResult(await api.get<EffectivePrice>(`/api/v1/pricing/variants/${checkVariant}/effective-price?${params.toString()}`));
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not check price");
    }
  }

  return (
    <div className="flex flex-col gap-6 max-w-5xl">
      <h1 className="text-2xl font-semibold">Price Rules</h1>
      <p className="text-sm text-zinc-500">
        Happy hours, weekend specials and seasonal prices. A rule sets the item&apos;s price at the till (up or down, never above
        MRP). A customer&apos;s wholesale price list always wins over a rule. Days and hours are each branch&apos;s local time.
        Quotations don&apos;t use rules.
      </p>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">New rule</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
            <div className="flex flex-col gap-2">
              <Label htmlFor="ruleName">Name</Label>
              <Input id="ruleName" value={name} onChange={(e) => setName(e.target.value)} placeholder="Weekend happy hour" />
            </div>
            <div className="flex flex-col gap-2">
              <Label>Applies to</Label>
              <Select
                value={scope}
                onValueChange={(v) => {
                  const next = (v as "variant" | "category") ?? "category";
                  setScope(next);
                  if (next === "category") setRuleType("percent");
                }}
                items={{ category: "A whole category", variant: "One product variant" }}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="category">A whole category</SelectItem>
                  <SelectItem value="variant">One product variant</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-2">
              <Label>{scope === "variant" ? "Product variant" : "Category"}</Label>
              {scope === "variant" ? (
                <Select value={variantId} onValueChange={(v) => setVariantId(v ?? "")} items={Object.fromEntries(variantOptions.map((o) => [o.id, o.label]))}>
                  <SelectTrigger>
                    <SelectValue placeholder="Choose a variant" />
                  </SelectTrigger>
                  <SelectContent>
                    {variantOptions.map((o) => (
                      <SelectItem key={o.id} value={o.id}>
                        {o.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              ) : (
                <Select value={categoryId} onValueChange={(v) => setCategoryId(v ?? "")} items={Object.fromEntries(categories.map((c) => [c.category_id, c.path || c.name]))}>
                  <SelectTrigger>
                    <SelectValue placeholder="Choose a category" />
                  </SelectTrigger>
                  <SelectContent>
                    {categories.map((c) => (
                      <SelectItem key={c.category_id} value={c.category_id}>
                        {c.path || c.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
            </div>
          </div>

          <div className="grid grid-cols-2 sm:grid-cols-4 gap-4">
            <div className="flex flex-col gap-2">
              <Label>Price change</Label>
              <Select
                value={ruleType}
                onValueChange={(v) => setRuleType((v as "percent" | "fixed_price") ?? "percent")}
                items={{ percent: "Percent of price", fixed_price: "Fixed price" }}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="percent">Percent of price</SelectItem>
                  {scope === "variant" && <SelectItem value="fixed_price">Fixed price</SelectItem>}
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="ruleValue">{ruleType === "percent" ? "Percent (−20 = 20% off)" : "Price (₹)"}</Label>
              <Input id="ruleValue" type="number" step="0.01" value={value} onChange={(e) => setValue(e.target.value)} />
            </div>
            <div className="flex flex-col gap-2">
              <Label>Branch</Label>
              <Select value={branchId} onValueChange={(v) => setBranchId(v ?? "")} items={{ "": "All branches", ...Object.fromEntries(branches.map((b) => [b.branch_id, b.name])) }}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="">All branches</SelectItem>
                  {branches.map((b) => (
                    <SelectItem key={b.branch_id} value={b.branch_id}>
                      {b.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="rulePriority">Priority</Label>
              <Input id="rulePriority" type="number" value={priority} onChange={(e) => setPriority(e.target.value)} />
            </div>
          </div>

          <div className="flex flex-col gap-2">
            <Label>Days (none = every day)</Label>
            <div className="flex gap-2 flex-wrap">
              {DAYS.map((d, i) => (
                <Button key={d} type="button" size="sm" variant={days.includes(i) ? "default" : "outline"} onClick={() => toggleDay(i)}>
                  {d}
                </Button>
              ))}
            </div>
          </div>

          <div className="grid grid-cols-2 sm:grid-cols-4 gap-4">
            <div className="flex flex-col gap-2">
              <Label htmlFor="timeStart">From (time)</Label>
              <Input id="timeStart" type="time" value={timeStart} onChange={(e) => setTimeStart(e.target.value)} />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="timeEnd">Until (time)</Label>
              <Input id="timeEnd" type="time" value={timeEnd} onChange={(e) => setTimeEnd(e.target.value)} />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="startsAt">Season starts</Label>
              <Input id="startsAt" type="datetime-local" value={startsAt} onChange={(e) => setStartsAt(e.target.value)} />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="endsAt">Season ends</Label>
              <Input id="endsAt" type="datetime-local" value={endsAt} onChange={(e) => setEndsAt(e.target.value)} />
            </div>
          </div>
          <p className="text-xs text-zinc-500">A time window may cross midnight (e.g. 22:00 until 02:00). Leave times and season empty for an always-on rule.</p>

          <div className="flex items-center gap-4">
            <label className="flex items-center gap-2 text-sm">
              <input type="checkbox" checked={override} onChange={(e) => setOverride(e.target.checked)} />
              Allow below cost
            </label>
            <Button onClick={createRule} disabled={saving}>
              {saving ? "Creating..." : "Create rule"}
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Rules</CardTitle>
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Rule</TableHead>
                <TableHead>Applies to</TableHead>
                <TableHead>Price</TableHead>
                <TableHead>When</TableHead>
                <TableHead>Status</TableHead>
                <TableHead></TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rules.map((r) => {
                const d = describeRule(r);
                return (
                  <TableRow key={r.price_rule_id}>
                    <TableCell>
                      {r.name}
                      {r.priority !== 0 && <span className="text-xs text-zinc-500"> · priority {r.priority}</span>}
                    </TableCell>
                    <TableCell className="text-sm">
                      {r.variant_id ? `${r.product_name} (${r.sku})` : `Category: ${r.category_name}`}
                      <div className="text-xs text-zinc-500">{branchName(r.branch_id)}</div>
                    </TableCell>
                    <TableCell>{d.what}</TableCell>
                    <TableCell className="text-sm">{d.when}</TableCell>
                    <TableCell>
                      <Badge variant={r.active ? "default" : "outline"}>{r.active ? "active" : "inactive"}</Badge>
                    </TableCell>
                    <TableCell className="flex gap-2">
                      <Button size="sm" variant="outline" onClick={() => setActive(r.price_rule_id, !r.active)}>
                        {r.active ? "Deactivate" : "Activate"}
                      </Button>
                      <Button size="sm" variant="ghost" onClick={() => remove(r.price_rule_id)}>
                        Delete
                      </Button>
                    </TableCell>
                  </TableRow>
                );
              })}
              {rules.length === 0 && (
                <TableRow>
                  <TableCell colSpan={6} className="text-center text-sm text-zinc-500 py-6">
                    No price rules yet
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Price check</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <p className="text-xs text-zinc-500">What would the till charge for an item at a branch and time? Leave the time empty for right now.</p>
          <div className="grid grid-cols-1 sm:grid-cols-4 gap-4 items-end">
            <div className="flex flex-col gap-2 sm:col-span-2">
              <Label>Product variant</Label>
              <Select value={checkVariant} onValueChange={(v) => setCheckVariant(v ?? "")} items={Object.fromEntries(variantOptions.map((o) => [o.id, o.label]))}>
                <SelectTrigger>
                  <SelectValue placeholder="Choose a variant" />
                </SelectTrigger>
                <SelectContent>
                  {variantOptions.map((o) => (
                    <SelectItem key={o.id} value={o.id}>
                      {o.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-2">
              <Label>Branch</Label>
              <Select value={checkBranch} onValueChange={(v) => setCheckBranch(v ?? "")} items={Object.fromEntries(branches.map((b) => [b.branch_id, b.name]))}>
                <SelectTrigger>
                  <SelectValue placeholder="Choose a branch" />
                </SelectTrigger>
                <SelectContent>
                  {branches.map((b) => (
                    <SelectItem key={b.branch_id} value={b.branch_id}>
                      {b.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="checkAt">At</Label>
              <Input id="checkAt" type="datetime-local" value={checkAt} onChange={(e) => setCheckAt(e.target.value)} />
            </div>
          </div>
          <Button variant="outline" className="w-fit" onClick={checkPrice} disabled={!checkVariant || !checkBranch}>
            Check price
          </Button>
          {checkResult && (
            <p className="text-sm" data-testid="price-check-result">
              <span className="font-semibold">₹{checkResult.unit_price}</span>{" "}
              {checkResult.source === "price_rule"
                ? `— rule "${checkResult.price_rule_name}" (base ₹${checkResult.base_selling_price})`
                : checkResult.source === "price_list"
                  ? "— customer price list"
                  : "— base price, no rule applies"}
            </p>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
