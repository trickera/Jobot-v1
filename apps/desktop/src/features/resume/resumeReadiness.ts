import type { GapResult } from "../../types";

export type ReadinessVerdict = "strong" | "competitive" | "needs_work" | "unlikely";

export type InterviewReadiness = {
  score: number; // 0-100, an honest heuristic — not a guarantee
  verdict: ReadinessVerdict;
  summary: string;
  topActions: string[];
};

export const VERDICT_LABELS: Record<ReadinessVerdict, string> = {
  strong: "Strong fit",
  competitive: "Competitive",
  needs_work: "Needs work",
  unlikely: "Long shot",
};

function count<T>(items: T[] | undefined): number {
  return items ? items.length : 0;
}

function verdictFor(score: number): ReadinessVerdict {
  if (score >= 80) return "strong";
  if (score >= 65) return "competitive";
  if (score >= 45) return "needs_work";
  return "unlikely";
}

// computeInterviewReadiness derives a transparent interview-readiness estimate
// from the gap analysis (and the ATS/HR scores once they exist). It is a
// heuristic over signals the user can see — coverage of the job's requirements
// plus, when available, the ATS/HR scores — not a backend guarantee. The UI
// labels it "estimate" so it is never read as a promise.
export function computeInterviewReadiness(
  gap: GapResult,
  scores?: { ats: number; hr: number } | null,
): InterviewReadiness {
  const found = count(gap.found);
  const partial = count(gap.partial);
  const missing = count(gap.missing);
  const toConfirm = count(gap.toConfirm);
  const total = found + partial + missing + toConfirm;

  // Coverage: fully-met requirements count 1, partial 0.5, over the whole set.
  const coverage = total === 0 ? 0 : (found + 0.5 * partial) / total;
  const coverageScore = Math.round(coverage * 100);

  // Blend with ATS/HR when the user has run the score step; otherwise the
  // estimate rests on requirement coverage alone.
  const score = scores
    ? Math.round(0.5 * coverageScore + 0.25 * scores.ats + 0.25 * scores.hr)
    : coverageScore;

  const summaryParts: string[] = [];
  if (found) summaryParts.push(`${found} strong match${found === 1 ? "" : "es"}`);
  if (partial) summaryParts.push(`${partial} to strengthen`);
  if (toConfirm) summaryParts.push(`${toConfirm} to confirm`);
  if (missing) summaryParts.push(`${missing} gap${missing === 1 ? "" : "s"} to prepare for`);
  const summary = summaryParts.length ? summaryParts.join(" · ") : "Run a gap analysis to see your fit.";

  const topActions: string[] = [];
  if (toConfirm) {
    topActions.push(`Confirm the ${toConfirm} "if true" item${toConfirm === 1 ? "" : "s"} you actually have.`);
  }
  if (partial) {
    topActions.push(`Add specifics to ${partial} partial match${partial === 1 ? "" : "es"}.`);
  }
  if (missing) {
    topActions.push(`Prepare talking points for ${missing} missing requirement${missing === 1 ? "" : "s"}.`);
  }
  if (topActions.length === 0 && found) {
    topActions.push("You cover the core requirements — tailor and export.");
  }

  return { score, verdict: verdictFor(score), summary, topActions };
}
