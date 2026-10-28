"use client";

// Partners (erp-core-go migrations/074, internal/partners): each partner's
// capital and profit share. Capital moves through the ledger: a pay-in
// (Dr cash/bank, Cr their capital), a pay-out (the reverse), and anything
// they paid for the shop from their own money — a supplier bill ("paid by
// a partner") or an expense. The profit share is the period's net profit
// (the same figure as the P&L report) times their ratio; it is worked out,
// not posted. partners.manage.
import { FormEvent, useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { ArrowDownToLine, ArrowUpFromLine, Loader2, Pencil, Plus } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import { Partner } from "@/lib/types";
import { localDate, rupees } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Modal } from "@/components/ui/dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { PageHeader } from "@/components/data/page-header";
import { Field, FilterBar, Panel, Stat, selectClass } from "@/components/data/panel";
import { StatusChip } from "@/components/data/status-chip";
import { BranchSelect, useMyBranches } from "@/components/data/branch-select";
import { BankAccountSelect, defaultAccount, useBankAccounts } from "@/components/banking/bank-account-select";

const message = (e: unknown, fallback: string) => (e instanceof ApiError ? e.message : fallback);

type SummaryRow = { partner_id: string; name: string; status: string; share_ratio: string; capital: string; paid_in: string; taken_out: string; profit_share: string };
type Summary = { from: string; to: string; net_profit: string; total_capital: string; active_share_total: string; shares_balanced: boolean; partners: SummaryRow[] };

/** The financial year so far (April to today), the merchant's calendar. */
function fyStart(): string {
  const today = localDate();
  const [y, m] = today.split("-").map(Number);
  return `${m >= 4 ? y : y - 1}-04-01`;
}

export default function PartnersPage() {
  const { can } = usePermissions();
  const allowed = can("partners.manage");
  const [from, setFrom] = useState(fyStart);
  const [to, setTo] = useState(() => localDate());
  const [summary, setSummary] = useState<Summary | null>(null);
  const [partners, setPartners] = useState<Partner[]>([]);
  const [editing, setEditing] = useState<Partner | "new" | null>(null);
  const [moving, setMoving] = useState<{ p: SummaryRow; kind: "pay-in" | "pay-out" } | null>(null);

  const load = useCallback(() => {
    if (!allowed) return;
    api
      .get<Summary>(`/api/v1/partners/summary?from=${from}&to=${to}`)
      .then(setSummary)
      .catch((e) => toast.error(message(e, "Could not load the partners")));
    api
      .get<{ partners: Partner[] }>("/api/v1/partners")
      .then((d) => setPartners(d.partners))
      .catch(() => {});
  }, [allowed, from, to]);
  useEffect(load, [load]);

  if (!allowed) {
    return (
      <div className="space-y-4">
        <PageHeader title="Partners" description="Partners' capital, what each put in, paid and took out, and their share of the profit." />
        <p className="text-sm text-muted-foreground">{missingPermissionHint("partners.manage")}</p>
      </div>
    );
  }
  const byId = (id: string) => partners.find((p) => p.partner_id === id);

  return (
    <div className="space-y-5">
      <PageHeader
        title="Partners"
        description="Each partner's capital — what they put in, paid for the shop from their own money and took out — and their share of the profit."
        actions={
          <Button onClick={() => setEditing("new")}>
            <Plus /> Add partner
          </Button>
        }
      />
      <FilterBar>
        <Field label="From" htmlFor="pt-from">
          <Input id="pt-from" type="date" value={from} max={to} onChange={(e) => setFrom(e.target.value)} />
        </Field>
        <Field label="To" htmlFor="pt-to">
          <Input id="pt-to" type="date" value={to} min={from} onChange={(e) => setTo(e.target.value)} />
        </Field>
      </FilterBar>
      {summary && (
        <Panel>
          <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
            <Stat label="Net profit in the period" value={rupees(summary.net_profit)} strong testId="pt-profit" />
            <Stat label="Total capital at the end" value={rupees(summary.total_capital)} />
            <Stat label="Active partners' shares" value={`${summary.active_share_total}%`} />
            <Stat
              label="Shares"
              value={
                summary.shares_balanced ? (
                  <StatusChip status="" tone="ok" label="Add up to 100%" />
                ) : (
                  <StatusChip status="" tone="warn" label="Don't add up to 100%" />
                )
              }
            />
          </div>
          <p className="text-xs text-muted-foreground">
            Profit is the same figure as Reports › Profit and loss for these dates. The share is shown, not posted; transfer it to capital with a journal
            voucher at year end if your firm does.
          </p>
        </Panel>
      )}
      <Panel title="Partners">
        <Table data-testid="partners-table">
          <TableHeader>
            <TableRow>
              <TableHead>Partner</TableHead>
              <TableHead className="text-right">Share</TableHead>
              <TableHead className="text-right">Put in / paid</TableHead>
              <TableHead className="text-right">Taken out</TableHead>
              <TableHead className="text-right">Capital</TableHead>
              <TableHead className="text-right">Profit share</TableHead>
              <TableHead className="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {(summary?.partners ?? []).map((p) => (
              <TableRow key={p.partner_id}>
                <TableCell>
                  <Link href={`/partners/${p.partner_id}?from=${from}&to=${to}`} className="font-medium hover:underline">
                    {p.name}
                  </Link>
                  {p.status !== "active" && <StatusChip status="" tone="muted" label="Retired" className="ms-2" />}
                  <span className="block text-xs text-muted-foreground">{byId(p.partner_id)?.account_code}</span>
                </TableCell>
                <TableCell className="tabular text-right">{p.share_ratio}%</TableCell>
                <TableCell className="tabular text-right">{rupees(p.paid_in)}</TableCell>
                <TableCell className="tabular text-right">{rupees(p.taken_out)}</TableCell>
                <TableCell className="tabular text-right font-medium">{rupees(p.capital)}</TableCell>
                <TableCell className="tabular text-right">{rupees(p.profit_share)}</TableCell>
                <TableCell>
                  <div className="flex justify-end gap-1.5">
                    <Button size="sm" variant="outline" disabled={p.status !== "active"} onClick={() => setMoving({ p, kind: "pay-in" })} aria-label={`Pay in for ${p.name}`}>
                      <ArrowDownToLine aria-hidden /> Pay in
                    </Button>
                    <Button size="sm" variant="outline" disabled={p.status !== "active"} onClick={() => setMoving({ p, kind: "pay-out" })} aria-label={`Pay out to ${p.name}`}>
                      <ArrowUpFromLine aria-hidden /> Pay out
                    </Button>
                    <Button size="sm" variant="ghost" onClick={() => setEditing(byId(p.partner_id) ?? null)} aria-label={`Edit ${p.name}`}>
                      <Pencil aria-hidden />
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ))}
            {summary && summary.partners.length === 0 && (
              <TableRow>
                <TableCell colSpan={7} className="py-6 text-center text-sm text-muted-foreground">
                  No partners yet. Add each partner with their profit-sharing ratio.
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </Panel>
      {editing && <PartnerDialog partner={editing === "new" ? null : editing} onClose={() => setEditing(null)} onSaved={load} />}
      {moving && <MoneyDialog partner={moving.p} kind={moving.kind} onClose={() => setMoving(null)} onSaved={load} />}
    </div>
  );
}

