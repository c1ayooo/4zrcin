# 贡献指南

欢迎提交问题、文档修正与代码贡献。参与前请阅读 [安全策略](SECURITY.md)、[核心架构](docs/01-architecture.md)与[设计取向](agent.md)。

## 授权与内容来源

只提交虚构目标、假凭据和脱敏材料。不要提交客户信息、真实目标、证据、API key、登录令牌或本地配置。第三方内容需说明来源并保留许可证。贡献本项目即表示你有权提交这些内容，并同意按 [AGPL-3.0](LICENSE) 发布；第三方资产遵循其原有许可，见 [NOTICE](NOTICE)。

## 开发环境与检查

Windows 与 Linux 是同等支持的开发与验收平台：两平台都需要 Go 1.26+、Node.js 24+（`scripts/check-docs.mjs` 用）与 Git，并且都能完整跑通同一套本地 gate。Windows 执行宿主还需要 Git for Windows 的 Bash 与 POSIX 工具。控制台前端源码在 [4zweb](https://github.com/c1ayooo/4zweb) 仓（同父目录检出，或设 `ZRCIN_WEB_DIR` 指向检出根），前端依赖在 4zweb 里用 pnpm 安装；主仓没有 package.json，不在仓库根目录装前端依赖。

提交前在仓库根目录运行本地 gate；推送后 GitHub Actions（`.github/workflows/ci.yml`）跑同一份 gate（`-skip-harness`）。检查只有一份实现（`cmd/zrcin-gate`），两个脚本只负责定位仓库根并透传参数和退出码，两平台检查项完全相同：

Windows / PowerShell：

```powershell
pwsh -File scripts/gate.ps1
```

Linux / macOS：

```bash
./scripts/gate.sh      # 或 bash scripts/gate.sh
```

`make gate` 等价。gate 串行执行并逐步打印耗时：`go build ./...`、`go vet ./...`、`go test ./... -count=1`、（找到 4zweb 检出时）前端 `pnpm typecheck` / `test` / `build` 与嵌入产物新鲜度、`node scripts/check-docs.mjs`，再从 harness 源码构建 `cyber-harness` + `cyber-scan`、构建 `bin/zrcin` 与 `bin/zrcin-repair`，最后写构建清单 `.build/build-manifest.json`（字段说明见 [README](README.md#构建清单)）。缺依赖时 gate 明确失败并给出两平台安装指引，不会静默跳过；没有 4zweb 检出时前端各步记录 skip，以已提交的 `internal/web/bundle` 为准。需要部分检查时用显式开关（`-skip-web`、`-skip-harness`、`-manifest-only`、`-no-model-probe`），不要手工删减检查。Linux 上用发行版包或官方 tarball 安装 Node.js 24+，再按上面的双平台命令验证；在 WSL 中直接跑 Windows 上的 checkout 时先执行 `git config --global core.autocrlf true`（与 Git for Windows 默认一致），否则 git 会把整棵树当作已修改。

gate 默认发一次 `max_tokens=1` 的模型请求以记录真实 model id；模型配置来自 `--harness-config` / `ZRCIN_HARNESS_CONFIG` / `~/.cyber/cyber.yaml`，无配置或探测失败只记录原因，不使 gate 失败，API key 不写入清单。

前端测试包含纯函数和真实 React 组件的状态回归，不需要运行控制面或调用模型（测试本身跑在 4zweb 仓里）。`internal/web/bundle` 是入库的生成产物（由 4zweb 构建出的 dist 经 `go generate ./internal/web` 生成），前端源码修改后必须在 4zweb 里重新构建 dist 并重新生成、一并提交；gate 在 `pnpm build + go generate` 前后比较该目录，只有本次重建新产生的差异才算失败（工作树原本就脏不算失败）。资源不足时可用 `go test -p 1 ./...`。并发敏感改动（复核结算、调度/唤醒、store 事务、decider）还应在有 C 工具链的环境跑 race 检测（Go race detector，需要 `CGO_ENABLED=1` 与 gcc）：Windows 用 `powershell -ExecutionPolicy Bypass -File scripts/race.ps1`，git-bash/WSL 用 `make race`，两者跑同一组包。Windows 上没有 gcc 时，可先用 winget 安装 MinGW-w64（`winget install BrechtSanders.WinLibs.POSIX.UCRT`，装完重开终端；`gcc --version` 应输出版本）。装好后也可以直接跑 `CGO_ENABLED=1 go test -race -count=1 -timeout 30m ./internal/api ./internal/lifecycle ./internal/verifier ./internal/store ./internal/decider ./internal/board ./internal/collab`。也可以改用 WSL/Docker。

harness 独立维护在 `c1ayooo/cyber-harness` 的 `tianwu/fixes` 分支，构建方式见 [README](README.md)。gate 通过 `-harness-dir` / `HARNESS_DIR` 或默认的 `.build/harness-src` 找到源码，把 Hub 与 scan 的构建转发给引擎 Makefile 的 `engine`/`scan` 目标（标签由引擎 Makefile 定义，外壳不再自行拼标签）；不要把 harness 源码或二进制复制进本仓库。需要宿主集成测试时，显式设置 `ZRCIN_HARNESS_BIN` 和 `ZRCIN_SCAN_BIN` 分别指向 Hub 与 scan 二进制；未设置时相关测试跳过，它们不替代 gate 的 harness 重建与清单记录。隔离验证及浏览器脚本见[验收说明](scripts/acceptance/README.md)。

AOP 协议只有一份，在引擎仓：`internal/aop` 直接 import 引擎的 `cyber/aop`（协议消息）、`cyber/aop/{operation,tool,traffic,pty}`、`cyber/exts/guardrail` 与 `cyber/core/types`，本仓不复制任何 `*.pb.go`。协议内容要改就改引擎仓的 proto 并在引擎侧重新生成；`go.mod` 的 `replace => ../cyber-harness` 使默认构建也依赖同父目录的引擎检出（CI 已检出，docs/02 记录目录布局）。

## 变更要求

- 为行为修复添加覆盖真实失败模式的回归测试；协议、授权与数据模型改动需说明兼容性和失败模式。
- 协议消息只改引擎仓的 proto 并在引擎侧重新生成；本仓 `internal/aop` 只 import 引擎包，不复制生成文件。
- 当前架构集中维护在 `docs/01-architecture.md`。修改行为时同步更新该文档或 README，不再新增阶段评审、路线图或交接文档。
- 一个 PR 聚焦一个问题，描述触发条件、前后行为与验证结果。
- 不提交 `data/`、`.build/`、`bin/`、`logs/`、`platform.yaml` 或数据快照。发布前用仓库的 `.gitleaks.toml` 扫描待发布内容。
- 不重启正在使用的控制面或宿主，不修改运行中的项目数据。验证使用临时目录与本地假模型。

## 反馈问题

Bug 请附版本或提交号、复现步骤、期望与实际行为及脱敏日志。功能建议请说明场景与边界。4zrcin自身安全问题通过 [安全策略](SECURITY.md)规定的私密渠道报告，不在公开 issue 中披露利用细节。
