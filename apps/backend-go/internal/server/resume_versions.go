package server

import (
	"net/http"
	"strings"
)

// resumeSaveVersion persists a tailored resume as a resume_versions row
// (canonical + accepted patches + template + scores) and, when the version
// was generated for a specific job, upserts the job_resume_matches row
// (gap + status "gerado") that ties the job to this version — closing the
// per-application history loop (brainstorm §7.10).
func (a *api) resumeSaveVersion(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	var payload saveVersionRequest
	if err := jsonDecodeLimited(r.Body, &payload, maxResumeUploadBytes*2); err != nil {
		writeResumeError(w, resumeErrorFor(http.StatusBadRequest, "invalid_request", "Invalid request body."))
		return
	}
	if strings.TrimSpace(payload.DocumentID) == "" {
		writeResumeError(w, resumeErrorFor(http.StatusBadRequest, "invalid_request", "documentId is required."))
		return
	}

	canonical := normalizeCanonical(payload.Canonical)
	if err := canonical.Validate(); err != nil {
		writeResumeError(w, resumeErrorFor(http.StatusBadRequest, "invalid_request", err.Error()))
		return
	}

	if strings.TrimSpace(payload.TemplateID) != "" {
		if err := a.configStore.seedResumeTemplates(); err != nil {
			a.logger.Printf("seed resume templates before save version: %v", err)
			writeResumeError(w, resumeErrorFor(http.StatusInternalServerError, "internal_error", "Something went wrong on our side. Check the app logs."))
			return
		}
	}

	versionID, err := a.configStore.saveResumeVersion(resumeVersion{
		DocumentID: payload.DocumentID,
		JobID:      payload.JobID,
		Canonical:  canonical,
		Patches:    payload.Patches,
		TemplateID: payload.TemplateID,
		ATSScore:   payload.ATSScore,
		HRScore:    payload.HRScore,
	})
	if err != nil {
		a.logger.Printf("save resume version: %v", err)
		writeResumeError(w, resumeErrorFor(http.StatusInternalServerError, "internal_error", "Something went wrong on our side. Check the app logs."))
		return
	}

	if jobID := strings.TrimSpace(payload.JobID); jobID != "" {
		if err := a.configStore.upsertJobResumeMatch(jobResumeMatch{
			JobID:     jobID,
			VersionID: versionID,
			Gap:       payload.Gap,
			Status:    "gerado",
		}); err != nil {
			a.logger.Printf("upsert job resume match: %v", err)
		}
	}

	writeJSON(w, http.StatusOK, saveVersionResponse{ID: versionID})
}

// resumeVersions lists saved versions by job (application history) or, when
// no job is selected, by base document (pasted-description and generic flows).
func (a *api) resumeVersions(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimSpace(r.URL.Query().Get("jobId"))
	documentID := strings.TrimSpace(r.URL.Query().Get("documentId"))
	if jobID == "" && documentID == "" {
		writeResumeError(w, resumeErrorFor(http.StatusBadRequest, "invalid_request", "jobId or documentId is required."))
		return
	}

	var (
		versions []resumeVersion
		err      error
	)
	if jobID != "" {
		versions, err = a.configStore.listResumeVersionsByJob(jobID)
	} else {
		versions, err = a.configStore.listResumeVersionsByDocument(documentID)
	}
	if err != nil {
		a.logger.Printf("list resume versions: %v", err)
		writeResumeError(w, resumeErrorFor(http.StatusInternalServerError, "internal_error", "Something went wrong on our side. Check the app logs."))
		return
	}
	if versions == nil {
		versions = []resumeVersion{}
	}

	writeJSON(w, http.StatusOK, versionsResponse{Versions: versions})
}

// resumeDeleteVersion removes a saved resume version (CH-05).
func (a *api) resumeDeleteVersion(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	existed, err := a.configStore.deleteResumeVersion(id)
	if err != nil {
		a.logger.Printf("delete resume version: %v", err)
		writeResumeError(w, resumeErrorFor(http.StatusInternalServerError, "internal_error", "Something went wrong on our side. Check the app logs."))
		return
	}
	if !existed {
		writeResumeError(w, resumeErrorFor(http.StatusNotFound, "version_not_found", "Resume version not found."))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// resumeRenameVersion updates the display name of a saved resume version
// (CH-05).
func (a *api) resumeRenameVersion(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	id := strings.TrimSpace(r.PathValue("id"))
	var payload renameVersionRequest
	if err := jsonDecodeLimited(r.Body, &payload, maxResumeUploadBytes*2); err != nil {
		writeResumeError(w, resumeErrorFor(http.StatusBadRequest, "invalid_request", "Invalid request body."))
		return
	}
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		writeResumeError(w, resumeErrorFor(http.StatusBadRequest, "invalid_request", "name is required."))
		return
	}

	existed, err := a.configStore.renameResumeVersion(id, name)
	if err != nil {
		a.logger.Printf("rename resume version: %v", err)
		writeResumeError(w, resumeErrorFor(http.StatusInternalServerError, "internal_error", "Something went wrong on our side. Check the app logs."))
		return
	}
	if !existed {
		writeResumeError(w, resumeErrorFor(http.StatusNotFound, "version_not_found", "Resume version not found."))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "name": name})
}
