package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"testing"
	"time"
)

func newCascadeTestAPI(t *testing.T) *api {
	t.Helper()
	store := newTestStore(t)
	for provider, key := range map[string]string{
		"Gemini":     "gemini-key",
		"OpenRouter": "openrouter-key",
		"Groq":       "groq-key",
	} {
		if err := store.setAIAPIKeyForProvider(provider, key); err != nil {
			t.Fatalf("seed key for %s: %v", provider, err)
		}
	}
	return &api{
		logger:                 log.New(io.Discard, "", 0),
		logs:                   &logBuffer{},
		configStore:            store,
		cascadeRetryDelay:      -1,
		cascadeRateLimitDelays: []time.Duration{-1, -1, -1},
		fetchGeminiCatalog: func(context.Context, string) ([]string, error) {
			return append([]string(nil), geminiFreeTierAllowlist...), nil
		},
	}
}

func geminiTestConfig(form configForm) appConfig {
	form.Provider = coalesce(form.Provider, "Gemini")
	form.Model = coalesce(form.Model, geminiPinnedFreeModel)
	form.AIDataConsent = true
	return appConfig{
		Form: form,
		ModelValidation: &modelValidationStatus{
			Status: "validated", Requested: form.Model, Active: form.Model,
			Message: "Test catalog fixture.", ValidatedAt: "2026-07-13T00:00:00Z",
		},
	}
}

func cascadeTestConfig(primaryProvider, primaryModel, fallback1Provider, fallback1Model, fallback2Provider, fallback2Model string) appConfig {
	config := defaultConfig()
	config.Form.Provider = primaryProvider
	config.Form.Model = primaryModel
	config.Form.Fallback1Provider = fallback1Provider
	config.Form.Fallback1Model = fallback1Model
	config.Form.Fallback2Provider = fallback2Provider
	config.Form.Fallback2Model = fallback2Model
	config.Form.AIDataConsent = true
	return config
}

// geminiAttemptCount is how many times the cascade asks Gemini before it is
// entitled to move on: the configured model, then each sibling model on the same
// key. Tests that stage a fixed sequence of provider responses must refuse all of
// them to reach the next provider — and hardcoding the number would rot the day a
// model is added to or dropped from geminiModelFallbacks.
func geminiAttemptCount(config appConfig) int {
	count := 0
	for _, attempt := range cascadeAttempts(config) {
		if isGeminiProvider(attempt.Provider) {
			count++
		}
	}
	return count
}

func repeatResponse(times int, response sequenceResponse) []sequenceResponse {
	responses := make([]sequenceResponse, 0, times)
	for i := 0; i < times; i++ {
		responses = append(responses, response)
	}
	return responses
}

func TestCascadeFallsBackOn429(t *testing.T) {
	a := newCascadeTestAPI(t)
	config := cascadeTestConfig("Gemini", geminiFreeModel, "OpenRouter", "some/model", "", "")
	calls := []string{}
	generate := func(ctx context.Context, cfg appConfig, key, prompt string) (string, error) {
		calls = append(calls, strings.ToLower(cfg.Form.Provider))
		if strings.EqualFold(cfg.Form.Provider, "gemini") {
			return "", errors.New("gemini: HTTP 429: quota")
		}
		return `{"ok":true}`, nil
	}

	result, err := a.runLLMWithFallback(context.Background(), "job_score", config, "p", generate)
	if err != nil {
		t.Fatalf("expected fallback success, got %v", err)
	}
	if result.ProviderUsed != "openrouter" {
		t.Fatalf("expected openrouter, got %q", result.ProviderUsed)
	}
	// The configured model gets 1 attempt + 3 same-provider retries — the fix for a
	// single-provider setup failing hard on a transient rate limit. Then Gemini's
	// sibling models get ONE try each: on the free tier the per-minute ceiling is
	// per model, so a sibling is a real chance, but a second backoff ladder would
	// only sleep another 95 seconds to be told the same thing.
	geminiCalls := 0
	for _, c := range calls {
		if c == "gemini" {
			geminiCalls++
		}
	}
	want := 4 + (geminiAttemptCount(config) - 1) // one ladder, then one try per sibling
	if geminiCalls != want {
		t.Fatalf("expected one ladder (4 calls) plus one try per sibling model = %d, got %d: %v", want, geminiCalls, calls)
	}
	if calls[len(calls)-1] != "openrouter" {
		t.Fatalf("expected the cascade to end on openrouter, got %v", calls)
	}
}

