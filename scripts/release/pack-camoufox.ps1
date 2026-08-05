param(
  [switch]$Force
)

# Builds resources/camoufox/ - a pre-fetched Camoufox stealth-Firefox browser
# binary bundled into the installer so a clean Windows machine never has to
# perform the ~1 GB first-run download over an unpredictable network
# (installer hardening, Phase 1). Bundled by electron-builder via the
# "extraResources" entry in apps/desktop/electron-builder.yml; copied into the user's
# writable browser cache on first run by apps/backend-go/internal/server/
# browser_bundle.go (resolveCamoufoxBundle / ensureCamoufoxCacheFromBundle).
#
# Unlike pack-python.ps1 (which pins an exact CPython version+SHA-256), this
# script always fetches whatever Camoufox release the vendored `camoufox`
# Python package itself considers current/compatible - there is no separate
# hash to pin against because camoufox's own installer
# (camoufox.pkgman.CamoufoxFetcher) resolves and downloads the release
# directly from GitHub. Re-run with -Force to refresh to the latest release.
#
# Camoufox resolves its install/cache directory via platformdirs'
# user_cache_dir("camoufox"), which normally maps to
# %LOCALAPPDATA%\camoufox\camoufox\Cache. platformdirs supports an official
# override for this on Windows: the WIN_PD_OVERRIDE_LOCAL_APPDATA env var
# (see platformdirs/windows.py get_win_folder). This script (and the Go
# runtime, at first-run copy and at worker-spawn time) uses that override to
# redirect Camoufox's cache into an app-controlled directory instead of
# patching the vendored package.

$ErrorActionPreference = "Stop"
$root = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path

$bundledPython = Join-Path $root "resources\python\python.exe"
if (-not (Test-Path $bundledPython)) {
  Write-Error "pack-camoufox: $bundledPython not found - run scripts/release/pack-python.ps1 first (Camoufox is fetched using the bundled Python's own camoufox package)."
  exit 1
}

$targetDir = Join-Path $root "resources\camoufox"
$versionMarker = Join-Path $targetDir "browser-version.json"

if (-not $Force -and (Test-Path (Join-Path $targetDir "camoufox.exe")) -and (Test-Path $versionMarker)) {
  Write-Output "pack-camoufox: resources/camoufox already populated ($((Get-Content $versionMarker -Raw).Trim())). Use -Force to refetch the latest release."
  exit 0
}

$stageRoot = Join-Path $root ".tools\camoufox-cache"
if (Test-Path $stageRoot) {
  Remove-Item $stageRoot -Recurse -Force
}
New-Item -ItemType Directory -Force -Path $stageRoot | Out-Null

Write-Output "pack-camoufox: fetching the latest compatible Camoufox release (this downloads the ~530 MB browser + ~66 MB GeoIP DB)..."
$previousOverride = $env:WIN_PD_OVERRIDE_LOCAL_APPDATA
try {
  $env:WIN_PD_OVERRIDE_LOCAL_APPDATA = $stageRoot
  & $bundledPython -m camoufox fetch
  $fetchExit = $LASTEXITCODE
} finally {
  if ($null -eq $previousOverride) {
    Remove-Item Env:\WIN_PD_OVERRIDE_LOCAL_APPDATA -ErrorAction SilentlyContinue
  } else {
    $env:WIN_PD_OVERRIDE_LOCAL_APPDATA = $previousOverride
  }
}
if ($fetchExit -ne 0) {
  Write-Error "pack-camoufox: 'python -m camoufox fetch' failed with exit code $fetchExit"
  exit 1
}

# platformdirs' Windows "opinion" appends <appauthor>\<appname>\Cache; camoufox
# calls user_cache_dir("camoufox") with no explicit appauthor, so appauthor
# defaults to appname - the real install dir is nested twice.
$stagedInstallDir = Join-Path $stageRoot "camoufox\camoufox\Cache"
$stagedExe = Join-Path $stagedInstallDir "camoufox.exe"
$stagedVersionJson = Join-Path $stagedInstallDir "version.json"
if (-not (Test-Path $stagedExe) -or -not (Test-Path $stagedVersionJson)) {
  Write-Error "pack-camoufox: fetch completed but $stagedExe or $stagedVersionJson is missing - camoufox's own install layout may have changed."
  exit 1
}

if (Test-Path $targetDir) {
  Remove-Item $targetDir -Recurse -Force
}
New-Item -ItemType Directory -Force -Path $targetDir | Out-Null

# Flat mirror of camoufox's own INSTALL_DIR layout (camoufox.exe, browser\,
# addons\, version.json, ... directly under resources/camoufox) so the Go
# first-run copy step can xcopy this directory straight into the resolved
# user cache dir without any path rewriting.
Copy-Item -Path (Join-Path $stagedInstallDir "*") -Destination $targetDir -Recurse -Force

$versionInfo = Get-Content $stagedVersionJson -Raw | ConvertFrom-Json
$builtAt = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
$marker = [ordered]@{
  engine  = "camoufox"
  version = $versionInfo.version
  release = $versionInfo.release
  source  = "bundled"
  builtAt = $builtAt
}
# -Encoding utf8 on Windows PowerShell 5.1 writes a UTF-8 BOM, which Go's
# encoding/json does not skip. This file must stay strict-JSON-parseable, so
# write it as plain ASCII (all field values here are ASCII-safe).
($marker | ConvertTo-Json -Compress) | Set-Content -Path $versionMarker -Encoding ascii -NoNewline

Remove-Item $stageRoot -Recurse -Force

$sizeMB = [math]::Round(((Get-ChildItem $targetDir -Recurse -File | Measure-Object -Property Length -Sum).Sum / 1MB), 1)
Write-Output "pack-camoufox: done. $targetDir ($sizeMB MB, Camoufox $($versionInfo.version)-$($versionInfo.release))."
