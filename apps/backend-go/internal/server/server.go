package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultAddress = "127.0.0.1:48730"

type appState struct {
	Service      string      `json:"service"`
	Status       string      `json:"status"`
	Version      string      `json:"version"`
	Jobs         int         `json:"jobs"`
	Saved        int         `json:"saved"`
	Applications int         `json:"applications"`
	Sources      int         `json:"sources"`
	Radar        radarStatus `json:"radar"`
}

type appConfig struct {
	Version         int                    `json:"version"`
	Form            configForm             `json:"form"`
	Toggles         map[string]bool        `json:"toggles"`
	LocalItems      localItems             `json:"localItems"`
	APIKeySet       bool                   `json:"apiKeySet"`
	UpdatedAt       string                 `json:"updatedAt,omitempty"`
	Notices         []string               `json:"notices,omitempty"`
	ModelValidation *modelValidationStatus `json:"modelValidation,omitempty"`

	// maxOutputTokens is the per-call output ceiling the cascade picks from the
	// purpose (see llmMaxOutputTokensFor). It rides on the config copy handed to
	// each attempt rather than widening the jsonGenerator signature, and being
	// unexported it never reaches the config file or the API response.
	maxOutputTokens int
	// availableGeminiModels is the successful first-use ListModels snapshot for
	// this operation. It prevents the cascade from retrying a curated pin that
	// the same catalog just proved absent.
	availableGeminiModels []string
}

type configForm struct {
	Source             string `json:"source"`
	Provider           string `json:"provider"`
	Model              string `json:"model"`
	APIKey             string `json:"apiKey"`
	Fallback1Provider  string `json:"fallback1Provider"`
	Fallback1Model     string `json:"fallback1Model"`
	Fallback2Provider  string `json:"fallback2Provider"`
	Fallback2Model     string `json:"fallback2Model"`
	AIMode             string `json:"aiMode"`
	AIDataConsent      bool   `json:"aiDataConsent"`
	Role               string `json:"role"`
	Roles              string `json:"roles"`
	Seniority          string `json:"seniority"`
	Levels             string `json:"levels"`
	ExcludedLevels     string `json:"excludedLevels"`
	SearchProfiles     string `json:"searchProfiles"`
	MaxYears           int    `json:"maxYears"`
	Location           string `json:"location"`
	WorkMode           string `json:"workMode"`
	OnsiteLocation     string `json:"onsiteLocation"`
	RemoteCountry      string `json:"remoteCountry"`
	ResumeName         string `json:"resumeName"`
	ResumePath         string `json:"resumePath"`
	ResumeMarkdownPath string `json:"resumeMarkdownPath"`
	ResumeText         string `json:"resumeText"`
	Keywords           string `json:"keywords"`
	// KeywordsForRoles records the roles the manual Keywords were last written
	// for. It is what lets the app notice that a nurse is still carrying AWS,
	// Terraform and Kubernetes from a backend profile — without ever deleting
	// them behind the user's back. Set by putConfig whenever Keywords changes.
	KeywordsForRoles      string `json:"keywordsForRoles"`
	Blacklist             string `json:"blacklistCompanies"`
	RecentHours           int    `json:"recentHours"`
	MaxJobs               int    `json:"maxJobs"`
	MaxDelaySeconds       int    `json:"maxDelaySeconds"`
	LLMRequestsPerMinute  int    `json:"llmRequestsPerMinute"`
	LLMRequestsPerDay     int    `json:"llmRequestsPerDay"`
	LLMTokensPerMinute    int    `json:"llmTokensPerMinute"`
	SearchTimeoutSeconds  int    `json:"searchTimeoutSeconds"`
	RadarIntervalMinutes  int    `json:"radarIntervalMinutes"`
	NotificationThreshold int    `json:"notificationThreshold"`
	LinkedinPages         int    `json:"linkedinPages"`
	ResponseSize          string `json:"responseSize"`
	ResponseStyle         string `json:"responseStyle"`
	BasePrompt            string `json:"basePrompt"`
	ShortcutSearch        string `json:"shortcutSearch"`
	ShortcutAsk           string `json:"shortcutAsk"`
	ShortcutNotes         string `json:"shortcutNotes"`
	ScoreCut              int    `json:"scoreCut"`
	RankingMode           string `json:"rankingMode"`
}

