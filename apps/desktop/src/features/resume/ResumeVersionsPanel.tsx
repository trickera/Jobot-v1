import { Download, LoaderCircle, Pencil, RotateCcw, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { useToast } from "../../components/Toast";
import { ApiError, deleteResumeVersion, exportResume, listResumeVersions, renameResumeVersion } from "../../services/api";
import { saveExportedFile } from "../../services/nativeFile";
import type { ResumeExportFormat, ResumeVersion } from "../../types";

type ResumeVersionsPanelProps = {
  jobId?: string;
  documentId?: string;
  refreshToken: number;
  onRestore: (version: ResumeVersion) => void;
};

const DEFAULT_TEMPLATE_ID = "template:ats-strict";

function formatCreatedAt(iso: string): string {
  const date = new Date(iso);
  return Number.isNaN(date.getTime()) ? iso : date.toLocaleString();
}

// A version saved without running "Compare scores" has no scores, and the app
// stores that absence as zero. Printing it as "ATS 0 · HR 0" tells the user their
// resume scored nothing, when in truth nobody measured — the opposite of reassuring,
// and about the worst possible number to invent. Scoring is optional, so say "not
// scored" instead of asserting a failure that never happened.
function formatVersionScores(version: ResumeVersion): string {
  if (!version.atsScore && !version.hrScore) return "not scored";
  return `ATS ${version.atsScore} · HR ${version.hrScore}`;
}

export function ResumeVersionsPanel({ jobId, documentId, refreshToken, onRestore }: ResumeVersionsPanelProps) {
  const toast = useToast();
  const [versions, setVersions] = useState<ResumeVersion[]>([]);
  const [loading, setLoading] = useState(false);
  const [renamingId, setRenamingId] = useState<string | null>(null);
  const [renameValue, setRenameValue] = useState("");
  const [busyId, setBusyId] = useState<string | null>(null);
  const [exportFormatByVersion, setExportFormatByVersion] = useState<Record<string, ResumeExportFormat>>({});

  useEffect(() => {
    if (!jobId && !documentId) {
      setVersions([]);
      return;
    }
    let cancelled = false;
    setLoading(true);
    listResumeVersions({ jobId, documentId })
      .then((result) => {
        if (!cancelled) setVersions(result.versions);
      })
      .catch((error) => {
        if (!cancelled) toast.error("Could not load saved versions", error instanceof ApiError ? error.message : undefined);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [jobId, documentId, refreshToken]);

  async function handleDelete(version: ResumeVersion) {
    if (!window.confirm(`Delete "${version.name || "this version"}"? This cannot be undone.`)) return;
    setBusyId(version.id);
    try {
      await deleteResumeVersion(version.id);
      setVersions((current) => current.filter((item) => item.id !== version.id));
      toast.success("Version deleted");
    } catch (error) {
      toast.error("Could not delete version", error instanceof ApiError ? error.message : undefined);
    } finally {
      setBusyId(null);
    }
  }

  async function commitRename(version: ResumeVersion) {
    const name = renameValue.trim();
    if (!name) {
      setRenamingId(null);
      return;
    }
    setBusyId(version.id);
    try {
      await renameResumeVersion(version.id, name);
      setVersions((current) => current.map((item) => (item.id === version.id ? { ...item, name } : item)));
      toast.success("Version renamed");
    } catch (error) {
      toast.error("Could not rename version", error instanceof ApiError ? error.message : undefined);
    } finally {
      setBusyId(null);
      setRenamingId(null);
    }
  }

  async function handleExport(version: ResumeVersion) {
    const format = exportFormatByVersion[version.id] ?? "pdf";
    setBusyId(version.id);
    try {
      const result = await exportResume(version.canonical, format, version.templateId || DEFAULT_TEMPLATE_ID);
      const saved = await saveExportedFile(result.fileName, result.content, format);
      toast.success("Version exported", "path" in saved ? `Saved to ${saved.path}` : `Downloaded ${result.fileName}.`);
    } catch (error) {
      if (error instanceof Error && error.message === "Save cancelled.") return;
      toast.error("Could not export version", error instanceof ApiError ? error.message : undefined);
    } finally {
      setBusyId(null);
    }
  }

  if (!jobId && !documentId) {
    return (
      <div className="resume-versions-panel">
        <p className="resume-save-hint">Parse the resume under Resume to see its saved version history.</p>
      </div>
    );
  }

  if (loading) {
    return (
      <div className="resume-versions-panel">
        <p className="resume-save-hint">
          <LoaderCircle className="is-spinning" size={14} /> Loading versions…
        </p>
      </div>
    );
  }

  if (versions.length === 0) {
    return (
      <div className="resume-versions-panel">
        <p className="resume-save-hint">No versions saved yet.</p>
      </div>
    );
  }

  return (
    <div className="resume-versions-panel">
      <ul className="resume-versions-list">
        {versions.map((version) => {
          const busy = busyId === version.id;
          return (
            <li key={version.id} className="resume-versions-item">
              <div className="resume-versions-item-info">
                {renamingId === version.id ? (
                  <input
                    autoFocus
                    className="resume-versions-rename-input"
                    value={renameValue}
                    onChange={(event) => setRenameValue(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === "Enter") {
                        event.preventDefault();
                        void commitRename(version);
                      }
                      if (event.key === "Escape") {
                        event.preventDefault();
                        setRenamingId(null);
                      }
                    }}
                  />
                ) : (
                  <strong>{version.name || "Untitled version"}</strong>
                )}
                <span className="resume-versions-meta">
                  {formatCreatedAt(version.createdAt)} · {formatVersionScores(version)}
                </span>
              </div>
              <div className="resume-versions-item-actions">
                <button
                  type="button"
                  className="secondary-button"
                  disabled={busy}
                  onClick={() => onRestore(version)}
                  title="Restore as current"
                >
                  <RotateCcw size={14} />
                  Restore
                </button>
                <button
                  type="button"
                  className="secondary-button"
                  disabled={busy}
                  aria-label="Rename version"
                  onClick={() => {
                    setRenamingId(version.id);
                    setRenameValue(version.name ?? "");
                  }}
                >
                  <Pencil size={14} />
                </button>
                <select
                  className="resume-versions-export-format"
                  aria-label="Export format"
                  value={exportFormatByVersion[version.id] ?? "pdf"}
                  onChange={(event) =>
                    setExportFormatByVersion((current) => ({ ...current, [version.id]: event.target.value as ResumeExportFormat }))
                  }
                >
                  <option value="pdf">PDF</option>
                  <option value="docx">DOCX</option>
                  <option value="md">MD</option>
                  <option value="html">HTML</option>
                </select>
                <button type="button" className="secondary-button" disabled={busy} onClick={() => void handleExport(version)}>
                  {busy ? <LoaderCircle className="is-spinning" size={14} /> : <Download size={14} />}
                  Export
                </button>
                <button
                  type="button"
                  className="secondary-button is-danger"
                  disabled={busy}
                  aria-label="Delete version"
                  onClick={() => void handleDelete(version)}
                >
                  <Trash2 size={14} />
                </button>
              </div>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
