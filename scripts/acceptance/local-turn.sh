#!/usr/bin/env bash
# 假模型回合验收（docs/19 §11 P6 的「一次本地意图跑通，含一次扫描工具调用」）。
# 单文件实例（-tags inprochost）+ 本地假模型服务（fake-model.py），不联网、不要
# 真模型与真密钥，整条链路真跑一遍：
#
#   控制台 → 内嵌 Hub → 本机节点（zrcin node agent）→ 假模型（SSE 回工具调用）
#   → bash 执行 curl 打到假模型服务的本地目标 → 工具结果随第二个模型请求回给
#   假模型 → 最终回复 → 回合正常结束。
#
# 断言（全部落在实证的接口与事件形状上）：
#   1. 实例就绪且没有进降级：日志有 host_event=ready，没有 host_event=degraded
#      （启动探测 provider.TestLLM 是非流式调用，假模型对它回普通 JSON）。
#   2. 目标按产品流程进了围栏：项目创建时带 scope_rules（allow 行 host=127.0.0.1，
#      default_action=review），资产表里有 in_fence 的 127.0.0.1 行——回环地址按
#      产品设计就可以入栏，场景不做任何围栏放宽。
#   3. 意图跑到终态且是 done（不是错误退出）。
#   4. run 事件里有一次 bash 工具调用，且有配对的 tool_result：ok=true、正文含
#      zrcin-acceptance-target（目标真实应答的标记正文）——证明请求真的穿过了
#      围栏打到目标并回来了，不是「不在授权范围」「被拒绝」「派发失败」。
#   5. 假模型日志的第二个 /chat/completions 请求的消息里带着 role=tool 的工具
#      结果——闭环是工具结果回到模型后由模型收尾，不是模型自己编的。
#
# 与 single-file.sh 的分工：共用脚手架在 lib.sh；本脚本与它的差异只有一处结构
# 性理由——start_zrcin 对四个既有场景写死 env -u CYBER_*（机器上导出的模型变量
# 不许漏进去），本场景要的恰好相反，所以单独成脚本，经 lib 的 START_ZRCIN_ENV
# 传入。四个既有场景一个不动。
#
# 隔离与依赖同 single-file.sh（见 lib.sh 文件头）：全部临时目录、全部空闲端口、
# trap 只收自己 MINE 里的 pid；结束确认没有孤儿。
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/../.." && pwd)"
ENGINE="${ENGINE:-$REPO/../cyber-harness}"

for tool in go python3 curl; do
  command -v "$tool" >/dev/null 2>&1 || { echo "FAIL: 缺依赖 $tool" >&2; exit 1; }
done
[ -x "$ENGINE/go.mod" ] || [ -f "$ENGINE/go.mod" ] || { echo "FAIL: 找不到引擎检出 $ENGINE（inprochost 构建需要）" >&2; exit 1; }

# shellcheck source=lib.sh
source "$HERE/lib.sh"

TMP="$(mktemp -d /tmp/local-turn.XXXXXX)"
declare -a MINE=()
trap acceptance_cleanup EXIT

echo "== 构建 zrcin（-tags inprochost）"
GOWORK=off GOFLAGS=-mod=readonly go build -tags inprochost -o "$TMP/zrcin" ./cmd/zrcin
touch "$TMP/cyber.yaml"

# ---- 1 · 起假模型服务 --------------------------------------------------------
FAKE_LOG="$TMP/fake-model.log"
: > "$FAKE_LOG"
python3 "$HERE/fake-model.py" --port 0 --log "$FAKE_LOG" \
  --tool bash \
  --arguments '{"command":"curl -s -m 10 http://127.0.0.1:{{PORT}}/target"}' \
  > "$TMP/fake.port" 2>&1 &
FAKE_PID=$!
MINE+=("$FAKE_PID")
wait_for "假模型服务打印 PORT=" 15 grep -q '^PORT=[0-9]*$' "$TMP/fake.port"
FPORT="$(grep -o 'PORT=[0-9]*' "$TMP/fake.port" | cut -d= -f2)"

