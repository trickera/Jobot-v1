package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type llmPurpose string

type jsonGenerator func(ctx context.Context, config appConfig, apiKey, prompt string) (string, error)

type cascadeResult struct {
	Raw          string
	ProviderUsed string
	ModelUsed    string
}

type cascadeAttempt struct {
	Provider string
	Model    string
}

const llmRepairSuffix = "\n\nRETORNE APENAS JSON VÁLIDO, sem texto extra."

// llmCascadeBudget bounds one logical LLM request end to end — every provider
// attempt, every repair call and every rate-limit sleep. Without it the worst
// case (single free-tier provider, all 429) was ~195s of backoff inside a
// single HTTP request the client waited on indefinitely.
const llmCascadeBudget = 4 * time.Minute

func (a *api) runLLMWithFallback(ctx context.Context, purpose llmPurpose, config appConfig, prompt string, generate jsonGenerator) (cascadeResult, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, llmCascadeBudget)
		defer cancel()
	}
	redaction := redactPromptPII(purpose, prompt)
	prompt = redaction.prompt
	var err error
	config, err = a.validateGeminiModelOnFirstUse(ctx, config)
	if err != nil {
		return cascadeResult{}, err
	}

	// Every provider call in the app — job scoring and every Resume Studio step
	// — funnels through this function, which makes it the one place the shared
	// per-minute budget can be enforced. Wrapping the generator rather than
	// gating at the call site means the repair call and both retry ladders spend
	// from the same budget instead of quietly going around it.
	priority := llmPriorityForPurpose(purpose)
	generate = a.rateLimited(generate, priority, purpose)
	generate = operationLimited(generate, config.Form.AIMode, purpose)

	attempts := cascadeAttemptsForPurpose(config, purpose)

	cacheKey := ""
	if len(attempts) > 0 {
		cacheKey = llmCacheKey(purpose, attempts[0].Provider, attempts[0].Model, prompt)
		if cached, ok := a.llmCache.get(cacheKey, time.Now()); ok {
			a.logCascadeCacheHit(purpose, cached)
			a.configStore.recordLLMUsage(time.Now(), purpose, cached.ProviderUsed, cached.ModelUsed, true)
			// Cache entries remain redacted. The same non-PII resume content can be
			// reused safely, while this operation restores only its own local
			// identifiers instead of inheriting those from an earlier document.
			cached.Raw = redaction.restore(cached.Raw)
			return cached, nil
		}
	}
	if purposeSharesCandidateData(purpose) && !config.Form.AIDataConsent && !isLocalProvider(config.Form.Provider) {
		return cascadeResult{}, errAIDataConsentRequired
	}

	var lastErr error

	// The key is derived from the primary attempt, not from whichever provider
	// ends up answering: it identifies "this prompt under this configuration",
	// and the stored result still records who actually answered it.
	succeed := func(attempt cascadeAttempt, providerKey, raw string) cascadeResult {
		a.logCascadeSuccess(purpose, attempt)
		cachedResult := cascadeResult{Raw: raw, ProviderUsed: providerKey, ModelUsed: attempt.Model}
		if cacheKey != "" {
			a.llmCache.put(cacheKey, cachedResult, time.Now())
		}
		cachedResult.Raw = redaction.restore(cachedResult.Raw)
		return cachedResult
	}

	// attempts now lists a provider's sibling models as well as its fallbacks, and
	// those two maps are what stop that from turning into a request amplifier on
	// the free key it exists to protect.
	//
	// exhausted: some failures are the PROVIDER's, not the model's — a rejected
	// key, an unreachable host. Every sibling model sits behind that same key and
	// that same host, so trying them buys nothing but the user's time.
	//
	// laddered: the rate-limit ladder sleeps up to 95s. Running one per sibling
	// model turned a single 429 into sixteen calls and four minutes of waiting.
	// One provider, one ladder; siblings after it get a single try each.
	exhausted := map[string]bool{}
	laddered := map[string]bool{}

	for index, attempt := range attempts {
		// Once the budget is spent, starting another provider only makes the
		// caller wait longer for the same failure.
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return cascadeResult{}, lastErr
			}
			return cascadeResult{}, err
		}
		providerKey := cascadeProviderKey(attempt.Provider)
		if providerKey == "" {
			continue
		}
		if exhausted[providerKey] {
			continue
		}
		if a.attemptOnCooldown(attempt, time.Now()) {
			continue
		}

		apiKey, err := a.configStore.aiAPIKeyForProvider(attempt.Provider)
		if err != nil {
			return cascadeResult{}, err
		}
		if strings.TrimSpace(apiKey) == "" && !isLocalProvider(attempt.Provider) {
			continue
		}

		attemptConfig := config
		attemptConfig.Form.Provider = attempt.Provider
		attemptConfig.Form.Model = attempt.Model
		// Ask for roughly what this task's answer needs: too small truncates the
		// JSON, too large starves a token-per-minute budget (see llm_json.go).
		attemptConfig.maxOutputTokens = llmMaxOutputTokensFor(purpose)

		raw, err := generate(ctx, attemptConfig, apiKey, prompt)
		if err == nil && validAIJSON(raw) {
			return succeed(attempt, providerKey, raw), nil
		}
		if err == nil {
			err = fmt.Errorf("%w: provider returned non-JSON", errInvalidAIJSON)
			repairRaw, repairErr := generate(ctx, attemptConfig, apiKey, prompt+llmRepairSuffix)
			if repairErr == nil && validAIJSON(repairRaw) {
				return succeed(attempt, providerKey, repairRaw), nil
			}
			if repairErr == nil {
				repairErr = fmt.Errorf("%w: provider returned non-JSON after repair", errInvalidAIJSON)
			}
			lastErr = repairErr
			a.logCascadeAttempt(purpose, attempt, classifyProviderError(repairErr), nextCascadeProvider(attempts, index+1))
			continue
		}

		code := classifyProviderError(err)
		lastErr = err
		if errors.Is(err, errLLMOperationBudgetSpent) {
			return cascadeResult{}, err
		}

		// A 429 is the one error whose handling turns on details we cannot see
		// from the outside — which quota was breached, and whether the provider
		// expects it to clear. Getting that reading wrong is what parked a working
		// key for an hour once already, and the only way anyone can check our
		// reading is to see what the provider actually sent. Error bodies carry no
		// credentials, so this is safe to keep.
		if code == "rate_limited" || code == "quota_exhausted" {
			a.logs.add("muted", fmt.Sprintf("[ LLM ] resposta 429 de %s: %s", providerKey, rateLimitDiagnostics(err)))
		}

		switch code {
		case "unauthorized", "forbidden":
			// The key is what was rejected, so every model behind it is out.
			a.setProviderCooldown(providerKey, 10*time.Minute)
		case "model_not_found":
			// The model is gone, not busy. No ladder brings it back and every later
			// call this session would 404 identically, so park the name and let the
			// next attempt — a sibling model on the same key — answer instead.
			// Without this the cascade fell straight through to the next PROVIDER,
			// which a free user does not have, and the app was simply dead for them.
			a.setModelCooldown(attempt, modelRetiredCooldown)
			a.logs.add("warning", fmt.Sprintf(
				"[ LLM ] %s: modelo %s não existe mais para esta chave (404). Trocando de modelo; ajuste Configurações > IA para não passar por isto de novo.",
				providerKey, attempt.Model))
		case "quota_exhausted":
			// Two very different things reach this branch, and telling a user to
			// "come back tomorrow" when it was OUR budget that ran out — a number
			// they can change in one edit — is a lie that costs them a day.
			if errors.Is(err, errQuotaBudgetSpent) {
				a.logs.add("warning", fmt.Sprintf(
					"[ LLM ] %s: orçamento diário do app esgotado (%v). Aumente llmRequestsPerDay para continuar hoje.",
					providerKey, err))
				// Our budget is spent for the whole app, not for this provider: no
				// other model and no other key can serve this call either. Stop, or
				// we log one failure per remaining attempt for a refusal that is ours.
				return cascadeResult{}, err
			}

			// The provider's quota is metered per MODEL on the free tiers this app
			// targets — measured on one Gemini key in one second: 2.0-flash refused
			// while flash-lite-latest answered. Parking the key would throw away the
			// models that are still working, so park only the model that ran out.
			//
			// The provider's own words go in the log: sidelining a model for an hour
			// is a heavy call to make on a classification, and when it is wrong the
			// only way anyone finds out is by reading what the provider actually
			// said — which is exactly how the first version of this check was caught
			// misreading a per-minute limit as a dead key.
			a.setModelCooldown(attempt, providerDailyQuotaCooldown)
			a.logs.add("warning", fmt.Sprintf(
				"[ LLM ] %s/%s: cota diária esgotada neste modelo — pausado até amanhã. Cota estourada: %s",
				providerKey, attempt.Model, quotaBreachedDescription(err)))
		case "rate_limited":
			// The provider rate-limited us even though the limiter thought we were
			// inside budget, so our budget was too generous. Halve it for a while.
			a.llmLimiter.penalize(llmRequestsPerMinute(attemptConfig)/2, time.Now().Add(rateLimitPenaltyWindow))

			// One ladder per provider. It sleeps up to 95 seconds, and this provider's
			// sibling models are behind the same ceiling — a second ladder would spend
			// another minute and a half to be told the same thing. Park this model and
			// give the siblings one try each instead.
			if laddered[providerKey] {
				a.setModelCooldown(attempt, 60*time.Second)
				break
			}
			laddered[providerKey] = true

			retryRaw, retryErr := a.retryRateLimited(ctx, generate, attemptConfig, apiKey, prompt, err)
			if retryErr == nil && validAIJSON(retryRaw) {
				return succeed(attempt, providerKey, retryRaw), nil
			}
			if retryErr == nil {
				retryErr = fmt.Errorf("%w: provider returned non-JSON after rate-limit retry", errInvalidAIJSON)
			}
			lastErr = retryErr
			code = classifyProviderError(retryErr)
			// Both ceilings are per-model on the free tiers, so a sibling model is
			// still worth trying — park this one, not the key.
			if code == "quota_exhausted" {
				a.setModelCooldown(attempt, providerDailyQuotaCooldown)
			} else {
				a.setModelCooldown(attempt, 60*time.Second)
			}
		case "provider_unavailable", "network_error", "local_model_unreachable":
			retryRaw, retryErr := a.retrySameProvider(ctx, generate, attemptConfig, apiKey, prompt)
			if retryErr == nil && validAIJSON(retryRaw) {
				return succeed(attempt, providerKey, retryRaw), nil
			}
			if retryErr == nil {
				retryErr = fmt.Errorf("%w: provider returned non-JSON after retry", errInvalidAIJSON)
			}
			lastErr = retryErr
			code = classifyProviderError(retryErr)
		}

		if providerScopedFailure(code) {
			exhausted[providerKey] = true
		}

		a.logCascadeAttempt(purpose, attempt, code, nextCascadeProvider(attempts, index+1))
	}

	if lastErr != nil {
		return cascadeResult{}, lastErr
	}
	return cascadeResult{}, errors.New("no configured LLM provider with an API key")
}

