package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResumeSearchableTextIncludesLanguagesHeadlineAndTarget(t *testing.T) {
	// Language/headline/target requirements (e.g. "fluent in Spanish", a
	// headline role match) could never pass the gap-analysis evidence gate
	// because this corpus excluded them — a real anti-invention over-strictness
	// bug, not a safety feature.
	r := CanonicalResume{
		Basics:    ResumeBasics{Name: "Jane Doe", Headline: "Staff Reliability Engineer"},
		Target:    ResumeTarget{JobTitle: "Platform Engineer", Category: "devops", Seniority: "Senior"},
		Summary:   "Backend engineer.",
		Languages: []ResumeLanguage{{Language: "Spanish", Fluency: "Fluent"}},
	}
	text := normalizeText(resumeSearchableText(r))
	for _, want := range []string{"spanish", "staff reliability engineer", "platform engineer"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected searchable text to contain %q, got %q", want, text)
		}
	}
}

func resumeWithBullets(bullets ...string) CanonicalResume {
	return CanonicalResume{
		Basics:  ResumeBasics{Name: "Jane Doe", Email: "jane@example.com"},
		Summary: "Backend engineer.",
		Skills:  ResumeSkills{Hard: []string{"AWS", "Terraform", "Kubernetes"}},
		Experience: []ResumeExperience{
			{Company: "Acme", Role: "Engineer", Start: "2020-01", End: "present", Bullets: bullets},
		},
		Education: []ResumeEducation{{Institution: "State University", Degree: "BSc"}},
	}
}

// "We could not measure this" and "you scored zero" are different sentences, and
// the panel was printing the second when it meant the first. Impact needs bullets to
// judge, and a resume has none until it has been parsed — so a user who had just
// uploaded their resume was met with a red bar reading 0 under "Impact". Nothing had
// judged it badly. Nothing had judged it. Measured on one real PDF: 0 before parsing,
// 38 after.
//
// The function already knew — it withholds the no_metrics warning in exactly this
// case — and threw the knowledge away by returning a number like any other.
func TestImpactIsReportedUnmeasurableWhenThereAreNoBulletsToJudge(t *testing.T) {
	// A resume with no experience section at all: what the offline canonical looks
	// like before the AI has structured anything.
	bare := CanonicalResume{Basics: ResumeBasics{Name: "Jane Doe", Email: "jane@example.com"}}

	scores, issues := diagnoseResume(bare, "Jane Doe\njane@example.com\nSome unstructured resume text.")
	if scores.ImpactMeasured {
		t.Fatal("with no bullets there is nothing to judge; impact must not be presented as a score")
	}
	if hasIssue(issues, "no_metrics") {
		t.Fatal("a resume we have not looked at must not be warned about its metrics")
	}

	// And the moment there ARE bullets, it is a real score again.
	scored, _ := diagnoseResume(resumeWithBullets("Reduced deployment time by 40% using CI/CD."), "")
	if !scored.ImpactMeasured {
		t.Fatal("bullets exist, so impact is measurable and must be reported as a score")
	}
	if scored.Impact == 0 {
		t.Fatalf("expected a real impact score for a metric-bearing bullet, got %d", scored.Impact)
	}
}

func TestDiagnoseResumeNoMetricsIssue(t *testing.T) {
	r := resumeWithBullets("Responsible for backend systems.", "Worked with the team on projects.")
	scores, issues := diagnoseResume(r, "")
	if scores.Impact >= 50 {
		t.Fatalf("expected low impact score without metrics, got %d", scores.Impact)
	}
	if !hasIssue(issues, "no_metrics") {
		t.Fatalf("expected no_metrics issue, got %+v", issues)
	}
}

func TestDiagnoseResumeHighImpactWithMetrics(t *testing.T) {
	r := resumeWithBullets(
		"Reduced deployment time by 40% using CI/CD automation.",
		"Managed a team of 5 engineers across 3 projects.",
		"Increased test coverage to 92%.",
	)
	scores, issues := diagnoseResume(r, "")
	if scores.Impact < 80 {
		t.Fatalf("expected high impact score with metrics-heavy bullets, got %d", scores.Impact)
	}
	if hasIssue(issues, "no_metrics") {
		t.Fatalf("did not expect no_metrics issue, got %+v", issues)
	}
}

