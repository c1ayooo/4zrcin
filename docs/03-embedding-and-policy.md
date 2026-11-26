# 内嵌 Hub（形态 C）与项目级策略实施计划

状态：**已实施并合入**（2026-11-26）。M1–M5b 全部落地（harness 补丁 22/23 → `tianwu/fixes`，zrcin → `main`）；后继的平台二进制形态与资源模板（单二进制、构建配方、节点文档）见 [04-templates-and-resources.md](04-templates-and-resources.md)（T1–T5 亦已合入）。前置阅读：[01-architecture.md](01-architecture.md)（§1、§10.8）、[02-deploy.md](02-deploy.md)（三 exe 现状）、harness 仓库 `docs/agent.md`。

## 0. 一句话目标

把 cyber-harness Hub 以**库级内嵌**并入 zrcin 进程（单 exe 交付），scan 维持为**可分布式部署的独立 agent**；在此基座上落地项目级策略：范围围栏（CIDR+通配）、不设围栏模式、高危命令审计保底、项目内集中配置面板。

## 1. 现状事实（实施依据，均已核实）

1. Hub 构造已是公开库：harness `pkg/web/host`（`host.go:21` Config、`:34` Serve）即全部 Hub 逻辑；`cmd/cyber-harness/main.go:92-107` 只是组装 Config 后调 Serve。**无需抽取重构，只差一个入参**。
2. `Serve(ctx, Config)` 阻塞至 ctx 取消；自建 listener；`Config.StartNode(nodeCtx, addr)` 监听后回调；`Static == nil` 不挂 SPA（zrcin 不需要 harness 的 Web UI）。
3. 依赖足迹：`go list -deps ./pkg/web/host` 共 619 包，fork 依赖**仅 cyber 自身**——27 个工具 fork 只在 scan 侧，内嵌不拉工具链进 zrcin 构建。
4. 两侧 `go 1.26.0` 一致。
5. scan 分布式是内建的：`agent --transport web` 连回 Hub 注册（`docs/agent.md` 四形态中的节点形态）；`internal/node/node.go` Spec 即远程节点；本机 "local" 是默认注册。
6. 分布式缺口（实施前事实）：`node.go:63-65` 要求远程 case 目录与 zrcin 同路径可见（证据靠共享文件系统）；`docs/14-nodes.md` 被引用但缺失。——**两者均已解决**：补丁 23c 的 `4zrcin.Artifact` sidecar 让证据走 Envelope 流回中心 case 目录（不再依赖共享文件系统）；`docs/14-nodes.md` 已补写（节点形态、接入步骤、配方矩阵、能力路由）。
7. host.go 监督组现状：hub+scan 同生共死，指数退避重启（1s→30s），3 次就绪失败降级；部署文档是"三个 exe"。

## 2. 里程碑总览

| 里程碑 | 编号 | 内容 | 仓库 | 依赖 |
| --- | --- | --- | --- | --- |
| M1 | 内嵌基座 | 补丁22 + zrcin 引入 + host.go inproc | 双仓 | 无 |
| M2 | **P1** | 围栏 schema（CIDR）+ FenceMode + 创建时配置 | zrcin | 无 |
| M3 | **P2** | policy 聚合端点 + 项目内策略面板 | zrcin | P1 |
| M4 | **P3** | 流量 verdict 筛选/判定卡 + 审计流水呈现 | zrcin | P1 |
| M5a | **P4** | 捕获配置写路径 | zrcin | 无 |
| M5b | **P5** | 补丁23（CIDR 匹配 + builtin 审计 + Artifact sidecar）+ 节点文档 | harness | P1 定稿 |

M2–M4 与 M1 无依赖可并行；补丁22 是 M1 前置，补丁23 依赖 P1 的 schema 定稿（CIDR 格式两侧必须一字不差）。P1–P5 的逐步实施明细见 §11。

**落地状态（2026-11-26）**：M1（补丁22 + inproc 单二进制）、M2（P1 CIDR schema）、M3（P2 policy 聚合 + 面板）、M4（P3 verdict 卡 + 审计呈现）、M5a（P4 捕获配置）、M5b（补丁 23a/23b/23c + docs/14）全部合入；实施时决策补记在各设计文档（本文件 §11 逐条保留原设计，落地差异见 [04-templates-and-resources.md](04-templates-and-resources.md) 的"落地决策"段）。

M2–M4 与 M1 无依赖可并行；补丁22 是 M1 前置，补丁23 依赖 P1 的 schema 定稿。P1–P5 的逐步实施明细见 §11。

