"use client";

// Localization (erp-core-go migrations/064, internal/locale): the
// merchant's language for the back office and every POS, the business time
// zone reports and day closes run on, the text direction and the POS cart
// side (a POS terminal can override both, Organization → POS Terminals),
// and the cash denominations counted at a shift close and in Cash
// Reconciliation. The currency itself is Tax Settings'; the denominations
// default to its standard notes and coins.
import { useEffect, useState } from "react";
import Link from "next/link";
import { Loader2 } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import { setLocalization, t } from "@/lib/i18n";
import { rupees } from "@/lib/format";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { PageHeader } from "@/components/data/page-header";
import { selectClass } from "@/components/data/panel";

type LocalizationSettings = {
  language: string;
  timezone: string;
  text_direction: "auto" | "ltr" | "rtl";
  cart_side: "left" | "right";
  denominations: string[] | null;
  effective: {
    language: string;
    direction: "ltr" | "rtl";
    timezone: string;
    utc_offset_minutes: number;
    currency_code: string;
    currency_symbol: string;
    denominations: string[];
  };
  languages: { code: string; name: string; native_name: string; direction: string }[];
  timezones: string[];
};

function offsetLabel(minutes: number): string {
  const sign = minutes < 0 ? "−" : "+";
  const m = Math.abs(minutes);
  return `UTC${sign}${String(Math.floor(m / 60)).padStart(2, "0")}:${String(m % 60).padStart(2, "0")}`;
}

/** "2000, 500, 0.25" -> [2000, 500, 0.25]; null when anything isn't a positive amount. */
function parseDenominations(text: string): number[] | null {
  const parts = text
    .split(/[,\s]+/)
    .map((p) => p.trim())
    .filter(Boolean);
  if (parts.length === 0 || parts.length > 20) return null;
  const values = parts.map(Number);
  return values.every((v) => Number.isFinite(v) && v > 0) ? values : null;
}

export default function LocalizationPage() {
  const { can } = usePermissions();
  const allowed = can("localization.manage");
  const [data, setData] = useState<LocalizationSettings | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api
      .get<LocalizationSettings>("/api/v1/settings/localization")
      .then(setData)
      .catch((e) => setError(e instanceof ApiError ? e.message : t("Could not load the localization settings")));
  }, []);

  return (
    <div className="max-w-2xl space-y-6">
      <PageHeader
        title={t("Localization")}
        description={t("Language, time zone, text direction and cash denominations for the back office and every POS terminal.")}
      />
      {error && <p className="text-sm text-destructive">{error}</p>}
      {data == null && !error ? (
        <p className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="size-4 animate-spin" aria-hidden /> {t("Loading…")}
        </p>
      ) : data != null ? (
        <SettingsForm key={JSON.stringify([data.language, data.timezone, data.text_direction, data.cart_side, data.denominations])} data={data} allowed={allowed} onSaved={setData} />
      ) : null}
      {!allowed && data && <p className="text-sm text-muted-foreground">{missingPermissionHint("localization.manage")}</p>}
    </div>
  );
}

