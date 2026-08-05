import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Application, JobSummary, SearchHistoryEntry } from "../../types";
import { ApplicationsView, HistoryView, SavedJobsView } from "./LibraryViews";

const apiMocks = vi.hoisted(() => ({
  applyJobAction: vi.fn(),
  loadApplications: vi.fn(),
  loadSavedJobs: vi.fn(),
  loadSearchHistory: vi.fn(),
  openJobUrl: vi.fn(),
}));

vi.mock("../../services/api", () => ({
  ApiError: class ApiError extends Error {},
  ...apiMocks,
}));

const job: JobSummary = {
  id: "job-1",
  source: "LinkedIn",
  title: "Platform Engineer",
  company: "Acme",
  location: "Remote",
  url: "https://example.com/jobs/1",
  status: "applied",
  score: 88,
  missingKeywords: [],
  savedAt: "2026-07-13T12:00:00Z",
};

describe("persistent library views", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMocks.applyJobAction.mockResolvedValue({ ok: true, message: "ok" });
    apiMocks.openJobUrl.mockResolvedValue({ ok: true, message: "ok" });
  });

  afterEach(() => cleanup());

  it("loads saved jobs and removes a bookmark after a successful request", async () => {
    apiMocks.loadSavedJobs.mockResolvedValue({ jobs: [job] });
    render(<SavedJobsView />);

    expect(await screen.findByText("Platform Engineer")).not.toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Remover Platform Engineer das salvas" }));

    await waitFor(() => expect(apiMocks.applyJobAction).toHaveBeenCalledWith({ action: "unsave", job }));
    await waitFor(() => expect(screen.queryByText("Platform Engineer")).toBeNull());
  });

  it("opens the exact saved job URL and renders its compact compatibility gauge", async () => {
    apiMocks.loadSavedJobs.mockResolvedValue({ jobs: [job] });
    render(<SavedJobsView />);

    expect(await screen.findByRole("img", { name: "Compatibilidade 88 de 100" })).not.toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Abrir vaga" }));

    await waitFor(() => expect(apiMocks.openJobUrl).toHaveBeenCalledWith(job.url));
  });

  it("keeps a persisted pending score honest instead of announcing zero", async () => {
    apiMocks.loadSavedJobs.mockResolvedValue({ jobs: [{ ...job, score: 0, scoringPending: true }] });
    render(<SavedJobsView />);

    expect(await screen.findByRole("img", { name: "Compatibilidade em analise" })).not.toBeNull();
    expect(screen.queryByRole("img", { name: "Compatibilidade 0 de 100" })).toBeNull();
  });

  it("renders applications returned by the read route", async () => {
    const application: Application = {
      id: "application:job-1",
      jobId: job.id,
      status: "applied",
      createdAt: "2026-07-13T12:00:00Z",
      updatedAt: "2026-07-13T12:30:00Z",
      job,
    };
    apiMocks.loadApplications.mockResolvedValue({ applications: [application] });
    render(<ApplicationsView />);

    expect(await screen.findByText("Platform Engineer")).not.toBeNull();
    expect(screen.getByText("applied")).not.toBeNull();
  });

  it("renders search history with the effective query and result count", async () => {
    const entry: SearchHistoryEntry = {
      id: "search-1",
      query: "Backend Engineer",
      filters: { workMode: "remote", location: "Brazil", recentHours: 24 },
      resultsCount: 7,
      createdAt: "2026-07-13T12:00:00Z",
    };
    apiMocks.loadSearchHistory.mockResolvedValue({ history: [entry] });
    render(<HistoryView />);

    expect(await screen.findByText("Backend Engineer")).not.toBeNull();
    expect(screen.getByText("remote / Brazil / ultimas 24h")).not.toBeNull();
    expect(screen.getByText("7")).not.toBeNull();
  });

  it("keeps previously loaded jobs visible when a refresh fails", async () => {
    apiMocks.loadSavedJobs.mockResolvedValueOnce({ jobs: [job] }).mockRejectedValueOnce(new Error("backend offline"));
    render(<SavedJobsView />);

    expect(await screen.findByText("Platform Engineer")).not.toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Atualizar" }));

    expect(await screen.findByRole("alert")).not.toBeNull();
    expect(screen.getByText("Platform Engineer")).not.toBeNull();
    expect(screen.queryByText("Nenhuma vaga salva")).toBeNull();
  });

  it("does not claim a library is empty when its read request failed", async () => {
    const cases = [
      {
        setup: () => apiMocks.loadSavedJobs.mockRejectedValue(new Error("backend offline")),
        renderView: () => render(<SavedJobsView />),
        alert: "Nao foi possivel carregar as vagas salvas.",
        empty: "Nenhuma vaga salva",
      },
      {
        setup: () => apiMocks.loadApplications.mockRejectedValue(new Error("backend offline")),
        renderView: () => render(<ApplicationsView />),
        alert: "Nao foi possivel carregar as candidaturas.",
        empty: "Nenhuma candidatura registrada",
      },
      {
        setup: () => apiMocks.loadSearchHistory.mockRejectedValue(new Error("backend offline")),
        renderView: () => render(<HistoryView />),
        alert: "Nao foi possivel carregar o historico.",
        empty: "Nenhuma busca registrada",
      },
    ];

    for (const item of cases) {
      item.setup();
      const view = item.renderView();
      expect((await screen.findByRole("alert")).textContent).toContain(item.alert);
      expect(screen.queryByText(item.empty)).toBeNull();
      view.unmount();
      cleanup();
    }
  });
});
