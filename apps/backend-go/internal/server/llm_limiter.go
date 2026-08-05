package server

import (
	"context"
	"strings"
	"sync"
	"time"
)

// The app spends one API key from two places: job-search scoring and Resume
// Studio. Until now neither knew about the other — search paced itself with its
// own llmGate, and Resume Studio built a fresh gate per call, so its gate always
// saw a zero lastCall and never waited at all. A background search and a
// foreground tailoring therefore raced each other into the provider's per-minute
// limit, on a free-tier key that has very little of it to give.
//
// llmLimiter is the single budget both go through. It is a sliding window, not a
// fixed delay between calls, and that distinction is the point: a fixed delay
// would tax Resume Studio's four-or-five calls with a wait each, re-introducing
// the very latency this work removed. A window lets a short burst through
// untouched and only holds back the caller that is actually filling the quota —
// in practice, the search scoring dozens of jobs.

// llmDefaultRequestsPerMinute is sized for Google AI Studio's free tier, which
// is the configuration the app ships with. A live free key was measured refusing
// the 20th request inside a minute ("limit: 20, model: gemini-2.5-flash"), so 10
// leaves real headroom rather than sitting on the edge of the provider's ceiling.
// A paid key can afford far more; raise it in Settings rather than here.
const llmDefaultRequestsPerMinute = 10

// llmRateWindow is the period the provider's per-minute limit is measured over.
const llmRateWindow = time.Minute

// Counting requests is not enough. Some providers meter TOKENS per minute, and
// they charge the RESERVED output (our max_tokens), not what the model actually
// produced. Groq's free tier allows 12k tokens/minute, and a resume parse —
// a ~2k-token prompt reserving 8k of output — is most of that on its own. A
// request-only limiter waved five such calls through per minute and every one
// after the first was refused, on a provider that was perfectly healthy.
//
// So the limiter also books an estimate of each call's tokens. Being an estimate
// it is deliberately generous: the cost of over-estimating is a short wait, the
// cost of under-estimating is the 429 this exists to prevent.
const (
	// Free-tier token budgets, per provider. Zero means "we do not know of a
	// token limit here", which leaves such a provider paced by requests alone.
	groqFreeTokensPerMinute   = 12_000
	geminiFreeTokensPerMinute = 250_000

	// charsPerToken is the usual rough ratio for English/Portuguese prose. It
	// only has to be in the right ballpark: the reserved output dominates.
	charsPerToken = 4
)

// llmTokensPerMinute is the token budget to respect for this provider, or 0 when
// none is known.
func llmTokensPerMinute(config appConfig) int {
	if configured := config.Form.LLMTokensPerMinute; configured > 0 {
		return configured
	}
	provider := cascadeProviderKey(config.Form.Provider)
	switch {
	case isLocalProvider(provider):
		return 0
	case strings.Contains(provider, "groq"):
		return groqFreeTokensPerMinute
	case strings.Contains(provider, "gemini") || strings.Contains(provider, "google"):
		return geminiFreeTokensPerMinute
	default:
		// Anthropic/OpenAI/OpenRouter meter tokens too, but their free/entry
		// budgets vary enough that guessing one would throttle honest users. Leave
		// them to the request limiter and the provider's own 429.
		return 0
	}
}

// estimateTokens is what one call will cost against a token-per-minute budget:
// the prompt plus the output we reserve, which providers charge whether or not
// the model fills it.
func estimateTokens(prompt string, maxOutputTokens int) int {
	return len(prompt)/charsPerToken + maxOutputTokens
}

// backgroundYield is how long a background caller steps aside for while someone
// is waiting on a spinner.
const backgroundYield = 250 * time.Millisecond

type llmPriority int

const (
	// llmBackground is work nobody is watching: job-search scoring.
	llmBackground llmPriority = iota
	// llmInteractive is work with a user waiting on it: every Resume Studio step.
	llmInteractive
)

// llmCall is one request booked against the window: when it went out, and the
// tokens it reserved.
type llmCall struct {
	at     time.Time
	tokens int
}

type llmLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	// tokenLimit is the tokens-per-minute budget, or 0 when the provider has none
	// we know of.
	tokenLimit int
	// calls holds each request still inside the window.
	calls []llmCall
	// interactiveWaiting is how many user-facing calls are queued. Background
	// callers stand aside while it is non-zero, so a long search cannot make the
	// user wait behind thirty job scores for their own click.
	interactiveWaiting int
	// penaltyLimit is a temporarily tighter budget applied after the provider
	// rate-limited us anyway, which means our configured guess was too generous.
	// It is kept separate from limit so that re-reading the user's setting on the
	// next call cannot silently undo the penalty.
	penaltyLimit int
	penaltyUntil time.Time
}

