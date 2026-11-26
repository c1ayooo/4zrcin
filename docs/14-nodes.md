# 节点形态与部署（docs/14）

一次部署先回答两个问题：**节点是什么形态**（跑什么二进制、连谁），以及**它声明什么能力**（调度
允许什么落到它上面）。本文覆盖 `internal/node` 的 Spec 定义（`node.go` 引用的 "docs/14-nodes.md，
D64"）、`make harness` / `make scan-capture` 产物的部署，以及第四种形态——跨公网、自己回连中心的
**隧道节点**（接入、部署、状态与销毁，§7–§10）。

## 1. 节点形态三矩阵

| 形态 | 机器 | 跑的进程 | 节点登记 | 适用 |
| --- | --- | --- | --- | --- |
| 单机一体包 | 一台 | `bin/zrcin`（内嵌 Hub 与本机 scan 宿主） | 自动，id=`local` | 个人/单机验收 |
| 中心 + 多台 scan agent | 一台中心 + N 台执行机 | 中心：`zrcin`（`--harness`/`--scan` 子进程）；执行机：`cyber-scan` | 中心 `<data>/nodes.json` 加条目（§2） | 分布式扫描/记录 |
| 远程成对 Hub+scan | 执行机自管 Hub | 执行机：`cyber-harness` + `cyber-scan` 成对 | `nodes.json` 指向该机 AOP 端点 | 执行机有自己的运维边界 |
| 中心 + 隧道回连节点 | 中心 + N 台操作员主机 | 中心：`zrcin --public-url …`（rem 服务端、一次性票据、产物分发）；执行机：`zrcin-node`（内置 rem 客户端）+ `cyber-scan` | 自动：接入记录 → 节点档案（`<data>/remote-nodes.json`） | 跨公网、主机归操作员、无共享目录 |

`nodes.json`（`File{default, nodes[]}`，`node.go`）除本机 `local` 外声明远程节点：

```json
{
  "default": "local",
  "nodes": [
    {"id": "fence", "aop": "http://10.0.0.9:18766", "token": "…",
     "note": "围栏记录节点", "capabilities": ["scan-capture"]}
  ]
}
```

## 2. scan agent 接入步骤（`--transport web`）

`cyber-scan` **不监听端口**：作为 web 客户端出站连 Hub（`internal/host` 的
`agent --transport web --server-url …`；四形态见 harness `docs/agent.md`）。控制台
"节点" 页的「生成原生接入命令」产出带 token 的完整命令；流程：

1. 中心侧：节点页「生成原生接入命令」→ 在执行机运行
   `cyber-scan '<token>' …`（含 `--transport web` 与 server-url）。这条「直连」路径要求执行机与
   控制台共享项目目录、能访问 Hub 与控制台 API；跨公网的主机走 §7 的隧道接入。
2. 执行机 hello 注册后，中心自动发现节点（`Registry.RefreshNodes`，3s TTL）。
3. 中心侧在节点页把新节点「启用到项目」（绑定 allowlist），可选设为项目默认。

## 3. 构建配方（docs/04 §3.3）

| 配方 | 构建命令 | 编入 | 排除 |
| --- | --- | --- | --- |
| `scan-full`（默认） | `go build -o cyber-scan ./cmd/cyber-scan` | 全引擎 + fingers/neutron + 472K 模板 bundle | — |
| `scan-capture` | `go build -tags 'emptyresources captureonly' -o cyber-scan-capture ./cmd/cyber-scan` | traffic 代理 + guardrail（保底审计）+ 判定（23a）+ Artifact sidecar（23c） | 扫描引擎、模板 bundle、gogo |

实测体积：full ≈185 MB，capture ≈158 MB（省约 15%）；`make manifest` 的
`capture_saved_bytes` 记录两个产物齐备时的差值。控制台节点页给出两条可复制指令。

## 4. 能力路由（T5，docs/04 §3.1）

