"use client";

// Download / print for the server-rendered business documents (erp-core-go
// internal/documents): sale receipts, GRNs, purchase bills and purchase
// returns, each under its branch's letterhead. The PDF comes from the API,
// not the browser, so it is byte-for-byte what the POS downloads too.
import { useState } from "react";
import { Download, Printer } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@/lib/api-client";
import { Button } from "@/components/ui/button";

export function saveBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 10_000);
}

type Size = "sm" | "default" | "icon-sm";

/** A "Download PDF" button for one document. `path` is the API path of the PDF. */
export function DownloadPdfButton({
  path,
  label = "Download PDF",
  variant = "outline",
  size = "sm",
}: {
  path: string;
  label?: string;
  variant?: "outline" | "ghost" | "default" | "secondary";
  size?: Size;
}) {
  const [busy, setBusy] = useState(false);
  async function run(e: React.MouseEvent) {
    e.stopPropagation(); // used inside clickable table rows too
    setBusy(true);
    try {
      const { blob, filename } = await api.download(path);
      saveBlob(blob, filename);
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : "Could not download the document");
    } finally {
      setBusy(false);
    }
  }
  const icon = <Download />;
  if (size === "icon-sm") {
    return (
      <Button variant={variant === "outline" ? "ghost" : variant} size="icon-sm" aria-label={label} title={label} disabled={busy} onClick={run}>
        {icon}
      </Button>
    );
  }
  return (
    <Button variant={variant} size={size} disabled={busy} onClick={run}>
      {icon}
      {busy ? "Preparing…" : label}
    </Button>
  );
}

/**
 * Opens the PDF in a new tab for the browser's own print dialog. The tab is
 * opened synchronously on the click (before the fetch) so a popup blocker
 * treats it as user-initiated.
 */
export function PrintPdfButton({ path, label = "Print" }: { path: string; label?: string }) {
  const [busy, setBusy] = useState(false);
  async function run() {
    const tab = window.open("", "_blank");
    setBusy(true);
    try {
      const { blob } = await api.download(path);
      const url = URL.createObjectURL(blob);
      if (tab) tab.location.href = url;
      else window.location.href = url;
      setTimeout(() => URL.revokeObjectURL(url), 60_000);
    } catch (err) {
      tab?.close();
      toast.error(err instanceof ApiError ? err.message : "Could not open the document");
    } finally {
      setBusy(false);
    }
  }
  return (
    <Button variant="outline" size="sm" disabled={busy} onClick={run}>
      <Printer />
      {label}
    </Button>
  );
}
