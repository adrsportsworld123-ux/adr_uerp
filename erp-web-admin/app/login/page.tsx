"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useAuth } from "@/lib/auth";
import { takePostLoginRedirect, takeSessionExpiredReason } from "@/lib/api-client";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";

// Closes the gap phased_roadmap.md's Phase 0 Definition of Done originally
// asked for ("Flutter app and Next.js admin each showing a login screen
// that successfully calls the real auth service") but was never actually
// built — see docs/phased_roadmap.md and phase0_1_design.md's notes on
// this. Talks to the same POST /auth/login erp-pos-flutter's login screen
// uses.
export default function LoginPage() {
  const { userId, ready, login } = useAuth();
  const router = useRouter();

  const [merchantCode, setMerchantCode] = useState("acme-sports");
  const [email, setEmail] = useState("ravi@acme-sports.test");
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
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (reason) setNotice(reason === "idle" ? "Your session expired due to inactivity. Please sign in again." : "Your session has ended. Please sign in again.");
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
    <div className="flex flex-1 items-center justify-center p-6">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>ERP Admin</CardTitle>
          <CardDescription>Back-office login for purchase, accounting, and reporting</CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleSubmit} className="flex flex-col gap-4">
            {notice && (
              <p role="status" data-testid="session-expired-notice" className="rounded border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-800">
                {notice}
              </p>
            )}
            <div className="flex flex-col gap-2">
              <Label htmlFor="merchantCode">Merchant code</Label>
              <Input id="merchantCode" value={merchantCode} onChange={(e) => setMerchantCode(e.target.value)} required />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="email">Email</Label>
              <Input id="email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} required />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="password">Password</Label>
              <Input id="password" type="password" value={password} onChange={(e) => setPassword(e.target.value)} required />
            </div>
            {error && <p className="text-sm text-red-600">{error}</p>}
            <Button type="submit" disabled={submitting}>
              {submitting ? "Signing in..." : "Sign in"}
            </Button>
            <Link href="/forgot-password" className="text-sm text-zinc-500 text-center hover:underline">
              Forgot password?
            </Link>
          </form>
        </CardContent>
      </Card>
    </div>
  );
}
