type MatchGaugeProps = {
  score: number;
  pending?: boolean;
  unmeasured?: boolean;
  size?: "sm" | "md" | "lg";
  caption?: string;
  label?: string;
};

export function MatchGauge({
  score,
  pending = false,
  unmeasured = false,
  size = "sm",
  caption,
  label = "Compatibilidade",
}: MatchGaugeProps) {
  const value = Math.min(100, Math.max(0, Math.round(Number.isFinite(score) ? score : 0)));
  const accessibleLabel = unmeasured
    ? `${label} nao medido`
    : pending
      ? `${label} em analise`
      : `${label} ${value} de 100`;
  const visibleCaption = caption ?? (unmeasured ? "nao medido" : pending ? "analisando" : size === "lg" ? "de 100" : "match");

  return (
    <div
      className={`match-gauge match-gauge--${size} ${pending ? "is-pending" : ""} ${unmeasured ? "is-unmeasured" : ""}`}
      role="img"
      aria-label={accessibleLabel}
    >
      <svg viewBox="0 0 84 52" aria-hidden="true">
        <path className="match-gauge-track" d="M8 44A34 34 0 0 1 76 44" pathLength="100" />
        <path
          className="match-gauge-value"
          d="M8 44A34 34 0 0 1 76 44"
          pathLength="100"
          style={{ strokeDasharray: `${pending || unmeasured ? 0 : value} 100` }}
        />
        <text x="42" y="39">{unmeasured ? "-" : pending ? "..." : value}</text>
      </svg>
      <small>{visibleCaption}</small>
    </div>
  );
}
