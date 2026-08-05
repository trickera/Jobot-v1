package server

import (
	"net/http"
	"testing"
	"time"
)

func TestParseRetryAfterReadsTheHeader(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)

	if got := parseRetryAfter("20", "", now); got != 20*time.Second {
		t.Fatalf("expected 20s from a seconds header, got %v", got)
	}
	if got := parseRetryAfter(now.Add(45*time.Second).Format(http.TimeFormat), "", now); got < 44*time.Second || got > 45*time.Second {
		t.Fatalf("expected ~45s from an HTTP-date header, got %v", got)
	}
	// A date already in the past means "go now", not "wait forever".
	if got := parseRetryAfter(now.Add(-time.Minute).Format(http.TimeFormat), "", now); got != 0 {
		t.Fatalf("expected 0 for a past date, got %v", got)
	}
	if got := parseRetryAfter("", "", now); got != 0 {
		t.Fatalf("expected 0 when the provider says nothing, got %v", got)
	}
}

// Gemini usually omits Retry-After and puts the delay in the error body instead.
func TestParseRetryAfterFallsBackToTheGeminiBodyHint(t *testing.T) {
	body := `{"error":{"code":429,"details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"21s"}]}}`

	if got := parseRetryAfter("", body, time.Now()); got != 21*time.Second {
		t.Fatalf("expected 21s from the body hint, got %v", got)
	}
}

// Regression: Gemini answers a transient rate limit with the SAME prose it uses
// for a spent daily quota — "You exceeded your current quota, please check your
// plan and billing details". Matching on that sentence classified every
// transient 429 as a dead key, parked the provider for an hour and skipped the
// retries that would have succeeded seconds later.
//
// (Measured later: the quotaId does not separate them either — a live free-tier
// key returns the PerDay id in both cases. See liftsSoon. The retry ladder, not
// the first response's labelling, is what decides the day is gone.)
func TestGeminiPerMinuteLimitIsNotADailyQuota(t *testing.T) {
	body := `{"error":{"code":429,"message":"You exceeded your current quota, please check your plan and billing details. For more information on this error, head to: https://ai.google.dev/gemini-api/docs/rate-limits.","status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.rpc.QuotaFailure","violations":[{"quotaMetric":"generativelanguage.googleapis.com/generate_content_free_tier_requests","quotaId":"GenerateRequestsPerMinutePerProjectPerModel-FreeTier"}]},{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"33s"}]}}`

	if isDailyQuotaExhausted(429, body) {
		t.Fatal("a per-minute limit must stay retryable: it lifts on its own in seconds")
	}

	err := &providerHTTPError{Provider: "gemini", Status: 429, Body: body, DailyQuota: isDailyQuotaExhausted(429, body)}
	if got := classifyProviderError(err); got != "rate_limited" {
		t.Fatalf("expected rate_limited so the cascade retries, got %q", got)
	}

	// And the delay the provider asked for must be honoured rather than guessed.
	if got := parseRetryAfter("", body, time.Now()); got != 33*time.Second {
		t.Fatalf("expected the 33s the provider asked for, got %v", got)
	}
}

// The same prose, but the quota that actually ran out is the daily one.
func TestGeminiPerDayQuotaIsRecognisedDespiteTheIdenticalMessage(t *testing.T) {
	body := `{"error":{"code":429,"message":"You exceeded your current quota, please check your plan and billing details.","status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.rpc.QuotaFailure","violations":[{"quotaId":"GenerateRequestsPerDayPerProjectPerModel-FreeTier"}]}]}}`

	if !isDailyQuotaExhausted(429, body) {
		t.Fatal("a per-day quota must be recognised, or we retry for hours against a wall")
	}
}

// A provider that names a per-day quota AND promises the block clears in seconds
// is not telling us the day is gone. Google can list several breached quotas in
// one response, so the promised delay wins: parking a working key for an hour on
// an ambiguous reading is the expensive mistake, and retrying a spent quota is
// the cheap one.
func TestAShortRetryDelayOverridesAPerDayQuotaID(t *testing.T) {
	body := `{"error":{"code":429,"details":[{"@type":"type.googleapis.com/google.rpc.QuotaFailure","violations":[{"quotaId":"GenerateRequestsPerMinutePerProjectPerModel-FreeTier"},{"quotaId":"GenerateRequestsPerDayPerProjectPerModel-FreeTier"}]},{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"27s"}]}}`

	response := &http.Response{StatusCode: 429, Header: http.Header{}}
	err := newProviderHTTPError("gemini", response, []byte(body))

	if err.DailyQuota {
		t.Fatal("a 27s retry delay means the block lifts on its own; the key must not be parked for an hour")
	}
	if got := classifyProviderError(err); got != "rate_limited" {
		t.Fatalf("expected rate_limited so the cascade waits and retries, got %q", got)
	}
	if err.RetryAfter != 27*time.Second {
		t.Fatalf("expected the 27s the provider asked for, got %v", err.RetryAfter)
	}
}

