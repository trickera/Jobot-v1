package server

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// This file implements the first-run install health check and repair
// endpoints from the installer hardening plan
// (docs/SONNET5-INSTALLER-HARDENING-PLAN.md, Phase 2): make installation
// state visible and repairable instead of a search silently failing deep in
// the pipeline the first time a user tries it.

type installCheck struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Label string `json:"label"`
	Note  string `json:"note,omitempty"`
}

type installHealthResponse struct {
	OK              bool           `json:"ok"`
	Packaged        bool           `json:"packaged"`
	Checks          []installCheck `json:"checks"`
	RepairAvailable bool           `json:"repairAvailable"`
	Message         string         `json:"message"`
}

// installDataWritable attempts to create and immediately remove a marker
// file in dir, reporting whether the directory is writable. It creates dir
// itself if missing (mirrors configStore.open()'s own MkdirAll), since a
// missing directory on first run is not itself a failure.
func installDataWritable(dir string) bool {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false
	}
	marker := filepath.Join(dir, ".sencia-write-test")
	if err := os.WriteFile(marker, []byte("ok"), 0o600); err != nil {
		return false
	}
	_ = os.Remove(marker)
	return true
}

// installInternetReachable does a short, dataless TCP dial to check whether
// the machine can reach the internet at all - used only to make a
// missing-browser diagnosis actionable ("no bundle and no internet" vs "no
// bundle, repair will download it"), never to gate core functionality.
func installInternetReachable() bool {
	conn, err := net.DialTimeout("tcp", "github.com:443", 3*time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// installHealth reports the state of every local piece the app depends on,
// per the plan's health contract. It never mutates anything - see
// installRepair for the fix-it counterpart.
func (a *api) installHealth(w http.ResponseWriter, r *http.Request) {
	packaged := strings.TrimSpace(os.Getenv("SENCIA_PACKAGED")) != ""

	pythonPath, pythonFound := resolvePythonPath()
	_ = pythonPath
	_, workerErr := resolveBrowserWorkerPath()
	workerFound := workerErr == nil

	name, args := pythonCommand()
	camoufoxOK := false
	if pythonFound {
		camoufoxOK = camoufoxImportable(r.Context(), name, args)
	}
	browserInstalled := camoufoxBrowserInstalled()

	_, sqliteErr := a.configStore.load()
	sqliteOK := sqliteErr == nil
	if sqliteErr != nil {
		// The detailed error (which can embed a filesystem path, and
		// therefore the Windows username) goes to the log stream only - the
		// plan requires the UI to avoid exposing local usernames, but the
		// existing Logs screen is the accepted place for path-bearing detail.
		a.log("error", "install health: sqlite: %v", sqliteErr)
	}

	appDataDir := filepath.Dir(a.configStore.path)
	appDataOK := installDataWritable(appDataDir)

	cacheDir := filepath.Dir(camoufoxInstallDir())
	cacheOK := installDataWritable(cacheDir)

	internetOK := installInternetReachable()

	checks := []installCheck{
		{ID: "backend", OK: true, Label: "Backend Go"},
		{ID: "sqlite", OK: sqliteOK, Label: "Banco local"},
		{ID: "python", OK: pythonFound, Label: "Python embutido"},
		{ID: "worker", OK: workerFound, Label: "Browser worker"},
		{ID: "camoufoxImport", OK: camoufoxOK, Label: "Camoufox Python"},
		{ID: "camoufoxBrowser", OK: browserInstalled, Label: "Navegador Camoufox", Note: camoufoxCacheSource()},
		{ID: "appDataWrite", OK: appDataOK, Label: "Permissao em AppData"},
		{ID: "cacheWrite", OK: cacheOK, Label: "Permissao em cache"},
		{ID: "internet", OK: internetOK, Label: "Internet"},
	}

	// internet is diagnostic-only (a bundled browser needs no network at
	// all); every other check is required for the app to function.
	ok := sqliteOK && pythonFound && workerFound && camoufoxOK && browserInstalled && appDataOK && cacheOK

	message := "Sencia Job pronto."
	if !ok {
		message = "Alguns componentes precisam de reparo antes de usar o app."
	}

	writeJSON(w, http.StatusOK, installHealthResponse{
		OK:              ok,
		Packaged:        packaged,
		Checks:          checks,
		RepairAvailable: !ok,
		Message:         message,
	})
}

type installRepairResponse struct {
	OK      bool           `json:"ok"`
	Checks  []installCheck `json:"checks"`
	Message string         `json:"message"`
}

// installRepair performs safe, additive recovery actions - recreating
// missing writable directories and copying a bundled Camoufox browser into
// the cache if it is missing - then re-runs the same checks installHealth
// does so the caller sees the result immediately. It never touches the
// user's database, resumes, or search history.
func (a *api) installRepair(w http.ResponseWriter, r *http.Request) {
	appDataDir := filepath.Dir(a.configStore.path)
	if err := os.MkdirAll(appDataDir, 0o700); err != nil {
		a.log("error", "repair: create app data dir: %v", err)
	}

	cacheDir := filepath.Dir(camoufoxInstallDir())
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		a.log("error", "repair: create cache dir: %v", err)
	}

	if copied, err := ensureCamoufoxCacheFromBundle(func(line string) { a.log("info", "[ REPAIR ] %s", line) }); err != nil {
		a.log("error", "repair: camoufox bundle copy: %v", err)
	} else if copied {
		a.log("success", "[ REPAIR ] bundled Camoufox browser installed to local cache")
	}

	if !camoufoxBrowserInstalled() && !camoufoxBundleAvailable() {
		// No bundle shipped with this build (or it failed to resolve) - fall
		// back to the same background download the manual bootstrap uses,
		// best-effort, bounded by camoufoxBootstrapTimeout.
		if a.bootstrap.tryStart() {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), camoufoxBootstrapTimeout)
				defer cancel()
				filter := &progressLineFilter{}
				err := runCamoufoxFetch(ctx, func(line string) {
					clean, ok := filter.sanitize(line)
					if !ok {
						return
					}
					a.log("info", "[ REPAIR BOOTSTRAP ] %s", clean)
				})
				if err != nil {
					a.log("error", "repair bootstrap: %v", err)
					a.bootstrap.finish(false, "Falha ao baixar o navegador Camoufox durante o reparo.")
					return
				}
				a.bootstrap.finish(true, "Navegador Camoufox instalado com sucesso.")
			}()
		}
	}

	pythonPath, pythonFound := resolvePythonPath()
	_ = pythonPath
	_, workerErr := resolveBrowserWorkerPath()
	workerFound := workerErr == nil
	name, args := pythonCommand()
	camoufoxOK := false
	if pythonFound {
		camoufoxOK = camoufoxImportable(r.Context(), name, args)
	}
	_, sqliteErr := a.configStore.load()
	sqliteOK := sqliteErr == nil

	checks := []installCheck{
		{ID: "sqlite", OK: sqliteOK, Label: "Banco local"},
		{ID: "python", OK: pythonFound, Label: "Python embutido"},
		{ID: "worker", OK: workerFound, Label: "Browser worker"},
		{ID: "camoufoxImport", OK: camoufoxOK, Label: "Camoufox Python"},
		{ID: "camoufoxBrowser", OK: camoufoxBrowserInstalled(), Label: "Navegador Camoufox"},
		{ID: "appDataWrite", OK: installDataWritable(appDataDir), Label: "Permissao em AppData"},
		{ID: "cacheWrite", OK: installDataWritable(cacheDir), Label: "Permissao em cache"},
	}

	ok := true
	for _, c := range checks {
		if !c.OK {
			ok = false
			break
		}
	}

	message := "Reparo concluido."
	if !ok {
		message = "Reparo executado, mas alguns itens ainda precisam de atencao. Veja os logs."
	}

	writeJSON(w, http.StatusOK, installRepairResponse{OK: ok, Checks: checks, Message: message})
}
