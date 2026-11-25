#!/usr/bin/env bash
# 上游雷达：巡检上游 main 的新提交，生成一份可读日报。
#
#   ./scripts/upstream-radar.sh            抓取上游 + 生成日报
#   ./scripts/upstream-radar.sh --no-fetch 不抓取，只读本地已有的上游 ref
#   make radar                             同 ./scripts/upstream-radar.sh
#
# 产物（默认 logs/upstream-radar/，已被 .gitignore 覆盖，不入库）：
#   <日期>.md   当天日报
#   latest.md   最近一次日报
#   last-seen   上次巡检到的上游提交，下次以它为基线
#   history.tsv 巡检流水（时间 / 上游提交 / 新增数）
#
# 环境变量：UPSTREAM_REMOTE（默认 upstream）、UPSTREAM_BRANCH（默认 main）、
#           RADAR_OUT（默认 logs/upstream-radar）
set -euo pipefail

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"

remote=${UPSTREAM_REMOTE:-upstream}
branch=${UPSTREAM_BRANCH:-main}
out_dir=${RADAR_OUT:-logs/upstream-radar}
fetch=1

for arg in "$@"; do
  case "$arg" in
    --no-fetch) fetch=0 ;;
    -h|--help) sed -n '2,17p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "未知参数：$arg（可用：--no-fetch）" >&2; exit 2 ;;
  esac
done

if [ "$fetch" -eq 1 ]; then
  if ! git fetch --quiet "$remote" "$branch"; then
    echo "抓取 $remote/$branch 失败：检查网络或 remote 配置（git remote -v）。" >&2
    exit 1
  fi
fi

up_ref="refs/remotes/$remote/$branch"
if ! git rev-parse --verify --quiet "$up_ref" >/dev/null; then
  echo "找不到 $remote/$branch。" >&2
  echo "先配置上游：git remote add upstream https://github.com/kksty/tianwu.git && git fetch upstream" >&2
  exit 1
fi

remote_url=$(git remote get-url "$remote" 2>/dev/null || echo "$remote")
up_sha=$(git rev-parse "$up_ref")
up_short=$(git rev-parse --short=7 "$up_ref")
up_date=$(git log -1 --format=%ad --date=format:'%Y-%m-%d %H:%M' "$up_ref")
head_short=$(git rev-parse --short=7 HEAD)
now=$(date '+%Y-%m-%d %H:%M')
today=$(date '+%Y-%m-%d')

mkdir -p "$out_dir"
state="$out_dir/last-seen"

# 分叉点：没有共同历史时为空。
mb=$(git merge-base HEAD "$up_ref" || true)
if [ -n "$mb" ]; then
  read -r ahead behind < <(git rev-list --left-right --count "HEAD...$up_ref")
  mb_short=$(git rev-parse --short=7 "$mb")
  fork="$mb_short"
else
  ahead=$(git rev-list --count HEAD --not "$up_ref")
  behind=$(git rev-list --count "$up_ref" --not HEAD)
  fork="无共同历史"
fi

# 本次巡检的对比基线：优先用上次记录的提交；上游改写历史时退回分叉点。
note=""
base=""
if [ -f "$state" ]; then
  last=$(tr -d '[:space:]' < "$state")
else
  last=""
fi
if [ -n "$last" ] && git cat-file -e "$last^{commit}" 2>/dev/null; then
  if git merge-base --is-ancestor "$last" "$up_ref"; then
    base=$last
  else
    note="上次巡检的提交 ${last:0:7} 已不在 $branch 上（上游可能改写了历史），本次从分叉点重新对齐。"
    base=$mb
  fi
else
  note="首次巡检：以分叉点 ${fork} 为基线。"
  base=$mb
fi

if [ -n "$base" ]; then
  range="$base..$up_ref"
  base_short=$(git rev-parse --short=7 "$base")
  new_count=$(git rev-list --count "$range")
else
  range=""
  base_short="-"
  new_count=$(git rev-list --count "$up_ref")
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
report="$tmp/report.md"
log="$tmp/new.tsv"

if [ -n "$range" ]; then
  git log --reverse --format='%H%x09%ad%x09%an%x09%s' --date=format:'%m-%d %H:%M' "$range" > "$log"
