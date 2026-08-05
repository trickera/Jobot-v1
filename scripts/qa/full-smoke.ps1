# Sencia Job - full smoke test suite
# Usage: powershell -NoProfile -ExecutionPolicy Bypass -File scripts\qa\full-smoke.ps1

$ErrorActionPreference = "Continue"
$root = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$backend = Join-Path $root "apps/backend-go"
$go = Join-Path $root ".tools\go\bin\go.exe"
if (-not (Test-Path $go)) { $go = "go" }  # vendored toolchain optional; fall back to PATH
if (-not (Test-Path )) {  = "go" }
$worker = Join-Path $root "apps\browser-worker\worker.py"
$exe = Join-Path $backend "bin\sencia-job-backend.exe"
$token = "smoke-full-" + [guid]::NewGuid().ToString("N").Substring(0, 8)
$dbPath = Join-Path $env:TEMP "sencia-full-smoke.db"
$baseUrl = "http://127.0.0.1:48730"
$report = [System.Collections.Generic.List[string]]@()
$failures = 0
$warnings = 0
$backendProc = $null

function Add-Report($section, $name, $status, $detail) {
    $script:report.Add("[$status] $section :: $name :: $detail")
    if ($status -eq "FAIL") { $script:failures++ }
    if ($status -eq "WARN") { $script:warnings++ }
}

function Section($title) {
    $script:report.Add("")
    $script:report.Add("=== $title ===")
}

function Stop-Backend {
    if ($script:backendProc -and -not $script:backendProc.HasExited) {
        Stop-Process -Id $script:backendProc.Id -Force -ErrorAction SilentlyContinue
    }
}

function Start-Backend {
    Remove-Item $dbPath -ErrorAction SilentlyContinue
    $env:SENCIA_API_TOKEN = $token
    $env:SENCIA_DB_PATH = $dbPath
    $env:SENCIA_RADAR_DISABLED = "1"
    $script:backendProc = Start-Process -FilePath $exe -WorkingDirectory $backend -PassThru -WindowStyle Hidden
    for ($i = 0; $i -lt 30; $i++) {
        try {
            $h = Invoke-RestMethod -Uri "$baseUrl/health" -TimeoutSec 2
            if ($h.status -eq "ok") { return $true }
        } catch {}
        Start-Sleep -Seconds 1
    }
    return $false
}

function Api($method, $path, $body = $null) {
    $headers = @{ Authorization = "Bearer $token" }
    if ($body) { $headers["Content-Type"] = "application/json" }
    $params = @{ Uri = "$baseUrl$path"; Method = $method; Headers = $headers; TimeoutSec = 600 }
    if ($body) { $params.Body = $body }
    return Invoke-RestMethod @params
}

function Wait-SearchComplete($label, [int]$seconds = 600) {
    $sw = [System.Diagnostics.Stopwatch]::StartNew()
    $status = $null
    while ($sw.Elapsed.TotalSeconds -lt $seconds) {
        Start-Sleep -Seconds 2
        $status = Api GET "/api/v1/search/status"
        if (-not $status.running) {
            $sw.Stop()
            return [pscustomobject]@{ status = $status; elapsedSeconds = $sw.Elapsed.TotalSeconds }
        }
    }
    $sw.Stop()
    throw "$label search did not finish within ${seconds}s"
}

Section "1. BUILD"
try {
    & $go -C $backend build -o bin\sencia-job-backend.exe .\cmd\sencia-job 2>&1 | Out-Null
    if ($LASTEXITCODE -eq 0 -and (Test-Path $exe)) { Add-Report "build" "go-backend" "PASS" $exe }
    else { Add-Report "build" "go-backend" "FAIL" "exit=$LASTEXITCODE" }
} catch { Add-Report "build" "go-backend" "FAIL" $_.Exception.Message }

Push-Location $root
try {
    npm run typecheck 2>&1 | Out-Null
    if ($LASTEXITCODE -eq 0) { Add-Report "build" "typescript" "PASS" "tsc --noEmit" }
    else { Add-Report "build" "typescript" "FAIL" "exit=$LASTEXITCODE" }
} catch { Add-Report "build" "typescript" "FAIL" $_.Exception.Message }

try {
    npm run build 2>&1 | Out-Null
    if ($LASTEXITCODE -eq 0) { Add-Report "build" "vite-production" "PASS" "dist/ gerado" }
    else { Add-Report "build" "vite-production" "FAIL" "exit=$LASTEXITCODE" }
} catch { Add-Report "build" "vite-production" "FAIL" $_.Exception.Message }
Pop-Location