// A provider that timed out was reachable and just too slow. Replaying the
// same prompt at it only spends the budget again, so the cascade must move
// straight to the next provider instead of taking the retrySameProvider path
// that "network_error" gets.
func TestCascadeDoesNotRetrySameProviderOnTimeout(t *testing.T) {
	a := newCascadeTestAPI(t)
	config := cascadeTestConfig("Gemini", geminiFreeModel, "OpenRouter", "some/model", "", "")
	calls := []string{}
	generate := func(ctx context.Context, cfg appConfig, key, prompt string) (string, error) {
		calls = append(calls, strings.ToLower(cfg.Form.Provider))
		if strings.EqualFold(cfg.Form.Provider, "gemini") {
			return "", fmt.Errorf("gemini call: %w", context.DeadlineExceeded)
		}
		return `{"ok":true}`, nil
	}

	result, err := a.runLLMWithFallback(context.Background(), "resume_optimize", config, "p", generate)
	if err != nil {
		t.Fatalf("expected fallback success, got %v", err)
	}
	if result.ProviderUsed != "openrouter" {
		t.Fatalf("expected openrouter, got %q", result.ProviderUsed)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 1 gemini attempt + 1 openrouter attempt, got %v", calls)
	}
}

// A spent daily allowance does not come back today. Feeding it to the
// rate-limit ladder makes the user sit through 10s + 25s + 60s of backoff for a
// wall that will not move — the exact failure a free-tier key hits first.
//
// It is spent per MODEL, though, not per key: each Gemini model is tried once,
// and none of them is retried.
func TestCascadeDoesNotRetryAnExhaustedDailyQuota(t *testing.T) {
	a := newCascadeTestAPI(t)
	config := cascadeTestConfig("Gemini", geminiFreeModel, "OpenRouter", "some/model", "", "")

	calls := []string{}
	generate := func(ctx context.Context, cfg appConfig, key, prompt string) (string, error) {
		calls = append(calls, strings.ToLower(cfg.Form.Provider)+"/"+cfg.Form.Model)
		if strings.EqualFold(cfg.Form.Provider, "gemini") {
			return "", &providerHTTPError{Provider: "gemini", Status: 429, Body: "quota", DailyQuota: true}
		}
		return `{"ok":true}`, nil
	}

	result, err := a.runLLMWithFallback(context.Background(), "resume_optimize", config, "p", generate)
	if err != nil {
		t.Fatalf("expected the cascade to fall through to the fallback, got %v", err)
	}
	if result.ProviderUsed != "openrouter" {
		t.Fatalf("expected openrouter, got %q", result.ProviderUsed)
	}

	// No model may be asked twice: that is what "no retry ladder" means here, and
	// counting distinct calls is how we know a ladder did not run behind our back.
	seen := map[string]bool{}
	for _, call := range calls {
		if seen[call] {
			t.Fatalf("a spent quota must not be retried, but %s was called twice: %v", call, calls)
		}
		seen[call] = true
	}
	want := geminiAttemptCount(config)
	if len(calls) != want+1 { // +1 for the openrouter attempt that answers
		t.Fatalf("expected each gemini model tried once then openrouter (%d calls), got %v", want+1, calls)
	}

	// The exhausted MODEL is parked. The key behind it is not: its sibling models
	// have their own allowances, and parking those would throw away the capacity
	// that makes a free key last the day.
	spent := cascadeAttempt{Provider: "Gemini", Model: geminiFreeModel}
	if !a.attemptOnCooldown(spent, time.Now().Add(30*time.Minute)) {
		t.Fatal("expected the exhausted model to stay on cooldown well past a rate-limit pause")
	}
	if a.providerOnCooldown("gemini", time.Now().Add(30*time.Minute)) {
		t.Fatal("one model running out of quota must not park the whole key")
	}
}

