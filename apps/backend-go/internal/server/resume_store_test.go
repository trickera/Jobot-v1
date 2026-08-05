package server

import "testing"

func sampleCanonicalResume() CanonicalResume {
	return CanonicalResume{
		SchemaVersion: currentResumeSchemaVersion,
		Basics:        ResumeBasics{Name: "Jane Doe", Email: "jane@example.com"},
		Target:        ResumeTarget{JobTitle: "DevOps Engineer", Category: "tech", Seniority: "Pleno"},
		Summary:       "Backend engineer with cloud experience.",
		Skills:        ResumeSkills{Hard: []string{"AWS", "Terraform"}},
		Experience: []ResumeExperience{
			{Company: "Acme", Role: "Engineer", Start: "2020-01", End: "present", Bullets: []string{"Built things."}},
		},
	}
}

func TestSaveAndGetResumeDocument(t *testing.T) {
	store := newTestStore(t)
	doc := sampleCanonicalResume()

	id, err := store.saveResumeDocument(doc, "My Resume", "resume.pdf", "pdf")
	if err != nil {
		t.Fatalf("saveResumeDocument: %v", err)
	}
	if id == "" {
		t.Fatal("expected a non-empty document id")
	}

	got, meta, err := store.getResumeDocument(id)
	if err != nil {
		t.Fatalf("getResumeDocument: %v", err)
	}
	if got.Basics.Name != "Jane Doe" {
		t.Fatalf("expected round-tripped canonical resume, got %+v", got)
	}
	if meta.Name != "My Resume" || meta.SourceFile != "resume.pdf" || meta.SourceFormat != "pdf" {
		t.Fatalf("unexpected doc meta: %+v", meta)
	}

	docs, err := store.listResumeDocuments()
	if err != nil {
		t.Fatalf("listResumeDocuments: %v", err)
	}
	if len(docs) != 1 || docs[0].ID != id {
		t.Fatalf("expected 1 listed document with id %s, got %+v", id, docs)
	}
}

func TestGetResumeDocumentNotFound(t *testing.T) {
	store := newTestStore(t)
	if _, _, err := store.getResumeDocument("resume:doc:missing"); err != errResumeDocumentNotFound {
		t.Fatalf("expected errResumeDocumentNotFound, got %v", err)
	}
}

