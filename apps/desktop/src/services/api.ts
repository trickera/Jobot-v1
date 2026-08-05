import type {
  AppConfig,
  AIUsageResponse,
  Application,
  ActionResponse,
  BrowserBootstrapStatusResponse,
  BrowserHealthResponse,
  CanonicalResume,
  JobActionRequest,
  JobRequirements,
  JobSummary,
  LogsResponse,
  InstallHealthResponse,
  InstallRepairResponse,
  ModelFetchRequest,
  ModelFetchResponse,
  OCRRunRequest,
  OCRRunResponse,
  OCRStatusResponse,
  ProviderTestResult,
  ResumeAnalyzeJobRequest,
  ResumeAnalyzeJobResponse,
  ResumeCoverLetterRequest,
  ResumeCoverLetterResponse,
  ResumeDiagnoseResponse,
  ResumeExportFormat,
  ResumeExportResponse,
  ResumeGapResponse,
  ResumeLanguageOption,
  ResumeOptimizeResponse,
  ResumePageSize,
  ResumeParseResponse,
  ResumeRenameVersionResponse,
  ResumeSaveVersionRequest,
  ResumeTemplatesResponse,
  ResumeUploadRequest,
  ResumeUploadResponse,
  ResumeVersionsResponse,
  ResumeVoiceOption,
  ScoreResult,
  SearchResponse,
  SearchHistoryEntry,
  SearchPlan,
  SearchStatusResponse,
  ServiceState,
} from "../types";

import { isElectron } from "./runtime";

const API_URL = import.meta.env.VITE_SENCIA_API_URL ?? "http://127.0.0.1:48730";

let tokenPromise: Promise<string> | null = null;

async function resolveToken(): Promise<string> {
  const envToken = import.meta.env.VITE_SENCIA_API_TOKEN;
  if (envToken) {
    return envToken;
  }
  if (isElectron()) {
    return window.senciaElectron!.getApiToken();
  }
  return "sencia-dev";
}

function apiToken(): Promise<string> {
  if (!tokenPromise) {
    tokenPromise = resolveToken().catch((error) => {
      tokenPromise = null;
      throw error;
    });
  }
  return tokenPromise;
}

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly code?: string,
  ) {
    super(message);
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const token = await apiToken();
  const response = await fetch(`${API_URL}${path}`, {
    ...init,
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
      ...init?.headers,
    },
  });

  const raw = await response.text();
  let payload: (T & { message?: string; code?: string }) | null = null;
  if (raw) {
    try {
      payload = JSON.parse(raw) as T & { message?: string; code?: string };
    } catch {
      payload = null;
    }
  }

  if (!response.ok) {
    throw new ApiError(payload?.message ?? (raw.trim() || "Não foi possível concluir a operação."), response.status, payload?.code);
  }

  return payload as T;
}

// Hands back the jobs a radar sweep found above the notification threshold, and
// empties the queue: a job is returned exactly once, so calling this twice does
// not announce the same posting twice.
export function drainNotifications(): Promise<{ jobs: JobSummary[] }> {
  return request<{ jobs: JobSummary[] }>("/api/v1/notifications/drain", { method: "POST" });
}

export function loadState(): Promise<ServiceState> {
  return request<ServiceState>("/api/v1/state");
}

export function loadConfig(): Promise<AppConfig> {
  return request<AppConfig>("/api/v1/config");
}

export function loadLogs(): Promise<LogsResponse> {
  return request<LogsResponse>("/api/v1/logs");
}

export function loadJobs(): Promise<SearchResponse> {
  return request<SearchResponse>("/api/v1/jobs");
}

export function loadSavedJobs(): Promise<{ jobs: JobSummary[] }> {
  return request<{ jobs: JobSummary[] }>("/api/v1/jobs/saved");
}

export function loadApplications(): Promise<{ applications: Application[] }> {
  return request<{ applications: Application[] }>("/api/v1/applications");
}

export function loadSearchHistory(): Promise<{ history: SearchHistoryEntry[] }> {
  return request<{ history: SearchHistoryEntry[] }>("/api/v1/history");
}

export function startBackgroundSearch(): Promise<{ message: string }> {
  return request<{ message: string }>("/api/v1/search", { method: "POST", body: "{}" });
}

