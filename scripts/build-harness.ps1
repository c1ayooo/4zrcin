# Build the rc10 Hub + scan node from the independent tianwu/fixes fork.
# -Out names the Hub; -ScanOut defaults beside it.
param(
    [string]$HarnessDir = "",
    [string]$Out = "./bin/cyber-harness.exe",
    [string]$ScanOut = "",
    [string]$Proxy = "https://goproxy.cn,direct"
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot

$go = Get-Command go -ErrorAction SilentlyContinue
if (-not $go) { throw "找不到 go：请安装 Go 1.26+ 并确保在 PATH 中（新开一个终端再试）。" }

if (-not $HarnessDir) { $HarnessDir = Join-Path $root ".build/harness-src" }
elseif (-not [System.IO.Path]::IsPathRooted($HarnessDir)) { $HarnessDir = Join-Path $root $HarnessDir }

$harnessPath = $HarnessDir
if (-not (Test-Path -LiteralPath $harnessPath)) {
    throw "找不到 harness 源码：$HarnessDir`n本仓库不内置 harness/。先单独 clone：`n  git clone --branch tianwu/fixes https://github.com/c1ayooo/cyber-harness.git <目录>`n再用 -HarnessDir 指向该目录。"
}
$harnessPath = (Resolve-Path -LiteralPath $harnessPath).Path
if (-not (Test-Path (Join-Path $harnessPath "go.mod"))) {
    throw "$harnessPath 不像 harness 源码（没有 go.mod）。请 checkout c1ayooo/cyber-harness 的 tianwu/fixes 分支。"
}

$env:GOPROXY = if ($env:GOPROXY) { $env:GOPROXY } else { $Proxy }
# c1ayooo/* 的 fork 模块不在 sum.golang.org 的公共校验库里，必须跳过 sumdb 校验；
# 完整性仍由 go.sum 的哈希锁定，安全边界不变。
if (-not $env:GONOSUMDB) { $env:GONOSUMDB = "github.com/c1ayooo/*" }
if (-not [System.IO.Path]::IsPathRooted($Out)) { $Out = Join-Path $root $Out }
New-Item -ItemType Directory -Path (Split-Path -Parent $Out) -Force | Out-Null

if (-not $ScanOut) { $ScanOut = Join-Path (Split-Path -Parent $Out) 'cyber-scan.exe' }
if (-not [System.IO.Path]::IsPathRooted($ScanOut)) { $ScanOut = Join-Path $root $ScanOut }
New-Item -ItemType Directory -Path (Split-Path -Parent $ScanOut) -Force | Out-Null
$tags = "forceposix emptytemplates noembed osusergo sqlite"
# Windows' native resolver does not require CGO. Forcing netgo bypasses the
# system DNS cache/NRPT and can fail while ordinary Windows clients work.
$targetOS = $env:GOOS
if (-not $targetOS) { $targetOS = (& go env GOOS).Trim() }
if ($targetOS -ne "windows") { $tags += " netgo" }
Write-Host "building cyber-harness + cyber-scan from $harnessPath"
Push-Location $harnessPath
try {
    go build -tags "$tags full" -o $Out ./cmd/cyber-harness
    if ($LASTEXITCODE -ne 0) { throw 'Hub build failed' }
    go build -tags $tags -o $ScanOut ./cmd/cyber-scan
    if ($LASTEXITCODE -ne 0) { throw 'scan node build failed' }
} finally {
    Pop-Location
}
Write-Host "ok: $Out"
& $Out --version
& $ScanOut --version
