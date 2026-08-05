package server

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func newTestStore(t *testing.T) *configStore {
	t.Helper()
	t.Setenv("SENCIA_DB_PATH", filepath.Join(t.TempDir(), "sencia.db"))
	return newConfigStore()
}

// A job already applied must be skipped on the next run even when the scraped
// title differs (e.g. em-dash vs hyphen), because the stable URL-based ID is in
// processedKeys. title+company alone would miss it.
func TestProcessedKeysMatchByStableID(t *testing.T) {
	store := newTestStore(t)
	applied := jobSummary{
		ID:      "linkedin:https://example.com/jobs/view/eng-1",
		Source:  "LinkedIn",
		Title:   "Engenheiro SRE — Observabilidade", // em-dash
		Company: "Acme",
		Status:  statusApply,
		Score:   90,
	}
	if _, _, err := store.applyJobAction("applied", applied); err != nil {
		t.Fatalf("applyJobAction: %v", err)
	}

	keys := store.processedKeys()
	if !keys[processedIDKey(applied.ID)] {
		t.Fatal("expected applied job ID present in processedKeys")
	}

	// Re-scrape produces the same ID (same URL) but a text-variant title.
	rescraped := jobPost{ID: applied.ID, Title: "Engenheiro SRE - Observabilidade", Company: "Acme"}
	if !keys[processedIDKey(rescraped.ID)] {
		t.Fatal("expected re-scraped job to match by stable ID despite different title text")
	}
	if keys[titleCompanyKey(rescraped.Title, rescraped.Company)] {
		t.Fatal("sanity: title-variant should NOT match by title+company (proves ID keying is what saves it)")
	}
}

