# Exit codes:
#   0 - deterministic invariants passed and all five live runs are reviewable.
#   1 - deterministic harness or product invariant failed.
#   2 - deterministic invariants passed, but live data left at least one case inconclusive.

$ErrorActionPreference = "Stop"

$root = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$backendDir = Join-Path $root "apps\backend-go"
$exe = Join-Path $backendDir "bin\sencia-job-backend.exe"
$baseUrl = "http://127.0.0.1:48730"
$runStamp = Get-Date -Format "yyyyMMdd-HHmmss"
$artifactRoot = Join-Path $root "qa-artifacts\stabilization-2026-07-14\$runStamp"
$expectedSources = @(
  "Arbeitnow",
  "Gupy",
  "Indeed",
  "Jobicy",
  "LinkedIn",
  "RemoteOK",
  "Remotive",
  "We Work Remotely"
)

$cases = @(
  [pscustomobject]@{
    Id = "backend"
    Role = "Backend Engineer"
    Location = "Brazil"
    RemoteCountry = "Brazil"
    WorkMode = "remote"
    Fixture = "1-software-backend.pdf"
    Keywords = @("Go", "Python", "PostgreSQL", "Docker", "Kubernetes", "AWS", "Terraform")
    CompatibleTerms = @("backend engineer", "backend developer", "back-end engineer", "back-end developer", "software engineer", "software developer", "platform engineer")
    QuestionableTerms = @("developer", "engineer", "devops", "site reliability")
  },
  [pscustomobject]@{
    Id = "nursing"
    Role = "Registered Nurse"
    Location = "Chicago, IL"
    RemoteCountry = "United States"
    WorkMode = "onsite"
    Fixture = "2-nursing.pdf"
    Keywords = @("critical care", "ventilator management", "Epic", "CCRN", "ACLS", "BLS")
    CompatibleTerms = @("registered nurse", "staff nurse", "critical care nurse", "icu nurse", " rn ")
    QuestionableTerms = @("nurse", "nursing", "clinical", "healthcare")
  },
  [pscustomobject]@{
    Id = "finance"
    Role = "Financial Analyst"
    Location = "Berlin, Germany"
    RemoteCountry = "Germany"
    WorkMode = "hybrid"
    Fixture = "3-finance.pdf"
    Keywords = @("FP&A", "financial modeling", "budgeting", "forecasting", "Excel", "Anaplan", "SAP")
    CompatibleTerms = @("financial analyst", "finance analyst", "fp&a analyst", "financial planning analyst")
    QuestionableTerms = @("finance", "financial", "accounting", "analyst")
  },
  [pscustomobject]@{
    Id = "marketing"
    Role = "Digital Marketing Manager"
    Location = "United Kingdom"
    RemoteCountry = "United Kingdom"
    WorkMode = "remote"
    Fixture = "4-marketing.pdf"
    Keywords = @("paid media", "Google Ads", "SEO", "email marketing", "GA4", "CAC/LTV")
    CompatibleTerms = @("digital marketing manager", "performance marketing manager", "growth marketing manager", "marketing manager")
    QuestionableTerms = @("digital marketing", "marketing", "paid media", "growth", "seo")
  },
  [pscustomobject]@{
    Id = "product-design"
    Role = "Product Designer"
    Location = "Lisbon, Portugal"
    RemoteCountry = "Portugal"
    WorkMode = "hybrid"
    Fixture = "5-product-design.pdf"
    Keywords = @("product design", "UX research", "Figma", "design systems", "prototyping", "accessibility")
    CompatibleTerms = @("product designer", "product design", "ux designer", "ui/ux designer", "experience designer")
    QuestionableTerms = @("designer", "design", "ux", "user experience")
  }
)

function Assert-Invariant {
  param(
    [bool]$Condition,
    [string]$Message
  )
  if (-not $Condition) {
    throw "Invariant failed: $Message"
  }
}

function Save-Json {
  param(
    [string]$Path,
    [object]$Value
  )
  $Value | ConvertTo-Json -Depth 30 | Set-Content -Path $Path -Encoding UTF8
}

function Invoke-Api {
  param(
    [ValidateSet("GET", "POST", "PUT")]
    [string]$Method,
    [string]$Path,
    [object]$Body = $null
  )
  $params = @{
    Uri = "$baseUrl$Path"
    Method = $Method
    Headers = @{ Authorization = "Bearer $script:token" }
    TimeoutSec = 45
  }
  if ($null -ne $Body) {
    $params.ContentType = "application/json"
    if ($Body -is [string]) {
      $params.Body = $Body
    } else {
      $params.Body = $Body | ConvertTo-Json -Depth 30 -Compress
    }
  }
  Invoke-RestMethod @params
}

