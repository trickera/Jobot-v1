package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The two bugs this file exists to keep dead, both found in the 2026-07-13
// usability run, both in a PDF a real user was about to send to an employer:
//
//	UX-007  a Terraform-only resume exported "IaC & CI/CD: Terraform"
//	UX-019  a nurse's "Electronic Health Records (Epic)" exported as "Cloud:"

func labelOf(groups []resumeSkillGroup, item string) string {
	for _, g := range groups {
		for _, it := range g.items {
			if it == item {
				return g.label
			}
		}
	}
	return ""
}

// TestSkillCategoryLabelsAreAtomic is the invariant that makes UX-007
// unrepresentable. A compound label lets one matched item vouch for a capability
// nothing in the resume evidenced.
func TestSkillCategoryLabelsAreAtomic(t *testing.T) {
	for _, cat := range resumeSkillCategories {
		if !labelIsAtomic(cat.label) {
			t.Errorf("category label %q is compound: one matched item would vouch for a second, unevidenced capability", cat.label)
		}
	}
	for _, label := range []string{"IaC & CI/CD", "Programming & Scripting", "Data & ML", "Cloud, Infra"} {
		if labelIsAtomic(label) {
			t.Errorf("labelIsAtomic(%q) = true; compound labels must be rejected", label)
		}
	}
	if !labelIsAtomic("CI/CD") {
		t.Error(`labelIsAtomic("CI/CD") = false; it is one named practice, not two`)
	}
}

// TestTerraformAloneNeverClaimsCICD is UX-007 itself.
func TestTerraformAloneNeverClaimsCICD(t *testing.T) {
	groups, grouped := groupSkills(ResumeSkills{
		Hard:  []string{"Terraform", "Go", "PostgreSQL", "Docker"},
		Tools: []string{"Git"},
	})
	if !grouped {
		t.Fatal("expected grouped=true for a normal backend skill set")
	}
	if got := labelOf(groups, "Terraform"); got != "Infrastructure as Code" {
		t.Errorf("Terraform landed under %q, want %q", got, "Infrastructure as Code")
	}
	for _, g := range groups {
		if g.label == "CI/CD" {
			t.Fatalf("a CI/CD category appeared with no CI/CD skill in the resume: %v", g.items)
		}
	}
}

// TestTerraformWithGitHubActionsDoesClaimCICD — the label is earned when, and
// only when, the evidence is there.
func TestTerraformWithGitHubActionsDoesClaimCICD(t *testing.T) {
	groups, grouped := groupSkills(ResumeSkills{
		Hard: []string{"Terraform", "GitHub Actions", "Docker", "Go"},
	})
	if !grouped {
		t.Fatal("expected grouped=true")
	}
	if got := labelOf(groups, "GitHub Actions"); got != "CI/CD" {
		t.Errorf("GitHub Actions landed under %q, want %q", got, "CI/CD")
	}
	if got := labelOf(groups, "Terraform"); got != "Infrastructure as Code" {
		t.Errorf("Terraform landed under %q, want %q", got, "Infrastructure as Code")
	}
}

// TestElectronicHealthRecordsIsNeverCloud is UX-019 itself: "rds" ⊂ "reco-rds-".
func TestElectronicHealthRecordsIsNeverCloud(t *testing.T) {
	groups, grouped := groupSkills(ResumeSkills{
		Hard: []string{
			"Electronic Health Records (Epic)",
			"ICU / Critical Care",
			"Ventilator Management",
			"CRRT",
			"Triage",
			"Medication Administration",
		},
	})
	if !grouped {
		t.Fatal("expected a nursing skill set to group under the clinical categories")
	}
	for _, g := range groups {
		if g.label == "Cloud" {
			t.Fatalf("a nursing resume produced a Cloud category: %v", g.items)
		}
	}
	if got := labelOf(groups, "Electronic Health Records (Epic)"); got != "Clinical Systems" {
		t.Errorf("Electronic Health Records (Epic) landed under %q, want %q", got, "Clinical Systems")
	}
	if got := labelOf(groups, "CRRT"); got != "Clinical Skills" {
		t.Errorf("CRRT landed under %q, want %q", got, "Clinical Skills")
	}
}

// TestSubstringCollisionsDoNotCategorize pins the exact word-boundary failures
// the old strings.Contains matcher had.
func TestSubstringCollisionsDoNotCategorize(t *testing.T) {
	for _, skill := range []string{
		"Electronic Health Records", // rds ⊂ records
		"Clinical Standards",        // rds ⊂ standards
		"Industry Awards",           // rds ⊂ awards
		"Miami Relocation",          // iam ⊂ Miami
		"Django REST",               // go ⊂ Django
		"Cargo Logistics",           // go ⊂ Cargo
	} {
		if i := categorizeSkill(skill); i >= 0 && resumeSkillCategories[i].label == "Cloud" {
			t.Errorf("%q was categorized as Cloud by substring collision", skill)
		}
		if i := categorizeSkill(skill); i >= 0 && resumeSkillCategories[i].label == "Programming Languages" {
			t.Errorf("%q was categorized as a programming language by substring collision", skill)
		}
	}
}

