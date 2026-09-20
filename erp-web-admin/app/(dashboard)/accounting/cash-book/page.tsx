"use client";

import { useEffect, useState, useCallback } from "react";
import { api, ApiError, SEED_BRANCH_ID } from "@/lib/api-client";
import { LedgerLine } from "@/lib/types";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

function today() {
  return new Date().toISOString().slice(0, 10);
}

export default function CashBookPage() {
  const [date, setDate] = useState(today());
  const [lines, setLines] = useState<LedgerLine[] | null>(null);
  const [netMovement, setNetMovement] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(() => {
    api
      .get<{ lines: LedgerLine[]; net_movement: string }>(`/api/v1/accounting/cash-book?branch_id=${SEED_BRANCH_ID}&date=${date}`)
      .then((d) => {
        setLines(d.lines);
        setNetMovement(d.net_movement);
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not reach the server"));
  }, [date]);

  useEffect(load, [load]);

  return (
    <div className="flex flex-col gap-6 max-w-2xl">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Cash Book</h1>
        <div className="flex items-center gap-2">
          <Label htmlFor="date" className="text-sm">
            Date
          </Label>
          <Input id="date" type="date" value={date} onChange={(e) => setDate(e.target.value)} className="w-40" />
        </div>
      </div>
      {error && <p className="text-sm text-red-600">{error}</p>}
      <p className="text-sm text-zinc-500">Cash, Bank, Card Clearing, and UPI Clearing movements only.</p>

      {lines && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Net movement: ₹{netMovement}</CardTitle>
          </CardHeader>
          <CardContent>
            {lines.length === 0 ? (
              <p className="text-sm text-zinc-500">No cash/bank movement on this date.</p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Description</TableHead>
                    <TableHead>Source</TableHead>
                    <TableHead>Debit</TableHead>
                    <TableHead>Credit</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {lines.map((l, i) => (
                    <TableRow key={i}>
                      <TableCell>{l.description}</TableCell>
                      <TableCell>{l.source_type}</TableCell>
                      <TableCell>{Number(l.debit) > 0 ? `₹${l.debit}` : ""}</TableCell>
                      <TableCell>{Number(l.credit) > 0 ? `₹${l.credit}` : ""}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </CardContent>
        </Card>
      )}
    </div>
  );
}