## 3. M1 内嵌基座

### 3.1 harness 补丁22（唯一代码改动）：`webhost.Config` 增加可选 `Listener net.Listener`

`Serve` 目前自己 `net.Listen(config.Addr)`。in-process 场景 zrcin 需要预先监听 `127.0.0.1:0` 才能拿到实际地址喂给 `waitReady`/scan agent。改动：`Config.Listener` 存在时忽略 `Addr`、直接使用传入 listener；`cmd/cyber-harness/main.go` 不受影响。按 FORK.md 补丁流程登记（编号、根因、测试：传入 listener 的 Serve 起停测试）。

### 3.2 zrcin 侧引入

1. `go.mod`：`require github.com/c1ayooo/cyber v0.0.0-<伪版本>`；开发期 `replace ... => /home/c1ay/cyber-harness`，发布用 commit 伪版本（脚本从 `scripts/build-harness.sh` 同源的 HARNESS_DIR 读 commit 生成）。`go mod tidy` 会带入 hub 的第三方依赖（connectrpc/sqlite 等）进 go.sum——接受；27 个工具 fork 不会出现（§1.3）。
2. `internal/host/host.go`：`Config.HubMode`（`exec` 默认 / `inproc`）。`execLaunch` 分支：
   - inproc：不起 hub 子进程。`listener, _ := net.Listen("tcp", "127.0.0.1:0")` → goroutine 里 `webhost.Serve(ctx, webhost.Config{Listener: listener, Static: nil, Management: webext.Config{Product: "4zrcin-hub", Database: <data>/hub.db, AccessKey: token, ...}})`；goroutine 外层 recover + 退出时走现有退避重启路径（复用 `watch` 的重试语义，只把"进程退出"换成"goroutine 返回"）。
   - scan agent 仍走 exec 子进程（保持易碎部分隔离），连接地址取 `listener.Addr()`。**不使用 `StartNode`**——那会把 scan 放进 hub 的生命周期，模糊监督边界。
3. `waitReady`（轮询 Connect ListAgents）、`Update` 复活、`SweepOrphans` 全部不变；AOP 客户端照旧连 loopback。
4. 逃生舱：`--harness <外部二进制>` 显式传入时走 `exec` 模式（现状路径原样保留），inproc 失败连续 3 次也自动降级提示可切换。
5. 构建与文档：`scripts/build-harness.sh` 保留 cyber-scan 构建，cyber-harness exe 转为逃生舱可选；`zrcin-gate` 的构建检查同步调整；`docs/02-deploy.md` 增加"单 exe 形态"章节（三 exe 形态保留为兼容章节）。

### 3.3 构建标签与模板资源（已核实，构成约束）

- 模板机制两层、全在 scan 侧：harness `tools/resources/template.go`（`templates_gen.go` go:generate 生成，472K 压缩 bundle 嵌入扫描资源）+ gogo fork 的 `pkg/templates.go`/`templates_empty.go`（`emptytemplates` 标签选空实现）。`noembed` 同理让 fork 库不内嵌数据。
- Hub 依赖树**不含** `tools/resources` 与 gogo——模板与 `emptytemplates/noembed` 不进 zrcin 二进制。
- zrcin 构建引入 cyber 模块后必须沿用共享库标签：`forceposix osusergo sqlite`（非 Windows 加 `netgo`）；`emptytemplates noembed` 对 hub 无效、可带可不带；`full` 仅 harness 独立构建需要。构建脚本与 `zrcin-gate` 的构建检查同步落这套标签约定，`GONOSUMDB=github.com/c1ayooo/*` 照旧。

### 3.4 M1 验收

- 单 exe（zrcin）冷启动：本地 scan 节点自动注册、AOP 会话跑通一轮、流量/guardrail 事件回传正常；
- zrcin-gate 全绿（协议零变化）；`go test ./internal/host ./internal/aopexec` 通过；
- 杀掉 scan 子进程：自动重启且 hub 不受影响；zrcin 进程内 hub goroutine 退出：退避重启生效；
- `--harness` 逃生舱模式仍可起完整三 exe 栈。

## 4. M2 围栏与创建（zrcin 侧，可与 M1 并行）

