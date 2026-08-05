package server

import (
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// Source endpoints. The Python worker only opens these URLs with a stealth
// browser and returns raw HTML (or captured Gupy XHR JSON). All parsing,
// dating, and filtering happen here in Go.
const (
	linkedInSearchURL = "https://www.linkedin.com/jobs-guest/jobs/api/seeMoreJobPostings/search"
	linkedInJobURL    = "https://www.linkedin.com/jobs-guest/jobs/api/jobPosting/%s"
	indeedSearchURL   = "https://br.indeed.com/jobs"
	indeedJobURL      = "https://br.indeed.com/viewjob?jk=%s"
	gupySearchURL     = "https://portal.gupy.io/job-search/list"
)

// ---------------------------------------------------------------------------
// URL building
// ---------------------------------------------------------------------------

func clampRecentHours(hours int) int {
	if hours <= 0 {
		return 48
	}
	if hours > 168 {
		return 168
	}
	return hours
}

func buildLinkedInURL(keywords string, location string, remote bool, start int, recentHours int) string {
	values := url.Values{}
	values.Set("keywords", keywords)
	values.Set("location", location)
	values.Set("f_TPR", fmt.Sprintf("r%d", clampRecentHours(recentHours)*3600))
	values.Set("start", strconv.Itoa(start))
	raw := values.Encode()
	if remote {
		raw += "&f_WT=2"
	} else {
		raw += "&f_WT=1%2C3"
	}
	return linkedInSearchURL + "?" + raw
}

func buildIndeedURL(keywords string, location string, remote bool, recentHours int) string {
	values := url.Values{}
	values.Set("q", keywords)
	values.Set("l", location)
	values.Set("sort", "date")
	fromage := (clampRecentHours(recentHours) + 23) / 24
	if fromage < 1 {
		fromage = 1
	}
	values.Set("fromage", strconv.Itoa(fromage))
	raw := values.Encode()
	if remote {
		raw += "&sc=0kf%3Aattr(DSQF7)%3B"
	}
	return indeedSearchURL + "?" + raw
}

func buildGupyURL(keywords string, location string, remote bool) string {
	raw := "searchTerm=" + url.QueryEscape(keywords)
	if remote {
		raw += "&workplaceType=remote"
	} else {
		if strings.TrimSpace(location) != "" {
			raw += "&city=" + url.QueryEscape(location)
		}
		raw += "&workplaceType=hybrid&workplaceType=on-site"
	}
	return gupySearchURL + "?" + raw
}

// ---------------------------------------------------------------------------
// Posting age parsing (Layer 2)
// ---------------------------------------------------------------------------

const ageUnknown = 9999.0

var literalAgeTokens = []struct {
	token string
	hours float64
}{
	{"hoje", 0}, {"today", 0}, {"just now", 0}, {"agora", 0},
	{"recem", 0}, {"recien", 0}, {"recentemente", 0},
	{"ontem", 24}, {"yesterday", 24}, {"ayer", 24},
}

var agePatterns = []struct {
	re   *regexp.Regexp
	mult float64
}{
	{regexp.MustCompile(`(\d+)\s*(?:min|minuto|minute)s?\b`), 1.0 / 60.0},
	{regexp.MustCompile(`(\d+)\s*(?:hr|hour|hora)s?\b`), 1},
	{regexp.MustCompile(`(\d+)\s*(?:day|dia)s?\b`), 24},
	{regexp.MustCompile(`(\d+)\s*(?:week|sem|semana)s?\b`), 24 * 7},
	{regexp.MustCompile(`(\d+)\s*(?:month|mes|meses)\b`), 24 * 30},
	{regexp.MustCompile(`(\d+)\s*(?:year|ano)s?\b`), 24 * 365},
}

// ageHoursFromText interprets relative labels in PT/EN/(defensive)ES.
// Returns ageUnknown when nothing recognizable is found.
func ageHoursFromText(text string) float64 {
	normalized := strings.TrimSpace(normalizeText(text))
	if normalized == "" {
		return ageUnknown
	}
	for _, literal := range literalAgeTokens {
		if strings.Contains(normalized, literal.token) {
			return literal.hours
		}
	}
	for _, pattern := range agePatterns {
		if match := pattern.re.FindStringSubmatch(normalized); len(match) > 1 {
			if value, err := strconv.Atoi(match[1]); err == nil {
				return float64(value) * pattern.mult
			}
		}
	}
	return ageUnknown
}

func isDateOnlyValue(value string) bool {
	value = strings.TrimSpace(value)
	return len(value) == 10 && value[4] == '-' && value[7] == '-'
}

func ageHoursFromISO(value string) float64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return ageUnknown
	}
	if isDateOnlyValue(value) {
		return ageHoursFromDateOnly(value)
	}
	value = strings.Replace(value, "Z", "+00:00", 1)
	layouts := []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			hours := time.Since(parsed).Hours()
			if hours < 0 {
				return 0
			}
			return hours
		}
	}
	return ageUnknown
}

