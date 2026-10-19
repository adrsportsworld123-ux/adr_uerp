"use client";

// One customer's or supplier's ledger: every posted line naming them, with
// a running balance (debit − credit: a customer's positive balance is what
// they owe, a supplier's negative one is what is owed to them). The party
// is searched on the server, and a link from a journal line
// (?party_type=&party_id=) opens straight onto that party. Print gives a
// statement.
import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { Printer } from "lucide-react";
import { api, ApiError } from "@/lib/api-client";
import { LedgerLine } from "@/lib/types";
import { SOURCE_LABEL, rupees } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { PageHeader } from "@/components/data/page-header";
import { selectClass } from "@/components/data/panel";
import { PartyPicker, PartyType } from "@/components/data/party-picker";

type Ledger = {
  party_name: string | null;
  opening_balance: string;
  total_debit: string;
  total_credit: string;
  balance: string;
  lines: LedgerLine[];
};

export default function PartyLedgerPage() {
  // Opened from a journal line (?party_type=&party_id=): that party, straight away.
  const params = useSearchParams();
  const linkedType = params.get("party_type");
  const linked = (linkedType === "customer" || linkedType === "supplier") && params.get("party_id") ? (linkedType as PartyType) : null;
  const [partyType, setPartyType] = useState<PartyType>(linked ?? "supplier");
  const [partyId, setPartyId] = useState(linked ? params.get("party_id")! : "");
  const [partyName, setPartyName] = useState<string | null>(null);
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [ledger, setLedger] = useState<Ledger | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async (type: PartyType, id: string, f: string, t: string) => {
    setError(null);
    try {
      const qs = new URLSearchParams({ party_type: type, party_id: id, from: f, to: t });
      setLedger(await api.get<Ledger>(`/api/v1/accounting/party-ledger?${qs}`));
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not reach the server");
    }
  }, []);

  const linkedId = params.get("party_id");
  useEffect(() => {
    if (!linked || !linkedId) return;
    api
      .get<Ledger>(`/api/v1/accounting/party-ledger?${new URLSearchParams({ party_type: linked, party_id: linkedId })}`)
      .then(setLedger)
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not reach the server"));
  }, [linked, linkedId]);

  return (
    <div className="flex max-w-5xl flex-col gap-5">
      <PageHeader
        title="Party Ledger"
        description="Everything posted against one customer or supplier, with a running balance."
        actions={
          ledger ? (
            <Button variant="outline" onClick={() => window.print()} className="print:hidden">
              <Printer /> Print statement
            </Button>
          ) : undefined
        }
      />

      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (partyId) void load(partyType, partyId, from, to);
        }}
        className="flex flex-wrap items-end gap-3 rounded-xl border bg-card p-4 print:hidden"
      >
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="pl-type">Party type</Label>
          <select
            id="pl-type"
            value={partyType}
            onChange={(e) => {
              setPartyType(e.target.value as PartyType);
              setPartyId("");
              setLedger(null);
            }}
            className={`${selectClass} w-36`}
          >
            <option value="supplier">Supplier</option>
            <option value="customer">Customer</option>
          </select>
        </div>
        <div className="flex w-72 flex-col gap-1.5">
          <Label>{partyType === "customer" ? "Customer" : "Supplier"}</Label>
          <PartyPicker
            type={partyType}
            value={partyId}
            initialLabel={partyName ?? ledger?.party_name}
            ariaLabel={partyType === "customer" ? "Customer" : "Supplier"}
            onChange={(id, name) => {
              setPartyId(id);
              setPartyName(name);
            }}
          />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="pl-from">From</Label>
          <Input id="pl-from" type="date" value={from} onChange={(e) => setFrom(e.target.value)} className="w-40" />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="pl-to">To</Label>
          <Input id="pl-to" type="date" value={to} onChange={(e) => setTo(e.target.value)} className="w-40" />
        </div>
        <Button type="submit" disabled={!partyId}>
          Load
        </Button>
      </form>

      {error && (
        <p className="text-sm text-destructive" role="alert">
          {error}
        </p>
      )}

      {ledger && (
        <div className="rounded-xl border bg-card p-4">
          <div className="mb-3 flex flex-wrap items-baseline justify-between gap-2">
            <h2 className="text-base font-semibold">
              {ledger.party_name ?? "(removed)"} <span className="text-sm font-normal text-muted-foreground">· {partyType === "customer" ? "Customer" : "Supplier"}</span>
              {(from || to) && <span className="block text-xs font-normal text-muted-foreground">{`${from || "start"} to ${to || "today"}`}</span>}
            </h2>
            <p className="text-sm">
              Balance: <span className="tabular font-semibold" data-testid="party-balance">{rupees(ledger.balance)}</span>
            </p>
          </div>
          {ledger.lines.length === 0 ? (
            <p className="text-sm text-muted-foreground">No ledger entries for this party{from || to ? " in these dates" : ""}.</p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full min-w-[720px] text-sm">
                <thead className="text-left text-xs text-muted-foreground">
                  <tr>
                    <th className="pb-1 font-medium">Date</th>
                    <th className="pb-1 font-medium">Entry</th>
                    <th className="pb-1 font-medium">Description</th>
                    <th className="pb-1 font-medium">Account</th>
                    <th className="pb-1 text-right font-medium">Debit</th>
                    <th className="pb-1 text-right font-medium">Credit</th>
                    <th className="pb-1 text-right font-medium">Balance</th>
                  </tr>
                </thead>
                <tbody>
                  {from && (
                    <tr className="border-t text-muted-foreground">
                      <td className="py-1.5" colSpan={6}>
                        Opening balance
                      </td>
                      <td className="tabular py-1.5 text-right">{rupees(ledger.opening_balance)}</td>
                    </tr>
                  )}
                  {ledger.lines.map((l, i) => (
                    <tr key={i} className="border-t">
                      <td className="py-1.5 whitespace-nowrap">{l.entry_date}</td>
                      <td className="py-1.5">
                        <Link href={`/accounting/journal-entries/${l.entry_id}`} className="font-mono text-xs hover:underline">
                          {l.entry_number}
                        </Link>
                        <span className="block text-xs text-muted-foreground">{SOURCE_LABEL[l.source_type] ?? l.source_type}</span>
                      </td>
                      <td className="py-1.5">{l.description}</td>
                      <td className="py-1.5 text-muted-foreground">
                        {l.account_code} · {l.account_name}
                      </td>
                      <td className="tabular py-1.5 text-right">{Number(l.debit) ? rupees(l.debit) : ""}</td>
                      <td className="tabular py-1.5 text-right">{Number(l.credit) ? rupees(l.credit) : ""}</td>
                      <td className="tabular py-1.5 text-right">{rupees(l.running_balance)}</td>
                    </tr>
                  ))}
                </tbody>
                <tfoot>
                  <tr className="border-t font-medium">
                    <td className="pt-2" colSpan={4}>
                      Total
                    </td>
                    <td className="tabular pt-2 text-right">{rupees(ledger.total_debit)}</td>
                    <td className="tabular pt-2 text-right">{rupees(ledger.total_credit)}</td>
                    <td className="tabular pt-2 text-right">{rupees(ledger.balance)}</td>
                  </tr>
                </tfoot>
              </table>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
