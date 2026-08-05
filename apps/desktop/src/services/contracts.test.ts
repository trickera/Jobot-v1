import appConfig from "../../../../contracts/app-config.json";
import resumeStudio from "../../../../contracts/resume-studio.json";
import searchStatus from "../../../../contracts/search-status.json";
import workerFixture from "../../../../contracts/browser-worker.ndjson?raw";
import { describe, expect, it } from "vitest";
import type { CanonicalResume, JobRequirements, JobSummary, ScoreSource, SettingsForm, SettingsToggleKey } from "../types";

type Obj = Record<string, unknown>;
const toggles: readonly SettingsToggleKey[] = ["remoteOnly", "useLinkedin", "useIndeed", "useGupy", "useRemotive", "useRemoteok", "useJobicy", "useArbeitnow", "useWeworkremotely", "headless", "compatibility", "score", "localOnly", "exportReady", "desktop", "daily", "saveHistory", "autoClean", "radarMode"];
const scoreSources: readonly ScoreSource[] = ["ai", "ai_cache", "offline_prefilter", "offline_fallback", "offline_no_key"];
const reviewRisks = ["new_metric", "new_skill", "new_certification", "identity_change", "unsupported_claim", "high_rewrite_distance"] as const;
const settingsStrings: readonly (keyof SettingsForm)[] = ["source", "provider", "model", "apiKey", "fallback1Provider", "fallback1Model", "fallback2Provider", "fallback2Model", "role", "roles", "seniority", "levels", "excludedLevels", "searchProfiles", "location", "workMode", "onsiteLocation", "remoteCountry", "resumeName", "resumePath", "resumeMarkdownPath", "resumeText", "keywords", "keywordsForRoles", "blacklistCompanies", "responseSize", "responseStyle", "basePrompt", "shortcutSearch", "shortcutAsk", "shortcutNotes", "rankingMode"];
const settingsNumbers: readonly (keyof SettingsForm)[] = ["maxYears", "recentHours", "maxJobs", "maxDelaySeconds", "radarIntervalMinutes", "notificationThreshold", "linkedinPages", "scoreCut"];

function obj(value: unknown, label: string): Obj {
  expect(value, label).not.toBeNull();
  expect(Array.isArray(value), label).toBe(false);
  expect(typeof value, label).toBe("object");
  return value as Obj;
}

function req(value: unknown, label: string, ...keys: string[]) {
  const item = obj(value, label);
  for (const key of keys) expect(item, `${label}.${key}`).toHaveProperty(key);
  return item;
}

function field(item: Obj, key: string, label: string, type: "string" | "number" | "boolean") {
  expect(typeof item[key], `${label}.${key}`).toBe(type);
}

function fields(item: Obj, keys: readonly string[], label: string, type: "string" | "number" | "boolean") {
  keys.forEach((key) => field(item, key, label, type));
}

function optional(item: Obj, key: string, label: string, type: "string" | "number" | "boolean") {
  if (key in item && item[key] !== undefined) field(item, key, label, type);
}

function array(value: unknown, label: string): unknown[] {
  expect(value, label).not.toBeNull();
  expect(Array.isArray(value), label).toBe(true);
  return value as unknown[];
}

function strings(value: unknown, label: string) {
  array(value, label).forEach((entry, index) => expect(typeof entry, `${label}[${index}]`).toBe("string"));
}

function optionalStrings(item: Obj, key: string, label: string) {
  if (key in item && item[key] !== undefined) strings(item[key], `${label}.${key}`);
}

function enumValue(value: unknown, allowed: readonly string[], label: string) {
  expect(typeof value, label).toBe("string");
  expect(allowed, label).toContain(value);
}

function numberMap(value: unknown, label: string) {
  const map = obj(value, label);
  for (const [key, entry] of Object.entries(map)) expect(typeof entry, `${label}.${key}`).toBe("number");
}

function records(value: unknown, label: string, requiredFields: readonly string[], arrayFields: readonly string[] = []) {
  array(value, label).forEach((entry, index) => {
    const item = req(entry, `${label}[${index}]`, ...requiredFields);
    fields(item, requiredFields, `${label}[${index}]`, "string");
    arrayFields.forEach((key) => strings(item[key], `${label}[${index}].${key}`));
  });
}

