package server

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const sampleJobRequirementsJSON = `{"category":"tech","jobTitle":"DevOps Engineer","hardRequirements":["AWS","Terraform","Kubernetes"],"niceToHave":["Grafana","Prometheus"],"seniority":"Pleno","atsKeywords":["CI/CD","IaC","containers"]}`

func TestAnalyzeJobWithMockedAI(t *testing.T) {
	bridge := newTestScraperBridge(&captureTransport{respBody: geminiJSONResponse(sampleJobRequirementsJSON)})
	a := &api{logger: log.New(io.Discard, "", 0), scraper: bridge}

	config := defaultConfig()
	config.Form.Provider = "gemini"

	req, err := a.analyzeJob(context.Background(), config, "test-key", "We need a DevOps engineer with AWS, Terraform, Kubernetes.", "", "")
	if err != nil {
		t.Fatalf("analyzeJob: %v", err)
	}
	if req.JobTitle != "DevOps Engineer" || req.Category != "tech" {
		t.Fatalf("unexpected requirements: %+v", req)
	}
	if len(req.HardRequirements) != 3 {
		t.Fatalf("expected 3 hard requirements, got %+v", req.HardRequirements)
	}
}

func TestAnalyzeJobRejectsInvalidJSON(t *testing.T) {
	bridge := newTestScraperBridge(&captureTransport{respBody: geminiJSONResponse("not json")})
	a := &api{logger: log.New(io.Discard, "", 0), scraper: bridge}

	config := defaultConfig()
	config.Form.Provider = "gemini"

	if _, err := a.analyzeJob(context.Background(), config, "test-key", "description", "", ""); err == nil {
		t.Fatal("expected error for invalid JSON response")
	}
}

func newAnalyzeJobTestAPI(t *testing.T, respBody string) *api {
	t.Helper()
	store := newTestStore(t)
	if err := store.save(geminiTestConfig(configForm{Provider: "gemini", APIKey: "test-key"})); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	bridge := newTestScraperBridge(&captureTransport{respBody: respBody})
	bridge.store = store
	return &api{logger: log.New(io.Discard, "", 0), configStore: store, scraper: bridge}
}

func openAIJSONResponse(payloadJSON string) string {
	escaped := strings.ReplaceAll(payloadJSON, `"`, `\"`)
	return `{"choices":[{"message":{"content":"` + escaped + `"}}]}`
}

func TestResumeAnalyzeJobHandlerWithPastedDescription(t *testing.T) {
	a := newAnalyzeJobTestAPI(t, geminiJSONResponse(sampleJobRequirementsJSON))

	body, _ := json.Marshal(analyzeJobRequest{Description: "We need a DevOps engineer."})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/resume/analyze-job", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	a.resumeAnalyzeJob(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp analyzeJobResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Requirements.JobTitle != "DevOps Engineer" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if resp.ProviderUsed != "gemini" {
		t.Fatalf("expected providerUsed gemini, got %q", resp.ProviderUsed)
	}
}

func TestResumeAnalyzeJobHandlerReportsFallbackProvider(t *testing.T) {
	store := newTestStore(t)
	config := defaultConfig()
	config.Form.Provider = "Gemini"
	config.Form.Model = geminiFreeModel
	config.Form.APIKey = "gemini-key"
	config.Form.Fallback1Provider = "OpenRouter"
	config.Form.Fallback1Model = "openai/gpt-4.1-mini"
	config.ModelValidation = &modelValidationStatus{Status: "validated", Requested: config.Form.Model, Active: config.Form.Model}
	if err := store.save(config); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	if err := store.setAIAPIKeyForProvider("OpenRouter", "openrouter-key"); err != nil {
		t.Fatalf("seed fallback key: %v", err)
	}
	// Every Gemini model has to refuse before the cascade is entitled to spend the
	// user's OTHER key: the sibling models cost nothing, since they run on the key
	// Gemini already has.
	responses := repeatResponse(geminiAttemptCount(config), sequenceResponse{status: 429, body: `{"error":"quota"}`})
	responses = append(responses, sequenceResponse{status: 200, body: openAIJSONResponse(sampleJobRequirementsJSON)})
	bridge := newTestScraperBridge(&sequenceTransport{responses: responses})
	bridge.store = store
	// This test asserts fallback-provider selection on a 429, not the
	// same-provider rate-limit retry — an empty (non-nil) delay schedule
	// opts out of retries so it doesn't consume the fixed 2-response
	// sequence meant for "gemini fails once, openrouter succeeds once".
	a := &api{logger: log.New(io.Discard, "", 0), configStore: store, scraper: bridge, cascadeRetryDelay: -1, cascadeRateLimitDelays: []time.Duration{}}

	body, _ := json.Marshal(analyzeJobRequest{Description: "We need a DevOps engineer."})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/resume/analyze-job", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	a.resumeAnalyzeJob(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp analyzeJobResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ProviderUsed != "openrouter" {
		t.Fatalf("expected providerUsed openrouter, got %q", resp.ProviderUsed)
	}
}

func TestResumeAnalyzeJobHandlerLooksUpJobByID(t *testing.T) {
	a := newAnalyzeJobTestAPI(t, geminiJSONResponse(sampleJobRequirementsJSON))

	if _, _, err := a.configStore.applyJobAction("dismiss", jobSummary{ID: "job:1", Title: "DevOps Engineer", Company: "Acme"}); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	// applyJobAction persists a jobSummary without a description; simulate a
	// collected job with a real description via raw_json.
	seedJobDescription(t, a.configStore, "job:1", "We need a DevOps engineer with AWS and Terraform.")

	body, _ := json.Marshal(analyzeJobRequest{JobID: "job:1"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/resume/analyze-job", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	a.resumeAnalyzeJob(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestResumeAnalyzeJobHandlerRequiresDescription(t *testing.T) {
	a := newAnalyzeJobTestAPI(t, geminiJSONResponse(sampleJobRequirementsJSON))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/resume/analyze-job", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	a.resumeAnalyzeJob(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 without description or jobId, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestResumeAnalyzeJobHandlerRequiresAIKey(t *testing.T) {
	store := newTestStore(t)
	a := &api{logger: log.New(io.Discard, "", 0), configStore: store, scraper: newTestScraperBridge(&captureTransport{})}

	body, _ := json.Marshal(analyzeJobRequest{Description: "We need a DevOps engineer."})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/resume/analyze-job", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	a.resumeAnalyzeJob(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 without an AI key, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// seedJobDescription writes a description straight into the job's raw_json,
// mirroring how a collected jobPost is stored (saveSearchResults), since
// applyJobAction only upserts the summary columns.
func seedJobDescription(t *testing.T, store *configStore, jobID, description string) {
	t.Helper()
	db, err := store.open()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	raw, err := json.Marshal(jobPost{ID: jobID, Description: description})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE jobs SET raw_json = ? WHERE id = ?`, string(raw), jobID); err != nil {
		t.Fatalf("seed job description: %v", err)
	}
}
