package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// A 429 can mean two very different things and we used to treat them as one.
//
//   - "you are asking too fast": wait the seconds the provider names and carry
//     on. This lifts on its own.
//   - "your daily quota is gone": nothing lifts until tomorrow. Retrying is pure
//     waste, and on a free-tier key it is the case that actually bites — a single
//     job search can spend a day's allowance.
//
// providerHTTPError carries what the provider actually told us, so the cascade
// can wait exactly as long as it was asked to instead of guessing 10/25/60s,
// and can stop retrying against a wall that will not move today.
type providerHTTPError struct {
	Provider string
	Status   int
	Body     string
	// RetryAfter is what the provider asked us to wait. Zero when it did not say.
	RetryAfter time.Duration
	// DailyQuota is set when the provider says the exhausted budget is a daily
	// (or account-level) one rather than a per-minute burst.
	DailyQuota bool
}

// Error keeps the historical "gemini HTTP 429: ..." shape. llmHTTPStatus reads
// the status back out of this text, and several tests assert on it.
func (e *providerHTTPError) Error() string {
	return fmt.Sprintf("%s HTTP %d: %s", e.Provider, e.Status, truncate(e.Body, 180))
}

// newProviderHTTPError reads the parts of a failed provider response that tell
// us how to behave next.
func newProviderHTTPError(provider string, response *http.Response, body []byte) *providerHTTPError {
	text := string(body)
	retryAfter := parseRetryAfter(response.Header.Get("Retry-After"), text, time.Now())
	return &providerHTTPError{
		Provider:   provider,
		Status:     response.StatusCode,
		Body:       text,
		RetryAfter: retryAfter,
		DailyQuota: exhaustedForTheDay(response.StatusCode, text, retryAfter),
	}
}

// exhaustedForTheDay decides whether to stop asking this provider. The order
// matters, and it is the order of how much the provider actually told us.
func exhaustedForTheDay(status int, body string, retryAfter time.Duration) bool {
	// The provider named a wait we cannot sit through inside one request. Groq
	// answers a spent daily token budget with "try again in 1h41m" — retrying
	// that is pure waste, whatever the rest of the payload says.
	if retryAfter > maxRateLimitRetryDelay {
		return true
	}
	// It promised relief within a wait we CAN sit through. Believe it.
	if liftsSoon(retryAfter) {
		return false
	}
	// It said nothing useful about timing: fall back to reading the body.
	return isDailyQuotaExhausted(status, body)
}

// liftsSoon reports that the provider asked us to wait only a short while. It is
// the tie-breaker on an ambiguous 429 — and "ambiguous" is not an overstatement.
//
// Measured against a live Gemini free-tier key (gemini-2.5-flash), a transient
// rate limit and a genuinely spent daily quota are INDISTINGUISHABLE in the
// response. Both arrive as:
//
//   - the same prose ("You exceeded your current quota, please check your plan
//     and billing details"),
//   - the same quotaId ("GenerateRequestsPerDayPerProjectPerModel-FreeTier"),
//   - and a short retryDelay (38s when a 20-request burst tripped a limit that
//     did clear; 9s when the day was actually gone and a single call, made after
//     90 seconds of silence, was still refused).
//
// So no field in the payload settles it, and this function is not a truth test.
// It is a *policy*: prefer the reading whose mistake is cheaper.
//
//   - Reading a transient limit as "daily" parks a working key for an hour and
//     skips the retry that would have succeeded in seconds. The user loses the
//     AI for an hour over a blip.
//   - Reading a daily exhaustion as "transient" costs one short retry ladder
//     that fails, after which the cascade parks the provider anyway. It
//     self-corrects within a minute.
//
// Hence: believe the short delay, retry, and let the retries' failure — not the
// first response's labelling — be what concludes the day is gone.
func liftsSoon(retryAfter time.Duration) bool {
	return retryAfter > 0 && retryAfter <= maxRateLimitRetryDelay
}

// geminiRetryDelay matches the RetryInfo Google returns inside a 429 body, e.g.
// {"retryDelay":"21s"}. It is more precise than the Retry-After header, which
// Gemini often omits.
var geminiRetryDelay = regexp.MustCompile(`"retryDelay"\s*:\s*"(\d+(?:\.\d+)?)s"`)

// parseRetryAfter prefers the standard header and falls back to the provider's
// in-body hint. The header is either a number of seconds or an HTTP date.
func parseRetryAfter(header, body string, now time.Time) time.Duration {
	if header = strings.TrimSpace(header); header != "" {
		if seconds, err := strconv.Atoi(header); err == nil && seconds >= 0 {
			return time.Duration(seconds) * time.Second
		}
		if at, err := http.ParseTime(header); err == nil {
			if wait := at.Sub(now); wait > 0 {
				return wait
			}
			return 0
		}
	}
	if match := geminiRetryDelay.FindStringSubmatch(body); len(match) == 2 {
		if seconds, err := strconv.ParseFloat(match[1], 64); err == nil && seconds >= 0 {
			return time.Duration(seconds * float64(time.Second))
		}
	}
	return 0
}