// The bug that made this whole change necessary. Google retires a model for keys
// that have not used it yet, so a config pinned to gemini-2.5-flash answered on
// the developer's machine and 404'd for every user who installed the app. The
// cascade had no case for a 404 at all: it fell straight through to the next
// PROVIDER, which a free user does not have, and the app was simply dead.
func TestCascadeMigratesARetiredModelBeforeSpendingAGeneration(t *testing.T) {
	a := newCascadeTestAPI(t)
	// No fallback provider at all — the free-tier setup this app is built for.
	config := cascadeTestConfig("Gemini", "gemini-2.5-flash", "", "", "", "")

	calls := []string{}
	generate := func(ctx context.Context, cfg appConfig, key, prompt string) (string, error) {
		calls = append(calls, cfg.Form.Model)
		if cfg.Form.Model == "gemini-2.5-flash" {
			return "", &providerHTTPError{
				Provider: "gemini",
				Status:   404,
				Body:     `{"error":{"code":404,"message":"This model models/gemini-2.5-flash is no longer available to new users."}}`,
			}
		}
		return `{"ok":true}`, nil
	}

	result, err := a.runLLMWithFallback(context.Background(), "resume_optimize", config, "p", generate)
	if err != nil {
		t.Fatalf("a retired model must not kill the call when the key still works: %v", err)
	}
	if result.ProviderUsed != "gemini" {
		t.Fatalf("expected the same key to answer on another model, got %q", result.ProviderUsed)
	}
	if result.ModelUsed != geminiPinnedFreeModel {
		t.Fatalf("expected the validated pin, got %q", result.ModelUsed)
	}
	if len(calls) != 1 || calls[0] == "gemini-2.5-flash" {
		t.Fatalf("the retired model must not consume a generation request, got %v", calls)
	}
}

// The mirror image: a key the provider rejects is rejected for every model behind
// it, so the sibling models must NOT be tried. Otherwise one bad key costs four
// round trips instead of one.
func TestCascadeDoesNotTrySiblingModelsBehindARejectedKey(t *testing.T) {
	a := newCascadeTestAPI(t)
	config := cascadeTestConfig("Gemini", geminiFreeModel, "OpenRouter", "some/model", "", "")

	calls := []string{}
	generate := func(ctx context.Context, cfg appConfig, key, prompt string) (string, error) {
		calls = append(calls, strings.ToLower(cfg.Form.Provider))
		if strings.EqualFold(cfg.Form.Provider, "gemini") {
			return "", &providerHTTPError{Provider: "gemini", Status: 401, Body: "invalid key"}
		}
		return `{"ok":true}`, nil
	}

	if _, err := a.runLLMWithFallback(context.Background(), "resume_optimize", config, "p", generate); err != nil {
		t.Fatalf("expected the fallback provider to answer, got %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected one gemini attempt then openrouter, got %v", calls)
	}
}

// When the provider names its own wait, obey it instead of the hardcoded ladder.
func TestCascadeHonorsRetryAfter(t *testing.T) {
	a := newCascadeTestAPI(t)
	// A non-nil schedule with a real delay would otherwise dominate the timing.
	a.cascadeRateLimitDelays = []time.Duration{time.Hour}
	config := cascadeTestConfig("Gemini", geminiFreeModel, "", "", "", "")

	calls := 0
	generate := func(ctx context.Context, cfg appConfig, key, prompt string) (string, error) {
		calls++
		if calls == 1 {
			return "", &providerHTTPError{
				Provider:   "gemini",
				Status:     429,
				Body:       "slow down",
				RetryAfter: 40 * time.Millisecond,
			}
		}
		return `{"ok":true}`, nil
	}

	start := time.Now()
	if _, err := a.runLLMWithFallback(context.Background(), "resume_gap", config, "p", generate); err != nil {
		t.Fatalf("expected the retry to succeed, got %v", err)
	}
	elapsed := time.Since(start)

	if calls != 2 {
		t.Fatalf("expected one retry after the requested pause, got %d calls", calls)
	}
	if elapsed < 30*time.Millisecond {
		t.Fatalf("expected to wait the ~40ms the provider asked for, waited %v", elapsed)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("expected the provider's own wait to win over the 1h fallback ladder, waited %v", elapsed)
	}
}

