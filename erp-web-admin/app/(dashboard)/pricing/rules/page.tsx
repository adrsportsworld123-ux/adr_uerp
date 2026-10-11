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
//
// CRUD: create and edit in a dialog, activate/deactivate, delete. Once a
// rule has priced a sale its terms explain that sale, so it can only be
// deactivated (and a new rule created) — the API enforces the same.
import { useCallback, useEffect, useMemo, useState } from "react";
import { Loader2, Pencil, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Branch, Category, EffectivePrice, PriceRule } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { AlertModal, Modal } from "@/components/ui/dialog";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { PageHeader } from "@/components/data/page-header";
import { StatusChip } from "@/components/data/status-chip";
import { ProductPicker, VariantOption } from "@/components/data/product-picker";
import { SearchSelect, categoryOption } from "@/components/data/search-select";
import { DaysOfWeekPicker, describeWhen, fromLocalInput, toLocalInput } from "@/components/pricing/schedule-fields";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

const errorText = (e: unknown, fallback: string) => (e instanceof ApiError || e instanceof Error ? e.message : fallback);

function priceText(r: PriceRule) {
  return r.rule_type === "fixed_price" ? `₹${r.value}` : `${Number(r.value) > 0 ? "+" : ""}${Number(r.value)}%`;
}

