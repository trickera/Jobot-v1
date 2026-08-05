package server

import (
	"context"
	"io"
	"log"
	"strings"
	"testing"
	"time"
)

func batchTestConfig() appConfig {
	config := defaultConfig()
	config.Form.Provider = "gemini"
	config.Form.Roles = "Backend Engineer"
	config.Form.ResumeText = "Go, PostgreSQL, Kubernetes"
	config.Form.AIDataConsent = true
	config.ModelValidation = &modelValidationStatus{Status: "validated", Requested: config.Form.Model, Active: config.Form.Model}
	config.Toggles["compatibility"] = true
	return config
}

func TestJobScoreBatchSizeFollowsFreeTierMode(t *testing.T) {
	economy := batchTestConfig()
	quality := batchTestConfig()
	quality.Form.AIMode = aiModeFreeQuality
	if got := jobScoreBatchSizeFor(economy); got != 20 {
		t.Fatalf("economy batch size = %d, want 20", got)
	}
	if got := jobScoreBatchSizeFor(quality); got != 15 {
		t.Fatalf("quality batch size = %d, want 15", got)
	}
	if jobScoreCacheKey(economy, jobPost{ID: "a"}) == jobScoreCacheKey(quality, jobPost{ID: "a"}) {
		t.Fatal("AI mode must participate in the job score cache key")
	}
}

// The point of the whole change: N jobs cost one request, not N.
func TestScoreJobsBatchScoresEveryJobInOneCall(t *testing.T) {
	store := newTestStore(t)
	if err := store.save(appConfig{Form: configForm{Provider: "gemini", APIKey: "test-key", AIDataConsent: true}}); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	transport := &captureTransport{respBody: geminiJSONResponse(
		`{"scores":[{"id":"a","match_score":91},{"id":"b","match_score":40}]}`)}
	bridge := newTestScraperBridge(transport)
	bridge.store = store
	bridge.api = &api{logger: log.New(io.Discard, "", 0), configStore: store, scraper: bridge}

	jobs := []jobPost{
		{ID: "a", Title: "Backend Engineer", Company: "Acme", Description: "Go, PostgreSQL."},
		{ID: "b", Title: "Frontend Engineer", Company: "Globex", Description: "React."},
	}

	batch, err := bridge.scoreJobsBatch(context.Background(), batchTestConfig(), jobs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if transport.calls != 1 {
		t.Fatalf("expected exactly one provider call for the whole batch, got %d", transport.calls)
	}
	if batch.Scores["a"] != 91 || batch.Scores["b"] != 40 {
		t.Fatalf("expected each job to keep its own score, got %v", batch.Scores)
	}
	if batch.Sources["a"] != scoreSourceAI || batch.Sources["b"] != scoreSourceAI {
		t.Fatalf("expected fresh model answers to be marked as AI, got %v", batch.Sources)
	}
	// Every job must reach the model, keyed by the id we will look the score up by.
	for _, id := range []string{"id=a", "id=b"} {
		if !strings.Contains(transport.lastBody, id) {
			t.Fatalf("expected the prompt to carry %s, got %q", id, transport.lastBody)
		}
	}
}

// A search must still finish when the AI is rate-limited or out of quota: the
// caller falls back to the offline heuristic, so a nil map is the contract.
func TestScoreJobsBatchReturnsNilWhenTheProviderFails(t *testing.T) {
	store := newTestStore(t)
	if err := store.save(appConfig{Form: configForm{Provider: "gemini", APIKey: "test-key", AIDataConsent: true}}); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	bridge := newTestScraperBridge(&captureTransport{status: 429, respBody: `{"error":"quota"}`})
	bridge.store = store
	bridge.api = &api{
		logger:                 log.New(io.Discard, "", 0),
		configStore:            store,
		scraper:                bridge,
		cascadeRetryDelay:      -1,
		cascadeRateLimitDelays: []time.Duration{},
	}

	jobs := []jobPost{{ID: "a", Title: "Backend Engineer", Description: "Go."}}

	batch, err := bridge.scoreJobsBatch(context.Background(), batchTestConfig(), jobs)
	if len(batch.Scores) != 0 {
		t.Fatalf("expected no scores so the caller falls back offline, got %v", batch.Scores)
	}
	if err == nil {
		t.Fatal("expected the failure to be reported, so the caller can tell a spent quota from a garbled batch")
	}
}

// The change that makes radar mode survivable: a job scored once is never paid
// for again. A second sweep over the same postings must make no request at all.
func TestScoreJobsBatchServesKnownJobsFromCache(t *testing.T) {
	store := newTestStore(t)
	if err := store.save(appConfig{Form: configForm{Provider: "gemini", APIKey: "test-key", AIDataConsent: true}}); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	transport := &captureTransport{respBody: geminiJSONResponse(
		`{"scores":[{"id":"a","match_score":91},{"id":"b","match_score":40}]}`)}
	bridge := newTestScraperBridge(transport)
	bridge.store = store
	bridge.api = &api{logger: log.New(io.Discard, "", 0), configStore: store, scraper: bridge}

	config := batchTestConfig()
	jobs := []jobPost{
		{ID: "a", Title: "Backend Engineer", Company: "Acme", Description: "Go, PostgreSQL."},
		{ID: "b", Title: "Backend Engineer", Company: "Globex", Description: "Go, Kubernetes."},
	}

	if _, err := bridge.scoreJobsBatch(context.Background(), config, jobs); err != nil {
		t.Fatalf("first sweep: %v", err)
	}
	if transport.calls != 1 {
		t.Fatalf("expected the first sweep to ask once, got %d", transport.calls)
	}

	// The radar comes back around over the same postings.
	batch, err := bridge.scoreJobsBatch(context.Background(), config, jobs)
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}

	if transport.calls != 1 {
		t.Fatalf("expected the second sweep to cost no request, got %d calls total", transport.calls)
	}
	if batch.CacheHits != 2 {
		t.Fatalf("expected both jobs served from cache, got %d hits", batch.CacheHits)
	}
	if batch.Scores["a"] != 91 || batch.Scores["b"] != 40 {
		t.Fatalf("cached scores do not match the originals: %v", batch.Scores)
	}
	if batch.Sources["a"] != scoreSourceAICache || batch.Sources["b"] != scoreSourceAICache {
		t.Fatalf("expected cached answers to be distinguished from fresh AI, got %v", batch.Sources)
	}
}

