package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func alignedResume() CanonicalResume {
	return CanonicalResume{
		Basics:  ResumeBasics{Name: "Jane Doe", Email: "jane@example.com", Headline: "DevOps Engineer"},
		Target:  ResumeTarget{JobTitle: "DevOps Engineer"},
		Summary: "DevOps engineer with strong AWS and Terraform background.",
		Skills:  ResumeSkills{Hard: []string{"AWS", "Terraform", "Kubernetes"}},
		Experience: []ResumeExperience{
			{
				Company: "Acme", Role: "Senior DevOps Engineer", Start: "2022-01", End: "present",
				Bullets: []string{
					"Automated AWS deployments with Terraform, reducing release time by 40%.",
					"Led migration of 20 services to Kubernetes.",
				},
			},
			{
				Company: "Globex", Role: "DevOps Engineer", Start: "2018-01", End: "2021-12",
				Bullets: []string{"Managed CI/CD pipelines with AWS and Terraform."},
			},
		},
	}
}

func unalignedResume() CanonicalResume {
	return CanonicalResume{
		Basics:  ResumeBasics{Name: "John Roe"},
		Summary: "Retail associate with customer service experience.",
		Experience: []ResumeExperience{
			{Company: "ShopCo", Role: "Cashier", Start: "2019-01", End: "2021-12", Bullets: []string{"Worked the register."}},
		},
	}
}

func devopsRequirements() jobRequirements {
	return jobRequirements{
		JobTitle:         "DevOps Engineer",
		HardRequirements: []string{"AWS", "Terraform", "Kubernetes"},
		ATSKeywords:      []string{"CI/CD", "IaC"},
		Seniority:        "Sênior",
	}
}

func TestATSScoreAlignedResumeScoresHigherThanUnaligned(t *testing.T) {
	req := devopsRequirements()
	alignedScore, _ := atsScore(alignedResume(), req)
	unalignedScore, _ := atsScore(unalignedResume(), req)
	if alignedScore <= unalignedScore {
		t.Fatalf("expected aligned resume to score higher: aligned=%d unaligned=%d", alignedScore, unalignedScore)
	}
}

func TestATSScoreBreakdownSumsToTotal(t *testing.T) {
	req := devopsRequirements()
	total, breakdown := atsScore(alignedResume(), req)
	sum := 0
	for _, v := range breakdown {
		sum += v
	}
	if sum != total {
		t.Fatalf("expected breakdown to sum to total, breakdown=%d total=%d (%+v)", sum, total, breakdown)
	}
}

func productDesignerResume() CanonicalResume {
	return CanonicalResume{
		Basics:  ResumeBasics{Name: "Sofia Almeida", Email: "sofia@example.com", Headline: "Senior Product Designer"},
		Summary: "Product designer with 6 years shipping end-to-end product experiences for fintech and marketplace apps.",
		Skills:  ResumeSkills{Hard: []string{"Figma", "prototyping", "design systems"}},
		Experience: []ResumeExperience{
			{
				Company: "Revolut", Role: "Senior Product Designer", Start: "2021-03", End: "present",
				Bullets: []string{"Built and maintained the mobile design system in Figma, used by 30+ designers."},
			},
		},
	}
}

// The E2E run (2026-07-13, area 5) scored phrase match 0/25 for a resume whose
// gap analysis had reported the very same requirements as STRONG MATCH: the
// component asked for the whole natural-language requirement to appear verbatim
// in the resume, which never happens outside a keyword list.
func TestATSPhraseMatchCreditsNaturalLanguageRequirement(t *testing.T) {
	req := jobRequirements{JobTitle: "Senior Product Designer", HardRequirements: []string{"5+ years product design"}}
	_, breakdown := atsScore(productDesignerResume(), req)
	if breakdown["phrase"] == 0 {
		t.Fatalf("a requirement the resume demonstrably covers must earn phrase points, got %+v", breakdown)
	}
}

func TestATSPhraseMatchIgnoresRequirementFillerWords(t *testing.T) {
	// "strong experience with Figma" is the same requirement as "Figma" once the
	// job-posting filler is stripped, so a resume listing Figma earns it in full.
	full := phraseMatchComponent(normalizeText(resumeSearchableText(productDesignerResume())), []string{"Figma"})
	padded := phraseMatchComponent(normalizeText(resumeSearchableText(productDesignerResume())), []string{"strong experience with Figma"})
	if padded != full {
		t.Fatalf("filler words must not dilute a covered requirement: padded=%d full=%d", padded, full)
	}
}

func TestATSPhraseMatchGivesPartialCreditForHalfCoveredRequirement(t *testing.T) {
	text := normalizeText(resumeSearchableText(productDesignerResume()))
	// "design systems" is on the resume; "quantitative research" is not.
	partial := phraseMatchComponent(text, []string{"design systems and quantitative research"})
	if partial <= 0 || partial >= 100 {
		t.Fatalf("a half-covered requirement must earn partial credit, got %d", partial)
	}
}

func TestATSPhraseMatchDoesNotCreditAnAbsentRequirement(t *testing.T) {
	text := normalizeText(resumeSearchableText(productDesignerResume()))
	if got := phraseMatchComponent(text, []string{"Kubernetes"}); got != 0 {
		t.Fatalf("a requirement absent from the resume must earn nothing, got %d", got)
	}
}

