package server

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Free, keyless job APIs. Indeed shut its door (Cloudflare "Security Check" on
// every domain, RSS and mobile included, verified 2026-07-12 — even a real human
// click did not pass, and the old jobhunter's own code gets zero today), so the
// international volume it used to provide comes from here instead. These are
// plain JSON/RSS endpoints: no browser, no key, no anti-bot wall, so they cannot
// be blocked the way the scraped boards are, and they add nothing to the search's
// wall-clock beyond one HTTP round-trip each.
//
// Each source is enabled by its own toggle (useRemotive, useRemoteok, useJobicy,
// useArbeitnow, useWeworkremotely); all default off, and LinkedIn stays the
// always-on default. Every parse function is pure and driven in tests by a real
// captured response under testdata/ — never synthetic markup, which is the exact
// blind spot that let the Indeed scraper stay dead while its test stayed green.

// remoteAPISource pairs a toggle key with the collector that runs when it is on.
type remoteAPISource struct {
	toggle string
	name   string
	fetch  func(ctx context.Context, client *http.Client, roles []string) ([]jobPost, error)
}

func remoteAPISources() []remoteAPISource {
	return []remoteAPISource{
		{"useRemotive", "Remotive", fetchRemotive},
		{"useRemoteok", "RemoteOK", fetchRemoteOK},
		{"useJobicy", "Jobicy", fetchJobicy},
		{"useArbeitnow", "Arbeitnow", fetchArbeitnow},
		{"useWeworkremotely", "WeWorkRemotely", fetchWeWorkRemotely},
	}
}

// anyRemoteAPIEnabled reports whether the search needs the REST pass at all.
func anyRemoteAPIEnabled(config appConfig) bool {
	for _, source := range remoteAPISources() {
		if config.Toggles[source.toggle] {
			return true
		}
	}
	return false
}

// isRemoteAPISource reports whether a job came from one of the REST boards, whose
// descriptions arrive with the listing and must not be re-fetched via a browser.
func isRemoteAPISource(source string) bool {
	for _, s := range remoteAPISources() {
		if strings.EqualFold(source, s.name) {
			return true
		}
	}
	return false
}

// collectRemoteAPIs runs every enabled REST source once for a profile. It is
// deliberately outside the browser modality loop: these boards are remote-only
// and location-agnostic, so querying them per modality/location would just
// duplicate work.
func (s *scraperBridge) collectRemoteAPIs(ctx context.Context, config appConfig, profile searchProfile) []jobPost {
	roles := profile.Roles
	var out []jobPost
	for _, source := range remoteAPISources() {
		if ctx.Err() != nil {
			return out
		}
		if !config.Toggles[source.toggle] {
			continue
		}
		jobs, err := source.fetch(ctx, s.client, roles)
		if err != nil {
			s.logSource(source.name, 0, err)
			continue
		}
		jobs = tagJobsWithProfile(jobs, profile)
		s.logSource(fmt.Sprintf("%s [%s]", source.name, profile.Name), len(jobs), nil)
		out = append(out, jobs...)
	}
	return out
}

// getBody issues a GET with a browser-shaped UA (some of these endpoints answer
// a bare Go UA with a 403) and returns the response body.
func getBody(ctx context.Context, client *http.Client, endpoint string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	request.Header.Set("Accept", "application/json, text/xml, */*")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	return io.ReadAll(io.LimitReader(response.Body, 8<<20))
}

// titleMatchesAnyRole keeps a firehose source honest: a board that returns every
// remote job in every field would flood the pipeline, so a job survives only if
// one of the profile's role terms appears in its title. With no roles configured
// (the profile is a catch-all), everything passes.
func titleMatchesAnyRole(title string, roles []string) bool {
	if len(roles) == 0 {
		return true
	}
	lowered := strings.ToLower(title)
	for _, role := range roles {
		role = strings.ToLower(strings.TrimSpace(role))
		if role != "" && strings.Contains(lowered, role) {
			return true
		}
	}
	return false
}

func remoteJob(source, jobURL, title, company, location, description, isoDate string) jobPost {
	age := ageHoursFromISO(isoDate)
	return jobPost{
		ID:          stableJobID(strings.ToLower(source), jobURL),
		Source:      source,
		Title:       strings.TrimSpace(title),
		Company:     strings.TrimSpace(company),
		Location:    strings.TrimSpace(location),
		URL:         strings.TrimSpace(jobURL),
		Description: truncate(cleanHTML(description), 2500),
		Modality:    "Remote",
		AgeHours:    age,
		PostedTime:  strings.TrimSpace(isoDate),
	}
}

var htmlTagPattern = regexp.MustCompile(`<[^>]+>`)

// cleanHTML strips tags and unescapes entities so a description that arrives as
// an HTML blob (WeWorkRemotely does this) reads as plain text downstream.
func cleanHTML(s string) string {
	if s == "" {
		return ""
	}
	s = htmlTagPattern.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(s, " "))
}

