"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { api } from "@/lib/api-client";
import { PurchaseReturnSummary } from "@/lib/types";
import { buttonVariants } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

export default function PurchaseReturnsPage() {
  const [returns, setReturns] = useState<PurchaseReturnSummary[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api
      .get<{ returns: PurchaseReturnSummary[] }>("/api/v1/purchase/returns")
      .then((d) => setReturns(d.returns))
      .catch((e) => setError(e.message));
  }, []);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Purchase Returns</h1>
        <Link href="/purchase/returns/new" className={buttonVariants()}>
          New return
        </Link>
      </div>
      {error && <p className="text-sm text-red-600">{error}</p>}
      {returns && returns.length === 0 && <p className="text-sm text-zinc-500">No purchase returns yet.</p>}
      {returns && returns.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Return Number</TableHead>
              <TableHead>Reason</TableHead>
              <TableHead>Grand Total</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Created</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {returns.map((r) => (
              <TableRow key={r.return_id}>
                <TableCell className="font-medium">{r.return_number}</TableCell>
                <TableCell>{r.reason}</TableCell>
                <TableCell>₹{r.grand_total}</TableCell>
                <TableCell>{r.status}</TableCell>
                <TableCell>{new Date(r.created_at).toLocaleString()}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  );
}
