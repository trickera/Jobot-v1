package server

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestGeminiTaskRouterKeepsPreviewModelsOut(t *testing.T) {
	economy := geminiModelsForPurpose("resume_optimize", aiModeFreeEconomy, "gemini-3-flash-preview")
	if len(economy) != 2 || economy[0] != geminiPinnedFreeModel || economy[1] != geminiLiteAlias {
		t.Fatalf("unexpected economy route: %v", economy)
	}
	dynamicLite := geminiModelsForPurpose("job_score", aiModeFreeEconomy, "gemini-4.1-flash-lite")
	if dynamicLite[0] != "gemini-4.1-flash-lite" {
		t.Fatalf("an explicitly validated stable Flash-Lite should lead economy routing: %v", dynamicLite)
	}
	quality := geminiModelsForPurpose("resume_optimize", aiModeFreeQuality, geminiPinnedFreeModel)
	if len(quality) != 3 || quality[0] != geminiQualityAlias {
		t.Fatalf("unexpected quality route: %v", quality)
	}
	for _, route := range append(economy, quality...) {
		if isPreviewOrExperimentalModel(route) {
			t.Fatalf("preview model entered curated route: %q", route)
		}
	}
}

func TestUnavailableCatalogIsAttemptedOnlyOncePerSession(t *testing.T) {
	a := newCascadeTestAPI(t)
	catalogCalls := 0
	a.fetchGeminiCatalog = func(context.Context, string) ([]string, error) {
		catalogCalls++
		return nil, errors.New("temporary catalog outage")
	}
	config := cascadeTestConfig("Gemini", geminiPinnedFreeModel, "", "", "", "")
	generate := func(context.Context, appConfig, string, string) (string, error) {
		return `{"ok":true}`, nil
	}
	for _, prompt := range []string{"one", "two"} {
		if _, err := a.runLLMWithFallback(context.Background(), "resume_parse", config, prompt, generate); err != nil {
			t.Fatalf("curated fallback should survive catalog outage: %v", err)
		}
	}
	if catalogCalls != 1 {
		t.Fatalf("catalog outage should be cached for the session, got %d attempts", catalogCalls)
	}
}

func TestBestAvailableFlashLiteUsesTheNewestStableVersion(t *testing.T) {
	models := []string{"gemini-3.2-flash-lite-preview", "gemini-3.2-flash-lite", "gemini-4.1-flash-lite", "gemini-3.10-flash-lite"}
	if got := bestAvailableFlashLite(models); got != "gemini-4.1-flash-lite" {
		t.Fatalf("best stable Flash-Lite = %q", got)
	}
	models = []string{"gemini-3.2-flash-lite", "gemini-3.10-flash-lite"}
	if got := bestAvailableFlashLite(models); got != "gemini-3.10-flash-lite" {
		t.Fatalf("minor versions must compare numerically, got %q", got)
	}
}

func TestGeminiFirstUseMigratesAbsentPinOnceAndPersistsIt(t *testing.T) {
	a := newCascadeTestAPI(t)
	catalogCalls := 0
	a.fetchGeminiCatalog = func(context.Context, string) ([]string, error) {
		catalogCalls++
		return []string{geminiLiteAlias, geminiQualityAlias, "gemini-3-flash-preview"}, nil
	}
	config := cascadeTestConfig("Gemini", geminiPinnedFreeModel, "", "", "", "")

	seen := []string{}
	generate := func(_ context.Context, cfg appConfig, _, _ string) (string, error) {
		seen = append(seen, cfg.Form.Model)
		return `{"ok":true}`, nil
	}
	for _, prompt := range []string{"first prompt", "second prompt"} {
		if _, err := a.runLLMWithFallback(context.Background(), "resume_parse", config, prompt, generate); err != nil {
			t.Fatalf("first-use route failed: %v", err)
		}
	}
	if catalogCalls != 1 {
		t.Fatalf("ListModels must be cached after first use, got %d calls", catalogCalls)
	}
	if len(seen) != 2 || seen[0] != geminiLiteAlias || seen[1] != geminiLiteAlias {
		t.Fatalf("absent pin was sent or migration was unstable: %v", seen)
	}

	saved, err := a.configStore.load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Form.Model != geminiLiteAlias || saved.ModelValidation == nil || saved.ModelValidation.Status != "migrated" {
		t.Fatalf("migration was not visible in persisted config: model=%q status=%+v", saved.Form.Model, saved.ModelValidation)
	}
	if !strings.Contains(saved.ModelValidation.Message, geminiPinnedFreeModel) {
		t.Fatalf("migration message did not name the missing pin: %q", saved.ModelValidation.Message)
	}
}

func TestOperationBudgetsAreModeAndPurposeAware(t *testing.T) {
	if got := llmOperationRequestBudget(aiModeFreeEconomy, "resume_optimize"); got != 6 {
		t.Fatalf("economy document budget = %d, want 6", got)
	}
	if got := llmOperationRequestBudget(aiModeFreeQuality, "resume_optimize"); got != 8 {
		t.Fatalf("quality document budget = %d, want 8", got)
	}
	if got := llmOperationRequestBudget(aiModeFreeQuality, "job_score"); got != 6 {
		t.Fatalf("job scoring budget = %d, want 6", got)
	}
}
