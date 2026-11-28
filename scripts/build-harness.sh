#!/usr/bin/env sh
# Build the rc10 Hub + scan node from the independent tianwu/fixes fork.
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
harness="${HARNESS_DIR:-$root/.build/harness-src}"

command -v go >/dev/null 2>&1 || {
  echo "找不到 go：请安装 Go 1.26+ 并确保在 PATH 中。" >&2
  exit 1
}

[ -f "$harness/go.mod" ] || {
  echo "找不到 $harness/go.mod：本仓库不内置 harness/" >&2
  echo "先单独 clone c1ayooo/cyber-harness 的 tianwu/fixes，再用 HARNESS_DIR 指过去：" >&2
  echo "  git clone --branch tianwu/fixes https://github.com/c1ayooo/cyber-harness.git .build/harness-src" >&2
  echo "  HARNESS_DIR=.build/harness-src ./scripts/build-harness.sh" >&2
  exit 1
}

out="${1:-$root/bin/cyber-harness}"
# Relative -Out/-ScanOut are resolved against the repository root, exactly like
# scripts/build-harness.ps1; resolving against the caller's cwd made the two
# entry points disagree when invoked from another directory.
case "$out" in /*) ;; *) out="$root/$out" ;; esac
scan_out="${2:-$(dirname "$out")/cyber-scan}"
case "$scan_out" in /*) ;; *) scan_out="$root/$scan_out" ;; esac
mkdir -p "$(dirname "$out")" "$(dirname "$scan_out")"

cd "$harness"
tags="forceposix emptytemplates noembed osusergo sqlite"
# Cross-compiling for Windows must retain its native system DNS resolver too.
target_os="${GOOS:-$(go env GOOS)}"
if [ "$target_os" != "windows" ]; then tags="$tags netgo"; fi
# c1ayooo/* 的 fork 模块不在 sum.golang.org 的公共校验库里，必须跳过 sumdb 校验；
# 完整性仍由 go.sum 的哈希锁定，安全边界不变。
export GONOSUMDB="${GONOSUMDB:-github.com/c1ayooo/*}"
# An existing GOPROXY wins; the fast mirror is only the fallback. This matches
# the PowerShell script, which also leaves an operator-set GOPROXY alone.
GOPROXY="${GOPROXY:-https://goproxy.cn,direct}" \
  go build -tags "$tags full" -o "$out" ./cmd/cyber-harness
GOPROXY="${GOPROXY:-https://goproxy.cn,direct}" \
  go build -tags "$tags" -o "$scan_out" ./cmd/cyber-scan
echo "ok: $out"
"$out" --version
"$scan_out" --version
# A capture node is this same cyber-scan binary launched with --capture-only
# (docs/19 P6) — there is no second artifact to build here.
