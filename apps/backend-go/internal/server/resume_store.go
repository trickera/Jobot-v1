package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var errResumeDocumentNotFound = errors.New("resume document não encontrado")
var errJobNotFound = errors.New("vaga não encontrada")

func nextResumeID(prefix string) string {
	return fmt.Sprintf("resume:%s:%d", prefix, time.Now().UnixNano())
}

// getJobByID reads a job's stored description from the existing `jobs`
// table (raw_json), so the Resume Studio job analyzer can work off a job
// already collected by the app instead of requiring the description to be
// pasted manually.
func (s *configStore) getJobByID(id string) (jobPost, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return jobPost{}, err
	}
	defer db.Close()

	var (
		job     jobPost
		rawJSON sql.NullString
	)
	err = db.QueryRow(`SELECT title, company, location, url, raw_json FROM jobs WHERE id = ?`, id).
		Scan(&job.Title, &job.Company, &job.Location, &job.URL, &rawJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return jobPost{}, errJobNotFound
	}
	if err != nil {
		return jobPost{}, err
	}
	job.ID = id
	if rawJSON.Valid && rawJSON.String != "" {
		var raw jobPost
		if err := json.Unmarshal([]byte(rawJSON.String), &raw); err == nil {
			job.Description = raw.Description
			job.Source = raw.Source
		}
	}
	return job, nil
}

