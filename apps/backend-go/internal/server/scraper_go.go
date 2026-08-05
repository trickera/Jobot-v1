package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
)

type scraperBridge struct {
	logger *log.Logger
	logs   *logBuffer
	store  *configStore
	client *http.Client
	// llmClient is deliberately separate from client: scraping a job board is
	// a sub-second fetch that should fail fast, while a Resume Studio tailoring
	// call legitimately runs for tens of seconds. Sharing one 25s ceiling meant
	// every optimize/gap request was killed mid-generation and could never
	// succeed, no matter how many times the cascade retried it.
	llmClient *http.Client
	api       *api
	mu        sync.Mutex
}

type jobPost struct {
	ID             string      `json:"id"`
	Source         string      `json:"source"`
	Title          string      `json:"title"`
	Company        string      `json:"company"`
	Location       string      `json:"location"`
	URL            string      `json:"url"`
	Description    string      `json:"description"`
	Status         string      `json:"status"`
	Score          int         `json:"score"`
	Missing        []string    `json:"missingKeywords"`
	Modality       string      `json:"modality"`
	AgeHours       float64     `json:"ageHours"`
	PostedTime     string      `json:"postedTime"`
	Profile        string      `json:"profile,omitempty"`
	ScoreSource    ScoreSource `json:"scoreSource,omitempty"`
	ScoreReason    string      `json:"scoreReason,omitempty"`
	ScoringPending bool        `json:"scoringPending,omitempty"`
	// rules carries the per-profile seniority window. When nil the global
	// config seniority is used (keeps legacy single-field setups working).
	rules *seniorityRuleSet
	// profileRoles are the roles of the profile that actually fetched this job.
	// The AI prefilter tests against these, never against the global Roles field
	// — a job must never be rejected as "off-target" by a role the search that
	// found it did not use (UX-016).
	profileRoles []string
}

func (j jobPost) seniorityRuleSet(config appConfig) seniorityRuleSet {
	if j.rules != nil {
		return *j.rules
	}
	return globalSeniorityRuleSet(config)
}

const (
	statusApply     = "[APPLY NOW]"
	statusAdjust    = "[ADJUST RESUME]"
	statusDiscard   = "[DISCARD]"
	statusApplied   = "applied"
	statusDismissed = "dismissed"
	statusScoring   = "[AI SCORING]"
)

// ScoreSource is the provenance of the score currently shown to the user.
// Keep this closed set in sync with apps/desktop/src/types.ts.
type ScoreSource string

const (
	scoreSourceAI               ScoreSource = "ai"
	scoreSourceAICache          ScoreSource = "ai_cache"
	scoreSourceOfflinePrefilter ScoreSource = "offline_prefilter"
	scoreSourceOfflineFallback  ScoreSource = "offline_fallback"
	scoreSourceOfflineNoKey     ScoreSource = "offline_no_key"
)

// llmCallTimeout bounds a single provider round-trip. The per-request context
// deadline (llmCascadeBudget) still bounds the whole cascade above it.
const llmCallTimeout = 90 * time.Second

func newScraperBridge(logger *log.Logger, logs *logBuffer, store *configStore) *scraperBridge {
	return &scraperBridge{
		logger:    logger,
		logs:      logs,
		store:     store,
		client:    &http.Client{Timeout: 25 * time.Second},
		llmClient: &http.Client{Timeout: llmCallTimeout},
	}
}

// llmHTTPClient returns the long-timeout client used for provider calls,
// falling back to the scraper client when unset (tests inject a mock
// transport through client alone).
func (s *scraperBridge) llmHTTPClient() *http.Client {
	if s.llmClient != nil {
		return s.llmClient
	}
	return s.client
}

type modalityPipeline struct {
	remote   bool
	location string
}

type indeedState struct {
	warmed bool
}

type jobEmitter func(jobSummary)

func postToSummary(job jobPost) jobSummary {
	return jobSummary{
		ID:              job.ID,
		Source:          job.Source,
		Title:           job.Title,
		Company:         job.Company,
		Location:        job.Location,
		URL:             job.URL,
		Status:          job.Status,
		Score:           boundedScore(job.Score),
		MissingKeywords: job.Missing,
		Description:     truncate(job.Description, 2500),
		Profile:         job.Profile,
		ScoreSource:     job.ScoreSource,
		ScoreReason:     job.ScoreReason,
		ScoringPending:  job.ScoringPending,
	}
}

