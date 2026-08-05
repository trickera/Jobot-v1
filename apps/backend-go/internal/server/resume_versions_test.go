package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newResumeVersionsTestAPI(t *testing.T) (*api, *configStore) {
	t.Helper()
	store := newTestStore(t)
	return &api{logger: log.New(io.Discard, "", 0), configStore: store}, store
}

// The app's central flow, and it was broken: find a job, tailor your resume to it,
// save it. The search streams approved jobs into the list as it finds them — that is
// what the live view is for — but only wrote them to the database once the whole
// sweep had finished. resume_versions.job_id carries a foreign key onto jobs(id), so
// saving a resume tailored to a job clicked out of a still-running search failed with
// "FOREIGN KEY constraint failed", for the entire duration of the search that
// surfaced the job.
//
// Every test around it missed this because each seeds its own job row first — see the
// applyJobAction call in the test below. Working around a row that production never
// created is not the same as the row being there, and only driving the real UI showed
// the difference. So this one seeds nothing: the search alone has to make the save
// legal.
func TestAJobIsSavedTheMomentTheSearchApprovesIt(t *testing.T) {
	store := newTestStore(t)

	approved := jobPost{
		ID:       "linkedin:42",
		Title:    "Senior Infrastructure Specialist",
		Company:  "Acme",
		Location: "Remote",
		Source:   "LinkedIn",
		Status:   statusApply,
		Score:    91,
	}
	if err := store.saveSearchResult(approved); err != nil {
		t.Fatalf("persisting a job the search just approved: %v", err)
	}

	docID, err := store.saveResumeDocument(sampleCanonicalResume(), "Base", "", "")
	if err != nil {
		t.Fatalf("saveResumeDocument: %v", err)
	}
	// The handler seeds these on demand before it saves; this test talks to the
	// store directly, so it has to stand in for that. The job row above is the one
	// thing it deliberately does NOT arrange for.
	if err := store.seedResumeTemplates(); err != nil {
		t.Fatalf("seedResumeTemplates: %v", err)
	}

	versionID, err := store.saveResumeVersion(resumeVersion{
		DocumentID: docID,
		JobID:      approved.ID,
		Canonical:  sampleCanonicalResume(),
		TemplateID: resumeATSStrictTemplateID,
		ATSScore:   84,
		HRScore:    80,
	})
	if err != nil {
		t.Fatalf("a resume tailored to a job the search has already shown the user must be savable: %v", err)
	}

	versions, err := store.listResumeVersionsByJob(approved.ID)
	if err != nil {
		t.Fatalf("listResumeVersionsByJob: %v", err)
	}
	if len(versions) != 1 || versions[0].ID != versionID {
		t.Fatalf("expected the saved version to be listed under its job, got %+v", versions)
	}
}