else
  git log --reverse --format='%H%x09%ad%x09%an%x09%s' --date=format:'%m-%d %H:%M' "$up_ref" > "$log"
fi

# 本次新增按 conventional 前缀计数（feat/fix/docs/...），其他一律算 other。
cats=$(awk -F'\t' '{
  s=$4
  t="other"
  if (match(s, /^[a-z]+/)) {
    w=substr(s,1,RLENGTH)
    if (w ~ /^(feat|fix|docs|refactor|test|tests|build|chore|style|perf|ci|revert)$/) t=w
  }
  print t
}' "$log" | sort | uniq -c | sort -rn | awk '{printf "%s %s · ", $2, $1}' | sed 's/ · $//')

{
  printf '# 上游雷达 · %s\n\n' "$today"
  printf -- '- 上游：`%s` · %s @ `%s`（%s）\n' "$remote_url" "$branch" "$up_short" "$up_date"
  printf -- '- 本地：HEAD @ `%s`\n' "$head_short"
  printf -- '- 分叉点：`%s` · 本地领先 %s / 落后 %s\n' "$fork" "$ahead" "$behind"
  printf -- '- 巡检时间：%s\n' "$now"
  [ -z "$note" ] || printf -- '- 注意：%s\n' "$note"
  printf '\n'

  printf '## 本次新增（%s 个提交）\n\n' "$new_count"
  if [ "$new_count" -eq 0 ]; then
    printf '自上次巡检（`%s`）以来，上游没有新提交。\n\n' "$base_short"
  else
    printf '构成：%s\n\n' "$cats"
    while IFS=$'\t' read -r sha cdate author subject; do
      short=$(git rev-parse --short=7 "$sha")
      printf '### `%s` %s · %s\n\n' "$short" "$subject" "$cdate"
      printf -- '- 作者：%s\n' "$author"
      parents=$(git rev-list --parents -n 1 "$sha" | wc -w)
      if [ "$parents" -gt 2 ]; then
        printf -- '- 合并提交（%s 个父提交），详情见 `git show %s`\n\n' "$((parents - 1))" "$short"
        continue
      fi
      shortstat=$(git show --shortstat --format='' "$sha" | tr -d '\n' | sed 's/^ *//')
      [ -n "$shortstat" ] && printf -- '- 改动量：%s\n' "$shortstat"
      printf -- '- 文件：\n'
      git diff-tree -M --no-commit-id --name-status -r "$sha" | awk -F'\t' 'NR<=25 {
        if ($1 ~ /^R/) printf "  - `%s` → `%s`（重命名）\n", $2, $3;
        else if ($1 == "A") printf "  - `%s`（新增）\n", $2;
        else if ($1 == "D") printf "  - `%s`（删除）\n", $2;
        else printf "  - `%s`\n", $2;
      }'
      total=$(git diff-tree -M --no-commit-id --name-only -r "$sha" | wc -l)
      [ "$total" -le 25 ] || printf '  - …另有 %s 个文件，见 `git show --stat %s`\n' "$((total - 25))" "$short"
      printf '\n'
    done < "$log"
  fi
} > "$report"

# 未合入积压：上游 main 上本地还没有的提交（含本次新增）。
{
  printf '## 未合入积压（%s 个提交，含本次）\n\n' "$behind"
  if [ "$behind" -eq 0 ]; then
    printf '本地已包含上游全部提交。\n\n'
  else
    git log -n 40 --format='- `%h` %ad %s' --date=short "HEAD..$up_ref"
    [ "$behind" -le 40 ] || printf -- '- …另有 %s 个，见 `git log HEAD..%s/%s`\n' "$((behind - 40))" "$remote" "$branch"
    printf '\n'
  fi
} >> "$report"

# 本地领先：我们自己的提交，合入上游时可能碰面。
{
  printf '## 本地领先（%s 个提交）\n\n' "$ahead"
  if [ "$ahead" -eq 0 ]; then
    printf '本地没有上游之外的提交。\n\n'
  else
    git log -n 20 --format='- `%h` %ad %s' --date=short "$up_ref..HEAD"
    [ "$ahead" -le 20 ] || printf -- '- …另有 %s 个\n' "$((ahead - 20))"
    printf '\n'
  fi
} >> "$report"

