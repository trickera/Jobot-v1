param(
  [switch]$SkipBuild,
  [switch]$SkipAI
)

$ErrorActionPreference = "Stop"
Add-Type -AssemblyName System.Net.Http

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
Set-Location $repoRoot

$qaToken = "resume-qa-" + [guid]::NewGuid().ToString("N").Substring(0, 8)
$qaDb = Join-Path $env:TEMP "sencia-resume-qa.db"
$qaArtifacts = Join-Path (Get-Location) "qa-artifacts\resume-studio"
$summaryPath = Join-Path $qaArtifacts "resume-studio-smoke-results.json"
$logPath = Join-Path $qaArtifacts "resume-studio-smoke.log"
$backendProc = $null
$results = [System.Collections.Generic.List[object]]::new()
$script:ParsedCanonical = $null
$script:AnalyzedRequirements = $null
$script:Gap = $null
$script:OptimizedPreview = $null
$script:OptimizeResponse = $null
$script:ScoreBefore = $null
$script:ScoreAfter = $null
$script:DocumentId = $null

function Write-Step($Message) {
  $line = "[{0}] {1}" -f (Get-Date -Format "yyyy-MM-dd HH:mm:ss"), $Message
  Write-Host $line
  Add-Content -Path $logPath -Value $line
}

function Add-QAResult($Id, $Area, $Case, $Result, $Evidence) {
  $results.Add([pscustomobject]@{
    id = $Id
    area = $Area
    case = $Case
    result = $Result
    evidence = $Evidence
  }) | Out-Null
}

function Copy-DeepObject($Value) {
  if ($null -eq $Value) { return $null }
  return ($Value | ConvertTo-Json -Depth 50 -Compress | ConvertFrom-Json)
}

function Convert-ToJsonBody($BodyObj) {
  return ($BodyObj | ConvertTo-Json -Depth 50 -Compress)
}

function Invoke-ResumeApiRaw($Method, $Path, $BodyObj = $null, [switch]$NoAuth) {
  $client = [System.Net.Http.HttpClient]::new()
  $client.Timeout = [TimeSpan]::FromSeconds(600)
  $request = [System.Net.Http.HttpRequestMessage]::new([System.Net.Http.HttpMethod]::new($Method), "http://127.0.0.1:48730$Path")
  if (-not $NoAuth) {
    $request.Headers.Authorization = [System.Net.Http.Headers.AuthenticationHeaderValue]::new("Bearer", $qaToken)
  }
  if ($null -ne $BodyObj) {
    $bytes = [System.Text.Encoding]::UTF8.GetBytes((Convert-ToJsonBody $BodyObj))
    $content = [System.Net.Http.ByteArrayContent]::new($bytes)
    $content.Headers.ContentType = [System.Net.Http.Headers.MediaTypeHeaderValue]::Parse("application/json")
    $request.Content = $content
  }
  $response = $client.SendAsync($request).Result
  $content = $response.Content.ReadAsStringAsync().Result
  $json = $null
  if ($content.Trim().Length -gt 0) {
    try { $json = $content | ConvertFrom-Json } catch { $json = $null }
  }
  $request.Dispose()
  $client.Dispose()
  return [pscustomobject]@{ Status = [int]$response.StatusCode; Content = $content; Json = $json }
}

function Assert-Result($Condition, $Id, $Area, $Case, $EvidencePass, $EvidenceFail) {
  if ($Condition) {
    Add-QAResult $Id $Area $Case "PASS" $EvidencePass
  } else {
    Add-QAResult $Id $Area $Case "FAIL" $EvidenceFail
  }
}

New-Item -ItemType Directory -Force -Path $qaArtifacts | Out-Null
Remove-Item $summaryPath, $logPath -ErrorAction SilentlyContinue
Remove-Item $qaDb -ErrorAction SilentlyContinue

$resumeText = @"
Alex Morgan
Senior DevOps Engineer | alex.morgan@example.com | +1 555 0199 | Austin, TX
SUMMARY
DevOps engineer with 6 years building CI/CD pipelines, Kubernetes platforms, and AWS infrastructure.
EXPERIENCE
Acme Corp - Senior DevOps Engineer - 2021-Present
- Built GitLab CI/CD pipelines reducing deploy time from 45 minutes to 12 minutes
- Managed EKS cluster serving 40 microservices with Terraform and Helm
- Implemented observability stack (Prometheus, Grafana) for on-call team of 5
Beta Systems - DevOps Engineer - 2018-2021
- Migrated legacy apps to Docker and automated releases with Jenkins
- Reduced cloud spend by 18% through rightsizing and reserved instances
SKILLS
AWS, Terraform, Kubernetes, Docker, Linux, GitLab CI, Prometheus, Grafana, Python, Bash
CERTIFICATIONS
AWS Certified Solutions Architect - Associate (2022)
"@

