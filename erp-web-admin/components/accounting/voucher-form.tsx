"use client";

// The journal voucher form (Phase 14): date, branch, description and a
// balanced set of lines. Used to create a voucher and to edit a draft. What
// the buttons do follows the merchant's setting: with approval off a voucher
// can post straight away (today's behaviour); with it on, it's submitted
// for someone else to approve. Amounts are checked here for balance, and
// again on the server, to the paisa.
//
// Each line can name its party (a customer or a supplier), which is what
// puts it on that party's ledger. A line on Accounts Receivable (1100) or
// Accounts Payable (2002) must name one, so the party ledger keeps agreeing
// with the control account; choosing either account picks the party type.
import { useEffect, useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { api, ApiError, SEED_BRANCH_ID } from "@/lib/api-client";
import { Account, AccountingSettings, Branch, Voucher } from "@/lib/types";
import { rupees } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { PartyPicker, PartyType } from "@/components/data/party-picker";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

type Line = { account_code: string; debit: string; credit: string; memo: string; party_type: PartyType | ""; party_id: string; party_name: string };
const blankLine = (): Line => ({ account_code: "", debit: "", credit: "", memo: "", party_type: "", party_id: "", party_name: "" });
// Control accounts and the party each one is kept by (erp-core-go vouchers.go partyAccounts).
const PARTY_ACCOUNTS: Record<string, PartyType> = { "1100": "customer", "2002": "supplier" };
const selectClass = "h-9 rounded-lg border bg-card px-2 text-sm";

function paise(v: string) {
  return Math.round((Number(v) || 0) * 100);
}

export function VoucherForm({ voucher, onSaved }: { voucher?: Voucher; onSaved: (v: Voucher) => void }) {
  const editing = !!voucher;
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [branches, setBranches] = useState<Branch[]>([]);
  const [settings, setSettings] = useState<AccountingSettings | null>(null);
  const [branchId, setBranchId] = useState(voucher?.branch_id ?? SEED_BRANCH_ID);
  const [date, setDate] = useState(voucher?.entry_date ?? new Date().toISOString().slice(0, 10));
  const [description, setDescription] = useState(voucher?.description ?? "");
  const [lines, setLines] = useState<Line[]>(
    voucher?.lines?.length
      ? voucher.lines.map((l) => ({
          account_code: l.account_code,
          debit: Number(l.debit) ? l.debit : "",
          credit: Number(l.credit) ? l.credit : "",
          memo: l.memo ?? "",
          party_type: l.party_type ?? "",
          party_id: l.party_id ?? "",
          party_name: l.party_name ?? "",
        }))
      : [blankLine(), blankLine()],
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api.get<{ accounts: Account[] }>("/api/v1/accounting/accounts").then((d) => setAccounts(d.accounts)).catch(() => {});
    api.get<{ branches: Branch[] }>("/api/v1/branches?status=active&scope=mine").then((d) => setBranches(d.branches)).catch(() => {});
    api.get<AccountingSettings>("/api/v1/accounting/settings").then(setSettings).catch(() => {});
  }, []);

  const update = (i: number, patch: Partial<Line>) => setLines((ls) => ls.map((l, j) => (j === i ? { ...l, ...patch } : l)));
  const dr = lines.reduce((s, l) => s + paise(l.debit), 0);
  const cr = lines.reduce((s, l) => s + paise(l.credit), 0);
  const filled = lines.filter((l) => l.account_code && (paise(l.debit) > 0) !== (paise(l.credit) > 0));
  const balanced = dr > 0 && dr === cr && filled.length >= 2 && filled.length === lines.filter((l) => l.account_code || l.debit || l.credit).length;
  const locked = settings?.locked_through && date <= settings.locked_through;
  // A party is all or nothing, and a control account needs its own kind.
  const partyProblem = (l: Line) =>
    l.party_type && !l.party_id
      ? `Choose the ${l.party_type}`
      : PARTY_ACCOUNTS[l.account_code] && l.party_type !== PARTY_ACCOUNTS[l.account_code]
        ? `Needs a ${PARTY_ACCOUNTS[l.account_code]}`
        : null;
  const partiesOk = lines.every((l) => !l.account_code || !partyProblem(l));
  const ready = balanced && partiesOk && description.trim() !== "" && !locked;
  const chooseAccount = (i: number, code: string) => {
    const want = PARTY_ACCOUNTS[code];
    const l = lines[i];
    update(i, want && l.party_type !== want ? { account_code: code, party_type: want, party_id: "", party_name: "" } : { account_code: code });
  };

  async function save(action: "draft" | "submit" | "post") {
    setBusy(true);
    setError(null);
    const body = {
      branch_id: branchId,
      entry_date: date,
      description: description.trim(),
      lines: filled.map((l) => ({
        account_code: l.account_code,
        debit: Number(l.debit) || 0,
        credit: Number(l.credit) || 0,
        memo: l.memo,
        party_type: l.party_id ? l.party_type : "",
        party_id: l.party_id ? l.party_id : "",
      })),
    };
    try {
      if (editing) {
        let v = await api.put<Voucher>(`/api/v1/accounting/vouchers/${voucher!.voucher_id}`, body);
        if (action !== "draft") v = await api.post<Voucher>(`/api/v1/accounting/vouchers/${v.voucher_id}/${action}`, {});
        onSaved(v);
      } else {
        onSaved(await api.post<Voucher>("/api/v1/accounting/vouchers", { ...body, action }));
      }
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not save the voucher");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="grid gap-3 sm:grid-cols-3">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="jv-date">Date</Label>
          <Input id="jv-date" type="date" value={date} onChange={(e) => setDate(e.target.value)} aria-invalid={!!locked} />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="jv-branch">Branch</Label>
          <select id="jv-branch" value={branchId} onChange={(e) => setBranchId(e.target.value)} className={selectClass}>
            {branches.map((b) => (
              <option key={b.branch_id} value={b.branch_id}>
                {b.name}
              </option>
            ))}
          </select>
        </div>
        <div className="flex flex-col gap-1.5 sm:col-span-3">
          <Label htmlFor="jv-desc">Description</Label>
          <Input id="jv-desc" value={description} onChange={(e) => setDescription(e.target.value)} placeholder="e.g. Rent accrual for October" />
        </div>
      </div>
      {locked && (
        <p className="text-sm text-destructive" role="alert">
          The books are locked through {settings!.locked_through}. Choose a later date.
        </p>
      )}

      <div className="overflow-x-auto">
      <table className="w-full min-w-[760px] text-sm">
        <thead className="text-left text-xs text-muted-foreground">
          <tr>
            <th className="pb-1 font-medium">Account</th>
            <th className="pb-1 font-medium">Party</th>
            <th className="pb-1 text-right font-medium">Debit</th>
            <th className="pb-1 text-right font-medium">Credit</th>
            <th className="pb-1 font-medium">Memo</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {lines.map((l, i) => (
            <tr key={i}>
              <td className="py-1 pr-2">
                <select aria-label={`Line ${i + 1} account`} value={l.account_code} onChange={(e) => chooseAccount(i, e.target.value)} className={`${selectClass} w-full`}>
                  <option value="">Choose an account</option>
                  {accounts.map((a) => (
                    <option key={a.account_id} value={a.code}>
                      {a.code} · {a.name}
                    </option>
                  ))}
                </select>
              </td>
              <td className="py-1 pr-2 align-top">
                <div className="flex min-w-56 gap-1.5">
                  <select
                    aria-label={`Line ${i + 1} party type`}
                    value={l.party_type}
                    onChange={(e) => update(i, { party_type: e.target.value as PartyType | "", party_id: "", party_name: "" })}
                    className={`${selectClass} w-28`}
                  >
                    <option value="">No party</option>
                    <option value="customer">Customer</option>
                    <option value="supplier">Supplier</option>
                  </select>
                  {l.party_type && (
                    <div className="min-w-0 flex-1">
                      <PartyPicker
                        type={l.party_type}
                        value={l.party_id}
                        initialLabel={l.party_name}
                        ariaLabel={`Line ${i + 1} party`}
                        onChange={(id, name) => update(i, { party_id: id, party_name: name })}
                      />
                    </div>
                  )}
                </div>
                {l.account_code && partyProblem(l) && (
                  <p className="mt-0.5 text-xs text-destructive" data-testid="jv-party-error">
                    {partyProblem(l)}
                  </p>
                )}
              </td>
              <td className="py-1 pr-2">
                <Input aria-label={`Line ${i + 1} debit`} className="tabular w-28 text-right" type="number" min={0} step="0.01" value={l.debit} onChange={(e) => update(i, { debit: e.target.value, credit: e.target.value ? "" : l.credit })} />
              </td>
              <td className="py-1 pr-2">
                <Input aria-label={`Line ${i + 1} credit`} className="tabular w-28 text-right" type="number" min={0} step="0.01" value={l.credit} onChange={(e) => update(i, { credit: e.target.value, debit: e.target.value ? "" : l.debit })} />
              </td>
              <td className="py-1 pr-2">
                <Input aria-label={`Line ${i + 1} memo`} value={l.memo} onChange={(e) => update(i, { memo: e.target.value })} />
              </td>
              <td className="py-1">
                {lines.length > 2 && (
                  <Button variant="ghost" size="icon-sm" aria-label={`Remove line ${i + 1}`} onClick={() => setLines((ls) => ls.filter((_, j) => j !== i))}>
                    <Trash2 />
                  </Button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
        <tfoot>
          <tr className="border-t font-medium">
            <td className="pt-2">
              <Button variant="ghost" size="sm" onClick={() => setLines((ls) => [...ls, blankLine()])}>
                <Plus /> Add line
              </Button>
            </td>
            <td />
            <td className="tabular pt-2 text-right">{rupees(dr / 100)}</td>
            <td className="tabular pt-2 text-right">{rupees(cr / 100)}</td>
            <td className={`pt-2 text-sm ${balanced ? "text-pay" : "text-muted-foreground"}`} colSpan={2} data-testid="jv-balance">
              {balanced ? "Balanced" : dr !== cr ? `Off by ${rupees(Math.abs(dr - cr) / 100)}` : "Two or more lines, each a debit or a credit"}
            </td>
          </tr>
        </tfoot>
      </table>
      </div>

      {error && (
        <p className="text-sm text-destructive" role="alert">
          {error}
        </p>
      )}
      <div className="flex flex-wrap gap-2">
        <Button variant="outline" disabled={busy || !ready} onClick={() => save("draft")}>
          {editing ? "Save changes" : "Save as draft"}
        </Button>
        {settings?.jv_approval_required ? (
          <Button disabled={busy || !ready} onClick={() => save("submit")}>
            Submit for approval
          </Button>
        ) : (
          <Button disabled={busy || !ready} onClick={() => save("post")}>
            Post now
          </Button>
        )}
      </div>
      <p className="text-xs text-muted-foreground">
        {settings?.jv_approval_required
          ? "Your business requires approval: a different user with JV approval rights approves it, then it can be posted."
          : "Posted entries are final: a mistake is corrected by reversing the voucher, never by editing it."}
      </p>
    </div>
  );
}
