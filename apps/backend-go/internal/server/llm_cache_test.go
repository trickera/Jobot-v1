package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// A free-tier user re-running tailoring on an unchanged resume and job should
// not spend a second request from their quota: the answer is deterministic, so
// it is served from the cache.
func TestCascadeServesRepeatedPromptFromCache(t *testing.T) {
	a := newCascadeTestAPI(t)
	a.llmCache = newLLMCache(nil)
	config := cascadeTestConfig("Gemini", geminiFreeModel, "", "", "", "")

	calls := 0
	generate := func(ctx context.Context, cfg appConfig, key, prompt string) (string, error) {
		calls++
		return `{"ok":true}`, nil
	}

	first, err := a.runLLMWithFallback(context.Background(), "resume_optimize", config, "same prompt", generate)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	second, err := a.runLLMWithFallback(context.Background(), "resume_optimize", config, "same prompt", generate)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	if calls != 1 {
		t.Fatalf("expected the provider to be asked once, got %d calls", calls)
	}
	if second.Raw != first.Raw || second.ProviderUsed != first.ProviderUsed {
		t.Fatalf("cached result %+v does not match the original %+v", second, first)
	}
}

// Changing the model in Settings must genuinely re-ask, not replay the answer
// the previous model gave.
func TestCascadeCacheKeyedByProviderAndModel(t *testing.T) {
	a := newCascadeTestAPI(t)
	a.llmCache = newLLMCache(nil)

	calls := 0
	generate := func(ctx context.Context, cfg appConfig, key, prompt string) (string, error) {
		calls++
		return `{"ok":true}`, nil
	}

	flash := cascadeTestConfig("OpenRouter", "google/gemini-flash-lite", "", "", "", "")
	pro := cascadeTestConfig("OpenRouter", "google/gemini-pro", "", "", "", "")

	if _, err := a.runLLMWithFallback(context.Background(), "resume_gap", flash, "p", generate); err != nil {
		t.Fatalf("flash call: %v", err)
	}
	if _, err := a.runLLMWithFallback(context.Background(), "resume_gap", pro, "p", generate); err != nil {
		t.Fatalf("pro call: %v", err)
	}

	if calls != 2 {
		t.Fatalf("expected each model to be asked once, got %d calls", calls)
	}
}

// A failure must not be cached: the next attempt has to reach the provider,
// otherwise a transient rate limit would poison the prompt for hours.
func TestCascadeDoesNotCacheFailures(t *testing.T) {
	a := newCascadeTestAPI(t)
	a.llmCache = newLLMCache(nil)
	config := cascadeTestConfig("Gemini", geminiFreeModel, "", "", "", "")

	calls := 0
	generate := func(ctx context.Context, cfg appConfig, key, prompt string) (string, error) {
		calls++
		if calls == 1 {
			return "", errors.New("gemini: HTTP 401: bad key")
		}
		return `{"ok":true}`, nil
	}

	if _, err := a.runLLMWithFallback(context.Background(), "resume_parse", config, "p", generate); err == nil {
		t.Fatal("expected the first call to fail")
	}

	// The 401 cooled the provider down; clear it so the retry is about caching.
	a.cascadeMu.Lock()
	a.cascadeCooldown = nil
	a.cascadeMu.Unlock()

	result, err := a.runLLMWithFallback(context.Background(), "resume_parse", config, "p", generate)
	if err != nil {
		t.Fatalf("expected the retry to reach the provider, got %v", err)
	}
	if strings.TrimSpace(result.Raw) != `{"ok":true}` {
		t.Fatalf("unexpected result %q", result.Raw)
	}
	if calls != 2 {
		t.Fatalf("expected the provider to be asked again after a failure, got %d calls", calls)
	}
}

func TestLLMCacheExpiresEntries(t *testing.T) {
	cache := newLLMCache(nil)
	now := time.Now()
	key := llmCacheKey("resume_optimize", "Gemini", geminiFreeModel, "prompt")
	cache.put(key, cascadeResult{Raw: `{"ok":true}`}, now)

	if _, ok := cache.get(key, now.Add(llmCacheTTL-time.Minute)); !ok {
		t.Fatal("expected a hit inside the TTL")
	}
	if _, ok := cache.get(key, now.Add(llmCacheTTL+time.Minute)); ok {
		t.Fatal("expected a miss past the TTL")
	}
}

// The point of persisting: an answer bought with free-tier quota must not
// evaporate when the user closes the app. A fresh cache over the same store —
// which is what a restart looks like — has to find it.
func TestLLMCacheSurvivesARestart(t *testing.T) {
	store := newTestStore(t)
	now := time.Now()
	key := llmCacheKey("resume_optimize", "Gemini", geminiFreeModel, "prompt")

	newLLMCache(store).put(key, cascadeResult{Raw: `{"ok":true}`, ProviderUsed: "gemini", ModelUsed: geminiFreeModel}, now)

	restarted := newLLMCache(store)
	got, ok := restarted.get(key, now.Add(time.Hour))
	if !ok {
		t.Fatal("expected the persisted answer to survive the restart")
	}
	if got.Raw != `{"ok":true}` || got.ProviderUsed != "gemini" {
		t.Fatalf("the restored entry lost data: %+v", got)
	}
}

func TestLLMCacheDoesNotServeAnExpiredPersistedEntry(t *testing.T) {
	store := newTestStore(t)
	now := time.Now()
	key := llmCacheKey("resume_gap", "Gemini", geminiFreeModel, "prompt")

	newLLMCache(store).put(key, cascadeResult{Raw: `{"ok":true}`}, now)

	if _, ok := newLLMCache(store).get(key, now.Add(llmCacheTTL+time.Minute)); ok {
		t.Fatal("expected a stale persisted entry to be a miss")
	}
}

func TestLLMCacheBoundsItsSize(t *testing.T) {
	cache := newLLMCache(nil)
	now := time.Now()
	for i := 0; i < llmCacheMaxEntries*2; i++ {
		key := llmCacheKey("resume_optimize", "Gemini", geminiFreeModel, strings.Repeat("x", i+1))
		cache.put(key, cascadeResult{Raw: `{"ok":true}`}, now.Add(time.Duration(i)*time.Second))
	}
	if len(cache.entries) > llmCacheMaxEntries {
		t.Fatalf("expected at most %d entries, got %d", llmCacheMaxEntries, len(cache.entries))
	}
}