func TestCascadeRetriesSameProviderOnRateLimitBeforeFallback(t *testing.T) {
	// The actual reported bug: Resume Studio's real handlers hit a 429 on the
	// single provider a user has configured (the common case — no
	// Fallback1/2 set) and failed hard instead of retrying, because the
	// cascade only set a cooldown and moved on. A transient rate limit should
	// recover via same-provider retry with backoff instead.
	a := newCascadeTestAPI(t)
	config := cascadeTestConfig("Gemini", geminiFreeModel, "", "", "", "")
	calls := 0
	generate := func(ctx context.Context, cfg appConfig, key, prompt string) (string, error) {
		calls++
		if calls < 3 {
			return "", errors.New("gemini: HTTP 429: quota")
		}
		return `{"ok":true}`, nil
	}

	result, err := a.runLLMWithFallback(context.Background(), "resume_gap", config, "p", generate)
	if err != nil {
		t.Fatalf("expected eventual success after rate-limit retries, got %v", err)
	}
	if result.ProviderUsed != "gemini" {
		t.Fatalf("expected gemini (single provider, no fallback configured), got %q", result.ProviderUsed)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls (1 initial + 2 retries) before success, got %d", calls)
	}
}

func TestSequentialResumeStudioCallsSurviveTransientRateLimit(t *testing.T) {
	// Mirrors the real Resume Studio flow shape (parse -> analyze job -> gap
	// -> optimize fired back-to-back by a user working through the feature) -
	// each call independently hits a transient 429 on its first attempt and
	// must still succeed via retry, not fail the whole step.
	a := newCascadeTestAPI(t)
	config := cascadeTestConfig("Gemini", geminiFreeModel, "", "", "", "")
	purposes := []llmPurpose{"resume_parse", "resume_analyze_job", "resume_gap", "resume_optimize"}
	for _, purpose := range purposes {
		calls := 0
		generate := func(ctx context.Context, cfg appConfig, key, prompt string) (string, error) {
			calls++
			if calls == 1 {
				return "", errors.New("gemini: HTTP 429: quota")
			}
			return `{"ok":true}`, nil
		}
		if _, err := a.runLLMWithFallback(context.Background(), purpose, config, "p", generate); err != nil {
			t.Fatalf("%s: expected retry to recover from a transient 429, got %v", purpose, err)
		}
	}
}

func TestCascadeSkipsProviderOn401AndCoolsDown(t *testing.T) {
	a := newCascadeTestAPI(t)
	config := cascadeTestConfig("Gemini", geminiFreeModel, "OpenRouter", "some/model", "", "")
	generate := func(ctx context.Context, cfg appConfig, key, prompt string) (string, error) {
		if strings.EqualFold(cfg.Form.Provider, "gemini") {
			return "", errors.New("gemini: HTTP 401: bad key")
		}
		return `{"ok":true}`, nil
	}
	if _, err := a.runLLMWithFallback(context.Background(), "resume_parse", config, "p", generate); err != nil {
		t.Fatalf("expected fallback success, got %v", err)
	}

	calls := []string{}
	generateAfterCooldown := func(ctx context.Context, cfg appConfig, key, prompt string) (string, error) {
		calls = append(calls, strings.ToLower(cfg.Form.Provider))
		if strings.EqualFold(cfg.Form.Provider, "gemini") {
			t.Fatal("expected gemini to be skipped while on cooldown")
		}
		return `{"ok":true}`, nil
	}
	result, err := a.runLLMWithFallback(context.Background(), "resume_parse", config, "p", generateAfterCooldown)
	if err != nil {
		t.Fatalf("expected cooldown fallback success, got %v", err)
	}
	if result.ProviderUsed != "openrouter" {
		t.Fatalf("expected openrouter, got %q", result.ProviderUsed)
	}
	if len(calls) != 1 || calls[0] != "openrouter" {
		t.Fatalf("expected only openrouter after cooldown, got %v", calls)
	}
}

