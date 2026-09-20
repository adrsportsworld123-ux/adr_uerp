"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { api } from "@/lib/api-client";
import { TransferSummary, Branch } from "@/lib/types";
import { buttonVariants } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";

const STATUS_VARIANT: Record<string, "default" | "secondary" | "outline" | "destructive"> = {
  pending_approval: "outline",
  approved: "secondary",
  in_transit: "secondary",
  completed: "default",
  rejected: "destructive",
  cancelled: "destructive",
};

export default function TransfersPage() {
  const [transfers, setTransfers] = useState<TransferSummary[] | null>(null);
  const [branches, setBranches] = useState<Record<string, string>>({});
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api.get<{ branches: Branch[] }>("/api/v1/branches").then((d) => {
      setBranches(Object.fromEntries(d.branches.map((b) => [b.branch_id, b.name])));
    });
    api
      .get<{ transfers: TransferSummary[] }>("/api/v1/branch-transfers")
      .then((d) => setTransfers(d.transfers))
      .catch((e) => setError(e.message));
  }, []);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Branch Transfers</h1>
        <Link href="/transfers/new" className={buttonVariants()}>
          New transfer
        </Link>
      </div>
      {error && <p className="text-sm text-red-600">{error}</p>}
      {transfers && transfers.length === 0 && <p className="text-sm text-zinc-500">No transfers yet.</p>}
      {transfers && transfers.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Transfer</TableHead>
              <TableHead>From</TableHead>
              <TableHead>To</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Created</TableHead>
              <TableHead></TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {transfers.map((t) => (
              <TableRow key={t.transfer_id}>
                <TableCell className="font-medium">{t.transfer_number}</TableCell>
                <TableCell>{branches[t.from_branch_id] ?? t.from_branch_id}</TableCell>
                <TableCell>{branches[t.to_branch_id] ?? t.to_branch_id}</TableCell>
                <TableCell>
                  <Badge variant={STATUS_VARIANT[t.status] ?? "outline"}>{t.status}</Badge>
                </TableCell>
                <TableCell>{new Date(t.created_at).toLocaleString()}</TableCell>
                <TableCell>
                  <Link href={`/transfers/${t.transfer_id}`} className="text-sm underline">
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
