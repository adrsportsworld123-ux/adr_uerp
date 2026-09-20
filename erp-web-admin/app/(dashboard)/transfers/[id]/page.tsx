"use client";

import { useEffect, useState, useCallback } from "react";
import { useParams } from "next/navigation";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Transfer, Branch } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";

export default function TransferDetailPage() {
  const { id } = useParams<{ id: string }>();
  const [transfer, setTransfer] = useState<Transfer | null>(null);
  const [branches, setBranches] = useState<Record<string, string>>({});
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const [rejectReason, setRejectReason] = useState("");
  const [showReject, setShowReject] = useState(false);
  const [receivedByLine, setReceivedByLine] = useState<Record<string, string>>({});

  const load = useCallback(() => {
    api
      .get<Transfer>(`/api/v1/branch-transfers/${id}`)
      .then(setTransfer)
      .catch((e) => setError(e.message));
  }, [id]);

  useEffect(() => {
    api.get<{ branches: Branch[] }>("/api/v1/branches").then((d) => {
      setBranches(Object.fromEntries(d.branches.map((b) => [b.branch_id, b.name])));
    });
    load();
  }, [load]);

  async function act(path: string, body?: unknown, successMsg?: string) {
    setBusy(true);
    try {
      await api.post(`/api/v1/branch-transfers/${id}${path}`, body);
      if (successMsg) toast.success(successMsg);
      load();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not reach the server");
    } finally {
      setBusy(false);
    }
  }

  async function handleComplete() {
    const lines = transfer!.lines
      .filter((l) => receivedByLine[l.line_id] !== undefined && receivedByLine[l.line_id] !== "")
      .map((l) => ({ line_id: l.line_id, received_quantity: Number(receivedByLine[l.line_id]) }));
    await act("/complete", { lines }, "Transfer completed");
  }

  if (error) return <p className="text-sm text-red-600">{error}</p>;
  if (!transfer) return <p className="text-sm text-zinc-500">Loading…</p>;

  const s = transfer.status;

  return (
    <div className="flex flex-col gap-6 max-w-2xl">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">{transfer.transfer_number}</h1>
          <p className="text-sm text-zinc-500">
            {branches[transfer.from_branch_id] ?? transfer.from_branch_id} → {branches[transfer.to_branch_id] ?? transfer.to_branch_id}
          </p>
        </div>
        <Badge>{s}</Badge>
      </div>

      {transfer.notes && <p className="text-sm text-zinc-600">Notes: {transfer.notes}</p>}
      {transfer.rejection_reason && <p className="text-sm text-red-600">Rejected: {transfer.rejection_reason}</p>}

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Lines</CardTitle>
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Product</TableHead>
                <TableHead>Requested</TableHead>
                <TableHead>Sent</TableHead>
                <TableHead>Received</TableHead>
                {s === "in_transit" && <TableHead>Actual received</TableHead>}
              </TableRow>
            </TableHeader>
            <TableBody>
              {transfer.lines.map((l) => (
                <TableRow key={l.line_id}>
                  <TableCell>
                    {l.product_name} <span className="text-zinc-400">({l.sku})</span>
                  </TableCell>
                  <TableCell>{l.requested_quantity}</TableCell>
                  <TableCell>{l.sent_quantity ?? "—"}</TableCell>
                  <TableCell>{l.received_quantity ?? "—"}</TableCell>
                  {s === "in_transit" && (
                    <TableCell>
                      <Input
                        type="number"
                        min="0"
                        step="0.001"
                        className="w-24"
                        placeholder={l.sent_quantity ?? ""}
                        value={receivedByLine[l.line_id] ?? ""}
                        onChange={(e) => setReceivedByLine((prev) => ({ ...prev, [l.line_id]: e.target.value }))}
                      />
                    </TableCell>
                  )}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      <div className="flex gap-2 flex-wrap">
        {s === "pending_approval" && (
          <>
            <Button disabled={busy} onClick={() => act("/approve", undefined, "Transfer approved")}>
              Approve
            </Button>
            <Button variant="outline" disabled={busy} onClick={() => setShowReject((v) => !v)}>
              Reject
            </Button>
            <Button variant="ghost" disabled={busy} onClick={() => act("/cancel", undefined, "Transfer cancelled")}>
              Cancel
            </Button>
          </>
        )}
        {s === "approved" && (
          <>
            <Button disabled={busy} onClick={() => act("/dispatch", undefined, "Transfer dispatched")}>
              Dispatch
            </Button>
            <Button variant="ghost" disabled={busy} onClick={() => act("/cancel", undefined, "Transfer cancelled")}>
              Cancel
            </Button>
          </>
        )}
        {s === "in_transit" && (
          <Button disabled={busy} onClick={handleComplete}>
            Complete (leave blank = received in full)
          </Button>
        )}
      </div>

      {showReject && (
        <Card>
          <CardContent className="pt-6 flex flex-col gap-3">
            <Label htmlFor="reason">Rejection reason</Label>
            <Input id="reason" value={rejectReason} onChange={(e) => setRejectReason(e.target.value)} />
            <Button
              variant="destructive"
              disabled={busy || !rejectReason}
              onClick={() => act("/reject", { reason: rejectReason }, "Transfer rejected")}
              className="w-fit"
            >
              Confirm rejection
            </Button>
          </CardContent>
        </Card>
      )}
    </div>
  );
}
