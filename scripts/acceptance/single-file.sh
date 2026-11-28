#!/usr/bin/env bash
# 单文件验收（docs/19 §2 本地模式、P6-4）：一个 zrcin（-tags inprochost）就是
# 一个完整实例。四件事，每件一行 ok：
#
#   1. 一个文件起完整实例：临时 --data / 临时 --harness-config / 空闲端口，
#      等到节点列表里出现在线的本地节点（节点进程是 zrcin 自己）。
#   2. 杀掉节点子进程后自动恢复：监督器重启，节点重新在线。
#   3. --no-local-node：Hub 就绪（host_event=ready mode=hub_only）而节点列表
#      里没有本地节点。
#   4. 能力一致：独立的 cyber-scan agent 注册到同一个 Hub，它与 zrcin node
#      注册的能力（capabilities）与工具清单（commands）必须一致。
#
# 不覆盖的：「一次本地意图跑通，含一次扫描工具调用」需要模型，本脚本不做
# （handback 单列）。无模型时实例以降级态运行，节点的 online 与注册不受影响。
#
# 隔离：机器上可能有一个正在运行的实例，本脚本绝不碰它——全部临时目录、全部
# 空闲端口（含 Hub 的 --host-addr，不用 18766 默认值）、退出时 trap 收掉自己
# 起的每个进程并确认没有孤儿。不依赖模型与网络：不配置任何模型也跑通。
#
# 依赖：go、python3、openssl、curl、node（JSON 校验），以及引擎检出
# （../cyber-harness，或 HARNESS_DIR）；cyber-scan 优先用 $REPO/bin/cyber-scan
# （gate 刚构建过），没有就从引擎检出现编（慢）。
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/../.." && pwd)"
ENGINE="${ENGINE:-$REPO/../cyber-harness}"

for tool in go python3 openssl curl node; do
  command -v "$tool" >/dev/null 2>&1 || { echo "FAIL: 缺依赖 $tool" >&2; exit 1; }
done
[ -x "$ENGINE/go.mod" ] || [ -f "$ENGINE/go.mod" ] || { echo "FAIL: 找不到引擎检出 $ENGINE（inprochost 构建需要）" >&2; exit 1; }

TMP="$(mktemp -d /tmp/single-file.XXXXXX)"
declare -a MINE=()

