package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type resumeContractError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type resumeRenameVersionContractResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type resumeContractFixture struct {
	ContractVersion int             `json:"contractVersion"`
	Canonical       CanonicalResume `json:"canonical"`
	Requirements    jobRequirements `json:"requirements"`
	Gap             gapResult       `json:"gap"`
	Patches         []jsonPatchOp   `json:"patches"`

	ParseResponse       resumeParseResponse                 `json:"parseResponse"`
	DiagnoseResponse    diagnoseResponse                    `json:"diagnoseResponse"`
	AnalyzeJobResponse  analyzeJobResponse                  `json:"analyzeJobResponse"`
	GapResponse         gapResponse                         `json:"gapResponse"`
	OptimizeResponse    optimizeResponse                    `json:"optimizeResponse"`
	ScoreResponse       scoreResult                         `json:"scoreResponse"`
	ExportRequest       exportRequest                       `json:"exportRequest"`
	ExportResponse      exportResponse                      `json:"exportResponse"`
	SaveVersionResponse saveVersionResponse                 `json:"saveVersionResponse"`
	RenameVersion       resumeRenameVersionContractResponse `json:"renameVersionResponse"`
	AsyncJobStatus      map[string]resumeJobStatus          `json:"asyncJobStatus"`

	ErrorCodes          []string                `json:"errorCodes"`
	ErrorResponses      []resumeContractError   `json:"errorResponses"`
	SaveVersionRequest  saveVersionRequest      `json:"saveVersionRequest"`
	VersionsResponse    versionsResponse        `json:"versionsResponse"`
	TemplatesResponse   resumeTemplatesResponse `json:"templatesResponse"`
	CoverLetterRequest  coverLetterRequest      `json:"coverLetterRequest"`
	CoverLetterResponse coverLetterResponse     `json:"coverLetterResponse"`
}

func contractRoot(t *testing.T) string {
	t.Helper()

	var starts []string
	if _, sourceFile, _, ok := runtime.Caller(0); ok {
		starts = append(starts, filepath.Dir(sourceFile))
	}
	if workingDir, err := os.Getwd(); err == nil {
		starts = append(starts, workingDir)
	}

	for _, start := range starts {
		for dir := start; ; dir = filepath.Dir(dir) {
			candidate := filepath.Join(dir, "contracts")
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
		}
	}
	t.Fatalf("could not find repository contracts directory from %v", starts)
	return ""
}

func readContract(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join(contractRoot(t), "contracts", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read contract %s: %v", path, err)
	}
	return data
}

func contractObject(t *testing.T, raw []byte, context string) map[string]json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("decode %s object: %v", context, err)
	}
	return object
}

func requireNonNullFields(t *testing.T, object map[string]json.RawMessage, context string, fields ...string) {
	t.Helper()
	for _, field := range fields {
		raw, ok := object[field]
		if !ok {
			t.Errorf("%s: missing required field %q", context, field)
			continue
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			t.Errorf("%s: required field %q must not be null", context, field)
		}
	}
}

func addFutureOptional(raw []byte) []byte {
	object := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &object); err != nil {
		return raw
	}
	object["futureOptional"] = json.RawMessage(`{"enabled":true}`)
	augmented, err := json.Marshal(object)
	if err != nil {
		return raw
	}
	return augmented
}

func assertCanonicalCollections(t *testing.T, label string, canonical CanonicalResume) {
	t.Helper()
	if canonical.Basics.Links == nil {
		t.Errorf("%s basics.links must be a JSON array", label)
	}
	if canonical.Skills.Hard == nil || canonical.Skills.Soft == nil || canonical.Skills.Tools == nil {
		t.Errorf("%s skills arrays must not be nil", label)
	}
	if canonical.Experience == nil || canonical.Education == nil || canonical.Projects == nil ||
		canonical.Licenses == nil || canonical.Certifications == nil || canonical.Languages == nil {
		t.Errorf("%s top-level resume arrays must not be nil", label)
	}
}