function validateSettings(value: unknown): asserts value is SettingsForm {
  const form = req(value, "appConfig.form", ...settingsStrings, ...settingsNumbers, "aiMode", "aiDataConsent");
  fields(form, settingsStrings, "appConfig.form", "string");
  fields(form, settingsNumbers, "appConfig.form", "number");
  field(form, "aiDataConsent", "appConfig.form", "boolean");
  enumValue(form.aiMode, ["free_economy", "free_quality"], "appConfig.form.aiMode");
}

function validateAppConfig(value: unknown) {
  const config = req(value, "appConfig", "version", "form", "toggles", "localItems");
  field(config, "version", "appConfig", "number");
  validateSettings(config.form);
  const toggleMap = req(config.toggles, "appConfig.toggles", ...toggles);
  for (const [key, enabled] of Object.entries(toggleMap)) expect(typeof enabled, `appConfig.toggles.${key}`).toBe("boolean");
  const counts = req(config.localItems, "appConfig.localItems", "jobs", "saved", "applications", "history");
  fields(counts, ["jobs", "saved", "applications", "history"], "appConfig.localItems", "number");
  optional(config, "apiKeySet", "appConfig", "boolean");
  optional(config, "updatedAt", "appConfig", "string");
  optionalStrings(config, "notices", "appConfig");
  if ("modelValidation" in config) {
    const validation = req(config.modelValidation, "appConfig.modelValidation", "status", "requested", "active", "message", "validatedAt");
    enumValue(validation.status, ["validated", "migrated", "unavailable", "failed"], "appConfig.modelValidation.status");
    fields(validation, ["requested", "active", "message", "validatedAt"], "appConfig.modelValidation", "string");
  }
}

function validateJob(value: unknown, label: string): asserts value is JobSummary {
  const job = req(value, label, "id", "source", "title", "company", "location", "url", "status", "score", "missingKeywords");
  fields(job, ["id", "source", "title", "company", "location", "url", "status"], label, "string");
  field(job, "score", label, "number");
  strings(job.missingKeywords, `${label}.missingKeywords`);
  for (const key of ["description", "profile", "scoreReason", "savedAt"]) optional(job, key, label, "string");
  optional(job, "scoringPending", label, "boolean");
  if ("scoreSource" in job) enumValue(job.scoreSource, scoreSources, `${label}.scoreSource`);
}

function validateDiagnostics(value: unknown, label: string) {
  const diagnostic = req(value, label, "collected", "fresh", "evaluated", "approved", "discarded", "dropped", "skippedNoDescription", "detailFetched");
  fields(diagnostic, ["collected", "fresh", "evaluated", "approved", "discarded", "dropped", "skippedNoDescription", "detailFetched"], label, "number");
  for (const key of ["timedOut", "aiQuotaExhausted", "aiConsentRequired", "blocked"]) optional(diagnostic, key, label, "boolean");
  for (const key of ["droppedDuplicate", "droppedDateWindow", "droppedSeniority", "droppedBlacklist", "droppedFakeRemote", "scoredOffline", "scoredFromCache", "skippedByPrefilter"]) optional(diagnostic, key, label, "number");
  optionalStrings(diagnostic, "suggestions", label);
  if ("sources" in diagnostic) {
    const sourceMap = obj(diagnostic.sources, `${label}.sources`);
    for (const [source, detail] of Object.entries(sourceMap)) validateDiagnostics(detail, `${label}.sources.${source}`);
  }
}

function validateSearchStatus(value: unknown) {
  const status = req(value, "searchStatus", "running", "message", "total", "jobs", "lowScoreJobs", "diagnostics");
  field(status, "running", "searchStatus", "boolean");
  field(status, "message", "searchStatus", "string");
  field(status, "total", "searchStatus", "number");
  array(status.jobs, "searchStatus.jobs").forEach((job, index) => validateJob(job, `searchStatus.jobs[${index}]`));
  array(status.lowScoreJobs, "searchStatus.lowScoreJobs").forEach((job, index) => validateJob(job, `searchStatus.lowScoreJobs[${index}]`));
  const diagnostics = req(status.diagnostics, "searchStatus.diagnostics", "sources", "suggestions");
  validateDiagnostics(diagnostics, "searchStatus.diagnostics");
  optional(status, "error", "searchStatus", "string");
}