// ageHoursFromDateOnly treats date-only LinkedIn labels as local midday so
// evening searches in UTC-3 do not drift past the 24h window at UTC midnight.
func ageHoursFromDateOnly(value string) float64 {
	parsed, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(value), time.Local)
	if err != nil {
		return ageUnknown
	}
	parsed = parsed.Add(12 * time.Hour)
	hours := time.Since(parsed).Hours()
	if hours < 0 {
		return 0
	}
	return hours
}

// ageFromLinkedInCard prefers the human-readable relative label ("Há 10 horas")
// over date-only datetime attributes. LinkedIn guest cards often expose only
// YYYY-MM-DD in datetime while the visible text carries the precise age.
func ageFromLinkedInCard(card *goquery.Selection) (float64, string) {
	scope := linkedInCardScope(card)
	timeNode := scope.Find("time").First()
	if timeNode.Length() == 0 {
		timeNode = scope.Find(`[class*="listdate"]`).First()
	}
	if timeNode.Length() == 0 {
		return ageUnknown, ""
	}

	label := cleanText(timeNode.Text())
	rawDT, hasDT := timeNode.Attr("datetime")
	rawDT = strings.TrimSpace(rawDT)

	if label != "" {
		if age := ageHoursFromText(label); age < ageUnknown {
			return age, label
		}
	}

	if hasDT && rawDT != "" {
		if age := ageHoursFromISO(rawDT); age < ageUnknown {
			return age, coalesce(label, rawDT)
		}
	}

	return ageUnknown, label
}

func linkedInCardScope(node *goquery.Selection) *goquery.Selection {
	for _, selector := range []string{".base-search-card", ".job-search-card", "li"} {
		candidate := node.Closest(selector)
		if candidate.Length() == 0 {
			continue
		}
		if candidate.Find(`time, [class*="listdate"]`).Length() > 0 {
			return candidate
		}
	}
	return closestCard(node)
}

// ---------------------------------------------------------------------------
// LinkedIn listing parser (obfuscation-tolerant, anchored on /jobs/view/)
// ---------------------------------------------------------------------------

var linkedInJobIDFromURN = regexp.MustCompile(`jobPosting:(\d+)`)
var digits6Plus = regexp.MustCompile(`(\d{6,})`)

func closestCard(node *goquery.Selection) *goquery.Selection {
	card := node.Closest("li")
	if card.Length() > 0 {
		return card
	}
	card = node.Closest("div")
	if card.Length() > 0 {
		return card
	}
	return node
}

func linkedInJobID(card *goquery.Selection, href string) string {
	urn, _ := card.Attr("data-entity-urn")
	if urn == "" {
		urn, _ = card.Find("[data-entity-urn]").First().Attr("data-entity-urn")
	}
	if match := linkedInJobIDFromURN.FindStringSubmatch(urn); len(match) > 1 {
		return match[1]
	}
	if match := digitsGroup(href); match != "" {
		return match
	}
	return ""
}

func digitsGroup(value string) string {
	if match := digits6Plus.FindStringSubmatch(value); len(match) > 1 {
		return match[1]
	}
	return ""
}

func parseLinkedInJobs(html string, modality string) []jobPost {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil
	}
	var jobs []jobPost
	seen := map[string]bool{}
	doc.Find(`a[href*="/jobs/view/"]`).Each(func(_ int, link *goquery.Selection) {
		href, _ := link.Attr("href")
		href = strings.Split(strings.TrimSpace(href), "?")[0]
		if href == "" {
			return
		}
		card := linkedInCardScope(link)
		jobID := linkedInJobID(card, href)
		key := jobID
		if key == "" {
			key = href
		}
		if seen[key] {
			return
		}
		seen[key] = true

		title := cleanText(firstText(card, `[class*="title"]`, "h3"))
		if title == "" {
			title = cleanText(link.Text())
		}
		company := cleanText(firstText(card,
			`[class*="subtitle"] a`, `[class*="subtitle"]`, "h4", `a[href*="/company/"]`))
		location := cleanText(firstText(card, `[class*="location"]`))
		ageHours, rawDate := ageFromLinkedInCard(card)
		postedLabel := cleanText(card.Find("time").First().Text())
		if title == "" {
			return
		}
		jobs = append(jobs, jobPost{
			ID:         stableJobID("linkedin", href),
			Source:     "LinkedIn",
			Title:      title,
			Company:    company,
			Location:   location,
			URL:        href,
			Status:     "new",
			Modality:   modality,
			AgeHours:   ageHours,
			PostedTime: coalesce(postedLabel, rawDate),
		})
	})
	return jobs
}

