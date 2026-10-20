"use client";

import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  DEFAULT_MERCHANT_CODE,
  takePostLoginRedirect,
  takeSessionExpiredReason,
} from "@/lib/api-client";
import { useAuth } from "@/lib/auth";
import { t, useCachedLocalization } from "@/lib/i18n";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";

// Closes the gap phased_roadmap.md's Phase 0 Definition of Done originally
// asked for ("Flutter app and Next.js admin each showing a login screen
// that successfully calls the real auth service") but was never actually
// built — see docs/phased_roadmap.md and phase0_1_design.md's notes on
// this. Talks to the same POST /auth/login erp-pos-flutter's login screen
// uses.
export default function LoginPage() {
  const { userId, ready, login } = useAuth();
  // The language this browser last signed in with (migrations/064); the
  // merchant's own is loaded after sign-in.
  const localeKey = useCachedLocalization();
  const router = useRouter();

  const [merchantCode, setMerchantCode] = useState(DEFAULT_MERCHANT_CODE);
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const [redirectTo, setRedirectTo] = useState("/dashboard");

  // Set by api-client.ts's expireSession() just before the dashboard guard
  // sent the user here: why their session ended, and where they were, so
  // signing back in returns them to the same screen. sessionStorage is
  // client-only, so this is read after mount (same hydration reasoning as
  // lib/auth.tsx's session-restore effect) and consumed exactly once.
  useEffect(() => {
    const reason = takeSessionExpiredReason();
    const back = takePostLoginRedirect();
    if (reason)
      // eslint-disable-next-line react-hooks/set-state-in-effect -- sessionStorage is client-only; see comment above
      setNotice(
        reason === "idle"
          ? "Your session expired due to inactivity. Please sign in again."
          : "Your session has ended. Please sign in again.",
      );
    if (back) setRedirectTo(back);
  }, []);

  useEffect(() => {
    if (ready && userId) router.replace(redirectTo);
  }, [ready, userId, router, redirectTo]);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    const err = await login(merchantCode, email, password);
    setSubmitting(false);
    if (err) {
      setError(err);
    } else {
      router.replace(redirectTo);
    }
  }

  return (
    <div key={localeKey} className="flex flex-1 items-center justify-center p-6">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>{t("ERP Admin")}</CardTitle>
          <CardDescription>
            {t("Back-office login for purchase, accounting, and reporting")}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleSubmit} className="flex flex-col gap-4">
            {notice && (
              <p
                role="status"
                data-testid="session-expired-notice"
                className="rounded border border-warn/40 bg-warn-soft px-3 py-2 text-sm text-warn"
              >
                {t(notice)}
              </p>
            )}
            <div className="flex flex-col gap-2">
              <Label htmlFor="merchantCode">{t("Merchant code")}</Label>
              <Input
                id="merchantCode"
                value={merchantCode}
                onChange={(e) => setMerchantCode(e.target.value)}
                required
              />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="email">{t("Email")}</Label>
              <Input
                id="email"
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                required
              />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="password">{t("Password")}</Label>
              <Input
                id="password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
              />
            </div>
            {error && <p className="text-sm text-destructive">{error}</p>}
            <Button type="submit" disabled={submitting}>
              {submitting ? t("Signing in...") : t("Sign in")}
            </Button>
            <Link
              href="/forgot-password"
              className="text-sm text-muted-foreground text-center hover:underline"
            >
              {t("Forgot password?")}
            </Link>
          </form>
        </CardContent>
      </Card>
    </div>
  );
}
