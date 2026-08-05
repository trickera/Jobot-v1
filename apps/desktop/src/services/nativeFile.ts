import type { ResumeExportFormat } from "../types";
import { isElectron } from "./runtime";

const MIME_TYPES: Record<ResumeExportFormat, string> = {
  md: "text/markdown",
  html: "text/html",
  pdf: "application/pdf",
  docx: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
};

const FORMAT_FILTERS: Record<ResumeExportFormat, { name: string; extensions: string[] }> = {
  md: { name: "Markdown", extensions: ["md"] },
  html: { name: "HTML", extensions: ["html"] },
  pdf: { name: "PDF", extensions: ["pdf"] },
  docx: { name: "Word document", extensions: ["docx"] },
};

export function base64ToBytes(base64: string): Uint8Array {
  const binary = atob(base64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i += 1) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes;
}

function downloadContentAsBlob(fileName: string, content: string, format: ResumeExportFormat) {
  const isBinary = format === "pdf" || format === "docx";
  const blob = isBinary
    ? new Blob([base64ToBytes(content).buffer as ArrayBuffer], { type: MIME_TYPES[format] })
    : new Blob([content], { type: MIME_TYPES[format] });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = fileName;
  anchor.click();
  URL.revokeObjectURL(url);
}

export type SavedFile = { path: string } | { downloaded: true };

// In Electron, show the native "Save as" dialog and write the file wherever
// the user picks. In the browser (dev, or the web fallback), keep the
// existing Blob-download behavior. Throws with message "Save cancelled." if
// the user dismisses the native dialog without picking a path — callers
// should treat that as a silent no-op, not an error.
export async function saveExportedFile(
  fileName: string,
  content: string,
  format: ResumeExportFormat,
): Promise<SavedFile> {
  const isBinary = format === "pdf" || format === "docx";

  if (isElectron()) {
    const result = isBinary
      ? await window.senciaElectron!.saveBinaryFile({ fileName, base64: content, filter: FORMAT_FILTERS[format] })
      : await window.senciaElectron!.saveTextFile({ fileName, content, filter: FORMAT_FILTERS[format] });
    if (result.canceled || !result.path) {
      throw new Error("Save cancelled.");
    }
    return { path: result.path };
  }

  downloadContentAsBlob(fileName, content, format);
  return { downloaded: true };
}

// saveTextFile is the plain-text counterpart of saveExportedFile for content
// that isn't one of the four resume export formats (e.g. a generated cover
// letter as .md/.txt). Same native "Save as" / browser-download behavior.
export async function saveTextFile(
  fileName: string,
  content: string,
  filter: { name: string; extensions: string[] },
): Promise<SavedFile> {
  if (isElectron()) {
    const result = await window.senciaElectron!.saveTextFile({ fileName, content, filter });
    if (result.canceled || !result.path) {
      throw new Error("Save cancelled.");
    }
    return { path: result.path };
  }

  const blob = new Blob([content], { type: "text/plain" });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = fileName;
  anchor.click();
  URL.revokeObjectURL(url);
  return { downloaded: true };
}

export async function openSavedFile(path: string): Promise<void> {
  await window.senciaElectron!.openPath(path);
}

export async function revealSavedFile(path: string): Promise<void> {
  await window.senciaElectron!.showItemInFolder(path);
}