- **声明**：`Spec.capabilities` 是操作员的部署事实（`["scan-capture"]` = capture-only）。
- **报告**：节点 hello 自带同一标记（harness `recipeCapabilities`，`-tags captureonly`
  时追加 `scan-capture`），经宿主 ListAgents → `aop.Agent.Capabilities` →
  `nodeProber.Health.Capabilities` → 态势页徽章。声明与报告不一致时徽章可见（呈现仲裁，
  不做强制）。
- **保守缺省**：无配方标记 = full。既有节点与旧版 scan 不受影响。
- **派发规则**：**隐式默认路由**（方向未指定节点，落到项目默认节点）不落 capture-only
  节点——派发时即拒绝（"默认节点 … 是 capture 配方（scan-capture，无扫描引擎）"），
  失败从"模型调到不存在的扫描工具"前移为调度拒绝。**显式指定**（方向上写明节点）尊重
  操作员：流量记录、围栏会话类方向按名字放到 capture 节点是刻意选择。

## 5. 远程节点的证据与围栏（补丁 23 后）

- **不再依赖共享文件系统**：扫描/代理证据经 `4zrcin.Artifact` sidecar 走 Envelope 流回
  中心 case 目录（限额：单文件 8 MiB、每批 16 文件/64 MiB；`.zrcin` 元数据与删除不回传）。
  远程节点只需要网络可达 AOP/模型端点，不需要与中心同路径的挂载。
- **围栏**：`scope_rules` 判定（含 CIDR）与保底审计（`builtin.` 前缀，无条件落审计行）
  在节点侧执行；capture 配方同样携带这两样——"审计责任不是能力"。

## 6. 绑定与默认（Nodes/DefaultNode）

- `default`（实例级，nodes.json）与项目绑定（allowlist + 项目默认，节点页管理）是两层；
  调度仲裁顺序：存在性 → 项目绑定 → 在线/就绪 → 能力（§4）。
- 一个控制台地址只允许一个 scan 实例（docs/02 §80）。

## 7. 隧道节点：接入、回连与销毁

`--public-url https://<控制台公网基址>` 打开接入面；不设置时 `/join`、`/node`、`/tunnel` 与节点
相关接口一律 404，现有部署不受影响。接入口令与清单见 docs/02「远程节点接入」。

### 7.1 接入面

| 路径 | 认证 | 作用 |
| --- | --- | --- |
| `GET /join/<令牌>/install.sh` | 接入令牌（一次性） | 引导脚本：识别平台、下载启动器并校验 SHA-256、执行安装 |
| `GET /join/<令牌>/dist/...` | 接入令牌 | 启动器产物与摘要（按 `uname` 选择平台） |
| `POST /node/join` | 接入令牌 | 换取节点凭据、隧道参数、Hub/控制台回环地址与产物清单 |
| `POST /node/ticket` | 节点凭据 | 换 60 秒一次性隧道票据与拨号 URL |
| `GET /node/next?wait=30` | 节点凭据 | 长轮询指令（目前只有 `uninstall`）与状态 |
| `POST /node/report` | 节点凭据 | 心跳（隧道与 scan 状态、主机信息、诊断）与卸载回执 |
| `GET /tunnel/<票据>` | 一次性票据 | WebSocket 升级后转给回环上的 rem 服务端 |

要点：令牌 `zr1_…` 只在创建响应出现一次，库里只有摘要；节点凭据 `zn1_<节点id>_<密钥>` 由实例
隧道密钥、节点 id 与安装 id 派生；隧道登录键取 31 字符（rem 会按 PKCS7 填充成 32 字节 AES 密钥，
64 字符会被拒）；两端固定 `?wrapper=raw`。拨号 URL 形如
`wss://<host>:443/tunnel/<票据>?wrapper=raw`——**端口必须显式**（rem 的控制台 URL 解析器会给无端口
地址填它自己的默认隧道端口 34996），**`wss` 就是 TLS**（websocket 拨号器在 HTTP upgrade 之下自己
握手并按系统根证书校验；私有 CA 走系统信任库或 `SSL_CERT_FILE`）。

### 7.2 启动器