export function resetSearchSession(): Promise<{ message: string }> {
  return request<{ message: string }>("/api/v1/search/reset", { method: "POST", body: "{}" });
}

export function fetchSearchStatus(): Promise<SearchStatusResponse> {
  return request<SearchStatusResponse>("/api/v1/search/status");
}

export function fetchSearchPlan(): Promise<SearchPlan> {
  return request<SearchPlan>("/api/v1/search/plan");
}

export function saveConfig(config: AppConfig): Promise<AppConfig> {
  return request<AppConfig>("/api/v1/config", { method: "PUT", body: JSON.stringify(config) });
}

export function fetchAIUsage(): Promise<AIUsageResponse> {
  return request<AIUsageResponse>("/api/v1/ai/usage");
}

export function openJobUrl(url: string): Promise<ActionResponse> {
  return request<ActionResponse>("/api/v1/open-url", { method: "POST", body: JSON.stringify({ url }) });
}

export function applyJobAction(payload: JobActionRequest): Promise<ActionResponse> {
  return request<ActionResponse>("/api/v1/jobs/action", { method: "POST", body: JSON.stringify(payload) });
}

export function fetchModels(payload: ModelFetchRequest): Promise<ModelFetchResponse> {
  return request<ModelFetchResponse>("/api/v1/models", { method: "POST", body: JSON.stringify(payload) });
}

export function testProvider(req: { provider: string; apiKey?: string; model: string }): Promise<ProviderTestResult> {
  return request<ProviderTestResult>("/api/v1/providers/test", { method: "POST", body: JSON.stringify(req) });
}

export function fetchBrowserHealth(): Promise<BrowserHealthResponse> {
  return request<BrowserHealthResponse>("/api/v1/browser/health");
}

export function fetchInstallHealth(): Promise<InstallHealthResponse> {
  return request<InstallHealthResponse>("/api/v1/health/install");
}

export function runInstallRepair(): Promise<InstallRepairResponse> {
  return request<InstallRepairResponse>("/api/v1/health/repair", { method: "POST", body: "{}" });
}

export function startBrowserBootstrap(): Promise<{ message: string }> {
  return request<{ message: string }>("/api/v1/browser/bootstrap", { method: "POST", body: "{}" });
}

export function fetchBrowserBootstrapStatus(): Promise<BrowserBootstrapStatusResponse> {
  return request<BrowserBootstrapStatusResponse>("/api/v1/browser/bootstrap/status");
}

export function getOCRStatus(): Promise<OCRStatusResponse> {
  return request<OCRStatusResponse>("/api/v1/ocr/status");
}

export function installOCR(): Promise<{ message: string }> {
  return request<{ message: string }>("/api/v1/ocr/install", { method: "POST", body: "{}" });
}

export function runOCR(payload: OCRRunRequest): Promise<OCRRunResponse> {
  return request<OCRRunResponse>("/api/v1/ocr/run", { method: "POST", body: JSON.stringify(payload) });
}

export function uploadResume(payload: ResumeUploadRequest): Promise<ResumeUploadResponse> {
  return request<ResumeUploadResponse>("/api/v1/resume", { method: "POST", body: JSON.stringify(payload) });
}

// --- Resume Studio (Appendix C client) ---

// The AI steps run for tens of seconds. Holding a fetch open for the whole
// generation gave a slow provider every chance to look like a frozen app, and
// lost the work outright if the socket hiccuped. The backend now runs these as
// detached jobs (POST /api/v1/resume/async/{op} -> 202 {jobId}) and we poll a
// cheap status route instead, so no single request is ever long-lived.
const RESUME_JOB_POLL_MS = 700;

// Slightly above the backend's own job budget, so a job that fails on the
// server surfaces its typed error rather than being pre-empted by this guard.
const RESUME_JOB_TIMEOUT_MS = 5 * 60 * 1000;

interface ResumeJobStatus<T> {
  state: "running" | "done";
  status?: number;
  result?: T & { message?: string; code?: string };
}

function wait(ms: number): Promise<void> {
  return new Promise((resolve) => {
    setTimeout(resolve, ms);
  });
}

