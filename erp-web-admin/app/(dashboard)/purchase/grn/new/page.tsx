"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { api, ApiError, SEED_BRANCH_ID } from "@/lib/api-client";
import { Supplier, GRN } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

export default function NewGRNPage() {
  const router = useRouter();
  const [suppliers, setSuppliers] = useState<Supplier[]>([]);
  const [supplierId, setSupplierId] = useState("");
  const [freight, setFreight] = useState("0");
  const [otherCharges, setOtherCharges] = useState("0");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api.get<{ suppliers: Supplier[] }>("/api/v1/suppliers").then((d) => setSuppliers(d.suppliers));
  }, []);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      const grn = await api.post<GRN>("/api/v1/purchase/grn", {
        supplier_id: supplierId,
        branch_id: SEED_BRANCH_ID,
        freight_amount: Number(freight) || 0,
        other_charges: Number(otherCharges) || 0,
      });
      router.push(`/purchase/grn/${grn.grn_id}`);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not reach the server");
      setSubmitting(false);
    }
  }

  return (
    <div className="max-w-lg">
      <Card>
        <CardHeader>
          <CardTitle>New Goods Receipt Note</CardTitle>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleSubmit} className="flex flex-col gap-4">
            <div className="flex flex-col gap-2">
              <Label>Supplier *</Label>
              <Select
                value={supplierId}
                onValueChange={(value) => setSupplierId(value ?? "")}
                items={Object.fromEntries(suppliers.map((s) => [s.supplier_id, s.name]))}
                required
              >
                <SelectTrigger>
                  <SelectValue placeholder="Select a supplier" />
                </SelectTrigger>
                <SelectContent>
                  {suppliers.map((s) => (
                    <SelectItem key={s.supplier_id} value={s.supplier_id}>
                      {s.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              {suppliers.length === 0 && (
                <p className="text-xs text-zinc-500">
                  No suppliers yet —{" "}
                  <a href="/suppliers/new" className="underline">
                    create one first
                  </a>
                  .
                </p>
              )}
            </div>
            <div className="grid grid-cols-2 gap-4">
              <div className="flex flex-col gap-2">
                <Label htmlFor="freight">Freight (₹)</Label>
                <Input id="freight" type="number" min="0" step="0.01" value={freight} onChange={(e) => setFreight(e.target.value)} />
              </div>
              <div className="flex flex-col gap-2">
                <Label htmlFor="other">Other charges (₹)</Label>
                <Input id="other" type="number" min="0" step="0.01" value={otherCharges} onChange={(e) => setOtherCharges(e.target.value)} />
              </div>
            </div>
            <p className="text-xs text-zinc-500">Add line items on the next screen, then complete the GRN to update stock and cost.</p>
            {error && <p className="text-sm text-red-600">{error}</p>}
            <Button type="submit" disabled={submitting || !supplierId}>
              {submitting ? "Creating..." : "Create draft GRN"}
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  );
}
