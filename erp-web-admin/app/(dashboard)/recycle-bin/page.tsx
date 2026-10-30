"use client";

// Recycle Bin (erp-core-go internal/softdelete, migrations/050): deleting
// a master or a draft keeps the record, hidden; here an admin sees what was
// deleted, by whom and when, and brings it back. Gated by recycle_bin.manage.
import { useCallback, useEffect, useState } from "react";
import { Loader2, RotateCcw } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { RecycleBinItem } from "@/lib/types";
import { dateTime } from "@/lib/format";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import { PageHeader } from "@/components/data/page-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

const ENTITY_LABEL: Record<string, string> = {
  category: "Category",
  brand: "Brand",
  tax_slab: "Tax slab",
  attribute_value: "Attribute value",
  price_list: "Price list",
  price_rule: "Price rule",
  markup_rule: "Markup rule",
  suggestion_rule: "Price suggestion rule",
  scheduled_change: "Scheduled price change",
  promotion: "Promotion",
  coupon: "Coupon",
  role: "Role",
  report_template: "Report template",
  quotation: "Quotation (draft)",
  journal_voucher: "Journal voucher (draft)",
  purchase_order: "Purchase order",
  grn: "Goods receipt (draft GRN)",
};

export default function RecycleBinPage() {
  const { can } = usePermissions();
  const allowed = can("recycle_bin.manage");
  const [entity, setEntity] = useState("");
  const [items, setItems] = useState<RecycleBinItem[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [restoring, setRestoring] = useState<string | null>(null);

  const load = useCallback(() => {
    api
      .get<{ items: RecycleBinItem[] }>(`/api/v1/recycle-bin${entity ? `?entity=${entity}` : ""}`)
      .then((d) => {
        setItems(d.items);
        setError(null);
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load the recycle bin"));
  }, [entity]);
  useEffect(load, [load]);

  async function restore(it: RecycleBinItem) {
    setRestoring(it.id);
    try {
      await api.post(`/api/v1/recycle-bin/${it.entity}/${it.id}/restore`);
      toast.success(`Restored ${ENTITY_LABEL[it.entity]?.toLowerCase() ?? it.entity} “${it.label}”`);
      load();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not restore");
    } finally {
      setRestoring(null);
    }
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="Recycle Bin"
        description="Deleted records are kept, hidden from every screen. Restore one to bring it back exactly as it was. Lines removed from carts and quotations are kept for the record but restore only with their document."
      />
      <Card>
        <CardContent className="flex items-end gap-4 pt-6">
          <div className="flex flex-col gap-2">
            <Label htmlFor="bin-entity">Show</Label>
            <select id="bin-entity" className="h-9 rounded-md border bg-background px-2 text-sm" value={entity} onChange={(e) => setEntity(e.target.value)}>
              <option value="">Everything</option>
              {Object.entries(ENTITY_LABEL).map(([v, l]) => (
                <option key={v} value={v}>
                  {l}
                </option>
              ))}
            </select>
          </div>
        </CardContent>
      </Card>
      {error && <p className="text-sm text-destructive">{error}</p>}
      <Card>
        <CardContent className="pt-6">
          {items == null && !error ? (
            <p className="flex items-center gap-2 text-sm text-muted-foreground">
              <Loader2 className="size-4 animate-spin" aria-hidden /> Loading…
            </p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Record</TableHead>
                  <TableHead>Type</TableHead>
                  <TableHead>Deleted</TableHead>
                  <TableHead>By</TableHead>
                  <TableHead className="text-right">Action</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(items ?? []).map((it) => (
                  <TableRow key={`${it.entity}-${it.id}`}>
                    <TableCell className="font-medium">{it.label || "—"}</TableCell>
                    <TableCell>{ENTITY_LABEL[it.entity] ?? it.entity}</TableCell>
                    <TableCell className="text-sm">{dateTime(it.deleted_at)}</TableCell>
                    <TableCell className="text-sm">{it.deleted_by_name ?? "—"}</TableCell>
                    <TableCell className="text-right">
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => restore(it)}
                        disabled={!allowed || restoring === it.id}
                        title={allowed ? undefined : missingPermissionHint("recycle_bin.manage")}
                        aria-label={`Restore ${it.label}`}
                      >
                        {restoring === it.id ? <Loader2 className="animate-spin" aria-hidden /> : <RotateCcw aria-hidden />}
                        Restore
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
                {items?.length === 0 && (
                  <TableRow>
                    <TableCell colSpan={5} className="py-6 text-center text-sm text-muted-foreground">
                      Nothing has been deleted
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