func TestATSScoreEmptyRequirementsDoesNotPenalizePhraseOrKeyword(t *testing.T) {
	_, breakdown := atsScore(alignedResume(), jobRequirements{})
	if breakdown["phrase"] != 25 || breakdown["keyword"] != 25 {
		t.Fatalf("expected full phrase/keyword weight when requirement lists are empty, got %+v", breakdown)
	}
}

func TestATSScoreIsDeterministic(t *testing.T) {
	req := devopsRequirements()
	r := alignedResume()
	s1, b1 := atsScore(r, req)
	s2, b2 := atsScore(r, req)
	if s1 != s2 {
		t.Fatalf("expected deterministic score, got %d vs %d", s1, s2)
	}
	for k := range b1 {
		if b1[k] != b2[k] {
			t.Fatalf("expected deterministic breakdown for %s, got %d vs %d", k, b1[k], b2[k])
		}
	}
}

func TestHRScoreAlignedResumeScoresHigherThanUnaligned(t *testing.T) {
	req := devopsRequirements()
	alignedScore, _ := hrScore(alignedResume(), req)
	unalignedScore, _ := hrScore(unalignedResume(), req)
	if alignedScore <= unalignedScore {
		t.Fatalf("expected aligned resume to score higher on HR: aligned=%d unaligned=%d", alignedScore, unalignedScore)
	}
}

func TestHRScoreBreakdownSumsToTotal(t *testing.T) {
	req := devopsRequirements()
	total, breakdown := hrScore(alignedResume(), req)
	sum := 0
	for _, v := range breakdown {
		sum += v
	}
	if sum != total {
		t.Fatalf("expected breakdown to sum to total, breakdown=%d total=%d (%+v)", sum, total, breakdown)
	}
}

func TestHRScoreTrajectoryRewardsPromotion(t *testing.T) {
	r := CanonicalResume{
		Basics: ResumeBasics{Name: "Jane Doe"},
		Experience: []ResumeExperience{
			{Company: "Acme", Role: "Engineering Manager", Start: "2022-01", End: "present", Bullets: []string{"Led the platform team."}},
			{Company: "Acme", Role: "Junior Engineer", Start: "2018-01", End: "2021-12", Bullets: []string{"Supported the team."}},
		},
	}
	_, breakdown := hrScore(r, jobRequirements{})
	if breakdown["trajectory"] != 15 {
		t.Fatalf("expected full trajectory score for a promotion, got %+v", breakdown)
	}
}

func TestHRScoreExperienceFitUsesExplicitRequiredYears(t *testing.T) {
	req := jobRequirements{HardRequirements: []string{"3+ years experience", "AWS"}}
	r := alignedResume() // ~2 years + ~4 years of experience = well above 3
	fit := experienceFitComponent(r, req)
	if fit != 100 {
		t.Fatalf("expected experience fit 100 when total years exceed the required 3+, got %d", fit)
	}
}

func TestHRScoreExperienceFitNoExperienceScoresLow(t *testing.T) {
	r := CanonicalResume{Basics: ResumeBasics{Name: "Jane Doe"}}
	fit := experienceFitComponent(r, jobRequirements{Seniority: "Sênior"})
	if fit != 40 {
		t.Fatalf("expected 40 for no experience at all, got %d", fit)
	}
}

func TestHRScoreExperienceFitNoSeniorityOrYearsSignalDoesNotPenalize(t *testing.T) {
	r := alignedResume()
	fit := experienceFitComponent(r, jobRequirements{})
	if fit != 100 {
		t.Fatalf("expected no penalty when neither years nor seniority signal is present, got %d", fit)
	}
}

func TestClarityComponentPenalizesVeryShortBullets(t *testing.T) {
	r := resumeWithBullets("Did stuff.", "Worked.", "Helped.")
	score := clarityComponent(r)
	if score >= 100 {
		t.Fatalf("expected penalty for very short bullets, got %d", score)
	}
}

func TestClarityComponentFullScoreInRange(t *testing.T) {
	r := resumeWithBullets(
		"Automated cloud infrastructure deployments using Terraform and AWS across three teams.",
		"Led migration of legacy services into containerized Kubernetes workloads for reliability.",
	)
	if score := clarityComponent(r); score != 100 {
		t.Fatalf("expected full clarity score for well-sized bullets, got %d", score)
	}
}

func TestResumeScoreHandlerWorksWithoutAIKey(t *testing.T) {
	store := newTestStore(t)
	a := &api{configStore: store}

	body, err := json.Marshal(scoreReq{Canonical: alignedResume(), Requirements: devopsRequirements()})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/v1/resume/score", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	a.resumeScore(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200 (score works without AI key), got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp scoreResult
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ATS == 0 || resp.HR == 0 {
		t.Fatalf("expected non-zero scores for an aligned resume, got %+v", resp)
	}
	if len(resp.ATSBreakdown) != 6 {
		t.Fatalf("expected 6 ATS breakdown components, got %+v", resp.ATSBreakdown)
	}
	if len(resp.HRBreakdown) != 5 {
		t.Fatalf("expected 5 HR breakdown components, got %+v", resp.HRBreakdown)
	}
}

func TestATSStructureUsesUploadedRawText(t *testing.T) {
	resume := alignedResume()
	_, clean := atsScoreWithRawText(resume, jobRequirements{}, "Single column resume text")
	_, risky := atsScoreWithRawText(resume, jobRequirements{}, "Name\t\tTitle\t\tDate\nleft | middle | right | extra")
	if risky["structure"] >= clean["structure"] {
		t.Fatalf("expected raw multi-column evidence to reduce structure points: clean=%v risky=%v", clean, risky)
	}
}
