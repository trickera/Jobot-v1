package server

import (
	"testing"
)

func TestMaskAPIKey(t *testing.T) {
	if got := maskAPIKey("AIzaSyABCDEF1234567890"); got != "AIza...7890" {
		t.Fatalf("unexpected mask: %q", got)
	}
	if maskAPIKey("") != "nao configurada" {
		t.Fatal("expected empty key label")
	}
}

// A short/typo'd key must never panic (key[len-4:] would go out of bounds for
// len < 4) and must not leak the whole value.
func TestMaskAPIKeyShortKeyDoesNotPanic(t *testing.T) {
	for _, key := range []string{"a", "ab", "abc", "abcd"} {
		got := maskAPIKey(key)
		if got != "****" {
			t.Fatalf("short key %q: expected masked ****, got %q", key, got)
		}
	}
	if got := maskAPIKey("abcdef"); got != "...cdef" {
		t.Fatalf("expected ...cdef for 6-char key, got %q", got)
	}
}

func TestGeminiModelForConfig(t *testing.T) {
	if got := geminiModelForConfig(appConfig{}); got != geminiFreeModel {
		t.Fatalf("expected %q, got %q", geminiFreeModel, got)
	}
	cfg := appConfig{Form: configForm{Model: "custom-model"}}
	if got := geminiModelForConfig(cfg); got != "custom-model" {
		t.Fatalf("expected custom model, got %q", got)
	}
}
