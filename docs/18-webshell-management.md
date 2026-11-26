# WebShell 管理（docs/18）

受管 webshell 是项目的**控制状态**：一份台账（地址、协议、封存的连接凭据、存活状态），加上两个执行
体——操作员在控制台直接操作，以及执行回合里的模型经 harness 工具操作。两个执行体共用同一张操作词表
（独立模块 `4zwebshell` 的 `webshell/ops.go`）和同一本台账，所以两条路不会漂移。

核心约束：**操作在节点上执行**。目标看到的出口是节点的地址，不是控制台进程的；控制面只排队、
等待回执、记账。凭据只在两个执行体的内存里短暂存在，台账只存 AES-GCM 密文。

## 1. 能力范围（v1）

| 操作 | 词表 | 说明 |
| --- | --- | --- |
| 存活检查 | `ping` | v2 走协议探测；legacy 以命令回显判定（未回显即失联） |
| 运行时信息 | `info` | 仅 v2 PHP 壳实现了 `I` op；其它壳按不支持处理 |
| 命令执行 | `exec` | 输出上限 64 KiB（`webshell.MaxOutput`），台账保留 8 KiB |
| 文件管理 | `files` | `list/read/write/delete/rename/mkdir/chmod`；读默认 256 KiB、上限 512 KiB；写单块 ≤256 KiB，`start+offset` 支持断点续传 |
| 自删除 | `remove` | 仅 v2：先请求自删除，再用独立探测复核（404/410 或协议不再响应才算消失）；legacy 无法自删除，只能人工删除后从台账移除 |

不搬入 v1：Java 内存马（`memshell_*`）、密钥轮换（`R`）、内网会话/隧道/撤离链。协议包
协议库里保留了这些代码（与 4zreco 同源），但没有任何接口面暴露它们。

## 2. 台账与密钥纪律

- 对象：`store.Shell`（`internal/store/shell.go`），落在项目账本里，`schemaVersion = 15`。
  与 assets/operator_requests 同类：`flushNoRev` 记账，**不进内容摘要、不唤醒指挥官、不参与报告**。
- 身份是地址：同一 URL 在存活期间只能有一行（`ErrShellDuplicate`）；`removed` 之后地址释放，可在同路径投放新壳。
- 凭据：v2 的通信密钥、legacy 的连接口令，都作为**整个 `webshell.Credential` 的 JSON** 封存后写入
  `Shell.Secret`（`webshell.Sealer`，密钥由实例签名密钥派生；`Sealer.Ready()` 为假时登记接口返回 503，
  绝不用可推导的密钥封存）。实例密钥轮换后旧行解封失败，接口明确报「请重新登记」，不做兜底。
- 视图不含凭据：`shellView`（`internal/api/shell.go`）没有 `secret` 字段；列表/更新/操作响应都不回传密文。
  明文只经 `GET /api/shells/{id}/credential`（`Cache-Control: no-store`）交给需要自己执行壳的 harness 工具。
- 输出记账：`last_output` 与 `last_checked` 随操作更新；只有操作**证明**了的事实才写状态——ping 定存活、
  成功的 exec 记存活、自删除以复核为准；失败的 exec 不改变存活状态（命令可能在健康的壳上失败）。

## 3. 两条执行路径

### 3.1 操作员直达（已实现）

操作员直达有**两个执行器**，按操作节点分流（`POST /api/shells/{id}/op` 内部解析）：

**本机 / 直连节点（命令通道）**——浏览器控制同款的直接命令通道：控制台把任务写成 case 目录里的
一次性票据（`<case>/.shells/<id>.json`，0600），在该节点宿主的控制会话上发
`!"${ZRCIN_BIN:-<zrcin>}" shell run --task …`，scan 进程里的执行器读票、**先删票再执行**、
输出一条回执 JSON。本机是默认 vantage；**直连 AOP 节点**同样走这条通道（各自独立的控制会话，
`control-session-<节点>.json`），前提是与控制台共享项目目录、且节点能解析到 zrcin 可执行文件。
控制会话持久化（宿主重启后自动重开一次）；命令未确认即最终失败（操作可能已执行，不自动重试）。
凭据只在票据文件里，不进 argv、日志与回执。

