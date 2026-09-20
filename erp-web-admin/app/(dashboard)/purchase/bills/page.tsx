"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { api } from "@/lib/api-client";
import { BillSummary } from "@/lib/types";
import { buttonVariants } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";

const STATUS_VARIANT: Record<string, "default" | "secondary" | "outline"> = {
  unpaid: "outline",
  partially_paid: "secondary",
  paid: "default",
};

export default function BillsListPage() {
  const [bills, setBills] = useState<BillSummary[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api
      .get<{ bills: BillSummary[] }>("/api/v1/purchase/bills")
      .then((d) => setBills(d.bills))
      .catch((e) => setError(e.message));
  }, []);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Purchase Bills</h1>
        <Link href="/purchase/bills/new" className={buttonVariants()}>
          New bill
        </Link>
      </div>
      {error && <p className="text-sm text-red-600">{error}</p>}
      {bills && bills.length === 0 && <p className="text-sm text-zinc-500">No bills yet.</p>}
      {bills && bills.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Bill Number</TableHead>
              <TableHead>Date</TableHead>
              <TableHead>Grand Total</TableHead>
              <TableHead>Paid</TableHead>
              <TableHead>Status</TableHead>
              <TableHead></TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {bills.map((b) => (
              <TableRow key={b.bill_id}>
                <TableCell className="font-medium">{b.bill_number}</TableCell>
                <TableCell>{b.bill_date}</TableCell>
                <TableCell>₹{b.grand_total}</TableCell>
                <TableCell>₹{b.amount_paid}</TableCell>
                <TableCell>
                  <Badge variant={STATUS_VARIANT[b.status] ?? "outline"}>{b.status}</Badge>
                </TableCell>
                <TableCell>
                  <Link href={`/purchase/bills/${b.bill_id}`} className="text-sm underline">
                    View
                  </Link>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  );
}
