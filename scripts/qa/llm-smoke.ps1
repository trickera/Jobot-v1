param(
  [string]$ApiKey = $env:GEMINI_API_KEY,
  [int]$MaxJobs = 2,
  [string]$Roles = "DevOps Engineer, SRE",
  [string]$Keywords = "AWS, Terraform, Kubernetes, Docker, Linux",
  [int]$RecentHours = 168,
  [string]$Address = "127.0.0.1:48734"
)

$ErrorActionPreference = "Stop"

if ([string]::IsNullOrWhiteSpace($ApiKey)) {
  Write-Host "FAIL: passe -ApiKey ou defina GEMINI_API_KEY."
  exit 1
}

if ($MaxJobs -lt 1) { $MaxJobs = 1 }
if ($MaxJobs -gt 3) { $MaxJobs = 3 }

$root = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$backendDir = Join-Path $root "apps/backend-go"
$exe = Join-Path $backendDir "bin\sencia-job-backend.exe"
$token = "llm-smoke"
$dbPath = Join-Path $env:TEMP "sencia-llm-smoke.db"
$baseUrl = "http://$Address"
$headers = @{
  Authorization = "Bearer $token"
  "Content-Type" = "application/json"
}

if (-not (Test-Path $exe)) {
  Write-Host "Backend exe nao encontrado. Rode: npm run backend:build"
  exit 1
}

Remove-Item $dbPath -ErrorAction SilentlyContinue

$oldToken = $env:SENCIA_API_TOKEN
$oldDb = $env:SENCIA_DB_PATH
$oldRadar = $env:SENCIA_RADAR_DISABLED
$oldAddress = $env:SENCIA_ADDRESS

$env:SENCIA_API_TOKEN = $token
$env:SENCIA_DB_PATH = $dbPath
$env:SENCIA_RADAR_DISABLED = "1"
$env:SENCIA_ADDRESS = $Address

$proc = $null
try {
  $proc = Start-Process -FilePath $exe -WorkingDirectory $backendDir -PassThru -WindowStyle Hidden

  $healthy = $false
  for ($i = 0; $i -lt 30; $i++) {
    try {
      $health = Invoke-RestMethod -Uri "$baseUrl/health" -TimeoutSec 2
      if ($health.status -eq "ok") { $healthy = $true; break }
    } catch {}
    Start-Sleep -Seconds 1
  }
  if (-not $healthy) { throw "backend nao respondeu em $baseUrl" }

  $config = @{
    version = 1
    form = @{
      provider = "Gemini"
      model = "gemini-2.5-flash"
      apiKey = $ApiKey
      roles = $Roles
      levels = "Junior, Pleno, Senior"
      excludedLevels = "Tech Lead, Lead, Staff, Principal, Manager"
      maxYears = 8
      workMode = "remote"
      remoteCountry = "Brazil"
      keywords = $Keywords
      recentHours = $RecentHours
      maxJobs = $MaxJobs
      maxDelaySeconds = 1
      linkedinPages = 1
      scoreCut = 60
      rankingMode = "compatibilidade"
    }
    toggles = @{
      remoteOnly = $true
      useLinkedin = $true
      useIndeed = $true
      useGupy = $false
      headless = $true
      compatibility = $true
      score = $true
      localOnly = $true
      saveHistory = $false
      autoClean = $false
      radarMode = $false
    }
    localItems = @{ jobs = 0; applications = 0; history = 0 }
  } | ConvertTo-Json -Depth 6 -Compress

  Invoke-RestMethod -Uri "$baseUrl/api/v1/config" -Method Put -Headers $headers -Body $config | Out-Null
  Invoke-RestMethod -Uri "$baseUrl/api/v1/search" -Method Post -Headers $headers -Body "{}" | Out-Null

  $sw = [System.Diagnostics.Stopwatch]::StartNew()
  $status = $null
  while ($sw.Elapsed.TotalSeconds -lt 600) {
    Start-Sleep -Seconds 2
    $status = Invoke-RestMethod -Uri "$baseUrl/api/v1/search/status" -Headers $headers
    if (-not $status.running) { break }
  }
  $sw.Stop()

  Write-Host "LLM smoke concluido em $($sw.Elapsed.TotalSeconds.ToString('F1'))s"
  Write-Host "Mensagem: $($status.message)"
  Write-Host "Vagas: $($status.jobs.Count)"
  Write-Host "Diagnostico: coletadas=$($status.diagnostics.collected) analisadas=$($status.diagnostics.evaluated) semDescricao=$($status.diagnostics.skippedNoDescription)"
  foreach ($job in $status.jobs) {
    Write-Host "- [$($job.source)] score=$($job.score) $($job.status) $($job.title) @ $($job.company)"
  }

  $logs = Invoke-RestMethod -Uri "$baseUrl/api/v1/logs" -Headers @{ Authorization = "Bearer $token" }
  $llmLogs = $logs.logs | Where-Object { $_.message -match '\[ LLM \]|Gemini|score falhou' } | Select-Object -Last 8
  foreach ($entry in $llmLogs) {
    Write-Host "LOG $($entry.level): $($entry.message)"
  }

  if ($status.error) { exit 2 }
  exit 0
} finally {
  if ($proc -and -not $proc.HasExited) {
    Stop-Process -Id $proc.Id -Force
  }
  Remove-Item $dbPath -ErrorAction SilentlyContinue
  $env:SENCIA_API_TOKEN = $oldToken
  $env:SENCIA_DB_PATH = $oldDb
  $env:SENCIA_RADAR_DISABLED = $oldRadar
  $env:SENCIA_ADDRESS = $oldAddress
}