func TestDiagnoseResumeKeywordsScoreFromSkillDictionary(t *testing.T) {
	rich := resumeWithBullets("Led migration to Kubernetes and Terraform on AWS with CI/CD.")
	rich.Summary = "Cloud engineer specializing in AWS, Terraform, Kubernetes, Docker, Linux, Grafana, Prometheus, CI/CD, GitHub Actions."

	// An almost-empty resume has no searchable text at all (resumeSearchableText
	// skips basics.name), so it must score far lower than a keyword-rich one.
	poor := CanonicalResume{Basics: ResumeBasics{Name: "Jane Doe"}}

	richScores, _ := diagnoseResume(rich, "")
	poorScores, _ := diagnoseResume(poor, "")
	if richScores.Keywords <= poorScores.Keywords {
		t.Fatalf("expected keyword-rich resume to score higher, rich=%d poor=%d", richScores.Keywords, poorScores.Keywords)
	}
}

func TestDiagnoseResumeContentPenalizesMissingSections(t *testing.T) {
	full := resumeWithBullets("Built things.")
	scores, _ := diagnoseResume(full, "")
	if scores.Content != 100 {
		t.Fatalf("expected full content score 100, got %d", scores.Content)
	}

	sparse := CanonicalResume{Basics: ResumeBasics{Name: "Jane Doe"}}
	sparseScores, issues := diagnoseResume(sparse, "")
	if sparseScores.Content >= scores.Content {
		t.Fatalf("expected sparse resume to score lower content, got %d", sparseScores.Content)
	}
	if !hasIssue(issues, "no_summary") {
		t.Fatalf("expected no_summary issue, got %+v", issues)
	}
}

func TestAnySubScoreBelow100ProducesAnIssue(t *testing.T) {
	base := resumeWithBullets("Built a platform used by 20 teams.")
	tests := []struct {
		name string
		edit func(*CanonicalResume)
		code string
	}{
		{name: "summary", edit: func(r *CanonicalResume) { r.Summary = "" }, code: "no_summary"},
		{name: "experience", edit: func(r *CanonicalResume) { r.Experience = nil }, code: "no_experience"},
		{name: "education", edit: func(r *CanonicalResume) { r.Education = nil }, code: "no_education"},
		{name: "skills", edit: func(r *CanonicalResume) { r.Skills = ResumeSkills{} }, code: "few_skills"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := base
			tt.edit(&candidate)
			scores, issues := diagnoseResume(candidate, "")
			if scores.Content >= 100 {
				t.Fatalf("expected a content deduction for missing %s", tt.name)
			}
			if !hasIssue(issues, tt.code) {
				t.Fatalf("content deduction had no %s issue: scores=%+v issues=%+v", tt.code, scores, issues)
			}
		})
	}
}

func TestBulletActionVerbMatchesPresentTenseAndGlyphs(t *testing.T) {
	bullets := []string{
		"Cut deployment time by 20%.",
		"Coordinate incident response across three teams.",
		"• Built a self-service platform.",
		"- Introduced release automation.",
		"Manage the on-call rotation.",
	}
	for _, bullet := range bullets {
		if !bulletStartsWithActionVerb(bullet) {
			t.Errorf("expected a strong action verb in %q", bullet)
		}
	}
	if genericBulletsDetected(resumeWithBullets(bullets...)) {
		t.Fatal("present-tense and glyph-prefixed strong bullets were classified as generic")
	}
	if got := skillsDemonstratedComponent(CanonicalResume{
		Skills:     ResumeSkills{Hard: []string{"AWS"}},
		Experience: []ResumeExperience{{Bullets: []string{"Coordinate AWS migrations."}}},
	}); got != 100 {
		t.Fatalf("action verb normalization must feed the HR skills component, got %d", got)
	}
}

func TestDiagnoseResumeMultiColumnRisk(t *testing.T) {
	r := resumeWithBullets("Built things with 20% improvement.")
	_, issues := diagnoseResume(r, "Name\t\tTitle\t\tDate\ncolumn | layout | detected | here")
	if !hasIssue(issues, "multi_column_risk") {
		t.Fatalf("expected multi_column_risk issue, got %+v", issues)
	}
}

