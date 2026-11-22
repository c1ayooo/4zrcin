# 天吴部署清单（三个 exe）

一次部署要同时核对三个可执行文件。任何一个旧了，「修复是否在跑」都是假结论——
2026-11-21 的评估器用量问题就是只换了 hub、漏了真正执行会话的 scan，连续两次窗口白做。

| # | 文件 | 来源 | 从哪启动 | 装的是什么 |
|---|------|------|----------|------------|
| 1 | `tianwu\.build\tw-zw.exe` | 本仓 `cmd/tw` | 人工/脚本直接启动 | 控制面：HTTP API、控制台静态资源、调度、账本 |
| 2 | `cyber-harness\cyber-harness.exe` | fork hub | tw 参数 `--harness <path>`，作为 tw 的子进程 | Hub：AOP 端点、会话托管、节点管理 |
| 3 | `tianwu\bin\cyber-scan.exe` | fork scan 节点 | tw 参数 `--scan <path>`，作为 tw 的子进程 | **真正执行会话与工具调用的那一侧** |

结论先说：**改 agent / 会话 / 评估器 / 工具实现，落在 #3；改 AOP 托管与节点管理，落在 #2；
改控制面、账本、界面，落在 #1。** 只换 hub 而会话代码在 scan 里，等于没换。

## 验收方法（每个 exe 都做，缺一不可）

1. **映像路径**：从监听端口反查 PID，再用 PID 查 `ExecutablePath`，不要凭参数推断。
2. **文件时间**：必须晚于修复提交时间。文件时间早于提交，说明跑的是旧产物。
3. **提交号**：`go version -m <exe>` 看 `vcs.revision`，并确认 `vcs.modified=false`；
   `modified=true` 说明构建时工作树不干净，产物无法与提交对应。
4. **符号/字符串**：按字节计数复核（`open(...,'rb').read().count(b'...')`），
   不要用 `Select-String`——它在几十 MB 的二进制上会假报 0 命中。
   查不到时用第二个工具（`findstr`、`go tool objdump`）复核再下结论。
5. **实际请求**：静态资源只走 `/static/assets/<chunk>`，且**必须带控制台 token**（`data\console.token`），
   核对 `content-type` 是 `javascript`。匿名请求任何非 `/static/` 路径都会被登录页接住
   （200 + 约 2.8KB HTML，落点 `cmd/tw/auth.go:449` 的兜底 `mux.HandleFunc("/")` → `loginPage`（`:577`）），
   带 token 后不存在即 404。所以「匿名 200」既不是 SPA 回退，也不能当 chunk 已加载。
   另：**static 没重建时新旧入口 chunk 同名**（2026-11-22 04:53 实测两边都是 `index-C2Ce3IyN.js`），
   此时「旧 chunk 必须 404」不适用，会自撞判红；只比新 chunk 200 + content-type 即可。

## 停启顺序

1. 停 tw（`Stop-Process -Id <tw>`）。**不要假设子进程会跟着走**：2026-11-22 一天内连续三次
   「停 tw 后旧 hub 仍活着并占着 18799」，都由脚本当场清掉。判据是**显式确认 18200 与 18799
   两个端口都空出**，不是看 tw 的 PID 没了。
   新 tw 会拉起一对**新 PID**——所以换完 tw 后「PID 变了」正常，
   判据是映像身份四项（路径、大小、文件时间、提交号）不变，不是 PID 不变。
   另外**起完必须确认 hub 在 18799**：端口被占时 hub 不会回到 18799（现场见过落到 18790），
   24 个会话指针随之被判 stale 改名重建，等于一次冷启。端口不对就重做干净重启。
2. 备份要换的 exe，记录 SHA256，与换前值比对。
3. 换名（先把新文件放到目标路径，再起 tw）。
4. 起 tw 用原命令行（存档在 `_build-artifacts\tw-start-cmdline.txt`），参数一个不改。
5. 起完按上表逐个核 #1/#2/#3 的 PID、映像路径、文件时间、提交号、符号。

## 干净环境冷启动（Windows）

本节写「一台没碰过这个项目的机器要起来，最少需要什么」，全部条目取自代码与本机事实；**尚未在另一台机器实测**，实测清单由「集成」维护，本节只作核对基准。Linux 见末尾单独一节，标注未实测。

**依赖**（版本以仓库为准，不靠记忆）：

