"use client";

import { useState } from "react";
import { Loader2 } from "lucide-react";
import { ApiError } from "@/lib/api-client";
import { Button } from "@/components/ui/button";
import { AlertModal, Modal } from "@/components/ui/dialog";

export type EditField = {
  key: string;
  label: string;
  type?: "text" | "email" | "tel" | "number";
  required?: boolean;
  hint?: string;
  placeholder?: string;
  /** Uppercase as typed (GSTIN, codes). */
  upper?: boolean;
  /** Half-width in the two-column grid. */
  half?: boolean;
};

// A record edit form in a dialog (design system §04). Sends only the
// fields that changed; an emptied optional field is sent as "" so the API
// clears it. Closing with unsaved input asks before discarding.
export function EditDialog({
  open,
  onOpenChange,
  title,
  description,
  fields,
  initial,
  onSave,
  saveLabel = "Save changes",
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description?: string;
  fields: EditField[];
  initial: Record<string, string>;
  onSave: (changes: Record<string, string>) => Promise<void>;
  saveLabel?: string;
}) {
  const [values, setValues] = useState(initial);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [confirmDiscard, setConfirmDiscard] = useState(false);

  const changes = Object.fromEntries(
    fields.filter((f) => (values[f.key] ?? "").trim() !== (initial[f.key] ?? "").trim()).map((f) => [f.key, (values[f.key] ?? "").trim()]),
  );
  const dirty = Object.keys(changes).length > 0;

  function close() {
    if (dirty) setConfirmDiscard(true);
    else onOpenChange(false);
  }

  async function save() {
    const missing = fields.find((f) => f.required && !(values[f.key] ?? "").trim());
    if (missing) {
      setError(`${missing.label} is required.`);
      return;
    }
    setSaving(true);
    setError(null);
    try {
      await onSave(changes);
      onOpenChange(false);
    } catch (e) {
      setError(e instanceof ApiError || e instanceof Error ? e.message : "Save failed");
    } finally {
      setSaving(false);
    }
  }

  return (
    <>
      <Modal
        open={open}
        onOpenChange={(o) => (o ? null : close())}
        title={title}
        description={description}
        width={560}
        footer={
          <>
            <Button variant="outline" onClick={close} disabled={saving}>
              Cancel
            </Button>
            <Button onClick={save} disabled={saving || !dirty}>
              {saving && <Loader2 className="animate-spin" aria-hidden />}
              {saveLabel}
            </Button>
          </>
        }
      >
        <form
          className="grid grid-cols-2 gap-x-3 gap-y-4"
          onSubmit={(e) => {
            e.preventDefault();
            save();
          }}
        >
          {fields.map((f, i) => (
            <div key={f.key} className={`flex flex-col gap-1.5 ${f.half ? "col-span-2 sm:col-span-1" : "col-span-2"}`}>
              <label htmlFor={`edit-${f.key}`} className="text-sm font-medium">
                {f.label}
                {f.required && <span className="text-destructive"> *</span>}
              </label>
              <input
                id={`edit-${f.key}`}
                autoFocus={i === 0}
                type={f.type ?? "text"}
                inputMode={f.type === "number" ? "decimal" : undefined}
                min={f.type === "number" ? 0 : undefined}
                step={f.type === "number" ? "0.01" : undefined}
                placeholder={f.placeholder}
                value={values[f.key] ?? ""}
                onChange={(e) => setValues((v) => ({ ...v, [f.key]: f.upper ? e.target.value.toUpperCase() : e.target.value }))}
                className="h-9 rounded-lg border bg-card px-3 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/30"
              />
              {f.hint && <span className="text-xs text-muted-foreground">{f.hint}</span>}
            </div>
          ))}
          {error && (
            <p role="alert" className="col-span-2 text-sm text-destructive">
              {error}
            </p>
          )}
          <button type="submit" hidden />
        </form>
      </Modal>
      <AlertModal
        open={confirmDiscard}
        onOpenChange={setConfirmDiscard}
        title="Discard unsaved changes?"
        actions={
          <>
            <Button variant="outline" onClick={() => setConfirmDiscard(false)}>
              Keep editing
            </Button>
            <Button
              variant="destructive"
              onClick={() => {
                setConfirmDiscard(false);
                onOpenChange(false);
              }}
            >
              Discard
            </Button>
          </>
        }
      >
        Your edits haven&rsquo;t been saved.
      </AlertModal>
    </>
  );
}
