import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AppConfig, JobSummary, SearchDiagnostics, SearchPlan } from "../../types";
import { SearchView } from "./SearchView";

const apiMocks = vi.hoisted(() => ({
  applyJobAction: vi.fn(),
  fetchSearchPlan: vi.fn(),
  loadConfig: vi.fn(),
  openJobUrl: vi.fn(),
  saveConfig: vi.fn(),
}));

const searchMocks = vi.hoisted(() => ({
  setJobSaved: vi.fn(),
  startSearch: vi.fn(),
  diagnostics: null as SearchDiagnostics | null,
  jobs: [] as JobSummary[],
  lowScoreJobs: [] as JobSummary[],
  running: false,
  notice: null as { tone: "neutral" | "error"; text: string } | null,
  liveCount: 0,
}));

vi.mock("../../services/api", () => ({
  ApiError: class ApiError extends Error {
    constructor(message: string, readonly status: number, readonly code?: string) {
      super(message);
    }
  },
  ...apiMocks,
}));

vi.mock("../../app/SearchContext", () => ({
  useSearch: () => ({
    jobs: searchMocks.jobs,
    lowScoreJobs: searchMocks.lowScoreJobs,
    running: searchMocks.running,
    notice: searchMocks.notice,
    liveCount: searchMocks.liveCount,
    diagnostics: searchMocks.diagnostics,
    setJobSaved: searchMocks.setJobSaved,
    startSearch: searchMocks.startSearch,
  }),
}));

const conflictedPlan: SearchPlan = {
  roles: ["Registered Nurse"],
  rolesSource: "profiles",
  ignoredRoles: ["Backend Developer"],
  levels: ["Senior"],
  excludedLevels: ["Lead"],
  scoringTerms: ["AWS", "Terraform"],
  scoringSource: "keywords",
  staleKeywords: true,
  keywordsForRoles: ["Backend Developer"],
  workMode: "onsite",
  locations: [{ location: "Chicago, IL", remote: false }],
  sources: ["LinkedIn", "Remotive"],
  summary: "Registered Nurse in Chicago",
};

function testConfig(): AppConfig {
  return {
    version: 1,
    form: {
      role: "Backend Developer",
      roles: "Backend Developer",
      searchProfiles: "Registered Nurse | Senior | Lead |",
      keywords: "AWS, Terraform",
      keywordsForRoles: "Backend Developer",
    } as AppConfig["form"],
    toggles: {} as AppConfig["toggles"],
    localItems: { jobs: 0, saved: 0, applications: 0, history: 0 },
  };
}

function resultJob(id: string, title: string, score: number, location = "Chicago, IL"): JobSummary {
  return {
    id,
    source: id === "job-2" ? "Indeed" : "LinkedIn",
    title,
    company: id === "job-2" ? "Beta Health" : "Acme Health",
    location,
    url: `https://example.com/${id}`,
    status: "[APPLY NOW]",
    score,
    missingKeywords: [],
  };
}