| 依赖 | 出处与用途 |
| --- | --- |
| Go 1.26.0 | `go.mod` 的 `go 1.26.0`；编译 `tw`、`tw-repair` 与 harness |
| Git | 取源码；Windows 上还提供 Bash 与 coreutils |
| Node 24+ / pnpm | 只在改前端、跑文档链接检查与 gate 时需要（产物已入库，常规启动不需要） |
| Git for Windows 的 Bash + `ls`/`grep`/`sh` | 宿主的 shell 工具是 POSIX 解释器，`internal/host/host.go:766-774` 会把 Git 的 `usr/bin` 补进子进程 PATH；缺了模型只能退到 `cmd`/`dir` |
| harness 源码（`tianwu/fixes`） | `scripts/build-harness.ps1 -HarnessDir <dir>` 一次编出 `cyber-harness.exe` + `cyber-scan.exe`（Hub 带 `full` tag，脚本 `:44`）；两者必须同一次构建 |
| 可达的模型端点 | OpenAI 兼容；不可达时宿主不就绪 |

**启动**（本机存档在 `_build-artifacts\tw-start-cmdline.txt`，参数一个不改）：

```powershell
.build\tw-zw.exe --addr 127.0.0.1:18200 --data <绝对数据目录> --project <项目ID> `
  --host-addr 127.0.0.1:18799 --harness <...\cyber-harness.exe> --scan <...\bin\cyber-scan.exe> `
  --model-url http://<网关> --model <模型名>
```

- 模型也可以不给参数，改用 `CYBER_BASE_URL` / `CYBER_MODEL` / `CYBER_API_KEY`，或启动后在“设置 → 模型连接”填（`cmd/tw/main.go:80-82`）。
- 控制台令牌按 `--console-token` → `--console-token-file` → `TW_CONSOLE_TOKEN` → `<data>/console.token`（首次启动生成，`cmd/tw/auth.go:799`）。

**端口只有两个是入站**：

| 端口 | 谁在听 | 默认值 |
| --- | --- | --- |
| 控制台 | `tw` | `--addr`，默认 `127.0.0.1:8080`；现场用 `18200` |
| Hub | `cyber-harness` | `--host-addr`，默认 `127.0.0.1:18766`；现场用 `18799` |

`cyber-scan` **不监听端口**：它作为 web 客户端连 Hub（`internal/host/host.go:652-665` 的 `agent --transport web --server-url …`），出站到 Hub 与模型端点。一个控制台地址只允许一个实例。

**数据目录**（必须可写本地盘）：空目录首次启动会自动建默认项目（`cmd/tw/main.go:183`）；已有目录必须是当前版本支持的 SQLite schema，不自动迁移旧 JSON 账本。落地物：`<data>/console.token`、`<data>/host/cyber-web.db`（Hub 会话库）与 `cyber-web.db.node.yaml`（scan 配置）、`<data>/projects/<id>/ledger.sqlite`。

**最小验证**（按顺序，任一失败就不算起来）：

1. 不带令牌 `GET /healthz` → 200（`cmd/tw/auth.go:442` 在鉴权之前放行）。
2. 带令牌 `GET /p/<项目>/api/board` → 200，且 `state.kind` 有值。
3. 带令牌 `GET /static/assets/<入口 chunk>` → 200 且 `content-type` 是 javascript；同一路径**匿名**返回 200 是登录页，不是资源（见上文第 5 条）。
4. 18200 与 18799 各有一个监听进程，且分别是 `tw` 与 `cyber-harness`。
5. 三个进程都在：`tw`、`cyber-harness`、`cyber-scan`；停 `tw` 不保证带走后两个（见停启第 1 条）。

**Linux：未实测。** 已知差异只有这些，别当作已验证：产物无 `.exe` 后缀、入口为 `scripts/gate.sh` / `run.sh`、Git for Windows 的 coreutils 段不适用（系统自带）、进程组回收走另一套实现（`internal/host/orphans_unix.go`）、路径大小写与分隔符按平台测试走。天吴声称两平台同等支持（`docs/01-architecture.md` 第 9 节），但**本仓库没有在 Linux 上跑过真实实例**。

## 从构建产物起一个实例

构建产物是四个文件：`tw.exe`（= `bin/tw`）、`tw-repair.exe`、`cyber-harness.exe`、`cyber-scan.exe`，外加构建清单 `.build/build-manifest.json`。它们由本地 gate 编出（`cmd/tw-gate/main.go:20-25`：第 8 步 harness + scan，第 9 步 `bin/tw` 与 `bin/tw-repair`，第 10 步写清单），版本号靠 `-ldflags "-X main.buildVersion=…"` 注入；**当前没有独立发布脚本，也不打 tag、不做发布包**，不注入版本时 `tw --version` 就是 `tw dev`。

**校验三步，缺一步就不算正确产物**：

1. `tw --version` 输出 `tw <版本>`（`cmd/tw/main.go:107`）；清单 `control_plane` 里该二进制的 `version` 记录的是同一串 `--version` 输出（`internal/buildinfo/buildinfo.go:122`），两边对不上即坏产物。`tw dev` 表示没注入版本，不是正式产物。
2. 清单 `source.dirty` 必须是 `false`，`source.commit` 是这次要对齐的那一笔。
3. 清单 `control_plane` 里每个二进制的 `sha256` 与磁盘上文件对得上；`harness` / `scan` 必须来自**同一次** harness 构建，混版等于没换。

