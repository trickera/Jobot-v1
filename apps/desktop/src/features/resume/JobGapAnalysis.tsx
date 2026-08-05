import type { JSX } from "react";
import { CheckCircle2, HelpCircle, ShieldAlert, TrendingUp } from "lucide-react";
import type { GapItem, GapResult } from "../../types";
import { InterviewReadinessStrip } from "./InterviewReadinessStrip";

type JobGapAnalysisProps = {
  gap: GapResult;
  confirmed: Set<string>;
  onToggleConfirm: (term: string) => void;
  confirmedSkills?: string[];
  scores?: { ats: number; hr: number } | null;
};

type GapKind = "strong_match" | "interview_booster" | "confirm_if_true" | "do_not_claim";

const KIND_META: Record<
  GapKind,
  { label: string; risk: string; riskTone: string; icon: JSX.Element }
> = {
  strong_match: {
    label: "Strong match",
    risk: "Safe",
    riskTone: "is-safe",
    icon: <CheckCircle2 size={15} aria-hidden="true" />,
  },
  interview_booster: {
    label: "Interview booster",
    risk: "Safe",
    riskTone: "is-safe",
    icon: <TrendingUp size={15} aria-hidden="true" />,
  },
  confirm_if_true: {
    label: "Confirm if true",
    risk: "Needs confirmation",
    riskTone: "is-confirm",
    icon: <HelpCircle size={15} aria-hidden="true" />,
  },
  do_not_claim: {
    label: "Gap to prepare",
    risk: "Blocked",
    riskTone: "is-blocked",
    icon: <ShieldAlert size={15} aria-hidden="true" />,
  },
};

function isUserConfirmed(term: string, confirmedSkills: string[]): boolean {
  const t = term.toLowerCase();
  return confirmedSkills.some((s) => {
    const c = s.toLowerCase();
    return c === t || c.includes(t) || t.includes(c);
  });
}

export function formatEvidence(evidence: string | readonly string[] | null | undefined): string {
  if (evidence && typeof evidence !== "string") return evidence.map((item) => item.trim()).filter(Boolean).join(" · ");
  const value = evidence?.trim() ?? "";
  if (!value.startsWith("[")) return value;
  try {
    const parsed: unknown = JSON.parse(value);
    return Array.isArray(parsed)
      ? parsed.filter((item): item is string => typeof item === "string").map((item) => item.trim()).filter(Boolean).join(" · ")
      : value;
  } catch {
    return value;
  }
}

function GapCard({
  kind,
  item,
  confirmedSkills,
  isChecked,
  onToggleConfirm,
}: {
  kind: GapKind;
  item: GapItem;
  confirmedSkills: string[];
  isChecked?: boolean;
  onToggleConfirm?: (term: string) => void;
}) {
  const meta = KIND_META[kind];
  const confirmedByUser = isUserConfirmed(item.term, confirmedSkills);
  const evidence = formatEvidence(item.evidence);

  return (
    <article className={`gap-card gap-${kind}`}>
      <header className="gap-card-head">
        <span className="gap-card-kind">
          {meta.icon}
          {meta.label}
        </span>
        <span className={`gap-card-risk ${meta.riskTone}`}>{meta.risk}</span>
      </header>
      <p className="gap-card-req">{item.term}</p>
      {kind === "strong_match" && evidence ? (
        <p className="gap-card-evidence">
          <span className="gap-card-evidence-label">Found</span>
          {evidence}
        </p>
      ) : null}
      {kind === "interview_booster" ? (
        <p className="gap-card-note">
          {evidence ? <span className="gap-card-evidence-inline">{evidence}</span> : null}
          Add a specific, quantified detail to make this land with a recruiter.
        </p>
      ) : null}
      {kind === "do_not_claim" ? (
        <p className="gap-card-note">
          Not on your resume — we won&apos;t add it. Prepare a talking point or a short learning plan
          for the interview.
        </p>
      ) : null}
      {confirmedByUser ? <span className="gap-card-confirmed-tag">Confirmed by you</span> : null}
      {kind === "confirm_if_true" && onToggleConfirm ? (
        <label className="gap-card-confirm">
          <input type="checkbox" checked={isChecked ?? false} onChange={() => onToggleConfirm(item.term)} />
          <span>I actually have this</span>
        </label>
      ) : null}
    </article>
  );
}

// JobGapAnalysis renders the gap analysis as dense, operational cards (not a
// form): each requirement is a card whose kind — strong match, interview
// booster, confirm-if-true, or gap-to-prepare — carries its own anti-invention
// risk level. Only confirm-if-true cards expose a checkbox; gaps are never
// silently addable. A readiness strip summarizes fit up top.
export function JobGapAnalysis({ gap, confirmed, onToggleConfirm, confirmedSkills, scores }: JobGapAnalysisProps) {
  const confirmedList = confirmedSkills ?? [];
  // Belt-and-suspenders against a null array on the wire (the backend now
  // guarantees non-null, but a null here used to crash the whole app).
  const found = gap.found ?? [];
  const partial = gap.partial ?? [];
  const toConfirm = gap.toConfirm ?? [];
  const missing = gap.missing ?? [];
  const isEmpty = found.length + partial.length + toConfirm.length + missing.length === 0;

  if (isEmpty) {
    return <p className="resume-empty-hint">No requirements to compare yet — analyze a job first.</p>;
  }

  return (
    <div className="gap-analysis">
      <InterviewReadinessStrip gap={gap} scores={scores} />
      <div className="gap-card-grid">
        {found.map((item) => (
          <GapCard key={`f-${item.term}`} kind="strong_match" item={item} confirmedSkills={confirmedList} />
        ))}
        {partial.map((item) => (
          <GapCard key={`p-${item.term}`} kind="interview_booster" item={item} confirmedSkills={confirmedList} />
        ))}
        {toConfirm.map((item) => (
          <GapCard
            key={`c-${item.term}`}
            kind="confirm_if_true"
            item={item}
            confirmedSkills={confirmedList}
            isChecked={confirmed.has(item.term)}
            onToggleConfirm={onToggleConfirm}
          />
        ))}
        {missing.map((item) => (
          <GapCard key={`m-${item.term}`} kind="do_not_claim" item={item} confirmedSkills={confirmedList} />
        ))}
      </div>
    </div>
  );
}
