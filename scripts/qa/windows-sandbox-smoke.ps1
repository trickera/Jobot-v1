param(
  [int]$TimeoutMinutes = 15
)

# Installer hardening Phase 6: run the installer smoke inside a real,
# disposable Windows Sandbox instance - the only way to prove the installer
# survives a machine with genuinely zero prior state (registry, AppData,
# Python/Node/Go, Camoufox cache) rather than a faked-clean profile on the
# dev machine (which is what clean-install-smoke.ps1 does instead).
#
# Detects whether the Windows Sandbox optional feature is available/enabled
# without requiring elevation; if it isn't, explains exactly how to turn it
# on and exits with a distinct SKIPPED status rather than failing the whole
# installer hardening pass, per the plan's explicit allowance for this.

$ErrorActionPreference = "Stop"
$root = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$reportRoot = Join-Path $root "qa-artifacts\windows-sandbox-smoke-$(Get-Date -Format 'yyyy-MM-dd')"
New-Item -ItemType Directory -Force -Path $reportRoot | Out-Null

function Log($msg) {
  Write-Output "[$(Get-Date -Format 'HH:mm:ss')] $msg"
}

$sandboxExe = Join-Path $env:WINDIR "System32\WindowsSandbox.exe"
if (-not (Test-Path $sandboxExe)) {
  Log "SKIPPED: Windows Sandbox is not installed/enabled on this machine ($sandboxExe not found)."
  Log ""
  Log "To enable it (requires admin + a reboot):"
  Log "  1. Settings > Apps > Optional features > More Windows features"
  Log "  2. Check 'Windows Sandbox', click OK, reboot."
  Log "  Or, in an elevated PowerShell:"
  Log "    Enable-WindowsOptionalFeature -Online -FeatureName Containers-DisposableClientVM -All"
  Log ""
  Log "Windows Sandbox also requires: Windows 10/11 Pro or Enterprise, virtualization enabled in"
  Log "BIOS/UEFI, and (per Microsoft docs) is not guaranteed to work inside an already-virtualized"
  Log "host (nested virtualization) - some cloud/VM dev machines cannot run it at all."
  Log ""
  Log "This step is optional for the installer hardening pass per the plan; the win-unpacked"
  Log "clean-profile smoke (scripts/qa/clean-install-smoke.ps1) already covers the runtime hardening"
  Log "code paths. Run this script again on a machine with Windows Sandbox enabled for the"
  Log "strongest possible signal (a truly clean OS, not a faked-clean profile)."
  exit 2
}

$setupExe = Get-ChildItem (Join-Path $root "release\electron") -Filter "Sencia Job Setup*.exe" -ErrorAction SilentlyContinue | Select-Object -First 1
if (-not $setupExe) {
  Log "FAIL: no 'Sencia Job Setup*.exe' found under release\electron. Run 'npm run release:electron' first."
  exit 1
}

$stageDir = Join-Path $env:TEMP "sencia-sandbox-stage-$(Get-Date -Format 'yyyyMMddHHmmss')"
New-Item -ItemType Directory -Force -Path $stageDir | Out-Null
Copy-Item $setupExe.FullName (Join-Path $stageDir $setupExe.Name) -Force
Copy-Item (Join-Path $PSScriptRoot "sandbox-inner-smoke.ps1") (Join-Path $stageDir "sandbox-inner-smoke.ps1") -Force

$wsbTemplate = Get-Content (Join-Path $PSScriptRoot "windows-sandbox-smoke.wsb") -Raw
$wsbContent = $wsbTemplate.Replace("{{STAGE_DIR}}", $stageDir).Replace("{{REPORT_DIR}}", $reportRoot)
$wsbPath = Join-Path $stageDir "run.wsb"
Set-Content -Path $wsbPath -Value $wsbContent -Encoding utf8

Log "Staged installer + inner smoke script at $stageDir"
Log "Launching Windows Sandbox with $wsbPath (timeout ${TimeoutMinutes}m)..."
Start-Process -FilePath $sandboxExe -ArgumentList "`"$wsbPath`""

$reportPath = Join-Path $reportRoot "sandbox-report.json"
$deadline = (Get-Date).AddMinutes($TimeoutMinutes)
while (-not (Test-Path $reportPath) -and (Get-Date) -lt $deadline) {
  Start-Sleep -Seconds 5
}

if (-not (Test-Path $reportPath)) {
  Log "FAIL: timed out after ${TimeoutMinutes}m waiting for $reportPath. The Sandbox window may still be open - close it manually."
  exit 1
}

$report = Get-Content $reportPath -Raw | ConvertFrom-Json
Log "Sandbox smoke verdict: $($report.verdict)"
if ($report.errors -and $report.errors.Count -gt 0) {
  foreach ($err in $report.errors) { Log "  error: $err" }
}
Log "Full report: $reportPath"

if ($report.verdict -ne "PASS") { exit 1 }
exit 0
