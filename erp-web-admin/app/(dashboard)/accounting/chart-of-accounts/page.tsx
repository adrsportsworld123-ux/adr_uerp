"use client";

import { useEffect, useState } from "react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Account } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

const TYPES = ["asset", "liability", "equity", "income", "expense"];

export default function ChartOfAccountsPage() {
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [error, setError] = useState<string | null>(null);

  const [code, setCode] = useState("");
  const [name, setName] = useState("");
  const [accountType, setAccountType] = useState("expense");
  const [submitting, setSubmitting] = useState(false);

  function load() {
    api
      .get<{ accounts: Account[] }>("/api/v1/accounting/accounts")
      .then((d) => setAccounts(d.accounts))
      .catch((e) => setError(e.message));
  }

  useEffect(load, []);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    try {
      await api.post("/api/v1/accounting/accounts", { code, name, account_type: accountType });
      toast.success("Account created");
      setCode("");
      setName("");
      load();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not create account");
    } finally {
      setSubmitting(false);
    }
  }

  const grouped = TYPES.map((t) => ({ type: t, items: accounts.filter((a) => a.account_type === t) }));

  return (
    <div className="flex flex-col gap-6 max-w-3xl">
      <h1 className="text-2xl font-semibold">Chart of Accounts</h1>
      {error && <p className="text-sm text-red-600">{error}</p>}

      {grouped.map(
        (g) =>
          g.items.length > 0 && (
            <div key={g.type}>
              <h2 className="text-sm font-semibold uppercase text-zinc-500 mb-2">{g.type}</h2>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Code</TableHead>
                    <TableHead>Name</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead></TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {g.items.map((a) => (
                    <TableRow key={a.account_id}>
                      <TableCell className="font-mono">{a.code}</TableCell>
                      <TableCell>{a.name}</TableCell>
                      <TableCell>
                        <Badge variant={a.status === "active" ? "default" : "secondary"}>{a.status}</Badge>
                      </TableCell>
                      <TableCell>{a.is_system && <span className="text-xs text-zinc-400">system</span>}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )
      )}

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Add a custom account</CardTitle>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleSubmit} className="flex flex-col gap-4">
            <div className="grid grid-cols-3 gap-4">
              <div className="flex flex-col gap-2">
                <Label htmlFor="code">Code *</Label>
                <Input id="code" required value={code} onChange={(e) => setCode(e.target.value)} placeholder="5010" />
              </div>
              <div className="flex flex-col gap-2 col-span-2">
                <Label htmlFor="name">Name *</Label>
                <Input id="name" required value={name} onChange={(e) => setName(e.target.value)} placeholder="Rent Expense" />
              </div>
            </div>
            <div className="flex flex-col gap-2">
              <Label>Type *</Label>
              <Select value={accountType} onValueChange={(v) => setAccountType(v ?? "expense")}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {TYPES.map((t) => (
                    <SelectItem key={t} value={t}>
                      {t}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <Button type="submit" disabled={submitting}>
              {submitting ? "Creating..." : "Add account"}
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  );
}
