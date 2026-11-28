#!/usr/bin/env bash
# scripts/acceptance 共用脚手架：single-file.sh 与 local-turn.sh 都 source 本文件。
# 这里只有函数定义；TMP / MINE / trap cleanup 归各场景脚本自己声明（cleanup 读的
# 是调用方 shell 的同名全局），lib 不持有状态。
#
# 隔离规则（两个脚本共同遵守，出处 single-file.sh 文件头）：机器上可能有正在服务
# 的实例，本目录的脚本绝不碰它——全部临时目录、全部空闲端口（含 Hub 的
# --host-addr，不用 18766 默认值）、退出时 trap 收掉自己起的每个进程并确认没有
# 孤儿。进程登记表是各脚本的 MINE 数组。

# alive 判定把僵尸态算作已死：被 kill -9 的子进程在脚本（父进程）退出前可能
# 仍是未 reap 的僵尸，kill -0 依然应答，但它不是活着的进程。
alive() {
  [ -n "${1:-}" ] && kill -0 "$1" 2>/dev/null &&
    [ "$(awk '/^State:/{print $2}' "/proc/$1/status" 2>/dev/null)" != "Z" ]
}

free_port() {
  python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'
}

wait_for() { # desc timeout_s cmd…
  local desc=$1 timeout=$2; shift 2
  local deadline=$((SECONDS + timeout))
  while (( SECONDS < deadline )); do
    if "$@" >/dev/null 2>&1; then return 0; fi
    sleep 0.5
  done
  echo "FAIL: 等待超时（${timeout}s）：$desc" >&2
  return 1
}

# cleanup 是 EXIT trap 的主体：先 TERM、给优雅退出一个窗口、再补刀，最后如实
# 报告孤儿并按原退出码退出。TMP 与 MINE 来自调用脚本的全局。
acceptance_cleanup() {
  local rc=$?
  for pid in "${MINE[@]:-}"; do
    [ -n "$pid" ] || continue
    kill "$pid" 2>/dev/null || continue
  done
  # 给优雅退出一个窗口，再补刀，最后如实报告孤儿。
  local waited=0
  while (( waited < 20 )); do
    alive=0
    for pid in "${MINE[@]:-}"; do
      alive "$pid" && alive=$((alive + 1))
    done
    [ "$alive" -eq 0 ] && break
    sleep 0.5
    waited=$((waited + 1))
  done
  for pid in "${MINE[@]:-}"; do
    [ -n "$pid" ] && kill -9 "$pid" 2>/dev/null || true
  done
  # The instance was started inside a command substitution, so it is not this
  # shell's child and `wait` cannot wait for it: a SIGKILLed 200MB process can
  # still be tearing itself down when the next line runs. Give it a moment
  # before calling it an orphan, or a clean run fails on a process that was
  # merely on its way out (seen once, right after a full gate had loaded the
  # machine).
  waited=0
  while (( waited < 20 )); do
    alive=0
    for pid in "${MINE[@]:-}"; do
      alive "$pid" && alive=$((alive + 1))
    done
    [ "$alive" -eq 0 ] && break
    sleep 0.25
    waited=$((waited + 1))
  done
  local orphans=0
  for pid in "${MINE[@]:-}"; do
    wait "$pid" 2>/dev/null || true
    alive "$pid" && orphans=$((orphans + 1))
  done
  rm -rf "$TMP"
  if (( orphans > 0 )); then
    echo "FAIL: $orphans 个进程没被收掉（孤儿）" >&2
    exit 1
  fi
  exit $rc
}

# start_zrcin data_dir console_port hub_port extra-flags… → 打印 zrcin pid。
# 只打印、不登记：函数跑在 $( ) 命令替换的子 shell 里，在那里改 MINE 到不了
# 父脚本——pid 必须由调用方收进 MINE，否则 cleanup 永远收不掉实例。
# （那次事故的说明保留：这正是曾经让脚本在机器上留了 6 组孤儿实例的坑。）
#
# START_ZRCIN_ENV：（可选）调用方在调用前填充的 KEY=VALUE 数组，逐项追加在
# -u 清除项之后传给 env。默认不设——现有场景的子进程环境与抽取前逐字节一致
# （CYBER_* 一律清掉，机器上导出的同名变量不许漏进来）；需要模型配置的场景
# （local-turn）用它给实例设 CYBER_*。
start_zrcin() {
  local data=$1 console=$2 hubport=$3; shift 3
  local -a env_args=(-u CYBER_BASE_URL -u CYBER_API_KEY -u CYBER_MODEL -u CYBER_PROVIDER \
                     -u ZRCIN_POSTGRES_DSN -u ZRCIN_HARNESS_CONFIG)
  local kv
  for kv in ${START_ZRCIN_ENV+"${START_ZRCIN_ENV[@]}"}; do
    env_args+=("$kv")
  done
  (
    cd "$TMP"
    env "${env_args[@]}" \
        GOWORK=off GOFLAGS=-mod=readonly \
        "$TMP/zrcin" --data "$data" --addr "127.0.0.1:$console" \
        --host-addr "127.0.0.1:$hubport" --harness-config "$TMP/cyber.yaml" "$@" \
        > "$data/zrcin.log" 2>&1 &
    echo $! > "$data/zrcin.pid"
  )
  cat "$data/zrcin.pid"
}

login_token() { # console_port → JWT
  local port=$1
  curl -sf -X POST "http://127.0.0.1:$port/api/auth/login" \
    -H 'Content-Type: application/json' \
    -d '{"username":"operator","password":"SingleFile2026"}' |
    python3 -c 'import json,sys;print(json.load(sys.stdin)["data"]["token"])'
}

create_project() { # console_port token name → project id
  curl -sf -X POST "http://127.0.0.1:$1/api/projects" \
    -H "Authorization: Bearer $2" -H 'Content-Type: application/json' \
    -d "{\"name\":\"$3\"}" |
    python3 -c 'import json,sys;print(json.load(sys.stdin)["id"])'
}

local_node_online() { # console_port token project_id → exit 0 当本地节点在线
  # 本地行的在线以 host.online 为准：那是监督器端到端核验（ListAgents + 工作目录）
  # 的结论；顶层的 online 只有被发现的扫描行才带（探测器的裁决）。
  curl -sf "http://127.0.0.1:$1/p/$3/api/nodes" -H "Authorization: Bearer $2" |
    python3 -c 'import json,sys
d = json.load(sys.stdin)
rows = [n for n in d.get("nodes", []) if n.get("local")]
def up(n):
    if n.get("online") is True:
        return True
    host = n.get("host") or {}
    return host.get("online") is True and not host.get("unhealthy")
sys.exit(0 if rows and all(up(n) for n in rows) else 1)'
}
