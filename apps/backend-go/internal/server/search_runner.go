package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

type searchStatusResponse struct {
	Running      bool              `json:"running"`
	Message      string            `json:"message"`
	Error        string            `json:"error,omitempty"`
	Total        int               `json:"total"`
	Jobs         []jobSummary      `json:"jobs"`
	LowScoreJobs []jobSummary      `json:"lowScoreJobs"`
	Diagnostics  searchDiagnostics `json:"diagnostics"`
}

type liveSearchState struct {
	mu             sync.RWMutex
	running        bool
	cancelling     bool
	nextRunID      uint64
	activeRunID    uint64
	completedRunID uint64
	cancel         context.CancelFunc
	done           chan struct{}
	jobs           []jobSummary
	lowScoreJobs   []jobSummary
	message        string
	errMsg         string
	diag           searchDiagnostics
}

// searchRun is the immutable handle handed to the goroutine that owns one
// search. Its ID is never reused, so a callback from an old goroutine cannot
// mutate a later run's state.
type searchRun struct {
	id  uint64
	ctx context.Context
}

// reset waits for a canceled scraper to acknowledge completion, but never
// holds the state lock while waiting. The bounded wait keeps the reset route
// responsive even if a third-party scraper ignores context cancellation.
var searchResetWait = 2 * time.Second

func (s *liveSearchState) startRun() (*searchRun, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return nil, false
	}
	s.nextRunID++
	if s.nextRunID == 0 { // practically unreachable; never expose zero as a run ID
		s.nextRunID++
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.running = true
	s.cancelling = false
	s.activeRunID = s.nextRunID
	s.completedRunID = 0
	s.cancel = cancel
	s.done = make(chan struct{})
	s.jobs = nil
	s.lowScoreJobs = nil
	s.message = "Busca iniciada."
	s.errMsg = ""
	s.diag = searchDiagnostics{}
	return &searchRun{id: s.activeRunID, ctx: ctx}, true
}

func (s *liveSearchState) tryStart() bool {
	_, ok := s.startRun()
	return ok
}

func (s *liveSearchState) snapshot() searchStatusResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()
	jobs := append([]jobSummary{}, s.jobs...)
	lowScoreJobs := append([]jobSummary{}, s.lowScoreJobs...)
	return normalizeSearchStatusResponse(searchStatusResponse{
		Running:      s.running,
		Message:      s.message,
		Error:        s.errMsg,
		Total:        len(jobs),
		Jobs:         jobs,
		LowScoreJobs: lowScoreJobs,
		Diagnostics:  s.diag,
	})
}

// normalizeSearchStatusResponse keeps the wire representation stable for
// empty live-search state. Go encodes nil slices/maps as JSON null, while the
// desktop contract treats these collections as arrays/objects and iterates
// them without null guards. Normalize only the response copy so state
// transitions remain unchanged.
func normalizeSearchStatusResponse(response searchStatusResponse) searchStatusResponse {
	if response.Jobs == nil {
		response.Jobs = []jobSummary{}
	}
	if response.LowScoreJobs == nil {
		response.LowScoreJobs = []jobSummary{}
	}
	if response.Diagnostics.Sources == nil {
		response.Diagnostics.Sources = map[string]sourceDiagnostics{}
	}
	if response.Diagnostics.Suggestions == nil {
		response.Diagnostics.Suggestions = []string{}
	}
	for i := range response.Jobs {
		if response.Jobs[i].MissingKeywords == nil {
			response.Jobs[i].MissingKeywords = []string{}
		}
	}
	for i := range response.LowScoreJobs {
		if response.LowScoreJobs[i].MissingKeywords == nil {
			response.LowScoreJobs[i].MissingKeywords = []string{}
		}
	}
	return response
}

func (s *liveSearchState) addJob(job jobSummary) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Keep zero-value state useful for contract/unit fixtures. Once a run has
	// existed, callbacks must use addJobForRun so an old goroutine cannot write
	// into a newer run (or idle state after reset).
	if !s.running {
		if s.nextRunID != 0 {
			return
		}
		applyLiveJobLocked(s, job)
		return
	}
	applyLiveJobLocked(s, job)
}

func (s *liveSearchState) addJobForRun(runID uint64, job jobSummary) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running || s.cancelling || s.activeRunID != runID {
		return
	}
	applyLiveJobLocked(s, job)
}

