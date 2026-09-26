"use client";

// Phase 8: Vertical Expansion (erp-core-go's phased_roadmap.md;
// internal/catalog/attributes.go). attributes/attribute_values have
// existed in the schema since Phase 1 specifically for this — "validates
// the configurable masters, not industry-specific code promise" — but had
// no management API or UI until now. This screen is the actual
// vertical-onboarding tool: define an attribute (e.g. "Purity", a select
// with values 18K/22K/24K), then assign it to whichever category needs it
// (Jewelry) as required or optional, with an optional display unit
// ("grams", "GB", ...). New Product then renders the right fields
// automatically once a category with a declared attribute set is picked.
import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Category, CatalogAttribute, CategoryAttribute } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

const INPUT_TYPE_LABEL: Record<string, string> = { select: "Select (controlled list)", text: "Free text", number: "Number" };

export default function AttributesPage() {
  const [attributes, setAttributes] = useState<CatalogAttribute[]>([]);
  const [newAttrName, setNewAttrName] = useState("");
  const [newAttrType, setNewAttrType] = useState<"select" | "text" | "number">("select");
  const [creatingAttr, setCreatingAttr] = useState(false);
  const [newValueByAttr, setNewValueByAttr] = useState<Record<string, string>>({});

  const [categories, setCategories] = useState<Category[]>([]);
  const [categoryId, setCategoryId] = useState("");
  const [categorySet, setCategorySet] = useState<CategoryAttribute[]>([]);
  const [assignAttrId, setAssignAttrId] = useState("");
  const [assignRequired, setAssignRequired] = useState(false);
  const [assignUnit, setAssignUnit] = useState("");

  const fetchAttributes = useCallback(() => api.get<{ attributes: CatalogAttribute[] }>("/api/v1/attributes"), []);

  useEffect(() => {
    fetchAttributes().then((d) => setAttributes(d.attributes)).catch(() => {});
    api.get<{ categories: Category[] }>("/api/v1/categories").then((d) => setCategories(d.categories)).catch(() => {});
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  function reloadAttributes() {
    fetchAttributes().then((d) => setAttributes(d.attributes)).catch((e) => toast.error(e instanceof ApiError ? e.message : "Could not load attributes"));
  }

  async function createAttribute() {
    if (!newAttrName.trim()) return;
    setCreatingAttr(true);
    try {
      await api.post("/api/v1/attributes", { name: newAttrName.trim(), input_type: newAttrType });
      setNewAttrName("");
      reloadAttributes();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not create attribute");
    } finally {
      setCreatingAttr(false);
    }
  }

  async function addValue(attributeId: string) {
    const value = (newValueByAttr[attributeId] ?? "").trim();
    if (!value) return;
    try {
      await api.post(`/api/v1/attributes/${attributeId}/values`, { value });
      setNewValueByAttr((prev) => ({ ...prev, [attributeId]: "" }));
      reloadAttributes();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not add value");
    }
  }

  async function removeValue(attributeId: string, valueId: string) {
    try {
      await api.delete(`/api/v1/attributes/${attributeId}/values/${valueId}`);
      reloadAttributes();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not remove value");
    }
  }

  function loadCategorySet(id: string) {
    setCategoryId(id);
    api
      .get<{ attributes: CategoryAttribute[] }>(`/api/v1/categories/${id}/attributes`)
      .then((d) => setCategorySet(d.attributes))
      .catch((e) => toast.error(e instanceof ApiError ? e.message : "Could not load category attribute set"));
  }

  async function assignToCategory() {
    if (!categoryId || !assignAttrId) return;
    try {
      await api.post(`/api/v1/categories/${categoryId}/attributes`, { attribute_id: assignAttrId, required: assignRequired, unit: assignUnit });
      setAssignAttrId("");
      setAssignRequired(false);
      setAssignUnit("");
      loadCategorySet(categoryId);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not assign attribute to category");
    }
  }

  async function removeFromCategory(attributeId: string) {
    try {
      await api.delete(`/api/v1/categories/${categoryId}/attributes/${attributeId}`);
      loadCategorySet(categoryId);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not remove attribute from category");
    }
  }

  const unassignedAttributes = attributes.filter((a) => !categorySet.some((ca) => ca.attribute_id === a.attribute_id));

  return (
    <div className="flex flex-col gap-6 max-w-5xl">
      <h1 className="text-2xl font-semibold">Product Attributes</h1>
      <p className="text-sm text-zinc-500">
        Define an attribute once (e.g. &quot;Purity&quot;, a select with 18K/22K/24K), then assign it to whichever
        category needs it — required or optional, with an optional display unit. New Product renders the right
        fields automatically once a category with a declared set is picked. The same mechanism works for any
        vertical: jewelry&apos;s Purity/Weight, electronics&apos; RAM/Storage, sports&apos; Material, etc.
      </p>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Attributes</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <div className="flex items-end gap-2">
            <div className="flex flex-col gap-2 w-56">
              <Label htmlFor="newAttrName">New attribute name</Label>
              <Input id="newAttrName" value={newAttrName} onChange={(e) => setNewAttrName(e.target.value)} placeholder="e.g. Purity" />
            </div>
            <div className="flex flex-col gap-2 w-56">
              <Label>Input type</Label>
              <Select value={newAttrType} onValueChange={(v) => setNewAttrType((v as typeof newAttrType) ?? "select")} items={INPUT_TYPE_LABEL}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  {Object.entries(INPUT_TYPE_LABEL).map(([value, label]) => (
                    <SelectItem key={value} value={value}>{label}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <Button onClick={createAttribute} disabled={creatingAttr || !newAttrName.trim()}>
              {creatingAttr ? "Creating..." : "Create"}
            </Button>
          </div>

          <div className="flex flex-col gap-3">
            {attributes.map((a) => (
              <div key={a.attribute_id} className="border rounded p-3 flex flex-col gap-2">
                <div className="flex items-center gap-2">
                  <span className="font-medium">{a.name}</span>
                  <Badge variant="outline">{INPUT_TYPE_LABEL[a.input_type]}</Badge>
                </div>
                {a.input_type === "select" && (
                  <div className="flex flex-col gap-2">
                    <div className="flex flex-wrap gap-2">
                      {a.values.map((v) => (
                        <Badge key={v.value_id} variant="secondary" className="gap-1">
                          {v.value}
                          <button
                            type="button"
                            className="ml-1 text-zinc-500 hover:text-red-600"
                            onClick={() => removeValue(a.attribute_id, v.value_id)}
                            aria-label={`Remove ${v.value}`}
                          >
                            ×
                          </button>
                        </Badge>
                      ))}
                      {a.values.length === 0 && <span className="text-xs text-zinc-500">No values yet</span>}
                    </div>
                    <div className="flex items-center gap-2">
                      <Input
                        className="w-40"
                        placeholder="New value"
                        value={newValueByAttr[a.attribute_id] ?? ""}
                        onChange={(e) => setNewValueByAttr((prev) => ({ ...prev, [a.attribute_id]: e.target.value }))}
                      />
                      <Button size="sm" variant="outline" onClick={() => addValue(a.attribute_id)}>
                        + Add value
                      </Button>
                    </div>
                  </div>
                )}
              </div>
            ))}
            {attributes.length === 0 && <p className="text-sm text-zinc-500">No attributes defined yet</p>}
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Category attribute sets</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <div className="flex flex-col gap-2 w-64">
            <Label>Category</Label>
            <Select value={categoryId} onValueChange={(v) => loadCategorySet(v ?? "")} items={Object.fromEntries(categories.map((c) => [c.category_id, c.name]))}>
              <SelectTrigger><SelectValue placeholder="Select a category" /></SelectTrigger>
              <SelectContent>
                {categories.map((c) => (
                  <SelectItem key={c.category_id} value={c.category_id}>{c.name}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          {categoryId && (
            <>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Attribute</TableHead>
                    <TableHead>Type</TableHead>
                    <TableHead>Required</TableHead>
                    <TableHead>Unit</TableHead>
                    <TableHead></TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {categorySet.map((ca) => (
                    <TableRow key={ca.attribute_id}>
                      <TableCell>{ca.name}</TableCell>
                      <TableCell>{INPUT_TYPE_LABEL[ca.input_type]}</TableCell>
                      <TableCell>{ca.required ? <Badge>Required</Badge> : <Badge variant="outline">Optional</Badge>}</TableCell>
                      <TableCell>{ca.unit || "—"}</TableCell>
                      <TableCell>
                        <Button size="sm" variant="destructive" onClick={() => removeFromCategory(ca.attribute_id)}>Remove</Button>
                      </TableCell>
                    </TableRow>
                  ))}
                  {categorySet.length === 0 && (
                    <TableRow>
                      <TableCell colSpan={5} className="text-center text-sm text-zinc-500 py-6">
                        No attributes assigned to this category yet — every product in it is unrestricted
                      </TableCell>
                    </TableRow>
                  )}
                </TableBody>
              </Table>

              <div className="flex items-end gap-2 flex-wrap border-t pt-4">
                <div className="flex flex-col gap-2 w-56">
                  <Label>Add attribute</Label>
                  <Select value={assignAttrId} onValueChange={(v) => setAssignAttrId(v ?? "")} items={Object.fromEntries(unassignedAttributes.map((a) => [a.attribute_id, a.name]))}>
                    <SelectTrigger><SelectValue placeholder="Select an attribute" /></SelectTrigger>
                    <SelectContent>
                      {unassignedAttributes.map((a) => (
                        <SelectItem key={a.attribute_id} value={a.attribute_id}>{a.name}</SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <div className="flex flex-col gap-2 w-36">
                  <Label htmlFor="unit">Unit (optional)</Label>
                  <Input id="unit" value={assignUnit} onChange={(e) => setAssignUnit(e.target.value)} placeholder="e.g. grams" />
                </div>
                <label className="flex items-center gap-2 text-sm pb-2">
                  <input type="checkbox" checked={assignRequired} onChange={(e) => setAssignRequired(e.target.checked)} />
                  Required
                </label>
                <Button onClick={assignToCategory} disabled={!assignAttrId}>
                  Add to category
                </Button>
              </div>
            </>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