cleanup() {
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
trap cleanup EXIT

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

# start_zrcin data_dir console_port hub_port extra-flags… → 打印 zrcin pid。
# 只打印、不登记：函数跑在 $( ) 命令替换的子 shell 里，在那里改 MINE 到不了
# 父脚本——pid 必须由调用方收进 MINE，否则 cleanup 永远收不掉实例。
start_zrcin() {
  local data=$1 console=$2 hubport=$3; shift 3
  (
    cd "$TMP"
    env -u CYBER_BASE_URL -u CYBER_API_KEY -u CYBER_MODEL -u CYBER_PROVIDER \
        -u ZRCIN_POSTGRES_DSN -u ZRCIN_HARNESS_CONFIG \
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

create_project() { # console_port token → project id
  curl -sf -X POST "http://127.0.0.1:$1/api/projects" \
    -H "Authorization: Bearer $2" -H 'Content-Type: application/json' \
    -d '{"name":"single-file"}' |
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

echo "== 构建 zrcin（-tags inprochost）"
GOWORK=off GOFLAGS=-mod=readonly go build -tags inprochost -o "$TMP/zrcin" ./cmd/zrcin
touch "$TMP/cyber.yaml"

# ---- 1 · 一个文件起完整实例 ------------------------------------------------
D1="$TMP/instance1"; mkdir -p "$D1"
C1=$(free_port); H1=$(free_port)
ZPID="$(start_zrcin "$D1" "$C1" "$H1")"
MINE+=("$ZPID")
wait_for "实例 1 控制台应答" 60 curl -sf "http://127.0.0.1:$C1/healthz"
curl -sf -X POST "http://127.0.0.1:$C1/api/auth/register" \
  -H 'Content-Type: application/json' \
  -d '{"username":"operator","password":"SingleFile2026"}' >/dev/null 2>&1 || true
T1="$(login_token "$C1")"
P1="$(create_project "$C1" "$T1")"
wait_for "本地节点上线（场景 1）" 120 local_node_online "$C1" "$T1" "$P1"
NODE_PID="$(python3 -c 'import json;print(json.load(open("'$D1'/host/local.json")).get("node_pid") or "")')"
[ -n "$NODE_PID" ] && kill -0 "$NODE_PID" 2>/dev/null
NODE_ARGV="$(tr '\0' ' ' < "/proc/$NODE_PID/cmdline" 2>/dev/null || true)"
case "$NODE_ARGV" in
  "$TMP/zrcin node agent"*) ;;
  *) echo "FAIL: 节点进程 argv 不是本可执行文件自执行：$NODE_ARGV" >&2; exit 1 ;;
esac
echo "ok 1 · 单文件起完整实例（console 127.0.0.1:$C1，节点 pid $NODE_PID = zrcin 自执行）"

# ---- 2 · 杀掉节点子进程后自动恢复 ------------------------------------------
kill -9 "$NODE_PID"
wait_for "节点自动恢复（场景 2）" 120 bash -c '
  new=$(python3 -c "import json;print(json.load(open(\"'$D1'/host/local.json\")).get(\"node_pid\") or \"\")")
  [ -n "$new" ] && [ "$new" != "'"$NODE_PID"'" ] && kill -0 "$new" 2>/dev/null'
wait_for "节点恢复后重新在线（场景 2）" 120 local_node_online "$C1" "$T1" "$P1"
NEW_NODE_PID="$(python3 -c 'import json;print(json.load(open("'$D1'/host/local.json")).get("node_pid") or "")')"
echo "ok 2 · 杀掉节点进程 $NODE_PID 后自动恢复为新进程 $NEW_NODE_PID 并重新在线"

# ---- 3 · --no-local-node ----------------------------------------------------
D2="$TMP/instance2"; mkdir -p "$D2"
C2=$(free_port); H2=$(free_port)
D2_PID="$(start_zrcin "$D2" "$C2" "$H2" --no-local-node)"
MINE+=("$D2_PID")
wait_for "纯控制面 Hub 就绪（场景 3）" 120 grep -q "host_event=ready .*mode=hub_only" "$D2/zrcin.log"
curl -sf -X POST "http://127.0.0.1:$C2/api/auth/register" \
  -H 'Content-Type: application/json' \
  -d '{"username":"operator","password":"SingleFile2026"}' >/dev/null 2>&1 || true
T2="$(login_token "$C2")"
P2="$(create_project "$C2" "$T2")"
no_local_node_absent() {
  curl -sf "http://127.0.0.1:$C2/p/$P2/api/nodes" -H "Authorization: Bearer $T2" |
    python3 -c 'import json,sys
d = json.load(sys.stdin)
rows = d.get("nodes", [])
sys.exit(0 if not any(n.get("local") or n.get("id") == "local" for n in rows) else 1)'
}
wait_for "节点列表稳定（场景 3）" 30 no_local_node_absent
no_local_node_absent
echo "ok 3 · --no-local-node：Hub 就绪（mode=hub_only），节点列表无本地节点"

# ---- 4 · 能力一致（独立 scan vs zrcin node）--------------------------------
SCAN_BIN="${SCAN_BIN:-$REPO/bin/cyber-scan}"
if [ ! -x "$SCAN_BIN" ]; then
  echo "== bin/cyber-scan 不存在，从引擎检出现编（一次性，较慢）"
  GOWORK=off make -C "$ENGINE" scan BIN_DIR="$TMP" TAGS="forceposix emptytemplates noembed osusergo sqlite netgo"
  SCAN_BIN="$TMP/cyber-scan"
fi
HUB_ENDPOINT="$(python3 -c 'import json;print(json.load(open("'$D1'/host/local.json"))["endpoint"])')"
HUB_HOSTPORT="${HUB_ENDPOINT#http://}"
SECRET="$(tr -d '[:space:]' < "$D1/jwt.key")"
# Hub 凭据派生（cmd/zrcin/auth.go hubAccessToken）：HMAC-SHA256(jwt.key, "4zrcin/harness-hub/v1\0"+dataDir)
HUB_TOKEN="$(printf '4zrcin/harness-hub/v1\x00%s' "$D1" | openssl dgst -sha256 -hmac "$SECRET" | awk '{print $NF}')"

printf '{}\n' > "$TMP/scan-node.yaml"
# IOA 的 node id 必须是 32 位十六进制（protocols.ValidID），与宿主给自己节点
# 生成的 id 同一格式；name 才是自由命名。
# 工作目录用实例 1 的数据目录：技能清单按工作目录下的 .cyber/skills 发现（D78），
# 是部署环境的效应而不是装配的差异（远程部署本来就不带技能，dist 不含 skills）。
# 环境拉平后，剩下的任何差异都是两份装配本身的差异。
PROBE_ID="$(python3 -c 'import secrets;print(secrets.token_hex(16))')"
(
  cd "$D1"
  env -u CYBER_BASE_URL -u CYBER_API_KEY -u CYBER_MODEL -u CYBER_PROVIDER \
    "$SCAN_BIN" agent --transport web \
    --server-url "http://$HUB_TOKEN@$HUB_HOSTPORT" \
    --node-id "$PROBE_ID" --node-name parity-probe \
    --observe http --timeout 0 --config "$TMP/scan-node.yaml" \
    > "$TMP/scan.log" 2>&1 &
  echo $! > "$TMP/scan.pid"
)
MINE+=("$(cat "$TMP/scan.pid")")

hub_agents() {
  curl -sf -X POST "$HUB_ENDPOINT/cyber.rpc.agent.AgentService/ListAgents" \
    -H 'Content-Type: application/json' -H 'Connect-Protocol-Version: 1' \
    -H "Authorization: Bearer $HUB_TOKEN" -d '{}'
}
LOCAL_AGENT_ID="$(python3 -c 'import json;print(json.load(open("'$D1'/host/local.json")).get("agent") or "")')"
agent_in_hub() {
  hub_agents | python3 -c 'import json,sys
d = json.load(sys.stdin)
ids = {a.get("hello", {}).get("nodeId") for a in d.get("agents", [])}
sys.exit(0 if {"'$LOCAL_AGENT_ID'", "'"$PROBE_ID"'"} <= ids else 1)'
}
wait_for "两个 agent 都注册到同一个 Hub（场景 4）" 120 agent_in_hub

agent_profile() { # nodeId → 排序后的能力 + 工具清单
  hub_agents | python3 -c 'import json,sys
d = json.load(sys.stdin)
want = sys.argv[1]
for a in d.get("agents", []):
    if a.get("hello", {}).get("nodeId") == want:
        for line in sorted(a.get("hello", {}).get("capabilities") or []):
            print("cap", line)
        for c in sorted(a.get("commands") or [], key=lambda c: c.get("name", "")):
            print("cmd", c.get("name", ""))
        sys.exit(0)
sys.exit(1)' "$1"
}
agent_profile "$LOCAL_AGENT_ID" > "$TMP/profile-local.txt"
agent_profile "$PROBE_ID" > "$TMP/profile-scan.txt"
if [ ! -s "$TMP/profile-local.txt" ]; then
  echo "FAIL: 本地节点没有可比较的能力/工具清单" >&2; exit 1
fi
if diff -u "$TMP/profile-local.txt" "$TMP/profile-scan.txt"; then
  echo "ok 4 · 能力一致：zrcin node 与独立 cyber-scan 的 $(wc -l < "$TMP/profile-local.txt") 条能力/工具清单完全一致"
else
  echo "FAIL: 能力不一致（上表为差异）" >&2
  exit 1
fi

echo "PASS: single-file · 单文件实例 / 节点自恢复 / 纯控制面 / 能力一致 全部通过"
