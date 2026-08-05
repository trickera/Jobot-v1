package server

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPromptPIIRedactionIsReversibleAndPreservesDates(t *testing.T) {
	prompt := `=== CURRICULO ===
Jane Doe
jane.doe@example.com | +1 (312) 555-0188 | https://example.com/jane
Engineer, 2020 - 2024. Improved conversion by 25%.`

	redacted := redactPromptPII("resume_parse", prompt)
	for _, secret := range []string{"Jane Doe", "jane.doe@example.com", "+1 (312) 555-0188", "https://example.com/jane"} {
		if strings.Contains(redacted.prompt, secret) {
			t.Fatalf("direct identifier remained in provider prompt: %q", secret)
		}
	}
	for _, safe := range []string{"2020 - 2024", "25%"} {
		if !strings.Contains(redacted.prompt, safe) {
			t.Fatalf("non-PII evidence was redacted: %q in %q", safe, redacted.prompt)
		}
	}

	restored := redacted.restore(`{"name":"__SENCIA_PII_1__","email":"__SENCIA_PII_2__","url":"__SENCIA_PII_3__","phone":"__SENCIA_PII_4__"}`)
	for _, original := range []string{"Jane Doe", "jane.doe@example.com", "https://example.com/jane", "+1 (312) 555-0188"} {
		if !strings.Contains(restored, original) {
			t.Fatalf("local restore lost %q: %s", original, restored)
		}
	}
}

func TestRemoteResumeAICallRequiresConsentBeforeProviderUse(t *testing.T) {
	a := newCascadeTestAPI(t)
	config := cascadeTestConfig("Gemini", geminiPinnedFreeModel, "", "", "", "")
	config.Form.AIDataConsent = false
	calls := 0
	_, err := a.runLLMWithFallback(context.Background(), "resume_parse", config, "Jane Doe resume", func(context.Context, appConfig, string, string) (string, error) {
		calls++
		return `{"ok":true}`, nil
	})
	if !errors.Is(err, errAIDataConsentRequired) || calls != 0 {
		t.Fatalf("expected consent error before provider use, err=%v calls=%d", err, calls)
	}
	if got := classifyProviderError(err); got != "consent_required" {
		t.Fatalf("search diagnostics need a stable consent code, got %q", got)
	}
}

func TestLocalProviderDoesNotRequireCloudConsent(t *testing.T) {
	a := newCascadeTestAPI(t)
	config := cascadeTestConfig("Ollama local", "llama3.1", "", "", "", "")
	config.Form.AIDataConsent = false
	result, err := a.runLLMWithFallback(context.Background(), "resume_parse", config, "Jane Doe resume", func(context.Context, appConfig, string, string) (string, error) {
		return `{"ok":true}`, nil
	})
	if err != nil || result.ProviderUsed != "ollama local" {
		t.Fatalf("local-only call should work without cloud consent: result=%+v err=%v", result, err)
	}
}

func TestRedactedCacheRestoresOnlyTheCurrentResumeIdentifiers(t *testing.T) {
	a := newCascadeTestAPI(t)
	a.llmCache = newLLMCache(nil)
	config := cascadeTestConfig("Gemini", geminiPinnedFreeModel, "", "", "", "")
	calls := 0
	generate := func(context.Context, appConfig, string, string) (string, error) {
		calls++
		return `{"name":"__SENCIA_PII_1__","email":"__SENCIA_PII_2__"}`, nil
	}
	prompt := func(name, email string) string {
		return "=== CURRICULO ===\n" + name + "\n" + email + "\nEngineer"
	}
	first, err := a.runLLMWithFallback(context.Background(), "resume_parse", config, prompt("Jane Doe", "jane@example.com"), generate)
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.runLLMWithFallback(context.Background(), "resume_parse", config, prompt("Ana Lima", "ana@example.com"), generate)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("equivalent redacted prompts should share one provider result, got %d calls", calls)
	}
	if !strings.Contains(first.Raw, "Jane Doe") || !strings.Contains(first.Raw, "jane@example.com") {
		t.Fatalf("first response was not restored locally: %s", first.Raw)
	}
	if !strings.Contains(second.Raw, "Ana Lima") || !strings.Contains(second.Raw, "ana@example.com") || strings.Contains(second.Raw, "Jane Doe") {
		t.Fatalf("cache leaked the earlier resume identifiers: %s", second.Raw)
	}
}
