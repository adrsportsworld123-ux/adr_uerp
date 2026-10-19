"use client";

// My account (erp-core-go internal/users, migrations/062): any signed-in
// user changes their own password, or their PIN for POS sign-in and manager
// approvals, after confirming the current password.
import { useState } from "react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { PageHeader } from "@/components/data/page-header";

export default function AccountPage() {
  return (
    <div className="space-y-6">
      <PageHeader title="My Account" description="Change your own password or PIN." />
      <ChangePassword />
      <ChangePIN />
    </div>
  );
}

function ChangePassword() {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const invalid = next.length > 0 && (next.length < 8 || next !== confirm);

  async function save() {
    setBusy(true);
    try {
      await api.put("/api/v1/me/password", { current_password: current, new_password: next });
      toast.success("Password changed. Your other sessions are signed out.");
      setCurrent("");
      setNext("");
      setConfirm("");
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not change the password");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card className="max-w-2xl">
      <CardHeader>
        <CardTitle>Password</CardTitle>
        <CardDescription>8 characters or more; your recent passwords can&apos;t be reused.</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-3 sm:grid-cols-3">
        <div className="space-y-1">
          <Label htmlFor="cur-password">Current password</Label>
          <Input id="cur-password" type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} />
        </div>
        <div className="space-y-1">
          <Label htmlFor="my-new-password">New password</Label>
          <Input id="my-new-password" type="password" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} />
        </div>
        <div className="space-y-1">
          <Label htmlFor="my-confirm-password">Confirm</Label>
          <Input id="my-confirm-password" type="password" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} />
        </div>
        {invalid && <p className="text-xs text-destructive sm:col-span-3">At least 8 characters, typed the same twice</p>}
        <div className="sm:col-span-3">
          <Button disabled={busy || !current || !next || invalid} onClick={save}>
            Change password
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

function ChangePIN() {
  const [current, setCurrent] = useState("");
  const [pin, setPin] = useState("");
  const [busy, setBusy] = useState(false);
  const invalid = pin.length > 0 && !/^\d{4,6}$/.test(pin);

  async function save() {
    setBusy(true);
    try {
      await api.put("/api/v1/me/pin", { current_password: current, pin });
      toast.success("PIN changed");
      setCurrent("");
      setPin("");
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not change the PIN");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card className="max-w-2xl">
      <CardHeader>
        <CardTitle>PIN</CardTitle>
        <CardDescription>For POS sign-in with your employee code, and for approving at the till. 4 to 6 digits.</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-3 sm:grid-cols-3">
        <div className="space-y-1">
          <Label htmlFor="pin-cur-password">Current password</Label>
          <Input id="pin-cur-password" type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} />
        </div>
        <div className="space-y-1">
          <Label htmlFor="my-new-pin">New PIN</Label>
          <Input id="my-new-pin" type="password" inputMode="numeric" maxLength={6} value={pin} onChange={(e) => setPin(e.target.value.replace(/\D/g, ""))} />
        </div>
        {invalid && <p className="text-xs text-destructive sm:col-span-3">4 to 6 digits</p>}
        <div className="sm:col-span-3">
          <Button disabled={busy || !current || !pin || invalid} onClick={save}>
            Change PIN
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}