# ---- 2 · 起实例（模型指向假服务）---------------------------------------------
D1="$TMP/instance"; mkdir -p "$D1"
C1=$(free_port); H1=$(free_port)
START_ZRCIN_ENV=(
  "CYBER_BASE_URL=http://127.0.0.1:$FPORT/v1"
  "CYBER_API_KEY=acceptance-fake"
  "CYBER_MODEL=fake"
  "CYBER_PROVIDER=openai"
)
ZPID="$(start_zrcin "$D1" "$C1" "$H1")"
MINE+=("$ZPID")
wait_for "实例控制台应答" 60 curl -sf "http://127.0.0.1:$C1/healthz"

# ---- 3 · 等就绪；断言没有进降级 ----------------------------------------------
# 启动探测走同一个假服务：探测通过 → host_event=ready。degraded 一旦出现就是
# 探测失败（模型链路没通），直接失败，不给场景放行。
wait_for "实例就绪（host_event=ready）" 90 grep -q "host_event=ready" "$D1/zrcin.log"
if grep -q "host_event=degraded" "$D1/zrcin.log"; then
  echo "FAIL: 实例进了降级（host_event=degraded）：模型链路没通" >&2
  exit 1
fi

# ---- 4 · 登录；按产品流程建带围栏的项目 ---------------------------------------
curl -sf -X POST "http://127.0.0.1:$C1/api/auth/register" \
  -H 'Content-Type: application/json' \
  -d '{"username":"operator","password":"SingleFile2026"}' >/dev/null 2>&1 || true
T1="$(login_token "$C1")"
# 建项目时带 scope_rules 是产品内的正当授权路径（POST /api/projects 的
# scope_rules/fence 字段；hub 建项目时落清单并 seedFenceAssets）：操作员在开工时
# 就把目标圈进围栏。allow 行 action=record（转发并记录），default_action=review
# （范围外拒绝并进待审），与围栏页的固定缺省一致。host 不含端口：范围规则只按
# authority 匹配。回环地址是合法围栏项（normalizeScopeHost 收纯数字 label）。
P1="$(curl -sf -X POST "http://127.0.0.1:$C1/api/projects" \
  -H "Authorization: Bearer $T1" -H 'Content-Type: application/json' \
  -d '{"name":"local-turn","scope_rules":{"enabled":true,"default_action":"review","rules":[{"action":"record","method":"*","host":"127.0.0.1","path_prefix":"/"}]}}' |
  python3 -c 'import json,sys;print(json.load(sys.stdin)["id"])')"
fenced() { # 资产表里有 in_fence 的 127.0.0.1（枚举值本身叫 in_fence）
  curl -sf "http://127.0.0.1:$C1/p/$P1/api/assets/records" -H "Authorization: Bearer $T1" |
    python3 -c 'import json,sys
rows = json.load(sys.stdin).get("assets", [])
sys.exit(0 if any(a.get("key") == "127.0.0.1" and a.get("fence") == "in_fence" for a in rows) else 1)'
}
wait_for "目标 127.0.0.1 已入栏（in_fence 资产行）" 30 fenced

wait_for "本地节点上线" 120 local_node_online "$C1" "$T1" "$P1"

# ---- 5 · 建意图、运行、轮询终态 ------------------------------------------------
IID="$(curl -sf -X POST "http://127.0.0.1:$C1/p/$P1/api/intents" \
  -H "Authorization: Bearer $T1" -H 'Content-Type: application/json' \
  -d "{\"title\":\"local-turn\",\"description\":\"用 bash 工具执行 curl -s http://127.0.0.1:$FPORT/target ，把返回正文原样汇报\"}" |
  python3 -c 'import json,sys;print(json.load(sys.stdin)["id"])')"
RUN_ID="$(curl -sf -X POST "http://127.0.0.1:$C1/p/$P1/api/intents/$IID/run" \
  -H "Authorization: Bearer $T1" |
  python3 -c 'import json,sys;print(json.load(sys.stdin)["run_id"])')"

