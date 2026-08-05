import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { GapResult } from "../../types";
import { JobGapAnalysis, formatEvidence } from "./JobGapAnalysis";

describe("JobGapAnalysis evidence", () => {
  afterEach(() => cleanup());

  it.each([
    [["Built AWS services", "Operated production"], "Built AWS services · Operated production"],
    ['["Built AWS services","Operated production"]', "Built AWS services · Operated production"],
  ] as const)("flattens array-shaped evidence", (evidence, expected) => {
    expect(formatEvidence(evidence)).toBe(expected);
  });

  it("renders flattened evidence without leaking JSON punctuation", () => {
    const gap: GapResult = {
      found: [{ term: "AWS", evidence: '["Built AWS services","Operated production"]' }],
      partial: [],
      missing: [],
      toConfirm: [],
    };
    render(<JobGapAnalysis gap={gap} confirmed={new Set()} onToggleConfirm={vi.fn()} />);

    expect(screen.getByText("Built AWS services · Operated production")).not.toBeNull();
    expect(screen.queryByText(/\["Built AWS/)).toBeNull();
  });
});