describe("SearchView effective search plan", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    searchMocks.diagnostics = null;
    searchMocks.jobs = [];
    searchMocks.lowScoreJobs = [];
    searchMocks.running = false;
    searchMocks.notice = null;
    searchMocks.liveCount = 0;
    apiMocks.fetchSearchPlan.mockResolvedValue(conflictedPlan);
    apiMocks.loadConfig.mockResolvedValue(testConfig());
    apiMocks.saveConfig.mockImplementation(async (config: AppConfig) => config);
    apiMocks.applyJobAction.mockResolvedValue({ ok: true, message: "Vaga salva." });
  });

  afterEach(() => cleanup());

  it("shows the actual roles, location, sources, and both persistent conflicts before search", async () => {
    render(<SearchView />);

    expect((await screen.findAllByText("Registered Nurse")).length).toBeGreaterThan(0);
    const disclosure = screen.getByRole("button", { name: "Como foi definido" });
    expect(disclosure.getAttribute("aria-expanded")).toBe("false");
    fireEvent.click(disclosure);
    expect(disclosure.getAttribute("aria-expanded")).toBe("true");
    expect(screen.getByText("Chicago, IL (presencial)")).not.toBeNull();
    expect(screen.getByText("LinkedIn, Remotive")).not.toBeNull();
    expect(screen.getByText(/Perfis avancados estao ignorando o cargo simples: Backend Developer/)).not.toBeNull();
    expect(screen.getByText(/As keywords foram escritas para Backend Developer/)).not.toBeNull();
  });

  it("renders the first-run plan when empty Go slices arrive as null", async () => {
    apiMocks.fetchSearchPlan.mockResolvedValue({
      roles: null,
      rolesSource: "role",
      levels: ["Junior", "Pleno", "Senior"],
      excludedLevels: ["Lead", "Staff"],
      scoringTerms: null,
      scoringSource: "none",
      staleKeywords: false,
      workMode: "remote",
      locations: [{ location: "Brazil", remote: true }],
      sources: ["Indeed", "LinkedIn"],
      summary: "First-run search plan",
    } as unknown as SearchPlan);

    render(<SearchView />);

    expect((await screen.findAllByText("Nenhum cargo configurado")).length).toBeGreaterThan(0);
    fireEvent.click(screen.getByRole("button", { name: "Como foi definido" }));
    expect(screen.getByText("Nenhuma")).not.toBeNull();
    expect(screen.getByText("Brazil (remota)")).not.toBeNull();
  });

  it("disables search when the effective plan has no configured role", async () => {
    apiMocks.fetchSearchPlan.mockResolvedValue({
      ...conflictedPlan,
      roles: [],
      ignoredRoles: [],
      rolesSource: "role",
      staleKeywords: false,
    });

    render(<SearchView />);

    const button = await screen.findByRole("button", { name: "Buscar vagas" });
    expect((button as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(button);
    expect(searchMocks.startSearch).not.toHaveBeenCalled();
  });

  it("sends first-run setup directly to the profile settings section", async () => {
    const onOpenSettings = vi.fn();
    render(<SearchView onOpenSettings={onOpenSettings} />);

    fireEvent.click(await screen.findByRole("button", { name: "Configurar busca" }));

    expect(onOpenSettings).toHaveBeenCalledWith("profile");
  });

  it("uses a full-width ready state before results and keeps technical plan details collapsed", async () => {
    render(<SearchView />);

    expect(await screen.findByRole("heading", { name: "Tudo pronto para procurar" })).not.toBeNull();
    expect(screen.getByRole("button", { name: "Buscar agora" })).not.toBeNull();
    expect(document.querySelector(".results-layout.is-empty")).not.toBeNull();
    expect(document.querySelector(".detail-panel")).toBeNull();
    expect((screen.getByRole("button", { name: "Como foi definido" }) as HTMLButtonElement).getAttribute("aria-expanded")).toBe("false");
  });

  it("shows source progress while a search is running with no results yet", async () => {
    searchMocks.running = true;

    render(<SearchView />);

    expect(await screen.findByRole("heading", { name: "Buscando vagas..." })).not.toBeNull();
    expect(screen.getByText("Consultando 2 fontes")).not.toBeNull();
    expect(document.querySelector(".search-running-track")).not.toBeNull();
    expect(screen.getAllByText("LinkedIn").length).toBeGreaterThan(0);
  });

  it("uses the ignored simple role only after an explicit user action", async () => {
    render(<SearchView />);
    fireEvent.click(await screen.findByRole("button", { name: "Usar cargo simples" }));

    await waitFor(() => expect(apiMocks.saveConfig).toHaveBeenCalledTimes(1));
    const saved = apiMocks.saveConfig.mock.calls[0][0] as AppConfig;
    expect(saved.form.searchProfiles).toBe("");
    expect(saved.form.roles).toBe("Backend Developer");
    expect(saved.form.role).toBe("Backend Developer");
    expect(screen.getByRole("heading", { name: "Tudo pronto para procurar" })).not.toBeNull();
  });

  it("clears inherited keywords only after the explicit clear action", async () => {
    render(<SearchView />);
    fireEvent.click(await screen.findByRole("button", { name: "Limpar keywords" }));

    await waitFor(() => expect(apiMocks.saveConfig).toHaveBeenCalledTimes(1));
    const saved = apiMocks.saveConfig.mock.calls[0][0] as AppConfig;
    expect(saved.form.keywords).toBe("");
    expect(saved.form.keywordsForRoles).toBe("");
  });

  it("accepts inherited keywords by moving their provenance to the effective roles", async () => {
    render(<SearchView />);
    fireEvent.click(await screen.findByRole("button", { name: "Manter keywords" }));

    await waitFor(() => expect(apiMocks.saveConfig).toHaveBeenCalledTimes(1));
    const saved = apiMocks.saveConfig.mock.calls[0][0] as AppConfig;
    expect(saved.form.keywords).toBe("AWS, Terraform");
    expect(saved.form.keywordsForRoles).toBe("Registered Nurse");
  });

  it("can clear advanced profiles without rewriting the simple role", async () => {
    const config = testConfig();
    config.form.roles = "Backend Developer, API Engineer";
    apiMocks.loadConfig.mockResolvedValue(config);

    render(<SearchView />);
    fireEvent.click(await screen.findByRole("button", { name: "Limpar perfis avancados" }));

    await waitFor(() => expect(apiMocks.saveConfig).toHaveBeenCalledTimes(1));
    const saved = apiMocks.saveConfig.mock.calls[0][0] as AppConfig;
    expect(saved.form.searchProfiles).toBe("");
    expect(saved.form.roles).toBe("Backend Developer, API Engineer");
  });

  it("shows degradation for every offline score, not only exhausted quota", async () => {
    searchMocks.diagnostics = {
      collected: 3,
      fresh: 3,
      evaluated: 3,
      approved: 2,
      discarded: 1,
      dropped: 0,
      skippedNoDescription: 0,
      detailFetched: 0,
      scoredOffline: 2,
      skippedByPrefilter: 1,
    };

    render(<SearchView />);
    expect(await screen.findByText(/2 vaga\(s\) receberam uma estimativa offline/)).not.toBeNull();
    expect(screen.getByText(/1 ficaram fora do prefilter de IA/)).not.toBeNull();
  });

  it("points an offline search blocked by consent to the exact Settings action", async () => {
    searchMocks.diagnostics = {
      collected: 1, fresh: 1, evaluated: 1, approved: 1, discarded: 0, dropped: 0,
      skippedNoDescription: 0, detailFetched: 1, scoredOffline: 1, aiConsentRequired: true,
    };

    render(<SearchView />);
    expect(await screen.findByText(/Autorize o processamento seguro em Configuracoes > Provedores de IA/)).not.toBeNull();
  });

  it("bookmarks a result without removing it from the active search", async () => {
    searchMocks.jobs = [{
      id: "job-1", source: "LinkedIn", title: "Platform Engineer", company: "Acme",
      location: "Remote", url: "https://example.com/job", status: "[APPLY NOW]",
      score: 88, missingKeywords: [],
    }];

    render(<SearchView />);
    fireEvent.click(await screen.findByRole("button", { name: "Salvar" }));

    await waitFor(() => expect(apiMocks.applyJobAction).toHaveBeenCalledWith({ action: "save", job: searchMocks.jobs[0] }));
    expect(searchMocks.setJobSaved).toHaveBeenCalledWith(searchMocks.jobs[0].id, true);
    expect(screen.getAllByText("Platform Engineer").length).toBeGreaterThan(0);
    expect(screen.getByRole("button", { name: "Salva" })).not.toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Salva" }));
    await waitFor(() => expect(apiMocks.applyJobAction).toHaveBeenLastCalledWith({ action: "unsave", job: searchMocks.jobs[0] }));
    expect(searchMocks.setJobSaved).toHaveBeenLastCalledWith(searchMocks.jobs[0].id, false);
  });

  it("keeps the selected result open after saving it from the detail pane", async () => {
    searchMocks.jobs = [
      resultJob("job-1", "Registered Nurse", 92),
      resultJob("job-2", "Clinical Nurse II", 87),
    ];

    render(<SearchView />);
    fireEvent.click(await screen.findByRole("button", { name: "Ver detalhes de Clinical Nurse II em Beta Health" }));
    const detail = document.querySelector(".detail-panel") as HTMLElement;
    expect(within(detail).getByRole("heading", { name: "Clinical Nurse II" })).not.toBeNull();

    fireEvent.click(within(detail).getByRole("button", { name: "Salvar" }));
    await waitFor(() => expect(apiMocks.applyJobAction).toHaveBeenCalledWith({ action: "save", job: searchMocks.jobs[1] }));
    expect(within(detail).getByRole("heading", { name: "Clinical Nurse II" })).not.toBeNull();
  });

  it("explains when completed results are hidden by the modality filter", async () => {
    searchMocks.jobs = [
      resultJob("job-1", "Registered Nurse", 92),
      resultJob("job-2", "Clinical Nurse II", 87),
    ];

    render(<SearchView />);
    fireEvent.click(await screen.findByRole("button", { name: "Remotas" }));

    expect(screen.getByRole("heading", { name: "Nenhuma vaga neste filtro" })).not.toBeNull();
    expect(screen.getByText(/2 vaga\(s\) encontradas, mas ocultas pelo filtro de modalidade/)).not.toBeNull();
  });

  it("keeps low-score jobs out of the recommended tab until the user opens them", async () => {
    searchMocks.jobs = [resultJob("job-1", "Recommended Nurse", 92)];
    searchMocks.lowScoreJobs = [{ ...resultJob("job-low", "Low Score Nurse", 44), status: "[DISCARD]" }];

    render(<SearchView />);

    const recommendedTab = await screen.findByRole("tab", { name: "Recomendadas (1)" });
    expect(recommendedTab.getAttribute("aria-selected")).toBe("true");
    expect(screen.getAllByText("Recommended Nurse").length).toBeGreaterThan(0);
    expect(screen.queryByText("Low Score Nurse")).toBeNull();

    fireEvent.click(screen.getByRole("tab", { name: "Score baixo (1)" }));
    expect(screen.getAllByText("Low Score Nurse").length).toBeGreaterThan(0);
    expect(screen.queryByText("Recommended Nurse")).toBeNull();
  });

  it("moves focus and selection between result tabs with the keyboard", async () => {
    searchMocks.jobs = [resultJob("job-1", "Recommended Nurse", 92)];
    searchMocks.lowScoreJobs = [{ ...resultJob("job-low", "Low Score Nurse", 44), status: "[DISCARD]" }];

    render(<SearchView />);

    const recommendedTab = await screen.findByRole("tab", { name: "Recomendadas (1)" });
    recommendedTab.focus();
    fireEvent.keyDown(recommendedTab, { key: "ArrowRight" });

    const lowScoreTab = screen.getByRole("tab", { name: "Score baixo (1)" });
    expect(lowScoreTab.getAttribute("aria-selected")).toBe("true");
    expect(document.activeElement).toBe(lowScoreTab);
  });

  it("adjusts an invalid detail selection when the active result group changes", async () => {
    searchMocks.jobs = [
      resultJob("job-1", "Recommended Nurse", 92),
      resultJob("job-2", "Recommended Nurse II", 87),
    ];
    searchMocks.lowScoreJobs = [{ ...resultJob("job-low", "Low Score Nurse", 44), status: "[DISCARD]" }];

    render(<SearchView />);
    fireEvent.click(await screen.findByRole("button", { name: "Ver detalhes de Recommended Nurse II em Beta Health" }));
    let detail = document.querySelector(".detail-panel") as HTMLElement;
    expect(within(detail).getByRole("heading", { name: "Recommended Nurse II" })).not.toBeNull();

    fireEvent.click(screen.getByRole("tab", { name: "Score baixo (1)" }));
    await waitFor(() => {
      detail = document.querySelector(".detail-panel") as HTMLElement;
      expect(within(detail).getByRole("heading", { name: "Low Score Nurse" })).not.toBeNull();
    });

    fireEvent.click(screen.getByRole("tab", { name: "Recomendadas (2)" }));
    await waitFor(() => {
      detail = document.querySelector(".detail-panel") as HTMLElement;
      expect(within(detail).getByRole("heading", { name: "Recommended Nurse" })).not.toBeNull();
    });
  });

  it("reuses manual job actions in the low-score tab without acting automatically", async () => {
    const lowScoreJob = { ...resultJob("job-low", "Low Score Nurse", 44), status: "[DISCARD]" };
    searchMocks.lowScoreJobs = [lowScoreJob];

    render(<SearchView />);
    fireEvent.click(await screen.findByRole("tab", { name: "Score baixo (1)" }));

    expect(apiMocks.applyJobAction).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Salvar" }));
    await waitFor(() => expect(apiMocks.applyJobAction).toHaveBeenCalledWith({ action: "save", job: lowScoreJob }));
    expect(searchMocks.setJobSaved).toHaveBeenCalledWith(lowScoreJob.id, true);
    expect(screen.getByRole("button", { name: "Salva" })).not.toBeNull();
  });
});
