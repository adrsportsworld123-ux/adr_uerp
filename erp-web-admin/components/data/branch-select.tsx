"use client";

import { useEffect, useState } from "react";
import { api } from "@/lib/api-client";
import { Branch } from "@/lib/types";
import { usePermissions } from "@/lib/permissions";
import { selectClass } from "@/components/data/panel";

/**
 * The branches the signed-in user may see (`?scope=mine`, Phase 15), and
 * the one a picker starts on: their own branch when it's among them,
 * otherwise the first.
 */
export function useMyBranches() {
  const { homeBranchId } = usePermissions();
  const [branches, setBranches] = useState<Branch[]>([]);
  useEffect(() => {
    api
      .get<{ branches: Branch[] }>("/api/v1/branches?scope=mine")
      .then((d) => setBranches(d.branches))
      .catch(() => {});
  }, []);
  const home = branches.find((b) => b.branch_id === homeBranchId) ?? branches[0];
  return { branches, defaultBranchId: home?.branch_id ?? "" };
}

/** A branch choice from the user's own branches. */
export function BranchSelect({
  id,
  branches,
  value,
  onChange,
  className = "w-48",
}: {
  id?: string;
  branches: Branch[];
  value: string;
  onChange: (branchId: string) => void;
  className?: string;
}) {
  return (
    <select id={id} aria-label={id ? undefined : "Branch"} value={value} onChange={(e) => onChange(e.target.value)} className={`${selectClass} ${className}`}>
      {branches.length === 0 && <option value="">Loading…</option>}
      {branches.map((b) => (
        <option key={b.branch_id} value={b.branch_id}>
          {b.name}
        </option>
      ))}
    </select>
  );
}
