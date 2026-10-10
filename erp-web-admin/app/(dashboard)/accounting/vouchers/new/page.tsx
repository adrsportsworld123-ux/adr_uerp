"use client";

import { useRouter } from "next/navigation";
import { toast } from "sonner";
import { PageHeader } from "@/components/data/page-header";
import { VoucherForm } from "@/components/accounting/voucher-form";

export default function NewVoucherPage() {
  const router = useRouter();
  return (
    <div className="flex max-w-4xl flex-col gap-5">
      <PageHeader title="New journal voucher" description="An entry the system doesn't post by itself: accruals, corrections, opening balances." />
      <div className="rounded-xl border bg-card p-4">
        <VoucherForm
          onSaved={(v) => {
            toast.success(v.status === "posted" ? `${v.voucher_number} posted as ${v.entry_number}` : `${v.voucher_number} saved (${v.status})`);
            router.push(`/accounting/vouchers/${v.voucher_id}`);
          }}
        />
      </div>
    </div>
  );
}
