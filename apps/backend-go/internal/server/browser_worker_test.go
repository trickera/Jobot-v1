package server

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestBrowserSessionCallTimesOutOnHungWorker is the direct regression test for
// BUG-01: a worker that never answers a request must not block call() (and,
// transitively, the whole search) forever. Before the fix, call() did a bare
// b.out.ReadBytes('\n') with no deadline, so a wedged Camoufox page load could
// only ever be recovered by the outer 12-minute search context killing the
// process - this test proves a single call now fails fast on its own.
func TestBrowserSessionCallTimesOutOnHungWorker(t *testing.T) {
	original := workerCallTimeout
	workerCallTimeout = 50 * time.Millisecond
	defer func() { workerCallTimeout = original }()

	stdinRead, stdinWrite := io.Pipe()
	defer stdinRead.Close()
	defer stdinWrite.Close()

	// Drain stdin so the write in call() never blocks on nobody reading it.
	go func() {
		_, _ = io.Copy(io.Discard, stdinRead)
	}()

	session := &browserSession{
		stdin: stdinWrite,
		out:   bufio.NewReaderSize(neverReader{}, 1<<10),
	}

	start := time.Now()
	_, err := session.call(workerRequest{Cmd: "fetch", URL: "https://example.com"})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a timeout error from a worker that never responds")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("call() took %s to time out; expected it to return close to workerCallTimeout (50ms)", elapsed)
	}

	session.mu.Lock()
	closed := session.closed
	session.mu.Unlock()
	if !closed {
		t.Fatal("expected session to be marked closed after a call timeout")
	}

	// A second call on the now-closed session must fail immediately, not hang
	// again waiting on the dead worker.
	start = time.Now()
	if _, err := session.call(workerRequest{Cmd: "fetch", URL: "https://example.com"}); err == nil {
		t.Fatal("expected an error calling a closed session")
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("call() on a closed session should fail instantly, took %s", elapsed)
	}
}

// neverReader blocks forever on Read, standing in for a worker stdout pipe
// that is never written to.
type neverReader struct{}

func (neverReader) Read(_ []byte) (int, error) {
	select {}
}

// TestBrowserSessionCallSkipsNonJSONStdoutLines is the direct regression test
// for the QA-003 fresh-profile bug: a third-party library (camoufox's own
// first-run browser downloader, via click.secho) printing a plain-text status
// line to stdout before the real JSON response must not fail the call with
// "invalid character 'D' looking for beginning of value" - it should be
// skipped so the worker's actual response still gets through.
func TestBrowserSessionCallSkipsNonJSONStdoutLines(t *testing.T) {
	stdinRead, stdinWrite := io.Pipe()
	defer stdinRead.Close()
	defer stdinWrite.Close()
	go func() {
		_, _ = io.Copy(io.Discard, stdinRead)
	}()

	contaminated := "Downloading package: https://github.com/daijro/camoufox/releases/download/v135/camoufox.zip\n" +
		"Extracting Camoufox: /some/cache/dir\n" +
		`{"ok":true}` + "\n"

	session := &browserSession{
		stdin: stdinWrite,
		out:   bufio.NewReader(strings.NewReader(contaminated)),
	}

	resp, err := session.call(workerRequest{Cmd: "start"})
	if err != nil {
		t.Fatalf("expected call() to skip the non-JSON lines and find the real response, got error: %v", err)
	}
	if !resp.OK {
		t.Fatalf("expected OK response, got %+v", resp)
	}
}

// TestBrowserSessionCallFailsCleanlyWhenNeverJSON ensures the skip loop in
// call() is bounded: a worker whose stdout is nothing but junk must return a
// clean error instead of hanging or panicking.
func TestBrowserSessionCallFailsCleanlyWhenNeverJSON(t *testing.T) {
	stdinRead, stdinWrite := io.Pipe()
	defer stdinRead.Close()
	defer stdinWrite.Close()
	go func() {
		_, _ = io.Copy(io.Discard, stdinRead)
	}()

	var junk strings.Builder
	for i := 0; i < maxWorkerResponseSkipLines+5; i++ {
		junk.WriteString("not json\n")
	}

	session := &browserSession{
		stdin: stdinWrite,
		out:   bufio.NewReader(strings.NewReader(junk.String())),
	}

	if _, err := session.call(workerRequest{Cmd: "start"}); err == nil {
		t.Fatal("expected an error when the worker never sends a JSON line")
	}
}

func TestBrowserSessionResponseLineLimitBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name        string
		length      int
		wantTooLong bool
	}{
		{name: "limit minus one", length: maxWorkerResponseLineBytes - 1},
		{name: "limit", length: maxWorkerResponseLineBytes},
		{name: "limit plus one", length: maxWorkerResponseLineBytes + 1, wantTooLong: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdinRead, stdinWrite := io.Pipe()
			defer stdinRead.Close()
			defer stdinWrite.Close()
			go func() {
				_, _ = io.Copy(io.Discard, stdinRead)
			}()

			line := fmt.Sprintf(`{"ok":true,"error":"%s"}`+"\n", strings.Repeat("x", tc.length-len(`{"ok":true,"error":""}`)-1))
			if len(line) != tc.length {
				t.Fatalf("fixture line length = %d, want %d", len(line), tc.length)
			}
			session := &browserSession{
				stdin: stdinWrite,
				out:   bufio.NewReader(strings.NewReader(line)),
			}
			response, err := session.call(workerRequest{Cmd: "fetch"})
			if tc.wantTooLong {
				var tooLarge *externalResponseTooLargeError
				if err == nil || !errors.As(err, &tooLarge) {
					t.Fatalf("expected typed over-limit error, got %v", err)
				}
				if strings.Contains(err.Error(), strings.Repeat("x", 32)) {
					t.Fatal("oversized worker content leaked into the error")
				}
				if !session.closed {
					t.Fatal("expected an oversized response to close the worker session")
				}
				return
			}
			if err != nil {
				t.Fatalf("response at %d bytes should succeed: %v", tc.length, err)
			}
			if !response.OK {
				t.Fatalf("expected OK response at %d bytes, got %+v", tc.length, response)
			}
		})
	}
}

