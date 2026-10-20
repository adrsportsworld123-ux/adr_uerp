"use client";

// Company Profile (erp-core-go migrations/065, GET/PUT /merchant/profile):
// the merchant's legal and trade name, GSTIN, PAN, contact and registered
// address, kept up to date by a Merchant Admin (merchant.profile). The
// merchant code is the sign-in key and is shown, never edited. The SaaS
// console edits the same profile on the merchant's behalf.
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import { dateTime } from "@/lib/format";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { PageHeader } from "@/components/data/page-header";
import { StatusChip } from "@/components/data/status-chip";
import { MerchantProfile, ProfileForm } from "@/components/merchant/profile-form";

export default function CompanyProfilePage() {
  const { can } = usePermissions();
  const allowed = can("merchant.profile");
  const [p, setP] = useState<MerchantProfile | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api
      .get<MerchantProfile>("/api/v1/merchant/profile")
      .then(setP)
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load the company profile"));
  }, []);

  return (
    <div className="max-w-2xl space-y-6">
      <PageHeader title="Company Profile" description="Your business as it appears on invoices, receipts and statutory documents." />
      {error && <p className="text-sm text-destructive">{error}</p>}
      {p && (
        <Card>
          <CardHeader>
            <CardTitle className="flex flex-wrap items-center gap-2">
              {p.trade_name || p.legal_name} <StatusChip status={p.status} />
            </CardTitle>
            <CardDescription>
              Merchant code <span className="font-mono">{p.code}</span> (used to sign in) · since {dateTime(p.created_at)}
            </CardDescription>
          </CardHeader>
          <CardContent>
            <ProfileForm
              key={JSON.stringify(p)}
              profile={p}
              disabled={!allowed}
              onSave={async (f) => {
                try {
                  setP(await api.put<MerchantProfile>("/api/v1/merchant/profile", f));
                  toast.success("Company profile saved");
                } catch (e) {
                  throw new Error(e instanceof ApiError ? e.message : "Could not save the company profile");
                }
              }}
            />
          </CardContent>
        </Card>
      )}
      {!allowed && p && <p className="text-sm text-muted-foreground">{missingPermissionHint("merchant.profile")}</p>}
    </div>
  );
}
