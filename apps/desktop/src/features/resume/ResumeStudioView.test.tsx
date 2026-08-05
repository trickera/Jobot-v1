import { afterEach, describe, expect, it, vi } from "vitest";
import type { JsonPatchOp } from "../../types";
import { defaultAcceptedPatchIndexes, scrollResultIntoView } from "./ResumeStudioView";

describe("Resume Studio result navigation", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("scrolls a completed operation into view on the next frame", () => {
    const scrollIntoView = vi.fn();
    vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) => {
      callback(0);
      return 1;
    });

    scrollResultIntoView({ scrollIntoView } as unknown as HTMLElement);

    expect(scrollIntoView).toHaveBeenCalledWith({ behavior: "smooth", block: "start" });
  });
});

describe("defaultAcceptedPatchIndexes", () => {
  // The E2E run against real Gemini (2026-07-13) generated a tailoring patch
  // flagged "new_skill — Unverified skill / Needs confirmation" that added
  // "mobile-first" to the summary — a term the gap analysis had explicitly
  // left unconfirmed. It defaulted to accepted, so it shipped into the
  // exported resume (and from there, legitimately, into the cover letter)
  // without the user ever confirming it. A risk label the user never has to
  // act on is not a gate.
  it("excludes patches carrying a review risk", () => {
    const patches: JsonPatchOp[] = [
      { op: "replace", path: "/summary", value: "safe rewrite", reason: "", reviewRisk: undefined },
      { op: "replace", path: "/summary", value: "mobile-first rewrite", reason: "", reviewRisk: "new_skill" },
    ];

    expect(defaultAcceptedPatchIndexes(patches)).toEqual(new Set([0]));
  });

  it("accepts every patch when none carry a review risk", () => {
    const patches: JsonPatchOp[] = [
      { op: "replace", path: "/summary", value: "a", reason: "" },
      { op: "add", path: "/skills/hard/-", value: "b", reason: "" },
    ];

    expect(defaultAcceptedPatchIndexes(patches)).toEqual(new Set([0, 1]));
  });
});
