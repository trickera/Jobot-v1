param(
  [switch]$InstallReal,
  [switch]$SkipUninstall
)

# Installer hardening Phase 5: repeatable local clean-install simulation.
#
# Default mode drives the packaged win-unpacked build
# (release/electron/win-unpacked/Sencia Job.exe) through a genuinely
# isolated Windows profile (fresh APPDATA/LOCALAPPDATA/TEMP, PATH stripped
# to system dirs only) via scripts/qa/clean-install-smoke.mjs (CDP-driven,
# adapted from the proven qa-artifacts/clean-electron-smoke/clean-smoke.mjs
# pattern). This exercises 100% of the runtime hardening (bundled
# resources, packaged paths, install health/repair, Camoufox bundle-first,
# process lifecycle) without touching the real Windows registry or the
# current user's real AppData/Programs folders.
#
# -InstallReal additionally runs the real NSIS installer
# (release/electron/Sencia Job Setup *.exe) silently against THIS machine's
# real per-user install location (electron-builder's default NSIS template
# is oneClick + per-user, so no admin is required, but it is a real,
# persistent change - Start Menu shortcut, registry uninstall entry, files
# under %LOCALAPPDATA%\Programs) and uninstalls it afterward unless
# -SkipUninstall is passed. Only use this with the user's explicit
# go-ahead; the win-unpacked mode above is the safe default and covers the
# same runtime code paths.

$ErrorActionPreference = "Stop"
$root = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$reportDir = Join-Path $root "qa-artifacts\clean-install-smoke-$(Get-Date -Format 'yyyy-MM-dd')"
New-Item -ItemType Directory -Force -Path $reportDir | Out-Null

function Log($msg) {
  Write-Output "[$(Get-Date -Format 'HH:mm:ss')] $msg"
}

$unpackedExe = Join-Path $root "release\electron\win-unpacked\Sencia Job.exe"
if (-not (Test-Path $unpackedExe)) {
  Log "FAIL: $unpackedExe not found. Run 'npm run release:electron' first."
  exit 1
}

Log "=== CLEAN INSTALL SMOKE (win-unpacked) ==="
node (Join-Path $PSScriptRoot "clean-install-smoke.mjs") --exe "$unpackedExe" --reportDir "$reportDir" --label "win-unpacked" --debugPort 9334
$unpackedExit = $LASTEXITCODE
if ($unpackedExit -eq 0) {
  Log "PASS win-unpacked clean-profile smoke"
} else {
  Log "FAIL win-unpacked clean-profile smoke (exit=$unpackedExit) - see $reportDir\win-unpacked-report.md"
}

$overallExit = $unpackedExit

if ($InstallReal) {
  Log "=== CLEAN INSTALL SMOKE (real NSIS installer, -InstallReal) ==="
  $setupExe = Get-ChildItem (Join-Path $root "release\electron") -Filter "Sencia Job Setup*.exe" -ErrorAction SilentlyContinue | Select-Object -First 1
  if (-not $setupExe) {
    Log "FAIL: no 'Sencia Job Setup*.exe' found under release\electron. Run 'npm run release:electron' first."
    exit 1
  }

  Log "Installing silently: $($setupExe.FullName) /S"
  $installProc = Start-Process -FilePath $setupExe.FullName -ArgumentList "/S" -PassThru -Wait
  if ($installProc.ExitCode -ne 0) {
    Log "FAIL silent install exit=$($installProc.ExitCode)"
    exit 1
  }

  # electron-builder's default NSIS per-user template installs under
  # %LOCALAPPDATA%\Programs\<productName>.
  $installedExe = Join-Path $env:LOCALAPPDATA "Programs\Sencia Job\Sencia Job.exe"
  if (-not (Test-Path $installedExe)) {
    Log "FAIL: expected installed exe not found at $installedExe (electron-builder NSIS layout may differ - check release\electron\builder-debug.yml)"
    exit 1
  }
  Log "PASS installed exe found at $installedExe"

  node (Join-Path $PSScriptRoot "clean-install-smoke.mjs") --exe "$installedExe" --reportDir "$reportDir" --label "nsis-installed" --debugPort 9335
  $installedExit = $LASTEXITCODE
  if ($installedExit -eq 0) {
    Log "PASS real-install clean-profile smoke"
  } else {
    Log "FAIL real-install clean-profile smoke (exit=$installedExit) - see $reportDir\nsis-installed-report.md"
  }
  if ($installedExit -ne 0) { $overallExit = $installedExit }

  if (-not $SkipUninstall) {
    $uninstallExe = Join-Path $env:LOCALAPPDATA "Programs\Sencia Job\Uninstall Sencia Job.exe"
    if (Test-Path $uninstallExe) {
      Log "Uninstalling: $uninstallExe /S"
      Start-Process -FilePath $uninstallExe -ArgumentList "/S" -PassThru -Wait | Out-Null
      if (Test-Path $installedExe) {
        Log "WARN: installed exe still present after uninstall ($installedExe) - check manually."
      } else {
        Log "PASS uninstall removed the installed app."
      }
    } else {
      Log "WARN: uninstaller not found at $uninstallExe - skipping automatic uninstall."
    }
  } else {
    Log "SkipUninstall set - leaving the real install in place."
  }
}

Log "=== DONE (exit=$overallExit) ==="
exit $overallExit
