#!/usr/bin/env pwsh
# Run Tianwu from source on Windows (no build step required).
#   ./run.ps1                      # default addr/data
#   ./run.ps1 -Harness "C:\path\cyber-harness.exe" -Scan "C:\path\cyber-scan.exe"
#   ./run.ps1 -Project acme-web    # open one project
param(
  [string]$Addr = "127.0.0.1:8080",
  [string]$Data = "./data",
  [string]$Project = "",
  [string]$Harness = "",
  [string]$Scan = "",
  [string]$Aiscan = ""
)

$ErrorActionPreference = "Stop"
$go = Get-Command go -ErrorAction SilentlyContinue
if (-not $go) {
  $guess = "C:\Program Files\Go\bin"
  if (Test-Path (Join-Path $guess "go.exe")) { $env:Path = "$guess;$env:Path" }
  else { throw "go not found. Install Go 1.26+ (winget install GoLang.Go)." }
}

$goArgs = @("run", "./cmd/tw", "--addr", $Addr, "--data", $Data)
if ($Project -ne "") { $goArgs += @("--project", $Project) }
if ($Harness -ne "") { $goArgs += @("--harness", $Harness) }
if ($Scan -ne "") { $goArgs += @("--scan", $Scan) }
if ($Aiscan -ne "") { $goArgs += @("--aiscan", $Aiscan) }

Write-Host "tianwu -> http://$Addr" -ForegroundColor Red
& go @goArgs
