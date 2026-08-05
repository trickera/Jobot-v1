package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCamoufoxCacheOverrideRootDevModeUntouched(t *testing.T) {
	t.Setenv("SENCIA_CAMOUFOX_CACHE", "")
	t.Setenv("SENCIA_PACKAGED", "")
	if got := camoufoxCacheOverrideRoot(); got != "" {
		t.Fatalf("expected empty override in dev mode, got %q", got)
	}
}

func TestCamoufoxCacheOverrideRootExplicit(t *testing.T) {
	t.Setenv("SENCIA_CAMOUFOX_CACHE", `C:\fake\browser`)
	if got := camoufoxCacheOverrideRoot(); got != `C:\fake\browser` {
		t.Fatalf("expected explicit override to win, got %q", got)
	}
}

func TestCamoufoxCacheOverrideRootPackagedDefault(t *testing.T) {
	t.Setenv("SENCIA_CAMOUFOX_CACHE", "")
	t.Setenv("SENCIA_PACKAGED", "1")
	t.Setenv("SENCIA_APP_DATA", `C:\fake\appdata\Sencia Job`)
	want := filepath.Join(`C:\fake\appdata\Sencia Job`, "Sencia Job", "browser")
	if got := camoufoxCacheOverrideRoot(); got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestCamoufoxInstallDirNoOverrideMatchesLegacyCacheDir(t *testing.T) {
	t.Setenv("SENCIA_CAMOUFOX_CACHE", "")
	t.Setenv("SENCIA_PACKAGED", "")
	t.Setenv("LOCALAPPDATA", `C:\fake\localappdata`)
	got := camoufoxInstallDir()
	want := camoufoxCacheDir()
	if got != want {
		t.Fatalf("expected camoufoxInstallDir to match camoufoxCacheDir with no override, got %s want %s", got, want)
	}
}

func TestCamoufoxInstallDirWithOverride(t *testing.T) {
	t.Setenv("SENCIA_CAMOUFOX_CACHE", `C:\fake\browser`)
	want := filepath.Join(`C:\fake\browser`, "camoufox", "camoufox", "Cache")
	if got := camoufoxInstallDir(); got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestCamoufoxProcEnvEmptyWithoutOverride(t *testing.T) {
	t.Setenv("SENCIA_CAMOUFOX_CACHE", "")
	t.Setenv("SENCIA_PACKAGED", "")
	if env := camoufoxProcEnv(); env != nil {
		t.Fatalf("expected nil extra env in dev mode, got %v", env)
	}
}

func TestCamoufoxProcEnvSetsOverride(t *testing.T) {
	t.Setenv("SENCIA_CAMOUFOX_CACHE", `C:\fake\browser`)
	env := camoufoxProcEnv()
	if len(env) != 1 || env[0] != `WIN_PD_OVERRIDE_LOCAL_APPDATA=C:\fake\browser` {
		t.Fatalf("expected single WIN_PD_OVERRIDE_LOCAL_APPDATA entry, got %v", env)
	}
}

func TestResolveCamoufoxBundleDirExplicitOverride(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "camoufox.exe"), []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SENCIA_CAMOUFOX_BUNDLE", dir)

	got, ok := resolveCamoufoxBundleDir()
	if !ok || got != dir {
		t.Fatalf("expected (%s, true), got (%s, %v)", dir, got, ok)
	}
}

func TestResolveCamoufoxBundleDirExplicitOverrideMissingExe(t *testing.T) {
	t.Setenv("SENCIA_CAMOUFOX_BUNDLE", t.TempDir())
	if _, ok := resolveCamoufoxBundleDir(); ok {
		t.Fatal("expected false when the override dir has no camoufox.exe")
	}
}

func TestEnsureCamoufoxCacheFromBundleCopiesOnce(t *testing.T) {
	bundleDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(bundleDir, "camoufox.exe"), []byte("fake-browser"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(bundleDir, "browser"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "browser", "asset.bin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "browser-version.json"), []byte(`{"engine":"camoufox"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SENCIA_CAMOUFOX_BUNDLE", bundleDir)

	cacheRoot := t.TempDir()
	t.Setenv("SENCIA_CAMOUFOX_CACHE", cacheRoot)
	installDir := camoufoxInstallDir()

	var logs []string
	copied, err := ensureCamoufoxCacheFromBundle(func(line string) { logs = append(logs, line) })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !copied {
		t.Fatal("expected copied=true on first call with an empty cache")
	}
	if len(logs) == 0 {
		t.Fatal("expected at least one progress log line")
	}
	if !fileExists(filepath.Join(installDir, "camoufox.exe")) {
		t.Fatal("expected camoufox.exe to exist in the install dir after copy")
	}
	if !fileExists(filepath.Join(installDir, "browser", "asset.bin")) {
		t.Fatal("expected nested browser/asset.bin to exist in the install dir after copy")
	}

	// Second call must be a no-op: mutate the cached exe first so a
	// re-copy would be detectable, then confirm it is left untouched.
	if err := os.WriteFile(filepath.Join(installDir, "camoufox.exe"), []byte("user-updated"), 0o755); err != nil {
		t.Fatal(err)
	}
	copied, err = ensureCamoufoxCacheFromBundle(nil)
	if err != nil {
		t.Fatalf("unexpected error on second call: %v", err)
	}
	if copied {
		t.Fatal("expected copied=false when the cache is already populated")
	}
	data, err := os.ReadFile(filepath.Join(installDir, "camoufox.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "user-updated" {
		t.Fatal("expected the second call to leave an already-populated cache untouched")
	}
}

func TestEnsureCamoufoxCacheFromBundleNoBundleAvailable(t *testing.T) {
	t.Setenv("SENCIA_CAMOUFOX_BUNDLE", t.TempDir())
	t.Setenv("SENCIA_CAMOUFOX_CACHE", t.TempDir())

	copied, err := ensureCamoufoxCacheFromBundle(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if copied {
		t.Fatal("expected copied=false when no bundle is available")
	}
}

func TestCamoufoxCacheSource(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("SENCIA_CAMOUFOX_CACHE", cacheRoot)
	installDir := camoufoxInstallDir()

	if got := camoufoxCacheSource(); got != "" {
		t.Fatalf("expected empty source with no browser installed, got %q", got)
	}

	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "camoufox.exe"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := camoufoxCacheSource(); got != "downloaded" {
		t.Fatalf(`expected "downloaded" without a browser-version.json marker, got %q`, got)
	}

	if err := os.WriteFile(filepath.Join(installDir, "browser-version.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := camoufoxCacheSource(); got != "bundled" {
		t.Fatalf(`expected "bundled" with a browser-version.json marker, got %q`, got)
	}
}

func TestCamoufoxBundleVersionReadsMarker(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "camoufox.exe"), []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := `{"engine":"camoufox","version":"135.0.1","release":"beta.24","source":"bundled","builtAt":"2026-07-09T16:36:07Z"}`
	if err := os.WriteFile(filepath.Join(dir, "browser-version.json"), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SENCIA_CAMOUFOX_BUNDLE", dir)

	if got := camoufoxBundleVersion(); got != marker {
		t.Fatalf("expected marker contents to round-trip, got %q", got)
	}
}

func TestCamoufoxBundleVersionNoBundle(t *testing.T) {
	t.Setenv("SENCIA_CAMOUFOX_BUNDLE", t.TempDir())
	if got := camoufoxBundleVersion(); got != "" {
		t.Fatalf("expected empty string with no bundle, got %q", got)
	}
}