function validateCanonical(value: unknown, label: string): asserts value is CanonicalResume {
  const resume = req(value, label, "schemaVersion", "basics", "target", "summary", "skills", "experience", "education", "projects", "licenses", "certifications", "languages");
  field(resume, "schemaVersion", label, "number");
  const basics = req(resume.basics, `${label}.basics`, "name", "headline", "email", "phone", "location", "links");
  fields(basics, ["name", "headline", "email", "phone", "location"], `${label}.basics`, "string");
  records(basics.links, `${label}.basics.links`, ["label", "url"]);
  fields(req(resume.target, `${label}.target`, "jobTitle", "category", "seniority"), ["jobTitle", "category", "seniority"], `${label}.target`, "string");
  field(resume, "summary", label, "string");
  const skills = req(resume.skills, `${label}.skills`, "hard", "soft", "tools");
  ["hard", "soft", "tools"].forEach((key) => strings(skills[key], `${label}.skills.${key}`));
  records(resume.experience, `${label}.experience`, ["company", "role", "start", "end", "location"], ["bullets"]);
  records(resume.education, `${label}.education`, ["institution", "degree", "area", "start", "end"]);
  records(resume.projects, `${label}.projects`, ["name", "description", "url"], ["bullets"]);
  records(resume.licenses, `${label}.licenses`, ["name", "issuer", "jurisdiction", "number", "expires"]);
  records(resume.certifications, `${label}.certifications`, ["name", "issuer", "year"]);
  records(resume.languages, `${label}.languages`, ["language", "fluency"]);
  optionalStrings(resume, "confirmedSkills", label);
}

function validateRequirements(value: unknown, label: string): asserts value is JobRequirements {
  const requirements = req(value, label, "category", "jobTitle", "hardRequirements", "niceToHave", "seniority", "atsKeywords");
  fields(requirements, ["category", "jobTitle", "seniority"], label, "string");
  ["hardRequirements", "niceToHave", "atsKeywords"].forEach((key) => strings(requirements[key], `${label}.${key}`));
}

function validateGap(value: unknown, label: string) {
  const gap = req(value, label, "found", "partial", "missing", "toConfirm");
  ["found", "partial", "missing", "toConfirm"].forEach((key) => {
    array(gap[key], `${label}.${key}`).forEach((entry, index) => {
      const item = req(entry, `${label}.${key}[${index}]`, "term", "evidence");
      field(item, "term", `${label}.${key}[${index}]`, "string");
      if (Array.isArray(item.evidence)) strings(item.evidence, `${label}.${key}[${index}].evidence`);
      else field(item, "evidence", `${label}.${key}[${index}]`, "string");
    });
  });
}

function validatePatches(value: unknown, label: string) {
  array(value, label).forEach((entry, index) => {
    const patch = req(entry, `${label}[${index}]`, "op", "path", "reason");
    enumValue(patch.op, ["replace", "add", "remove"], `${label}[${index}].op`);
    fields(patch, ["path", "reason"], `${label}[${index}]`, "string");
    if ("reviewRisk" in patch) enumValue(patch.reviewRisk, reviewRisks, `${label}[${index}].reviewRisk`);
  });
}

