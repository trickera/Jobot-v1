$ErrorActionPreference = "Stop"
$root = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$shots = Join-Path $root "docs\qa\screenshots"
New-Item -ItemType Directory -Force -Path $shots | Out-Null

Get-Process -Name "node" -ErrorAction SilentlyContinue | Where-Object {
  $_.Path -like "*sencia job*"
} | Stop-Process -Force -ErrorAction SilentlyContinue

$env:SENCIA_MOCK_TOKEN = "mini-test"
$mock = Start-Process -FilePath "node" -ArgumentList (Join-Path $root "scripts\dev\mock-ui-api.mjs") -PassThru -WindowStyle Hidden
Start-Sleep 1

$preview = Start-Process -FilePath "npm" -ArgumentList "run","preview","--","--port","1420","--host","127.0.0.1" -WorkingDirectory $root -PassThru -WindowStyle Hidden -RedirectStandardOutput (Join-Path $env:TEMP "sencia-preview.log") -RedirectStandardError (Join-Path $env:TEMP "sencia-preview.err")
Start-Sleep 3

Write-Output "mock pid=$($mock.Id) preview pid=$($preview.Id)"
Write-Output "VITE_SENCIA_API_URL=http://127.0.0.1:48731"
Write-Output "screenshots dir=$shots"
