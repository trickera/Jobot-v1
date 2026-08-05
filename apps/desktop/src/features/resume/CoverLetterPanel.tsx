import { ClipboardCopy, Download, LoaderCircle, Sparkles } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useToast } from "../../components/Toast";
import { ApiError, generateCoverLetter } from "../../services/api";
import { saveTextFile } from "../../services/nativeFile";
import type { CanonicalResume, GapResult, ResumeCoverLetterTone, ResumeLanguageOption } from "../../types";
import { createStepToast } from "./stepToast";

type CoverLetterPanelProps = {
  canonical: CanonicalResume;
  jobId?: string;
  jobDescription: string;
  company?: string;
  role?: string;
  gap: GapResult | null;
  confirmed: string[];
};

const TONE_OPTIONS: Array<{ value: ResumeCoverLetterTone; label: string }> = [
  { value: "professional", label: "Professional (default)" },
  { value: "direct", label: "Direct" },
  { value: "consultative", label: "Consultative" },
];

const LANGUAGE_OPTIONS: Array<{ value: ResumeLanguageOption; label: string }> = [
  { value: "en", label: "English (default)" },
  { value: "pt", label: "Portuguese" },
  { value: "es", label: "Spanish" },
  { value: "auto", label: "Match job language" },
];

const WARNING_SKILL_PREFIX = "mentions_skill_not_in_resume: ";

function formatWarning(code: string): string {
  if (code.startsWith(WARNING_SKILL_PREFIX)) {
    const skill = code.slice(WARNING_SKILL_PREFIX.length);
    return `This letter mentions "${skill}", which is not on your resume — review before sending.`;
  }
  return code;
}