// ---------------------------------------------------------------------------
// Indeed listing parser (anchored on data-jk)
// ---------------------------------------------------------------------------

var indeedTitleBadge = regexp.MustCompile(`(?i)^(novo!?|new!?)\s+`)

func cleanIndeedTitle(value string) string {
	value = cleanText(value)
	value = indeedTitleBadge.ReplaceAllString(value, "")
	return strings.TrimSpace(value)
}

func extractIndeedTitle(card *goquery.Selection, node *goquery.Selection) string {
	if span := card.Find(`h2.jobTitle span[title]`).First(); span.Length() > 0 {
		if attr, ok := span.Attr("title"); ok {
			if cleaned := cleanIndeedTitle(attr); cleaned != "" {
				return cleaned
			}
		}
		if cleaned := cleanIndeedTitle(span.Text()); cleaned != "" {
			return cleaned
		}
	}
	if titled := card.Find("[title]").First(); titled.Length() > 0 {
		if attr, ok := titled.Attr("title"); ok {
			if cleaned := cleanIndeedTitle(attr); cleaned != "" {
				return cleaned
			}
		}
	}
	for _, sel := range []string{"h2.jobTitle", "a.jcs-JobTitle span", "a.jcs-JobTitle", `[class*="jobTitle"]`, "h2"} {
		if cleaned := cleanIndeedTitle(firstText(card, sel)); cleaned != "" {
			return cleaned
		}
	}
	return cleanIndeedTitle(node.Text())
}

func extractIndeedSnippet(card *goquery.Selection) string {
	for _, selector := range []string{
		`[data-testid="job-snippet"]`,
		".job-snippet",
		`[class*="job-snippet"]`,
		`[class*="underShelfFooter"]`,
		`[class*="summary"]`,
		`[class*="metadata"]`,
		"ul",
	} {
		text := cleanText(card.Find(selector).First().Text())
		if len(text) >= 40 {
			return truncate(text, 2000)
		}
	}
	return ""
}

func parseIndeedJobs(html string, modality string) []jobPost {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil
	}
	var jobs []jobPost
	seen := map[string]bool{}
	doc.Find("[data-jk]").Each(func(_ int, node *goquery.Selection) {
		jk, _ := node.Attr("data-jk")
		jk = strings.TrimSpace(jk)
		if jk == "" || seen[jk] {
			return
		}
		seen[jk] = true
		card := closestCard(node)
		title := extractIndeedTitle(card, node)
		if title == "" {
			return
		}
		company := cleanText(firstText(card, `[data-testid="company-name"]`, ".companyName"))
		location := cleanText(firstText(card, `[data-testid="text-location"]`, `[class*="companyLocation"]`))
		snippet := extractIndeedSnippet(card)
		dateLabel := cleanText(firstText(card, `[data-testid="myJobsStateDate"]`, `[class*="date"]`, `[data-testid*="Date"]`))
		ageHours := 0.0
		if dateLabel != "" {
			ageHours = ageHoursFromText(dateLabel)
		}
		jobURL := fmt.Sprintf(indeedJobURL, jk)
		jobs = append(jobs, jobPost{
			ID:          stableJobID("indeed", jobURL),
			Source:      "Indeed",
			Title:       title,
			Company:     company,
			Location:    location,
			URL:         jobURL,
			Description: snippet,
			Status:      "new",
			Modality:    modality,
			AgeHours:    ageHours,
			PostedTime:  dateLabel,
		})
	})
	return jobs
}

// ---------------------------------------------------------------------------
// Gupy parsing (XHR-captured records + __NEXT_DATA__ fallback)
// ---------------------------------------------------------------------------

var gupyJobIDFromURL = regexp.MustCompile(`/jobs/(\d+)`)
var gupyNextData = regexp.MustCompile(`(?s)<script[^>]*id="__NEXT_DATA__"[^>]*>(.+?)</script>`)