// TestExactOnlyAliasesDoNotSwallowUnrelatedSkills covers the true whole-word
// collisions that word boundaries alone cannot catch.
func TestExactOnlyAliasesDoNotSwallowUnrelatedSkills(t *testing.T) {
	cases := map[string]string{
		"Sous Chef":               "Infrastructure as Code",
		"Flux Cored Arc Welding":  "CI/CD",
		"Epic Games Unreal":       "Clinical Systems",
		"Go-To-Market Strategy":   "Programming Languages",
		"Salt Water Chemistry":    "Infrastructure as Code",
		"Word of Mouth Marketing": "Productivity",
	}
	for skill, forbidden := range cases {
		i := categorizeSkill(skill)
		if i >= 0 && resumeSkillCategories[i].label == forbidden {
			t.Errorf("%q was wrongly categorized as %q — exact-only alias leaked", skill, forbidden)
		}
	}
	// The exact forms still work.
	for skill, want := range map[string]string{
		"Chef":  "Infrastructure as Code",
		"Epic":  "Clinical Systems",
		"Go":    "Programming Languages",
		"Argo":  "CI/CD",
		"Excel": "Productivity",
	} {
		i := categorizeSkill(skill)
		if i < 0 || resumeSkillCategories[i].label != want {
			got := "(none)"
			if i >= 0 {
				got = resumeSkillCategories[i].label
			}
			t.Errorf("categorizeSkill(%q) = %q, want %q", skill, got, want)
		}
	}
}

// TestGroupSkillsFallsBackWhenTheTaxonomyDoesNotEarnItsKeep — one accidental hit
// must not drag a whole non-technical resume into a technical taxonomy.
func TestGroupSkillsFallsBackWhenTheTaxonomyDoesNotEarnItsKeep(t *testing.T) {
	_, grouped := groupSkills(ResumeSkills{
		Hard: []string{"Flux Cored Arc Welding", "MIG Welding", "TIG Welding", "Blueprint Reading", "Chef"},
	})
	if grouped {
		t.Fatal("a welding resume with one incidental match must fall back to the flat skill list")
	}
	_, grouped = groupSkills(ResumeSkills{Hard: []string{"Cobol", "Fortran"}})
	if grouped {
		t.Fatal("expected grouped=false when nothing matches a category")
	}
}

// TestGroupSkillsNeverLosesOrInventsASkill is the multiset invariant, run across
// five professions so it cannot be satisfied by a software-shaped fixture alone.
func TestGroupSkillsNeverLosesOrInventsASkill(t *testing.T) {
	personas := map[string]ResumeSkills{
		"software": {
			Hard:  []string{"Go", "PostgreSQL", "Terraform", "Kubernetes", "gRPC"},
			Tools: []string{"Docker", "Git"},
			Soft:  []string{"Mentoring"},
		},
		"nursing": {
			Hard: []string{"ICU", "CRRT", "Ventilator Management", "Electronic Health Records (Epic)", "Triage", "ACLS"},
			Soft: []string{"Patient Advocacy"},
		},
		"finance": {
			Hard:  []string{"Financial Modeling", "GAAP", "Variance Analysis", "SOX", "Valuation"},
			Tools: []string{"Excel", "NetSuite"},
		},
		"marketing": {
			Hard:  []string{"SEO", "Content Marketing", "Google Ads", "Copywriting", "Brand Strategy"},
			Tools: []string{"HubSpot"},
		},
		"design": {
			Hard:  []string{"User Research", "Wireframing", "Design Systems", "Prototyping", "WCAG"},
			Tools: []string{"Figma", "Photoshop"},
		},
	}
	for name, skills := range personas {
		t.Run(name, func(t *testing.T) {
			groups, grouped := groupSkills(skills)
			if !grouped {
				t.Fatalf("%s persona did not group — the taxonomy has no category for this profession", name)
			}
			want := map[string]int{}
			for _, s := range append(append(append([]string{}, skills.Hard...), skills.Tools...), skills.Soft...) {
				want[s]++
			}
			got := map[string]int{}
			for _, g := range groups {
				if !labelIsAtomic(g.label) && g.label != "Additional" && g.label != "Soft Skills" {
					t.Errorf("emitted compound label %q", g.label)
				}
				for _, item := range g.items {
					got[item]++
				}
			}
			for k, v := range want {
				if got[k] != v {
					t.Errorf("skill %q: emitted %d times, resume lists it %d times", k, got[k], v)
				}
			}
			for k := range got {
				if want[k] == 0 {
					t.Errorf("skill %q was emitted but is not on the resume — invention", k)
				}
			}
		})
	}
}