function validateResumeStudio(value: unknown) {
  const studio = req(value, "resumeStudio", "contractVersion", "canonical", "requirements", "gap", "patches", "parseResponse", "diagnoseResponse", "analyzeJobResponse", "gapResponse", "optimizeResponse", "scoreResponse", "exportRequest", "exportResponse", "saveVersionResponse", "renameVersionResponse", "asyncJobStatus", "errorCodes", "errorResponses", "saveVersionRequest", "versionsResponse", "templatesResponse", "coverLetterRequest", "coverLetterResponse");
  field(studio, "contractVersion", "resumeStudio", "number");
  validateCanonical(studio.canonical, "resumeStudio.canonical");
  validateRequirements(studio.requirements, "resumeStudio.requirements");
  validateGap(studio.gap, "resumeStudio.gap");
  validatePatches(studio.patches, "resumeStudio.patches");

  const parse = req(studio.parseResponse, "resumeStudio.parseResponse", "documentId", "canonical");
  fields(parse, ["documentId"], "resumeStudio.parseResponse", "string");
  validateCanonical(parse.canonical, "resumeStudio.parseResponse.canonical");
  optionalStrings(parse, "warnings", "resumeStudio.parseResponse");
  optional(parse, "providerUsed", "resumeStudio.parseResponse", "string");

  const diagnose = req(studio.diagnoseResponse, "resumeStudio.diagnoseResponse", "scores", "issues", "canonical");
  const scores = req(diagnose.scores, "resumeStudio.diagnoseResponse.scores", "readability", "content", "impact", "keywords");
  fields(scores, ["readability", "content", "impact", "keywords"], "resumeStudio.diagnoseResponse.scores", "number");
  optional(scores, "impactMeasured", "resumeStudio.diagnoseResponse.scores", "boolean");
  array(diagnose.issues, "resumeStudio.diagnoseResponse.issues").forEach((issue, index) => {
    const item = req(issue, `resumeStudio.diagnoseResponse.issues[${index}]`, "code", "severity", "message");
    fields(item, ["code", "message"], `resumeStudio.diagnoseResponse.issues[${index}]`, "string");
    enumValue(item.severity, ["low", "medium", "high"], `resumeStudio.diagnoseResponse.issues[${index}].severity`);
  });
  validateCanonical(diagnose.canonical, "resumeStudio.diagnoseResponse.canonical");
  optional(diagnose, "heuristic", "resumeStudio.diagnoseResponse", "boolean");

  const analyzed = req(studio.analyzeJobResponse, "resumeStudio.analyzeJobResponse", "requirements");
  validateRequirements(analyzed.requirements, "resumeStudio.analyzeJobResponse.requirements");
  optional(analyzed, "providerUsed", "resumeStudio.analyzeJobResponse", "string");
  const gapResponse = req(studio.gapResponse, "resumeStudio.gapResponse", "gap");
  validateGap(gapResponse.gap, "resumeStudio.gapResponse.gap");
  optional(gapResponse, "providerUsed", "resumeStudio.gapResponse", "string");
  const optimize = req(studio.optimizeResponse, "resumeStudio.optimizeResponse", "patches", "preview", "rejected");
  validatePatches(optimize.patches, "resumeStudio.optimizeResponse.patches");
  validateCanonical(optimize.preview, "resumeStudio.optimizeResponse.preview");
  validatePatches(optimize.rejected, "resumeStudio.optimizeResponse.rejected");
  optional(optimize, "providerUsed", "resumeStudio.optimizeResponse", "string");

  const score = req(studio.scoreResponse, "resumeStudio.scoreResponse", "ats", "hr", "atsBreakdown", "hrBreakdown");
  fields(score, ["ats", "hr"], "resumeStudio.scoreResponse", "number");
  numberMap(score.atsBreakdown, "resumeStudio.scoreResponse.atsBreakdown");
  numberMap(score.hrBreakdown, "resumeStudio.scoreResponse.hrBreakdown");

  const exportRequest = req(studio.exportRequest, "resumeStudio.exportRequest", "canonical", "format", "templateId", "pageSize");
  validateCanonical(exportRequest.canonical, "resumeStudio.exportRequest.canonical");
  enumValue(exportRequest.format, ["md", "html", "pdf", "docx"], "resumeStudio.exportRequest.format");
  field(exportRequest, "templateId", "resumeStudio.exportRequest", "string");
  enumValue(exportRequest.pageSize, ["letter", "a4"], "resumeStudio.exportRequest.pageSize");
  const exportResponse = req(studio.exportResponse, "resumeStudio.exportResponse", "format", "content", "fileName");
  enumValue(exportResponse.format, ["md", "html", "pdf", "docx"], "resumeStudio.exportResponse.format");
  fields(exportResponse, ["content", "fileName"], "resumeStudio.exportResponse", "string");

  fields(req(studio.saveVersionResponse, "resumeStudio.saveVersionResponse", "id"), ["id"], "resumeStudio.saveVersionResponse", "string");
  fields(req(studio.renameVersionResponse, "resumeStudio.renameVersionResponse", "id", "name"), ["id", "name"], "resumeStudio.renameVersionResponse", "string");
  const asyncJob = req(studio.asyncJobStatus, "resumeStudio.asyncJobStatus", "running", "done");
  enumValue(req(asyncJob.running, "resumeStudio.asyncJobStatus.running", "state").state, ["running", "done"], "resumeStudio.asyncJobStatus.running.state");
  const done = req(asyncJob.done, "resumeStudio.asyncJobStatus.done", "state", "status", "result");
  enumValue(done.state, ["running", "done"], "resumeStudio.asyncJobStatus.done.state");
  field(done, "status", "resumeStudio.asyncJobStatus.done", "number");
  obj(done.result, "resumeStudio.asyncJobStatus.done.result");

  const codes = [] as string[];
  array(studio.errorCodes, "resumeStudio.errorCodes").forEach((code, index) => {
    field({ code }, "code", `resumeStudio.errorCodes[${index}]`, "string");
    codes.push(code as string);
  });
  expect(codes.length, "resumeStudio.errorCodes").toBeGreaterThan(0);
  array(studio.errorResponses, "resumeStudio.errorResponses").forEach((error, index) => {
    const item = req(error, `resumeStudio.errorResponses[${index}]`, "code", "message");
    const code = item.code as string;
    field(item, "code", `resumeStudio.errorResponses[${index}]`, "string");
    expect(codes).toContain(code);
    field(item, "message", `resumeStudio.errorResponses[${index}]`, "string");
  });

  const save = req(studio.saveVersionRequest, "resumeStudio.saveVersionRequest", "documentId", "canonical", "patches", "templateId", "atsScore", "hrScore", "gap");
  field(save, "documentId", "resumeStudio.saveVersionRequest", "string");
  optional(save, "jobId", "resumeStudio.saveVersionRequest", "string");
  validateCanonical(save.canonical, "resumeStudio.saveVersionRequest.canonical");
  validatePatches(save.patches, "resumeStudio.saveVersionRequest.patches");
  field(save, "templateId", "resumeStudio.saveVersionRequest", "string");
  fields(save, ["atsScore", "hrScore"], "resumeStudio.saveVersionRequest", "number");
  validateGap(save.gap, "resumeStudio.saveVersionRequest.gap");

  const versions = req(studio.versionsResponse, "resumeStudio.versionsResponse", "versions");
  array(versions.versions, "resumeStudio.versionsResponse.versions").forEach((version, index) => {
    const item = req(version, `resumeStudio.versionsResponse.versions[${index}]`, "id", "documentId", "jobId", "canonical", "patches", "templateId", "atsScore", "hrScore", "createdAt");
    fields(item, ["id", "documentId", "jobId", "templateId", "createdAt"], `resumeStudio.versionsResponse.versions[${index}]`, "string");
    optional(item, "name", `resumeStudio.versionsResponse.versions[${index}]`, "string");
    validateCanonical(item.canonical, `resumeStudio.versionsResponse.versions[${index}].canonical`);
    validatePatches(item.patches, `resumeStudio.versionsResponse.versions[${index}].patches`);
    fields(item, ["atsScore", "hrScore"], `resumeStudio.versionsResponse.versions[${index}]`, "number");
  });
  const templates = req(studio.templatesResponse, "resumeStudio.templatesResponse", "templates");
  array(templates.templates, "resumeStudio.templatesResponse.templates").forEach((template, index) => {
    const item = req(template, `resumeStudio.templatesResponse.templates[${index}]`, "id", "name", "category", "engine", "isAts");
    fields(item, ["id", "name", "category", "engine"], `resumeStudio.templatesResponse.templates[${index}]`, "string");
    field(item, "isAts", `resumeStudio.templatesResponse.templates[${index}]`, "boolean");
  });

  const coverRequest = req(studio.coverLetterRequest, "resumeStudio.coverLetterRequest", "canonical");
  validateCanonical(coverRequest.canonical, "resumeStudio.coverLetterRequest.canonical");
  for (const key of ["jobId", "jobDescription", "company", "role"]) optional(coverRequest, key, "resumeStudio.coverLetterRequest", "string");
  if ("language" in coverRequest) enumValue(coverRequest.language, ["en", "pt", "es", "auto"], "resumeStudio.coverLetterRequest.language");
  if ("tone" in coverRequest) enumValue(coverRequest.tone, ["direct", "professional", "consultative"], "resumeStudio.coverLetterRequest.tone");
  optional(coverRequest, "maxWords", "resumeStudio.coverLetterRequest", "number");
  if ("gap" in coverRequest) validateGap(coverRequest.gap, "resumeStudio.coverLetterRequest.gap");
  optionalStrings(coverRequest, "confirmed", "resumeStudio.coverLetterRequest");
  const coverResponse = req(studio.coverLetterResponse, "resumeStudio.coverLetterResponse", "markdown", "plainText", "warnings");
  fields(coverResponse, ["markdown", "plainText"], "resumeStudio.coverLetterResponse", "string");
  strings(coverResponse.warnings, "resumeStudio.coverLetterResponse.warnings");
  optional(coverResponse, "id", "resumeStudio.coverLetterResponse", "string");
  optional(coverResponse, "requiresConfirmation", "resumeStudio.coverLetterResponse", "boolean");
  optional(coverResponse, "providerUsed", "resumeStudio.coverLetterResponse", "string");
}