function Wait-Health {
  param([int]$Seconds = 30)
  for ($i = 0; $i -lt $Seconds; $i++) {
    try {
      $health = Invoke-RestMethod -Uri "$baseUrl/health" -TimeoutSec 2
      if ($health.status -eq "ok") {
        return $true
      }
    } catch {}
    Start-Sleep -Seconds 1
  }
  return $false
}

function Stop-Backend {
  param([System.Diagnostics.Process]$Process)
  if ($null -eq $Process) {
    return
  }
  try {
    $Process.Refresh()
    if (-not $Process.HasExited) {
      Stop-Process -Id $Process.Id -ErrorAction SilentlyContinue
      if (-not $Process.WaitForExit(5000)) {
        Stop-Process -Id $Process.Id -Force -ErrorAction SilentlyContinue
        $Process.WaitForExit(5000) | Out-Null
      }
    }
  } catch {}
}

function Assert-Plan {
  param(
    [object]$Plan,
    [object]$Case
  )
  Assert-Invariant ($null -ne $Plan.roles) "$($Case.Id): plan.roles must be an array"
  Assert-Invariant ($null -ne $Plan.levels) "$($Case.Id): plan.levels must be an array"
  Assert-Invariant ($null -ne $Plan.scoringTerms) "$($Case.Id): plan.scoringTerms must be an array"
  Assert-Invariant ($null -ne $Plan.locations) "$($Case.Id): plan.locations must be an array"
  Assert-Invariant ($null -ne $Plan.sources) "$($Case.Id): plan.sources must be an array"
  Assert-Invariant (@($Plan.roles).Count -eq 1 -and @($Plan.roles)[0] -ceq $Case.Role) "$($Case.Id): exact role was not preserved"
  Assert-Invariant ($Plan.rolesSource -eq "role") "$($Case.Id): advanced profiles unexpectedly overrode the role"
  Assert-Invariant ($Plan.workMode -eq $Case.WorkMode) "$($Case.Id): work mode mismatch"

  $actualSources = @($Plan.sources | Sort-Object)
  $sourceDiff = @(Compare-Object -ReferenceObject $expectedSources -DifferenceObject $actualSources)
  Assert-Invariant ($sourceDiff.Count -eq 0) "$($Case.Id): enabled source list mismatch"

  foreach ($keyword in $Case.Keywords) {
    Assert-Invariant (@($Plan.scoringTerms) -ccontains $keyword) "$($Case.Id): scoring term '$keyword' is missing"
  }

  $locations = @($Plan.locations)
  if ($Case.WorkMode -eq "remote") {
    Assert-Invariant ($locations.Count -eq 1) "$($Case.Id): remote plan must have one location"
    Assert-Invariant ($locations[0].remote -eq $true -and $locations[0].location -ceq $Case.RemoteCountry) "$($Case.Id): remote location mismatch"
  } elseif ($Case.WorkMode -eq "onsite") {
    Assert-Invariant ($locations.Count -eq 1) "$($Case.Id): onsite plan must have one location"
    Assert-Invariant ($locations[0].remote -eq $false -and $locations[0].location -ceq $Case.Location) "$($Case.Id): onsite location mismatch"
  } else {
    Assert-Invariant ($locations.Count -eq 2) "$($Case.Id): hybrid plan must have two locations"
    $remote = @($locations | Where-Object { $_.remote -eq $true })
    $onsite = @($locations | Where-Object { $_.remote -eq $false })
    Assert-Invariant ($remote.Count -eq 1 -and $remote[0].location -ceq $Case.RemoteCountry) "$($Case.Id): hybrid remote location mismatch"
    Assert-Invariant ($onsite.Count -eq 1 -and $onsite[0].location -ceq $Case.Location) "$($Case.Id): hybrid onsite location mismatch"
  }
}

function Find-Term {
  param(
    [string]$Title,
    [string[]]$Terms
  )
  $haystack = " " + $Title.Trim().ToLowerInvariant() + " "
  foreach ($term in $Terms) {
    if ($haystack.Contains($term.ToLowerInvariant())) {
      return $term.Trim()
    }
  }
  return ""
}

