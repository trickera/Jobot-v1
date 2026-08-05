import { Download, ExternalLink, FolderOpen, LoaderCircle } from "lucide-react";
import { useState } from "react";
import { useToast } from "../../components/Toast";
import { ApiError, exportResume } from "../../services/api";
import { openSavedFile, revealSavedFile, saveExportedFile } from "../../services/nativeFile";
import type { CanonicalResume, ResumeExportFormat, ResumePageSize } from "../../types";

type ResumeExportPanelProps = {
  canonical: CanonicalResume;
  templateId: string;
  onError: (message: string) => void;
};

const EXPORT_OPTIONS: Array<{ format: ResumeExportFormat; label: string }> = [
  { format: "md", label: "MD" },
  { format: "html", label: "HTML" },
  { format: "pdf", label: "PDF" },
  { format: "docx", label: "DOCX (Word)" },
];

export function ResumeExportPanel({ canonical, templateId, onError }: ResumeExportPanelProps) {
  const toast = useToast();
  const [pageSize, setPageSize] = useState<ResumePageSize>("letter");
  const [busyFormat, setBusyFormat] = useState<ResumeExportFormat | null>(null);
  const [lastSavedPath, setLastSavedPath] = useState<string | null>(null);

  async function handleExport(format: ResumeExportFormat) {
    setBusyFormat(format);
    try {
      const result = await exportResume(canonical, format, templateId, pageSize);
      const saved = await saveExportedFile(result.fileName, result.content, format);
      if ("path" in saved) {
        setLastSavedPath(saved.path);
        toast.success("Resume exported", `Saved to ${saved.path}`);
      } else {
        setLastSavedPath(null);
        toast.success("Resume exported", `Downloaded ${result.fileName}.`);
      }
    } catch (error) {
      if (error instanceof Error && error.message === "Save cancelled.") return;
      const message = error instanceof ApiError ? error.message : "Export failed.";
      toast.error("Export failed", message);
      onError(message);
    } finally {
      setBusyFormat(null);
    }
  }

  async function handleOpenFile() {
    if (!lastSavedPath) return;
    try {
      await openSavedFile(lastSavedPath);
    } catch (error) {
      onError(error instanceof Error ? error.message : "Could not open the file.");
    }
  }

  async function handleOpenFolder() {
    if (!lastSavedPath) return;
    try {
      await revealSavedFile(lastSavedPath);
    } catch (error) {
      onError(error instanceof Error ? error.message : "Could not open the folder.");
    }
  }

  return (
    <div className="resume-export-panel">
      <label className="settings-field resume-page-size">
        <span>Page size</span>
        <select value={pageSize} onChange={(event) => setPageSize(event.target.value as ResumePageSize)}>
          <option value="letter">Letter (default)</option>
          <option value="a4">A4</option>
        </select>
      </label>
      <div className="resume-export-actions">
        {EXPORT_OPTIONS.map(({ format, label }) => (
          <button
            key={format}
            type="button"
            className="secondary-button"
            disabled={busyFormat !== null}
            onClick={() => void handleExport(format)}
          >
            {busyFormat === format ? <LoaderCircle className="is-spinning" size={14} /> : <Download size={14} />}
            {label}
          </button>
        ))}
      </div>
      {lastSavedPath ? (
        <div className="resume-export-saved">
          <p className="resume-export-saved-path">Saved to {lastSavedPath}</p>
          <div className="resume-export-saved-actions">
            <button type="button" className="secondary-button" onClick={() => void handleOpenFile()}>
              <ExternalLink size={14} />
              Open file
            </button>
            <button type="button" className="secondary-button" onClick={() => void handleOpenFolder()}>
              <FolderOpen size={14} />
              Open folder
            </button>
          </div>
        </div>
      ) : null}
    </div>
  );
}
