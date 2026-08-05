package server

import (
	"context"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallDataWritableCreatesAndCleansMarker(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "dir")
	if !installDataWritable(dir) {
		t.Fatal("expected a fresh temp dir to be writable")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected the write-test marker to be cleaned up, found %+v", entries)
	}
}

// skipWithoutRunnablePython mirrors TestBrowserHealthHandlerEndToEnd's guard
// (browser_health_test.go): these end-to-end tests exercise the real
// camoufoxImportable() check, so they need a real Python (bundled or dev
// `py -3`/`python3`) and must skip gracefully where none is available (e.g.
// a CI runner with no Python at all).
func skipWithoutRunnablePython(t *testing.T) {
	t.Helper()
	name, args := pythonCommand()
	if filepath.IsAbs(name) {
		if !fileExists(name) {
			t.Skip("no python available in this environment")
		}
	} else if _, err := exec.LookPath(name); err != nil {
		t.Skip("no python available in this environment")
	}
	if err := exec.CommandContext(context.Background(), name, append(append([]string{}, args...), "--version")...).Run(); err != nil {
		t.Skip("python found but not runnable in this environment")
	}
}

func TestInstallHealthHandlerEndToEnd(t *testing.T) {
	skipWithoutRunnablePython(t)
	t.Setenv("SENCIA_DB_PATH", filepath.Join(t.TempDir(), "sencia.db"))
	t.Setenv("SENCIA_CAMOUFOX_CACHE", t.TempDir())

	a := &api{
		logger:      log.New(io.Discard, "", 0),
		logs:        &logBuffer{},
		configStore: newConfigStore(),
	}

	req := httptest.NewRequest("GET", "/api/v1/health/install", nil)
	rec := httptest.NewRecorder()
	a.installHealth(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, id := range []string{"backend", "sqlite", "python", "worker", "camoufoxImport", "camoufoxBrowser", "appDataWrite", "cacheWrite", "internet"} {
		if !strings.Contains(body, `"id":"`+id+`"`) {
			t.Fatalf("expected check %q in response, got %s", id, body)
		}
	}
	if !strings.Contains(body, `"appDataWrite":true`) && !strings.Contains(body, `"id":"appDataWrite","ok":true`) {
		t.Fatalf("expected appDataWrite=true for a fresh writable temp dir, got %s", body)
	}
}

func TestInstallRepairHandlerEndToEnd(t *testing.T) {
	skipWithoutRunnablePython(t)
	t.Setenv("SENCIA_DB_PATH", filepath.Join(t.TempDir(), "sencia.db"))
	cacheRoot := t.TempDir()
	t.Setenv("SENCIA_CAMOUFOX_CACHE", cacheRoot)
	t.Setenv("SENCIA_CAMOUFOX_BUNDLE", t.TempDir()) // no camoufox.exe -> bundle unavailable

	// Pre-seed a fake already-installed browser so camoufoxBrowserInstalled()
	// is true and installRepair's no-bundle-available branch (which would
	// otherwise kick off a real multi-hundred-MB background download) never
	// triggers - this test only exercises the directory/permission repair
	// logic, not a real network fetch.
	installDir := camoufoxInstallDir()
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "camoufox.exe"), []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}

	a := &api{
		logger:      log.New(io.Discard, "", 0),
		logs:        &logBuffer{},
		configStore: newConfigStore(),
	}

	req := httptest.NewRequest("POST", "/api/v1/health/repair", nil)
	rec := httptest.NewRecorder()
	a.installRepair(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if a.bootstrap.snapshot().Running {
		t.Fatal("expected no background bootstrap download when the browser is already installed")
	}
}