function Review-Jobs {
  param(
    [object[]]$Jobs,
    [object]$Case
  )
  $review = @()
  $rank = 0
  foreach ($job in @($Jobs | Select-Object -First 10)) {
    $rank++
    $term = Find-Term $job.title $Case.CompatibleTerms
    if ($term -ne "") {
      $classification = "compatible"
      $reason = "Title contains the role-compatible term '$term'."
    } else {
      $term = Find-Term $job.title $Case.QuestionableTerms
      if ($term -ne "") {
        $classification = "questionable"
        $reason = "Title contains only the broader domain term '$term'; manual review is required."
      } else {
        $classification = "incompatible"
        $reason = "No case-compatible title term was found; manual review is required."
      }
    }
    $review += [pscustomobject]@{
      rank = $rank
      id = $job.id
      title = $job.title
      company = $job.company
      location = $job.location
      source = $job.source
      classification = $classification
      evidence = $reason
      score = $job.score
      scoreSource = $job.scoreSource
      scoreReason = $job.scoreReason
    }
  }
  return $review
}

function Duplicate-Count {
  param(
    [object[]]$Jobs,
    [scriptblock]$Key
  )
  return @(
    $Jobs |
      ForEach-Object { & $Key $_ } |
      Where-Object { -not [string]::IsNullOrWhiteSpace($_) } |
      Group-Object |
      Where-Object { $_.Count -gt 1 }
  ).Count
}

function Source-Statuses {
  param(
    [object]$Diagnostics,
    [object[]]$LogEntries
  )
  $lookupNames = @{
    "We Work Remotely" = "WeWorkRemotely"
  }
  $result = @()
  foreach ($source in $expectedSources) {
    $lookup = $source
    if ($lookupNames.ContainsKey($source)) {
      $lookup = $lookupNames[$source]
    }
    $sourceDiag = $null
    if ($null -ne $Diagnostics.sources) {
      $property = $Diagnostics.sources.PSObject.Properties[$lookup]
      if ($null -ne $property) {
        $sourceDiag = $property.Value
      }
    }
    if ($null -eq $sourceDiag) {
      $sourceDiag = [pscustomobject]@{
        collected = 0; fresh = 0; evaluated = 0; approved = 0; discarded = 0
        dropped = 0; skippedNoDescription = 0; detailFetched = 0; blocked = $false
      }
    }
    $sourceLogs = @($LogEntries | Where-Object { $_.message -match [regex]::Escape($lookup) })
    $attempted = $sourceLogs.Count -gt 0
    $errored = @($sourceLogs | Where-Object { $_.message -match '(?i)falhou|error|erro|timeout|timed out|http 4\d\d|http 5\d\d' }).Count -gt 0
    $reshaped = @($sourceLogs | Where-Object { $_.message -match '(?i)marcacao.*mudado|markup.*changed' }).Count -gt 0
    if ($sourceDiag.blocked -eq $true) {
      $status = "External/blocked"
      $attempted = $true
    } elseif ($reshaped) {
      $status = "External/reshaped"
    } elseif ($errored) {
      $status = "errored"
    } elseif ([int]$sourceDiag.collected -gt 0) {
      $status = "successful"
      $attempted = $true
    } elseif ($attempted) {
      $status = "empty"
    } else {
      $status = "not-attempted"
    }
    $result += [pscustomobject]@{
      source = $source
      configured = $true
      attempted = $attempted
      status = $status
      collected = [int]$sourceDiag.collected
      fresh = [int]$sourceDiag.fresh
      evaluated = [int]$sourceDiag.evaluated
      approved = [int]$sourceDiag.approved
      discarded = [int]$sourceDiag.discarded
      dropped = [int]$sourceDiag.dropped
      skippedNoDescription = [int]$sourceDiag.skippedNoDescription
      detailFetched = [int]$sourceDiag.detailFetched
      blocked = $sourceDiag.blocked -eq $true
    }
  }
  return $result
}

if (-not (Test-Path $exe)) {
  Write-Host "FAIL: backend executable not found; run npm run backend:build first."
  exit 1
}

New-Item -ItemType Directory -Force -Path $artifactRoot | Out-Null
$summaries = @()
$deterministicFailure = $false
$liveInconclusive = $false

Write-Host "=== SEARCH RELEVANCE MATRIX ==="
Write-Host "Artifacts: $artifactRoot"

