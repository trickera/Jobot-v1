package server

import (
	"context"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// findBundledPythonForTest locates the real bundled Python built by
// scripts/release/pack-python.ps1 (Wave 5 Task 1), if present. It's gitignored and
// machine-local, so tests using it must skip gracefully when it's absent
// (e.g. a fresh checkout that hasn't run pack-python.ps1 yet, or CI).
func findBundledPythonForTest(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Skip("cannot resolve working directory")
	}
	candidate := filepath.Join(wd, "..", "..", "..", "resources", "python", "python.exe")
	if !fileExists(candidate) {
		t.Skip("bundled python not present (run scripts/release/pack-python.ps1 first) - skipping")
	}
	abs, err := filepath.Abs(candidate)
	if err != nil {
		t.Skip("cannot resolve bundled python path")
	}
	return abs
}

func TestRunStreamedCommandHappyPath(t *testing.T) {
	python := findBundledPythonForTest(t)

	var mu sync.Mutex
	var lines []string
	err := runStreamedCommand(context.Background(), python, []string{"-c", "print('line one'); print('line two')"}, nil, func(line string) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, line)
	})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if len(lines) != 2 || lines[0] != "line one" || lines[1] != "line two" {
		t.Fatalf("expected both lines streamed in order, got %+v", lines)
	}
}

func TestRunStreamedCommandNonZeroExit(t *testing.T) {
	python := findBundledPythonForTest(t)

	err := runStreamedCommand(context.Background(), python, []string{"-c", "import sys; sys.exit(1)"}, nil, nil)
	if err == nil {
		t.Fatal("expected an error for a non-zero exit code")
	}
}

func TestBrowserBootstrapStateTryStartRefusesConcurrent(t *testing.T) {
	var s browserBootstrapState
	if !s.tryStart() {
		t.Fatal("expected the first tryStart to succeed")
	}
	if s.tryStart() {
		t.Fatal("expected a second tryStart to be refused while running")
	}
	s.finish(true, "done")
	if !s.tryStart() {
		t.Fatal("expected tryStart to succeed again after finish")
	}
}

func TestBrowserBootstrapStateSnapshot(t *testing.T) {
	var s browserBootstrapState
	s.tryStart()
	snap := s.snapshot()
	if !snap.Running || snap.Done {
		t.Fatalf("expected running=true done=false right after tryStart, got %+v", snap)
	}

	s.finish(true, "Navegador Camoufox instalado com sucesso.")
	snap = s.snapshot()
	if snap.Running || !snap.Done || !snap.Success || snap.Message == "" {
		t.Fatalf("expected a finished successful snapshot, got %+v", snap)
	}
}

func TestBrowserBootstrapHandlerRefusesWhileRunning(t *testing.T) {
	a := &api{logger: log.New(io.Discard, "", 0)}
	a.bootstrap.tryStart()

	req := httptest.NewRequest("POST", "/api/v1/browser/bootstrap", nil)
	rec := httptest.NewRecorder()
	a.browserBootstrap(rec, req)

	if rec.Code != 409 {
		t.Fatalf("expected 409 while a bootstrap is already running, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestBrowserBootstrapStatusHandlerReflectsState(t *testing.T) {
	a := &api{logger: log.New(io.Discard, "", 0)}
	a.bootstrap.finish(true, "Navegador Camoufox instalado com sucesso.")

	req := httptest.NewRequest("GET", "/api/v1/browser/bootstrap/status", nil)
	rec := httptest.NewRecorder()
	a.browserBootstrapStatus(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "instalado com sucesso") {
		t.Fatalf("expected the status body to reflect the finished message, got %s", rec.Body.String())
	}
}
