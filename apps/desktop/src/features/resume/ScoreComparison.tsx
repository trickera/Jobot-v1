import { MatchGauge } from "../../components/MatchGauge";
import type { GapResult, ScoreResult } from "../../types";
import { MetricHelp } from "./MetricHelp";

const ATS_TARGET = 80;
const HR_TARGET = 70;

const atsComponents = [
  { key: "phrase", label: "Phrase match (vs job)", max: 25 },
  { key: "keyword", label: "Keyword match (vs job)", max: 25 },
  { key: "title", label: "Title match", max: 15 },
  { key: "structure", label: "ATS structure", max: 15 },
  { key: "skillsContext", label: "Skills in context", max: 10 },
  { key: "recency", label: "Recency", max: 10 },
] as const;

const hrComponents = [
  { key: "experienceFit", label: "Experience fit", max: 30 },
  { key: "impact", label: "Demonstrated impact", max: 25 },
  { key: "skills", label: "Skills demonstrated", max: 20 },
  { key: "trajectory", label: "Career trajectory", max: 15 },
  { key: "clarity", label: "Bullet clarity", max: 10 },
] as const;

const SCORE_HELP = {
  ats:
    "Weighted job-match score: phrases and keywords (25 points each), title and structure (15 each), skills in context and recency (10 each). The table below shows every awarded point.",
  hr:
    "Recruiter-facing evidence score: experience fit (30 points), impact (25), skills in context (20), trajectory (15) and clarity (10). It is not a guarantee of an interview.",
} as const;

function deltaLabel(before: number, after: number) {
  const delta = after - before;
  if (delta > 0) return `+${delta}`;
  return String(delta);
}

function ScoreMetric({
  label,
  value,
  target,
  help,
}: {
  label: "ATS" | "HR";
  value: number;
  target: number;
  help: string;
}) {
  return (
    <div className={`resume-comparison-metric${value >= target ? " is-target" : ""}`}>
      <div className="resume-metric-label">
        <strong>{label}</strong>
        <MetricHelp label={label} description={help} />
      </div>
      <MatchGauge score={value} size="md" caption={`target ${target}`} label={`${label} score`} />
      <span className="resume-comparison-target">{value >= target ? "Target reached" : `${target - value} points to target`}</span>
    </div>
  );
}

function BreakdownTable({
  label,
  components,
  before,
  after,
}: {
  label: string;
  components: ReadonlyArray<{ key: string; label: string; max: number }>;
  before: Record<string, number>;
  after: Record<string, number>;
}) {
  const hasBreakdown = components.some(
    (component) =>
      Object.prototype.hasOwnProperty.call(before, component.key) ||
      Object.prototype.hasOwnProperty.call(after, component.key),
  );

  if (!hasBreakdown) {
    return <div className="inline-notice neutral">{label} is unavailable for this score.</div>;
  }

  return (
    <div className="ats-breakdown" role="table" aria-label={label}>
      <div className="ats-breakdown-row ats-breakdown-head" role="row">
        <span role="columnheader">Component</span>
        <span role="columnheader">Before</span>
        <span role="columnheader">After</span>
        <span role="columnheader">Delta</span>
      </div>
      {components.map((component) => {
        const beforeMeasured = Object.prototype.hasOwnProperty.call(before, component.key);
        const afterMeasured = Object.prototype.hasOwnProperty.call(after, component.key);
        const beforeValue = before[component.key];
        const afterValue = after[component.key];
        return (
          <div className="ats-breakdown-row" role="row" key={component.key}>
            <strong role="cell">{component.label}</strong>
            <span role="cell">{beforeMeasured ? `${beforeValue} / ${component.max}` : "Not measured"}</span>
            <span role="cell">{afterMeasured ? `${afterValue} / ${component.max}` : "Not measured"}</span>
            <span role="cell" className={beforeMeasured && afterMeasured && afterValue > beforeValue ? "is-positive" : undefined}>
              {beforeMeasured && afterMeasured ? deltaLabel(beforeValue, afterValue) : "-"}
            </span>
          </div>
        );
      })}
    </div>
  );
}

export function ScoreComparison({
  before,
  after,
  gap,
  confirmed = [],
}: {
  before: ScoreResult;
  after: ScoreResult;
  gap?: GapResult | null;
  confirmed?: string[];
}) {
  // Below target with unconfirmed requirements is the one case where the score
  // is capped by something the user can act on.
  const unconfirmed =
    after.ats < ATS_TARGET
      ? (gap?.toConfirm ?? []).filter((item) => !confirmed.includes(item.term)).length
      : 0;

  return (
    <div className="resume-score-result">
      <div className="resume-score-comparison">
        <section className="resume-score-comparison-column" aria-labelledby="resume-score-before">
          <h4 id="resume-score-before">Before</h4>
          <div className="resume-comparison-metric-grid">
            <ScoreMetric label="ATS" value={before.ats} target={ATS_TARGET} help={SCORE_HELP.ats} />
            <ScoreMetric label="HR" value={before.hr} target={HR_TARGET} help={SCORE_HELP.hr} />
          </div>
        </section>
        <section className="resume-score-comparison-column is-after" aria-labelledby="resume-score-after">
          <h4 id="resume-score-after">After accepted changes</h4>
          <div className="resume-comparison-metric-grid">
            <ScoreMetric label="ATS" value={after.ats} target={ATS_TARGET} help={SCORE_HELP.ats} />
            <ScoreMetric label="HR" value={after.hr} target={HR_TARGET} help={SCORE_HELP.hr} />
          </div>
        </section>
      </div>

      <div className="resume-score-breakdown-block">
        <div className="resume-subsection-title">
          <h4>Why the ATS score changed</h4>
          <span>Weighted points, not a prediction</span>
        </div>
        <BreakdownTable
          label="ATS score breakdown"
          components={atsComponents}
          before={before.atsBreakdown}
          after={after.atsBreakdown}
        />
      </div>

      <details className="resume-secondary-disclosure">
        <summary>Why the HR score changed</summary>
        <BreakdownTable
          label="HR score breakdown"
          components={hrComponents}
          before={before.hrBreakdown}
          after={after.hrBreakdown}
        />
      </details>

      <p className="resume-score-explanation">
        Phrase and keyword points move only when the final resume contains additional job terms that are already
        evidenced or explicitly confirmed.
      </p>
      {before.ats === after.ats ? (
        <div className="inline-notice neutral" role="status">
          An unchanged ATS total can be correct: the anti-invention gate may reject unsupported additions,
          while accepted wording or ordering changes improve readability without changing keyword coverage.
        </div>
      ) : null}
      {unconfirmed > 0 ? (
        <div className="inline-notice neutral">
          {unconfirmed} job requirement{unconfirmed > 1 ? "s are" : " is"} still unconfirmed, so tailoring cannot
          use {unconfirmed > 1 ? "them" : "it"} and the ATS score does not count {unconfirmed > 1 ? "them" : "it"}.
          Confirm under Target &amp; Fit the ones you actually have - or accept that this resume is honestly below the {ATS_TARGET}{" "}
          target for this job.
        </div>
      ) : null}
    </div>
  );
}
