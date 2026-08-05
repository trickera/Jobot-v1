import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ToastProvider } from "../../components/Toast";
import type { CanonicalResume, GapResult } from "../../types";
import { CoverLetterPanel } from "./CoverLetterPanel";

const apiMocks = vi.hoisted(() => ({
  generateCoverLetter: vi.fn(),
}));

vi.mock("../../services/api", () => ({
  ApiError: class ApiError extends Error {},
  generateCoverLetter: apiMocks.generateCoverLetter,
}));

const saveTextFile = vi.hoisted(() => vi.fn());
vi.mock("../../services/nativeFile", () => ({ saveTextFile }));

function baseResume(): CanonicalResume {
  return {
    basics: { name: "Sofia Almeida" },
    summary: "",
    skills: { hard: [], soft: [], tools: [] },
    experience: [],
    education: [],
    projects: [],
    certifications: [],
    licenses: [],
    confirmedSkills: [],
    target: {},
  } as unknown as CanonicalResume;
}

function panel(props: { jobId?: string; jobDescription?: string; gap?: GapResult | null } = {}) {
  return (
    <ToastProvider>
      <CoverLetterPanel
        canonical={baseResume()}
        jobId={props.jobId}
        jobDescription={props.jobDescription ?? "Mobile-first product design."}
        gap={props.gap ?? null}
        confirmed={[]}
      />
    </ToastProvider>
  );
}

function renderPanel() {
  return render(panel());
}

describe("CoverLetterPanel", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("blocks copy and save until the user confirms an unsupported claim", async () => {
    apiMocks.generateCoverLetter.mockResolvedValueOnce({
      markdown: "I build mobile-first products.",
      plainText: "I build mobile-first products.",
      warnings: ["mentions_skill_not_in_resume: mobile-first"],
      requiresConfirmation: true,
    });
    renderPanel();

    fireEvent.click(screen.getByRole("button", { name: /generate cover letter/i }));
    await waitFor(() => expect(screen.getByText(/I build mobile-first products/)).toBeTruthy());

    expect(screen.getByRole("button", { name: /^Copy$/i })).toHaveProperty("disabled", true);
    expect(screen.getByRole("button", { name: /Save as \.md/i })).toHaveProperty("disabled", true);

    fireEvent.click(screen.getByRole("checkbox", { name: /confirm/i }));

    expect(screen.getByRole("button", { name: /^Copy$/i })).toHaveProperty("disabled", false);
    expect(screen.getByRole("button", { name: /Save as \.md/i })).toHaveProperty("disabled", false);
  });

  it("leaves copy and save enabled for a clean letter", async () => {
    apiMocks.generateCoverLetter.mockResolvedValueOnce({
      markdown: "I ship products end to end.",
      plainText: "I ship products end to end.",
      warnings: [],
      requiresConfirmation: false,
    });
    renderPanel();

    fireEvent.click(screen.getByRole("button", { name: /generate cover letter/i }));
    await waitFor(() => expect(screen.getByText(/I ship products end to end/)).toBeTruthy());

    expect(screen.queryByRole("checkbox", { name: /confirm/i })).toBeNull();
    expect(screen.getByRole("button", { name: /^Copy$/i })).toHaveProperty("disabled", false);
  });

  it("drops a letter written for a job the user has moved on from", async () => {
    apiMocks.generateCoverLetter.mockResolvedValueOnce({
      markdown: "Letter for the fintech role.",
      plainText: "Letter for the fintech role.",
      warnings: [],
      requiresConfirmation: false,
    });
    const { rerender } = render(panel({ jobId: "job:fintech" }));

    fireEvent.click(screen.getByRole("button", { name: /generate cover letter/i }));
    await waitFor(() => expect(screen.getByText(/Letter for the fintech role/)).toBeTruthy());

    rerender(panel({ jobId: "job:marketplace" }));

    await waitFor(() => expect(screen.queryByText(/Letter for the fintech role/)).toBeNull());
    expect(screen.getByRole("button", { name: /^Generate cover letter$/i })).toBeTruthy();
  });

  it("drops a letter when a fresh gap analysis retargets the same pasted job", async () => {
    apiMocks.generateCoverLetter.mockResolvedValueOnce({
      markdown: "Letter written against the first gap.",
      plainText: "Letter written against the first gap.",
      warnings: [],
      requiresConfirmation: false,
    });
    const firstGap = { found: [], partial: [], missing: [], toConfirm: [] } as unknown as GapResult;
    const { rerender } = render(panel({ gap: firstGap }));

    fireEvent.click(screen.getByRole("button", { name: /generate cover letter/i }));
    await waitFor(() => expect(screen.getByText(/against the first gap/)).toBeTruthy());

    const secondGap = { found: [], partial: [], missing: [], toConfirm: [] } as unknown as GapResult;
    rerender(panel({ gap: secondGap }));

    await waitFor(() => expect(screen.queryByText(/against the first gap/)).toBeNull());
  });

  it("keeps the letter while the user edits the pasted description", async () => {
    apiMocks.generateCoverLetter.mockResolvedValueOnce({
      markdown: "Letter that survives a typo fix.",
      plainText: "Letter that survives a typo fix.",
      warnings: [],
      requiresConfirmation: false,
    });
    const { rerender } = render(panel({ jobDescription: "Mobile-first product desgin." }));

    fireEvent.click(screen.getByRole("button", { name: /generate cover letter/i }));
    await waitFor(() => expect(screen.getByText(/survives a typo fix/)).toBeTruthy());

    rerender(panel({ jobDescription: "Mobile-first product design." }));

    expect(screen.getByText(/survives a typo fix/)).toBeTruthy();
  });

  it("re-arms the confirmation gate when a new letter is generated", async () => {
    apiMocks.generateCoverLetter
      .mockResolvedValueOnce({
        markdown: "First draft with mobile-first.",
        plainText: "First draft with mobile-first.",
        warnings: ["mentions_skill_not_in_resume: mobile-first"],
        requiresConfirmation: true,
      })
      .mockResolvedValueOnce({
        markdown: "Second draft still with mobile-first.",
        plainText: "Second draft still with mobile-first.",
        warnings: ["mentions_skill_not_in_resume: mobile-first"],
        requiresConfirmation: true,
      });
    renderPanel();

    fireEvent.click(screen.getByRole("button", { name: /generate cover letter/i }));
    await waitFor(() => expect(screen.getByText(/First draft/)).toBeTruthy());
    fireEvent.click(screen.getByRole("checkbox", { name: /confirm/i }));
    expect(screen.getByRole("button", { name: /^Copy$/i })).toHaveProperty("disabled", false);

    fireEvent.click(screen.getByRole("button", { name: /regenerate cover letter/i }));
    await waitFor(() => expect(screen.getByText(/Second draft/)).toBeTruthy());

    expect(screen.getByRole("checkbox", { name: /confirm/i })).toHaveProperty("checked", false);
    expect(screen.getByRole("button", { name: /^Copy$/i })).toHaveProperty("disabled", true);
  });
});