// TestNursingResumeNeverAcquiresDevOpsSkills — the acceptance criterion, stated
// as a test: no explicit evidence, no technical category.
func TestNursingResumeNeverAcquiresDevOpsSkills(t *testing.T) {
	groups, _ := groupSkills(ResumeSkills{
		Hard: []string{
			"Electronic Health Records (Epic)", "ICU", "CRRT", "Ventilator Management",
			"Triage", "Wound Care", "Patient Assessment", "Infection Control",
		},
	})
	forbidden := map[string]bool{
		"Cloud": true, "Infrastructure as Code": true, "CI/CD": true,
		"Containers": true, "Observability": true, "Methodologies": true,
	}
	for _, g := range groups {
		if forbidden[g.label] {
			t.Fatalf("nursing resume acquired the technical category %q: %v", g.label, g.items)
		}
	}
}

// TestExportedPDFNeverContainsAnUnevidencedCategory is the end-to-end assertion
// that would have caught both bugs: it renders the real PDF the user downloads
// and reads the text back out of it.
func TestExportedPDFNeverContainsAnUnevidencedCategory(t *testing.T) {
	cases := []struct {
		name     string
		skills   ResumeSkills
		mustNot  []string
		mustHave []string
	}{
		{
			name: "terraform without ci/cd",
			skills: ResumeSkills{
				Hard:  []string{"Terraform", "Go", "PostgreSQL"},
				Tools: []string{"Docker"},
			},
			mustNot:  []string{"CI/CD", "IaC &"},
			mustHave: []string{"Infrastructure as Code"},
		},
		{
			name: "nurse with an EHR",
			skills: ResumeSkills{
				Hard: []string{"Electronic Health Records (Epic)", "ICU", "CRRT", "Triage", "Wound Care"},
			},
			mustNot:  []string{"Cloud", "DevOps", "Terraform"},
			mustHave: []string{"Clinical"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resume := CanonicalResume{
				SchemaVersion: 1,
				Basics:        ResumeBasics{Name: "Test Candidate", Email: "candidate@example.com"},
				Summary:       "Practitioner.",
				Skills:        tc.skills,
				Experience: []ResumeExperience{{
					Company: "Example Org", Role: "Practitioner", Start: "2020", End: "present",
					Bullets: []string{"Delivered work."},
				}},
			}
			pdfBytes, err := exportPDF(resume, resumeTemplate{ID: resumeATSCleanTemplateID}, "letter")
			if err != nil {
				t.Fatalf("exportPDF: %v", err)
			}
			path := filepath.Join(t.TempDir(), "resume.pdf")
			if err := os.WriteFile(path, pdfBytes, 0o600); err != nil {
				t.Fatalf("write pdf: %v", err)
			}
			text, err := extractPDFText(path)
			if err != nil {
				t.Fatalf("extractPDFText: %v", err)
			}
			for _, banned := range tc.mustNot {
				if strings.Contains(text, banned) {
					t.Errorf("exported PDF contains %q, which no skill on the resume evidences.\nPDF text:\n%s", banned, text)
				}
			}
			for _, want := range tc.mustHave {
				if !strings.Contains(text, want) {
					t.Errorf("exported PDF is missing the grounded label %q.\nPDF text:\n%s", want, text)
				}
			}
		})
	}
}

// TestEveryExporterUsesTheSameCategorization — preview (HTML), Markdown and the
// PDF must not disagree. UX-007 was only visible in the PDF because the diff the
// user reviewed never showed a category at all.
func TestEveryExporterUsesTheSameCategorization(t *testing.T) {
	resume := CanonicalResume{
		SchemaVersion: 1,
		Basics:        ResumeBasics{Name: "Test Candidate"},
		Skills: ResumeSkills{
			Hard: []string{"Electronic Health Records (Epic)", "ICU", "CRRT", "Triage"},
		},
		Experience: []ResumeExperience{{Company: "Example Hospital", Role: "RN", Bullets: []string{"Cared for patients."}}},
	}

	md := exportMarkdown(resume)
	html := exportHTML(resume, resumeTemplate{ID: resumeATSCleanTemplateID})
	for name, out := range map[string]string{"markdown": md, "html": html} {
		if strings.Contains(out, "Cloud") {
			t.Errorf("%s export contains a Cloud category for a nursing resume:\n%s", name, out)
		}
		if !strings.Contains(out, "Clinical Systems") {
			t.Errorf("%s export lost the Clinical Systems category:\n%s", name, out)
		}
	}
}
