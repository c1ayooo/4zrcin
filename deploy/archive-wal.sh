#!/usr/bin/env bash
# PostgreSQL WAL 归档接收脚本。postgresql.conf 里配：
#   archive_mode = on
#   archive_command = '/绝对路径/deploy/archive-wal.sh %p %f'
#   archive_timeout = 60        # 归档间隔=RPO 上限，按 docs/16 §11 的口径
#
# 本地目录只是示例。生产环境把 copy_local 段换成对象存储上传，例如：
#   aws s3 cp "$src" "s3://$BUCKET/wal/$file"        # S3 兼容
#   rclone copyto "$src" "remote:bucket/wal/$file"   # rclone 任一后端
# 注意：archive_command 必须在「确已落到持久存储」后才返回 0；返回非 0
# PostgreSQL 会反复重试同一段，堆积 pg_wal。
set -euo pipefail

src=$1   # %p：待归段文件路径
file=$2  # %f：段文件名

# 归档目录：生产改成本机对象存储同步工具的目标，或直接换成云 CLI。
ARCHIVE_DIR=${ZRCIN_WAL_ARCHIVE_DIR:-/var/lib/zrcin/wal-archive}
mkdir -p "$ARCHIVE_DIR"

dest="$ARCHIVE_DIR/$file"
if [ -e "$dest" ]; then
  # 同名段已归档：只允许完全一致的重复（PostgreSQL 重试或时间线复用）。
  cmp -s "$src" "$dest" && exit 0
  echo "archive-wal: 归档目录已有 $file 但内容不同，拒绝覆盖" >&2
  exit 1
fi

# 先落临时名再 rename，保证 archive_command 返回 0 时目标已完整。
tmp="$ARCHIVE_DIR/.${file}.partial"
cp "$src" "$tmp"
mv "$tmp" "$dest"
chmod 600 "$dest"
