package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Scoring used to cost one LLM call per job. A 40-job search therefore spent 40
// requests — more than a Google AI Studio free-tier key has in a minute, and a
// serious dent in what it has in a day. The search was the app's single largest
// consumer of a budget the Resume Studio also needs.
//
// Batching and the score cache are what pay for that, and the measurements are
// unambiguous. Two consecutive real searches on the shipped defaults: 33 jobs
// collected, 24 scored, 15 approved — three AI requests the first time, and zero
// the second, every score served from cache, same fifteen jobs.
//
// titleRelevant below is the third piece, and honesty about it matters more than
// the credit: on those same searches it filtered out NOTHING. Zero jobs skipped,
// both runs. That is not a bug, it is arithmetic — the search query is built from
// the same roles the filter tests against, so the boards return jobs that match
// them by construction, and the test also passes on a role appearing anywhere in
// the description. It earns its place only for someone whose boards return noise
// their query did not ask for, and searchDiagnostics.SkippedByPrefilter now says
// out loud when that happens. On an ordinary search, assume it does nothing.

const (

	// maxBatchJobDescriptionChars caps each posting inside a batch prompt. Ten
	// full descriptions would blow the token budget the batching is meant to
	// protect; the top of a posting is where the requirements live.
	maxBatchJobDescriptionChars = 1200

	// minBatchScoringTime is how much of the search budget must be left for a
	// scoring call to be worth starting at all.
	//
	// Below it the call cannot finish, and starting it anyway is the worst of both
	// worlds — observed on a real search: the request is debited from the user's
	// daily budget before it leaves, the search then sits waiting for an answer
	// until its own deadline kills it, and the jobs are scored offline in the end
	// regardless. One of a free key's 200 daily requests, spent to arrive at
	// exactly the result we would have had for free.
	//
	// Batch calls were measured answering in roughly 5-15s; 25 leaves room for one
	// rate-limit pause on top.
	minBatchScoringTime = 25 * time.Second

	// A partially filled batch must not wait for ten serial description fetches.
	// The collection preview is already visible, and this timer bounds how long
	// it stays in the honest "AI scoring pending" state before the model is asked.
	jobScoreBatchMaxWait = 8 * time.Second
)

// A synchronous free-tier search groups 20 jobs in economy mode and 15 in
// quality mode. Both stay within the measured prompt envelope while reducing a
// 40-job sweep to two or three calls instead of the old four.
func jobScoreBatchSizeFor(config appConfig) int {
	if normalizeAIMode(config.Form.AIMode) == aiModeFreeQuality {
		return 15
	}
	return 20
}

// titleRelevant is the deterministic "is this even in the right ballpark" test:
// the searched role appears in the title, or anywhere in the posting. It is
// deliberately lenient — it exists to skip the obvious mismatches an LLM call
// would be wasted on, not to make the final judgement, which statusForScore
// still does off the score.
//
// It used to read config.Form.Roles, and that was UX-016. Collection is driven
// by parseSearchProfiles, which ignores Form.Roles entirely whenever an advanced
// profile exists — so a user who moved from Backend Developer to Registered
// Nurse could have a nursing profile fetching nursing jobs while a stale
// "Backend Developer" sat in Form.Roles prefiltering every one of them out. All
// seventeen jobs of a real search were skipped and scored offline, with a valid
// key and a passing provider test, and the only trace was one muted log line.
//
// The roles now come from the profile that fetched the job (tagJobsWithProfile
// attaches it), falling back to the union of every profile the search ran. A job
// can no longer be rejected as off-target by a role the search never used.
func titleRelevant(config appConfig, job jobPost) bool {
	roles := job.profileRoles
	if len(roles) == 0 {
		roles = effectiveSearchConfig(config).Roles
	}
	if len(roles) == 0 {
		return true
	}
	if titleMatchesAny(job.Title, roles) {
		return true
	}
	text := normalizeText(job.Title + " " + job.Description)
	for _, role := range roles {
		if containsTerm(text, role) {
			return true
		}
	}
	return false
}

func filterByTitleRelevance(config appConfig, jobs []jobPost) []jobPost {
	filtered := make([]jobPost, 0, len(jobs))
	for _, job := range jobs {
		if titleRelevant(config, job) {
			filtered = append(filtered, job)
		}
	}
	return filtered
}

// llmScoringEnabled reports whether the AI scorer is available at all. Without
// it every job falls to the offline heuristic, which is the app's designed
// no-key behaviour.
func llmScoringEnabled(config appConfig, apiKey string) bool {
	return strings.TrimSpace(apiKey) != "" && config.Toggles["compatibility"]
}

