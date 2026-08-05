export type ViewId =
  | "search"
  | "saved"
  | "applications"
  | "history"
  | "logs"
  | "settings"
  | "resume";

export type ServiceState = {
  service: string;
  status: "ready" | "running" | "offline";
  version: string;
  jobs: number;
  saved: number;
  applications: number;
  sources: number;
  radar?: RadarStatus;
};

export type RadarStatus = {
  enabled: boolean;
  running: boolean;
  nextRun?: string;
  lastRun?: string;
  lastStatus?: string;
};

export type ScoreSource = "ai" | "ai_cache" | "offline_prefilter" | "offline_fallback" | "offline_no_key";

export type JobSummary = {
  id: string;
  source: string;
  title: string;
  company: string;
  location: string;
  url: string;
  status: string;
  score: number;
  missingKeywords: string[];
  description?: string;
  profile?: string;
  scoreSource?: ScoreSource;
  scoreReason?: string;
  scoringPending?: boolean;
  savedAt?: string;
};

export type SearchResponse = {
  message: string;
  jobs: JobSummary[];
  lowScoreJobs: JobSummary[];
  diagnostics?: SearchDiagnostics;
};

export type BrowserHealthResponse = {
  pythonFound: boolean;
  pythonPath?: string;
  workerFound: boolean;
  camoufoxImportable: boolean;
  browserInstalled: boolean;
  browserBundled: boolean;
  browserSource?: "bundled" | "downloaded";
  message: string;
};
export type BrowserBootstrapStatusResponse = {
  running: boolean;
  done: boolean;
  success: boolean;
  message: string;
};

export type InstallCheck = {
  id: string;
  ok: boolean;
  label: string;
  note?: string;
};
export type InstallHealthResponse = {
  ok: boolean;
  packaged: boolean;
  checks: InstallCheck[];
  repairAvailable: boolean;
  message: string;
};
export type InstallRepairResponse = {
  ok: boolean;
  checks: InstallCheck[];
  message: string;
};

export type OCRStatusResponse = {
  installed: boolean;
  installing: boolean;
  installMessage?: string;
  tesseractVersion?: string;
  popplerVersion?: string;
  tesseractPath?: string;
  pdftoppmPath?: string;
};
export type OCRRunRequest = {
  fileName: string;
  mimeType: string;
  contentBase64: string;
};
export type OCRRunResponse = {
  text: string;
  pages: number;
  warnings?: string[];
};

export type SourceDiagnostics = {
  collected: number;
  fresh: number;
  evaluated: number;
  approved: number;
  discarded: number;
  dropped: number;
  skippedNoDescription: number;
  detailFetched: number;
  // The board served an anti-bot wall instead of results. Without this, a blocked
  // source is indistinguishable from a source that had nothing to offer: both
  // collect zero.
  blocked?: boolean;
};

export type SearchDiagnostics = SourceDiagnostics & {
  timedOut?: boolean;
  droppedDuplicate?: number;
  droppedDateWindow?: number;
  droppedSeniority?: number;
  droppedBlacklist?: number;
  droppedFakeRemote?: number;
  sources?: Record<string, SourceDiagnostics>;
  suggestions?: string[];
  aiQuotaExhausted?: boolean;
  aiConsentRequired?: boolean;
  scoredOffline?: number;
  scoredFromCache?: number;
  skippedByPrefilter?: number;
};

export type SearchStatusResponse = {
  running: boolean;
  message: string;
  error?: string;
  total: number;
  jobs: JobSummary[];
  lowScoreJobs: JobSummary[];
  diagnostics?: SearchDiagnostics;
};

export type SearchPlanLocation = {
  location: string;
  remote: boolean;
};

export type SearchPlan = {
  roles: string[];
  rolesSource: "profiles" | "role";
  ignoredRoles?: string[];
  levels: string[];
  excludedLevels?: string[];
  scoringTerms: string[];
  scoringSource: "keywords" | "resume" | "none";
  staleKeywords: boolean;
  keywordsForRoles?: string[];
  workMode: "remote" | "hybrid" | "onsite";
  locations: SearchPlanLocation[];
  sources: string[];
  summary: string;
};

export type LogEntry = {
  id: number;
  ts: string;
  level: "info" | "success" | "warning" | "error" | "muted";
  message: string;
};

export type LogsResponse = {
  logs: LogEntry[];
};

export type ActionResponse = {
  ok: boolean;
  message: string;
};

export type JobAction = "applied" | "dismiss" | "blacklist" | "save" | "unsave";

export type JobActionRequest = {
  action: JobAction;
  job: JobSummary;
};

export type Application = {
  id: string;
  jobId: string;
  status: string;
  notes?: string;
  createdAt: string;
  updatedAt: string;
  job: JobSummary;
};

