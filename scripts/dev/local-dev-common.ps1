Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Get-LocalDevConfig {
  param(
    [Parameter(Mandatory = $true)]
    [string]$RepoRoot,

    [Parameter(Mandatory = $true)]
    [string]$ScriptRoot
  )

  $serverStdout = Join-Path $RepoRoot "server.18080.stdout.log"
  $serverStderr = Join-Path $RepoRoot "server.18080.stderr.log"
  $webStdout = Join-Path $RepoRoot "web.3000.stdout.log"
  $webStderr = Join-Path $RepoRoot "web.3000.stderr.log"

  return [ordered]@{
    RepoRoot        = $RepoRoot
    ScriptRoot      = $ScriptRoot
    WebRoot         = Join-Path $RepoRoot "apps\\web"
    StatePath       = Join-Path $ScriptRoot ".local-dev-state.json"
    ComposeFile     = Join-Path $RepoRoot "docker-compose.yml"
    ComposeServices = @("tika")
    Logs            = [ordered]@{
      ServerStdout = $serverStdout
      ServerStderr = $serverStderr
      WebStdout    = $webStdout
      WebStderr    = $webStderr
    }
    Urls            = [ordered]@{
      Server = "http://127.0.0.1:18080/healthz"
      Web    = "http://127.0.0.1:3000"
      Tika   = "http://127.0.0.1:9998/tika"
    }
    Ports           = [ordered]@{
      Tika = 9998
    }
  }
}

function Test-LocalDevHttpReady {
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

function Wait-LocalDevHttpReady {
  param(
    [Parameter(Mandatory = $true)]
    [string]$Url,

    [Parameter(Mandatory = $true)]
    [int]$TimeoutSeconds
  )

  $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
  while ((Get-Date) -lt $deadline) {
    if (Test-LocalDevHttpReady -Url $Url) {
      return $true
    }

    Start-Sleep -Seconds 1
  }

  return $false
}

function Test-LocalDevTcpReady {
  param(
    [Parameter(Mandatory = $true)]
    [string]$HostName,

    [Parameter(Mandatory = $true)]
    [int]$Port
  )

  $client = New-Object System.Net.Sockets.TcpClient
  try {
    $async = $client.BeginConnect($HostName, $Port, $null, $null)
    $connected = $async.AsyncWaitHandle.WaitOne(2000, $false)
    if (-not $connected) {
      return $false
    }

    $client.EndConnect($async)
    return $true
  } catch {
    return $false
  } finally {
    $client.Dispose()
  }
}

function Wait-LocalDevTcpReady {
  param(
    [Parameter(Mandatory = $true)]
    [string]$HostName,

    [Parameter(Mandatory = $true)]
    [int]$Port,

    [Parameter(Mandatory = $true)]
    [int]$TimeoutSeconds
  )

  $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
  while ((Get-Date) -lt $deadline) {
    if (Test-LocalDevTcpReady -HostName $HostName -Port $Port) {
      return $true
    }

    Start-Sleep -Seconds 1
  }

  return $false
}

function New-TrackedProcessRecord {
  param(
    [Parameter(Mandatory = $true)]
    [System.Diagnostics.Process]$Process,

    [Parameter(Mandatory = $true)]
    [string]$Stdout,

    [Parameter(Mandatory = $true)]
    [string]$Stderr,

    [Parameter(Mandatory = $true)]
    [string]$Url
  )

  return [ordered]@{
    pid            = $Process.Id
    start_time_utc = $Process.StartTime.ToUniversalTime().ToString("o")
    stdout         = $Stdout
    stderr         = $Stderr
    url            = $Url
  }
}

function New-LocalDevState {
  param(
    [Parameter(Mandatory = $true)]
    [hashtable]$Config,

    [Parameter(Mandatory = $true)]
    [string]$StartedAtUtc,

    [Parameter(Mandatory = $true)]
    [hashtable]$Server,

    [Parameter(Mandatory = $true)]
    [hashtable]$Web
  )

  return [ordered]@{
    started_at_utc = $StartedAtUtc
    infrastructure = [ordered]@{
      compose_services = @($Config.ComposeServices)
      tika             = [ordered]@{
        service = "tika"
        url     = $Config.Urls.Tika
      }
    }
    server         = $Server
    web            = $Web
  }
}

function ConvertTo-LocalDevUtcTimestamp {
  param(
    [Parameter(Mandatory = $true)]
    [object]$Value
  )

  if ($Value -is [datetime]) {
    return $Value.ToUniversalTime().ToString("o")
  }

  if ($Value -is [string]) {
    $parsed = $null
    if ([datetime]::TryParse($Value, [ref]$parsed)) {
      return $parsed.ToUniversalTime().ToString("o")
    }
  }

  return [string]$Value
}

function Get-LocalDevTrackedProcessStatus {
  param(
    [Parameter(Mandatory = $true)]
    [hashtable]$Tracked
  )

  $proc = Get-Process -Id $Tracked.pid -ErrorAction SilentlyContinue
  if ($null -eq $proc) {
    return "missing"
  }

  $actualStart = $proc.StartTime.ToUniversalTime().ToString("o")
  $trackedStart = ConvertTo-LocalDevUtcTimestamp -Value $Tracked.start_time_utc
  if ($actualStart -ne $trackedStart) {
    return "pid-reused"
  }

  return "running"
}

function Stop-LocalDevTrackedProcess {
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
  $trackedStart = ConvertTo-LocalDevUtcTimestamp -Value $Tracked.start_time_utc
  if ($actualStart -ne $trackedStart) {
    Write-Host "PID $($Tracked.pid) 已被其它进程复用，跳过停止。"
    return
  }

  & taskkill.exe /PID $Tracked.pid /T /F | Out-Null
  if ($LASTEXITCODE -ne 0) {
    Write-Host "停止进程树 $($Tracked.pid) 失败，已跳过。"
    return
  }

  Write-Host "已停止进程树 $($Tracked.pid)。"
}

function Invoke-LocalDevCompose {
  param(
    [Parameter(Mandatory = $true)]
    [hashtable]$Config,

    [Parameter(Mandatory = $true)]
    [string[]]$Arguments
  )

  $docker = Get-Command docker -ErrorAction SilentlyContinue
  if ($null -eq $docker) {
    throw "未找到 docker 命令，无法托管本地 PostgreSQL / Tika 基础设施。"
  }

  & $docker.Source compose -f $Config.ComposeFile @Arguments
  if ($LASTEXITCODE -ne 0) {
    throw "docker compose $($Arguments -join ' ') 执行失败。"
  }
}