func assertRequirementsCollections(t *testing.T, label string, requirements jobRequirements) {
	t.Helper()
	if requirements.HardRequirements == nil || requirements.NiceToHave == nil || requirements.ATSKeywords == nil {
		t.Errorf("%s requirement arrays must not be nil", label)
	}
}

func assertGapCollections(t *testing.T, label string, gap gapResult) {
	t.Helper()
	if gap.Found == nil || gap.Partial == nil || gap.Missing == nil || gap.ToConfirm == nil {
		t.Errorf("%s gap buckets must not be nil", label)
	}
}

func assertPatchCollections(t *testing.T, label string, patches []jsonPatchOp) {
	t.Helper()
	if patches == nil {
		t.Errorf("%s patches must be a JSON array", label)
	}
}

func assertScoreMaps(t *testing.T, label string, score scoreResult) {
	t.Helper()
	if score.ATSBreakdown == nil || score.HRBreakdown == nil {
		t.Errorf("%s score breakdowns must be JSON objects", label)
	}
}

func TestLiveSearchStatusUsesNonNullDiagnosticsAndMissingKeywords(t *testing.T) {
	var state liveSearchState
	state.addJob(jobSummary{ID: "fixture-job", Status: statusApply})

	payload, err := json.Marshal(state.snapshot())
	if err != nil {
		t.Fatal(err)
	}

	var response struct {
		Jobs []struct {
			MissingKeywords []string `json:"missingKeywords"`
		} `json:"jobs"`
		Diagnostics struct {
			Sources     map[string]sourceDiagnostics `json:"sources"`
			Suggestions []string                     `json:"suggestions"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Jobs) != 1 || response.Jobs[0].MissingKeywords == nil {
		t.Fatalf("missingKeywords must be a JSON array, payload=%s", payload)
	}
	if response.Diagnostics.Sources == nil {
		t.Fatalf("diagnostics.sources must be a JSON object, payload=%s", payload)
	}
	if response.Diagnostics.Suggestions == nil {
		t.Fatalf("diagnostics.suggestions must be a JSON array, payload=%s", payload)
	}
}

func TestAppConfigContractFixture(t *testing.T) {
	raw := readContract(t, "app-config.json")
	root := contractObject(t, raw, "app-config")
	requireNonNullFields(t, root, "app-config", "version", "form", "toggles", "localItems", "apiKeySet")

	var config appConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatalf("decode app-config into appConfig: %v", err)
	}
	if config.Toggles == nil {
		t.Fatal("app-config toggles must be a JSON object")
	}
	if config.Form.APIKey != "" {
		t.Fatal("app-config fixture must not carry an API secret")
	}

	form := contractObject(t, root["form"], "app-config.form")
	requireNonNullFields(t, form, "app-config.form",
		"source", "provider", "model", "apiKey", "fallback1Provider", "fallback1Model",
		"fallback2Provider", "fallback2Model", "aiMode", "aiDataConsent", "role", "roles",
		"seniority", "levels", "excludedLevels", "searchProfiles", "maxYears", "location",
		"workMode", "onsiteLocation", "remoteCountry", "resumeName", "resumePath",
		"resumeMarkdownPath", "resumeText", "keywords", "keywordsForRoles", "blacklistCompanies",
		"recentHours", "maxJobs", "maxDelaySeconds", "llmRequestsPerMinute", "llmRequestsPerDay",
		"llmTokensPerMinute", "searchTimeoutSeconds", "radarIntervalMinutes", "notificationThreshold",
		"linkedinPages", "responseSize", "responseStyle", "basePrompt", "shortcutSearch",
		"shortcutAsk", "shortcutNotes", "scoreCut", "rankingMode")
	localItems := contractObject(t, root["localItems"], "app-config.localItems")
	requireNonNullFields(t, localItems, "app-config.localItems", "jobs", "saved", "applications", "history")
	if config.ModelValidation != nil {
		switch config.ModelValidation.Status {
		case "validated", "migrated", "unavailable", "failed":
		default:
			t.Fatalf("unexpected modelValidation.status %q", config.ModelValidation.Status)
		}
	}

	var augmented appConfig
	if err := json.Unmarshal(addFutureOptional(raw), &augmented); err != nil {
		t.Fatalf("future optional app-config field must be ignored: %v", err)
	}
	withoutOptional := contractObject(t, raw, "app-config")
	delete(withoutOptional, "notices")
	delete(withoutOptional, "modelValidation")
	withoutOptionalRaw, err := json.Marshal(withoutOptional)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(withoutOptionalRaw, &config); err != nil {
		t.Fatalf("optional app-config fields must be omittable: %v", err)
	}
}

func TestSearchStatusContractFixture(t *testing.T) {
	raw := readContract(t, "search-status.json")
	root := contractObject(t, raw, "search-status")
	requireNonNullFields(t, root, "search-status", "running", "message", "total", "jobs", "lowScoreJobs", "diagnostics")

	var status searchStatusResponse
	if err := json.Unmarshal(raw, &status); err != nil {
		t.Fatalf("decode search-status into searchStatusResponse: %v", err)
	}
	if status.Jobs == nil || status.LowScoreJobs == nil {
		t.Fatal("search-status jobs collections must be JSON arrays")
	}
	if status.Diagnostics.Sources == nil || status.Diagnostics.Suggestions == nil {
		t.Fatal("search-status diagnostics.sources/suggestions must not be null")
	}

	diagnostics := contractObject(t, root["diagnostics"], "search-status.diagnostics")
	requireNonNullFields(t, diagnostics, "search-status.diagnostics",
		"collected", "fresh", "evaluated", "approved", "discarded", "dropped", "skippedNoDescription",
		"detailFetched", "timedOut", "droppedDuplicate", "droppedDateWindow", "droppedSeniority",
		"droppedBlacklist", "droppedFakeRemote", "sources", "suggestions", "aiQuotaExhausted",
		"aiConsentRequired", "scoredOffline", "scoredFromCache", "skippedByPrefilter")

	allowedStatus := map[string]bool{
		"new": true, statusApply: true, statusAdjust: true, statusDiscard: true,
		statusApplied: true, statusDismissed: true, statusScoring: true,
	}
	allowedScoreSource := map[ScoreSource]bool{
		scoreSourceAI: true, scoreSourceAICache: true, scoreSourceOfflinePrefilter: true,
		scoreSourceOfflineFallback: true, scoreSourceOfflineNoKey: true,
	}
	for _, jobs := range [][]jobSummary{status.Jobs, status.LowScoreJobs} {
		for _, job := range jobs {
			if job.MissingKeywords == nil {
				t.Error("search-status job missingKeywords must be a JSON array")
			}
			if !allowedStatus[job.Status] {
				t.Errorf("unexpected search-status job status %q", job.Status)
			}
			if job.ScoreSource != "" && !allowedScoreSource[job.ScoreSource] {
				t.Errorf("unexpected search-status scoreSource %q", job.ScoreSource)
			}
		}
	}

	var augmented searchStatusResponse
	if err := json.Unmarshal(addFutureOptional(raw), &augmented); err != nil {
		t.Fatalf("future optional search-status field must be ignored: %v", err)
	}
}

func TestResumeStudioContractFixture(t *testing.T) {
	raw := readContract(t, "resume-studio.json")
	root := contractObject(t, raw, "resume-studio")
	requireNonNullFields(t, root, "resume-studio",
		"contractVersion", "canonical", "requirements", "gap", "patches", "parseResponse",
		"diagnoseResponse", "analyzeJobResponse", "gapResponse", "optimizeResponse", "scoreResponse",
		"exportRequest", "exportResponse", "saveVersionResponse", "renameVersionResponse", "asyncJobStatus",
		"errorCodes", "errorResponses", "saveVersionRequest", "versionsResponse", "templatesResponse",
		"coverLetterRequest", "coverLetterResponse")

	var fixture resumeContractFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode resume-studio fixture: %v", err)
	}
	assertCanonicalCollections(t, "canonical", fixture.Canonical)
	assertRequirementsCollections(t, "requirements", fixture.Requirements)
	assertGapCollections(t, "gap", fixture.Gap)
	assertPatchCollections(t, "patches", fixture.Patches)

	assertCanonicalCollections(t, "parseResponse.canonical", fixture.ParseResponse.Canonical)
	assertCanonicalCollections(t, "diagnoseResponse.canonical", fixture.DiagnoseResponse.Canonical)
	assertCanonicalCollections(t, "optimizeResponse.preview", fixture.OptimizeResponse.Preview)
	assertCanonicalCollections(t, "exportRequest.canonical", fixture.ExportRequest.Canonical)
	assertCanonicalCollections(t, "saveVersionRequest.canonical", fixture.SaveVersionRequest.Canonical)
	assertRequirementsCollections(t, "analyzeJobResponse.requirements", fixture.AnalyzeJobResponse.Requirements)
	assertGapCollections(t, "gapResponse.gap", fixture.GapResponse.Gap)
	assertPatchCollections(t, "optimizeResponse.patches", fixture.OptimizeResponse.Patches)
	assertPatchCollections(t, "optimizeResponse.rejected", fixture.OptimizeResponse.Rejected)
	assertPatchCollections(t, "saveVersionRequest.patches", fixture.SaveVersionRequest.Patches)
	assertScoreMaps(t, "scoreResponse", fixture.ScoreResponse)
	if fixture.DiagnoseResponse.Issues == nil {
		t.Error("diagnoseResponse.issues must be a JSON array")
	}
	if fixture.CoverLetterResponse.Warnings == nil {
		t.Error("coverLetterResponse.warnings must be a JSON array")
	}
	if fixture.VersionsResponse.Versions == nil {
		t.Error("versionsResponse.versions must be a JSON array")
	}
	if fixture.TemplatesResponse.Templates == nil {
		t.Error("templatesResponse.templates must be a JSON array")
	}
	for i, version := range fixture.VersionsResponse.Versions {
		assertCanonicalCollections(t, fmt.Sprintf("versionsResponse.versions[%d].canonical", i), version.Canonical)
		assertPatchCollections(t, fmt.Sprintf("versionsResponse.versions[%d].patches", i), version.Patches)
	}
	if fixture.CoverLetterRequest.Gap != nil {
		assertGapCollections(t, "coverLetterRequest.gap", *fixture.CoverLetterRequest.Gap)
	}

	allowedPatchOp := map[string]bool{"replace": true, "add": true, "remove": true}
	allowedReviewRisk := map[string]bool{
		reviewRiskNewMetric: true, reviewRiskNewSkill: true, reviewRiskNewCertification: true,
		reviewRiskIdentityChange: true, reviewRiskUnsupportedClaim: true, reviewRiskHighRewriteDistance: true,
	}
	checkPatches := func(label string, patches []jsonPatchOp) {
		t.Helper()
		for _, patch := range patches {
			if !allowedPatchOp[patch.Op] {
				t.Errorf("%s has unsupported patch op %q", label, patch.Op)
			}
			if patch.ReviewRisk != "" && !allowedReviewRisk[patch.ReviewRisk] {
				t.Errorf("%s has unsupported reviewRisk %q", label, patch.ReviewRisk)
			}
			if strings.TrimSpace(patch.Path) == "" || strings.TrimSpace(patch.Reason) == "" {
				t.Errorf("%s patch must include path and reason", label)
			}
		}
	}
	checkPatches("patches", fixture.Patches)
	checkPatches("optimizeResponse.patches", fixture.OptimizeResponse.Patches)
	checkPatches("optimizeResponse.rejected", fixture.OptimizeResponse.Rejected)
	checkPatches("saveVersionRequest.patches", fixture.SaveVersionRequest.Patches)

	if fixture.ExportRequest.Format != "md" && fixture.ExportRequest.Format != "html" &&
		fixture.ExportRequest.Format != "pdf" && fixture.ExportRequest.Format != "docx" {
		t.Errorf("unsupported export format %q", fixture.ExportRequest.Format)
	}
	if fixture.ExportRequest.PageSize != "letter" && fixture.ExportRequest.PageSize != "a4" {
		t.Errorf("unsupported export page size %q", fixture.ExportRequest.PageSize)
	}
	if fixture.CoverLetterRequest.Language != "" {
		allowedLanguages := map[string]bool{"en": true, "pt": true, "es": true, "auto": true}
		if !allowedLanguages[fixture.CoverLetterRequest.Language] {
			t.Errorf("unsupported cover-letter language %q", fixture.CoverLetterRequest.Language)
		}
	}
	if fixture.CoverLetterRequest.Tone != "" {
		allowedTones := map[string]bool{"direct": true, "professional": true, "consultative": true}
		if !allowedTones[fixture.CoverLetterRequest.Tone] {
			t.Errorf("unsupported cover-letter tone %q", fixture.CoverLetterRequest.Tone)
		}
	}
	for label, status := range fixture.AsyncJobStatus {
		if status.State != resumeJobRunning && status.State != resumeJobDone {
			t.Errorf("asyncJobStatus[%q] has unsupported state %q", label, status.State)
		}
		if status.State == resumeJobDone && len(status.Result) == 0 {
			t.Errorf("asyncJobStatus[%q] done result must be present", label)
		}
	}

	allowedErrorCodes := map[string]bool{
		"invalid_request": true, "missing_name": true, "invalid_ai_json": true, "ai_timeout": true,
		"ai_consent_required": true, "ai_operation_budget_spent": true, "ai_budget_spent": true,
		"ai_quota_exhausted": true, "ai_model_unavailable": true, "ai_rate_limited": true,
		"ai_key_invalid": true, "ai_unavailable": true, "ai_key_required": true,
		"empty_resume_text": true, "empty_job_description": true, "job_not_found": true,
		"unknown_operation": true, "version_not_found": true, "internal_error": true,
		"unsupported_format": true, "invalid_file": true, "file_too_large": true,
		"ocr_not_installed": true, "ocr_no_text": true, "ocr_timeout": true, "ocr_failed": true,
	}
	declaredErrors := map[string]bool{}
	for _, code := range fixture.ErrorCodes {
		if !allowedErrorCodes[code] {
			t.Errorf("unsupported resume error code %q", code)
		}
		declaredErrors[code] = true
	}
	if fixture.ErrorResponses == nil {
		t.Fatal("errorResponses must be a JSON array")
	}
	for _, response := range fixture.ErrorResponses {
		if response.Message == "" || !declaredErrors[response.Code] {
			t.Errorf("error response must have a declared code and message: %+v", response)
		}
	}

	var augmented resumeContractFixture
	if err := json.Unmarshal(addFutureOptional(raw), &augmented); err != nil {
		t.Fatalf("future optional resume-studio field must be ignored: %v", err)
	}
}

func readNDJSONContract(t *testing.T, name string) [][]byte {
	t.Helper()
	path := filepath.Join(contractRoot(t), "contracts", name)
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open NDJSON contract %s: %v", path, err)
	}
	defer file.Close()

	reader := bufio.NewReaderSize(file, 64*1024)
	var lines [][]byte
	for {
		line, readErr := reader.ReadBytes('\n')
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			lines = append(lines, append([]byte(nil), line...))
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			t.Fatalf("read NDJSON contract %s: %v", path, readErr)
		}
	}
	return lines
}

func TestBrowserWorkerNDJSONContractFixture(t *testing.T) {
	lines := readNDJSONContract(t, "browser-worker.ndjson")
	if len(lines) == 0 {
		t.Fatal("browser-worker.ndjson must contain at least one envelope")
	}

	expectedCommands := map[string]string{
		"start": "start", "fetch_normal": "fetch", "fetch_blocked": "fetch",
		"fetch_gupy": "fetch_gupy", "warm_indeed": "warm_indeed", "close": "close",
		"unknown_command": "unknown_command",
	}
	seen := map[string]bool{}
	for i, line := range lines {
		object := contractObject(t, line, fmt.Sprintf("browser-worker line %d", i+1))
		requireNonNullFields(t, object, fmt.Sprintf("browser-worker line %d", i+1), "name", "response")

		var name string
		if err := json.Unmarshal(object["name"], &name); err != nil {
			t.Fatalf("browser-worker line %d name: %v", i+1, err)
		}
		seen[name] = true

		requestRaw, hasRequest := object["request"]
		requestLineRaw, hasRequestLine := object["requestLine"]
		if hasRequest == hasRequestLine {
			t.Fatalf("browser-worker %q must have exactly one of request/requestLine", name)
		}
		if expected, ok := expectedCommands[name]; ok && name != "invalid_json" {
			if !hasRequest {
				t.Fatalf("browser-worker %q must carry a typed request", name)
			}
			var request workerRequest
			if err := json.Unmarshal(requestRaw, &request); err != nil {
				t.Fatalf("browser-worker %q request: %v", name, err)
			}
			if request.Cmd != expected {
				t.Errorf("browser-worker %q cmd=%q, want %q", name, request.Cmd, expected)
			}
			if name == "fetch_gupy" {
				// The nested request also accepts additive optional fields.
				var augmentedRequest workerRequest
				if err := json.Unmarshal(addFutureOptional(requestRaw), &augmentedRequest); err != nil {
					t.Fatalf("browser-worker request additive field: %v", err)
				}
			}
		} else if name == "invalid_json" {
			var requestLine string
			if err := json.Unmarshal(requestLineRaw, &requestLine); err != nil {
				t.Fatalf("browser-worker invalid requestLine: %v", err)
			}
			if json.Valid([]byte(requestLine)) {
				t.Errorf("browser-worker invalid_json requestLine unexpectedly valid")
			}
		} else {
			t.Fatalf("unknown browser-worker envelope name %q", name)
		}

		var response workerResponse
		if err := json.Unmarshal(object["response"], &response); err != nil {
			t.Fatalf("browser-worker %q response: %v", name, err)
		}
		if !response.OK && strings.TrimSpace(response.Error) == "" {
			t.Errorf("browser-worker %q error response must include error", name)
		}
		if response.OK {
			switch name {
			case "fetch_normal", "fetch_blocked", "fetch_gupy":
				if response.HTML == "" {
					t.Errorf("browser-worker %q success must include html", name)
				}
			}
		}
		if name == "fetch_gupy" && response.Records == nil {
			t.Error("browser-worker fetch_gupy records must be a JSON array")
		}
		if name == "fetch_blocked" && !response.Blocked {
			t.Error("browser-worker fetch_blocked must set blocked=true")
		}
	}

	for _, name := range []string{"start", "fetch_normal", "fetch_blocked", "fetch_gupy", "warm_indeed", "invalid_json", "unknown_command", "close"} {
		if !seen[name] {
			t.Errorf("browser-worker fixture is missing %q envelope", name)
		}
	}

	// Unknown fields on a response are additive-compatible for the real DTO.
	var first map[string]json.RawMessage
	if err := json.Unmarshal(lines[0], &first); err != nil {
		t.Fatal(err)
	}
	first["futureOptional"] = json.RawMessage(`{"enabled":true}`)
	augmentedLine, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	var augmented struct {
		Response workerResponse `json:"response"`
	}
	if err := json.Unmarshal(augmentedLine, &augmented); err != nil {
		t.Fatalf("future optional browser-worker field must be ignored: %v", err)
	}
}