func (s *scraperBridge) startSearch(parent context.Context, config appConfig, emit jobEmitter) (searchResponse, error) {
	if strings.TrimSpace(os.Getenv("SENCIA_GO_SCRAPER_DISABLED")) == "1" {
		return searchResponse{}, errors.New("go scraper disabled")
	}
	if parent == nil {
		parent = context.Background()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.log("muted", "// starting run ...")

	profiles := parseSearchProfiles(config)
	s.logSearchPlan(config, profiles)

	searchTimeout := searchTimeoutDuration(config)
	ctx, cancel := context.WithTimeout(parent, searchTimeout)
	defer cancel()
	s.log("info", "[ BUDGET ] tempo limite da busca: %s", searchTimeout)

	session, err := s.openBrowserSession(ctx, config.Toggles["headless"])
	if err != nil {
		return searchResponse{}, err
	}
	defer session.close()

	pipelines := modalityPipelines(config)
	recent := config.Form.RecentHours
	if recent <= 0 {
		recent = 48
	}
	processed := s.store.processedKeys()
	blacklist := splitCSV(config.Form.Blacklist)
	previewed := map[string]bool{}
	previewKeys := map[string]bool{}
	finalizedIDs := map[string]bool{}

	// Show cheaply pre-filtered collection results immediately. They are honest
	// previews, not final scores: actions that require persistence stay disabled
	// until the same ID is replaced by finalize below. This removes the old
	// collection + serial-description + ten-job-batch wall before the first card.
	previewCollected := func(jobs []jobPost) {
		if emit == nil {
			return
		}
		for _, job := range filterFresh(jobs, recent) {
			if strings.TrimSpace(job.ID) == "" || processed[processedIDKey(job.ID)] || processed[titleCompanyKey(job.Title, job.Company)] {
				continue
			}
			if companyBlacklisted(blacklist, job.Company) || seniorityBlockReason(config, job) != "" {
				continue
			}
			key := titleCompanyKey(job.Title, job.Company)
			if previewKeys[key] {
				continue
			}
			previewKeys[key] = true
			previewed[job.ID] = true
			job.Status = statusScoring
			job.Score = 0
			job.ScoreReason = "Vaga coletada; descricao e score de IA ainda estao sendo processados."
			job.ScoringPending = true
			emit(postToSummary(job))
		}
	}

	var collected []jobPost
	// Shared with the description fetches below, so the Indeed session is warmed
	// once per search rather than once per profile.
	var indeed indeedState
	blockedSources := map[string]bool{}
collectLoop:
	for _, platform := range enabledPlatforms(config) {
		for _, pipe := range pipelines {
			for pi := range profiles {
				if ctx.Err() != nil {
					s.log("warning", "[ BUDGET ] tempo limite atingido durante a coleta; encerrando varredura com o que foi coletado ate agora")
					break collectLoop
				}
				jobs, blocked := s.collectFromPlatform(ctx, session, config, platform, pipe, profiles[pi], recent, &indeed)
				if blocked {
					blockedSources[platform] = true
				}
				collected = append(collected, jobs...)
				previewCollected(jobs)
			}
			s.jitter(ctx, config, platform)
		}
	}

	// The free REST boards (Remotive, RemoteOK, Jobicy, Arbeitnow, WeWorkRemotely).
	// They need no browser and no location pass, so they run once per profile,
	// after the scraped boards, and only when at least one is enabled. This is
	// where the international volume Indeed used to provide now comes from.
	if anyRemoteAPIEnabled(config) {
		for pi := range profiles {
			if ctx.Err() != nil {
				break
			}
			jobs := s.collectRemoteAPIs(ctx, config, profiles[pi])
			collected = append(collected, jobs...)
			previewCollected(jobs)
		}
	}

	diagnostics := newSearchDiagnostics(enabledPlatforms(config))
	// A board that served an anti-bot wall collected zero, and so does a board
	// that simply had nothing to offer. Recording which one happened is the whole
	// difference between "no jobs today" and "this source has been dead for
	// weeks and the search kept saying Busca concluida".
	for platform := range blockedSources {
		diagnostics.bump(platform, func(item *sourceDiagnostics) { item.Blocked = true })
	}
	diagnostics.addCollectedJobs(collected)
	preDedupCount := len(collected)
	collected = dedupeByTitleCompany(collected)
	diagnostics.DroppedDuplicate = preDedupCount - len(collected)
	fresh := filterFresh(collected, recent)
	diagnostics.DroppedDateWindow = len(collected) - len(fresh)
	diagnostics.addFreshJobs(fresh)
	s.log("info", "[ FILTER ] %d/%d vagas dentro de %dh", len(fresh), len(collected), recent)
	// Order by cheap title/snippet relevance BEFORE the maxJobs cut so the
	// expensive description-fetch + LLM budget is spent on the strongest
	// candidates, not arbitrary collection order. Interleaving then keeps one
	// source from monopolizing the budget.
	fresh = prioritizeByRelevance(config, fresh)
	fresh = interleaveBySource(fresh)
	limit := normalizedMaxJobs(config)
	if len(fresh) > limit {
		fresh = fresh[:limit]
	}

	apiKey, err := s.store.aiAPIKey()
	if err != nil {
		return searchResponse{}, err
	}
	if strings.TrimSpace(apiKey) != "" && config.Toggles["compatibility"] {
		s.log("info", "[ LLM ] chave %s (%s) modelo %s", strings.TrimSpace(config.Form.Provider), maskAPIKey(apiKey), geminiModelForConfig(config))
	}
	llmEnabled := llmScoringEnabled(config, apiKey)

	results := make([]jobPost, 0, len(fresh))
	lowScoreResults := make([]jobPost, 0, len(fresh))

	// finalize turns a scored job into a verdict. Split out of the loop because
	// batch-scored jobs reach it from the flush below rather than inline.
	finalize := func(job jobPost) {
		job.ScoringPending = false
		job.Status = statusForScore(config, job.Score, job.Missing)
		diagnostics.addEvaluated(job)
		s.log("info", "[ EVAL ] score=%d %s - %s", job.Score, job.Status, job.Title)
		if job.Status == statusApply || job.Status == statusAdjust {
			finalizedIDs[job.ID] = true
			diagnostics.addApproved(job)
			results = append(results, job)
			// Persist before emitting, not after the sweep ends. The moment this job
			// appears in the list the user can act on it, and every action writes a
			// row that references it by id — resume_versions.job_id has a foreign key
			// onto jobs(id). Saving a tailored resume for a job picked out of a search
			// that was still running therefore failed outright, because the row it
			// pointed at did not exist yet. A job we let the user click is a job we
			// have stored.
			if err := s.store.saveSearchResult(job); err != nil {
				s.log("warning", "[ BUSCA ] nao foi possivel salvar a vaga %s: %v", job.ID, err)
			}
			if emit != nil {
				emit(postToSummary(job))
			}
		} else if job.Status == statusDiscard {
			diagnostics.addDiscarded(job)
			if err := s.store.saveSearchResult(job); err != nil {
				s.log("warning", "[ BUSCA ] nao foi possivel salvar a vaga de score baixo %s: %v", job.ID, err)
			} else {
				finalizedIDs[job.ID] = true
				lowScoreResults = append(lowScoreResults, job)
				if emit != nil {
					emit(postToSummary(job))
				}
			}
		}
	}

	// pending holds jobs waiting for an AI score. They are sent several at a time
	// instead of one call each, which is what makes a free-tier key survive a
	// whole search. A job the model does not answer for falls back to the offline
	// heuristic rather than being dropped.
	batchSize := jobScoreBatchSizeFor(config)
	pending := make([]jobPost, 0, batchSize)
	var pendingStarted time.Time
	flush := func() {
		if len(pending) == 0 {
			return
		}
		batch, err := s.scoreJobsBatch(ctx, config, pending)
		if err != nil && classifyProviderError(err) == "quota_exhausted" {
			// The key has nothing left today. The search still finishes offline,
			// but the user has to be told why the scores changed character —
			// silently degrading would look like the app got worse at its job.
			diagnostics.AIQuotaExhausted = true
		}
		if errors.Is(err, errAIDataConsentRequired) {
			diagnostics.AIConsentRequired = true
		}
		diagnostics.ScoredFromCache += batch.CacheHits
		for _, job := range pending {
			if score, ok := batch.Scores[job.ID]; ok {
				job.Score, job.Missing = score, missingKeywords(config, job)
				job.ScoreSource = batch.Sources[job.ID]
				if job.ScoreSource == scoreSourceAICache {
					job.ScoreReason = "Score de IA reutilizado do cache local."
				} else {
					job.ScoreReason = "Score calculado pela IA configurada."
				}
			} else {
				job.Score, job.Missing = heuristicScoreV2(config, job)
				job.ScoreSource = offlineScoreSource(config, apiKey, false)
				if err != nil {
					job.ScoreReason = fmt.Sprintf("A IA falhou (%s); estimativa offline usada.", classifyProviderError(err))
				} else {
					job.ScoreReason = "A IA nao respondeu a tempo; estimativa offline usada."
				}
				diagnostics.addOfflineScore(job.ScoreSource)
			}
			finalize(job)
		}
		pending = pending[:0]
		pendingStarted = time.Time{}
	}

	for i := range fresh {
		if ctx.Err() != nil {
			break
		}
		if len(pending) > 0 && !pendingStarted.IsZero() && time.Since(pendingStarted) >= jobScoreBatchMaxWait {
			flush()
		}
		job := fresh[i]
		if processed[titleCompanyKey(job.Title, job.Company)] || processed[processedIDKey(job.ID)] {
			s.log("muted", "   [skip] vaga ja processada - %s", job.Title)
			continue
		}
		if companyBlacklisted(blacklist, job.Company) {
			diagnostics.addDroppedBlacklist(job)
			s.log("muted", "   [skip] empresa na blacklist - %s", job.Company)
			continue
		}
		if reason := seniorityBlockReason(config, job); reason != "" {
			diagnostics.addDroppedSeniority(job)
			s.log("warning", "[ DROP ] %s (titulo) - %s", reason, job.Title)
			continue
		}
		if shouldJitterBeforeDescription(job.Source) {
			s.jitterBeforeDescription(ctx, config)
		}
		desc := s.fetchJobDescription(session, &job)
		if strings.TrimSpace(desc) == "" {
			diagnostics.addSkippedNoDescription(job)
			s.log("muted", "   [skip] sem descricao - %s", job.Title)
			continue
		}
		job.Description = desc
		if reason := seniorityBlockReason(config, job); reason != "" {
			diagnostics.addDroppedSeniority(job)
			s.log("warning", "[ DROP ] %s - %s", reason, job.Title)
			continue
		}
		if job.Modality == "Remote" {
			if reason := fakeRemoteReason(config, job); reason != "" {
				diagnostics.addDroppedFakeRemote(job)
				s.log("warning", "[ DROP ] %s - %s", reason, job.Title)
				continue
			}
		}
		// Spend an AI call only on a job that is plausibly in the right ballpark.
		// The rest are scored offline: they still get a score and can still
		// surface, they just do not cost a request the user may not have.
		if llmEnabled {
			if titleRelevant(config, job) {
				pending = append(pending, job)
				if len(pending) == 1 {
					pendingStarted = time.Now()
				}
				if len(pending) >= batchSize || time.Since(pendingStarted) >= jobScoreBatchMaxWait {
					flush()
				}
				continue
			}
			// Counted apart from every other reason a job ends up scored offline —
			// a spent quota, a batch that failed, no time left — because those are
			// failures and this is the filter working. Lumped together, as they were,
			// there is no way to tell from a real search whether the filter earns its
			// place or silently passes everything through.
			job.ScoreSource = offlineScoreSource(config, apiKey, true)
			diagnostics.addOfflineScore(job.ScoreSource)
			job.ScoreReason = "A vaga ficou fora do prefilter de IA; estimativa offline usada sem gastar requisicao."
		}
		job.Score, job.Missing = heuristicScoreV2(config, job)
		if job.ScoreSource == "" {
			job.ScoreSource = offlineScoreSource(config, apiKey, false)
			if strings.TrimSpace(apiKey) == "" {
				job.ScoreReason = "Sem chave de IA configurada; estimativa offline usada."
			} else {
				job.ScoreReason = "Analise por IA desativada; estimativa offline usada."
			}
			diagnostics.addOfflineScore(job.ScoreSource)
		}
		finalize(job)
	}
	flush()
	for id := range previewed {
		if emit != nil && !finalizedIDs[id] {
			emit(jobSummary{ID: id, Remove: true})
		}
	}
	if diagnostics.SkippedByPrefilter > 0 {
		s.log("muted", "[ LLM ] %d vaga(s) fora do alvo — pontuadas offline, sem gastar requisição", diagnostics.SkippedByPrefilter)
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].Source < results[j].Source
		}
		return results[i].Score > results[j].Score
	})
	sort.SliceStable(lowScoreResults, func(i, j int) bool {
		if lowScoreResults[i].Score == lowScoreResults[j].Score {
			return lowScoreResults[i].Source < lowScoreResults[j].Source
		}
		return lowScoreResults[i].Score > lowScoreResults[j].Score
	})

	if err := s.store.saveSearchResults(config, results); err != nil {
		return searchResponse{}, err
	}

	summaries := make([]jobSummary, 0, len(results))
	for _, job := range results {
		summaries = append(summaries, postToSummary(job))
	}
	lowScoreSummaries := make([]jobSummary, 0, len(lowScoreResults))
	for _, job := range lowScoreResults {
		lowScoreSummaries = append(lowScoreSummaries, postToSummary(job))
	}
	timedOut := ctx.Err() != nil
	diagnostics.TimedOut = timedOut
	if timedOut {
		s.log("warning", "[ DONE ] busca encerrada por tempo limite (%s); %d vagas aprovadas/ajustaveis ate o corte.", searchTimeout, len(summaries))
	} else {
		s.log("success", "[ DONE ] Busca concluida: %d vagas aprovadas/ajustaveis.", len(summaries))
	}
	diagnostics = diagnostics.withSuggestions(config)
	return searchResponse{
		Message:      buildSearchOutcomeMessage(diagnostics, timedOut, len(summaries)),
		Jobs:         summaries,
		LowScoreJobs: lowScoreSummaries,
		Diagnostics:  diagnostics,
	}, nil
}