// defaultRateLimitRetryDelays mirrors the already-proven Gemini backoff
// schedule used by job-search scoring (scoreWithGemini in scraper_go.go):
// same-provider retries with real backoff before falling through to a
// cooldown + the next cascade provider. Without this, a single 429 fails the
// whole call outright for the common single-provider setup (no fallback
// configured), which is the confirmed root cause of Resume Studio's
// "gap analysis and optimize did not work" reports.
var defaultRateLimitRetryDelays = []time.Duration{10 * time.Second, 25 * time.Second, 60 * time.Second}

const (
	// maxRateLimitRetryDelay caps what we will honour from a provider's
	// Retry-After. A provider that asks for ten minutes is really telling us to
	// go away; that is the cascade's and the caller's decision to make, not a
	// sleep to sit through inside one request.
	maxRateLimitRetryDelay = 60 * time.Second

	// providerDailyQuotaCooldown parks a model whose daily allowance is spent.
	// It is deliberately long: nothing this side of midnight brings it back, and
	// every further call is a request the user does not have.
	providerDailyQuotaCooldown = time.Hour

	// modelRetiredCooldown parks a model the provider says does not exist. Unlike
	// a quota, this does not come back tomorrow either — the name is gone for this
	// key for good — so the only reason it expires at all is that a user who fixes
	// the model in Settings should not have to restart the app to be believed.
	modelRetiredCooldown = 24 * time.Hour

	// rateLimitPenaltyWindow is how long a tightened per-minute budget holds after
	// the provider rate-limited us despite it.
	rateLimitPenaltyWindow = 5 * time.Minute
)

