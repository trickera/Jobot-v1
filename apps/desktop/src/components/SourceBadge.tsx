import { IntegrationLogo, integrationKeyForName, integrationLabel } from "./IntegrationLogo";

type SourceBadgeProps = {
  source: string;
  showLabel?: boolean;
  size?: "sm" | "md";
  className?: string;
};

export function SourceBadge({ source, showLabel = true, size = "sm", className = "" }: SourceBadgeProps) {
  const kind = integrationKeyForName(source);
  const label = integrationLabel(source) || "Fonte";
  const iconSize = size === "md" ? 18 : 15;

  return (
    <span className={`source-badge source-badge--${kind} source-badge--${size} ${className}`.trim()} title={label}>
      <span className="source-badge-icon">
        <IntegrationLogo name={label} size={iconSize} />
      </span>
      {showLabel ? <span className="source-badge-label">{label}</span> : null}
    </span>
  );
}

export function formatJobMeta(company: string, location: string) {
  const parts = [company?.trim() || "Empresa nao informada", location?.trim() || "Local nao informado"].filter(Boolean);
  return parts.join(" · ");
}
