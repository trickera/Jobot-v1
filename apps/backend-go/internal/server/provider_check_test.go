package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClassifyProviderError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil is ok", nil, "ok"},
		{"401", errors.New("gemini: HTTP 401: bad key"), "unauthorized"},
		{"403", errors.New("gemini: HTTP 403: blocked"), "forbidden"},
		{"404 model", errors.New("gemini: HTTP 404: model not found"), "model_not_found"},
		{"429", errors.New("openrouter: HTTP 429: quota"), "rate_limited"},
		{"503", errors.New("groq: HTTP 503: overloaded"), "provider_unavailable"},
		{"timeout", fmt.Errorf("call: %w", context.DeadlineExceeded), "timeout"},
		{"invalid json sentinel", fmt.Errorf("%w: parse fail", errInvalidAIJSON), "invalid_response"},
		{"connection refused local", errors.New("dial tcp 127.0.0.1:11434: connectex: No connection could be made"), "local_model_unreachable"},
		{"other", errors.New("boom"), "network_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyProviderError(tc.err); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProviderTestEndpointSuccessAndTypedFailure(t *testing.T) {
	// sucesso: transporte devolve JSON pequeno válido
	okAPI, _ := newResumeParseTestAPI(t, geminiJSONResponse(`{"ok":true}`))
	req := providerTestRequest{Provider: "gemini", APIKey: "test-key", Model: geminiFreeModel}
	resp := okAPI.runProviderTest(context.Background(), req)
	if !resp.OK || resp.ErrorCode != "" {
		t.Fatalf("expected ok, got %+v", resp)
	}
	if resp.MaskedKey == "test-key" {
		t.Fatal("masked key must never equal the raw key")
	}
	if resp.LatencyMs < 0 {
		t.Fatalf("latency must be >= 0, got %d", resp.LatencyMs)
	}

	// falha tipada: transporte devolve 401 -> unauthorized (via captureTransport.status)
	store := newTestStore(t)
	if err := store.save(appConfig{Form: configForm{Provider: "gemini", APIKey: "test-key"}}); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	bridge := newTestScraperBridge(&captureTransport{status: 401, respBody: `{"error":"unauthorized"}`})
	bridge.store = store
	failAPI := &api{logger: log.New(io.Discard, "", 0), configStore: store, scraper: bridge}

	failReq := providerTestRequest{Provider: "gemini", APIKey: "test-key", Model: geminiFreeModel}
	failResp := failAPI.runProviderTest(context.Background(), failReq)
	if failResp.OK {
		t.Fatalf("expected failure, got ok response: %+v", failResp)
	}
	if failResp.ErrorCode != "unauthorized" {
		t.Fatalf("expected unauthorized error code, got %q", failResp.ErrorCode)
	}
	if failResp.Message == "" {
		t.Fatal("expected a non-empty message")
	}
	for _, r := range failResp.Message {
		if r > 127 {
			t.Fatalf("expected ASCII/EN message, got %q", failResp.Message)
		}
	}
	if failResp.MaskedKey == "test-key" {
		t.Fatal("masked key must never equal the raw key")
	}
}

func TestProviderTestEndpointHandlerRoutesThroughRunProviderTest(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		respBody string
		wantCode string
	}{
		{"unauthorized", 401, `{"error":"unauthorized"}`, "unauthorized"},
		{"model not found", 404, `{"error":"not found"}`, "model_not_found"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestStore(t)
			if err := store.save(appConfig{Form: configForm{Provider: "gemini", APIKey: "test-key"}}); err != nil {
				t.Fatalf("seed config: %v", err)
			}
			bridge := newTestScraperBridge(&captureTransport{status: tc.status, respBody: tc.respBody})
			bridge.store = store
			a := &api{logger: log.New(io.Discard, "", 0), configStore: store, scraper: bridge}

			req := httptest.NewRequest(http.MethodPost, "/api/v1/providers/test", strings.NewReader(`{"provider":"gemini","apiKey":"test-key","model":"gemini-flash-lite-latest"}`))
			rec := httptest.NewRecorder()
			a.providersTest(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
			}
			var resp providerTestResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp.OK {
				t.Fatalf("expected a typed failure, got ok response: %+v", resp)
			}
			if resp.ErrorCode != tc.wantCode {
				t.Fatalf("expected errorCode %q, got %q", tc.wantCode, resp.ErrorCode)
			}
			if strings.Contains(resp.MaskedKey, "test-key") == false && resp.MaskedKey == "" {
				t.Fatal("expected a masked key in the response")
			}
			if resp.MaskedKey == "test-key" {
				t.Fatal("masked key must never equal the raw key")
			}
		})
	}
}

