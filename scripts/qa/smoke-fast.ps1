$ErrorActionPreference = "Continue"
$root = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$log = Join-Path $env:TEMP "sencia-smoke-log.txt"
"" | Set-Content $log

function Log($msg) {
    $line = "[$(Get-Date -Format 'HH:mm:ss')] $msg"
    Add-Content $log $line
    Write-Output $line
}

$go = Join-Path $root ".tools\go\bin\go.exe"
if (-not (Test-Path $go)) { $go = "go" }  # vendored toolchain optional; fall back to PATH
if (-not (Test-Path )) {  = "go" }
$backend = Join-Path $root "apps/backend-go"
$exe = Join-Path $backend "bin\sencia-job-backend.exe"
$worker = Join-Path $root "apps\browser-worker\worker.py"
$token = "smoke-fast"
$db = Join-Path $env:TEMP "sencia-smoke-fast.db"
$base = "http://127.0.0.1:48730"
$hdr = @{ Authorization = "Bearer $token"; "Content-Type" = "application/json" }
$proc = $null

Log "=== SMOKE RAPIDO SENCIA JOB ==="

# 1 Build + tests
& $go -C $backend build -o bin\sencia-job-backend.exe .\cmd\sencia-job 2>&1 | Out-Null
Log $(if ($LASTEXITCODE -eq 0) { "PASS go build" } else { "FAIL go build exit=$LASTEXITCODE" })
$testOut = & $go -C $backend test ./... -count=1 2>&1 | Out-String
Log $(if ($LASTEXITCODE -eq 0) { "PASS go test" } else { "FAIL go test: $testOut" })
Push-Location $root
npm run typecheck 2>&1 | Out-Null; Log $(if ($LASTEXITCODE -eq 0) { "PASS typecheck" } else { "FAIL typecheck" })
Pop-Location

# 2 Worker
$inp = '{"cmd":"start","headless":true}'+"`n"+'{"cmd":"fetch","url":"https://www.linkedin.com/jobs-guest/jobs/api/seeMoreJobPostings/search?keywords=devops&location=Brazil&f_WT=2&f_TPR=r86400&start=0","waitUntil":"domcontentloaded"}'+"`n"+'{"cmd":"close"}'
$wout = $inp | py -3 $worker 2>&1
$fetch = ($wout | Where-Object { $_ -match '"html"' } | Select-Object -First 1 | ConvertFrom-Json)
Log $(if ($fetch.html.Length -gt 5000) { "PASS worker linkedin html=$($fetch.html.Length)" } else { "FAIL worker" })

# 3 API
Remove-Item $db -ErrorAction SilentlyContinue
$env:SENCIA_API_TOKEN = $token; $env:SENCIA_DB_PATH = $db; $env:SENCIA_RADAR_DISABLED = "1"
$proc = Start-Process $exe -WorkingDirectory $backend -PassThru -WindowStyle Hidden
Start-Sleep 2
try { $h = Invoke-RestMethod "$base/health" -TimeoutSec 5; Log "PASS health $($h.status)" } catch { Log "FAIL health $($_.Exception.Message)" }

# Resume
try {
  $b64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes("AWS Terraform Kubernetes Docker Linux"))
  $up = Invoke-RestMethod "$base/api/v1/resume" -Method POST -Headers $hdr -Body (@{fileName="t.txt";mimeType="text/plain";contentBase64=$b64}|ConvertTo-Json -Compress) -TimeoutSec 30
  Log "PASS resume upload keywords=$($up.keywords.Count)"
} catch { Log "FAIL resume $($_.Exception.Message)" }

