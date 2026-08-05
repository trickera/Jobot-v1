package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// generateJSON asks the configured AI provider to answer a free-form prompt
// and returns the raw JSON text of the response. It reuses the same
// provider routing, retry/rate-limit handling as scoreWithLLM, but accepts
// any prompt instead of the fixed job-scoring one, so any Resume Studio
// feature (parse, job analysis, gap, tailoring, cover letter) can request
// structured JSON without duplicating the HTTP plumbing.
func (s *scraperBridge) generateJSON(ctx context.Context, config appConfig, apiKey, prompt string) (string, error) {
	retries := []time.Duration{0, 10 * time.Second, 25 * time.Second, 60 * time.Second}
	var lastErr error
	for attempt, pause := range retries {
		if pause > 0 {
			s.log("info", "[ LLM ] rate limit: aguardando %ds (tentativa %d/%d)", int(pause.Seconds()), attempt+1, len(retries))
			timer := time.NewTimer(pause)
			select {
			case <-ctx.Done():
				timer.Stop()
				return "", ctx.Err()
			case <-timer.C:
			}
		}
		text, err := s.generateJSONOnce(ctx, config, apiKey, prompt)
		if err == nil {
			return text, nil
		}
		lastErr = err
		if !isRateLimitStatus(llmHTTPStatus(err)) {
			return "", err
		}
	}
	return "", lastErr
}

// generateJSONOnce is generateJSON's provider-routing switch without its
// retry/backoff loop. The cascade owns retry and cooldown decisions itself.
func (s *scraperBridge) generateJSONOnce(ctx context.Context, config appConfig, apiKey, prompt string) (string, error) {
	provider := strings.ToLower(strings.TrimSpace(config.Form.Provider))
	switch {
	case strings.Contains(provider, "google") || strings.Contains(provider, "gemini"):
		return s.generateJSONGeminiOnce(ctx, geminiModelForConfig(config), apiKey, prompt, maxOutputTokensFor(config))
	case strings.Contains(provider, "anthropic"):
		return s.generateJSONAnthropicOnce(ctx, config, apiKey, prompt)
	case strings.Contains(provider, "openrouter"):
		return s.generateJSONOpenAICompatibleOnce(ctx, "https://openrouter.ai/api/v1/chat/completions", config, apiKey, prompt)
	case strings.Contains(provider, "groq"):
		return s.generateJSONOpenAICompatibleOnce(ctx, "https://api.groq.com/openai/v1/chat/completions", config, apiKey, prompt)
	case strings.Contains(provider, "openai"):
		return s.generateJSONOpenAICompatibleOnce(ctx, "https://api.openai.com/v1/chat/completions", config, apiKey, prompt)
	default:
		return "", errors.New("provedor de IA não suportado no backend Go")
	}
}

// Output ceilings, per task. The size matters twice over, and in opposite
// directions, which is why one global number could not work:
//
//   - Too small truncates the JSON mid-answer. The reply is then unparseable and
//     costs a repair round-trip, or fails outright. Parse is the hungry one: its
//     answer is the entire canonical resume, and a real one (four roles, ~50
//     skills) does not fit in 4k.
//   - Too large starves the provider's token budget, because providers charge the
//     RESERVED output against their rate limits, not the tokens actually produced.
//     Groq's free tier allows 12k tokens per minute; a live run showed it refusing
//     a ~2k-token prompt with "Requested 8527" — the rest was the reservation. At a
//     flat 8192, back-to-back resume steps rate-limited each other on a provider
//     that was otherwise perfectly healthy.
//
// So each task asks for roughly what its answer needs.
const (
	llmMaxOutputTokens = 4096 // default for anything not named below

	// The whole canonical resume comes back here.
	llmMaxOutputTokensParse = 8192

	// A patch set, already bounded by maxTailorPatchOps with short reasons.
	llmMaxOutputTokensOptimize = 6144

	// Small structured answers: a requirements list, four gap arrays, a letter.
	llmMaxOutputTokensAnalyze = 2048
	llmMaxOutputTokensScore   = 1024
)

func readLLMResponseBody(response *http.Response, provider string) ([]byte, error) {
	if response.ContentLength > llmMaxResponseBodyBytes {
		return nil, &externalResponseTooLargeError{Protocol: provider + " LLM", Limit: llmMaxResponseBodyBytes}
	}
	return readLimitedBody(response.Body, llmMaxResponseBodyBytes, provider+" LLM")
}