// rateLimitRetryDelays returns the configured retry schedule, or the
// production default when unset (nil). An explicit non-nil empty slice
// means "no retries" — used by tests that need the pre-retry fallback
// behavior preserved against a fixed sequence of mocked responses.
func (a *api) rateLimitRetryDelays() []time.Duration {
	if a.cascadeRateLimitDelays != nil {
		return a.cascadeRateLimitDelays
	}
	return defaultRateLimitRetryDelays
}

// retryRateLimited retries the same provider after a 429, using the
// configured backoff schedule. It gives up and returns the last error once
// a retry stops being a rate-limit error (a different failure) or the
// schedule is exhausted (still rate-limited) — the caller then cools this
// provider down and, if configured, falls through to the next one.
func (a *api) retryRateLimited(ctx context.Context, generate jsonGenerator, config appConfig, apiKey, prompt string, firstErr error) (string, error) {
	raw := ""
	err := firstErr
	for _, fallbackDelay := range a.rateLimitRetryDelays() {
		// The provider knows when its own window reopens; the hardcoded ladder is
		// only a guess for the providers that decline to say. Obeying Retry-After
		// means we neither hammer the limit early nor sit out a full minute when
		// we were asked for twenty seconds.
		delay := fallbackDelay
		if asked, ok := retryAfterFor(err); ok {
			delay = min(asked, maxRateLimitRetryDelay)
		}
		if err := sleepContext(ctx, delay); err != nil {
			return "", err
		}

		raw, err = generate(ctx, config, apiKey, prompt)
		if err == nil {
			return raw, nil
		}
		// A spent daily quota will not lift no matter how long the ladder is.
		if code := classifyProviderError(err); code != "rate_limited" {
			return raw, err
		}
	}
	return raw, err
}

