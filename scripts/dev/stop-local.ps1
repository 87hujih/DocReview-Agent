Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\\..")).Path
$helperPath = Join-Path $PSScriptRoot "local-dev-common.ps1"
. $helperPath
$config = Get-LocalDevConfig -RepoRoot $repoRoot -ScriptRoot $PSScriptRoot

if (-not (Test-Path -LiteralPath $config.StatePath)) {
  Write-Host "未发现当前项目记录的本地开发进程。"
  exit 0
}

$state = Get-Content -Raw -LiteralPath $config.StatePath | ConvertFrom-Json -AsHashtable

if ($state.ContainsKey("server") -and $state.server) {
  Stop-LocalDevTrackedProcess -Tracked $state.server
}

if ($state.ContainsKey("web") -and $state.web) {
  Stop-LocalDevTrackedProcess -Tracked $state.web
}

if ($state.ContainsKey("infrastructure") -and $state.infrastructure -and $state.infrastructure.compose_services) {
  Invoke-LocalDevCompose -Config $config -Arguments (@("stop") + @($state.infrastructure.compose_services))
  Write-Host "已停止 compose 服务：$(@($state.infrastructure.compose_services) -join ', ')。"
}

Remove-Item -LiteralPath $config.StatePath -Force
Write-Host "已清理本地开发状态文件。"
