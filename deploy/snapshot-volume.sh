#!/usr/bin/env bash
# 卷快照：把一个目录打成 tar + sha256（docs/16 §11，每天一份基线）。
#
# 用法：deploy/snapshot-volume.sh <目录> <输出目录>
#   deploy/snapshot-volume.sh /var/lib/docker/volumes/zrcin_zrcin-data/_data /backups
#
# zrcin 数据卷内 SQLite/文件没有事务一致性保证——快照前先停写（compose 里
# docker compose stop zrcin）或接受崩溃级一致（SQLite 页可能混版本）。
# PostgreSQL 卷不要用它：库的备份走 pg_basebackup + WAL 归档，见恢复手册。
set -euo pipefail

src=${1:?用法：snapshot-volume.sh <目录> <输出目录>}
out=${2:?用法：snapshot-volume.sh <目录> <输出目录>}

[ -d "$src" ] || { echo "snapshot-volume: $src 不是目录" >&2; exit 1; }
mkdir -p "$out"

name="snapshot-$(date +%Y%m%d-%H%M%S)-$(basename "$src").tar.gz"
tmp="$out/.${name}.partial"

tar -C "$src" -czf "$tmp" .
mv "$tmp" "$out/$name"
sha256sum "$out/$name" > "$out/$name.sha256"
echo "$out/$name"
echo "$out/$name.sha256"
