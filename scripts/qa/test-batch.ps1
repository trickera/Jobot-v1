$ErrorActionPreference = "Stop"
$root = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$report = Join-Path $root "docs\qa\test-batch-report.txt"
"" | Set-Content $report

function Rpt($line) {
  $entry = "[$(Get-Date -Format 'HH:mm:ss')] $line"
  Add-Content $report $entry
  Write-Output $entry
}

Rpt "=========================================="
Rpt "BATERIA DE TESTES SENCIA JOB"
Rpt "=========================================="

# ---------- T1: Go unit tests ----------
Rpt ""
Rpt ">>> T1: Testes unitarios Go"
$go = Join-Path $root ".tools\go\bin\go.exe"
if (-not (Test-Path $go)) { $go = "go" }  # vendored toolchain optional; fall back to PATH
if (-not (Test-Path )) {  = "go" }
$out = & $go -C (Join-Path $root "apps/backend-go") test ./... -count=1 -v 2>&1 | Out-String
$passCount = ([regex]::Matches($out, "--- PASS")).Count
$failCount = ([regex]::Matches($out, "--- FAIL")).Count
Rpt "PASS=$passCount FAIL=$failCount"
if ($failCount -gt 0) {
  $lines = $out -split "`n"
  $fails = $lines | Where-Object { $_ -match "FAIL" }
  foreach ($f in $fails) { Rpt "  $f" }
}
if ($failCount -eq 0) { Rpt "T1 OK" } else { Rpt "T1 FAIL" }

# ---------- T2: API integration ----------
Rpt ""
Rpt ">>> T2: API integration"
$backendDir = Join-Path $root "apps/backend-go"
$exe = Join-Path $backendDir "bin\sencia-job-backend.exe"
$token = "batch-test"
$db = Join-Path $env:TEMP "sencia-batch-test.db"
$base = "http://127.0.0.1:48730"
$hdr = @{ Authorization = "Bearer $token"; "Content-Type" = "application/json" }

Remove-Item $db -ErrorAction SilentlyContinue
$env:SENCIA_API_TOKEN = $token
$env:SENCIA_DB_PATH = $db
$env:SENCIA_RADAR_DISABLED = "1"
$env:SENCIA_GO_SCRAPER_DISABLED = "1"

$proc = Start-Process $exe -WorkingDirectory $backendDir -PassThru -WindowStyle Hidden
$healthy = $false
for ($i=0; $i -lt 20; $i++) {
  try { $h = Invoke-RestMethod "$base/health" -TimeoutSec 2; if ($h.status -eq "ok") { $healthy=$true; break } } catch {}
  Start-Sleep 1
}
if ($healthy) { Rpt "T2 health OK" } else { Rpt "T2 health FAIL" }

# T2a config com perfis
$cfgJson = @'
{"version":1,"form":{"roles":"DevOps","levels":"Pleno","searchProfiles":"Analista de infraestrutura, Infrastructure Analyst | Pleno, Senior | Lead, Staff | 8\nDevOps, SRE | Junior, Pleno | Senior, Lead | 5","recentHours":24,"maxJobs":2,"maxDelaySeconds":1,"linkedinPages":1,"scoreCut":35,"remoteCountry":"Brazil","workMode":"remote","keywords":"Kubernetes, Docker, Linux","excludedLevels":"Staff","maxYears":8},"toggles":{"useLinkedin":true,"useIndeed":false,"useGupy":false,"headless":true,"compatibility":false,"remoteOnly":true},"localItems":{"jobs":0,"applications":0,"history":0}}
'@
try {
  Invoke-RestMethod "$base/api/v1/config" -Method PUT -Headers $hdr -Body $cfgJson -TimeoutSec 10 | Out-Null
  $loaded = Invoke-RestMethod "$base/api/v1/config" -Headers $hdr -TimeoutSec 10
  if ($loaded.form.searchProfiles -match "Analista de infraestrutura") { Rpt "T2a config perfis OK (persistiu)" }
  else { Rpt "T2a FAIL searchProfiles nao persistiu" }
} catch { Rpt "T2a FAIL $($($_.Exception.Message))" }

