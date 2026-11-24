# 天吴 · Tianwu

单用户、单实例、多项目的授权安全测试平台：Go 服务端 + Web 控制台，复用 cyber-harness 的 Hub 与 scan 承载模型会话、工具调用与观测。

> **当前是 0.x 版本。** 接口、账本格式与运行行为在 1.0 之前仍会变，升级前先备份 `data/`；不要把 0.x 当作稳定版产品或直接用于无人值守的生产环境。

## 它是什么

- **图谱**：意图前提、产出、引用和固定上下文是显式关系；事实断言、线索与原始证据分开存放。
- **意图生命周期**：意图派发、子意图、等待、交接、重试、停放与完成；每次有账本记录的执行都留下输入、状态、用量与停止原因。
- **协作与涌现**：执行者按需互发消息、派子意图或平级意图，不必层层请示；关键结论与可复用打法汇回共享账本，打法经复核落库后跨意图复用，退役与降级由证据决定。
- **前提与复核**：必要前提须在当前断言版本上通过独立核验；成功意图进入复核阶段；复核默认开启，可在设置里改实例默认、在单个项目里覆盖（不覆盖就跟随实例），也可以新建项目时一次设定。
- **长会话状态**：笔记保存判断与回执，一个压力周期以有效交接收口后轮换会话；记忆载体是笔记与账本，不是会话本身。
- **操作员监督**：暂停/继续、操作员指令（通信页侧栏，持续注入每轮）、人工接管、待办收件箱、运行停止；暂停原因分类落盘，操作员的盖章与撤销另写 `operator-actions.jsonl`。
- **工具审批**：需要人工决定的工具调用在 guardrail 命名空间挂起，由操作员批准或拒绝；裁判可插拔（`none`/`jev`/`llm`/`hybrid`）。
- **执行观测与分账**：AOP 采集运行事件、工具调用、命令与 HTTP 流量；token 按指挥官/执行者/裁判分账，只记录不设阈值。
- **模型与容量**：单一模型连接，shared 段在线热更新；项目与实例并发上限、单轮墙钟上限、空闲检测。
- **项目迁移**：`.twproj` 带账本快照与证据摘要，导入后以暂停状态落地。

## 架构

```mermaid
flowchart LR
  OP[操作员<br/>浏览器控制台] -->|HTTP + console.token| TW[tw 服务端<br/>账本 · 项目 · 监督 · 复核]
  TW -->|AOP 会话 / 工具调用| HUB[cyber-harness Hub]
  HUB --> S1[scan 节点]
  HUB --> S2[scan 节点]
  S1 --> LLM[(模型连接<br/>OpenAI 兼容)]
  S2 --> LLM
  S1 -.->|事实 · 证据 · 交接 · 用量| TW
  S2 -.->|事实 · 证据 · 交接 · 用量| TW
  TW <--> LEDGER[(data/projects/&lt;id&gt;/ledger.sqlite)]
```

一次运行的样子：指挥官轮读项目文档与图谱，把一条意图派给执行者；执行者在 scan 节点上跑模型会话、调用工具，把事实与证据写回账本；复核 run 独立核验断言；操作员在控制台看图谱、暂停、发指令或批准工具调用。三处职责的边界与已知限制见 [核心架构](docs/01-architecture.md)。

## 目录结构

```
cmd/        tw（服务端）、tw-gate（本地门禁）、tw-repair（离线修复）、tw-manifest、tw-metrics
internal/   api（HTTP/路由）、decider（指挥官循环）、store（账本）、host（Hub/scan 监督）、
            project（项目清单）、verifier（复核）、report（报告）、board（态势投影）、aop（生成代码）
web/        src（控制台）、packages/ui 与 packages/theme（组件库，随主仓维护）
scripts/    gate、build-harness、collect-run、watchdog、check-docs 等入口
docs/       01-architecture.md、02-deploy.md
data/       运行时实例目录（账本、项目、证据、console.token），不随仓库分发
```

## 环境要求

- Go 1.26 或更高（`go.mod` 当前声明 `go 1.26.0`）。
- Node.js 24 或更高（前端测试用 `node --test` 直接执行 `.ts`，低于 24 跑不起来）、pnpm（锁文件 `pnpm-lock.yaml`）。
- Git。
- Windows 上还需要 Git for Windows 提供的 Bash 与常用 POSIX 工具：宿主的 shell 工具依赖它们。

本机验证过的组合：Go 1.27.0、Node.js 24.20.0、pnpm 12.14.0（`go version` / `node -v` / `pnpm -v`，2026-11-23）。

## 从源码构建与启动

控制面会嵌入仓库里的前端产物。修改了界面源码时，先运行 `pnpm install --frozen-lockfile` 和 `pnpm build`，再构建 `tw`。

