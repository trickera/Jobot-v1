package server

import (
	"errors"
	"io"
	"log"
	"strings"
	"testing"
)

// fakePageFetcher records every browser request so the listing-only contract
// fails if description enrichment touches an individual Indeed job page.
type fakePageFetcher struct {
	urls      []string
	warmCalls int
}

func (f *fakePageFetcher) fetch(url string, _ string, _ string) (string, bool, error) {
	f.urls = append(f.urls, url)
	return "", false, errors.New("unexpected Indeed detail fetch")
}

func (f *fakePageFetcher) warmIndeed() error {
	f.warmCalls++
	return nil
}

func newIndeedTestBridge() *scraperBridge {
	return &scraperBridge{logger: log.New(io.Discard, "", 0)}
}

func assertNoIndeedDetailFetch(t *testing.T, fetcher *fakePageFetcher) {
	t.Helper()
	if len(fetcher.urls) != 0 {
		t.Fatalf("expected zero detail fetches, got %v", fetcher.urls)
	}
	if fetcher.warmCalls != 0 {
		t.Fatalf("expected warmIndeed not to run for detail enrichment, got %d call(s)", fetcher.warmCalls)
	}
}

func TestFetchJobDescriptionUsesIndeedListingSnippetOnly(t *testing.T) {
	bridge := newIndeedTestBridge()
	fetcher := &fakePageFetcher{}
	job := jobPost{
		Source:      "Indeed",
		URL:         "https://br.indeed.com/viewjob?jk=abc123",
		Description: "Operate AWS, Terraform and Kubernetes platforms for production systems.",
	}

	got := bridge.fetchJobDescription(fetcher, &job)
	if got != job.Description {
		t.Fatalf("expected listing snippet %q, got %q", job.Description, got)
	}
	assertNoIndeedDetailFetch(t, fetcher)
	if job.URL != "https://br.indeed.com/viewjob?jk=abc123" {
		t.Fatalf("expected the human-openable /viewjob URL to stay on the card, got %q", job.URL)
	}
}

func TestFetchJobDescriptionRejectsMissingOrInsufficientIndeedSnippetWithoutFetching(t *testing.T) {
	for _, snippet := range []string{"", "too short"} {
		t.Run(snippet, func(t *testing.T) {
			bridge := newIndeedTestBridge()
			fetcher := &fakePageFetcher{}
			job := jobPost{
				Source:      "Indeed",
				URL:         "https://br.indeed.com/viewjob?jk=abc123",
				Description: snippet,
			}

			if got := bridge.fetchJobDescription(fetcher, &job); got != "" {
				t.Fatalf("expected an honest empty description for snippet %q, got %q", snippet, got)
			}
			assertNoIndeedDetailFetch(t, fetcher)
		})
	}
}

func TestIndeedListingOnlyJobReachesScorePersistenceAndResult(t *testing.T) {
	listing := `
		<div data-jk="abc123">
			<h2 class="jobTitle"><span title="Backend Engineer">Backend Engineer</span></h2>
			<span data-testid="company-name">Acme Cloud</span>
			<div data-testid="text-location">Remote</div>
			<div data-testid="job-snippet">Build backend APIs with Go, PostgreSQL and AWS for remote production systems.</div>
			<span data-testid="myJobsStateDate">2 days ago</span>
		</div>`
	jobs := parseIndeedJobs(listing, "Remote")
	if len(jobs) != 1 {
		t.Fatalf("expected one listing job, got %d", len(jobs))
	}

	bridge := newIndeedTestBridge()
	fetcher := &fakePageFetcher{}
	job := jobs[0]
	job.Description = bridge.fetchJobDescription(fetcher, &job)
	assertNoIndeedDetailFetch(t, fetcher)

	config := defaultConfig()
	config.Form.Role = "Backend Engineer"
	config.Form.Roles = "Backend Engineer"
	config.Form.Keywords = "Go, PostgreSQL, AWS"
	config.Form.ResumeText = "Backend Engineer using Go, PostgreSQL and AWS."
	job.Score, job.Missing = heuristicScoreV2(config, job)
	job.ScoreSource = scoreSourceOfflineNoKey
	job.ScoreReason = "Sem chave de IA configurada; estimativa offline usada."
	job.Status = statusForScore(config, job.Score, job.Missing)
	if job.Status != statusApply {
		t.Fatalf("expected the matching listing snippet to reach an approved score, got score=%d status=%q missing=%v", job.Score, job.Status, job.Missing)
	}

	store := newTestStore(t)
	if err := store.saveSearchResults(config, []jobPost{job}); err != nil {
		t.Fatalf("persist listing-only result: %v", err)
	}
	stored, err := store.listRecentJobs(10)
	if err != nil {
		t.Fatalf("read persisted jobs: %v", err)
	}
	if len(stored) != 1 || stored[0].URL != job.URL || stored[0].Description != job.Description || stored[0].Score != job.Score {
		t.Fatalf("listing fields/score did not survive persistence: %+v", stored)
	}
	history, err := store.listSearchHistory(10)
	if err != nil {
		t.Fatalf("read persisted history: %v", err)
	}
	if len(history) != 1 || history[0].ResultsCount != 1 {
		t.Fatalf("expected one persisted search result, got %+v", history)
	}

	result := postToSummary(job)
	if result.Title != job.Title || result.Company != job.Company || result.Location != job.Location ||
		result.Description != job.Description || result.URL != job.URL || result.Score != job.Score {
		t.Fatalf("listing-only card lost fields before the result: %+v", result)
	}
	if !strings.Contains(result.URL, "/viewjob") {
		t.Fatalf("expected result to retain the human-openable /viewjob URL, got %q", result.URL)
	}
}
