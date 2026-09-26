"use client";

// Phase 1's last named-but-never-built roadmap item: "Password-reuse
// history, a real forgot-password flow (superseding the dev-only
// /dev/set-password tool)" — closed 2026-09-22 in an explicit "review
// previous phases for anything missing" audit. Talks to
// POST /auth/forgot-password / POST /auth/reset-password
// (erp-core-go's internal/authn/password_reset.go), both unauthenticated
// by design (proving receipt of the emailed code *is* the
// authentication).
import { useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { api, ApiError } from "@/lib/api-client";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";

export default function ForgotPasswordPage() {
  const router = useRouter();
  const [merchantCode, setMerchantCode] = useState("acme-sports");
  const [email, setEmail] = useState("");
  const [requested, setRequested] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const [code, setCode] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [done, setDone] = useState(false);

  async function requestCode(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api.post("/api/v1/auth/forgot-password", { merchant_code: merchantCode, email });
      // Always shows the same "requested" state regardless of whether the
      // email actually matched an account — the backend's own response is
      // deliberately identical either way (no account enumeration), and
      // this screen mirrors that rather than distinguishing success from
      // "nothing happened."
      setRequested(true);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not reach the server");
    } finally {
      setBusy(false);
    }
  }

  async function submitReset(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api.post("/api/v1/auth/reset-password", { token: code.trim(), new_password: newPassword });
      setDone(true);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not reset password");
    } finally {
      setBusy(false);
    }
  }

  if (done) {
    return (
      <div className="flex flex-1 items-center justify-center p-6">
        <Card className="w-full max-w-sm">
          <CardHeader>
            <CardTitle>Password reset</CardTitle>
            <CardDescription>Your password has been changed. Every other signed-in session was signed out.</CardDescription>
          </CardHeader>
          <CardContent>
            <Button className="w-full" onClick={() => router.replace("/login")}>
              Go to login
            </Button>
          </CardContent>
        </Card>
      </div>
    );
  }

  return (
    <div className="flex flex-1 items-center justify-center p-6">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>Forgot password</CardTitle>
          <CardDescription>
            {requested
              ? "Enter the code we emailed you (expires in 1 hour) and a new password."
              : "We'll email a one-time reset code if the account exists."}
          </CardDescription>
        </CardHeader>
        <CardContent>
          {!requested ? (
            <form onSubmit={requestCode} className="flex flex-col gap-4">
              <div className="flex flex-col gap-2">
                <Label htmlFor="merchantCode">Merchant code</Label>
                <Input id="merchantCode" value={merchantCode} onChange={(e) => setMerchantCode(e.target.value)} required />
              </div>
              <div className="flex flex-col gap-2">
                <Label htmlFor="email">Email</Label>
                <Input id="email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} required />
              </div>
              {error && <p className="text-sm text-red-600">{error}</p>}
              <Button type="submit" disabled={busy}>
                {busy ? "Sending..." : "Send reset code"}
              </Button>
              <Link href="/login" className="text-sm text-zinc-500 text-center hover:underline">
                Back to login
              </Link>
            </form>
          ) : (
            <form onSubmit={submitReset} className="flex flex-col gap-4">
              <div className="flex flex-col gap-2">
                <Label htmlFor="code">Reset code</Label>
                <Input id="code" value={code} onChange={(e) => setCode(e.target.value)} required />
              </div>
              <div className="flex flex-col gap-2">
                <Label htmlFor="newPassword">New password</Label>
                <Input id="newPassword" type="password" value={newPassword} onChange={(e) => setNewPassword(e.target.value)} required />
              </div>
              {error && <p className="text-sm text-red-600">{error}</p>}
              <Button type="submit" disabled={busy}>
                {busy ? "Resetting..." : "Reset password"}
              </Button>
            </form>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