// buildSearchOutcomeMessage picks the single most useful terminal message for
// a finished search run (BUG-01/Phase 6). A run must always end in a state
// the user can act on: timed out, nothing collected, collected-but-filtered,
// or a normal success - never a silent "0 vagas encontradas" that looks
// identical whether the scan finished cleanly or was cut short.
// blockedSourceNames lists the sources that answered with an anti-bot wall this
// run, so the outcome message can name them. A source that is being blocked and
// a source that had a quiet day both collect zero; only this tells them apart,
// and the user is the one who needs to know — they are the one who thinks the app
// is searching three boards.
func blockedSourceNames(diagnostics searchDiagnostics) []string {
	var names []string
	for name, item := range diagnostics.Sources {
		if item.Blocked {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// buildSearchOutcomeMessage is wrapped so that every outcome — completed, timed
// out, nothing collected — carries the anti-bot note when a source was walled.
// "Busca concluida: 4 vaga(s)" while a third of the enabled boards served a
// captcha is not a completed search, and printing it anyway is how Indeed stayed
// dead without anyone noticing.
func buildSearchOutcomeMessage(diagnostics searchDiagnostics, timedOut bool, approvedCount int) string {
	message := searchOutcomeBody(diagnostics, timedOut, approvedCount)
	if blocked := blockedSourceNames(diagnostics); len(blocked) > 0 {
		return fmt.Sprintf(
			"%s bloqueou o acesso automatizado (desafio anti-bot) e nao retornou vagas nesta busca. %s",
			strings.Join(blocked, " e "),
			message,
		)
	}
	return message
}

func searchOutcomeBody(diagnostics searchDiagnostics, timedOut bool, approvedCount int) string {
	if timedOut {
		if approvedCount > 0 {
			return fmt.Sprintf(
				"Busca encerrada por tempo limite. Aprovamos %d vaga(s) antes do corte, de %d coletada(s) no total. Tente reduzir maxJobs, ampliar a janela de data ou rodar novamente para cobrir o restante.",
				approvedCount, diagnostics.Collected,
			)
		}
		return fmt.Sprintf(
			"Busca encerrada por tempo limite. Coletamos %d vaga(s), mas a varredura nao terminou a tempo. Tente reduzir maxJobs, ampliar a janela de data ou rodar novamente.",
			diagnostics.Collected,
		)
	}
	if approvedCount > 0 {
		return fmt.Sprintf("Busca concluida: %d vaga(s) encontrada(s).", approvedCount)
	}
	if diagnostics.Collected == 0 {
		return "Nenhuma vaga recente foi coletada. Tente ampliar localizacao, janela de postagem ou fontes."
	}
	base := fmt.Sprintf(
		"As fontes coletaram %d vaga(s), mas nenhuma passou nos filtros.",
		diagnostics.Collected,
	)
	if reasons := topDropReasons(diagnostics); reasons != "" {
		base += " Principais cortes: " + reasons + "."
	}
	return base + " Tente reduzir score minimo, ampliar keywords, incluir mais senioridades ou aumentar a janela de postagem."
}

// topDropReasons names the highest-signal reasons a run ended with zero
// approved jobs despite collecting some, e.g. "senioridade (6), data (3),
// score minimo (3)" - so the user sees WHY, not just a bare zero (Phase 6).
func topDropReasons(d searchDiagnostics) string {
	type reasonCount struct {
		label string
		count int
	}
	candidates := []reasonCount{
		{"senioridade", d.DroppedSeniority},
		{"data de postagem", d.DroppedDateWindow},
		{"score minimo", d.Discarded},
		{"blacklist", d.DroppedBlacklist},
		{"modalidade (falso remoto)", d.DroppedFakeRemote},
		{"duplicadas", d.DroppedDuplicate},
		{"sem descricao suficiente", d.SkippedNoDescription},
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].count > candidates[j].count
	})
	var parts []string
	for _, c := range candidates {
		if c.count <= 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s (%d)", c.label, c.count))
		if len(parts) == 3 {
			break
		}
	}
	return strings.Join(parts, ", ")
}

func (s *scraperBridge) collectFromPlatform(ctx context.Context, session *browserSession, config appConfig, platform string, pipe modalityPipeline, profile searchProfile, recent int, indeed *indeedState) (jobs []jobPost, blocked bool) {
	modality := "Remote"
	if !pipe.remote {
		modality = "Hybrid/On-site"
	}
	// One flaky platform/profile must not abort the whole run.
	defer func() {
		if r := recover(); r != nil {
			s.log("error", "[ %s ] erro inesperado no perfil %s: %v", platform, profile.Name, r)
			jobs = tagJobsWithProfile(jobs, profile)
		}
	}()
	groups := profile.queryGroups(5)

	// A wall breaks out of the source's own loops but still falls through to the
	// tagging below: whatever was collected before the wall went up is real, and
	// dropping it untagged would lose jobs to a bug fix.
platformScan:
	switch platform {
	case "Gupy":
		query := profile.gupyQuery()
		target := buildGupyURL(query, pipe.location, pipe.remote)
		s.log("info", "[ GUPY ] %s", target)
		records, html, err := session.fetchGupy(target)
		if err != nil {
			s.logSource("Gupy", 0, err)
			return nil, false
		}
		jobs = gupyRecordsToJobs(records, modality)
		if len(jobs) == 0 {
			jobs = parseGupyNextData(html, modality)
		}
		if len(jobs) == 0 {
			s.log("warning", "[ GUPY ] gupy_spa_empty - nem o XHR capturado nem o __NEXT_DATA__ da pagina trouxeram vagas; a Gupy pode ter mudado a estrutura da SPA")
		}
	case "Indeed":
		s.log("info", "[ INDEED ] modo listing-only: nenhum /viewjob sera acessado automaticamente; os links permanecem nos cards para abertura manual.")
		// Indeed fingerprints a cold session and answers it with an anti-bot wall.
		// Warm the browser once on the home page before the listing request. This
		// warm-up never navigates to an individual job page.
		if !indeed.warmed {
			_ = session.warmIndeed()
			indeed.warmed = true
		}
		for _, query := range groups {
			if strings.TrimSpace(query) == "" {
				continue
			}
			target := buildIndeedURL(query, pipe.location, pipe.remote, recent)
			s.log("info", "[ INDEED ] %s", target)
			// The blocked flag used to be discarded here (`html, _, err :=`). The
			// worker was already detecting the wall and saying so, and the search
			// threw the answer away: it then parsed zero cards out of a captcha
			// page and reported "Busca concluida". A dead source and a quiet day
			// looked exactly alike.
			html, wall, err := session.fetch(target, "domcontentloaded", "")
			if err != nil {
				s.logSource("Indeed", 0, err)
				continue
			}
			if wall {
				blocked = true
				s.log("error", "[ INDEED ] bloqueado: a pagina devolvida e um desafio anti-bot, nao resultados. Nenhuma vaga do Indeed nesta varredura.")
				break platformScan
			}
			jobs = append(jobs, parseIndeedJobs(html, modality)...)
			s.jitter(ctx, config, "Indeed")
		}
		if pipe.remote && len(jobs) == 0 {
			s.log("warning", "[ INDEED ] filtro remoto sem resultados; tentando sem o token remoto")
			for _, query := range groups {
				if strings.TrimSpace(query) == "" {
					continue
				}
				target := buildIndeedURL(query, pipe.location, false, recent)
				html, wall, err := session.fetch(target, "domcontentloaded", "")
				if err != nil {
					continue
				}
				if wall {
					blocked = true
					s.log("error", "[ INDEED ] bloqueado tambem sem o token remoto; desistindo do Indeed nesta varredura.")
					break platformScan
				}
				jobs = append(jobs, parseIndeedJobs(html, modality)...)
				s.jitter(ctx, config, "Indeed")
			}
		}
		// Not blocked, and still nothing. That is the other failure, and it is not
		// the same one: the page came back intact and parseIndeedJobs found no
		// [data-jk] in it, which means Indeed moved its markup and the parser is
		// reading a page that no longer exists.
		if !blocked && len(jobs) == 0 {
			s.log("warning", "[ INDEED ] a pagina carregou sem bloqueio e mesmo assim nenhuma vaga foi extraida; a marcacao do Indeed pode ter mudado (parseIndeedJobs procura [data-jk]).")
		}
	default:
		pages := config.Form.LinkedinPages
		if pages < 1 {
			pages = 1
		}
		if pages > 10 {
			pages = 10
		}
		for _, query := range groups {
			if strings.TrimSpace(query) == "" {
				continue
			}
			for page := 0; page < pages; page++ {
				target := buildLinkedInURL(query, pipe.location, pipe.remote, page*10, recent)
				s.log("info", "[ LINKEDIN ] %s", target)
				// Same discarded flag as Indeed had. LinkedIn is not walling us
				// today, which is exactly why this was never noticed — and exactly
				// why it would have gone unnoticed on the day it starts.
				html, wall, err := session.fetch(target, "domcontentloaded", "")
				if err != nil {
					s.logSource("LinkedIn", 0, err)
					break
				}
				if wall {
					blocked = true
					s.log("error", "[ LINKEDIN ] bloqueado: authwall/captcha em vez de resultados.")
					break platformScan
				}
				pageJobs := parseLinkedInJobs(html, modality)
				jobs = append(jobs, pageJobs...)
				if len(pageJobs) == 0 {
					break
				}
				s.jitter(ctx, config, "LinkedIn")
			}
		}
	}
	jobs = tagJobsWithProfile(jobs, profile)
	s.logSource(fmt.Sprintf("%s %s [%s]", platform, modality, profile.Name), len(jobs), nil)
	return jobs, blocked
}

// tagJobsWithProfile records which profile discovered each job and attaches the
// profile seniority window so later filters use per-profile rules.
func tagJobsWithProfile(jobs []jobPost, profile searchProfile) []jobPost {
	if len(jobs) == 0 {
		return jobs
	}
	var rules *seniorityRuleSet
	if profile.explicit {
		r := profile.seniorityRules()
		rules = &r
	}
	for i := range jobs {
		jobs[i].Profile = profile.Name
		jobs[i].rules = rules
		jobs[i].profileRoles = profile.Roles
	}
	return jobs
}

func (s *scraperBridge) logSearchPlan(config appConfig, profiles []searchProfile) {
	eff := effectiveSearchConfig(config)
	// The log is the only place a user can audit what the app actually did. It
	// stayed silent on all three of the things that went wrong in the 2026-07-13
	// run — the overridden target role, the inherited keywords, the modality that
	// became remote-only. It says all three now, before the first request goes out.
	s.log("info", "[ PLANO ] %s", eff.summary())
	if len(eff.IgnoredRoles) > 0 {
		s.log("warning", "[ PLANO ] perfis avancados estao no controle: o cargo simples %q NAO sera usado nesta busca",
			strings.Join(eff.IgnoredRoles, ", "))
	}
	if eff.StaleKeywords {
		s.log("warning", "[ PLANO ] as keywords (%s) foram escritas para %q e nao tem relacao com os cargos desta busca — elas continuam ativas",
			strings.Join(eff.ScoringTerms, ", "), strings.Join(eff.KeywordsForRoles, ", "))
	}
	s.log("info", "[ PLANO ] %d perfil(is) de busca", len(profiles))
	for i, p := range profiles {
		levels := strings.Join(p.Levels, ", ")
		if strings.TrimSpace(levels) == "" {
			levels = "(todas)"
		}
		roles := booleanGroup(p.Roles)
		if roles == "" {
			roles = "(sem cargos)"
		}
		s.log("info", "  perfil %d [%s]: %s | niveis: %s | max %d anos", i+1, p.Name, roles, levels, p.MaxYears)
	}
}

// pageFetcher is the subset of *browserSession used by per-job enrichment.
// Keeping this as an interface lets tests prove Indeed never receives it.
type pageFetcher interface {
	fetch(url string, waitUntil string, waitForSelector string) (string, bool, error)
}

func (s *scraperBridge) fetchJobDescription(session pageFetcher, job *jobPost) string {
	// The REST boards return the full description in the listing response, so
	// there is nothing to fetch — and nothing to fetch it WITH (they have no
	// browser page). Without this, they would fall into the default LinkedIn
	// branch below, which would run digitsGroup on a remotive/jobicy URL, hit a
	// bogus guest-API endpoint, get nothing, and drop every REST job as "sem
	// descricao". The description is already on the job.
	if isRemoteAPISource(job.Source) {
		return strings.TrimSpace(job.Description)
	}
	switch job.Source {
	case "Indeed":
		snippet := strings.TrimSpace(job.Description)
		if len(snippet) < 40 {
			return ""
		}
		return snippet
	case "Gupy":
		html, blocked, err := session.fetch(job.URL, "domcontentloaded", "")
		if err != nil || blocked {
			return ""
		}
		return extractDescription(html,
			`[data-testid="job-description"]`, `[data-testid="vacancy-description"]`,
			`[class*="vacancy-description"]`, `[class*="job-description"]`, `[class*="description"]`, "main")
	default:
		jobID := digitsGroup(job.URL)
		if jobID == "" {
			return ""
		}
		html, blocked, err := session.fetch(fmt.Sprintf(linkedInJobURL, jobID), "domcontentloaded", "")
		if err != nil || blocked {
			return ""
		}
		return extractDescription(html,
			`[class*="show-more-less-html__markup"]`, `[class*="description__text"]`,
			`[class*="markup"]`, `[class*="description"]`)
	}
}

func newSearchDiagnostics(platforms []string) searchDiagnostics {
	diag := searchDiagnostics{Sources: map[string]sourceDiagnostics{}}
	for _, platform := range platforms {
		diag.Sources[platform] = sourceDiagnostics{}
	}
	return diag
}

func (d *searchDiagnostics) bump(source string, fn func(*sourceDiagnostics)) {
	if d.Sources == nil {
		d.Sources = map[string]sourceDiagnostics{}
	}
	source = strings.TrimSpace(source)
	if source == "" {
		source = "other"
	}
	item := d.Sources[source]
	fn(&item)
	d.Sources[source] = item
}

func (d *searchDiagnostics) addCollectedJobs(jobs []jobPost) {
	d.Collected += len(jobs)
	for _, job := range jobs {
		d.bump(job.Source, func(item *sourceDiagnostics) {
			item.Collected++
		})
	}
}

func (d *searchDiagnostics) addFreshJobs(jobs []jobPost) {
	d.Fresh += len(jobs)
	for _, job := range jobs {
		d.bump(job.Source, func(item *sourceDiagnostics) {
			item.Fresh++
		})
	}
}

func (d *searchDiagnostics) addEvaluated(job jobPost) {
	d.Evaluated++
	d.bump(job.Source, func(item *sourceDiagnostics) {
		item.Evaluated++
	})
}

func (d *searchDiagnostics) addApproved(job jobPost) {
	d.Approved++
	d.bump(job.Source, func(item *sourceDiagnostics) {
		item.Approved++
	})
}

func (d *searchDiagnostics) addDiscarded(job jobPost) {
	d.Discarded++
	d.bump(job.Source, func(item *sourceDiagnostics) {
		item.Discarded++
	})
}

func (d *searchDiagnostics) addOfflineScore(source ScoreSource) {
	d.ScoredOffline++
	if source == scoreSourceOfflinePrefilter {
		d.SkippedByPrefilter++
	}
}

func (d *searchDiagnostics) addDropped(job jobPost) {
	d.Dropped++
	d.bump(job.Source, func(item *sourceDiagnostics) {
		item.Dropped++
	})
}

// addDroppedBlacklist/Seniority/FakeRemote record WHY a job was dropped, on
// top of the aggregate addDropped(), so the empty-state message can name the
// actual top reason(s) instead of a single opaque "dropped" count (Phase 6:
// "Coletamos 12 vagas... Principais cortes: senioridade (6), blacklist (2)").
func (d *searchDiagnostics) addDroppedBlacklist(job jobPost) {
	d.addDropped(job)
	d.DroppedBlacklist++
}

func (d *searchDiagnostics) addDroppedSeniority(job jobPost) {
	d.addDropped(job)
	d.DroppedSeniority++
}

func (d *searchDiagnostics) addDroppedFakeRemote(job jobPost) {
	d.addDropped(job)
	d.DroppedFakeRemote++
}

func (d *searchDiagnostics) addSkippedNoDescription(job jobPost) {
	d.SkippedNoDescription++
	d.bump(job.Source, func(item *sourceDiagnostics) {
		item.SkippedNoDescription++
	})
}

func (d searchDiagnostics) withSuggestions(config appConfig) searchDiagnostics {
	if d.Approved > 0 {
		return d
	}
	if d.Collected == 0 {
		// A run that collected nothing is the most confusing outcome: it is
		// usually anti-bot blocking or simply no recent postings, not a missing
		// configuration. Give the user an actionable, honest explanation.
		d.Suggestions = []string{
			"As fontes nao retornaram vagas nesta varredura (possivel bloqueio anti-bot do LinkedIn/Indeed ou nenhuma vaga recente).",
			"Tente novamente em instantes, aumente a janela de data de postagem, ou revise cargos/roles e fontes ativas.",
		}
		return d
	}
	var suggestions []string
	if d.Fresh == 0 {
		suggestions = append(suggestions, "Aumente a janela de data de postagem ou remova filtros de fonte muito restritos.")
	}
	if d.SkippedNoDescription > 0 {
		suggestions = append(suggestions, "Algumas vagas nao abriram descricao completa; tente novamente ou reduza maxJobs para dar mais tempo por vaga.")
	}
	if d.Evaluated > 0 && d.Discarded > 0 {
		suggestions = append(suggestions, "Reduza o match threshold ou amplie as keywords editaveis do curriculo.")
	}
	if d.Dropped > 0 {
		suggestions = append(suggestions, "Revise senioridade, maximo de anos, modalidade e blacklist.")
	}
	if len(candidateScoringTerms(config)) > 0 {
		suggestions = append(suggestions, "Confirme se as keywords representam a area buscada; elas pesam em todas as vagas.")
	}
	if len(suggestions) == 0 {
		suggestions = append(suggestions, "Amplie cargos/roles ou fontes e rode a busca novamente.")
	}
	d.Suggestions = suggestions
	return d
}

func (s *scraperBridge) logSource(source string, count int, err error) {
	if err != nil {
		s.logger.Printf("%s scraper: %v", source, err)
		s.log("warning", "[ %s ] falhou: %v", strings.ToUpper(source), err)
		return
	}
	s.logger.Printf("%s scraper: %d jobs", source, count)
	s.log("info", "[ %s ] %d vagas coletadas", strings.ToUpper(source), count)
}

// modalityPipelines decides which passes to run: a remote pass targeting the
// remote country, and/or a local pass targeting the onsite location.
//
// It used to string-compare config.Form.WorkMode directly, and the Settings
// <select> emits "hybrid_onsite" — a token no branch here matched. Both passes
// were therefore skipped, and the "no pipelines" fallback below reinstated a
// remote-only pass against RemoteCountry. That is how a user's saved
// "Chicago, on-site" left the app as "location=United States&f_WT=2" and
// returned jobs in Florida (UX-015).
//
// The mode now arrives already canonicalized (normalizeConfig -> canonicalWorkMode),
// so there is exactly one vocabulary and the fallback is unreachable by a typo.
func modalityPipelines(config appConfig) []modalityPipeline {
	eff := effectiveSearchConfig(config)

	var pipelines []modalityPipeline
	if wantsRemotePass(eff.WorkMode) {
		pipelines = append(pipelines, modalityPipeline{remote: true, location: eff.RemoteLocation})
	}
	if wantsLocalPass(eff.WorkMode) && strings.TrimSpace(eff.OnsiteLocation) != "" {
		pipelines = append(pipelines, modalityPipeline{remote: false, location: eff.OnsiteLocation})
	}
	if len(pipelines) == 0 {
		// On-site with no location given: there is nowhere to search but the
		// remote country. Say so rather than pretending the user asked for it.
		pipelines = append(pipelines, modalityPipeline{remote: true, location: eff.RemoteLocation})
	}
	return pipelines
}

// Search run deadline (BUG-01): a commercial search must never hang
// indefinitely. defaultSearchTimeoutSeconds replaces the old fixed
// 12-minute budget with a much shorter, user-tunable one; min/max clamp a
// user-supplied value to a sane range regardless of what a client sends.
const (
	minSearchTimeoutSeconds     = 60
	maxSearchTimeoutSeconds     = 900
	defaultSearchTimeoutSeconds = 240
)

func searchTimeoutDuration(config appConfig) time.Duration {
	seconds := config.Form.SearchTimeoutSeconds
	if seconds <= 0 {
		seconds = defaultSearchTimeoutSeconds
	}
	if seconds < minSearchTimeoutSeconds {
		seconds = minSearchTimeoutSeconds
	}
	if seconds > maxSearchTimeoutSeconds {
		seconds = maxSearchTimeoutSeconds
	}
	return time.Duration(seconds) * time.Second
}

func enabledPlatforms(config appConfig) []string {
	var platforms []string
	if config.Toggles["useLinkedin"] {
		platforms = append(platforms, "LinkedIn")
	}
	if config.Toggles["useIndeed"] {
		platforms = append(platforms, "Indeed")
	}
	if config.Toggles["useGupy"] {
		platforms = append(platforms, "Gupy")
	}
	return platforms
}

// jitter paces the listing scrape: between pages of a source and between
// sources. This is where a scraping pattern is most visible to an anti-bot
// system — a burst of search queries — so it keeps the user's full
// MaxDelaySeconds band.
func (s *scraperBridge) jitter(ctx context.Context, config appConfig, platform string) {
	maxSeconds := config.Form.MaxDelaySeconds
	if maxSeconds <= 0 {
		maxSeconds = 5
	}
	if maxSeconds > 60 {
		maxSeconds = 60
	}
	floor := 1
	if platform == "Indeed" && maxSeconds >= 3 {
		floor = 3
	}
	s.sleepJitter(ctx, floor, maxSeconds)
}

// jitterBeforeDescription paces the per-job page fetches, and derives its band
// from the search budget instead of taking MaxDelaySeconds raw.
//
// It has to, because the raw setting was self-defeating. At the default (15s,
// so a 1-15s band averaging 8s) a 40-job search spent ~320s asleep against a
// 240s budget: the search timed out, dropped the jobs it never reached, and the
// user re-ran it. Two truncated searches send MORE traffic at the job boards
// than one that finishes. Slower is not safer when it causes retries.
//
// So the per-job pace is whatever fits: half the search budget, spread across
// the jobs we intend to visit, clamped to a human-plausible band and never
// above what the user configured.
func (s *scraperBridge) jitterBeforeDescription(ctx context.Context, config appConfig) {
	floor, ceiling := descriptionJitterBand(config)
	s.sleepJitter(ctx, floor, ceiling)
}

func shouldJitterBeforeDescription(source string) bool {
	return source != "Indeed"
}

const (
	// descriptionJitterBudgetShare is how much of a search's wall clock may go to
	// pacing the per-job fetches. The rest belongs to the work itself: loading
	// pages, scoring, and the sources' own latency.
	descriptionJitterBudgetShare = 0.5

	minDescriptionJitterSeconds = 1
	maxDescriptionJitterSeconds = 8
)

func descriptionJitterBand(config appConfig) (floor int, ceiling int) {
	budget := config.Form.SearchTimeoutSeconds
	if budget <= 0 {
		budget = defaultSearchTimeoutSeconds
	}
	jobs := normalizedMaxJobs(config)
	if jobs <= 0 {
		jobs = 1
	}

	// A uniform band [1, c] averages (1+c)/2, so the ceiling that spends the
	// allowance exactly is 2*mean - 1.
	mean := (float64(budget) * descriptionJitterBudgetShare) / float64(jobs)
	ceiling = int(2*mean) - minDescriptionJitterSeconds

	if ceiling > maxDescriptionJitterSeconds {
		ceiling = maxDescriptionJitterSeconds
	}
	// Never pace harder than the user asked for.
	if configured := config.Form.MaxDelaySeconds; configured > 0 && ceiling > configured {
		ceiling = configured
	}
	if ceiling < minDescriptionJitterSeconds {
		ceiling = minDescriptionJitterSeconds
	}
	return minDescriptionJitterSeconds, ceiling
}

func (s *scraperBridge) sleepJitter(ctx context.Context, floor, ceiling int) {
	if ceiling < floor {
		ceiling = floor
	}
	delay := time.Duration(rand.Intn(ceiling-floor+1)+floor) * time.Second
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func dedupeByTitleCompany(jobs []jobPost) []jobPost {
	seen := map[string]bool{}
	out := make([]jobPost, 0, len(jobs))
	for _, job := range jobs {
		title := strings.TrimSpace(job.Title)
		if title == "" {
			continue
		}
		key := titleCompanyKey(job.Title, job.Company)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, job)
	}
	return out
}

func filterFresh(jobs []jobPost, recentHours int) []jobPost {
	limit := float64(recentHours)
	out := make([]jobPost, 0, len(jobs))
	for _, job := range jobs {
		age := job.AgeHours
		if age >= ageUnknown && strings.EqualFold(job.Source, "LinkedIn") {
			// LinkedIn search URLs already include f_TPR; unknown card age should
			// not discard results the platform filtered server-side.
			age = limit
		}
		if isRemoteAPISource(job.Source) {
			// The free REST boards have no server-side freshness filter and return
			// their full list of ACTIVE openings (they drop filled/expired ones
			// themselves), which is a 30-day span, not a 24-hour one. The user's
			// recentHours is meant for scraped searches where "recent" = "posted
			// recently"; applying it here would cull most of the volume these
			// boards exist to provide, since the board's own curation is already
			// the freshness signal. Keep them; scoring still ranks them.
			age = limit
		}
		if age <= limit {
			out = append(out, job)
		}
	}
	return out
}

// preRankScore is a cheap, description-free relevance signal (title + any
// listing snippet only) used to order candidates before the maxJobs cut. It
// returns 0 when there is no resume/keyword signal, which leaves ordering
// unchanged (stable sort), so setups without keywords behave exactly as before.
func preRankScore(config appConfig, job jobPost) int {
	score := 0
	text := normalizeText(job.Title + " " + job.Description)
	for _, term := range candidateScoringTerms(config) {
		if containsTerm(text, term) {
			score += 2
		}
	}
	if inTitle, inHead := jobMatchesSearchRoles(config, job); inTitle {
		score += 3
	} else if inHead {
		score++
	}
	return score
}

// prioritizeByRelevance stably sorts jobs by preRankScore (desc). Combined with
// interleaveBySource + the maxJobs cut, this makes the budget favor the
// strongest candidate from each source. Stable: equal scores keep collection
// order. Scores are memoized so the comparator does not recompute per pair.
func prioritizeByRelevance(config appConfig, jobs []jobPost) []jobPost {
	if len(jobs) < 2 {
		return jobs
	}
	scores := make(map[string]int, len(jobs))
	scoreFor := func(job jobPost) int {
		key := job.ID
		if key == "" {
			key = job.Source + "\x00" + job.Title
		}
		if s, ok := scores[key]; ok {
			return s
		}
		s := preRankScore(config, job)
		scores[key] = s
		return s
	}
	sort.SliceStable(jobs, func(i, j int) bool {
		return scoreFor(jobs[i]) > scoreFor(jobs[j])
	})
	return jobs
}

// interleaveBySource round-robins jobs so low maxJobs still surfaces every source.
func interleaveBySource(jobs []jobPost) []jobPost {
	if len(jobs) < 2 {
		return jobs
	}
	buckets := map[string][]jobPost{}
	order := make([]string, 0, 4)
	for _, job := range jobs {
		src := job.Source
		if _, ok := buckets[src]; !ok {
			order = append(order, src)
		}
		buckets[src] = append(buckets[src], job)
	}
	if len(order) < 2 {
		return jobs
	}
	maxLen := 0
	for _, src := range order {
		if l := len(buckets[src]); l > maxLen {
			maxLen = l
		}
	}
	out := make([]jobPost, 0, len(jobs))
	for i := 0; i < maxLen; i++ {
		for _, src := range order {
			if i < len(buckets[src]) {
				out = append(out, buckets[src][i])
			}
		}
	}
	return out
}

func titleCompanyKey(title string, company string) string {
	return strings.ToLower(strings.TrimSpace(title)) + "\x00" + strings.ToLower(strings.TrimSpace(company))
}

func companyBlacklisted(blacklist []string, company string) bool {
	company = strings.ToLower(company)
	for _, term := range blacklist {
		term = strings.ToLower(strings.TrimSpace(term))
		if term != "" && strings.Contains(company, term) {
			return true
		}
	}
	return false
}

// Hybrid/on-site markers that contradict a "Remote" label, plus phrases that
// negate them (to avoid false positives).
var hybridMarkers = []string{
	"hibrido", "hibrida", "presencial", "semipresencial", "escritorio",
	"comparecer", "comparecimento", "ida ao escritorio", "dias no escritorio",
	"dias presenciais", "modelo hibrido", "hybrid", "on-site", "on site", "onsite",
	"in office", "work from office", "days in office", "days a week on-site",
}

var remoteNegations = []string{
	"sem necessidade de ida ao escritorio", "sem ida ao escritorio",
	"nao precisa ir ao escritorio", "nao precisa comparecer", "sem comparecimento",
	"sem exigencia presencial", "sem atividade presencial", "sem presencial",
	"nao presencial", "no office required", "no need to go to office", "without office visits",
}

func hasHybridMarker(text string) bool {
	normalized := normalizeText(text)
	for _, negation := range remoteNegations {
		normalized = strings.ReplaceAll(normalized, normalizeText(negation), " ")
	}
	for _, marker := range hybridMarkers {
		if containsTerm(normalized, marker) {
			return true
		}
	}
	return false
}

// fakeRemoteReason discards a "Remote" job whose description mandates
// hybrid/on-site presence, unless the job is in the user's commutable onsite
// location (generalized from the original POA-metro allowlist).
func fakeRemoteReason(config appConfig, job jobPost) string {
	if !hasHybridMarker(job.Description) {
		return ""
	}
	haystack := normalizeText(job.Location + " " + job.Description)
	for _, token := range splitCSV(config.Form.OnsiteLocation) {
		if strings.TrimSpace(token) != "" && containsTerm(haystack, token) {
			return ""
		}
	}
	return "remoto falso: exige presenca hibrida/presencial fora da sua localizacao"
}

func llmHTTPStatus(err error) int {
	msg := err.Error()
	for _, code := range []int{429, 503, 401, 403, 404} {
		if strings.Contains(msg, fmt.Sprintf("HTTP %d", code)) {
			return code
		}
	}
	return 0
}
func heuristicScore(config appConfig, job jobPost) int {
	score := 45
	text := strings.ToLower(job.Title + " " + job.Company + " " + job.Location + " " + job.Description)
	for _, term := range splitCSV(config.Form.Role + "," + config.Form.Roles + "," + config.Form.Seniority + "," + config.Form.Levels) {
		term = strings.ToLower(strings.TrimSpace(term))
		if term != "" && strings.Contains(text, term) {
			score += 12
		}
	}
	for _, term := range splitCSV(config.Form.Keywords) {
		term = strings.ToLower(strings.TrimSpace(term))
		if term != "" && strings.Contains(text, term) {
			score += 4
		}
	}
	if wantsRemote(config) && hasAny(text, "remote", "remoto", "home office", "híbrido", "hybrid") {
		score += 15
	}
	if hasAny(text, "senior", "sênior", "staff", "principal", "lead") && strings.Contains(strings.ToLower(config.Form.Seniority), "jun") {
		score -= 25
	}
	return boundedScore(score)
}

func searchQueries(config appConfig) []string {
	query := searchRoleQuery(config)
	if query == "" {
		return []string{"Software Engineer"}
	}
	return []string{query}
}

func splitCSV(raw string) []string {
	seen := map[string]bool{}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		value := strings.TrimSpace(part)
		key := strings.ToLower(value)
		if value != "" && !seen[key] {
			seen[key] = true
			out = append(out, value)
		}
	}
	return out
}

