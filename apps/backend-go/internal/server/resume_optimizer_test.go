package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

const sampleCanonicalJSON = `{"schemaVersion":1,"basics":{"name":"Jane Doe","email":"jane@example.com"},"target":{"jobTitle":"DevOps Engineer"},"summary":"Backend engineer.","skills":{"hard":["AWS"]},"experience":[{"company":"Acme","role":"Engineer","start":"2020-01","end":"present","bullets":["Built things."]}]}`

func geminiJSONResponse(payloadJSON string) string {
	escaped := strings.ReplaceAll(payloadJSON, `"`, `\"`)
	return `{"candidates":[{"content":{"parts":[{"text":"` + escaped + `"}]}}]}`
}

func TestParseResumeToCanonicalWithMockedAI(t *testing.T) {
	bridge := newTestScraperBridge(&captureTransport{respBody: geminiJSONResponse(sampleCanonicalJSON)})
	a := &api{logger: log.New(io.Discard, "", 0), scraper: bridge}

	config := defaultConfig()
	config.Form.Provider = "gemini"

	canonical, _, err := a.parseResumeToCanonical(context.Background(), config, "test-key", "Jane Doe resume text")
	if err != nil {
		t.Fatalf("parseResumeToCanonical: %v", err)
	}
	if canonical.Basics.Name != "Jane Doe" {
		t.Fatalf("unexpected canonical: %+v", canonical)
	}
	if canonical.Experience[0].Company != "Acme" {
		t.Fatalf("expected experience to round-trip, got %+v", canonical.Experience)
	}
}

func TestParseResumeToCanonicalAcceptsStringSchemaVersion(t *testing.T) {
	raw := strings.Replace(sampleCanonicalJSON, `"schemaVersion":1`, `"schemaVersion":"1"`, 1)
	bridge := newTestScraperBridge(&captureTransport{respBody: geminiJSONResponse(raw)})
	a := &api{logger: log.New(io.Discard, "", 0), scraper: bridge}

	config := defaultConfig()
	config.Form.Provider = "gemini"

	canonical, _, err := a.parseResumeToCanonical(context.Background(), config, "test-key", "Jane Doe resume text")
	if err != nil {
		t.Fatalf("parseResumeToCanonical: %v", err)
	}
	if canonical.SchemaVersion != currentResumeSchemaVersion {
		t.Fatalf("expected schema version %d, got %d", currentResumeSchemaVersion, canonical.SchemaVersion)
	}
}

func TestResumeParsePromptSeparatesNameFromLocation(t *testing.T) {
	prompt := resumeParsePrompt("RAFAEL MOREIRA    Belo Horizonte")
	if !strings.Contains(prompt, "Em basics.name, coloque SOMENTE o nome da pessoa") ||
		!strings.Contains(prompt, "cidade, estado e\npaís pertencem a basics.location") {
		t.Fatalf("expected explicit name/location guidance, got:\n%s", prompt)
	}
}

func TestDecodeResumeParseResultRemovesLocationSuffixFromName(t *testing.T) {
	raw := `{"schemaVersion":2,"basics":{"name":"RAFAEL MOREIRA Belo Horizonte","location":"MG"},"experience":[{"company":"Northwind Systems","role":"DevOps","location":"Belo Horizonte, Brasil"}]}`
	canonical, warnings, err := decodeResumeParseResult(raw, "RAFAEL MOREIRA    Belo Horizonte")
	if err != nil {
		t.Fatalf("decodeResumeParseResult: %v", err)
	}
	if canonical.Basics.Name != "RAFAEL MOREIRA" {
		t.Fatalf("expected location suffix to be removed, got %q", canonical.Basics.Name)
	}
	if !slices.Contains(warnings, "name_location_suffix_removed") {
		t.Fatalf("expected location cleanup warning, got %v", warnings)
	}
}

