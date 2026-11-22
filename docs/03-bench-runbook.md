# 测评运行手册（TSecBench API 接入）

把天吴接到 TSecBench API，在另一台 Windows 上无人干预跑一场，跑完还能被完整复盘的最小流程。

本手册只适用于**显式配置了测评的实例**。不配 bench 的实例没有这条代码路径：真实渗透项目的暂停语义、内置约束文档与审批模型都不随本手册改变。

## 前置检查（缺一条就不要开跑）

1. **构建里有 `tw bench`**：`bin\tw.exe bench -h` 有输出。没有这条子命令的构建（例如 2026-11-22 之前 gate 编出的 `bin\tw.exe`）照本手册开跑只会卡在第一条命令——先换成本次带 bench 的构建。
2. **产物能说出自己是谁**：`bin\tw.exe --version`；四件产物（`tw`、`tw-repair`、`cyber-harness`、`cyber-scan`）来自同一次构建，`cyber-harness` 与 `cyber-scan` 混版等于没换。
3. **干净机器依赖**：Git for Windows（tw 启动时会用 `ls` 做权限完备性检查，缺了它会自己补 `C:\Program Files\Git\usr\bin`）。

## 1. 模型先通，再谈平台

模型不通时控制台能起来，但**指挥官轮一个都不会跑**，账本会一直是空的——这是换机器后第一个会卡住的地方。

启动时给三个值（参数与环境变量等价，环境变量优先按程序约定）：

| 参数 | 环境变量 | 例子 |
|---|---|---|
| `--model-url` | `CYBER_BASE_URL` | 运行端可用的 OpenAI 兼容端点 |
| `--model` | `CYBER_MODEL` | 实际模型 id |
| `--model-key` | `CYBER_API_KEY` | 端点要求的 key |

准备端的 `http://127.0.0.1:3425`（`group/smart-split`）只属于准备端；运行端要么跑同样的网关，要么填真实端点。

**判据**：控制台里让它跑一次最小编排，账本里出现第一条 run（`data/projects/<id>/runs/`）。0 个 run 就去查模型与 key，不要接着填平台凭证。

## 2. 平台凭证与 VPN

- 凭证来自准考证：`BENCHMARK_TOKEN`、`BENCHMARK_BASE_URL`。
- VPN 先连上（`ovpn` 是 `dev tun` + udp）。**`tw bench` 不做 VPN 预检**（这一层已砍）：连上后自己先 `ping` 一个容器地址，或手工 `GET /openapi/v1/challenges` 成功一次，再启 `tw bench`。
- 手工自检一次：`GET /openapi/v1/challenges` 返回数组；`container_addr` **只在 `container_status == available` 时非空**，其它状态平台按契约返回 `[]`。所以「有题在做」和「容器还活着」是两件事，空数组不等于题做完了。
- **任务时限从创建起算**，不是从开始跑起算：先把本手册第 1–3 步在这台机器上演练一遍，再让操作员在平台上创建任务。

## 3. 起实例

```text
bin\tw.exe --addr 127.0.0.1:18200 --data <data> ^
  --harness <cyber-harness.exe> --scan <cyber-scan.exe> ^
  --host-addr 127.0.0.1:18799 ^
  --model-url <url> --model <id> --model-key <key>
```

- 控制台：`http://127.0.0.1:18200/`；首次启动自动生成 `<data>\console.token`，用它登录。
- 项目级接口一律带项目前缀：`/p/<项目 id>/api/...`（实例级 `POST /api/project/pause` 不存在，启动时带了 `--project` 才有短路径）。
- 全新数据目录会自动建 `default` 项目，且**默认暂停**（`restart_pause`），要显式继续。

**测评实例的三项配置口径**（与真实渗透项目互不影响）：

1. **不配 `extensions.guardrail`**：默认 `provider=none`，即无工具审批（`internal/settings/guardrail_policy.go` 只认 `none/jev/llm/hybrid`；不写这段就是不启用）。真实项目要审批时才显式配 `provider=jev` 并重启宿主。
2. **不生成内置约束文档**：TSecBench 测评场景没有限制文档、也不开审批；内置约束与审批只用于红队、日常渗透项目。
3. **起点只写 `start` 返回的地址**：由 bench 运行器在每次 `start` 后，按最新 `GET /challenges` 快照把所有 `available` 容器的地址并集写进项目起点（审计与 `updated_by` 记 `bench`），旧地址不留；地址为空或快照不可用时**停手报错**，不得清空起点（静默清空等于把「没有目标」当成「没有限制」）。不要手工编辑起点，也不要用操作员消息下发地址——操作员指令优先级最高，运行器不能冒充它。

### 运行器（`tw bench`）

控制面起来之后，另起一个进程跑运行器；它只做四步循环：**轮询 → `Plan` → 执行（建目标 / `start` / 写起点）→ 睡 30s**，退出时写 `FinalScope`。

```text
bin\tw.exe bench -console http://127.0.0.1:18200 ^
  -data <data> -project <项目 id> ^
  -dump-dir <data>\bench\challenges ^
  -submit-log <data>\bench\submissions.jsonl ^
  -attempt-log <data>\bench\attempts.jsonl ^
  -interval 30s
```