// providerBodyReadError keeps HTTP status classification available when a
// provider sends an oversized error body, while replacing that body with a
// fixed diagnostic. The original response bytes never enter an error string.
func providerBodyReadError(provider string, response *http.Response, readErr error) error {
	var tooLarge *externalResponseTooLargeError
	diagnostic := []byte("response body unavailable")
	if errors.As(readErr, &tooLarge) {
		diagnostic = []byte("response body exceeded configured limit")
	}
	return errors.Join(readErr, newProviderHTTPError(provider, response, diagnostic))
}

func llmMaxOutputTokensFor(purpose llmPurpose) int {
	switch purpose {
	case "resume_parse":
		return llmMaxOutputTokensParse
	case "resume_optimize":
		return llmMaxOutputTokensOptimize
	case "job_analyze", "resume_gap", "cover_letter":
		return llmMaxOutputTokensAnalyze
	case "job_score":
		return llmMaxOutputTokensScore
	default:
		return llmMaxOutputTokens
	}
}

// maxOutputTokensFor reads the ceiling the cascade chose for this attempt,
// falling back to the default when a caller bypassed the cascade.
func maxOutputTokensFor(config appConfig) int {
	if config.maxOutputTokens > 0 {
		return config.maxOutputTokens
	}
	return llmMaxOutputTokens
}

// geminiThinkingConfig disables reasoning tokens on the Gemini models that think
// by default. Every task this app asks for — score these jobs, rewrite this
// bullet, return this JSON — wants formatting discipline, not deliberation.
//
// Thinking is not free in three ways at once, which is why it is worth turning
// off rather than tolerating: it is unbounded latency before the first output
// byte, it is billed, and — the one that actually bites — Gemini counts thought
// tokens against maxOutputTokens. Job scoring reserves 1024 of those. A model
// that spends 500 of them thinking has half a budget left to write the JSON in,
// and a JSON answer that stops halfway is not a slow answer, it is a broken one.
//
// This used to key on the literal string "2.5-flash", which silently stopped
// applying the moment the default model changed to an alias — the very next model
// we shipped went back to thinking, and a live radar sweep answered with unusable
// JSON on the first call. Gate on the family instead of the version, so the next
// rename does not turn it off again.
//
// Measured accepting thinkingBudget: 0 (thoughtsTokenCount drops to zero, answer
// still valid) on 2026-07-11: gemini-2.5-flash, gemini-flash-latest,
// gemini-flash-lite-latest, gemini-3-flash-preview. The pro models are left alone:
// they reject a zero budget, and reasoning is the thing they are for.
func geminiThinkingConfig(model string) map[string]any {
	model = strings.ToLower(model)
	if !strings.Contains(model, "flash") || strings.Contains(model, "pro") {
		return nil
	}
	return map[string]any{"thinkingBudget": 0}
}

func (s *scraperBridge) generateJSONGeminiOnce(ctx context.Context, model, apiKey, prompt string, maxOutputTokens int) (string, error) {
	endpoint := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent", url.PathEscape(model))
	generationConfig := map[string]any{
		"temperature":      0,
		"responseMimeType": "application/json",
		"maxOutputTokens":  maxOutputTokens,
	}
	if thinking := geminiThinkingConfig(model); thinking != nil {
		generationConfig["thinkingConfig"] = thinking
	}
	payload := map[string]any{
		"contents": []map[string]any{{
			"parts": []map[string]string{{"text": prompt}},
		}},
		"generationConfig": generationConfig,
	}
	body, _ := json.Marshal(payload)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("x-goog-api-key", apiKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := s.llmHTTPClient().Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	rawBody, readErr := readLLMResponseBody(response, "gemini")
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if readErr != nil {
			return "", providerBodyReadError("gemini", response, readErr)
		}
		return "", newProviderHTTPError("gemini", response, rawBody)
	}
	if readErr != nil {
		return "", readErr
	}
	var data struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(rawBody, &data); err != nil {
		return "", err
	}
	if len(data.Candidates) == 0 || len(data.Candidates[0].Content.Parts) == 0 {
		return "", errors.New("gemini sem resposta")
	}
	return data.Candidates[0].Content.Parts[0].Text, nil
}

