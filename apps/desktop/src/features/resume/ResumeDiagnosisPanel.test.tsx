import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import type { AtsScores } from "../../types";
import { ResumeDiagnosisPanel } from "./ResumeDiagnosisPanel";

const scores: AtsScores = {
  readability: 90,
  content: 100,
  impact: 100,
  keywords: 100,
  impactMeasured: true,
};

describe("ResumeDiagnosisPanel", () => {
  afterEach(cleanup);

  // The E2E run showed "Keywords 100" here and "Keyword match 16/25" in the
  // score comparison on the same screen. They measure different things — this
  // one counts the skill keywords the resume lists, with no job involved — so
  // the label must not read as the job-coverage number.
  it("names the offline score for what it measures, not the job-coverage one", () => {
    render(<ResumeDiagnosisPanel scores={scores} issues={[]} />);

    expect(screen.getByText("Skill keywords")).toBeTruthy();
    expect(screen.queryByText("Keywords")).toBeNull();
  });

  it("keeps an unmeasured impact distinct from a measured zero", () => {
    const { rerender } = render(
      <ResumeDiagnosisPanel scores={{ ...scores, impact: 0, impactMeasured: false }} issues={[]} />,
    );

    expect(screen.getByRole("img", { name: "Impact score nao medido" })).toBeTruthy();

    rerender(<ResumeDiagnosisPanel scores={{ ...scores, impact: 0, impactMeasured: true }} issues={[]} />);
    expect(screen.getByRole("img", { name: "Impact score 0 de 100" })).toBeTruthy();
  });

  it("exposes keyboard-readable explanations for each diagnostic metric", () => {
    render(<ResumeDiagnosisPanel scores={scores} issues={[]} />);

    expect(screen.getByLabelText(/^Readability:/)).toBeTruthy();
    expect(screen.getByLabelText(/^Impact:/)).toBeTruthy();
    expect(screen.getByLabelText(/^Skill keywords:/)).toBeTruthy();
  });
});
