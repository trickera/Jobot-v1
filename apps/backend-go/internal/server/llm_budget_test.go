package server

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// The request that would have been refused is never sent. That is the whole
// point: run out on our own terms instead of discovering the wall by hitting it
// in the middle of something the user is waiting on.
func TestCascadeStopsAtTheDailyBudgetWithoutCallingTheProvider(t *testing.T) {
	a := newCascadeTestAPI(t)
	config := cascadeTestConfig("Gemini", geminiFreeModel, "", "", "", "")
	config.Form.LLMRequestsPerDay = 2

	calls := 0
	generate := func(ctx context.Context, cfg appConfig, key, prompt string) (string, error) {
		calls++
		return `{"ok":true}`, nil
	}

	for i := 0; i < 2; i++ {
		if _, err := a.runLLMWithFallback(context.Background(), "job_score", config, uniquePrompt(i), generate); err != nil {
			t.Fatalf("call %d inside the budget: %v", i, err)
		}
	}
	if calls != 2 {
		t.Fatalf("expected both in-budget calls to reach the provider, got %d", calls)
	}

	_, err := a.runLLMWithFallback(context.Background(), "job_score", config, uniquePrompt(99), generate)
	if err == nil {
		t.Fatal("expected the call past the budget to fail")
	}
	if calls != 2 {
		t.Fatalf("expected no provider call past the budget, got %d", calls)
	}
	if got := classifyProviderError(err); got != "quota_exhausted" {
		t.Fatalf("expected quota_exhausted so callers degrade honestly, got %q", got)
	}
	// Our budget, not the provider's — the message has to point at the setting.
	if got := classifyResumeError(err).Code; got != "ai_budget_spent" {
		t.Fatalf("expected the UI to see ai_budget_spent, got %q", got)
	}
}

// Our own budget running out and the provider refusing us both stop the AI, but
// only one of them is the user's to fix. Telling someone to "come back tomorrow"
// when the number they could raise in one edit is what stopped them costs them a
// day for nothing.
func TestOurBudgetAndTheProvidersQuotaGetDifferentMessages(t *testing.T) {
	ours := fmt.Errorf("%w: 200/200 requests today", errQuotaBudgetSpent)
	theirs := &providerHTTPError{Provider: "gemini", Status: 429, Body: "quota", DailyQuota: true}

	if got := classifyResumeError(ours).Code; got != "ai_budget_spent" {
		t.Fatalf("expected ai_budget_spent for our own budget, got %q", got)
	}
	if got := classifyResumeError(theirs).Code; got != "ai_quota_exhausted" {
		t.Fatalf("expected ai_quota_exhausted for the provider's quota, got %q", got)
	}
	if !strings.Contains(classifyResumeError(ours).Message, "Settings") {
		t.Fatal("the budget message must point at the setting the user can change")
	}

	// They still behave the same to the cascade: stop asking this provider.
	if got := classifyProviderError(ours); got != "quota_exhausted" {
		t.Fatalf("expected the cascade to stop spending, got %q", got)
	}
}

// The counter is in the store, so closing the app does not hand the user a fresh
// daily allowance the provider will not honour.
func TestDailyBudgetCountSurvivesARestart(t *testing.T) {
	store := newTestStore(t)
	now := time.Now()

	if got := store.llmRequestsToday(now); got != 0 {
		t.Fatalf("expected a fresh day to start at 0, got %d", got)
	}
	store.recordLLMRequest(now)
	store.recordLLMRequest(now)

	if got := store.llmRequestsToday(now); got != 2 {
		t.Fatalf("expected 2 requests counted, got %d", got)
	}
	// Tomorrow is a new allowance.
	if got := store.llmRequestsToday(now.Add(24 * time.Hour)); got != 0 {
		t.Fatalf("expected the count to reset on a new day, got %d", got)
	}
}

func TestLLMRequestsPerDay(t *testing.T) {
	if got := llmRequestsPerDay(appConfig{}); got != llmDefaultRequestsPerDay {
		t.Fatalf("expected the free-tier default %d, got %d", llmDefaultRequestsPerDay, got)
	}
	configured := appConfig{Form: configForm{LLMRequestsPerDay: 5000}}
	if got := llmRequestsPerDay(configured); got != 5000 {
		t.Fatalf("expected the configured budget to win, got %d", got)
	}
	// A local model has no daily allowance to protect.
	local := appConfig{Form: configForm{Provider: "Ollama"}}
	if got := llmRequestsPerDay(local); got != 0 {
		t.Fatalf("expected no budget for a local model, got %d", got)
	}
}

// A cached answer must not be booked against the budget: it never reaches the
// provider, so charging for it would shrink the day for no reason.
func TestCachedAnswerCostsNothingFromTheDailyBudget(t *testing.T) {
	a := newCascadeTestAPI(t)
	a.llmCache = newLLMCache(nil)
	config := cascadeTestConfig("Gemini", geminiFreeModel, "", "", "", "")
	config.Form.LLMRequestsPerDay = 1

	generate := func(ctx context.Context, cfg appConfig, key, prompt string) (string, error) {
		return `{"ok":true}`, nil
	}

	if _, err := a.runLLMWithFallback(context.Background(), "resume_gap", config, "p", generate); err != nil {
		t.Fatalf("first call: %v", err)
	}
	// The budget is now spent, but this prompt is already answered.
	if _, err := a.runLLMWithFallback(context.Background(), "resume_gap", config, "p", generate); err != nil {
		t.Fatalf("expected the cached answer to be served despite the spent budget, got %v", err)
	}
}

func uniquePrompt(i int) string {
	return "prompt-" + strings.Repeat("x", i+1)
}
