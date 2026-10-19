"use client";

// A sale's GST compliance documents (erp-core-go migrations/022_einvoice.sql):
// the e-invoice (IRN) and the e-way bill, with generate and cancel. Used on
// the sale's own page and on the E-Invoicing page. Writes need
// einvoice.manage; without it the buttons are disabled with the reason, and
// the server refuses anyway.
import { ComponentType, ReactNode, useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { EInvoice, EWayBill } from "@/lib/types";
import { missingPermissionHint, usePermissions } from "@/lib/permissions";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Separator } from "@/components/ui/separator";
import { Field, Panel } from "@/components/data/panel";
import { StatusChip } from "@/components/data/status-chip";

import { currencySymbol } from "@/lib/currency";
type Frame = ComponentType<{ title: ReactNode; children: ReactNode }>;

function notFoundIsNull<T>(p: Promise<T>): Promise<T | null> {
  return p.catch((e) => {
    if (e instanceof ApiError && e.status === 404) return null;
    throw e;
  });
}

export function EInvoiceDocuments({ orderId, frame: FrameC = Panel }: { orderId: string; frame?: Frame }) {
  const { can } = usePermissions();
  const allowed = can("einvoice.manage");
  const denied = allowed ? undefined : missingPermissionHint("einvoice.manage");
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [einvoice, setEinvoice] = useState<EInvoice | null>(null);
  const [ewayBill, setEwayBill] = useState<EWayBill | null>(null);
  const [eiReason, setEiReason] = useState("");
  const [ewbReason, setEwbReason] = useState("");
  const [vehicleNo, setVehicleNo] = useState("");
  const [transporterId, setTransporterId] = useState("");
  const [distanceKm, setDistanceKm] = useState("");

  const load = useCallback(() => {
    Promise.all([
      notFoundIsNull(api.get<EInvoice>(`/api/v1/sales/orders/${orderId}/e-invoice`)),
      notFoundIsNull(api.get<EWayBill>(`/api/v1/sales/orders/${orderId}/e-way-bill`)),
    ])
      .then(([ei, ewb]) => {
        setEinvoice(ei);
        setEwayBill(ewb);
        setError(null);
        setLoaded(true);
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load this order's compliance documents"));
  }, [orderId]);
  useEffect(load, [load]);

  async function run<T>(call: () => Promise<T>, done: (r: T) => void, ok: string, fail: string) {
    setBusy(true);
    try {
      done(await call());
      toast.success(ok);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : fail);
    } finally {
      setBusy(false);
    }
  }

  const base = `/api/v1/sales/orders/${orderId}`;
  const generateEInvoice = () => run(() => api.post<EInvoice>(`${base}/e-invoice`), setEinvoice, "E-invoice generated", "Could not generate e-invoice");
  const cancelEInvoice = () =>
    run(() => api.post<EInvoice>(`${base}/e-invoice/cancel`, { reason: eiReason }), (r) => (setEinvoice(r), setEiReason("")), "E-invoice cancelled", "Could not cancel e-invoice");
  const generateEWayBill = () =>
    run(
      () =>
        api.post<EWayBill>(`${base}/e-way-bill`, {
          vehicle_no: vehicleNo || undefined,
          transporter_id: transporterId || undefined,
          distance_km: distanceKm ? Number(distanceKm) : undefined,
        }),
      setEwayBill,
      "E-way bill generated",
      "Could not generate e-way bill",
    );
  const cancelEWayBill = () =>
    run(() => api.post<EWayBill>(`${base}/e-way-bill/cancel`, { reason: ewbReason }), (r) => (setEwayBill(r), setEwbReason("")), "E-way bill cancelled", "Could not cancel e-way bill");

  if (error) {
    return (
      <p className="text-sm text-destructive" role="alert">
        {error}
      </p>
    );
  }
  if (!loaded) return <p className="text-sm text-muted-foreground">Loading e-invoice and e-way bill…</p>;

  const einvoiceActive = einvoice?.status === "generated";
  const ewayBillActive = ewayBill?.status === "generated";

  return (
    <>
      <FrameC
        title={
          <>
            E-Invoice
            {einvoice && <StatusChip status={einvoice.status} tone={einvoiceActive ? "ok" : "muted"} />}
          </>
        }
      >
        <div className="flex flex-col gap-4">
          {einvoice ? (
            <div className="flex flex-col gap-1 text-sm">
              <div>
                IRN: <span className="font-mono text-xs break-all">{einvoice.irn}</span>
              </div>
              <div>
                Ack No: <span className="font-mono text-xs">{einvoice.ack_no}</span> — {einvoice.ack_date}
              </div>
              <div>GSP: {einvoice.gsp_provider}</div>
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">No e-invoice generated for this order yet.</p>
          )}
          <Separator />
          {!einvoiceActive && einvoice?.status !== "cancelled" && (
            <Button className="w-fit" onClick={generateEInvoice} disabled={busy || !allowed} title={denied}>
              Generate e-invoice
            </Button>
          )}
          {einvoiceActive && (
            <div className="flex flex-wrap items-end gap-2">
              <Field label="Cancel reason" htmlFor="cancelReasonEi" className="w-64">
                <Textarea id="cancelReasonEi" value={eiReason} onChange={(e) => setEiReason(e.target.value)} rows={2} disabled={!allowed} />
              </Field>
              <Button variant="destructive" onClick={cancelEInvoice} disabled={busy || !eiReason || !allowed} title={denied}>
                Cancel e-invoice
              </Button>
            </div>
          )}
          {einvoice?.status === "cancelled" && (
            <p className="text-xs text-muted-foreground">Cancelled — a cancelled IRN can never be reused (real NIC rule); this order cannot get a new e-invoice.</p>
          )}
        </div>
      </FrameC>

      <FrameC
        title={
          <>
            E-Way Bill
            {ewayBill && <StatusChip status={ewayBill.status} tone={ewayBillActive ? "ok" : "muted"} />}
            {ewayBill?.interstate && <StatusChip status="interstate" tone="info" label="Interstate" />}
            {ewayBill?.required_by_rule && <StatusChip status="required" tone="warn" label={`Required by rule (>${currencySymbol()}50k interstate)`} />}
          </>
        }
      >
        <div className="flex flex-col gap-4">
          {ewayBill ? (
            <div className="flex flex-col gap-1 text-sm">
              <div>
                EWB No: <span className="font-mono text-xs">{ewayBill.ewb_no}</span> — valid until {ewayBill.valid_until}
              </div>
              <div>
                Vehicle: {ewayBill.vehicle_no || "—"} · Transporter: {ewayBill.transporter_id || "—"} · Distance: {ewayBill.distance_km || "—"} km
              </div>
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">No e-way bill generated for this order yet.</p>
          )}
          <Separator />
          {!ewayBillActive && ewayBill?.status !== "cancelled" && (
            <div className="flex flex-wrap items-end gap-3">
              <Field label="Vehicle no." htmlFor="vehicleNo" className="w-40">
                <Input id="vehicleNo" value={vehicleNo} onChange={(e) => setVehicleNo(e.target.value)} placeholder="KA01AB1234" disabled={!allowed} />
              </Field>
              <Field label="Transporter ID" htmlFor="transporterId" className="w-40">
                <Input id="transporterId" value={transporterId} onChange={(e) => setTransporterId(e.target.value)} disabled={!allowed} />
              </Field>
              <Field label="Distance (km)" htmlFor="distanceKm" className="w-32">
                <Input id="distanceKm" type="number" value={distanceKm} onChange={(e) => setDistanceKm(e.target.value)} disabled={!allowed} />
              </Field>
              <Button onClick={generateEWayBill} disabled={busy || !allowed} title={denied}>
                Generate e-way bill
              </Button>
            </div>
          )}
          {ewayBillActive && (
            <div className="flex flex-wrap items-end gap-2">
              <Field label="Cancel reason" htmlFor="cancelReasonEwb" className="w-64">
                <Textarea id="cancelReasonEwb" value={ewbReason} onChange={(e) => setEwbReason(e.target.value)} rows={2} disabled={!allowed} />
              </Field>
              <Button variant="destructive" onClick={cancelEWayBill} disabled={busy || !ewbReason || !allowed} title={denied}>
                Cancel e-way bill
              </Button>
            </div>
          )}
          {ewayBill?.status === "cancelled" && <p className="text-xs text-muted-foreground">Cancelled.</p>}
          {!allowed && <p className="text-xs text-muted-foreground">{denied}</p>}
        </div>
      </FrameC>
    </>
  );
}