func gupyField(job map[string]any, keys ...string) string {
	for _, key := range keys {
		if strings.Contains(key, ".") {
			node := any(job)
			ok := true
			for _, part := range strings.Split(key, ".") {
				asMap, isMap := node.(map[string]any)
				if !isMap {
					ok = false
					break
				}
				node = asMap[part]
			}
			if ok {
				if value, isString := node.(string); isString && strings.TrimSpace(value) != "" {
					return strings.TrimSpace(value)
				}
			}
			continue
		}
		if value, isString := job[key].(string); isString && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func gupyRecordsToJobs(records []map[string]any, modality string) []jobPost {
	var jobs []jobPost
	seen := map[string]bool{}
	for _, record := range records {
		title := gupyField(record, "name", "jobName", "title")
		company := gupyField(record, "careerPageName", "companyName", "company.name", "company")
		jobURL := gupyField(record, "careerPageUrl", "customUrl", "jobUrl", "url")
		if title == "" || jobURL == "" || !strings.Contains(jobURL, "gupy.io") {
			continue
		}
		jobID := ""
		if raw, ok := record["id"]; ok {
			jobID = anyToID(raw)
		}
		if jobID == "" {
			if match := gupyJobIDFromURL.FindStringSubmatch(jobURL); len(match) > 1 {
				jobID = match[1]
			}
		}
		if jobID != "" {
			if seen[jobID] {
				continue
			}
			seen[jobID] = true
		}
		published := gupyField(record, "publishedDate", "createdAt", "publishDate")
		city := gupyField(record, "city", "address.city")
		state := gupyField(record, "state", "address.state")
		location := strings.TrimSpace(strings.Join(filterEmpty([]string{city, state}), ", "))
		cleanURL := strings.Split(jobURL, "?")[0]
		jobs = append(jobs, jobPost{
			ID:         stableJobID("gupy", cleanURL),
			Source:     "Gupy",
			Title:      title,
			Company:    company,
			Location:   location,
			URL:        cleanURL,
			Status:     "new",
			Modality:   modality,
			AgeHours:   ageHoursFromISO(published),
			PostedTime: firstTenChars(published),
		})
	}
	return jobs
}

func parseGupyNextData(html string, modality string) []jobPost {
	match := gupyNextData.FindStringSubmatch(html)
	if len(match) < 2 {
		return nil
	}
	var data any
	if err := json.Unmarshal([]byte(match[1]), &data); err != nil {
		return nil
	}
	var records []map[string]any
	seen := map[string]bool{}
	var walk func(node any)
	walk = func(node any) {
		switch value := node.(type) {
		case map[string]any:
			title := firstStringField(value, "name", "jobName", "title")
			jobURL := firstGupyURLField(value)
			if title != "" && jobURL != "" {
				key := jobURL
				if id, ok := value["id"]; ok {
					if idStr := anyToID(id); idStr != "" {
						key = idStr
					}
				}
				if !seen[key] {
					seen[key] = true
					records = append(records, value)
				}
			}
			for _, child := range value {
				walk(child)
			}
		case []any:
			for _, child := range value {
				walk(child)
			}
		}
	}
	walk(data)
	return gupyRecordsToJobs(records, modality)
}

func firstStringField(node map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := node[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstGupyURLField(node map[string]any) string {
	for _, key := range []string{"careerPageUrl", "customUrl", "jobUrl", "url", "link", "permalink"} {
		if value, ok := node[key].(string); ok && strings.Contains(value, "gupy.io") {
			return value
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Description extraction
// ---------------------------------------------------------------------------

var scriptStyleTag = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
var anyHTMLTag = regexp.MustCompile(`(?s)<[^>]+>`)

func extractDescription(html string, selectors ...string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err == nil {
		for _, selector := range selectors {
			text := cleanText(doc.Find(selector).First().Text())
			if len(text) >= 40 {
				return truncate(text, 8000)
			}
		}
		if block := largestTextBlock(doc); len(block) >= 40 {
			return truncate(block, 8000)
		}
	}
	stripped := stripHTML(html)
	if len(stripped) >= 40 {
		return truncate(stripped, 8000)
	}
	return ""
}

func largestTextBlock(doc *goquery.Document) string {
	best := ""
	doc.Find("div, section, article").Each(func(_ int, node *goquery.Selection) {
		text := cleanText(node.Text())
		if len(text) > len(best) {
			best = text
		}
	})
	return best
}

func stripHTML(rawHTML string) string {
	cleaned := scriptStyleTag.ReplaceAllString(rawHTML, " ")
	cleaned = anyHTMLTag.ReplaceAllString(cleaned, " ")
	return cleanText(html.UnescapeString(cleaned))
}

// ---------------------------------------------------------------------------
// small helpers
// ---------------------------------------------------------------------------

// anyToID renders a JSON id (string, or number decoded as float64/json.Number)
// as a plain string without scientific notation.
func anyToID(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

func filterEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, strings.TrimSpace(value))
		}
	}
	return out
}

func firstTenChars(value string) string {
	if len(value) <= 10 {
		return value
	}
	return value[:10]
}