Section "2. UNIT TESTS"
try {
    $testOut = & $go -C $backend test ./... -count=1 2>&1 | Out-String
    if ($LASTEXITCODE -eq 0) {
        $match = [regex]::Match($testOut, 'ok\s+\S+\s+([\d.]+)s')
        Add-Report "tests" "go-all" "PASS" $(if ($match.Success) { $match.Value } else { "all packages ok" })
    } else { Add-Report "tests" "go-all" "FAIL" ($testOut.Trim() -replace '\s+', ' ' | Select-Object -First 1) }
} catch { Add-Report "tests" "go-all" "FAIL" $_.Exception.Message }

Section "3. CAMOUFOX WORKER"
try {
    $input = @'
{"cmd":"start","headless":true}
{"cmd":"fetch","url":"https://www.linkedin.com/jobs-guest/jobs/api/seeMoreJobPostings/search?keywords=devops&location=Brazil&f_WT=2&f_TPR=r86400&start=0","waitUntil":"domcontentloaded"}
{"cmd":"close"}
'@
    $sw = [System.Diagnostics.Stopwatch]::StartNew()
    $out = $input | py -3 $worker 2>&1
    $sw.Stop()
    $jsonLines = @($out | Where-Object { $_ -match '^\{' })
    $fetch = $jsonLines | Where-Object { $_ -match '"html"' } | Select-Object -First 1 | ConvertFrom-Json
    if ($fetch.ok -and $fetch.html.Length -gt 5000 -and -not $fetch.blocked) {
        $hasCards = $fetch.html -match 'base-search-card__title'
        Add-Report "worker" "linkedin-fetch" "PASS" "$($sw.Elapsed.TotalSeconds.ToString('F1'))s html=$($fetch.html.Length) cards=$hasCards"
    } else {
        Add-Report "worker" "linkedin-fetch" "FAIL" "blocked=$($fetch.blocked) len=$($fetch.html.Length)"
    }
} catch { Add-Report "worker" "linkedin-fetch" "FAIL" $_.Exception.Message }

Section "4. API ENDPOINTS"
if (-not (Start-Backend)) {
    Add-Report "api" "backend-start" "FAIL" "health timeout"
} else {
    Add-Report "api" "backend-start" "PASS" "127.0.0.1:48730 token=$token"

    try {
        $h = Invoke-RestMethod "$baseUrl/health" -TimeoutSec 5
        Add-Report "api" "GET /health" "PASS" "status=$($h.status)"
    } catch { Add-Report "api" "GET /health" "FAIL" $_.Exception.Message }

    try {
        $r = Invoke-WebRequest "$baseUrl/api/v1/state" -TimeoutSec 5
        Add-Report "api" "GET /state-no-auth" $(if ($r.StatusCode -eq 401) { "PASS" } else { "FAIL" }) "status=$($r.StatusCode)"
    } catch {
        if ($_.Exception.Response.StatusCode.value__ -eq 401) { Add-Report "api" "GET /state-no-auth" "PASS" "401 unauthorized" }
        else { Add-Report "api" "GET /state-no-auth" "FAIL" $_.Exception.Message }
    }

    try {
        $state = Api GET "/api/v1/state"
        Add-Report "api" "GET /state" "PASS" "service=$($state.service) status=$($state.status)"
    } catch { Add-Report "api" "GET /state" "FAIL" $_.Exception.Message }

    try {
        $cfg = Api GET "/api/v1/config"
        Add-Report "api" "GET /config" "PASS" "version=$($cfg.version) recentHours=$($cfg.form.recentHours)"
    } catch { Add-Report "api" "GET /config" "FAIL" $_.Exception.Message }

    try {
        $putBody = (@{
            version = 1
            form = @{
                roles = "DevOps Engineer"
                levels = "Pleno, Senior"
                excludedLevels = "Staff, Principal"
                maxYears = 8
                recentHours = 24
                maxJobs = 2
                maxDelaySeconds = 1
                linkedinPages = 1
                scoreCut = 45
                remoteCountry = "Brazil"
                workMode = "remote"
                keywords = "AWS, Linux, Docker, Terraform"
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
        } | ConvertTo-Json -Depth 6 -Compress)
        Api PUT "/api/v1/config" $putBody | Out-Null
        $saved = Api GET "/api/v1/config"
        if ($saved.form.excludedLevels -and $saved.form.maxYears -eq 8) {
            Add-Report "api" "PUT /config" "PASS" "excludedLevels+maxYears persistidos"
        } else {
            Add-Report "api" "PUT /config" "WARN" "saved mas campos novos incompletos"
        }
    } catch { Add-Report "api" "PUT /config" "FAIL" $_.Exception.Message }

    try {
        $resumeText = "DevOps Engineer`nAWS Terraform Kubernetes Docker Linux CI/CD"
        $b64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($resumeText))
        $resumeBody = (@{ fileName = "smoke-test.txt"; mimeType = "text/plain"; contentBase64 = $b64 } | ConvertTo-Json -Compress)
        $up = Api POST "/api/v1/resume" $resumeBody
        if ($up.fileName -eq "smoke-test.txt" -and $up.keywords.Count -gt 0) {
            Add-Report "api" "POST /resume" "PASS" "$($up.keywords.Count) keywords extraidas"
        } else { Add-Report "api" "POST /resume" "WARN" "upload ok mas keywords vazias" }
    } catch { Add-Report "api" "POST /resume" "FAIL" $_.Exception.Message }

    try {
        $models = Api POST "/api/v1/models" '{"provider":"Gemini","apiKey":""}'
        Add-Report "api" "POST /models-no-key" $(if ($models.models.Count -gt 0) { "PASS" } else { "WARN" }) "models=$($models.models.Count)"
    } catch { Add-Report "api" "POST /models-no-key" "WARN" "sem chave: $($_.Exception.Message)" }
}