1. **CIDR 校验**（`internal/project/scope_rules.go`）：`host` 字段新增第 4 种形态——含 `/` 的值必须 `net.ParseCIDR` 通过（IPv4/IPv6）；`Valid()` 加分支，其余校验（动作封闭集、method 枚举、64 行上限、重复行）沿用。
2. **FenceMode 推导**（不落盘新字段）：`internal/project` 新增 `FenceMode(m Manifest) string`——`ScopeRuleSet == nil → unfenced`；`Shadow == true → shadow`；`Enabled && !Shadow → enforced`；`Enabled == false → draft`。
3. **创建 API**（`cmd/zrcin/hub.go` serveProjects POST）：请求体新增可选 `scope_rules`（结构同 PUT 三态）与 `fence`（`unfenced/shadow/enforced` 速记：unfenced=不写、shadow=Enabled+Shadow、enforced=Enabled）。非法规则创建即拒。
4. **policy 聚合端点**：`GET /api/projects/{id}/policy` 一次返回 `{fence_mode, scope_rules, audit:{guaranteed:true, pending_count}, capture:{mode, decrypt_https, ca_fingerprint}, stats:{today_by_action}}`。
5. **创建对话框**（`web/src/components/NewProjectDialog.tsx`）：新增"访问围栏"步骤——三选一（不设围栏=默认/试运行 shadow/强制执行 enforced）+ 选后展开初始规则速填（CIDR 行、域名通配行、兜底动作），复用 ScopeRulesCard 校验，可跳过稍后配置。

## 5. M3 项目内策略面板

1. 注册：`web/src/views/GraphView.tsx` 的 `PANEL_LOADERS` 加 `policy` 项 + SideNav 项（现有懒加载惯例）。
2. 新组件 `web/src/views/PolicyPanel.tsx`，三区：
   - **范围与拦截**：围栏模式切换（enforced 切换复用现有二次确认）+ 规则编辑器——把 `ScopeRulesCard` 重构为共享组件嵌入，Settings 保留入口跳转，不维护两份编辑器；
   - **审计**："保底审计 · 始终开启（不可关闭）"只读标识、guardrail 策略摘要（复用 `GuardrailPolicyCard`）、待审批数与入口；
   - **捕获**：`capture_mode/decrypt_https/CA 指纹` 只读 + "在主机设置中修改"跳转（捕获是主机级资源——一个 Hub 实例服务全部项目，不做项目级捕获配置，UI 明示边界）。
3. 数据源即 `GET /policy`，单一聚合。

## 6. M4 流量与审计呈现

1. `Traffic.tsx` 列表加 verdict 筛选 chips（`GET /api/traffic/flows?verdict=`，`http_flows.go` 加参数）；unfenced 项目页头常驻"未设围栏 · 仅记录"横幅（取 policy 端点）。
2. 详情视图：Exchange 面板上方插**判定卡**——动作徽章、命中规则原文、时间线（mitm 判定→合成 403/挂审→verdict sidecar），按 intent_id 关联同 run 的 guardrail 事件。
3. **审计保底呈现**：新组件列 guardrail_decision 流水（按项目聚合 run 事件），`reason` 前缀 `builtin.` 的行带"保底"徽章。语义定死：保底在执行端实现（harness 内置高危命令集无条件发 decision，任何项目配置不能关闭；项目只能通过 guardrail 策略升级为 review/block），zrcin 侧只是呈现——不放 manifest、不进 scope_rules。

## 7. M5 执行端与分布式（harness 补丁23 + 收尾）

1. **补丁23（harness）**，三个独立小改动，一个补丁号登记：
   - scope_rules 判定加 CIDR 分支：通配/精确未命中且值含 `/` 时对目标解析 IP 匹配（CONNECT 对 host 做 DNS 解析）；未知形态兜底按精确主机名（双端版本错配不误拦）；
   - 内置高危命令集（`rm -rf`、`curl|sh` 等外传/破坏类）：判定无条件、reason 带 `builtin.` 前缀，不受 on_error/provider 影响；
   - `4zrcin.Artifact` anypb sidecar：运行产出的证据内容走 Envelope 流回 zrcin 落盘中心 case 目录——**解除 case 目录同路径耦合，这是远程 scan 节点真正可用的前提**。旧 zrcin 忽略、旧 harness 不发，向后兼容。
2. **capture 写路径**（zrcin）：Settings 的 `TrafficCaptureCard` 从只读变可写（PUT 主机 `extensions.traffic`，CA 指纹确认）；内嵌后写路径落在 hub 的 `FileConfigStore`（`ConfigAPI.Sections` 已支持），无需新机制。
3. **节点文档**：补写 `docs/14-nodes.md`（`node.go` 已引用）：节点形态三矩阵（单机一体包 / 中心 zrcin+内嵌 Hub + 多台 scan agent / 远程成对 Hub+scan）、scan agent 的 `--transport web` 接入步骤（引用 harness `docs/agent.md`）、Artifact 解耦后的远程节点要求、`Nodes/DefaultNode` 绑定说明。
4. **展望（不在本计划内）**：节点池自动调度（按标签/能力/负载替代手工 `SetNodeBinding`）；inbox 常驻 Router 与 zrcin 平台的编排结合。

