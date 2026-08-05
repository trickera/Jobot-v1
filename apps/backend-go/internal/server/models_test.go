package server

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// BUG-03 regression: fetching models for a key-required provider with no key
// configured must be classified as apiKeyRequiredError (distinct, actionable)
// rather than a generic upstream failure.
func TestFetchProviderModelsNoKeyReturnsAPIKeyRequiredError(t *testing.T) {
	store := newTestStore(t)
	a := &api{logger: log.New(io.Discard, "", 0), configStore: store}

	for _, provider := range []string{"gemini", "anthropic", "groq", "openai"} {
		t.Run(provider, func(t *testing.T) {
			_, err := a.fetchProviderModels(context.Background(), modelFetchRequest{Provider: provider})
			if err == nil {
				t.Fatalf("expected an error for %s with no key configured", provider)
			}
			if !isAPIKeyRequiredError(err) {
				t.Fatalf("expected an apiKeyRequiredError for %s, got: %v", provider, err)
			}
		})
	}
}

// POST /api/v1/models must answer a missing key with 409 + a stable "code"
// field so the frontend can render a precise message, not a 502 that reads
// like a real upstream/network failure.
func TestModelsHandlerReturnsConflictForMissingKey(t *testing.T) {
	store := newTestStore(t)
	a := &api{logger: log.New(io.Discard, "", 0), configStore: store}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/models", strings.NewReader(`{"provider":"gemini"}`))
	rec := httptest.NewRecorder()
	a.models(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"api_key_required"`) {
		t.Fatalf("expected code=api_key_required in body, got: %s", rec.Body.String())
	}
}

func TestIsAPIKeyRequiredErrorFalseForOtherErrors(t *testing.T) {
	if isAPIKeyRequiredError(errors.New("some other failure")) {
		t.Fatal("expected a plain error not to be classified as apiKeyRequiredError")
	}
}
