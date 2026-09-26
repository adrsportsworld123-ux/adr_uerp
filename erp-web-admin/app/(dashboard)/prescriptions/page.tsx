"use client";

// Phase 8: Vertical Expansion — Pharmacy's "prescription linkage" item
// (erp-core-go's phased_roadmap.md; internal/pharmacy). A prescription is
// real evidence a cashier/pharmacist collected at the counter — see
// erp-core-go's internal/sales Checkout for the actual enforcement (a
// "Drug Schedule" line other than "OTC" refuses checkout without one
// attached). This app has no checkout UI (see credit.spec.ts's own note),
// so attaching a prescription to a specific order is driven via the API
// directly, same posture as this app's existing credit-sale flow — this
// screen is the record-keeping half: create one for a customer, and look
// up their history.
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Customer, Prescription } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

export default function PrescriptionsPage() {
  const [customers, setCustomers] = useState<Customer[]>([]);
  const [customerId, setCustomerId] = useState("");
  const [doctorName, setDoctorName] = useState("");
  const [doctorRegNo, setDoctorRegNo] = useState("");
  const [notes, setNotes] = useState("");
  const [creating, setCreating] = useState(false);

  const [lookupCustomerId, setLookupCustomerId] = useState("");
  const [history, setHistory] = useState<Prescription[]>([]);

  useEffect(() => {
    api.get<{ customers: Customer[] }>("/api/v1/customers?limit=200").then((d) => setCustomers(d.customers)).catch(() => {});
  }, []);

  async function createPrescription() {
    if (!customerId || !doctorName.trim()) {
      toast.error("Customer and doctor name are required");
      return;
    }
    setCreating(true);
    try {
      await api.post("/api/v1/prescriptions", { customer_id: customerId, doctor_name: doctorName.trim(), doctor_reg_no: doctorRegNo, notes });
      toast.success("Prescription recorded");
      setDoctorName("");
      setDoctorRegNo("");
      setNotes("");
      if (lookupCustomerId === customerId) loadHistory(customerId);
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not record prescription");
    } finally {
      setCreating(false);
    }
  }

  function loadHistory(id: string) {
    setLookupCustomerId(id);
    if (!id) {
      setHistory([]);
      return;
    }
    api
      .get<{ prescriptions: Prescription[] }>(`/api/v1/prescriptions?customer_id=${id}`)
      .then((d) => setHistory(d.prescriptions))
      .catch((e) => toast.error(e instanceof ApiError ? e.message : "Could not load prescription history"));
  }

  return (
    <div className="flex flex-col gap-6 max-w-4xl">
      <h1 className="text-2xl font-semibold">Prescriptions</h1>
      <p className="text-sm text-zinc-500">
        A scheduled-drug sale (anything with a &quot;Drug Schedule&quot; other than OTC) refuses at checkout unless a
        prescription like this is attached to the order — attaching one to a specific sale is done via the API
        directly (this app has no checkout screen), the same way an existing credit sale is created.
      </p>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Record a prescription</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <div className="flex flex-col gap-2">
            <Label>Customer</Label>
            <Select value={customerId} onValueChange={(v) => setCustomerId(v ?? "")} items={Object.fromEntries(customers.map((c) => [c.customer_id, c.name || c.phone]))}>
              <SelectTrigger><SelectValue placeholder="Select customer" /></SelectTrigger>
              <SelectContent>
                {customers.map((c) => (
                  <SelectItem key={c.customer_id} value={c.customer_id}>{c.name || c.phone}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div className="flex flex-col gap-2">
              <Label htmlFor="doctorName">Doctor name</Label>
              <Input id="doctorName" value={doctorName} onChange={(e) => setDoctorName(e.target.value)} />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="doctorRegNo">Doctor registration no. (optional)</Label>
              <Input id="doctorRegNo" value={doctorRegNo} onChange={(e) => setDoctorRegNo(e.target.value)} />
            </div>
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="notes">Notes</Label>
            <Textarea id="notes" value={notes} onChange={(e) => setNotes(e.target.value)} rows={2} />
          </div>
          <Button onClick={createPrescription} disabled={creating} className="w-fit">
            {creating ? "Saving..." : "Record prescription"}
          </Button>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">History</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <div className="flex flex-col gap-2 w-64">
            <Label>Customer</Label>
            <Select value={lookupCustomerId} onValueChange={(v) => loadHistory(v ?? "")} items={Object.fromEntries(customers.map((c) => [c.customer_id, c.name || c.phone]))}>
              <SelectTrigger><SelectValue placeholder="Select customer" /></SelectTrigger>
              <SelectContent>
                {customers.map((c) => (
                  <SelectItem key={c.customer_id} value={c.customer_id}>{c.name || c.phone}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Date</TableHead>
                <TableHead>Doctor</TableHead>
                <TableHead>Reg. no.</TableHead>
                <TableHead>Notes</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {history.map((p) => (
                <TableRow key={p.prescription_id}>
                  <TableCell>{p.created_at}</TableCell>
                  <TableCell>{p.doctor_name}</TableCell>
                  <TableCell>{p.doctor_reg_no || "—"}</TableCell>
                  <TableCell>{p.notes || "—"}</TableCell>
                </TableRow>
              ))}
              {history.length === 0 && (
                <TableRow>
                  <TableCell colSpan={4} className="text-center text-sm text-zinc-500 py-6">
                    {lookupCustomerId ? "No prescriptions on file" : "Select a customer to see their history"}
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
    </div>
  );
}