func (s *configStore) saveResumeDocument(doc CanonicalResume, name, sourceFile, sourceFormat string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return "", err
	}
	defer db.Close()

	doc = normalizeCanonical(doc)
	content, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}

	id := nextResumeID("doc")
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = db.Exec(
		`INSERT INTO resume_documents (id, name, source_file, source_format, canonical_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, name, sourceFile, sourceFormat, string(content), now, now,
	)
	if err != nil {
		return "", err
	}
	return id, nil
}

func (s *configStore) getResumeDocument(id string) (CanonicalResume, docMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return CanonicalResume{}, docMeta{}, err
	}
	defer db.Close()

	var canonicalJSON string
	meta := docMeta{ID: id}
	err = db.QueryRow(
		`SELECT name, source_file, source_format, canonical_json, created_at, updated_at
		 FROM resume_documents WHERE id = ?`,
		id,
	).Scan(&meta.Name, &meta.SourceFile, &meta.SourceFormat, &canonicalJSON, &meta.CreatedAt, &meta.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CanonicalResume{}, docMeta{}, errResumeDocumentNotFound
	}
	if err != nil {
		return CanonicalResume{}, docMeta{}, err
	}

	var canonical CanonicalResume
	if err := json.Unmarshal([]byte(canonicalJSON), &canonical); err != nil {
		return CanonicalResume{}, docMeta{}, err
	}
	return normalizeCanonical(canonical), meta, nil
}

func (s *configStore) listResumeDocuments() ([]docMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(
		`SELECT id, name, source_file, source_format, created_at, updated_at
		 FROM resume_documents ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var docs []docMeta
	for rows.Next() {
		var m docMeta
		if err := rows.Scan(&m.ID, &m.Name, &m.SourceFile, &m.SourceFormat, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		docs = append(docs, m)
	}
	return docs, rows.Err()
}

func (s *configStore) saveResumeVersion(v resumeVersion) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return "", err
	}
	defer db.Close()

	v.Canonical = normalizeCanonical(v.Canonical)
	canonicalJSON, err := json.Marshal(v.Canonical)
	if err != nil {
		return "", err
	}
	patchesJSON, err := json.Marshal(v.Patches)
	if err != nil {
		return "", err
	}

	id := v.ID
	if id == "" {
		id = nextResumeID("ver")
	}
	now := time.Now().UTC().Format(time.RFC3339)

	jobID := sql.NullString{String: v.JobID, Valid: v.JobID != ""}
	templateID := sql.NullString{String: v.TemplateID, Valid: v.TemplateID != ""}

	_, err = db.Exec(
		`INSERT INTO resume_versions (id, name, document_id, job_id, canonical_json, diff_json, template_id, ats_score, hr_score, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, v.Name, v.DocumentID, jobID, string(canonicalJSON), string(patchesJSON), templateID, v.ATSScore, v.HRScore, now,
	)
	if err != nil {
		return "", err
	}
	return id, nil
}

func (s *configStore) listResumeVersionsByJob(jobID string) ([]resumeVersion, error) {
	return s.listResumeVersions(
		`SELECT id, name, document_id, job_id, canonical_json, diff_json, template_id, ats_score, hr_score, created_at
		 FROM resume_versions WHERE job_id = ? ORDER BY created_at DESC`,
		jobID,
	)
}

func (s *configStore) listResumeVersionsByDocument(documentID string) ([]resumeVersion, error) {
	return s.listResumeVersions(
		`SELECT id, name, document_id, job_id, canonical_json, diff_json, template_id, ats_score, hr_score, created_at
		 FROM resume_versions WHERE document_id = ? ORDER BY created_at DESC`,
		documentID,
	)
}

func (s *configStore) listResumeVersions(query, id string) ([]resumeVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(query, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var versions []resumeVersion
	for rows.Next() {
		var (
			v             resumeVersion
			canonicalJSON string
			patchesJSON   sql.NullString
			jobIDCol      sql.NullString
			templateID    sql.NullString
		)
		if err := rows.Scan(&v.ID, &v.Name, &v.DocumentID, &jobIDCol, &canonicalJSON, &patchesJSON, &templateID, &v.ATSScore, &v.HRScore, &v.CreatedAt); err != nil {
			return nil, err
		}
		v.JobID = jobIDCol.String
		v.TemplateID = templateID.String
		if err := json.Unmarshal([]byte(canonicalJSON), &v.Canonical); err != nil {
			return nil, err
		}
		v.Canonical = normalizeCanonical(v.Canonical)
		if patchesJSON.Valid && patchesJSON.String != "" {
			if err := json.Unmarshal([]byte(patchesJSON.String), &v.Patches); err != nil {
				return nil, err
			}
		}
		versions = append(versions, v)
	}
	return versions, rows.Err()
}

// deleteResumeVersion removes a resume_versions row (CH-05). It reports
// whether a row existed so the handler can return a typed 404 instead of a
// silent 204 for an id that was never valid.
func (s *configStore) deleteResumeVersion(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return false, err
	}
	defer db.Close()

	res, err := db.Exec(`DELETE FROM resume_versions WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

// renameResumeVersion updates the display name of a resume_versions row
// (CH-05). It reports whether a row existed for the given id.
func (s *configStore) renameResumeVersion(id, name string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return false, err
	}
	defer db.Close()

	res, err := db.Exec(`UPDATE resume_versions SET name = ? WHERE id = ?`, name, id)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

// insertCoverLetter persists a generated cover letter (CH-06/D8). Callers
// only insert when a jobId is present — cover_letters.job_id is NOT NULL.
func (s *configStore) insertCoverLetter(jobID, versionID, contentMD string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return "", err
	}
	defer db.Close()

	id := nextResumeID("letter")
	now := time.Now().UTC().Format(time.RFC3339)
	versionIDCol := sql.NullString{String: versionID, Valid: versionID != ""}

	_, err = db.Exec(
		`INSERT INTO cover_letters (id, job_id, version_id, content_md, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		id, jobID, versionIDCol, contentMD, now,
	)
	if err != nil {
		return "", err
	}
	return id, nil
}

func (s *configStore) saveResumeAnalysis(a resumeAnalysis) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return err
	}
	defer db.Close()

	scoresJSON, err := json.Marshal(a.Scores)
	if err != nil {
		return err
	}
	issuesJSON, err := json.Marshal(a.Issues)
	if err != nil {
		return err
	}

	id := a.ID
	if id == "" {
		id = nextResumeID("analysis")
	}
	now := time.Now().UTC().Format(time.RFC3339)

	_, err = db.Exec(
		`INSERT INTO resume_analyses (id, subject_kind, subject_id, scores_json, issues_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id, a.SubjectKind, a.SubjectID, string(scoresJSON), string(issuesJSON), now,
	)
	return err
}

// upsertJobResumeMatch keeps one match row per job (deterministic id keyed
// by jobID), updating its version/gap/status when Resume Studio re-runs for
// the same job.
func (s *configStore) upsertJobResumeMatch(m jobResumeMatch) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return err
	}
	defer db.Close()

	gapJSON, err := json.Marshal(m.Gap)
	if err != nil {
		return err
	}

	id := m.ID
	if id == "" {
		id = "resume:match:" + m.JobID
	}
	now := time.Now().UTC().Format(time.RFC3339)
	status := m.Status
	if status == "" {
		status = "gerado"
	}

	_, err = db.Exec(
		`INSERT INTO job_resume_matches (id, job_id, version_id, gap_json, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   version_id = excluded.version_id,
		   gap_json   = excluded.gap_json,
		   status     = excluded.status,
		   updated_at = excluded.updated_at`,
		id, m.JobID, m.VersionID, string(gapJSON), status, now, now,
	)
	return err
}