- 平台凭证是环境变量，不放在命令行：`BENCHMARK_BASE_URL` 与 `BENCHMARK_TOKEN` **两个都给**才启用（缺一个直接报 `bench_not_configured` 退出，不会半开）。
- `-console` 的默认值是 `http://127.0.0.1:8080`；本实例监听在 18200，所以必须显式给。控制台令牌由 `-data` 自动找 `<data>\console.token`，也可用 `-token-file` / `-token` 指定（`-token` 会进进程列表，优先用文件）。
- `-dump-dir` 里每次成功 `GET /challenges` 落一份原始快照（默认 `<data>\bench\challenges`），是复盘与越界判据的唯一真值；`-attempt-log` 每次尝试一行（成功失败都记），`-submit-log` 每个 `submit` 一行（默认 `<data>\bench\submissions.jsonl`，收包脚本按这个路径取）。
- `-flag-regex` 默认 `flag\{[^{}]{1,512}\}`：平台 flag 形状不同才改，不要为了「多抓几个」放宽它。
- `-once` 只跑一轮就退出——**正式挂八小时前先用 `-once` 演练一遍**：确认控制台连得上、平台返回题目、起点被写成当前 `available` 地址、目标被建出来，再挂长跑。
- 目标按题稳定（`statement` 带 `unique_code`），地址只在起点：题 `close` 后同一地址可能分给别的题，地址写进目标会让换环境变成改目标、进而撤回达成收据。
- 本节参数已按 `tw bench -h` 的实际输出逐字核对（`feat/bench-client` `36f0bc25`）；合并后若 `-h` 有变，以 `-h` 为准并同步本节。

## 4. 看进度

- 控制台：项目状态、待你处理、目标进度、运行日志与图谱。
- 账本口径（`<data>/projects/<id>/`）：run 数与结果分布、`pause_kind`、每个方向的状态。目标应持续存在——目标耗尽说明平台没题或接线断了，停下来看，不要把它当成「跑完了」。
- 「连续自主」按 run 时间线判断：中间没有人工 `manual_pause`、没有项目级暂停，才算一段。

## 5. 中途停 / 恢复

- 暂停：`POST /p/<id>/api/project/pause`；恢复：`POST /p/<id>/api/project/resume`。
- 控制面重启后项目是 `restart_pause`（不是操作员暂停），需要显式继续；看护脚本可以只对这一种恢复。
- **停 tw 不保证 hub/scan 退出**（多次实测）：停之前确认 `18200` 与 `18799` 两个端口都空出；换二进制同理，否则会留下旧 hub 与新控制面混跑。

## 6. 看护（可选，只挂测评实例）

```text
pwsh -File scripts/watchdog.ps1 -Project <id> -DataDir <data> -Mode restart ^
  -StartCommand '<tw 的完整启动命令行>'
```

口径：默认 `observe` 只记不动；`resume` 只恢复 `restart_pause`（操作员暂停、目标完成、指挥官停止、无原因暂停一律不碰）；`start` 只在控制面端口空出、确认进程已消失时才按 `-StartCommand` 拉起，**停摆但仍在监听只记录、不杀进程**；run 数读不到时判 `unverified`，不当停摆。日志写在数据目录外（`_build-artifacts\watchdog\<id>.jsonl`）。

**真实渗透项目不要挂它。**

## 7. 跑完收包

```text
pwsh -File scripts/collect-run.ps1 -Project <id> -DataDir <data> -Out <zip>
```

提交器日志不用传 `-SubmitLog`：收包会先找 `<data>\bench\submissions.jsonl`（`tw bench` 的默认 `-submit-log` 路径）。只有自定义了 `-submit-log` 时才需要显式传。

收包只读、不动账本。zip 里固定有：`ledger.sqlite`（含 `-wal`/`-shm`）、`runs/*.jsonl`、`decisions.jsonl`、`project.json`、`state.md`、`operator-actions.jsonl`、提交器 JSONL（`submit/`）、`manifest/build-manifest.json`，外加 `COLLECT.json`（每项大小、sha256、来源、清单版本与 commit）。缺哪样就写进 `MISSING.txt` 并说明原因，不静默跳过。

拿到 zip 应能回答三个问题：每道题谁在什么时候做了什么、flag 是怎么找到的、哪些分是因为自己的 bug 丢的。

## 8. 跑完怎么判

- **判据**：目标一直存在的前提下，一段**没有人工暂停**的连续运行；失败只应废掉一轮（意图仍在、可重试），不应让项目停。
- **复盘三看**：① 失败构成（拨号、harness `stop_reason`、模型错误各有几次）；② 每轮 token 与其上下文曲线；③ `submit` 日志里的 `correct/awarded/cumulative_score` 与账本 flag 事实是否一一对应。
- **已知风险**：模型端点单点（挂了没人重启就整场停）；harness `stop_reason=error` 无根因（约一天一次，只废一轮）；Linux 未实测；自定义了 `-submit-log` 却没给收包脚本 `-SubmitLog` 时，收包会把「日志缺失」如实记进 `MISSING.txt`（默认路径已对齐，不会踩）。
