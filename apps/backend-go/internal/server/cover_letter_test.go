package server

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func newCoverLetterTestAPI(t *testing.T, respBody string) *api {
	t.Helper()
	store := newTestStore(t)
	if err := store.save(geminiTestConfig(configForm{Provider: "gemini", APIKey: "test-key"})); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	bridge := newTestScraperBridge(&captureTransport{respBody: respBody})
	bridge.store = store
	return &api{logger: log.New(io.Discard, "", 0), configStore: store, scraper: bridge}
}

// coverLetterDrafts replays one canned AI draft per provider call, so a test
// can drive the cover-letter retry ladder (first draft invents, second draft
// is clean) without hitting the network.
func coverLetterDrafts(markdowns ...string) *sequenceTransport {
	responses := make([]sequenceResponse, 0, len(markdowns))
	for _, markdown := range markdowns {
		responses = append(responses, sequenceResponse{status: 200, body: geminiJSONResponse(coverLetterJSON(markdown))})
	}
	return &sequenceTransport{responses: responses}
}

func newCoverLetterTestAPIWithTransport(t *testing.T, transport http.RoundTripper) *api {
	t.Helper()
	store := newTestStore(t)
	if err := store.save(geminiTestConfig(configForm{Provider: "gemini", APIKey: "test-key"})); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	bridge := newTestScraperBridge(transport)
	bridge.store = store
	return &api{logger: log.New(io.Discard, "", 0), configStore: store, scraper: bridge}
}

func coverLetterJSON(markdown string) string {
	payload, _ := json.Marshal(map[string]string{"markdown": markdown})
	return string(payload)
}

func TestCoverLetterUnverifiedTermsExcludesEvidencedAndConfirmed(t *testing.T) {
	// Only gap terms the resume cannot back — and that the user has not
	// explicitly confirmed — may be fed to the prompt as forbidden claims.
	gap := &gapResult{
		Missing:   []gapItem{{Term: "mobile-first"}},
		ToConfirm: []gapItem{{Term: "quantitative research"}, {Term: "Kubernetes"}},
		Found:     []gapItem{{Term: "AWS"}},
	}
	terms := coverLetterUnverifiedTerms(sampleTailoringBaseResume(), gap, []string{"Kubernetes"})

	if !slices.Contains(terms, "mobile-first") {
		t.Fatalf("an unevidenced missing term must be forbidden, got %+v", terms)
	}
	if !slices.Contains(terms, "quantitative research") {
		t.Fatalf("an unconfirmed toConfirm term must be forbidden, got %+v", terms)
	}
	if slices.Contains(terms, "Kubernetes") {
		t.Fatalf("an explicitly confirmed term must not be forbidden, got %+v", terms)
	}
	if slices.Contains(terms, "AWS") {
		t.Fatalf("a term already evidenced in the resume must not be forbidden, got %+v", terms)
	}
}

func TestCoverLetterPromptListsForbiddenTerms(t *testing.T) {
	prompt := coverLetterPrompt(coverLetterPromptInput{
		CanonicalJSON:  "{}",
		JobDescription: "Design fintech apps.",
		Language:       "English",
		Tone:           "professional",
		MaxWords:       220,
		Forbidden:      []string{"mobile-first"},
	})
	if !strings.Contains(prompt, "mobile-first") {
		t.Fatalf("the prompt must name the forbidden terms so the model can avoid them, got:\n%s", prompt)
	}
}

func TestCoverLetterWarningsCatchScatteredTokenClaim(t *testing.T) {
	// The old gate only matched the gap term verbatim, so a letter could claim
	// "WCAG-compliant accessibility audits" while the unverified term was
	// "accessibility (WCAG)" and slip through untouched.
	gap := &gapResult{ToConfirm: []gapItem{{Term: "accessibility (WCAG)"}}}
	warnings := coverLetterWarnings("I ran WCAG-compliant accessibility audits.", gap, CanonicalResume{}, nil)
	if len(warnings) != 1 {
		t.Fatalf("expected the reworded WCAG claim to be flagged, got %+v", warnings)
	}
}

func TestCoverLetterWarningsIgnoreOnlyTheEvidencedHalfOfATerm(t *testing.T) {
	// The counterpart: mentioning only "accessibility" (which the resume has)
	// is not a claim about WCAG and must stay silent.
	base := CanonicalResume{Skills: ResumeSkills{Hard: []string{"accessibility"}}}
	gap := &gapResult{ToConfirm: []gapItem{{Term: "accessibility (WCAG)"}}}
	warnings := coverLetterWarnings("I care about accessibility in every flow.", gap, base, nil)
	if len(warnings) != 0 {
		t.Fatalf("mentioning only the evidenced half of a term must not warn, got %+v", warnings)
	}
}

