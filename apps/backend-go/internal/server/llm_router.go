package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	aiModeFreeEconomy = "free_economy"
	aiModeFreeQuality = "free_quality"
)

type modelValidationStatus struct {
	Status      string `json:"status"`
	Requested   string `json:"requested"`
	Active      string `json:"active"`
	Message     string `json:"message"`
	ValidatedAt string `json:"validatedAt"`
}

var (
	errNoCuratedGeminiModel    = errors.New("no curated Gemini Flash-Lite model is available")
	errLLMOperationBudgetSpent = errors.New("AI operation request budget spent")
)

func normalizeAIMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case aiModeFreeQuality:
		return aiModeFreeQuality
	default:
		return aiModeFreeEconomy
	}
}

func isPreviewOrExperimentalModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(model, "preview") || strings.Contains(model, "experimental") ||
		strings.Contains(model, "-exp-") || strings.HasSuffix(model, "-exp")
}

func containsModel(models []string, target string) bool {
	for _, model := range models {
		if strings.EqualFold(strings.TrimSpace(model), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}

func bestAvailableFlashLite(models []string) string {
	for _, preferred := range []string{geminiPinnedFreeModel, geminiLiteAlias} {
		if containsModel(models, preferred) {
			return preferred
		}
	}
	candidates := make([]string, 0, len(models))
	for _, model := range models {
		if strings.Contains(strings.ToLower(model), "flash-lite") && !isPreviewOrExperimentalModel(model) {
			candidates = append(candidates, model)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		leftMajor, leftMinor := geminiVersion(candidates[i])
		rightMajor, rightMinor := geminiVersion(candidates[j])
		if leftMajor != rightMajor {
			return leftMajor > rightMajor
		}
		if leftMinor != rightMinor {
			return leftMinor > rightMinor
		}
		return strings.ToLower(candidates[i]) > strings.ToLower(candidates[j])
	})
	if len(candidates) > 0 {
		return candidates[0]
	}
	return ""
}

var geminiVersionPattern = regexp.MustCompile(`(?i)gemini-(\d+)(?:\.(\d+))?`)

func geminiVersion(model string) (int, int) {
	match := geminiVersionPattern.FindStringSubmatch(model)
	if len(match) == 0 {
		return 0, 0
	}
	major, _ := strconv.Atoi(match[1])
	minor := 0
	if len(match) > 2 {
		minor, _ = strconv.Atoi(match[2])
	}
	return major, minor
}

func highValueAIPurpose(purpose llmPurpose) bool {
	switch purpose {
	case "resume_parse", "resume_optimize", "cover_letter":
		return true
	default:
		return false
	}
}

// geminiModelsForPurpose is the task router. Economy keeps Flash-Lite first for
// every task. Quality spends the scarcer Flash allowance only on the three
// operations where a richer answer materially changes the user's document; job
// scoring and small extraction steps remain on Lite in both modes.
func geminiModelsForPurpose(purpose llmPurpose, mode, configured string) []string {
	mode = normalizeAIMode(mode)
	order := []string{geminiPinnedFreeModel, geminiLiteAlias}
	if mode == aiModeFreeQuality && highValueAIPurpose(purpose) {
		order = []string{geminiQualityAlias, geminiPinnedFreeModel, geminiLiteAlias}
	} else if stableFlashLiteModel(configured) {
		order = append([]string{configured}, order...)
	}

	// A stable user-selected model returned by ListModels remains an explicit
	// choice. The curated list is the automatic route and fallback, not a reason
	// to erase a compatible selection without retirement evidence.
	configured = strings.TrimSpace(configured)
	if configured != "" && !containsModel(geminiFreeTierAllowlist, configured) && !isPreviewOrExperimentalModel(configured) {
		order = append(order, configured)
	}

	out := make([]string, 0, len(order))
	seen := map[string]bool{}
	for _, model := range order {
		key := strings.ToLower(strings.TrimSpace(model))
		if key == "" || seen[key] || isPreviewOrExperimentalModel(model) {
			continue
		}
		seen[key] = true
		out = append(out, model)
	}
	return out
}

func stableFlashLiteModel(model string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(model)), "flash-lite") && !isPreviewOrExperimentalModel(model)
}

func llmOperationRequestBudget(mode string, purpose llmPurpose) int {
	if normalizeAIMode(mode) == aiModeFreeQuality && highValueAIPurpose(purpose) {
		return 8
	}
	return 6
}

func operationLimited(generate jsonGenerator, mode string, purpose llmPurpose) jsonGenerator {
	remaining := llmOperationRequestBudget(mode, purpose)
	return func(ctx context.Context, config appConfig, apiKey, prompt string) (string, error) {
		if remaining <= 0 {
			return "", fmt.Errorf("%w: %s permits %d provider calls for %s",
				errLLMOperationBudgetSpent, normalizeAIMode(mode), llmOperationRequestBudget(mode, purpose), purpose)
		}
		remaining--
		return generate(ctx, config, apiKey, prompt)
	}
}