export function CoverLetterPanel({ canonical, jobId, jobDescription, company, role, gap, confirmed }: CoverLetterPanelProps) {
  const toast = useToast();
  const [tone, setTone] = useState<ResumeCoverLetterTone>("professional");
  const [language, setLanguage] = useState<ResumeLanguageOption>("en");
  const [maxWords, setMaxWords] = useState(220);
  const [busy, setBusy] = useState(false);
  const [markdown, setMarkdown] = useState("");
  const [plainText, setPlainText] = useState("");
  const [warnings, setWarnings] = useState<string[]>([]);
  // The backend already retried once and the letter still claims something the
  // resume cannot back. Copy/save stay locked until the user says the claim is
  // true — the anti-invention gate must not be walkable by ignoring a banner.
  const [requiresConfirmation, setRequiresConfirmation] = useState(false);
  const [claimsConfirmed, setClaimsConfirmed] = useState(false);
  const locked = requiresConfirmation && !claimsConfirmed;

  // A letter is written for one target. Picking another job — or re-running the
  // analysis, which produces a new gap — leaves the old letter on screen, still
  // exportable and carrying warnings computed against a gap that no longer
  // applies. Drop it. Editing the pasted description alone does not count: the
  // rest of the view only reacts once the user re-analyzes, and wiping a
  // generated letter on every keystroke would cost an AI call to undo.
  const target = useRef<{ jobId?: string; gap: GapResult | null }>({ jobId, gap });
  useEffect(() => {
    if (target.current.jobId === jobId && target.current.gap === gap) return;
    target.current = { jobId, gap };
    setMarkdown("");
    setPlainText("");
    setWarnings([]);
    setRequiresConfirmation(false);
    setClaimsConfirmed(false);
  }, [jobId, gap]);

  // A saved job (jobId) is enough on its own: the backend resolves the job
  // description from the id via getJobByID, so the textarea can be empty here
  // (e.g. picking a saved job whose description lives only in the DB).
  const canGenerate = Boolean(canonical.basics.name && (jobDescription.trim() || jobId));

  async function handleGenerate() {
    if (!canGenerate) return;
    setBusy(true);
    const pending = createStepToast((title) => toast.loading(title), "Generating cover letter with AI…");
    try {
      const result = await generateCoverLetter({
        canonical,
        jobId,
        jobDescription,
        company,
        role,
        language,
        tone,
        maxWords,
        gap: gap ?? undefined,
        confirmed,
      });
      setMarkdown(result.markdown);
      setPlainText(result.plainText);
      setWarnings(result.warnings ?? []);
      setRequiresConfirmation(Boolean(result.requiresConfirmation));
      setClaimsConfirmed(false);
      pending.success(
        "Cover letter generated",
        result.requiresConfirmation
          ? "It still claims something your resume doesn't back — confirm it below before using it."
          : undefined,
      );
    } catch (error) {
      pending.error(
        "Could not generate the cover letter",
        error instanceof ApiError ? error.message : "Check your AI key and try again.",
      );
    } finally {
      setBusy(false);
    }
  }

  async function handleCopy() {
    if (locked) return;
    try {
      await navigator.clipboard.writeText(plainText || markdown);
      toast.success("Copied to clipboard");
    } catch {
      toast.error("Could not copy to clipboard");
    }
  }

  async function handleSave(format: "md" | "txt") {
    if (locked) return;
    try {
      const content = format === "md" ? markdown : plainText;
      const filter = format === "md" ? { name: "Markdown", extensions: ["md"] } : { name: "Text", extensions: ["txt"] };
      const saved = await saveTextFile(`cover-letter.${format}`, content, filter);
      toast.success("Cover letter saved", "path" in saved ? `Saved to ${saved.path}` : "Downloaded.");
    } catch (error) {
      if (error instanceof Error && error.message === "Save cancelled.") return;
      toast.error("Could not save the cover letter");
    }
  }

  return (
    <div className="cover-letter-panel">
      <div className="cover-letter-controls">
        <label className="settings-field">
          <span>Tone</span>
          <select value={tone} onChange={(event) => setTone(event.target.value as ResumeCoverLetterTone)}>
            {TONE_OPTIONS.map((option) => (
              <option key={option.value} value={option.value}>
                {option.label}
              </option>
            ))}
          </select>
        </label>
        <label className="settings-field">
          <span>Language</span>
          <select value={language} onChange={(event) => setLanguage(event.target.value as ResumeLanguageOption)}>
            {LANGUAGE_OPTIONS.map((option) => (
              <option key={option.value} value={option.value}>
                {option.label}
              </option>
            ))}
          </select>
        </label>
        <label className="settings-field cover-letter-max-words">
          <span>Max words</span>
          <input
            type="number"
            min={80}
            max={500}
            value={maxWords}
            onChange={(event) => setMaxWords(Number(event.target.value) || 220)}
          />
        </label>
      </div>

      <button className="primary-button" type="button" disabled={busy || !canGenerate} onClick={() => void handleGenerate()}>
        {busy ? <LoaderCircle className="is-spinning" size={16} /> : <Sparkles size={16} />}
        {markdown ? "Regenerate cover letter" : "Generate cover letter"}
      </button>
      {!canGenerate ? (
        <p className="resume-save-hint">Parse a resume and add a job description under Target &amp; Fit — or pick a saved job — before generating a cover letter.</p>
      ) : null}

      {warnings.length > 0 ? (
        <div className="cover-letter-warnings">
          {warnings.map((warning) => (
            <p key={warning} className="inline-notice warning">
              {formatWarning(warning)}
            </p>
          ))}
        </div>
      ) : null}

      {requiresConfirmation ? (
        <label className="cover-letter-confirm">
          <input
            type="checkbox"
            checked={claimsConfirmed}
            onChange={(event) => setClaimsConfirmed(event.target.checked)}
          />
          <span>
            I confirm the claim{warnings.length > 1 ? "s" : ""} above {warnings.length > 1 ? "are" : "is"} true, and I
            take responsibility for {warnings.length > 1 ? "them" : "it"}. Regenerate instead if not.
          </span>
        </label>
      ) : null}

      {markdown ? (
        <>
          <pre className="cover-letter-preview">{markdown}</pre>
          <div className="cover-letter-actions">
            <button type="button" className="secondary-button" disabled={locked} onClick={() => void handleCopy()}>
              <ClipboardCopy size={14} />
              Copy
            </button>
            <button type="button" className="secondary-button" disabled={locked} onClick={() => void handleSave("md")}>
              <Download size={14} />
              Save as .md
            </button>
            <button type="button" className="secondary-button" disabled={locked} onClick={() => void handleSave("txt")}>
              <Download size={14} />
              Save as .txt
            </button>
          </div>
        </>
      ) : null}
    </div>
  );
}
