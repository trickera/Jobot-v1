package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every one of these parsers is driven by a REAL captured API response under
// testdata/, not a hand-written sample. The Indeed scraper's test fed synthetic
// [data-jk] markup and stayed green while the source was dead; these fixtures are
// the actual bytes Remotive/RemoteOK/Jobicy/Arbeitnow/WeWorkRemotely returned on
// 2026-07-12, so a parser that drifts from the real shape fails here.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func TestParseRemotive(t *testing.T) {
	jobs, err := parseRemotiveJobs(fixture(t, "remotive_sample.json"), nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(jobs) == 0 {
		t.Fatal("no jobs parsed from a real Remotive response")
	}
	j := jobs[0]
	if j.Source != "Remotive" || j.Title == "" || j.Company == "" || j.URL == "" {
		t.Fatalf("incomplete job: %+v", j)
	}
	if j.Modality != "Remote" {
		t.Fatalf("Remotive jobs are remote; got %q", j.Modality)
	}
	if j.AgeHours >= ageUnknown {
		t.Fatalf("publication_date should have parsed into an age; got %v", j.AgeHours)
	}
}

func TestParseRemoteOKSkipsLegalNotice(t *testing.T) {
	jobs, err := parseRemoteOKJobs(fixture(t, "remoteok_sample.json"), nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(jobs) == 0 {
		t.Fatal("no jobs parsed")
	}
	// The first array element is RemoteOK's legal-notice object. If it leaked
	// through, a job with an empty title would be here.
	for _, j := range jobs {
		if strings.TrimSpace(j.Title) == "" {
			t.Fatalf("the legal-notice element was not skipped: %+v", j)
		}
		if j.URL == "" {
			t.Fatalf("job has no URL: %+v", j)
		}
	}
}

func TestParseJobicy(t *testing.T) {
	jobs, err := parseJobicyJobs(fixture(t, "jobicy_sample.json"), nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(jobs) == 0 {
		t.Fatal("no jobs parsed")
	}
	if jobs[0].Company == "" || jobs[0].Title == "" {
		t.Fatalf("incomplete: %+v", jobs[0])
	}
}

func TestParseArbeitnowCarriesOnsiteJobs(t *testing.T) {
	jobs, err := parseArbeitnowJobs(fixture(t, "arbeitnow_sample.json"), nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(jobs) == 0 {
		t.Fatal("no jobs parsed")
	}
	// Arbeitnow is the one source that carries on-site jobs — the breadth Indeed
	// used to give. Its remote=false entries must be tagged Hybrid/On-site, not
	// silently marked Remote.
	for _, j := range jobs {
		if j.Modality != "Remote" && j.Modality != "Hybrid/On-site" {
			t.Fatalf("unexpected modality %q on %+v", j.Modality, j)
		}
		if j.AgeHours >= ageUnknown {
			t.Fatalf("created_at (unix) should have parsed into an age; got %v on %q", j.AgeHours, j.Title)
		}
	}
}

func TestParseWeWorkRemotelySplitsCompanyFromTitle(t *testing.T) {
	jobs, err := parseWeWorkRemotelyJobs(fixture(t, "weworkremotely_sample.xml"), nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(jobs) == 0 {
		t.Fatal("no jobs parsed from the RSS feed")
	}
	// WeWorkRemotely titles are "Company: Role"; both halves must be recovered.
	j := jobs[0]
	if j.Company == "" || j.Title == "" {
		t.Fatalf("company/title not split: %+v", j)
	}
	if strings.Contains(j.Title, ": ") && strings.HasPrefix(j.Title, j.Company) {
		t.Fatalf("title still carries the company prefix: %q", j.Title)
	}
	// The description arrives as an HTML blob and must be flattened to text.
	if strings.Contains(j.Description, "<") {
		t.Fatalf("description still has HTML tags: %q", j.Description[:80])
	}
}

// The firehose sources (RemoteOK, Arbeitnow, WeWorkRemotely) return every remote
// job regardless of role. Without a title filter they would flood the pipeline,
// so a role that matches nothing must yield nothing.
func TestRoleFilterDropsUnrelatedTitles(t *testing.T) {
	all, err := parseRemoteOKJobs(fixture(t, "remoteok_sample.json"), nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	none, err := parseRemoteOKJobs(fixture(t, "remoteok_sample.json"), []string{"zzz-nonexistent-role"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("expected some jobs with no filter")
	}
	if len(none) != 0 {
		t.Fatalf("a role matching no title should drop everything; kept %d", len(none))
	}
}

// The REST toggles must default off, so a stock config does not silently start
// hitting five extra endpoints — LinkedIn stays the only default source.
func TestRemoteAPIsDefaultOff(t *testing.T) {
	config := defaultConfig()
	if anyRemoteAPIEnabled(config) {
		t.Fatal("REST sources must be off by default")
	}
	if !config.Toggles["useLinkedin"] {
		t.Fatal("LinkedIn must stay on by default")
	}
	config.Toggles["useJobicy"] = true
	if !anyRemoteAPIEnabled(config) {
		t.Fatal("enabling one REST source must flip anyRemoteAPIEnabled")
	}
}

// A search with only the free boards enabled is a valid search. The "no sources
// configured" guard used to count only LinkedIn/Indeed/Gupy, so it rejected such
// a search with 409 and the REST toggles did nothing — found by driving the app.
func TestConfiguredSourcesCountsRemoteAPIs(t *testing.T) {
	config := defaultConfig()
	config.Toggles["useLinkedin"] = false
	config.Toggles["useIndeed"] = false
	config.Toggles["useGupy"] = false
	config.Form.Source = ""
	if configuredSources(config) != 0 {
		t.Fatal("precondition: no sources should be configured yet")
	}
	config.Toggles["useRemotive"] = true
	if configuredSources(config) == 0 {
		t.Fatal("a search with only a free board enabled must be allowed to start")
	}
}

// The REST boards list active openings across ~30 days with no server-side date
// filter, so the local recentHours window must not cull them — otherwise, with
// the 24h default, a real search dropped 12 of 13 collected jobs (found by
// driving the app). A scraped LinkedIn job the same age is still dropped.
func TestFreshnessWindowSpareTheRESTBoards(t *testing.T) {
	old := 20 * 24.0 // 20 days, well past the default 24h window
	jobs := []jobPost{
		{ID: "r", Source: "Remotive", Title: "Dev", AgeHours: old},
		{ID: "w", Source: "WeWorkRemotely", Title: "Dev", AgeHours: old},
		{ID: "l", Source: "LinkedIn", Title: "Dev", AgeHours: old},
	}
	kept := filterFresh(jobs, 24)
	keptSources := map[string]bool{}
	for _, j := range kept {
		keptSources[j.Source] = true
	}
	if !keptSources["Remotive"] || !keptSources["WeWorkRemotely"] {
		t.Fatalf("REST boards must survive the freshness window; kept %+v", keptSources)
	}
	if keptSources["LinkedIn"] {
		t.Fatal("a scraped LinkedIn job past the window must still be dropped")
	}
}
