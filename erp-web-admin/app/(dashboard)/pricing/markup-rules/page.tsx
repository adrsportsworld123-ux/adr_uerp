"use client";

// Markup rules (erp-core-go migrations/071, internal/pricing/markup.go): a
// price worked out from the cost price — cost + a percentage or an amount —
// for one product, a category (with its sub-categories) or every product,
// optionally at one branch and on chosen dates, days and hours. Never above
// MRP; a product with no cost has no markup price.
//
// How the till uses it is the branch's "Customer price" (Sales Policy):
// as today the markup price stands in for the selling price; Customer
// benefit / Shop benefit compare MRP, selling, markup and price-rule prices
// and take the lowest / highest. Same CRUD rules as Price Rules: a rule
// that has priced a sale can only be switched off.
import { useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { Loader2, Pencil, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Branch, Category, MarkupRule } from "@/lib/types";
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
import { rupees } from "@/lib/format";
import { currencySymbol } from "@/lib/currency";

const errorText = (e: unknown, fallback: string) => (e instanceof ApiError || e instanceof Error ? e.message : fallback);

function markupText(r: MarkupRule) {
  return r.markup_type === "percent" ? `Cost + ${Number(r.value)}%` : `Cost + ${rupees(r.value)}`;
}

function scopeText(r: MarkupRule) {
  if (r.variant_id) return `${r.product_name} (${r.sku})`;
  if (r.category_id) return `Category: ${r.category_name} (and its sub-categories)`;
  return "Every product";
}

export default function MarkupRulesPage() {
  const [rules, setRules] = useState<MarkupRule[]>([]);
  const [loading, setLoading] = useState(true);
  const [categories, setCategories] = useState<Category[]>([]);
  const [branches, setBranches] = useState<Branch[]>([]);
  const [status, setStatus] = useState("");
  const [query, setQuery] = useState("");
  const [editing, setEditing] = useState<MarkupRule | "new" | null>(null);
  const [deleting, setDeleting] = useState<MarkupRule | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);

  const refresh = useCallback(() => {
    api
      .get<{ rules: MarkupRule[] }>("/api/v1/pricing/markup-rules")
      .then((d) => setRules(d.rules))
      .catch((e) => toast.error(errorText(e, "Could not load markup rules")))
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    refresh();
    api.get<{ categories: Category[] }>("/api/v1/categories?status=active&sort=path").then((d) => setCategories(d.categories)).catch(() => {});
    api.get<{ branches: Branch[] }>("/api/v1/branches").then((d) => setBranches(d.branches)).catch(() => {});
  }, [refresh]);

  const branchOptions = branches.map((b) => ({ value: b.branch_id, label: b.name }));

  const shown = useMemo(() => {
    const q = query.trim().toLowerCase();
    return rules.filter(
      (r) =>
        (!status || (status === "active") === r.active) &&
        (!q || `${r.name} ${r.product_name ?? ""} ${r.sku ?? ""} ${r.category_name ?? ""}`.toLowerCase().includes(q)),
    );
  }, [rules, status, query]);

  async function setActive(r: MarkupRule, active: boolean) {
    setBusyId(r.markup_rule_id);
    try {
      await api.patch(`/api/v1/pricing/markup-rules/${r.markup_rule_id}`, { active });
      toast.success(active ? "Markup rule switched on" : "Markup rule switched off");
      refresh();
    } catch (e) {
      toast.error(errorText(e, "Could not update the rule"));
    } finally {
      setBusyId(null);
    }
  }

  async function remove(r: MarkupRule) {
    setDeleting(null);
    try {
      await api.delete(`/api/v1/pricing/markup-rules/${r.markup_rule_id}`);
      toast.success("Markup rule deleted");
      refresh();
    } catch (e) {
      toast.error(errorText(e, "Could not delete the rule"));
    }
  }

  return (
    <div className="flex max-w-6xl flex-col gap-6">
      <PageHeader
        title="Markup Rules"
        description="A price from the cost: cost plus a percentage or an amount, never above MRP. Which price the till charges is each branch's Customer price in Sales Policy: as today the markup price acts as the selling price; Customer benefit or Shop benefit compare MRP, selling, markup and price-rule prices."
        actions={
          <Button onClick={() => setEditing("new")}>
            <Plus /> New markup rule
          </Button>
        }
      />
      <p className="-mt-3 text-sm text-muted-foreground">
        Choose the branch&apos;s price under{" "}
        <Link href="/sales-settings" className="text-primary hover:underline">
          Sales Policy › Customer price
        </Link>
        . Check what an item would cost under{" "}
        <Link href="/pricing/rules" className="text-primary hover:underline">
          Price Rules › Price check
        </Link>
        .
      </p>

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
              aria-label="Search markup rules"
              className="h-9 w-56 rounded-lg border bg-card px-3 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/30"
            />
            <select aria-label="Markup rule status" value={status} onChange={(e) => setStatus(e.target.value)} className="h-9 rounded-lg border bg-card px-2 text-sm">
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
                <TableHead>Markup</TableHead>
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
                  <TableRow key={r.markup_rule_id}>
                    <TableCell>
                      {r.name}
                      {r.priority !== 0 && <span className="text-xs text-muted-foreground"> · priority {r.priority}</span>}
                    </TableCell>
                    <TableCell className="text-sm">
                      {scopeText(r)}
                      <div className="text-xs text-muted-foreground">{r.branch_name ?? "All branches"}</div>
                    </TableCell>
                    <TableCell>{markupText(r)}</TableCell>
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
                          title={used ? "This rule has priced sales, so its terms are kept. Switch it off and create a new rule." : undefined}
                          onClick={() => setEditing(r)}
                        >
                          <Pencil /> Edit
                        </Button>
                        <Button size="sm" variant="outline" disabled={busyId === r.markup_rule_id} onClick={() => setActive(r, !r.active)}>
                          {r.active ? "Deactivate" : "Activate"}
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          aria-label="Delete"
                          disabled={used}
                          title={used ? "This rule has priced sales — switch it off instead." : undefined}
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
                    {rules.length ? "No rule matches these filters" : "No markup rules yet"}
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      {editing !== null && (
        <MarkupDialog
          key={editing === "new" ? "new" : editing.markup_rule_id}
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
        title={`Delete markup rule “${deleting?.name ?? ""}”?`}
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
        It hasn&rsquo;t priced any sale yet. It goes to the Recycle Bin, where it can be restored.
      </AlertModal>
    </div>
  );
}

function MarkupDialog({
  rule,
  categories,
  branchOptions,
  onClose,
  onSaved,
}: {
  rule: MarkupRule | null;
  categories: Category[];
  branchOptions: { value: string; label: string }[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const r = rule;
  const [name, setName] = useState(r?.name ?? "");
  const [scope, setScope] = useState<"all" | "category" | "variant">(r?.variant_id ? "variant" : r?.category_id ? "category" : r ? "all" : "category");
  const [variant, setVariant] = useState<VariantOption | null>(
    r?.variant_id
      ? { variantId: r.variant_id, productId: "", productName: r.product_name ?? "", sku: r.sku ?? "", sellingPrice: "", label: `${r.product_name} (${r.sku})`, trackBatch: false }
      : null,
  );
  const [categoryId, setCategoryId] = useState(r?.category_id ?? "");
  const [branchId, setBranchId] = useState(r?.branch_id ?? "");
  const [markupType, setMarkupType] = useState<"percent" | "fixed">(r?.markup_type ?? "percent");
  const [value, setValue] = useState(r ? String(Number(r.value)) : "");
  const [priority, setPriority] = useState(String(r?.priority ?? 0));
  const [days, setDays] = useState<number[]>(r?.days_of_week ?? []);
  const [timeStart, setTimeStart] = useState(r?.time_start ?? "");
  const [timeEnd, setTimeEnd] = useState(r?.time_end ?? "");
  const [startsAt, setStartsAt] = useState(toLocalInput(r?.starts_at));
  const [endsAt, setEndsAt] = useState(toLocalInput(r?.ends_at));
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const categoryOptions = useMemo(() => {
    const opts = categories.map(categoryOption);
    if (r?.category_id && !opts.some((o) => o.value === r.category_id)) opts.unshift({ value: r.category_id, label: `${r.category_name ?? "Category"} (inactive)` });
    return opts;
  }, [categories, r]);

  const example = Number(value) > 0 ? (markupType === "percent" ? 100 * (1 + Number(value) / 100) : 100 + Number(value)) : null;

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    if (!name.trim() || !(Number(value) > 0) || (scope === "variant" && !variant) || (scope === "category" && !categoryId)) {
      setError("A name, what it applies to, and a markup above 0 are required.");
      return;
    }
    if ((timeStart === "") !== (timeEnd === "")) {
      setError("Give both a start and an end time, or neither.");
      return;
    }
    setSaving(true);
    const body = {
      name: name.trim(),
      ...(scope === "variant" ? { variant_id: variant!.variantId } : scope === "category" ? { category_id: categoryId } : {}),
      ...(branchId ? { branch_id: branchId } : {}),
      markup_type: markupType,
      value: Number(value),
      priority: Number(priority) || 0,
      ...(days.length ? { days_of_week: days } : {}),
      ...(timeStart && timeEnd ? { time_start: timeStart, time_end: timeEnd } : {}),
      ...(startsAt ? { starts_at: fromLocalInput(startsAt) } : {}),
      ...(endsAt ? { ends_at: fromLocalInput(endsAt) } : {}),
    };
    try {
      if (r) await api.put(`/api/v1/pricing/markup-rules/${r.markup_rule_id}`, body);
      else await api.post("/api/v1/pricing/markup-rules", body);
      toast.success(r ? "Markup rule updated" : "Markup rule created");
      onSaved();
      onClose();
    } catch (err) {
      setError(errorText(err, r ? "Could not update the markup rule" : "Could not create the markup rule"));
    } finally {
      setSaving(false);
    }
  }

  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={r ? `Edit “${r.name}”` : "New markup rule"}
      width={720}
      footer={
        <>
          <Button variant="outline" onClick={onClose} disabled={saving}>
            Cancel
          </Button>
          <Button type="submit" form="markup-form" disabled={saving}>
            {saving && <Loader2 className="animate-spin" aria-hidden />}
            {r ? "Save changes" : "Create rule"}
          </Button>
        </>
      }
    >
      <form id="markup-form" onSubmit={save} className="flex flex-col gap-4">
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
          <div className="flex flex-col gap-2">
            <Label htmlFor="markupName">Name *</Label>
            <Input id="markupName" autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="Badminton 30% markup" />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="markupScope">Applies to</Label>
            <select id="markupScope" value={scope} onChange={(e) => setScope(e.target.value as typeof scope)} className="h-9 rounded-lg border bg-card px-2 text-sm">
              <option value="category">A category (with sub-categories)</option>
              <option value="variant">One product variant</option>
              <option value="all">Every product</option>
            </select>
          </div>
          {scope !== "all" && (
            <div className="flex flex-col gap-2">
              <Label htmlFor="markupTarget">{scope === "variant" ? "Product variant *" : "Category *"}</Label>
              {scope === "variant" ? (
                <ProductPicker id="markupTarget" ariaLabel="Product variant" value={variant} onChange={setVariant} placeholder="Choose a variant" />
              ) : (
                <SearchSelect id="markupTarget" ariaLabel="Category" value={categoryId} onChange={setCategoryId} options={categoryOptions} placeholder="Choose a category" />
              )}
            </div>
          )}
        </div>

        <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
          <div className="flex flex-col gap-2">
            <Label htmlFor="markupType">Markup</Label>
            <select id="markupType" value={markupType} onChange={(e) => setMarkupType(e.target.value as typeof markupType)} className="h-9 rounded-lg border bg-card px-2 text-sm">
              <option value="percent">Percent over cost</option>
              <option value="fixed">Amount over cost ({currencySymbol()})</option>
            </select>
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="markupValue">{markupType === "percent" ? "Percent *" : `Amount (${currencySymbol()}) *`}</Label>
            <Input id="markupValue" type="number" min="0.01" step="0.01" value={value} onChange={(e) => setValue(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="markupBranch">Branch</Label>
            <SearchSelect id="markupBranch" ariaLabel="Branch" value={branchId} onChange={setBranchId} options={branchOptions} noneLabel="All branches" placeholder="All branches" />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="markupPriority">Priority</Label>
            <Input id="markupPriority" type="number" value={priority} onChange={(e) => setPriority(e.target.value)} />
          </div>
        </div>
        {example != null && (
          <p className="text-xs text-muted-foreground" data-testid="markup-example">
            Cost {rupees(100)} → markup price {rupees(example)} (plus GST for a product whose prices include it; never above MRP).
          </p>
        )}

        <DaysOfWeekPicker value={days} onChange={setDays} />

        <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
          <div className="flex flex-col gap-2">
            <Label htmlFor="mTimeStart">From (time)</Label>
            <Input id="mTimeStart" type="time" value={timeStart} onChange={(e) => setTimeStart(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="mTimeEnd">Until (time)</Label>
            <Input id="mTimeEnd" type="time" value={timeEnd} onChange={(e) => setTimeEnd(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="mStartsAt">Starts</Label>
            <Input id="mStartsAt" type="datetime-local" value={startsAt} onChange={(e) => setStartsAt(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="mEndsAt">Ends</Label>
            <Input id="mEndsAt" type="datetime-local" value={endsAt} onChange={(e) => setEndsAt(e.target.value)} />
          </div>
        </div>
        <p className="text-xs text-muted-foreground">
          A product rule beats a category rule, which beats an every-product rule; then the higher priority, then the newest. Leave times and dates empty
          for an always-on rule.
        </p>
        {error && (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        )}
      </form>
    </Modal>
  );
}
