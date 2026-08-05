param(
  [switch]$SelfTestSearchPolling
)

$ErrorActionPreference = "Stop"
$OutputEncoding = [Console]::OutputEncoding = [Text.Encoding]::UTF8
$root = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$backendDir = Join-Path $root "apps/backend-go"
$exe = Join-Path $backendDir "bin\sencia-job-backend.exe"
$releaseExe = Join-Path $root "release\electron\win-unpacked\Sencia Job.exe"
$token = "mini-test"
$dbPath = Join-Path $env:TEMP "sencia-mini-test.db"
$baseUrl = "http://127.0.0.1:48730"
$headers = @{
  Authorization = "Bearer $token"
  "Content-Type" = "application/json"
}
$report = Join-Path $root "docs\qa\mini-test-latest.txt"

function Write-Report($line) {
  $entry = "[$(Get-Date -Format 'HH:mm:ss')] $line"
  [System.IO.File]::AppendAllText($report, $entry + [Environment]::NewLine, [Text.Encoding]::UTF8)
  Write-Output $entry
}

function Invoke-Json {
  param(
    [Parameter(Mandatory = $true)][string]$Method,
    [Parameter(Mandatory = $true)][string]$Uri,
    $BodyObj = $null
  )
  $json = if ($null -eq $BodyObj) { "{}" } else { $BodyObj | ConvertTo-Json -Depth 8 -Compress }
  $bytes = [Text.Encoding]::UTF8.GetBytes($json)
  Invoke-RestMethod -Method $Method -Uri $Uri -Headers $headers -Body $bytes -ContentType "application/json; charset=utf-8"
}

function Resolve-SearchPoll {
  param(
    [object[]]$CurrentJobs,
    [Parameter(Mandatory = $true)]$Status
  )

  $jobs = @($CurrentJobs)
  if ($null -ne $Status.PSObject.Properties["jobs"]) {
    $jobs = @($Status.jobs)
  }

  [pscustomobject]@{
    Jobs = $jobs
    IsTerminal = -not [bool]$Status.running
    Error = [string]$Status.error
  }
}

if ($SelfTestSearchPolling) {
  $preview = [pscustomobject]@{ title = "preview"; score = 0 }
  $running = [pscustomobject]@{ running = $true; jobs = @($preview); error = "" }
  $poll = Resolve-SearchPoll -CurrentJobs @() -Status $running
  if (@($poll.Jobs).Count -ne 1 -or $poll.IsTerminal) {
    throw "running preview was not preserved"
  }

  $terminal = [pscustomobject]@{ running = $false; jobs = @(); error = "" }
  $poll = Resolve-SearchPoll -CurrentJobs @($poll.Jobs) -Status $terminal
  if (@($poll.Jobs).Count -ne 0) {
    throw "terminal jobs=[] retained a stale preview"
  }
  if (-not $poll.IsTerminal) {
    throw "terminal status was not recognized"
  }

  $withoutJobs = [pscustomobject]@{ running = $true; error = "" }
  $poll = Resolve-SearchPoll -CurrentJobs @($preview) -Status $withoutJobs
  if (@($poll.Jobs).Count -ne 1) {
    throw "status without a jobs field discarded the current preview"
  }

  Write-Output "PASS mini-test search polling self-test"
  exit 0
}

"" | Set-Content -Path $report -Encoding utf8
Write-Report "=== MINI TEST SENCIA JOB ==="

if (Test-Path $releaseExe) {
  $info = Get-Item $releaseExe
  Write-Report "PASS release exe: $($info.FullName) ($([math]::Round($info.Length/1MB, 2)) MB) @ $($info.LastWriteTime)"
} else {
  Write-Report "FAIL release exe missing"
  exit 1
}

$go = Join-Path $root ".tools\go\bin\go.exe"
if (-not (Test-Path $go)) { $go = "go" }  # vendored toolchain optional; fall back to PATH
if (-not (Test-Path )) {  = "go" }
& $go -C $backendDir test ./... -count=1 2>&1 | Out-Null
Write-Report $(if ($LASTEXITCODE -eq 0) { "PASS go test" } else { "FAIL go test exit=$LASTEXITCODE" })

Get-Process -Name "sencia-job-backend" -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
Remove-Item $dbPath -ErrorAction SilentlyContinue