func TestResumeCoverLetterHandlerRetriesWhenFirstDraftInvents(t *testing.T) {
	transport := coverLetterDrafts(
		"I build mobile-first products end to end.",
		"I build products end to end, backed by AWS work at Acme.",
	)
	a := newCoverLetterTestAPIWithTransport(t, transport)

	body, _ := json.Marshal(coverLetterRequest{
		Canonical:      sampleTailoringBaseResume(),
		JobDescription: "Mobile-first product design.",
		Gap:            &gapResult{ToConfirm: []gapItem{{Term: "mobile-first"}}},
	})
	req := httptest.NewRequest("POST", "/api/v1/resume/cover-letter", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	a.resumeCoverLetter(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp coverLetterResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(transport.requests) != 2 {
		t.Fatalf("an invented first draft must trigger exactly one retry, got %d provider calls", len(transport.requests))
	}
	if strings.Contains(strings.ToLower(resp.Markdown), "mobile-first") {
		t.Fatalf("the clean retry must be the letter returned, got %q", resp.Markdown)
	}
	if len(resp.Warnings) != 0 {
		t.Fatalf("expected no warnings after a clean retry, got %+v", resp.Warnings)
	}
	if resp.RequiresConfirmation {
		t.Fatal("a clean retry must not require confirmation")
	}
}

func TestResumeCoverLetterHandlerRequiresConfirmationWhenRetryStillInvents(t *testing.T) {
	const invented = "I build mobile-first products end to end."
	a := newCoverLetterTestAPIWithTransport(t, coverLetterDrafts(invented, invented))

	body, _ := json.Marshal(coverLetterRequest{
		Canonical:      sampleTailoringBaseResume(),
		JobDescription: "Mobile-first product design.",
		Gap:            &gapResult{ToConfirm: []gapItem{{Term: "mobile-first"}}},
	})
	req := httptest.NewRequest("POST", "/api/v1/resume/cover-letter", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	a.resumeCoverLetter(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp coverLetterResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.RequiresConfirmation {
		t.Fatalf("a letter that still invents after the retry must be gated behind an explicit confirmation, got %+v", resp)
	}
	if len(resp.Warnings) == 0 {
		t.Fatal("expected the unsupported claim to be reported alongside the confirmation gate")
	}
}

func TestResumeCoverLetterHandlerDoesNotPersistAnUnconfirmedLetter(t *testing.T) {
	const invented = "I build mobile-first products end to end."
	a := newCoverLetterTestAPIWithTransport(t, coverLetterDrafts(invented, invented))

	jobID := "job:1"
	if _, _, err := a.configStore.applyJobAction("dismiss", jobSummary{ID: jobID, Title: "Product Designer", Company: "Acme"}); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	seedJobDescription(t, a.configStore, jobID, "Mobile-first product design.")

	body, _ := json.Marshal(coverLetterRequest{
		Canonical: sampleTailoringBaseResume(),
		JobID:     jobID,
		Gap:       &gapResult{ToConfirm: []gapItem{{Term: "mobile-first"}}},
	})
	req := httptest.NewRequest("POST", "/api/v1/resume/cover-letter", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	a.resumeCoverLetter(rec, req)

	var resp coverLetterResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ID != "" {
		t.Fatalf("a letter still carrying unsupported claims must not be persisted, got id=%q", resp.ID)
	}
}

func TestResolveCoverLetterMaxWordsClampsRange(t *testing.T) {
	// The request value is injected verbatim into the AI prompt, so it needs
	// an upper bound too — not just the <=0 default.
	cases := []struct{ in, want int }{
		{0, defaultCoverLetterMaxWords},
		{-5, defaultCoverLetterMaxWords},
		{220, 220},
		{80, 80},
		{100000, maxCoverLetterMaxWords},
	}
	for _, c := range cases {
		if got := resolveCoverLetterMaxWords(c.in); got != c.want {
			t.Fatalf("resolveCoverLetterMaxWords(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestResumeCoverLetterHandlerGeneratesFromCanonical(t *testing.T) {
	letter := "Dear Hiring Manager, I'm excited to apply for this **backend** role, bringing hands-on AWS experience from Acme. Best, Jane Doe"
	a := newCoverLetterTestAPI(t, geminiJSONResponse(coverLetterJSON(letter)))

	body, _ := json.Marshal(coverLetterRequest{
		Canonical:      sampleTailoringBaseResume(),
		JobDescription: "We need a backend engineer with AWS experience.",
	})
	req := httptest.NewRequest("POST", "/api/v1/resume/cover-letter", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	a.resumeCoverLetter(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp coverLetterResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Markdown == "" {
		t.Fatal("expected non-empty markdown")
	}
	if strings.Contains(resp.PlainText, "**") || strings.Contains(resp.PlainText, "#") {
		t.Fatalf("expected plainText to strip markdown markers, got %q", resp.PlainText)
	}
	if resp.ID != "" {
		t.Fatalf("expected no persistence without a jobId, got id=%q", resp.ID)
	}
	if resp.ProviderUsed == "" {
		t.Fatal("expected providerUsed to be set")
	}
	if len(resp.Warnings) != 0 {
		t.Fatalf("expected no warnings without a gap, got %+v", resp.Warnings)
	}
}

func TestResumeCoverLetterHandlerFlagsSkillNotInResume(t *testing.T) {
	letter := "Dear Hiring Manager, my deep expertise in Kubernetes makes me a strong fit. Best, Jane Doe"
	a := newCoverLetterTestAPI(t, geminiJSONResponse(coverLetterJSON(letter)))

	body, _ := json.Marshal(coverLetterRequest{
		Canonical:      sampleTailoringBaseResume(),
		JobDescription: "We need a backend engineer with Kubernetes experience.",
		Gap:            &gapResult{Missing: []gapItem{{Term: "Kubernetes"}}},
	})
	req := httptest.NewRequest("POST", "/api/v1/resume/cover-letter", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	a.resumeCoverLetter(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp coverLetterResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range resp.Warnings {
		if w == "mentions_skill_not_in_resume: kubernetes" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a mentions_skill_not_in_resume warning for kubernetes, got %+v", resp.Warnings)
	}
}

func TestResumeCoverLetterHandlerDoesNotWarnForCanonicalRNLicense(t *testing.T) {
	letter := "I maintain an active RN license and bring nine years of acute-care experience."
	a := newCoverLetterTestAPI(t, geminiJSONResponse(coverLetterJSON(letter)))

	body, _ := json.Marshal(coverLetterRequest{
		Canonical:      licensedNurseResume(),
		JobDescription: "Active RN license required.",
		Gap:            &gapResult{Missing: []gapItem{{Term: "active RN license"}}},
	})
	req := httptest.NewRequest("POST", "/api/v1/resume/cover-letter", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	a.resumeCoverLetter(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp coverLetterResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Warnings) != 0 {
		t.Fatalf("a canonical RN license must not be reported as absent, got %+v", resp.Warnings)
	}
}

func TestCoverLetterWarningsHonorExplicitConfirmation(t *testing.T) {
	gap := &gapResult{ToConfirm: []gapItem{{Term: "active compact RN license"}}}
	warnings := coverLetterWarnings(
		"I hold an active compact RN license.",
		gap,
		CanonicalResume{},
		[]string{"compact RN license"},
	)
	if len(warnings) != 0 {
		t.Fatalf("an explicit confirmation must be shared with the cover-letter gate, got %+v", warnings)
	}
}

func TestResumeCoverLetterHandlerPersistsWhenJobIDPresent(t *testing.T) {
	letter := "Dear Hiring Manager, I'm excited to apply, bringing hands-on AWS experience. Best, Jane Doe"
	a := newCoverLetterTestAPI(t, geminiJSONResponse(coverLetterJSON(letter)))

	jobID := "job:1"
	if _, _, err := a.configStore.applyJobAction("dismiss", jobSummary{ID: jobID, Title: "Backend Engineer", Company: "Acme"}); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	seedJobDescription(t, a.configStore, jobID, "We need a backend engineer with AWS experience.")

	body, _ := json.Marshal(coverLetterRequest{Canonical: sampleTailoringBaseResume(), JobID: jobID})
	req := httptest.NewRequest("POST", "/api/v1/resume/cover-letter", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	a.resumeCoverLetter(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp coverLetterResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ID == "" {
		t.Fatal("expected the letter to be persisted (non-empty id) when jobId is present")
	}

	db, err := a.configStore.open()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM cover_letters WHERE id = ? AND job_id = ?`, resp.ID, jobID).Scan(&count); err != nil {
		t.Fatalf("count cover_letters: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 persisted cover_letters row, got %d", count)
	}
}

func TestResumeCoverLetterHandlerRequiresJobDescription(t *testing.T) {
	a := newCoverLetterTestAPI(t, geminiJSONResponse(coverLetterJSON("irrelevant")))

	body, _ := json.Marshal(coverLetterRequest{Canonical: sampleTailoringBaseResume()})
	req := httptest.NewRequest("POST", "/api/v1/resume/cover-letter", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	a.resumeCoverLetter(rec, req)

	if rec.Code != 400 {
		t.Fatalf("expected 400 without a job description or jobId, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestResumeCoverLetterHandlerRequiresAIKey(t *testing.T) {
	store := newTestStore(t)
	a := &api{logger: log.New(io.Discard, "", 0), configStore: store, scraper: newTestScraperBridge(&captureTransport{})}

	body, _ := json.Marshal(coverLetterRequest{Canonical: sampleTailoringBaseResume(), JobDescription: "AWS backend role."})
	req := httptest.NewRequest("POST", "/api/v1/resume/cover-letter", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	a.resumeCoverLetter(rec, req)

	if rec.Code != 409 {
		t.Fatalf("expected 409 without an AI key, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestResumeCoverLetterRouteNoLongerAnswers501(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/resume/cover-letter", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotImplemented {
		t.Fatalf("cover-letter must no longer be a 501 stub, got %d", resp.StatusCode)
	}
}
