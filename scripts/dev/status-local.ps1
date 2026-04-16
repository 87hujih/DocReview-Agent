Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

${repoRoot} = (Resolve-Path (Join-Path $PSScriptRoot "..\\..")).Path
$helperPath = Join-Path $PSScriptRoot "local-dev-common.ps1"
. $helperPath
$config = Get-LocalDevConfig -RepoRoot $repoRoot -ScriptRoot $PSScriptRoot

Write-Host "state_file: $($config.StatePath)"
Write-Host "server_url: $($config.Urls.Server)"
Write-Host "web_url:    $($config.Urls.Web)"
Write-Host "tika_url:   $($config.Urls.Tika)"

if (-not (Test-Path -LiteralPath $config.StatePath)) {
  Write-Host "tracked:    none"
  Write-Host "server_http: $(if (Test-LocalDevHttpReady -Url $config.Urls.Server) { 'ready' } else { 'down' })"
  Write-Host "web_http:    $(if (Test-LocalDevHttpReady -Url $config.Urls.Web) { 'ready' } else { 'down' })"
  Write-Host "tika_tcp:    $(if (Test-LocalDevTcpReady -HostName '127.0.0.1' -Port $config.Ports.Tika) { 'ready' } else { 'down' })"
  exit 0
}

$state = Get-Content -Raw -LiteralPath $config.StatePath | ConvertFrom-Json -AsHashtable

if ($state.ContainsKey("infrastructure") -and $state.infrastructure) {
  Write-Host "compose_services: $(@($state.infrastructure.compose_services) -join ', ')"
}

if ($state.ContainsKey("server") -and $state.server) {
  Write-Host "server_pid: $($state.server.pid)"
  Write-Host "server_process: $(Get-LocalDevTrackedProcessStatus -Tracked $state.server)"
  Write-Host "server_stdout: $($state.server.stdout)"
  Write-Host "server_stderr: $($state.server.stderr)"
}

if ($state.ContainsKey("web") -and $state.web) {
  Write-Host "web_pid: $($state.web.pid)"
  Write-Host "web_process: $(Get-LocalDevTrackedProcessStatus -Tracked $state.web)"
  Write-Host "web_stdout: $($state.web.stdout)"
  Write-Host "web_stderr: $($state.web.stderr)"
}

Write-Host "server_http: $(if (Test-LocalDevHttpReady -Url $config.Urls.Server) { 'ready' } else { 'down' })"
Write-Host "web_http:    $(if (Test-LocalDevHttpReady -Url $config.Urls.Web) { 'ready' } else { 'down' })"
Write-Host "tika_tcp:    $(if (Test-LocalDevTcpReady -HostName '127.0.0.1' -Port $config.Ports.Tika) { 'ready' } else { 'down' })"
