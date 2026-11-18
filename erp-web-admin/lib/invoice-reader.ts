// Getting a supplier's invoice ready for POST /purchase/imports/read
// (erp-core-go internal/purchase/invoice_read.go, §3.90): a typed PDF goes
// as its pages' text with positions (pdf.js, here in the browser — the
// server reads the table from them); photos and scanned PDFs go as files
// for the AI reader, photos scaled down first.

export type DocItem = { s: string; x: number; y: number; w: number };
export type DocPage = { width: number; height: number; items: DocItem[] };
export type ReadFile = { name: string; media_type: string; data: string };

// Fewer runs of text than this and the PDF is a scan (a photo inside a PDF).
const SCAN_BELOW = 15;
const PHOTO_MAX_SIDE = 2200;

const round = (v: number) => Math.round(v * 100) / 100;

/** The pages' text with positions, in points from each page's top-left; null for a scanned PDF. */
export async function pdfPages(file: File): Promise<DocPage[] | null> {
  // pdf.js in this thread (its "fake worker"): the worker module is loaded
  // as an ordinary chunk, so there's no worker file for Next to serve.
  const [pdfjs, worker] = await Promise.all([import("pdfjs-dist"), import("pdfjs-dist/build/pdf.worker.min.mjs")]);
  (globalThis as { pdfjsWorker?: unknown }).pdfjsWorker = worker;
  const doc = await pdfjs.getDocument({ data: new Uint8Array(await file.arrayBuffer()) }).promise;
  const pages: DocPage[] = [];
  let runs = 0;
  try {
    for (let n = 1; n <= doc.numPages; n++) {
      const page = await doc.getPage(n);
      const view = page.getViewport({ scale: 1 });
      const content = await page.getTextContent();
      const items: DocItem[] = [];
      for (const it of content.items) {
        if (!("str" in it) || !it.str.trim()) continue;
        items.push({ s: it.str, x: round(it.transform[4]), y: round(view.height - it.transform[5]), w: round(it.width) });
      }
      runs += items.length;
      pages.push({ width: round(view.width), height: round(view.height), items });
    }
  } finally {
    void doc.destroy();
  }
  return runs < SCAN_BELOW ? null : pages;
}

function base64(buf: ArrayBuffer): string {
  const bytes = new Uint8Array(buf);
  let s = "";
  for (let i = 0; i < bytes.length; i += 0x8000) s += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  return btoa(s);
}

/** A file as it is (a scanned PDF), base64. */
export async function asFile(file: File): Promise<ReadFile> {
  return { name: file.name, media_type: file.type || "application/pdf", data: base64(await file.arrayBuffer()) };
}

/** A photo scaled to at most 2200 px on its long side, as JPEG — sharp enough to read, small enough to send. */
export async function asPhoto(file: File): Promise<ReadFile> {
  const bitmap = await createImageBitmap(file);
  const scale = Math.min(1, PHOTO_MAX_SIDE / Math.max(bitmap.width, bitmap.height));
  const canvas = document.createElement("canvas");
  canvas.width = Math.round(bitmap.width * scale);
  canvas.height = Math.round(bitmap.height * scale);
  const ctx = canvas.getContext("2d");
  if (!ctx) throw new Error("This browser can't prepare the photo");
  ctx.fillStyle = "#fff";
  ctx.fillRect(0, 0, canvas.width, canvas.height);
  ctx.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
  bitmap.close();
  const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, "image/jpeg", 0.85));
  if (!blob) throw new Error("This browser can't prepare the photo");
  return { name: file.name.replace(/\.[^.]+$/, "") + ".jpg", media_type: "image/jpeg", data: base64(await blob.arrayBuffer()) };
}

export const isPhoto = (f: File) => /^image\/(jpeg|png|webp|heic|heif)$/.test(f.type) || /\.(jpe?g|png|webp)$/i.test(f.name);
export const isPdf = (f: File) => f.type === "application/pdf" || /\.pdf$/i.test(f.name);