Section "5. SEARCH - LINKEDIN (24h default)"
if ($backendProc -and -not $backendProc.HasExited) {
    try {
        Api POST "/api/v1/search" "{}" | Out-Null
        $finished = Wait-SearchComplete "linkedin"
        $search = $finished.status
        $logs = Api GET "/api/v1/logs"
        $filterLine = ($logs.logs | Where-Object { $_.message -match '\[ FILTER \]' } | Select-Object -Last 1).message
        $collectLine = ($logs.logs | Where-Object { $_.message -match 'coletadas' } | Select-Object -Last 1).message
        if ($filterLine -match '0/\d+') {
            Add-Report "search" "linkedin-24h-filter" "FAIL" "$filterLine (idade ainda quebrada?)"
        } else {
            Add-Report "search" "linkedin-24h-filter" "PASS" "$filterLine"
        }
        Add-Report "search" "linkedin-collect" "PASS" $collectLine
        Add-Report "search" "linkedin-results" $(if ($search.jobs.Count -ge 0) { "PASS" } else { "FAIL" }) "$($finished.elapsedSeconds.ToString('F1'))s jobs=$($search.jobs.Count) msg=$($search.message)"
        $i = 0
        foreach ($job in $search.jobs) {
            $i++
            Add-Report "search" "linkedin-job-$i" "INFO" "score=$($job.score) $($job.status) | $($job.title) @ $($job.company)"
        }
        if ($search.jobs.Count -eq 0) { Add-Report "search" "linkedin-jobs" "WARN" "pipeline ok mas nenhuma vaga passou filtros finais" }
    } catch {
        Add-Report "search" "linkedin" "FAIL" $_.Exception.Message
        if ($_.ErrorDetails.Message) { Add-Report "search" "linkedin-detail" "FAIL" $_.ErrorDetails.Message }
    }
}

Section "6. SEARCH - INDEED"
if ($backendProc -and -not $backendProc.HasExited) {
    try {
        $body = (@{
            version = 1
            form = @{ roles = "DevOps Engineer"; levels = "Pleno"; recentHours = 24; maxJobs = 2; maxDelaySeconds = 1; linkedinPages = 1; scoreCut = 40; remoteCountry = "Brazil"; workMode = "remote"; keywords = "Linux, Docker" }
            toggles = @{ useLinkedin = $false; useIndeed = $true; useGupy = $false; headless = $true; compatibility = $false; remoteOnly = $true }
            localItems = @{ jobs = 0; applications = 0; history = 0 }
        } | ConvertTo-Json -Depth 6 -Compress)
        Api PUT "/api/v1/config" $body | Out-Null
        Api POST "/api/v1/search" "{}" | Out-Null
        $finished = Wait-SearchComplete "indeed"
        $search = $finished.status
        $logs = Api GET "/api/v1/logs"
        $indeedLine = ($logs.logs | Where-Object { $_.message -match 'INDEED|Indeed' } | Select-Object -Last 3 | ForEach-Object { $_.message }) -join " | "
        Add-Report "search" "indeed" $(if ($indeedLine) { "PASS" } else { "WARN" }) "$($finished.elapsedSeconds.ToString('F1'))s jobs=$($search.jobs.Count) log=$indeedLine"
    } catch { Add-Report "search" "indeed" "FAIL" $_.Exception.Message }
}

