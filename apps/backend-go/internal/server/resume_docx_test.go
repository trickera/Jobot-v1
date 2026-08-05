package server

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func TestExportDOCXRoundTripsText(t *testing.T) {
	r := CanonicalResume{
		Basics:         ResumeBasics{Name: "Jane Doe", Email: "jane@example.com"},
		Summary:        "Backend engineer.",
		Skills:         ResumeSkills{Hard: []string{"AWS", "Terraform"}},
		Experience:     []ResumeExperience{{Company: "Acme", Role: "Engineer", Start: "2020-01", End: "present", Bullets: []string{"Built the platform."}}},
		Licenses:       []ResumeLicense{{Name: "Registered Nurse", Jurisdiction: "State of Illinois"}},
		Certifications: []ResumeCertification{{Name: "CKA", Issuer: "CNCF", Year: "2023"}},
	}
	data, err := exportDOCX(r, resumeTemplate{ID: resumeATSCleanTemplateID})
	if err != nil {
		t.Fatalf("exportDOCX: %v", err)
	}
	text, err := extractDOCXText(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("generated DOCX is not readable: %v", err)
	}
	for _, want := range []string{"Jane Doe", "jane@example.com", "AWS", "Built the platform.", "Registered Nurse", "LICENSES", "CKA", "EXPERIENCE"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in extracted text, got: %s", want, text)
		}
	}
	if !strings.Contains(text, "\nEXPERIENCE\n") {
		t.Fatalf("expected DOCX paragraph boundaries to survive extraction, got: %q", text)
	}
}

func TestExportDOCXGroupsSkillsLikePDF(t *testing.T) {
	// DOCX must group skills by category (as PDF/HTML/Markdown already do)
	// instead of the flat "Hard skills:" fallback, so the four export formats
	// stay consistent.
	r := CanonicalResume{
		Basics: ResumeBasics{Name: "Jane Doe", Email: "jane@example.com"},
		Skills: ResumeSkills{Hard: []string{"AWS", "Terraform", "Kubernetes"}, Tools: []string{"Docker"}},
	}
	if _, grouped := groupSkills(r.Skills); !grouped {
		t.Fatal("test precondition: these skills should be groupable")
	}
	data, err := exportDOCX(r, resumeTemplate{ID: resumeATSCleanTemplateID})
	if err != nil {
		t.Fatalf("exportDOCX: %v", err)
	}
	text, err := extractDOCXText(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("read DOCX: %v", err)
	}
	if strings.Contains(text, "Hard skills:") {
		t.Fatalf("expected grouped skills, but found the flat 'Hard skills:' fallback:\n%s", text)
	}
	if !strings.Contains(text, "AWS") || !strings.Contains(text, "Docker") {
		t.Fatalf("expected the skill items to survive grouping, got:\n%s", text)
	}
}

func TestExportDOCXIsValidZipWithRequiredParts(t *testing.T) {
	data, err := exportDOCX(CanonicalResume{Basics: ResumeBasics{Name: "A B"}}, resumeTemplate{ID: resumeATSStrictTemplateID})
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	found := map[string]bool{}
	for _, f := range zr.File {
		found[f.Name] = true
	}
	for _, part := range []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml", "word/styles.xml"} {
		if !found[part] {
			t.Fatalf("missing OOXML part %s", part)
		}
	}
}
