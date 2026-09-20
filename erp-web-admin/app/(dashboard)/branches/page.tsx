"use client";

import { useEffect, useState } from "react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Branch } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";

export default function BranchesPage() {
  const [branches, setBranches] = useState<Branch[]>([]);
  const [error, setError] = useState<string | null>(null);

  const [name, setName] = useState("");
  const [code, setCode] = useState("");
  const [gstin, setGstin] = useState("");
  const [submitting, setSubmitting] = useState(false);

  function load() {
    api
      .get<{ branches: Branch[] }>("/api/v1/branches")
      .then((d) => setBranches(d.branches))
      .catch((e) => setError(e.message));
  }

  useEffect(load, []);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    try {
      await api.post("/api/v1/branches", { name, code, gstin });
      toast.success("Branch created");
      setName("");
      setCode("");
      setGstin("");
      load();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : "Could not create branch");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="flex flex-col gap-6 max-w-2xl">
      <h1 className="text-2xl font-semibold">Branches</h1>
      {error && <p className="text-sm text-red-600">{error}</p>}

      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Name</TableHead>
            <TableHead>Code</TableHead>
            <TableHead>Timezone</TableHead>
            <TableHead>Status</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {branches.map((b) => (
            <TableRow key={b.branch_id}>
              <TableCell className="font-medium">{b.name}</TableCell>
              <TableCell className="font-mono">{b.code}</TableCell>
              <TableCell>{b.timezone}</TableCell>
              <TableCell>
                <Badge variant={b.status === "active" ? "default" : "secondary"}>{b.status}</Badge>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">New branch</CardTitle>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleSubmit} className="flex flex-col gap-4">
            <div className="grid grid-cols-2 gap-4">
              <div className="flex flex-col gap-2">
                <Label htmlFor="name">Name *</Label>
                <Input id="name" required value={name} onChange={(e) => setName(e.target.value)} />
              </div>
              <div className="flex flex-col gap-2">
                <Label htmlFor="code">Code *</Label>
                <Input id="code" required value={code} onChange={(e) => setCode(e.target.value)} placeholder="BLR02" />
              </div>
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="gstin">GSTIN</Label>
              <Input id="gstin" value={gstin} onChange={(e) => setGstin(e.target.value)} />
            </div>
            <Button type="submit" disabled={submitting}>
              {submitting ? "Creating..." : "Create branch"}
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  );
}