intent_status() { # → 意图当前状态（stdout）
  curl -sf "http://127.0.0.1:$C1/p/$P1/api/intents/$IID" -H "Authorization: Bearer $T1" |
    python3 -c 'import json,sys;print(json.load(sys.stdin)["intent"]["status"])'
}
deadline=$((SECONDS + 120))
STATUS=""
while :; do
  STATUS="$(intent_status 2>/dev/null || echo "")"
  case "$STATUS" in
    done|failed|review_failed|parked) break ;;
  esac
  (( SECONDS < deadline )) || { echo "FAIL: 等待意图终态超时（当前 ${STATUS:-未知}）" >&2; exit 1; }
  sleep 1
done
if [ "$STATUS" != "done" ]; then
  echo "FAIL: 意图终态是 $STATUS，不是 done（回合没有正常结束）" >&2
  exit 1
fi

# ---- 6 · run 事件：一次 bash 工具调用 + 它的真实结果 ---------------------------
RUN_EVENTS="$TMP/run-events.json"
curl -sf "http://127.0.0.1:$C1/p/$P1/api/runs/$RUN_ID/events" -H "Authorization: Bearer $T1" > "$RUN_EVENTS"
TOOL_PROOF="$(python3 - "$RUN_EVENTS" <<'PYEOF'
import json, sys
events = json.load(open(sys.argv[1], encoding="utf-8")).get("events", [])
calls = [e for e in events if e.get("kind") == "tool_call" and e.get("tool") == "bash"]
results = {e.get("call_id"): e for e in events if e.get("kind") == "tool_result" and e.get("tool") == "bash"}
if not calls:
    sys.exit("run 事件里没有 bash 的 tool_call")
for c in calls:
    r = results.get(c.get("call_id"))
    if r and r.get("ok") is True and "zrcin-acceptance-target" in (r.get("text") or ""):
        print(c.get("call_id"))
        sys.exit(0)
sys.exit("bash 的 tool_result 没有一个 ok=true 且带目标正文（可能被围栏拒绝或派发失败）")
PYEOF
)"

# ---- 7 · 假模型日志：第二个回合请求带着工具结果 --------------------------------
# 日志里的 /chat/completions 有两类：启动探测（stream=false，普通 JSON）和回合
# 调用（stream=true，SSE）。断言按回合过滤：第一个回合请求不该有工具结果（它
# 是发起调用的一步），第二个回合请求必须带着 role=tool 的结果——闭环由工具结果
# 回到模型后收尾，不是模型自己编的最终回复。
python3 - "$FAKE_LOG" <<'PYEOF'
import json, sys
turns = []
for line in open(sys.argv[1], encoding="utf-8"):
    try:
        e = json.loads(line)
    except ValueError:
        continue
    if e.get("method") != "POST" or not e.get("path", "").endswith("/chat/completions"):
        continue
    try:
        body = json.loads(e["body"])
    except ValueError:
        continue
    if body.get("stream") is True:
        turns.append(body)
if len(turns) < 2:
    sys.exit("假模型日志里的回合调用不足两次（%d）：回合没走到第二步" % len(turns))
def roles(b):
    return [m.get("role") for m in b.get("messages", [])]
if "tool" in roles(turns[0]):
    sys.exit("第一个回合请求就带了工具结果：%s" % roles(turns[0]))
if "tool" not in roles(turns[1]):
    sys.exit("第二个回合请求的消息角色是 %s，没有 role=tool 的工具结果" % roles(turns[1]))
print("回合角色序列：%s → %s（工具结果回到模型）" % (roles(turns[0]), roles(turns[1])))
PYEOF

echo "ok · 假模型回合：实例 ready 无降级 · 127.0.0.1 已入栏 · 意图 done · bash 调用($TOOL_PROOF)带真实目标结果 · 工具结果回到模型"
echo "PASS: local-turn · 单文件实例跑通一个真实回合（含一次扫描工具调用）"
