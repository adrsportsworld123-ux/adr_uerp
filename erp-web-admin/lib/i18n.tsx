"use client";

// The merchant's localization (erp-core-go migrations/064, Organization →
// Localization): the back office's language and text direction, and the
// business time zone dates are shown in. Same shape as lib/currency.tsx: a
// module-level setting read by t() and dateTime(), loaded once signed in,
// with the screens re-rendered only when it changes. Every string is
// written in English and looked up with t(), so English (the default)
// shows exactly the text in the code, and a missing translation falls back
// to English. The POS has its own copy of the same dictionaries' idea
// (erp-pos-flutter lib/l10n), with a terminal's own direction on top.
import { ReactNode, useEffect, useState } from "react";
import { api } from "@/lib/api-client";
import { ar } from "@/lib/locales/ar";
import { es } from "@/lib/locales/es";
import { fr } from "@/lib/locales/fr";
import { hi } from "@/lib/locales/hi";
import { kn } from "@/lib/locales/kn";
import { ml } from "@/lib/locales/ml";
import { ta } from "@/lib/locales/ta";
import { te } from "@/lib/locales/te";

export const LANGUAGES = ["en", "ta", "hi", "ar", "ml", "te", "kn", "fr", "es"] as const;

// Each language in its own script, then in English (a user's own choice
// lists them, migrations/065).
export const LANGUAGE_NAMES: Record<string, string> = {
  en: "English",
  ta: "தமிழ் — Tamil",
  hi: "हिन्दी — Hindi",
  ar: "العربية — Arabic",
  ml: "മലയാളം — Malayalam",
  te: "తెలుగు — Telugu",
  kn: "ಕನ್ನಡ — Kannada",
  fr: "Français — French",
  es: "Español — Spanish",
};
const DICTIONARIES: Record<string, Record<string, string>> = { ta, hi, ar, ml, te, kn, fr, es };
const CACHE_KEY = "erp.localization";

export type Localization = { language: string; direction: "ltr" | "rtl"; timezone: string };

const DEFAULT: Localization = { language: "en", direction: "ltr", timezone: "Asia/Kolkata" };
let current: Localization = DEFAULT;

/** `en` in the merchant's language; `{0}`, `{1}` … are replaced by `args`, in order. */
export function t(en: string, args: (string | number | null | undefined)[] = []): string {
  const text = DICTIONARIES[current.language]?.[en] ?? en;
  if (args.length === 0) return text;
  return text.replace(/\{(\d+)\}/g, (m, i) => (Number(i) < args.length ? String(args[Number(i)] ?? "") : m));
}

/** The merchant's business time zone (IANA name), for dateTime() and localDate(). */
export function merchantTimeZone(): string {
  return current.timezone;
}

export function currentLanguage(): string {
  return current.language;
}

/** Every string a language translates (the completeness check in e2e). */
export function dictionaryFor(language: string): Record<string, string> {
  return DICTIONARIES[language] ?? {};
}

function apply(l: Localization) {
  current = {
    language: (LANGUAGES as readonly string[]).includes(l.language) ? l.language : "en",
    direction: l.direction === "rtl" ? "rtl" : "ltr",
    timezone: l.timezone || DEFAULT.timezone,
  };
  if (typeof document !== "undefined") {
    document.documentElement.lang = current.language;
    document.documentElement.dir = current.direction;
  }
}

function key(l: Localization) {
  return `${l.language}|${l.direction}|${l.timezone}`;
}

function readCache(): Localization | null {
  try {
    const raw = window.localStorage.getItem(CACHE_KEY);
    return raw ? (JSON.parse(raw) as Localization) : null;
  } catch {
    return null;
  }
}

/** Applies a localization the settings screen just saved, without a reload. */
export function setLocalization(l: Localization) {
  apply(l);
  try {
    window.localStorage.setItem(CACHE_KEY, JSON.stringify(current));
  } catch {
    // per-browser convenience only
  }
  listeners.forEach((fn) => fn());
}

const listeners = new Set<() => void>();

/**
 * The current localization as a render key, following every change. On
 * mount it applies the last localization this browser saw, so the login
 * screen speaks the merchant's language too.
 */
export function useCachedLocalization(): string {
  const [k, setK] = useState(key(current));
  useEffect(() => {
    const onChange = () => setK(key(current));
    listeners.add(onChange);
    const cached = readCache();
    if (cached && key(cached) !== key(current)) setLocalization(cached);
    return () => {
      listeners.delete(onChange);
    };
  }, []);
  return k;
}

/** Loads the merchant's localization once signed in; children re-render with it. */
export function LocaleProvider({ children }: { children: ReactNode }) {
  const k = useCachedLocalization();
  useEffect(() => {
    api
      .get<{ effective: { language: string; direction: "ltr" | "rtl"; timezone: string } }>("/api/v1/settings/localization")
      .then((s) => setLocalization({ language: s.effective.language, direction: s.effective.direction, timezone: s.effective.timezone }))
      .catch(() => {}); // an older API: English, IST
  }, []);
  return (
    <div key={k} className="contents">
      {children}
    </div>
  );
}