foreach ($case in $cases) {
  $caseDir = Join-Path $artifactRoot $case.Id
  $tempDir = Join-Path $env:TEMP ("sencia-search-matrix-{0}-{1}" -f $case.Id, [guid]::NewGuid().ToString("N"))
  $dbPath = Join-Path $tempDir "sencia.db"
  $stdoutPath = Join-Path $caseDir "backend.stdout.log"
  $stderrPath = Join-Path $caseDir "backend.stderr.log"
  $proc = $null
  $script:token = "search-matrix-" + [guid]::NewGuid().ToString("N")
  New-Item -ItemType Directory -Force -Path $caseDir, $tempDir | Out-Null

  Write-Host "[$($case.Id)] starting isolated backend"
  try {
    if (Get-Command Get-NetTCPConnection -ErrorAction SilentlyContinue) {
      $listener = Get-NetTCPConnection -LocalPort 48730 -State Listen -ErrorAction SilentlyContinue
      Assert-Invariant ($null -eq $listener) "$($case.Id): port 48730 is already in use"
    }

    $env:SENCIA_API_TOKEN = $script:token
    $env:SENCIA_DB_PATH = $dbPath
    $env:SENCIA_RADAR_DISABLED = "1"

    $proc = Start-Process -FilePath $exe -WorkingDirectory $backendDir -PassThru -WindowStyle Hidden -RedirectStandardOutput $stdoutPath -RedirectStandardError $stderrPath
    Assert-Invariant (Wait-Health 30) "$($case.Id): backend health check timed out"
    $proc.Refresh()
    Assert-Invariant (-not $proc.HasExited) "$($case.Id): backend exited during startup"

    $config = Invoke-Api GET "/api/v1/config"
    $config.version = 2
    $config.apiKeySet = $false
    $config.form.apiKey = ""
    $config.form.role = $case.Role
    $config.form.roles = $case.Role
    $config.form.searchProfiles = ""
    $config.form.location = $case.Location
    $config.form.onsiteLocation = $case.Location
    $config.form.remoteCountry = $case.RemoteCountry
    $config.form.workMode = $case.WorkMode
    $config.form.keywords = $case.Keywords -join ", "
    $config.form.keywordsForRoles = $case.Role
    $config.form.recentHours = 336
    $config.form.maxJobs = 10
    $config.form.linkedinPages = 1
    $config.form.maxDelaySeconds = 1
    $config.form.searchTimeoutSeconds = 300
    $config.toggles.remoteOnly = $case.WorkMode -eq "remote"
    $config.toggles.useLinkedin = $true
    $config.toggles.useIndeed = $true
    $config.toggles.useGupy = $true
    $config.toggles.useRemotive = $true
    $config.toggles.useRemoteok = $true
    $config.toggles.useJobicy = $true
    $config.toggles.useArbeitnow = $true
    $config.toggles.useWeworkremotely = $true
    $config.toggles.headless = $true
    $config.toggles.compatibility = $true
    $config.toggles.score = $true
    $config.toggles.localOnly = $true
    $config.toggles.saveHistory = $true
    $config.toggles.radarMode = $false

    $savedConfig = Invoke-Api PUT "/api/v1/config" $config
    Assert-Invariant ($savedConfig.apiKeySet -eq $false) "$($case.Id): API key unexpectedly present"
    Save-Json (Join-Path $caseDir "config.json") $savedConfig

    $fixturePath = Join-Path $root "scripts\qa\fixtures\personas\$($case.Fixture)"
    Assert-Invariant (Test-Path $fixturePath) "$($case.Id): fixture is missing"
    $resumeBody = @{
      fileName = $case.Fixture
      mimeType = "application/pdf"
      contentBase64 = [Convert]::ToBase64String([IO.File]::ReadAllBytes($fixturePath))
    }
    $resumeUpload = Invoke-Api POST "/api/v1/resume" $resumeBody
    Save-Json (Join-Path $caseDir "resume-upload.json") $resumeUpload

    $plan = Invoke-Api GET "/api/v1/search/plan"
    Assert-Plan $plan $case
    Save-Json (Join-Path $caseDir "plan.json") $plan

    Invoke-Api POST "/api/v1/search/reset" @{} | Out-Null
    $searchStart = Invoke-Api POST "/api/v1/search" @{}
    Save-Json (Join-Path $caseDir "search-start.json") $searchStart

    $clock = [Diagnostics.Stopwatch]::StartNew()
    $status = $null
    while ($clock.Elapsed.TotalSeconds -lt 360) {
      Start-Sleep -Seconds 2
      $status = Invoke-Api GET "/api/v1/search/status"
      if (-not $status.running) {
        break
      }
    }
    $clock.Stop()
    Assert-Invariant ($null -ne $status) "$($case.Id): no search status was returned"
    Assert-Invariant (-not $status.running) "$($case.Id): search exceeded the 360-second harness timeout"

    $logs = Invoke-Api GET "/api/v1/logs"
    $jobsRead = Invoke-Api GET "/api/v1/jobs"
    $historyRead = Invoke-Api GET "/api/v1/history"
    $statusJobs = @($status.jobs)
    $persistedJobs = @($jobsRead.jobs)
    $history = @($historyRead.history)
    $sourceStatuses = @(Source-Statuses $status.diagnostics @($logs.logs))
    $review = @(Review-Jobs $statusJobs $case)

    $duplicates = [pscustomobject]@{
      ids = Duplicate-Count $statusJobs { param($job) $job.id }
      urls = Duplicate-Count $statusJobs { param($job) $job.url }
      titleCompany = Duplicate-Count $statusJobs { param($job) (("{0}|{1}" -f $job.title, $job.company).Trim().ToLowerInvariant()) }
    }

    Save-Json (Join-Path $caseDir "status.json") $status
    Save-Json (Join-Path $caseDir "diagnostics.json") $status.diagnostics
    Save-Json (Join-Path $caseDir "logs.json") $logs
    Save-Json (Join-Path $caseDir "result-summaries.json") $statusJobs
    Save-Json (Join-Path $caseDir "jobs.json") $jobsRead
    Save-Json (Join-Path $caseDir "history.json") $historyRead
    Save-Json (Join-Path $caseDir "source-statuses.json") $sourceStatuses
    Save-Json (Join-Path $caseDir "relevance-review.json") $review

    $caseInconclusive = $false
    $notes = @()
    if (-not [string]::IsNullOrWhiteSpace($status.error)) {
      $caseInconclusive = $true
      $notes += "Search ended with an error: $($status.error)"
    }
    if ($statusJobs.Count -eq 0) {
      $caseInconclusive = $true
      $notes += "Nao verificado: live search returned zero reviewable jobs."
    }
    if ($history.Count -lt 1) {
      throw "Invariant failed: $($case.Id): completed search did not persist history"
    }
    if ($statusJobs.Count -gt 0 -and $persistedJobs.Count -lt $statusJobs.Count) {
      throw "Invariant failed: $($case.Id): fewer persisted jobs than completed status jobs"
    }
    if ($duplicates.ids -gt 0 -or $duplicates.titleCompany -gt 0) {
      $caseInconclusive = $true
      $notes += "Live duplicate invariant needs deterministic reproduction before classification as a product bug."
    }

    $classification = "reviewable"
    if ($caseInconclusive) {
      $classification = "Nao verificado"
      $liveInconclusive = $true
    }
    $summary = [pscustomobject]@{
      id = $case.Id
      role = $case.Role
      location = $case.Location
      remoteCountry = $case.RemoteCountry
      workMode = $case.WorkMode
      keywords = $case.Keywords
      durationSeconds = [math]::Round($clock.Elapsed.TotalSeconds, 1)
      message = $status.message
      error = $status.error
      diagnostics = $status.diagnostics
      sources = $sourceStatuses
      duplicates = $duplicates
      resultCount = $statusJobs.Count
      persistedJobCount = $persistedJobs.Count
      persistedHistoryCount = $history.Count
      relevanceReview = $review
      classification = $classification
      notes = $notes
    }
    Save-Json (Join-Path $caseDir "summary.json") $summary
    $summaries += $summary
    Write-Host "[$($case.Id)] $classification; jobs=$($statusJobs.Count); persisted=$($persistedJobs.Count); history=$($history.Count); $($clock.Elapsed.TotalSeconds.ToString('F1'))s"
  } catch {
    $deterministicFailure = $true
    $failure = [pscustomobject]@{
      id = $case.Id
      classification = "deterministic-failure"
      message = $_.Exception.Message
    }
    Save-Json (Join-Path $caseDir "failure.json") $failure
    $summaries += $failure
    Write-Host "[$($case.Id)] FAIL: $($_.Exception.Message)"
  } finally {
    Stop-Backend $proc
    Remove-Item Env:SENCIA_API_TOKEN -ErrorAction SilentlyContinue
    Remove-Item Env:SENCIA_DB_PATH -ErrorAction SilentlyContinue
    Remove-Item Env:SENCIA_RADAR_DISABLED -ErrorAction SilentlyContinue
    Remove-Item $tempDir -Recurse -Force -ErrorAction SilentlyContinue
  }
}

Save-Json (Join-Path $artifactRoot "matrix-summary.json") $summaries

if ($deterministicFailure) {
  Write-Host "RESULT: deterministic harness/product invariant failure"
  exit 1
}
if ($liveInconclusive) {
  Write-Host "RESULT: honest live-data inconclusive"
  exit 2
}
Write-Host "RESULT: all five live runs are reviewable"
exit 0
