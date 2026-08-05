package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParseGupyNextDataExtractsNestedJobs proves the generic recursive walk
// in parseGupyNextData finds job records regardless of the exact
// props.pageProps.* path Gupy nests them under (CH-08 hardening) - the
// fixture mirrors a realistic __NEXT_DATA__ shape.
func TestParseGupyNextDataExtractsNestedJobs(t *testing.T) {
	path := filepath.Join("testdata", "gupy_next_data_sample.html")
	html, err := os.ReadFile(path)
	if err != nil {
		t.Skip("fixture not available:", err)
	}

	jobs := parseGupyNextData(string(html), "Remote")
	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs extracted from the nested fixture, got %d: %+v", len(jobs), jobs)
	}

	byTitle := map[string]jobPost{}
	for _, job := range jobs {
		byTitle[job.Title] = job
		if job.Source != "Gupy" {
			t.Fatalf("expected Source=Gupy, got %q", job.Source)
		}
		if !strings.Contains(job.URL, "gupy.io") {
			t.Fatalf("expected a gupy.io URL, got %q", job.URL)
		}
	}
	support, ok := byTitle["Analista de Suporte Pleno"]
	if !ok {
		t.Fatalf("expected to find the support analyst job, got %+v", jobs)
	}
	if support.Location != "Sao Paulo, SP" {
		t.Fatalf("expected city+state location, got %q", support.Location)
	}
	if !strings.Contains(support.URL, "111111") || strings.Contains(support.URL, "utm_source") {
		t.Fatalf("expected the tracking query stripped from the URL, got %q", support.URL)
	}
}

func TestParseGupyNextDataReturnsEmptyForEmptySPA(t *testing.T) {
	path := filepath.Join("testdata", "gupy_spa_empty_sample.html")
	html, err := os.ReadFile(path)
	if err != nil {
		t.Skip("fixture not available:", err)
	}

	jobs := parseGupyNextData(string(html), "Remote")
	if len(jobs) != 0 {
		t.Fatalf("expected 0 jobs from an empty SPA payload, got %d: %+v", len(jobs), jobs)
	}
}

func TestParseGupyNextDataReturnsNilWithoutScriptTag(t *testing.T) {
	jobs := parseGupyNextData(`<html><body>no next data here</body></html>`, "Remote")
	if jobs != nil {
		t.Fatalf("expected nil jobs when the page has no __NEXT_DATA__ script, got %+v", jobs)
	}
}

func TestGupyRecordsToJobsDedupesByID(t *testing.T) {
	records := []map[string]any{
		{"id": float64(123), "name": "Vaga A", "careerPageUrl": "https://empresa.gupy.io/jobs/123", "careerPageName": "Empresa"},
		{"id": float64(123), "name": "Vaga A (duplicate)", "careerPageUrl": "https://empresa.gupy.io/jobs/123?ref=2", "careerPageName": "Empresa"},
		{"id": float64(456), "name": "Vaga B", "careerPageUrl": "https://empresa.gupy.io/jobs/456", "careerPageName": "Empresa"},
	}
	jobs := gupyRecordsToJobs(records, "Remote")
	if len(jobs) != 2 {
		t.Fatalf("expected duplicates by id collapsed to 2 jobs, got %d: %+v", len(jobs), jobs)
	}
}

func TestGupyRecordsToJobsSkipsNonGupyURLs(t *testing.T) {
	records := []map[string]any{
		{"id": float64(1), "name": "Vaga externa", "careerPageUrl": "https://outra-empresa.example.com/jobs/1", "careerPageName": "Outra"},
	}
	jobs := gupyRecordsToJobs(records, "Remote")
	if len(jobs) != 0 {
		t.Fatalf("expected non-gupy.io URLs to be skipped, got %+v", jobs)
	}
}
