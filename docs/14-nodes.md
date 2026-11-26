# 节点形态与部署（docs/14）

一次部署先回答两个问题：**节点是什么形态**（跑什么二进制、连谁），以及**它声明什么能力**（调度
允许什么落到它上面）。本文覆盖 `internal/node` 的 Spec 定义（`node.go` 引用的 "docs/14-nodes.md，
D64"）与 `make harness` / `make scan-capture` 产物的部署。

## 1. 节点形态三矩阵

| 形态 | 机器 | 跑的进程 | 节点登记 | 适用 |
| --- | --- | --- | --- | --- |
| 单机一体包 | 一台 | `bin/zrcin`（内嵌 Hub 与本机 scan 宿主） | 自动，id=`local` | 个人/单机验收 |
| 中心 + 多台 scan agent | 一台中心 + N 台执行机 | 中心：`zrcin`（`--harness`/`--scan` 子进程）；执行机：`cyber-scan` | 中心 `<data>/nodes.json` 加条目（§2） | 分布式扫描/记录 |
| 远程成对 Hub+scan | 执行机自管 Hub | 执行机：`cyber-harness` + `cyber-scan` 成对 | `nodes.json` 指向该机 AOP 端点 | 执行机有自己的运维边界 |

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
   `cyber-scan '<token>' …`（含 `--transport web` 与 server-url）。
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