func applyLiveJobLocked(s *liveSearchState, job jobSummary) {
	if job.Remove {
		s.jobs = liveJobsWithoutID(s.jobs, job.ID)
		s.lowScoreJobs = liveJobsWithoutID(s.lowScoreJobs, job.ID)
		return
	}
	if job.Status == statusDiscard {
		s.jobs = liveJobsWithoutID(s.jobs, job.ID)
		s.lowScoreJobs = upsertLiveJob(s.lowScoreJobs, job)
		return
	}
	s.lowScoreJobs = liveJobsWithoutID(s.lowScoreJobs, job.ID)
	s.jobs = upsertLiveJob(s.jobs, job)
}

func upsertLiveJob(jobs []jobSummary, job jobSummary) []jobSummary {
	for i := range jobs {
		if jobs[i].ID == job.ID {
			jobs[i] = job
			return jobs
		}
	}
	return append(jobs, job)
}

func liveJobsWithoutID(jobs []jobSummary, id string) []jobSummary {
	filtered := jobs[:0]
	for _, job := range jobs {
		if job.ID != id {
			filtered = append(filtered, job)
		}
	}
	return append([]jobSummary{}, filtered...)
}

func (s *liveSearchState) removeJob(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs = liveJobsWithoutID(s.jobs, id)
	s.lowScoreJobs = liveJobsWithoutID(s.lowScoreJobs, id)
}

func (s *liveSearchState) setJobSaved(id string, saved bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	savedAt := ""
	if saved {
		savedAt = time.Now().UTC().Format(time.RFC3339)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	update := func(jobs []jobSummary) {
		for i := range jobs {
			if jobs[i].ID == id {
				jobs[i].SavedAt = savedAt
			}
		}
	}
	update(s.jobs)
	update(s.lowScoreJobs)
}

func (s *liveSearchState) removeCompany(company string) {
	company = strings.ToLower(strings.TrimSpace(company))
	if company == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	remove := func(jobs []jobSummary) []jobSummary {
		filtered := jobs[:0]
		for _, job := range jobs {
			if strings.ToLower(strings.TrimSpace(job.Company)) != company {
				filtered = append(filtered, job)
			}
		}
		return append([]jobSummary{}, filtered...)
	}
	s.jobs = remove(s.jobs)
	s.lowScoreJobs = remove(s.lowScoreJobs)
}

func (s *liveSearchState) finish(message string, err error) {
	s.mu.RLock()
	runID := s.activeRunID
	s.mu.RUnlock()
	s.finishWithDiagnosticsForRun(runID, message, searchDiagnostics{}, err)
}

func (s *liveSearchState) finishForRun(runID uint64, message string, err error) {
	s.finishWithDiagnosticsForRun(runID, message, searchDiagnostics{}, err)
}

func (s *liveSearchState) finishWithDiagnostics(message string, diagnostics searchDiagnostics, err error) {
	s.mu.RLock()
	runID := s.activeRunID
	s.mu.RUnlock()
	s.finishWithDiagnosticsForRun(runID, message, diagnostics, err)
}

func (s *liveSearchState) finishWithDiagnosticsForRun(runID uint64, message string, diagnostics searchDiagnostics, err error) {
	s.mu.Lock()
	if !s.running || s.activeRunID != runID {
		s.mu.Unlock()
		return
	}
	s.running = false
	s.cancelling = false
	s.activeRunID = 0
	s.completedRunID = runID
	s.diag = diagnostics
	if strings.TrimSpace(message) != "" {
		s.message = message
	}
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		s.errMsg = err.Error()
	}
	if s.message == "" && s.errMsg == "" {
		s.message = "Busca concluida."
	}
	done := s.done
	s.done = nil
	cancel := s.cancel
	s.cancel = nil
	if done != nil {
		close(done)
	}
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// searchRolesConfigured used to demand a non-empty Form.Roles. That refusal is
// what kept a stale simple-role value populated across a change of profession —
// the user could not start a search without it, so it stayed, and then the AI
// prefilter used it to reject every job the advanced profiles had just fetched.
// A profiles-only config is a perfectly configured search, and is now accepted
// as one.
func searchRolesConfigured(config appConfig) bool {
	return effectiveSearchConfig(config).configured()
}

func (a *api) searchStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.liveSearch.snapshot())
}