type localItems struct {
	Jobs         int `json:"jobs"`
	Saved        int `json:"saved"`
	Applications int `json:"applications"`
	History      int `json:"history"`
}

type jobSummary struct {
	ID              string      `json:"id"`
	Source          string      `json:"source"`
	Title           string      `json:"title"`
	Company         string      `json:"company"`
	Location        string      `json:"location"`
	URL             string      `json:"url"`
	Status          string      `json:"status"`
	Score           int         `json:"score"`
	MissingKeywords []string    `json:"missingKeywords"`
	Description     string      `json:"description,omitempty"`
	Profile         string      `json:"profile,omitempty"`
	ScoreSource     ScoreSource `json:"scoreSource,omitempty"`
	ScoreReason     string      `json:"scoreReason,omitempty"`
	ScoringPending  bool        `json:"scoringPending,omitempty"`
	SavedAt         string      `json:"savedAt,omitempty"`
	// Remove is an internal live-search event. It is never serialized by an API:
	// the state consumes it to withdraw a provisional card that did not survive
	// final scoring.
	Remove bool `json:"-"`
}

type application struct {
	ID        string     `json:"id"`
	JobID     string     `json:"jobId"`
	Status    string     `json:"status"`
	Notes     string     `json:"notes,omitempty"`
	CreatedAt string     `json:"createdAt"`
	UpdatedAt string     `json:"updatedAt"`
	Job       jobSummary `json:"job"`
}

type searchHistoryEntry struct {
	ID           string         `json:"id"`
	Query        string         `json:"query"`
	Filters      map[string]any `json:"filters"`
	ResultsCount int            `json:"resultsCount"`
	CreatedAt    string         `json:"createdAt"`
}

type searchResponse struct {
	Message      string            `json:"message"`
	Jobs         []jobSummary      `json:"jobs"`
	LowScoreJobs []jobSummary      `json:"lowScoreJobs"`
	Diagnostics  searchDiagnostics `json:"diagnostics"`
}

type sourceDiagnostics struct {
	Collected            int `json:"collected"`
	Fresh                int `json:"fresh"`
	Evaluated            int `json:"evaluated"`
	Approved             int `json:"approved"`
	Discarded            int `json:"discarded"`
	Dropped              int `json:"dropped"`
	SkippedNoDescription int `json:"skippedNoDescription"`
	DetailFetched        int `json:"detailFetched"`
	// Blocked records that the board served an anti-bot wall instead of results.
	// Without it, a blocked source is indistinguishable from a source that simply
	// had no matching jobs: both collect zero, and the search still reports
	// "Busca concluida".
	Blocked bool `json:"blocked,omitempty"`
}

type searchDiagnostics struct {
	Collected            int  `json:"collected"`
	Fresh                int  `json:"fresh"`
	Evaluated            int  `json:"evaluated"`
	Approved             int  `json:"approved"`
	Discarded            int  `json:"discarded"`
	Dropped              int  `json:"dropped"`
	SkippedNoDescription int  `json:"skippedNoDescription"`
	DetailFetched        int  `json:"detailFetched"`
	TimedOut             bool `json:"timedOut"`
	// Reason-tagged breakdowns of Dropped/the collected-fresh gap, so the
	// empty-state message can name the actual top reasons instead of one
	// opaque "dropped" count (Phase 6).
	DroppedDuplicate  int                          `json:"droppedDuplicate"`
	DroppedDateWindow int                          `json:"droppedDateWindow"`
	DroppedSeniority  int                          `json:"droppedSeniority"`
	DroppedBlacklist  int                          `json:"droppedBlacklist"`
	DroppedFakeRemote int                          `json:"droppedFakeRemote"`
	Sources           map[string]sourceDiagnostics `json:"sources"`
	Suggestions       []string                     `json:"suggestions"`
	// AIQuotaExhausted records that the key ran out of its daily allowance
	// mid-search, and ScoredOffline how many jobs the deterministic scorer had to
	// judge because the AI was unavailable or skipped them. Together they let the
	// UI explain a search whose scores are cruder than usual, instead of leaving
	// the user to think the app simply got worse.
	AIQuotaExhausted  bool `json:"aiQuotaExhausted"`
	AIConsentRequired bool `json:"aiConsentRequired"`
	ScoredOffline     int  `json:"scoredOffline"`
	// ScoredFromCache is how many jobs were already scored from a previous sweep
	// and cost no request. On a radar-mode key this is most of them.
	ScoredFromCache int `json:"scoredFromCache"`
	// SkippedByPrefilter is how many jobs the deterministic relevance filter kept
	// away from the model — the boards return plenty that are nowhere near the
	// searched role, and asking about those is a request spent to be told no.
	//
	// It is separate from ScoredOffline on purpose. That number counts every reason
	// a job went unscored by the AI, most of them failures; this one counts the
	// filter doing its job. Rolled into one figure, as they were, a search cannot
	// tell you whether the filter is earning its place or passing everything through.
	SkippedByPrefilter int `json:"skippedByPrefilter"`
}

