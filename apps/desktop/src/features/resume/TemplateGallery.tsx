import { ChevronLeft, ChevronRight, Eye, LoaderCircle, Sparkles, X } from "lucide-react";
import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import { useToast } from "../../components/Toast";
import { ApiError, exportResume, listResumeTemplates } from "../../services/api";
import type { CanonicalResume, ResumeTemplate } from "../../types";

// Mirrors the backend's seeded default (resumeATSStrictTemplateID in
// resume_store.go) — used as the fallback if GET /resume/templates fails.
const FALLBACK_TEMPLATES: ResumeTemplate[] = [
  { id: "template:ats-strict", name: "ATS Strict", category: "ats", engine: "native", isAts: true },
];

// Thumbnail only — real preview styling comes from the backend (D6).
function templateVisualKind(templateId: string) {
  if (templateId === "template:ats-clean") return "ats-clean";
  if (templateId === "template:modern-accent") return "modern-accent";
  return "ats-strict";
}

// Electron does not reliably paint PDF Blob/data URLs in a sandboxed iframe.
// PDF.js renders the same export bytes in the existing in-app modal instead.
let pdfjsPromise: Promise<typeof import("pdfjs-dist")> | null = null;

function loadPDFJS() {
  if (!pdfjsPromise) {
    pdfjsPromise = Promise.all([import("pdfjs-dist"), import("pdfjs-dist/build/pdf.worker.min.mjs?url")]).then(
      ([pdfjs, worker]) => {
        pdfjs.GlobalWorkerOptions.workerSrc = worker.default;
        return pdfjs;
      },
    );
  }
  return pdfjsPromise;
}

