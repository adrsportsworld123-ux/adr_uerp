// pdf.js ships its worker without types; lib/invoice-reader.ts only hands
// the module to pdf.js (globalThis.pdfjsWorker) to run it in this thread.
declare module "pdfjs-dist/build/pdf.worker.min.mjs" {
  export const WorkerMessageHandler: unknown;
}
