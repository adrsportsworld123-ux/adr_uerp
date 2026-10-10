"use client";

// Phase 7: Wholesale/B2B & Omnichannel (erp-core-go's phased_roadmap.md;
// internal/pricing/price_lists.go). A price list is a named set of
// per-variant override prices; a customer assigned to one (see the
// Customers screen's price list field) gets that price automatically at
// checkout and on any quotation — see internal/pricing.ResolvePrice.
import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { PriceListSummary, PriceListDetail } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { AlertModal } from "@/components/ui/dialog";
import { PageHeader } from "@/components/data/page-header";
import { ProductPicker, VariantOption } from "@/components/data/product-picker";

export default function PriceListsPage() {
  const [lists, setLists] = useState<PriceListSummary[]>([]);
  const [newName, setNewName] = useState("");
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const [selectedId, setSelectedId] = useState("");
  const [detail, setDetail] = useState<PriceListDetail | null>(null);
  const [variant, setVariant] = useState<VariantOption | null>(null);
  const variantId = variant?.variantId ?? "";
  const [deleting, setDeleting] = useState<PriceListSummary | null>(null);
  const [price, setPrice] = useState("");
  const [savingItem, setSavingItem] = useState(false);

  const fetchLists = useCallback(() => api.get<{ price_lists: PriceListSummary[] }>("/api/v1/pricing/price-lists"), []);

  useEffect(() => {
    fetchLists().then((d) => setLists(d.price_lists)).catch((e) => setError(e instanceof ApiError ? e.message : "Could not load price lists"));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  function loadLists() {
    fetchLists().then((d) => setLists(d.price_lists)).catch((e) => toast.error(e instanceof ApiError ? e.message : "Could not load price lists"));
  }

  async function loadDetail(id: string) {
    setSelectedId(id);
    try {
      const d = await api.get<PriceListDetail>(`/api/v1/pricing/price-lists/${id}`);
      setDetail(d);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not load price list");
    }
  }

  async function createList() {
    if (!newName.trim()) return;
    setCreating(true);
    try {
      await api.post("/api/v1/pricing/price-lists", { name: newName.trim() });
      setNewName("");
      loadLists();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not create price list");
    } finally {
      setCreating(false);
    }
  }

  async function saveItem() {
    if (!selectedId || !variantId || !price) return;
    setSavingItem(true);
    try {
      await api.put(`/api/v1/pricing/price-lists/${selectedId}/items/${variantId}`, { price: Number(price) });
      setVariant(null);
      setPrice("");
      await loadDetail(selectedId);
      loadLists();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not set item price");
    } finally {
      setSavingItem(false);
    }
  }

  async function removeItem(vId: string) {
    try {
      await api.delete(`/api/v1/pricing/price-lists/${selectedId}/items/${vId}`);
      await loadDetail(selectedId);
      loadLists();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not remove item");
    }
  }

  async function deleteList(id: string) {
    setDeleting(null);
    try {
      await api.delete(`/api/v1/pricing/price-lists/${id}`);
      toast.success("Price list deleted");
      if (id === selectedId) {
        setSelectedId("");
        setDetail(null);
      }
      loadLists();
    } catch (e) {
      // 409 PRICE_LIST_IN_USE is the expected, common refusal here — a
      // customer is still assigned, reassign them first.
      toast.error(e instanceof ApiError ? e.message : "Could not delete price list");
    }
  }

  return (
    <div className="flex flex-col gap-6 max-w-5xl">
      <PageHeader
        title="Wholesale Price Lists"
        description="Assign a customer to a price list (on their Customers page) and their orders and quotations use these prices instead of retail."
      />
      {error && <p className="text-sm text-destructive">{error}</p>}

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Price lists</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <div className="flex items-end gap-2">
            <div className="flex flex-col gap-2 w-64">
              <Label htmlFor="newName">New price list name</Label>
              <Input id="newName" value={newName} onChange={(e) => setNewName(e.target.value)} placeholder="e.g. Wholesale Tier 1" />
            </div>
            <Button onClick={createList} disabled={creating || !newName.trim()}>
              {creating ? "Creating..." : "Create"}
            </Button>
          </div>

          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Items</TableHead>
                <TableHead></TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {lists.map((l) => (
                <TableRow key={l.price_list_id} className={l.price_list_id === selectedId ? "bg-primary-soft" : ""}>
                  <TableCell>{l.name}</TableCell>
                  <TableCell>{l.item_count}</TableCell>
                  <TableCell className="flex gap-2">
                    <Button variant="outline" onClick={() => loadDetail(l.price_list_id)}>
                      View / edit
                    </Button>
                    <Button variant="destructive" onClick={() => setDeleting(l)}>
                      Delete
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
              {lists.length === 0 && (
                <TableRow>
                  <TableCell colSpan={3} className="text-center text-sm text-muted-foreground py-6">
                    No price lists yet
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      {detail && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">{detail.name}</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            <div className="flex items-end gap-2 flex-wrap">
              <div className="flex flex-col gap-2 w-64">
                <Label>Product</Label>
                <ProductPicker value={variant} onChange={setVariant} />
              </div>
              <div className="flex flex-col gap-2 w-36">
                <Label htmlFor="itemPrice">Price</Label>
                <Input id="itemPrice" type="number" value={price} onChange={(e) => setPrice(e.target.value)} />
              </div>
              <Button onClick={saveItem} disabled={savingItem || !variantId || !price}>
                {savingItem ? "Saving..." : "Set price"}
              </Button>
            </div>

            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Product</TableHead>
                  <TableHead>Price</TableHead>
                  <TableHead></TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {detail.items.map((it) => (
                  <TableRow key={it.variant_id}>
                    <TableCell>{it.product_name} <span className="text-xs text-muted-foreground">({it.sku})</span></TableCell>
                    <TableCell>₹{it.price}</TableCell>
                    <TableCell>
                      <Button variant="outline" onClick={() => removeItem(it.variant_id)}>Remove</Button>
                    </TableCell>
                  </TableRow>
                ))}
                {detail.items.length === 0 && (
                  <TableRow>
                    <TableCell colSpan={3} className="text-center text-sm text-muted-foreground py-6">
                      No items set yet — every product still sells at plain retail price for customers on this list
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      )}
      <AlertModal
        open={!!deleting}
        onOpenChange={(o) => !o && setDeleting(null)}
        title={`Delete price list “${deleting?.name ?? ""}”?`}
        actions={
          <>
            <Button variant="outline" onClick={() => setDeleting(null)}>
              Cancel
            </Button>
            <Button variant="destructive" onClick={() => deleting && deleteList(deleting.price_list_id)}>
              Delete price list
            </Button>
          </>
        }
      >
        Its {deleting?.item_count ?? 0} item price{deleting?.item_count === 1 ? "" : "s"} will be removed. Past orders and quotations keep
        the prices they were billed at. A list that customers are still assigned to can&rsquo;t be deleted.
      </AlertModal>
    </div>
  );
}
