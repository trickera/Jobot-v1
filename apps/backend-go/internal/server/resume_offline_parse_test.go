package server

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const sampleRawResumeText = `Jane Doe
Backend Engineer
jane.doe@example.com | +1 555-123-4567

SUMMARY
Backend engineer with 6 years of experience building scalable APIs and cloud infrastructure.

EXPERIENCE
Senior Backend Engineer, Acme Corp (2019-2024)
Reduced deployment time by 40% using CI/CD automation.
Increased test coverage to 92% across core services.
Led migration to Kubernetes and Terraform on AWS.

EDUCATION
State University, BSc Computer Science, 2015

SKILLS
AWS, Terraform, Kubernetes, Docker, Python, PostgreSQL
`

// BUG-04 regression: buildHeuristicCanonical must extract real section
// signal from plain text alone (no AI parse), since that is exactly the
// situation a user with no API key is in.
func TestBuildHeuristicCanonicalExtractsRealSections(t *testing.T) {
	canonical := buildHeuristicCanonical(sampleRawResumeText)

	if canonical.Basics.Email != "jane.doe@example.com" {
		t.Fatalf("expected email extracted, got %q", canonical.Basics.Email)
	}
	if canonical.Basics.Phone == "" {
		t.Fatal("expected a phone number to be extracted")
	}
	if canonical.Summary == "" {
		t.Fatal("expected a non-empty summary")
	}
	if len(canonical.Experience) == 0 || len(canonical.Experience[0].Bullets) == 0 {
		t.Fatalf("expected at least one experience entry with bullets, got %+v", canonical.Experience)
	}
	if canonical.Experience[0].Start == "" {
		t.Fatal("expected a best-effort start year to be detected from the experience section")
	}
	if len(canonical.Education) == 0 {
		t.Fatalf("expected at least one education entry, got %+v", canonical.Education)
	}
	if len(canonical.Skills.Hard) == 0 {
		t.Fatalf("expected extracted skills, got %+v", canonical.Skills)
	}
}

func TestBuildHeuristicCanonicalSeparatesLicenseFromCertifications(t *testing.T) {
	raw := `Priya Raghunathan, RN, BSN

Licenses and Certifications
Registered Nurse, State of Illinois; CCRN; ACLS, BLS, PALS, TNCC`
	canonical := buildHeuristicCanonical(raw)

	if len(canonical.Licenses) != 1 || canonical.Licenses[0].Name != "Registered Nurse" || canonical.Licenses[0].Jurisdiction != "State of Illinois" {
		t.Fatalf("expected the nursing license to have its own canonical bucket, got %+v", canonical.Licenses)
	}
	if len(canonical.Certifications) != 5 || canonical.Certifications[0].Name != "CCRN" || canonical.Certifications[2].Name != "BLS" {
		t.Fatalf("expected non-license credentials to remain certifications, got %+v", canonical.Certifications)
	}
}

func TestNursingPersonaFixtureCanonicalJSONKeepsLicense(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "scripts", "qa", "fixtures", "personas", "2-nursing.pdf")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		t.Skip("persona fixture is added by the next QA-fixtures phase")
	}
	text, err := extractPDFText(path)
	if err != nil {
		t.Fatalf("extract nursing persona PDF: %v", err)
	}
	canonical := normalizeCanonical(buildHeuristicCanonical(text))
	if len(canonical.Licenses) != 1 || canonical.Licenses[0].Name != "Registered Nurse" {
		t.Fatalf("real nursing fixture lost its license: %+v", canonical.Licenses)
	}
	raw, err := json.Marshal(canonical)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("nursing fixture canonical JSON: %s", raw)
}

func TestIsEmptyCanonicalDetectsZeroValue(t *testing.T) {
	if !isEmptyCanonical(CanonicalResume{}) {
		t.Fatal("expected zero-value CanonicalResume to be considered empty")
	}
	if isEmptyCanonical(resumeWithBullets("Reduced costs by 15%.")) {
		t.Fatal("expected a populated CanonicalResume not to be considered empty")
	}
}

// BUG-04 regression, end to end: a resume that never went through the
// AI-gated /resume/parse route (rawText only, no canonical) must still get a
// genuinely useful offline ATS diagnostic - not an all-empty-resume score
// with misleading "no_contact"/"no_summary" issues for a resume that clearly
// has both.
func TestDiagnoseResumeFallsBackToHeuristicCanonicalWhenNoneProvided(t *testing.T) {
	canonical := buildHeuristicCanonical(sampleRawResumeText)
	scores, issues := diagnoseResume(canonical, sampleRawResumeText)

	if scores.Content < 60 {
		t.Fatalf("expected a reasonably high content score for a well-structured resume, got %d", scores.Content)
	}
	if hasIssue(issues, "no_contact") {
		t.Fatalf("did not expect no_contact issue for a resume with an email/phone, got %+v", issues)
	}
	if hasIssue(issues, "no_summary") {
		t.Fatalf("did not expect no_summary issue for a resume with a summary section, got %+v", issues)
	}
}

func TestDetectResumeSearchProfileAcrossDomains(t *testing.T) {
	tests := []struct {
		name       string
		resumeText string
		role       string
		seniority  string
	}{
		{"backend", "EXPERIENCE\nBackend Engineer — Acme\n2022 - present", "Backend Engineer", ""},
		{"nursing", "EXPERIENCE\nRegistered Nurse | Northwestern Memorial Hospital\n2021 - present", "Registered Nurse", ""},
		{"finance", "WORK HISTORY\nDeutsche Bank | Senior Financial Analyst\n2020 - present", "Senior Financial Analyst", "Senior"},
		{"marketing", "EXPERIENCE\nDigital Marketing Manager — Northstar\n2019 - present", "Digital Marketing Manager", "Manager"},
		{"product design", "EMPLOYMENT\nSenior Product Designer at Linear\n2023 - present", "Senior Product Designer", "Senior"},
		{"role with employer on the same line", "EXPERIÊNCIA\nAnalista DevOps – ExampleMarket.com\n2024-12 – 2026-04", "Analista DevOps", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			profile := detectResumeSearchProfile(tc.resumeText)
			if profile.Role != tc.role || profile.Seniority != tc.seniority {
				t.Fatalf("got role=%q seniority=%q, want role=%q seniority=%q", profile.Role, profile.Seniority, tc.role, tc.seniority)
			}
		})
	}
}

func TestDetectResumeSearchProfileDoesNotGuessFromSummary(t *testing.T) {
	profile := detectResumeSearchProfile("SUMMARY\nTechnology professional interested in healthcare and finance.")
	if profile.Role != "" || profile.Seniority != "" {
		t.Fatalf("expected no invented profile, got %+v", profile)
	}
}

func TestDetectResumeSearchProfileDoesNotTreatCompanyAsRole(t *testing.T) {
	profile := detectResumeSearchProfile("EXPERIENCE\nACME\n2020 - present\nImproved internal operations")
	if profile.Role != "" || profile.Seniority != "" {
		t.Fatalf("expected no invented role from company or prose, got %+v", profile)
	}
}