func geminiCatalogKey(apiKey string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(apiKey)))
	return hex.EncodeToString(sum[:])
}

func (a *api) loadGeminiCatalog(ctx context.Context, apiKey string) ([]string, error) {
	key := geminiCatalogKey(apiKey)
	a.modelCatalogMu.Lock()
	if cached, ok := a.geminiCatalog[key]; ok {
		models := append([]string(nil), cached...)
		a.modelCatalogMu.Unlock()
		return models, nil
	}
	if cachedErr, ok := a.geminiCatalogError[key]; ok {
		a.modelCatalogMu.Unlock()
		return nil, cachedErr
	}
	a.modelCatalogMu.Unlock()

	validationCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var (
		models []string
		err    error
	)
	if a.fetchGeminiCatalog != nil {
		models, err = a.fetchGeminiCatalog(validationCtx, apiKey)
	} else {
		models, err = a.fetchGeminiModels(validationCtx, apiKey)
	}
	if err != nil {
		a.modelCatalogMu.Lock()
		if a.geminiCatalogError == nil {
			a.geminiCatalogError = map[string]error{}
		}
		a.geminiCatalogError[key] = err
		a.modelCatalogMu.Unlock()
		return nil, err
	}

	a.modelCatalogMu.Lock()
	if a.geminiCatalog == nil {
		a.geminiCatalog = map[string][]string{}
	}
	a.geminiCatalog[key] = append([]string(nil), models...)
	delete(a.geminiCatalogError, key)
	a.modelCatalogMu.Unlock()
	return models, nil
}

// validateGeminiModelOnFirstUse resolves the pinned default before a generation
// request is spent. ListModels is cached only in memory and keyed by a one-way
// fingerprint; neither the key nor the catalog is persisted or logged.
func (a *api) validateGeminiModelOnFirstUse(ctx context.Context, config appConfig) (appConfig, error) {
	if !isGeminiProvider(config.Form.Provider) {
		return config, nil
	}
	if a == nil || a.configStore == nil {
		return config, nil
	}
	if status := config.ModelValidation; status != nil &&
		(status.Status == "validated" || status.Status == "migrated") &&
		strings.EqualFold(strings.TrimSpace(status.Active), strings.TrimSpace(config.Form.Model)) {
		return config, nil
	}
	apiKey, err := a.configStore.aiAPIKeyForProvider(config.Form.Provider)
	if err != nil || strings.TrimSpace(apiKey) == "" {
		return config, err
	}

	requested := strings.TrimSpace(config.Form.Model)
	models, err := a.loadGeminiCatalog(ctx, apiKey)
	if err != nil {
		status := modelValidationStatus{
			Status: "unavailable", Requested: requested, Active: requested,
			Message:     "Não foi possível validar o catálogo agora; o fallback curado continuará protegendo esta chamada.",
			ValidatedAt: time.Now().UTC().Format(time.RFC3339),
		}
		config.ModelValidation = &status
		_ = a.configStore.updateModelValidation(config.Form.Provider, requested, requested, status)
		if a.logs != nil {
			a.logs.add("warning", "[ LLM ] ListModels indisponível no primeiro uso; mantendo o modelo configurado e a allowlist curada.")
		}
		return config, nil
	}
	config.availableGeminiModels = append([]string(nil), models...)

	resolved := requested
	if !containsModel(models, requested) || isPreviewOrExperimentalModel(requested) {
		resolved = bestAvailableFlashLite(models)
	}
	if resolved == "" {
		status := modelValidationStatus{
			Status: "failed", Requested: requested, Active: "",
			Message:     "Nenhum Flash-Lite estável da allowlist foi anunciado por ListModels.",
			ValidatedAt: time.Now().UTC().Format(time.RFC3339),
		}
		config.ModelValidation = &status
		_ = a.configStore.updateModelValidation(config.Form.Provider, requested, requested, status)
		return config, errNoCuratedGeminiModel
	}

	status := modelValidationStatus{
		Status: "validated", Requested: requested, Active: resolved,
		Message:     "Modelo validado contra ListModels no primeiro uso.",
		ValidatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if !strings.EqualFold(requested, resolved) {
		status.Status = "migrated"
		status.Message = fmt.Sprintf("%s não apareceu em ListModels; migrado para %s.", requested, resolved)
		if a.logs != nil {
			a.logs.add("warning", "[ LLM ] "+status.Message)
		}
	}
	config.Form.Model = resolved
	config.ModelValidation = &status
	_ = a.configStore.updateModelValidation(config.Form.Provider, requested, resolved, status)
	return config, nil
}