## 8. 风险与对策

| 风险 | 对策 |
| --- | --- |
| zrcin 进程内 hub 崩溃=zrcin 崩溃 | hub 是稳定服务端代码；recover+退避重启；`--harness` 逃生舱；scan 仍隔离在外 |
| go.sum 混入 harness 第三方依赖 | 接受并锁定；fork 依赖已证实不沾染（§1.3）；zrcin-gate 增加依赖面断言防回退 |
| 双端版本错配（新 zrcin + 旧 harness） | CIDR 兜底精确匹配；Artifact 旧端不发；fence 推导不依赖执行端 |
| hub 升级与 vendored 协议漂移 | zrcin-gate 照常守卫——内嵌后单仓单构建，验证反而更直接（§10.8 流程不变） |
| scan agent 分布式证据路径耦合 | M5 Artifact sidecar 是硬前置，先补丁后扩节点 |

## 9. 回滚

- M1：`HubMode` 回 `exec`（一行配置），或整体 revert + `go mod tidy`；
- M2–M4：均为增量字段/端点/组件，revert 即消失，旧 manifest 不受影响；
- 补丁22/23：按 FORK.md 既有回滚条目格式登记。

## 10. 实施顺序建议

1. M1（1）：补丁22 → zrcin 引入 → inproc → 单 exe 验收 → 02-deploy 更新；
2. M2（2）+ M3/M4 组件（3、4）：与 M1 并行，纯 zrcin 侧；
3. M5（5）：补丁23 三件套 → artifact 联调 → 14-nodes 文档 → capture 写路径收尾。

每个里程碑合入前：zrcin-gate 全绿 + 受影响包测试 + FORK.md/文档登记（沿用补丁 20/21 的格式）。

## 11. P1–P5 实施明细（逐步骤，含文件与验证）

### P1 围栏 schema + 创建时配置（zrcin 侧，先行）

| # | 文件 | 内容 | 验证 |
| --- | --- | --- | --- |
| 1 | `internal/project/scope_rules.go` | `Valid()` 加 CIDR 分支：`host` 含 `/` 时 `net.ParseCIDR` 必须通过（IPv4/IPv6）；其余校验（动作封闭集、method 枚举、64 行上限、重复行）不动 | `scope_rules_test.go` 表驱动用例：合法 CIDR、非法 `10.0.0.0/99`、`*.foo/24` 拒绝 |
| 2 | `internal/project/project.go` | 新增 `FenceMode(m Manifest) string`：`ScopeRuleSet == nil → unfenced`；`Shadow → shadow`；`Enabled && !Shadow → enforced`；else → `draft`。纯推导，不落盘新字段 | 单测覆盖四种组合 |
| 3 | `cmd/zrcin/hub.go` `serveProjects` POST | 请求体加可选 `scope_rules`（结构同 PUT 三态）与 `fence`（`unfenced/shadow/enforced` 速记：unfenced=不写、shadow=Enabled+Shadow、enforced=Enabled）；创建前走 `Valid()`，非法即拒 | api 层测试：三态 fence → manifest 断言 |
| 4 | `web/src/lib/api.ts`、`web/src/components/NewProjectDialog.tsx` | `createProject` 加参数；对话框新增"访问围栏"步骤——三选一（不设围栏=默认/试运行/强制执行），选后展开初始规则速填（CIDR 行、域名通配行、兜底动作），复用 ScopeRulesCard 校验，可跳过稍后配置 | 前端 `npm test` + 手动创建走查 |

### P2 policy 聚合端点 + 项目内策略面板

