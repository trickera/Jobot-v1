package server

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// Resume Studio fires four or five calls back to back. A limiter that paced
// every call would tax each of them with a wait and undo the latency work; a
// window only bites once the quota is actually being filled.
func TestLimiterLetsABurstUnderTheLimitThrough(t *testing.T) {
	limiter := newLLMLimiter(10)

	start := time.Now()
	for i := 0; i < 5; i++ {
		if err := limiter.acquire(context.Background(), llmInteractive, 0); err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
	}

	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("a burst inside the budget must not wait, took %v", elapsed)
	}
}

// Once the window is full the next caller has to wait for the oldest call to
// age out of it.
func TestLimiterBlocksOnceTheWindowIsFull(t *testing.T) {
	limiter := newLLMLimiter(2)
	limiter.window = 120 * time.Millisecond

	for i := 0; i < 2; i++ {
		if err := limiter.acquire(context.Background(), llmBackground, 0); err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
	}

	start := time.Now()
	if err := limiter.acquire(context.Background(), llmBackground, 0); err != nil {
		t.Fatalf("third acquire: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 80*time.Millisecond {
		t.Fatalf("expected the third call to wait for the window, waited only %v", elapsed)
	}
}

// A background search must not make the user queue behind thirty job scores for
// the button they just pressed.
func TestLimiterServesInteractiveBeforeBackground(t *testing.T) {
	limiter := newLLMLimiter(1)
	limiter.window = 150 * time.Millisecond

	// Fill the window, so both callers below have to wait for a slot.
	if err := limiter.acquire(context.Background(), llmBackground, 0); err != nil {
		t.Fatalf("seed acquire: %v", err)
	}

	order := make(chan string, 2)
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		if err := limiter.acquire(context.Background(), llmBackground, 0); err != nil {
			t.Errorf("background acquire: %v", err)
			return
		}
		order <- "background"
	}()

	// Let the background caller reach the limiter first, so that winning the slot
	// would be the natural outcome without the priority rule.
	time.Sleep(20 * time.Millisecond)

	go func() {
		defer wg.Done()
		if err := limiter.acquire(context.Background(), llmInteractive, 0); err != nil {
			t.Errorf("interactive acquire: %v", err)
			return
		}
		order <- "interactive"
	}()

	wg.Wait()
	close(order)

	if first := <-order; first != "interactive" {
		t.Fatalf("expected the user-facing call to be served first, got %q", first)
	}
}

