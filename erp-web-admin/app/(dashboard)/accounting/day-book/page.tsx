"use client";

import { useEffect, useState, useCallback } from "react";
import { api, ApiError, SEED_BRANCH_ID } from "@/lib/api-client";
import { DayBookEntry } from "@/lib/types";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";

function today() {
  return new Date().toISOString().slice(0, 10);
}

export default function DayBookPage() {
  const [date, setDate] = useState(today());
  const [entries, setEntries] = useState<DayBookEntry[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(() => {
    api
      .get<{ entries: DayBookEntry[] }>(`/api/v1/accounting/day-book?branch_id=${SEED_BRANCH_ID}&date=${date}`)
      .then((d) => setEntries(d.entries))
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not reach the server"));
  }, [date]);

  useEffect(load, [load]);

  return (
    <div className="flex flex-col gap-6 max-w-3xl">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Day Book</h1>
        <div className="flex items-center gap-2">
          <Label htmlFor="date" className="text-sm">
            Date
          </Label>
          <Input id="date" type="date" value={date} onChange={(e) => setDate(e.target.value)} className="w-40" />
        </div>
      </div>
      {error && <p className="text-sm text-red-600">{error}</p>}
      {entries && entries.length === 0 && <p className="text-sm text-zinc-500">No journal entries posted for this date.</p>}
      <div className="flex flex-col gap-4">
        {entries?.map((entry) => (
          <Card key={entry.entry_number}>
            <CardHeader className="pb-2">
              <div className="flex items-center justify-between">
                <CardTitle className="text-sm font-mono">{entry.entry_number}</CardTitle>
                <Badge variant="outline">{entry.source_type}</Badge>
              </div>
              <p className="text-sm text-zinc-500">{entry.description}</p>
            </CardHeader>
            <CardContent>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Account</TableHead>
                    <TableHead>Debit</TableHead>
                    <TableHead>Credit</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {entry.lines.map((l, i) => (
                    <TableRow key={i}>
                      <TableCell>
                        {l.account_code} — {l.account_name}
                      </TableCell>
                      <TableCell>{Number(l.debit) > 0 ? `₹${l.debit}` : ""}</TableCell>
                      <TableCell>{Number(l.credit) > 0 ? `₹${l.credit}` : ""}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </CardContent>
          </Card>
        ))}
      </div>
    </div>
  );
}
