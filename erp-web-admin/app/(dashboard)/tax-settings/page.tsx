"use client";

// Tax settings (erp-core-go migrations/060, internal/taxes): how tax is
// split and labelled on every bill, invoice, receipt and report.
//   * GST (India, the default): CGST + SGST for a supply within the
//     branch's state, IGST for one to another state (decided from the
//     customer's or supplier's GSTIN).
//   * Single tax: one line under the merchant's own label (VAT, Sales Tax)
//     for a merchant outside India.
// The merchant sets the default; any branch can override the regime and
// label, and each branch can name the GST state it supplies from (else
// its GSTIN, then the merchant's, decides). The currency (migrations/061)
// names the money on documents, the POS and here; a branch may use its own.
import { useEffect, useState } from "react";
import { Loader2 } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import type { TaxRegime, TaxSettings } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { PageHeader } from "@/components/data/page-header";
import { selectClass } from "@/components/data/panel";
import { setCurrency as applyCurrency } from "@/lib/currency";
import { StatusChip } from "@/components/data/status-chip";

type BranchRow = TaxSettings["branches"][number];

export default function TaxSettingsPage() {
  const { can } = usePermissions();
  const allowed = can("tax.settings");
  const [data, setData] = useState<TaxSettings | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api
      .get<TaxSettings>("/api/v1/tax/settings")
      .then(setData)
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load the tax settings"));
  }, []);

  return (
    <div className="space-y-6">
      <PageHeader
        title="Tax Settings"
        description="How tax is split and named on bills, invoices, receipts and reports. Set the merchant default, then override any branch that differs."
      />
      {error && <p className="text-sm text-destructive">{error}</p>}
      {data == null && !error ? (
        <p className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="size-4 animate-spin" aria-hidden /> Loading…
        </p>
      ) : data != null ? (
        <>
          <MerchantCard key={`m-${data.merchant.currency_code}-${data.merchant.regime}`} data={data} allowed={allowed} onSaved={setData} />
          <Card className="max-w-4xl">
            <CardHeader>
              <CardTitle>Branches</CardTitle>
              <CardDescription>
                A branch follows the merchant default unless you override it. Its GST state decides CGST + SGST or IGST: when it isn&apos;t
                set here, the branch GSTIN is used, then the merchant&apos;s.
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              {data.branches.map((b) => (
                <BranchCard key={b.branch_id} branch={b} states={data.states} currencies={currencyList(data)} allowed={allowed} onSaved={setData} />
              ))}
              {data.branches.length === 0 && <p className="text-sm text-muted-foreground">No branches yet.</p>}
            </CardContent>
          </Card>
          {!allowed && <p className="text-sm text-muted-foreground">{missingPermissionHint("tax.settings")}</p>}
        </>
      ) : null}
    </div>
  );
}

function RegimeChoice({ name, value, onChange, inherit }: { name: string; value: TaxRegime | null; onChange: (v: TaxRegime | null) => void; inherit?: string }) {
  return (
    <div className="space-y-2">
      {inherit != null && (
        <label className="flex items-start gap-3 text-sm">
          <input type="radio" name={name} className="mt-1" checked={value == null} onChange={() => onChange(null)} />
          <span>
            <span className="font-medium">Same as the merchant</span>
            <span className="block text-muted-foreground">{inherit}</span>
          </span>
        </label>
      )}
      <label className="flex items-start gap-3 text-sm">
        <input type="radio" name={name} className="mt-1" checked={value === "gst_split"} onChange={() => onChange("gst_split")} />
        <span>
          <span className="font-medium">GST (India)</span> — CGST and SGST shown separately
          <span className="block text-muted-foreground">
            Within the branch&apos;s state each line and total shows CGST + SGST (half the rate each); to a customer or from a supplier in
            another state, IGST. Invoices carry the place of supply and a rate-wise tax summary.
          </span>
        </span>
      </label>
      <label className="flex items-start gap-3 text-sm">
        <input type="radio" name={name} className="mt-1" checked={value === "single"} onChange={() => onChange("single")} />
        <span>
          <span className="font-medium">Single tax</span>
          <span className="block text-muted-foreground">One tax line under your own label, such as VAT or Sales Tax — for a business outside India.</span>
        </span>
      </label>
    </div>
  );
}

function CurrencySelect({ id, value, onChange, currencies, inherit }: { id: string; value: string; onChange: (code: string) => void; currencies: TaxSettings["currencies"]; inherit?: string }) {
  return (
    <select id={id} value={value} onChange={(e) => onChange(e.target.value)} className={`${selectClass} w-72`}>
      {inherit != null && <option value="">{inherit}</option>}
      {currencies.map((c) => (
        <option key={c.code} value={c.code}>
          {c.code} ({c.symbol})
        </option>
      ))}
    </select>
  );
}

