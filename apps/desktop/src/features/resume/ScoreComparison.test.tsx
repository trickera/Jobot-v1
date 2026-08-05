import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import type { GapResult, ScoreResult } from "../../types";
import { ScoreComparison } from "./ScoreComparison";

const before: ScoreResult = {
  ats: 63,
  hr: 64,
  atsBreakdown: { phrase: 10, keyword: 13, title: 15, structure: 15, skillsContext: 5, recency: 5 },
  hrBreakdown: {},
};

describe("ScoreComparison", () => {
  afterEach(() => cleanup());

  it("renders each weighted ATS component before and after", () => {
    const after: ScoreResult = {
      ...before,
      ats: 68,
      atsBreakdown: { ...before.atsBreakdown, skillsContext: 10 },
    };
    render(<ScoreComparison before={before} after={after} />);

    expect(screen.getByRole("table", { name: "ATS score breakdown" })).not.toBeNull();
    expect(screen.getByText("Phrase match (vs job)")).not.toBeNull();
    expect(screen.getByText("Skills in context")).not.toBeNull();
    expect(screen.getByText("+5")).not.toBeNull();
    expect(screen.queryByRole("table", { name: "HR score breakdown" })).toBeNull();
    expect(screen.getByText("HR score breakdown is unavailable for this score.")).not.toBeNull();
    expect(screen.getAllByLabelText(/^ATS:/)).toHaveLength(2);
    expect(screen.getAllByLabelText(/^HR:/)).toHaveLength(2);
  });

  it("explains an unchanged total instead of implying score comparison failed", () => {
    render(<ScoreComparison before={before} after={{ ...before }} />);
    expect(screen.getByRole("status").textContent).toContain("unchanged ATS total can be correct");
    expect(screen.getByText(/anti-invention gate may reject unsupported additions/)).not.toBeNull();
  });

  // The E2E run ended at ATS 52 with four unconfirmed "if true" items and no
  // hint that confirming them was the lever. Saying the gate blocked growth is
  // true but dead-ends the user.
  it("points at the unconfirmed items when the ATS target is out of reach", () => {
    const gap = {
      found: [],
      partial: [],
      missing: [],
      toConfirm: [{ term: "mobile-first" }, { term: "quantitative research" }],
    } as unknown as GapResult;

    render(<ScoreComparison before={before} after={{ ...before }} gap={gap} />);

    expect(screen.getByText(/2 job requirements are still unconfirmed/)).not.toBeNull();
  });

  it("counts only the items the user has not confirmed yet", () => {
    const gap = {
      found: [],
      partial: [],
      missing: [],
      toConfirm: [{ term: "mobile-first" }, { term: "quantitative research" }],
    } as unknown as GapResult;

    render(<ScoreComparison before={before} after={{ ...before }} gap={gap} confirmed={["mobile-first"]} />);

    expect(screen.getByText(/1 job requirement is still unconfirmed/)).not.toBeNull();
  });

  it("does not nag about confirmations once the ATS target is met", () => {
    const gap = { found: [], partial: [], missing: [], toConfirm: [{ term: "mobile-first" }] } as unknown as GapResult;
    const strong: ScoreResult = { ...before, ats: 84 };

    render(<ScoreComparison before={before} after={strong} gap={gap} />);

    expect(screen.queryByText(/still unconfirmed/)).toBeNull();
  });
});
