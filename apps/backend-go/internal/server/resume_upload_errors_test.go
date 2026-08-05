package server

import (
	"encoding/base64"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestSaveResumeReturnsTypedErrors(t *testing.T) {
	store := newTestStore(t)
	cases := []struct {
		name    string
		payload resumeUploadRequest
		want    error
	}{
		{"empty file name", resumeUploadRequest{FileName: "", ContentBase64: base64.StdEncoding.EncodeToString([]byte("x"))}, errInvalidFile},
		{"unsupported extension", resumeUploadRequest{FileName: "resume.exe", ContentBase64: base64.StdEncoding.EncodeToString([]byte("x"))}, errUnsupportedFormat},
		{"bad base64", resumeUploadRequest{FileName: "resume.txt", ContentBase64: "%%%not-base64%%%"}, errInvalidFile},
		{"empty content", resumeUploadRequest{FileName: "resume.txt", ContentBase64: ""}, errInvalidFile},
		{"too large", resumeUploadRequest{FileName: "resume.txt", ContentBase64: base64.StdEncoding.EncodeToString(make([]byte, maxResumeUploadBytes+1))}, errFileTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.saveResume(tc.payload)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want errors.Is(..., %v)", err, tc.want)
			}
			for _, r := range err.Error() {
				if r > 0x7f {
					t.Fatalf("non-ASCII rune in upload error %q (PT leaking?)", err.Error())
				}
			}
		})
	}
}

func TestSaveResumeStillAcceptsValidTxt(t *testing.T) {
	store := newTestStore(t)
	payload := resumeUploadRequest{
		FileName:      "resume.txt",
		ContentBase64: base64.StdEncoding.EncodeToString([]byte("Maya Chen\nDesigner with Figma experience.")),
	}
	result, err := store.saveResume(payload)
	if err != nil {
		t.Fatalf("saveResume: %v", err)
	}
	if !strings.Contains(result.ExtractedText, "Maya Chen") {
		t.Fatalf("expected extracted text, got %q", result.ExtractedText)
	}
	if result.ExtractedText != "Maya Chen\nDesigner with Figma experience." {
		t.Fatalf("expected resume line boundaries to be preserved, got %q", result.ExtractedText)
	}
}

func TestCleanResumeExtractedTextPreservesHeaderColumns(t *testing.T) {
	got := cleanResumeExtractedText("  RAFAEL MOREIRA    Belo Horizonte  \r\n\r\n  Profissional de Tecnologia  ")
	want := "RAFAEL MOREIRA    Belo Horizonte\nProfissional de Tecnologia"
	if got != want {
		t.Fatalf("expected line and column boundaries to survive, got %q", got)
	}
}

func TestSaveResumeReplacesStaleSearchProfile(t *testing.T) {
	store := newTestStore(t)
	config := defaultConfig()
	config.Form.Role = "Registered Nurse"
	config.Form.Roles = "Registered Nurse, ICU Nurse"
	config.Form.Seniority = "Senior"
	config.Form.Levels = "Senior"
	config.Form.SearchProfiles = "Registered Nurse, ICU Nurse | Senior | Director | 10"
	config.Form.Keywords = "patient, epic, icu"
	config.Form.KeywordsForRoles = "Registered Nurse"
	config.Form.WorkMode = workModeHybrid
	config.Form.Location = "Chicago"
	config.Form.OnsiteLocation = "Chicago"
	config.Form.RemoteCountry = "Brazil"
	config.Form.Blacklist = "Outlier"
	config.Toggles["useIndeed"] = false
	if err := store.save(config); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	text := `RAFAEL MOREIRA
Profissional de Tecnologia

EXPERIÊNCIA
Analista DevOps – ExampleMarket.com
2024-12 – 2026-04
Automatizei pipelines com AWS, Terraform, Kubernetes e GitHub Actions.
`
	result, err := store.saveResume(resumeUploadRequest{
		FileName:      "Rafael_Moreira-DevOps.txt",
		ContentBase64: base64.StdEncoding.EncodeToString([]byte(text)),
	})
	if err != nil {
		t.Fatalf("saveResume: %v", err)
	}
	if result.DetectedRole != "Analista DevOps" || result.DetectedSeniority != "" {
		t.Fatalf("unexpected detected profile: role=%q seniority=%q", result.DetectedRole, result.DetectedSeniority)
	}
	loaded, err := store.load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if loaded.Form.Role != "Analista DevOps" || loaded.Form.Roles != "Analista DevOps" || loaded.Form.SearchProfiles != "" {
		t.Fatalf("stale profile survived upload: %+v", loaded.Form)
	}
	if loaded.Form.KeywordsForRoles != "Analista DevOps" || !strings.Contains(loaded.Form.Keywords, "Terraform") || strings.Contains(strings.ToLower(loaded.Form.Keywords), "patient") {
		t.Fatalf("keywords were not replaced for the new role: keywords=%q roles=%q", loaded.Form.Keywords, loaded.Form.KeywordsForRoles)
	}
	if loaded.Form.WorkMode != workModeHybrid || loaded.Form.Location != "Chicago" || loaded.Form.OnsiteLocation != "Chicago" || loaded.Form.RemoteCountry != "Brazil" || loaded.Form.Blacklist != "Outlier" || loaded.Toggles["useIndeed"] {
		t.Fatalf("manual search preferences changed during resume upload: form=%+v toggles=%+v", loaded.Form, loaded.Toggles)
	}
	effective := effectiveSearchConfig(loaded)
	if len(effective.Roles) != 1 || effective.Roles[0] != "Analista DevOps" || effective.RolesSource != "role" {
		t.Fatalf("unexpected effective search after upload: %+v", effective)
	}
}

func TestSaveResumeClearsStaleRoleWhenNoRoleIsDetectable(t *testing.T) {
	store := newTestStore(t)
	config := defaultConfig()
	config.Form.Role = "Registered Nurse"
	config.Form.Roles = "Registered Nurse"
	config.Form.SearchProfiles = "Registered Nurse | Senior"
	if err := store.save(config); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	result, err := store.saveResume(resumeUploadRequest{
		FileName:      "resume.txt",
		ContentBase64: base64.StdEncoding.EncodeToString([]byte("SUMMARY\nTechnology professional.")),
	})
	if err != nil {
		t.Fatalf("saveResume: %v", err)
	}
	loaded, err := store.load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if loaded.Form.Role != "" || loaded.Form.Roles != "" || loaded.Form.SearchProfiles != "" {
		t.Fatalf("stale role survived an inconclusive resume: role=%q roles=%q profiles=%q", loaded.Form.Role, loaded.Form.Roles, loaded.Form.SearchProfiles)
	}
	if !slices.Contains(result.Warnings, "search_role_not_detected") {
		t.Fatalf("expected an honest detection warning, got %v", result.Warnings)
	}
}