安装布局：`<root>/{bin/zrcin-node,bin/cyber-scan,bin/zrcin,etc/node.json,var/}`，非 root 时根目录在
`~/.zrcin-node`；平台数据目录的同路径在节点主机上作为 scan 工作目录与 case 落地点，用归属标记
（`.zrcin-node-owner`）防止误删别人的目录。`install` 预检（镜像端口空闲、数据目录归属、磁盘空间）
→ 下载并校验产物 → 写配置 → 注册并启动 systemd 服务（无 systemd 时明示并前台运行，`status` 显示
`service: none`）。`run` 保持隧道（断线换新票据重连）、隧道就绪后拉起 scan 并按退避重启、30 秒心跳、
长轮询指令；收到卸载指令后由脱离服务的子进程执行卸载（停服务、删安装根与带标记的数据目录）。
心跳带主机信息、`$ZRCIN_BIN` 路径与隧道/scan 状态；**隧道翻转立刻补发一次心跳**（离线窗口是
三个心跳宽，翻转应在下一拍就被看到，而不是等下一次 tick）；连续失败按 30s 翻倍到 5m 退避并带
±20% 抖动，避免平台恢复瞬间整群节点同时重试。平台侧一个 15 秒的 watcher 比对节点档案，把
上线 / 离线 / 已销毁 翻转接进 IM 通知（`node_online` / `node_offline` / `node_destroyed`），
SSH 部署任务的成败经 `node_deploy` 事件走同一通道；重启只静默建立快照，不把冷启动当成全员上线。

### 7.3 生命周期与销毁

`pending`（接入记录未回连）→ `joining`（已换凭据）→ `online`（心跳新鲜，窗口 90 秒）→ `destroying`
（已下发卸载，拒绝新票据）→ `destroyed`（收到回执或操作员标记移除）。`dispatch_ready = online &&
状态不是 destroying/destroyed`。销毁会吊销凭据、解除所有项目绑定、关闭现有隧道、排队卸载指令；
离线节点在下次回连时执行。`POST /api/remote-nodes/{id}/forget` 把长期不回连的 `destroying` 标记为
`destroyed`。

接入策略在 rem 服务端钩子处执行：登录别名必须等于票据所属节点、一张票只登录一次、只允许节点侧
回环监听、目的地址只允许 Hub 与控制台两个回环端口。服务端只监听 `127.0.0.1`（显式 `-i`、
`--no-sub`），公网这一跳只有反代后的 `/tunnel/<票据>`。一个进程只起一个入口：rem 的
`Options.Prepare` 写的是进程级全局日志器，同一进程里并发起第二个服务端会与第一个竞争（这也符合
“一个 zrcin 进程就是一个平台实例”）。

### 7.4 SSH 部署

节点页「SSH 部署」把 §7.1 的引导脚本经 SSH 的 **stdin** 送远端 shell，不落远端磁盘、令牌不进
argv；步骤为连接 → 探测（`uname`、root/systemd、既有安装）→ 引导 → 等待回连 → 完成，日志随远端
输出实时显示。凭据只在任务内存（可擦除的字节切片），任务结束清零，视图里没有凭据字段；主机密钥
首次连接时记录（`<data>/ssh-host-keys.json`），不一致即中止且不发送凭据。失败即停并保留日志；
接入记录保持待回连，重试发新令牌（旧的待回连记录由界面撤销）。

## 8. 文件合同：种子、交接与证据

隧道节点与中心没有共享文件系统，回合所需文件随回合下发、结果经事件流回传：

