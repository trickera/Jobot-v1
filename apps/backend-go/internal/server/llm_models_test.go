package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeConfigMigratesRetiredModelsInEverySlot(t *testing.T) {
	config := defaultConfig()
	config.Form.Provider = "Gemini"
	config.Form.Model = "gemini-2.5-flash"
	config.Form.Fallback1Provider = "Google Gemini"
	config.Form.Fallback1Model = "gemini-2.5-flash-lite"
	config.Form.Fallback2Provider = "Gemini"
	config.Form.Fallback2Model = "gemini-2.5-flash"

	got := normalizeConfig(config)
	for slot, model := range map[string]string{
		"primary":    got.Form.Model,
		"fallback 1": got.Form.Fallback1Model,
		"fallback 2": got.Form.Fallback2Model,
	} {
		if model != geminiFreeModel {
			t.Errorf("%s model = %q, want %q", slot, model, geminiFreeModel)
		}
	}
	if len(got.Notices) != 3 {
		t.Fatalf("expected one visible notice per migrated slot, got %v", got.Notices)
	}
}

func TestNormalizeConfigPreservesCompatibleModelNotKnownToBeRetired(t *testing.T) {
	config := defaultConfig()
	config.Form.Model = "gemini-user-selected-stable"

	got := normalizeConfig(config)
	if got.Form.Model != config.Form.Model {
		t.Fatalf("a compatible choice without retirement evidence was overwritten: %q", got.Form.Model)
	}
	if len(got.Notices) != 0 {
		t.Fatalf("expected no migration notice, got %v", got.Notices)
	}
}

func TestConfigStorePersistsRetiredModelMigrationWithoutPersistingNotice(t *testing.T) {
	store := newTestStore(t)
	db, err := store.open()
	if err != nil {
		t.Fatal(err)
	}
	config := defaultConfig()
	config.Form.Model = "gemini-2.5-flash"
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		configSettingKey,
		string(raw),
	); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	loaded, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Form.Model != geminiFreeModel || len(loaded.Notices) != 1 {
		t.Fatalf("expected migrated response with a visible notice, got model=%q notices=%v", loaded.Form.Model, loaded.Notices)
	}

	reloaded, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Form.Model != geminiFreeModel || len(reloaded.Notices) != 0 {
		t.Fatalf("expected durable migration and no stale persisted notice, got model=%q notices=%v", reloaded.Form.Model, reloaded.Notices)
	}
}

func TestRankGeminiModelsPutsCuratedFreeTierChoicesFirst(t *testing.T) {
	models := []string{"gemini-z-stable", "gemini-flash-latest", "gemini-3-flash-preview", geminiFreeModel}
	got := rankGeminiModels(models)
	want := []string{geminiPinnedFreeModel, geminiQualityAlias}
	wantPrefix := strings.Join(want, ",")
	if strings.Join(got[:len(want)], ",") != wantPrefix {
		t.Fatalf("ranked models = %v, want present curated prefix %v", got, want)
	}
}