// rateLimited wraps a generator so that issuing a request first spends a slot
// from the shared per-minute budget and one from today's daily budget. Both are
// checked here rather than at the call sites, because this is the one funnel
// every provider call — including repairs and retries — passes through.
func (a *api) rateLimited(generate jsonGenerator, priority llmPriority, purpose llmPurpose) jsonGenerator {
	return func(ctx context.Context, config appConfig, apiKey, prompt string) (string, error) {
		a.llmLimiter.setLimit(llmRequestsPerMinute(config), llmTokensPerMinute(config))
		tokens := estimateTokens(prompt, maxOutputTokensFor(config))
		if err := a.llmLimiter.acquire(ctx, priority, tokens); err != nil {
			return "", err
		}
		// Count only after the limiter grants the slot. A cancelled wait never
		// reaches the provider and must not consume the daily request budget.
		if err := a.spendDailyRequest(config, time.Now()); err != nil {
			return "", err
		}
		a.configStore.recordLLMUsage(time.Now(), purpose, config.Form.Provider, config.Form.Model, false)
		return generate(ctx, config, apiKey, prompt)
	}
}

func (a *api) retrySameProvider(ctx context.Context, generate jsonGenerator, config appConfig, apiKey, prompt string) (string, error) {
	delay := a.cascadeRetryDelay
	if delay == 0 {
		delay = 2 * time.Second
	}
	if delay > 0 {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
		}
	}
	return generate(ctx, config, apiKey, prompt)
}