func TestDiagnoseResumeMissingContact(t *testing.T) {
	r := resumeWithBullets("Built things with 20% improvement.")
	r.Basics.Email = ""
	r.Basics.Phone = ""
	_, issues := diagnoseResume(r, "")
	if !hasIssue(issues, "no_contact") {
		t.Fatalf("expected no_contact issue, got %+v", issues)
	}
}

func TestDiagnoseResumeIsDeterministic(t *testing.T) {
	r := resumeWithBullets("Reduced costs by 15% through automation.")
	scores1, issues1 := diagnoseResume(r, "some raw text")
	scores2, issues2 := diagnoseResume(r, "some raw text")
	if scores1 != scores2 {
		t.Fatalf("expected deterministic scores, got %+v vs %+v", scores1, scores2)
	}
	if len(issues1) != len(issues2) {
		t.Fatalf("expected deterministic issues, got %+v vs %+v", issues1, issues2)
	}
}

func hasIssue(issues []atsIssue, code string) bool {
	for _, i := range issues {
		if i.Code == code {
			return true
		}
	}
	return false
}

func TestResumeDiagnoseHandlerWorksWithoutAIKey(t *testing.T) {
	store := newTestStore(t) // no AI key configured at all
	a := &api{configStore: store}

	body, err := json.Marshal(diagnoseRequest{Canonical: resumeWithBullets("Reduced costs by 15%.")})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/v1/resume/diagnose", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	a.resumeDiagnose(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200 (diagnose works without AI key), got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp diagnoseResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Scores.Content == 0 {
		t.Fatalf("expected non-zero content score, got %+v", resp.Scores)
	}
	if resp.Heuristic {
		t.Fatalf("did not expect the heuristic fallback when a real canonical was supplied")
	}
	if resp.Canonical.Basics.Name != "Jane Doe" {
		t.Fatalf("expected the response to echo back the supplied canonical, got %+v", resp.Canonical.Basics)
	}
}

// BUG-04 regression: the actual product flow for a user with no AI key is
// upload -> diagnose, with NO canonical ever produced (parse is AI-gated).
// The handler must recognize the empty canonical and fall back to the
// offline heuristic parser automatically, rather than scoring an all-empty
// resume that looks broken.
func TestResumeDiagnoseHandlerBuildsHeuristicCanonicalFromRawTextAlone(t *testing.T) {
	store := newTestStore(t) // no AI key configured at all
	a := &api{configStore: store}

	body, err := json.Marshal(diagnoseRequest{RawText: sampleRawResumeText})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/v1/resume/diagnose", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	a.resumeDiagnose(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200 (diagnose works offline from raw text alone), got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp diagnoseResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Scores.Content == 0 {
		t.Fatalf("expected a non-zero content score from the heuristic fallback, got %+v", resp.Scores)
	}
	if hasIssue(resp.Issues, "no_contact") {
		t.Fatalf("did not expect no_contact for a resume with an email/phone in the raw text, got %+v", resp.Issues)
	}
	// The frontend needs the heuristic canonical echoed back so it can offer
	// export without ever calling the AI-gated /resume/parse route.
	if !resp.Heuristic {
		t.Fatalf("expected Heuristic=true when the canonical was built from raw text alone")
	}
	if resp.Canonical.Basics.Email == "" {
		t.Fatalf("expected the echoed heuristic canonical to carry the extracted email, got %+v", resp.Canonical.Basics)
	}
}

func TestResumeDiagnoseHandlerPersistsAnalysisWhenDocumentIDProvided(t *testing.T) {
	store := newTestStore(t)
	docID, err := store.saveResumeDocument(sampleCanonicalResume(), "Base", "", "")
	if err != nil {
		t.Fatalf("saveResumeDocument: %v", err)
	}
	a := &api{configStore: store}

	body, err := json.Marshal(diagnoseRequest{Canonical: sampleCanonicalResume(), DocumentID: docID})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/v1/resume/diagnose", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	a.resumeDiagnose(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}