# 冲突热区：上游改过、本地也改过的文件。分开看已提交（真合并风险）和未提交（拉取前要处理）。
hot_committed=0
hot_dirty=0
if [ -n "$mb" ]; then
  git diff --name-only "$mb" "$up_ref" | LC_ALL=C sort -u > "$tmp/up.files"
  git diff --name-only "$mb" HEAD | LC_ALL=C sort -u > "$tmp/committed.files"
  { git diff --name-only HEAD; git ls-files --others --exclude-standard; } | LC_ALL=C sort -u > "$tmp/dirty.files"
  LC_ALL=C comm -12 "$tmp/up.files" "$tmp/committed.files" > "$tmp/hot.committed"
  LC_ALL=C comm -12 "$tmp/up.files" "$tmp/dirty.files" > "$tmp/hot.dirty"
  hot_committed=$(wc -l < "$tmp/hot.committed" | tr -d ' ')
  hot_dirty=$(wc -l < "$tmp/hot.dirty" | tr -d ' ')
fi

{
  printf '## 冲突热区\n\n'
  if [ "$hot_committed" -eq 0 ] && [ "$hot_dirty" -eq 0 ]; then
    printf '没有交集：上游改动和本地改动落在不同文件上。\n\n'
  fi
  if [ "$hot_committed" -gt 0 ]; then
    printf '### 已提交的本地改动（%s 个文件，合入时可能冲突）\n\n' "$hot_committed"
    head -20 "$tmp/hot.committed" | while IFS= read -r f; do
      last_up=$(git log -1 --format='%h %s' "$mb..$up_ref" -- "$f" 2>/dev/null || true)
      printf -- '- `%s`%s\n' "$f" "${last_up:+ ← $last_up}"
    done
    [ "$hot_committed" -le 20 ] || printf -- '- …另有 %s 个\n' "$((hot_committed - 20))"
    printf '\n'
  fi
  if [ "$hot_dirty" -gt 0 ]; then
    printf '### 工作区未提交（%s 个文件，拉取/合入前先提交或暂存）\n\n' "$hot_dirty"
    head -15 "$tmp/hot.dirty" | while IFS= read -r f; do
      printf -- '- `%s`\n' "$f"
    done
    [ "$hot_dirty" -le 15 ] || printf -- '- …另有 %s 个\n' "$((hot_dirty - 15))"
    printf '\n'
  fi
} >> "$report"

# 敏感改动：依赖、协议、生成产物。
{
  printf '## 敏感改动\n\n'
  hits=0
  if [ -n "$mb" ]; then
    for pat in '^go\.mod$' '^go\.sum$' '^package\.json$' '^pnpm-lock\.yaml$' '^internal/aop/' '^internal/web/static/' '\.proto$'; do
      files=$(git diff --name-only "$mb" "$up_ref" | grep -E "$pat" || true)
      if [ -n "$files" ]; then
        hits=1
        printf -- '- `%s`：%s 个文件\n' "$pat" "$(printf '%s\n' "$files" | wc -l | tr -d ' ')"
      fi
    done
  fi
  if [ "$hits" -eq 0 ]; then
    printf '无（依赖、proto、前端产物均未动）。\n'
  else
    printf '\n合入前建议：`make gate`，前端产物用 `make web` 重新生成，proto 用 `make sync-aop-protos` 之类的既有脚本同步。\n'
  fi
  printf '\n'
} >> "$report"

{
  printf '## 继续看\n\n'
  printf -- '- 完整 diff：`git log -p %s/%s`\n' "$remote" "$branch"
  printf -- '- 单个提交：`git show <sha>`\n'
  printf -- '- 只看某个文件：`git log -p %s/%s -- <path>`\n' "$remote" "$branch"
  printf -- '- 试合入（不提交）：`git merge --no-commit --no-ff %s/%s`，冲突后用 `git merge --abort` 退出\n' "$remote" "$branch"
} >> "$report"

cp "$report" "$out_dir/latest.md"
cp "$report" "$out_dir/$today.md"
printf '%s\n' "$up_sha" > "$state"
printf '%s\t%s\t%s\n' "$now" "$up_short" "$new_count" >> "$out_dir/history.tsv"

cat "$report"
