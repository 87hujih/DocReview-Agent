Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$statePath = Join-Path $PSScriptRoot ".local-dev-state.json"
$serverUrl = "http://127.0.0.1:18080/healthz"
$webUrl = "http://127.0.0.1:3000"

function Test-HttpReady {
  param(
    [Parameter(Mandatory = $true)]
    [string]$Url
  )

  try {
    $null = Invoke-WebRequest -Uri $Url -Method Get -TimeoutSec 2
    return $true
  } catch {
    return $false
  }
}

function Get-TrackedProcessStatus {
  param(
    [Parameter(Mandatory = $true)]
    [hashtable]$Tracked
  )

  $proc = Get-Process -Id $Tracked.pid -ErrorAction SilentlyContinue
  if ($null -eq $proc) {
    return "missing"
  }

  $actualStart = $proc.StartTime.ToUniversalTime().ToString("o")
  if ($actualStart -ne $Tracked.start_time_utc) {
    return "pid-reused"
  }

  return "running"
}

Write-Host "state_file: $statePath"
Write-Host "server_url: $serverUrl"
Write-Host "web_url:    $webUrl"

if (-not (Test-Path -LiteralPath $statePath)) {
  Write-Host "tracked:    none"
  Write-Host "server_http: $(if (Test-HttpReady -Url $serverUrl) { 'ready' } else { 'down' })"
  Write-Host "web_http:    $(if (Test-HttpReady -Url $webUrl) { 'ready' } else { 'down' })"
  exit 0
}

$state = Get-Content -Raw -LiteralPath $statePath | ConvertFrom-Json -AsHashtable

if ($state.server) {
  Write-Host "server_pid: $($state.server.pid)"
  Write-Host "server_process: $(Get-TrackedProcessStatus -Tracked $state.server)"
  Write-Host "server_stdout: $($state.server.stdout)"
  Write-Host "server_stderr: $($state.server.stderr)"
}

if ($state.web) {
  Write-Host "web_pid: $($state.web.pid)"
  Write-Host "web_process: $(Get-TrackedProcessStatus -Tracked $state.web)"
  Write-Host "web_stdout: $($state.web.stdout)"
  Write-Host "web_stderr: $($state.web.stderr)"
}

Write-Host "server_http: $(if (Test-HttpReady -Url $serverUrl) { 'ready' } else { 'down' })"
Write-Host "web_http:    $(if (Test-HttpReady -Url $webUrl) { 'ready' } else { 'down' })"
