package server

import (
	"context"
	"io"
	"log"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
)

type sequenceResponse struct {
	status int
	body   string
}

type sequenceTransport struct {
	responses []sequenceResponse
	requests  []string
}

func (s *sequenceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	s.requests = append(s.requests, req.URL.String())
	response := sequenceResponse{status: 200, body: `{}`}
	if len(s.responses) > 0 {
		response = s.responses[0]
		s.responses = s.responses[1:]
	}
	if response.status == 0 {
		response.status = 200
	}
	return &http.Response{
		StatusCode: response.status,
		Body:       io.NopCloser(strings.NewReader(response.body)),
		Header:     make(http.Header),
	}, nil
}

// BUG-01 regression: the old fixed 12-minute budget must become a much
// shorter, clamped, user-tunable one so a run always terminates in a
// commercially reasonable time.
func TestSearchTimeoutDurationClampsToSaneRange(t *testing.T) {
	cases := []struct {
		name     string
		seconds  int
		expected time.Duration
	}{
		{"zero uses default", 0, defaultSearchTimeoutSeconds * time.Second},
		{"negative uses default", -10, defaultSearchTimeoutSeconds * time.Second},
		{"below floor clamps up", 5, minSearchTimeoutSeconds * time.Second},
		{"above ceiling clamps down", 100000, maxSearchTimeoutSeconds * time.Second},
		{"in range passes through", 180, 180 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config := appConfig{Form: configForm{SearchTimeoutSeconds: tc.seconds}}
			if got := searchTimeoutDuration(config); got != tc.expected {
				t.Fatalf("searchTimeoutDuration(%d) = %s, want %s", tc.seconds, got, tc.expected)
			}
		})
	}
}

// BUG-01 regression: the terminal message must distinguish a timeout from a
// normal empty result, so the UI never shows a misleading "0 vagas
// encontradas" for a run that was actually cut short.
func TestBuildSearchOutcomeMessageDistinguishesOutcomes(t *testing.T) {
	t.Run("timed out with nothing approved names the collected count", func(t *testing.T) {
		diag := searchDiagnostics{Collected: 12}
		msg := buildSearchOutcomeMessage(diag, true, 0)
		if !strings.Contains(msg, "tempo limite") || !strings.Contains(msg, "12") {
			t.Fatalf("expected a timeout message naming 12 collected jobs, got: %q", msg)
		}
	})

	t.Run("timed out but some jobs already approved mentions both counts", func(t *testing.T) {
		diag := searchDiagnostics{Collected: 12}
		msg := buildSearchOutcomeMessage(diag, true, 2)
		if !strings.Contains(msg, "tempo limite") || !strings.Contains(msg, "2") || !strings.Contains(msg, "12") {
			t.Fatalf("expected a timeout message naming both approved (2) and collected (12), got: %q", msg)
		}
	})

	t.Run("normal success names the approved count", func(t *testing.T) {
		msg := buildSearchOutcomeMessage(searchDiagnostics{Collected: 5}, false, 3)
		if !strings.Contains(msg, "concluida") || !strings.Contains(msg, "3") {
			t.Fatalf("expected a success message naming 3 approved jobs, got: %q", msg)
		}
	})

	t.Run("nothing collected at all", func(t *testing.T) {
		msg := buildSearchOutcomeMessage(searchDiagnostics{Collected: 0}, false, 0)
		if !strings.Contains(msg, "Nenhuma vaga recente") {
			t.Fatalf("expected the zero-collected message, got: %q", msg)
		}
	})

	t.Run("collected but everything filtered out", func(t *testing.T) {
		msg := buildSearchOutcomeMessage(searchDiagnostics{Collected: 8}, false, 0)
		if !strings.Contains(msg, "nenhuma passou nos filtros") || !strings.Contains(msg, "8") {
			t.Fatalf("expected a collected-but-filtered message naming 8, got: %q", msg)
		}
	})
}