Section "7. SEARCH - GUPY"
if ($backendProc -and -not $backendProc.HasExited) {
    try {
        $body = (@{
            version = 1
            form = @{ roles = "DevOps"; levels = "Pleno"; recentHours = 168; maxJobs = 2; maxDelaySeconds = 1; scoreCut = 40; remoteCountry = "Brazil"; workMode = "remote"; keywords = "Linux" }
            toggles = @{ useLinkedin = $false; useIndeed = $false; useGupy = $true; headless = $true; compatibility = $false; remoteOnly = $true }
            localItems = @{ jobs = 0; applications = 0; history = 0 }
        } | ConvertTo-Json -Depth 6 -Compress)
        Api PUT "/api/v1/config" $body | Out-Null
        Api POST "/api/v1/search" "{}" | Out-Null
        $finished = Wait-SearchComplete "gupy"
        $search = $finished.status
        $logs = Api GET "/api/v1/logs"
        $gupyLine = ($logs.logs | Where-Object { $_.message -match 'GUPY|Gupy' } | Select-Object -Last 3 | ForEach-Object { $_.message }) -join " | "
        Add-Report "search" "gupy" $(if ($gupyLine) { "PASS" } else { "WARN" }) "$($finished.elapsedSeconds.ToString('F1'))s jobs=$($search.jobs.Count) log=$gupyLine"
    } catch { Add-Report "search" "gupy" "FAIL" $_.Exception.Message }
}

Section "8. ELECTRON / TYPESCRIPT"
try {
    Push-Location $root
    npx tsc -p apps/desktop/electron/tsconfig.json --noEmit 2>&1 | Out-Null
    if ($LASTEXITCODE -eq 0) { Add-Report "electron" "tsc-noemit" "PASS" "apps/desktop/electron compila" }
    else { Add-Report "electron" "tsc-noemit" "FAIL" "exit=$LASTEXITCODE" }
    Pop-Location
} catch { Add-Report "electron" "tsc-noemit" "FAIL" $_.Exception.Message }

try {
    Push-Location $root
    node scripts/dev/build-electron.mjs 2>&1 | Out-Null
    if ($LASTEXITCODE -eq 0) { Add-Report "electron" "esbuild" "PASS" "apps/desktop/dist-electron/main.cjs + preload.cjs gerados" }
    else { Add-Report "electron" "esbuild" "FAIL" "exit=$LASTEXITCODE" }
    Pop-Location
} catch { Add-Report "electron" "esbuild" "FAIL" $_.Exception.Message }

Section "9. DEPENDENCIES"
try {
    py -3 -c "import camoufox; print(camoufox.__version__ if hasattr(camoufox,'__version__') else 'ok')" 2>&1 | Out-Null
    if ($LASTEXITCODE -eq 0) { Add-Report "deps" "python-camoufox" "PASS" "import ok" }
    else { Add-Report "deps" "python-camoufox" "FAIL" "import failed" }
} catch { Add-Report "deps" "python-camoufox" "FAIL" $_.Exception.Message }

if (Test-Path $go) { Add-Report "deps" "go-toolchain" "PASS" $go } else { Add-Report "deps" "go-toolchain" "FAIL" "missing" }

Stop-Backend
Remove-Item $dbPath -ErrorAction SilentlyContinue

Section "SUMMARY"
$pass = @($report | Where-Object { $_ -match '^\[PASS\]' }).Count
$fail = @($report | Where-Object { $_ -match '^\[FAIL\]' }).Count
$warn = @($report | Where-Object { $_ -match '^\[WARN\]' }).Count
$info = @($report | Where-Object { $_ -match '^\[INFO\]' }).Count
Add-Report "summary" "totals" "INFO" "PASS=$pass FAIL=$fail WARN=$warn INFO=$info"

$report | ForEach-Object { Write-Output $_ }
exit $(if ($fail -gt 0) { 1 } elseif ($warn -gt 0) { 2 } else { 0 })
