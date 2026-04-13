Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

# 基于脚本目录计算仓库根目录，避免依赖调用方当前工作目录。
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\\..")).Path
$webRoot = Join-Path $repoRoot "apps\\web"
$statePath = Join-Path $PSScriptRoot ".local-dev-state.json"
$serverStdout = Join-Path $repoRoot "server.18080.stdout.log"
$serverStderr = Join-Path $repoRoot "server.18080.stderr.log"
$webStdout = Join-Path $repoRoot "web.3000.stdout.log"
$webStderr = Join-Path $repoRoot "web.3000.stderr.log"
$serverUrl = "http://127.0.0.1:18080/healthz"
$webUrl = "http://127.0.0.1:3000"
$pwshPath = Join-Path $PSHOME "pwsh.exe"

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

function Wait-HttpReady {
  param(
    [Parameter(Mandatory = $true)]
    [string]$Url,

    [Parameter(Mandatory = $true)]
    [int]$TimeoutSeconds
  )

  $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
  while ((Get-Date) -lt $deadline) {
    if (Test-HttpReady -Url $Url) {
      return $true
    }

    Start-Sleep -Seconds 1
  }

  return $false
}

function Stop-TrackedProcess {
  param(
    [Parameter(Mandatory = $true)]
    [System.Diagnostics.Process]$Process
  )

  if ($null -eq $Process -or $Process.HasExited) {
    return
  }

  Stop-Process -Id $Process.Id -Force
}

if (Test-Path -LiteralPath $statePath) {
  & (Join-Path $PSScriptRoot "stop-local.ps1") | Out-Host
}

if (Test-HttpReady -Url $serverUrl) {
  throw "检测到 $serverUrl 已可访问，但它不在当前项目状态文件中。请先手动释放 18080 端口后再重试。"
}

if (Test-HttpReady -Url $webUrl) {
  throw "检测到 $webUrl 已可访问，但它不在当前项目状态文件中。请先手动释放 3000 端口后再重试。"
}

$serverProcess = $null
$webProcess = $null

try {
  $serverCommand = "& { Set-Location '$repoRoot'; `$env:SERVER_PORT='18080'; go run ./apps/server/cmd/server }"
  $serverProcess = Start-Process `
    -FilePath $pwshPath `
    -ArgumentList @("-NoLogo", "-NoProfile", "-Command", $serverCommand) `
    -WorkingDirectory $repoRoot `
    -RedirectStandardOutput $serverStdout `
    -RedirectStandardError $serverStderr `
    -PassThru

  if (-not (Wait-HttpReady -Url $serverUrl -TimeoutSeconds 45)) {
    throw "后端未在 45 秒内就绪。stdout: $serverStdout ; stderr: $serverStderr"
  }

  $webCommand = "& { Set-Location '$webRoot'; `$env:NEXT_PUBLIC_API_URL='http://127.0.0.1:18080'; npm run dev -- --hostname 127.0.0.1 --port 3000 }"
  $webProcess = Start-Process `
    -FilePath $pwshPath `
    -ArgumentList @("-NoLogo", "-NoProfile", "-Command", $webCommand) `
    -WorkingDirectory $webRoot `
    -RedirectStandardOutput $webStdout `
    -RedirectStandardError $webStderr `
    -PassThru

  if (-not (Wait-HttpReady -Url $webUrl -TimeoutSeconds 90)) {
    throw "前端未在 90 秒内就绪。stdout: $webStdout ; stderr: $webStderr"
  }

  $state = [ordered]@{
    started_at_utc = (Get-Date).ToUniversalTime().ToString("o")
    server         = @{
      pid            = $serverProcess.Id
      start_time_utc = $serverProcess.StartTime.ToUniversalTime().ToString("o")
      stdout         = $serverStdout
      stderr         = $serverStderr
      url            = $serverUrl
    }
    web            = @{
      pid            = $webProcess.Id
      start_time_utc = $webProcess.StartTime.ToUniversalTime().ToString("o")
      stdout         = $webStdout
      stderr         = $webStderr
      url            = $webUrl
    }
  }

  $state | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath $statePath -Encoding UTF8

  Write-Host "本地联调环境已启动。"
  Write-Host "server: $serverUrl"
  Write-Host "web:    $webUrl"
  Write-Host "state:  $statePath"
} catch {
  Stop-TrackedProcess -Process $webProcess
  Stop-TrackedProcess -Process $serverProcess

  if (Test-Path -LiteralPath $statePath) {
    Remove-Item -LiteralPath $statePath -Force
  }

  throw
}
