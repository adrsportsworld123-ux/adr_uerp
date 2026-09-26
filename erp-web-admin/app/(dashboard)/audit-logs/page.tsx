"use client";

// Phase 4's "immutable audit trail" item (erp-core-go's phased_roadmap.md
// / docs/phase0_1_design.md §3.19/§3.20) — closed 2026-09-22. Gated by
// audit.view (Merchant Admin only): rows here can contain other users'
// before/after field values (prices, credit limits, discount overrides),
// a real compliance/oversight surface, not a routine operational read.
import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { AuditLogEntry, AuditVerifyResult } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";

export default function AuditLogsPage() {
  const [entityType, setEntityType] = useState("");
  const [entityId, setEntityId] = useState("");
  const [entries, setEntries] = useState<AuditLogEntry[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [verifying, setVerifying] = useState(false);
  const [verifyResult, setVerifyResult] = useState<AuditVerifyResult | null>(null);

  const load = useCallback(() => {
    const params = new URLSearchParams();
    if (entityType) params.set("entity_type", entityType);
    if (entityId) params.set("entity_id", entityId);
    api
      .get<{ entries: AuditLogEntry[] }>(`/api/v1/audit-logs?${params.toString()}`)
      .then((d) => {
        setEntries(d.entries);
        setError(null);
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Could not load audit logs"));
  }, [entityType, entityId]);
  useEffect(load, [load]);

  async function verify() {
    setVerifying(true);
    try {
      const result = await api.get<AuditVerifyResult>("/api/v1/audit-logs/verify");
      setVerifyResult(result);
      if (result.valid) {
        toast.success(`Chain verified — ${result.rows_checked} row(s) checked, ${result.legacy_rows_skipped} pre-hardening row(s) skipped`);
      } else {
        toast.error(`Chain broken at row ${result.broken_at_id}: ${result.broken_reason}`);
      }
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not verify audit chain");
    } finally {
      setVerifying(false);
    }
  }

  return (
    <div className="flex flex-col gap-6 max-w-5xl">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Audit Log</h1>
        <Button onClick={verify} disabled={verifying} variant="outline">
          {verifying ? "Verifying..." : "Verify tamper-evident chain"}
        </Button>
      </div>

      {verifyResult && (
        <Card>
          <CardContent className="pt-6 flex items-center gap-4">
            <Badge variant={verifyResult.valid ? "default" : "destructive"}>{verifyResult.valid ? "Valid" : "Broken"}</Badge>
            <span className="text-sm text-zinc-600">
              {verifyResult.rows_checked} row(s) checked, {verifyResult.legacy_rows_skipped} pre-hardening row(s) skipped
              {!verifyResult.valid && (
                <>
                  {" "}
                  — broken at row <span className="font-mono">{verifyResult.broken_at_id}</span>: {verifyResult.broken_reason}
                </>
              )}
            </span>
          </CardContent>
        </Card>
      )}

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Filter</CardTitle>
        </CardHeader>
        <CardContent className="flex items-end gap-4 flex-wrap">
          <div className="flex flex-col gap-2 w-48">
            <Label htmlFor="entityType">Entity type</Label>
            <Input id="entityType" value={entityType} onChange={(e) => setEntityType(e.target.value)} placeholder="e.g. stock_levels" />
          </div>
          <div className="flex flex-col gap-2 flex-1 min-w-64">
            <Label htmlFor="entityId">Entity ID</Label>
            <Input id="entityId" value={entityId} onChange={(e) => setEntityId(e.target.value)} placeholder="UUID" className="font-mono text-xs" />
          </div>
        </CardContent>
      </Card>

      {error && <p className="text-sm text-red-600">{error}</p>}

      <Card>
        <CardContent className="pt-6">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>When</TableHead>
                <TableHead>Entity</TableHead>
                <TableHead>Action</TableHead>
                <TableHead>Performed by</TableHead>
                <TableHead>Reason</TableHead>
                <TableHead>Checksum</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {entries.map((e) => (
                <TableRow key={e.id}>
                  <TableCell className="text-xs">{e.created_at}</TableCell>
                  <TableCell className="text-xs">
                    {e.entity_type} <span className="font-mono text-zinc-500">{e.entity_id.slice(0, 8)}…</span>
                  </TableCell>
                  <TableCell>
                    <Badge variant="outline">{e.action}</Badge>
                  </TableCell>
                  <TableCell className="font-mono text-xs">{e.performed_by ? e.performed_by.slice(0, 8) + "…" : "—"}</TableCell>
                  <TableCell className="text-xs">{e.reason || "—"}</TableCell>
                  <TableCell className="text-xs">
                    {e.checksum ? (
                      <span className="font-mono">{e.checksum.slice(0, 10)}…</span>
                    ) : (
                      <span className="text-zinc-400">pre-hardening</span>
                    )}
                  </TableCell>
                </TableRow>
              ))}
              {entries.length === 0 && (
                <TableRow>
                  <TableCell colSpan={6} className="text-center text-sm text-zinc-500 py-6">
                    No audit log entries match this filter
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
