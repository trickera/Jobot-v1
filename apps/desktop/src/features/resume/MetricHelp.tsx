import { useId } from "react";

type MetricHelpProps = {
  label: string;
  description: string;
};

export function MetricHelp({ label, description }: MetricHelpProps) {
  const tooltipId = useId();

  return (
    <span className="resume-metric-help-wrap">
      <button
        className="resume-metric-help"
        type="button"
        title={description}
        aria-describedby={tooltipId}
        aria-label={`${label}: ${description}`}
      >
        ?
      </button>
      <span className="resume-metric-tooltip" id={tooltipId} role="tooltip">{description}</span>
    </span>
  );
}