func targetLocation(config appConfig) string {
	if wantsRemote(config) {
		if strings.TrimSpace(config.Form.RemoteCountry) != "" {
			return strings.TrimSpace(config.Form.RemoteCountry)
		}
		return "Brazil"
	}
	if strings.TrimSpace(config.Form.OnsiteLocation) != "" {
		return strings.TrimSpace(config.Form.OnsiteLocation)
	}
	if strings.TrimSpace(config.Form.Location) != "" {
		return strings.TrimSpace(config.Form.Location)
	}
	return "Brazil"
}

func normalizedMaxJobs(config appConfig) int {
	limit := config.Form.MaxJobs
	if limit <= 0 {
		return 40
	}
	if limit > 200 {
		return 200
	}
	return limit
}

func wantsRemote(config appConfig) bool {
	mode := strings.ToLower(strings.TrimSpace(config.Form.WorkMode))
	return mode == "remote" || config.Toggles["remoteOnly"]
}

func (s *scraperBridge) log(level string, format string, args ...any) {
	message := format
	if len(args) > 0 {
		message = fmt.Sprintf(format, args...)
	}
	s.logger.Print(message)
	if s.logs != nil {
		s.logs.add(level, message)
	}
}

func searchRoleQuery(config appConfig) string {
	return booleanGroup(splitCSV(coalesce(config.Form.Roles, config.Form.Role)))
}