function PartnerDialog({ partner, onClose, onSaved }: { partner: Partner | null; onClose: () => void; onSaved: () => void }) {
  const p = partner;
  const [name, setName] = useState(p?.name ?? "");
  const [share, setShare] = useState(p ? String(Number(p.share_ratio)) : "");
  const [phone, setPhone] = useState(p?.phone ?? "");
  const [email, setEmail] = useState(p?.email ?? "");
  const [pan, setPan] = useState(p?.pan ?? "");
  const [joined, setJoined] = useState(p?.joined_on ?? "");
  const [status, setStatus] = useState(p?.status ?? "active");
  const [left, setLeft] = useState(p?.left_on ?? "");
  const [notes, setNotes] = useState(p?.notes ?? "");
  const [busy, setBusy] = useState(false);

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    const body = { name, share_ratio: Number(share) || 0, phone, email, pan, joined_on: joined, notes, ...(p ? { status, left_on: left } : {}) };
    try {
      if (p) await api.patch(`/api/v1/partners/${p.partner_id}`, body);
      else await api.post("/api/v1/partners", body);
      toast.success(p ? "Partner saved" : `${name} added, with their own capital account`);
      onSaved();
      onClose();
    } catch (err) {
      toast.error(message(err, "Could not save the partner"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={p ? `Edit ${p.name}` : "Add partner"}
      description={p ? undefined : "A capital account is opened for them under Partners' Capital (3000)."}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="partner-form" disabled={busy || !name.trim()}>
            {busy && <Loader2 className="animate-spin" aria-hidden />} Save
          </Button>
        </>
      }
    >
      <form id="partner-form" onSubmit={save} className="grid gap-3 sm:grid-cols-2">
        <Field label="Name *" htmlFor="pt-name">
          <Input id="pt-name" autoFocus value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        <Field label="Profit share (%)" htmlFor="pt-share">
          <Input id="pt-share" type="number" min="0" max="100" step="0.01" value={share} onChange={(e) => setShare(e.target.value)} placeholder="e.g. 60" />
        </Field>
        <Field label="Phone" htmlFor="pt-phone">
          <Input id="pt-phone" value={phone} onChange={(e) => setPhone(e.target.value)} />
        </Field>
        <Field label="Email" htmlFor="pt-email">
          <Input id="pt-email" value={email} onChange={(e) => setEmail(e.target.value)} />
        </Field>
        <Field label="PAN" htmlFor="pt-pan">
          <Input id="pt-pan" value={pan} onChange={(e) => setPan(e.target.value.toUpperCase())} />
        </Field>
        <Field label="Joined on" htmlFor="pt-joined">
          <Input id="pt-joined" type="date" value={joined} onChange={(e) => setJoined(e.target.value)} />
        </Field>
        {p && (
          <>
            <Field label="Status" htmlFor="pt-status">
              <select id="pt-status" value={status} onChange={(e) => setStatus(e.target.value as Partner["status"])} className={`${selectClass} w-full`}>
                <option value="active">Active</option>
                <option value="retired">Retired</option>
              </select>
            </Field>
            <Field label="Left on" htmlFor="pt-left">
              <Input id="pt-left" type="date" value={left} onChange={(e) => setLeft(e.target.value)} />
            </Field>
          </>
        )}
        <Field label="Notes" htmlFor="pt-notes" className="sm:col-span-2">
          <Input id="pt-notes" value={notes} onChange={(e) => setNotes(e.target.value)} />
        </Field>
      </form>
    </Modal>
  );
}

function MoneyDialog({ partner, kind, onClose, onSaved }: { partner: SummaryRow; kind: "pay-in" | "pay-out"; onClose: () => void; onSaved: () => void }) {
  const { branches, defaultBranchId } = useMyBranches();
  const accounts = useBankAccounts();
  const [branchId, setBranchId] = useState("");
  const [amount, setAmount] = useState("");
  const [date, setDate] = useState(() => localDate());
  const [method, setMethod] = useState<"cash" | "bank_transfer" | "cheque">("bank_transfer");
  const [bankId, setBankId] = useState<string | null>(null);
  const [reference, setReference] = useState("");
  const [notes, setNotes] = useState("");
  const [busy, setBusy] = useState(false);
  const bank = bankId ?? defaultAccount(accounts)?.bank_account_id ?? "";
  const viaBank = method !== "cash" && accounts.length > 0;

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      await api.post(`/api/v1/partners/${partner.partner_id}/${kind}`, {
        amount: Number(amount),
        date,
        method,
        branch_id: branchId || defaultBranchId,
        reference,
        notes,
        ...(viaBank && bank ? { bank_account_id: bank } : {}),
      });
      toast.success(kind === "pay-in" ? `${rupees(amount)} paid in by ${partner.name}` : `${rupees(amount)} paid out to ${partner.name}`);
      onSaved();
      onClose();
    } catch (err) {
      toast.error(message(err, "Could not record it"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={kind === "pay-in" ? `${partner.name} pays in` : `Pay out to ${partner.name}`}
      description={
        kind === "pay-in" ? "Money the partner puts into the business; it adds to their capital." : "Money the partner takes out (drawings); it reduces their capital."
      }
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="pt-money-form" disabled={busy || !(Number(amount) > 0)}>
            {busy && <Loader2 className="animate-spin" aria-hidden />} {kind === "pay-in" ? "Record pay-in" : "Record pay-out"}
          </Button>
        </>
      }
    >
      <form id="pt-money-form" onSubmit={save} className="grid gap-3 sm:grid-cols-2">
        <Field label="Amount *" htmlFor="pm-amount">
          <Input id="pm-amount" type="number" min="0.01" step="0.01" autoFocus value={amount} onChange={(e) => setAmount(e.target.value)} />
        </Field>
        <Field label="Date" htmlFor="pm-date">
          <Input id="pm-date" type="date" max={localDate()} value={date} onChange={(e) => setDate(e.target.value)} />
        </Field>
        <Field label="How" htmlFor="pm-method">
          <select id="pm-method" value={method} onChange={(e) => setMethod(e.target.value as typeof method)} className={`${selectClass} w-full`}>
            <option value="bank_transfer">Bank transfer</option>
            <option value="cheque">Cheque</option>
            <option value="cash">Cash</option>
          </select>
        </Field>
        {viaBank ? (
          <Field label="Bank account" htmlFor="pm-bank">
            <BankAccountSelect id="pm-bank" accounts={accounts} value={bank} onChange={setBankId} allowNone className="w-full" />
          </Field>
        ) : (
          <span />
        )}
        <Field label="Branch (whose books)" htmlFor="pm-branch">
          <BranchSelect id="pm-branch" branches={branches} value={branchId || defaultBranchId} onChange={setBranchId} className="w-full" />
        </Field>
        <Field label="Reference" htmlFor="pm-ref">
          <Input id="pm-ref" value={reference} onChange={(e) => setReference(e.target.value)} placeholder="UTR / cheque no." />
        </Field>
        <Field label="Notes" htmlFor="pm-notes" className="sm:col-span-2">
          <Input id="pm-notes" value={notes} onChange={(e) => setNotes(e.target.value)} />
        </Field>
      </form>
    </Modal>
  );
}