func validAIJSON(raw string) bool {
	return json.Valid([]byte(stripJSONFence(strings.TrimSpace(raw))))
}

func cascadeAttempts(config appConfig) []cascadeAttempt {
	return cascadeAttemptsForPurpose(config, "")
}

func cascadeAttemptsForPurpose(config appConfig, purpose llmPurpose) []cascadeAttempt {
	form := config.Form
	configured := []cascadeAttempt{
		{Provider: form.Provider, Model: form.Model},
		{Provider: form.Fallback1Provider, Model: form.Fallback1Model},
		{Provider: form.Fallback2Provider, Model: form.Fallback2Model},
	}

	attempts := make([]cascadeAttempt, 0, len(configured))
	seen := map[cascadeAttempt]bool{}
	add := func(attempt cascadeAttempt) {
		if attempt.Provider == "" || attempt.Model == "" || seen[attempt] {
			return
		}
		seen[attempt] = true
		attempts = append(attempts, attempt)
	}

	for _, attempt := range configured {
		attempt.Provider = strings.TrimSpace(attempt.Provider)
		if attempt.Provider == "" {
			continue
		}
		attempt.Model = strings.TrimSpace(attempt.Model)
		if attempt.Model == "" {
			attempt.Model = defaultModelForProvider(attempt.Provider)
		}
		if isGeminiProvider(attempt.Provider) {
			for _, model := range geminiModelsForPurpose(purpose, config.Form.AIMode, attempt.Model) {
				if len(config.availableGeminiModels) > 0 && !containsModel(config.availableGeminiModels, model) {
					continue
				}
				add(cascadeAttempt{Provider: attempt.Provider, Model: model})
			}
			continue
		}
		add(attempt)

		// A sibling model on the same provider reuses the key the user already
		// gave us and, on Gemini's free tier, carries its own quota — so it is
		// tried before falling through to a provider whose key they may not have.
		// This is what rescues a config pinned to a model Google has since retired
		// (see llm_models.go), and what lets a free key keep working once one
		// model's daily allowance is gone.
		for _, model := range modelFallbacksFor(attempt.Provider) {
			add(cascadeAttempt{Provider: attempt.Provider, Model: model})
		}
	}
	return attempts
}

// A cooldown is parked at the granularity of whatever actually failed, and the
// two are not the same thing:
//
//   - A rejected key kills every model behind it. Park the provider.
//   - A retired model name, or a spent per-model quota, kills only itself — its
//     siblings on the same key are very often still answering (llm_models.go).
//     Park the model, or we throw away a working provider over one dead model.
//
// Both live in one map; only the key differs.
func cascadeModelKey(attempt cascadeAttempt) string {
	return cascadeProviderKey(attempt.Provider) + "/" + strings.ToLower(strings.TrimSpace(attempt.Model))
}

func (a *api) attemptOnCooldown(attempt cascadeAttempt, now time.Time) bool {
	return a.onCooldown(cascadeProviderKey(attempt.Provider), now) || a.onCooldown(cascadeModelKey(attempt), now)
}

func (a *api) providerOnCooldown(provider string, now time.Time) bool {
	return a.onCooldown(cascadeProviderKey(provider), now)
}