async function runResumeJob<T>(op: string, payload: unknown): Promise<T> {
  const { jobId } = await request<{ jobId: string }>(`/api/v1/resume/async/${op}`, {
    method: "POST",
    body: JSON.stringify(payload),
  });

  const deadline = Date.now() + RESUME_JOB_TIMEOUT_MS;
  while (Date.now() < deadline) {
    await wait(RESUME_JOB_POLL_MS);

    const job = await request<ResumeJobStatus<T>>(`/api/v1/resume/jobs/${encodeURIComponent(jobId)}`);
    if (job.state !== "done") {
      continue;
    }

    const status = job.status ?? 500;
    if (status >= 200 && status < 300) {
      return job.result as T;
    }
    throw new ApiError(
      job.result?.message ?? "Não foi possível concluir a operação.",
      status,
      job.result?.code,
    );
  }

  throw new ApiError("The AI call took too long. Try again, or switch to a faster model in Settings.", 504, "ai_timeout");
}

export function parseResume(text: string): Promise<ResumeParseResponse> {
  return runResumeJob<ResumeParseResponse>("parse", { text });
}

export function diagnoseResume(canonical: CanonicalResume, rawText?: string): Promise<ResumeDiagnoseResponse> {
  return request<ResumeDiagnoseResponse>("/api/v1/resume/diagnose", {
    method: "POST",
    body: JSON.stringify({ canonical, rawText }),
  });
}

export function analyzeJob(payload: ResumeAnalyzeJobRequest): Promise<ResumeAnalyzeJobResponse> {
  return runResumeJob<ResumeAnalyzeJobResponse>("analyze-job", payload);
}

export function gapAnalysis(canonical: CanonicalResume, requirements: JobRequirements): Promise<ResumeGapResponse> {
  return runResumeJob<ResumeGapResponse>("gap", { canonical, requirements });
}

export function optimizeResume(
  canonical: CanonicalResume,
  requirements: JobRequirements,
  confirmed: string[],
  language: ResumeLanguageOption = "en",
  voice: ResumeVoiceOption = "third",
): Promise<ResumeOptimizeResponse> {
  return runResumeJob<ResumeOptimizeResponse>("optimize", { canonical, requirements, confirmed, language, voice });
}

export function scoreResume(canonical: CanonicalResume, requirements: JobRequirements, rawText?: string): Promise<ScoreResult> {
  return request<ScoreResult>("/api/v1/resume/score", {
    method: "POST",
    body: JSON.stringify({ canonical, requirements, rawText }),
  });
}

export function exportResume(
  canonical: CanonicalResume,
  format: ResumeExportFormat,
  templateId: string,
  pageSize: ResumePageSize = "letter",
): Promise<ResumeExportResponse> {
  return request<ResumeExportResponse>("/api/v1/resume/export", {
    method: "POST",
    body: JSON.stringify({ canonical, format, templateId, pageSize }),
  });
}

export function saveResumeVersion(payload: ResumeSaveVersionRequest): Promise<{ id: string }> {
  return request<{ id: string }>("/api/v1/resume/version", { method: "POST", body: JSON.stringify(payload) });
}

export function listResumeVersions(filters: { jobId?: string; documentId?: string }): Promise<ResumeVersionsResponse> {
  const params = new URLSearchParams();
  if (filters.jobId) params.set("jobId", filters.jobId);
  if (filters.documentId) params.set("documentId", filters.documentId);
  return request<ResumeVersionsResponse>(`/api/v1/resume/versions?${params.toString()}`);
}

export function deleteResumeVersion(id: string): Promise<void> {
  return request<void>(`/api/v1/resume/versions/${encodeURIComponent(id)}`, { method: "DELETE" });
}

export function renameResumeVersion(id: string, name: string): Promise<ResumeRenameVersionResponse> {
  return request<ResumeRenameVersionResponse>(`/api/v1/resume/versions/${encodeURIComponent(id)}`, {
    method: "PATCH",
    body: JSON.stringify({ name }),
  });
}

export function listResumeTemplates(): Promise<ResumeTemplatesResponse> {
  return request<ResumeTemplatesResponse>("/api/v1/resume/templates");
}

export function generateCoverLetter(payload: ResumeCoverLetterRequest): Promise<ResumeCoverLetterResponse> {
  return runResumeJob<ResumeCoverLetterResponse>("cover-letter", payload);
}
