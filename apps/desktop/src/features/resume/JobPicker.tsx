import { useEffect, useState } from "react";
import { ApiError, loadJobs } from "../../services/api";
import type { JobSummary } from "../../types";

type JobPickerProps = {
  onPick: (job: JobSummary) => void;
  // Switching jobs mid-AI-operation would mix one job's results into
  // another's screen — the parent disables the picker while busy.
  disabled?: boolean;
};

export function JobPicker({ onPick, disabled = false }: JobPickerProps) {
  const [jobs, setJobs] = useState<JobSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    loadJobs()
      .then((result) => {
        if (!cancelled) setJobs(result.jobs);
      })
      .catch((err) => {
        if (!cancelled) setError(err instanceof ApiError ? err.message : "Could not load saved jobs.");
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  if (loading) {
    return <p className="resume-save-hint">Loading saved jobs…</p>;
  }
  if (error) {
    return <p className="resume-save-feedback is-error">{error}</p>;
  }
  if (jobs.length === 0) {
    return <p className="resume-save-hint">No saved jobs — run a search first.</p>;
  }

  return (
    <label className="settings-field resume-job-picker">
      <span>Pick a saved job</span>
      <select
        defaultValue=""
        disabled={disabled}
        onChange={(event) => {
          const job = jobs.find((item) => item.id === event.target.value);
          if (job) onPick(job);
        }}
      >
        <option value="" disabled>
          Select a job…
        </option>
        {jobs.map((job) => (
          <option key={job.id} value={job.id}>
            {job.title} — {job.company} (score {job.score})
          </option>
        ))}
      </select>
    </label>
  );
}