type searchStreamEvent struct {
	Type    string      `json:"type"`
	Message string      `json:"message,omitempty"`
	Job     *jobSummary `json:"job,omitempty"`
	Total   int         `json:"total,omitempty"`
}

type modelFetchRequest struct {
	Provider string `json:"provider"`
	APIKey   string `json:"apiKey"`
}

type modelFetchResponse struct {
	Provider string   `json:"provider"`
	Models   []string `json:"models"`
}

type resumeUploadRequest struct {
	FileName      string `json:"fileName"`
	MimeType      string `json:"mimeType"`
	ContentBase64 string `json:"contentBase64"`
}

type resumeUploadResponse struct {
	FileName          string   `json:"fileName"`
	StoredPath        string   `json:"storedPath"`
	MarkdownPath      string   `json:"markdownPath"`
	Markdown          string   `json:"markdown"`
	ExtractedText     string   `json:"extractedText"`
	Keywords          []string `json:"keywords"`
	DetectedRole      string   `json:"detectedRole"`
	DetectedSeniority string   `json:"detectedSeniority"`
	DetectedLevels    string   `json:"detectedLevels"`
	Warnings          []string `json:"warnings,omitempty"`
}

type logEntry struct {
	ID      int64  `json:"id"`
	Time    string `json:"ts"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

type logBuffer struct {
	mu      sync.Mutex
	nextID  int64
	entries []logEntry
}

type api struct {
	logger          *log.Logger
	logs            *logBuffer
	token           string
	configStore     *configStore
	scraper         *scraperBridge
	liveSearch      liveSearchState
	radarMu         sync.Mutex
	radar           radarStatus
	bootstrap       browserBootstrapState
	ocrInstallState ocrInstallState

	cascadeMu              sync.Mutex
	cascadeCooldown        map[string]time.Time
	cascadeRetryDelay      time.Duration
	cascadeRateLimitDelays []time.Duration
	// llmCache is nil in tests that build an api literal directly, which keeps
	// their call-count assertions honest; newHandler wires a real one.
	llmCache   *llmCache
	resumeJobs *resumeJobStore
	// llmLimiter is the one per-minute budget shared by job scoring and Resume
	// Studio. Nil in tests that build an api literal, which keeps them off the
	// clock; newHandler wires a real one.
	llmLimiter *llmLimiter

	modelCatalogMu     sync.Mutex
	geminiCatalog      map[string][]string
	geminiCatalogError map[string]error
	fetchGeminiCatalog func(context.Context, string) ([]string, error)
}

func Run(logger *log.Logger) error {
	address := strings.TrimSpace(os.Getenv("SENCIA_ADDRESS"))
	if address == "" {
		address = defaultAddress
	}

	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}

	handler := newHandler(logger, sessionToken())
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	logger.Printf("SENCIA_BACKEND=%s", listener.Addr().String())
	return server.Serve(listener)
}

func newHandler(logger *log.Logger, token string) http.Handler {
	store := newConfigStore()
	logs := &logBuffer{}
	service := &api{
		logger:      logger,
		logs:        logs,
		token:       token,
		configStore: store,
		llmCache:    newLLMCache(store),
		resumeJobs:  newResumeJobStore(),
		llmLimiter:  newLLMLimiter(llmDefaultRequestsPerMinute),
	}
	service.scraper = newScraperBridge(logger, logs, store)
	service.scraper.api = service
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", service.health)
	mux.HandleFunc("GET /api/v1/state", service.authorize(service.state))
	mux.HandleFunc("GET /api/v1/config", service.authorize(service.getConfig))
	mux.HandleFunc("PUT /api/v1/config", service.authorize(service.putConfig))
	mux.HandleFunc("GET /api/v1/ai/usage", service.authorize(service.aiUsage))
	mux.HandleFunc("GET /api/v1/logs", service.authorize(service.getLogs))
	mux.HandleFunc("POST /api/v1/notifications/drain", service.authorize(service.drainNotifications))
	mux.HandleFunc("POST /api/v1/search", service.authorize(service.search))
	mux.HandleFunc("GET /api/v1/search/status", service.authorize(service.searchStatus))
	mux.HandleFunc("GET /api/v1/search/plan", service.authorize(service.searchPlan))
	mux.HandleFunc("POST /api/v1/search/reset", service.authorize(service.resetSearchSession))
	mux.HandleFunc("GET /api/v1/jobs", service.authorize(service.listJobs))
	mux.HandleFunc("GET /api/v1/jobs/saved", service.authorize(service.listSavedJobs))
	mux.HandleFunc("POST /api/v1/jobs/action", service.authorize(service.jobAction))
	mux.HandleFunc("GET /api/v1/applications", service.authorize(service.listApplications))
	mux.HandleFunc("GET /api/v1/history", service.authorize(service.listSearchHistory))
	mux.HandleFunc("POST /api/v1/models", service.authorize(service.models))
	mux.HandleFunc("POST /api/v1/resume", service.authorize(service.uploadResume))
	mux.HandleFunc("POST /api/v1/resume/parse", service.authorize(service.resumeParse))
	mux.HandleFunc("POST /api/v1/resume/diagnose", service.authorize(service.resumeDiagnose))
	mux.HandleFunc("POST /api/v1/resume/analyze-job", service.authorize(service.resumeAnalyzeJob))
	mux.HandleFunc("POST /api/v1/resume/gap", service.authorize(service.resumeGap))
	mux.HandleFunc("POST /api/v1/resume/optimize", service.authorize(service.resumeOptimize))
	mux.HandleFunc("POST /api/v1/resume/score", service.authorize(service.resumeScore))
	mux.HandleFunc("POST /api/v1/resume/export", service.authorize(service.resumeExport))
	mux.HandleFunc("GET /api/v1/resume/templates", service.authorize(service.resumeTemplatesList))
	mux.HandleFunc("POST /api/v1/resume/version", service.authorize(service.resumeSaveVersion))
	mux.HandleFunc("GET /api/v1/resume/versions", service.authorize(service.resumeVersions))
	mux.HandleFunc("DELETE /api/v1/resume/versions/{id}", service.authorize(service.resumeDeleteVersion))
	mux.HandleFunc("PATCH /api/v1/resume/versions/{id}", service.authorize(service.resumeRenameVersion))
	mux.HandleFunc("POST /api/v1/resume/cover-letter", service.authorize(service.resumeCoverLetter))
	// Async wrappers over the AI-backed routes above, so the desktop app polls
	// a job instead of holding a fetch open for the whole generation.
	mux.HandleFunc("POST /api/v1/resume/async/{op}", service.authorize(service.resumeAsyncStart))
	mux.HandleFunc("GET /api/v1/resume/jobs/{id}", service.authorize(service.resumeJobStatusHandler))
	mux.HandleFunc("POST /api/v1/providers/test", service.authorize(service.providersTest))
	mux.HandleFunc("GET /api/v1/health/install", service.authorize(service.installHealth))
	mux.HandleFunc("POST /api/v1/health/repair", service.authorize(service.installRepair))
	mux.HandleFunc("GET /api/v1/browser/health", service.authorize(service.browserHealth))
	mux.HandleFunc("POST /api/v1/browser/bootstrap", service.authorize(service.browserBootstrap))
	mux.HandleFunc("GET /api/v1/browser/bootstrap/status", service.authorize(service.browserBootstrapStatus))
	mux.HandleFunc("POST /api/v1/ocr/install", service.authorize(service.ocrInstall))
	mux.HandleFunc("GET /api/v1/ocr/status", service.authorize(service.ocrStatus))
	mux.HandleFunc("POST /api/v1/ocr/run", service.authorize(service.ocrRun))
	mux.HandleFunc("POST /api/v1/open-url", service.authorize(service.openURL))
	service.startRadarLoop()
	return service.cors(mux)
}

func sessionToken() string {
	if token := strings.TrimSpace(os.Getenv("SENCIA_API_TOKEN")); token != "" {
		return token
	}
	return "sencia-dev"
}

func (a *api) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *api) state(w http.ResponseWriter, _ *http.Request) {
	config, err := a.configStore.load()
	if err != nil {
		a.logger.Printf("load config for state: %v", err)
		config = defaultConfig()
	}
	stats, err := a.configStore.stats()
	if err != nil {
		a.logger.Printf("load stats for state: %v", err)
		stats = config.LocalItems
	}

	status := "ready"
	if a.liveSearch.snapshot().Running {
		status = "running"
	}

	writeJSON(w, http.StatusOK, appState{
		Service:      "Sencia Job",
		Status:       status,
		Version:      "1.0.0",
		Jobs:         stats.Jobs,
		Saved:        stats.Saved,
		Applications: stats.Applications,
		Sources:      configuredSources(config),
		Radar:        a.radarSnapshot(),
	})
}

func (a *api) getConfig(w http.ResponseWriter, _ *http.Request) {
	config, err := a.configStore.load()
	if err != nil {
		a.logger.Printf("load config: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": "Não foi possível carregar as configurações."})
		return
	}
	writeJSON(w, http.StatusOK, config)
}

func (a *api) putConfig(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	var config appConfig
	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Configuração inválida."})
		return
	}

	config = normalizeConfig(config)
	notices := append([]string(nil), config.Notices...)

	// Remember which roles the manual keywords were written for. Without this the
	// app cannot tell a deliberate keyword list from one inherited across a change
	// of profession — and a nurse kept searching with AWS, Terraform, Kubernetes
	// and CI/CD because nothing knew those words had been typed for a backend
	// role. The keywords are never touched here: recording their provenance is
	// what lets the UI *ask*, instead of silently keeping or silently deleting
	// them (UX-014).
	if previous, err := a.configStore.load(); err == nil {
		// Validation status is runtime evidence, not a client-writable field. Keep
		// it while the route is unchanged and clear it when the provider, model or
		// mode changes so the next real use validates the new choice.
		if sameProvider(config.Form.Provider, previous.Form.Provider) &&
			strings.EqualFold(strings.TrimSpace(config.Form.Model), strings.TrimSpace(previous.Form.Model)) &&
			config.Form.AIMode == previous.Form.AIMode {
			config.ModelValidation = previous.ModelValidation
		} else {
			config.ModelValidation = nil
		}
		if strings.TrimSpace(config.Form.Keywords) != strings.TrimSpace(previous.Form.Keywords) {
			config.Form.KeywordsForRoles = strings.Join(effectiveSearchConfig(config).Roles, ", ")
		} else if config.Form.KeywordsForRoles == "" {
			config.Form.KeywordsForRoles = previous.Form.KeywordsForRoles
		}
	}

	if err := a.configStore.save(config); err != nil {
		a.logger.Printf("save config: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": "Não foi possível salvar as configurações."})
		return
	}
	saved, err := a.configStore.load()
	if err != nil {
		a.logger.Printf("reload config: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": "Configuração salva, mas não foi possível recarregar."})
		return
	}
	saved.Notices = append(saved.Notices, notices...)
	writeJSON(w, http.StatusOK, saved)
}

func (a *api) search(w http.ResponseWriter, _ *http.Request) {
	run, ok := a.liveSearch.startRun()
	if !ok {
		writeJSON(w, http.StatusConflict, map[string]string{
			"code":    "search_already_running",
			"message": "Ja existe uma busca em andamento.",
		})
		return
	}

	config, err := a.configStore.load()
	if err != nil {
		a.liveSearch.finishForRun(run.id, "", err)
		a.logger.Printf("load config for search: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": "Não foi possível carregar as configurações."})
		return
	}
	if configuredSources(config) == 0 {
		a.liveSearch.finishForRun(run.id, "", fmt.Errorf("ative ao menos uma fonte de vagas antes de iniciar a busca"))
		writeJSON(w, http.StatusConflict, map[string]string{
			"code":    "sources_not_configured",
			"message": "Ative ao menos uma fonte em Configuracoes > Fontes de vagas (LinkedIn, Gupy, ou as fontes internacionais) antes de iniciar a busca.",
		})
		return
	}
	if !searchRolesConfigured(config) {
		a.liveSearch.finishForRun(run.id, "", fmt.Errorf("configure cargos/roles em Configuracoes antes de buscar"))
		writeJSON(w, http.StatusConflict, map[string]string{
			"code":    "roles_not_configured",
			"message": "Configure cargos/roles em Configuracoes (aba Perfil) antes de iniciar a busca.",
		})
		return
	}
	// Reset may cancel this request while config validation is in progress. Do
	// not launch a canceled scraper goroutine after a newer run has taken over.
	if run.ctx.Err() != nil {
		writeJSON(w, http.StatusConflict, map[string]string{
			"code":    "search_already_running",
			"message": "Ja existe uma busca em andamento.",
		})
		return
	}

	go a.runSearchBackground(run, config)
	writeJSON(w, http.StatusAccepted, map[string]string{"message": "Busca iniciada."})
}

func (a *api) models(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	var payload modelFetchRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Pedido de modelos inválido."})
		return
	}
	models, err := a.fetchProviderModels(r.Context(), payload)
	if err != nil {
		a.logger.Printf("fetch models: %v", err)
		if isAPIKeyRequiredError(err) {
			// Distinct from a real upstream/network failure (502 below): the
			// user simply has not configured a key yet for this provider
			// (BUG-03), so the frontend can show a precise, actionable
			// message instead of a generic error.
			writeJSON(w, http.StatusConflict, map[string]string{
				"code":    "api_key_required",
				"message": err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, modelFetchResponse{Provider: payload.Provider, Models: models})
}

func (a *api) authorize(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		// Exact, constant-time comparison: EqualFold made the secret
		// case-insensitive (less entropy) and leaked timing.
		if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(a.token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "unauthorized"})
			return
		}
		next(w, r)
	}
}

// staticAllowedOrigins is the fixed allowlist for Tauri and the default Vite
// port. Additional localhost dev ports are validated by allowedOrigin.
var staticAllowedOrigins = map[string]bool{
	"http://localhost:1420":   true,
	"http://127.0.0.1:1420":   true,
	"http://tauri.localhost":  true,
	"https://tauri.localhost": true,
	"tauri://localhost":       true,
	// Tauri 2 packaged Windows builds use the IPC custom protocol origin.
	"http://ipc.localhost":  true,
	"https://ipc.localhost": true,
}

func allowedOrigin(origin string) bool {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return false
	}
	if staticAllowedOrigins[origin] {
		return true
	}
	for _, extra := range strings.Split(os.Getenv("SENCIA_ALLOWED_ORIGINS"), ",") {
		if strings.EqualFold(strings.TrimSpace(extra), origin) {
			return true
		}
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return false
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil {
		return false
	}
	return port >= 1420 && port <= 1499
}

func (a *api) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if allowedOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		return
	}
}

func defaultConfig() appConfig {
	return appConfig{
		Version: 2,
		Form: configForm{
			Provider:              "Gemini",
			Model:                 geminiFreeModel,
			AIMode:                aiModeFreeEconomy,
			AIDataConsent:         false,
			Roles:                 "",
			Levels:                "Junior, Jr, Pleno, Senior, Sr, Especialista",
			ExcludedLevels:        "Tech Lead, Lead, Staff, Principal, Manager, Coordenador, Gerente",
			MaxYears:              8,
			Location:              "Remoto",
			WorkMode:              "remote",
			OnsiteLocation:        "",
			RemoteCountry:         "Brazil",
			RecentHours:           24,
			MaxJobs:               40,
			MaxDelaySeconds:       15,
			LLMRequestsPerMinute:  llmDefaultRequestsPerMinute,
			LLMRequestsPerDay:     llmDefaultRequestsPerDay,
			SearchTimeoutSeconds:  defaultSearchTimeoutSeconds,
			RadarIntervalMinutes:  20,
			NotificationThreshold: 85,
			LinkedinPages:         2,
			ResponseSize:          "compacto",
			ResponseStyle:         "objetivo",
			BasePrompt:            "Priorize compatibilidade técnica, senioridade e modalidade.",
			ShortcutSearch:        "F8",
			ShortcutAsk:           "F9",
			ShortcutNotes:         "F4",
			ScoreCut:              60,
			RankingMode:           "compatibilidade",
		},
		Toggles: map[string]bool{
			"remoteOnly":  true,
			"useLinkedin": true,
			"useIndeed":   true,
			"useGupy":     false,
			// Free keyless REST boards. Off by default (LinkedIn stays the always-on
			// source); the user turns these on in Settings > Fontes de vagas. This is
			// the international volume that replaced Indeed after Cloudflare closed it.
			"useRemotive":       false,
			"useRemoteok":       false,
			"useJobicy":         false,
			"useArbeitnow":      false,
			"useWeworkremotely": false,
			"headless":          true,
			"compatibility":     true,
			"score":             true,
			"localOnly":         true,
			"exportReady":       false,
			"desktop":           false,
			"daily":             false,
			"saveHistory":       true,
			"autoClean":         false,
			"radarMode":         false,
		},
		LocalItems: localItems{},
	}
}

func normalizeConfig(config appConfig) appConfig {
	// Notices describe migrations performed during this normalization pass. They
	// are response metadata, never client-authoritative configuration.
	config.Notices = nil
	defaults := defaultConfig()
	if config.Version == 0 {
		config.Version = defaults.Version
	}
	if config.Version < defaults.Version {
		config.Version = defaults.Version
	}
	if config.Form.Model == "" {
		config.Form.Model = defaults.Form.Model
	}
	if config.Form.Provider == "" {
		config.Form.Provider = defaults.Form.Provider
	}
	if !modelMatchesProvider(config.Form.Provider, config.Form.Model) {
		config.Form.Model = defaultModelForProvider(config.Form.Provider)
	}
	config.Form.Model = normalizeConfiguredModel(&config, "Modelo principal", config.Form.Provider, config.Form.Model)
	config.Form.Fallback1Model = normalizeFallbackModel(&config, "Fallback 1", config.Form.Fallback1Provider, config.Form.Fallback1Model)
	config.Form.Fallback2Model = normalizeFallbackModel(&config, "Fallback 2", config.Form.Fallback2Provider, config.Form.Fallback2Model)
	config.Form.AIMode = normalizeAIMode(config.Form.AIMode)
	if config.Form.Location == "" {
		config.Form.Location = defaults.Form.Location
	}
	if config.Form.Roles == "" {
		config.Form.Roles = coalesce(config.Form.Role, defaults.Form.Roles)
	}
	if config.Form.Levels == "" {
		config.Form.Levels = coalesce(config.Form.Seniority, defaults.Form.Levels)
	}
	if config.Form.ExcludedLevels == "" {
		config.Form.ExcludedLevels = defaults.Form.ExcludedLevels
	}
	if config.Form.MaxYears == 0 {
		config.Form.MaxYears = defaults.Form.MaxYears
	}
	// The <select> in Settings has always been able to emit "hybrid_onsite", and
	// the scraper has never understood it: it matched no branch of
	// modalityPipelines, so both passes were skipped and the empty-pipelines
	// fallback quietly reinstated a remote-only search against RemoteCountry.
	// A user's saved "Chicago, on-site" therefore left the app as
	// "location=United States&f_WT=2". Canonicalize at the door — and persist the
	// canonical value, so the bad token cannot round-trip forever (UX-015).
	config.Form.WorkMode = canonicalWorkMode(config.Form.WorkMode)
	// remoteOnly used to be a second, independently-writable source of truth for
	// the same fact, and the two could disagree. Work mode is the fact; the
	// toggle is now derived from it.
	if config.Toggles == nil {
		config.Toggles = map[string]bool{}
	}
	config.Toggles["remoteOnly"] = config.Form.WorkMode == workModeRemote
	if config.Form.OnsiteLocation == "" {
		config.Form.OnsiteLocation = coalesce(config.Form.Location, defaults.Form.OnsiteLocation)
	}
	if config.Form.RemoteCountry == "" {
		config.Form.RemoteCountry = defaults.Form.RemoteCountry
	}
	if config.Form.RecentHours == 0 {
		config.Form.RecentHours = defaults.Form.RecentHours
	}
	if config.Form.MaxJobs == 0 {
		config.Form.MaxJobs = defaults.Form.MaxJobs
	}
	if config.Form.MaxDelaySeconds == 0 {
		config.Form.MaxDelaySeconds = defaults.Form.MaxDelaySeconds
	}
	if config.Form.LLMRequestsPerMinute == 0 {
		config.Form.LLMRequestsPerMinute = defaults.Form.LLMRequestsPerMinute
	}
	if config.Form.LLMRequestsPerDay == 0 {
		config.Form.LLMRequestsPerDay = defaults.Form.LLMRequestsPerDay
	}
	if config.Form.SearchTimeoutSeconds == 0 {
		config.Form.SearchTimeoutSeconds = defaults.Form.SearchTimeoutSeconds
	}
	if config.Form.SearchTimeoutSeconds < minSearchTimeoutSeconds {
		config.Form.SearchTimeoutSeconds = minSearchTimeoutSeconds
	}
	if config.Form.SearchTimeoutSeconds > maxSearchTimeoutSeconds {
		config.Form.SearchTimeoutSeconds = maxSearchTimeoutSeconds
	}
	if config.Form.RadarIntervalMinutes == 0 {
		config.Form.RadarIntervalMinutes = defaults.Form.RadarIntervalMinutes
	}
	if config.Form.NotificationThreshold == 0 {
		config.Form.NotificationThreshold = defaults.Form.NotificationThreshold
	}
	if config.Form.LinkedinPages == 0 {
		config.Form.LinkedinPages = defaults.Form.LinkedinPages
	}
	if config.Form.ResponseSize == "" {
		config.Form.ResponseSize = defaults.Form.ResponseSize
	}
	if config.Form.ResponseStyle == "" {
		config.Form.ResponseStyle = defaults.Form.ResponseStyle
	}
	if config.Form.BasePrompt == "" {
		config.Form.BasePrompt = defaults.Form.BasePrompt
	}
	if config.Form.ShortcutSearch == "" {
		config.Form.ShortcutSearch = defaults.Form.ShortcutSearch
	}
	if config.Form.ShortcutAsk == "" {
		config.Form.ShortcutAsk = defaults.Form.ShortcutAsk
	}
	if config.Form.ShortcutNotes == "" {
		config.Form.ShortcutNotes = defaults.Form.ShortcutNotes
	}
	if config.Form.ScoreCut == 0 {
		config.Form.ScoreCut = defaults.Form.ScoreCut
	}
	if config.Form.RankingMode == "" {
		config.Form.RankingMode = defaults.Form.RankingMode
	}
	if config.Toggles == nil {
		config.Toggles = map[string]bool{}
	}
	for key, value := range defaults.Toggles {
		if _, ok := config.Toggles[key]; !ok {
			config.Toggles[key] = value
		}
	}
	return config
}

func normalizeFallbackModel(config *appConfig, slot, provider, model string) string {
	if strings.TrimSpace(provider) == "" {
		return ""
	}
	if !modelMatchesProvider(provider, model) {
		model = defaultModelForProvider(provider)
	}
	return normalizeConfiguredModel(config, slot, provider, model)
}

func normalizeConfiguredModel(config *appConfig, slot, provider, model string) string {
	replacement, retired := replacementForRetiredModel(provider, model)
	if !retired {
		return model
	}
	config.Notices = append(config.Notices, slot+": o modelo retirado "+model+" foi substituído por "+replacement+".")
	return replacement
}

func configuredSources(config appConfig) int {
	count := 0
	if config.Toggles["useLinkedin"] {
		count++
	}
	if config.Toggles["useIndeed"] {
		count++
	}
	if config.Toggles["useGupy"] {
		count++
	}
	// The free REST boards count too: a search with only Remotive enabled is a
	// valid search. Before this, the "no sources configured" guard rejected it
	// with 409, so those sources could be toggled on and never run.
	if anyRemoteAPIEnabled(config) {
		count++
	}
	if count == 0 && strings.TrimSpace(config.Form.Source) != "" {
		count = 1
	}
	return count
}

func defaultModelForProvider(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	switch {
	case strings.Contains(provider, "gemini") || strings.Contains(provider, "google"):
		return geminiFreeModel
	case strings.Contains(provider, "anthropic"):
		return "claude-sonnet-4-5"
	case strings.Contains(provider, "openrouter"):
		return "openai/gpt-4.1-mini"
	case strings.Contains(provider, "groq"):
		return "llama-3.3-70b-versatile"
	case strings.Contains(provider, "ollama"):
		return "llama3.1"
	default:
		return "gpt-4.1-mini"
	}
}

func modelMatchesProvider(provider string, model string) bool {
	provider = strings.ToLower(strings.TrimSpace(provider))
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return false
	}
	switch {
	case strings.Contains(provider, "gemini") || strings.Contains(provider, "google"):
		return strings.HasPrefix(model, "gemini")
	case strings.Contains(provider, "anthropic"):
		return strings.HasPrefix(model, "claude")
	case strings.Contains(provider, "openrouter"):
		return strings.Contains(model, "/")
	default:
		return true
	}
}