// Phase 6 regression: when a run collects jobs but approves none, the
// message must name the top concrete reasons (not a bare "0 vagas"), so a
// user can tell a search failure apart from strict filters doing their job.
func TestTopDropReasonsNamesHighestSignalCuts(t *testing.T) {
	diag := searchDiagnostics{
		Collected:         12,
		DroppedSeniority:  6,
		DroppedDateWindow: 3,
		Discarded:         3,
	}
	got := topDropReasons(diag)
	want := "senioridade (6), data de postagem (3), score minimo (3)"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestTopDropReasonsEmptyWhenNothingDropped(t *testing.T) {
	if got := topDropReasons(searchDiagnostics{Collected: 5}); got != "" {
		t.Fatalf("expected no reasons when nothing was dropped, got %q", got)
	}
}

func TestTopDropReasonsCapsAtThreeReasons(t *testing.T) {
	diag := searchDiagnostics{
		DroppedSeniority:  9,
		DroppedDateWindow: 8,
		Discarded:         7,
		DroppedBlacklist:  6,
		DroppedFakeRemote: 5,
	}
	got := strings.Split(topDropReasons(diag), ", ")
	if len(got) != 3 {
		t.Fatalf("expected exactly 3 reasons, got %v", got)
	}
}

// The collected-but-filtered outcome message must include the top reasons
// inline, matching the plan's example UX copy style.
func TestBuildSearchOutcomeMessageIncludesTopDropReasons(t *testing.T) {
	diag := searchDiagnostics{Collected: 12, DroppedSeniority: 6, DroppedDateWindow: 3, Discarded: 3}
	msg := buildSearchOutcomeMessage(diag, false, 0)
	if !strings.Contains(msg, "Principais cortes: senioridade (6)") {
		t.Fatalf("expected the message to name top drop reasons, got: %q", msg)
	}
}

func TestLinkedInParserExtractsRealMarkup(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(`
			<div class="base-card">
				<a class="base-card__full-link" href="https://www.linkedin.com/jobs/view/123?trackingId=x"></a>
				<h3 class="base-search-card__title">Backend Engineer</h3>
				<h4 class="base-search-card__subtitle">Acme</h4>
				<span class="job-search-card__location">Remote</span>
			</div>`))
	if err != nil {
		t.Fatal(err)
	}
	var jobs []jobPost
	doc.Find(".base-card, .job-search-card").Each(func(_ int, card *goquery.Selection) {
		link, _ := card.Find("a.base-card__full-link").First().Attr("href")
		jobs = append(jobs, jobPost{
			ID:       stableJobID("linkedin", link),
			Title:    cleanText(firstText(card, ".base-search-card__title")),
			Company:  cleanText(firstText(card, ".base-search-card__subtitle")),
			Location: cleanText(firstText(card, ".job-search-card__location")),
			URL:      normalizeLink(link),
		})
	})
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	if jobs[0].Title != "Backend Engineer" || jobs[0].Company != "Acme" || jobs[0].Location != "Remote" {
		t.Fatalf("unexpected parsed job: %+v", jobs[0])
	}
	if strings.Contains(jobs[0].URL, "trackingId") {
		t.Fatalf("expected tracking query stripped, got %s", jobs[0].URL)
	}
}

func TestHeuristicScoreUsesCollectedText(t *testing.T) {
	config := defaultConfig()
	config.Form.Role = "Backend Engineer"
	config.Form.Seniority = "Senior"
	score := heuristicScore(config, jobPost{
		Title:       "Senior Backend Engineer",
		Location:    "Remote",
		Description: "Build APIs with Go and PostgreSQL.",
	})
	if score <= 45 {
		t.Fatalf("expected score above base for matching text, got %d", score)
	}
}

