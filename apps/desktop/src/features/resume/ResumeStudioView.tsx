import { ClipboardType, FileText, LoaderCircle, RotateCcw, ScanText, Sparkles } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { useToast } from "../../components/Toast";
import {
  ApiError,
  analyzeJob,
  diagnoseResume,
  gapAnalysis,
  getOCRStatus,
  installOCR,
  loadConfig,
  optimizeResume,
  parseResume,
  runOCR,
  saveResumeVersion,
  scoreResume,
  uploadResume,
} from "../../services/api";
import type {
  AtsIssue,
  AtsScores,
  CanonicalResume,
  GapResult,
  JobRequirements,
  JobSummary,
  JsonPatchOp,
  OCRRunRequest,
  ResumeLanguageOption,
  ResumeVersion,
  ResumeVoiceOption,
  ScoreResult,
} from "../../types";
import { CoverLetterPanel } from "./CoverLetterPanel";
import { JobGapAnalysis } from "./JobGapAnalysis";
import { JobPicker } from "./JobPicker";
import { ResumeDiagnosisPanel } from "./ResumeDiagnosisPanel";
import { ResumeDiffPreview } from "./ResumeDiffPreview";
import { ResumeExportPanel } from "./ResumeExportPanel";
import { ResumeUploadPanel } from "./ResumeUploadPanel";
import { ResumeProgress } from "./ResumeProgress";
import { ScoreComparison } from "./ScoreComparison";
import { ResumeVersionsPanel } from "./ResumeVersionsPanel";
import { applyResumePatchesDetailed } from "./resumeJsonPatch";
import { recommendTemplate } from "./resumeTemplateRecommendation";
import { createStepToast } from "./stepToast";
import { TemplateGallery } from "./TemplateGallery";
import "./ResumePrecision.css";

const ATS_STRICT_TEMPLATE_ID = "template:ats-strict";

// A blank CanonicalResume placeholder for the offline ATS diagnosis call:
// the backend recognizes an empty canonical and builds a best-effort
// structure directly from rawText (no AI parse, no API key needed), so this
// same call can run before/without ever hitting the AI-gated /resume/parse
// route.
const emptyCanonicalResume: CanonicalResume = {
  schemaVersion: 2,
  basics: { name: "", headline: "", email: "", phone: "", location: "", links: [] },
  target: { jobTitle: "", category: "", seniority: "" },
  summary: "",
  skills: { hard: [], soft: [], tools: [] },
  experience: [],
  education: [],
  projects: [],
  licenses: [],
  certifications: [],
  languages: [],
};

type ResumeInputMode = "upload" | "paste";

function fileToBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onerror = () => reject(new Error("Could not read the file."));
    reader.onload = () => {
      const result = String(reader.result ?? "");
      resolve(result.includes(",") ? result.split(",")[1] : result);
    };
    reader.readAsDataURL(file);
  });
}

function wait(ms: number) {
  return new Promise((resolve) => window.setTimeout(resolve, ms));
}

export function scrollResultIntoView(element: HTMLElement | null) {
  window.requestAnimationFrame(() => element?.scrollIntoView({ behavior: "smooth", block: "start" }));
}

// A patch carrying a reviewRisk (new_skill, unsupported_claim, new_metric,
// high_rewrite_distance — identity_change/new_certification never reach the
// client, the backend already routes those to `rejected`) is exactly the
// "human half of the gate" ResumeDiffPreview's badges describe: something the
// deterministic backend gate could not fact-check on its own. Defaulting it
// to accepted made that badge decorative — the E2E run against real Gemini
// shipped an unconfirmed "mobile-first" claim into the exported resume this
// way. Only risk-free patches start checked; a flagged one requires the
// user's own click.
export function defaultAcceptedPatchIndexes(patches: JsonPatchOp[]): Set<number> {
  const accepted = new Set<number>();
  patches.forEach((patch, index) => {
    if (!patch.reviewRisk) accepted.add(index);
  });
  return accepted;
}

type Notice = { tone: "neutral" | "error"; text: string };

type ResumeStudioViewProps = {
  job?: JobSummary | null;
  jobRequest?: number;
};

function StudioBlockHeader({
  eyebrow,
  title,
  description,
}: {
  eyebrow: string;
  title: string;
  description: string;
}) {
  return (
    <header className="resume-block-header">
      <span className="resume-block-eyebrow">{eyebrow}</span>
      <div>
        <h2>{title}</h2>
        <p>{description}</p>
      </div>
    </header>
  );
}

function providerDescription(provider?: string) {
  return provider ? ` via ${provider}` : "";
}