func TestLimiterRespectsContextCancellation(t *testing.T) {
	limiter := newLLMLimiter(1)
	limiter.window = time.Hour

	if err := limiter.acquire(context.Background(), llmInteractive, 0); err != nil {
		t.Fatalf("seed acquire: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	if err := limiter.acquire(ctx, llmInteractive, 0); err == nil {
		t.Fatal("expected a cancelled context to abort the wait")
	}
}

// The provider is the authority on its own limit: when it rate-limits us
// despite our budget, the penalty has to actually bind, and a later re-read of
// the user's setting must not quietly undo it.
func TestLimiterPenaltyOverridesTheConfiguredLimit(t *testing.T) {
	limiter := newLLMLimiter(10)
	limiter.window = 150 * time.Millisecond
	limiter.penalize(1, time.Now().Add(time.Hour))

	if err := limiter.acquire(context.Background(), llmInteractive, 0); err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	limiter.setLimit(10, 0) // Settings re-read on the next call.

	start := time.Now()
	if err := limiter.acquire(context.Background(), llmInteractive, 0); err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 80*time.Millisecond {
		t.Fatalf("expected the penalty to hold the second call, waited only %v", elapsed)
	}
}

func TestLLMPriorityForPurpose(t *testing.T) {
	if got := llmPriorityForPurpose("job_score"); got != llmBackground {
		t.Fatalf("job scoring runs behind a background search, expected llmBackground")
	}
	for _, purpose := range []llmPurpose{"resume_parse", "resume_gap", "resume_optimize", "cover_letter", "job_analyze"} {
		if got := llmPriorityForPurpose(purpose); got != llmInteractive {
			t.Fatalf("%s has a user waiting on it, expected llmInteractive", purpose)
		}
	}
}

// Counting requests is not enough: Groq's free tier meters TOKENS per minute and
// charges the RESERVED output, so a resume parse (2k prompt, 8k reserved) is most
// of the 12k budget on its own. A request-only limiter waved five such calls
// through per minute and the provider refused every one after the first.
func TestLimiterPacesOnTokensNotJustRequests(t *testing.T) {
	limiter := newLLMLimiter(100) // requests are not the constraint here
	limiter.window = 150 * time.Millisecond
	limiter.setLimit(100, 10_000)

	// 6k + 6k fits; the third 6k call must wait for the window.
	for i := 0; i < 1; i++ {
		if err := limiter.acquire(context.Background(), llmInteractive, 6_000); err != nil {
			t.Fatalf("first acquire: %v", err)
		}
	}
	start := time.Now()
	if err := limiter.acquire(context.Background(), llmInteractive, 6_000); err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 80*time.Millisecond {
		t.Fatalf("expected the token budget to hold the second call, waited only %v", elapsed)
	}
}

// A single call too large for the budget must not deadlock: let it go and let the
// provider's 429 be the judge. A 429 is recoverable; a hang is not.
func TestLimiterDoesNotStallOnACallBiggerThanTheTokenBudget(t *testing.T) {
	limiter := newLLMLimiter(10)
	limiter.setLimit(10, 1_000)

	done := make(chan error, 1)
	go func() { done <- limiter.acquire(context.Background(), llmInteractive, 50_000) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected the oversized call to be let through, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("an oversized call deadlocked the limiter")
	}
}

func TestTokenBudgetPerProvider(t *testing.T) {
	groq := appConfig{Form: configForm{Provider: "Groq"}}
	if got := llmTokensPerMinute(groq); got != groqFreeTokensPerMinute {
		t.Fatalf("expected Groq's free token budget %d, got %d", groqFreeTokensPerMinute, got)
	}
	gemini := appConfig{Form: configForm{Provider: "Gemini"}}
	if llmTokensPerMinute(gemini) <= groqFreeTokensPerMinute {
		t.Fatal("Gemini's free token budget is far larger than Groq's; pacing it like Groq would throttle it for nothing")
	}
	// Guessing a budget for a provider we have not measured would throttle honest
	// users; leave those to the request limiter and the provider's own 429.
	if got := llmTokensPerMinute(appConfig{Form: configForm{Provider: "OpenAI"}}); got != 0 {
		t.Fatalf("expected no token budget for an unmeasured provider, got %d", got)
	}
	custom := appConfig{Form: configForm{Provider: "Groq", LLMTokensPerMinute: 500_000}}
	if got := llmTokensPerMinute(custom); got != 500_000 {
		t.Fatalf("expected the configured budget to win, got %d", got)
	}
}

// The reserved output is what providers charge, so it has to be in the estimate.
func TestEstimateTokensCountsTheReservedOutput(t *testing.T) {
	got := estimateTokens(strings.Repeat("x", 4_000), 8_192)
	if got < 8_192 {
		t.Fatalf("the reservation alone is 8192; estimate must exceed it, got %d", got)
	}
	if got != 4_000/charsPerToken+8_192 {
		t.Fatalf("unexpected estimate %d", got)
	}
}

func TestLLMRequestsPerMinuteDefaultsToTheFreeTier(t *testing.T) {
	if got := llmRequestsPerMinute(appConfig{}); got != llmDefaultRequestsPerMinute {
		t.Fatalf("expected the free-tier default %d, got %d", llmDefaultRequestsPerMinute, got)
	}
	config := appConfig{Form: configForm{LLMRequestsPerMinute: 120}}
	if got := llmRequestsPerMinute(config); got != 120 {
		t.Fatalf("expected the configured budget to win, got %d", got)
	}
	local := appConfig{Form: configForm{Provider: "Ollama"}}
	if got := llmRequestsPerMinute(local); got <= llmDefaultRequestsPerMinute {
		t.Fatalf("a local model is nobody's rate limit, expected a wide budget, got %d", got)
	}
}
