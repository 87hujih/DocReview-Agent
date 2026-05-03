Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

Describe "status-local.ps1" {
  It "stays compatible with old state files that do not include infrastructure metadata" {
    $statePath = Join-Path $PSScriptRoot ".local-dev-state.json"
    $backupPath = "$statePath.bak"
    $hadOriginal = Test-Path -LiteralPath $statePath

    try {
      if ($hadOriginal) {
        Copy-Item -LiteralPath $statePath -Destination $backupPath -Force
      }

      @'
{
  "started_at_utc": "2026-04-16T08:00:00.0000000Z",
  "server": {
    "pid": 111,
    "start_time_utc": "2026-04-16T08:00:01.0000000Z",
    "stdout": "server.stdout.log",
    "stderr": "server.stderr.log",
    "url": "http://127.0.0.1:18080/healthz"
  },
  "web": {
    "pid": 222,
    "start_time_utc": "2026-04-16T08:00:02.0000000Z",
    "stdout": "web.stdout.log",
    "stderr": "web.stderr.log",
    "url": "http://127.0.0.1:3000"
  }
}
'@ | Set-Content -LiteralPath $statePath -Encoding UTF8

      $output = & pwsh -File (Join-Path $PSScriptRoot "status-local.ps1") 2>&1
      $joined = ($output | ForEach-Object { $_.ToString() }) -join "`n"

      $LASTEXITCODE | Should Be 0
      $joined | Should Match "tika_url:"
      $joined | Should Match "tika_tcp:"
    } finally {
      if ($hadOriginal) {
        Move-Item -LiteralPath $backupPath -Destination $statePath -Force
      } else {
        Remove-Item -LiteralPath $statePath -Force -ErrorAction SilentlyContinue
        Remove-Item -LiteralPath $backupPath -Force -ErrorAction SilentlyContinue
      }
    }
  }
}
