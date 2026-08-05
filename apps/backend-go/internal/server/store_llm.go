package server

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

type llmUsageBreakdown struct {
	Purpose   string `json:"purpose"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Requests  int    `json:"requests"`
	CacheHits int    `json:"cacheHits"`
}

// The store side of AI cost control. Two small tables, both best-effort: a cache
// miss or a lost counter must never fail a search or a resume operation, it just
// costs a request. Every method therefore swallows its errors and degrades to
// "we don't know", which is the safe direction — the provider's own 429 is still
// the backstop.

// llmCacheGet returns a cached value if it exists and has not expired.
func (s *configStore) llmCacheGet(key string, now time.Time) (string, bool) {
	if s == nil {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return "", false
	}
	defer db.Close()

	var value string
	var expiresAt int64
	err = db.QueryRow(`SELECT value, expires_at FROM llm_cache WHERE key = ?`, key).Scan(&value, &expiresAt)
	if err != nil {
		return "", false
	}
	if now.Unix() >= expiresAt {
		_, _ = db.Exec(`DELETE FROM llm_cache WHERE key = ?`, key)
		return "", false
	}
	return value, true
}

func (s *configStore) llmCachePut(key, value string, expiresAt time.Time) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return
	}
	defer db.Close()

	_, _ = db.Exec(
		`INSERT INTO llm_cache (key, value, expires_at) VALUES (?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, expires_at = excluded.expires_at`,
		key, value, expiresAt.Unix(),
	)
	// Expired rows are only worth the one DELETE that piggybacks on a write.
	_, _ = db.Exec(`DELETE FROM llm_cache WHERE expires_at <= ?`, time.Now().Unix())
}

// llmRequestsToday reports how many provider calls have been spent today.
func (s *configStore) llmRequestsToday(now time.Time) int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return 0
	}
	defer db.Close()

	var count int
	err = db.QueryRow(`SELECT requests FROM llm_usage WHERE day = ?`, usageDay(now)).Scan(&count)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0
	}
	return count
}

// recordLLMRequest counts one provider call against today and returns the new
// total. Rows for other days are dropped: the only day anyone asks about is the
// current one, so history here would be dead weight.
func (s *configStore) recordLLMRequest(now time.Time) int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return 0
	}
	defer db.Close()

	day := usageDay(now)
	if _, err := db.Exec(
		`INSERT INTO llm_usage (day, requests) VALUES (?, 1)
		 ON CONFLICT(day) DO UPDATE SET requests = requests + 1`,
		day,
	); err != nil {
		return 0
	}
	_, _ = db.Exec(`DELETE FROM llm_usage WHERE day <> ?`, day)

	var count int
	if err := db.QueryRow(`SELECT requests FROM llm_usage WHERE day = ?`, day).Scan(&count); err != nil {
		return 0
	}
	return count
}

// usageDay is the local calendar day. Providers reset free-tier daily quotas on
// their own clock (Gemini's is midnight Pacific), so this is an approximation —
// but the budget it guards is deliberately set below the provider's real cap, so
// being a few hours out of phase costs headroom, never a surprise 429.
func usageDay(now time.Time) string {
	return now.Format("2006-01-02")
}

func (s *configStore) recordLLMUsage(now time.Time, purpose llmPurpose, provider, model string, cacheHit bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return
	}
	defer db.Close()

	requests, cacheHits := 1, 0
	if cacheHit {
		requests, cacheHits = 0, 1
	}
	_, _ = db.Exec(
		`INSERT INTO llm_usage_detail (day, purpose, provider, model, requests, cache_hits, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(day, purpose, provider, model) DO UPDATE SET
		 requests = requests + excluded.requests,
		 cache_hits = cache_hits + excluded.cache_hits,
		 updated_at = excluded.updated_at`,
		usageDay(now), string(purpose), strings.ToLower(strings.TrimSpace(provider)), strings.TrimSpace(model),
		requests, cacheHits, now.UTC().Format(time.RFC3339),
	)
	_, _ = db.Exec(`DELETE FROM llm_usage_detail WHERE day <> ?`, usageDay(now))
}

func (s *configStore) llmUsageToday(now time.Time) []llmUsageBreakdown {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return nil
	}
	defer db.Close()

	rows, err := db.Query(
		`SELECT purpose, provider, model, requests, cache_hits
		 FROM llm_usage_detail WHERE day = ?
		 ORDER BY purpose, provider, model`, usageDay(now))
	if err != nil {
		return nil
	}
	defer rows.Close()

	var usage []llmUsageBreakdown
	for rows.Next() {
		var item llmUsageBreakdown
		if err := rows.Scan(&item.Purpose, &item.Provider, &item.Model, &item.Requests, &item.CacheHits); err == nil {
			usage = append(usage, item)
		}
	}
	return usage
}

// updateModelValidation persists the first-use decision without overwriting a
// concurrent Settings save. The expected provider/model pair is an optimistic
// guard: if the user changed either while ListModels was in flight, their newer
// choice wins and only the in-memory operation uses its resolved copy.
func (s *configStore) updateModelValidation(provider, expectedModel, activeModel string, status modelValidationStatus) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if err != nil {
		return err
	}
	defer db.Close()

	config, err := s.loadConfigFromDBLocked(db)
	if err != nil {
		return err
	}
	if !sameProvider(config.Form.Provider, provider) || !strings.EqualFold(strings.TrimSpace(config.Form.Model), strings.TrimSpace(expectedModel)) {
		return nil
	}
	if strings.TrimSpace(activeModel) != "" {
		config.Form.Model = activeModel
	}
	config.ModelValidation = &status
	return s.saveLocked(db, config)
}
