package server

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// browserHealthResponse reports whether the Camoufox browser transport is
// ready to run a search, without actually opening a site (CH-01/D9).
type browserHealthResponse struct {
	PythonFound        bool   `json:"pythonFound"`
	PythonPath         string `json:"pythonPath,omitempty"`
	WorkerFound        bool   `json:"workerFound"`
	CamoufoxImportable bool   `json:"camoufoxImportable"`
	BrowserInstalled   bool   `json:"browserInstalled"`
	BrowserBundled     bool   `json:"browserBundled"`
	BrowserSource      string `json:"browserSource,omitempty"`
	Message            string `json:"message"`
}

// resolvePythonPath reports the resolved Python interpreter path (if the
// bundled/overridden one is an absolute path that actually exists) or
// whether a bare command like "py"/"python3" resolves via PATH.
func resolvePythonPath() (string, bool) {
	if pythonResolutionError() != nil {
		// Packaged mode with no valid bundled/overridden interpreter: report
		// not-found even if a coincidental global "py"/"python3" happens to
		// be on PATH on this particular machine (Decision 2) - the health
		// check must reflect what packaged mode will actually refuse to use,
		// not what LookPath happens to find.
		return "", false
	}
	name, _ := pythonCommand()
	if filepath.IsAbs(name) {
		return name, fileExists(name)
	}
	resolved, err := exec.LookPath(name)
	if err != nil {
		return "", false
	}
	return resolved, true
}

// camoufoxCacheDir mirrors camoufox's own platformdirs.user_cache_dir("camoufox")
// resolution on Windows (documented: %LOCALAPPDATA%\camoufox\camoufox\Cache)
// so the health check can tell whether the browser binary was already
// downloaded without shelling out to Python just for that.
func camoufoxCacheDir() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		if dir, err := os.UserCacheDir(); err == nil {
			base = dir
		}
	}
	return filepath.Join(base, "camoufox", "camoufox", "Cache")
}

func camoufoxBrowserInstalled() bool {
	entries, err := os.ReadDir(camoufoxInstallDir())
	return err == nil && len(entries) > 0
}

// camoufoxImportable runs a lightweight `import camoufox` check (no site is
// opened) with a hard timeout so a broken interpreter can't hang the health
// endpoint.
func camoufoxImportable(ctx context.Context, name string, args []string) bool {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmdArgs := append(append([]string{}, args...), "-c", "import camoufox")
	cmd := exec.CommandContext(ctx, name, cmdArgs...)
	cmd.Env = withCamoufoxEnv(os.Environ())
	hideWorkerConsole(cmd)
	return cmd.Run() == nil
}

func browserHealthMessage(pythonFound, workerFound, camoufoxOK, browserInstalled bool) string {
	switch {
	case !pythonFound:
		return "Python nao encontrado. Reinstale o aplicativo ou configure a variavel SENCIA_PYTHON."
	case !workerFound:
		return "Script do worker de navegador nao encontrado."
	case !camoufoxOK:
		return "Nao foi possivel importar o Camoufox no Python encontrado. Reinstale o aplicativo."
	case !browserInstalled:
		return "Navegador Camoufox ainda nao foi instalado. Rode o bootstrap para baixa-lo."
	default:
		return "Camoufox pronto para uso."
	}
}

// browserHealth reports whether the Camoufox transport can run a search
// (CH-01/D9) without starting one - a fast, safe check the UI can poll
// before/instead of surfacing a raw scrape failure.
func (a *api) browserHealth(w http.ResponseWriter, r *http.Request) {
	name, args := pythonCommand()
	pythonPath, pythonFound := resolvePythonPath()

	_, workerErr := resolveBrowserWorkerPath()
	workerFound := workerErr == nil

	camoufoxOK := false
	if pythonFound {
		camoufoxOK = camoufoxImportable(r.Context(), name, args)
	}

	browserInstalled := camoufoxBrowserInstalled()

	writeJSON(w, http.StatusOK, browserHealthResponse{
		PythonFound:        pythonFound,
		PythonPath:         pythonPath,
		WorkerFound:        workerFound,
		CamoufoxImportable: camoufoxOK,
		BrowserInstalled:   browserInstalled,
		BrowserBundled:     camoufoxBundleAvailable(),
		BrowserSource:      camoufoxCacheSource(),
		Message:            browserHealthMessage(pythonFound, workerFound, camoufoxOK, browserInstalled),
	})
}

// browserBootstrapState tracks the one background `camoufox fetch` download
// (CH-01), mirroring the running/done/message shape liveSearchState already
// uses for the background search job.
type browserBootstrapState struct {
	mu      sync.RWMutex
	running bool
	done    bool
	success bool
	message string
}

type browserBootstrapStatusResponse struct {
	Running bool   `json:"running"`
	Done    bool   `json:"done"`
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func (s *browserBootstrapState) tryStart() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return false
	}
	s.running = true
	s.done = false
	s.success = false
	s.message = "Baixando o navegador Camoufox..."
	return true
}

func (s *browserBootstrapState) finish(success bool, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = false
	s.done = true
	s.success = success
	s.message = message
}

func (s *browserBootstrapState) snapshot() browserBootstrapStatusResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return browserBootstrapStatusResponse{Running: s.running, Done: s.done, Success: s.success, Message: s.message}
}

const camoufoxBootstrapTimeout = 10 * time.Minute

// browserBootstrap starts the one-time Camoufox browser download in the
// background and returns immediately; poll browserBootstrapStatus for
// progress. Refuses to start a second download concurrently.
func (a *api) browserBootstrap(w http.ResponseWriter, r *http.Request) {
	if !a.bootstrap.tryStart() {
		writeJSON(w, http.StatusConflict, map[string]string{"message": "A instalacao do Camoufox ja esta em andamento."})
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), camoufoxBootstrapTimeout)
		defer cancel()
		filter := &progressLineFilter{}
		err := runCamoufoxFetch(ctx, func(line string) {
			clean, ok := filter.sanitize(line)
			if !ok {
				return
			}
			a.log("info", "[ CAMOUFOX BOOTSTRAP ] %s", clean)
		})
		if err != nil {
			a.log("error", "camoufox bootstrap: %v", err)
			a.bootstrap.finish(false, "Falha ao baixar o navegador Camoufox. Veja os logs para detalhes.")
			return
		}
		a.bootstrap.finish(true, "Navegador Camoufox instalado com sucesso.")
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{"message": "Instalacao do Camoufox iniciada."})
}

func (a *api) browserBootstrapStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.bootstrap.snapshot())
}
