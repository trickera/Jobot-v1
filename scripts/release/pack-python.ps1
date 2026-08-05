param(
  [switch]$Force
)

# Builds resources/python/ - a self-contained CPython embeddable runtime
# with the browser worker's dependencies pre-installed, so the packaged
# .exe never needs a system-wide Python (CH-01/D9). Bundled by
# electron-builder via the "extraResources" entry in apps/desktop/electron-builder.yml;
# resolved at runtime by apps/backend-go/internal/server/browser_worker.go's
# resolveBundledPython().
#
# The CPython embeddable zip is pinned by version+SHA-256 for reproducible
# builds. get-pip.py is fetched from the official PyPA bootstrap endpoint
# without a pinned hash - by design PyPA rotates it for security fixes, so
# there is no stable long-term hash to pin against; it is only ever used to
# install pip itself, not application code.

$ErrorActionPreference = "Stop"
$root = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path

$pythonVersion = "3.12.7"
$pythonZipSha256 = "0d57bb6cb078b74d23dbfe91f77d6780d45bed328911609f1f7ee2ba1606bf44"
$pythonZipUrl = "https://www.python.org/ftp/python/$pythonVersion/python-$pythonVersion-embed-amd64.zip"
$getPipUrl = "https://bootstrap.pypa.io/get-pip.py"

$targetDir = Join-Path $root "resources\python"
$cacheDir = Join-Path $root ".tools\python-cache"
$requirementsLock = Join-Path $root "apps\browser-worker\requirements.lock.txt"
$markerPath = Join-Path $targetDir ".pack-version"

if (-not (Test-Path $requirementsLock)) {
  Write-Error "Missing $requirementsLock - run this from the repo root."
  exit 1
}
$lockHash = (Get-FileHash $requirementsLock -Algorithm SHA256).Hash
$expectedMarker = "$pythonVersion|$lockHash"

if (-not $Force -and (Test-Path $markerPath)) {
  $currentMarker = Get-Content $markerPath -Raw
  if ($currentMarker.Trim() -eq $expectedMarker) {
    Write-Output "pack-python: up to date (Python $pythonVersion, lock hash $($lockHash.Substring(0,12))...). Use -Force to rebuild."
    exit 0
  }
}

New-Item -ItemType Directory -Force -Path $cacheDir | Out-Null

$zipPath = Join-Path $cacheDir "python-$pythonVersion-embed-amd64.zip"
if (-not (Test-Path $zipPath)) {
  Write-Output "pack-python: downloading CPython $pythonVersion embeddable..."
  Invoke-WebRequest -Uri $pythonZipUrl -OutFile $zipPath -UseBasicParsing
}
$actualHash = (Get-FileHash $zipPath -Algorithm SHA256).Hash
if ($actualHash -ne $pythonZipSha256) {
  Remove-Item $zipPath -Force
  Write-Error "pack-python: SHA-256 mismatch for CPython embeddable zip (expected $pythonZipSha256, got $actualHash). Deleted the bad download; re-run the script."
  exit 1
}

if (Test-Path $targetDir) {
  Remove-Item $targetDir -Recurse -Force
}
New-Item -ItemType Directory -Force -Path $targetDir | Out-Null
Expand-Archive -Path $zipPath -DestinationPath $targetDir -Force

# Enable site-packages: the embeddable distribution ships with
# `import site` commented out and no site-packages path in its ._pth file.
$pthFile = Get-ChildItem -Path $targetDir -Filter "python3*._pth" | Select-Object -First 1
if (-not $pthFile) {
  Write-Error "pack-python: could not find the embeddable's ._pth file in $targetDir"
  exit 1
}
$pthContent = Get-Content $pthFile.FullName
$pthContent = $pthContent | ForEach-Object { if ($_ -eq "#import site") { "import site" } else { $_ } }
if ($pthContent -notcontains "Lib\site-packages") {
  $pthContent += "Lib\site-packages"
}
Set-Content -Path $pthFile.FullName -Value $pthContent -Encoding ASCII

New-Item -ItemType Directory -Force -Path (Join-Path $targetDir "Lib\site-packages") | Out-Null

$pythonExe = Join-Path $targetDir "python.exe"

$getPipPath = Join-Path $cacheDir "get-pip.py"
if (-not (Test-Path $getPipPath) -or $Force) {
  Write-Output "pack-python: fetching get-pip.py..."
  Invoke-WebRequest -Uri $getPipUrl -OutFile $getPipPath -UseBasicParsing
}
& $pythonExe $getPipPath --no-warn-script-location --quiet
if ($LASTEXITCODE -ne 0) {
  Write-Error "pack-python: get-pip.py failed with exit code $LASTEXITCODE"
  exit 1
}

Write-Output "pack-python: installing browser worker dependencies (camoufox + deps, no browser binary)..."
& $pythonExe -m pip install --no-cache-dir -r $requirementsLock --quiet
if ($LASTEXITCODE -ne 0) {
  Write-Error "pack-python: pip install failed with exit code $LASTEXITCODE"
  exit 1
}

Set-Content -Path $markerPath -Value $expectedMarker -Encoding ASCII -NoNewline

$sizeMB = [math]::Round(((Get-ChildItem $targetDir -Recurse -File | Measure-Object -Property Length -Sum).Sum / 1MB), 1)
Write-Output "pack-python: done. $targetDir ($sizeMB MB, Python $pythonVersion). Camoufox's browser binary is NOT included - it is fetched on first use via bootstrap (CH-01)."