func (s *configStore) getJobResumeMatch(jobID string) (jobResumeMatch, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return jobResumeMatch{}, false, err
	}
	defer db.Close()

	var (
		m       jobResumeMatch
		gapJSON sql.NullString
	)
	m.JobID = jobID
	err = db.QueryRow(
		`SELECT id, version_id, gap_json, status, created_at, updated_at
		 FROM job_resume_matches WHERE job_id = ? ORDER BY updated_at DESC LIMIT 1`,
		jobID,
	).Scan(&m.ID, &m.VersionID, &gapJSON, &m.Status, &m.CreatedAt, &m.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return jobResumeMatch{}, false, nil
	}
	if err != nil {
		return jobResumeMatch{}, false, err
	}
	if gapJSON.Valid && gapJSON.String != "" {
		if err := json.Unmarshal([]byte(gapJSON.String), &m.Gap); err != nil {
			return jobResumeMatch{}, false, err
		}
	}
	return m, true, nil
}

// resumeATSStrictTemplateID is the default, always-available export
// template (single-column, ATS-safe).
const resumeATSStrictTemplateID = "template:ats-strict"

// resumeATSCleanTemplateID is an ATS-safe template with a lighter,
// accent-colored heading style (single-column, still ATS-safe).
const resumeATSCleanTemplateID = "template:ats-clean"

// resumeModernAccentTemplateID is a visual/premium template (still
// single-column and text-selectable) explicitly marked as "may lower ATS".
const resumeModernAccentTemplateID = "template:modern-accent"

// seedResumeTemplates inserts the built-in template catalog. It is
// idempotent (INSERT OR IGNORE keyed by the stable template id).
func (s *configStore) seedResumeTemplates() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return err
	}
	defer db.Close()

	now := time.Now().UTC().Format(time.RFC3339)
	seeds := []struct {
		id, name, category string
		isATS              bool
	}{
		{resumeATSStrictTemplateID, "ATS Strict", "ats", true},
		{resumeATSCleanTemplateID, "ATS Clean", "ats", true},
		{resumeModernAccentTemplateID, "Modern Accent", "visual", false},
	}
	for _, seed := range seeds {
		isATS := 0
		if seed.isATS {
			isATS = 1
		}
		if _, err := db.Exec(
			`INSERT OR IGNORE INTO resume_templates (id, name, category, engine, spec_json, is_ats, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			seed.id, seed.name, seed.category, "native", "{}", isATS, now,
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *configStore) listResumeTemplates() ([]resumeTemplate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`SELECT id, name, category, engine, is_ats FROM resume_templates ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var templates []resumeTemplate
	for rows.Next() {
		var (
			t     resumeTemplate
			isATS int
		)
		if err := rows.Scan(&t.ID, &t.Name, &t.Category, &t.Engine, &isATS); err != nil {
			return nil, err
		}
		t.IsATS = isATS != 0
		templates = append(templates, t)
	}
	return templates, rows.Err()
}