func (a *api) listJobs(w http.ResponseWriter, _ *http.Request) {
	jobs, err := a.configStore.listRecentJobs(100)
	if err != nil {
		a.logger.Printf("list jobs: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": "Não foi possível carregar as vagas."})
		return
	}
	if jobs == nil {
		jobs = []jobSummary{}
	}
	lowScoreJobs, err := a.configStore.listLowScoreJobs(100)
	if err != nil {
		a.logger.Printf("list low-score jobs: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": "Nao foi possivel carregar as vagas de score baixo."})
		return
	}
	if lowScoreJobs == nil {
		lowScoreJobs = []jobSummary{}
	}
	writeJSON(w, http.StatusOK, searchResponse{
		Message:      "Vagas carregadas.",
		Jobs:         jobs,
		LowScoreJobs: lowScoreJobs,
	})
}

func (a *api) listSavedJobs(w http.ResponseWriter, _ *http.Request) {
	jobs, err := a.configStore.listSavedJobs(100)
	if err != nil {
		a.logger.Printf("list saved jobs: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": "Não foi possível carregar as vagas salvas."})
		return
	}
	if jobs == nil {
		jobs = []jobSummary{}
	}
	writeJSON(w, http.StatusOK, map[string][]jobSummary{"jobs": jobs})
}

func (a *api) listApplications(w http.ResponseWriter, _ *http.Request) {
	items, err := a.configStore.listApplications(100)
	if err != nil {
		a.logger.Printf("list applications: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": "Não foi possível carregar as candidaturas."})
		return
	}
	if items == nil {
		items = []application{}
	}
	writeJSON(w, http.StatusOK, map[string][]application{"applications": items})
}

func (a *api) listSearchHistory(w http.ResponseWriter, _ *http.Request) {
	items, err := a.configStore.listSearchHistory(100)
	if err != nil {
		a.logger.Printf("list search history: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": "Não foi possível carregar o histórico."})
		return
	}
	if items == nil {
		items = []searchHistoryEntry{}
	}
	writeJSON(w, http.StatusOK, map[string][]searchHistoryEntry{"history": items})
}

func (s *liveSearchState) reset() {
	s.mu.Lock()
	if !s.running {
		s.clearLocked()
		s.mu.Unlock()
		return
	}
	runID := s.activeRunID
	done := s.done
	cancel := s.cancel
	if done == nil {
		// A zero-value/test-constructed state cannot have a worker to await.
		s.clearLocked()
		s.mu.Unlock()
		return
	}
	s.cancelling = true
	s.message = "Cancelando busca..."
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}

	timer := time.NewTimer(searchResetWait)
	select {
	case <-done:
		timer.Stop()
	case <-timer.C:
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// A new run may have started after the old one reached terminal state. Never
	// clear that newer run while completing a reset for an older ID.
	if s.running && s.activeRunID != runID {
		return
	}
	if !s.running && s.completedRunID != runID {
		return
	}
	if s.running {
		s.running = false
		s.cancelling = false
		s.activeRunID = 0
		s.completedRunID = runID
		if s.done != nil {
			close(s.done)
			s.done = nil
		}
		s.cancel = nil
	}
	s.clearLocked()
}

func (s *liveSearchState) clearLocked() {
	s.running = false
	s.cancelling = false
	s.activeRunID = 0
	s.cancel = nil
	s.done = nil
	s.jobs = nil
	s.lowScoreJobs = nil
	s.message = ""
	s.errMsg = ""
	s.diag = searchDiagnostics{}
}

func (a *api) resetSearchSession(w http.ResponseWriter, _ *http.Request) {
	a.liveSearch.reset()
	writeJSON(w, http.StatusOK, map[string]string{"message": "Sessao de busca limpa."})
}

func (a *api) runSearchBackground(run *searchRun, config appConfig) {
	// A panic in the scraper (parser, browser worker, etc.) must never crash the
	// whole backend process: recover and surface it as a normal search error.
	defer func() {
		if r := recover(); r != nil {
			a.logger.Printf("search panic recovered: %v", r)
			a.liveSearch.finishForRun(run.id, "", fmt.Errorf("busca interrompida por erro interno: %v", r))
		}
	}()

	result, err := a.scraper.startSearch(run.ctx, config, func(job jobSummary) {
		a.liveSearch.addJobForRun(run.id, job)
	})
	if err != nil {
		a.liveSearch.finishForRun(run.id, "", err)
		return
	}
	a.liveSearch.finishWithDiagnosticsForRun(run.id, result.Message, result.Diagnostics, nil)
}
