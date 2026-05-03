Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

# 基于脚本目录计算仓库根目录，避免依赖调用方当前工作目录。
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\\..")).Path
$pwshPath = Join-Path $PSHOME "pwsh.exe"
$helperPath = Join-Path $PSScriptRoot "local-dev-common.ps1"
. $helperPath
$config = Get-LocalDevConfig -RepoRoot $repoRoot -ScriptRoot $PSScriptRoot

if (Test-Path -LiteralPath $config.StatePath) {
  & (Join-Path $PSScriptRoot "stop-local.ps1") | Out-Host
}

if (Test-LocalDevHttpReady -Url $config.Urls.Server) {
  throw "检测到 $($config.Urls.Server) 已可访问，但它不在当前项目状态文件中。请先手动释放 18080 端口后再重试。"
}

if (Test-LocalDevHttpReady -Url $config.Urls.Web) {
  throw "检测到 $($config.Urls.Web) 已可访问，但它不在当前项目状态文件中。请先手动释放 3000 端口后再重试。"
}

$serverProcess = $null
$webProcess = $null

try {
  Invoke-LocalDevCompose -Config $config -Arguments (@("up", "-d") + $config.ComposeServices)

  if (-not (Wait-LocalDevTcpReady -HostName "127.0.0.1" -Port $config.Ports.Tika -TimeoutSeconds 45)) {
    throw "Tika 未在 45 秒内就绪。请检查 docker compose 服务 tika。"
  }

  $serverCommand = "& { Set-Location '$repoRoot'; `$env:SERVER_PORT='18080'; go run ./apps/server/cmd/server }"
  $serverProcess = Start-Process `
    -FilePath $pwshPath `
    -ArgumentList @("-NoLogo", "-NoProfile", "-Command", $serverCommand) `
    -WorkingDirectory $repoRoot `
    -RedirectStandardOutput $config.Logs.ServerStdout `
    -RedirectStandardError $config.Logs.ServerStderr `
    -PassThru

  if (-not (Wait-LocalDevHttpReady -Url $config.Urls.Server -TimeoutSeconds 45)) {
    throw "后端未在 45 秒内就绪。stdout: $($config.Logs.ServerStdout) ; stderr: $($config.Logs.ServerStderr)"
  }

  $webCommand = "& { Set-Location '$($config.WebRoot)'; `$env:NEXT_PUBLIC_API_URL='http://127.0.0.1:18080'; npm run dev -- --hostname 127.0.0.1 --port 3000 }"
  $webProcess = Start-Process `
    -FilePath $pwshPath `
    -ArgumentList @("-NoLogo", "-NoProfile", "-Command", $webCommand) `
    -WorkingDirectory $config.WebRoot `
    -RedirectStandardOutput $config.Logs.WebStdout `
    -RedirectStandardError $config.Logs.WebStderr `
    -PassThru

  if (-not (Wait-LocalDevHttpReady -Url $config.Urls.Web -TimeoutSeconds 90)) {
    throw "前端未在 90 秒内就绪。stdout: $($config.Logs.WebStdout) ; stderr: $($config.Logs.WebStderr)"
  }

  $state = New-LocalDevState `
    -Config $config `
    -StartedAtUtc (Get-Date).ToUniversalTime().ToString("o") `
    -Server (New-TrackedProcessRecord -Process $serverProcess -Stdout $config.Logs.ServerStdout -Stderr $config.Logs.ServerStderr -Url $config.Urls.Server) `
    -Web (New-TrackedProcessRecord -Process $webProcess -Stdout $config.Logs.WebStdout -Stderr $config.Logs.WebStderr -Url $config.Urls.Web)

  $state | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $config.StatePath -Encoding UTF8

  Write-Host "本地联调环境已启动。"
  Write-Host "infra:  $($config.ComposeServices -join ', ')"
  Write-Host "tika:   $($config.Urls.Tika)"
  Write-Host "server: $($config.Urls.Server)"
  Write-Host "web:    $($config.Urls.Web)"
  Write-Host "state:  $($config.StatePath)"
} catch {
  if ($null -ne $webProcess -and -not $webProcess.HasExited) {
    Stop-Process -Id $webProcess.Id -Force
  }
  if ($null -ne $serverProcess -and -not $serverProcess.HasExited) {
    Stop-Process -Id $serverProcess.Id -Force
  }

  if (Test-Path -LiteralPath $config.StatePath) {
    Remove-Item -LiteralPath $config.StatePath -Force
  }

  throw
}
