import { redirect } from "next/navigation";

// Phase 14: manual entries are journal vouchers now (draft, approval when
// the business turns it on, then posting). The old URL keeps working.
export default function NewJournalEntryRedirect() {
  redirect("/accounting/vouchers/new");
}
