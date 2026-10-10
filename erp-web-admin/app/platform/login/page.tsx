"use client";

// SaaS console sign-in (Phase 16): platform staff only, separate from the
// merchant login at /login.
import { FormEvent, useState } from "react";
import { useRouter } from "next/navigation";
import { Loader2 } from "lucide-react";
import { ApiError } from "@/lib/api-client";
import { platformLogin } from "@/lib/platform-api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

export default function PlatformLoginPage() {
  const router = useRouter();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await platformLogin(email.trim(), password);
      router.replace("/platform");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Can't reach the server");
      setBusy(false);
    }
  }

  return (
    <main className="flex flex-1 items-center justify-center p-4">
      <form onSubmit={submit} className="w-full max-w-sm space-y-4 rounded-xl border bg-card p-6">
        <div>
          <h1 className="text-lg font-semibold">SaaS console</h1>
          <p className="text-sm text-muted-foreground">Platform staff sign-in. Merchants sign in at the admin login.</p>
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="p-email">Email</Label>
          <Input id="p-email" type="email" autoComplete="username" value={email} onChange={(e) => setEmail(e.target.value)} required />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="p-password">Password</Label>
          <Input
            id="p-password"
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
          />
        </div>
        {error && (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        )}
        <Button type="submit" className="w-full" disabled={busy}>
          {busy && <Loader2 className="size-4 animate-spin" aria-hidden />}
          Sign in
        </Button>
      </form>
    </main>
  );
}
