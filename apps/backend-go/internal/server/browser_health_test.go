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

func TestCamoufoxCacheDirUsesLocalAppData(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\fake\localappdata`)
	got := camoufoxCacheDir()
	want := filepath.Join(`C:\fake\localappdata`, "camoufox", "camoufox", "Cache")
	if got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestCamoufoxBrowserInstalledDetectsNonEmptyCache(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)

	if camoufoxBrowserInstalled() {
		t.Fatal("expected browserInstalled=false before the cache dir exists")
	}

	cacheDir := filepath.Join(dir, "camoufox", "camoufox", "Cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if camoufoxBrowserInstalled() {
		t.Fatal("expected browserInstalled=false for an empty cache dir")
	}

	if err := os.WriteFile(filepath.Join(cacheDir, "camoufox-1.0"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !camoufoxBrowserInstalled() {
		t.Fatal("expected browserInstalled=true once the cache dir has entries")
	}
}

func TestResolvePythonPathAbsoluteBundled(t *testing.T) {
	dir := t.TempDir()
	fakePython := filepath.Join(dir, "python.exe")
	if err := os.WriteFile(fakePython, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SENCIA_PYTHON", fakePython)

	path, found := resolvePythonPath()
	if !found {
		t.Fatal("expected the bundled/overridden absolute path to be found")
	}
	if path != fakePython {
		t.Fatalf("expected %s, got %s", fakePython, path)
	}
}

func TestResolvePythonPathAbsoluteMissingFile(t *testing.T) {
	t.Setenv("SENCIA_PYTHON", filepath.Join(t.TempDir(), "does-not-exist.exe"))
	if _, found := resolvePythonPath(); found {
		t.Fatal("expected found=false when the overridden path does not exist")
	}
}

func TestBrowserHealthMessagePriority(t *testing.T) {
	cases := []struct {
		name                                                 string
		pythonFound, workerFound, camoufoxOK, browserInstall bool
		wantSubstr                                           string
	}{
		{"no python", false, true, true, true, "Python"},
		{"no worker", true, false, true, true, "worker"},
		{"no camoufox", true, true, false, true, "Camoufox"},
		{"not installed", true, true, true, false, "instalado"},
		{"all good", true, true, true, true, "pronto"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := browserHealthMessage(tc.pythonFound, tc.workerFound, tc.camoufoxOK, tc.browserInstall)
			if !strings.Contains(got, tc.wantSubstr) {
				t.Fatalf("expected message to mention %q, got %q", tc.wantSubstr, got)
			}
		})
	}
}

// TestBrowserHealthHandlerEndToEnd exercises the real handler against
// whatever Python is available on the machine (bundled or dev py -3), and
// skips if none is found - this matches Wave 5's own guidance ("skip se sem
// python em CI") since a CI runner may not have Python installed at all.
func TestBrowserHealthHandlerEndToEnd(t *testing.T) {
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

	a := &api{logger: log.New(io.Discard, "", 0)}
	req := httptest.NewRequest("GET", "/api/v1/browser/health", nil)
	rec := httptest.NewRecorder()
	a.browserHealth(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}