// Only the new postings in a sweep may cost anything.
func TestScoreJobsBatchOnlyAsksAboutUnseenJobs(t *testing.T) {
	store := newTestStore(t)
	if err := store.save(appConfig{Form: configForm{Provider: "gemini", APIKey: "test-key", AIDataConsent: true}}); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	transport := &captureTransport{respBody: geminiJSONResponse(`{"scores":[{"id":"a","match_score":91}]}`)}
	bridge := newTestScraperBridge(transport)
	bridge.store = store
	bridge.api = &api{logger: log.New(io.Discard, "", 0), configStore: store, scraper: bridge}

	config := batchTestConfig()
	known := jobPost{ID: "a", Title: "Backend Engineer", Company: "Acme", Description: "Go."}

	if _, err := bridge.scoreJobsBatch(context.Background(), config, []jobPost{known}); err != nil {
		t.Fatalf("first sweep: %v", err)
	}

	transport.respBody = geminiJSONResponse(`{"scores":[{"id":"b","match_score":55}]}`)
	fresh := jobPost{ID: "b", Title: "Backend Engineer", Company: "Globex", Description: "Rust."}

	batch, err := bridge.scoreJobsBatch(context.Background(), config, []jobPost{known, fresh})
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}

	if batch.CacheHits != 1 {
		t.Fatalf("expected the known job served from cache, got %d hits", batch.CacheHits)
	}
	if batch.Scores["a"] != 91 || batch.Scores["b"] != 55 {
		t.Fatalf("expected both jobs scored, got %v", batch.Scores)
	}
	// The prompt for the second call must carry only the new job.
	if strings.Contains(transport.lastBody, "id=a") {
		t.Fatalf("expected the already-scored job to be left out of the prompt, got %q", transport.lastBody)
	}
	if !strings.Contains(transport.lastBody, "id=b") {
		t.Fatalf("expected the new job in the prompt, got %q", transport.lastBody)
	}
}

// An edited resume changes the answer, so it must not reuse the old one.
func TestJobScoreCacheKeyTracksTheResumeAndThePosting(t *testing.T) {
	config := batchTestConfig()
	job := jobPost{ID: "a", Title: "Backend Engineer", Description: "Go."}
	base := jobScoreCacheKey(config, job)

	edited := batchTestConfig()
	edited.Form.ResumeText = "Rust, Kafka"
	if jobScoreCacheKey(edited, job) == base {
		t.Fatal("editing the resume must change the key")
	}

	reposted := job
	reposted.Description = "Go, and now also Kubernetes."
	if jobScoreCacheKey(config, reposted) == base {
		t.Fatal("a changed posting must change the key")
	}

	stricter := batchTestConfig()
	stricter.Form.MaxYears = 2
	if jobScoreCacheKey(stricter, job) == base {
		t.Fatal("changed seniority rules must change the key")
	}
}