func (s *scraperBridge) generateJSONAnthropicOnce(ctx context.Context, config appConfig, apiKey, prompt string) (string, error) {
	model := strings.TrimSpace(config.Form.Model)
	if model == "" {
		model = "claude-sonnet-4-5"
	}
	payload := map[string]any{
		"model":       model,
		"max_tokens":  maxOutputTokensFor(config),
		"temperature": 0,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}
	body, _ := json.Marshal(payload)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("x-api-key", apiKey)
	request.Header.Set("anthropic-version", "2023-06-01")
	request.Header.Set("Content-Type", "application/json")
	response, err := s.llmHTTPClient().Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	rawBody, readErr := readLLMResponseBody(response, "Anthropic")
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if readErr != nil {
			return "", providerBodyReadError("Anthropic", response, readErr)
		}
		return "", newProviderHTTPError("Anthropic", response, rawBody)
	}
	if readErr != nil {
		return "", readErr
	}
	var data struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(rawBody, &data); err != nil {
		return "", err
	}
	if len(data.Content) == 0 {
		return "", errors.New("Anthropic sem resposta")
	}
	return data.Content[0].Text, nil
}

// generateJSONOpenAICompatibleOnce asks an OpenAI-shaped API for JSON.
//
// It asks twice at most. The first attempt requests constrained decoding
// (response_format json_object): Gemini has had responseMimeType from the start
// and never returned malformed JSON, while these providers were only asked in
// prose and treated it as a suggestion — a live run had Groq's llama-3.3-70b
// fail resume_parse with invalid_response and then fail the repair call too,
// every attempt paid for and none usable.
//
// Not every model behind an OpenAI-compatible endpoint supports the parameter,
// and those reject the whole request. Rather than deny JSON mode to everyone for
// the sake of the models that lack it, ask for it and drop it if refused.
func (s *scraperBridge) generateJSONOpenAICompatibleOnce(ctx context.Context, endpoint string, config appConfig, apiKey, prompt string) (string, error) {
	raw, err := s.openAICompatibleCall(ctx, endpoint, config, apiKey, prompt, true)
	if err != nil && rejectsJSONMode(err) {
		s.log("muted", "[ LLM ] %s não aceita response_format; repetindo sem JSON mode", config.Form.Model)
		return s.openAICompatibleCall(ctx, endpoint, config, apiKey, prompt, false)
	}
	return raw, err
}

// rejectsJSONMode spots the provider complaining about response_format itself,
// as opposed to any other 400. Retrying blindly on every 400 would double the
// cost of genuinely bad requests.
func rejectsJSONMode(err error) bool {
	var providerErr *providerHTTPError
	if !errors.As(err, &providerErr) || providerErr.Status != http.StatusBadRequest {
		return false
	}
	body := strings.ToLower(providerErr.Body)
	return strings.Contains(body, "response_format") || strings.Contains(body, "json_object")
}

func (s *scraperBridge) openAICompatibleCall(ctx context.Context, endpoint string, config appConfig, apiKey, prompt string, jsonMode bool) (string, error) {
	model := strings.TrimSpace(config.Form.Model)
	if model == "" {
		model = "gpt-4.1-mini"
	}
	payload := map[string]any{
		"model":       model,
		"temperature": 0,
		"max_tokens":  maxOutputTokensFor(config),
		"messages": []map[string]string{
			{"role": "system", "content": "Reply ONLY with valid JSON, no extra text."},
			{"role": "user", "content": prompt},
		},
	}
	if jsonMode {
		payload["response_format"] = map[string]string{"type": "json_object"}
	}
	body, _ := json.Marshal(payload)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := s.llmHTTPClient().Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	rawBody, readErr := readLLMResponseBody(response, "LLM")
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if readErr != nil {
			return "", providerBodyReadError("LLM", response, readErr)
		}
		return "", newProviderHTTPError("LLM", response, rawBody)
	}
	if readErr != nil {
		return "", readErr
	}
	var data struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rawBody, &data); err != nil {
		return "", err
	}
	if len(data.Choices) == 0 {
		return "", errors.New("LLM sem resposta")
	}
	return data.Choices[0].Message.Content, nil
}