/** The settings' currency list, with any code already saved that isn't in it. */
function currencyList(data: TaxSettings) {
  const list = [...(data.currencies ?? [{ code: "INR", symbol: "₹" }])];
  const add = (code: string | null | undefined, symbol: string | null | undefined) => {
    if (code && symbol && !list.some((c) => c.code === code)) list.push({ code, symbol });
  };
  add(data.merchant.currency_code, data.merchant.currency_symbol);
  data.branches.forEach((b) => add(b.currency_code, b.currency_symbol));
  return list;
}

function StateSelect({ id, value, onChange, states, placeholder }: { id: string; value: string; onChange: (v: string) => void; states: TaxSettings["states"]; placeholder: string }) {
  return (
    <select id={id} value={value} onChange={(e) => onChange(e.target.value)} className={`${selectClass} w-72`}>
      <option value="">{placeholder}</option>
      {states.map((s) => (
        <option key={s.code} value={s.code}>
          {s.code} – {s.name}
        </option>
      ))}
    </select>
  );
}

const labelInvalid = (v: string) => v.trim().length > 20;

function MerchantCard({ data, allowed, onSaved }: { data: TaxSettings; allowed: boolean; onSaved: (d: TaxSettings) => void }) {
  const m = data.merchant;
  const [regime, setRegime] = useState<TaxRegime>(m.regime);
  const [label, setLabel] = useState(m.tax_label);
  const [state, setState] = useState(m.state_code ?? "");
  const currencies = currencyList(data);
  const [currency, setCurrency] = useState(m.currency_code ?? "INR");
  const [saving, setSaving] = useState(false);
  const dirty = regime !== m.regime || label.trim() !== m.tax_label || state !== (m.state_code ?? "") || currency !== (m.currency_code ?? "INR");
  const invalid = labelInvalid(label) || (regime === "single" && !label.trim());

  async function save() {
    setSaving(true);
    try {
      const c = currencies.find((x) => x.code === currency);
      const d = await api.put<TaxSettings>("/api/v1/tax/settings", {
        regime,
        tax_label: label.trim() || "GST",
        state_code: state || null,
        currency_code: c?.code ?? null,
        currency_symbol: c?.symbol ?? null,
      });
      onSaved(d);
      applyCurrency({ code: d.merchant.currency_code, symbol: d.merchant.currency_symbol }); // the back office shows it from now on
      toast.success("Tax settings saved");
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not save the tax settings");
    } finally {
      setSaving(false);
    }
  }

  return (
    <Card className="max-w-4xl">
      <CardHeader>
        <CardTitle>Merchant default</CardTitle>
        <CardDescription>Applies to every branch that doesn&apos;t override it.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-5">
        <fieldset className="space-y-3" disabled={!allowed}>
          <legend className="text-sm font-medium">Tax on bills and invoices</legend>
          <RegimeChoice name="merchant-regime" value={regime} onChange={(v) => setRegime(v ?? "gst_split")} />
        </fieldset>
        <fieldset className="grid gap-4 sm:grid-cols-2" disabled={!allowed}>
          <div className="space-y-1.5">
            <Label htmlFor="merchant-tax-label">Tax name</Label>
            <Input id="merchant-tax-label" value={label} maxLength={20} aria-invalid={invalid} onChange={(e) => setLabel(e.target.value)} placeholder="GST" />
            <p className={invalid ? "text-xs text-destructive" : "text-xs text-muted-foreground"}>
              {invalid ? "Enter a name of up to 20 characters" : regime === "single" ? "Printed as the tax line, e.g. VAT" : "Used in summaries (\"GST summary\")"}
            </p>
          </div>
          {regime === "gst_split" && (
            <div className="space-y-1.5">
              <Label htmlFor="merchant-state">Home GST state</Label>
              <StateSelect id="merchant-state" value={state} onChange={setState} states={data.states} placeholder="From the merchant GSTIN" />
              <p className="text-xs text-muted-foreground">Used for a branch with no state or GSTIN of its own.</p>
            </div>
          )}
          <div className="space-y-1.5">
            <Label htmlFor="merchant-currency">Currency</Label>
            <CurrencySelect id="merchant-currency" value={currency} onChange={setCurrency} currencies={currencies} />
            <p className="text-xs text-muted-foreground">Names the money on bills, receipts, the POS and these screens. Amounts aren&apos;t converted.</p>
          </div>
        </fieldset>
        <Button onClick={save} disabled={!allowed || !dirty || invalid || saving} title={allowed ? undefined : missingPermissionHint("tax.settings")}>
          {saving && <Loader2 className="size-4 animate-spin" aria-hidden />}
          Save default
        </Button>
      </CardContent>
    </Card>
  );
}