function validateWorker(value: string) {
  const commands = ["start", "fetch", "fetch_gupy", "warm_indeed", "close"] as const;
  const lines = value.trim().split(/\r?\n/).filter(Boolean);
  expect(lines.length, "browser-worker.ndjson").toBeGreaterThan(0);
  lines.forEach((line, index) => {
    const entry = req(JSON.parse(line) as unknown, `browser-worker.ndjson[${index}]`, "name", "response");
    field(entry, "name", `browser-worker.ndjson[${index}]`, "string");
    const response = req(entry.response, `browser-worker.ndjson[${index}].response`, "ok");
    field(response, "ok", `browser-worker.ndjson[${index}].response`, "boolean");
    if ("request" in entry) {
      const request = req(entry.request, `browser-worker.ndjson[${index}].request`, "cmd");
      const command = request.cmd as string;
      field(request, "cmd", `browser-worker.ndjson[${index}].request`, "string");
      const knownCommand = (commands as readonly string[]).includes(command);
      if (knownCommand) enumValue(command, commands, `browser-worker.ndjson[${index}].request.cmd`);
      else expect(response.ok, `browser-worker.ndjson[${index}].response.ok`).toBe(false);
      if (command === "fetch" || command === "fetch_gupy") field(request, "url", `browser-worker.ndjson[${index}].request`, "string");
      if (command === "fetch") fields(request, ["waitUntil", "waitForSelector"], `browser-worker.ndjson[${index}].request`, "string");
      optional(request, "headless", `browser-worker.ndjson[${index}].request`, "boolean");
    } else {
      field(entry, "requestLine", `browser-worker.ndjson[${index}]`, "string");
      expect(() => JSON.parse(entry.requestLine as string), `browser-worker.ndjson[${index}].requestLine`).toThrow();
    }
    if (response.ok === false) field(response, "error", `browser-worker.ndjson[${index}].response`, "string");
    else {
      optional(response, "html", `browser-worker.ndjson[${index}].response`, "string");
      optional(response, "blocked", `browser-worker.ndjson[${index}].response`, "boolean");
      if ("records" in response) records(response.records, `browser-worker.ndjson[${index}].response.records`, ["id", "title", "careerPageUrl"]);
    }
  });
}

describe("desktop JSON contracts", () => {
  it("validates app-config and tolerates additive fields", () => {
    validateAppConfig(appConfig);
    expect(() => validateAppConfig({ ...appConfig, futureOptional: true, form: { ...appConfig.form, futureOptional: "ignored" } })).not.toThrow();
  });

  it("validates search-status arrays/maps and job enums", () => {
    validateSearchStatus(searchStatus);
    expect(() => validateSearchStatus({ ...searchStatus, futureOptional: true, jobs: searchStatus.jobs.map((job) => ({ ...job, futureOptional: 1 })) })).not.toThrow();
  });

  it("validates Resume Studio DTOs, arrays, enums, maps, and represented errors", () => {
    validateResumeStudio(resumeStudio);
    expect(() => validateResumeStudio({ ...resumeStudio, futureOptional: true })).not.toThrow();
  });

  it("validates browser-worker NDJSON requests and responses", () => {
    validateWorker(workerFixture);
  });
});
