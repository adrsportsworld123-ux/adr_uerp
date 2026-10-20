import Link from "next/link";
import { PARTY_LABEL, PartyType } from "@/components/data/party-picker";

// A ledger line's party: its name and kind, linking to that party's ledger.
export function PartyCell({ type, id, name }: { type: PartyType | null; id: string | null; name: string | null }) {
  if (!type || !id) return <span className="text-muted-foreground">—</span>;
  return (
    <Link href={`/accounting/party-ledger?party_type=${type}&party_id=${id}`} className="hover:underline">
      {name ?? "(removed)"} <span className="text-xs text-muted-foreground">· {PARTY_LABEL[type] ?? type}</span>
    </Link>
  );
}
