import { useEffect, useRef, useState } from "react";
import { Check, LoaderCircle } from "lucide-react";
import { activeStageIndex, resumeProgressStages } from "./resumeProgressStages";

type ResumeProgressProps = {
  // Key into resumeProgressStages: "parse" | "analyze-job" | "gap" | "optimize".
  operation: string;
  title: string;
};

// ResumeProgress shows a staged, honest progress card while a long AI call is
// in flight. It advances through the operation's estimated sub-steps on a
// timer and holds on the final step until the request resolves (the parent
// unmounts it), so it never claims completion the backend hasn't reported. The
// copy says "Estimated" out loud — there is no fake percentage.
export function ResumeProgress({ operation, title }: ResumeProgressProps) {
  const stages = resumeProgressStages[operation] ?? [];
  const [elapsedMs, setElapsedMs] = useState(0);
  const startedAtRef = useRef<number>(0);

  useEffect(() => {
    startedAtRef.current = Date.now();
    setElapsedMs(0);
    const timer = window.setInterval(() => {
      setElapsedMs(Date.now() - startedAtRef.current);
    }, 250);
    return () => window.clearInterval(timer);
  }, [operation]);

  if (stages.length === 0) return null;

  const active = activeStageIndex(elapsedMs, stages);
  const seconds = Math.floor(elapsedMs / 1000);

  return (
    <div className="resume-progress" role="status" aria-live="polite">
      <div className="resume-progress-head">
        <span className="resume-progress-title">{title}</span>
        <span className="resume-progress-counter">
          Step {active + 1} of {stages.length}
        </span>
      </div>
      <ol className="resume-progress-steps">
        {stages.map((stage, index) => {
          const state = index < active ? "is-done" : index === active ? "is-active" : "is-upcoming";
          return (
            <li key={stage.label} className={`resume-progress-step ${state}`}>
              <span className="resume-progress-marker" aria-hidden="true">
                {index < active ? (
                  <Check size={13} />
                ) : index === active ? (
                  <LoaderCircle size={13} className="is-spinning" />
                ) : (
                  <span className="resume-progress-dot" />
                )}
              </span>
              <span>{stage.label}</span>
            </li>
          );
        })}
      </ol>
      <div className="resume-progress-rail" aria-hidden="true">
        <div className="resume-progress-rail-fill" />
      </div>
      <p className="resume-progress-note">
        Estimated · {seconds}s elapsed
        {seconds >= 20 ? " · a busy provider is taking longer than usual" : ""}
      </p>
    </div>
  );
}
