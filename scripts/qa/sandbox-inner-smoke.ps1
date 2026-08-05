# Runs INSIDE Windows Sandbox (staged read-only under
# C:\Users\WDAGUtilityAccount\Desktop\stage by windows-sandbox-smoke.ps1's
# LogonCommand). This is a genuinely clean, disposable Windows profile with
# no Python/Node/Go/repo/prior Camoufox cache at all - the real target
# environment installer hardening is meant to survive. Unlike
# clean-install-smoke.ps1 (which only fakes a clean profile via env var
# overrides on the dev machine), this script can safely run the REAL NSIS
# installer with its REAL default install location, since the whole OS
# instance is discarded when the Sandbox closes.

$ErrorActionPreference = "Stop"
$stageDir = "C:\Users\WDAGUtilityAccount\Desktop\stage"
$reportDir = "C:\Users\WDAGUtilityAccount\Desktop\sandbox-report"
New-Item -ItemType Directory -Force -Path $reportDir | Out-Null

$report = [ordered]@{
  startedAt = (Get-Date).ToString("o")
  verdict   = "UNKNOWN"
  checks    = [ordered]@{}
  errors    = @()
}

function Save-Report {
  ($report | ConvertTo-Json -Depth 10) | Set-Content -Path (Join-Path $reportDir "sandbox-report.json") -Encoding utf8
}

try {
  $setupExe = Get-ChildItem $stageDir -Filter "Sencia Job Setup*.exe" -ErrorAction SilentlyContinue | Select-Object -First 1
  if (-not $setupExe) { throw "No 'Sencia Job Setup*.exe' found under $stageDir" }

  Write-Output "Installing silently: $($setupExe.FullName) /S"
  $installProc = Start-Process -FilePath $setupExe.FullName -ArgumentList "/S" -PassThru -Wait
  $report.checks.installExitCode = $installProc.ExitCode
  if ($installProc.ExitCode -ne 0) { throw "Silent install failed with exit code $($installProc.ExitCode)" }

  $installedExe = Join-Path $env:LOCALAPPDATA "Programs\Sencia Job\Sencia Job.exe"
  if (-not (Test-Path $installedExe)) { throw "Installed exe not found at $installedExe" }
  $report.checks.installedExePath = "found"

  Write-Output "Launching $installedExe"
  $appProc = Start-Process -FilePath $installedExe -PassThru
  $token = $env:SENCIA_API_TOKEN
  $base = "http://127.0.0.1:48730"
  $headers = @{}

  $health = $null
  for ($i = 0; $i -lt 30; $i++) {
    try { $health = Invoke-RestMethod "$base/health" -TimeoutSec 2; break } catch { Start-Sleep 1 }
  }
  if (-not $health -or $health.status -ne "ok") { throw "Backend /health did not respond" }
  $report.checks.backendHealth = "ok"

  # The app generates a per-run token exposed to the renderer via IPC, not
  # readable from PowerShell without a CDP connection. This inner smoke
  # keeps it deliberately simple (no CDP) and validates the installed
  # binary boots and serves /health on a genuinely clean OS; it defers the
  # full authenticated API walkthrough to clean-install-smoke.ps1's
  # CDP-driven flow, which windows-sandbox-smoke.ps1's caller can also stage
  # into the Sandbox if a deeper run is needed.
  Start-Sleep -Seconds 5

  Write-Output "Closing app"
  if (-not $appProc.HasExited) {
    Stop-Process -Id $appProc.Id -Force -ErrorAction SilentlyContinue
  }
  Start-Sleep -Seconds 3

  $orphans = Get-Process -Name "sencia-job-backend", "python", "camoufox", "Sencia Job" -ErrorAction SilentlyContinue
  $report.checks.orphanProcesses = @($orphans | ForEach-Object { @{ name = $_.Name; id = $_.Id } })
  $report.orphanFree = ($orphans.Count -eq 0)

  $report.verdict = if ($report.orphanFree) { "PASS" } else { "FAIL" }
} catch {
  $report.errors += $_.Exception.Message
  $report.verdict = "FAIL"
} finally {
  $report.finishedAt = (Get-Date).ToString("o")
  Save-Report
  Write-Output "Sandbox inner smoke verdict: $($report.verdict)"
}