export default function PriceRulesPage() {
  const [rules, setRules] = useState<PriceRule[]>([]);
  const [loading, setLoading] = useState(true);
  const [categories, setCategories] = useState<Category[]>([]);
  const [branches, setBranches] = useState<Branch[]>([]);
  const [status, setStatus] = useState("");
  const [query, setQuery] = useState("");
  const [editing, setEditing] = useState<PriceRule | "new" | null>(null);
  const [deleting, setDeleting] = useState<PriceRule | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);

  const [checkVariant, setCheckVariant] = useState<VariantOption | null>(null);
  const [checkBranch, setCheckBranch] = useState("");
  const [checkAt, setCheckAt] = useState("");
  const [checkResult, setCheckResult] = useState<EffectivePrice | null>(null);

  const refresh = useCallback(() => {
    api
      .get<{ rules: PriceRule[] }>("/api/v1/pricing/rules")
      .then((d) => setRules(d.rules))
      .catch((e) => toast.error(errorText(e, "Could not load price rules")))
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    refresh();
    api.get<{ categories: Category[] }>("/api/v1/categories?status=active&sort=path").then((d) => setCategories(d.categories)).catch(() => {});
    api.get<{ branches: Branch[] }>("/api/v1/branches").then((d) => setBranches(d.branches)).catch(() => {});
  }, [refresh]);

  const branchName = (id: string | null) => (id ? (branches.find((b) => b.branch_id === id)?.name ?? "one branch") : "All branches");
  const branchOptions = branches.map((b) => ({ value: b.branch_id, label: b.name }));

  const shown = useMemo(() => {
    const q = query.trim().toLowerCase();
    return rules.filter(
      (r) =>
        (!status || (status === "active") === r.active) &&
        (!q || `${r.name} ${r.product_name ?? ""} ${r.sku ?? ""} ${r.category_name ?? ""}`.toLowerCase().includes(q)),
    );
  }, [rules, status, query]);

  async function setActive(r: PriceRule, active: boolean) {
    setBusyId(r.price_rule_id);
    try {
      await api.patch(`/api/v1/pricing/rules/${r.price_rule_id}`, { active });
      toast.success(active ? "Price rule activated" : "Price rule deactivated");
      refresh();
    } catch (e) {
      toast.error(errorText(e, "Could not update rule"));
    } finally {
      setBusyId(null);
    }
  }

  async function remove(r: PriceRule) {
    setDeleting(null);
    try {
      await api.delete(`/api/v1/pricing/rules/${r.price_rule_id}`);
      toast.success("Price rule deleted");
      refresh();
    } catch (e) {
      toast.error(errorText(e, "Could not delete rule"));
    }
  }

  async function checkPrice() {
    if (!checkVariant) return;
    const params = new URLSearchParams({ branch_id: checkBranch });
    if (checkAt) params.set("at", new Date(checkAt).toISOString());
    try {
      setCheckResult(await api.get<EffectivePrice>(`/api/v1/pricing/variants/${checkVariant.variantId}/effective-price?${params.toString()}`));
    } catch (e) {
      toast.error(errorText(e, "Could not check price"));
    }
  }

  return (
    <div className="flex max-w-6xl flex-col gap-6">
      <PageHeader
        title="Price Rules"
        description="Happy hours, weekend specials and seasonal prices. A rule sets the item's price at the till (up or down, never above MRP). A customer's wholesale price list always wins over a rule. Days and hours are each branch's local time. Quotations don't use rules."
        actions={
          <Button onClick={() => setEditing("new")}>
            <Plus /> New rule
          </Button>
        }
      />

      <Card>
        <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3">
          <CardTitle className="text-base">
            Rules <span className="font-normal text-muted-foreground">({rules.length})</span>
          </CardTitle>
          <div className="flex flex-wrap items-center gap-2">
            <input
              type="search"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Search rules"
              aria-label="Search rules"
              className="h-9 w-56 rounded-lg border bg-card px-3 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/30"
            />
            <select aria-label="Rule status" value={status} onChange={(e) => setStatus(e.target.value)} className="h-9 rounded-lg border bg-card px-2 text-sm">
              <option value="">All statuses</option>
              <option value="active">Active</option>
              <option value="inactive">Inactive</option>
            </select>
          </div>
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Rule</TableHead>
                <TableHead>Applies to</TableHead>
                <TableHead>Price</TableHead>
                <TableHead>When</TableHead>
                <TableHead className="text-right">Used</TableHead>
                <TableHead>Status</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {shown.map((r) => {
                const used = r.times_used > 0;
                return (
                  <TableRow key={r.price_rule_id}>
                    <TableCell>
                      {r.name}
                      {r.priority !== 0 && <span className="text-xs text-muted-foreground"> · priority {r.priority}</span>}
                    </TableCell>
                    <TableCell className="text-sm">
                      {r.variant_id ? `${r.product_name} (${r.sku})` : `Category: ${r.category_name}`}
                      <div className="text-xs text-muted-foreground">{branchName(r.branch_id)}</div>
                    </TableCell>
                    <TableCell>{priceText(r)}</TableCell>
                    <TableCell className="text-sm">{describeWhen(r)}</TableCell>
                    <TableCell className="tabular text-right">{r.times_used}</TableCell>
                    <TableCell>
                      <StatusChip status={r.active ? "active" : "inactive"} />
                    </TableCell>
                    <TableCell>
                      <div className="flex justify-end gap-1.5">
                        <Button
                          size="sm"
                          variant="outline"
                          disabled={used}
                          title={used ? "This rule has priced sales, so its terms are kept. Deactivate it and create a new rule." : undefined}
                          onClick={() => setEditing(r)}
                        >
                          <Pencil /> Edit
                        </Button>
                        <Button size="sm" variant="outline" disabled={busyId === r.price_rule_id} onClick={() => setActive(r, !r.active)}>
                          {r.active ? "Deactivate" : "Activate"}
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          aria-label="Delete"
                          disabled={used}
                          title={used ? "This rule has priced sales — deactivate it instead." : undefined}
                          className="text-destructive hover:text-destructive"
                          onClick={() => setDeleting(r)}
                        >
                          <Trash2 /> Delete
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                );
              })}
              {!loading && shown.length === 0 && (
                <TableRow>
                  <TableCell colSpan={7} className="py-6 text-center text-sm text-muted-foreground">
                    {rules.length ? "No rule matches these filters" : "No price rules yet"}
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
          <p className="text-xs text-muted-foreground">What would the till charge for an item at a branch and time? Leave the time empty for right now.</p>
          <div className="grid grid-cols-1 items-end gap-4 sm:grid-cols-4">
            <div className="flex flex-col gap-2 sm:col-span-2">
              <Label htmlFor="checkVariant">Product variant</Label>
              <ProductPicker id="checkVariant" ariaLabel="Product variant to check" value={checkVariant} onChange={setCheckVariant} placeholder="Choose a variant" />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="checkBranch">Branch</Label>
              <SearchSelect id="checkBranch" ariaLabel="Branch" value={checkBranch} onChange={setCheckBranch} options={branchOptions} placeholder="Choose a branch" />
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

      {editing !== null && (
        <RuleDialog
          key={editing === "new" ? "new" : editing.price_rule_id}
          rule={editing === "new" ? null : editing}
          categories={categories}
          branchOptions={branchOptions}
          onClose={() => setEditing(null)}
          onSaved={refresh}
        />
      )}
      <AlertModal
        open={!!deleting}
        onOpenChange={(o) => !o && setDeleting(null)}
        title={`Delete price rule “${deleting?.name ?? ""}”?`}
        actions={
          <>
            <Button variant="outline" onClick={() => setDeleting(null)}>
              Cancel
            </Button>
            <Button variant="destructive" onClick={() => deleting && remove(deleting)}>
              Delete rule
            </Button>
          </>
        }
      >
        It hasn&rsquo;t priced any sale yet, so it can be removed completely. This can&rsquo;t be undone.
      </AlertModal>
    </div>
  );
}

function RuleDialog({
  rule,
  categories,
  branchOptions,
  onClose,
  onSaved,
}: {
  rule: PriceRule | null;
  categories: Category[];
  branchOptions: { value: string; label: string }[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const r = rule;
  const [name, setName] = useState(r?.name ?? "");
  const [scope, setScope] = useState<"variant" | "category">(r?.variant_id ? "variant" : "category");
  const [variant, setVariant] = useState<VariantOption | null>(
    r?.variant_id
      ? { variantId: r.variant_id, productId: "", productName: r.product_name ?? "", sku: r.sku ?? "", sellingPrice: "", label: `${r.product_name} (${r.sku})`, trackBatch: false }
      : null,
  );
  const [categoryId, setCategoryId] = useState(r?.category_id ?? "");
  const [branchId, setBranchId] = useState(r?.branch_id ?? "");
  const [ruleType, setRuleType] = useState<"percent" | "fixed_price">(r?.rule_type ?? "percent");
  const [value, setValue] = useState(r ? String(Number(r.value)) : "");
  const [priority, setPriority] = useState(String(r?.priority ?? 0));
  const [days, setDays] = useState<number[]>(r?.days_of_week ?? []);
  const [timeStart, setTimeStart] = useState(r?.time_start ?? "");
  const [timeEnd, setTimeEnd] = useState(r?.time_end ?? "");
  const [startsAt, setStartsAt] = useState(toLocalInput(r?.starts_at));
  const [endsAt, setEndsAt] = useState(toLocalInput(r?.ends_at));
  const [override, setOverride] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const categoryOptions = useMemo(() => {
    const opts = categories.map(categoryOption);
    if (r?.category_id && !opts.some((o) => o.value === r.category_id)) opts.unshift({ value: r.category_id, label: `${r.category_name ?? "Category"} (inactive)` });
    return opts;
  }, [categories, r]);

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    if (!name.trim() || !value || (scope === "variant" ? !variant : !categoryId)) {
      setError("Name, a product or category, and a value are required.");
      return;
    }
    if ((timeStart === "") !== (timeEnd === "")) {
      setError("Give both a start and an end time, or neither.");
      return;
    }
    setSaving(true);
    const body = {
      name: name.trim(),
      ...(scope === "variant" ? { variant_id: variant!.variantId } : { category_id: categoryId }),
      ...(branchId ? { branch_id: branchId } : {}),
      rule_type: ruleType,
      value: Number(value),
      priority: Number(priority) || 0,
      ...(days.length ? { days_of_week: days } : {}),
      ...(timeStart && timeEnd ? { time_start: timeStart, time_end: timeEnd } : {}),
      ...(startsAt ? { starts_at: fromLocalInput(startsAt) } : {}),
      ...(endsAt ? { ends_at: fromLocalInput(endsAt) } : {}),
      override,
    };
    try {
      if (r) await api.put(`/api/v1/pricing/rules/${r.price_rule_id}`, body);
      else await api.post("/api/v1/pricing/rules", body);
      toast.success(r ? "Price rule updated" : "Price rule created");
      onSaved();
      onClose();
    } catch (err) {
      setError(errorText(err, r ? "Could not update price rule" : "Could not create price rule"));
    } finally {
      setSaving(false);
    }
  }

  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={r ? `Edit “${r.name}”` : "New price rule"}
      width={720}
      footer={
        <>
          <Button variant="outline" onClick={onClose} disabled={saving}>
            Cancel
          </Button>
          <Button type="submit" form="rule-form" disabled={saving}>
            {saving && <Loader2 className="animate-spin" aria-hidden />}
            {r ? "Save changes" : "Create rule"}
          </Button>
        </>
      }
    >
      <form id="rule-form" onSubmit={save} className="flex flex-col gap-4">
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
          <div className="flex flex-col gap-2">
            <Label htmlFor="ruleName">Name *</Label>
            <Input id="ruleName" autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="Weekend happy hour" />
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
              <SelectTrigger aria-label="Applies to">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="category">A whole category</SelectItem>
                <SelectItem value="variant">One product variant</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="ruleTarget">{scope === "variant" ? "Product variant *" : "Category *"}</Label>
            {scope === "variant" ? (
              <ProductPicker id="ruleTarget" ariaLabel="Product variant" value={variant} onChange={setVariant} placeholder="Choose a variant" />
            ) : (
              <SearchSelect id="ruleTarget" ariaLabel="Category" value={categoryId} onChange={setCategoryId} options={categoryOptions} placeholder="Choose a category" />
            )}
          </div>
        </div>

        <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
          <div className="flex flex-col gap-2">
            <Label>Price change</Label>
            <Select value={ruleType} onValueChange={(v) => setRuleType((v as "percent" | "fixed_price") ?? "percent")} items={{ percent: "Percent of price", fixed_price: "Fixed price" }}>
              <SelectTrigger aria-label="Price change">
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
            <Label htmlFor="ruleBranch">Branch</Label>
            <SearchSelect id="ruleBranch" ariaLabel="Branch" value={branchId} onChange={setBranchId} options={branchOptions} noneLabel="All branches" placeholder="All branches" />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="rulePriority">Priority</Label>
            <Input id="rulePriority" type="number" value={priority} onChange={(e) => setPriority(e.target.value)} />
          </div>
        </div>

        <DaysOfWeekPicker value={days} onChange={setDays} />

        <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
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
        <p className="text-xs text-muted-foreground">A time window may cross midnight (e.g. 22:00 until 02:00). Leave times and season empty for an always-on rule.</p>

        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={override} onChange={(e) => setOverride(e.target.checked)} />
          Allow below cost
        </label>
        {error && (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        )}
      </form>
    </Modal>
  );
}