# LinkedIn search 24h max 2 jobs
$cfg = '{"version":1,"form":{"roles":"Software Engineer","levels":"Pleno","recentHours":24,"maxJobs":2,"maxDelaySeconds":1,"linkedinPages":1,"scoreCut":40,"remoteCountry":"Brazil","workMode":"remote","keywords":"Python, AWS, Linux","excludedLevels":"Staff","maxYears":8},"toggles":{"useLinkedin":true,"useIndeed":false,"useGupy":false,"headless":true,"compatibility":false,"remoteOnly":true},"localItems":{"jobs":0,"applications":0,"history":0}}'
Invoke-RestMethod "$base/api/v1/config" -Method PUT -Headers $hdr -Body $cfg -TimeoutSec 10 | Out-Null
Log "PASS config saved"
$sw = [System.Diagnostics.Stopwatch]::StartNew()
try {
  $s = Invoke-RestMethod "$base/api/v1/search" -Method POST -Headers $hdr -Body "{}" -TimeoutSec 180
  $sw.Stop()
  $logs = Invoke-RestMethod "$base/api/v1/logs" -Headers @{Authorization="Bearer $token"} -TimeoutSec 10
  $filt = ($logs.logs | Where-Object { $_.message -match 'FILTER' } | Select-Object -Last 1).message
  Log "PASS linkedin search $($sw.Elapsed.TotalSeconds.ToString('F0'))s jobs=$($s.jobs.Count) | $filt"
  foreach ($j in $s.jobs) { Log "  JOB score=$($j.score) $($j.title) @ $($j.company)" }
} catch { $sw.Stop(); Log "FAIL linkedin search $($sw.Elapsed.TotalSeconds.ToString('F0'))s $($_.Exception.Message)" }

# Indeed quick (max 1 job, 120s timeout)
$cfg2 = '{"version":1,"form":{"roles":"DevOps Engineer","recentHours":24,"maxJobs":1,"maxDelaySeconds":1,"scoreCut":40,"remoteCountry":"Brazil","workMode":"remote","keywords":"Linux"},"toggles":{"useLinkedin":false,"useIndeed":true,"useGupy":false,"headless":true,"compatibility":false,"remoteOnly":true},"localItems":{"jobs":0,"applications":0,"history":0}}'
Invoke-RestMethod "$base/api/v1/config" -Method PUT -Headers $hdr -Body $cfg2 -TimeoutSec 10 | Out-Null
$sw2 = [System.Diagnostics.Stopwatch]::StartNew()
try {
  $s2 = Invoke-RestMethod "$base/api/v1/search" -Method POST -Headers $hdr -Body "{}" -TimeoutSec 120
  $sw2.Stop()
  $logs2 = Invoke-RestMethod "$base/api/v1/logs" -Headers @{Authorization="Bearer $token"}
  $indeed = ($logs2.logs | Where-Object { $_.message -match 'INDEED|Indeed' } | Select-Object -Last 2 | ForEach-Object { $_.message }) -join " | "
  Log "PASS indeed search $($sw2.Elapsed.TotalSeconds.ToString('F0'))s jobs=$($s2.jobs.Count) | $indeed"
} catch { $sw2.Stop(); Log "WARN indeed search $($sw2.Elapsed.TotalSeconds.ToString('F0'))s timeout/erro: $($_.Exception.Message)" }

# Gupy quick
$cfg3 = '{"version":1,"form":{"roles":"DevOps","recentHours":168,"maxJobs":1,"maxDelaySeconds":1,"scoreCut":40,"remoteCountry":"Brazil","workMode":"remote","keywords":"Linux"},"toggles":{"useLinkedin":false,"useIndeed":false,"useGupy":true,"headless":true,"compatibility":false,"remoteOnly":true},"localItems":{"jobs":0,"applications":0,"history":0}}'
Invoke-RestMethod "$base/api/v1/config" -Method PUT -Headers $hdr -Body $cfg3 -TimeoutSec 10 | Out-Null
$sw3 = [System.Diagnostics.Stopwatch]::StartNew()
try {
  $s3 = Invoke-RestMethod "$base/api/v1/search" -Method POST -Headers $hdr -Body "{}" -TimeoutSec 120
  $sw3.Stop()
  $logs3 = Invoke-RestMethod "$base/api/v1/logs" -Headers @{Authorization="Bearer $token"}
  $gupy = ($logs3.logs | Where-Object { $_.message -match 'GUPY|Gupy' } | Select-Object -Last 2 | ForEach-Object { $_.message }) -join " | "
  Log "PASS gupy search $($sw3.Elapsed.TotalSeconds.ToString('F0'))s jobs=$($s3.jobs.Count) | $gupy"
} catch { $sw3.Stop(); Log "WARN gupy search $($sw3.Elapsed.TotalSeconds.ToString('F0'))s timeout/erro: $($_.Exception.Message)" }

if ($proc -and -not $proc.HasExited) { Stop-Process $proc.Id -Force }
Log "=== FIM ==="
Get-Content $log
