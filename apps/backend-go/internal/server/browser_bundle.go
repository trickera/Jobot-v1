package server

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

// This file implements the Camoufox browser bundling strategy from the
// installer hardening plan (docs/SONNET5-INSTALLER-HARDENING-PLAN.md, Phase
// 1) and the hybrid bundle-first decision recorded in
// docs/superpowers/specs/2026-07-09-installer-hardening-design.md: ship the
// ~1 GB Camoufox browser binary inside the installer (built by
// scripts/release/pack-camoufox.ps1 into resources/camoufox) so a clean machine
// never depends on a fragile first-run download; the existing
// browserBootstrap download flow (browser_health.go) remains as a
// repair/fallback path when no bundle is present or it fails to copy.
//
// Camoufox's own cache directory (camoufox.pkgman.INSTALL_DIR) is computed
// once at import time from platformdirs.user_cache_dir("camoufox"), which
// normally resolves to %LOCALAPPDATA%\camoufox\camoufox\Cache. platformdirs
// supports an official override for this on Windows via the
// WIN_PD_OVERRIDE_LOCAL_APPDATA env var (see get_win_folder in
// platformdirs/windows.py) - camoufoxProcEnv() sets it (in packaged mode, or
// whenever SENCIA_CAMOUFOX_CACHE is explicit) so every python subprocess
// that touches Camoufox agrees on one cache location contained under the
// app's own data dir instead of an unrelated %LOCALAPPDATA%\camoufox path.

// camoufoxCacheOverrideRoot returns the directory Camoufox's cache should be
// contained under, or "" to leave platformdirs' default resolution
// untouched. Untouched is the deliberate dev-mode default: SENCIA_PACKAGED
// unset means today's behavior (global %LOCALAPPDATA%\camoufox\camoufox\
// Cache) is preserved exactly, matching Decision 2 (packaged-mode hardening
// must not change dev/QA behavior).
func camoufoxCacheOverrideRoot() string {
	if v := strings.TrimSpace(os.Getenv("SENCIA_CAMOUFOX_CACHE")); v != "" {
		return v
	}
	if strings.TrimSpace(os.Getenv("SENCIA_PACKAGED")) == "" {
		return ""
	}
	base := strings.TrimSpace(os.Getenv("SENCIA_APP_DATA"))
	if base == "" {
		base = strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	}
	if base == "" {
		if dir, err := os.UserCacheDir(); err == nil {
			base = dir
		}
	}
	if base == "" {
		return ""
	}
	return filepath.Join(base, "Sencia Job", "browser")
}

// camoufoxInstallDir mirrors platformdirs' Windows resolution for
// user_cache_dir("camoufox") (appauthor defaults to appname, "Cache"
// opinion): <root>\camoufox\camoufox\Cache. When no override root is set,
// <root> is the real %LOCALAPPDATA%, matching today's camoufoxCacheDir().
func camoufoxInstallDir() string {
	root := camoufoxCacheOverrideRoot()
	if root == "" {
		return camoufoxCacheDir()
	}
	return filepath.Join(root, "camoufox", "camoufox", "Cache")
}

// camoufoxProcEnv returns the extra environment variables every python
// subprocess touching Camoufox (worker session, bootstrap fetch, importable
// check) should get appended to its inherited environment, so they all
// agree on the same cache location.
func camoufoxProcEnv() []string {
	root := camoufoxCacheOverrideRoot()
	if root == "" {
		return nil
	}
	return []string{"WIN_PD_OVERRIDE_LOCAL_APPDATA=" + root}
}

// withCamoufoxEnv returns base (typically os.Environ()) with camoufoxProcEnv
// appended. A later duplicate env var wins in os/exec, so appending is
// sufficient to override.
func withCamoufoxEnv(base []string) []string {
	extra := camoufoxProcEnv()
	if len(extra) == 0 {
		return base
	}
	return append(append([]string{}, base...), extra...)
}

// resolveCamoufoxBundleDir looks for a bundled Camoufox browser next to the
// packaged .exe, mirroring resolveBundledPythonFromDir's dual-candidate
// layout pattern. SENCIA_CAMOUFOX_BUNDLE takes an explicit override.
func resolveCamoufoxBundleDir() (string, bool) {
	if v := strings.TrimSpace(os.Getenv("SENCIA_CAMOUFOX_BUNDLE")); v != "" {
		if fileExists(filepath.Join(v, "camoufox.exe")) {
			return v, true
		}
		return "", false
	}
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	dir := filepath.Dir(exe)
	candidates := []string{
		filepath.Join(dir, "camoufox"),
		filepath.Join(dir, "resources", "camoufox"),
	}
	for _, candidate := range candidates {
		if fileExists(filepath.Join(candidate, "camoufox.exe")) {
			return candidate, true
		}
	}
	return "", false
}

// camoufoxBundleAvailable reports whether a bundled browser was shipped with
// this install, without touching the user cache - used by the install
// health check (Phase 2).
func camoufoxBundleAvailable() bool {
	_, ok := resolveCamoufoxBundleDir()
	return ok
}

// ensureCamoufoxCacheFromBundle copies a bundled Camoufox browser into the
// resolved cache dir the running Camoufox will actually use, if (a) a bundle
// is available and (b) the cache is not already populated. It never
// overwrites an existing cache - a user's already-fetched or
// already-updated browser is left alone. onLog receives a compact progress
// message; it may be nil.
func ensureCamoufoxCacheFromBundle(onLog func(string)) (copied bool, err error) {
	installDir := camoufoxInstallDir()
	if fileExists(filepath.Join(installDir, "camoufox.exe")) {
		return false, nil
	}
	bundleDir, ok := resolveCamoufoxBundleDir()
	if !ok {
		return false, nil
	}
	if onLog != nil {
		onLog("copying bundled Camoufox browser to " + installDir)
	}
	if err := copyDirRecursive(bundleDir, installDir); err != nil {
		return false, err
	}
	if !fileExists(filepath.Join(installDir, "camoufox.exe")) {
		return false, errNoCamoufoxExeAfterCopy
	}
	if onLog != nil {
		onLog("bundled Camoufox browser ready")
	}
	return true, nil
}

var errNoCamoufoxExeAfterCopy = camoufoxCopyError("camoufox.exe missing from cache dir after copying the bundle")

type camoufoxCopyError string

func (e camoufoxCopyError) Error() string { return string(e) }

// copyDirRecursive copies every file under src into dst, creating
// directories as needed and preserving the source file mode.
func copyDirRecursive(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// camoufoxBundleVersion reads the browser-version.json marker written by
// scripts/release/pack-camoufox.ps1 next to the bundled browser, for display in the
// install health check. Returns "" if no bundle is present.
func camoufoxBundleVersion() string {
	bundleDir, ok := resolveCamoufoxBundleDir()
	if !ok {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(bundleDir, "browser-version.json"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// camoufoxCacheSource reports how the currently installed browser cache got
// there: "bundled" if the browser-version.json marker (copied verbatim from
// the bundle by ensureCamoufoxCacheFromBundle) is present in the cache dir,
// "downloaded" if the cache is populated without that marker (camoufox's own
// fetcher never writes it), or "" if no browser is installed at all.
func camoufoxCacheSource() string {
	installDir := camoufoxInstallDir()
	if !fileExists(filepath.Join(installDir, "camoufox.exe")) {
		return ""
	}
	if fileExists(filepath.Join(installDir, "browser-version.json")) {
		return "bundled"
	}
	return "downloaded"
}