func newLLMLimiter(requestsPerMinute int) *llmLimiter {
	if requestsPerMinute <= 0 {
		requestsPerMinute = llmDefaultRequestsPerMinute
	}
	return &llmLimiter{limit: requestsPerMinute, window: llmRateWindow}
}

// setLimit re-reads the configured budgets, so changing the provider or the
// setting takes effect without a restart. A tokensPerMinute of 0 means the
// provider has no token budget we know of.
func (l *llmLimiter) setLimit(requestsPerMinute, tokensPerMinute int) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if requestsPerMinute > 0 {
		l.limit = requestsPerMinute
	}
	l.tokenLimit = tokensPerMinute
}

// penalize tightens the budget for a while. The provider is the authority on its
// own limit: if it rate-limited us while we thought we were inside budget, our
// number was wrong and backing off is the only honest response.
func (l *llmLimiter) penalize(requestsPerMinute int, until time.Time) {
	if l == nil || requestsPerMinute <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.penaltyLimit = requestsPerMinute
	l.penaltyUntil = until
}

// limitAtLocked is the budget in force right now.
func (l *llmLimiter) limitAtLocked(now time.Time) int {
	if l.penaltyLimit > 0 && now.Before(l.penaltyUntil) && l.penaltyLimit < l.limit {
		return l.penaltyLimit
	}
	return l.limit
}

// acquire blocks until this caller may issue one request costing about tokens,
// and books it. A nil limiter allows everything, which keeps tests that build an
// api literal free of timing dependencies.
func (l *llmLimiter) acquire(ctx context.Context, priority llmPriority, tokens int) error {
	if l == nil {
		return nil
	}

	if priority == llmInteractive {
		l.mu.Lock()
		l.interactiveWaiting++
		l.mu.Unlock()
		defer func() {
			l.mu.Lock()
			l.interactiveWaiting--
			l.mu.Unlock()
		}()
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		l.mu.Lock()
		now := time.Now()
		l.pruneLocked(now)

		// Yield to a user who is waiting rather than take the slot they need.
		if priority == llmBackground && l.interactiveWaiting > 0 {
			l.mu.Unlock()
			if err := sleepContext(ctx, backgroundYield); err != nil {
				return err
			}
			continue
		}

		requestsOK := len(l.calls) < l.limitAtLocked(now)
		tokensOK := l.tokenLimit <= 0 || l.tokensUsedLocked()+tokens <= l.tokenLimit

		if requestsOK && tokensOK {
			l.calls = append(l.calls, llmCall{at: now, tokens: tokens})
			l.mu.Unlock()
			return nil
		}

		// A single call that cannot fit the token budget even in an empty window
		// would wait forever. Let it through and let the provider be the judge:
		// its 429 is recoverable, a deadlock is not.
		if !tokensOK && len(l.calls) == 0 {
			l.calls = append(l.calls, llmCall{at: now, tokens: tokens})
			l.mu.Unlock()
			return nil
		}

		// Whichever budget is full, the oldest call has to age out of the window
		// before anything frees up.
		retryIn := l.calls[0].at.Add(l.window).Sub(now)
		l.mu.Unlock()

		if retryIn <= 0 {
			continue
		}
		if err := sleepContext(ctx, retryIn); err != nil {
			return err
		}
	}
}

func (l *llmLimiter) tokensUsedLocked() int {
	total := 0
	for _, call := range l.calls {
		total += call.tokens
	}
	return total
}

func (l *llmLimiter) pruneLocked(now time.Time) {
	cutoff := now.Add(-l.window)
	kept := l.calls[:0]
	for _, call := range l.calls {
		if call.at.After(cutoff) {
			kept = append(kept, call)
		}
	}
	l.calls = kept
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// llmRequestsPerMinute reads the budget from config, defaulting to the free
// tier. Local models are not rate-limited by anyone, so they get a wide budget.
func llmRequestsPerMinute(config appConfig) int {
	if configured := config.Form.LLMRequestsPerMinute; configured > 0 {
		return configured
	}
	if isLocalProvider(config.Form.Provider) {
		return 600
	}
	return llmDefaultRequestsPerMinute
}

// llmPriorityForPurpose splits the callers by whether a human is waiting. Every
// Resume Studio step is a button the user just pressed; job scoring runs behind
// a background search they can leave alone.
func llmPriorityForPurpose(purpose llmPurpose) llmPriority {
	if strings.EqualFold(string(purpose), "job_score") {
		return llmBackground
	}
	return llmInteractive
}