func (a *api) onCooldown(key string, now time.Time) bool {
	a.cascadeMu.Lock()
	defer a.cascadeMu.Unlock()
	if a.cascadeCooldown == nil {
		return false
	}
	until, ok := a.cascadeCooldown[key]
	if !ok {
		return false
	}
	if now.Before(until) {
		return true
	}
	delete(a.cascadeCooldown, key)
	return false
}

func (a *api) setProviderCooldown(provider string, duration time.Duration) {
	a.setCooldown(cascadeProviderKey(provider), duration)
}

func (a *api) setModelCooldown(attempt cascadeAttempt, duration time.Duration) {
	a.setCooldown(cascadeModelKey(attempt), duration)
}

func (a *api) setCooldown(key string, duration time.Duration) {
	a.cascadeMu.Lock()
	defer a.cascadeMu.Unlock()
	if a.cascadeCooldown == nil {
		a.cascadeCooldown = map[string]time.Time{}
	}
	a.cascadeCooldown[key] = time.Now().Add(duration)
}

// logCascadeAttempt records a failed/fallen-back cascade step. It writes to
// both the server logger and the in-memory logBuffer so the Logs view surfaces
// LLM activity during Resume Studio (parse/analyze/gap/optimize/cover) and job
// scoring. The message carries only provider/model/purpose/error code — never
// the API key or prompt — so it is safe to show in the UI.
func (a *api) logCascadeAttempt(purpose llmPurpose, attempt cascadeAttempt, code string, next string) {
	if next == "" {
		next = "none"
	}
	message := fmt.Sprintf("[ LLM ] %s %s/%s -> %s, fallback %s", purpose, attempt.Provider, attempt.Model, code, next)
	if a.logger != nil {
		a.logger.Print(message)
	}
	a.logs.add("warning", message)
}

// logCascadeSuccess records the provider/model that ultimately answered, so the
// Logs view shows a line even when the first provider succeeds outright (no
// fallback). Same redaction guarantees as logCascadeAttempt.
func (a *api) logCascadeSuccess(purpose llmPurpose, attempt cascadeAttempt) {
	message := fmt.Sprintf("[ LLM ] %s %s/%s -> ok", purpose, attempt.Provider, attempt.Model)
	if a.logger != nil {
		a.logger.Print(message)
	}
	a.logs.add("success", message)
}

// logCascadeCacheHit makes a reused answer visible in the Logs view, so a
// suspiciously instant result reads as "we already had this" rather than as a
// silently skipped AI call.
func (a *api) logCascadeCacheHit(purpose llmPurpose, result cascadeResult) {
	message := fmt.Sprintf("[ LLM ] %s %s/%s -> cached", purpose, result.ProviderUsed, result.ModelUsed)
	if a.logger != nil {
		a.logger.Print(message)
	}
	a.logs.add("success", message)
}

func nextCascadeProvider(attempts []cascadeAttempt, start int) string {
	for _, attempt := range attempts[start:] {
		if provider := cascadeProviderKey(attempt.Provider); provider != "" {
			return provider
		}
	}
	return ""
}

func cascadeProviderKey(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}

// providerScopedFailure reports a failure that belongs to the provider rather
// than to the model we happened to ask for: a key it rejected, a host that will
// not answer, a call that never came back. Every sibling model sits behind that
// same key and that same host, so once one of these lands, the rest of the
// provider's models have nothing new to offer and are skipped.
//
// A model's own failures — retired, out of quota, rate-limited, answering with
// unparseable JSON — are deliberately NOT here: those are exactly the cases a
// sibling model can still answer.
func providerScopedFailure(code string) bool {
	switch code {
	case "unauthorized", "forbidden", "provider_unavailable", "network_error", "timeout", "local_model_unreachable":
		return true
	}
	return false
}

func isLocalProvider(provider string) bool {
	provider = cascadeProviderKey(provider)
	return strings.Contains(provider, "ollama") || strings.Contains(provider, "local")
}
