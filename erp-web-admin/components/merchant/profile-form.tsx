"use client";

// The company profile (erp-core-go migrations/065, internal/merchants):
// the same form on the merchant's own Company Profile page and in the SaaS
// console. The API validates (GSTIN, PAN, the GSTIN carrying the PAN), so
// a refusal is shown as the API's message.
import { FormEvent, ReactNode, useState } from "react";
import { Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { selectClass } from "@/components/data/panel";
import { GST_STATES } from "@/lib/gst-states";

export type MerchantAddress = {
  line1: string;
  line2: string;
  city: string;
  state: string;
  state_code: string;
  pincode: string;
  country: string;
};

export type MerchantProfile = {
  merchant_id: string;
  code: string;
  legal_name: string;
  trade_name: string;
  gstin: string;
  pan: string;
  email: string;
  phone: string;
  website: string;
  address: MerchantAddress;
  status: string;
  status_reason: string;
  status_changed_at: string | null;
  created_at: string;
};

type Fields = Omit<MerchantProfile, "merchant_id" | "code" | "status" | "status_reason" | "status_changed_at" | "created_at">;

const emptyAddress: MerchantAddress = { line1: "", line2: "", city: "", state: "", state_code: "", pincode: "", country: "India" };

function fieldsOf(p: MerchantProfile): Fields {
  return {
    legal_name: p.legal_name,
    trade_name: p.trade_name,
    gstin: p.gstin,
    pan: p.pan,
    email: p.email,
    phone: p.phone,
    website: p.website,
    address: { ...emptyAddress, ...p.address, country: p.address?.country || "India" },
  };
}

export function ProfileForm({
  profile,
  disabled,
  onSave,
  extra,
  saveLabel = "Save",
}: {
  profile: MerchantProfile;
  disabled?: boolean;
  onSave: (f: Fields) => Promise<void>;
  // Shown above the save button (the console's audited reason).
  extra?: ReactNode;
  saveLabel?: string;
}) {
  const [f, setF] = useState<Fields>(() => fieldsOf(profile));
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const set = (k: keyof Fields, v: string) => setF({ ...f, [k]: v });
  const setAddr = (k: keyof MerchantAddress, v: string) => setF({ ...f, address: { ...f.address, [k]: v } });
  const dirty = JSON.stringify(f) !== JSON.stringify(fieldsOf(profile));

  async function submit(e: FormEvent) {
    e.preventDefault();
    setSaving(true);
    setError(null);
    try {
      await onSave({ ...f, address: { ...f.address, state: GST_STATES.find(([c]) => c === f.address.state_code)?.[1] ?? f.address.state } });
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not save");
    } finally {
      setSaving(false);
    }
  }

  const text = (id: string, label: string, k: keyof Fields, props: React.ComponentProps<typeof Input> = {}) => (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      <Input id={id} value={f[k] as string} onChange={(e) => set(k, e.target.value)} {...props} />
    </div>
  );

  return (
    <form onSubmit={submit} className="space-y-5">
      <fieldset disabled={disabled || saving} className="space-y-5">
        <div className="grid gap-4 sm:grid-cols-2">
          {text("mp-legal", "Legal name", "legal_name", { required: true, maxLength: 200 })}
          {text("mp-trade", "Trade name (on receipts)", "trade_name", { maxLength: 200 })}
          {text("mp-gstin", "GSTIN", "gstin", { maxLength: 15, className: "uppercase", placeholder: "29AABCA1234F1Z5" })}
          {text("mp-pan", "PAN", "pan", { maxLength: 10, className: "uppercase", placeholder: "AABCA1234F" })}
          {text("mp-email", "E-mail", "email", { type: "email" })}
          {text("mp-phone", "Phone", "phone", { type: "tel" })}
          {text("mp-web", "Website", "website")}
        </div>
        <fieldset className="space-y-3">
          <legend className="text-sm font-medium">Registered address</legend>
          <p className="text-xs text-muted-foreground">Printed on invoices and receipts of any branch whose print settings give no address of its own.</p>
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="flex flex-col gap-1.5 sm:col-span-2">
              <Label htmlFor="mp-line1">Address line 1</Label>
              <Input id="mp-line1" value={f.address.line1} onChange={(e) => setAddr("line1", e.target.value)} maxLength={200} />
            </div>
            <div className="flex flex-col gap-1.5 sm:col-span-2">
              <Label htmlFor="mp-line2">Address line 2</Label>
              <Input id="mp-line2" value={f.address.line2} onChange={(e) => setAddr("line2", e.target.value)} maxLength={200} />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="mp-city">City</Label>
              <Input id="mp-city" value={f.address.city} onChange={(e) => setAddr("city", e.target.value)} maxLength={100} />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="mp-state">State</Label>
              <select id="mp-state" value={f.address.state_code} onChange={(e) => setAddr("state_code", e.target.value)} className={selectClass}>
                <option value="">—</option>
                {GST_STATES.map(([c, n]) => (
                  <option key={c} value={c}>
                    {c} – {n}
                  </option>
                ))}
              </select>
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="mp-pin">PIN code</Label>
              <Input id="mp-pin" value={f.address.pincode} onChange={(e) => setAddr("pincode", e.target.value)} maxLength={10} />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="mp-country">Country</Label>
              <Input id="mp-country" value={f.address.country} onChange={(e) => setAddr("country", e.target.value)} maxLength={60} />
            </div>
          </div>
        </fieldset>
        {extra}
      </fieldset>
      {error && (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      )}
      <div className="flex justify-end">
        <Button type="submit" disabled={disabled || saving || !dirty}>
          {saving && <Loader2 className="animate-spin" aria-hidden />}
          {saveLabel}
        </Button>
      </div>
    </form>
  );
}
