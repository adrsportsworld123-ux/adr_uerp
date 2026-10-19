"use client";

// Confirm-and-delete for a purchase document entered by mistake: a draft
// or open purchase order with nothing received, or a draft GRN (erp-core-go
// migrations/058). The server soft-deletes it, so it can be restored from
// the Recycle Bin; the reason goes to the audit log.
import { useState } from "react";
import { Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { Modal } from "@/components/ui/dialog";
import { Field } from "@/components/data/panel";

export function DeleteDocumentButton({
  path,
  what,
  number,
  note,
  onDeleted,
  disabled,
}: {
  /** API path of the document, e.g. /api/v1/purchase/orders/{id}. */
  path: string;
  /** "purchase order", "GRN" */
  what: string;
  number: string;
  /** One line on what deleting means for this document. */
  note: string;
  onDeleted: () => void;
  disabled?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);

  async function confirm() {
    setBusy(true);
    try {
      const q = reason.trim() ? `?${new URLSearchParams({ reason: reason.trim() })}` : "";
      await api.delete(`${path}${q}`);
      toast.success(`${number} deleted — restore it from the Recycle Bin if needed`);
      setOpen(false);
      onDeleted();
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : `Could not delete the ${what}`);
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <Button variant="outline" className="text-destructive" disabled={disabled} onClick={() => setOpen(true)}>
        <Trash2 /> Delete
      </Button>
      {open && (
        <Modal
          open
          onOpenChange={(o) => !o && setOpen(false)}
          title={`Delete ${number}?`}
          description={note}
          footer={
            <>
              <Button variant="outline" onClick={() => setOpen(false)}>
                Keep it
              </Button>
              <Button variant="destructive" disabled={busy} onClick={confirm}>
                {busy ? "Deleting…" : `Delete ${what}`}
              </Button>
            </>
          }
        >
          <Field label="Reason (for the audit log)" htmlFor="deleteReason">
            <Textarea id="deleteReason" rows={2} value={reason} onChange={(e) => setReason(e.target.value)} placeholder="e.g. Entered twice by mistake" />
          </Field>
        </Modal>
      )}
    </>
  );
}