export type SearchHistoryEntry = {
  id: string;
  query: string;
  filters: Record<string, unknown>;
  resultsCount: number;
  createdAt: string;
};

export type ModelFetchRequest = {
  provider: string;
  apiKey?: string;
};

export type ModelFetchResponse = {
  provider: string;
  models: string[];
};

export type ProviderTestResult = {
  ok: boolean;
  provider: string;
  model: string;
  latencyMs: number;
  errorCode?: string;
  message?: string;
  maskedKey: string;
};

export type ResumeUploadRequest = {
  fileName: string;
  mimeType: string;
  contentBase64: string;
};

export type ResumeUploadResponse = {
  fileName: string;
  storedPath: string;
  markdownPath: string;
  markdown: string;
  extractedText: string;
  keywords: string[];
  detectedRole: string;
  detectedSeniority: string;
  detectedLevels: string;
  warnings?: string[];
};

export type SettingsToggleKey =
  | "remoteOnly"
  | "useLinkedin"
  | "useIndeed"
  | "useGupy"
  | "useRemotive"
  | "useRemoteok"
  | "useJobicy"
  | "useArbeitnow"
  | "useWeworkremotely"
  | "headless"
  | "compatibility"
  | "score"
  | "localOnly"
  | "exportReady"
  | "desktop"
  | "daily"
  | "saveHistory"
  | "autoClean"
  | "radarMode";

export type SettingsForm = {
  source: string;
  provider: string;
  model: string;
  apiKey: string;
  fallback1Provider: string;
  fallback1Model: string;
  fallback2Provider: string;
  fallback2Model: string;
  aiMode: "free_economy" | "free_quality";
  aiDataConsent: boolean;
  role: string;
  roles: string;
  seniority: string;
  levels: string;
  excludedLevels: string;
  searchProfiles: string;
  maxYears: number;
  location: string;
  workMode: string;
  onsiteLocation: string;
  remoteCountry: string;
  resumeName: string;
  resumePath: string;
  resumeMarkdownPath: string;
  resumeText: string;
  keywords: string;
  keywordsForRoles: string;
  blacklistCompanies: string;
  recentHours: number;
  maxJobs: number;
  maxDelaySeconds: number;
  radarIntervalMinutes: number;
  notificationThreshold: number;
  linkedinPages: number;
  responseSize: string;
  responseStyle: string;
  basePrompt: string;
  shortcutSearch: string;
  shortcutAsk: string;
  shortcutNotes: string;
  scoreCut: number;
  rankingMode: string;
};

export type SettingsLocalItems = {
  jobs: number;
  saved: number;
  applications: number;
  history: number;
};

export type AppConfig = {
  version: number;
  form: SettingsForm;
  toggles: Record<SettingsToggleKey, boolean>;
  localItems: SettingsLocalItems;
  apiKeySet?: boolean;
  updatedAt?: string;
  notices?: string[];
  modelValidation?: AIModelValidation;
};

export type AIModelValidation = {
  status: "validated" | "migrated" | "unavailable" | "failed";
  requested: string;
  active: string;
  message: string;
  validatedAt: string;
};

export type AIUsageBreakdown = {
  purpose: string;
  provider: string;
  model: string;
  requests: number;
  cacheHits: number;
};

export type AIUsageResponse = {
  day: string;
  mode: "free_economy" | "free_quality";
  consent: boolean;
  requests: number;
  cacheHits: number;
  budget: number;
  remaining: number;
  operationBudgets: Record<string, number>;
  breakdown: AIUsageBreakdown[];
  modelValidation?: AIModelValidation;
};

// --- Resume Studio (Appendix C) ---
// UI strings for this module are written in English by design (the
// product is moving to a 100% English UI); the rest of the app stays PT.

export type ResumeLink = { label: string; url: string };
export type ResumeBasics = {
  name: string;
  headline: string;
  email: string;
  phone: string;
  location: string;
  links: ResumeLink[];
};
export type ResumeTarget = { jobTitle: string; category: string; seniority: string };
export type ResumeSkills = { hard: string[]; soft: string[]; tools: string[] };
export type ResumeExperience = {
  company: string;
  role: string;
  start: string;
  end: string;
  location: string;
  bullets: string[];
};
export type ResumeEducation = {
  institution: string;
  degree: string;
  area: string;
  start: string;
  end: string;
};
export type ResumeProject = { name: string; description: string; url: string; bullets: string[] };
export type ResumeLicense = { name: string; issuer: string; jurisdiction: string; number: string; expires: string };
export type ResumeCertification = { name: string; issuer: string; year: string };
export type ResumeLanguage = { language: string; fluency: string };
export type CanonicalResume = {
  schemaVersion: number;
  basics: ResumeBasics;
  target: ResumeTarget;
  summary: string;
  skills: ResumeSkills;
  experience: ResumeExperience[];
  education: ResumeEducation[];
  projects: ResumeProject[];
  licenses: ResumeLicense[];
  certifications: ResumeCertification[];
  languages: ResumeLanguage[];
  // Capabilities the user explicitly confirmed having during gap analysis,
  // kept separate from `skills` so the UI can show they came from the user's
  // confirmation, not the originally-parsed resume. Reused by the backend
  // anti-invention gate in future analyses.
  confirmedSkills?: string[];
};