| # | 文件 | 内容 |
| --- | --- | --- |
| 1 | `internal/api/policy.go`（新） | `GET /api/projects/{id}/policy`：返回 `{fence_mode, scope_rules, audit:{guaranteed:true, pending_count}, capture:{mode, decrypt_https, ca_fingerprint}, stats:{today_by_action}}`——数据全部来自现有结构（manifest + settings 投影 + store 统计），无新存储 |
| 2 | `web/src/components/ScopeRulesCard.tsx` | 重构为受控共享组件（规则编辑逻辑抽出），Settings 与新面板复用，不维护两份编辑器 |
| 3 | `web/src/views/PolicyPanel.tsx`（新） | 三区：**范围与拦截**（围栏模式切换 + 编辑器，enforced 复用现有二次确认）/ **审计**（"保底审计 · 始终开启"只读标识 + guardrail 策略摘要复用 `GuardrailPolicyCard` + 待审批数）/ **捕获**（只读 + "在主机设置中修改"跳转——捕获是主机级资源，UI 明示边界） |
| 4 | `web/src/views/GraphView.tsx` | `PANEL_LOADERS` 注册 `policy` 项 + SideNav 项（现有懒加载惯例） |

### P3 流量与审计呈现

| # | 文件 | 内容 |
| --- | --- | --- |
| 1 | `internal/api/http_flows.go` | `GET /api/traffic/flows` 加 `verdict=` 过滤参数 |
| 2 | `web/src/views/Traffic.tsx` | 列表 verdict 筛选 chips；详情在 Exchange 面板上方插**判定卡**（动作徽章、命中规则原文、时间线，按 intent_id 关联同 run 的 guardrail 事件）；unfenced 项目页头常驻"未设围栏 · 仅记录"横幅（取 policy 端点） |
| 3 | `internal/api` 审计聚合（新） | `GET /api/projects/{id}/audit`：按 intent 聚合 guardrail_decision 事件，`?source=builtin&tool=&action=` 过滤，分页 |
| 4 | 审计视图组件（新） | `reason` 前缀 `builtin.` 的行带"保底"徽章，嵌入 PolicyPanel 审计区 |

语义要点：保底审计在执行端实现（补丁 23），P3 只是呈现——zrcin 侧不改任何判定逻辑，不放 manifest、不进 scope_rules。

### P4 捕获配置写路径

| # | 文件 | 内容 |
| --- | --- | --- |
| 1 | `internal/settings` | `extensions.traffic` 段从只读投影变可写（PUT，封闭键 `{capture_mode, decrypt_https, ca_cert}` 校验沿用） |
| 2 | `cmd/zrcin/hub.go` | 设置保存路由接线（现有热更新机制下发） |
| 3 | `web/src/components/TrafficCaptureCard.tsx` | 只读变可写 + CA 指纹确认对话框 |

内嵌完成后写路径直接落在 hub 的 `FileConfigStore`（`ConfigAPI.Sections` 已支持）；若 P4 先于内嵌实施，走现有子进程配置下发路径——两者接口相同，无需返工。

### P5 harness 补丁 22/23（FORK.md 登记，走既有补丁流程）

| 补丁 | 内容 | 文件 | 测试 |
| --- | --- | --- | --- |
| **22**（内嵌唯一前置） | `webhost.Config` 加可选 `Listener net.Listener`（传入时忽略 `Addr`） | `pkg/web/host/host.go`，约 5 行 | 传入 listener 的 Serve 起停测试；`cmd/cyber-harness` 回归不受影响 |
| **23a** | scope_rules 判定 CIDR 分支：通配/精确未命中且值含 `/` → 对目标解析 IP 匹配（CONNECT 对 host 做 DNS 解析）；未知形态兜底按精确主机名（双端版本错配不误拦） | `core/scoperules` / proxy 判定处 | CIDR 命中/不命中/错配用例 |
| **23b** | 内置高危命令集（`rm -rf`、`curl\|sh` 等外传/破坏类）无条件审计，reason 带 `builtin.` 前缀，不受 on_error/provider 影响 | `exts/guardrail` | "项目策略无法关闭保底"用例 |
| **23c** | `4zrcin.Artifact` anypb sidecar：证据内容走 Envelope 流回 zrcin 落盘中心 case 目录，解除 case 目录同路径耦合（远程 scan 节点可用的硬前置） | `core` + `exts/observe` | 旧 zrcin 忽略兼容 + 新 zrcin 落盘断言 |

### 实施顺序与并行关系

```
补丁22 ──→ 内嵌基座(M1) ─────────────────┐
P1 ──→ P2 ──→ P3                         ├──→ 补丁23 ──→ docs/14-nodes.md ──→ 远程节点验收
         └──── P4（随时可插） ────────────┘
```

- P1/P2/P3/P4 纯 zrcin 侧、互相独立可并行，且不依赖内嵌完成；
- 补丁 23 依赖 P1 的 schema 定稿（CIDR 格式两侧一字不差），在 P1 合入后开始；
- 每个合入点：zrcin-gate 全绿 + 受影响包测试 + FORK.md/文档登记。
