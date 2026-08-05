$ErrorActionPreference = "Stop"
$root = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$log = Join-Path $env:TEMP "sencia-commercial-install-smoke.txt"
"" | Set-Content $log -Encoding utf8

function Log($msg) {
  $line = "[$(Get-Date -Format 'HH:mm:ss')] $msg"
  Add-Content $log $line
  Write-Output $line
}

Log "=== COMMERCIAL INSTALL SMOKE (CH-01) ==="
Log "Automates checks 1-2 (bundled python resolves without a global Python on PATH)."
Log "Checks 3-4 (real Camoufox bootstrap over the internet, real search) are MANUAL - see wave5.md Task 7."

# --- 1. Bundled python must exist (build it if missing) ---
$bundledPython = Join-Path $root "resources\python\python.exe"
if (-not (Test-Path $bundledPython)) {
  Log "Bundled python not found - running pack-python.ps1..."
  powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $root "scripts\release\pack-python.ps1")
  if ($LASTEXITCODE -ne 0) {
    Log "FAIL pack-python.ps1 exit=$LASTEXITCODE"
    exit 1
  }
}
if (Test-Path $bundledPython) {
  Log "PASS bundled python present: $bundledPython"
} else {
  Log "FAIL bundled python still missing after pack-python.ps1"
  exit 1
}

# --- 2. Build the backend and stage it exactly like the electron-builder NSIS bundle would ---
$go = Join-Path $root ".tools\go\bin\go.exe"
if (-not (Test-Path $go)) { $go = "go" }  # vendored toolchain optional; fall back to PATH
if (-not (Test-Path )) {  = "go" }
$backendDir = Join-Path $root "apps/backend-go"
Push-Location $root
try {
  & $go -C $backendDir build -o bin\sencia-job-backend.exe .\cmd\sencia-job
  if ($LASTEXITCODE -ne 0) {
    Log "FAIL go build exit=$LASTEXITCODE"
    exit 1
  }
  Log "PASS backend build"
} finally {
  Pop-Location
}

$stageDir = Join-Path $env:TEMP "sencia-commercial-install-stage"
if (Test-Path $stageDir) { Remove-Item $stageDir -Recurse -Force }
New-Item -ItemType Directory -Force -Path $stageDir | Out-Null

Copy-Item (Join-Path $backendDir "bin\sencia-job-backend.exe") (Join-Path $stageDir "sencia-job-backend.exe") -Force

# Junctions (not copies) so this doesn't duplicate the ~200 MB python bundle
# or the browser worker script on every smoke run.
$stageResources = Join-Path $stageDir "resources"
New-Item -ItemType Directory -Force -Path $stageResources | Out-Null
cmd /c mklink /J "`"$stageResources\python`"" "`"$(Join-Path $root 'resources\python')`"" | Out-Null
cmd /c mklink /J "`"$stageDir\backend-browser`"" "`"$(Join-Path $root 'apps\browser-worker')`"" | Out-Null

if (-not (Test-Path (Join-Path $stageResources "python\python.exe"))) {
  Log "FAIL staged resources/python junction did not resolve"
  exit 1
}
Log "PASS staged install layout at $stageDir (resources/python + backend-browser junctioned)"

# --- 3. Run it exactly like a clean-machine install: no SENCIA_PYTHON override ---
Remove-Item Env:\SENCIA_PYTHON -ErrorAction SilentlyContinue
$token = "commercial-smoke"
$dbPath = Join-Path $env:TEMP "sencia-commercial-install-smoke.db"
Remove-Item $dbPath -ErrorAction SilentlyContinue
$env:SENCIA_API_TOKEN = $token
$env:SENCIA_DB_PATH = $dbPath
$env:SENCIA_RADAR_DISABLED = "1"
$headers = @{ Authorization = "Bearer $token" }
$base = "http://127.0.0.1:48730"

$proc = Start-Process -FilePath (Join-Path $stageDir "sencia-job-backend.exe") -WorkingDirectory $stageDir -PassThru -WindowStyle Hidden
try {
  Start-Sleep -Seconds 2
  $health = $null
  for ($i = 0; $i -lt 10; $i++) {
    try { $health = Invoke-RestMethod "$base/health" -TimeoutSec 2; break } catch { Start-Sleep 1 }
  }
  if ($health.status -eq "ok") {
    Log "PASS /health from the staged install"
  } else {
    Log "FAIL /health did not respond from the staged install"
    exit 1
  }

  $browserHealth = Invoke-RestMethod "$base/api/v1/browser/health" -Headers $headers -TimeoutSec 20
  Log "browser/health: $($browserHealth | ConvertTo-Json -Compress)"

  if (-not $browserHealth.pythonFound) {
    Log "FAIL pythonFound=false from the staged install (bundled python not resolved)"
    exit 1
  }
  if ($browserHealth.pythonPath -notlike "*resources\python\python.exe") {
    Log "FAIL pythonPath did not resolve to the bundled resources/python (got: $($browserHealth.pythonPath)) - a global Python may have been used instead"
    exit 1
  }
  Log "PASS pythonFound=true via the BUNDLED interpreter ($($browserHealth.pythonPath)), not a global install"

  if ($browserHealth.workerFound) {
    Log "PASS workerFound=true (backend-browser/worker.py resolved via the staged junction)"
  } else {
    Log "FAIL workerFound=false"
    exit 1
  }
} finally {
  if ($proc -and -not $proc.HasExited) {
    Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
  }
}

Log ""
Log "=== MANUAL STEPS STILL REQUIRED (not automated here) ==="
Log "3. Bootstrap Camoufox for real (needs internet): POST $base/api/v1/browser/bootstrap, poll"
Log "   GET $base/api/v1/browser/bootstrap/status until done=true, then browserInstalled=true on /browser/health."
Log "4. Run a real short search (maxJobs=1) against a configured source and confirm HTML or a clear error."
Log "5. GATE HUMANO: build the real NSIS installer (npm run release:electron) and run it end-to-end on a"
Log "   clean machine or VM without Python/Node/Go installed - this script only stages the backend exe"
Log "   next to the bundled resources, it does not exercise the actual installer or a clean OS."
Log "=== END ==="
