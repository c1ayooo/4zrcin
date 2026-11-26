#!/usr/bin/env bash
# server 形态冒烟（docs/16 §13 M0 第 7 条）：
#   (a) server 形态起实例（--postgres 指向测试库），/healthz 200 后核对 PG
#       里出现该实例的表，SIGTERM 停止；
#   (b) 桌面形态回滚检查：同一二进制、无 DSN 起 /healthz 200 后停止。
#
# 用法：bash deploy/smoke-server.sh <测试库 DSN>
#       DSN 通常来自：bash scripts/test-pg.sh（ZRCIN_TEST_PG_DSN 同一实例）
#
# 只用独立端口 18431（控制台）/18931（Hub），数据目录是临时目录，可重复
# 执行；不碰 8080/18799/18901 上正在运行的平台实例。
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
DSN=${1:?用法：deploy/smoke-server.sh <测试库 DSN>；先跑 bash scripts/test-pg.sh}
PORT_HTTP=18431
PORT_HOST=18931

work=$(mktemp -d /tmp/zrcin-smoke.XXXXXX)
bin="$work/zrcin"
mkdir -p "$work/server-data" "$work/desktop-data"
pids=()
cleanup() {
  for p in "${pids[@]:-}"; do
    kill -TERM "$p" 2>/dev/null || true
  done
  for p in "${pids[@]:-}"; do
    wait "$p" 2>/dev/null || true
  done
  rm -rf "$work"
}
trap cleanup EXIT

# ---- 构建：tags 判定与 cmd/zrcin-gate/main.go:160-177 一致 ----
tags=""
for cand in "$root/.build/harness-src" "$root/../cyber-harness"; do
  if [ -f "$cand/go.mod" ]; then tags="inprochost"; break; fi
done
if [ -d "$root/../rem" ]; then
  [ -n "$tags" ] && tags="$tags,"
  tags="${tags}remtunnel"
fi
echo "== go build（tags: ${tags:-无}）"
( cd "$root" && go build ${tags:+-tags "$tags"} -o "$bin" ./cmd/zrcin )

# Hub 与 scan 必须成对：bin/ 里有 gate 产物就直接用；没有才从 harness 源码现编。
extra_args=()
if [ -x "$root/bin/cyber-harness" ] && [ -x "$root/bin/cyber-scan" ]; then
  extra_args=(--harness "$root/bin/cyber-harness" --scan "$root/bin/cyber-scan")
  echo "== hub/scan：用 bin/ 现有产物"
else
  echo "== hub/scan：bin/ 缺产物，scripts/build-harness.sh 现编"
  ( cd "$root" && sh scripts/build-harness.sh )
  extra_args=(--harness "$root/bin/cyber-harness" --scan "$root/bin/cyber-scan")
fi

# ---- 端口预检：18431/18931 必须空闲，否则报出占用者并退出 ----
# 2026-11-27 的教训：早期版本用子 shell（不带 exec）起 zrcin，清理只杀了
# 子 shell，zrcin 变孤儿继续占端口应答 /healthz——后续运行全对孤儿判绿。
port_owner() { # $1=端口；输出 "pid=xx (proc)" 或空
  ss -ltnp 2>/dev/null | awk -v p=":$1" '$4 ~ p {print}' | \
    grep -o 'pid=[0-9]*' | head -1 | tr -d 'pid='
}
for port in $PORT_HTTP $PORT_HOST; do
  if owner=$(port_owner "$port"); [ -n "$owner" ]; then
    echo "端口 $port 已被 pid $owner 占用（$(ps -o args= -p "$owner" 2>/dev/null)）。" >&2
    echo "是上一次冒烟泄漏的实例就先 kill 掉它，不要让本次冒烟对着别人的 /healthz 判绿。" >&2
    exit 1
  fi
done

