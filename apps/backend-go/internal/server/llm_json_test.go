package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"testing"
	"time"
)

// captureTransport records the last request it served and replies with a
// canned body, so tests can assert on the endpoint/headers/body used by
// each provider without hitting the network.
type captureTransport struct {
	lastRequest   *http.Request
	lastBody      string
	status        int
	respBody      string
	contentLength int64
	// calls counts round-trips, so a test can assert that a batch of jobs cost
	// one provider request rather than one each.
	calls int
}

func (c *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c.lastRequest = req
	c.calls++
	if req.Body != nil {
		body, _ := io.ReadAll(req.Body)
		c.lastBody = string(body)
	}
	status := c.status
	if status == 0 {
		status = 200
	}
	return &http.Response{
		StatusCode:    status,
		Body:          io.NopCloser(strings.NewReader(c.respBody)),
		Header:        make(http.Header),
		ContentLength: c.contentLength,
	}, nil
}

func newTestScraperBridge(transport http.RoundTripper) *scraperBridge {
	return &scraperBridge{
		logger: log.New(io.Discard, "", 0),
		client: &http.Client{Transport: transport},
	}
}

func TestGenerateJSONRoutesByProvider(t *testing.T) {
	cases := []struct {
		name           string
		provider       string
		respBody       string
		wantEndpoint   string
		wantAuthHeader string
		wantJSON       string
	}{
		{
			name:         "gemini",
			provider:     "google-gemini",
			respBody:     `{"candidates":[{"content":{"parts":[{"text":"{\"ok\":true}"}]}}]}`,
			wantEndpoint: "generateContent",
			wantJSON:     `{"ok":true}`,
		},
		{
			name:           "anthropic",
			provider:       "anthropic",
			respBody:       `{"content":[{"text":"{\"ok\":true}"}]}`,
			wantEndpoint:   "api.anthropic.com/v1/messages",
			wantAuthHeader: "x-api-key",
			wantJSON:       `{"ok":true}`,
		},
		{
			name:           "openai",
			provider:       "openai",
			respBody:       `{"choices":[{"message":{"content":"{\"ok\":true}"}}]}`,
			wantEndpoint:   "api.openai.com/v1/chat/completions",
			wantAuthHeader: "Authorization",
			wantJSON:       `{"ok":true}`,
		},
		{
			name:           "openrouter",
			provider:       "openrouter",
			respBody:       `{"choices":[{"message":{"content":"{\"ok\":true}"}}]}`,
			wantEndpoint:   "openrouter.ai/api/v1/chat/completions",
			wantAuthHeader: "Authorization",
			wantJSON:       `{"ok":true}`,
		},
		{
			name:           "groq",
			provider:       "groq",
			respBody:       `{"choices":[{"message":{"content":"{\"ok\":true}"}}]}`,
			wantEndpoint:   "api.groq.com/openai/v1/chat/completions",
			wantAuthHeader: "Authorization",
			wantJSON:       `{"ok":true}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transport := &captureTransport{respBody: tc.respBody}
			s := newTestScraperBridge(transport)
			config := defaultConfig()
			config.Form.Provider = tc.provider
			config.Form.Model = "test-model"

			got, err := s.generateJSON(context.Background(), config, "test-key", "prompt text")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantJSON {
				t.Fatalf("expected %q, got %q", tc.wantJSON, got)
			}
			if transport.lastRequest == nil {
				t.Fatal("expected a request to be sent")
			}
			if !strings.Contains(transport.lastRequest.URL.String(), tc.wantEndpoint) {
				t.Fatalf("expected endpoint containing %q, got %q", tc.wantEndpoint, transport.lastRequest.URL.String())
			}
			if tc.wantAuthHeader != "" && transport.lastRequest.Header.Get(tc.wantAuthHeader) == "" {
				t.Fatalf("expected header %q to be set", tc.wantAuthHeader)
			}
			if !strings.Contains(transport.lastBody, "prompt text") {
				t.Fatalf("expected request body to include the prompt, got %q", transport.lastBody)
			}
		})
	}
}

// Every model the cascade can fall back to must have thinking switched off, or
// the fallback quietly reintroduces the failure it exists to rescue us from: a
// thinking model spends the job-scoring output budget deliberating and answers
// with half a JSON object. Adding a model to geminiModelFallbacks should mean
// deciding this on purpose, not discovering it from a broken sweep.
func TestEveryFallbackModelHasThinkingDisabled(t *testing.T) {
	for _, model := range geminiModelFallbacks {
		if geminiThinkingConfig(model) == nil {
			t.Errorf("%s is a fallback model but would be asked to think; job scoring reserves only %d output tokens and thought tokens are billed against them", model, llmMaxOutputTokensScore)
		}
	}
}

// TestGeminiGenerationConfig locks in the two latency guards on the Gemini
// payload: thinking is switched off for the flash models that default to it
// (the reason a tailoring call used to outlive its timeout), and the response
// is capped so an uncapped model cannot ramble past the deadline.
func TestGeminiGenerationConfig(t *testing.T) {
	cases := []struct {
		name         string
		model        string
		wantThinking bool
	}{
		{name: "flash disables thinking", model: "gemini-2.5-flash", wantThinking: true},
		{name: "flash-lite disables thinking", model: "gemini-2.5-flash-lite", wantThinking: true},
		{name: "pro keeps its default", model: "gemini-2.5-pro", wantThinking: false},
		// The guard used to key on the literal "2.5-flash", so it silently stopped
		// applying the day the default became an alias — and the next model we
		// shipped went straight back to thinking. A live radar sweep caught it,
		// answering job scoring with unusable JSON. Every model we can actually be
		// configured with has to be covered, not just the one that was current when
		// the check was written.
		{name: "the aliased default disables thinking", model: geminiFreeModel, wantThinking: true},
		{name: "flash-latest disables thinking", model: "gemini-flash-latest", wantThinking: true},
		{name: "flash-lite-latest disables thinking", model: "gemini-flash-lite-latest", wantThinking: true},
		{name: "gemini 3 flash disables thinking", model: "gemini-3-flash-preview", wantThinking: true},
		{name: "pro-latest keeps its default", model: "gemini-pro-latest", wantThinking: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transport := &captureTransport{respBody: `{"candidates":[{"content":{"parts":[{"text":"{\"ok\":true}"}]}}]}`}
			s := newTestScraperBridge(transport)
			config := defaultConfig()
			config.Form.Provider = "google-gemini"
			config.Form.Model = tc.model

			if _, err := s.generateJSON(context.Background(), config, "test-key", "prompt text"); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			var sent struct {
				GenerationConfig struct {
					MaxOutputTokens int `json:"maxOutputTokens"`
					ThinkingConfig  *struct {
						ThinkingBudget *int `json:"thinkingBudget"`
					} `json:"thinkingConfig"`
				} `json:"generationConfig"`
			}
			if err := json.Unmarshal([]byte(transport.lastBody), &sent); err != nil {
				t.Fatalf("could not decode request body: %v", err)
			}

			if sent.GenerationConfig.MaxOutputTokens != llmMaxOutputTokens {
				t.Fatalf("expected the default maxOutputTokens %d, got %d", llmMaxOutputTokens, sent.GenerationConfig.MaxOutputTokens)
			}
			gotThinking := sent.GenerationConfig.ThinkingConfig != nil
			if gotThinking != tc.wantThinking {
				t.Fatalf("expected thinkingConfig present=%v for %s, got %v", tc.wantThinking, tc.model, gotThinking)
			}
			if gotThinking {
				budget := sent.GenerationConfig.ThinkingConfig.ThinkingBudget
				if budget == nil || *budget != 0 {
					t.Fatalf("expected thinkingBudget 0 for %s, got %v", tc.model, budget)
				}
			}
		})
	}
}

// The output ceiling has to fit the task in both directions. Parse returns a
// whole canonical resume and truncates below ~8k; job scoring returns one small
// number, and reserving 8k for it starves a provider's token-per-minute budget —
// Groq's free tier refused a healthy request purely on the reservation.
func TestOutputCeilingIsSizedPerTask(t *testing.T) {
	if llmMaxOutputTokensFor("resume_parse") <= llmMaxOutputTokensFor("job_score") {
		t.Fatal("parse returns a whole resume; scoring returns a number — parse must get the bigger ceiling")
	}
	if got := llmMaxOutputTokensFor("resume_parse"); got != llmMaxOutputTokensParse {
		t.Fatalf("expected the parse ceiling %d, got %d", llmMaxOutputTokensParse, got)
	}
	if got := llmMaxOutputTokensFor("something_new"); got != llmMaxOutputTokens {
		t.Fatalf("an unnamed purpose must fall back to the default %d, got %d", llmMaxOutputTokens, got)
	}
	// A caller that bypasses the cascade still gets a bounded response.
	if got := maxOutputTokensFor(appConfig{}); got != llmMaxOutputTokens {
		t.Fatalf("expected the default ceiling with no cascade, got %d", got)
	}
}

// The ceiling the cascade picks for the purpose must actually reach the provider.
func TestCascadeSendsThePurposeCeilingToTheProvider(t *testing.T) {
	a := newCascadeTestAPI(t)
	config := cascadeTestConfig("Gemini", geminiFreeModel, "", "", "", "")

	var seen int
	generate := func(ctx context.Context, cfg appConfig, key, prompt string) (string, error) {
		seen = maxOutputTokensFor(cfg)
		return `{"ok":true}`, nil
	}

	if _, err := a.runLLMWithFallback(context.Background(), "resume_parse", config, "p", generate); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seen != llmMaxOutputTokensParse {
		t.Fatalf("expected the parse ceiling %d to reach the generator, got %d", llmMaxOutputTokensParse, seen)
	}
}

// The OpenAI-compatible providers were only asked for JSON in prose, and treated
// it as a suggestion — Groq's llama-3.3-70b failed a resume parse outright.
func TestOpenAICompatibleAsksForJSONMode(t *testing.T) {
	transport := &captureTransport{respBody: `{"choices":[{"message":{"content":"{\"ok\":true}"}}]}`}
	s := newTestScraperBridge(transport)
	config := defaultConfig()
	config.Form.Provider = "groq"
	config.Form.Model = "llama-3.3-70b-versatile"

	if _, err := s.generateJSON(context.Background(), config, "test-key", "prompt"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(transport.lastBody, `"response_format"`) || !strings.Contains(transport.lastBody, "json_object") {
		t.Fatalf("expected constrained decoding to be requested, got %q", transport.lastBody)
	}
}

// Not every model behind an OpenAI-shaped endpoint supports the parameter, and
// those reject the whole request. Drop it for them rather than deny JSON mode to
// everyone else.
func TestOpenAICompatibleRetriesWithoutJSONModeWhenRefused(t *testing.T) {
	transport := &sequenceTransport{responses: []sequenceResponse{
		{status: 400, body: `{"error":{"message":"response_format is not supported for this model"}}`},
		{status: 200, body: `{"choices":[{"message":{"content":"{\"ok\":true}"}}]}`},
	}}
	s := newTestScraperBridge(transport)
	config := defaultConfig()
	config.Form.Provider = "openrouter"
	config.Form.Model = "some/model"

	got, err := s.generateJSON(context.Background(), config, "test-key", "prompt")
	if err != nil {
		t.Fatalf("expected the retry without JSON mode to succeed, got %v", err)
	}
	if strings.TrimSpace(got) != `{"ok":true}` {
		t.Fatalf("unexpected result %q", got)
	}
	if len(transport.requests) != 2 {
		t.Fatalf("expected one refused call plus one retry, got %d", len(transport.requests))
	}
}

// A 400 that has nothing to do with response_format must not be retried: that
// would just pay twice for a genuinely bad request.
func TestOpenAICompatibleDoesNotRetryUnrelatedBadRequests(t *testing.T) {
	transport := &captureTransport{status: 400, respBody: `{"error":{"message":"model not found"}}`}
	s := newTestScraperBridge(transport)
	config := defaultConfig()
	config.Form.Provider = "groq"

	if _, err := s.generateJSON(context.Background(), config, "test-key", "prompt"); err == nil {
		t.Fatal("expected the bad request to fail")
	}
	if transport.calls != 1 {
		t.Fatalf("expected exactly one call for an unrelated 400, got %d", transport.calls)
	}
}

func TestGenerateJSONUnsupportedProvider(t *testing.T) {
	s := newTestScraperBridge(&captureTransport{})
	config := defaultConfig()
	config.Form.Provider = "unknown-provider"

	if _, err := s.generateJSON(context.Background(), config, "test-key", "prompt"); err == nil {
		t.Fatal("expected error for unsupported provider")
	}
}

func TestGenerateJSONReturnsErrorOnHTTPFailure(t *testing.T) {
	transport := &captureTransport{status: 401, respBody: `{"error":"unauthorized"}`}
	s := newTestScraperBridge(transport)
	config := defaultConfig()
	config.Form.Provider = "openai"

	if _, err := s.generateJSON(context.Background(), config, "bad-key", "prompt"); err == nil {
		t.Fatal("expected error for HTTP 401")
	}
}

func sizedJSONBody(t *testing.T, target int, build func(string) []byte) string {
	t.Helper()
	low, high := 0, target
	for low <= high {
		padding := (low + high) / 2
		body := build(strings.Repeat("x", padding))
		switch {
		case len(body) == target:
			return string(body)
		case len(body) < target:
			low = padding + 1
		default:
			high = padding - 1
		}
	}
	t.Fatalf("could not build a JSON body of exactly %d bytes", target)
	return ""
}

func providerSuccessBody(t *testing.T, provider string, target int) string {
	return sizedJSONBody(t, target, func(padding string) []byte {
		var payload any
		switch provider {
		case "gemini":
			payload = map[string]any{
				"candidates": []any{map[string]any{
					"content": map[string]any{"parts": []any{map[string]string{"text": `{"ok":true}`}}},
				}},
				"padding": padding,
			}
		case "anthropic":
			payload = map[string]any{
				"content": []any{map[string]string{"text": `{"ok":true}`}},
				"padding": padding,
			}
		default:
			payload = map[string]any{
				"choices": []any{map[string]any{
					"message": map[string]string{"content": `{"ok":true}`},
				}},
				"padding": padding,
			}
		}
		body, _ := json.Marshal(payload)
		return body
	})
}

func providerErrorBody(t *testing.T, target int) string {
	return sizedJSONBody(t, target, func(padding string) []byte {
		body, _ := json.Marshal(map[string]any{
			"error":   map[string]string{"message": "provider error"},
			"padding": padding,
		})
		return body
	})
}

func TestProviderResponseBodyLimitBoundaries(t *testing.T) {
	for _, provider := range []string{"gemini", "anthropic", "openrouter", "groq", "openai"} {
		t.Run(provider+" success", func(t *testing.T) {
			for _, target := range []int{llmMaxResponseBodyBytes - 1, llmMaxResponseBodyBytes} {
				transport := &captureTransport{respBody: providerSuccessBody(t, provider, target)}
				s := newTestScraperBridge(transport)
				config := defaultConfig()
				config.Form.Provider = provider
				got, err := s.generateJSONOnce(context.Background(), config, "test-key", "prompt")
				if err != nil {
					t.Fatalf("body at %d bytes should succeed: %v", target, err)
				}
				if got != `{"ok":true}` {
					t.Fatalf("unexpected provider result at %d bytes: %q", target, got)
				}
			}
		})

		t.Run(provider+" oversize success", func(t *testing.T) {
			body := providerSuccessBody(t, provider, llmMaxResponseBodyBytes+1)
			transport := &captureTransport{respBody: body}
			s := newTestScraperBridge(transport)
			config := defaultConfig()
			config.Form.Provider = provider
			_, err := s.generateJSONOnce(context.Background(), config, "test-key", "prompt")
			var tooLarge *externalResponseTooLargeError
			if err == nil || !errors.As(err, &tooLarge) {
				t.Fatalf("expected typed over-limit error, got %v", err)
			}
			if strings.Contains(err.Error(), "padding") {
				t.Fatal("oversized provider content leaked into the error")
			}
		})

		t.Run(provider+" oversize error", func(t *testing.T) {
			transport := &captureTransport{
				status:   429,
				respBody: providerErrorBody(t, llmMaxResponseBodyBytes+1),
			}
			s := newTestScraperBridge(transport)
			config := defaultConfig()
			config.Form.Provider = provider
			_, err := s.generateJSONOnce(context.Background(), config, "test-key", "prompt")
			var tooLarge *externalResponseTooLargeError
			if err == nil || !errors.As(err, &tooLarge) {
				t.Fatalf("expected typed over-limit error for HTTP error body, got %v", err)
			}
			if got := llmHTTPStatus(err); got != http.StatusTooManyRequests {
				t.Fatalf("expected HTTP status to survive bounded error handling, got %d (%v)", got, err)
			}
			if got := classifyProviderError(err); got != "rate_limited" {
				t.Fatalf("expected oversized 429 to remain rate_limited, got %q", got)
			}
		})
	}
}

func TestProviderResponseBodyContentLengthIsCheckedBeforeRead(t *testing.T) {
	transport := &captureTransport{
		contentLength: int64(llmMaxResponseBodyBytes + 1),
		respBody:      `{"choices":[{"message":{"content":"{\"ok\":true}"}}]}`,
	}
	s := newTestScraperBridge(transport)
	config := defaultConfig()
	config.Form.Provider = "openai"
	_, err := s.generateJSONOnce(context.Background(), config, "test-key", "prompt")
	var tooLarge *externalResponseTooLargeError
	if err == nil || !errors.As(err, &tooLarge) {
		t.Fatalf("expected typed Content-Length over-limit error, got %v", err)
	}
}

func TestGenerateJSONOnceDoesNotRetryRateLimit(t *testing.T) {
	transport := &captureTransport{status: 429, respBody: `{"error":"rate limited"}`}
	s := newTestScraperBridge(transport)
	config := defaultConfig()
	config.Form.Provider = "openai"

	start := time.Now()
	if _, err := s.generateJSONOnce(context.Background(), config, "test-key", "prompt"); err == nil {
		t.Fatal("expected rate limit error")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("expected generateJSONOnce to return without retry backoff, took %s", elapsed)
	}
}