- **种子（Z1 / harness 补丁 26）**：派发到隧道节点时，case 目录 `.zrcin` 的快照编码成 fork 扩展
  `4zrcin.CaseSeed` 附在 `RunTurnRequest` 上（JSON-in-Any，不改 proto）；隔离 `media/`、
  `aop-session.json`、`*.offset`、`cursor-repair-*.json`，限额为单文件 8 MiB、每回合 256 文件 /
  64 MiB（23c 的字节上限不变，文件数上限高于证据批次：一个真实 case 的 kb 副本就有几十个文件）。
  **技能优先**：显式选定的技能先占额度，其余按「运行上下文在前、kb 副本在后」排序，压力下截断的
  永远是 kb 副本。宿主在 `applyTurnWorkDir` 之后、回合开始之前落盘，拒绝越界路径与符号链接。
  **显式选定的技能**随种子一起走：技能目录镜像到 `.zrcin/skills/<name>/…`，提示词在隧道节点上
  指向这份副本（节点没有平台的 `<data>/skills`）；本机节点仍读原路径。可发现技能仍用
  `/skill:name`，由节点侧 harness 展开。
- **交接（Z2 / 补丁 27）**：`.zrcin` 下只有控制面专用文件仍被排除；`facts.jsonl` 与
  `worker-note.md` 走现有 artifact sidecar 直接写中心 case 目录。`facts.jsonl` 只在「中心内容是节点
  内容前缀」时追加，否则记 run warning、不覆盖；`worker-note.md` 原子覆盖。
- **证据（Z3）**：声明路径在中心磁盘不存在时，按 `<runID>/<case 相对路径>` 到 `.native` 工件库取
  字节，再进内容寻址证据库。这条在回合结算（finish）时生效：解析阶段就先问工件库，路径不再因为
  本机不存在而被降级成纯引用。
- **`$ZRCIN_BIN`（Z4）**：隧道节点的会话环境指向节点上报的本地 `zrcin` 路径。
- **派发（Z5）**：文件合同就绪后 `remote.dispatch` 为 `ready`；控制台的「启用到项目 / 设为默认」
  随之解除禁用。

`artifacts` 观测 kind 单独启用时也自给自足（补丁 28）：只在载体事件确实带产物时才发事件；旧的
`processes,artifacts` 等组合行为不变。

## 9. 节点页

部署相关界面全部在执行环境的「节点」页：

- **左列表**按管理边界分组：远程节点（隧道）、待回连、直连、本机。每行只有状态带语义色
  （隧道已连接 / 重连中 / 离线 / 等待卸载 / 已销毁），其余字段安静等宽；中断状态有 `aria-live` 播报。
- **右栏**是部署栏：命令部署（生成一行命令并等待回连）、SSH 部署（表单、步骤与日志）、直连
  （原生接入命令）；下面是两条构建配方与产物可分发状态；`--public-url` 未开启时栏顶给出启动参数并
  禁用两种自动部署。打开终端时右栏换成终端面板，关闭后回到部署栏。
- **行菜单**：启用到项目、设为默认、销毁（仅隧道节点，需输入节点名确认）；`destroying` 的行显示
  「等待卸载」并提供「标记已移除」。

## 10. 验收与已知限制

自动化覆盖：接入面（令牌/凭据/票据的 401/410、限速）、隧道策略（越界监听与目的地址、重放票据被
拒）、真实启动器 + 真实 rem 隧道的安装→回连→心跳→销毁→卸载清目录、SSH 部署（密码/私钥、非 root
的 sudo、失败即停、凭据擦除与主机密钥固定）、前端纯函数与节点页视图。

真实环境验收（2026-11-26，Debian 13 / systemd 257）：平台以 systemd 单元运行；节点先在同机的独立
网络+挂载命名空间里用真实一行命令安装回连，后又在 nginx 终止 TLS（含 IP SAN 的证书）的公网 443 上
以 `wss` 回连（`tunnel.up`、流量计数、`dispatch_ready: true`），销毁后安装目录、进程与镜像数据目录
全部清空。已记录的两个 rem 事实见 §7.1（无端口默认 34996、`wss` 才是 TLS）；主机所在云封锁 80/443 时
公开 HTTPS 需要备案域名或放行端口；私有 CA 通过系统信任库信任。

未覆盖面：真实 Linux 主机上的 systemd 节点单元（同机部署会与平台抢回环镜像端口，需要另一台带
systemd 的主机）、浏览器四档视口的人工实测、以及 server 形态（docs/16，另一条独立路线）。
