// Staged, honest loading model for Resume Studio's AI operations.
//
// The backend runs these as background jobs the client polls; the poll reports
// running/done, not a percentage. So the stages below are an honest client-side
// ESTIMATE of what the AI is doing — never a real backend progress figure. The
// final stage of each operation is "sticky": once reached it stays in progress
// until the real request resolves, so the UI never claims completion the backend
// hasn't reported.
//
// The budgets were re-timed after the latency work (Gemini thinking disabled,
// prompts capped, answers cached): a tailoring call that used to be killed at a
// 25s timeout now returns in single-digit seconds, and a repeat run comes from
// cache in well under a second. The old estimates promised up to 60s, which left
// the bar frozen near the start while the operation was already done — a
// progress bar that lies about being slow reads as a hang.

export type ProgressStage = { label: string; estMs: number };

export const resumeProgressStages: Record<string, ProgressStage[]> = {
  parse: [
    { label: "Reading your resume text", estMs: 1500 },
    { label: "Structuring experience and skills", estMs: 4000 },
  ],
  "analyze-job": [
    { label: "Reading the job description", estMs: 1500 },
    { label: "Extracting must-have and nice-to-have requirements", estMs: 3500 },
  ],
  gap: [
    { label: "Mapping your experience to the job", estMs: 1500 },
    { label: "Finding interview-critical gaps", estMs: 2500 },
    { label: "Preparing confirm-if-true suggestions", estMs: 2000 },
    { label: "Checking for unsupported claims", estMs: 2000 },
  ],
  optimize: [
    { label: "Deciding what is safe to rewrite", estMs: 2000 },
    { label: "Building reviewable resume changes", estMs: 5000 },
  ],
};

// activeStageIndex returns the index of the stage to show as "in progress" for
// the given elapsed time. The final stage is sticky (returned for any elapsed
// time past the second-to-last boundary) so the card never advances past the
// last real phase until the operation actually completes.
export function activeStageIndex(elapsedMs: number, stages: ProgressStage[]): number {
  if (stages.length === 0) return 0;
  let boundary = 0;
  for (let i = 0; i < stages.length - 1; i += 1) {
    boundary += stages[i].estMs;
    if (elapsedMs < boundary) return i;
  }
  return stages.length - 1;
}
