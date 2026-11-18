#!/usr/bin/env pwsh
# Thin local-gate entry for Windows.
#
# All checks live in cmd/tw-gate (one implementation shared with scripts/gate.sh),
# so Windows and Linux run exactly the same list. This script only locates the
# repository root, runs the gate and forwards its arguments and exit code.
#
#   pwsh -File scripts/gate.ps1
#   pwsh -File scripts/gate.ps1 -skip-web -manifest-out .build/build-manifest.json
#   pwsh -File scripts/gate.ps1 -manifest-only -no-model-probe
#
# Run `pwsh -File scripts/gate.ps1 -h` for every option.
$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $PSScriptRoot
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    [Console]::Error.WriteLine("找不到 go：需要 Go 1.26+（winget install GoLang.Go 或 https://go.dev/dl/），装好后重开终端。")
    exit 1
}

Push-Location $root
try {
    & go run ./cmd/tw-gate @args
    exit $LASTEXITCODE
}
finally {
    Pop-Location
}
