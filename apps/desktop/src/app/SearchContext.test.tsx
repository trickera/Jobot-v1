import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { JobSummary, SearchStatusResponse } from "../types";
import { SearchProvider, useSearch } from "./SearchContext";

const apiMocks = vi.hoisted(() => ({
  fetchBrowserHealth: vi.fn(),
  fetchSearchStatus: vi.fn(),
  resetSearchSession: vi.fn(),
  startBackgroundSearch: vi.fn(),
}));

vi.mock("../services/api", () => ({
  ApiError: class ApiError extends Error {
    constructor(message: string, readonly status: number) {
      super(message);
    }
  },
  ...apiMocks,
}));

function resultJob(id: string): JobSummary {
  return {
    id,
    source: "Indeed",
    title: "Platform Engineer",
    company: "Acme",
    location: "Remote",
    url: `https://example.com/${id}`,
    status: "[DISCARD]",
    score: 42,
    missingKeywords: [],
  };
}

function Probe() {
  const { jobs, lowScoreJobs } = useSearch();
  return <output>{`recommended:${jobs.length}|low:${lowScoreJobs.length}`}</output>;
}

function StatusProbe() {
  const { jobs, lowScoreJobs, running, notice } = useSearch();
  return <output>{`running:${running}|recommended:${jobs.length}|low:${lowScoreJobs.length}|notice:${notice?.text ?? ""}`}</output>;
}

function SavedProbe() {
  const { jobs, lowScoreJobs, setJobSaved } = useSearch();
  return (
    <>
      <output>{`recommended:${jobs[0]?.savedAt ? "saved" : "unsaved"}|low:${lowScoreJobs[0]?.savedAt ? "saved" : "unsaved"}`}</output>
      <button type="button" onClick={() => setJobSaved("recommended-1", false)}>Unsave recommended</button>
      <button type="button" onClick={() => setJobSaved("low-1", true)}>Save low</button>
    </>
  );
}

describe("SearchProvider result groups", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMocks.resetSearchSession.mockResolvedValue({ message: "reset" });
  });

  afterEach(() => cleanup());

  it("keeps low-score jobs separate and normalizes a null recommended list", async () => {
    apiMocks.fetchSearchStatus.mockResolvedValue({
      running: true,
      message: "Buscando",
      total: 1,
      jobs: null,
      lowScoreJobs: [resultJob("low-1")],
    } as unknown as SearchStatusResponse);

    render(<SearchProvider><Probe /></SearchProvider>);

    expect(await screen.findByText("recommended:0|low:1")).not.toBeNull();
  });

  it("normalizes a missing low-score list from an older status response", async () => {
    apiMocks.fetchSearchStatus.mockResolvedValue({
      running: true,
      message: "Buscando",
      total: 1,
      jobs: [resultJob("recommended-1")],
    } as SearchStatusResponse);

    render(<SearchProvider><Probe /></SearchProvider>);

    expect(await screen.findByText("recommended:1|low:0")).not.toBeNull();
  });

  it("keeps successful save changes in both result collections outside SearchView", async () => {
    apiMocks.fetchSearchStatus.mockResolvedValue({
      running: true,
      message: "Buscando",
      total: 2,
      jobs: [{ ...resultJob("recommended-1"), savedAt: "2026-07-15T00:00:00Z" }],
      lowScoreJobs: [resultJob("low-1")],
    } as SearchStatusResponse);

    render(<SearchProvider><SavedProbe /></SearchProvider>);
    expect(await screen.findByText("recommended:saved|low:unsaved")).not.toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Unsave recommended" }));
    expect(screen.getByText("recommended:unsaved|low:unsaved")).not.toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Save low" }));
    expect(screen.getByText("recommended:unsaved|low:saved")).not.toBeNull();
  });
});

describe("SearchProvider session recovery", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMocks.resetSearchSession.mockResolvedValue({ message: "reset" });
  });

  afterEach(() => cleanup());

  it("restores a running search on remount without resetting or starting another search", async () => {
    apiMocks.fetchSearchStatus.mockResolvedValue({
      running: true,
      message: "Buscando vagas...",
      total: 1,
      jobs: [resultJob("active-1")],
      lowScoreJobs: [],
    } as SearchStatusResponse);

    const first = render(<SearchProvider><StatusProbe /></SearchProvider>);
    expect(await screen.findByText("running:true|recommended:1|low:0|notice:Buscando vagas...")).not.toBeNull();
    first.unmount();

    render(<SearchProvider><StatusProbe /></SearchProvider>);
    expect(await screen.findByText("running:true|recommended:1|low:0|notice:Buscando vagas...")).not.toBeNull();
    expect(apiMocks.resetSearchSession).not.toHaveBeenCalled();
    expect(apiMocks.startBackgroundSearch).not.toHaveBeenCalled();
  });

  it("applies a terminal status snapshot recovered from the backend", async () => {
    apiMocks.fetchSearchStatus.mockResolvedValue({
      running: false,
      message: "Busca concluida.",
      total: 2,
      jobs: [resultJob("done-1")],
      lowScoreJobs: [resultJob("done-low-1")],
    } as SearchStatusResponse);

    render(<SearchProvider><StatusProbe /></SearchProvider>);

    expect(await screen.findByText("running:false|recommended:1|low:1|notice:Busca concluida.")).not.toBeNull();
    expect(apiMocks.resetSearchSession).not.toHaveBeenCalled();
    expect(apiMocks.startBackgroundSearch).not.toHaveBeenCalled();
  });

  it("replaces the running view with a terminal snapshot when polling completes", async () => {
    apiMocks.fetchSearchStatus
      .mockResolvedValueOnce({
        running: true,
        message: "Buscando vagas...",
        total: 0,
        jobs: [],
        lowScoreJobs: [],
      } as SearchStatusResponse)
      .mockResolvedValue({
        running: false,
        message: "Busca concluida.",
        total: 1,
        jobs: [resultJob("polled-done-1")],
        lowScoreJobs: [],
      } as SearchStatusResponse);

    render(<SearchProvider><StatusProbe /></SearchProvider>);
    expect(await screen.findByText("running:true|recommended:0|low:0|notice:Buscando vagas...")).not.toBeNull();

    await waitFor(() => {
      expect(screen.getByText("running:false|recommended:1|low:0|notice:Busca concluida.")).not.toBeNull();
    }, { timeout: 2000 });
    expect(apiMocks.resetSearchSession).not.toHaveBeenCalled();
    expect(apiMocks.startBackgroundSearch).not.toHaveBeenCalled();
  });
});