func TestCascadeRetriesInvalidJSONOnceWithRepairPrompt(t *testing.T) {
	a := newCascadeTestAPI(t)
	config := cascadeTestConfig("Gemini", geminiFreeModel, "", "", "", "")
	calls := 0
	prompts := []string{}
	generate := func(ctx context.Context, cfg appConfig, key, prompt string) (string, error) {
		calls++
		prompts = append(prompts, prompt)
		if calls == 1 {
			return "not json", nil
		}
		return `{"ok":true}`, nil
	}

	result, err := a.runLLMWithFallback(context.Background(), "resume_gap", config, "base prompt", generate)
	if err != nil {
		t.Fatalf("expected repair success, got %v", err)
	}
	if result.ProviderUsed != "gemini" {
		t.Fatalf("expected gemini, got %q", result.ProviderUsed)
	}
	if calls != 2 {
		t.Fatalf("expected one repair retry, got %d calls", calls)
	}
	if !strings.Contains(prompts[1], "RETORNE APENAS JSON") {
		t.Fatalf("expected repair prompt, got %q", prompts[1])
	}
}

func TestCascadeSurfacesLogsInBuffer(t *testing.T) {
	a := newCascadeTestAPI(t)
	a.logs = &logBuffer{}
	config := cascadeTestConfig("Gemini", geminiFreeModel, "OpenRouter", "some/model", "", "")
	generate := func(ctx context.Context, cfg appConfig, key, prompt string) (string, error) {
		if strings.EqualFold(cfg.Form.Provider, "gemini") {
			return "", errors.New("gemini: HTTP 429: quota")
		}
		return `{"ok":true}`, nil
	}

	if _, err := a.runLLMWithFallback(context.Background(), "resume_optimize", config, "secret-prompt", generate); err != nil {
		t.Fatalf("expected fallback success, got %v", err)
	}

	entries := a.logs.list()
	var sawFallback, sawSuccess bool
	for _, entry := range entries {
		if strings.Contains(entry.Message, "gemini-key") || strings.Contains(entry.Message, "secret-prompt") {
			t.Fatalf("log leaked a secret: %q", entry.Message)
		}
		if strings.Contains(entry.Message, "resume_optimize") && strings.Contains(entry.Message, "rate_limited") {
			sawFallback = true
		}
		if strings.Contains(entry.Message, "resume_optimize") && strings.Contains(entry.Message, "-> ok") {
			sawSuccess = true
		}
	}
	if !sawFallback {
		t.Fatalf("expected a fallback log line, got %+v", entries)
	}
	if !sawSuccess {
		t.Fatalf("expected a success log line, got %+v", entries)
	}
}

func TestCascadeAllFailReturnsLastError(t *testing.T) {
	a := newCascadeTestAPI(t)
	config := cascadeTestConfig("Gemini", geminiFreeModel, "OpenRouter", "some/model", "Groq", "llama-3.3-70b-versatile")
	calls := 0
	generate := func(ctx context.Context, cfg appConfig, key, prompt string) (string, error) {
		calls++
		return "", errors.New(strings.ToLower(cfg.Form.Provider) + ": HTTP 503: unavailable")
	}

	_, err := a.runLLMWithFallback(context.Background(), "cover_letter", config, "p", generate)
	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
	if got := classifyProviderError(err); got != "provider_unavailable" {
		t.Fatalf("expected provider_unavailable, got %q err=%v", got, err)
	}
	if calls != 6 {
		t.Fatalf("expected 503 retry once per provider, got %d calls", calls)
	}
}
