package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// qaSampleResume is a realistic, ~1.5-page resume used to inspect PDF layout
// quality (typography, spacing, bullets, pagination) — long enough to spill
// onto a second page so the "sparse page 2" problem is reproducible.
func qaSampleResume() CanonicalResume {
	return CanonicalResume{
		Basics: ResumeBasics{
			Name:     "Alex Rivera",
			Headline: "Senior Platform Engineer",
			Email:    "alex.rivera@example.com",
			Phone:    "+1 (415) 555-0142",
			Location: "Remote · United States",
			Links: []ResumeLink{
				{Label: "GitHub", URL: "github.com/alexrivera"},
				{Label: "LinkedIn", URL: "linkedin.com/in/alexrivera"},
			},
		},
		Summary: "Platform engineer with 8 years building and operating cloud infrastructure at scale. " +
			"Focused on reliability, developer velocity, and cost efficiency across AWS and Kubernetes environments.",
		Skills: ResumeSkills{
			Hard:  []string{"AWS", "Terraform", "Kubernetes", "CI/CD", "Go", "Python", "PostgreSQL", "Observability"},
			Soft:  []string{"Mentorship", "Incident command", "Technical writing"},
			Tools: []string{"Docker", "Prometheus", "Grafana", "GitHub Actions", "ArgoCD", "Datadog"},
		},
		Experience: []ResumeExperience{
			{
				Company: "Northwind Cloud", Role: "Senior Platform Engineer", Start: "2021-03", End: "present",
				Location: "Remote",
				Bullets: []string{
					"Cut median deploy time by 40% by rebuilding the CI/CD pipeline on GitHub Actions and ArgoCD across 30+ services.",
					"Led the migration of 60 microservices to Kubernetes, reducing infrastructure cost by 28% through right-sizing and spot capacity.",
					"Built a self-service Terraform module library that removed the platform team from 80% of routine provisioning requests.",
					"Established SLOs and an on-call runbook program that cut mean time to recovery from 45 to 12 minutes.",
				},
			},
			{
				Company: "Baytech Systems", Role: "Site Reliability Engineer", Start: "2018-06", End: "2021-02",
				Location: "Austin, TX",
				Bullets: []string{
					"Owned the observability stack (Prometheus, Grafana, Datadog) for a fleet of 200+ instances.",
					"Automated incident response with runbooks and alerting that reduced pages by 35% quarter over quarter.",
					"Partnered with product teams to instrument critical user journeys, improving error-budget visibility.",
				},
			},
			{
				Company: "Cedar Analytics", Role: "DevOps Engineer", Start: "2016-01", End: "2018-05",
				Location: "Denver, CO",
				Bullets: []string{
					"Containerized a legacy monolith and introduced a staging environment that caught 90% of regressions before release.",
					"Wrote the first infrastructure-as-code baseline in Terraform, replacing manual console provisioning.",
				},
			},
		},
		Education: []ResumeEducation{
			{Institution: "University of Colorado Boulder", Degree: "BSc Computer Science", Area: "Distributed Systems", Start: "2011", End: "2015"},
		},
		Certifications: []ResumeCertification{
			{Name: "Certified Kubernetes Administrator", Issuer: "CNCF", Year: "2022"},
			{Name: "AWS Certified Solutions Architect – Professional", Issuer: "AWS", Year: "2021"},
		},
		Languages: []ResumeLanguage{
			{Language: "English", Fluency: "Native"},
			{Language: "Spanish", Fluency: "Professional"},
		},
	}
}

// TestRenderSamplePDFsForVisualQA writes one PDF per template to the directory
// named by SENCIA_PDF_QA_OUT, for out-of-band visual inspection (render to PNG
// with PyMuPDF). It is skipped in normal runs so it never pollutes the suite or
// the working tree. Usage:
//
//	SENCIA_PDF_QA_OUT=<dir> go test ./internal/server -run TestRenderSamplePDFsForVisualQA
func TestRenderSamplePDFsForVisualQA(t *testing.T) {
	outDir := os.Getenv("SENCIA_PDF_QA_OUT")
	if outDir == "" {
		t.Skip("set SENCIA_PDF_QA_OUT to a directory to emit sample PDFs for visual QA")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir out: %v", err)
	}
	// A long variant (extra experiences) to inspect how a genuinely 2-page
	// résumé balances — page 2 should carry real content, not an orphan line.
	long := qaSampleResume()
	firstExp := long.Experience[0]
	for i := 0; i < 6; i++ {
		extra := firstExp
		extra.Company = firstExp.Company + " " + strings.Repeat("X", i+1)
		long.Experience = append(long.Experience, extra)
	}

	cases := []struct {
		name   string
		resume CanonicalResume
		tmpl   resumeTemplate
	}{
		{"ats-strict", qaSampleResume(), resumeTemplate{ID: resumeATSStrictTemplateID}},
		{"ats-clean", qaSampleResume(), resumeTemplate{ID: resumeATSCleanTemplateID}},
		{"modern-accent", qaSampleResume(), resumeTemplate{ID: resumeModernAccentTemplateID}},
		{"long-ats-clean", long, resumeTemplate{ID: resumeATSCleanTemplateID}},
	}
	for _, c := range cases {
		pdfBytes, err := exportPDF(c.resume, c.tmpl, "letter")
		if err != nil {
			t.Fatalf("exportPDF(%s): %v", c.name, err)
		}
		path := filepath.Join(outDir, "template-"+c.name+".pdf")
		if err := os.WriteFile(path, pdfBytes, 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		t.Logf("wrote %s (%d bytes)", path, len(pdfBytes))
	}
}
