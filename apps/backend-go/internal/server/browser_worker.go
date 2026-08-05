package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

// workerCallTimeout bounds a single Camoufox worker request/response
// round-trip (one page fetch, one Gupy XHR capture, the Indeed warmup, ...).
// Without this, a single wedged page load blocks on an unbuffered pipe read
// forever, regardless of the overall search deadline (BUG-01): the worker
// process is only killed by the outer context when the WHOLE search budget
// expires, so one stuck fetch could stall an entire run. A var (not const) so
// tests can shrink it instead of waiting out the real timeout.
var workerCallTimeout = 45 * time.Second

// maxWorkerResponseSkipLines bounds how many non-JSON stdout lines call()
// will skip while looking for the real protocol response (defense in depth
// - see the read goroutine in call()).
const maxWorkerResponseSkipLines = 200

// maxWorkerResponseLineBytes is a protocol boundary, not an expected payload
// size. The largest HTML fixture in this repository is 29,854 bytes; the 1 MiB
// cap leaves roughly 35x headroom while ensuring a corrupt worker cannot make
// ReadSlice accumulate an unbounded line in memory.
const maxWorkerResponseLineBytes = 1 << 20

// llmMaxResponseBodyBytes bounds provider responses before they reach JSON
// decoding. The largest checked-in Resume Studio contract is 14,319 bytes;
// 256 KiB leaves an ample envelope for provider metadata and generated JSON
// while keeping an accidental unbounded response out of memory.
const llmMaxResponseBodyBytes = 256 << 10

// externalResponseTooLargeError is deliberately small and body-free. Callers
// can classify the failure without retaining or logging untrusted response
// content.
type externalResponseTooLargeError struct {
	Protocol string
	Limit    int64
}

func (e *externalResponseTooLargeError) Error() string {
	return fmt.Sprintf("%s response exceeded %d-byte limit", e.Protocol, e.Limit)
}

// readLimitedBody consumes at most limit+1 bytes so the caller can distinguish
// an exact-limit response from an oversized one without buffering an unbounded
// body.
func readLimitedBody(r io.Reader, limit int64, protocol string) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if int64(len(body)) > limit {
		return nil, &externalResponseTooLargeError{Protocol: protocol, Limit: limit}
	}
	if err != nil {
		return nil, err
	}
	return body, nil
}

// limitedWriter enforces a hard pre-write cap for downloads. It writes only
// the bytes that fit, then returns a typed error; callers can remove the
// resulting partial file before it is ever used.
type limitedWriter struct {
	dst      io.Writer
	limit    int64
	written  int64
	protocol string
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	remaining := w.limit - w.written
	if remaining <= 0 {
		return 0, &externalResponseTooLargeError{Protocol: w.protocol, Limit: w.limit}
	}
	if int64(len(p)) > remaining {
		n, err := w.dst.Write(p[:int(remaining)])
		w.written += int64(n)
		if err != nil {
			return n, err
		}
		return n, &externalResponseTooLargeError{Protocol: w.protocol, Limit: w.limit}
	}
	n, err := w.dst.Write(p)
	w.written += int64(n)
	return n, err
}

func readWorkerResponseLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if int64(len(line))+int64(len(chunk)) > maxWorkerResponseLineBytes {
			return nil, &externalResponseTooLargeError{Protocol: "browser worker", Limit: maxWorkerResponseLineBytes}
		}
		line = append(line, chunk...)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			return line, err
		}
		return line, nil
	}
}

// browserSession keeps a single stealth browser process alive for the whole
// search. The Python worker is a thin transport: it opens URLs with Camoufox
// and returns raw HTML (or captured Gupy XHR JSON). All parsing and business
// logic live in Go.
type browserSession struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	out    *bufio.Reader
	mu     sync.Mutex
	closed bool
}

type workerRequest struct {
	Cmd             string `json:"cmd"`
	URL             string `json:"url,omitempty"`
	WaitUntil       string `json:"waitUntil,omitempty"`
	WaitForSelector string `json:"waitForSelector,omitempty"`
	Headless        bool   `json:"headless,omitempty"`
}

type workerResponse struct {
	OK      bool              `json:"ok"`
	Error   string            `json:"error,omitempty"`
	HTML    string            `json:"html,omitempty"`
	Blocked bool              `json:"blocked,omitempty"`
	Records []json.RawMessage `json:"records,omitempty"`
}