// dailyQuotaMarkers are phrases that mean the exhausted budget is a daily or
// account-level one, and therefore will NOT lift on its own today.
//
// The bar for adding one is high, and one entry here was already wrong. Gemini
// answers BOTH a per-minute rate limit and a spent daily quota with the same
// prose — "You exceeded your current quota, please check your plan and billing
// details" — so matching on that sentence classified every transient 429 as a
// dead key: it parked the provider for an hour and skipped the retries that
// would have succeeded seconds later. Only the structured quotaId tells the two
// apart, which is what quotaIDMentionsDay reads.
//
// So: nothing here may match a message a provider also emits for a limit that
// lifts by itself. Anything unrecognised falls through to "transient", because
// retrying a transient limit is cheap and refusing to retry a real one is not.
var dailyQuotaMarkers = []string{
	"requests per day",
	// Groq meters a daily TOKEN budget as well as a per-minute one.
	"tokens per day",
	"(tpd)",
	"daily limit",
	// OpenAI/OpenRouter: the account balance is gone, not a burst limit.
	"insufficient_quota",
	"insufficient quota",
	"out of credits",
}

func isDailyQuotaExhausted(status int, body string) bool {
	if status != http.StatusTooManyRequests && status != http.StatusPaymentRequired && status != http.StatusForbidden {
		return false
	}
	normalized := strings.ToLower(body)
	for _, marker := range dailyQuotaMarkers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return quotaIDMentionsDay(body)
}

// quotaIDMentionsDay looks inside Google's structured QuotaFailure for a quota
// whose id is a per-day one, e.g. "GenerateRequestsPerDayPerProjectPerModel".
func quotaIDMentionsDay(body string) bool {
	for _, id := range quotaIDs(body) {
		if strings.Contains(strings.ToLower(id), "perday") {
			return true
		}
	}
	return false
}

// quotaIDs lists the quotas the provider says we breached. Naming the quota is
// the difference between "wait 30 seconds" and "come back tomorrow", and it is
// the only thing in the response that actually distinguishes them — so it is
// worth surfacing rather than leaving the user to guess which one they hit.
func quotaIDs(body string) []string {
	var payload struct {
		Error struct {
			Details []struct {
				Violations []struct {
					QuotaID string `json:"quotaId"`
				} `json:"violations"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return nil
	}
	var ids []string
	for _, detail := range payload.Error.Details {
		for _, violation := range detail.Violations {
			if id := strings.TrimSpace(violation.QuotaID); id != "" {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// quotaBreachedDescription names what the provider said ran out, for the log.
func quotaBreachedDescription(err error) string {
	var providerErr *providerHTTPError
	if !errors.As(err, &providerErr) {
		return truncate(err.Error(), 180)
	}
	if ids := quotaIDs(providerErr.Body); len(ids) > 0 {
		return strings.Join(ids, ", ")
	}
	return truncate(providerErr.Body, 180)
}

// rateLimitDiagnostics reports everything a 429 told us, so a wrong reading is
// visible in the Logs view instead of having to be inferred from behaviour.
func rateLimitDiagnostics(err error) string {
	var providerErr *providerHTTPError
	if !errors.As(err, &providerErr) {
		return truncate(err.Error(), 240)
	}
	ids := quotaIDs(providerErr.Body)
	if len(ids) == 0 {
		ids = []string{"(nenhuma cota nomeada)"}
	}
	// The body is kept long enough to reach the quota metric and its limit, which
	// is the part that actually says WHICH ceiling was hit. Truncating before it
	// is how the first two misreadings of this error survived as long as they did.
	return fmt.Sprintf("cotas=%s retryAfter=%s tratadoComoDiario=%v | %s",
		strings.Join(ids, ","),
		providerErr.RetryAfter,
		providerErr.DailyQuota,
		truncate(collapseWhitespace(providerErr.Body), 900),
	)
}

// collapseWhitespace squeezes a pretty-printed JSON error body onto one line, so
// the log stays readable and the truncation budget is spent on content rather
// than on indentation.
func collapseWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

// retryAfterFor digs the provider's requested wait out of an error chain.
func retryAfterFor(err error) (time.Duration, bool) {
	var providerErr *providerHTTPError
	if !errors.As(err, &providerErr) || providerErr.RetryAfter <= 0 {
		return 0, false
	}
	return providerErr.RetryAfter, true
}

func isDailyQuotaError(err error) bool {
	var providerErr *providerHTTPError
	return errors.As(err, &providerErr) && providerErr.DailyQuota
}
