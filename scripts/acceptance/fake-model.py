#!/usr/bin/env python3
# 假模型服务（docs/19 §11 P6「一次本地意图跑通，含一次扫描工具调用」的本地替身）。
# 纯 Python 3 标准库，不联网、不要真模型与真密钥：
#
#   - POST <任意>/chat/completions：按请求体分流。
#       stream=false（引擎启动探测，provider.TestLLM）→ 普通 JSON chat.completion；
#       stream=true（回合调用）→ text/event-stream。消息里还没有工具结果时回
#       「调用 --tool，参数 --arguments」的工具调用；已有工具结果（存在 role=tool
#       的消息）时回最终文本回复。两种回合应答都是 SSE，最后一帧 data: [DONE]。
#   - GET <任意>/models：OpenAI 列表形状（引擎当前不调它，留着以防将来调）。
#   - 其它任何方法与路径：200 / text/plain / 正文 zrcin-acceptance-target——
#     同一个服务就是扫描工具的本地目标。
#
# 帧的形状按引擎的解析器对齐（cyber-harness agent/provider/openai.go
# parseOpenAIStreamChunk + agent/loop.go messageBuilder）：delta.tool_calls 按
# index 累积、function.arguments 顺序拼接，所以工具调用整包放在一帧里；
# usage 放在 choices 为空的帧（stream_options.include_usage 的约定），给固定的
# 非零数字方便断言。每个请求（方法、路径、请求体全文）追加进 --log，场景断言读它。
#
# 用法（scripts/acceptance 的场景脚本这么用它）：
#   fake-model.py --port 0 --log /tmp/fake.log --tool bash --arguments '{"command":"curl -s http://127.0.0.1:{{PORT}}/target"}'
# 监听成功后向 stdout 打印一行 PORT=<端口号> 并 flush，脚本靠这一行拿到端口；
# {{PORT}} 占位符在应答里替换成本服务自己的端口。
import argparse
import json
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

ARGS = None  # set in main
LOCK = threading.Lock()

TARGET_BODY = "zrcin-acceptance-target"
MODEL_ID = "fake"
# 固定的非零 usage：场景断言只要求非零与一致，不追求真实。
USAGE = {"prompt_tokens": 42, "completion_tokens": 7, "total_tokens": 49}


def log(text: str) -> None:
    with LOCK:
        with open(ARGS.log, "a", encoding="utf-8") as f:
            f.write(text + "\n")


def has_tool_result(messages) -> bool:
    """消息序列里是否已经带着工具的结果：任何 role=tool 的消息都算。"""
    for m in messages or []:
        if isinstance(m, dict) and m.get("role") == "tool":
            return True
        # content 数组形态的兜底：content 部件里带 tool 结果标记也算。
        content = m.get("content") if isinstance(m, dict) else None
        if isinstance(content, list):
            for part in content:
                if isinstance(part, dict) and part.get("type") == "tool_result":
                    return True
    return False


def completion_id() -> str:
    return "chatcmpl-fake-0000000000000000"


def nonstream_reply(model: str, content: str) -> bytes:
    """启动探测（stream=false）的应答：普通 JSON chat.completion。"""
    payload = {
        "id": completion_id(),
        "object": "chat.completion",
        "created": 0,
        "model": model or MODEL_ID,
        "choices": [{
            "index": 0,
            "message": {"role": "assistant", "content": content},
            "finish_reason": "stop",
        }],
        "usage": dict(USAGE),
    }
    return json.dumps(payload).encode()


def sse_frame(payload: dict) -> bytes:
    return b"data: " + json.dumps(payload).encode() + b"\n\n"


def stream_reply(model: str, kind: str, tool_name: str, tool_arguments: str, final_text: str) -> bytes:
    """回合应答（stream=true）：SSE 流，帧形见文件头注释。kind: call|final。"""
    out = bytearray()
    base = {"id": completion_id(), "object": "chat.completion.chunk", "created": 0,
            "model": model or MODEL_ID}

    def chunk(delta, finish=None):
        frame = dict(base)
        frame["choices"] = [{"index": 0, "delta": delta, "finish_reason": finish}]
        out.extend(sse_frame(frame))

    def usage_frame():
        frame = dict(base)
        frame["choices"] = []
        frame["usage"] = dict(USAGE)
        out.extend(sse_frame(frame))

    chunk({"role": "assistant", "content": ""})
    if kind == "call":
        chunk({"tool_calls": [{
            "index": 0,
            "id": "call_fake_0001",
            "type": "function",
            "function": {"name": tool_name, "arguments": tool_arguments},
        }]})
        chunk({}, "tool_calls")
    else:
        chunk({"content": final_text})
        chunk({}, "stop")
    usage_frame()
    out.extend(b"data: [DONE]\n\n")
    return bytes(out)


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def _body(self) -> bytes:
        length = int(self.headers.get("Content-Length") or 0)
        return self.rfile.read(length) if length else b""

    def _reply(self, payload: bytes, code: int = 200, ctype: str = "application/json") -> None:
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def do_GET(self):
        body = self._body()
        log(json.dumps({"method": self.command, "path": self.path,
                        "body": body.decode("utf-8", "replace")}, ensure_ascii=False))
        if self.path.endswith("/models"):
            payload = json.dumps({"object": "list",
                                  "data": [{"id": MODEL_ID, "object": "model"}]}).encode()
            self._reply(payload)
            return
        self._reply(TARGET_BODY.encode(), 200, "text/plain")

    def do_POST(self):
        body = self._body()
        log(json.dumps({"method": self.command, "path": self.path,
                        "body": body.decode("utf-8", "replace")}, ensure_ascii=False))
        if not self.path.endswith("/chat/completions"):
            self._reply(TARGET_BODY.encode(), 200, "text/plain")
            return
        try:
            req = json.loads(body or b"{}")
        except ValueError:
            req = {}
        model = req.get("model") or MODEL_ID
        messages = req.get("messages") or []
        if req.get("stream") is not True:
            # 启动探测或显式非流式调用：普通 JSON 应答。
            self._reply(nonstream_reply(model, "pong"))
            return
        if has_tool_result(messages):
            # 工具结果已经回到模型：给最终回复，回合闭环。
            final = "扫描完成：目标返回 zrcin-acceptance-target。"
            self._reply(stream_reply(model, "final", ARGS.tool, "", final),
                        ctype="text/event-stream")
            return
        # 还没有工具结果：回「调用 --tool，参数 --arguments」。
        arguments = ARGS.arguments.replace("{{PORT}}", str(HOST_PORT))
        self._reply(stream_reply(model, "call", ARGS.tool, arguments, ""),
                    ctype="text/event-stream")

    def log_message(self, fmt, *args):  # 静默默认的 stderr 访问日志，记录走 --log
        pass


HOST_PORT = 0  # set in main, after bind


def main() -> None:
    global ARGS, HOST_PORT
    p = argparse.ArgumentParser()
    p.add_argument("--port", type=int, default=0, help="监听端口；0 = 随机空闲端口")
    p.add_argument("--log", required=True, help="请求日志文件（追加写）")
    p.add_argument("--tool", default="bash", help="第一回合让模型调用的工具名")
    p.add_argument("--arguments", default="{}", help="工具参数 JSON 串；{{PORT}} 替换为本服务端口")
    ARGS = p.parse_args()
    srv = ThreadingHTTPServer(("127.0.0.1", ARGS.port), Handler)
    HOST_PORT = srv.server_address[1]
    print(f"PORT={HOST_PORT}", flush=True)
    try:
        srv.serve_forever()
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()