// impactMeasured is false when the resume has no bullets to judge yet — true of any
// resume that has not been parsed. Optional so an older backend, which does not send
// it, keeps the previous behaviour rather than silently blanking every Impact bar.
export type AtsScores = {
  readability: number;
  content: number;
  impact: number;
  keywords: number;
  impactMeasured?: boolean;
};
export type AtsIssue = { code: string; severity: "low" | "medium" | "high"; message: string };
export type JobRequirements = {
  category: string;
  jobTitle: string;
  hardRequirements: string[];
  niceToHave: string[];
  seniority: string;
  atsKeywords: string[];
};
export type GapItem = { term: string; evidence: string | string[] };
export type GapResult = { found: GapItem[]; partial: GapItem[]; missing: GapItem[]; toConfirm: GapItem[] };
export type ReviewRisk =
  | "new_metric"
  | "new_skill"
  | "new_certification"
  | "identity_change"
  | "unsupported_claim"
  | "high_rewrite_distance";

export type JsonPatchOp = {
  op: "replace" | "add" | "remove";
  path: string;
  value?: unknown;
  reason: string;
  reviewRisk?: ReviewRisk;
};
export type ScoreResult = { ats: number; hr: number; atsBreakdown: Record<string, number>; hrBreakdown: Record<string, number> };
export type ResumeVersion = {
  id: string;
  name?: string;
  documentId: string;
  jobId: string;
  canonical: CanonicalResume;
  patches: JsonPatchOp[];
  templateId: string;
  atsScore: number;
  hrScore: number;
  createdAt: string;
};

export type ResumeLanguageOption = "en" | "pt" | "es" | "auto";
export type ResumeVoiceOption = "first" | "third";

export type ResumeParseResponse = { documentId: string; canonical: CanonicalResume; warnings?: string[]; providerUsed?: string };
export type ResumeDiagnoseResponse = {
  scores: AtsScores;
  issues: AtsIssue[];
  // Echoes back the structure that was actually scored - either the caller-
  // supplied canonical, or (when `heuristic` is true) a best-effort structure
  // built server-side from raw text with no AI call. Lets the offline (no
  // AI key) flow export a resume without ever calling /resume/parse.
  canonical: CanonicalResume;
  heuristic?: boolean;
};
export type ResumeAnalyzeJobRequest = {
  jobId?: string;
  description?: string;
  category?: string;
  seniority?: string;
};
export type ResumeAnalyzeJobResponse = { requirements: JobRequirements; providerUsed?: string };
export type ResumeGapResponse = { gap: GapResult; providerUsed?: string };
export type ResumeOptimizeResponse = { patches: JsonPatchOp[]; preview: CanonicalResume; rejected: JsonPatchOp[]; providerUsed?: string };
export type ResumeExportFormat = "md" | "html" | "pdf" | "docx";
export type ResumePageSize = "letter" | "a4";
export type ResumeExportResponse = { format: string; content: string; fileName: string };
export type ResumeSaveVersionRequest = {
  documentId: string;
  jobId?: string;
  canonical: CanonicalResume;
  patches: JsonPatchOp[];
  templateId: string;
  atsScore: number;
  hrScore: number;
  gap: GapResult;
};
export type ResumeVersionsResponse = { versions: ResumeVersion[] };
export type ResumeRenameVersionResponse = { id: string; name: string };
export type ResumeCoverLetterTone = "direct" | "professional" | "consultative";
export type ResumeCoverLetterRequest = {
  canonical: CanonicalResume;
  jobId?: string;
  jobDescription?: string;
  company?: string;
  role?: string;
  language?: ResumeLanguageOption;
  tone?: ResumeCoverLetterTone;
  maxWords?: number;
  gap?: GapResult;
  confirmed?: string[];
};
export type ResumeCoverLetterResponse = {
  id?: string;
  markdown: string;
  plainText: string;
  warnings: string[];
  // The letter still claims something the resume cannot back, even after the
  // backend's retry. The UI must gate copy/export behind an explicit
  // confirmation, and the backend does not persist it.
  requiresConfirmation?: boolean;
  providerUsed?: string;
};

export type ResumeTemplate = {
  id: string;
  name: string;
  category: string;
  engine: string;
  isAts: boolean;
};
export type ResumeTemplatesResponse = { templates: ResumeTemplate[] };
