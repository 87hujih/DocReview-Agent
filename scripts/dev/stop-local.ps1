Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$statePath = Join-Path $PSScriptRoot ".local-dev-state.json"

function Stop-TrackedProcess {
  param(
    [Parameter(Mandatory = $true)]
    [hashtable]$Tracked
  )

  $proc = Get-Process -Id $Tracked.pid -ErrorAction SilentlyContinue
  if ($null -eq $proc) {
    Write-Host "进程 $($Tracked.pid) 已不存在，跳过。"
    return
  }

  $actualStart = $proc.StartTime.ToUniversalTime().ToString("o")
  if ($actualStart -ne $Tracked.start_time_utc) {
    Write-Host "PID $($Tracked.pid) 已被其它进程复用，跳过停止。"
    return
  }

  Stop-Process -Id $Tracked.pid -Force
  Write-Host "已停止进程 $($Tracked.pid)。"
}

if (-not (Test-Path -LiteralPath $statePath)) {
  Write-Host "未发现当前项目记录的本地开发进程。"
  exit 0
}

$state = Get-Content -Raw -LiteralPath $statePath | ConvertFrom-Json -AsHashtable

if ($state.server) {
  Stop-TrackedProcess -Tracked $state.server
}

if ($state.web) {
  Stop-TrackedProcess -Tracked $state.web
}

Remove-Item -LiteralPath $statePath -Force
Write-Host "已清理本地开发状态文件。"