wait_healthz() { # $1=名字 $2=pid
  local pid=$2
  for _ in $(seq 1 120); do
    # 进程死了就立即失败：200 可能来自残留实例，别等满 120s。
    if ! kill -0 "$pid" 2>/dev/null; then
      echo "$1 进程已退出（启动失败？看 $work/*.log）" >&2
      return 1
    fi
    if curl -fsS "http://127.0.0.1:$PORT_HTTP/healthz" >/dev/null 2>&1; then
      # 200 还不够：端口必须真的在本 pid 手里，否则是残留实例在应答。
      if [ "$(port_owner "$PORT_HTTP")" = "$pid" ]; then
        echo "== $1 /healthz 200（pid $pid 在听 $PORT_HTTP）"
        return 0
      fi
      echo "$1 /healthz 200 但端口不是 pid $pid（实际：$(port_owner "$PORT_HTTP")），疑似残留实例" >&2
      return 1
    fi
    sleep 1
  done
  echo "$1：120s 内 /healthz 未就绪" >&2
  return 1
}

stop_pid() { # $1=名字 $2=pid
  kill -TERM "$2" 2>/dev/null || true
  for _ in $(seq 1 30); do
    kill -0 "$2" 2>/dev/null || { echo "== $1 已退出（SIGTERM）"; return 0; }
    sleep 1
  done
  echo "$1：SIGTERM 后 30s 未退出" >&2
  return 1
}

# ---- (a) server 形态 ----
echo "== (a) server 形态：--postgres + 临时数据目录"
( cd "$root" && exec "$bin" "${extra_args[@]}" \
    --data "$work/server-data" \
    --postgres "$DSN" \
    --addr "127.0.0.1:$PORT_HTTP" \
    --host-addr "127.0.0.1:$PORT_HOST" \
    --public-url "http://127.0.0.1:$PORT_HTTP" \
    >"$work/server.log" 2>&1 ) &
pid=$!
pids+=("$pid")
wait_healthz "server 形态" "$pid"

# PG 里应出现该实例建的表。实例状态在 instance schema（instance_state、
# users），项目账本按项目开在 ledger_p-<项目ID> schema（objects、versions、
# meta、fact_search、vectors、relations、ledger_format）。psql 在宿主上没有
# 时，借测试容器执行（容器名固定，见 scripts/test-pg.sh）。
pg() {
  if command -v psql >/dev/null 2>&1; then psql "$DSN" -Atc "$1"; else
    docker exec zrcin-test-pg psql -U postgres -d zrcin_test -Atc "$1"
  fi
}
before=$(pg "select table_schema from information_schema.tables
             where table_schema='instance'" | sort -u)
for t in instance_state users; do
  pg "select 1 from information_schema.tables
      where table_schema='instance' and table_name='$t'" | grep -q 1 \
    || { echo "PG 的 instance schema 缺少表 $t" >&2; exit 1; }
done
pg "select 1 from pg_extension where extname='pg_trgm'" | grep -q 1 \
  || { echo "PG 缺少 pg_trgm 扩展" >&2; exit 1; }
after=$(pg "select table_schema from information_schema.tables
            where table_schema='instance'" | sort -u)
new=$(comm -13 <(echo "$before") <(echo "$after") | tr '\n' ' ')
# 注：测试库会被多次冒烟复用，instance schema 常在首次就建好；表存在即算
# 通过，「新建 schema」只是本次是否为首个使用者的记录。
echo "== server 形态：instance.instance_state / instance.users / pg_trgm 齐${new:+（本次新建）}"
stop_pid "server 形态" "$pid"
pids=()
# 停了就必须真停：端口还被占着就是孤儿，后续判据全部作废。
for _ in $(seq 1 10); do
  [ -z "$(port_owner "$PORT_HTTP")" ] && break
  sleep 1
done
if owner=$(port_owner "$PORT_HTTP"); [ -n "$owner" ]; then
  echo "server 形态退出后 $PORT_HTTP 仍被 pid $owner 占用（孤儿进程）" >&2
  exit 1
fi
echo "== 端口 $PORT_HTTP 已释放"

# ---- (b) 桌面形态回滚检查：同一二进制、无 DSN ----
echo "== (b) 桌面形态：同一二进制、新临时目录、无 DSN"
"$bin" "${extra_args[@]}" \
  --data "$work/desktop-data" \
  --addr "127.0.0.1:$PORT_HTTP" \
  --host-addr "127.0.0.1:$PORT_HOST" \
  >"$work/desktop.log" 2>&1 &
pid=$!
pids+=("$pid")
wait_healthz "桌面形态" "$pid"
stop_pid "桌面形态" "$pid"

echo "== smoke-server 通过"
