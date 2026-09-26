"use client";

// Phase 4's last sub-area: E-Invoicing (IRN generation, QR code) and
// E-Way Bill (erp-core-go's phased_roadmap.md / docs/phase0_1_design.md
// §3.13-ish neighborhood — see migrations/022_einvoice.sql's header
// comment). Gated by einvoice.manage (Branch Manager/Merchant Admin).
//
// There's no sales-order browse/detail screen anywhere in this admin app
// yet — POS orders are transacted entirely through erp-pos-flutter, and
// this app has never needed a server-side order list. So this page takes
// a sales_order_id directly (copied from a receipt, the audit log, or the
// Flutter app) rather than picking one from a list — a known, documented
// limitation, not an oversight, the same shape as other already-flagged
// gaps in this codebase (e.g. the B2C GSTIN-based place-of-supply
// limitation in erp-core-go's internal/gst).
import { useState } from "react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { EInvoice, EWayBill } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Separator } from "@/components/ui/separator";

export default function EInvoicingPage() {
  const [orderId, setOrderId] = useState("");
  const [loaded, setLoaded] = useState(false);
  const [loading, setLoading] = useState(false);
  const [einvoice, setEinvoice] = useState<EInvoice | null>(null);
  const [ewayBill, setEwayBill] = useState<EWayBill | null>(null);
  const [cancelReason, setCancelReason] = useState("");
  const [vehicleNo, setVehicleNo] = useState("");
  const [transporterId, setTransporterId] = useState("");
  const [distanceKm, setDistanceKm] = useState("");

  async function load() {
    if (!orderId) return;
    setLoading(true);
    try {
      const [ei, ewb] = await Promise.all([
        api.get<EInvoice>(`/api/v1/sales/orders/${orderId}/e-invoice`).catch((e) => {
          if (e instanceof ApiError && e.status === 404) return null;
          throw e;
        }),
        api.get<EWayBill>(`/api/v1/sales/orders/${orderId}/e-way-bill`).catch((e) => {
          if (e instanceof ApiError && e.status === 404) return null;
          throw e;
        }),
      ]);
      setEinvoice(ei);
      setEwayBill(ewb);
      setLoaded(true);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not load this order's compliance documents");
    } finally {
      setLoading(false);
    }
  }

  async function generateEInvoice() {
    try {
      const resp = await api.post<EInvoice>(`/api/v1/sales/orders/${orderId}/e-invoice`);
      setEinvoice(resp);
      toast.success("E-invoice generated");
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not generate e-invoice");
    }
  }

  async function cancelEInvoice() {
    try {
      const resp = await api.post<EInvoice>(`/api/v1/sales/orders/${orderId}/e-invoice/cancel`, { reason: cancelReason });
      setEinvoice(resp);
      toast.success("E-invoice cancelled");
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not cancel e-invoice");
    }
  }

  async function generateEWayBill() {
    try {
      const resp = await api.post<EWayBill>(`/api/v1/sales/orders/${orderId}/e-way-bill`, {
        vehicle_no: vehicleNo || undefined,
        transporter_id: transporterId || undefined,
        distance_km: distanceKm ? Number(distanceKm) : undefined,
      });
      setEwayBill(resp);
      toast.success("E-way bill generated");
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not generate e-way bill");
    }
  }

  async function cancelEWayBill() {
    try {
      const resp = await api.post<EWayBill>(`/api/v1/sales/orders/${orderId}/e-way-bill/cancel`, { reason: cancelReason });
      setEwayBill(resp);
      toast.success("E-way bill cancelled");
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not cancel e-way bill");
    }
  }

  const einvoiceActive = einvoice && einvoice.status === "generated";
  const ewayBillActive = ewayBill && ewayBill.status === "generated";

  return (
    <div className="flex flex-col gap-6 max-w-3xl">
      <h1 className="text-2xl font-semibold">E-Invoicing & E-Way Bill</h1>
      <p className="text-sm text-zinc-500">
        Uses a vendor-agnostic stub GSP client (no real GST Suvidha Provider is wired in yet — see
        migrations/022_einvoice.sql). IRNs and e-way-bill numbers generated here are realistic in shape but not
        genuine government-issued documents.
      </p>

      <Card>
        <CardContent className="pt-6 flex items-end gap-4">
          <div className="flex flex-col gap-2 flex-1">
            <Label htmlFor="orderId">Sales order ID</Label>
            <Input id="orderId" value={orderId} onChange={(e) => setOrderId(e.target.value)} placeholder="UUID" className="font-mono text-xs" />
          </div>
          <Button onClick={load} disabled={!orderId || loading}>
            {loading ? "Loading..." : "Load"}
          </Button>
        </CardContent>
      </Card>

      {loaded && (
        <>
          <Card>
            <CardHeader>
              <CardTitle className="text-base flex items-center gap-2">
                E-Invoice
                {einvoice && <Badge variant={einvoiceActive ? "default" : "outline"}>{einvoice.status}</Badge>}
              </CardTitle>
            </CardHeader>
            <CardContent className="flex flex-col gap-3">
              {einvoice ? (
                <div className="text-sm flex flex-col gap-1">
                  <div>
                    IRN: <span className="font-mono text-xs break-all">{einvoice.irn}</span>
                  </div>
                  <div>
                    Ack No: <span className="font-mono text-xs">{einvoice.ack_no}</span> — {einvoice.ack_date}
                  </div>
                  <div>GSP: {einvoice.gsp_provider}</div>
                </div>
              ) : (
                <p className="text-sm text-zinc-500">No e-invoice generated for this order yet.</p>
              )}
              <Separator />
              <div className="flex items-end gap-4 flex-wrap">
                {!einvoiceActive && (
                  <Button onClick={generateEInvoice} disabled={!!einvoice && einvoice.status === "cancelled"}>
                    Generate e-invoice
                  </Button>
                )}
                {einvoiceActive && (
                  <div className="flex items-end gap-2 flex-wrap">
                    <div className="flex flex-col gap-2 w-64">
                      <Label htmlFor="cancelReasonEi">Cancel reason</Label>
                      <Textarea id="cancelReasonEi" value={cancelReason} onChange={(e) => setCancelReason(e.target.value)} rows={2} />
                    </div>
                    <Button variant="destructive" onClick={cancelEInvoice} disabled={!cancelReason}>
                      Cancel e-invoice
                    </Button>
                  </div>
                )}
              </div>
              {einvoice?.status === "cancelled" && (
                <p className="text-xs text-zinc-500">Cancelled — a cancelled IRN can never be reused (real NIC rule); this order cannot get a new e-invoice.</p>
              )}
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="text-base flex items-center gap-2">
                E-Way Bill
                {ewayBill && <Badge variant={ewayBillActive ? "default" : "outline"}>{ewayBill.status}</Badge>}
                {ewayBill?.interstate && <Badge variant="outline">Interstate</Badge>}
                {ewayBill?.required_by_rule && <Badge variant="destructive">Required by rule (&gt;₹50k interstate)</Badge>}
              </CardTitle>
            </CardHeader>
            <CardContent className="flex flex-col gap-3">
              {ewayBill ? (
                <div className="text-sm flex flex-col gap-1">
                  <div>
                    EWB No: <span className="font-mono text-xs">{ewayBill.ewb_no}</span> — valid until {ewayBill.valid_until}
                  </div>
                  <div>
                    Vehicle: {ewayBill.vehicle_no || "—"} · Transporter: {ewayBill.transporter_id || "—"} · Distance:{" "}
                    {ewayBill.distance_km || "—"} km
                  </div>
                </div>
              ) : (
                <p className="text-sm text-zinc-500">No e-way bill generated for this order yet.</p>
              )}
              <Separator />
              {!ewayBillActive && ewayBill?.status !== "cancelled" && (
                <div className="flex items-end gap-4 flex-wrap">
                  <div className="flex flex-col gap-2 w-40">
                    <Label htmlFor="vehicleNo">Vehicle no.</Label>
                    <Input id="vehicleNo" value={vehicleNo} onChange={(e) => setVehicleNo(e.target.value)} placeholder="KA01AB1234" />
                  </div>
                  <div className="flex flex-col gap-2 w-40">
                    <Label htmlFor="transporterId">Transporter ID</Label>
                    <Input id="transporterId" value={transporterId} onChange={(e) => setTransporterId(e.target.value)} />
                  </div>
                  <div className="flex flex-col gap-2 w-32">
                    <Label htmlFor="distanceKm">Distance (km)</Label>
                    <Input id="distanceKm" type="number" value={distanceKm} onChange={(e) => setDistanceKm(e.target.value)} />
                  </div>
                  <Button onClick={generateEWayBill}>Generate e-way bill</Button>
                </div>
              )}
              {ewayBillActive && (
                <div className="flex items-end gap-2 flex-wrap">
                  <div className="flex flex-col gap-2 w-64">
                    <Label htmlFor="cancelReasonEwb">Cancel reason</Label>
                    <Textarea id="cancelReasonEwb" value={cancelReason} onChange={(e) => setCancelReason(e.target.value)} rows={2} />
                  </div>
                  <Button variant="destructive" onClick={cancelEWayBill} disabled={!cancelReason}>
                    Cancel e-way bill
                  </Button>
                </div>
              )}
              {ewayBill?.status === "cancelled" && <p className="text-xs text-zinc-500">Cancelled.</p>}
            </CardContent>
          </Card>
        </>
      )}
    </div>
  );
}