func TestDecodeResumeParseResultKeepsLegitimateName(t *testing.T) {
	raw := `{"schemaVersion":2,"basics":{"name":"Maria de Porto Alegre","location":"RS"},"experience":[{"company":"Acme","role":"Engineer","location":"Porto Alegre, Brasil"}]}`
	canonical, warnings, err := decodeResumeParseResult(raw, "Maria de Porto Alegre")
	if err != nil {
		t.Fatalf("decodeResumeParseResult: %v", err)
	}
	if canonical.Basics.Name != "Maria de Porto Alegre" {
		t.Fatalf("expected legitimate name to be preserved, got %q", canonical.Basics.Name)
	}
	if slices.Contains(warnings, "name_location_suffix_removed") {
		t.Fatalf("unexpected location cleanup warning: %v", warnings)
	}
}

func TestParseResumeToCanonicalRejectsInvalidJSON(t *testing.T) {
	bridge := newTestScraperBridge(&captureTransport{respBody: geminiJSONResponse("not valid json")})
	a := &api{logger: log.New(io.Discard, "", 0), scraper: bridge}

	config := defaultConfig()
	config.Form.Provider = "gemini"

	if _, _, err := a.parseResumeToCanonical(context.Background(), config, "test-key", "text"); err == nil {
		t.Fatal("expected an error for invalid JSON response")
	}
}

func TestParseResumeToCanonicalRejectsMissingName(t *testing.T) {
	bridge := newTestScraperBridge(&captureTransport{respBody: geminiJSONResponse(`{"schemaVersion":1,"basics":{"name":""}}`)})
	a := &api{logger: log.New(io.Discard, "", 0), scraper: bridge}

	config := defaultConfig()
	config.Form.Provider = "gemini"

	_, _, err := a.parseResumeToCanonical(context.Background(), config, "test-key", "text")
	if !errors.Is(err, errMissingName) {
		t.Fatalf("expected errMissingName, got %v", err)
	}
}

