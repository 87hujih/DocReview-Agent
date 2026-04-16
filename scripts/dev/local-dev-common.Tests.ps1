Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$helperPath = Join-Path $PSScriptRoot "local-dev-common.ps1"
. $helperPath

Describe "Get-LocalDevConfig" {
  It "includes tika in compose managed services and endpoints" {
    $repoRoot = "G:\gofile\Agent_Project"
    $scriptRoot = "G:\gofile\Agent_Project\scripts\dev"

    $config = Get-LocalDevConfig -RepoRoot $repoRoot -ScriptRoot $scriptRoot

    $config.ComposeServices.Count | Should Be 1
    $config.ComposeServices[0] | Should Be "tika"
    $config.Urls.Server | Should Be "http://127.0.0.1:18080/healthz"
    $config.Urls.Web | Should Be "http://127.0.0.1:3000"
    $config.Urls.Tika | Should Be "http://127.0.0.1:9998/tika"
  }
}

Describe "New-LocalDevState" {
  It "records compose managed tika metadata in state output" {
    $repoRoot = "G:\gofile\Agent_Project"
    $scriptRoot = "G:\gofile\Agent_Project\scripts\dev"
    $config = Get-LocalDevConfig -RepoRoot $repoRoot -ScriptRoot $scriptRoot

    $state = New-LocalDevState `
      -Config $config `
      -StartedAtUtc "2026-04-16T08:00:00.0000000Z" `
      -Server @{
        pid = 101
        start_time_utc = "2026-04-16T08:00:01.0000000Z"
        stdout = "server.stdout.log"
        stderr = "server.stderr.log"
        url = $config.Urls.Server
      } `
      -Web @{
        pid = 202
        start_time_utc = "2026-04-16T08:00:02.0000000Z"
        stdout = "web.stdout.log"
        stderr = "web.stderr.log"
        url = $config.Urls.Web
      }

    $state.infrastructure.compose_services.Count | Should Be 1
    $state.infrastructure.compose_services[0] | Should Be "tika"
    $state.infrastructure.tika.service | Should Be "tika"
    $state.infrastructure.tika.url | Should Be $config.Urls.Tika
  }
}

Describe "Get-LocalDevTrackedProcessStatus" {
  It "treats datetime start_time_utc values from ConvertFrom-Json as the same process" {
    $tracked = @{
      pid = $PID
      start_time_utc = (Get-Process -Id $PID).StartTime.ToUniversalTime()
    }

    Get-LocalDevTrackedProcessStatus -Tracked $tracked | Should Be "running"
  }
}