func TestApplyJobActionAppliedWritesAnApplicationsRow(t *testing.T) {
	store := newTestStore(t)
	job := jobSummary{
		ID: "linkedin:https://example.com/jobs/view/application-row", Source: "LinkedIn",
		Title: "Platform Engineer", Company: "Acme", Status: statusApply, Score: 91,
	}
	if _, _, err := store.applyJobAction("applied", job); err != nil {
		t.Fatalf("applyJobAction: %v", err)
	}

	db, err := sql.Open("sqlite", store.path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var id, jobID, status string
	if err := db.QueryRow(`SELECT id, job_id, status FROM applications WHERE job_id = ?`, job.ID).Scan(&id, &jobID, &status); err != nil {
		t.Fatalf("read application row: %v", err)
	}
	if id != "application:"+job.ID || jobID != job.ID || status != statusApplied {
		t.Fatalf("unexpected application row: id=%q job_id=%q status=%q", id, jobID, status)
	}
}

func TestListApplicationsReturnsAppliedJobs(t *testing.T) {
	store := newTestStore(t)
	job := jobSummary{
		ID: "linkedin:https://example.com/jobs/view/list-application", Source: "LinkedIn",
		Title: "Data Engineer", Company: "Northwind", Location: "Remote", URL: "https://example.com/job",
		Status: statusApply, Score: 84, Description: "Build reliable data pipelines.",
	}
	if _, _, err := store.applyJobAction("applied", job); err != nil {
		t.Fatal(err)
	}

	items, err := store.listApplications(10)
	if err != nil {
		t.Fatalf("listApplications: %v", err)
	}
	if len(items) != 1 || items[0].JobID != job.ID || items[0].Status != statusApplied {
		t.Fatalf("unexpected applications: %+v", items)
	}
	if items[0].Job.Title != job.Title || items[0].Job.Description != job.Description || items[0].Job.URL != job.URL {
		t.Fatalf("application did not include its job: %+v", items[0])
	}
}

func TestListSearchHistoryReturnsSavedRuns(t *testing.T) {
	store := newTestStore(t)
	config := defaultConfig()
	config.Form.Roles = "Backend Engineer"
	jobs := []jobPost{{ID: "history-job", Source: "LinkedIn", Title: "Backend Engineer", Company: "Acme", Status: statusApply}}
	if err := store.saveSearchResults(config, jobs); err != nil {
		t.Fatal(err)
	}

	items, err := store.listSearchHistory(10)
	if err != nil {
		t.Fatalf("listSearchHistory: %v", err)
	}
	if len(items) != 1 || items[0].Query != "Backend Engineer" || items[0].ResultsCount != 1 {
		t.Fatalf("unexpected history: %+v", items)
	}
	if items[0].Filters["roles"] != "Backend Engineer" {
		t.Fatalf("history filters did not round-trip: %+v", items[0].Filters)
	}
}

func TestLowScorePersistenceDoesNotContaminateMainJobsOrHistory(t *testing.T) {
	store := newTestStore(t)
	config := defaultConfig()
	config.Form.Roles = "Backend Engineer"

	low := jobPost{
		ID: "low-score-job", Source: "Indeed", Title: "Junior Backend Engineer", Company: "Acme",
		Status: statusDiscard, Score: 48, Description: "Maintain internal services.",
	}
	approved := jobPost{
		ID: "approved-job", Source: "LinkedIn", Title: "Backend Engineer", Company: "Northwind",
		Status: statusApply, Score: 88, Description: "Build Go services.",
	}

	if err := store.saveSearchResult(low); err != nil {
		t.Fatalf("persist low score: %v", err)
	}
	if err := store.saveSearchResults(config, []jobPost{approved}); err != nil {
		t.Fatalf("persist main results/history: %v", err)
	}

	main, err := store.listRecentJobs(10)
	if err != nil || len(main) != 1 || main[0].ID != approved.ID {
		t.Fatalf("main jobs include low-score rows: jobs=%+v err=%v", main, err)
	}
	lowScores, err := store.listLowScoreJobs(10)
	if err != nil || len(lowScores) != 1 || lowScores[0].ID != low.ID {
		t.Fatalf("low-score jobs were not persisted separately: jobs=%+v err=%v", lowScores, err)
	}
	if lowScores[0].Description != low.Description || lowScores[0].Status != statusDiscard {
		t.Fatalf("low-score job lost its card fields: %+v", lowScores[0])
	}
	history, err := store.listSearchHistory(10)
	if err != nil || len(history) != 1 || history[0].ResultsCount != 1 {
		t.Fatalf("history count must include only main results: history=%+v err=%v", history, err)
	}
}

func TestSaveHistoryToggleOffWritesNothing(t *testing.T) {
	store := newTestStore(t)
	config := defaultConfig()
	config.Toggles["saveHistory"] = false
	jobs := []jobPost{{ID: "no-history-job", Source: "LinkedIn", Title: "SRE", Company: "Acme", Status: statusApply}}
	if err := store.saveSearchResults(config, jobs); err != nil {
		t.Fatal(err)
	}

	items, err := store.listSearchHistory(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("saveHistory=false wrote entries: %+v", items)
	}
	if recent, err := store.listRecentJobs(10); err != nil || len(recent) != 1 {
		t.Fatalf("toggle must only disable history, jobs=%+v err=%v", recent, err)
	}
}

func TestSaveAndUnsaveJobRoundTrip(t *testing.T) {
	store := newTestStore(t)
	job := jobSummary{
		ID: "linkedin:https://example.com/jobs/view/saved-roundtrip", Source: "LinkedIn",
		Title: "Security Engineer", Company: "Acme", Status: statusApply, Score: 82,
	}
	if _, _, err := store.applyJobAction("save", job); err != nil {
		t.Fatalf("save: %v", err)
	}
	items, err := store.listSavedJobs(10)
	if err != nil || len(items) != 1 || items[0].ID != job.ID || items[0].SavedAt == "" {
		t.Fatalf("saved jobs after save: %+v err=%v", items, err)
	}
	stats, err := store.stats()
	if err != nil || stats.Saved != 1 {
		t.Fatalf("saved stats=%+v err=%v", stats, err)
	}

	if _, _, err := store.applyJobAction("unsave", jobSummary{ID: job.ID}); err != nil {
		t.Fatalf("unsave: %v", err)
	}
	items, err = store.listSavedJobs(10)
	if err != nil || len(items) != 0 {
		t.Fatalf("saved jobs after unsave: %+v err=%v", items, err)
	}
}

// BUG-02 regression: Mark Applied/Dismiss/Blacklist must be visible in the
// "Banco local" counters after an app restart, i.e. after a brand-new
// configStore/process re-opens the same DB path. Before the fix, GET
// /api/v1/config echoed back config.LocalItems, a field only ever set to the
// zero value on first init and otherwise round-tripped verbatim - so the UI
// showed 0/0/0 forever regardless of real jobs/applications rows.
func TestLocalItemsReflectLiveStatsAfterRestart(t *testing.T) {
	store := newTestStore(t)

	applied := jobSummary{ID: "linkedin:https://example.com/jobs/view/applied-1", Source: "LinkedIn", Title: "Applied Role", Company: "Acme", Status: statusApply, Score: 90}
	if _, _, err := store.applyJobAction("applied", applied); err != nil {
		t.Fatalf("applyJobAction applied: %v", err)
	}
	dismissed := jobSummary{ID: "linkedin:https://example.com/jobs/view/dismissed-1", Source: "LinkedIn", Title: "Dismissed Role", Company: "Acme", Status: statusApply, Score: 80}
	if _, _, err := store.applyJobAction("dismiss", dismissed); err != nil {
		t.Fatalf("applyJobAction dismiss: %v", err)
	}

	// Simulate an app restart: a brand-new configStore value pointed at the
	// same DB path (mirrors a fresh Go process re-reading the same file).
	restarted := newConfigStore()

	config, err := restarted.load()
	if err != nil {
		t.Fatalf("load after restart: %v", err)
	}
	if config.LocalItems.Jobs == 0 {
		t.Fatalf("expected LocalItems.Jobs > 0 after restart, got %+v", config.LocalItems)
	}
	if config.LocalItems.Applications == 0 {
		t.Fatalf("expected LocalItems.Applications > 0 after restart, got %+v", config.LocalItems)
	}

	stats, err := restarted.stats()
	if err != nil {
		t.Fatalf("stats after restart: %v", err)
	}
	if stats != config.LocalItems {
		t.Fatalf("expected config.LocalItems to match live stats(): config=%+v stats=%+v", config.LocalItems, stats)
	}
}

func jobStatus(t *testing.T, store *configStore, id string) string {
	t.Helper()
	db, err := sql.Open("sqlite", store.path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var status string
	if err := db.QueryRow(`SELECT status FROM jobs WHERE id = ?`, id).Scan(&status); err != nil {
		t.Fatalf("read status for %s: %v", id, err)
	}
	return status
}

func TestJobScoreSourcePersistsAcrossStoreReads(t *testing.T) {
	store := newTestStore(t)
	job := jobPost{
		ID:          "linkedin:https://example.com/jobs/scored-1",
		Source:      "LinkedIn",
		Title:       "Backend Engineer",
		Company:     "Acme",
		Status:      statusApply,
		Score:       88,
		ScoreSource: scoreSourceAICache,
		ScoreReason: "Score de IA reutilizado do cache local.",
	}
	if err := store.saveSearchResult(job); err != nil {
		t.Fatalf("saveSearchResult: %v", err)
	}

	jobs, err := store.listRecentJobs(10)
	if err != nil {
		t.Fatalf("listRecentJobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected one job, got %+v", jobs)
	}
	if jobs[0].ScoreSource != scoreSourceAICache || jobs[0].ScoreReason != job.ScoreReason {
		t.Fatalf("score provenance did not round-trip: %+v", jobs[0])
	}

	db, err := sql.Open("sqlite", store.path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var stored string
	if err := db.QueryRow(`SELECT score_source FROM jobs WHERE id = ?`, job.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != string(scoreSourceAICache) {
		t.Fatalf("score_source column=%q, want %q", stored, scoreSourceAICache)
	}
}

// Re-saving search results must not reset an already applied/dismissed job back
// to an active status, otherwise handled jobs resurface as fresh APPLY cards.
func TestSaveSearchResultsPreservesAppliedAndDismissedStatus(t *testing.T) {
	store := newTestStore(t)
	config := defaultConfig()

	appliedID := "linkedin:https://example.com/jobs/applied-1"
	dismissedID := "linkedin:https://example.com/jobs/dismissed-1"
	freshID := "linkedin:https://example.com/jobs/fresh-1"

	if _, _, err := store.applyJobAction("applied", jobSummary{ID: appliedID, Source: "LinkedIn", Title: "Applied Role", Company: "Acme", Status: statusApply}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.applyJobAction("dismiss", jobSummary{ID: dismissedID, Source: "LinkedIn", Title: "Dismissed Role", Company: "Acme", Status: statusApply}); err != nil {
		t.Fatal(err)
	}

	// A subsequent search re-approves all three (same IDs) with active statuses.
	results := []jobPost{
		{ID: appliedID, Source: "LinkedIn", Title: "Applied Role", Company: "Acme", Status: statusApply, Score: 88},
		{ID: dismissedID, Source: "LinkedIn", Title: "Dismissed Role", Company: "Acme", Status: statusApply, Score: 77},
		{ID: freshID, Source: "LinkedIn", Title: "Fresh Role", Company: "Acme", Status: statusApply, Score: 70},
	}
	if err := store.saveSearchResults(config, results); err != nil {
		t.Fatalf("saveSearchResults: %v", err)
	}

	if got := jobStatus(t, store, appliedID); got != statusApplied {
		t.Fatalf("expected applied status preserved, got %q", got)
	}
	if got := jobStatus(t, store, dismissedID); got != statusDismissed {
		t.Fatalf("expected dismissed status preserved, got %q", got)
	}
	if got := jobStatus(t, store, freshID); got != statusApply {
		t.Fatalf("expected fresh job to take active status, got %q", got)
	}
}

// listRecentJobs must carry the full job description from raw_json so the
// Resume Studio job picker can prefill the "Job description" textarea when a
// saved job is chosen (regression: the picker showed an empty textarea).
func TestListRecentJobsIncludesDescription(t *testing.T) {
	store := newTestStore(t)
	config := defaultConfig()
	results := []jobPost{{
		ID:          "linkedin:https://example.com/jobs/desc-1",
		Source:      "LinkedIn",
		Title:       "SRE",
		Company:     "Acme",
		Status:      statusApply,
		Score:       90,
		Description: "Operate Kubernetes clusters and own the on-call rotation.",
	}}
	if err := store.saveSearchResults(config, results); err != nil {
		t.Fatalf("saveSearchResults: %v", err)
	}

	jobs, err := store.listRecentJobs(10)
	if err != nil {
		t.Fatalf("listRecentJobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	if jobs[0].Description != "Operate Kubernetes clusters and own the on-call rotation." {
		t.Fatalf("expected description carried from raw_json, got %q", jobs[0].Description)
	}
}

func TestAIAPIKeyForProviderFallsBackToLegacyKey(t *testing.T) {
	store := newTestStore(t)
	config := defaultConfig()
	config.Form.Provider = "Gemini"
	if err := store.save(config); err != nil {
		t.Fatal(err)
	}
	if err := store.setAIAPIKey("legacy-key"); err != nil {
		t.Fatal(err)
	}
	got, err := store.aiAPIKeyForProvider("Gemini")
	if err != nil || got != "legacy-key" {
		t.Fatalf("expected legacy fallback for the primary provider, got %q err=%v", got, err)
	}

	got, err = store.aiAPIKeyForProvider("OpenRouter")
	if err != nil || got != "" {
		t.Fatalf("expected empty for unkeyed provider, got %q err=%v", got, err)
	}

	if err := store.setAIAPIKeyForProvider("OpenRouter", "or-key"); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.aiAPIKeyForProvider("OpenRouter"); got != "or-key" {
		t.Fatalf("expected provider-specific key, got %q", got)
	}
}

// A zero-collection run must surface an actionable, honest explanation instead
// of leaving the user with a blank/"configure your profile" empty state.
func TestZeroCollectedRunProducesSuggestions(t *testing.T) {
	d := newSearchDiagnostics([]string{"LinkedIn"})
	got := d.withSuggestions(defaultConfig())
	if len(got.Suggestions) == 0 {
		t.Fatal("expected suggestions when nothing was collected")
	}
}
