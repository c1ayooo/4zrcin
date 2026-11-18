#!/usr/bin/env bash
# 本机跑数据竞争检测（需要 cgo / C 工具链）。
#
# 与 scripts/race.ps1 和 Makefile 的 race 目标是同一组包、同一组参数：
#   bash scripts/race.sh
#   make race
#   powershell -ExecutionPolicy Bypass -File scripts/race.ps1
#
# 需要 Go 1.26+ 和 gcc。Windows 可用 winget 安装 MinGW-w64：
#   winget install BrechtSanders.WinLibs.POSIX.UCRT
# 装完重开终端，`gcc --version` 应输出版本。没有 C 工具链时用
# WSL/容器/Linux 跑同一脚本。
set -uo pipefail

# 与 Makefile 的 RACE_PKGS、scripts/race.ps1 的 $packages 保持一致。
packages=(
  ./internal/api
  ./internal/lifecycle
  ./internal/verifier
  ./internal/store
  ./internal/decider
  ./internal/board
  ./internal/collab
)

usage() {
  cat <<'EOF'
用法: scripts/race.sh

对与 make race / scripts/race.ps1 相同的包跑 go test -race。
需要 CGO_ENABLED=1 可用的 C 工具链（gcc）。本脚本不修改任何项目数据。
EOF
}

if [ "${1:-}" = "--help" ] || [ "${1:-}" = "-h" ]; then
  usage
  exit 0
fi
if [ $# -gt 0 ]; then
  echo "未知参数: $1（可用 --help）" >&2
  exit 2
fi

cc=$(go env CC)
if ! command -v "$cc" >/dev/null 2>&1; then
  echo "race 检测需要 C 工具链（cgo）：找不到 '$cc'。" >&2
  echo "请用 WSL/容器/Linux 跑同一脚本，或安装 MinGW-w64（见 CONTRIBUTING）。" >&2
  exit 1
fi

export CGO_ENABLED=1
exec go test -race -count=1 -timeout 30m "${packages[@]}"
