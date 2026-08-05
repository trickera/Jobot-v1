package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The page br.indeed.com actually serves, captured on 2026-07-12 through the real
// Camoufox worker: a Cloudflare interstitial titled "Security Check - Indeed.com".
//
// This fixture exists because scraper_indeed_test.go was passing the whole time
// Indeed was dead. It feeds parseIndeedJobs synthetic HTML carrying data-jk, so
// it proved the parser could read a page Indeed no longer sends. A test fed
// markup production does not return is not a test.
func indeedBlockPage(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "indeed_cloudflare_challenge_sample.html"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(raw)
}

// The failure that shipped: zero cards parse out of a captcha page, and zero
// cards is exactly what a quiet day looks like too.
func TestIndeedBlockPageYieldsNoJobs(t *testing.T) {
	jobs := parseIndeedJobs(indeedBlockPage(t), "Remote")
	if len(jobs) != 0 {
		t.Fatalf("expected no jobs out of a Cloudflare challenge page, got %d", len(jobs))
	}
}

// The fix. The Python worker's looks_blocked() must recognise this page — its
// BLOCK_MARKERS list said "security challenge" while Cloudflare says "Security
// Check", so it answered blocked=false and Go believed it. This asserts the
// markers the live page actually carries, so the same near-miss cannot recur.
func TestIndeedBlockPageCarriesTheMarkersTheWorkerLooksFor(t *testing.T) {
	html := strings.ToLower(indeedBlockPage(t))
	// Kept in sync with BLOCK_MARKERS in apps/browser-worker/worker.py.
	cloudflare := []string{"_cf_chl_opt", "__cf_chl_tk", "/challenge-platform/"}
	found := false
	for _, marker := range cloudflare {
		if strings.Contains(html, marker) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("the captured block page carries none of the Cloudflare markers the worker looks for: %v", cloudflare)
	}

	// And the marker must not be the bare word "captcha": job postings for security
	// roles contain it, and a marker that fires on a real result page throws away
	// good jobs.
	if strings.Contains(strings.Join(cloudflare, " "), "captcha") {
		t.Fatal("do not match on the bare word captcha - it appears in legitimate job descriptions")
	}
}

// A walled source must be named to the user. Before this, the app said
// "Busca concluida: N vaga(s)" whether it had searched three boards or one.
func TestOutcomeMessageNamesABlockedSource(t *testing.T) {
	diagnostics := searchDiagnostics{
		Collected: 10,
		Sources: map[string]sourceDiagnostics{
			"LinkedIn": {Collected: 10, Approved: 4},
			"Indeed":   {Blocked: true},
		},
	}
	message := buildSearchOutcomeMessage(diagnostics, false, 4)
	if !strings.Contains(message, "Indeed") {
		t.Fatalf("a blocked source must be named; got %q", message)
	}
	if !strings.Contains(message, "anti-bot") {
		t.Fatalf("the message must say why the source returned nothing; got %q", message)
	}
	// The real outcome still has to be there - the note prepends, it does not
	// replace.
	if !strings.Contains(message, "4 vaga(s)") {
		t.Fatalf("the note must not swallow the actual outcome; got %q", message)
	}
}

// And a healthy search must stay clean: no scary note when nothing was blocked.
func TestOutcomeMessageStaysQuietWhenNothingIsBlocked(t *testing.T) {
	diagnostics := searchDiagnostics{
		Collected: 10,
		Sources:   map[string]sourceDiagnostics{"LinkedIn": {Collected: 10, Approved: 4}},
	}
	message := buildSearchOutcomeMessage(diagnostics, false, 4)
	if strings.Contains(message, "anti-bot") {
		t.Fatalf("a clean search must not mention blocking; got %q", message)
	}
	if !strings.Contains(message, "Busca concluida") {
		t.Fatalf("expected the normal completion message; got %q", message)
	}
}