func offlineScoreSource(config appConfig, apiKey string, skippedByPrefilter bool) ScoreSource {
	if skippedByPrefilter {
		return scoreSourceOfflinePrefilter
	}
	if !llmScoringEnabled(config, apiKey) {
		return scoreSourceOfflineNoKey
	}
	return scoreSourceOfflineFallback
}

func scoreBatchPrompt(config appConfig, jobs []jobPost) string {
	levels := coalesce(config.Form.Levels, config.Form.Seniority)
	excluded := coalesce(config.Form.ExcludedLevels, "(nenhum)")

	var postings strings.Builder
	for _, job := range jobs {
		fmt.Fprintf(&postings, `
--- VAGA id=%s
Titulo: %s
Empresa: %s
Local: %s
Modalidade: %s
Fonte: %s
Descricao:
%s
`,
			job.ID,
			job.Title,
			job.Company,
			job.Location,
			coalesce(job.Modality, config.Form.WorkMode),
			job.Source,
			truncate(job.Description, maxBatchJobDescriptionChars),
		)
	}

	return fmt.Sprintf(`Voce e um recrutador tecnico STRICT e especialista em ATS. Compare rigorosamente
os requisitos de CADA vaga com o perfil do candidato. Nao infle notas.

=== SKILLS/KEYWORDS DO CANDIDATO (ground truth — editaveis, derivadas do curriculo) ===
%s
Avalie cada vaga ESTRITAMENTE contra essa lista e contra o curriculo abaixo.
Se a maioria das skills do candidato estiver ausente de uma vaga, o match_score dela DEVE ser baixo.

=== SENIORIDADE ALVO ===
Niveis desejados: %s
Niveis bloqueados: %s
Maximo de anos de experiencia: %d
Se o titulo ou a descricao de uma vaga claramente exige um nivel bloqueado ou mais anos que o
maximo, o match_score dela DEVE ser 0.

=== REMOTO vs HIBRIDO ===
Se uma vaga foi marcada como Remote mas a descricao exige QUALQUER presenca fisica
(pt: 'hibrido','presencial','ida ao escritorio','dias no escritorio';
en: 'hybrid','on-site','days in office'), o match_score dela DEVE ser 0.

Avalie cada vaga de forma INDEPENDENTE — nao normalize as notas entre elas, nao force variedade.
Responda SOMENTE um JSON valido, com uma entrada para CADA vaga, usando o id exato recebido:
{"scores": [{"id": "<id da vaga>", "match_score": <int 0-100>}]}

=== CURRICULO ===
%s

=== VAGAS (%d) ===%s`,
		formatCandidateScoringTerms(config),
		coalesce(levels, "(nenhum)"),
		excluded,
		config.Form.MaxYears,
		truncate(config.Form.ResumeText, 2200),
		len(jobs),
		postings.String(),
	)
}

// decodeBatchScores reads the id/score pairs. A job the model forgot, or scored
// nonsensically, is simply absent from the map: the caller falls back to the
// offline heuristic for it rather than inventing a number.
func decodeBatchScores(raw string) (map[string]int, error) {
	var payload struct {
		Scores []struct {
			ID         string `json:"id"`
			Score      int    `json:"score"`
			MatchScore int    `json:"match_score"`
		} `json:"scores"`
	}
	if err := json.Unmarshal([]byte(stripJSONFence(raw)), &payload); err != nil {
		return nil, fmt.Errorf("%w: %v", errInvalidAIJSON, err)
	}

	scores := make(map[string]int, len(payload.Scores))
	for _, entry := range payload.Scores {
		id := strings.TrimSpace(entry.ID)
		if id == "" {
			continue
		}
		score := entry.MatchScore
		if score == 0 {
			score = entry.Score
		}
		scores[id] = boundedScore(score)
	}
	if len(scores) == 0 {
		return nil, fmt.Errorf("%w: batch scoring returned no usable scores", errInvalidAIJSON)
	}
	return scores, nil
}

// jobScoreCacheTTL is how long a job's score stays reusable. A score is a pure
// function of (resume, posting, scoring settings) — all of which are in the key
// — so the only thing time buys us is a chance to pick up a better model. A week
// is long enough to cover radar re-scoring the same postings for as long as they
// stay open.
const jobScoreCacheTTL = 7 * 24 * time.Hour

