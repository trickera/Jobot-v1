package server

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestCoreUserDataSurvivesStoreRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sencia.db")
	t.Setenv("SENCIA_DB_PATH", dbPath)
	store := newConfigStore()

	initial, err := store.load()
	if err != nil {
		t.Fatalf("first-run load: %v", err)
	}
	if initial.LocalItems != (localItems{}) {
		t.Fatalf("first-run database is not empty: %+v", initial.LocalItems)
	}
	if docs, err := store.listResumeDocuments(); err != nil || len(docs) != 0 {
		t.Fatalf("first-run resume documents: %+v err=%v", docs, err)
	}

	config := defaultConfig()
	config.Form.Roles = "DevOps Engineer"
	config.Form.Location = "Chicago"
	config.Form.OnsiteLocation = "Chicago"
	config.Form.RemoteCountry = "United States"
	config.Form.WorkMode = workModeHybrid
	config.Form.Keywords = "AWS, Terraform"
	config.Form.KeywordsForRoles = "DevOps Engineer"
	config.Form.ResumeName = "Jane Doe Resume"
	config.Form.ResumeText = "Jane Doe\nDevOps Engineer\nAWS Terraform"
	config.Toggles["useIndeed"] = false
	config.Toggles["useGupy"] = true
	if err := store.save(config); err != nil {
		t.Fatalf("save config: %v", err)
	}
	persistedConfig, err := store.load()
	if err != nil {
		t.Fatalf("load config before restart: %v", err)
	}
	planBeforeRestart := buildSearchPlan(persistedConfig)

	job := jobPost{
		ID: "linkedin:https://example.com/jobs/restart-1", Source: "LinkedIn",
		Title: "DevOps Engineer", Company: "Acme", Location: "Chicago",
		URL: "https://example.com/jobs/restart-1", Status: statusApply, Score: 91,
		Description: "Operate AWS infrastructure with Terraform.",
	}
	if err := store.saveSearchResults(persistedConfig, []jobPost{job}); err != nil {
		t.Fatalf("save search results: %v", err)
	}
	jobSummary := jobSummary{
		ID: job.ID, Source: job.Source, Title: job.Title, Company: job.Company,
		Location: job.Location, URL: job.URL, Status: job.Status, Score: job.Score,
		Description: job.Description,
	}
	if _, _, err := store.applyJobAction("save", jobSummary); err != nil {
		t.Fatalf("save job: %v", err)
	}

	canonical := sampleCanonicalResume()
	documentID, err := store.saveResumeDocument(canonical, "Jane Doe Resume", "resume.pdf", "pdf")
	if err != nil {
		t.Fatalf("save resume document: %v", err)
	}
	if err := store.seedResumeTemplates(); err != nil {
		t.Fatalf("seed resume templates: %v", err)
	}
	versionID, err := store.saveResumeVersion(resumeVersion{
		Name: "DevOps Engineer at Acme", DocumentID: documentID, JobID: job.ID,
		Canonical: canonical, TemplateID: resumeATSStrictTemplateID, ATSScore: 83, HRScore: 76,
	})
	if err != nil {
		t.Fatalf("save resume version: %v", err)
	}
	if _, _, err := store.applyJobAction("applied", jobSummary); err != nil {
		t.Fatalf("mark job applied: %v", err)
	}

	restarted := newConfigStore()
	if restarted == store || restarted.path != dbPath {
		t.Fatalf("restart did not create a new store over the same file: old=%p new=%p path=%q", store, restarted, restarted.path)
	}

	gotConfig, err := restarted.load()
	if err != nil {
		t.Fatalf("load config after restart: %v", err)
	}
	if gotConfig.Form.Roles != config.Form.Roles || gotConfig.Form.WorkMode != workModeHybrid ||
		gotConfig.Form.OnsiteLocation != config.Form.OnsiteLocation || gotConfig.Form.RemoteCountry != config.Form.RemoteCountry ||
		gotConfig.Form.Keywords != config.Form.Keywords || gotConfig.Form.ResumeName != config.Form.ResumeName ||
		gotConfig.Form.ResumeText != config.Form.ResumeText || gotConfig.Toggles["useIndeed"] || !gotConfig.Toggles["useGupy"] {
		t.Fatalf("config did not survive restart: %+v", gotConfig.Form)
	}
	if planAfterRestart := buildSearchPlan(gotConfig); !reflect.DeepEqual(planAfterRestart, planBeforeRestart) {
		t.Fatalf("effective search plan changed after restart:\nbefore=%+v\nafter=%+v", planBeforeRestart, planAfterRestart)
	}

	gotResume, meta, err := restarted.getResumeDocument(documentID)
	if err != nil {
		t.Fatalf("get resume document after restart: %v", err)
	}
	if meta.ID != documentID || meta.Name != "Jane Doe Resume" || meta.SourceFile != "resume.pdf" ||
		gotResume.Basics.Name != canonical.Basics.Name || gotResume.Target.JobTitle != canonical.Target.JobTitle ||
		!reflect.DeepEqual(gotResume.Skills.Hard, canonical.Skills.Hard) {
		t.Fatalf("resume document did not survive restart: meta=%+v resume=%+v", meta, gotResume)
	}
	versions, err := restarted.listResumeVersionsByJob(job.ID)
	if err != nil {
		t.Fatalf("list resume versions after restart: %v", err)
	}
	if len(versions) != 1 || versions[0].ID != versionID || versions[0].DocumentID != documentID ||
		versions[0].Name != "DevOps Engineer at Acme" || versions[0].ATSScore != 83 || versions[0].HRScore != 76 {
		t.Fatalf("resume version did not survive restart: %+v", versions)
	}

	saved, err := restarted.listSavedJobs(10)
	if err != nil || len(saved) != 1 || saved[0].ID != job.ID || saved[0].SavedAt == "" {
		t.Fatalf("saved job did not survive restart: %+v err=%v", saved, err)
	}
	applications, err := restarted.listApplications(10)
	if err != nil || len(applications) != 1 || applications[0].JobID != job.ID || applications[0].Status != statusApplied {
		t.Fatalf("application did not survive restart: %+v err=%v", applications, err)
	}
	history, err := restarted.listSearchHistory(10)
	if err != nil || len(history) != 1 || history[0].Query != config.Form.Roles || history[0].ResultsCount != 1 ||
		history[0].Filters["workMode"] != workModeHybrid || history[0].Filters["location"] != config.Form.OnsiteLocation {
		t.Fatalf("search history did not survive restart: %+v err=%v", history, err)
	}
	stats, err := restarted.stats()
	if err != nil {
		t.Fatalf("stats after restart: %v", err)
	}
	if stats != (localItems{Jobs: 1, Saved: 1, Applications: 1, History: 1}) || stats != gotConfig.LocalItems {
		t.Fatalf("live counters after restart: stats=%+v config=%+v", stats, gotConfig.LocalItems)
	}
}