function BranchCard({
  branch,
  states,
  currencies,
  allowed,
  onSaved,
}: {
  branch: BranchRow;
  states: TaxSettings["states"];
  currencies: TaxSettings["currencies"];
  allowed: boolean;
  onSaved: (d: TaxSettings) => void;
}) {
  const [regime, setRegime] = useState<TaxRegime | null>(branch.regime);
  const [label, setLabel] = useState(branch.tax_label ?? "");
  const [state, setState] = useState(branch.state_code ?? "");
  const [currency, setCurrency] = useState(branch.currency_code ?? "");
  const [saving, setSaving] = useState(false);
  const dirty =
    regime !== branch.regime || (label.trim() || null) !== branch.tax_label || (state || null) !== branch.state_code || (currency || null) !== branch.currency_code;
  const invalid = labelInvalid(label);
  const eff = branch.effective;
  const gstinState = /^\d{2}/.test(branch.branch_gstin) ? branch.branch_gstin.slice(0, 2) : "";
  const stateName = (code: string) => states.find((s) => s.code === code)?.name ?? code;
  const showState = (regime ?? eff.regime) === "gst_split";

  async function save() {
    setSaving(true);
    try {
      const d = await api.put<TaxSettings>(`/api/v1/tax/settings/branches/${branch.branch_id}`, {
        regime,
        tax_label: label.trim() || null,
        state_code: state || null,
        currency_code: currency || null,
        currency_symbol: currencies.find((c) => c.code === currency)?.symbol ?? null,
      });
      onSaved(d);
      toast.success(`${branch.branch_name} saved`);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not save the branch");
    } finally {
      setSaving(false);
    }
  }

  return (
    <section className="space-y-4 rounded-lg border p-4" aria-label={`Tax for ${branch.branch_name}`}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h3 className="font-medium">{branch.branch_name}</h3>
          <p className="text-xs text-muted-foreground">{branch.branch_gstin ? `GSTIN ${branch.branch_gstin}` : "No GSTIN"}</p>
        </div>
        <div className="flex flex-wrap items-center gap-2 text-sm" data-testid="branch-effective">
          <StatusChip status={eff.regime} tone={eff.regime === "gst_split" ? "info" : "teal"} label={eff.regime === "gst_split" ? "CGST + SGST / IGST" : eff.tax_label} />
          <StatusChip status="currency" tone="muted" label={`${eff.currency_code} ${eff.currency_symbol}`} />
          {eff.regime === "gst_split" && (
            <span className="text-muted-foreground">{eff.state_code ? `${eff.state_code} – ${stateName(eff.state_code)}` : "State not known: every sale treated as intra-state"}</span>
          )}
        </div>
      </div>
      <fieldset className="space-y-3" disabled={!allowed}>
        <legend className="sr-only">Tax regime for {branch.branch_name}</legend>
        <RegimeChoice name={`regime-${branch.branch_id}`} value={regime} onChange={setRegime} inherit="Uses the merchant default above." />
      </fieldset>
      <fieldset className="grid gap-4 sm:grid-cols-2" disabled={!allowed}>
        <div className="space-y-1.5">
          <Label htmlFor={`label-${branch.branch_id}`}>Tax name</Label>
          <Input id={`label-${branch.branch_id}`} value={label} maxLength={20} aria-invalid={invalid} onChange={(e) => setLabel(e.target.value)} placeholder="Same as the merchant" />
        </div>
        {showState && (
          <div className="space-y-1.5">
            <Label htmlFor={`state-${branch.branch_id}`}>GST state</Label>
            <StateSelect
              id={`state-${branch.branch_id}`}
              value={state}
              onChange={setState}
              states={states}
              placeholder={gstinState ? `From the GSTIN (${gstinState} – ${stateName(gstinState)})` : "From the merchant"}
            />
          </div>
        )}
        <div className="space-y-1.5">
          <Label htmlFor={`currency-${branch.branch_id}`}>Currency</Label>
          <CurrencySelect
            id={`currency-${branch.branch_id}`}
            value={currency}
            onChange={setCurrency}
            currencies={currencies}
            inherit={`Same as the merchant`}
          />
        </div>
      </fieldset>
      <Button size="sm" variant="outline" onClick={save} disabled={!allowed || !dirty || invalid || saving}>
        {saving && <Loader2 className="size-4 animate-spin" aria-hidden />}
        Save {branch.branch_name}
      </Button>
    </section>
  );
}
