package server

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// pollResumeJob drives the same loop the desktop client runs: ask until the job
// stops being "running".
func pollResumeJob(t *testing.T, a *api, id string) resumeJobStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/resume/jobs/"+id, nil)
		req.SetPathValue("id", id)
		rec := httptest.NewRecorder()
		a.resumeJobStatusHandler(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 from the status route, got %d body=%s", rec.Code, rec.Body.String())
		}
		var status resumeJobStatus
		if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
			t.Fatalf("decode job status: %v", err)
		}
		if status.State == resumeJobDone {
			return status
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job never finished")
	return resumeJobStatus{}
}

func startResumeJob(t *testing.T, a *api, op, body string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/resume/async/"+op, strings.NewReader(body))
	req.SetPathValue("op", op)
	rec := httptest.NewRecorder()
	a.resumeAsyncStart(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", rec.Code, rec.Body.String())
	}
	var accepted struct {
		JobID string `json:"jobId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("decode 202 body: %v", err)
	}
	if accepted.JobID == "" {
		t.Fatal("expected a job id in the 202 body")
	}
	return accepted.JobID
}

// The async route must hand back exactly what the synchronous route would
// have: same status, same body. That is the whole point of running the
// existing handler behind a buffered writer instead of reimplementing it.
func TestResumeAsyncJobReplaysTheSyncHandlerResult(t *testing.T) {
	a, _ := newResumeParseTestAPI(t, geminiJSONResponse(sampleCanonicalJSON))
	a.resumeJobs = newResumeJobStore()

	id := startResumeJob(t, a, "parse", `{"text":"Ana Souza\nEngenheira de Software"}`)
	status := pollResumeJob(t, a, id)

	if status.Status != http.StatusOK {
		t.Fatalf("expected the job to carry 200, got %d result=%s", status.Status, status.Result)
	}
	var parsed resumeParseResponse
	if err := json.Unmarshal(status.Result, &parsed); err != nil {
		t.Fatalf("the job result is not a parse response: %v (%s)", err, status.Result)
	}
	if strings.TrimSpace(parsed.Canonical.Basics.Name) == "" {
		t.Fatalf("expected a parsed resume in the job result, got %s", status.Result)
	}
}

// A failing AI call must surface through the job with the handler's own typed
// error, not as a generic poll failure — the UI keys its messages off that code.
func TestResumeAsyncJobCarriesTypedHandlerErrors(t *testing.T) {
	store := newTestStore(t)
	if err := store.save(geminiTestConfig(configForm{Provider: "gemini", APIKey: "test-key"})); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	bridge := newTestScraperBridge(&captureTransport{status: 429, respBody: `{"error":"quota"}`})
	bridge.store = store
	a := &api{
		logger:                 log.New(io.Discard, "", 0),
		configStore:            store,
		scraper:                bridge,
		resumeJobs:             newResumeJobStore(),
		cascadeRetryDelay:      -1,
		cascadeRateLimitDelays: []time.Duration{},
	}

	id := startResumeJob(t, a, "parse", `{"text":"Ana Souza"}`)
	status := pollResumeJob(t, a, id)

	if status.Status == http.StatusOK {
		t.Fatalf("expected the job to carry a failure, got 200: %s", status.Result)
	}
	var failure struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(status.Result, &failure); err != nil {
		t.Fatalf("decode failure body: %v", err)
	}
	if failure.Code != "ai_rate_limited" {
		t.Fatalf("expected the handler's own ai_rate_limited code, got %q (%s)", failure.Code, status.Result)
	}
}

func TestResumeAsyncRejectsUnknownOperation(t *testing.T) {
	a := &api{logger: log.New(io.Discard, "", 0), resumeJobs: newResumeJobStore()}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/resume/async/nope", strings.NewReader(`{}`))
	req.SetPathValue("op", "nope")
	rec := httptest.NewRecorder()
	a.resumeAsyncStart(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// Polling an id we never issued must fail loudly instead of looking like a job
// that is merely still running, which the client would poll forever.
func TestResumeJobStatusUnknownIDIs404(t *testing.T) {
	a := &api{logger: log.New(io.Discard, "", 0), resumeJobs: newResumeJobStore()}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/resume/jobs/does-not-exist", nil)
	req.SetPathValue("id", "does-not-exist")
	rec := httptest.NewRecorder()
	a.resumeJobStatusHandler(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestResumeJobStoreEvictsFinishedJobsPastTTL(t *testing.T) {
	store := newResumeJobStore()
	store.finish("old", http.StatusOK, []byte(`{}`))

	store.mu.Lock()
	record := store.jobs["old"]
	record.finishedAt = time.Now().Add(-resumeJobTTL - time.Minute)
	store.jobs["old"] = record
	store.mu.Unlock()

	store.start("new")

	if _, ok := store.get("old"); ok {
		t.Fatal("expected the aged-out job to be evicted")
	}
	if _, ok := store.get("new"); !ok {
		t.Fatal("expected the fresh job to survive")
	}
}