func TestResolveBundledPythonFromDirDirectSibling(t *testing.T) {
	dir := t.TempDir()
	pythonDir := filepath.Join(dir, "python")
	if err := os.MkdirAll(pythonDir, 0o755); err != nil {
		t.Fatal(err)
	}
	exePath := filepath.Join(pythonDir, "python.exe")
	if err := os.WriteFile(exePath, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}

	path, ok := resolveBundledPythonFromDir(dir)
	if !ok {
		t.Fatal("expected a bundled python to be found")
	}
	if path != exePath {
		t.Fatalf("expected %s, got %s", exePath, path)
	}
}

func TestResolveBrowserWorkerPathOverride(t *testing.T) {
	dir := t.TempDir()
	workerPath := filepath.Join(dir, "worker.py")
	if err := os.WriteFile(workerPath, []byte("# test worker"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SENCIA_BROWSER_WORKER", workerPath)

	got, err := resolveBrowserWorkerPath()
	if err != nil {
		t.Fatalf("expected worker override to resolve: %v", err)
	}
	if got != workerPath {
		t.Fatalf("expected %s, got %s", workerPath, got)
	}
}

func TestResolveBundledPythonFromDirResourcesNested(t *testing.T) {
	dir := t.TempDir()
	pythonDir := filepath.Join(dir, "resources", "python")
	if err := os.MkdirAll(pythonDir, 0o755); err != nil {
		t.Fatal(err)
	}
	exePath := filepath.Join(pythonDir, "python.exe")
	if err := os.WriteFile(exePath, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}

	path, ok := resolveBundledPythonFromDir(dir)
	if !ok {
		t.Fatal("expected a bundled python to be found under resources/")
	}
	if path != exePath {
		t.Fatalf("expected %s, got %s", exePath, path)
	}
}

func TestResolveBundledPythonFromDirNotFound(t *testing.T) {
	dir := t.TempDir()
	if _, ok := resolveBundledPythonFromDir(dir); ok {
		t.Fatal("expected no bundled python to be found in an empty dir")
	}
}

func TestPythonCommandSenciaPythonOverrideWins(t *testing.T) {
	t.Setenv("SENCIA_PYTHON", "C:\\fake\\python.exe")
	name, args := pythonCommand()
	if name != "C:\\fake\\python.exe" {
		t.Fatalf("expected SENCIA_PYTHON override to win, got %q", name)
	}
	if len(args) != 0 {
		t.Fatalf("expected no extra args for an explicit override, got %+v", args)
	}
}

func TestPythonResolutionErrorDevModeAlwaysNil(t *testing.T) {
	t.Setenv("SENCIA_PACKAGED", "")
	t.Setenv("SENCIA_PYTHON", filepath.Join(t.TempDir(), "does-not-exist.exe"))
	if err := pythonResolutionError(); err != nil {
		t.Fatalf("expected dev mode to never hard-fail python resolution, got %v", err)
	}
}

func TestPythonResolutionErrorPackagedValidOverride(t *testing.T) {
	dir := t.TempDir()
	pythonExe := filepath.Join(dir, "python.exe")
	if err := os.WriteFile(pythonExe, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SENCIA_PACKAGED", "1")
	t.Setenv("SENCIA_PYTHON", pythonExe)
	if err := pythonResolutionError(); err != nil {
		t.Fatalf("expected a valid SENCIA_PYTHON override to satisfy packaged mode, got %v", err)
	}
}

func TestPythonResolutionErrorPackagedInvalidOverride(t *testing.T) {
	t.Setenv("SENCIA_PACKAGED", "1")
	t.Setenv("SENCIA_PYTHON", filepath.Join(t.TempDir(), "does-not-exist.exe"))
	err := pythonResolutionError()
	if err == nil {
		t.Fatal("expected packaged mode to hard-fail on an invalid SENCIA_PYTHON override")
	}
	if !strings.Contains(err.Error(), "SENCIA_PYTHON") {
		t.Fatalf("expected the error to name SENCIA_PYTHON, got %v", err)
	}
}

func TestPythonResolutionErrorPackagedNoOverrideNoBundle(t *testing.T) {
	t.Setenv("SENCIA_PACKAGED", "1")
	t.Setenv("SENCIA_PYTHON", "")
	err := pythonResolutionError()
	if err == nil {
		t.Fatal("expected packaged mode with no override and no bundled python next to the test binary to hard-fail")
	}
}

func TestResolvePythonPathPackagedResolutionFailureReportsNotFound(t *testing.T) {
	t.Setenv("SENCIA_PACKAGED", "1")
	t.Setenv("SENCIA_PYTHON", "")
	// Even though a real "py" almost certainly resolves via PATH on this dev
	// machine, packaged mode with no valid override/bundle must report
	// not-found rather than silently trusting a coincidental global
	// interpreter (Decision 2).
	if _, found := resolvePythonPath(); found {
		t.Fatal("expected resolvePythonPath to report not-found when packaged resolution fails, regardless of PATH")
	}
}
