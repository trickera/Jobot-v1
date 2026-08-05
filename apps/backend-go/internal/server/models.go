package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// apiKeyRequiredError marks a "no key configured" failure so callers (the
// HTTP handlers) can return a distinct, actionable status/code instead of a
// generic upstream-failure response (BUG-03: no-key feedback was previously
// indistinguishable from a real provider/network error).
type apiKeyRequiredError struct{ message string }

func (e *apiKeyRequiredError) Error() string { return e.message }

func newAPIKeyRequiredError(message string) error { return &apiKeyRequiredError{message: message} }

func isAPIKeyRequiredError(err error) bool {
	var target *apiKeyRequiredError
	return errors.As(err, &target)
}

// providerRequiresAPIKey reports whether a provider needs a key to be usable
// at all. OpenRouter's model list is public and Ollama is a local server, so
// neither should be blocked on a missing key the way Gemini/Anthropic/
// Groq/OpenAI must be.
func providerRequiresAPIKey(provider string) bool {
	p := strings.ToLower(strings.TrimSpace(provider))
	switch {
	case strings.Contains(p, "gemini"), strings.Contains(p, "google"):
		return true
	case strings.Contains(p, "anthropic"):
		return true
	case strings.Contains(p, "groq"):
		return true
	case strings.Contains(p, "openai"):
		return true
	default:
		return false
	}
}

func (a *api) fetchProviderModels(ctx context.Context, payload modelFetchRequest) ([]string, error) {
	providerName := strings.TrimSpace(payload.Provider)
	provider := strings.ToLower(providerName)
	apiKey := strings.TrimSpace(payload.APIKey)
	if apiKey == "" {
		saved, err := a.configStore.aiAPIKeyForProvider(providerName)
		if err != nil {
			return nil, err
		}
		apiKey = strings.TrimSpace(saved)
	}

	switch {
	case strings.Contains(provider, "gemini") || strings.Contains(provider, "google"):
		if apiKey == "" {
			return nil, newAPIKeyRequiredError("Informe a chave Gemini antes de buscar modelos.")
		}
		return a.fetchGeminiModels(ctx, apiKey)
	case strings.Contains(provider, "anthropic"):
		if apiKey == "" {
			return nil, newAPIKeyRequiredError("Informe a chave Anthropic antes de buscar modelos.")
		}
		return a.fetchAnthropicModels(ctx, apiKey)
	case strings.Contains(provider, "openrouter"):
		return a.fetchOpenRouterModels(ctx, apiKey)
	case strings.Contains(provider, "groq"):
		if apiKey == "" {
			return nil, newAPIKeyRequiredError("Informe a chave Groq antes de buscar modelos.")
		}
		return a.fetchOpenAIModelList(ctx, "https://api.groq.com/openai/v1/models", apiKey)
	case strings.Contains(provider, "openai"):
		if apiKey == "" {
			return nil, newAPIKeyRequiredError("Informe a chave OpenAI antes de buscar modelos.")
		}
		return a.fetchOpenAIModelList(ctx, "https://api.openai.com/v1/models", apiKey)
	case strings.Contains(provider, "ollama"):
		return a.fetchOllamaModels(ctx)
	default:
		return nil, fmt.Errorf("Provedor %q não suportado.", payload.Provider)
	}
}

func (a *api) fetchGeminiModels(ctx context.Context, apiKey string) ([]string, error) {
	endpoint := "https://generativelanguage.googleapis.com/v1beta/models"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("x-goog-api-key", apiKey)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("Gemini retornou HTTP %d: %s", response.StatusCode, providerErrorMessage(response.Body))
	}

	var data struct {
		Models []struct {
			Name                       string   `json:"name"`
			SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	if err := json.NewDecoder(response.Body).Decode(&data); err != nil {
		return nil, err
	}
	var models []string
	for _, model := range data.Models {
		if !supportsMethod(model.SupportedGenerationMethods, "generateContent") {
			continue
		}
		models = append(models, strings.TrimPrefix(model.Name, "models/"))
	}
	return rankGeminiModels(sortedUnique(models)), nil
}

func (a *api) fetchAnthropicModels(ctx context.Context, apiKey string) ([]string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.anthropic.com/v1/models", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("x-api-key", apiKey)
	request.Header.Set("anthropic-version", "2023-06-01")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("Anthropic retornou HTTP %d: %s", response.StatusCode, providerErrorMessage(response.Body))
	}
	var data struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&data); err != nil {
		return nil, err
	}
	var models []string
	for _, model := range data.Data {
		models = append(models, model.ID)
	}
	return sortedUnique(models), nil
}

func (a *api) fetchOpenRouterModels(ctx context.Context, apiKey string) ([]string, error) {
	var data struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := a.getJSON(ctx, "https://openrouter.ai/api/v1/models", "Bearer "+apiKey, "", &data); err != nil {
		return nil, err
	}
	var models []string
	for _, model := range data.Data {
		models = append(models, model.ID)
	}
	return sortedUnique(models), nil
}

func (a *api) fetchOpenAIModelList(ctx context.Context, endpoint string, apiKey string) ([]string, error) {
	var data struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := a.getJSON(ctx, endpoint, "Bearer "+apiKey, "", &data); err != nil {
		return nil, err
	}
	var models []string
	for _, model := range data.Data {
		models = append(models, model.ID)
	}
	return sortedUnique(models), nil
}

func (a *api) fetchOllamaModels(ctx context.Context) ([]string, error) {
	var data struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := a.getJSON(ctx, "http://127.0.0.1:11434/api/tags", "", "", &data); err != nil {
		return nil, err
	}
	var models []string
	for _, model := range data.Models {
		models = append(models, model.Name)
	}
	return sortedUnique(models), nil
}

func (a *api) getJSON(ctx context.Context, endpoint string, authorization string, apiKeyHeader string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	if authorization != "" && authorization != "Bearer " {
		request.Header.Set("Authorization", authorization)
	}
	if apiKeyHeader != "" {
		request.Header.Set("x-api-key", apiKeyHeader)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Servidor de modelos retornou HTTP %d: %s", response.StatusCode, providerErrorMessage(response.Body))
	}
	return json.NewDecoder(response.Body).Decode(target)
}

func providerErrorMessage(body io.Reader) string {
	raw, err := io.ReadAll(io.LimitReader(body, 16<<10))
	if err != nil || len(raw) == 0 {
		return "sem detalhes"
	}
	var payload struct {
		Error any `json:"error"`
	}
	if err := json.Unmarshal(raw, &payload); err == nil && payload.Error != nil {
		switch value := payload.Error.(type) {
		case string:
			return value
		case map[string]any:
			if message, ok := value["message"].(string); ok && strings.TrimSpace(message) != "" {
				return message
			}
		}
	}
	return strings.TrimSpace(string(raw))
}

func supportsMethod(methods []string, target string) bool {
	for _, method := range methods {
		if strings.EqualFold(method, target) {
			return true
		}
	}
	return len(methods) == 0
}

func sortedUnique(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
