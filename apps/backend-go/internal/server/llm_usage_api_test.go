package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAIUsageReportsDailyBudgetCacheAndBreakdown(t *testing.T) {
	store := newTestStore(t)
	config := defaultConfig()
	config.Form.AIMode = aiModeFreeQuality
	config.Form.AIDataConsent = true
	config.Form.LLMRequestsPerDay = 10
	if err := store.save(config); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	store.recordLLMRequest(now)
	store.recordLLMRequest(now)
	store.recordLLMUsage(now, "resume_parse", "Gemini", geminiPinnedFreeModel, false)
	store.recordLLMUsage(now, "resume_parse", "Gemini", geminiPinnedFreeModel, true)

	a := &api{logger: log.New(io.Discard, "", 0), configStore: store}
	rec := httptest.NewRecorder()
	a.aiUsage(rec, httptest.NewRequest("GET", "/api/v1/ai/usage", nil))
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var usage aiUsageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &usage); err != nil {
		t.Fatal(err)
	}
	if usage.Requests != 2 || usage.Budget != 10 || usage.Remaining != 8 || usage.CacheHits != 1 {
		t.Fatalf("unexpected usage summary: %+v", usage)
	}
	if usage.Mode != aiModeFreeQuality || !usage.Consent || len(usage.Breakdown) != 1 {
		t.Fatalf("missing mode, consent or breakdown: %+v", usage)
	}
}

func TestConfigSavePreservesServerValidationEvidenceAndClearsItOnRouteChange(t *testing.T) {
	store := newTestStore(t)
	config := defaultConfig()
	original := &modelValidationStatus{
		Status: "validated", Requested: config.Form.Model, Active: config.Form.Model,
		Message: "server evidence", ValidatedAt: "2026-07-13T00:00:00Z",
	}
	config.ModelValidation = original
	if err := store.save(config); err != nil {
		t.Fatal(err)
	}
	a := &api{logger: log.New(io.Discard, "", 0), configStore: store}

	client := config
	client.ModelValidation = &modelValidationStatus{Status: "migrated", Message: "forged by client"}
	body, _ := json.Marshal(client)
	rec := httptest.NewRecorder()
	a.putConfig(rec, httptest.NewRequest("PUT", "/api/v1/config", bytes.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("same-route save failed: %d %s", rec.Code, rec.Body.String())
	}
	saved, err := store.load()
	if err != nil || saved.ModelValidation == nil || saved.ModelValidation.Message != original.Message {
		t.Fatalf("client overwrote validation evidence: config=%+v err=%v", saved.ModelValidation, err)
	}

	client = saved
	client.Form.Model = "gemini-user-selected-stable"
	client.ModelValidation = &modelValidationStatus{Status: "validated", Message: "forged again"}
	body, _ = json.Marshal(client)
	rec = httptest.NewRecorder()
	a.putConfig(rec, httptest.NewRequest("PUT", "/api/v1/config", bytes.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("route-changing save failed: %d %s", rec.Code, rec.Body.String())
	}
	saved, err = store.load()
	if err != nil || saved.ModelValidation != nil {
		t.Fatalf("changed model must require fresh first-use validation: config=%+v err=%v", saved.ModelValidation, err)
	}
}