func TestGuessNameFromResumeText(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{"simple name", "Maya Chen\nMarketing Designer", "Maya Chen"},
		{"name with pipe segment", "Joao da Silva | Porto Alegre\nDevOps", "Joao da Silva"},
		{"skips blank lines", "\n\n  Alex Morgan\nEngineer", "Alex Morgan"},
		{"rejects email line", "maya.chen@example.com\nMaya Chen", ""},
		{"rejects digits", "11 99999-0000\nMaya Chen", ""},
		{"rejects single word", "Resume\nMaya Chen", ""},
		{"rejects section header", "Curriculum Vitae\nMaya Chen", ""},
		{"rejects long line", strings.Repeat("word ", 15) + "\nMaya", ""},
		{"empty text", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := guessNameFromResumeText(tc.text); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseResumeFallsBackToFirstLineName(t *testing.T) {
	noName := `{"schemaVersion":1,"basics":{"name":""},"summary":"Engineer.","skills":{"hard":["AWS"]}}`
	bridge := newTestScraperBridge(&captureTransport{respBody: geminiJSONResponse(noName)})
	a := &api{logger: log.New(io.Discard, "", 0), scraper: bridge}
	config := defaultConfig()
	config.Form.Provider = "gemini"

	canonical, warnings, err := a.parseResumeToCanonical(context.Background(), config, "test-key",
		"Maya Chen\nMarketing Designer | maya@example.com")
	if err != nil {
		t.Fatalf("parseResumeToCanonical: %v", err)
	}
	if canonical.Basics.Name != "Maya Chen" {
		t.Fatalf("expected fallback name, got %q", canonical.Basics.Name)
	}
	if len(warnings) != 1 || warnings[0] != "name_inferred_from_first_line" {
		t.Fatalf("expected inference warning, got %v", warnings)
	}
}

func TestParseResumeMissingNameIsTypedWhenHeuristicFails(t *testing.T) {
	noName := `{"schemaVersion":1,"basics":{"name":""}}`
	bridge := newTestScraperBridge(&captureTransport{respBody: geminiJSONResponse(noName)})
	a := &api{logger: log.New(io.Discard, "", 0), scraper: bridge}
	config := defaultConfig()
	config.Form.Provider = "gemini"

	// primeira linha tem email -> heurística recusa -> erro tipado missing_name
	_, _, err := a.parseResumeToCanonical(context.Background(), config, "test-key",
		"maya.chen@example.com\nDesigner")
	if !errors.Is(err, errMissingName) {
		t.Fatalf("expected errMissingName, got %v", err)
	}
}

func newResumeParseTestAPI(t *testing.T, respBody string) (*api, *configStore) {
	t.Helper()
	store := newTestStore(t)
	if err := store.save(geminiTestConfig(configForm{Provider: "gemini", APIKey: "test-key"})); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	bridge := newTestScraperBridge(&captureTransport{respBody: respBody})
	bridge.store = store
	return &api{logger: log.New(io.Discard, "", 0), configStore: store, scraper: bridge}, store
}

func TestResumeParseHandlerSavesDocument(t *testing.T) {
	a, store := newResumeParseTestAPI(t, geminiJSONResponse(sampleCanonicalJSON))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/resume/parse", strings.NewReader(`{"text":"Jane Doe resume text"}`))
	rec := httptest.NewRecorder()
	a.resumeParse(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp resumeParseResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.DocumentID == "" {
		t.Fatal("expected a document id")
	}
	if resp.Canonical.Basics.Name != "Jane Doe" {
		t.Fatalf("unexpected canonical in response: %+v", resp.Canonical)
	}
	if resp.ProviderUsed != "gemini" {
		t.Fatalf("expected providerUsed gemini, got %q", resp.ProviderUsed)
	}

	docs, err := store.listResumeDocuments()
	if err != nil {
		t.Fatalf("listResumeDocuments: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 persisted document, got %d", len(docs))
	}
}

func TestResumeParseHandlerPersistsNameWithoutLocation(t *testing.T) {
	raw := `{"schemaVersion":2,"basics":{"name":"RAFAEL MOREIRA Belo Horizonte","location":"MG"},"experience":[{"company":"Northwind Systems","role":"DevOps","location":"Belo Horizonte, Brasil"}]}`
	a, store := newResumeParseTestAPI(t, geminiJSONResponse(raw))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/resume/parse", strings.NewReader(`{"text":"RAFAEL MOREIRA    Belo Horizonte\nProfissional de Tecnologia"}`))
	rec := httptest.NewRecorder()
	a.resumeParse(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var response resumeParseResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	stored, meta, err := store.getResumeDocument(response.DocumentID)
	if err != nil {
		t.Fatalf("getResumeDocument: %v", err)
	}
	if stored.Basics.Name != "RAFAEL MOREIRA" || meta.Name != "RAFAEL MOREIRA" {
		t.Fatalf("location leaked into persisted document: canonical=%q meta=%q", stored.Basics.Name, meta.Name)
	}
}

func TestResumeParseHandlerRequiresAIKey(t *testing.T) {
	store := newTestStore(t)
	a := &api{logger: log.New(io.Discard, "", 0), configStore: store, scraper: newTestScraperBridge(&captureTransport{})}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/resume/parse", strings.NewReader(`{"text":"Jane Doe resume text"}`))
	rec := httptest.NewRecorder()
	a.resumeParse(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 without an AI key, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestResumeParseHandlerRequiresText(t *testing.T) {
	a, _ := newResumeParseTestAPI(t, geminiJSONResponse(sampleCanonicalJSON))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/resume/parse", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	a.resumeParse(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 without resume text, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestResumeParseHandlerFallsBackToConfiguredResumeText(t *testing.T) {
	store := newTestStore(t)
	if err := store.save(geminiTestConfig(configForm{Provider: "gemini", APIKey: "test-key", ResumeText: "Configured resume text"})); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	bridge := newTestScraperBridge(&captureTransport{respBody: geminiJSONResponse(sampleCanonicalJSON)})
	bridge.store = store
	a := &api{logger: log.New(io.Discard, "", 0), configStore: store, scraper: bridge}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/resume/parse", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	a.resumeParse(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 falling back to configured resume text, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestResumeParseHandlerBadGatewayOnAIFailure(t *testing.T) {
	a, _ := newResumeParseTestAPI(t, geminiJSONResponse("not json"))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/resume/parse", strings.NewReader(`{"text":"Jane Doe resume text"}`))
	rec := httptest.NewRecorder()
	a.resumeParse(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 when the AI response is not valid JSON, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func sampleGapBaseResume() CanonicalResume {
	return CanonicalResume{
		Basics:  ResumeBasics{Name: "Jane Doe"},
		Summary: "Cloud engineer focused on AWS automation.",
		Skills:  ResumeSkills{Hard: []string{"AWS", "Docker"}},
		Experience: []ResumeExperience{
			{Company: "Acme", Role: "Engineer", Start: "2020-01", End: "present", Bullets: []string{"Automated AWS deployments with Docker."}},
		},
	}
}

func TestHasRealEvidenceMatchesShortTermWithScatteredTokens(t *testing.T) {
	// A short skill/requirement term ("cloud infrastructure") should count as
	// found when its tokens appear in the resume even if not as an adjacent
	// phrase — this is the over-strictness that was silently downgrading real
	// matches to "confirm".
	base := CanonicalResume{Summary: "Managed production infrastructure running on the cloud platform."}
	if !hasRealEvidence(base, nil, gapItem{Term: "cloud infrastructure"}) {
		t.Fatal("expected a short 2-token term with scattered tokens to be evidenced")
	}
}

func TestHasRealEvidenceRejectsLongSentenceWithScatteredTokens(t *testing.T) {
	// A long requirement sentence must NOT be evidenced just because its
	// individual tokens appear scattered around the resume — that is exactly
	// where false "found" claims would start slipping through, so long terms
	// stay held to the stricter whole-phrase/evidence check.
	base := CanonicalResume{Summary: "Large-scale systems. Led migrations. The team shipped. Engineers collaborated."}
	item := gapItem{Term: "led a large team of engineers building the platform"}
	if hasRealEvidence(base, nil, item) {
		t.Fatal("expected a long requirement sentence with only scattered tokens NOT to be evidenced")
	}
}

func TestGapAnalysisKeepsFoundWithRealEvidence(t *testing.T) {
	aiResponse := `{"found":[{"term":"AWS","evidence":"Automated AWS deployments"}],"partial":[],"missing":[],"toConfirm":[]}`
	bridge := newTestScraperBridge(&captureTransport{respBody: geminiJSONResponse(aiResponse)})
	a := &api{logger: log.New(io.Discard, "", 0), scraper: bridge}

	config := defaultConfig()
	config.Form.Provider = "gemini"

	gap, err := a.gapAnalysis(context.Background(), config, "test-key", sampleGapBaseResume(), jobRequirements{HardRequirements: []string{"AWS"}})
	if err != nil {
		t.Fatalf("gapAnalysis: %v", err)
	}
	if len(gap.Found) != 1 || gap.Found[0].Term != "AWS" {
		t.Fatalf("expected AWS to stay found (real evidence), got %+v", gap)
	}
	if len(gap.ToConfirm) != 0 {
		t.Fatalf("expected no downgrades for a well-evidenced claim, got %+v", gap.ToConfirm)
	}
}

func TestGapEvidenceAcceptsAndFlattensStringOrArray(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "array",
			raw:  `{"found":[{"term":"AWS","evidence":["Automated AWS deployments","AWS production"]}],"partial":[],"missing":[],"toConfirm":[]}`,
		},
		{
			name: "stringified array",
			raw:  `{"found":[{"term":"AWS","evidence":"[\"Automated AWS deployments\",\"AWS production\"]"}],"partial":[],"missing":[],"toConfirm":[]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gap, err := decodeGapAnalysisResult(sampleGapBaseResume(), tt.raw)
			if err != nil {
				t.Fatalf("decodeGapAnalysisResult: %v", err)
			}
			if len(gap.Found) != 1 || string(gap.Found[0].Evidence) != "Automated AWS deployments · AWS production" {
				t.Fatalf("evidence was not flattened: %+v", gap)
			}
			encoded, err := json.Marshal(gap)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), `"evidence":[`) {
				t.Fatalf("persisted gap evidence must be a clean string: %s", encoded)
			}
		})
	}
}

func TestGapAnalysisDowngradesUnsupportedFoundClaim(t *testing.T) {
	// The AI claims "Kubernetes" is found, but neither the term nor the
	// fabricated evidence actually appear in the base resume — the
	// deterministic gate must catch this and downgrade it.
	aiResponse := `{"found":[{"term":"Kubernetes","evidence":"Managed Kubernetes clusters in production"}],"partial":[],"missing":[],"toConfirm":[]}`
	bridge := newTestScraperBridge(&captureTransport{respBody: geminiJSONResponse(aiResponse)})
	a := &api{logger: log.New(io.Discard, "", 0), scraper: bridge}

	config := defaultConfig()
	config.Form.Provider = "gemini"

	gap, err := a.gapAnalysis(context.Background(), config, "test-key", sampleGapBaseResume(), jobRequirements{HardRequirements: []string{"Kubernetes"}})
	if err != nil {
		t.Fatalf("gapAnalysis: %v", err)
	}
	if len(gap.Found) != 0 {
		t.Fatalf("expected the unsupported claim to be removed from found, got %+v", gap.Found)
	}
	if len(gap.ToConfirm) != 1 || gap.ToConfirm[0].Term != "Kubernetes" {
		t.Fatalf("expected Kubernetes downgraded to toConfirm, got %+v", gap.ToConfirm)
	}
}

func TestGapAnalysisDowngradesUnsupportedPartialClaim(t *testing.T) {
	aiResponse := `{"found":[],"partial":[{"term":"Terraform","evidence":"Terraform IaC pipelines"}],"missing":[],"toConfirm":[]}`
	bridge := newTestScraperBridge(&captureTransport{respBody: geminiJSONResponse(aiResponse)})
	a := &api{logger: log.New(io.Discard, "", 0), scraper: bridge}

	config := defaultConfig()
	config.Form.Provider = "gemini"

	gap, err := a.gapAnalysis(context.Background(), config, "test-key", sampleGapBaseResume(), jobRequirements{HardRequirements: []string{"Terraform"}})
	if err != nil {
		t.Fatalf("gapAnalysis: %v", err)
	}
	if len(gap.Partial) != 0 {
		t.Fatalf("expected unsupported partial claim removed, got %+v", gap.Partial)
	}
	if len(gap.ToConfirm) != 1 || gap.ToConfirm[0].Term != "Terraform" {
		t.Fatalf("expected Terraform downgraded to toConfirm, got %+v", gap.ToConfirm)
	}
}

func TestGapAnalysisPassesThroughMissing(t *testing.T) {
	aiResponse := `{"found":[],"partial":[],"missing":[{"term":"Kubernetes","evidence":""}],"toConfirm":[]}`
	bridge := newTestScraperBridge(&captureTransport{respBody: geminiJSONResponse(aiResponse)})
	a := &api{logger: log.New(io.Discard, "", 0), scraper: bridge}

	config := defaultConfig()
	config.Form.Provider = "gemini"

	gap, err := a.gapAnalysis(context.Background(), config, "test-key", sampleGapBaseResume(), jobRequirements{HardRequirements: []string{"Kubernetes"}})
	if err != nil {
		t.Fatalf("gapAnalysis: %v", err)
	}
	if len(gap.Missing) != 1 || gap.Missing[0].Term != "Kubernetes" {
		t.Fatalf("expected Kubernetes to remain missing, got %+v", gap)
	}
}

func TestGapAnalysisRestoresHardRequirementOmittedByAI(t *testing.T) {
	// Installed-app E2E-12: analyze-job extracted this certification as a hard
	// requirement, but the gap model omitted it from every bucket. A required
	// term must never disappear from the user-facing gap; without a model
	// classification, the only safe deterministic fallback is Missing.
	aiResponse := `{"found":[{"term":"AWS","evidence":"AWS production"}],"partial":[],"missing":[],"toConfirm":[]}`
	bridge := newTestScraperBridge(&captureTransport{respBody: geminiJSONResponse(aiResponse)})
	a := &api{logger: log.New(io.Discard, "", 0), scraper: bridge}

	config := defaultConfig()
	config.Form.Provider = "gemini"

	const requirement = "AWS Certified Solutions Architect certification"
	gap, err := a.gapAnalysis(context.Background(), config, "test-key", sampleGapBaseResume(), jobRequirements{
		HardRequirements: []string{"AWS", requirement},
	})
	if err != nil {
		t.Fatalf("gapAnalysis: %v", err)
	}
	if len(gap.Missing) != 1 || gap.Missing[0].Term != requirement {
		t.Fatalf("expected generic AWS not to cover the omitted certification, got %+v", gap)
	}
}

func TestGapRequirementEquivalentIsConservative(t *testing.T) {
	tests := []struct {
		name        string
		left, right string
		want        bool
	}{
		{name: "qualified CFA", left: "active CFA charter", right: "CFA", want: true},
		{name: "qualified Python", left: "Python automation", right: "Python", want: true},
		{name: "credential acronym", left: "Certified Emergency Nurse (CEN)", right: "CEN", want: true},
		{name: "singular plural", left: "design systems", right: "design system", want: true},
		{name: "generic cloud is not certification", left: "AWS Certified Solutions Architect certification", right: "AWS", want: false},
		{name: "state license is not generic RN", left: "active California RN license", right: "RN", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := gapRequirementEquivalent(tt.left, tt.right); got != tt.want {
				t.Fatalf("gapRequirementEquivalent(%q, %q) = %v, want %v", tt.left, tt.right, got, tt.want)
			}
		})
	}
}

func TestGapEvidenceGateDeduplicatesConflictingBuckets(t *testing.T) {
	// Installed-app E2E: Finance rendered SQL simultaneously as "Confirm if
	// true" and "Blocked". A requirement must have one operational outcome;
	// when the model contradicts itself, Missing is the safer result because it
	// cannot be silently confirmed from evidence that is not on the resume.
	gap := gapResult{
		Missing:   []gapItem{{Term: "SQL", Evidence: "Not present in resume"}},
		ToConfirm: []gapItem{{Term: "SQL", Evidence: "Confirm SQL experience"}},
	}

	got := enforceGapEvidenceGate(sampleGapBaseResume(), gap)
	if len(got.Missing) != 1 || got.Missing[0].Term != "SQL" {
		t.Fatalf("expected SQL to remain blocked once, got missing=%+v", got.Missing)
	}
	if len(got.ToConfirm) != 0 {
		t.Fatalf("expected the conflicting SQL confirmation removed, got %+v", got.ToConfirm)
	}
}

func TestGapEvidenceGateDeduplicatesSemanticallyEquivalentBuckets(t *testing.T) {
	// The first exact-term fix removed SQL/SQL, but the installed rerun still
	// rendered CFA vs active CFA charter and Python vs Python automation in
	// conflicting buckets. These are the same underlying requirements with
	// model-added qualifiers, so the conservative Missing classification wins.
	gap := gapResult{
		Missing: []gapItem{
			{Term: "active CFA charter", Evidence: "Not present in resume"},
			{Term: "Python automation", Evidence: "Not present in resume"},
		},
		ToConfirm: []gapItem{
			{Term: "CFA", Evidence: "Confirm if true"},
			{Term: "Python", Evidence: "Confirm if true"},
		},
	}

	got := enforceGapEvidenceGate(sampleGapBaseResume(), gap)
	if len(got.Missing) != 2 {
		t.Fatalf("expected both blocked requirements preserved, got %+v", got.Missing)
	}
	if len(got.ToConfirm) != 0 {
		t.Fatalf("expected equivalent confirmation cards removed, got %+v", got.ToConfirm)
	}
}

func TestGapAnalysisTreatsPersistedConfirmedSkillAsFound(t *testing.T) {
	// Once a user has confirmed having a skill (persisted on the resume as
	// ConfirmedSkills), a later gap analysis must treat it as found rather
	// than asking them to confirm the same thing again — confirmations are
	// reused going forward, per the interview-assistant contract.
	aiResponse := `{"found":[{"term":"Kubernetes","evidence":"Confirmed by the candidate"}],"partial":[],"missing":[],"toConfirm":[]}`
	bridge := newTestScraperBridge(&captureTransport{respBody: geminiJSONResponse(aiResponse)})
	a := &api{logger: log.New(io.Discard, "", 0), scraper: bridge}

	config := defaultConfig()
	config.Form.Provider = "gemini"

	base := sampleGapBaseResume()
	base.ConfirmedSkills = []string{"Kubernetes"} // not present in the resume text itself
	gap, err := a.gapAnalysis(context.Background(), config, "test-key", base, jobRequirements{HardRequirements: []string{"Kubernetes"}})
	if err != nil {
		t.Fatalf("gapAnalysis: %v", err)
	}
	if len(gap.Found) != 1 || gap.Found[0].Term != "Kubernetes" {
		t.Fatalf("expected a persisted confirmed skill to stay found, got %+v", gap)
	}
	if len(gap.ToConfirm) != 0 {
		t.Fatalf("expected no re-confirmation for an already-confirmed skill, got %+v", gap.ToConfirm)
	}
}

func TestGapAnalysisNeverEmitsNullArraysToJSON(t *testing.T) {
	// The AI can omit a bucket entirely or return JSON null for it when it has
	// nothing to report for that category. This must never surface as JSON
	// `null` on the wire: the frontend (JobGapAnalysis.tsx) spreads these
	// arrays without a null guard, and a `null` there throws, which blanks
	// the entire app via the root ErrorBoundary — not just the gap panel.
	aiResponse := `{"found":null,"partial":null,"missing":null,"toConfirm":null}`
	bridge := newTestScraperBridge(&captureTransport{respBody: geminiJSONResponse(aiResponse)})
	a := &api{logger: log.New(io.Discard, "", 0), scraper: bridge}

	config := defaultConfig()
	config.Form.Provider = "gemini"

	gap, err := a.gapAnalysis(context.Background(), config, "test-key", sampleGapBaseResume(), jobRequirements{HardRequirements: []string{"Kubernetes"}})
	if err != nil {
		t.Fatalf("gapAnalysis: %v", err)
	}

	encoded, err := json.Marshal(gap)
	if err != nil {
		t.Fatalf("marshal gap: %v", err)
	}
	for _, wantAbsent := range []string{`"found":null`, `"partial":null`, `"missing":null`, `"toConfirm":null`} {
		if strings.Contains(string(encoded), wantAbsent) {
			t.Fatalf("expected no null array in gap JSON, got %s", encoded)
		}
	}
	for _, wantPresent := range []string{`"found":[]`, `"partial":[]`, `"missing":[{"term":"Kubernetes"`, `"toConfirm":[]`} {
		if !strings.Contains(string(encoded), wantPresent) {
			t.Fatalf("expected non-null bucket and hard-requirement coverage for %s, got %s", wantPresent, encoded)
		}
	}
}

func TestResumeGapHandlerWithMockedAI(t *testing.T) {
	store := newTestStore(t)
	if err := store.save(geminiTestConfig(configForm{Provider: "gemini", APIKey: "test-key"})); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	aiResponse := `{"found":[{"term":"AWS","evidence":"Automated AWS deployments"}],"partial":[],"missing":[],"toConfirm":[]}`
	bridge := newTestScraperBridge(&captureTransport{respBody: geminiJSONResponse(aiResponse)})
	bridge.store = store
	a := &api{logger: log.New(io.Discard, "", 0), configStore: store, scraper: bridge}

	body, _ := json.Marshal(gapRequest{Canonical: sampleGapBaseResume(), Requirements: jobRequirements{HardRequirements: []string{"AWS"}}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/resume/gap", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	a.resumeGap(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp gapResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Gap.Found) != 1 {
		t.Fatalf("unexpected gap response: %+v", resp.Gap)
	}
}

func TestResumeGapHandlerRequiresAIKey(t *testing.T) {
	store := newTestStore(t)
	a := &api{logger: log.New(io.Discard, "", 0), configStore: store, scraper: newTestScraperBridge(&captureTransport{})}

	body, _ := json.Marshal(gapRequest{Canonical: sampleGapBaseResume()})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/resume/gap", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	a.resumeGap(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 without an AI key, got %d body=%s", rec.Code, rec.Body.String())
	}
}
