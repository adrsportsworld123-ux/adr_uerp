"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Customer } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

export default function NewCustomerPage() {
  const router = useRouter();
  const [name, setName] = useState("");
  const [phone, setPhone] = useState("");
  const [email, setEmail] = useState("");
  const [customerType, setCustomerType] = useState<"b2c" | "b2b">("b2c");
  const [companyName, setCompanyName] = useState("");
  const [gstin, setGstin] = useState("");
  const [address, setAddress] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      const customer = await api.post<Customer>("/api/v1/customers", {
        name,
        phone,
        email,
        customer_type: customerType,
        company_name: companyName,
        gstin,
        address,
      });
      toast.success("Customer registered");
      router.push(`/customers/${customer.customer_id}`);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not create customer");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="flex flex-col gap-6 max-w-xl">
      <h1 className="text-2xl font-semibold">New customer</h1>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Profile</CardTitle>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleSubmit} className="flex flex-col gap-4">
            <div className="flex flex-col gap-2">
              <Label>Customer type</Label>
              <Select
                value={customerType}
                onValueChange={(v) => setCustomerType((v as "b2c" | "b2b") ?? "b2c")}
                items={{ b2c: "B2C", b2b: "B2B" }}
              >
                <SelectTrigger className="w-40">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="b2c">B2C</SelectItem>
                  <SelectItem value="b2b">B2B</SelectItem>
                </SelectContent>
              </Select>
            </div>

            <div className="grid grid-cols-2 gap-4">
              <div className="flex flex-col gap-2">
                <Label htmlFor="name">Name *</Label>
                <Input id="name" required value={name} onChange={(e) => setName(e.target.value)} />
              </div>
              <div className="flex flex-col gap-2">
                <Label htmlFor="phone">Phone *</Label>
                <Input id="phone" required value={phone} onChange={(e) => setPhone(e.target.value)} />
              </div>
            </div>

            <div className="flex flex-col gap-2">
              <Label htmlFor="email">Email *</Label>
              <Input id="email" type="email" required value={email} onChange={(e) => setEmail(e.target.value)} />
            </div>

            <div className="flex flex-col gap-2">
              <Label htmlFor="address">Address</Label>
              <Input id="address" value={address} onChange={(e) => setAddress(e.target.value)} />
            </div>

            {customerType === "b2b" && (
              <>
                <div className="flex flex-col gap-2">
                  <Label htmlFor="companyName">Company name</Label>
                  <Input id="companyName" value={companyName} onChange={(e) => setCompanyName(e.target.value)} />
                </div>
                <div className="flex flex-col gap-2">
                  <Label htmlFor="gstin">GSTIN *</Label>
                  <Input id="gstin" required value={gstin} onChange={(e) => setGstin(e.target.value)} />
                  <p className="text-xs text-zinc-500">Mandatory for B2B registration.</p>
                </div>
              </>
            )}

            {error && <p className="text-sm text-red-600">{error}</p>}
            <Button type="submit" disabled={submitting}>
              {submitting ? "Creating..." : "Register customer"}
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  );
}