// jobScoreCacheKey identifies a score by everything that shapes it. Change the
// resume, the posting, the seniority rules or the model, and this changes with
// it — so a stale score can never be served for a question we have not actually
// asked before.
func jobScoreCacheKey(config appConfig, job jobPost) string {
	sum := sha256.New()
	parts := []string{
		"job_score_v2",
		strings.ToLower(strings.TrimSpace(config.Form.Provider)),
		strings.TrimSpace(config.Form.Model),
		normalizeAIMode(config.Form.AIMode),
		formatCandidateScoringTerms(config),
		coalesce(config.Form.Levels, config.Form.Seniority),
		config.Form.ExcludedLevels,
		strconv.Itoa(config.Form.MaxYears),
		config.Form.WorkMode,
		truncate(config.Form.ResumeText, 2200),
		job.ID,
		job.Title,
		job.Company,
		job.Location,
		coalesce(job.Modality, config.Form.WorkMode),
		job.Source,
		truncate(job.Description, maxBatchJobDescriptionChars),
	}
	for _, part := range parts {
		sum.Write([]byte(part))
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// batchScoreResult separates what we knew already from what we had to ask for,
// so the search can report how much of a sweep was free.
type batchScoreResult struct {
	Scores    map[string]int
	Sources   map[string]ScoreSource
	CacheHits int
}

// scoreJobsBatch scores a batch and returns id -> score. Jobs whose score is
// already cached cost nothing; only the rest reach the provider, and a batch
// that is entirely cached makes no request at all.
//
// That distinction is what makes radar mode survivable on a free key. Radar
// re-runs a full search every 20 minutes and processedKeys only skips jobs the
// user has actually applied to or dismissed, so every sweep used to re-score the
// same postings from scratch — at 72 sweeps a day that alone can outrun a
// free-tier daily quota, to learn nothing new.
//
// On failure it returns the error rather than swallowing it, so the caller can
// tell "the AI is gone for the day" (worth telling the user about) from "one
// batch came back garbled" (not worth alarming them about).
func (s *scraperBridge) scoreJobsBatch(ctx context.Context, config appConfig, jobs []jobPost) (batchScoreResult, error) {
	result := batchScoreResult{Scores: map[string]int{}, Sources: map[string]ScoreSource{}}
	if s.api == nil || len(jobs) == 0 {
		return result, nil
	}
	// Resolve the first-use pin before computing per-job cache keys. Otherwise a
	// batch generated by the migrated alias would be stored under the missing pin
	// and become unreachable as soon as the persisted migration is reloaded.
	resolvedConfig, err := s.api.validateGeminiModelOnFirstUse(ctx, config)
	if err != nil {
		return result, err
	}
	config = resolvedConfig

	now := time.Now()
	ask := make([]jobPost, 0, len(jobs))
	for _, job := range jobs {
		if raw, ok := s.store.llmCacheGet(jobScoreCacheKey(config, job), now); ok {
			if score, err := strconv.Atoi(raw); err == nil {
				result.Scores[job.ID] = boundedScore(score)
				result.Sources[job.ID] = scoreSourceAICache
				result.CacheHits++
				s.store.recordLLMUsage(now, "job_score", config.Form.Provider, config.Form.Model, true)
				continue
			}
		}
		ask = append(ask, job)
	}

	if len(ask) == 0 {
		s.log("muted", "[ LLM ] %d vaga(s) já pontuadas em cache — nenhuma chamada", len(jobs))
		return result, nil
	}

	// Cached scores are free and instant, so they are served above regardless. A
	// network call is neither: with too little of the search budget left it will be
	// killed mid-flight, having already spent one of the user's daily requests. The
	// jobs below fall to the offline heuristic — the same place they would have
	// landed after the timeout, minus the request and minus the wait.
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < minBatchScoringTime {
		s.log("muted", "[ LLM ] %d vaga(s) sem tempo hábil de IA antes do fim da busca — heurística offline, sem gastar requisição", len(ask))
		return result, nil
	}
	if !config.Form.AIDataConsent && !isLocalProvider(config.Form.Provider) {
		return result, errAIDataConsentRequired
	}

	cascade, err := s.api.runLLMWithFallback(ctx, "job_score", config, scoreBatchPrompt(config, ask), s.generateJSONOnce)
	if err != nil {
		s.log("warning", "[ LLM ] scoring em lote falhou (%d vagas): %v — heurística offline", len(ask), err)
		return result, err
	}

	fresh, err := decodeBatchScores(cascade.Raw)
	if err != nil {
		s.log("warning", "[ LLM ] resposta de scoring ilegível (%d vagas): %v — heurística offline", len(ask), err)
		return result, err
	}

	for _, job := range ask {
		score, ok := fresh[job.ID]
		if !ok {
			continue
		}
		result.Scores[job.ID] = score
		result.Sources[job.ID] = scoreSourceAI
		s.store.llmCachePut(jobScoreCacheKey(config, job), strconv.Itoa(score), now.Add(jobScoreCacheTTL))
	}
	return result, nil
}
