package server

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPromptJobDescriptionCapsLongPostings(t *testing.T) {
	// A posting whose requirements are followed by pages of benefits boilerplate.
	description := strings.Repeat("requisito ", 100) + strings.Repeat("benefício café gratuito. ", 2000)

	got := promptJobDescription(description)

	if len(got) > maxJobDescriptionPromptChars {
		t.Fatalf("expected at most %d chars, got %d", maxJobDescriptionPromptChars, len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatal("truncation split a multibyte character")
	}
	if !strings.Contains(got, "requisito") {
		t.Fatal("truncation dropped the top of the posting, where the requirements live")
	}
}

func TestPromptJobDescriptionLeavesShortPostingsIntact(t *testing.T) {
	description := "Vaga de Go backend. Requisitos: Go, PostgreSQL."

	if got := promptJobDescription(description); got != description {
		t.Fatalf("expected a short posting to pass through unchanged, got %q", got)
	}
}

// The tailoring call's cost is dominated by what it emits, so the output
// budget has to actually reach the model.
func TestTailorPromptCarriesItsOutputBudget(t *testing.T) {
	prompt := tailorResumePrompt(`{"basics":{}}`, `{"jobTitle":"Dev"}`, `[]`, "English", "first person")

	if !strings.Contains(prompt, "no máximo 25 operações") {
		t.Fatalf("expected the op budget in the prompt, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "no máximo 10 palavras") {
		t.Fatalf("expected the reason word cap in the prompt, got:\n%s", prompt)
	}
}