```powershell
pwsh -File scripts/build-harness.ps1 -HarnessDir ../cyber-harness   # -> bin/cyber-harness.exe + bin/cyber-scan.exe
go build -o bin/tw.exe ./cmd/tw
./bin/tw.exe --harness ./bin/cyber-harness.exe --scan ./bin/cyber-scan.exe --data ./data
```

Linux / macOS 用 `scripts/build-harness.sh`，`bin/tw` 不带后缀。端口、数据目录、模型连接、最小验证与换版判据见[部署清单](docs/02-deploy.md)；完整启动参数见 `tw --help`。

## 如何使用

1. **配置模型连接。** 启动前设置 `CYBER_BASE_URL`、`CYBER_API_KEY` 和 `CYBER_MODEL`，或在「设置 → 模型连接」中填写。保存结果会说明是否已在线应用，或是否需要重启宿主。
2. **打开控制台。** 默认 `http://127.0.0.1:8080`；令牌在 `data/console.token`（首次启动生成，只给本机操作员）。
3. **建项目。** 新建项目时写三份文档：起点（范围与已有信息）、终点（验收标准）、内置约束（必须守住的东西）。这三份原文就是授权边界，代码不再另加规则；边界可以随项目推进在控制台里更新。
4. **让它跑。** 启动项目后，指挥官按文档与图谱派意图，执行者接意图执行；在「图谱」看待办、事实与证据，「项目状态」看谁在跑、为什么停。
5. **随时接管。** 暂停/继续、给指挥官发指令、在待办里处理审批；工具调用需要人工决定时会挂起等操作员。
6. **复盘与带走。** 每个 run 有输入、状态、用量与停止原因；报告预检从结论直达原始证据；整项目可导出为 `.twproj`。

第一次上手建议用离线假模型或隔离环境跑一遍，只看流程是否通，不要直接指向真实资产。

## 常用命令

| 用途 | 命令 |
| --- | --- |
| 本地门禁（构建、静态检查、测试、前端检查、文档链接、AOP 漂移、构建清单） | `pwsh -File scripts/gate.ps1`，Linux 为 `./scripts/gate.sh` |
| Go 测试（串行） | `go test ./... -p 1 -count=1` |
| 前端检查 | `pnpm --dir web typecheck` 与 `pnpm --dir web test` |
| 收一个 run 供复盘 | `pwsh -File scripts/collect-run.ps1 -Project <项目ID> -Out <out.zip>` |
| 长跑外部看护（默认只观察） | `pwsh -File scripts/watchdog.ps1 -Project <项目ID>` |
| 版本 | `./bin/tw.exe --version` |

## 构建清单

gate 第 10 步写 `.build/build-manifest.json`（`cmd/tw-gate/main.go:20-25`）：源码身份（仓库根、commit、branch、dirty、工作树补丁 SHA-256）、harness 身份与提交、AOP 生成文件的聚合 fingerprint 与逐文件 SHA-256、协议版本、平台与工具链、模型连接与真实上游 model id、以及各二进制的 sha256 与 `--version` 输出。清单从不写 API key。

## 离线修复交接游标

历史有效置信度、交接游标的修复都要求项目暂停且 `tw` 已停止，先预览、核对后再提交：

```powershell
./bin/tw-repair.exe --project <项目绝对目录> --confidence            # 预览
./bin/tw-repair.exe --project <项目绝对目录> --confidence --commit   # 先备份账本再写回
./bin/tw-repair.exe --project <项目绝对目录> --intent <精确意图ID>    # 预览该意图的交接游标
```

`--commit` 只写回能保留断言哈希的派生标签或与现存声明一致的游标，并保留修复前的游标、文件摘要与审计记录。

## 免责声明

- **只用于已获授权的目标。** 天吴是安全测试工具，未经目标所有者书面授权对其系统做测试可能构成违法；授权范围由使用者自行取得并负责，本项目不提供任何目标清单或授权。
- **按「现状」提供，不附带担保。** 依 AGPL-3.0 分发，不承诺适销性、特定用途适用性或不侵权；因使用本软件造成的任何直接或间接损失，作者与贡献者不承担责任。
- **测试会改变目标状态。** 扫描、上传、登录尝试都可能留下痕迹或影响业务；请在隔离或可回滚的环境先行验证，风险与后果由使用者承担。
- **数据与合规由使用者负责。** 账本、证据与抓取的流量可能含个人信息或敏感数据，保存、传输与销毁须符合所在地法律与授权约定。
- 本项目与任何被测试的第三方无关；文中出现的产品名仅用于说明。

## 贡献与许可

提交前跑本地门禁；约定见 [CONTRIBUTING.md](CONTRIBUTING.md) 与 [agent.md](agent.md)。测试与演示只使用临时目录、本地假模型或明确授权的隔离环境，不把协议测试表述为真实效果。

天吴以 [AGPL-3.0](LICENSE) 发布；第三方内容的来源和许可见 [NOTICE](NOTICE)。