// --- Remotive ---------------------------------------------------------------

func parseRemotiveJobs(data []byte, roles []string) ([]jobPost, error) {
	var payload struct {
		Jobs []struct {
			URL             string `json:"url"`
			Title           string `json:"title"`
			CompanyName     string `json:"company_name"`
			Location        string `json:"candidate_required_location"`
			PublicationDate string `json:"publication_date"`
			Description     string `json:"description"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	var jobs []jobPost
	for _, item := range payload.Jobs {
		if !titleMatchesAnyRole(item.Title, roles) {
			continue
		}
		jobs = append(jobs, remoteJob("Remotive", item.URL, item.Title, item.CompanyName,
			coalesce(item.Location, "Remote"), item.Description, item.PublicationDate))
	}
	return jobs, nil
}

func fetchRemotive(ctx context.Context, client *http.Client, roles []string) ([]jobPost, error) {
	endpoint := "https://remotive.com/api/remote-jobs?limit=50"
	if search := strings.TrimSpace(strings.Join(roles, " ")); search != "" {
		endpoint += "&search=" + url.QueryEscape(search)
	}
	body, err := getBody(ctx, client, endpoint)
	if err != nil {
		return nil, err
	}
	return parseRemotiveJobs(body, roles)
}

// --- RemoteOK ---------------------------------------------------------------

func parseRemoteOKJobs(data []byte, roles []string) ([]jobPost, error) {
	// The first array element is a legal-notice object, not a job; it has no
	// "position", so decoding into a struct without that field leaves it empty
	// and the guard below drops it.
	var items []struct {
		Position    string `json:"position"`
		Company     string `json:"company"`
		Location    string `json:"location"`
		URL         string `json:"url"`
		ApplyURL    string `json:"apply_url"`
		Date        string `json:"date"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	var jobs []jobPost
	for _, item := range items {
		if strings.TrimSpace(item.Position) == "" {
			continue
		}
		if !titleMatchesAnyRole(item.Position, roles) {
			continue
		}
		jobs = append(jobs, remoteJob("RemoteOK", coalesce(item.URL, item.ApplyURL), item.Position,
			item.Company, coalesce(item.Location, "Remote"), item.Description, item.Date))
	}
	return jobs, nil
}

func fetchRemoteOK(ctx context.Context, client *http.Client, roles []string) ([]jobPost, error) {
	body, err := getBody(ctx, client, "https://remoteok.com/api")
	if err != nil {
		return nil, err
	}
	return parseRemoteOKJobs(body, roles)
}

// --- Jobicy -----------------------------------------------------------------

func parseJobicyJobs(data []byte, roles []string) ([]jobPost, error) {
	var payload struct {
		Jobs []struct {
			URL            string `json:"url"`
			JobTitle       string `json:"jobTitle"`
			CompanyName    string `json:"companyName"`
			JobGeo         string `json:"jobGeo"`
			PubDate        string `json:"pubDate"`
			JobDescription string `json:"jobDescription"`
			JobExcerpt     string `json:"jobExcerpt"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	var jobs []jobPost
	for _, item := range payload.Jobs {
		if !titleMatchesAnyRole(item.JobTitle, roles) {
			continue
		}
		jobs = append(jobs, remoteJob("Jobicy", item.URL, item.JobTitle, item.CompanyName,
			coalesce(item.JobGeo, "Remote"), coalesce(item.JobDescription, item.JobExcerpt), item.PubDate))
	}
	return jobs, nil
}

func fetchJobicy(ctx context.Context, client *http.Client, roles []string) ([]jobPost, error) {
	body, err := getBody(ctx, client, "https://jobicy.com/api/v2/remote-jobs?count=50")
	if err != nil {
		return nil, err
	}
	return parseJobicyJobs(body, roles)
}

// --- Arbeitnow (EU/Germany, and the only one that carries on-site jobs) ------

func parseArbeitnowJobs(data []byte, roles []string) ([]jobPost, error) {
	var payload struct {
		Data []struct {
			Title       string `json:"title"`
			CompanyName string `json:"company_name"`
			Location    string `json:"location"`
			Remote      bool   `json:"remote"`
			URL         string `json:"url"`
			Description string `json:"description"`
			CreatedAt   int64  `json:"created_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	var jobs []jobPost
	for _, item := range payload.Data {
		if !titleMatchesAnyRole(item.Title, roles) {
			continue
		}
		job := remoteJob("Arbeitnow", item.URL, item.Title, item.CompanyName,
			coalesce(item.Location, "Europe"), item.Description, "")
		// created_at is a unix timestamp, not ISO — set age from it directly.
		if item.CreatedAt > 0 {
			hours := time.Since(time.Unix(item.CreatedAt, 0)).Hours()
			if hours < 0 {
				hours = 0
			}
			job.AgeHours = hours
			job.PostedTime = time.Unix(item.CreatedAt, 0).UTC().Format(time.RFC3339)
		}
		if !item.Remote {
			job.Modality = "Hybrid/On-site"
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

func fetchArbeitnow(ctx context.Context, client *http.Client, roles []string) ([]jobPost, error) {
	body, err := getBody(ctx, client, "https://www.arbeitnow.com/api/job-board-api")
	if err != nil {
		return nil, err
	}
	return parseArbeitnowJobs(body, roles)
}

// --- WeWorkRemotely (RSS) ---------------------------------------------------

func parseWeWorkRemotelyJobs(data []byte, roles []string) ([]jobPost, error) {
	var feed struct {
		Channel struct {
			Items []struct {
				Title       string `xml:"title"`
				Region      string `xml:"region"`
				Link        string `xml:"link"`
				PubDate     string `xml:"pubDate"`
				Description string `xml:"description"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(data, &feed); err != nil {
		return nil, err
	}
	var jobs []jobPost
	for _, item := range feed.Channel.Items {
		// WeWorkRemotely titles are "Company: Role".
		company, title := item.Title, item.Title
		if idx := strings.Index(item.Title, ": "); idx > 0 {
			company = strings.TrimSpace(item.Title[:idx])
			title = strings.TrimSpace(item.Title[idx+2:])
		}
		if !titleMatchesAnyRole(title, roles) {
			continue
		}
		iso := ""
		if parsed, err := time.Parse(time.RFC1123Z, strings.TrimSpace(item.PubDate)); err == nil {
			iso = parsed.UTC().Format(time.RFC3339)
		}
		jobs = append(jobs, remoteJob("WeWorkRemotely", item.Link, title, company,
			coalesce(item.Region, "Remote"), item.Description, iso))
	}
	return jobs, nil
}

func fetchWeWorkRemotely(ctx context.Context, client *http.Client, roles []string) ([]jobPost, error) {
	body, err := getBody(ctx, client, "https://weworkremotely.com/categories/remote-programming-jobs.rss")
	if err != nil {
		return nil, err
	}
	return parseWeWorkRemotelyJobs(body, roles)
}
