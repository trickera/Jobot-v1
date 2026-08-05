package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const configSettingKey = "app_config"
const apiKeySecretKey = "ai_api_key"

type configStore struct {
	mu   sync.Mutex
	path string
}

func newConfigStore() *configStore {
	if path := strings.TrimSpace(os.Getenv("SENCIA_DB_PATH")); path != "" {
		return &configStore{path: path}
	}
	if path := strings.TrimSpace(os.Getenv("SENCIA_CONFIG_PATH")); path != "" {
		return &configStore{path: strings.TrimSuffix(path, filepath.Ext(path)) + ".db"}
	}

	baseDir, err := os.UserConfigDir()
	if err != nil || baseDir == "" {
		baseDir = "."
	}
	return &configStore{path: filepath.Join(baseDir, "Sencia Job", "sencia.db")}
}

func (s *configStore) load() (appConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return appConfig{}, err
	}
	defer db.Close()

	var raw string
	err = db.QueryRow(`SELECT value FROM settings WHERE key = ?`, configSettingKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		config := s.initialConfig()
		if strings.TrimSpace(config.Form.APIKey) != "" {
			if err := s.saveSecretLocked(db, apiKeySecretKey, config.Form.APIKey); err != nil {
				return appConfig{}, err
			}
			if err := s.saveSecretLocked(db, providerAPIKeySecretKey(config.Form.Provider), config.Form.APIKey); err != nil {
				return appConfig{}, err
			}
			config.APIKeySet = true
		}
		config.Form.APIKey = ""
		if err := s.saveLocked(db, config); err != nil {
			return appConfig{}, err
		}
		if stats, statsErr := statsLocked(db); statsErr == nil {
			config.LocalItems = stats
		}
		return config, nil
	}
	if err != nil {
		return appConfig{}, err
	}

	var config appConfig
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return appConfig{}, err
	}
	config = normalizeConfig(config)
	if len(config.Notices) > 0 {
		// Persist the replacement immediately so a retired model cannot return on
		// every restart. saveLocked strips response-only Notices from the blob.
		if err := s.saveLocked(db, config); err != nil {
			return appConfig{}, err
		}
	}
	config.Form.APIKey = ""
	config.APIKeySet = s.secretExistsLocked(db, apiKeySecretKey) || s.secretExistsLocked(db, providerAPIKeySecretKey(config.Form.Provider))
	// LocalItems must always reflect the real jobs/applications/search_history
	// tables, never whatever the client last echoed back through PUT /config -
	// otherwise "Banco local" counters look stuck at 0 even after actions that
	// persisted correctly (BUG-02). Best-effort: keep the persisted value if the
	// live count query fails for some reason.
	if stats, statsErr := statsLocked(db); statsErr == nil {
		config.LocalItems = stats
	}
	return config, nil
}

func (s *configStore) save(config appConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return err
	}
	defer db.Close()

	config = normalizeConfig(config)
	if strings.TrimSpace(config.Form.APIKey) != "" {
		if err := s.saveSecretLocked(db, apiKeySecretKey, config.Form.APIKey); err != nil {
			return err
		}
		if err := s.saveSecretLocked(db, providerAPIKeySecretKey(config.Form.Provider), config.Form.APIKey); err != nil {
			return err
		}
		config.APIKeySet = true
	} else if !config.APIKeySet {
		if err := s.deleteSecretLocked(db, apiKeySecretKey); err != nil {
			return err
		}
		if err := s.deleteSecretLocked(db, providerAPIKeySecretKey(config.Form.Provider)); err != nil {
			return err
		}
	} else if s.secretExistsLocked(db, apiKeySecretKey) || s.secretExistsLocked(db, providerAPIKeySecretKey(config.Form.Provider)) {
		config.APIKeySet = true
	}
	config.Form.APIKey = ""
	return s.saveLocked(db, config)
}

func (s *configStore) open() (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", s.path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA foreign_keys = ON; PRAGMA busy_timeout = 5000;`); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func (s *configStore) saveLocked(db *sql.DB, config appConfig) error {
	config.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	config.Form.APIKey = ""
	config.Notices = nil
	content, err := json.Marshal(config)
	if err != nil {
		return err
	}

	_, err = db.Exec(
		`INSERT INTO settings (key, value, updated_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		configSettingKey,
		string(content),
		config.UpdatedAt,
	)
	return err
}

func (s *configStore) stats() (localItems, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return localItems{}, err
	}
	defer db.Close()

	return statsLocked(db)
}

// statsLocked computes local counters from an already-open db handle. It must
// only be called while s.mu is held (directly, or via stats()) so concurrent
// callers never see a torn read across the three counts.
func statsLocked(db *sql.DB) (localItems, error) {
	var stats localItems
	if err := db.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&stats.Jobs); err != nil {
		return localItems{}, err
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE saved_at IS NOT NULL`).Scan(&stats.Saved); err != nil {
		return localItems{}, err
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM applications`).Scan(&stats.Applications); err != nil {
		return localItems{}, err
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM search_history`).Scan(&stats.History); err != nil {
		return localItems{}, err
	}
	return stats, nil
}

