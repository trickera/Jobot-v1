package server

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	t.Setenv("SENCIA_DB_PATH", filepath.Join(t.TempDir(), "sencia.db"))
	return httptest.NewServer(newHandler(log.New(io.Discard, "", 0), "test-token"))
}

func TestHealth(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	response, err := http.Get(server.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.StatusCode)
	}
}

func TestStateRequiresToken(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	response, err := http.Get(server.URL + "/api/v1/state")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", response.StatusCode)
	}
}

func TestCorsAllowsLocalVitePortsOnly(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	request, err := http.NewRequest(http.MethodOptions, server.URL+"/api/v1/state", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", "http://127.0.0.1:1422")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if got := response.Header.Get("Access-Control-Allow-Origin"); got != "http://127.0.0.1:1422" {
		t.Fatalf("expected local dev origin allowed, got %q", got)
	}

	request, err = http.NewRequest(http.MethodOptions, server.URL+"/api/v1/state", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", "http://ipc.localhost")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if got := response.Header.Get("Access-Control-Allow-Origin"); got != "http://ipc.localhost" {
		t.Fatalf("expected Tauri IPC origin allowed, got %q", got)
	}

	request, err = http.NewRequest(http.MethodOptions, server.URL+"/api/v1/state", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", "https://example.com")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if got := response.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected external origin blocked, got %q", got)
	}
}

func TestConfigRoundTrip(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	config := defaultConfig()
	config.Form.Provider = "OpenAI"
	config.Form.APIKey = "test-key"
	config.Form.ScoreCut = 85
	// LocalItems is intentionally NOT client-authoritative (BUG-02 fix): the
	// server always recomputes it from the real jobs/applications/
	// search_history tables on load, ignoring whatever a client echoes back
	// here, so a stale/arbitrary value like this must NOT survive the round
	// trip.
	config.LocalItems.Jobs = 12

	body, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}

	request, err := http.NewRequest(http.MethodPut, server.URL+"/api/v1/config", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer test-token")
	request.Header.Set("Content-Type", "application/json")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.StatusCode)
	}

	request, err = http.NewRequest(http.MethodGet, server.URL+"/api/v1/config", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer test-token")

	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.StatusCode)
	}

	var saved appConfig
	if err := json.NewDecoder(response.Body).Decode(&saved); err != nil {
		t.Fatal(err)
	}
	if saved.Form.Provider != "OpenAI" || saved.Form.APIKey != "" || !saved.APIKeySet || saved.Form.ScoreCut != 85 {
		t.Fatalf("saved config mismatch: %+v", saved)
	}
	if saved.LocalItems.Jobs != 0 {
		t.Fatalf("expected LocalItems to reflect live DB stats (0 jobs persisted), not the client-echoed value: %+v", saved.LocalItems)
	}
}

func TestLogsEndpointNeverReturnsNull(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	request, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/logs", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer test-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	var payload struct {
		Logs []logEntry `json:"logs"`
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte(`"logs":null`)) {
		t.Fatalf("logs must serialize as [] not null: %s", body)
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Logs == nil {
		t.Fatal("expected non-nil logs slice on a fresh server")
	}
}

func TestSQLiteSchemaIsCreated(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sencia.db")
	t.Setenv("SENCIA_DB_PATH", dbPath)

	store := newConfigStore()
	if _, err := store.load(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for _, table := range []string{"settings", "secure_refs", "job_sources", "jobs", "applications", "search_history"} {
		var name string
		err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("expected table %s: %v", table, err)
		}
	}
}

func TestSearchDoesNotGeneratePlaceholderJobs(t *testing.T) {
	t.Setenv("SENCIA_GO_SCRAPER_DISABLED", "1")
	server := newTestServer(t)
	defer server.Close()

	config := defaultConfig()
	config.Form.Roles = "Platform Engineer"
	config.Form.Source = "LinkedIn"
	body, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPut, server.URL+"/api/v1/config", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer test-token")
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected config save 200, got %d", response.StatusCode)
	}

	request, err = http.NewRequest(http.MethodPost, server.URL+"/api/v1/search", bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer test-token")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("expected search 202, got %d", response.StatusCode)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		statusRequest, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/search/status", nil)
		if err != nil {
			t.Fatal(err)
		}
		statusRequest.Header.Set("Authorization", "Bearer test-token")
		statusResponse, err := http.DefaultClient.Do(statusRequest)
		if err != nil {
			t.Fatal(err)
		}
		var status searchStatusResponse
		if err := json.NewDecoder(statusResponse.Body).Decode(&status); err != nil {
			statusResponse.Body.Close()
			t.Fatal(err)
		}
		statusResponse.Body.Close()
		if !status.Running {
			if status.Error == "" {
				t.Fatalf("expected search error when scraper disabled, got %+v", status)
			}
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	store := newConfigStore()
	stats, err := store.stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.Jobs != 0 || stats.History != 0 {
		t.Fatalf("expected no generated jobs or history, got %+v", stats)
	}
}

func TestSearchRequiresRoles(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	config := defaultConfig()
	config.Form.Roles = ""
	config.Form.Role = ""
	body, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPut, server.URL+"/api/v1/config", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer test-token")
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()

	request, err = http.NewRequest(http.MethodPost, server.URL+"/api/v1/search", bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer test-token")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("expected roles conflict 409, got %d", response.StatusCode)
	}
}

func TestJobActionsPersistAppliedDismissAndBlacklist(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	job := jobSummary{
		ID:              "linkedin:https://example.com/jobs/123",
		Source:          "LinkedIn",
		Title:           "Platform Engineer",
		Company:         "Acme",
		Location:        "Remote",
		URL:             "https://example.com/jobs/123",
		Status:          statusApply,
		Score:           91,
		MissingKeywords: []string{"Kubernetes"},
	}

	postAction := func(action string, item jobSummary) {
		t.Helper()
		body, err := json.Marshal(map[string]any{"action": action, "job": item})
		if err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/jobs/action", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer test-token")
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("expected action %s 200, got %d", action, response.StatusCode)
		}
	}

	postAction("applied", job)

	store := newConfigStore()
	stats, err := store.stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.Applications != 1 {
		t.Fatalf("expected 1 application, got %+v", stats)
	}
	if !store.processedKeys()[titleCompanyKey(job.Title, job.Company)] {
		t.Fatal("expected applied job to be skipped in future searches")
	}

	job.ID = "linkedin:https://example.com/jobs/456"
	job.Title = "SRE"
	postAction("dismiss", job)
	if !store.processedKeys()[titleCompanyKey(job.Title, job.Company)] {
		t.Fatal("expected dismissed job to be skipped in future searches")
	}

	job.ID = "linkedin:https://example.com/jobs/789"
	job.Title = "Cloud Engineer"
	postAction("blacklist", job)

	config, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	if !csvContains(config.Form.Blacklist, "Acme") {
		t.Fatalf("expected Acme in blacklist, got %q", config.Form.Blacklist)
	}
}

func TestRadarIntervalBounds(t *testing.T) {
	config := defaultConfig()
	config.Form.RadarIntervalMinutes = 0
	if got := radarInterval(config); got != 20*time.Minute {
		t.Fatalf("expected default 20m, got %s", got)
	}
	config.Form.RadarIntervalMinutes = -5
	if got := radarInterval(config); got != 20*time.Minute {
		t.Fatalf("expected negative interval to default, got %s", got)
	}
	config.Form.RadarIntervalMinutes = 1
	if got := radarInterval(config); got != time.Minute {
		t.Fatalf("expected minimum 1m, got %s", got)
	}
}