func booleanGroup(values []string) string {
	if len(values) == 0 {
		return ""
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		parts = append(parts, strconv.Quote(value))
	}
	if len(parts) == 0 {
		return ""
	}
	return "(" + strings.Join(parts, " OR ") + ")"
}

func filterBlacklistedCompanies(config appConfig, jobs []jobPost) []jobPost {
	blacklist := splitCSV(config.Form.Blacklist)
	if len(blacklist) == 0 {
		return jobs
	}
	filtered := make([]jobPost, 0, len(jobs))
	for _, job := range jobs {
		company := strings.ToLower(job.Company)
		blocked := false
		for _, term := range blacklist {
			term = strings.ToLower(strings.TrimSpace(term))
			if term != "" && strings.Contains(company, term) {
				blocked = true
				break
			}
		}
		if !blocked {
			filtered = append(filtered, job)
		}
	}
	return filtered
}

func heuristicScoreV2(config appConfig, job jobPost) (int, []string) {
	terms := candidateScoringTerms(config)
	text := normalizeText(job.Title + " " + job.Company + " " + job.Location + " " + job.Description)
	matched := 0
	for _, term := range terms {
		if containsTerm(text, term) {
			matched++
		}
	}
	score := 42
	if len(terms) > 0 {
		ratio := float64(matched) / float64(len(terms))
		score = 28 + int(ratio*67)
	}
	inTitle, inDescHead := jobMatchesSearchRoles(config, job)
	if inTitle {
		score += 8
	} else if inDescHead {
		score += 4
	}
	if wantsRemote(config) && hasAny(text, "remote", "remoto", "home office", "hibrido", "hybrid") {
		score += 6
	}
	return boundedScore(score), missingKeywords(config, job)
}