function base64ToBytes(base64: string): Uint8Array {
  const binary = atob(base64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i += 1) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

type TemplateGalleryProps = {
  templateId: string;
  onSelect: (templateId: string) => void;
  canonical: CanonicalResume | null;
  recommendedId?: string | null;
  recommendedReason?: string | null;
};

export function TemplateGallery({ templateId, onSelect, canonical, recommendedId, recommendedReason }: TemplateGalleryProps) {
  const toast = useToast();
  const [templates, setTemplates] = useState<ResumeTemplate[]>(FALLBACK_TEMPLATES);
  const [previewTemplate, setPreviewTemplate] = useState<ResumeTemplate | null>(null);
  const [previewBytes, setPreviewBytes] = useState<Uint8Array | null>(null);
  const [previewFetching, setPreviewFetching] = useState(false);
  const [previewRendering, setPreviewRendering] = useState(false);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const [previewPage, setPreviewPage] = useState(1);
  const [previewPageCount, setPreviewPageCount] = useState(0);
  const groupRef = useRef<HTMLDivElement>(null);
  const modalRef = useRef<HTMLDivElement>(null);
  const closeButtonRef = useRef<HTMLButtonElement>(null);
  const lastTriggerRef = useRef<HTMLButtonElement | null>(null);
  const previewCanvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    let cancelled = false;
    listResumeTemplates()
      .then((result) => {
        if (!cancelled && result.templates.length > 0) setTemplates(result.templates);
      })
      .catch(() => {
        // Keep the ATS Strict fallback already in state.
      });
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (!previewTemplate) return;
    closeButtonRef.current?.focus();
    function onKeyDown(event: globalThis.KeyboardEvent) {
      if (event.key === "Escape") closePreview();
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [previewTemplate]);

  useEffect(() => {
    const bytes = previewBytes;
    if (!bytes || !previewCanvasRef.current) return;
    let cancelled = false;
    let destroyLoadingTask: (() => Promise<void>) | null = null;

    async function renderPage() {
      setPreviewRendering(true);
      setPreviewError(null);
      try {
        const { getDocument } = await loadPDFJS();
        if (cancelled) return;
        const loadingTask = getDocument({ data: bytes!.slice() });
        destroyLoadingTask = () => loadingTask.destroy();
        const pdf = await loadingTask.promise;
        if (cancelled) return;
        setPreviewPageCount(pdf.numPages);
        const page = await pdf.getPage(Math.min(previewPage, pdf.numPages));
        if (cancelled || !previewCanvasRef.current) return;
        const viewport = page.getViewport({ scale: 1.5 });
        const canvas = previewCanvasRef.current;
        const context = canvas.getContext("2d");
        if (!context) throw new Error("Preview canvas is unavailable.");
        canvas.width = Math.ceil(viewport.width);
        canvas.height = Math.ceil(viewport.height);
        await page.render({ canvas, canvasContext: context, viewport }).promise;
      } catch (error) {
        if (!cancelled) {
          setPreviewError(error instanceof Error ? error.message : "Could not render the PDF preview.");
        }
      } finally {
        if (!cancelled) setPreviewRendering(false);
      }
    }

    void renderPage();
    return () => {
      cancelled = true;
      if (destroyLoadingTask) void destroyLoadingTask();
    };
  }, [previewBytes, previewPage]);

  function selectTemplate(template: ResumeTemplate) {
    onSelect(template.id);
    toast.success("Template selected", template.name);
  }

  async function openPreview(template: ResumeTemplate, trigger: HTMLButtonElement) {
    if (!canonical) return;
    lastTriggerRef.current = trigger;
    setPreviewTemplate(template);
    setPreviewBytes(null);
    setPreviewPage(1);
    setPreviewPageCount(0);
    setPreviewError(null);
    setPreviewFetching(true);
    try {
      const result = await exportResume(canonical, "pdf", template.id);
      setPreviewBytes(base64ToBytes(result.content));
    } catch (error) {
      toast.error("Could not load preview", error instanceof ApiError ? error.message : undefined);
      setPreviewTemplate(null);
    } finally {
      setPreviewFetching(false);
    }
  }

  function closePreview() {
    setPreviewTemplate(null);
    setPreviewBytes(null);
    setPreviewPage(1);
    setPreviewPageCount(0);
    setPreviewError(null);
    lastTriggerRef.current?.focus();
  }

  function trapPreviewFocus(event: KeyboardEvent<HTMLDivElement>) {
    if (event.key !== "Tab") return;
    const modal = modalRef.current;
    if (!modal) return;
    const focusable = [...modal.querySelectorAll<HTMLElement>(
      "a[href], button:not([disabled]), [tabindex]:not([tabindex='-1'])",
    )].filter((element) => {
      const style = window.getComputedStyle(element);
      return style.display !== "none" && style.visibility !== "hidden";
    });
    if (focusable.length === 0) return;

    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    const active = document.activeElement;
    if (event.shiftKey && (active === first || !modal.contains(active))) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && active === last) {
      event.preventDefault();
      first.focus();
    }
  }

  function handleCardKeyDown(event: KeyboardEvent<HTMLButtonElement>, index: number) {
    if (event.key !== "ArrowRight" && event.key !== "ArrowLeft") return;
    event.preventDefault();
    const next =
      event.key === "ArrowRight" ? (index + 1) % templates.length : (index - 1 + templates.length) % templates.length;
    const buttons = groupRef.current?.querySelectorAll<HTMLButtonElement>("[role='radio']");
    buttons?.[next]?.focus();
    selectTemplate(templates[next]);
  }

  return (
    <div className="resume-template-gallery">
      <div className="resume-template-cards" role="radiogroup" aria-label="Resume template" ref={groupRef}>
        {templates.map((template, index) => {
          const selected = template.id === templateId;
          const recommended = recommendedId != null && template.id === recommendedId;
          return (
            <div key={template.id} className={`resume-template-card${selected ? " is-selected" : ""}`}>
              <button
                type="button"
                role="radio"
                aria-checked={selected}
                tabIndex={selected ? 0 : -1}
                className="resume-template-card-select"
                onClick={() => selectTemplate(template)}
                onKeyDown={(event) => handleCardKeyDown(event, index)}
              >
                <span className={`resume-template-thumb is-${templateVisualKind(template.id)}`} aria-hidden="true">
                  <span className="resume-template-thumb-name" />
                  <span className="resume-template-thumb-contact" />
                  <span className="resume-template-thumb-section">
                    <i />
                    <b />
                    <b />
                  </span>
                  <span className="resume-template-thumb-section is-short">
                    <i />
                    <b />
                  </span>
                </span>
                <span className="resume-template-card-name">{template.name}</span>
                <span className={`resume-template-badge ${template.isAts ? "is-ats" : "is-visual"}`}>
                  {template.isAts ? "ATS-safe" : "Visual — may lower ATS"}
                </span>
                {recommended ? (
                  <span className="resume-template-recommended" title={recommendedReason ?? undefined}>
                    <Sparkles size={11} aria-hidden="true" />
                    Recommended
                  </span>
                ) : null}
              </button>
              <button
                type="button"
                className="secondary-button resume-template-preview-button"
                disabled={!canonical}
                title={canonical ? undefined : "Parse a resume first"}
                onClick={(event) => void openPreview(template, event.currentTarget)}
              >
                <Eye size={14} />
                Preview
              </button>
            </div>
          );
        })}
      </div>

      {previewTemplate ? (
        <div className="resume-preview-scrim" onClick={closePreview}>
          <div
            className="resume-preview-modal"
            role="dialog"
            aria-modal="true"
            aria-label={`${previewTemplate.name} preview`}
            ref={modalRef}
            onClick={(event) => event.stopPropagation()}
            onKeyDown={trapPreviewFocus}
          >
            <header className="resume-preview-modal-header">
              <h3>{previewTemplate.name} preview</h3>
              <button
                type="button"
                ref={closeButtonRef}
                className="resume-preview-close"
                aria-label="Close preview"
                onClick={closePreview}
              >
                <X size={16} />
              </button>
            </header>
            {previewFetching ? (
              <div className="resume-preview-loading">
                <LoaderCircle className="is-spinning" size={20} />
                <span>Rendering preview…</span>
              </div>
            ) : previewBytes ? (
              <>
                <div className="resume-preview-document" aria-busy={previewRendering}>
                  <canvas
                    ref={previewCanvasRef}
                    className="resume-preview-canvas"
                    role="img"
                    aria-label={`${previewTemplate.name} PDF preview, page ${previewPage}`}
                  />
                  {previewRendering ? (
                    <div className="resume-preview-loading is-overlay">
                      <LoaderCircle className="is-spinning" size={20} />
                      <span>Rendering preview…</span>
                    </div>
                  ) : null}
                </div>
                {previewError ? <p className="resume-preview-error">{previewError}</p> : null}
                {previewPageCount > 1 ? (
                  <div className="resume-preview-pagination" aria-label="Preview page navigation">
                    <button
                      type="button"
                      className="secondary-button"
                      aria-label="Previous preview page"
                      title="Previous page"
                      disabled={previewPage <= 1}
                      onClick={() => setPreviewPage((page) => Math.max(1, page - 1))}
                    >
                      <ChevronLeft size={16} />
                    </button>
                    <span>
                      Page {previewPage} of {previewPageCount}
                    </span>
                    <button
                      type="button"
                      className="secondary-button"
                      aria-label="Next preview page"
                      title="Next page"
                      disabled={previewPage >= previewPageCount}
                      onClick={() => setPreviewPage((page) => Math.min(previewPageCount, page + 1))}
                    >
                      <ChevronRight size={16} />
                    </button>
                  </div>
                ) : null}
                <p className="resume-preview-fallback">Preview is rendered from the same PDF bytes used for export.</p>
              </>
            ) : null}
          </div>
        </div>
      ) : null}
    </div>
  );
}