// A spent daily quota must reach the caller as such, so the search can tell the
// user its scores came from the offline scorer instead of silently degrading.
func TestScoreJobsBatchReportsAnExhaustedQuota(t *testing.T) {
	store := newTestStore(t)
	if err := store.save(appConfig{Form: configForm{Provider: "gemini", APIKey: "test-key", AIDataConsent: true}}); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	body := `{"error":{"details":[{"violations":[{"quotaId":"GenerateRequestsPerDayPerProjectPerModel-FreeTier"}]}]}}`
	bridge := newTestScraperBridge(&captureTransport{status: 429, respBody: body})
	bridge.store = store
	bridge.api = &api{
		logger:                 log.New(io.Discard, "", 0),
		configStore:            store,
		scraper:                bridge,
		cascadeRetryDelay:      -1,
		cascadeRateLimitDelays: []time.Duration{},
	}

	_, err := bridge.scoreJobsBatch(context.Background(), batchTestConfig(),
		[]jobPost{{ID: "a", Title: "Backend Engineer", Description: "Go."}})

	if got := classifyProviderError(err); got != "quota_exhausted" {
		t.Fatalf("expected quota_exhausted to reach the caller, got %q (%v)", got, err)
	}
}

// A job the model forgets must not silently inherit another job's score.
func TestDecodeBatchScoresOmitsJobsTheModelSkipped(t *testing.T) {
	scores, err := decodeBatchScores(`{"scores":[{"id":"a","match_score":77}]}`)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if scores["a"] != 77 {
		t.Fatalf("expected 77 for the answered job, got %d", scores["a"])
	}
	if _, ok := scores["b"]; ok {
		t.Fatal("expected the unanswered job to be absent, so the caller falls back to the heuristic")
	}
}

func TestDecodeBatchScoresRejectsAnEmptyAnswer(t *testing.T) {
	if _, err := decodeBatchScores(`{"scores":[]}`); err == nil {
		t.Fatal("expected an unusable answer to be an error, not an empty map")
	}
	if _, err := decodeBatchScores(`not json`); err == nil {
		t.Fatal("expected malformed JSON to be an error")
	}
}

func TestDecodeBatchScoresClampsOutOfRangeScores(t *testing.T) {
	scores, err := decodeBatchScores(`{"scores":[{"id":"a","match_score":250},{"id":"b","match_score":-10}]}`)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if scores["a"] > 100 || scores["b"] < 0 {
		t.Fatalf("expected scores clamped into 0-100, got %v", scores)
	}
}

// The pre-filter decides who is worth an AI call. It is lenient on purpose:
// it skips the obvious mismatches, it does not make the final judgement.
func TestTitleRelevantSkipsObviousMismatches(t *testing.T) {
	config := batchTestConfig()

	if !titleRelevant(config, jobPost{Title: "Senior Backend Engineer", Description: "Go."}) {
		t.Fatal("a role match in the title must be worth an AI call")
	}
	if !titleRelevant(config, jobPost{Title: "Software Craftsperson", Description: "You will work as a backend engineer."}) {
		t.Fatal("a role match in the body must be worth an AI call")
	}
	if titleRelevant(config, jobPost{Title: "Dentist", Description: "Clinic in São Paulo."}) {
		t.Fatal("a job with no trace of the searched role must not cost a request")
	}

	// With no role configured there is nothing to filter on, so nothing is skipped.
	noRoles := batchTestConfig()
	noRoles.Form.Roles = ""
	noRoles.Form.Role = ""
	if !titleRelevant(noRoles, jobPost{Title: "Dentist"}) {
		t.Fatal("without a configured role the filter must not drop anything")
	}
}

func TestLLMScoringEnabled(t *testing.T) {
	config := batchTestConfig()

	if !llmScoringEnabled(config, "key") {
		t.Fatal("expected scoring enabled with a key and the toggle on")
	}
	if llmScoringEnabled(config, "  ") {
		t.Fatal("no key means offline scoring")
	}
	off := batchTestConfig()
	off.Toggles["compatibility"] = false
	if llmScoringEnabled(off, "key") {
		t.Fatal("the compatibility toggle off means offline scoring")
	}
}

func TestOfflineScoreSourceCoversEveryFallbackPath(t *testing.T) {
	withAI := batchTestConfig()
	withoutAI := batchTestConfig()
	withoutAI.Toggles["compatibility"] = false

	tests := []struct {
		name      string
		config    appConfig
		key       string
		prefilter bool
		want      ScoreSource
	}{
		{name: "prefilter", config: withAI, key: "key", prefilter: true, want: scoreSourceOfflinePrefilter},
		{name: "provider fallback", config: withAI, key: "key", want: scoreSourceOfflineFallback},
		{name: "no key", config: withAI, want: scoreSourceOfflineNoKey},
		{name: "AI disabled", config: withoutAI, key: "key", want: scoreSourceOfflineNoKey},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := offlineScoreSource(test.config, test.key, test.prefilter); got != test.want {
				t.Fatalf("offlineScoreSource()=%q, want %q", got, test.want)
			}
		})
	}
}
