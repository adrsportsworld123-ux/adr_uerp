"use client";

// Phase 4's last sub-area: E-Invoicing (IRN generation, QR code) and
// E-Way Bill (see erp-core-go migrations/022_einvoice.sql's header
// comment). Gated by einvoice.manage (Branch Manager/Merchant Admin).
//
// The usual place for this is the sale's own page (/sales/{id}), which
// shows the same documents. This page is the lookup by order ID, and opens
// straight onto one with ?order_id=.
import { Suspense, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/data/page-header";
import { Field, FilterBar } from "@/components/data/panel";
import { EInvoiceDocuments } from "@/components/sales/einvoice-documents";

function EInvoicingView() {
  const linked = useSearchParams().get("order_id") ?? "";
  const [orderId, setOrderId] = useState(linked);
  const [shown, setShown] = useState(linked);

  return (
    <div className="flex max-w-5xl flex-col gap-5">
      <PageHeader
        title="E-Invoicing & E-Way Bill"
        description={
          <>
            Generate or cancel a sale&apos;s IRN and e-way bill. You can also do this from the sale itself: open it from the{" "}
            <Link href="/sales" className="text-foreground underline">
              Sales
            </Link>{" "}
            page.
          </>
        }
      />
      <p className="rounded-lg bg-warn-soft p-3 text-xs text-warn">
        Uses a stub GSP client (no real GST Suvidha Provider is wired in yet). IRNs and e-way-bill numbers generated here are realistic in shape
        but not genuine government-issued documents.
      </p>

      <FilterBar onSubmit={() => setShown(orderId.trim())}>
        <Field label="Sales order ID" htmlFor="orderId" className="min-w-72 flex-1">
          <Input id="orderId" value={orderId} onChange={(e) => setOrderId(e.target.value)} placeholder="UUID" className="font-mono text-xs" />
        </Field>
        <Button type="submit" disabled={!orderId.trim()}>
          Load
        </Button>
      </FilterBar>

      {shown && <EInvoiceDocuments key={shown} orderId={shown} />}
    </div>
  );
}

export default function EInvoicingPage() {
  return (
    <Suspense>
      <EInvoicingView />
    </Suspense>
  );
}
