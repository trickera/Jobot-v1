import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { JobSummary, ScoreSource } from "../../types";
import { JobDetailPanel, MatchGauge } from "./JobDetailPanel";

function renderJob(scoreSource: ScoreSource, overrides: Partial<JobSummary> = {}) {
  const job: JobSummary = {
    id: "job-1",
    source: "LinkedIn",
    title: "Backend Engineer",
    company: "Acme",
    location: "Remote",
    url: "https://example.com/job-1",
    status: "[APPLY NOW]",
    score: 82,
    missingKeywords: [],
    scoreSource,
    ...overrides,
  };
  render(
    <JobDetailPanel
      job={job}
      onOpen={vi.fn()}
      onDismiss={vi.fn()}
      onMarkApplied={vi.fn()}
      onBlacklist={vi.fn()}
      busyAction={null}
      onOptimizeResume={vi.fn()}
    />,
  );
}

describe("JobDetailPanel score provenance", () => {
  afterEach(() => cleanup());

  it.each([
    ["ai", "AI score 82"],
    ["ai_cache", "Cached AI score 82"],
    ["offline_fallback", "Offline estimate 82"],
    ["offline_prefilter", "Not AI-scored — offline estimate 82"],
    ["offline_no_key", "Not AI-scored — offline estimate 82"],
  ] as const)("renders %s honestly", (source, copy) => {
    renderJob(source);
    expect(screen.getByText(new RegExp(copy))).not.toBeNull();
  });

  it("renders a pending state and disables actions that require a persisted final job", () => {
    renderJob("offline_fallback", { status: "[AI SCORING]", scoringPending: true, score: 0 });

    expect(screen.getByText(/AI scoring pending — this collected job is visible/)).not.toBeNull();
    expect((screen.getByRole("button", { name: "Marcar aplicada" }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: /Abrir no Resume Studio/ }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("presents missing job terms as evidence gaps, never instructions to invent skills", () => {
    renderJob("ai", { missingKeywords: ["Kubernetes", "Terraform"] });

    expect(screen.getByText("Requirements not evidenced on your resume")).not.toBeNull();
    expect(screen.getByText(/Verify each item is true before adding it/)).not.toBeNull();
    expect(screen.queryByText(/inclua no curriculo/i)).toBeNull();
  });

  it("renders the compact gauge accessibly and clamps invalid score ranges", () => {
    render(<MatchGauge score={140} />);

    expect(screen.getByRole("img", { name: "Compatibilidade 100 de 100" })).not.toBeNull();
    expect(screen.getByText("100")).not.toBeNull();
  });

  it("exposes score explanations to keyboard and assistive technology", () => {
    renderJob("ai");

    const help = screen.getByRole("button", { name: "Como esta nota foi calculada" });
    expect(help.getAttribute("aria-describedby")).toBe("score-help-copy");
    expect(document.getElementById("score-help-copy")?.textContent).toMatch(/compara os requisitos da vaga/);
  });
});
