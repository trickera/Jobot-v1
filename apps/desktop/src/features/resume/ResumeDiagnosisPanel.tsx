import { MatchGauge } from "../../components/MatchGauge";
import type { AtsIssue, AtsScores } from "../../types";
import { MetricHelp } from "./MetricHelp";

const METRIC_HELP = {
  readability:
    "Offline score for ATS-safe layout, readable dates, contact details and text extraction quality. The issues below identify any deductions.",
  content:
    "Offline completeness check for summary, experience, education and an organized skills section. The issues below identify any deductions.",
  impact:
    "Offline check of experience bullets for measurable impact. It stays unscored until there are parsed bullets to evaluate.",
  keywords:
    "Offline count of distinct skill terms already present in this resume. It does not compare the resume with a job.",
} as const;

type DiagnosisMetricProps = {
  label: string;
  value: number;
  help: string;
  measured?: boolean;
};

function DiagnosisMetric({ label, value, help, measured = true }: DiagnosisMetricProps) {
  return (
    <div className={`resume-diagnosis-metric${measured ? "" : " is-unmeasured"}`}>
      <MatchGauge
        score={value}
        unmeasured={!measured}
        size="md"
        caption={measured ? "of 100" : "not scored"}
        label={`${label} score`}
      />
      <div className="resume-diagnosis-metric-copy">
        <div className="resume-metric-label">
          <strong>{label}</strong>
          <MetricHelp label={label} description={help} />
        </div>
        <span>{measured ? "Offline diagnostic" : "Parse the resume to measure this"}</span>
      </div>
    </div>
  );
}

function severityLabel(severity: AtsIssue["severity"]): string {
  if (severity === "high") return "High";
  if (severity === "medium") return "Medium";
  return "Low";
}

export function ResumeDiagnosisPanel({ scores, issues }: { scores: AtsScores; issues: AtsIssue[] }) {
  const impactMeasured = scores.impactMeasured ?? true;

  return (
    <div className="resume-diagnosis-panel">
      <div className="resume-diagnosis-metrics" aria-label="Offline resume diagnostic scores">
        <DiagnosisMetric label="Readability" value={scores.readability} help={METRIC_HELP.readability} />
        <DiagnosisMetric label="Content" value={scores.content} help={METRIC_HELP.content} />
        <DiagnosisMetric label="Impact" value={scores.impact} measured={impactMeasured} help={METRIC_HELP.impact} />
        {/* This counts terms in the resume itself. Job coverage appears later in the ATS comparison. */}
        <DiagnosisMetric label="Skill keywords" value={scores.keywords} help={METRIC_HELP.keywords} />
      </div>
      {issues.length > 0 ? (
        <div className="resume-diagnosis-findings">
          <div className="resume-subsection-title">
            <h3>What affected these scores</h3>
            <span>{issues.length} finding{issues.length === 1 ? "" : "s"}</span>
          </div>
          <ul className="resume-issue-list">
            {issues.map((issue) => (
              <li key={issue.code} className={`resume-issue resume-issue-${issue.severity}`}>
                <span className="resume-issue-severity">{severityLabel(issue.severity)}</span>
                <span>{issue.message}</span>
              </li>
            ))}
          </ul>
        </div>
      ) : (
        <p className="resume-empty-hint">No issues found - this offline diagnosis works without an AI key.</p>
      )}
    </div>
  );
}
