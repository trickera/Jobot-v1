package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

// The Resume Studio route surface (Task 5) must exist and require the
// bearer token like every other route. /resume/parse, /resume/diagnose,
// /resume/analyze-job, /resume/gap, /resume/optimize, /resume/score,
// /resume/export, /resume/version + /resume/versions and
// /resume/cover-letter (Wave 3 Task 8) are all implemented and checked
// separately in resume_optimizer_test.go / resume_analyzer_test.go /
// job_description_analyzer_test.go / ats_score_test.go /
// resume_export_test.go / resume_versions_test.go / cover_letter_test.go.
func TestResumeRoutesRequireToken(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	routes := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/resume/parse"},
		{http.MethodPost, "/api/v1/resume/diagnose"},
		{http.MethodPost, "/api/v1/resume/analyze-job"},
		{http.MethodPost, "/api/v1/resume/gap"},
		{http.MethodPost, "/api/v1/resume/optimize"},
		{http.MethodPost, "/api/v1/resume/score"},
		{http.MethodPost, "/api/v1/resume/export"},
		{http.MethodPost, "/api/v1/resume/version"},
		{http.MethodGet, "/api/v1/resume/versions"},
		{http.MethodPost, "/api/v1/resume/cover-letter"},
		{http.MethodGet, "/api/v1/resume/templates"},
	}

	for _, route := range routes {
		t.Run(route.path, func(t *testing.T) {
			req, err := http.NewRequest(route.method, server.URL+route.path, bytes.NewReader([]byte("{}")))
			if err != nil {
				t.Fatal(err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("expected 401 without token, got %d", resp.StatusCode)
			}
		})
	}
}

// The bearer token is a secret: comparison must be exact (EqualFold made it
// case-insensitive, shrinking the effective entropy) and constant-time.
func TestAuthorizeRejectsCaseVariantToken(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/resume/templates", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer TEST-TOKEN")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a case-variant token, got %d", resp.StatusCode)
	}
}

// DELETE /resume/versions/{id} and PATCH /resume/versions/{id} exist, but the
// CORS middleware only advertised GET/POST/PUT — a browser dev origin (Vite)
// failed the preflight for delete/rename version.
func TestCORSPreflightAdvertisesDeleteAndPatch(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	req, err := http.NewRequest(http.MethodOptions, server.URL+"/api/v1/resume/versions/some-id", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "http://localhost:1420")
	req.Header.Set("Access-Control-Request-Method", http.MethodDelete)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	methods := resp.Header.Get("Access-Control-Allow-Methods")
	for _, want := range []string{"DELETE", "PATCH"} {
		if !bytes.Contains([]byte(methods), []byte(want)) {
			t.Fatalf("expected Allow-Methods to include %s, got %q", want, methods)
		}
	}
}

// Resume JSON handlers must bound how much request body they read — the
// upload/OCR endpoints already did (jsonDecodeLimited), the rest used an
// unbounded json.NewDecoder(r.Body).
func TestResumeJSONHandlersLimitBodySize(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	// ~17 MB of valid JSON — just above the 16 MB shared limit, so a limited
	// decoder fails mid-string while an unbounded one happily processes it.
	huge := bytes.Repeat([]byte("a"), 17<<20)
	body := append([]byte(`{"rawText":"`), huge...)
	body = append(body, []byte(`"}`)...)

	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/resume/diagnose", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for an oversized body, got %d", resp.StatusCode)
	}
}

// TestResumeTemplatesRouteListsSeeded checks that GET /resume/templates
// seeds and returns the 3 built-in templates (Fase 1.5, Task 2).
func TestResumeTemplatesRouteListsSeeded(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/resume/templates", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with token, got %d", resp.StatusCode)
	}

	var payload resumeTemplatesResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Templates) != 3 {
		t.Fatalf("expected 3 templates, got %d: %+v", len(payload.Templates), payload.Templates)
	}

	byID := make(map[string]resumeTemplate, len(payload.Templates))
	for _, tmpl := range payload.Templates {
		byID[tmpl.ID] = tmpl
	}
	if tmpl, ok := byID[resumeATSStrictTemplateID]; !ok || !tmpl.IsATS {
		t.Fatalf("expected ATS Strict template with isAts=true: %+v", tmpl)
	}
	if tmpl, ok := byID[resumeATSCleanTemplateID]; !ok || !tmpl.IsATS {
		t.Fatalf("expected ATS Clean template with isAts=true: %+v", tmpl)
	}
	if tmpl, ok := byID[resumeModernAccentTemplateID]; !ok || tmpl.IsATS {
		t.Fatalf("expected Modern Accent template with isAts=false: %+v", tmpl)
	}
}
