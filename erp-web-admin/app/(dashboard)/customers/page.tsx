"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { api } from "@/lib/api-client";
import { Customer } from "@/lib/types";
import { buttonVariants } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

const SEGMENT_VARIANT: Record<string, "default" | "secondary" | "outline" | "destructive"> = {
  vip: "default",
  regular: "secondary",
  new: "outline",
  dormant: "destructive",
};

export default function CustomersPage() {
  const [customers, setCustomers] = useState<Customer[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [q, setQ] = useState("");
  const [customerType, setCustomerType] = useState<string>("");
  const [segment, setSegment] = useState<string>("");

  function load() {
    const params = new URLSearchParams();
    if (q) params.set("q", q);
    if (customerType) params.set("customer_type", customerType);
    if (segment) params.set("segment", segment);
    api
      .get<{ customers: Customer[] }>(`/api/v1/customers?${params.toString()}`)
      .then((d) => setCustomers(d.customers))
      .catch((e) => setError(e.message));
  }

  useEffect(() => {
    const t = setTimeout(load, 250);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [q, customerType, segment]);

  return (
    <div className="flex flex-col gap-6 max-w-4xl">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Customers</h1>
        <Link href="/customers/new" className={buttonVariants()}>
          New customer
        </Link>
      </div>
      {error && <p className="text-sm text-red-600">{error}</p>}

      <Card>
        <CardContent className="flex gap-2 items-end flex-wrap pt-6">
          <div className="flex flex-col gap-2 flex-1 min-w-48">
            <Label htmlFor="q">Search</Label>
            <Input id="q" placeholder="Name, phone, or email" value={q} onChange={(e) => setQ(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2">
            <Label>Type</Label>
            <Select value={customerType || "all"} onValueChange={(v) => setCustomerType(v === "all" ? "" : (v ?? ""))}>
              <SelectTrigger className="w-32">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">All types</SelectItem>
                <SelectItem value="b2c">B2C</SelectItem>
                <SelectItem value="b2b">B2B</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-2">
            <Label>Segment</Label>
            <Select value={segment || "all"} onValueChange={(v) => setSegment(v === "all" ? "" : (v ?? ""))}>
              <SelectTrigger className="w-36">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">All segments</SelectItem>
                <SelectItem value="vip">VIP</SelectItem>
                <SelectItem value="regular">Regular</SelectItem>
                <SelectItem value="new">New</SelectItem>
                <SelectItem value="dormant">Dormant</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">{customers.length} customer{customers.length === 1 ? "" : "s"}</CardTitle>
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Phone</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>Segment</TableHead>
                <TableHead>Total spend</TableHead>
                <TableHead>Orders</TableHead>
                <TableHead></TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {customers.map((c) => (
                <TableRow key={c.customer_id}>
                  <TableCell className="font-medium">{c.name}</TableCell>
                  <TableCell>{c.phone}</TableCell>
                  <TableCell className="uppercase text-xs">{c.customer_type}</TableCell>
                  <TableCell>
                    <Badge variant={SEGMENT_VARIANT[c.segment] ?? "outline"}>{c.segment}</Badge>
                  </TableCell>
                  <TableCell>₹{c.total_spend}</TableCell>
                  <TableCell>{c.transaction_count}</TableCell>
                  <TableCell>
                    <Link href={`/customers/${c.customer_id}`} className={buttonVariants({ variant: "outline", size: "sm" })}>
                      View
                    </Link>
                  </TableCell>
                </TableRow>
              ))}
              {customers.length === 0 && (
                <TableRow>
                  <TableCell colSpan={7} className="text-center text-sm text-zinc-500 py-6">
                    No matching customers
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
    </div>
  );
}
