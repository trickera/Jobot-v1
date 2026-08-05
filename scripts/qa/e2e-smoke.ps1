$ErrorActionPreference = "Stop"
$root = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$backendDir = Join-Path $root "apps/backend-go"
$exe = Join-Path $backendDir "bin\sencia-job-backend.exe"
$token = "e2e-test-token"
$dbPath = Join-Path $env:TEMP "sencia-e2e-smoke.db"
$logOut = Join-Path $env:TEMP "sencia-e2e-backend.log"
$baseUrl = "http://127.0.0.1:48730"
$headers = @{
  Authorization = "Bearer $token"
  "Content-Type" = "application/json"
}

Remove-Item $dbPath -ErrorAction SilentlyContinue
Remove-Item $logOut -ErrorAction SilentlyContinue

$env:SENCIA_API_TOKEN = $token
$env:SENCIA_DB_PATH = $dbPath
$env:SENCIA_RADAR_DISABLED = "1"

Write-Host "=== E2E SMOKE TEST ==="
Write-Host "Starting backend..."

$proc = Start-Process -FilePath $exe -WorkingDirectory $backendDir -PassThru -RedirectStandardOutput $logOut -WindowStyle Hidden

function Wait-Health {
  param([int]$Seconds = 30)
  for ($i = 0; $i -lt $Seconds; $i++) {
    try {
      $r = Invoke-RestMethod -Uri "$baseUrl/health" -TimeoutSec 2
      if ($r.status -eq "ok") { return $true }
    } catch {}
    Start-Sleep -Seconds 1
  }
  return $false
}

if (-not (Wait-Health)) {
  Write-Host "FAIL: backend health check timeout"
  if (-not $proc.HasExited) { Stop-Process -Id $proc.Id -Force }
  Get-Content $logOut -ErrorAction SilentlyContinue | Select-Object -Last 20
  exit 1
}
Write-Host "OK: health"

$config = @{
  version = 1
  form = @{
    source = "LinkedIn"
    provider = "Gemini"
    model = "gemini-2.5-flash"
    role = "DevOps Engineer"
    roles = "DevOps Engineer, SRE"
    levels = "Pleno, Senior"
    excludedLevels = "Tech Lead, Lead, Staff, Principal, Manager"
    maxYears = 8
    location = "Brazil"
    workMode = "remote"
    onsiteLocation = ""
    remoteCountry = "Brazil"
    keywords = "AWS, Terraform, Linux, Docker, Kubernetes, CI/CD"
    recentHours = 24
    maxJobs = 3
    maxDelaySeconds = 2
    linkedinPages = 1
    scoreCut = 50
    rankingMode = "compatibilidade"
  }
  toggles = @{
    remoteOnly = $true
    useLinkedin = $true
    useIndeed = $false
    useGupy = $false
    headless = $true
    compatibility = $false
    score = $true
    localOnly = $true
    saveHistory = $true
    autoClean = $false
    radarMode = $false
  }
  localItems = @{ jobs = 0; applications = 0; history = 0 }
} | ConvertTo-Json -Depth 6 -Compress

Invoke-RestMethod -Uri "$baseUrl/api/v1/config" -Method Put -Headers $headers -Body $config | Out-Null
Write-Host "OK: config saved"

$sw = [System.Diagnostics.Stopwatch]::StartNew()
try {
  Invoke-RestMethod -Uri "$baseUrl/api/v1/search" -Method Post -Headers $headers -Body "{}" -TimeoutSec 30 | Out-Null
} catch {
  $sw.Stop()
  Write-Host "FAIL: search request - $($_.Exception.Message)"
  if ($_.ErrorDetails.Message) { Write-Host $_.ErrorDetails.Message }
  if (-not $proc.HasExited) { Stop-Process -Id $proc.Id -Force }
  Get-Content $logOut -ErrorAction SilentlyContinue | Select-Object -Last 30
  exit 1
}

$search = $null
while ($sw.Elapsed.TotalSeconds -lt 600) {
  Start-Sleep -Seconds 2
  $search = Invoke-RestMethod -Uri "$baseUrl/api/v1/search/status" -Headers $headers
  if (-not $search.running) { break }
}
$sw.Stop()

Write-Host "OK: search completed in $($sw.Elapsed.TotalSeconds.ToString('F1'))s"
Write-Host "MESSAGE: $($search.message)"
Write-Host "JOBS_FOUND: $($search.jobs.Count)"

$idx = 0
foreach ($job in $search.jobs) {
  $idx++
  Write-Host "--- Job $idx ---"
  Write-Host "  title: $($job.title)"
  Write-Host "  company: $($job.company)"
  Write-Host "  source: $($job.source)"
  Write-Host "  location: $($job.location)"
  Write-Host "  score: $($job.score)"
  Write-Host "  status: $($job.status)"
  if ($job.missingKeywords -and $job.missingKeywords.Count -gt 0) {
    Write-Host "  missing: $($job.missingKeywords -join ', ')"
  }
}

$logs = Invoke-RestMethod -Uri "$baseUrl/api/v1/logs" -Headers @{ Authorization = "Bearer $token" }
Write-Host "LOG_ENTRIES: $($logs.logs.Count)"
$logs.logs | Select-Object -Last 12 | ForEach-Object {
  Write-Host "  [$($_.level)] $($_.message)"
}

$state = Invoke-RestMethod -Uri "$baseUrl/api/v1/state" -Headers @{ Authorization = "Bearer $token" }
Write-Host "STATE: status=$($state.status) jobs=$($state.jobs) history=$($state.history)"

if (-not $proc.HasExited) { Stop-Process -Id $proc.Id -Force }
Write-Host "=== E2E COMPLETE ==="

if ($search.jobs.Count -eq 0) {
  Write-Host "WARN: zero jobs returned (pipeline ran but no matches above threshold)"
  exit 2
}
exit 0
