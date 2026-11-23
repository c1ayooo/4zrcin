# 天吴 · Tianwu

单用户、单实例、多项目的授权安全测试平台：Go 服务端 + Web 控制台，复用 cyber-harness 的 Hub 与 scan 承载模型会话、工具调用与观测。

## 它是什么

- **事实与意图图**：任务前提、产出、引用和固定上下文是显式关系；事实断言、线索与原始证据分开存放。
- **任务生命周期**：意图派发、子任务、等待、交接、重试、停放与完成；每次有账本记录的执行都留下输入、状态、用量与停止原因。
- **前提与复核**：必要前提须在当前断言版本上通过独立核验；成功意图进入复核阶段；复核有实例级运行时开关，`--no-verify` 只是它的启动初值。
- **长会话状态**：笔记保存判断与回执，一个压力周期以有效交接收口后轮换会话；记忆载体是笔记与账本，不是会话本身。
- **操作员监督**：暂停/继续、持久指令、人工接管、待办收件箱、运行停止；暂停原因分类落盘，操作员的盖章与撤销另写 `operator-actions.jsonl`。
- **工具审批**：需要人工决定的工具调用在 guardrail 命名空间挂起，由操作员批准或拒绝；裁判可插拔（`none`/`jev`/`llm`/`hybrid`）。
- **执行观测与分账**：AOP 采集运行事件、工具调用、命令与 HTTP 流量；token 按指挥官/执行者/裁判分账，只记录不设阈值。
- **模型与容量**：单一模型连接，shared 段在线热更新；项目与实例并发上限、单轮墙钟上限、空闲检测。
- **项目迁移**：`.twproj` 带账本快照与证据摘要，导入后以暂停状态落地。

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
- Node.js 24 或更高（前端测试用 `node --test` 直接执行 `.ts`，低于 24 跑不起来）、pnpm（锁文件 `pnpm-lock.yaml` 的 `lockfileVersion: '9.0'`）。
- Git。
- Windows 上还需要 Git for Windows 提供的 Bash 与常用 POSIX 工具：宿主的 shell 工具依赖它们。

本机验证过的组合：Go 1.27.0、Node.js 24.20.0、pnpm 12.14.0（`go version` / `node -v` / `pnpm -v`，2026-11-23）。

## 从源码构建与启动

```powershell
pwsh -File scripts/build-harness.ps1 -HarnessDir ../cyber-harness   # -> bin/cyber-harness.exe + bin/cyber-scan.exe
go build -o bin/tw.exe ./cmd/tw
./bin/tw.exe --harness ./bin/cyber-harness.exe --scan ./bin/cyber-scan.exe --data ./data
```

Linux / macOS 用 `scripts/build-harness.sh`，`bin/tw` 不带后缀。端口、数据目录、模型连接、最小验证与换版判据见[部署清单](docs/02-deploy.md)；完整启动参数见 `tw --help`。

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

## 文档索引

- [核心架构](docs/01-architecture.md)：角色与执行契约、账本、生命周期、复核与报告、已知限制。这是唯一的核心架构文档。
- [部署清单](docs/02-deploy.md)：三个 exe 的职责、换版验收、停启顺序、干净环境冷启动、从构建产物起一个实例。
- [变更记录](CHANGELOG.md)
- [实现约定](agent.md)：代码、提示词、测试与文档变更的约定。
- [贡献指南](CONTRIBUTING.md)
- [安全策略](SECURITY.md)
- [控制台组件库](web/packages/ui/README.md)、[主题包](web/packages/theme/README.md)
- [验收脚本](scripts/acceptance/README.md)
- 仓库内资产文档：[AOP 生成代码](internal/aop/pb/README.md)、[知识库](internal/kb/assets/README.md)、[知识库条目](internal/kb/assets/entries/README.md)、[协作 Skill](internal/skills/assets/collaborate/SKILL.md)、[指挥官 Skill](internal/skills/assets/commander/SKILL.md)、[报告 Skill](internal/skills/assets/tianwu-report/SKILL.md)

## 贡献与许可

提交前跑本地门禁；约定见 [CONTRIBUTING.md](CONTRIBUTING.md) 与 [agent.md](agent.md)。测试与演示只使用临时目录、本地假模型或明确授权的隔离环境，不把协议测试表述为真实效果。

天吴以 [AGPL-3.0](LICENSE) 发布；第三方内容的来源和许可见 [NOTICE](NOTICE)。