func TestSaveResumeVersionAndListByJob(t *testing.T) {
	store := newTestStore(t)
	docID, err := store.saveResumeDocument(sampleCanonicalResume(), "Base", "", "")
	if err != nil {
		t.Fatalf("saveResumeDocument: %v", err)
	}

	jobID := "job:1"
	if _, _, err := store.applyJobAction("dismiss", jobSummary{ID: jobID, Title: "Test Job", Company: "Acme"}); err != nil {
		t.Fatalf("seed job row: %v", err)
	}
	if err := store.seedResumeTemplates(); err != nil {
		t.Fatalf("seedResumeTemplates: %v", err)
	}
	verID, err := store.saveResumeVersion(resumeVersion{
		DocumentID: docID,
		JobID:      jobID,
		Canonical:  sampleCanonicalResume(),
		Patches:    []jsonPatchOp{{Op: "replace", Path: "/summary", Value: nil, Reason: "test"}},
		TemplateID: resumeATSStrictTemplateID,
		ATSScore:   83,
		HRScore:    76,
	})
	if err != nil {
		t.Fatalf("saveResumeVersion: %v", err)
	}
	if verID == "" {
		t.Fatal("expected a non-empty version id")
	}

	versions, err := store.listResumeVersionsByJob(jobID)
	if err != nil {
		t.Fatalf("listResumeVersionsByJob: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("expected 1 version for job, got %d", len(versions))
	}
	v := versions[0]
	if v.ID != verID || v.DocumentID != docID || v.JobID != jobID {
		t.Fatalf("unexpected version: %+v", v)
	}
	if v.ATSScore != 83 || v.HRScore != 76 {
		t.Fatalf("expected scores to round-trip, got ats=%d hr=%d", v.ATSScore, v.HRScore)
	}
	if len(v.Patches) != 1 || v.Patches[0].Reason != "test" {
		t.Fatalf("expected patches to round-trip, got %+v", v.Patches)
	}

	// A different job must not see this version.
	other, err := store.listResumeVersionsByJob("job:other")
	if err != nil {
		t.Fatalf("listResumeVersionsByJob(other): %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("expected no versions for unrelated job, got %d", len(other))
	}
}

func TestSaveResumeAnalysis(t *testing.T) {
	store := newTestStore(t)
	docID, err := store.saveResumeDocument(sampleCanonicalResume(), "Base", "", "")
	if err != nil {
		t.Fatalf("saveResumeDocument: %v", err)
	}
	err = store.saveResumeAnalysis(resumeAnalysis{
		SubjectKind: "document",
		SubjectID:   docID,
		Scores:      atsScores{Readability: 80, Content: 70, Impact: 60, Keywords: 90},
		Issues:      []atsIssue{{Code: "no_metrics", Severity: "high", Message: "Few bullets with impact metrics."}},
	})
	if err != nil {
		t.Fatalf("saveResumeAnalysis: %v", err)
	}
}

func TestUpsertJobResumeMatchUpdatesStatus(t *testing.T) {
	store := newTestStore(t)
	jobID := "job:1"
	if _, _, err := store.applyJobAction("dismiss", jobSummary{ID: jobID, Title: "Test Job", Company: "Acme"}); err != nil {
		t.Fatalf("seed job row: %v", err)
	}
	docID, err := store.saveResumeDocument(sampleCanonicalResume(), "Base", "", "")
	if err != nil {
		t.Fatalf("saveResumeDocument: %v", err)
	}
	verID1, err := store.saveResumeVersion(resumeVersion{DocumentID: docID, JobID: jobID, Canonical: sampleCanonicalResume()})
	if err != nil {
		t.Fatalf("saveResumeVersion (1): %v", err)
	}
	verID2, err := store.saveResumeVersion(resumeVersion{DocumentID: docID, JobID: jobID, Canonical: sampleCanonicalResume()})
	if err != nil {
		t.Fatalf("saveResumeVersion (2): %v", err)
	}

	err = store.upsertJobResumeMatch(jobResumeMatch{
		JobID:     jobID,
		VersionID: verID1,
		Gap:       gapResult{Found: []gapItem{{Term: "AWS", Evidence: "skills"}}},
		Status:    "gerado",
	})
	if err != nil {
		t.Fatalf("upsertJobResumeMatch (insert): %v", err)
	}

	match, ok, err := store.getJobResumeMatch(jobID)
	if err != nil {
		t.Fatalf("getJobResumeMatch: %v", err)
	}
	if !ok {
		t.Fatal("expected match to exist")
	}
	if match.Status != "gerado" || match.VersionID != verID1 {
		t.Fatalf("unexpected match: %+v", match)
	}
	if len(match.Gap.Found) != 1 || match.Gap.Found[0].Term != "AWS" {
		t.Fatalf("expected gap to round-trip, got %+v", match.Gap)
	}

	err = store.upsertJobResumeMatch(jobResumeMatch{
		JobID:     jobID,
		VersionID: verID2,
		Status:    "aplicado",
	})
	if err != nil {
		t.Fatalf("upsertJobResumeMatch (update): %v", err)
	}

	updated, ok, err := store.getJobResumeMatch(jobID)
	if err != nil {
		t.Fatalf("getJobResumeMatch (after update): %v", err)
	}
	if !ok {
		t.Fatal("expected match to still exist after update")
	}
	if updated.Status != "aplicado" || updated.VersionID != verID2 {
		t.Fatalf("expected upsert to update the existing match, got %+v", updated)
	}
}

func TestGetJobResumeMatchNotFound(t *testing.T) {
	store := newTestStore(t)
	_, ok, err := store.getJobResumeMatch("job:none")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected no match to be found")
	}
}

func TestSeedResumeTemplatesIsIdempotent(t *testing.T) {
	store := newTestStore(t)
	if err := store.seedResumeTemplates(); err != nil {
		t.Fatalf("seedResumeTemplates (first): %v", err)
	}
	if err := store.seedResumeTemplates(); err != nil {
		t.Fatalf("seedResumeTemplates (second): %v", err)
	}

	templates, err := store.listResumeTemplates()
	if err != nil {
		t.Fatalf("listResumeTemplates: %v", err)
	}
	if len(templates) != 3 {
		t.Fatalf("expected exactly 3 seeded templates (seeding twice must stay idempotent), got %d: %+v", len(templates), templates)
	}

	byID := make(map[string]resumeTemplate, len(templates))
	for _, tmpl := range templates {
		byID[tmpl.ID] = tmpl
	}
	if tmpl, ok := byID[resumeATSStrictTemplateID]; !ok || !tmpl.IsATS {
		t.Fatalf("expected ATS Strict template to be seeded and ATS-safe: %+v", tmpl)
	}
	if tmpl, ok := byID[resumeATSCleanTemplateID]; !ok || !tmpl.IsATS {
		t.Fatalf("expected ATS Clean template to be seeded and ATS-safe: %+v", tmpl)
	}
	if tmpl, ok := byID[resumeModernAccentTemplateID]; !ok || tmpl.IsATS {
		t.Fatalf("expected Modern Accent template to be seeded and marked non-ATS: %+v", tmpl)
	}
}
