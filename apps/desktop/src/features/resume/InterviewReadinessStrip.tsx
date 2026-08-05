import type { GapResult } from "../../types";
import { computeInterviewReadiness, VERDICT_LABELS } from "./resumeReadiness";

type InterviewReadinessStripProps = {
  gap: GapResult;
  scores?: { ats: number; hr: number } | null;
};

// InterviewReadinessStrip is the compact operational summary above the gap
// cards — readiness estimate + verdict, requirement breakdown, and the single
// most important next action. Deliberately not a hero: one dense row.
export function InterviewReadinessStrip({ gap, scores }: InterviewReadinessStripProps) {
  const readiness = computeInterviewReadiness(gap, scores);
  const stats: { label: string; value: number; tone: string }[] = [
    { label: "strong", value: gap.found?.length ?? 0, tone: "is-strong" },
    { label: "strengthen", value: gap.partial?.length ?? 0, tone: "is-boost" },
    { label: "confirm", value: gap.toConfirm?.length ?? 0, tone: "is-confirm" },
    { label: "gaps", value: gap.missing?.length ?? 0, tone: "is-gap" },
  ];

  return (
    <div className={`readiness-strip readiness-${readiness.verdict}`}>
      <div className="readiness-score" title="Heuristic estimate from your gap analysis">
        <span className="readiness-score-value">{readiness.score}</span>
        <span className="readiness-score-caption">
          readiness<span className="readiness-estimate"> · est.</span>
        </span>
      </div>
      <div className="readiness-body">
        <div className="readiness-verdict-row">
          <span className="readiness-verdict">{VERDICT_LABELS[readiness.verdict]}</span>
          {scores ? (
            <span className="readiness-sub">
              ATS {scores.ats} · HR {scores.hr}
            </span>
          ) : null}
        </div>
        <div className="readiness-stats">
          {stats.map((stat) => (
            <span key={stat.label} className={`readiness-stat ${stat.tone}`}>
              <b>{stat.value}</b> {stat.label}
            </span>
          ))}
        </div>
      </div>
      {readiness.topActions.length > 0 ? (
        <div className="readiness-next">
          <span className="readiness-next-label">Next</span>
          <span className="readiness-next-text">{readiness.topActions[0]}</span>
        </div>
      ) : null}
    </div>
  );
}