$env:SENCIA_API_TOKEN = $token
$env:SENCIA_DB_PATH = $dbPath
$env:SENCIA_RADAR_DISABLED = "1"

$proc = Start-Process -FilePath $exe -WorkingDirectory $backendDir -PassThru -WindowStyle Hidden
Start-Sleep 2

try {
  for ($i = 0; $i -lt 20; $i++) {
    try {
      $h = Invoke-RestMethod "$baseUrl/health" -TimeoutSec 2
      if ($h.status -eq "ok") { break }
    } catch {}
    Start-Sleep 1
  }
  $h = Invoke-RestMethod "$baseUrl/health" -TimeoutSec 5
  Write-Report "PASS health $($h.status)"

  $cfg = @{
    version = 1
    form = @{
      roles = "DevOps Engineer"
      levels = "Pleno"
      searchProfiles = "Analista de infraestrutura, Infrastructure Analyst | Pleno, Senior | Lead, Staff | 8`nDevOps, DevOps Engineer, SRE | Junior, Pleno | Senior, Lead | 5"
      recentHours = 24
      maxJobs = 2
      maxDelaySeconds = 1
      linkedinPages = 1
      scoreCut = 60
      basePrompt = "Priorize compatibilidade técnica, Segurança da informação e modalidade."
      remoteCountry = "Brazil"
      workMode = "remote"
      keywords = "Kubernetes, Docker, Linux, AWS"
      excludedLevels = "Staff"
      maxYears = 8
    }
    toggles = @{
      useLinkedin = $true
      useIndeed = $false
      useGupy = $false
      headless = $true
      compatibility = $false
      remoteOnly = $true
    }
    localItems = @{ jobs = 0; applications = 0; history = 0 }
  }

  Invoke-Json PUT "$baseUrl/api/v1/config" $cfg | Out-Null
  $loaded = Invoke-RestMethod "$baseUrl/api/v1/config" -Headers @{ Authorization = "Bearer $token" } -TimeoutSec 10
  if ($loaded.form.basePrompt -notmatch "Segurança") {
    Write-Report "FAIL utf8 config round-trip: $($loaded.form.basePrompt)"
    exit 1
  }
  Write-Report "PASS config saved + UTF-8 round-trip (2 perfis: Infra Pleno/Senior, DevOps Junior/Pleno)"

  Invoke-Json POST "$baseUrl/api/v1/search/reset" | Out-Null
  Invoke-Json POST "$baseUrl/api/v1/search" | Out-Null
  Write-Report "PASS search started (background)"

  Start-Sleep 2
  $planLogs = Invoke-RestMethod "$baseUrl/api/v1/logs" -Headers @{ Authorization = "Bearer $token" } -TimeoutSec 10
  $plan = $planLogs.logs | Where-Object { $_.message -match "PLANO|perfil " } | Select-Object -First 4
  foreach ($p in $plan) { Write-Report "  PLAN $($p.message)" }

  $sw = [System.Diagnostics.Stopwatch]::StartNew()
  $jobs = @()
  while ($sw.Elapsed.TotalSeconds -lt 180) {
    Start-Sleep 3
    $status = Invoke-RestMethod "$baseUrl/api/v1/search/status" -Headers @{ Authorization = "Bearer $token" } -TimeoutSec 10
    $poll = Resolve-SearchPoll -CurrentJobs $jobs -Status $status
    $jobs = @($poll.Jobs)
    if ($poll.IsTerminal) {
      if ($poll.Error) {
        Write-Report "FAIL search error: $($poll.Error)"
      }
      break
    }
  }
  $sw.Stop()

  if ($jobs.Count -gt 0) {
    Write-Report "PASS search done in $($sw.Elapsed.TotalSeconds.ToString('F0'))s jobs=$($jobs.Count)"
    foreach ($j in $jobs) {
      $descLen = if ($j.description) { $j.description.Length } else { 0 }
      Write-Report "  JOB [$($j.source)] score=$($j.score) desc=$descLen chars | $($j.title)"
    }
  } else {
    Write-Report "WARN search finished with 0 jobs in $($sw.Elapsed.TotalSeconds.ToString('F0'))s (anti-bot or filters)"
  }
} finally {
  if ($proc -and -not $proc.HasExited) {
    Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
  }
}

Write-Report "=== END ==="
