import type { CanonicalResume, JsonPatchOp, ReviewRisk } from "../../types";
import { getValueAtPointer } from "./resumeJsonPatch";

type ResumeDiffPreviewProps = {
  base: CanonicalResume | null;
  patches: JsonPatchOp[];
  rejected: JsonPatchOp[];
  accepted: Set<number>;
  onToggle: (index: number) => void;
  // Toggling during an in-flight score/save would make the "after" result
  // reflect a selection that no longer matches the screen.
  togglesDisabled?: boolean;
};

const CRITICAL_RISKS = new Set<ReviewRisk>(["identity_change", "new_certification"]);

const RISK_LABELS: Record<ReviewRisk, string> = {
  new_metric: "New metric",
  new_skill: "Unverified skill",
  new_certification: "New certification",
  identity_change: "Identity change",
  unsupported_claim: "Strong claim",
  high_rewrite_distance: "Heavy rewrite",
};

const RISK_TOOLTIPS: Record<ReviewRisk, string> = {
  new_metric: "Adds a number not found in your original resume — verify before accepting.",
  new_skill: "Mentions a job skill that is not on your resume — confirm you have it.",
  new_certification: "Adds a certification that was not on your original resume.",
  identity_change: "Changes employer, role, dates, or education facts — blocked.",
  unsupported_claim: "Uses a strong leadership claim not supported by the original text.",
  high_rewrite_distance: "Rewrites most of the original sentence — review carefully.",
};

function formatValue(value: unknown): string {
  if (value === undefined || value === null) return "";
  if (typeof value === "string") return value;
  if (Array.isArray(value)) return value.map((v) => formatValue(v)).filter(Boolean).join(", ");
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
}

// describePath turns a raw JSON Pointer into a human label, resolving
// experience/education context from the base resume so the user sees
// "Bullet — Acme · Engineer", not "/experience/0/bullets/1".
function describePath(base: CanonicalResume | null, path: string): string {
  const normalized = path === "/basics/summary" ? "/summary" : path;
  if (normalized === "/summary") return "Summary";
  if (normalized === "/experience") return "Experience order";
  if (normalized === "/education") return "Education order";

  const skill = normalized.match(/^\/skills\/(hard|soft|tools)(\/.*)?$/);
  if (skill) return `Skill · ${skill[1]}`;

  const expBullet = normalized.match(/^\/experience\/(\d+)\/bullets\/(\d+|-)$/);
  if (expBullet && base) {
    const exp = base.experience?.[Number(expBullet[1])];
    const where = exp ? `${exp.company} · ${exp.role}` : `experience ${expBullet[1]}`;
    return `Bullet — ${where}`;
  }
  const expField = normalized.match(/^\/experience\/(\d+)\/(\w+)$/);
  if (expField && base) {
    const exp = base.experience?.[Number(expField[1])];
    const where = exp ? `${exp.company} · ${exp.role}` : `experience ${expField[1]}`;
    return `${expField[2]} — ${where}`;
  }
  const basics = normalized.match(/^\/basics\/(\w+)$/);
  if (basics) return basics[1].charAt(0).toUpperCase() + basics[1].slice(1);
  return normalized;
}

type GateState = { label: string; tone: string };

function gateStateFor(risk: ReviewRisk | undefined, blocked: boolean): GateState {
  if (blocked) return { label: "Blocked", tone: "is-blocked" };
  if (risk) return { label: "Needs confirmation", tone: "is-confirm" };
  return { label: "Safe", tone: "is-safe" };
}

function ReviewRiskBadge({ risk }: { risk: ReviewRisk }) {
  const critical = CRITICAL_RISKS.has(risk);
  return (
    <span
      className={`diff-risk-badge ${critical ? "is-critical" : "is-warning"}`}
      title={RISK_TOOLTIPS[risk]}
    >
      {RISK_LABELS[risk]}
    </span>
  );
}

// ResumeDiffPreview shows every AI-suggested change (already past the backend's
// deterministic anti-invention gate) as an operational patch card with a
// human-readable target, before → after text, a Safe/Needs-confirmation/Blocked
// gate label, and per-op accept toggles — the human half of the gate for
// free-text rewrites the code can't fact-check.
export function ResumeDiffPreview({ base, patches, rejected, accepted, onToggle, togglesDisabled = false }: ResumeDiffPreviewProps) {
  if (patches.length === 0 && rejected.length === 0) {
    return <p className="resume-empty-hint">No changes suggested yet.</p>;
  }

  return (
    <div className="diff-preview">
      {patches.length > 0 ? (
        <div className="diff-card-grid">
          {patches.map((patch, index) => {
            const risk = patch.reviewRisk;
            const blocked = risk ? CRITICAL_RISKS.has(risk) : false;
            const gate = gateStateFor(risk, blocked);
            const before = patch.op === "add" ? "" : formatValue(getValueAtPointer(base ?? ({} as CanonicalResume), patch.path));
            const after = formatValue(patch.value);
            return (
              <article key={`${patch.path}-${index}`} className={`diff-card ${accepted.has(index) ? "is-accepted" : ""}`}>
                <header className="diff-card-head">
                  <label className="diff-card-toggle">
                    <input
                      type="checkbox"
                      checked={accepted.has(index)}
                      disabled={blocked || togglesDisabled}
                      onChange={() => onToggle(index)}
                    />
                    <span className="diff-card-target">{describePath(base, patch.path)}</span>
                  </label>
                  <span className={`diff-gate ${gate.tone}`}>{gate.label}</span>
                </header>
                {risk ? <ReviewRiskBadge risk={risk} /> : null}
                {before ? (
                  <p className="diff-before">
                    <span className="diff-line-label">Now</span>
                    {before}
                  </p>
                ) : null}
                {after ? (
                  <p className="diff-after">
                    <span className="diff-line-label">Improved</span>
                    {after}
                  </p>
                ) : null}
                {patch.reason ? <p className="diff-reason">{patch.reason}</p> : null}
              </article>
            );
          })}
        </div>
      ) : (
        <p className="resume-empty-hint">All suggested changes were blocked by the anti-fabrication gate.</p>
      )}
      {rejected.length > 0 ? (
        <div className="diff-rejected">
          <h4>Blocked by the anti-fabrication gate ({rejected.length})</h4>
          <ul>
            {rejected.map((patch, index) => (
              <li key={`rejected-${patch.path}-${index}`}>
                <span className="diff-rejected-target">{describePath(base, patch.path)}</span>
                {patch.reviewRisk ? <ReviewRiskBadge risk={patch.reviewRisk} /> : null}
                <span className="diff-rejected-reason">{patch.reason}</span>
              </li>
            ))}
          </ul>
        </div>
      ) : null}
    </div>
  );
}