func (s *configStore) listRecentJobs(limit int) ([]jobSummary, error) {
	if limit <= 0 {
		limit = 100
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(
		`SELECT id, title, company, location, url, status, score, score_source, raw_json, saved_at
		 FROM jobs
		 WHERE status IN (?, ?)
		 ORDER BY score DESC, updated_at DESC
		 LIMIT ?`,
		statusApply,
		statusAdjust,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	jobs := make([]jobSummary, 0, limit)
	for rows.Next() {
		var (
			item    jobSummary
			rawJSON string
		)
		var savedAt sql.NullString
		if err := rows.Scan(&item.ID, &item.Title, &item.Company, &item.Location, &item.URL, &item.Status, &item.Score, &item.ScoreSource, &rawJSON, &savedAt); err != nil {
			return nil, err
		}
		item.SavedAt = savedAt.String
		var raw jobPost
		if err := json.Unmarshal([]byte(rawJSON), &raw); err == nil {
			item.Source = raw.Source
			item.MissingKeywords = raw.Missing
			// Carry the full job description so the Resume Studio job picker can
			// prefill the "Job description" textarea when a saved job is chosen
			// (matches jobSummaryByIDTx, which the analyze/gap paths rely on).
			item.Description = raw.Description
			item.Profile = raw.Profile
			item.ScoreReason = raw.ScoreReason
			item.ScoringPending = raw.ScoringPending
		}
		jobs = append(jobs, item)
	}
	return jobs, rows.Err()
}

func (s *configStore) listLowScoreJobs(limit int) ([]jobSummary, error) {
	if limit <= 0 {
		limit = 100
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(
		`SELECT id, title, company, location, url, status, score, score_source, raw_json, saved_at
		 FROM jobs
		 WHERE status = ?
		 ORDER BY score DESC, updated_at DESC
		 LIMIT ?`,
		statusDiscard,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	jobs := make([]jobSummary, 0, limit)
	for rows.Next() {
		var (
			item    jobSummary
			rawJSON string
		)
		var savedAt sql.NullString
		if err := rows.Scan(&item.ID, &item.Title, &item.Company, &item.Location, &item.URL, &item.Status, &item.Score, &item.ScoreSource, &rawJSON, &savedAt); err != nil {
			return nil, err
		}
		item.SavedAt = savedAt.String
		var raw jobPost
		if err := json.Unmarshal([]byte(rawJSON), &raw); err == nil {
			item.Source = raw.Source
			item.MissingKeywords = raw.Missing
			item.Description = raw.Description
			item.Profile = raw.Profile
			item.ScoreReason = raw.ScoreReason
			item.ScoringPending = raw.ScoringPending
		}
		jobs = append(jobs, item)
	}
	return jobs, rows.Err()
}

func (s *configStore) listSavedJobs(limit int) ([]jobSummary, error) {
	if limit <= 0 {
		limit = 100
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(
		`SELECT id, title, company, location, url, status, score, score_source, raw_json, saved_at
		 FROM jobs WHERE saved_at IS NOT NULL ORDER BY saved_at DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	jobs := make([]jobSummary, 0, limit)
	for rows.Next() {
		var item jobSummary
		var rawJSON string
		if err := rows.Scan(&item.ID, &item.Title, &item.Company, &item.Location, &item.URL, &item.Status, &item.Score, &item.ScoreSource, &rawJSON, &item.SavedAt); err != nil {
			return nil, err
		}
		var raw jobPost
		if err := json.Unmarshal([]byte(rawJSON), &raw); err == nil {
			item.Source = raw.Source
			item.MissingKeywords = raw.Missing
			item.Description = raw.Description
			item.Profile = raw.Profile
			item.ScoreReason = raw.ScoreReason
		}
		jobs = append(jobs, item)
	}
	return jobs, rows.Err()
}

func (s *configStore) listApplications(limit int) ([]application, error) {
	if limit <= 0 {
		limit = 100
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(
		`SELECT a.id, a.job_id, a.status, a.notes, a.created_at, a.updated_at,
		        j.title, j.company, j.location, j.url, j.status, j.score,
		        j.score_source, j.raw_json, j.saved_at
		 FROM applications a JOIN jobs j ON j.id = a.job_id
		 ORDER BY a.updated_at DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]application, 0, limit)
	for rows.Next() {
		var item application
		var rawJSON string
		var notes sql.NullString
		var savedAt sql.NullString
		if err := rows.Scan(
			&item.ID, &item.JobID, &item.Status, &notes, &item.CreatedAt, &item.UpdatedAt,
			&item.Job.Title, &item.Job.Company, &item.Job.Location, &item.Job.URL,
			&item.Job.Status, &item.Job.Score, &item.Job.ScoreSource, &rawJSON, &savedAt,
		); err != nil {
			return nil, err
		}
		item.Notes = notes.String
		item.Job.ID = item.JobID
		item.Job.SavedAt = savedAt.String
		var raw jobPost
		if err := json.Unmarshal([]byte(rawJSON), &raw); err == nil {
			item.Job.Source = raw.Source
			item.Job.MissingKeywords = raw.Missing
			item.Job.Description = raw.Description
			item.Job.Profile = raw.Profile
			item.Job.ScoreReason = raw.ScoreReason
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *configStore) listSearchHistory(limit int) ([]searchHistoryEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(
		`SELECT id, query, filters_json, results_count, created_at
		 FROM search_history ORDER BY created_at DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]searchHistoryEntry, 0, limit)
	for rows.Next() {
		var item searchHistoryEntry
		var filtersJSON string
		if err := rows.Scan(&item.ID, &item.Query, &filtersJSON, &item.ResultsCount, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.Filters = map[string]any{}
		_ = json.Unmarshal([]byte(filtersJSON), &item.Filters)
		items = append(items, item)
	}
	return items, rows.Err()
}

// processedKeys returns title+company keys for jobs that already have an
// application row, so a new search/radar sweep can skip them before spending
// any network or LLM cost. Best-effort: returns an empty set on any error.
func (s *configStore) processedKeys() map[string]bool {
	keys := map[string]bool{}
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return keys
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT id, title, company FROM jobs WHERE status IN (?, ?)
		UNION
		SELECT j.id, j.title, j.company FROM applications a JOIN jobs j ON j.id = a.job_id`,
		statusApplied,
		statusDismissed,
	)
	if err != nil {
		return keys
	}
	defer rows.Close()
	for rows.Next() {
		var id, title, company sql.NullString
		if err := rows.Scan(&id, &title, &company); err == nil {
			keys[titleCompanyKey(title.String, company.String)] = true
			// The stable URL-based job ID is the robust identity: it matches
			// even when the site tweaks the title text between runs, which
			// title+company alone would miss.
			if key := processedIDKey(id.String); key != "" {
				keys[key] = true
			}
		}
	}
	return keys
}

// processedIDKey namespaces a stable job ID so it never collides with a
// title+company key in the processed set. Returns "" for empty IDs.
func processedIDKey(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return ""
	}
	return "id\x00" + id
}

func (s *configStore) applyJobAction(action string, job jobSummary) (string, string, error) {
	action = normalizeJobAction(action)
	if action == "" {
		return "", "", fmt.Errorf("Acao de vaga desconhecida.")
	}
	job.ID = strings.TrimSpace(job.ID)
	if job.ID == "" {
		return "", "", fmt.Errorf("Vaga sem ID para persistir a acao.")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return "", "", err
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()

	if strings.TrimSpace(job.Title) != "" {
		if err := upsertJobSummaryTx(tx, job); err != nil {
			return "", "", err
		}
	}

	if strings.TrimSpace(job.Company) == "" {
		loaded, err := jobSummaryByIDTx(tx, job.ID)
		if err == nil {
			job = mergeJobSummary(job, loaded)
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	switch action {
	case "save":
		if _, err := tx.Exec(`UPDATE jobs SET saved_at = ?, updated_at = ? WHERE id = ?`, now, now, job.ID); err != nil {
			return "", "", err
		}
		if err := tx.Commit(); err != nil {
			return "", "", err
		}
		return "Vaga salva para revisar depois.", job.Company, nil
	case "unsave":
		if _, err := tx.Exec(`UPDATE jobs SET saved_at = NULL, updated_at = ? WHERE id = ?`, now, job.ID); err != nil {
			return "", "", err
		}
		if err := tx.Commit(); err != nil {
			return "", "", err
		}
		return "Vaga removida das salvas.", job.Company, nil
	case "applied":
		if _, err := tx.Exec(`UPDATE jobs SET status = ?, updated_at = ? WHERE id = ?`, statusApplied, now, job.ID); err != nil {
			return "", "", err
		}
		if _, err := tx.Exec(
			`INSERT INTO applications (id, job_id, status, notes, created_at, updated_at)
			 VALUES (?, ?, ?, '', ?, ?)
			 ON CONFLICT(id) DO UPDATE SET status = excluded.status, updated_at = excluded.updated_at`,
			"application:"+job.ID,
			job.ID,
			statusApplied,
			now,
			now,
		); err != nil {
			return "", "", err
		}
		// Reflect the "applied" action onto any Resume Studio match already
		// generated for this job (brainstorm §7.10 history). A no-op UPDATE
		// when no match exists for the job.
		if _, err := tx.Exec(
			`UPDATE job_resume_matches SET status = ?, updated_at = ? WHERE job_id = ?`,
			"aplicado", now, job.ID,
		); err != nil {
			return "", "", err
		}
		if err := tx.Commit(); err != nil {
			return "", "", err
		}
		return "Candidatura marcada como aplicada.", job.Company, nil
	case "dismiss":
		if _, err := tx.Exec(`UPDATE jobs SET status = ?, updated_at = ? WHERE id = ?`, statusDismissed, now, job.ID); err != nil {
			return "", "", err
		}
		if err := tx.Commit(); err != nil {
			return "", "", err
		}
		return "Vaga dispensada e ocultada das proximas buscas.", job.Company, nil
	case "blacklist":
		company := strings.TrimSpace(job.Company)
		if company == "" {
			return "", "", fmt.Errorf("Nao foi possivel identificar a empresa para blacklist.")
		}
		if _, err := tx.Exec(`UPDATE jobs SET status = ?, updated_at = ? WHERE lower(trim(company)) = lower(trim(?))`, statusDismissed, now, company); err != nil {
			return "", "", err
		}
		config, err := loadConfigLocked(tx)
		if err != nil {
			return "", "", err
		}
		if !csvContains(config.Form.Blacklist, company) {
			config.Form.Blacklist = appendCSV(config.Form.Blacklist, company)
		}
		config = normalizeConfig(config)
		config.Form.APIKey = ""
		content, err := json.Marshal(config)
		if err != nil {
			return "", "", err
		}
		if _, err := tx.Exec(
			`INSERT INTO settings (key, value, updated_at)
			 VALUES (?, ?, ?)
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			configSettingKey,
			string(content),
			now,
		); err != nil {
			return "", "", err
		}
		if err := tx.Commit(); err != nil {
			return "", "", err
		}
		return fmt.Sprintf("%s adicionada a blacklist.", company), company, nil
	default:
		return "", "", fmt.Errorf("Acao de vaga desconhecida.")
	}
}

func normalizeJobAction(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "applied", "apply", "mark_applied", "mark-applied":
		return "applied"
	case "dismiss", "dismissed", "discard":
		return "dismiss"
	case "blacklist", "blacklist_company", "blacklist-company":
		return "blacklist"
	case "save", "saved", "bookmark":
		return "save"
	case "unsave", "remove_saved", "remove-saved":
		return "unsave"
	default:
		return ""
	}
}

func upsertJobSummaryTx(tx *sql.Tx, job jobSummary) error {
	now := time.Now().UTC().Format(time.RFC3339)
	sourceID := strings.ToLower(strings.TrimSpace(job.Source))
	if sourceID == "" {
		sourceID = "unknown"
	}
	if _, err := tx.Exec(
		`INSERT INTO job_sources (id, name, kind, enabled, created_at, updated_at)
		 VALUES (?, ?, 'scraper', 1, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET name = excluded.name, enabled = 1, updated_at = excluded.updated_at`,
		sourceID,
		coalesce(job.Source, "Unknown"),
		now,
		now,
	); err != nil {
		return err
	}
	raw, err := json.Marshal(jobPost{
		ID:          job.ID,
		Source:      job.Source,
		Title:       job.Title,
		Company:     job.Company,
		Location:    job.Location,
		URL:         job.URL,
		Description: job.Description,
		Status:      job.Status,
		Score:       job.Score,
		Missing:     job.MissingKeywords,
		Profile:     job.Profile,
		ScoreSource: job.ScoreSource,
		ScoreReason: job.ScoreReason,
	})
	if err != nil {
		return err
	}
	_, err = tx.Exec(
		`INSERT INTO jobs (id, source_id, title, company, location, url, status, score, score_source, raw_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
			source_id = excluded.source_id,
			title = excluded.title,
			company = excluded.company,
			location = excluded.location,
			url = excluded.url,
			score = excluded.score,
			score_source = excluded.score_source,
			raw_json = excluded.raw_json,
			updated_at = excluded.updated_at`,
		job.ID,
		sourceID,
		job.Title,
		job.Company,
		job.Location,
		job.URL,
		coalesce(job.Status, statusApply),
		boundedScore(job.Score),
		job.ScoreSource,
		string(raw),
		now,
		now,
	)
	return err
}

func jobSummaryByIDTx(tx *sql.Tx, id string) (jobSummary, error) {
	var item jobSummary
	var rawJSON string
	var savedAt sql.NullString
	err := tx.QueryRow(`SELECT id, title, company, location, url, status, score, score_source, raw_json, saved_at FROM jobs WHERE id = ?`, id).Scan(
		&item.ID,
		&item.Title,
		&item.Company,
		&item.Location,
		&item.URL,
		&item.Status,
		&item.Score,
		&item.ScoreSource,
		&rawJSON,
		&savedAt,
	)
	if err != nil {
		return item, err
	}
	item.SavedAt = savedAt.String
	var raw jobPost
	if err := json.Unmarshal([]byte(rawJSON), &raw); err == nil {
		item.Source = raw.Source
		item.MissingKeywords = raw.Missing
		item.Description = raw.Description
		item.Profile = raw.Profile
		item.ScoreReason = raw.ScoreReason
		item.ScoringPending = raw.ScoringPending
	}
	return item, nil
}

func mergeJobSummary(primary jobSummary, fallback jobSummary) jobSummary {
	if strings.TrimSpace(primary.ID) == "" {
		primary.ID = fallback.ID
	}
	if strings.TrimSpace(primary.Source) == "" {
		primary.Source = fallback.Source
	}
	if strings.TrimSpace(primary.Title) == "" {
		primary.Title = fallback.Title
	}
	if strings.TrimSpace(primary.Company) == "" {
		primary.Company = fallback.Company
	}
	if strings.TrimSpace(primary.Location) == "" {
		primary.Location = fallback.Location
	}
	if strings.TrimSpace(primary.URL) == "" {
		primary.URL = fallback.URL
	}
	if strings.TrimSpace(primary.Status) == "" {
		primary.Status = fallback.Status
	}
	if primary.Score == 0 {
		primary.Score = fallback.Score
	}
	if len(primary.MissingKeywords) == 0 {
		primary.MissingKeywords = fallback.MissingKeywords
	}
	if strings.TrimSpace(primary.Description) == "" {
		primary.Description = fallback.Description
	}
	if strings.TrimSpace(primary.Profile) == "" {
		primary.Profile = fallback.Profile
	}
	if primary.ScoreSource == "" {
		primary.ScoreSource = fallback.ScoreSource
	}
	if strings.TrimSpace(primary.ScoreReason) == "" {
		primary.ScoreReason = fallback.ScoreReason
	}
	if strings.TrimSpace(primary.SavedAt) == "" {
		primary.SavedAt = fallback.SavedAt
	}
	return primary
}

func loadConfigLocked(tx *sql.Tx) (appConfig, error) {
	var raw string
	err := tx.QueryRow(`SELECT value FROM settings WHERE key = ?`, configSettingKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultConfig(), nil
	}
	if err != nil {
		return appConfig{}, err
	}
	var config appConfig
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return appConfig{}, err
	}
	return normalizeConfig(config), nil
}

func csvContains(csv string, value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return true
	}
	for _, item := range splitCSV(csv) {
		if strings.EqualFold(strings.TrimSpace(item), value) {
			return true
		}
	}
	return false
}

func appendCSV(csv string, value string) string {
	value = strings.TrimSpace(value)
	if strings.TrimSpace(csv) == "" {
		return value
	}
	return strings.TrimRight(strings.TrimSpace(csv), ",") + ", " + value
}

func (s *configStore) aiAPIKey() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return "", err
	}
	defer db.Close()
	return s.loadSecretLocked(db, apiKeySecretKey)
}

func (s *configStore) setAIAPIKey(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return err
	}
	defer db.Close()
	key = strings.TrimSpace(key)
	if key == "" {
		return s.deleteSecretLocked(db, apiKeySecretKey)
	}
	return s.saveSecretLocked(db, apiKeySecretKey, key)
}

func (s *configStore) aiAPIKeyForProvider(provider string) (string, error) {
	provider = strings.TrimSpace(provider)
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return "", err
	}
	defer db.Close()

	if provider != "" {
		key, err := s.loadSecretLocked(db, providerAPIKeySecretKey(provider))
		if err != nil || strings.TrimSpace(key) != "" {
			return key, err
		}
	}

	config, err := s.loadConfigFromDBLocked(db)
	if err != nil {
		return "", err
	}
	if sameProvider(provider, config.Form.Provider) {
		return s.loadSecretLocked(db, apiKeySecretKey)
	}
	return "", nil
}

func (s *configStore) setAIAPIKeyForProvider(provider, key string) error {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return fmt.Errorf("provider is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return err
	}
	defer db.Close()
	key = strings.TrimSpace(key)
	if key == "" {
		return s.deleteSecretLocked(db, providerAPIKeySecretKey(provider))
	}
	return s.saveSecretLocked(db, providerAPIKeySecretKey(provider), key)
}

func (s *configStore) loadConfigFromDBLocked(db *sql.DB) (appConfig, error) {
	var raw string
	err := db.QueryRow(`SELECT value FROM settings WHERE key = ?`, configSettingKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultConfig(), nil
	}
	if err != nil {
		return appConfig{}, err
	}
	var config appConfig
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return appConfig{}, err
	}
	return normalizeConfig(config), nil
}

func providerAPIKeySecretKey(provider string) string {
	return apiKeySecretKey + ":" + strings.ToLower(strings.TrimSpace(provider))
}

func sameProvider(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
}

// saveSearchResult persists one job the moment the search approves it, rather than
// waiting for the whole sweep to finish.
//
// A job the user can see has to be a job the database knows about. The search
// streams approved jobs into the list as it finds them — that is the point of the
// live view — and every action offered on one of those cards writes a row that
// references it. resume_versions.job_id has a foreign key onto jobs(id), so
// tailoring a resume to a job and saving it (the app's whole reason to exist)
// failed outright with "FOREIGN KEY constraint failed" for as long as the search
// that surfaced the job was still running: the row it pointed at did not exist yet.
//
// Persisting on approval closes that window. The end-of-search save still runs and
// is still idempotent — this only moves the write earlier, to the moment the job
// becomes clickable.
func (s *configStore) saveSearchResult(job jobPost) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return err
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := upsertJobPostTx(tx, job, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	return tx.Commit()
}

// upsertJobPostTx writes a job and the source it came from. The source goes first:
// jobs.source_id references it, so the order is not a style choice.
func upsertJobPostTx(tx *sql.Tx, job jobPost, now string) error {
	if strings.TrimSpace(job.ID) == "" || strings.TrimSpace(job.Title) == "" {
		return nil
	}

	sourceID := strings.ToLower(strings.TrimSpace(job.Source))
	if sourceID == "" {
		sourceID = "unknown"
	}
	if _, err := tx.Exec(
		`INSERT INTO job_sources (id, name, kind, enabled, created_at, updated_at)
		 VALUES (?, ?, 'scraper', 1, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET name = excluded.name, enabled = 1, updated_at = excluded.updated_at`,
		sourceID,
		coalesce(job.Source, "unknown"),
		now,
		now,
	); err != nil {
		return err
	}

	raw, err := json.Marshal(job)
	if err != nil {
		return err
	}
	_, err = tx.Exec(
		`INSERT INTO jobs (id, source_id, title, company, location, url, status, score, score_source, raw_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
			source_id = excluded.source_id,
			title = excluded.title,
			company = excluded.company,
			location = excluded.location,
			url = excluded.url,
			status = CASE WHEN jobs.status IN ('applied', 'dismissed')
				THEN jobs.status ELSE excluded.status END,
			score = excluded.score,
			score_source = excluded.score_source,
			raw_json = excluded.raw_json,
			updated_at = excluded.updated_at`,
		job.ID,
		sourceID,
		job.Title,
		job.Company,
		job.Location,
		job.URL,
		coalesce(job.Status, "new"),
		boundedScore(job.Score),
		job.ScoreSource,
		string(raw),
		now,
		now,
	)
	return err
}

func (s *configStore) saveSearchResults(config appConfig, jobs []jobPost) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return err
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)
	for _, job := range jobs {
		if err := upsertJobPostTx(tx, job, now); err != nil {
			return err
		}
	}

	filters, err := json.Marshal(map[string]any{
		"role":               config.Form.Role,
		"roles":              config.Form.Roles,
		"seniority":          config.Form.Seniority,
		"levels":             config.Form.Levels,
		"keywords":           config.Form.Keywords,
		"workMode":           config.Form.WorkMode,
		"location":           targetLocation(config),
		"recentHours":        config.Form.RecentHours,
		"blacklistCompanies": config.Form.Blacklist,
		"remoteOnly":         config.Toggles["remoteOnly"],
		"useLinkedin":        config.Toggles["useLinkedin"],
		"useIndeed":          config.Toggles["useIndeed"],
		"useGupy":            config.Toggles["useGupy"],
		"linkedinPages":      config.Form.LinkedinPages,
	})
	if err != nil {
		return err
	}
	if config.Toggles["saveHistory"] {
		_, err = tx.Exec(
			`INSERT INTO search_history (id, query, filters_json, results_count, created_at)
			 VALUES (?, ?, ?, ?, ?)`,
			fmt.Sprintf("search:%d", time.Now().UnixNano()),
			strings.Join(effectiveSearchConfig(config).Roles, ", "),
			string(filters),
			len(jobs),
			now,
		)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *configStore) saveSecretLocked(db *sql.DB, key string, value string) error {
	protected, err := protectSecret([]byte(value))
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = db.Exec(
		`INSERT INTO secure_refs (key, protected_value, updated_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET protected_value = excluded.protected_value, updated_at = excluded.updated_at`,
		key,
		protected,
		now,
	)
	return err
}

func (s *configStore) secretExistsLocked(db *sql.DB, key string) bool {
	var exists int
	err := db.QueryRow(`SELECT 1 FROM secure_refs WHERE key = ? LIMIT 1`, key).Scan(&exists)
	return err == nil && exists == 1
}

func (s *configStore) loadSecretLocked(db *sql.DB, key string) (string, error) {
	var protected string
	err := db.QueryRow(`SELECT protected_value FROM secure_refs WHERE key = ?`, key).Scan(&protected)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	value, err := unprotectSecret(protected)
	if err != nil {
		return "", err
	}
	return string(value), nil
}

func (s *configStore) deleteSecretLocked(db *sql.DB, key string) error {
	_, err := db.Exec(`DELETE FROM secure_refs WHERE key = ?`, key)
	return err
}

func (s *configStore) initialConfig() appConfig {
	config := defaultConfig()
	legacyPath := filepath.Join(filepath.Dir(s.path), "config.json")

	content, err := os.ReadFile(legacyPath)
	if err != nil {
		return config
	}

	var legacy appConfig
	if err := json.Unmarshal(content, &legacy); err != nil {
		return config
	}
	return normalizeConfig(legacy)
}

func migrate(db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS secure_refs (
			key TEXT PRIMARY KEY,
			protected_value TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS job_sources (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			kind TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			source_id TEXT,
			title TEXT NOT NULL,
			company TEXT,
			location TEXT,
			url TEXT,
			status TEXT NOT NULL DEFAULT 'new',
			score INTEGER NOT NULL DEFAULT 0,
			score_source TEXT NOT NULL DEFAULT '',
			saved_at TEXT,
			raw_json TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			FOREIGN KEY(source_id) REFERENCES job_sources(id) ON DELETE SET NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_jobs_status_score ON jobs(status, score DESC)`,
		`CREATE TABLE IF NOT EXISTS applications (
			id TEXT PRIMARY KEY,
			job_id TEXT NOT NULL,
			status TEXT NOT NULL,
			notes TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			FOREIGN KEY(job_id) REFERENCES jobs(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS search_history (
			id TEXT PRIMARY KEY,
			query TEXT,
			filters_json TEXT,
			results_count INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL
		)`,
		`INSERT OR IGNORE INTO schema_migrations (version, applied_at) VALUES (1, datetime('now'))`,
		`CREATE TABLE IF NOT EXISTS resume_documents (
			id             TEXT PRIMARY KEY,
			name           TEXT NOT NULL,
			source_file    TEXT,
			source_format  TEXT,
			canonical_json TEXT NOT NULL,
			created_at     TEXT NOT NULL,
			updated_at     TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS resume_templates (
			id         TEXT PRIMARY KEY,
			name       TEXT NOT NULL,
			category   TEXT NOT NULL,
			engine     TEXT NOT NULL,
			spec_json  TEXT,
			is_ats     INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS resume_versions (
			id             TEXT PRIMARY KEY,
			document_id    TEXT NOT NULL,
			job_id         TEXT,
			canonical_json TEXT NOT NULL,
			diff_json      TEXT,
			template_id    TEXT,
			ats_score      INTEGER NOT NULL DEFAULT 0,
			hr_score       INTEGER NOT NULL DEFAULT 0,
			created_at     TEXT NOT NULL,
			FOREIGN KEY(document_id) REFERENCES resume_documents(id) ON DELETE CASCADE,
			FOREIGN KEY(job_id)      REFERENCES jobs(id)             ON DELETE SET NULL,
			FOREIGN KEY(template_id) REFERENCES resume_templates(id) ON DELETE SET NULL
		)`,
		`CREATE TABLE IF NOT EXISTS resume_analyses (
			id           TEXT PRIMARY KEY,
			subject_kind TEXT NOT NULL,
			subject_id   TEXT NOT NULL,
			scores_json  TEXT,
			issues_json  TEXT,
			created_at   TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS job_resume_matches (
			id         TEXT PRIMARY KEY,
			job_id     TEXT NOT NULL,
			version_id TEXT NOT NULL,
			gap_json   TEXT,
			status     TEXT NOT NULL DEFAULT 'gerado',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			FOREIGN KEY(job_id)     REFERENCES jobs(id)             ON DELETE CASCADE,
			FOREIGN KEY(version_id) REFERENCES resume_versions(id)  ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS cover_letters (
			id         TEXT PRIMARY KEY,
			job_id     TEXT NOT NULL,
			version_id TEXT,
			content_md TEXT NOT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY(job_id)     REFERENCES jobs(id)            ON DELETE CASCADE,
			FOREIGN KEY(version_id) REFERENCES resume_versions(id) ON DELETE SET NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_resume_versions_job ON resume_versions(job_id)`,
		`CREATE INDEX IF NOT EXISTS idx_resume_versions_doc ON resume_versions(document_id)`,
		`CREATE INDEX IF NOT EXISTS idx_job_resume_matches_job ON job_resume_matches(job_id)`,
		// llm_cache survives a restart on purpose: an AI answer the user already
		// spent free-tier quota on is worth more than the few kilobytes it costs
		// to keep, and an in-memory cache threw it away every time the app closed.
		`CREATE TABLE IF NOT EXISTS llm_cache (
			key        TEXT PRIMARY KEY,
			value      TEXT NOT NULL,
			expires_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_llm_cache_expires ON llm_cache(expires_at)`,
		// llm_usage counts provider calls per calendar day, so the app can stop
		// before the provider refuses instead of discovering the daily cap by
		// getting a 429 in the middle of a search.
		`CREATE TABLE IF NOT EXISTS llm_usage (
			day      TEXT PRIMARY KEY,
			requests INTEGER NOT NULL DEFAULT 0
		)`,
		`INSERT OR IGNORE INTO schema_migrations (version, applied_at) VALUES (2, datetime('now'))`,
	}

	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return err
		}
	}

	// resume_versions.name (CH-05, Wave 3): added after v2 shipped, so it
	// runs as a guarded ALTER instead of joining the CREATE TABLE above —
	// migrate() re-runs on every open(), and ADD COLUMN errors if the
	// column already exists.
	if _, err := db.Exec(`ALTER TABLE resume_versions ADD COLUMN name TEXT NOT NULL DEFAULT ''`); err != nil && !isDuplicateColumnErr(err) {
		return err
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO schema_migrations (version, applied_at) VALUES (3, datetime('now'))`); err != nil {
		return err
	}

	// Radar desktop notifications. notify_pending is set by a radar sweep on the
	// jobs it just brought in above the threshold; notified_at is stamped when
	// the notification actually reaches the desktop, and is what stops the next
	// sweep re-announcing the same job.
	freshColumn := false
	if _, err := db.Exec(`ALTER TABLE jobs ADD COLUMN notified_at TEXT`); err != nil {
		if !isDuplicateColumnErr(err) {
			return err
		}
	} else {
		freshColumn = true
	}
	if _, err := db.Exec(`ALTER TABLE jobs ADD COLUMN notify_pending INTEGER NOT NULL DEFAULT 0`); err != nil && !isDuplicateColumnErr(err) {
		return err
	}
	// Every job that predates this column has notified_at NULL, which otherwise
	// reads as "never announced" — so the first sweep after an upgrade would
	// notify about the entire history at once. Backfill them as already
	// announced. This is inside the freshColumn branch on purpose: migrate()
	// re-runs on every open(), and running it unconditionally would stamp
	// notified_at on jobs that are still legitimately waiting to be delivered.
	if freshColumn {
		if _, err := db.Exec(`UPDATE jobs SET notified_at = created_at WHERE notified_at IS NULL`); err != nil {
			return err
		}
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO schema_migrations (version, applied_at) VALUES (4, datetime('now'))`); err != nil {
		return err
	}
	if _, err := db.Exec(`ALTER TABLE jobs ADD COLUMN score_source TEXT NOT NULL DEFAULT ''`); err != nil && !isDuplicateColumnErr(err) {
		return err
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO schema_migrations (version, applied_at) VALUES (5, datetime('now'))`); err != nil {
		return err
	}
	if _, err := db.Exec(`ALTER TABLE jobs ADD COLUMN saved_at TEXT`); err != nil && !isDuplicateColumnErr(err) {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_jobs_saved_at ON jobs(saved_at DESC)`); err != nil {
		return err
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO schema_migrations (version, applied_at) VALUES (6, datetime('now'))`); err != nil {
		return err
	}
	// Detailed, privacy-safe AI counters. No prompt, key, resume text or provider
	// response is stored — only the day, purpose, provider/model and whether the
	// operation used a request or a local cache hit.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS llm_usage_detail (
		day        TEXT NOT NULL,
		purpose    TEXT NOT NULL,
		provider   TEXT NOT NULL,
		model      TEXT NOT NULL,
		requests   INTEGER NOT NULL DEFAULT 0,
		cache_hits INTEGER NOT NULL DEFAULT 0,
		updated_at TEXT NOT NULL,
		PRIMARY KEY(day, purpose, provider, model)
	)`); err != nil {
		return err
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO schema_migrations (version, applied_at) VALUES (7, datetime('now'))`); err != nil {
		return err
	}
	return nil
}

// markJobsPendingNotification flags the jobs from a radar sweep that scored at
// or above the user's threshold and have never been announced. It takes the IDs
// the sweep actually returned rather than querying by score alone: a job the
// radar re-finds on a later sweep is still in the table with the same score, and
// announcing it again every twenty minutes would be worse than never announcing
// it at all.
func (s *configStore) markJobsPendingNotification(ids []string, threshold int) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return 0, err
	}
	defer db.Close()

	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, len(ids)+1)
	args = append(args, threshold)
	for _, id := range ids {
		args = append(args, id)
	}

	// status NOT IN (applied, dismissed) -- NOT status = 'new'. A scraped job is
	// never 'new': statusForScore stamps it '[APPLY NOW]', '[ADJUST RESUME]' or
	// '[DISCARD]', and 'new' is only ever the column default. Guarding on 'new'
	// silently matched nothing in the real app while passing every unit test that
	// seeded its own rows -- the tests below now seed the statuses the scraper
	// actually writes.
	result, err := db.Exec(
		`UPDATE jobs SET notify_pending = 1
		 WHERE score >= ?
		   AND notified_at IS NULL
		   AND notify_pending = 0
		   AND status NOT IN ('applied', 'dismissed')
		   AND id IN (`+placeholders+`)`,
		args...,
	)
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	return int(affected), err
}

// drainPendingNotifications hands back the jobs waiting to be announced and
// stamps them delivered in the same transaction, so a job is announced exactly
// once even if two polls race.
func (s *configStore) drainPendingNotifications() ([]jobSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.Query(
		`SELECT id, title, company, location, url, status, score, score_source, raw_json
		 FROM jobs WHERE notify_pending = 1 ORDER BY score DESC`,
	)
	if err != nil {
		return nil, err
	}

	jobs := make([]jobSummary, 0)
	for rows.Next() {
		var item jobSummary
		var rawJSON string
		if err := rows.Scan(&item.ID, &item.Title, &item.Company, &item.Location,
			&item.URL, &item.Status, &item.Score, &item.ScoreSource, &rawJSON); err != nil {
			rows.Close()
			return nil, err
		}
		var raw jobPost
		if err := json.Unmarshal([]byte(rawJSON), &raw); err == nil {
			item.Source = raw.Source
			item.ScoreReason = raw.ScoreReason
		}
		jobs = append(jobs, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	if len(jobs) == 0 {
		return jobs, nil
	}

	if _, err := tx.Exec(
		`UPDATE jobs SET notified_at = ?, notify_pending = 0 WHERE notify_pending = 1`,
		time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return jobs, nil
}

func isDuplicateColumnErr(err error) bool {
	return strings.Contains(err.Error(), "duplicate column name")
}

func boundedScore(score int) int {
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

func coalesce(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
