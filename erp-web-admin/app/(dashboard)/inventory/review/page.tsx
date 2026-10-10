"use client";

// Stock Review (Phase 13, erp-core-go internal/inventory/stock_review.go):
// sales a till made offline that sold more than the branch had in stock.
// A sale that already happened is never rejected at sync, so stock went
// negative; each such line waits here until someone checks the shelf, fixes
// the stock with a normal adjustment or count if needed, and resolves it
// with a note of what they found.
import { Suspense, useState } from "react";
import Link from "next/link";
import { CheckCircle2 } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import { Button } from "@/components/ui/button";
import { Modal } from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Column, DataTable } from "@/components/data/data-table";
import { PageHeader } from "@/components/data/page-header";
import { StatusChip } from "@/components/data/status-chip";
import { useListState } from "@/components/data/use-list-state";
import { useServerList } from "@/components/data/use-server-list";

type ReviewItem = {
  item_id: string;
  status: "open" | "resolved";
  branch_name: string;
  sku: string;
  product_name: string;
  order_number: string;
  terminal_name: string | null;
  quantity: string;
  on_hand_after: string;
  on_hand_now: string;
  created_at: string;
  resolution: string | null;
  resolved_by: string | null;
  resolved_at: string | null;
};

const qty = (s: string) => String(Number(s));
const when = (iso: string) =>
  new Date(iso).toLocaleString("en-IN", { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });

const columns: Column<ReviewItem>[] = [
  { key: "when", header: "Synced", sort: "created", cell: (r) => <span className="text-xs">{when(r.created_at)}</span> },
  {
    key: "product",
    header: "Product",
    cell: (r) => (
      <div>
        <div className="font-medium">{r.product_name}</div>
        <div className="font-mono text-xs text-muted-foreground">{r.sku}</div>
      </div>
    ),
  },
  {
    key: "sale",
    header: "Sale",
    cell: (r) => (
      <div className="text-xs">
        <div>{r.order_number}</div>
        <div className="text-muted-foreground">
          {r.branch_name}
          {r.terminal_name ? ` · ${r.terminal_name}` : ""}
        </div>
      </div>
    ),
  },
  { key: "sold", header: "Sold", cell: (r) => qty(r.quantity) },
  {
    key: "stock",
    header: "Stock after / now",
    cell: (r) => (
      <span className={Number(r.on_hand_now) < 0 ? "text-destructive" : ""}>
        {qty(r.on_hand_after)} / {qty(r.on_hand_now)}
      </span>
    ),
  },
  {
    key: "status",
    header: "Status",
    cell: (r) =>
      r.status === "open" ? (
        <StatusChip status="pending" label="Needs review" />
      ) : (
        <div className="text-xs">
          <StatusChip status="active" label="Resolved" />
          <div className="mt-1 max-w-56 text-muted-foreground" title={r.resolution ?? ""}>
            {r.resolution} · {r.resolved_by}
          </div>
        </div>
      ),
  },
];

function StockReviewView() {
  const { state, update, apiQuery, activeFilterCount } = useListState({ sort: "created", dir: "desc", status: "open" });
  const list = useServerList<ReviewItem>("/api/v1/inventory/review", "items", apiQuery);
  const { can } = usePermissions();
  const allowed = can("inventory.adjust");
  const [resolving, setResolving] = useState<ReviewItem | null>(null);
  const [note, setNote] = useState("");
  const [saving, setSaving] = useState(false);

  async function resolve() {
    if (!resolving) return;
    setSaving(true);
    try {
      await api.post(`/api/v1/inventory/review/${resolving.item_id}/resolve`, { resolution: note.trim() });
      toast.success(`Resolved ${resolving.product_name}`);
      setResolving(null);
      setNote("");
      list.reload();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not resolve");
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Stock Review"
        description="Offline sales that sold more than the branch had in stock. The sale stands; check the shelf, fix the stock if needed, then resolve with what you found."
      />
      <DataTable<ReviewItem>
        columns={columns}
        rows={list.rows}
        total={list.total}
        loading={list.loading}
        error={list.error}
        onRetry={list.reload}
        state={state}
        update={(p) => {
          list.setLoading(true);
          update(p);
        }}
        rowKey={(r) => r.item_id}
        searchPlaceholder="Product, SKU or order number"
        searchLabel="Search review items"
        filters={
          <select
            aria-label="Status"
            value={state.status || "all"}
            onChange={(e) => update({ status: e.target.value })}
            className="h-9 rounded-lg border bg-card px-2 text-sm"
          >
            <option value="open">Needs review</option>
            <option value="resolved">Resolved</option>
            <option value="all">All</option>
          </select>
        }
        activeFilterCount={activeFilterCount}
        onClearFilters={() => update({ q: "", status: "open" })}
        empty={{ title: "Nothing to review", body: "Every offline sale so far had the stock it needed." }}
        rowActions={(r) =>
          r.status === "open" ? (
            <Button
              variant="outline"
              size="sm"
              disabled={!allowed}
              title={allowed ? undefined : missingPermissionHint("inventory.adjust")}
              aria-label={`Resolve ${r.product_name} on ${r.order_number}`}
              onClick={() => setResolving(r)}
            >
              <CheckCircle2 /> Resolve
            </Button>
          ) : null
        }
      />

      {resolving && (
        <Modal
          open
          onOpenChange={(o) => !o && setResolving(null)}
          title={`Resolve ${resolving.product_name}`}
          description={`${resolving.order_number} sold ${qty(resolving.quantity)}; stock is ${qty(resolving.on_hand_now)} now.`}
          footer={
            <>
              <Button variant="outline" onClick={() => setResolving(null)}>
                Cancel
              </Button>
              <Button onClick={resolve} disabled={saving || note.trim() === ""}>
                Mark resolved
              </Button>
            </>
          }
        >
          <div className="flex flex-col gap-2">
            <Label htmlFor="resolution">What did you find?</Label>
            <Textarea
              id="resolution"
              value={note}
              onChange={(e) => setNote(e.target.value)}
              placeholder="e.g. Counted 4 on the shelf, adjusted +6"
            />
            <p className="text-xs text-muted-foreground">
              Resolving doesn&apos;t change stock. Fix the quantity first with an{" "}
              <Link href="/inventory" className="underline">
                adjustment
              </Link>{" "}
              or an{" "}
              <Link href="/inventory-reconciliation" className="underline">
                inventory count
              </Link>
              .
            </p>
          </div>
        </Modal>
      )}
    </div>
  );
}

export default function StockReviewPage() {
  return (
    <Suspense>
      <StockReviewView />
    </Suspense>
  );
}
