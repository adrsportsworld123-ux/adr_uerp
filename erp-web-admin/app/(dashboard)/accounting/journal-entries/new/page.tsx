"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { api, ApiError, SEED_BRANCH_ID } from "@/lib/api-client";
import { Account } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

interface LineDraft {
  account_code: string;
  debit: string;
  credit: string;
}

export default function NewJournalEntryPage() {
  const router = useRouter();
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [description, setDescription] = useState("");
  const [lines, setLines] = useState<LineDraft[]>([
    { account_code: "", debit: "", credit: "" },
    { account_code: "", debit: "", credit: "" },
  ]);
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    api.get<{ accounts: Account[] }>("/api/v1/accounting/accounts").then((d) => setAccounts(d.accounts));
  }, []);

  function updateLine(i: number, patch: Partial<LineDraft>) {
    setLines((prev) => prev.map((l, idx) => (idx === i ? { ...l, ...patch } : l)));
  }

  function addLine() {
    setLines((prev) => [...prev, { account_code: "", debit: "", credit: "" }]);
  }

  function removeLine(i: number) {
    setLines((prev) => prev.filter((_, idx) => idx !== i));
  }

  const totalDebit = lines.reduce((sum, l) => sum + (Number(l.debit) || 0), 0);
  const totalCredit = lines.reduce((sum, l) => sum + (Number(l.credit) || 0), 0);
  const balanced = Math.abs(totalDebit - totalCredit) < 0.01 && totalDebit > 0;

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      await api.post("/api/v1/accounting/journal-entries", {
        branch_id: SEED_BRANCH_ID,
        description,
        lines: lines
          .filter((l) => l.account_code)
          .map((l) => ({ account_code: l.account_code, debit: Number(l.debit) || 0, credit: Number(l.credit) || 0 })),
      });
      router.push("/accounting/day-book");
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not reach the server");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="max-w-2xl">
      <Card>
        <CardHeader>
          <CardTitle>New manual journal entry</CardTitle>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleSubmit} className="flex flex-col gap-4">
            <div className="flex flex-col gap-2">
              <Label htmlFor="description">Description *</Label>
              <Input id="description" required value={description} onChange={(e) => setDescription(e.target.value)} />
            </div>

            <div className="flex flex-col gap-2">
              <Label>Lines * (exactly one of debit/credit per line)</Label>
              {lines.map((line, i) => (
                <div key={i} className="flex gap-2 items-center">
                  <Select value={line.account_code} onValueChange={(v) => updateLine(i, { account_code: v ?? "" })}>
                    <SelectTrigger className="w-56">
                      <SelectValue placeholder="Account" />
                    </SelectTrigger>
                    <SelectContent>
                      {accounts.map((a) => (
                        <SelectItem key={a.account_id} value={a.code}>
                          {a.code} — {a.name}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  <Input
                    type="number"
                    min="0"
                    step="0.01"
                    placeholder="Debit"
                    value={line.debit}
                    onChange={(e) => updateLine(i, { debit: e.target.value, credit: e.target.value ? "" : line.credit })}
                    className="w-28"
                  />
                  <Input
                    type="number"
                    min="0"
                    step="0.01"
                    placeholder="Credit"
                    value={line.credit}
                    onChange={(e) => updateLine(i, { credit: e.target.value, debit: e.target.value ? "" : line.debit })}
                    className="w-28"
                  />
                  {lines.length > 2 && (
                    <Button type="button" variant="ghost" size="sm" onClick={() => removeLine(i)}>
                      Remove
                    </Button>
                  )}
                </div>
              ))}
              <Button type="button" variant="outline" size="sm" onClick={addLine} className="w-fit">
                Add line
              </Button>
            </div>

            <p className={`text-sm ${balanced ? "text-green-600" : "text-zinc-500"}`}>
              Total debit ₹{totalDebit.toFixed(2)} · Total credit ₹{totalCredit.toFixed(2)} {balanced ? "— balanced" : "— must balance before posting"}
            </p>

            {error && <p className="text-sm text-red-600">{error}</p>}
            <Button type="submit" disabled={submitting || !balanced}>
              {submitting ? "Posting..." : "Post entry"}
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  );
}