**放置与启动**：四个文件放同一目录即可省略 `--harness`/`--scan`（同目录 → `./bin` → PATH 查找）；建议仍显式写出。启动命令、端口、数据目录与最小验证按上一节「干净环境冷启动」执行，参数一个不改。

**回滚**：保留上一版四个产物与其 SHA256，按「停启顺序」整组替换；只换 `tw` 而 hub/scan 还是旧的，属于混版，判据会红。

**可复现判据**：同一个 commit 连跑两次 gate（或同一条 `go build`），四个产物的 sha256 必须一致。不一致说明产物里内嵌了时间、绝对路径或随机值，先找出内嵌点再交付。

## 从源码起一个实例（不做任何部署包）

不需要打包，也不需要预编译产物：在一台新机器上从仓库直接编、直接起。这样做的好处是产物身份由 `build-manifest.json` 自己写下来，坏在哪一步一眼可见；代价是必须先把三个运行时装齐。

**工具链（本机实测版本，写在这里是为了让版本差异可追）**：Go 1.27.0、Node v24.20.0、pnpm 12.14.0。Go 版本低于 `go.mod` 的 `go` 指令时直接报错，不会静默降级；`pnpm` 供 `web/` 的类型检查、测试与 static 构建。

**一次性准备**（把三个仓库放在同一个父目录下，harness 才能被自动找到）：

```powershell
git clone https://github.com/kksty/tianwu.git
git clone https://github.com/kksty/cyber-harness.git   # fork，hub 与 scan 的来源
pnpm --dir tianwu/web install
```

`internal/web/static` 是**已提交的构建产物**，源码树里就有；只有改了 `web/` 才需要重建（`pnpm --dir web build`，输出目录就是 `internal/web/static`）。重建后必须与源码同一个提交提交进去，否则页面与接口会不同步。

**一条命令编全部**：

```powershell
pwsh -File scripts/gate.ps1
```

11 步串行（`go build` / vet / race / 前端 typecheck+test+build / 文档闸 / AOP 生成代码漂移 / harness 构建 / `bin/tw` 与 `bin/tw-repair` / 清单）。任一步失败即停，**没跑过的检查不会被当成通过**。产物落在 `bin/`，清单落在 `.build/build-manifest.json`，`source.dirty` 必须为 `false`——主树里有未提交或未跟踪的源文件时它会是 `true`，那时编出来的二进制不能用于交付。

**起实例**：四个产物放同一目录，按「干净环境冷启动」那条命令起；模型端点两种写法任选其一——

```powershell
# 线上 API
.\bin\tw.exe --addr 127.0.0.1:18200 --data <数据目录> --host-addr 127.0.0.1:18799 `
    --harness .\bin\cyber-harness.exe --scan .\bin\cyber-scan.exe `
    --model-url <线上 API 基址> --model <模型名> --model-key <密钥>

# 或自建网关（本机就是这种）
.\bin\tw.exe ... --model-url http://127.0.0.1:3425 --model group/smart-split
```

密钥不要写进命令行历史：用 `--model-key` 时可先读环境变量，或起实例前 `$env:TW_MODEL_KEY`。


## 2026-11-21 的现场教训

- **从 DSH 会话里拉起的 tw，会跟着 DSH 重启一起消失。** 20:35 左右 DSH 重启，
  tw / hub / scan 三个进程同时不见、端口全空（不是 tw 崩了：三个一起没，
  是宿主把整棵进程树带走了）。`paused=true` 之类的落盘状态没丢，20:36:56
  重新拉起后 tw `16628` / hub `27012`(18799) / scan `10032` 即恢复。
  因此：**长期控制面必须用独立终端、计划任务或系统服务拉起**，
  不要挂在 DSH 会话、Bot 的后台任务或任何会被重启回收的父进程下。
  判定「实例还在不在」要同时看端口与三个进程，只看 tw 会漏掉
  「tw 在跑但 hub/scan 被回收」的半死状态。
- 会话跑在 `cyber-scan.exe`（PID 由 tw 拉起），hub 只做托管；只换 hub 后，
  评估器仍是 11-19（`39eac04a`）的实现，`addTokenUsage`、`usage_missing`、
  `eval_model_hash` 在 scan 里都是 0，journal 里也就看不到任何标记。
- 取消/失败的回合会写 `turn_ended` 但用量字段全 0；那是「没有可用用量」，
  不是「少报」，不能算成负差值（tianwu 侧同样不会用全 0 覆盖逐次之和）。
- 回环拨号失败会偶发假红（`agent/provider`、`agent/session` 各一例），
  单跑/复跑全绿即按假红记录，不为它放宽超时。
