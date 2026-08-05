package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// classifyProviderError maps a raw LLM/provider error to the stable CH-02
// error codes shared by the provider-test endpoint and the cascade (CH-03).
func classifyProviderError(err error) string {
	if err == nil {
		return "ok"
	}
	if errors.Is(err, errAIDataConsentRequired) {
		return "consent_required"
	}
	if errors.Is(err, errLLMOperationBudgetSpent) {
		return "operation_budget_spent"
	}
	// A spent daily allowance is not a rate limit: nothing lifts until tomorrow,
	// so it must not be fed to a retry ladder. It is checked before the status
	// switch because the provider reports it as a 429 like any other. Our own
	// budget running out is the same condition, reached before the provider had
	// to refuse us.
	if isDailyQuotaError(err) || errors.Is(err, errQuotaBudgetSpent) {
		return "quota_exhausted"
	}
	switch llmHTTPStatus(err) {
	case 401:
		return "unauthorized"
	case 403:
		return "forbidden"
	case 404:
		return "model_not_found"
	case 429:
		return "rate_limited"
	case 503:
		return "provider_unavailable"
	}
	if errors.Is(err, errInvalidAIJSON) {
		return "invalid_response"
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "127.0.0.1:11434") || strings.Contains(msg, "localhost:11434") ||
		strings.Contains(msg, "connection could be made") || strings.Contains(msg, "connection refused") {
		return "local_model_unreachable"
	}
	// A timeout is not a transient network blip: the provider was reachable and
	// simply took longer than the budget. Retrying the same provider with the
	// same prompt just burns the budget a second time, so the cascade treats
	// this code as "move on" rather than "try again".
	if errors.Is(err, context.DeadlineExceeded) || isNetTimeout(err) {
		return "timeout"
	}
	return "network_error"
}

// providerTestRequest is the CH-02 request contract: the caller may override
// provider/model/key for a one-off test without persisting them.
type providerTestRequest struct {
	Provider string `json:"provider"`
	APIKey   string `json:"apiKey,omitempty"`
	Model    string `json:"model"`
	BaseURL  string `json:"baseUrl,omitempty"`
}

// providerTestResponse is the CH-02 response contract shared with the
// Settings screen (Task 3 consumes this).
type providerTestResponse struct {
	OK        bool   `json:"ok"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	LatencyMs int64  `json:"latencyMs"`
	ErrorCode string `json:"errorCode,omitempty"`
	Message   string `json:"message,omitempty"`
	MaskedKey string `json:"maskedKey"`
}

const providerTestPrompt = `Responda APENAS com o JSON {"ok":true} e nada mais.`

// runProviderTest issues one tiny, cheap call against the requested
// provider/model and translates the outcome into the CH-02 typed contract.
func (a *api) runProviderTest(ctx context.Context, req providerTestRequest) providerTestResponse {
	config, err := a.configStore.load()
	if err != nil {
		config = defaultConfig()
	}
	config.Form.Provider = strings.TrimSpace(req.Provider)
	if m := strings.TrimSpace(req.Model); m != "" {
		config.Form.Model = m
	}
	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		if saved, err := a.configStore.aiAPIKeyForProvider(config.Form.Provider); err == nil {
			apiKey = saved
		}
	}

	// BUG-03: without this fast path, testing a key-required provider with no
	// key still fired a real call with an empty credential, which providers
	// typically reject with a 400 (not 401) - classifyProviderError has no
	// case for that, so it fell through to a generic "network error" message
	// that gave the user no idea a key was even the problem.
	if apiKey == "" && providerRequiresAPIKey(config.Form.Provider) {
		return providerTestResponse{
			Provider:  config.Form.Provider,
			Model:     config.Form.Model,
			ErrorCode: "no_key",
			Message:   providerTestMessage("no_key"),
			MaskedKey: maskAPIKey(""),
		}
	}

	testCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	start := time.Now()
	_, callErr := a.scraper.generateJSON(testCtx, config, apiKey, providerTestPrompt)
	resp := providerTestResponse{
		Provider:  config.Form.Provider,
		Model:     config.Form.Model,
		LatencyMs: time.Since(start).Milliseconds(),
		MaskedKey: maskAPIKey(apiKey),
	}
	if callErr == nil {
		resp.OK = true
		return resp
	}
	resp.ErrorCode = classifyProviderError(callErr)
	resp.Message = providerTestMessage(resp.ErrorCode)
	a.logger.Printf("[ PROVIDER ] test %s/%s -> %s", resp.Provider, resp.Model, resp.ErrorCode)
	return resp
}

func providerTestMessage(code string) string {
	switch code {
	case "no_key":
		return "No API key configured for this provider. Add one above and save before testing."
	case "unauthorized":
		return "The provider rejected the key (401). Check the API key."
	case "forbidden":
		return "The provider refused access (403). Check key permissions/region."
	case "model_not_found":
		return "Model not found (404). Fetch models and pick a valid one."
	case "rate_limited":
		return "Rate limited / out of quota (429). Wait or switch provider."
	case "quota_exhausted":
		return "The daily AI quota is spent. It resets tomorrow — or switch provider in Settings."
	case "provider_unavailable":
		return "Provider unavailable (503). Try again in a few minutes."
	case "invalid_response":
		return "Provider answered with an unreadable response."
	case "local_model_unreachable":
		return "Local model server unreachable. Is Ollama running?"
	case "timeout":
		return "The provider took too long to answer. Try a faster model, or try again."
	default:
		return "Network error while contacting the provider."
	}
}

// providersTest is the HTTP handler for POST /api/v1/providers/test (CH-02).
// It never logs the raw API key or the full test prompt: only the masked
// key (via maskAPIKey) crosses into logs or the JSON response body.
func (a *api) providersTest(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var req providerTestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Requisição de teste inválida."})
		return
	}
	writeJSON(w, http.StatusOK, a.runProviderTest(r.Context(), req))
}