export function ResumeStudioView({ job, jobRequest = 0 }: ResumeStudioViewProps) {
  const toast = useToast();
  const [inputMode, setInputMode] = useState<ResumeInputMode>("upload");
  const [resumeText, setResumeText] = useState("");
  const [resumeFileName, setResumeFileName] = useState<string | null>(null);
  const [uploading, setUploading] = useState(false);
  const [documentId, setDocumentId] = useState<string | null>(null);
  const [canonical, setCanonical] = useState<CanonicalResume | null>(null);
  // Populated by the offline (no AI key) diagnosis call when the backend had
  // to build a best-effort structure from raw text (buildHeuristicCanonical).
  // Lets a user with no AI key export the extracted/heuristic resume without
  // ever reaching the AI-gated "Parse resume" step (QA-002).
  const [heuristicCanonical, setHeuristicCanonical] = useState<CanonicalResume | null>(null);
  const [diagnosis, setDiagnosis] = useState<{ scores: AtsScores; issues: AtsIssue[] } | null>(null);

  const [selectedJob, setSelectedJob] = useState<JobSummary | null>(job ?? null);
  const [jobDescription, setJobDescription] = useState(job?.description ?? "");
  const [requirements, setRequirements] = useState<JobRequirements | null>(null);
  const [gap, setGap] = useState<GapResult | null>(null);
  const [confirmed, setConfirmed] = useState<Set<string>>(new Set());

  const [language, setLanguage] = useState<ResumeLanguageOption>("en");
  const [voice, setVoice] = useState<ResumeVoiceOption>("third");
  const [reviewOpen, setReviewOpen] = useState(false);
  const [patches, setPatches] = useState<JsonPatchOp[]>([]);
  const [rejectedPatches, setRejectedPatches] = useState<JsonPatchOp[]>([]);
  const [acceptedIndexes, setAcceptedIndexes] = useState<Set<number>>(new Set());
  // The backend's own patched resume (validated + applied with real RFC 6902
  // semantics via evanphx/json-patch). Authoritative whenever every patch is
  // accepted — the client-side applier is only a convenience for partial
  // accept/reject toggling.
  const [serverPreview, setServerPreview] = useState<CanonicalResume | null>(null);

  const [scoreBefore, setScoreBefore] = useState<ScoreResult | null>(null);
  const [scoreAfter, setScoreAfter] = useState<ScoreResult | null>(null);
  const [templateId, setTemplateId] = useState(ATS_STRICT_TEMPLATE_ID);
  const recommendation = useMemo(() => recommendTemplate(requirements), [requirements]);

  const [busyStep, setBusyStep] = useState<string | null>(null);
  const [notice, setNotice] = useState<Notice | null>(null);
  const [saveFeedback, setSaveFeedback] = useState<{ tone: "neutral" | "error"; text: string } | null>(null);
  const [versionsRefreshToken, setVersionsRefreshToken] = useState(0);
  const [showExtractedPreview, setShowExtractedPreview] = useState(false);
  const [scannedPdfWarning, setScannedPdfWarning] = useState(false);
  const [ocrCandidate, setOCRCandidate] = useState<OCRRunRequest | null>(null);
  const [ocrReviewText, setOCRReviewText] = useState("");
  const [ocrReviewOpen, setOCRReviewOpen] = useState(false);
  const [ocrStatusText, setOCRStatusText] = useState<string | null>(null);
  const [resetPending, setResetPending] = useState(false);
  const pasteTextareaRef = useRef<HTMLTextAreaElement | null>(null);
  const externalJobRequestRef = useRef<number | null>(null);
  // Bumped whenever the working context changes (job switch, reset, version
  // restore). An async AI response captured under an older epoch is discarded
  // instead of overwriting state that now belongs to a different job/resume.
  const sessionEpochRef = useRef(0);
  const optimizeResultRef = useRef<HTMLDivElement | null>(null);
  const scoreResultRef = useRef<HTMLDivElement | null>(null);
  const saveResultRef = useRef<HTMLDivElement | null>(null);
  // Diagnosis ordering: the offline (fire-and-forget) diagnosis and the
  // post-parse AI diagnosis both call setDiagnosis. Each call takes a
  // sequence number at start; a stale (lower-seq) response never overwrites
  // a newer one.
  const diagnosisSeqRef = useRef(0);
  const appliedDiagnosisSeqRef = useRef(0);

  function scrollToResult(ref: { current: HTMLDivElement | null }) {
    scrollResultIntoView(ref.current);
  }
  const [profileResumeText, setProfileResumeText] = useState<string | null>(null);
  const [profileResumeName, setProfileResumeName] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    loadConfig()
      .then((config) => {
        if (cancelled) return;
        setProfileResumeText(config.form.resumeText ?? "");
        setProfileResumeName(config.form.resumeName ?? "");
      })
      .catch(() => {
        // Silent: the profile-resume shortcut is optional, users can still upload/paste.
      });
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (!job) return;
    if (externalJobRequestRef.current === jobRequest) return;
    externalJobRequestRef.current = jobRequest;
    sessionEpochRef.current += 1;
    setSelectedJob(job);
    setJobDescription(job.description ?? "");
    setRequirements(null);
    setGap(null);
    setConfirmed(new Set());
    setReviewOpen(false);
    setPatches([]);
    setRejectedPatches([]);
    setServerPreview(null);
    setAcceptedIndexes(new Set());
    setScoreBefore(null);
    setScoreAfter(null);
  }, [job, jobRequest]);

  const patchApplication = useMemo(() => {
    if (!canonical) return { finalCanonical: null as CanonicalResume | null, failedPaths: [] as string[] };
    const accepted = patches.filter((_, index) => acceptedIndexes.has(index));
    if (accepted.length === 0) return { finalCanonical: canonical, failedPaths: [] as string[] };
    // Default state (everything accepted): use the backend's own validated
    // result byte-for-byte instead of re-deriving it client-side.
    if (serverPreview && accepted.length === patches.length) {
      return { finalCanonical: serverPreview, failedPaths: [] as string[] };
    }
    const { resume, failedIndexes } = applyResumePatchesDetailed(canonical, accepted);
    return {
      finalCanonical: resume,
      failedPaths: failedIndexes.map((i) => accepted[i]?.path ?? "?"),
    };
  }, [canonical, patches, acceptedIndexes, serverPreview]);
  const finalCanonical = patchApplication.finalCanonical;

  // What the Deliver block actually exports: the fully AI-parsed
  // (and possibly tailored) resume when available, otherwise the offline
  // heuristic resume - so export never requires an AI key (QA-002).
  const exportCanonical = finalCanonical ?? heuristicCanonical;
  const hasWorkingSession = Boolean(
    resumeText ||
      resumeFileName ||
      documentId ||
      canonical ||
      heuristicCanonical ||
      diagnosis ||
      selectedJob ||
      jobDescription ||
      requirements ||
      gap ||
      patches.length ||
      rejectedPatches.length,
  );

  function fail(error: unknown, fallback: string) {
    const message = error instanceof ApiError ? error.message : fallback;
    setNotice({ tone: "error", text: message });
    toast.error(fallback, error instanceof ApiError ? error.message : undefined);
  }

  // Offline ATS diagnosis: works with no AI key at all (the backend builds a
  // best-effort structure from rawText when no canonical is provided yet).
  // Called right after upload/paste so the diagnostic panel shows up
  // immediately, without requiring the AI-gated "Parse resume" step first.
  async function runOfflineDiagnosis(text: string) {
    const trimmed = text.trim();
    if (!trimmed) return;
    const seq = ++diagnosisSeqRef.current;
    try {
      const diag = await diagnoseResume(emptyCanonicalResume, trimmed);
      if (seq < appliedDiagnosisSeqRef.current) return; // an AI diagnosis landed meanwhile
      appliedDiagnosisSeqRef.current = seq;
      setDiagnosis(diag);
      if (diag.heuristic) {
        setHeuristicCanonical(diag.canonical);
      }
    } catch {
      // Best-effort convenience shown right after upload/paste; if it fails,
      // the user still gets a diagnosis after Parse resume, so stay silent
      // here instead of piling another error notice on top of the upload one.
    }
  }

  async function handleResumeFile(file: File) {
    setUploading(true);
    setNotice(null);
    setScannedPdfWarning(false);
    setOCRCandidate(null);
    setOCRReviewOpen(false);
    setOCRReviewText("");
    setOCRStatusText(null);
    const pending = toast.loading("Reading resume file…", file.name);
    try {
      const contentBase64 = await fileToBase64(file);
      const result = await uploadResume({ fileName: file.name, mimeType: file.type, contentBase64 });
      const extracted = result.extractedText?.trim() || result.markdown?.trim() || "";
      const scannedSuspected = result.warnings?.includes("scanned_pdf_suspected") ?? false;
      if (scannedSuspected) {
        setOCRCandidate({ fileName: file.name, mimeType: file.type, contentBase64 });
      }
      if (!extracted) {
        setScannedPdfWarning(scannedSuspected);
        pending.update({
          tone: "error",
          title: "No text found in this file",
          description: "If it's a scanned PDF, it needs OCR. Try a text-based PDF, DOCX or TXT.",
        });
        return;
      }
      setResumeText(extracted);
      setResumeFileName(result.fileName);
      setScannedPdfWarning(scannedSuspected);
      if (scannedSuspected) {
        pending.update({
          tone: "error",
          title: "This PDF looks scanned",
          description: "Very little text could be extracted — it may be an image-only PDF. Review the options below.",
        });
      } else {
        pending.update({
          tone: "success",
          title: "Resume file loaded",
          description: `${result.fileName} · offline ATS diagnosis below, or click Parse resume for AI structuring.`,
        });
        void runOfflineDiagnosis(extracted);
      }
    } catch (error) {
      const description =
        error instanceof ApiError
          ? error.message
          : error instanceof Error
            ? error.message
            : "Please try another file.";
      pending.update({
        tone: "error",
        title: "Could not read the resume file",
        description,
      });
    } finally {
      setUploading(false);
    }
  }

  async function ensureOCRInstalled() {
    let status = await getOCRStatus();
    if (status.installed) return status;
    if (!status.installing) {
      try {
        await installOCR();
      } catch (error) {
        if (!(error instanceof ApiError) || error.status !== 409) {
          throw error;
        }
      }
    }
    for (let attempt = 0; attempt < 180; attempt += 1) {
      await wait(2000);
      status = await getOCRStatus();
      setOCRStatusText(status.installMessage ?? "Installing OCR components...");
      if (status.installed) return status;
      if (!status.installing && status.installMessage?.toLowerCase().includes("failed")) {
        throw new ApiError(status.installMessage, 502, "ocr_install_failed");
      }
    }
    throw new ApiError("OCR installation is still running. Try again in a moment.", 504, "ocr_install_timeout");
  }

  async function handleSetUpOCR() {
    if (!ocrCandidate) {
      toast.info("Upload the scanned PDF again before running OCR.");
      return;
    }
    setBusyStep("ocr");
    setNotice(null);
    setOCRStatusText("Checking OCR components...");
    const pending = createStepToast(
      (title) => toast.loading(title),
      "Setting up OCR...",
      "OCR runs locally and the text will be shown for review before AI parsing.",
    );
    try {
      const status = await getOCRStatus();
      if (!status.installed) {
        pending.step(status.installing ? "Waiting for OCR installation..." : "Installing OCR components...");
        await ensureOCRInstalled();
      }
      pending.step("Running OCR locally...");
      const result = await runOCR(ocrCandidate);
      setOCRReviewText(result.text);
      setOCRReviewOpen(true);
      setOCRStatusText(`${result.pages} page(s) converted. Review the text before parsing.`);
      pending.success(
        "OCR text ready",
        `${result.pages} page(s) converted locally. Review the text before sending it to AI.`,
      );
      scrollToResult(optimizeResultRef);
    } catch (error) {
      const message = error instanceof ApiError ? error.message : "OCR setup or extraction failed.";
      setOCRStatusText(message);
      pending.error("OCR failed", message);
      setNotice({ tone: "error", text: message });
    } finally {
      setBusyStep(null);
    }
  }

  async function handleParse(textOverride?: string) {
    const textToParse = (textOverride ?? resumeText).trim();
    if (!textToParse) {
      const message =
        inputMode === "upload" ? "Upload a resume file first." : "Paste your resume text first.";
      setNotice({ tone: "error", text: message });
      toast.info(message);
      return;
    }
    if (textOverride !== undefined) {
      setResumeText(textToParse);
      setInputMode("paste");
      setScannedPdfWarning(false);
      setOCRReviewOpen(false);
    }
    setShowExtractedPreview(false);
    setBusyStep("parse");
    setNotice(null);
    // Offline diagnosis never needs an AI key, so run it up front - a user
    // with no key still gets a real ATS diagnostic even if the AI parse
    // below fails or is never configured (BUG-04).
    void runOfflineDiagnosis(textToParse);
    const pending = createStepToast((title) => toast.loading(title), "Structuring resume with AI…");
    try {
      const result = await parseResume(textToParse);
      setDocumentId(result.documentId);
      setCanonical(result.canonical);
      pending.step("Running ATS diagnosis (offline)…");
      const seq = ++diagnosisSeqRef.current;
      const diag = await diagnoseResume(result.canonical, textToParse);
      if (seq >= appliedDiagnosisSeqRef.current) {
        appliedDiagnosisSeqRef.current = seq;
        setDiagnosis(diag);
      }
      const inferredName = result.warnings?.includes("name_inferred_from_first_line") ?? false;
      pending.success(
        "Resume parsed",
        inferredName
          ? `Name taken from the top of your file ("${result.canonical.basics.name}") - edit the text and re-parse if wrong.${providerDescription(result.providerUsed)}`
          : `${result.canonical.basics.name || "Resume"} · ATS diagnosis ready below.${providerDescription(result.providerUsed)}`,
      );
    } catch (error) {
      const apiError = error instanceof ApiError ? error : null;
      const message = apiError?.message ?? "Could not parse the resume. Check that an AI key is configured in Settings.";
      pending.error("Could not parse the resume", message);
      if (apiError?.code === "missing_name") {
        setShowExtractedPreview(true);
      }
      setNotice({ tone: "error", text: message });
    } finally {
      setBusyStep(null);
    }
  }

  async function handleAnalyzeJob() {
    if (!jobDescription.trim() && !selectedJob?.id) {
      setNotice({ tone: "error", text: "Paste a job description first." });
      return;
    }
    setBusyStep("analyze-job");
    setNotice(null);
    const epoch = sessionEpochRef.current;
    const pending = createStepToast((title) => toast.loading(title), "Sending job description to AI…");
    try {
      const result = await analyzeJob({ jobId: selectedJob?.id, description: jobDescription || undefined });
      if (epoch !== sessionEpochRef.current) {
        pending.error("Result discarded", "The job/resume selection changed while the AI was working — run Analyze job again.");
        return;
      }
      setRequirements(result.requirements);
      setGap(null);
      setConfirmed(new Set());
      pending.success(
        "Job analyzed",
        `${result.requirements.hardRequirements?.length ?? 0} key requirements detected.${providerDescription(result.providerUsed)}`,
      );
    } catch (error) {
      pending.error(
        "Could not analyze the job",
        error instanceof ApiError ? error.message : "Check the job description or your AI key.",
      );
      setNotice({ tone: "error", text: error instanceof ApiError ? error.message : "Could not analyze the job description." });
    } finally {
      setBusyStep(null);
    }
  }

  async function handleFindGaps() {
    if (!canonical || !requirements) return;
    setBusyStep("gap");
    setNotice(null);
    const pending = createStepToast(
      (title) => toast.loading(title),
      "Comparing resume against job requirements…",
      "A long job description can take a little longer.",
    );
    const epoch = sessionEpochRef.current;
    try {
      const result = await gapAnalysis(canonical, requirements);
      if (epoch !== sessionEpochRef.current) {
        pending.error("Result discarded", "The job/resume selection changed while the AI was working — run Find gaps again.");
        return;
      }
      setGap(result.gap);
      const missing = result.gap.missing?.length ?? 0;
      const toConfirm = result.gap.toConfirm?.length ?? 0;
      pending.success("Gap analysis ready", `${missing} missing · ${toConfirm} to confirm.${providerDescription(result.providerUsed)}`);
    } catch (error) {
      pending.error(
        "Could not run gap analysis",
        error instanceof ApiError ? error.message : "Check your AI key and try again.",
      );
      setNotice({ tone: "error", text: error instanceof ApiError ? error.message : "Could not run the gap analysis." });
    } finally {
      setBusyStep(null);
    }
  }

  function toggleConfirm(term: string) {
    const willConfirm = !confirmed.has(term);
    setConfirmed((current) => {
      const next = new Set(current);
      if (next.has(term)) next.delete(term);
      else next.add(term);
      return next;
    });
    // Persist the confirmation onto the canonical resume so it is durable:
    // it flows into the next optimize/gap call and is saved with the version,
    // marked separately from the originally-parsed skills.
    setCanonical((current) => {
      if (!current) return current;
      const existing = current.confirmedSkills ?? [];
      const has = existing.some((s) => s.toLowerCase() === term.toLowerCase());
      if (willConfirm === has) return current;
      const confirmedSkills = willConfirm
        ? [...existing, term]
        : existing.filter((s) => s.toLowerCase() !== term.toLowerCase());
      return { ...current, confirmedSkills };
    });
  }

  async function handleOptimize() {
    if (!canonical || !requirements) return;
    setBusyStep("optimize");
    setNotice(null);
    const pending = createStepToast(
      (title) => toast.loading(title),
      "Generating tailored resume with AI…",
      "A large resume or a busy provider can take a little longer.",
    );
    const epoch = sessionEpochRef.current;
    try {
      const result = await optimizeResume(canonical, requirements, [...confirmed], language, voice);
      if (epoch !== sessionEpochRef.current) {
        pending.error("Result discarded", "The job/resume selection changed while the AI was working — run Optimize again.");
        return;
      }
      setPatches(result.patches);
      setRejectedPatches(result.rejected);
      setServerPreview(result.preview ?? null);
      setAcceptedIndexes(defaultAcceptedPatchIndexes(result.patches));
      const rejectedNote = result.rejected.length
        ? ` ${result.rejected.length} change(s) were blocked by the anti-invention gate.`
        : "";
      pending.success(
        "Tailored resume generated",
        `${result.patches.length} suggested change(s) — review and accept below.${rejectedNote}${providerDescription(result.providerUsed)}`,
      );
    } catch (error) {
      pending.error(
        "Could not generate the tailored resume",
        error instanceof ApiError ? error.message : "Check your AI key and try again.",
      );
      setNotice({ tone: "error", text: error instanceof ApiError ? error.message : "Could not optimize the resume." });
    } finally {
      setBusyStep(null);
    }
  }

  function toggleAccepted(index: number) {
    setAcceptedIndexes((current) => {
      const next = new Set(current);
      if (next.has(index)) next.delete(index);
      else next.add(index);
      return next;
    });
  }

  async function handleCompareScores() {
    if (!canonical || !requirements || !finalCanonical) return;
    setBusyStep("score");
    setNotice(null);
    const epoch = sessionEpochRef.current;
    try {
      const [before, after] = await Promise.all([
        scoreResume(canonical, requirements, resumeText),
        scoreResume(finalCanonical, requirements, resumeText),
      ]);
      if (epoch !== sessionEpochRef.current) return;
      setScoreBefore(before);
      setScoreAfter(after);
      toast.success(
        "Scores updated",
        `ATS ${before.ats}→${after.ats} · HR ${before.hr}→${after.hr}.`,
      );
      scrollToResult(scoreResultRef);
    } catch (error) {
      fail(error, "Could not compute ATS/HR scores.");
    } finally {
      setBusyStep(null);
    }
  }

  async function handleSaveVersion() {
    if (!documentId) {
      const message = "Parse the resume first so a document exists before saving a version.";
      setSaveFeedback({ tone: "error", text: message });
      toast.info(message);
      return;
    }
    if (!finalCanonical) {
      const message = "No tailored resume to save yet.";
      setSaveFeedback({ tone: "error", text: message });
      toast.info(message);
      return;
    }
    setBusyStep("save");
    setNotice(null);
    setSaveFeedback(null);
    const pending = toast.loading("Saving resume version…");
    try {
      const { id } = await saveResumeVersion({
        documentId,
        jobId: selectedJob?.id,
        canonical: finalCanonical,
        patches: patches.filter((_, index) => acceptedIndexes.has(index)),
        templateId,
        atsScore: scoreAfter?.ats ?? 0,
        hrScore: scoreAfter?.hr ?? 0,
        gap: gap ?? { found: [], partial: [], missing: [], toConfirm: [] },
      });
      const jobNote = selectedJob ? ` Linked to "${selectedJob.title}" at ${selectedJob.company}.` : "";
      pending.update({
        tone: "success",
        title: "Version saved",
        description: `Stored locally in your database.${jobNote}`,
      });
      setSaveFeedback({
        tone: "neutral",
        text: `Version saved (${id}). Stored locally in SQLite (resume_versions).${jobNote} Export the PDF from here anytime.`,
      });
      setVersionsRefreshToken((token) => token + 1);
      scrollToResult(saveResultRef);
    } catch (error) {
      const message = error instanceof ApiError ? error.message : "Could not save this version.";
      setSaveFeedback({ tone: "error", text: message });
      pending.update({
        tone: "error",
        title: "Could not save this version",
        description: message,
      });
    } finally {
      setBusyStep(null);
    }
  }

  function handlePickJob(picked: JobSummary) {
    sessionEpochRef.current += 1;
    setSelectedJob(picked);
    setJobDescription(picked.description ?? "");
    setRequirements(null);
    setGap(null);
    setConfirmed(new Set());
    setReviewOpen(false);
    setPatches([]);
    setRejectedPatches([]);
    setServerPreview(null);
    setAcceptedIndexes(new Set());
    setScoreBefore(null);
    setScoreAfter(null);
    toast.info("Job selected", `${picked.title} at ${picked.company}`);
  }

  function handleUseProfileResume() {
    if (!profileResumeText?.trim()) return;
    setInputMode("paste");
    setResumeText(profileResumeText);
    setResumeFileName(profileResumeName || null);
    toast.success("Profile resume loaded", profileResumeName || undefined);
  }

  function resetStudioSession() {
    sessionEpochRef.current += 1;
    setInputMode("upload");
    setResumeText("");
    setResumeFileName(null);
    setDocumentId(null);
    setCanonical(null);
    setHeuristicCanonical(null);
    setDiagnosis(null);
    setSelectedJob(null);
    setJobDescription("");
    setRequirements(null);
    setGap(null);
    setConfirmed(new Set());
    setReviewOpen(false);
    setPatches([]);
    setRejectedPatches([]);
    setServerPreview(null);
    setAcceptedIndexes(new Set());
    setScoreBefore(null);
    setScoreAfter(null);
    setTemplateId(ATS_STRICT_TEMPLATE_ID);
    setNotice(null);
    setSaveFeedback(null);
    setShowExtractedPreview(false);
    setScannedPdfWarning(false);
    setOCRCandidate(null);
    setOCRReviewText("");
    setOCRReviewOpen(false);
    setOCRStatusText(null);
    setResetPending(false);
    toast.info("Resume Studio reset", "Your profile resume and saved versions were not changed.");
  }

  function handleRestoreVersion(version: ResumeVersion) {
    sessionEpochRef.current += 1;
    setDocumentId(version.documentId);
    setCanonical(version.canonical);
    setTemplateId(version.templateId || ATS_STRICT_TEMPLATE_ID);
    setPatches([]);
    setRejectedPatches([]);
    setServerPreview(null);
    setAcceptedIndexes(new Set());
    setScoreBefore(null);
    setScoreAfter(null);
    setNotice({ tone: "neutral", text: `Restored "${version.name || "this version"}" as the current resume.` });
    toast.success("Version restored", version.name || undefined);
  }

  return (
    <section className="workspace resume-studio" aria-labelledby="resume-studio-title">
      <div className="workspace-header">
        <div className="resume-studio-heading">
          <h1 id="resume-studio-title">Resume Studio</h1>
          <p>Check your resume, match it to a role and export an evidence-backed version.</p>
          {hasWorkingSession ? (
            <div className="resume-session-actions">
              {resetPending ? (
                <>
                  <span className="resume-reset-confirmation" role="status" aria-live="polite">
                    Clear this working session?
                  </span>
                  <button
                    className="secondary-button resume-reset-button is-confirm"
                    type="button"
                    disabled={busyStep !== null || uploading}
                    onClick={resetStudioSession}
                  >
                    <RotateCcw size={14} />
                    Confirm reset
                  </button>
                  <button className="secondary-button" type="button" onClick={() => setResetPending(false)}>
                    Cancel
                  </button>
                </>
              ) : (
                <button
                  className="secondary-button resume-reset-button"
                  type="button"
                  disabled={busyStep !== null || uploading}
                  onClick={() => setResetPending(true)}
                >
                  <RotateCcw size={14} />
                  Start over
                </button>
              )}
            </div>
          ) : null}
        </div>
      </div>

      <div className="resume-workflow-overview" aria-label="Resume Studio workflow overview">
        <ol>
          <li className={exportCanonical ? "is-ready" : "is-current"}>
            <span className="resume-workflow-index">01</span>
            <span><strong>Resume</strong><small>{exportCanonical ? "Ready" : "Add resume"}</small></span>
          </li>
          <li className={requirements ? "is-ready" : exportCanonical ? "is-current" : ""}>
            <span className="resume-workflow-index">02</span>
            <span><strong>Target &amp; Fit</strong><small>{requirements ? "Analyzed" : "Optional"}</small></span>
          </li>
          <li className={patches.length > 0 || rejectedPatches.length > 0 ? "is-ready" : gap ? "is-current" : ""}>
            <span className="resume-workflow-index">03</span>
            <span>
              <strong>Tailor &amp; Verify</strong>
              <small>{patches.length > 0 || rejectedPatches.length > 0 ? "Suggestions ready" : "Optional"}</small>
            </span>
          </li>
          <li className={exportCanonical ? "is-ready" : ""}>
            <span className="resume-workflow-index">04</span>
            <span><strong>Deliver</strong><small>{exportCanonical ? "Available" : "After resume"}</small></span>
          </li>
        </ol>
        <p>Offline diagnosis and export work without AI. All AI suggestions stay visible; risky claims start unchecked.</p>
      </div>

      {notice ? (
        <div
          className={`inline-notice ${notice.tone}`}
          role={notice.tone === "error" ? "alert" : "status"}
          aria-live={notice.tone === "error" ? "assertive" : "polite"}
        >
          {notice.text}
        </div>
      ) : null}

      <section className="resume-block" id="resume-block-base" aria-labelledby="resume-block-base-title">
        <StudioBlockHeader
          eyebrow="01 / Foundation"
          title="Resume"
          description="Load the source document, then review the offline diagnostic before using AI."
        />
        <div className="resume-subsection">
          <div className="resume-subsection-title">
            <h3 id="resume-block-base-title">Source document</h3>
            <span>PDF, DOCX or TXT</span>
          </div>
        <div className="segmented-control resume-input-toggle" role="tablist" aria-label="Resume input method">
          <button
            type="button"
            role="tab"
            aria-selected={inputMode === "upload"}
            className={inputMode === "upload" ? "is-selected" : ""}
            disabled={uploading}
            onClick={() => setInputMode("upload")}
          >
            <FileText size={14} />
            Upload file
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={inputMode === "paste"}
            className={inputMode === "paste" ? "is-selected" : ""}
            disabled={uploading}
            onClick={() => setInputMode("paste")}
          >
            <ClipboardType size={14} />
            Paste text
          </button>
        </div>
        {profileResumeText?.trim() ? (
          <button type="button" className="secondary-button" onClick={handleUseProfileResume}>
            <FileText size={14} />
            Use profile resume
          </button>
        ) : null}

        {inputMode === "upload" ? (
          <ResumeUploadPanel
            busy={uploading}
            fileName={resumeFileName}
            onFile={(file) => void handleResumeFile(file)}
            onReject={(message) => toast.error("Unsupported file", message)}
          />
        ) : (
          <label className="settings-field">
            <span>Resume text</span>
            <textarea
              ref={pasteTextareaRef}
              className="large-textarea"
              placeholder="Paste your resume text here..."
              value={resumeText}
              onChange={(event) => setResumeText(event.target.value)}
              rows={8}
            />
          </label>
        )}

        {inputMode === "upload" && resumeText ? (
          <p className="resume-upload-ready">
            Text extracted — offline ATS diagnosis below. Click “Parse resume” to structure it with AI (requires an
            API key in Settings &gt; AI) for tailoring, gap analysis and cover letters.
          </p>
        ) : null}

        {scannedPdfWarning ? (
          <div className="inline-notice error resume-scanned-pdf-notice">
            <p>This PDF looks scanned — very little text could be extracted from it.</p>
            <div className="resume-scanned-pdf-actions">
              <button
                type="button"
                className="secondary-button"
                disabled={busyStep !== null || uploading || !ocrCandidate}
                onClick={() => void handleSetUpOCR()}
              >
                {busyStep === "ocr" ? <LoaderCircle className="is-spinning" size={14} /> : <ScanText size={14} />}
                Set up OCR
              </button>
              <button
                type="button"
                className="secondary-button"
                onClick={() => {
                  setInputMode("paste");
                  setScannedPdfWarning(false);
                  setOCRReviewOpen(false);
                  window.setTimeout(() => pasteTextareaRef.current?.focus(), 0);
                }}
              >
                Paste text instead
              </button>
              <button
                type="button"
                className="secondary-button"
                onClick={() => {
                  setScannedPdfWarning(false);
                  setResumeText("");
                  setResumeFileName(null);
                  setOCRCandidate(null);
                  setOCRReviewOpen(false);
                  setOCRReviewText("");
                  setOCRStatusText(null);
                }}
              >
                Choose another file
              </button>
            </div>
            {ocrStatusText ? <p className="resume-ocr-status">{ocrStatusText}</p> : null}
          </div>
        ) : null}

        {ocrReviewOpen ? (
          <div className="inline-notice neutral resume-ocr-review">
            <p>Review the OCR text before parsing. Nothing from OCR is sent to AI until you click Parse reviewed text.</p>
            <textarea
              className="large-textarea"
              value={ocrReviewText}
              onChange={(event) => setOCRReviewText(event.target.value)}
              rows={10}
            />
            <div className="resume-scanned-pdf-actions">
              <button
                type="button"
                className="primary-button"
                disabled={busyStep !== null || !ocrReviewText.trim()}
                onClick={() => void handleParse(ocrReviewText)}
              >
                {busyStep === "parse" ? <LoaderCircle className="is-spinning" size={16} /> : <Sparkles size={16} />}
                Parse reviewed text
              </button>
              <button type="button" className="secondary-button" onClick={() => setOCRReviewOpen(false)}>
                Close review
              </button>
            </div>
          </div>
        ) : null}

        <button
          className="primary-button"
          type="button"
          disabled={busyStep !== null || uploading || !resumeText.trim()}
          onClick={() => void handleParse()}
        >
          {busyStep === "parse" ? <LoaderCircle className="is-spinning" size={16} /> : <Sparkles size={16} />}
          Parse resume
        </button>
        {busyStep === "parse" ? <ResumeProgress operation="parse" title="Structuring your resume" /> : null}
        {showExtractedPreview && (
          <div className="inline-notice error resume-extract-preview">
            <p>This is what we read from your file:</p>
            <pre className="resume-extract-preview__text">
              {resumeText.split("\n").slice(0, 15).join("\n")}
            </pre>
            <button
              type="button"
              className="secondary-button"
              onClick={() => {
                setInputMode("paste");
                setShowExtractedPreview(false);
                window.setTimeout(() => pasteTextareaRef.current?.focus(), 0);
              }}
            >
              Edit text and retry
            </button>
          </div>
        )}
        {canonical ? <p className="resume-parsed-name">Parsed: {canonical.basics.name || "(no name found)"}</p> : null}
        </div>

      {diagnosis ? (
        <div className="resume-subsection resume-diagnosis-subsection">
          <div className="resume-subsection-title">
            <h3>Offline diagnosis</h3>
            <span>No AI key required</span>
          </div>
          <ResumeDiagnosisPanel scores={diagnosis.scores} issues={diagnosis.issues} />
        </div>
      ) : null}
      </section>

      {exportCanonical ? (
        <section className="resume-block" id="resume-block-target" aria-labelledby="resume-block-target-title">
          <StudioBlockHeader
            eyebrow="02 / Direction"
            title="Target & Fit"
            description="Choose one role, inspect the requirements and confirm only evidence you genuinely have."
          />
          <div className="resume-subsection">
            <div className="resume-subsection-title">
              <h3 id="resume-block-target-title">Target role</h3>
              <span>Saved job or pasted description</span>
            </div>
          <JobPicker onPick={handlePickJob} disabled={busyStep !== null} />
          {selectedJob ? (
            <p className="resume-parsed-name">
              Selected: {selectedJob.title} — {selectedJob.company} (score {selectedJob.score})
            </p>
          ) : null}
          <label className="settings-field">
            <span>Job description</span>
            <textarea
              className="large-textarea"
              placeholder="Paste the job description here..."
              value={jobDescription}
              onChange={(event) => setJobDescription(event.target.value)}
              rows={6}
            />
          </label>
          <button
            className="secondary-button"
            type="button"
            disabled={busyStep !== null}
            onClick={() => void handleAnalyzeJob()}
          >
            {busyStep === "analyze-job" ? <LoaderCircle className="is-spinning" size={14} /> : <Sparkles size={14} />}
            Analyze job
          </button>
          {busyStep === "analyze-job" ? (
            <ResumeProgress operation="analyze-job" title="Analyzing the job" />
          ) : null}
          {!canonical ? (
            <p className="resume-job-ai-note">
              You can prepare the job description now. Analyze job, gap analysis, and tailoring require an AI-parsed
              resume and an AI key.
            </p>
          ) : null}
          {requirements ? (
            <p className="resume-parsed-name">
              {requirements.jobTitle || "Role"} — {requirements.seniority || "seniority unknown"}
            </p>
          ) : null}
          </div>

      {requirements && canonical ? (
        <div className="resume-subsection">
          <div className="resume-subsection-title">
            <h3>Evidence check</h3>
            <span>Anti-invention gate</span>
          </div>
          <button className="secondary-button" type="button" disabled={busyStep !== null} onClick={() => void handleFindGaps()}>
            {busyStep === "gap" ? <LoaderCircle className="is-spinning" size={14} /> : <Sparkles size={14} />}
            Find gaps
          </button>
          {busyStep === "gap" ? <ResumeProgress operation="gap" title="Finding gaps" /> : null}
          {gap ? (
            <JobGapAnalysis
              gap={gap}
              confirmed={confirmed}
              onToggleConfirm={toggleConfirm}
              confirmedSkills={canonical?.confirmedSkills}
              scores={
                scoreAfter
                  ? { ats: scoreAfter.ats, hr: scoreAfter.hr }
                  : scoreBefore
                    ? { ats: scoreBefore.ats, hr: scoreBefore.hr }
                    : null
              }
            />
          ) : null}
        </div>
      ) : null}
        </section>
      ) : null}

      {gap || (finalCanonical && requirements) ? (
        <section className="resume-block" id="resume-block-tailor" aria-label="Tailor and verify">
          <StudioBlockHeader
            eyebrow="03 / Controlled AI"
            title="Tailor & Verify"
            description="Generate suggestions, inspect every patch and measure only the changes you accept."
          />
          {gap ? (
          <div className="resume-subsection">
            <div className="resume-subsection-title">
              <h3 id="resume-block-tailor-title">Tailoring controls</h3>
              <span>Every risky claim starts unchecked</span>
            </div>
          <div className="resume-tailor-options">
            <label className="settings-field resume-language-picker">
              <span>Generated content language</span>
              <select value={language} onChange={(event) => setLanguage(event.target.value as ResumeLanguageOption)}>
                <option value="en">English (default)</option>
                <option value="pt">Portuguese</option>
                <option value="es">Spanish</option>
                <option value="auto">Auto (match job)</option>
              </select>
            </label>
            <label className="settings-field resume-voice-picker">
              <span>Voice</span>
              <select value={voice} onChange={(event) => setVoice(event.target.value as ResumeVoiceOption)}>
                <option value="third">Third person (default)</option>
                <option value="first">First person</option>
              </select>
            </label>
          </div>
          {!reviewOpen ? (
            <button className="primary-button" type="button" disabled={busyStep !== null} onClick={() => setReviewOpen(true)}>
              <Sparkles size={16} />
              Optimize resume
            </button>
          ) : (
            <div className="resume-review-card">
              <h3>Review before generating</h3>
              <dl className="resume-review-list">
                <div>
                  <dt>Name</dt>
                  <dd>{canonical?.basics.name || "—"}</dd>
                </div>
                <div>
                  <dt>Job</dt>
                  <dd>{selectedJob ? `${selectedJob.title} at ${selectedJob.company}` : requirements?.jobTitle || "Pasted description"}</dd>
                </div>
                <div>
                  <dt>Template</dt>
                  <dd>{templateId}</dd>
                </div>
                <div>
                  <dt>Voice</dt>
                  <dd>{voice === "first" ? "First person" : "Third person"}</dd>
                </div>
                <div>
                  <dt>Language</dt>
                  <dd>{language === "auto" ? "Auto (match job)" : language.toUpperCase()}</dd>
                </div>
              </dl>
              <button className="primary-button" type="button" disabled={busyStep !== null} onClick={() => void handleOptimize()}>
                {busyStep === "optimize" ? <LoaderCircle className="is-spinning" size={16} /> : <Sparkles size={16} />}
                Generate tailored resume
              </button>
              {busyStep === "optimize" ? (
                <ResumeProgress operation="optimize" title="Tailoring your resume" />
              ) : null}
            </div>
          )}
          <div ref={optimizeResultRef}>
            {patchApplication.failedPaths.length > 0 && (
              <div className="inline-notice error">
                {patchApplication.failedPaths.length} accepted change(s) could not be applied to the
                resume and were skipped: {patchApplication.failedPaths.join(", ")}. Uncheck them or run
                Optimize again — they are NOT in the exported/saved resume.
              </div>
            )}
            {(patches.length > 0 || rejectedPatches.length > 0) && (
              <ResumeDiffPreview
                base={canonical}
                patches={patches}
                rejected={rejectedPatches}
                accepted={acceptedIndexes}
                onToggle={toggleAccepted}
                togglesDisabled={busyStep === "score" || busyStep === "save"}
              />
            )}
          </div>
          </div>
          ) : null}

      {finalCanonical && requirements ? (
        <div className="resume-subsection">
          <div className="resume-subsection-title">
            <h3>Score comparison</h3>
            <span>Before vs accepted changes</span>
          </div>
          <button className="secondary-button" type="button" disabled={busyStep !== null} onClick={() => void handleCompareScores()}>
            {busyStep === "score" ? <LoaderCircle className="is-spinning" size={14} /> : <Sparkles size={14} />}
            Compare scores
          </button>
          <div ref={scoreResultRef}>
            {scoreBefore && scoreAfter ? (
              <ScoreComparison before={scoreBefore} after={scoreAfter} gap={gap} confirmed={[...confirmed]} />
            ) : null}
          </div>
        </div>
      ) : null}
        </section>
      ) : null}

      {exportCanonical ? (
        <section className="resume-block" id="resume-block-deliver" aria-labelledby="resume-block-deliver-title">
          <StudioBlockHeader
            eyebrow="04 / Output"
            title="Deliver"
            description="Choose a template, export locally and keep optional versions or a cover letter nearby."
          />
          <div className="resume-subsection resume-export-subsection">
            <div className="resume-subsection-title">
              <h3 id="resume-block-deliver-title">Export resume</h3>
              <span>PDF, DOCX, Markdown or HTML</span>
            </div>
          {!finalCanonical ? (
            <div className="inline-notice neutral">
              Exporting the extracted / heuristic version of your resume (no AI key used). Use AI (Parse resume,
              above) to tailor it for a specific job before exporting.
            </div>
          ) : null}
          <TemplateGallery
            templateId={templateId}
            onSelect={setTemplateId}
            canonical={exportCanonical}
            recommendedId={recommendation.templateId}
            recommendedReason={recommendation.reason}
          />
          <ResumeExportPanel
            canonical={exportCanonical}
            templateId={templateId}
            onError={(message) => setNotice({ tone: "error", text: message })}
          />
          <div className="resume-save-version" ref={saveResultRef}>
            <button
              className="secondary-button"
              type="button"
              disabled={busyStep !== null || !documentId}
              onClick={() => void handleSaveVersion()}
            >
              {busyStep === "save" ? <LoaderCircle className="is-spinning" size={14} /> : <FileText size={14} />}
              Save version{selectedJob ? " for this job" : ""}
            </button>
            {!documentId ? (
              <p className="resume-save-hint">Parse the resume under Resume before saving a version.</p>
            ) : null}
            {saveFeedback ? (
              <p className={`resume-save-feedback ${saveFeedback.tone === "error" ? "is-error" : "is-success"}`}>
                {saveFeedback.text}
              </p>
            ) : null}
          </div>
        </div>

      {finalCanonical ? (
        <details className="resume-secondary-disclosure">
          <summary>
            <span>Saved versions</span>
            <small>Restore, rename or export earlier versions</small>
          </summary>
          <div className="resume-disclosure-body">
            <ResumeVersionsPanel
              jobId={selectedJob?.id}
              documentId={documentId ?? undefined}
              refreshToken={versionsRefreshToken}
              onRestore={handleRestoreVersion}
            />
          </div>
        </details>
      ) : null}

      {finalCanonical ? (
        <details className="resume-secondary-disclosure">
          <summary>
            <span>Cover letter</span>
            <small>Optional and protected by the same claim checks</small>
          </summary>
          <div className="resume-disclosure-body">
            <CoverLetterPanel
              canonical={finalCanonical}
              jobId={selectedJob?.id}
              jobDescription={jobDescription}
              company={selectedJob?.company}
              role={selectedJob?.title}
              gap={gap}
              confirmed={[...confirmed]}
            />
          </div>
        </details>
      ) : null}
        </section>
      ) : null}
    </section>
  );
}