func (s *scraperBridge) openBrowserSession(ctx context.Context, headless bool) (*browserSession, error) {
	workerPath, err := resolveBrowserWorkerPath()
	if err != nil {
		return nil, err
	}
	if err := pythonResolutionError(); err != nil {
		return nil, err
	}
	name, args := pythonCommand()
	args = append(args, workerPath)

	if copied, err := ensureCamoufoxCacheFromBundle(func(line string) { s.log("info", "[ CAMOUFOX ] %s", line) }); err != nil {
		s.log("error", "camoufox bundle copy: %v", err)
	} else if copied {
		s.log("success", "[ CAMOUFOX ] bundled browser installed to local cache")
	}

	command := exec.CommandContext(ctx, name, args...)
	command.Env = withCamoufoxEnv(os.Environ())
	hideWorkerConsole(command)
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return nil, err
	}

	s.log("info", "[ SCRAPER ] Camoufox worker (stealth transport): %s", workerPath)
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("nao foi possivel iniciar Python/Camoufox: %w", err)
	}

	go func() {
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		filter := &progressLineFilter{}
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			clean, ok := filter.sanitize(line)
			if !ok {
				continue
			}
			s.log("info", "%s", clean)
		}
	}()

	session := &browserSession{
		cmd:   command,
		stdin: stdin,
		out:   bufio.NewReaderSize(stdout, 1<<20),
	}

	if _, err := session.call(workerRequest{Cmd: "start", Headless: headless}); err != nil {
		session.close()
		return nil, fmt.Errorf("Camoufox nao inicializou: %w", err)
	}
	return session, nil
}

func (b *browserSession) call(req workerRequest) (workerResponse, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return workerResponse{}, errors.New("browser session closed")
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return workerResponse{}, err
	}
	if _, err := b.stdin.Write(append(payload, '\n')); err != nil {
		return workerResponse{}, fmt.Errorf("worker stdin: %w", err)
	}

	type readResult struct {
		line []byte
		err  error
	}
	resultCh := make(chan readResult, 1)
	go func() {
		// Defense in depth: the worker's own stdout hygiene (module docstring
		// in worker.py) should make every stdout line valid JSON, but a stray
		// non-JSON line here should not be fatal - skip it and keep reading
		// for the real protocol response instead of failing the whole call
		// (this is what surfaced as "worker resposta invalida: invalid
		// character 'D' looking for beginning of value" before worker.py
		// redirected all non-protocol stdout writes to stderr).
		for i := 0; i < maxWorkerResponseSkipLines; i++ {
			line, err := readWorkerResponseLine(b.out)
			if err != nil {
				resultCh <- readResult{line: line, err: err}
				return
			}
			if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 && trimmed[0] == '{' {
				resultCh <- readResult{line: line, err: nil}
				return
			}
		}
		resultCh <- readResult{err: fmt.Errorf("no JSON response after %d non-JSON stdout lines", maxWorkerResponseSkipLines)}
	}()

	select {
	case res := <-resultCh:
		if res.err != nil {
			var tooLarge *externalResponseTooLargeError
			if errors.As(res.err, &tooLarge) {
				b.abortLocked()
			}
			return workerResponse{}, fmt.Errorf("worker stdout: %w", res.err)
		}
		var response workerResponse
		if err := json.Unmarshal(res.line, &response); err != nil {
			return workerResponse{}, fmt.Errorf("worker resposta invalida: %w", err)
		}
		if !response.OK {
			return response, fmt.Errorf("worker: %s", coalesce(response.Error, "falha desconhecida"))
		}
		return response, nil
	case <-time.After(workerCallTimeout):
		// The read goroutine above is still blocked on ReadBytes and there is
		// no way to cancel a single pending pipe read. Treat the whole session
		// as unusable: kill the process (which unblocks/errors the leaked
		// goroutine so it can exit) and mark closed so every subsequent call
		// on this session fails fast instead of hanging again.
		b.abortLocked()
		return workerResponse{}, fmt.Errorf("worker call excedeu %s sem resposta (pagina/navegador travado)", workerCallTimeout)
	}
}

