"use client";

import { useEffect, useState } from "react";
import { api, ApiError } from "@/lib/api-client";
import { Supplier, LedgerLine } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

export default function PartyLedgerPage() {
  const [suppliers, setSuppliers] = useState<Supplier[]>([]);
  const [partyType, setPartyType] = useState<"supplier" | "customer">("supplier");
  const [partyId, setPartyId] = useState("");
  const [lines, setLines] = useState<LedgerLine[] | null>(null);
  const [balance, setBalance] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api.get<{ suppliers: Supplier[] }>("/api/v1/suppliers").then((d) => setSuppliers(d.suppliers));
  }, []);

  async function handleLoad(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    try {
      const d = await api.get<{ lines: LedgerLine[]; balance: string }>(
        `/api/v1/accounting/party-ledger?party_type=${partyType}&party_id=${encodeURIComponent(partyId)}`
      );
      setLines(d.lines);
      setBalance(d.balance);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not reach the server");
    }
  }

  return (
    <div className="flex flex-col gap-6 max-w-2xl">
      <h1 className="text-2xl font-semibold">Party Ledger</h1>

      <Card>
        <CardContent className="pt-6">
          <form onSubmit={handleLoad} className="flex gap-2 items-end flex-wrap">
            <div className="flex flex-col gap-2">
              <Label>Party type</Label>
              <Select value={partyType} onValueChange={(v) => setPartyType((v as "supplier" | "customer") ?? "supplier")}>
                <SelectTrigger className="w-36">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="supplier">Supplier</SelectItem>
                  <SelectItem value="customer">Customer</SelectItem>
                </SelectContent>
              </Select>
            </div>
            {partyType === "supplier" ? (
              <div className="flex flex-col gap-2">
                <Label>Supplier</Label>
                <Select value={partyId} onValueChange={(v) => setPartyId(v ?? "")}>
                  <SelectTrigger className="w-64">
                    <SelectValue placeholder="Select a supplier" />
                  </SelectTrigger>
                  <SelectContent>
                    {suppliers.map((s) => (
                      <SelectItem key={s.supplier_id} value={s.supplier_id}>
                        {s.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            ) : (
              <div className="flex flex-col gap-2">
                <Label htmlFor="customerId">Customer ID</Label>
                <Input id="customerId" className="w-64" value={partyId} onChange={(e) => setPartyId(e.target.value)} placeholder="UUID — no customer directory yet" />
              </div>
            )}
            <Button type="submit" disabled={!partyId}>
              Load
            </Button>
          </form>
          {partyType === "customer" && (
            <p className="text-xs text-zinc-500 mt-2">
              No customer directory exists yet (POS sales are always paid in full — there is no credit-sale flow that would create a customer ledger entry).
              Enter a customer ID directly if you have one.
            </p>
          )}
        </CardContent>
      </Card>

      {error && <p className="text-sm text-red-600">{error}</p>}

      {lines && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Balance: ₹{balance}</CardTitle>
          </CardHeader>
          <CardContent>
            {lines.length === 0 ? (
              <p className="text-sm text-zinc-500">No ledger entries for this party.</p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Date</TableHead>
                    <TableHead>Description</TableHead>
                    <TableHead>Source</TableHead>
                    <TableHead>Debit</TableHead>
                    <TableHead>Credit</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {lines.map((l, i) => (
                    <TableRow key={i}>
                      <TableCell>{l.entry_date}</TableCell>
                      <TableCell>{l.description}</TableCell>
                      <TableCell>{l.source_type}</TableCell>
                      <TableCell>₹{l.debit}</TableCell>
                      <TableCell>₹{l.credit}</TableCell>
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
