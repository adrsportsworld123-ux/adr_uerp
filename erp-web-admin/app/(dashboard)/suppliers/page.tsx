"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { api } from "@/lib/api-client";
import { Supplier } from "@/lib/types";
import { buttonVariants } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";

export default function SuppliersPage() {
  const [suppliers, setSuppliers] = useState<Supplier[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api
      .get<{ suppliers: Supplier[] }>("/api/v1/suppliers")
      .then((d) => setSuppliers(d.suppliers))
      .catch((e) => setError(e.message));
  }, []);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Suppliers</h1>
        <Link href="/suppliers/new" className={buttonVariants()}>
          New supplier
        </Link>
      </div>
      {error && <p className="text-sm text-red-600">{error}</p>}
      {suppliers && suppliers.length === 0 && <p className="text-sm text-zinc-500">No suppliers yet.</p>}
      {suppliers && suppliers.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>GSTIN</TableHead>
              <TableHead>Contact</TableHead>
              <TableHead>Payment terms</TableHead>
              <TableHead>Credit limit</TableHead>
              <TableHead>Status</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {suppliers.map((s) => (
              <TableRow key={s.supplier_id}>
                <TableCell className="font-medium">{s.name}</TableCell>
                <TableCell>{s.gstin || "—"}</TableCell>
                <TableCell>
                  {s.contact_name || "—"} {s.phone && `· ${s.phone}`}
                </TableCell>
                <TableCell>{s.payment_terms || "—"}</TableCell>
                <TableCell>₹{s.credit_limit}</TableCell>
                <TableCell>
                  <Badge variant={s.status === "active" ? "default" : "secondary"}>{s.status}</Badge>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  );
}