// Batch scoring goes through the same cascade as everything else, so a 429 on
// the primary provider still falls through to the configured fallback rather
// than dropping the whole batch to the offline heuristic.
// Observed on a real search: a scoring call started with seconds left on the
// search budget is debited from the user's daily allowance, then killed in flight,
// and the jobs are scored offline anyway. One of a free key's 200 daily requests,
// spent to reach exactly the result we would have had for nothing.
func TestScoreJobsBatchDoesNotStartACallItCannotFinish(t *testing.T) {
	store := newTestStore(t)
	if err := store.setAIAPIKeyForProvider("Gemini", "gemini-key"); err != nil {
		t.Fatal(err)
	}
	config := defaultConfig()
	config.Form.Provider = "Gemini"
	config.Toggles["compatibility"] = true

	transport := &sequenceTransport{responses: []sequenceResponse{
		{status: 200, body: `{"candidates":[{"content":{"parts":[{"text":"{\"scores\":[{\"id\":\"job:1\",\"match_score\":91}]}"}]}}]}`},
	}}
	bridge := newTestScraperBridge(transport)
	bridge.store = store
	bridge.api = &api{logger: log.New(io.Discard, "", 0), configStore: store, scraper: bridge}

	ctx, cancel := context.WithTimeout(context.Background(), minBatchScoringTime/2)
	defer cancel()

	batch, err := bridge.scoreJobsBatch(ctx, config, []jobPost{{ID: "job:1", Title: "SRE", Description: "K8s."}})
	if err != nil {
		t.Fatalf("running out of time is not an error the user needs to see: %v", err)
	}
	if len(transport.requests) != 0 {
		t.Fatalf("expected no request to be spent on a call that cannot finish, got %v", transport.requests)
	}
	// The job is simply absent, which is the caller's signal to score it offline.
	if _, ok := batch.Scores["job:1"]; ok {
		t.Fatalf("expected the job to be left for the offline heuristic, got %v", batch.Scores)
	}
}

// But a cached score costs neither time nor a request, so it must still be served
// when the clock has run out — otherwise the guard above throws away the very
// thing that makes a repeated sweep free.
func TestScoreJobsBatchStillServesTheCacheWithNoTimeLeft(t *testing.T) {
	store := newTestStore(t)
	if err := store.setAIAPIKeyForProvider("Gemini", "gemini-key"); err != nil {
		t.Fatal(err)
	}
	config := defaultConfig()
	config.Form.Provider = "Gemini"
	config.Toggles["compatibility"] = true

	job := jobPost{ID: "job:1", Title: "SRE", Description: "K8s."}
	store.llmCachePut(jobScoreCacheKey(config, job), "77", time.Now().Add(time.Hour))

	transport := &sequenceTransport{}
	bridge := newTestScraperBridge(transport)
	bridge.store = store
	bridge.api = &api{logger: log.New(io.Discard, "", 0), configStore: store, scraper: bridge}

	ctx, cancel := context.WithTimeout(context.Background(), minBatchScoringTime/2)
	defer cancel()

	batch, err := bridge.scoreJobsBatch(ctx, config, []jobPost{job})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batch.Scores["job:1"] != 77 {
		t.Fatalf("expected the cached score to be served with no time left, got %v", batch.Scores)
	}
	if len(transport.requests) != 0 {
		t.Fatalf("a cached score must not touch the network, got %v", transport.requests)
	}
}