func jobMatchesSearchRoles(config appConfig, job jobPost) (inTitle bool, inDescHead bool) {
	roles := splitCSV(coalesce(config.Form.Roles, config.Form.Role))
	if len(roles) == 0 {
		return true, false
	}
	if titleMatchesAny(job.Title, roles) {
		return true, false
	}
	descHead := normalizeText(truncate(strings.TrimSpace(job.Description), 400))
	for _, role := range roles {
		if containsTerm(descHead, role) {
			return false, true
		}
	}
	return false, false
}

func filterVisibleJobs(jobs []jobPost) []jobPost {
	filtered := make([]jobPost, 0, len(jobs))
	for _, job := range jobs {
		if job.Status == statusApply || job.Status == statusAdjust {
			filtered = append(filtered, job)
		}
	}
	return filtered
}

var experienceYearsPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:experiencia|experience|exp\.?)\s*(?:minima|minimum|min\.?|de)?\s*(?:de\s+)?(\d{1,2})\s*\+?\s*(?:anos|years|yrs)\b`),
	regexp.MustCompile(`(?i)\b(?:minimo|minimum|min\.?|pelo menos|at least|requer(?:emos)?|exige(?:mos)?)\s+(?:de\s+)?(\d{1,2})\s*\+?\s*(?:anos|years|yrs)\b`),
	regexp.MustCompile(`(?i)\b(\d{1,2})\+\s*(?:anos|years|yrs)\s*(?:de\s+)?(?:experiencia|experience|exp\.?)\b`),
	regexp.MustCompile(`(?i)\bcom\s+(\d{1,2})\s*\+?\s*(?:anos|years|yrs)\b(?:\s*(?:de\s+)?(?:experiencia|experience|exp\.?))?`),
}

var versionBeforeYears = regexp.MustCompile(`(?i)(?:node|react|angular|vue|python|java|iso|aws|linux|windows|sql|\.net|net|stack)\s+\d`)

func experienceYearsRequired(text string) int {
	text = normalizeText(text)
	maxYears := 0
	for _, pattern := range experienceYearsPatterns {
		for _, match := range pattern.FindAllStringSubmatch(text, -1) {
			if len(match) < 2 {
				continue
			}
			years, err := strconv.Atoi(match[1])
			if err != nil || years <= 0 || years > 25 {
				continue
			}
			idx := strings.Index(text, match[0])
			if idx > 0 {
				windowStart := idx - 24
				if windowStart < 0 {
					windowStart = 0
				}
				if versionBeforeYears.MatchString(text[windowStart : idx+len(match[1])]) {
					continue
				}
			}
			if years > maxYears {
				maxYears = years
			}
		}
	}
	return maxYears
}

// seniorityBlockReason blocks jobs whose title hits a configured excluded
// level, or demands more years than the configured maximum. Seniority levels
// are checked on the title only to avoid description boilerplate noise.
func seniorityBlockReason(config appConfig, job jobPost) string {
	rules := job.seniorityRuleSet(config)
	title := strings.TrimSpace(job.Title)
	titleHaystack := normalizeText(title)
	roles := job.profileRoles
	if len(roles) == 0 {
		roles = effectiveSearchConfig(config).Roles
	}
	roleHaystack := normalizeText(strings.Join(roles, " "))
	roleLevels := map[string]bool{}
	roleAligned := false
	for _, role := range roles {
		if containsTerm(titleHaystack, role) {
			roleAligned = true
			break
		}
	}
	if roleAligned {
		for _, level := range detectSeniorityLevels(roleHaystack) {
			roleLevels[level] = true
		}
	}
	if reason := rules.outsideAllowedReason(title, roleLevels); reason != "" {
		return reason
	}
	for _, term := range rules.excluded {
		allowed := false
		for _, level := range canonicalSeniorityTerms(term) {
			if rules.allowed[level] {
				allowed = true
				break
			}
		}
		if allowed {
			continue
		}
		if roleAligned && containsExcludedSeniorityTerm(roleHaystack, term) {
			continue
		}
		if containsExcludedSeniorityTerm(titleHaystack, term) {
			return "senioridade fora do alvo: " + term
		}
	}

	descHaystack := normalizeText(job.Description)
	if rules.maxYears > 0 {
		haystack := titleHaystack
		if descHaystack != "" {
			haystack = titleHaystack + "\n" + descHaystack
		}
		if years := experienceYearsRequired(haystack); years > rules.maxYears {
			return fmt.Sprintf("senioridade fora do alvo: exige %d+ anos", years)
		}
	}
	return ""
}

func statusForScore(config appConfig, score int, missing []string) string {
	threshold := config.Form.ScoreCut
	if threshold <= 0 {
		threshold = 80
	}
	score = boundedScore(score)
	if score >= threshold {
		return statusApply
	}
	// [ADJUST] means a near-threshold match with concrete job terms that are not
	// evidenced in the candidate resume. It is deliberately gated by the fixed
	// missingKeywords result: a low score with no actionable/evidence gap is not
	// promoted merely because it is close to the cutoff.
	if score >= threshold-10 && len(missing) > 0 {
		return statusAdjust
	}
	return statusDiscard
}

func missingKeywords(config appConfig, job jobPost) []string {
	jobText := normalizeText(job.Title + "\n" + job.Description)
	jobTerms := make([]string, 0, 16)
	seen := map[string]bool{}
	// Requirements shown as gaps must come from the curated multi-domain skill
	// dictionary. Frequency-derived tokens are useful for broad resume scoring,
	// but on a job description they include noise such as "requires" and
	// "responsibilities", which is not actionable resume advice.
	for _, term := range knownResumeSkillTerms {
		if containsTerm(jobText, term) {
			appendResumeKeyword(&jobTerms, seen, term)
		}
	}
	resumeCorpus := normalizeText(config.Form.ResumeText + "\n" + strings.Join(candidateScoringTerms(config), " "))
	missing := make([]string, 0, 6)
	for _, keyword := range jobTerms {
		// This direction matters: the old implementation iterated the resume's
		// own terms and returned those absent from the job, then the UI told the
		// candidate to add words that were already on their resume (UX-017).
		if !containsTerm(resumeCorpus, keyword) {
			missing = append(missing, keyword)
		}
		if len(missing) >= 6 {
			break
		}
	}
	return missing
}

func titleMatchesRoles(config appConfig, title string) bool {
	return titleMatchesAny(title, splitCSV(coalesce(config.Form.Roles, config.Form.Role)))
}

func titleMatchesAny(title string, roles []string) bool {
	text := normalizeText(title)
	for _, role := range roles {
		if containsTerm(text, role) {
			return true
		}
		for _, part := range strings.Fields(normalizeText(role)) {
			if len(part) >= 4 && containsTerm(text, part) {
				return true
			}
		}
	}
	return false
}

func normalizeText(text string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case 'á', 'à', 'ã', 'â', 'ä', 'Á', 'À', 'Ã', 'Â', 'Ä':
			return 'a'
		case 'é', 'è', 'ê', 'ë', 'É', 'È', 'Ê', 'Ë':
			return 'e'
		case 'í', 'ì', 'î', 'ï', 'Í', 'Ì', 'Î', 'Ï':
			return 'i'
		case 'ó', 'ò', 'õ', 'ô', 'ö', 'Ó', 'Ò', 'Õ', 'Ô', 'Ö':
			return 'o'
		case 'ú', 'ù', 'û', 'ü', 'Ú', 'Ù', 'Û', 'Ü':
			return 'u'
		case 'ç', 'Ç':
			return 'c'
		default:
			return unicode.ToLower(r)
		}
	}, text)
}

func containsTerm(normalizedHaystack string, term string) bool {
	term = strings.TrimSpace(normalizeText(term))
	if term == "" {
		return false
	}
	if strings.Contains(term, " ") || strings.ContainsAny(term, "/+#") {
		return strings.Contains(normalizedHaystack, term)
	}
	re := regexp.MustCompile(`(^|[^a-z0-9])` + regexp.QuoteMeta(term) + `([^a-z0-9]|$)`)
	return re.FindStringIndex(normalizedHaystack) != nil
}

func filterByScore(config appConfig, jobs []jobPost) []jobPost {
	filtered := make([]jobPost, 0, len(jobs))
	for _, job := range jobs {
		if job.Status == statusApply || job.Status == statusAdjust {
			filtered = append(filtered, job)
		}
	}
	return filtered
}

func dedupeJobs(jobs []jobPost) []jobPost {
	seen := map[string]bool{}
	var out []jobPost
	for _, job := range jobs {
		key := strings.ToLower(strings.TrimSpace(job.URL))
		if key == "" {
			key = strings.ToLower(job.Source + ":" + job.Title + ":" + job.Company)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, job)
	}
	return out
}

func firstText(scope *goquery.Selection, selectors ...string) string {
	for _, selector := range selectors {
		text := strings.TrimSpace(scope.Find(selector).First().Text())
		if text != "" {
			return text
		}
	}
	return ""
}

func cleanText(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func normalizeLink(link string) string {
	link = strings.TrimSpace(link)
	if link == "" {
		return ""
	}
	if idx := strings.Index(link, "?"); idx >= 0 {
		link = link[:idx]
	}
	return link
}

func stableJobID(source string, link string) string {
	cleaned := strings.TrimSpace(link)
	cleaned = strings.TrimSuffix(cleaned, "/")
	if cleaned == "" {
		return source + ":" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return source + ":" + cleaned
}

func hasAny(text string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(text, strings.ToLower(term)) {
			return true
		}
	}
	return false
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	// Trim back to a valid UTF-8 rune boundary so a multibyte character (e.g.
	// an accented letter or em-dash) is never sliced in half, which would emit
	// a replacement character in the JSON/description output.
	cut := max
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}
