"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";

export default function NewSupplierPage() {
  const router = useRouter();
  const [form, setForm] = useState({
    name: "",
    legal_name: "",
    gstin: "",
    contact_name: "",
    email: "",
    phone: "",
    payment_terms: "",
    credit_limit: "",
  });
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  function set<K extends keyof typeof form>(key: K, value: string) {
    setForm((f) => ({ ...f, [key]: value }));
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      await api.post("/api/v1/suppliers", {
        ...form,
        credit_limit: form.credit_limit ? Number(form.credit_limit) : 0,
      });
      toast.success("Supplier created");
      router.push("/suppliers");
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not reach the server");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="max-w-lg">
      <Card>
        <CardHeader>
          <CardTitle>New supplier</CardTitle>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleSubmit} className="flex flex-col gap-4">
            <div className="flex flex-col gap-2">
              <Label htmlFor="name">Name *</Label>
              <Input id="name" required value={form.name} onChange={(e) => set("name", e.target.value)} />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="legal_name">Legal name</Label>
              <Input id="legal_name" value={form.legal_name} onChange={(e) => set("legal_name", e.target.value)} />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="gstin">GSTIN</Label>
              <Input id="gstin" value={form.gstin} onChange={(e) => set("gstin", e.target.value)} />
            </div>
            <div className="grid grid-cols-2 gap-4">
              <div className="flex flex-col gap-2">
                <Label htmlFor="contact_name">Contact name</Label>
                <Input id="contact_name" value={form.contact_name} onChange={(e) => set("contact_name", e.target.value)} />
              </div>
              <div className="flex flex-col gap-2">
                <Label htmlFor="phone">Phone</Label>
                <Input id="phone" value={form.phone} onChange={(e) => set("phone", e.target.value)} />
              </div>
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="email">Email</Label>
              <Input id="email" type="email" value={form.email} onChange={(e) => set("email", e.target.value)} />
            </div>
            <div className="grid grid-cols-2 gap-4">
              <div className="flex flex-col gap-2">
                <Label htmlFor="payment_terms">Payment terms</Label>
                <Input id="payment_terms" placeholder="Net 30" value={form.payment_terms} onChange={(e) => set("payment_terms", e.target.value)} />
              </div>
              <div className="flex flex-col gap-2">
                <Label htmlFor="credit_limit">Credit limit (₹)</Label>
                <Input id="credit_limit" type="number" min="0" step="0.01" value={form.credit_limit} onChange={(e) => set("credit_limit", e.target.value)} />
              </div>
            </div>
            {error && <p className="text-sm text-red-600">{error}</p>}
            <Button type="submit" disabled={submitting}>
              {submitting ? "Saving..." : "Create supplier"}
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  );
}