function SettingsForm({ data, allowed, onSaved }: { data: LocalizationSettings; allowed: boolean; onSaved: (d: LocalizationSettings) => void }) {
  const [language, setLanguage] = useState(data.language);
  const [timezone, setTimezone] = useState(data.timezone);
  const [applyToBranches, setApplyToBranches] = useState(false);
  const [direction, setDirection] = useState(data.text_direction);
  const [cartSide, setCartSide] = useState(data.cart_side);
  const [ownDenominations, setOwnDenominations] = useState(data.denominations != null);
  const [denominationText, setDenominationText] = useState((data.denominations ?? data.effective.denominations).map((d) => String(Number(d))).join(", "));
  const [saving, setSaving] = useState(false);

  const zones = data.timezones.includes(data.timezone) ? data.timezones : [data.timezone, ...data.timezones];
  const parsed = ownDenominations ? parseDenominations(denominationText) : null;
  const denominationsInvalid = ownDenominations && parsed == null;
  const dirty =
    language !== data.language ||
    timezone !== data.timezone ||
    applyToBranches ||
    direction !== data.text_direction ||
    cartSide !== data.cart_side ||
    ownDenominations !== (data.denominations != null) ||
    (ownDenominations && JSON.stringify(parsed) !== JSON.stringify((data.denominations ?? []).map(Number)));

  async function save() {
    setSaving(true);
    try {
      const d = await api.put<LocalizationSettings>("/api/v1/settings/localization", {
        language,
        timezone,
        text_direction: direction,
        cart_side: cartSide,
        denominations: ownDenominations ? parsed : null,
        apply_timezone_to_branches: applyToBranches,
      });
      // The back office speaks the new language from now on.
      setLocalization({ language: d.effective.language, direction: d.effective.direction, timezone: d.effective.timezone });
      onSaved(d);
      toast.success(t("Localization saved"));
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("Could not save the localization settings"));
    } finally {
      setSaving(false);
    }
  }

  return (
    <fieldset disabled={!allowed || saving} className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>{t("Language")}</CardTitle>
          <CardDescription>{t("The back office and every POS terminal show their screens in this language.")}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="loc-language">{t("Language")}</Label>
            <select id="loc-language" value={language} onChange={(e) => setLanguage(e.target.value)} className={`${selectClass} w-72`}>
              {data.languages.map((l) => (
                <option key={l.code} value={l.code}>
                  {l.native_name === l.name ? l.name : `${l.native_name} — ${l.name}`}
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="loc-direction">{t("Text direction")}</Label>
            <select id="loc-direction" value={direction} onChange={(e) => setDirection(e.target.value as LocalizationSettings["text_direction"])} className={`${selectClass} w-72`}>
              <option value="auto">{t("Automatic (right-to-left for Arabic)")}</option>
              <option value="ltr">{t("Left to right")}</option>
              <option value="rtl">{t("Right to left")}</option>
            </select>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{t("Time zone")}</CardTitle>
          <CardDescription>{t("The business day: dashboards, reports, day closes and dates on documents follow this clock.")}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="loc-timezone">{t("Time zone")}</Label>
            <select id="loc-timezone" value={timezone} onChange={(e) => setTimezone(e.target.value)} className={`${selectClass} w-72`}>
              {zones.map((z) => (
                <option key={z} value={z}>
                  {z}
                </option>
              ))}
            </select>
            <p className="text-xs text-muted-foreground">{t("Now {0}", [offsetLabel(data.effective.utc_offset_minutes)])}</p>
          </div>
          <label className="flex items-start gap-3 text-sm">
            <input type="checkbox" className="mt-1" checked={applyToBranches} onChange={(e) => setApplyToBranches(e.target.checked)} />
            <span>
              <span className="font-medium">{t("Also set every branch to this time zone")}</span>
              <span className="block text-muted-foreground">{t("A branch's own time zone prints the times on its receipts. New branches start on the merchant's.")}</span>
            </span>
          </label>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{t("POS layout")}</CardTitle>
          <CardDescription>{t("Where the cart sits on a wide POS screen. A terminal can override this and the text direction.")}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-2">
          {(["right", "left"] as const).map((side) => (
            <label key={side} className="flex items-center gap-3 text-sm">
              <input type="radio" name="cart-side" checked={cartSide === side} onChange={() => setCartSide(side)} />
              {side === "right" ? t("Cart on the right") : t("Cart on the left")}
            </label>
          ))}
          <p className="text-xs text-muted-foreground">
            <Link href="/terminals" className="underline">
              {t("POS Terminals")}
            </Link>
          </p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{t("Currency and denominations")}</CardTitle>
          <CardDescription>
            {t("Money is shown in {0} ({1}), set in Tax Settings. These are the notes and coins counted at a shift close and in Cash Reconciliation.", [
              data.effective.currency_code,
              data.effective.currency_symbol,
            ])}{" "}
            <Link href="/tax-settings" className="underline">
              {t("Tax Settings")}
            </Link>
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <label className="flex items-center gap-3 text-sm">
            <input type="radio" name="denoms" checked={!ownDenominations} onChange={() => setOwnDenominations(false)} />
            {t("The currency's standard notes and coins")}
          </label>
          <label className="flex items-center gap-3 text-sm">
            <input type="radio" name="denoms" checked={ownDenominations} onChange={() => setOwnDenominations(true)} />
            {t("Our own list")}
          </label>
          {ownDenominations ? (
            <div className="space-y-1.5">
              <Label htmlFor="loc-denominations">{t("Denominations")}</Label>
              <Input id="loc-denominations" value={denominationText} aria-invalid={denominationsInvalid} onChange={(e) => setDenominationText(e.target.value)} placeholder="2000, 500, 200, 100" />
              <p className={denominationsInvalid ? "text-xs text-destructive" : "text-xs text-muted-foreground"}>
                {denominationsInvalid ? t("Enter 1 to 20 positive amounts, separated by commas") : t("Separated by commas; they are sorted largest first")}
              </p>
            </div>
          ) : (
            <p className="text-sm text-muted-foreground" data-testid="standard-denominations">
              {data.effective.denominations.map((d) => rupees(d)).join(" · ")}
            </p>
          )}
        </CardContent>
      </Card>

      <div className="flex justify-end">
        <Button onClick={save} disabled={!allowed || !dirty || denominationsInvalid || saving}>
          {saving && <Loader2 className="animate-spin" aria-hidden />}
          {t("Save")}
        </Button>
      </div>
    </fieldset>
  );
}
