package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
)

func TestAgeFromLinkedInCardPrefersRelativeText(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(`
		<li>
		  <div class="base-search-card job-search-card">
		    <a href="https://www.linkedin.com/jobs/view/123"></a>
		    <time class="job-search-card__listdate--new" datetime="2026-07-03">Há 10 horas</time>
		  </div>
		</li>`))
	if err != nil {
		t.Fatal(err)
	}

	card := doc.Find("a").First()
	age, label := ageFromLinkedInCard(card)
	if age != 10 {
		t.Fatalf("expected relative text age 10h, got %v (label=%q)", age, label)
	}
	if !strings.Contains(label, "10") {
		t.Fatalf("expected label to mention 10 hours, got %q", label)
	}
}

func TestAgeFromLinkedInCardDateOnlyUsesLocalMidday(t *testing.T) {
	today := time.Now().In(time.Local).Format("2006-01-02")
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(`
		<div class="base-search-card">
		  <time datetime="` + today + `"></time>
		</div>`))
	if err != nil {
		t.Fatal(err)
	}

	age, _ := ageFromLinkedInCard(doc.Find(".base-search-card").First())
	if age >= 24 {
		t.Fatalf("date-only card posted today should be <24h at local midday anchor, got %v", age)
	}
}

func TestAgeHoursFromTextPortugueseAndEnglish(t *testing.T) {
	cases := map[string]float64{
		"Há 10 horas":    10,
		"2 days ago":     48,
		"3 semanas":      24 * 7 * 3,
		"Publicado hoje": 0,
		"just now":       0,
		"texto invalido": ageUnknown,
	}
	for input, want := range cases {
		if got := ageHoursFromText(input); got != want {
			t.Fatalf("ageHoursFromText(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestFilterFreshLinkedInUnknownAgeTrustsSearchWindow(t *testing.T) {
	jobs := []jobPost{
		{Source: "LinkedIn", Title: "A", AgeHours: ageUnknown},
		{Source: "Indeed", Title: "B", AgeHours: ageUnknown},
		{Source: "LinkedIn", Title: "C", AgeHours: 12},
		{Source: "LinkedIn", Title: "D", AgeHours: 72},
	}
	fresh := filterFresh(jobs, 24)
	if len(fresh) != 2 {
		t.Fatalf("expected LinkedIn unknown + 12h jobs, got %d: %+v", len(fresh), fresh)
	}
}

func TestParseIndeedJobsExtractsListingSnippet(t *testing.T) {
	html := `
		<div data-jk="abc123">
			<h2 class="jobTitle"><span title="DevOps Engineer">DevOps Engineer</span></h2>
			<span data-testid="company-name">Acme Cloud</span>
			<div data-testid="text-location">Remote</div>
			<div data-testid="job-snippet">Operate AWS, Terraform and Kubernetes platforms for production systems.</div>
			<span data-testid="myJobsStateDate">2 days ago</span>
		</div>`

	jobs := parseIndeedJobs(html, "Remote")
	if len(jobs) != 1 {
		t.Fatalf("expected 1 Indeed job, got %d", len(jobs))
	}
	job := jobs[0]
	if job.Title != "DevOps Engineer" || job.Company != "Acme Cloud" || job.Location != "Remote" {
		t.Fatalf("expected title/company/location from the listing card, got %+v", job)
	}
	if job.Description == "" || !strings.Contains(job.Description, "Terraform") {
		t.Fatalf("expected real listing snippet as the description, got %+v", job)
	}
	if job.PostedTime != "2 days ago" || job.AgeHours != 48 {
		t.Fatalf("expected listing date and age, got postedTime=%q ageHours=%v", job.PostedTime, job.AgeHours)
	}
	if job.URL != "https://br.indeed.com/viewjob?jk=abc123" {
		t.Fatalf("expected the human-openable /viewjob URL, got %q", job.URL)
	}
}

func TestParseLinkedInGuestSampleExtractsFreshAges(t *testing.T) {
	path := filepath.Join("testdata", "linkedin_guest_sample.html")
	html, err := os.ReadFile(path)
	if err != nil {
		t.Skip("fixture not available:", err)
	}

	jobs := parseLinkedInJobs(string(html), "Remote")
	if len(jobs) == 0 {
		t.Fatal("expected jobs from guest sample fixture")
	}

	fresh := filterFresh(jobs, 24)
	if len(fresh) == 0 {
		t.Fatalf("expected at least one job within 24h from fixture, got 0/%d parsed ages=%v", len(jobs), sampleAges(jobs))
	}
	if len(fresh) != len(jobs) {
		t.Logf("note: %d/%d jobs within 24h (some may be older in live fixture)", len(fresh), len(jobs))
	}
}

func sampleAges(jobs []jobPost) []float64 {
	out := make([]float64, 0, 5)
	for i, job := range jobs {
		if i >= 5 {
			break
		}
		out = append(out, job.AgeHours)
	}
	return out
}