$jobDescription = @"
Senior DevOps Engineer - NovaTech
We are looking for a Senior DevOps Engineer to own our AWS and Kubernetes platform.
Requirements:
- 5+ years of DevOps or SRE experience
- Strong AWS (EKS, VPC, IAM), Terraform, Kubernetes, Docker
- CI/CD with GitLab or Jenkins
- Observability: Prometheus, Grafana
- Scripting in Python or Bash
Nice to have:
- Helm, Argo CD, cost optimization
- On-call experience with production incidents
Keywords: DevOps, AWS, Terraform, Kubernetes, CI/CD, Prometheus
"@

$canonical = [ordered]@{
  schemaVersion = 1
  basics = [ordered]@{
    name = "Alex Morgan"
    email = "alex.morgan@example.com"
    phone = "+1 555 0199"
    location = "Austin, TX"
    headline = "Senior DevOps Engineer"
    links = @()
  }
  target = [ordered]@{ jobTitle = ""; category = ""; seniority = "" }
  summary = "DevOps engineer with 6 years building CI/CD pipelines and Kubernetes platforms."
  skills = [ordered]@{
    hard = @("AWS", "Terraform", "Kubernetes", "Docker", "Linux", "Python")
    soft = @("Collaboration", "Incident response")
    tools = @("GitLab CI", "Prometheus", "Grafana", "Helm")
  }
  experience = @(
    [ordered]@{
      company = "Acme Corp"
      role = "Senior DevOps Engineer"
      location = "Remote"
      start = "2021-01"
      end = "present"
      bullets = @(
        "Built GitLab CI/CD pipelines reducing deploy time from 45 minutes to 12 minutes",
        "Managed EKS cluster serving 40 microservices with Terraform and Helm"
      )
    },
    [ordered]@{
      company = "Beta Systems"
      role = "DevOps Engineer"
      location = "Austin, TX"
      start = "2018-06"
      end = "2021-01"
      bullets = @(
        "Migrated legacy apps to Docker and automated releases with Jenkins",
        "Reduced cloud spend by 18% through rightsizing"
      )
    }
  )
  education = @()
  projects = @()
  certifications = @([ordered]@{ name = "AWS Certified Solutions Architect - Associate"; issuer = ""; year = "2022" })
  languages = @([ordered]@{ language = "English"; fluency = "" })
}

$requirements = [ordered]@{
  jobTitle = "Senior DevOps Engineer"
  category = "DevOps"
  seniority = "Senior"
  hardRequirements = @("5+ years DevOps", "AWS", "Terraform", "Kubernetes", "CI/CD", "Prometheus")
  niceToHave = @("Helm", "Argo CD")
  atsKeywords = @("DevOps", "AWS", "Terraform", "Kubernetes", "CI/CD", "Docker", "Grafana")
}