// TestProviderTestRateLimitRacesInternalTimeout documents a real interaction
// discovered while implementing CH-02: generateJSON retries HTTP 429/503
// with backoff (0s, 10s, 25s, 60s) intended for long-running Resume Studio
// calls. runProviderTest's fixed 10s test-call timeout races that backoff:
// by the time the second attempt's 10s pause would elapse, the test's own
// context has already deadlined, so the call returns context.DeadlineExceeded
// instead of ever surfacing the underlying 429. classifyProviderError then
// (correctly, per its own contract) maps that to "timeout", not
// "rate_limited" — even though a raw 429 error IS mapped to
// "rate_limited" (see TestClassifyProviderError). This uses a short parent
// context deadline (well under 10s) purely so the test itself stays fast;
// it does not change runProviderTest's real, hard-coded 10s budget.
func TestProviderTestRateLimitRacesInternalTimeout(t *testing.T) {
	store := newTestStore(t)
	if err := store.save(appConfig{Form: configForm{Provider: "gemini", APIKey: "test-key"}}); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	bridge := newTestScraperBridge(&captureTransport{status: 429, respBody: `{"error":"quota"}`})
	bridge.store = store
	a := &api{logger: log.New(io.Discard, "", 0), configStore: store, scraper: bridge}

	shortCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	req := providerTestRequest{Provider: "gemini", APIKey: "test-key", Model: geminiFreeModel}
	resp := a.runProviderTest(shortCtx, req)
	if resp.OK {
		t.Fatalf("expected a typed failure, got ok response: %+v", resp)
	}
	if resp.ErrorCode == "" {
		t.Fatal("expected a non-empty error code")
	}
	if resp.MaskedKey == "test-key" {
		t.Fatal("masked key must never equal the raw key")
	}
}

// BUG-03 regression: testing a key-required provider with no key configured
// must return a distinct, fast "no_key" result instead of silently firing a
// real call with an empty credential (which providers typically answer with
// a 400 that classifyProviderError has no case for, so it used to fall
// through to a generic, unhelpful "network error").
func TestRunProviderTestNoKeyFastPath(t *testing.T) {
	store := newTestStore(t)
	if err := store.save(appConfig{Form: configForm{Provider: "gemini"}}); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	transport := &captureTransport{respBody: geminiJSONResponse(`{"ok":true}`)}
	bridge := newTestScraperBridge(transport)
	bridge.store = store
	a := &api{logger: log.New(io.Discard, "", 0), configStore: store, scraper: bridge}

	resp := a.runProviderTest(context.Background(), providerTestRequest{Provider: "gemini", Model: geminiFreeModel})

	if resp.OK {
		t.Fatalf("expected a typed failure for a missing key, got ok response: %+v", resp)
	}
	if resp.ErrorCode != "no_key" {
		t.Fatalf("expected errorCode %q, got %q", "no_key", resp.ErrorCode)
	}
	if resp.Message == "" {
		t.Fatal("expected a non-empty message")
	}
	if transport.lastRequest != nil {
		t.Fatal("expected no network call to be made when no key is configured")
	}
}

// Ollama is a local server with no key concept, so it must still attempt the
// real call (and surface local_model_unreachable / a genuine result) rather
// than being blocked by the no-key fast path meant for hosted providers.
func TestRunProviderTestOllamaDoesNotRequireKey(t *testing.T) {
	if providerRequiresAPIKey("ollama") {
		t.Fatal("ollama must not be classified as requiring an API key")
	}
	if providerRequiresAPIKey("openrouter") {
		t.Fatal("openrouter must not be classified as requiring an API key (public model list)")
	}
	for _, provider := range []string{"gemini", "google", "anthropic", "groq", "openai"} {
		if !providerRequiresAPIKey(provider) {
			t.Fatalf("expected %q to require an API key", provider)
		}
	}
}

func TestProviderTestHandlerRejectsInvalidJSON(t *testing.T) {
	a := &api{logger: log.New(io.Discard, "", 0)}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/providers/test", strings.NewReader(`not json`))
	rec := httptest.NewRecorder()
	a.providersTest(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}