// abortLocked marks a session unusable and tears down its process. Callers
// hold b.mu; Wait runs asynchronously because the worker may be blocked in a
// pipe read that the kill is intended to unblock.
func (b *browserSession) abortLocked() {
	b.closed = true
	if b.cmd != nil && b.cmd.Process != nil {
		_ = b.cmd.Process.Kill()
	}
	if b.cmd != nil {
		go b.cmd.Wait()
	}
}

func (b *browserSession) fetch(url string, waitUntil string, waitForSelector string) (string, bool, error) {
	if waitUntil == "" {
		waitUntil = "domcontentloaded"
	}
	response, err := b.call(workerRequest{
		Cmd:             "fetch",
		URL:             url,
		WaitUntil:       waitUntil,
		WaitForSelector: waitForSelector,
	})
	if err != nil {
		return "", false, err
	}
	return response.HTML, response.Blocked, nil
}

func (b *browserSession) fetchGupy(url string) ([]map[string]any, string, error) {
	response, err := b.call(workerRequest{Cmd: "fetch_gupy", URL: url})
	if err != nil {
		return nil, "", err
	}
	records := make([]map[string]any, 0, len(response.Records))
	for _, raw := range response.Records {
		var record map[string]any
		if err := json.Unmarshal(raw, &record); err == nil {
			records = append(records, record)
		}
	}
	return records, response.HTML, nil
}

func (b *browserSession) warmIndeed() error {
	_, err := b.call(workerRequest{Cmd: "warm_indeed"})
	return err
}

func (b *browserSession) close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	b.mu.Unlock()

	if b.stdin != nil {
		payload, _ := json.Marshal(workerRequest{Cmd: "close"})
		_, _ = b.stdin.Write(append(payload, '\n'))
		_ = b.stdin.Close()
	}
	if b.cmd != nil {
		_ = b.cmd.Wait()
	}
}

func resolveBrowserWorkerPath() (string, error) {
	if value := strings.TrimSpace(os.Getenv("SENCIA_BROWSER_WORKER")); value != "" {
		if fileExists(value) {
			return value, nil
		}
		return "", fmt.Errorf("SENCIA_BROWSER_WORKER aponta para arquivo inexistente: %s", value)
	}

	var candidates []string
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(cwd, "apps", "browser-worker", "worker.py"),
			filepath.Join(cwd, "..", "browser-worker", "worker.py"),
			filepath.Join(cwd, "backend-browser", "worker.py"),
			filepath.Join(cwd, "..", "backend-browser", "worker.py"),
			filepath.Join(cwd, "..", "..", "backend-browser", "worker.py"),
		)
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, "..", "..", "browser-worker", "worker.py"),
			filepath.Join(dir, "backend-browser", "worker.py"),
			filepath.Join(dir, "resources", "backend-browser", "worker.py"),
			filepath.Join(dir, "..", "resources", "backend-browser", "worker.py"),
			filepath.Join(dir, "..", "..", "backend-browser", "worker.py"),
			filepath.Join(dir, "..", "..", "..", "backend-browser", "worker.py"),
		)
	}
	for _, candidate := range candidates {
		if fileExists(candidate) {
			if abs, err := filepath.Abs(candidate); err == nil {
				return abs, nil
			}
			return candidate, nil
		}
	}
	return "", errors.New("browser worker Python nao encontrado; a pipeline Camoufox nao pode iniciar")
}

// pythonCommand resolves how to invoke Python for the Camoufox worker.
// Order (CH-01/D9): SENCIA_PYTHON override -> bundled CPython embeddable next
// to the packaged .exe (built by scripts/release/pack-python.ps1) -> `py -3` (Windows
// dev machines) -> `python3` (unix dev machines).
func pythonCommand() (string, []string) {
	if value := strings.TrimSpace(os.Getenv("SENCIA_PYTHON")); value != "" {
		return value, nil
	}
	if exe, err := os.Executable(); err == nil {
		if path, ok := resolveBundledPythonFromDir(filepath.Dir(exe)); ok {
			return path, nil
		}
	}
	if runtime.GOOS == "windows" {
		return "py", []string{"-3"}
	}
	return "python3", nil
}

