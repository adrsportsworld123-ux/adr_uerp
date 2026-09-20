"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { api } from "@/lib/api-client";
import { GRNSummary } from "@/lib/types";
import { buttonVariants } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";

const STATUS_VARIANT: Record<string, "default" | "secondary" | "outline"> = {
  draft: "outline",
  completed: "default",
  cancelled: "secondary",
};

export default function GRNListPage() {
  const [grns, setGrns] = useState<GRNSummary[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api
      .get<{ grns: GRNSummary[] }>("/api/v1/purchase/grn")
      .then((d) => setGrns(d.grns))
      .catch((e) => setError(e.message));
  }, []);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Goods Receipt Notes</h1>
        <Link href="/purchase/grn/new" className={buttonVariants()}>
          New GRN
        </Link>
      </div>
      {error && <p className="text-sm text-red-600">{error}</p>}
      {grns && grns.length === 0 && <p className="text-sm text-zinc-500">No GRNs yet.</p>}
      {grns && grns.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>GRN Number</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Grand Total</TableHead>
              <TableHead>Created</TableHead>
              <TableHead></TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {grns.map((g) => (
              <TableRow key={g.grn_id}>
                <TableCell className="font-medium">{g.grn_number}</TableCell>
                <TableCell>
                  <Badge variant={STATUS_VARIANT[g.status] ?? "outline"}>{g.status}</Badge>
                </TableCell>
                <TableCell>₹{g.grand_total}</TableCell>
                <TableCell>{new Date(g.created_at).toLocaleString()}</TableCell>
                <TableCell>
                  <Link href={`/purchase/grn/${g.grn_id}`} className="text-sm underline">
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