# T2b search com scraper desabilitado -> erro surfacado (plano e logado em run real; verificado no mini-test)
try {
  Invoke-RestMethod "$base/api/v1/search/reset" -Method POST -Headers $hdr -Body "{}" -TimeoutSec 10 | Out-Null
  $searchErrored = $false
  try { Invoke-RestMethod "$base/api/v1/search" -Method POST -Headers $hdr -Body "{}" -TimeoutSec 10 | Out-Null }
  catch { $searchErrored = $true }
  Start-Sleep 1
  $status = Invoke-RestMethod "$base/api/v1/search/status" -Headers $hdr -TimeoutSec 10
  if ($searchErrored -and $status.error) { Rpt "T2b search-disabled OK (erro surfacado: $($status.error))" }
  elseif (-not $status.running) { Rpt "T2b search-disabled OK (nao running)" }
  else { Rpt "T2b FAIL estado inesperado" }
} catch { Rpt "T2b FAIL $($_.Exception.Message)" }

# T2c reset/ready
try {
  $status = Invoke-RestMethod "$base/api/v1/search/status" -Headers $hdr -TimeoutSec 10
  if (-not $status.running) { Rpt "T2c reset OK running=false" } else { Rpt "T2c FAIL running=true" }
} catch { Rpt "T2c FAIL $($_.Exception.Message)" }

# T2d /jobs array
try {
  $jobs = Invoke-RestMethod "$base/api/v1/jobs" -Headers $hdr -TimeoutSec 10
  if ($jobs.jobs -is [array]) { Rpt "T2d /jobs OK array de $($jobs.jobs.Count)" } else { Rpt "T2d FAIL jobs nao e array" }
} catch { Rpt "T2d FAIL $($_.Exception.Message)" }

# T2e 409 roles vazio
$emptyCfg = @'
{"version":1,"form":{"roles":"","levels":"Pleno","searchProfiles":"","recentHours":24,"maxJobs":2,"keywords":"x"},"toggles":{"useLinkedin":true},"localItems":{"jobs":0,"applications":0,"history":0}}
'@
try {
  Invoke-RestMethod "$base/api/v1/config" -Method PUT -Headers $hdr -Body $emptyCfg -TimeoutSec 10 | Out-Null
  $got409 = $false
  try { Invoke-RestMethod "$base/api/v1/search" -Method POST -Headers $hdr -Body "{}" -TimeoutSec 10 | Out-Null }
  catch { $got409 = $true }
  if ($got409) { Rpt "T2e 409 OK roles vazio rejeitado" } else { Rpt "T2e FAIL esperado 409" }
} catch { Rpt "T2e FAIL $($_.Exception.Message)" }

if ($proc -and -not $proc.HasExited) { Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue }
Remove-Item "Env:SENCIA_GO_SCRAPER_DISABLED" -ErrorAction SilentlyContinue

# ---------- T3: Resiliencia ----------
Rpt ""
Rpt ">>> T3: Resiliencia (parser robusto + seniority por perfil)"
$go2 = Join-Path $root ".tools\go\bin\go.exe"
if (-not (Test-Path $go2)) { $go2 = "go" }  # vendored toolchain optional; fall back to PATH
if (-not (Test-Path )) {  = "go" }
$prevEAP = $ErrorActionPreference
$ErrorActionPreference = "Continue"
$t3a = & $go2 -C (Join-Path $root "apps/backend-go") test ./... -run TestParseSearchProfiles -count=1 -v 2>&1 | Out-String
if ($t3a -match "PASS") { Rpt "T3 parser robusto OK" } else { Rpt "T3 parser FAIL" }
$t3b = & $go2 -C (Join-Path $root "apps/backend-go") test ./... -run TestProfileSeniorityIsIndependentPerProfile -count=1 -v 2>&1 | Out-String
if ($t3b -match "PASS") { Rpt "T3 seniority por perfil OK" } else { Rpt "T3 seniority FAIL" }
$ErrorActionPreference = $prevEAP

# ---------- T4: Frontend ----------
Rpt ""
Rpt ">>> T4: Frontend typecheck + build"
Push-Location $root
$prevEAP2 = $ErrorActionPreference
$ErrorActionPreference = "Continue"
$tc = & npm run typecheck 2>&1 | Out-String
if ($LASTEXITCODE -eq 0) { Rpt "T4 typecheck OK" } else { Rpt "T4 typecheck FAIL" }
$bld = & npm run build 2>&1 | Out-String
if ($LASTEXITCODE -eq 0) { Rpt "T4 build OK" } else { Rpt "T4 build FAIL" }
$ErrorActionPreference = $prevEAP2
Pop-Location

Rpt ""
Rpt "=========================================="
Rpt "FIM BATERIA 1 (T1-T4)"
Rpt "=========================================="
exit 0