try {
  $env:SENCIA_API_TOKEN = $qaToken
  $env:SENCIA_DB_PATH = $qaDb
  $env:SENCIA_RADAR_DISABLED = "1"

  if (-not $SkipBuild) {
    Write-Step "Building backend"
    npm run backend:build | Tee-Object -FilePath $logPath -Append
  }

  $backendExe = Join-Path (Get-Location) "apps/backend-go\bin\sencia-job-backend.exe"
  Write-Step "Starting backend with isolated DB"
  $backendProc = Start-Process -FilePath $backendExe -WorkingDirectory "apps/backend-go" -PassThru -WindowStyle Hidden

  $healthy = $false
  for ($i = 0; $i -lt 40; $i++) {
    Start-Sleep -Milliseconds 500
    try {
      $health = Invoke-RestMethod "http://127.0.0.1:48730/health" -TimeoutSec 2
      if ($health.status -eq "ok") {
        $healthy = $true
        break
      }
    } catch {}
  }
  if (-not $healthy) {
    throw "Backend health check did not return ok."
  }
  Add-QAResult "T2.0" "Setup" "Isolated backend health" "PASS" "GET /health returned ok; db=$qaDb"

  Write-Step "Running offline API smoke"
  $auth = Invoke-ResumeApiRaw "POST" "/api/v1/resume/diagnose" @{ canonical = $canonical; rawText = $resumeText } -NoAuth
  Assert-Result ($auth.Status -eq 401) "T3.1" "API offline" "Auth required" "HTTP 401 without Authorization" "Expected 401, got HTTP $($auth.Status): $($auth.Content)"

  $diagnose = Invoke-ResumeApiRaw "POST" "/api/v1/resume/diagnose" @{ canonical = $canonical; rawText = $resumeText }
  $scoresOk = $diagnose.Status -eq 200 -and $diagnose.Json.scores.structure -eq $null -and $diagnose.Json.scores.readability -ge 0 -and $diagnose.Json.scores.keywords -ge 0
  Assert-Result $scoresOk "T3.2" "API offline" "Diagnose without AI" "HTTP $($diagnose.Status); scores=$($diagnose.Json.scores | ConvertTo-Json -Compress)" "Unexpected diagnose response HTTP $($diagnose.Status): $($diagnose.Content)"

  $score = Invoke-ResumeApiRaw "POST" "/api/v1/resume/score" @{ canonical = $canonical; requirements = $requirements }
  $scoreOk = $score.Status -eq 200 -and $score.Json.ats -ge 0 -and $score.Json.hr -ge 0 -and $null -ne $score.Json.atsBreakdown
  if ($scoreOk) { $script:ScoreBefore = $score.Json }
  Assert-Result $scoreOk "T3.3" "API offline" "Score without AI" "HTTP $($score.Status); ATS=$($score.Json.ats) HR=$($score.Json.hr)" "Unexpected score response HTTP $($score.Status): $($score.Content)"

  $md = Invoke-ResumeApiRaw "POST" "/api/v1/resume/export" @{ canonical = $canonical; format = "md"; templateId = "template:ats-strict" }
  if ($md.Status -eq 200) {
    Set-Content -Path (Join-Path $qaArtifacts "export.md") -Value $md.Json.content -Encoding UTF8
  }
  Assert-Result ($md.Status -eq 200 -and $md.Json.content.Contains("# Alex Morgan") -and $md.Json.content.Contains("## SKILLS") -and $md.Json.content.Contains("## EXPERIENCE")) "T3.4" "API offline" "Export Markdown" "Saved export.md; fileName=$($md.Json.fileName)" "Unexpected markdown response HTTP $($md.Status): $($md.Content)"

  $malicious = Copy-DeepObject $canonical
  $malicious.summary = "Uses <script>alert('x')</script> safely."
  $html = Invoke-ResumeApiRaw "POST" "/api/v1/resume/export" @{ canonical = $malicious; format = "html"; templateId = "template:ats-strict" }
  if ($html.Status -eq 200) {
    Set-Content -Path (Join-Path $qaArtifacts "export.html") -Value $html.Json.content -Encoding UTF8
  }
  Assert-Result ($html.Status -eq 200 -and $html.Json.content.Contains("&lt;script&gt;") -and -not $html.Json.content.Contains("<script>alert")) "T3.5" "API offline" "Export HTML escapes content" "Saved export.html; script tag escaped" "Unexpected HTML response HTTP $($html.Status): $($html.Content)"

  $pdfLetter = Invoke-ResumeApiRaw "POST" "/api/v1/resume/export" @{ canonical = $canonical; format = "pdf"; templateId = "template:ats-strict"; pageSize = "letter" }
  $letterPath = Join-Path $qaArtifacts "export-letter.pdf"
  $letterOk = $false
  if ($pdfLetter.Status -eq 200) {
    $bytes = [Convert]::FromBase64String($pdfLetter.Json.content)
    [IO.File]::WriteAllBytes($letterPath, $bytes)
    $prefix = [Text.Encoding]::ASCII.GetString($bytes, 0, [Math]::Min(4, $bytes.Length))
    $letterOk = $prefix -eq "%PDF" -and $bytes.Length -gt 5KB -and $pdfLetter.Json.fileName.EndsWith(".pdf")
  }
  $letterEvidence = if (Test-Path $letterPath) { "Saved $letterPath; size=$((Get-Item $letterPath).Length) bytes" } else { "PDF file was not written" }
  Assert-Result $letterOk "T3.6" "API offline" "Export PDF Letter" $letterEvidence "Unexpected PDF Letter response HTTP $($pdfLetter.Status): $($pdfLetter.Content)"

  $pdfA4 = Invoke-ResumeApiRaw "POST" "/api/v1/resume/export" @{ canonical = $canonical; format = "pdf"; templateId = "template:ats-strict"; pageSize = "a4" }
  $a4Path = Join-Path $qaArtifacts "export-a4.pdf"
  $a4Ok = $false
  if ($pdfA4.Status -eq 200) {
    $bytes = [Convert]::FromBase64String($pdfA4.Json.content)
    [IO.File]::WriteAllBytes($a4Path, $bytes)
    $prefix = [Text.Encoding]::ASCII.GetString($bytes, 0, [Math]::Min(4, $bytes.Length))
    $a4Ok = $prefix -eq "%PDF" -and $bytes.Length -gt 5KB
  }
  $a4Evidence = if (Test-Path $a4Path) { "Saved $a4Path; size=$((Get-Item $a4Path).Length) bytes" } else { "PDF file was not written" }
  Assert-Result $a4Ok "T3.7" "API offline" "Export PDF A4" $a4Evidence "Unexpected PDF A4 response HTTP $($pdfA4.Status): $($pdfA4.Content)"

  $cover = Invoke-ResumeApiRaw "POST" "/api/v1/resume/cover-letter" @{ canonical = $canonical; requirements = $requirements; jobId = "qa-job-001" }
  Assert-Result ($cover.Status -eq 404 -and $cover.Content -match '"code"\s*:\s*"job_not_found"') "T3.8" "API offline" "Cover letter missing job" "HTTP 404 job_not_found as expected" "Expected 404 job_not_found, got HTTP $($cover.Status): $($cover.Content)"

  $parseNoKey = Invoke-ResumeApiRaw "POST" "/api/v1/resume/parse" @{ text = $resumeText }
  Assert-Result ($parseNoKey.Status -eq 409 -and $parseNoKey.Content.Contains("ai_key_required")) "T3.9" "API offline" "Parse without AI key" "HTTP $($parseNoKey.Status); clear AI key required error" "Expected 409 AI key error, got HTTP $($parseNoKey.Status): $($parseNoKey.Content)"

  $optNoKey = Invoke-ResumeApiRaw "POST" "/api/v1/resume/optimize" @{ canonical = $canonical; requirements = $requirements; confirmed = @(); language = "en" }
  Assert-Result ($optNoKey.Status -eq 409 -and $optNoKey.Content.Contains("ai_key_required")) "T3.10" "API offline" "Optimize without AI key" "HTTP $($optNoKey.Status); clear AI key required error" "Expected 409 AI key error, got HTTP $($optNoKey.Status): $($optNoKey.Content)"

  $aiKey = [string]$env:SENCIA_QA_GEMINI_KEY
  if ($SkipAI -or [string]::IsNullOrWhiteSpace($aiKey)) {
    foreach ($id in @("T4.1", "T4.2", "T4.3", "T4.4", "T4.5", "T4.6", "T4.7", "T4.8")) {
      Add-QAResult $id "API with AI" "Gemini flow" "SKIP" "SENCIA_QA_GEMINI_KEY not set or -SkipAI was used."
    }
  } else {
    Write-Step "Running optional Gemini API smoke (key value is not logged)"
    $config = (Invoke-ResumeApiRaw "GET" "/api/v1/config").Json
    $config.form.provider = "gemini"
    $config.form.model = "gemini-2.5-flash"
    $config.form.apiKey = $aiKey
    $savedConfig = Invoke-ResumeApiRaw "PUT" "/api/v1/config" $config
    Assert-Result ($savedConfig.Status -eq 200 -and $savedConfig.Json.apiKeySet -eq $true -and [string]::IsNullOrEmpty($savedConfig.Json.form.apiKey)) "T4.1" "API with AI" "Configure Gemini provider" "HTTP 200; apiKeySet=true; key not echoed" "Unexpected config response HTTP $($savedConfig.Status): $($savedConfig.Content)"

    $parse = Invoke-ResumeApiRaw "POST" "/api/v1/resume/parse" @{ text = $resumeText }
    if ($parse.Status -eq 200) {
      $script:DocumentId = $parse.Json.documentId
      $script:ParsedCanonical = $parse.Json.canonical
      ($parse.Json.canonical | ConvertTo-Json -Depth 50) | Set-Content -Path (Join-Path $qaArtifacts "parsed-canonical.json") -Encoding UTF8
    }
    $parseOk = $parse.Status -eq 200 -and ($parse.Json.canonical.basics.name -match "Alex|Morgan") -and -not (($parse.Content -match "Google") -or ($parse.Content -match "Cobol"))
    Assert-Result $parseOk "T4.2" "API with AI" "Parse resume with Gemini" "HTTP $($parse.Status); documentId=$($parse.Json.documentId); no Google/Cobol hallucination" "Unexpected parse response HTTP $($parse.Status): $($parse.Content)"

    $analyze = Invoke-ResumeApiRaw "POST" "/api/v1/resume/analyze-job" @{ description = $jobDescription; category = "DevOps"; seniority = "Senior" }
    if ($analyze.Status -eq 200) {
      $script:AnalyzedRequirements = $analyze.Json.requirements
      ($analyze.Json.requirements | ConvertTo-Json -Depth 50) | Set-Content -Path (Join-Path $qaArtifacts "analyzed-requirements.json") -Encoding UTF8
    }
    $reqText = if ($analyze.Status -eq 200) { ($analyze.Json.requirements | ConvertTo-Json -Depth 20 -Compress) } else { "" }
    Assert-Result ($analyze.Status -eq 200 -and $reqText.Contains("AWS") -and $reqText.Contains("Kubernetes")) "T4.3" "API with AI" "Analyze job with Gemini" "HTTP $($analyze.Status); requirements include AWS/Kubernetes" "Unexpected analyze response HTTP $($analyze.Status): $($analyze.Content)"

    $canonForAI = if ($null -ne $script:ParsedCanonical) { $script:ParsedCanonical } else { $canonical }
    $reqForAI = if ($null -ne $script:AnalyzedRequirements) { $script:AnalyzedRequirements } else { $requirements }
    $gap = Invoke-ResumeApiRaw "POST" "/api/v1/resume/gap" @{ canonical = $canonForAI; requirements = $reqForAI }
    if ($gap.Status -eq 200) {
      $script:Gap = $gap.Json.gap
      ($gap.Json.gap | ConvertTo-Json -Depth 50) | Set-Content -Path (Join-Path $qaArtifacts "gap-analysis.json") -Encoding UTF8
    }
    $gapStatusesOk = $gap.Status -eq 200 -and $null -ne $gap.Json.gap
    Assert-Result $gapStatusesOk "T4.4" "API with AI" "Gap analysis with evidence gate" "HTTP $($gap.Status); found=$($gap.Json.gap.found.Count) partial=$($gap.Json.gap.partial.Count) toConfirm=$($gap.Json.gap.toConfirm.Count)" "Unexpected gap response HTTP $($gap.Status): $($gap.Content)"

    $confirmed = @()
    if ($null -ne $script:Gap -and $null -ne $script:Gap.toConfirm) {
      $confirmed = @($script:Gap.toConfirm | ForEach-Object { $_.term } | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
    }
    $opt = Invoke-ResumeApiRaw "POST" "/api/v1/resume/optimize" @{ canonical = $canonForAI; requirements = $reqForAI; confirmed = $confirmed; language = "en" }
    if ($opt.Status -eq 200) {
      $script:OptimizeResponse = $opt.Json
      $script:OptimizedPreview = $opt.Json.preview
      ($opt.Json | ConvertTo-Json -Depth 50) | Set-Content -Path (Join-Path $qaArtifacts "optimize-response.json") -Encoding UTF8
    }
    $optText = if ($opt.Status -eq 200) { $opt.Content } else { "" }
    $optOk = $opt.Status -eq 200 -and $null -ne $opt.Json.preview -and -not ($optText -match '"company"\s*:\s*"Google"')
    Assert-Result $optOk "T4.5" "API with AI" "Optimize resume with anti-invention gate" "HTTP $($opt.Status); patches=$($opt.Json.patches.Count); rejected=$($opt.Json.rejected.Count)" "Unexpected optimize response HTTP $($opt.Status): $($opt.Content)"

    $afterScore = $null
    if ($null -ne $script:OptimizedPreview) {
      $afterScore = Invoke-ResumeApiRaw "POST" "/api/v1/resume/score" @{ canonical = $script:OptimizedPreview; requirements = $reqForAI }
      if ($afterScore.Status -eq 200) { $script:ScoreAfter = $afterScore.Json }
    }
    Assert-Result ($afterScore.Status -eq 200) "T4.6" "API with AI" "Score before/after optimize" "Before ATS/HR=$($script:ScoreBefore.ats)/$($script:ScoreBefore.hr); after ATS/HR=$($afterScore.Json.ats)/$($afterScore.Json.hr)" "Unexpected after score response HTTP $($afterScore.Status): $($afterScore.Content)"

    $seedJob = Invoke-ResumeApiRaw "POST" "/api/v1/jobs/action" @{
      action = "dismiss"
      job = @{
        id = "qa-job-001"
        source = "qa"
        title = "Senior DevOps Engineer"
        company = "NovaTech"
        location = "Remote"
        url = "https://example.invalid/jobs/qa-job-001"
        status = "new"
        score = 90
        missingKeywords = @()
        description = $jobDescription
      }
    }
    if ($seedJob.Status -ne 200) {
      Add-QAResult "T4.7a" "API with AI" "Seed QA job row for version FK" "WARN" "Could not seed QA job row before saving version; HTTP $($seedJob.Status): $($seedJob.Content)"
    } else {
      Add-QAResult "T4.7a" "API with AI" "Seed QA job row for version FK" "PASS" "Seeded qa-job-001 through /jobs/action without exposing user data."
    }

    $save = Invoke-ResumeApiRaw "POST" "/api/v1/resume/version" @{
      documentId = $script:DocumentId
      jobId = "qa-job-001"
      canonical = $script:OptimizedPreview
      patches = if ($null -ne $script:OptimizeResponse) { $script:OptimizeResponse.patches } else { @() }
      templateId = "template:ats-strict"
      atsScore = if ($null -ne $script:ScoreAfter) { $script:ScoreAfter.ats } else { 0 }
      hrScore = if ($null -ne $script:ScoreAfter) { $script:ScoreAfter.hr } else { 0 }
      gap = if ($null -ne $script:Gap) { $script:Gap } else { @{ found = @(); partial = @(); missing = @(); toConfirm = @() } }
    }
    Assert-Result ($save.Status -eq 200 -and -not [string]::IsNullOrWhiteSpace($save.Json.id)) "T4.7" "API with AI" "Save optimized version" "HTTP $($save.Status); versionId=$($save.Json.id)" "Unexpected save response HTTP $($save.Status): $($save.Content)"

    $versions = Invoke-ResumeApiRaw "GET" "/api/v1/resume/versions?jobId=qa-job-001"
    $versionCount = if ($versions.Status -eq 200 -and $null -ne $versions.Json.versions) { @($versions.Json.versions).Count } else { 0 }
    Assert-Result ($versions.Status -eq 200 -and $versionCount -ge 1) "T4.8" "API with AI" "List saved versions" "HTTP $($versions.Status); versions=$versionCount" "Unexpected versions response HTTP $($versions.Status): $($versions.Content)"
  }

  Write-Step "Collecting backend logs"
  $logs = Invoke-ResumeApiRaw "GET" "/api/v1/logs"
  if ($logs.Status -eq 200) {
    ($logs.Json | ConvertTo-Json -Depth 20) | Set-Content -Path (Join-Path $qaArtifacts "backend-logs.json") -Encoding UTF8
  }
} finally {
  if ($backendProc -and -not $backendProc.HasExited) {
    Write-Step "Stopping backend"
    Stop-Process -Id $backendProc.Id -Force
  }
  Remove-Item $qaDb -ErrorAction SilentlyContinue
  $env:SENCIA_API_TOKEN = $null
  $env:SENCIA_DB_PATH = $null
  $env:SENCIA_RADAR_DISABLED = $null
}

$counts = $results | Group-Object result | ForEach-Object { [pscustomobject]@{ result = $_.Name; count = $_.Count } }
$summary = [pscustomobject]@{
  generatedAt = (Get-Date).ToString("s")
  artifactDir = $qaArtifacts
  results = $results
  counts = $counts
  aiRan = (-not $SkipAI -and -not [string]::IsNullOrWhiteSpace([string]$env:SENCIA_QA_GEMINI_KEY))
  scores = [pscustomobject]@{
    before = $script:ScoreBefore
    after = $script:ScoreAfter
  }
}
$summary | ConvertTo-Json -Depth 80 | Set-Content -Path $summaryPath -Encoding UTF8
Write-Step "Smoke summary saved to $summaryPath"

$failed = @($results | Where-Object { $_.result -eq "FAIL" })
if ($failed.Count -gt 0) {
  Write-Host "Resume Studio smoke completed with $($failed.Count) failure(s)."
  exit 1
}

Write-Host "Resume Studio smoke completed without FAIL results."