// A wait we cannot sit through inside one request is not a rate limit to ride
// out, whatever else the payload says. Groq answers a spent daily TOKEN budget
// with a 1h41m retry delay; retrying that burns the user's time for nothing.
// This is the real body, verbatim.
func TestAWaitTooLongToSitThroughIsTreatedAsExhausted(t *testing.T) {
	body := `{"error":{"message":"Rate limit reached for model ` + "`llama-3.3-70b-versatile`" + ` in organization ` + "`org_x`" + ` service tier ` + "`on_demand`" + ` on tokens per day (TPD): Limit 100000, Used 96984, Requested 10059. Please try again in 1h41m25.152s.","type":"tokens","code":"rate_limit_exceeded"}}`

	response := &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"6085"}}}
	err := newProviderHTTPError("groq", response, []byte(body))

	if !err.DailyQuota {
		t.Fatal("a provider asking for 1h41m has not rate-limited us, it has cut us off for the day")
	}
	if got := classifyProviderError(err); got != "quota_exhausted" {
		t.Fatalf("expected quota_exhausted so the cascade stops retrying, got %q", got)
	}
}

// But a per-day quota with no promise of relief is exactly what it looks like.
func TestAPerDayQuotaWithNoRetryDelayStaysDaily(t *testing.T) {
	body := `{"error":{"code":429,"details":[{"@type":"type.googleapis.com/google.rpc.QuotaFailure","violations":[{"quotaId":"GenerateRequestsPerDayPerProjectPerModel-FreeTier"}]}]}}`

	response := &http.Response{StatusCode: 429, Header: http.Header{}}
	err := newProviderHTTPError("gemini", response, []byte(body))

	if !err.DailyQuota {
		t.Fatal("expected a per-day quota with no retry hint to be treated as spent")
	}
}

// The distinction the whole task rests on: a per-minute limit lifts on its own,
// a spent daily allowance does not.
func TestIsDailyQuotaExhausted(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{
			name:   "gemini per-day quota id",
			status: 429,
			body:   `{"error":{"details":[{"@type":"type.googleapis.com/google.rpc.QuotaFailure","violations":[{"quotaId":"GenerateRequestsPerDayPerProjectPerModel-FreeTier"}]}]}}`,
			want:   true,
		},
		{
			name:   "gemini per-minute quota id",
			status: 429,
			body:   `{"error":{"details":[{"@type":"type.googleapis.com/google.rpc.QuotaFailure","violations":[{"quotaId":"GenerateRequestsPerMinutePerProjectPerModel-FreeTier"}]}]}}`,
			want:   false,
		},
		{
			name:   "openai exhausted balance",
			status: 429,
			body:   `{"error":{"type":"insufficient_quota","message":"You exceeded your current quota"}}`,
			want:   true,
		},
		{
			name:   "plain per-minute rate limit",
			status: 429,
			body:   `{"error":{"message":"Too many requests, please slow down"}}`,
			want:   false,
		},
		{
			// The sentence Gemini uses for BOTH cases, with no quota id to say which.
			// Unrecognised must mean transient: retrying a burst limit is cheap,
			// refusing to retry one is not.
			name:   "ambiguous quota prose with no quota id",
			status: 429,
			body:   `{"error":{"message":"You exceeded your current quota, please check your plan and billing details."}}`,
			want:   false,
		},
		{
			name:   "not a quota status at all",
			status: 500,
			body:   `{"error":"requests per day"}`,
			want:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDailyQuotaExhausted(tc.status, tc.body); got != tc.want {
				t.Fatalf("expected %v, got %v", tc.want, got)
			}
		})
	}
}

// llmHTTPStatus and several tests read the status back out of the error text, so
// the structured error has to keep the historical shape.
func TestProviderHTTPErrorKeepsTheLegacyMessageShape(t *testing.T) {
	err := &providerHTTPError{Provider: "gemini", Status: 429, Body: "quota"}

	if got := err.Error(); got != "gemini HTTP 429: quota" {
		t.Fatalf("unexpected message %q", got)
	}
	if got := llmHTTPStatus(err); got != 429 {
		t.Fatalf("expected llmHTTPStatus to still read 429, got %d", got)
	}
}

func TestClassifyDailyQuotaSeparatelyFromRateLimit(t *testing.T) {
	daily := &providerHTTPError{Provider: "gemini", Status: 429, Body: "quota", DailyQuota: true}
	burst := &providerHTTPError{Provider: "gemini", Status: 429, Body: "slow down"}

	if got := classifyProviderError(daily); got != "quota_exhausted" {
		t.Fatalf("expected quota_exhausted, got %q", got)
	}
	if got := classifyProviderError(burst); got != "rate_limited" {
		t.Fatalf("expected rate_limited, got %q", got)
	}
	if got := classifyResumeError(daily).Code; got != "ai_quota_exhausted" {
		t.Fatalf("expected the UI to see ai_quota_exhausted, got %q", got)
	}
	if got := classifyResumeError(burst).Code; got != "ai_rate_limited" {
		t.Fatalf("expected the UI to see ai_rate_limited, got %q", got)
	}
}