**zrcin-node（隧道节点）**——指令通道：`enroll.Service.RunShell` → `/node/next` →
节点内 `webshell.Run` → `POST /node/result` → API 记账并回显。

```
控制台 WebShell tab ──HTTP──▶ project API
                                ├─ node_id == 本机 ──▶ 控制会话 "!zrcin shell run --task …"
                                │                        └─ 本机 scan 进程 webshell.Run（出口=控制台主机）
                                └─ node_id == 隧道节点 ──Enqueue──▶ 节点长轮询 /node/next
                                                            └─ 节点内 webshell.Run（出口=节点）
                                 ◀── 两个执行器都回 API 记账并回显 ──
```

### 3.2 模型路径（设计定稿，待实现）

harness 里的 `webshell_*` 工具按 harness 自己的类型化工具模版构建（`coretool.Tool`：
`Name/Description/Definition/Execute` + `ParseArgs` + `TextResult`，经 `exts/webshell` 装配）。工具
**自己执行** `webshell.Run`（流量同样从节点发出），台账经平台的参与者 API 读写：

- 读：`GET /api/shells`（列表，无凭据）、`GET /api/shells/{id}/credential`（连接材料）。这些路由
  挂在参与者 mux（`/agent/<项目>/api/…`），会话用每回合身份 `$ZRCIN_AUTH` 调，与
  `GET /api/access/{id}/material` 同一先例；地址来自回合环境 `$ZRCIN_AGENT`。
- 写：`POST /api/shells`（`source: model`，`run_id` 取自身份）登记新壳；操作观察经
  `POST /api/shells/{id}/outcome`（`{op, result, error}`）回写，服务端用与操作员路径**同一套**判定
  更新 `status/last_output/last_checked`——同一个壳不会在两个面上读出不同的存活状态。

选择按需拉取而不是「每回合把全项目凭据推给每个节点」：只有真正执行该壳的节点才拿到那一把密钥。

## 4. 指令协议（docs/14 §7.2 的扩展）

```go
type Directive struct {
    Seq   uint64      `json:"seq"`
    Op    string      `json:"op"`              // "uninstall" | "shell"
    Shell *ShellTask  `json:"shell,omitempty"` // op=shell 时必填
}
type ShellTask struct {
    RequestID  string              // 与回执配对
    URL        string
    Credential webshell.Credential // 明文凭据：只走 TLS + zn1 凭据的接入面，不落盘、不记日志
    Op         webshell.Op
    NotAfter   time.Time           // 过期不执行
}
type ShellResult struct { RequestID string; Result *webshell.Result; Error string }
```

语义：

- **至多一次**：长轮询取出即清空；调用方 `RunShell` 等待回执，超时（`enroll.ShellWait` 75s 或调用方
  自己的 deadline）就报错，**不重试**——操作可能已经执行过。
- **过期不执行**：节点取到指令时若 `NotAfter` 已过，不执行，只回一条「已过期，未执行」。
- **不阻塞指令通道**：shell 操作在节点内异步执行（并发上限 4），同一批里的 `uninstall` 不会被慢目标拖住。
- **一次只回一个节点自己的任务**：`ShellResult` 按 `(nodeID, requestID)` 配对，别的节点无法代替回执。
- **并发上限**：单节点在途 16 个（`enroll.ShellInFlightMax`），超出直接拒绝而不是无限排队。
- 节点凭据被吊销（410/401）时，回执发送停止重试；结果 POST 失败会做有限次重试（回执幂等），
  操作本身不重试。

## 5. 接口面