func TestScoreJobsBatchFallsBackToTheNextProvider(t *testing.T) {
	store := newTestStore(t)
	if err := store.setAIAPIKeyForProvider("Gemini", "gemini-key"); err != nil {
		t.Fatal(err)
	}
	if err := store.setAIAPIKeyForProvider("OpenRouter", "openrouter-key"); err != nil {
		t.Fatal(err)
	}
	config := defaultConfig()
	config.Form.Provider = "Gemini"
	config.Form.Model = geminiFreeModel
	config.Form.Fallback1Provider = "OpenRouter"
	config.Form.Fallback1Model = "openai/gpt-4.1-mini"
	config.Form.AIDataConsent = true
	config.Toggles["compatibility"] = true

	// Every Gemini model has to refuse before the cascade is entitled to spend the
	// user's OTHER key: the sibling models cost nothing, since they run on the key
	// Gemini already has.
	responses := repeatResponse(geminiAttemptCount(config), sequenceResponse{status: 429, body: `{"error":"quota"}`})
	responses = append(responses, sequenceResponse{status: 200, body: `{"choices":[{"message":{"content":"{\"scores\":[{\"id\":\"job:1\",\"match_score\":91}]}"}}]}`})
	transport := &sequenceTransport{responses: responses}
	bridge := newTestScraperBridge(transport)
	bridge.store = store
	// This test asserts fallback-provider selection on a 429, not the
	// same-provider rate-limit retry — an empty (non-nil) delay schedule
	// opts out of retries so it doesn't consume the fixed 2-response
	// sequence meant for "gemini fails once, openrouter succeeds once".
	bridge.api = &api{
		logger: log.New(io.Discard, "", 0), configStore: store, scraper: bridge,
		cascadeRetryDelay: -1, cascadeRateLimitDelays: []time.Duration{},
		fetchGeminiCatalog: func(context.Context, string) ([]string, error) {
			return append([]string(nil), geminiFreeTierAllowlist...), nil
		},
	}

	batch, err := bridge.scoreJobsBatch(context.Background(), config, []jobPost{{
		ID:          "job:1",
		Title:       "Backend Engineer",
		Company:     "Acme",
		Location:    "Remote",
		Description: "Build APIs.",
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if batch.Scores["job:1"] != 91 {
		t.Fatalf("expected the fallback provider's score 91, got %v", batch.Scores)
	}
	// Gemini is asked once per model — every one of them on the key the user
	// already gave us — and only once they have all refused does the cascade reach
	// for the second provider.
	if len(transport.requests) != geminiAttemptCount(config)+1 {
		t.Fatalf("expected one request per gemini model plus the fallback, got %v", transport.requests)
	}
	if !strings.Contains(transport.requests[0], "generativelanguage.googleapis.com") {
		t.Fatalf("expected Gemini first, got %q", transport.requests[0])
	}
	if last := transport.requests[len(transport.requests)-1]; !strings.Contains(last, "openrouter.ai") {
		t.Fatalf("expected OpenRouter fallback last, got %q", last)
	}
}

func TestSearchQueryUsesRolesOnly(t *testing.T) {
	config := defaultConfig()
	config.Form.Roles = "DevOps Engineer, SRE"
	config.Form.Levels = "Junior, Pleno"

	query := searchQueries(config)[0]
	if strings.Contains(query, "Junior") || strings.Contains(query, "Pleno") {
		t.Fatalf("expected source query to avoid seniority terms, got %s", query)
	}
	if !strings.Contains(query, "DevOps Engineer") || !strings.Contains(query, "SRE") {
		t.Fatalf("expected source query to include roles, got %s", query)
	}
}

func TestStatusOverridesScraperNewStatus(t *testing.T) {
	config := defaultConfig()
	config.Form.ScoreCut = 80
	job := jobPost{Status: "new", Score: 86}

	job.Status = statusForScore(config, job.Score, nil)
	if job.Status != statusApply {
		t.Fatalf("expected scored job to become apply, got %s", job.Status)
	}
}

func TestAutoCleanKeepsAdjustResume(t *testing.T) {
	config := defaultConfig()
	jobs := filterByScore(config, []jobPost{
		{Title: "DevOps Pleno", Score: 65, Status: statusAdjust},
		{Title: "Random", Score: 99, Status: "new"},
	})
	if len(jobs) != 1 || jobs[0].Status != statusAdjust {
		t.Fatalf("expected adjust-resume job to survive auto clean, got %+v", jobs)
	}
}

func TestConfiguredKeywordsAffectEveryJobScore(t *testing.T) {
	config := defaultConfig()
	config.Form.Keywords = "AWS, Terraform, Kubernetes"
	matchingScore, matchingMissing := heuristicScoreV2(config, jobPost{
		Title:       "DevOps Engineer",
		Description: "Operate AWS environments with Terraform and Kubernetes.",
	})
	weakScore, _ := heuristicScoreV2(config, jobPost{
		Title:       "DevOps Engineer",
		Description: "Operate generic internal systems.",
	})
	if matchingScore <= weakScore {
		t.Fatalf("expected keyword-matching job to score higher, got matching=%d weak=%d", matchingScore, weakScore)
	}
	for _, evidenced := range []string{"AWS", "Terraform", "Kubernetes"} {
		if sliceContainsFold(matchingMissing, evidenced) {
			t.Fatalf("configured candidate term %q cannot be reported as a resume gap: %v", evidenced, matchingMissing)
		}
	}
}

func TestExtractResumeKeywordsPrefersKnownSkills(t *testing.T) {
	keywords := extractResumeKeywords("Porto Alegre 2024 investimentos AWS Terraform Kubernetes CI/CD GitHub Actions Grafana Prometheus Docker Linux")
	joined := strings.Join(keywords, ",")
	for _, expected := range []string{"AWS", "Terraform", "Kubernetes", "CI/CD", "GitHub Actions"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("expected %s in extracted keywords: %v", expected, keywords)
		}
	}
	for _, noise := range []string{"porto", "alegre", "investimentos", "2024"} {
		if strings.Contains(joined, noise) {
			t.Fatalf("did not expect noise token %s in keywords: %v", noise, keywords)
		}
	}
}

func TestExtractResumeKeywordsMultiDomain(t *testing.T) {
	cases := []struct {
		name   string
		resume string
		want   []string
		reject []string
	}{
		{
			name:   "nursing",
			resume: "Joao Silva. Enfermeiro com experiencia em UTI, triagem e prontuario eletronico. COREN ativo. Cuidados intensivos.",
			want:   []string{"UTI", "Triagem", "Prontuário", "COREN", "Cuidados Intensivos"},
			reject: []string{"joao", "silva"},
		},
		{
			name:   "finance",
			resume: "Maria Santos. Analista contabil: conciliacao bancaria, fluxo de caixa, SPED fiscal, fechamento contabil e Excel avancado.",
			want:   []string{"Contábil", "Conciliação", "Fluxo de Caixa", "SPED", "Excel"},
			reject: []string{"maria", "santos"},
		},
		{
			name:   "education",
			resume: "Fernanda Lima, professora com foco em BNCC, EAD via Moodle, planejamento de aula e avaliacao formativa.",
			want:   []string{"BNCC", "EAD", "Moodle", "Planejamento de Aula", "Avaliação"},
			reject: []string{"fernanda", "foco"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractResumeKeywords(tc.resume)
			joined := strings.ToLower(strings.Join(got, ","))
			for _, w := range tc.want {
				if !strings.Contains(joined, strings.ToLower(w)) {
					t.Fatalf("expected %q in keywords, got %v", w, got)
				}
			}
			for _, r := range tc.reject {
				for _, term := range got {
					if strings.EqualFold(term, r) {
						t.Fatalf("did not expect noise %q in keywords, got %v", r, got)
					}
				}
			}
		})
	}
}

func TestPrioritizeByRelevancePutsBestCandidatesFirst(t *testing.T) {
	config := defaultConfig()
	config.Form.Roles = "DevOps Engineer"
	config.Form.Keywords = "AWS, Terraform, Kubernetes, Docker"

	// Collection order deliberately buries the strong match at the end so a
	// naive maxJobs cut would drop it.
	jobs := []jobPost{
		{ID: "l:1", Source: "LinkedIn", Title: "Analista de Suporte", Description: "help desk"},
		{ID: "l:2", Source: "LinkedIn", Title: "Assistente Administrativo", Description: "rotinas"},
		{ID: "l:3", Source: "LinkedIn", Title: "DevOps Engineer", Description: "AWS, Terraform, Kubernetes, Docker"},
	}
	ordered := prioritizeByRelevance(config, jobs)
	if ordered[0].ID != "l:3" {
		t.Fatalf("expected strongest match first, got %q (%v)", ordered[0].Title, ids(ordered))
	}

	// With maxJobs = 1 the strong match must survive the cut.
	config.Form.MaxJobs = 1
	cut := interleaveBySource(ordered)
	if limit := normalizedMaxJobs(config); len(cut) > limit {
		cut = cut[:limit]
	}
	if len(cut) != 1 || cut[0].ID != "l:3" {
		t.Fatalf("expected DevOps job to survive maxJobs=1 cut, got %v", ids(cut))
	}
}

func TestPrioritizeByRelevanceStableWithoutSignal(t *testing.T) {
	// No keywords and no resume -> no signal -> order must be unchanged.
	config := defaultConfig()
	config.Form.Keywords = ""
	config.Form.ResumeText = ""
	config.Form.Roles = ""
	jobs := []jobPost{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	ordered := prioritizeByRelevance(config, jobs)
	for i, want := range []string{"a", "b", "c"} {
		if ordered[i].ID != want {
			t.Fatalf("expected stable order a,b,c, got %v", ids(ordered))
		}
	}
}

func ids(jobs []jobPost) []string {
	out := make([]string, len(jobs))
	for i, j := range jobs {
		out[i] = j.ID
	}
	return out
}

func TestTruncateDoesNotSplitUTF8(t *testing.T) {
	// "Sênior" — the ê is a 2-byte rune; a byte-slice at length 2 would split it.
	for _, max := range []int{1, 2, 3, 4, 5} {
		out := truncate("Sênior", max)
		if !utf8.ValidString(out) {
			t.Fatalf("truncate(%d) produced invalid UTF-8: %q", max, out)
		}
	}
	if got := truncate("abc", 10); got != "abc" {
		t.Fatalf("short input should be unchanged, got %q", got)
	}
}

func TestCleanText(t *testing.T) {
	if got := cleanText(" Backend\n\tEngineer  "); got != "Backend Engineer" {
		t.Fatalf("unexpected clean text: %q", got)
	}
	if strings.Contains(cleanText("a\nb"), "\n") {
		t.Fatal("expected newlines to be collapsed")
	}
}

func TestHeuristicRejectsTangentialTitleWithoutRoleMatch(t *testing.T) {
	config := defaultConfig()
	config.Form.Roles = "Farmaceutico, Farmaceutica, Biomedicina, Laboratorio"
	config.Form.Keywords = "farmacia, biomedicina, laboratorio"

	score, _ := heuristicScoreV2(config, jobPost{
		Title:       "Comprador Farma",
		Description: "Responsavel por compras de insumos hospitalares.",
	})
	if score >= 50 {
		t.Fatalf("expected tangential title with no CV keyword overlap to score low, got %d", score)
	}

	strongScore, _ := heuristicScoreV2(config, jobPost{
		Title:       "Farmaceutico Hospitalar",
		Description: "Atuacao em farmacia clinica e laboratorio.",
	})
	if strongScore <= score {
		t.Fatalf("expected CV-aligned job to outscore tangential match, got strong=%d weak=%d", strongScore, score)
	}
}

func TestCandidateScoringTermsUsesResumeWhenKeywordsEmpty(t *testing.T) {
	config := defaultConfig()
	config.Form.Keywords = ""
	config.Form.ResumeText = "Engenheiro DevOps com AWS, Terraform, Kubernetes e Docker em producao."

	terms := candidateScoringTerms(config)
	if len(terms) == 0 {
		t.Fatal("expected resume-derived scoring terms")
	}
	joined := strings.Join(terms, ",")
	for _, expected := range []string{"AWS", "Terraform", "Kubernetes", "Docker"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("expected %s in resume-derived terms: %v", expected, terms)
		}
	}

	score, _ := heuristicScoreV2(config, jobPost{
		Title:       "DevOps Engineer",
		Description: "AWS, Terraform, Kubernetes and Docker required.",
	})
	if score < 55 {
		t.Fatalf("expected strong resume/job overlap to score high, got %d", score)
	}
}

func TestCandidateScoringTermsRespectsUserCustomization(t *testing.T) {
	config := defaultConfig()
	config.Form.ResumeText = "Perfil com AWS, Terraform, Kubernetes, Docker, Linux, Grafana."
	config.Form.Keywords = "AWS, Terraform"

	scoreFull, _ := heuristicScoreV2(config, jobPost{
		Title:       "Cloud Engineer",
		Description: "Must know AWS and Terraform deeply.",
	})
	config.Form.Keywords = "Grafana, Prometheus"
	scoreCustom, _ := heuristicScoreV2(config, jobPost{
		Title:       "Cloud Engineer",
		Description: "Must know AWS and Terraform deeply.",
	})
	if scoreCustom >= scoreFull {
		t.Fatalf("expected customized keyword list to change score, full=%d custom=%d", scoreFull, scoreCustom)
	}
}

func TestInterleaveBySourceRoundRobins(t *testing.T) {
	jobs := []jobPost{
		{Source: "LinkedIn", Title: "A"},
		{Source: "LinkedIn", Title: "B"},
		{Source: "LinkedIn", Title: "C"},
		{Source: "Indeed", Title: "D"},
		{Source: "Indeed", Title: "E"},
	}
	got := interleaveBySource(jobs)
	if len(got) != 5 {
		t.Fatalf("expected 5 jobs, got %d", len(got))
	}
	want := []string{"LinkedIn", "Indeed", "LinkedIn", "Indeed", "LinkedIn"}
	for i, src := range want {
		if got[i].Source != src {
			t.Fatalf("index %d: expected %s got %s (full=%v)", i, src, got[i].Source, got)
		}
	}
}

func TestScoringDiagnosticsCountEveryOfflinePath(t *testing.T) {
	var diagnostics searchDiagnostics
	diagnostics.addOfflineScore(scoreSourceOfflineNoKey)
	diagnostics.addOfflineScore(scoreSourceOfflineFallback)
	diagnostics.addOfflineScore(scoreSourceOfflinePrefilter)

	if diagnostics.ScoredOffline != 3 {
		t.Fatalf("expected all three offline paths counted, got %+v", diagnostics)
	}
	if diagnostics.SkippedByPrefilter != 1 {
		t.Fatalf("expected only the prefilter path counted as skipped, got %+v", diagnostics)
	}
}

func TestMissingKeywordsExcludesTermsAlreadyOnTheResume(t *testing.T) {
	config := defaultConfig()
	config.Form.ResumeText = "Platform engineer experienced with AWS and Docker."
	config.Form.Keywords = ""
	job := jobPost{
		Title:       "Platform Engineer",
		Description: "Requires AWS, Kubernetes, and Terraform experience.",
	}

	missing := missingKeywords(config, job)
	for _, term := range missing {
		if strings.EqualFold(term, "AWS") || strings.EqualFold(term, "Docker") {
			t.Fatalf("resume-owned term leaked into missing requirements: %v", missing)
		}
		if strings.EqualFold(term, "requires") || strings.EqualFold(term, "experience") {
			t.Fatalf("job-description noise leaked into missing requirements: %v", missing)
		}
	}
	if !sliceContainsFold(missing, "Kubernetes") || !sliceContainsFold(missing, "Terraform") {
		t.Fatalf("expected job requirements absent from the resume, got %v", missing)
	}
}

func TestStatusAdjustRequiresAConcreteResumeGap(t *testing.T) {
	config := defaultConfig()
	config.Form.ScoreCut = 80
	if got := statusForScore(config, 75, []string{"Kubernetes"}); got != statusAdjust {
		t.Fatalf("near-threshold job with a gap=%q, want %q", got, statusAdjust)
	}
	if got := statusForScore(config, 75, nil); got != statusDiscard {
		t.Fatalf("near-threshold job without a gap=%q, want %q", got, statusDiscard)
	}
}

func sliceContainsFold(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(value, want) {
			return true
		}
	}
	return false
}