func TestResumeSaveVersionHandlerPersistsVersionAndMatch(t *testing.T) {
	a, store := newResumeVersionsTestAPI(t)

	docID, err := store.saveResumeDocument(sampleCanonicalResume(), "Base", "", "")
	if err != nil {
		t.Fatalf("saveResumeDocument: %v", err)
	}
	jobID := "job:1"
	if _, _, err := store.applyJobAction("dismiss", jobSummary{ID: jobID, Title: "Test Job", Company: "Acme"}); err != nil {
		t.Fatalf("seed job row: %v", err)
	}
	body, err := json.Marshal(saveVersionRequest{
		DocumentID: docID,
		JobID:      jobID,
		Canonical:  sampleCanonicalResume(),
		Patches:    []jsonPatchOp{{Op: "replace", Path: "/summary", Reason: "align"}},
		TemplateID: resumeATSStrictTemplateID,
		ATSScore:   83,
		HRScore:    76,
		Gap:        gapResult{Found: []gapItem{{Term: "AWS", Evidence: "skills"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/v1/resume/version", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	a.resumeSaveVersion(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp saveVersionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ID == "" {
		t.Fatal("expected a non-empty version id")
	}

	versions, err := store.listResumeVersionsByJob(jobID)
	if err != nil {
		t.Fatalf("listResumeVersionsByJob: %v", err)
	}
	if len(versions) != 1 || versions[0].ID != resp.ID {
		t.Fatalf("expected the saved version to be listed, got %+v", versions)
	}
	if versions[0].ATSScore != 83 || versions[0].HRScore != 76 {
		t.Fatalf("expected scores to persist, got %+v", versions[0])
	}

	match, ok, err := store.getJobResumeMatch(jobID)
	if err != nil {
		t.Fatalf("getJobResumeMatch: %v", err)
	}
	if !ok {
		t.Fatal("expected a job_resume_matches row to be created")
	}
	if match.VersionID != resp.ID || match.Status != "gerado" {
		t.Fatalf("unexpected match: %+v", match)
	}
	if len(match.Gap.Found) != 1 || match.Gap.Found[0].Term != "AWS" {
		t.Fatalf("expected gap to persist, got %+v", match.Gap)
	}
}

func TestResumeSaveVersionHandlerWithoutJobIDSkipsMatch(t *testing.T) {
	a, store := newResumeVersionsTestAPI(t)
	docID, err := store.saveResumeDocument(sampleCanonicalResume(), "Base", "", "")
	if err != nil {
		t.Fatalf("saveResumeDocument: %v", err)
	}

	body, _ := json.Marshal(saveVersionRequest{DocumentID: docID, Canonical: sampleCanonicalResume()})
	req := httptest.NewRequest("POST", "/api/v1/resume/version", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	a.resumeSaveVersion(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var saved saveVersionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	versions, err := store.listResumeVersionsByDocument(docID)
	if err != nil {
		t.Fatalf("listResumeVersionsByDocument: %v", err)
	}
	if len(versions) != 1 || versions[0].ID != saved.ID || versions[0].JobID != "" {
		t.Fatalf("expected the jobless version to remain retrievable by document, got %+v", versions)
	}
}

func TestResumeSaveVersionHandlerRequiresDocumentID(t *testing.T) {
	a, _ := newResumeVersionsTestAPI(t)

	body, _ := json.Marshal(saveVersionRequest{Canonical: sampleCanonicalResume()})
	req := httptest.NewRequest("POST", "/api/v1/resume/version", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	a.resumeSaveVersion(rec, req)

	if rec.Code != 400 {
		t.Fatalf("expected 400 without documentId, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestResumeSaveVersionHandlerRejectsInvalidCanonical(t *testing.T) {
	a, store := newResumeVersionsTestAPI(t)
	docID, err := store.saveResumeDocument(sampleCanonicalResume(), "Base", "", "")
	if err != nil {
		t.Fatalf("saveResumeDocument: %v", err)
	}

	body, _ := json.Marshal(saveVersionRequest{DocumentID: docID, Canonical: CanonicalResume{}})
	req := httptest.NewRequest("POST", "/api/v1/resume/version", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	a.resumeSaveVersion(rec, req)

	if rec.Code != 400 {
		t.Fatalf("expected 400 for a canonical resume with no name, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestResumeVersionsHandlerListsByJob(t *testing.T) {
	a, store := newResumeVersionsTestAPI(t)
	docID, err := store.saveResumeDocument(sampleCanonicalResume(), "Base", "", "")
	if err != nil {
		t.Fatalf("saveResumeDocument: %v", err)
	}
	jobID := "job:1"
	if _, _, err := store.applyJobAction("dismiss", jobSummary{ID: jobID, Title: "Test Job", Company: "Acme"}); err != nil {
		t.Fatalf("seed job row: %v", err)
	}
	if _, err := store.saveResumeVersion(resumeVersion{DocumentID: docID, JobID: jobID, Canonical: sampleCanonicalResume()}); err != nil {
		t.Fatalf("saveResumeVersion: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/v1/resume/versions?jobId="+jobID, nil)
	rec := httptest.NewRecorder()
	a.resumeVersions(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp versionsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Versions) != 1 {
		t.Fatalf("expected 1 version, got %+v", resp.Versions)
	}
}

func TestResumeVersionsHandlerListsByDocumentWhenNoJob(t *testing.T) {
	a, store := newResumeVersionsTestAPI(t)
	docID, err := store.saveResumeDocument(sampleCanonicalResume(), "Base", "", "")
	if err != nil {
		t.Fatalf("saveResumeDocument: %v", err)
	}
	wantedID, err := store.saveResumeVersion(resumeVersion{DocumentID: docID, Canonical: sampleCanonicalResume()})
	if err != nil {
		t.Fatalf("saveResumeVersion: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/v1/resume/versions?documentId="+docID, nil)
	rec := httptest.NewRecorder()
	a.resumeVersions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp versionsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Versions) != 1 || resp.Versions[0].ID != wantedID {
		t.Fatalf("expected document version %q, got %+v", wantedID, resp.Versions)
	}
}

func TestDeleteResumeVersionRemovesRow(t *testing.T) {
	store := newTestStore(t)
	docID, err := store.saveResumeDocument(sampleCanonicalResume(), "Base", "", "")
	if err != nil {
		t.Fatalf("saveResumeDocument: %v", err)
	}
	jobID := "job:1"
	if _, _, err := store.applyJobAction("dismiss", jobSummary{ID: jobID, Title: "Test Job", Company: "Acme"}); err != nil {
		t.Fatalf("seed job row: %v", err)
	}
	verID, err := store.saveResumeVersion(resumeVersion{DocumentID: docID, JobID: jobID, Canonical: sampleCanonicalResume()})
	if err != nil {
		t.Fatalf("saveResumeVersion: %v", err)
	}

	existed, err := store.deleteResumeVersion(verID)
	if err != nil {
		t.Fatalf("deleteResumeVersion: %v", err)
	}
	if !existed {
		t.Fatal("expected the version to have existed before delete")
	}

	versions, err := store.listResumeVersionsByJob(jobID)
	if err != nil {
		t.Fatalf("listResumeVersionsByJob: %v", err)
	}
	if len(versions) != 0 {
		t.Fatalf("expected the version to be gone, got %+v", versions)
	}

	existed, err = store.deleteResumeVersion(verID)
	if err != nil {
		t.Fatalf("deleteResumeVersion (again): %v", err)
	}
	if existed {
		t.Fatal("expected deleting an already-deleted id to report false")
	}

	existed, err = store.deleteResumeVersion("resume:ver:does-not-exist")
	if err != nil {
		t.Fatalf("deleteResumeVersion (unknown id): %v", err)
	}
	if existed {
		t.Fatal("expected deleting an unknown id to report false")
	}
}

func TestRenameResumeVersionUpdatesName(t *testing.T) {
	store := newTestStore(t)
	docID, err := store.saveResumeDocument(sampleCanonicalResume(), "Base", "", "")
	if err != nil {
		t.Fatalf("saveResumeDocument: %v", err)
	}
	jobID := "job:1"
	if _, _, err := store.applyJobAction("dismiss", jobSummary{ID: jobID, Title: "Test Job", Company: "Acme"}); err != nil {
		t.Fatalf("seed job row: %v", err)
	}
	verID, err := store.saveResumeVersion(resumeVersion{DocumentID: docID, JobID: jobID, Canonical: sampleCanonicalResume()})
	if err != nil {
		t.Fatalf("saveResumeVersion: %v", err)
	}

	existed, err := store.renameResumeVersion(verID, "Tailored for Acme")
	if err != nil {
		t.Fatalf("renameResumeVersion: %v", err)
	}
	if !existed {
		t.Fatal("expected the version to have existed before rename")
	}

	versions, err := store.listResumeVersionsByJob(jobID)
	if err != nil {
		t.Fatalf("listResumeVersionsByJob: %v", err)
	}
	if len(versions) != 1 || versions[0].Name != "Tailored for Acme" {
		t.Fatalf("expected renamed version, got %+v", versions)
	}

	existed, err = store.renameResumeVersion("resume:ver:does-not-exist", "Whatever")
	if err != nil {
		t.Fatalf("renameResumeVersion (unknown id): %v", err)
	}
	if existed {
		t.Fatal("expected renaming an unknown id to report false")
	}
}

func TestResumeDeleteVersionHandlerRemovesVersionAnd204(t *testing.T) {
	a, store := newResumeVersionsTestAPI(t)
	docID, err := store.saveResumeDocument(sampleCanonicalResume(), "Base", "", "")
	if err != nil {
		t.Fatalf("saveResumeDocument: %v", err)
	}
	verID, err := store.saveResumeVersion(resumeVersion{DocumentID: docID, Canonical: sampleCanonicalResume()})
	if err != nil {
		t.Fatalf("saveResumeVersion: %v", err)
	}

	req := httptest.NewRequest("DELETE", "/api/v1/resume/versions/"+verID, nil)
	req.SetPathValue("id", verID)
	rec := httptest.NewRecorder()
	a.resumeDeleteVersion(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestResumeDeleteVersionHandlerUnknownIDReturns404(t *testing.T) {
	a, _ := newResumeVersionsTestAPI(t)

	req := httptest.NewRequest("DELETE", "/api/v1/resume/versions/resume:ver:missing", nil)
	req.SetPathValue("id", "resume:ver:missing")
	rec := httptest.NewRecorder()
	a.resumeDeleteVersion(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["code"] != "version_not_found" {
		t.Fatalf("expected version_not_found code, got %+v", body)
	}
}

func TestResumeRenameVersionHandlerUpdatesNameAnd200(t *testing.T) {
	a, store := newResumeVersionsTestAPI(t)
	docID, err := store.saveResumeDocument(sampleCanonicalResume(), "Base", "", "")
	if err != nil {
		t.Fatalf("saveResumeDocument: %v", err)
	}
	jobID := "job:1"
	if _, _, err := store.applyJobAction("dismiss", jobSummary{ID: jobID, Title: "Test Job", Company: "Acme"}); err != nil {
		t.Fatalf("seed job row: %v", err)
	}
	verID, err := store.saveResumeVersion(resumeVersion{DocumentID: docID, JobID: jobID, Canonical: sampleCanonicalResume()})
	if err != nil {
		t.Fatalf("saveResumeVersion: %v", err)
	}

	body, _ := json.Marshal(renameVersionRequest{Name: "Tailored for Acme"})
	req := httptest.NewRequest("PATCH", "/api/v1/resume/versions/"+verID, bytes.NewReader(body))
	req.SetPathValue("id", verID)
	rec := httptest.NewRecorder()
	a.resumeRenameVersion(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	versions, err := store.listResumeVersionsByJob(jobID)
	if err != nil {
		t.Fatalf("listResumeVersionsByJob: %v", err)
	}
	if len(versions) != 1 || versions[0].Name != "Tailored for Acme" {
		t.Fatalf("expected renamed version, got %+v", versions)
	}
}

func TestResumeRenameVersionHandlerRequiresName(t *testing.T) {
	a, store := newResumeVersionsTestAPI(t)
	docID, err := store.saveResumeDocument(sampleCanonicalResume(), "Base", "", "")
	if err != nil {
		t.Fatalf("saveResumeDocument: %v", err)
	}
	verID, err := store.saveResumeVersion(resumeVersion{DocumentID: docID, Canonical: sampleCanonicalResume()})
	if err != nil {
		t.Fatalf("saveResumeVersion: %v", err)
	}

	body, _ := json.Marshal(renameVersionRequest{Name: "  "})
	req := httptest.NewRequest("PATCH", "/api/v1/resume/versions/"+verID, bytes.NewReader(body))
	req.SetPathValue("id", verID)
	rec := httptest.NewRecorder()
	a.resumeRenameVersion(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestResumeRenameVersionHandlerUnknownIDReturns404(t *testing.T) {
	a, _ := newResumeVersionsTestAPI(t)

	body, _ := json.Marshal(renameVersionRequest{Name: "Whatever"})
	req := httptest.NewRequest("PATCH", "/api/v1/resume/versions/resume:ver:missing", bytes.NewReader(body))
	req.SetPathValue("id", "resume:ver:missing")
	rec := httptest.NewRecorder()
	a.resumeRenameVersion(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestResumeVersionsHandlerRequiresJobIDOrDocumentID(t *testing.T) {
	a, _ := newResumeVersionsTestAPI(t)
	req := httptest.NewRequest("GET", "/api/v1/resume/versions", nil)
	rec := httptest.NewRecorder()
	a.resumeVersions(rec, req)

	if rec.Code != 400 {
		t.Fatalf("expected 400 without jobId or documentId, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// Marking a job "applied" (the pre-existing job action, not Resume Studio
// specific) must flip any Resume Studio match already generated for that
// job to status "aplicado" — closing the per-application history loop.
func TestApplyJobActionAppliedUpdatesResumeMatchStatus(t *testing.T) {
	store := newTestStore(t)
	jobID := "job:1"
	if _, _, err := store.applyJobAction("dismiss", jobSummary{ID: jobID, Title: "Test Job", Company: "Acme"}); err != nil {
		t.Fatalf("seed job row: %v", err)
	}
	docID, err := store.saveResumeDocument(sampleCanonicalResume(), "Base", "", "")
	if err != nil {
		t.Fatalf("saveResumeDocument: %v", err)
	}
	verID, err := store.saveResumeVersion(resumeVersion{DocumentID: docID, JobID: jobID, Canonical: sampleCanonicalResume()})
	if err != nil {
		t.Fatalf("saveResumeVersion: %v", err)
	}
	if err := store.upsertJobResumeMatch(jobResumeMatch{JobID: jobID, VersionID: verID, Status: "gerado"}); err != nil {
		t.Fatalf("upsertJobResumeMatch: %v", err)
	}

	if _, _, err := store.applyJobAction("applied", jobSummary{ID: jobID, Title: "Test Job", Company: "Acme"}); err != nil {
		t.Fatalf("applyJobAction(applied): %v", err)
	}

	match, ok, err := store.getJobResumeMatch(jobID)
	if err != nil {
		t.Fatalf("getJobResumeMatch: %v", err)
	}
	if !ok {
		t.Fatal("expected match to still exist")
	}
	if match.Status != "aplicado" {
		t.Fatalf("expected match status updated to aplicado, got %q", match.Status)
	}
}

func TestApplyJobActionAppliedWithoutMatchIsNoOp(t *testing.T) {
	store := newTestStore(t)
	jobID := "job:no-match"
	if _, _, err := store.applyJobAction("applied", jobSummary{ID: jobID, Title: "Test Job", Company: "Acme"}); err != nil {
		t.Fatalf("applyJobAction(applied) without a prior match: %v", err)
	}
	if _, ok, err := store.getJobResumeMatch(jobID); err != nil || ok {
		t.Fatalf("expected no match to be created, ok=%v err=%v", ok, err)
	}
}