项目 mux（`/p/<项目>/api/…`，控制台与指挥官会话）：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/shells` | 列表（`shellView`，无凭据） |
| POST | `/api/shells` | 登记：不给凭据=生成新 v2 壳（返回部署文件，仅一次）；给 key/carrier/password=接入已有壳 |
| PUT | `/api/shells/{id}` | 改名称/节点/备注（身份字段不可改） |
| DELETE | `/api/shells/{id}` | 只删台账行，不动目标文件 |
| POST | `/api/shells/{id}/op` | 在绑定节点上执行一个 `webshell.Op` |

错误语义：请求级问题用错误状态（404 壳不存在、400 参数非法/未绑定节点、409 地址重复或凭据无法解封、
503 本实例没有节点指令通道或没有封存密钥、502 节点没执行）；操作到达节点并返回结果（包括壳自己报错）一律
200，响应带 `result` 与 `error`。

参与者 mux（`/agent/<项目>/api/…`，会话身份）：`GET /api/shells`（列表）、
`GET /api/shells/{id}/credential`（连接材料）、`POST /api/shells`（source=model）、
`POST /api/shells/{id}/outcome`（观察回执）。整个 `shells` 段在项目面上对会话隐藏
（`agentRouteHidden`），会话只走参与者面。

## 6. 控制台

执行环境组新增 **WebShell** tab（`web/src/views/Shells.tsx`）：左列台账（状态点、类型/协议、来源），
右列详情（地址/节点/协议、存活检查、信息、命令、目录列出与文件读写、自删除、删除记录）与「登记」表单
（新建或接入、选执行节点）。部署文件只在登记响应里出现一次，复制后手工投放。

## 7. package 形态：独立模块 `4zwebshell`

协议与工具是一个独立 Go module（`github.com/c1ayooo/4zwebshell`，本地路径 `../4zwebshell`，
与两个仓库都可用 `replace` 指向同一份源码）：

| 包 | 内容 | 依赖 |
| --- | --- | --- |
| `webshell/` | 协议客户端（v2 加密/legacy）、文件管理、内存马、操作词表 `Op/Run`、凭据封存 `Sealer` | 仅标准库 |
| `tools/` | 7 个类型化工具（harness `coretool.Tool` 模版）：`webshell_list/register/exec/files/info/remove` | harness 的 `core/tool`、`aop`、`core/operation` |
| `extension/` | 按 harness 的扩展契约装配（`New(Config) → Load(scope) → extension.Add`） | harness 的 `core/extension` |

4zrcin 只依赖 `webshell/`（台账、API、节点执行器）；cyber-harness 依赖 `tools/` + `extension/`
（scan 产品在 `extensions()` 里加一行装配）。**协议只有一份源码**，两个消费者的回归测试在模块里跑。

与 4zreco 的差异（有意保留在两处）：

1. **修了一个假阳性**：legacy 壳按「地址+口令」重建时 `Marker` 为空，而 `strings.Contains(body, "")`
   恒真——任何 200 页面都会被判成「存活」。现在空 marker 不参与判定（4zreco 可同步回补）。
2. **legacy 只用于接入**：不提供「生成新 legacy 壳」，只有 v2 能新造；legacy 明文协议仅为兼容目标上
   已有的壳。

## 8. 验收与已知限制

- 单测：`4zwebshell/webshell`（协议、密封、词表、假壳、PHP 运行时 E2E 在装了 php 的机器上跑）、`4zwebshell/tools`（身份门、台账读取、执行与回执、登记、错误分级）、`4zwebshell/extension`（装配）、
  `internal/store`（台账 roundtrip/校验/幂等/记账不唤醒）、`internal/enroll`（配对、超时、并发上限、
  ingress 回执）、`cmd/zrcin-node`（在本机执行并回传、过期不执行、不阻塞 uninstall、并发上限）、
  `internal/api`（登记/接入/更新/操作/各错误语义/视图不含凭据）、`web/tests/shells.test.ts`、
  `web/tests/runtime-tabs.test.ts`。
- 限制：操作员直达路径覆盖**本机、直连 AOP 节点**（命令通道，要求共享项目目录与节点侧 zrcin
  可解析）与 **zrcin-node 形态的隧道节点**（指令通道）。本机/直连路径的回执经直接命令通道，
  `ShellWait` 超时后操作可能仍在目标上继续。指令队列与配对都在内存里，进程重启会丢掉在途操作
  （调用方看到超时）；legacy 壳不能自删除；文件写入一次一块、无二进制上传 UI（文本写入经 base64
  传输，二进制需走命令或 harness 工具）。