// pythonResolutionError enforces Decision 2 (installer hardening: packaged
// paths, docs/architecture/specs/2026-07-09-installer-hardening-design.md):
// packaged mode must never silently fall back to a global `py -3`/`python3`
// - a coincidental global interpreter on a "clean" machine could subtly
// differ (missing camoufox package, wrong version) and nobody would notice
// since the app would still start. It returns a clear, actionable error the
// moment resolution would otherwise fall through to a bare/global command,
// so callers can fail fast with a real message instead of a cryptic
// "executable file not found in %PATH%" from os/exec. Returns nil in dev
// mode (SENCIA_PACKAGED unset) - today's fallback chain there is unchanged.
func pythonResolutionError() error {
	if strings.TrimSpace(os.Getenv("SENCIA_PACKAGED")) == "" {
		return nil
	}
	if value := strings.TrimSpace(os.Getenv("SENCIA_PYTHON")); value != "" {
		if fileExists(value) {
			return nil
		}
		return fmt.Errorf("SENCIA_PYTHON aponta para um arquivo inexistente: %s", value)
	}
	if exe, err := os.Executable(); err == nil {
		if _, ok := resolveBundledPythonFromDir(filepath.Dir(exe)); ok {
			return nil
		}
	}
	return errors.New("Python embutido nao encontrado nesta instalacao. Reinstale o aplicativo ou use Reparar instalacao.")
}

// resolveBundledPythonFromDir looks for the bundled CPython embeddable next
// to the given directory (normally the packaged app's install directory).
// Two candidate layouts are checked because Tauri's NSIS resource placement
// mirrors resolveBrowserWorkerPath's existing dual-candidate pattern for
// backend-browser: some installs place resources as a direct sibling of the
// exe, others nest them under a "resources" folder.
func resolveBundledPythonFromDir(dir string) (string, bool) {
	candidates := []string{
		filepath.Join(dir, "python", "python.exe"),
		filepath.Join(dir, "resources", "python", "python.exe"),
	}
	for _, candidate := range candidates {
		if fileExists(candidate) {
			return candidate, true
		}
	}
	return "", false
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// runCamoufoxFetch spawns `python -m camoufox fetch`, the ~1 GB download of
// the Camoufox browser binary itself. Since the installer hardening pass
// this is a fallback/repair path only - the browser is bundled by default
// (scripts/release/pack-camoufox.ps1, copied into the local cache on first run by
// ensureCamoufoxCacheFromBundle in browser_bundle.go); this function is what
// runs when no bundle was shipped or the bundle copy failed. Every
// stdout/stderr line is handed to onLog as it arrives so callers can surface
// live progress.
func runCamoufoxFetch(ctx context.Context, onLog func(string)) error {
	if err := pythonResolutionError(); err != nil {
		return err
	}
	name, args := pythonCommand()
	cmdArgs := append(append([]string{}, args...), "-m", "camoufox", "fetch")
	if err := runStreamedCommand(ctx, name, cmdArgs, withCamoufoxEnv(os.Environ()), onLog); err != nil {
		return fmt.Errorf("download do Camoufox falhou: %w", err)
	}
	return nil
}

// runStreamedCommand runs name+args to completion, handing every
// stdout/stderr line to onLog as it arrives. Split out of runCamoufoxFetch
// so the process-streaming mechanics can be tested against any executable
// (not just a real `camoufox fetch`, which downloads ~1 GB). extraEnv, if
// non-nil, replaces the child's inherited environment (pass
// withCamoufoxEnv(os.Environ()) to inherit normally plus the Camoufox cache
// override; pass nil to inherit the environment unchanged).
func runStreamedCommand(ctx context.Context, name string, args []string, extraEnv []string, onLog func(string)) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = extraEnv
	hideWorkerConsole(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	var wg sync.WaitGroup
	wg.Add(2)
	streamLines := func(r io.Reader) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" && onLog != nil {
				onLog(line)
			}
		}
	}
	go streamLines(stdout)
	go streamLines(stderr)
	wg.Wait()

	return cmd.Wait()
}

// hideWorkerConsole prevents a visible cmd.exe window when the Go backend
// launches py/python for the Camoufox worker on Windows GUI builds.
func hideWorkerConsole(cmd *exec.Cmd) {
	if runtime.GOOS != "windows" {
		return
	}
	const createNoWindow = 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}
