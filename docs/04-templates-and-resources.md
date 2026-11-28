# 04 平台二进制形态与资源模板设计

状态：**已实施并合入**（2026-11-26：harness 补丁 22–25 → `tianwu/fixes`；zrcin 接收侧 T1–T5 → `main`）。各 T 的实施时决策见任务表后的"落地决策"段；前身讨论见 docs/03 §3.3（构建标签已核实事实）与 docs/03 §11 P5（harness 补丁）。

## 1. 目标形态（两个产物）

发布只有两个产物（docs/19 §2，已决 28–32）：

| 产物 | 内容 | 形态 |
| --- | --- | --- |
| **`bin/zrcin`** | console（生成器 bundle）+ 进程内 hub + **scan 节点装配**（`pkg/assembly`） | **单文件**：本地模式一个文件、两个进程——zrcin 起进程内 Hub，再用自己的可执行文件起一个节点子进程（`zrcin node agent --transport web …`），经回环 WebSocket 注册为 local 节点；`--no-local-node` 切纯控制面，`--scan`/`--harness exec` 是逃生与调试口 |
| **`cyber-scan`** | 执行节点 | **运行模式可组合**：full（默认）/ capture（`--capture-only` 启动开关，docs/19 P6），开关决定引擎块是否装配 |；与 zrcin 内嵌的节点装配同源 |
| **console** | 生成器压缩 bundle + `noconsole` 标签 | zrcin 的嵌入资产，与 hub 同生命周期 |

核心判断：**P5 的三个 23 系补丁（23a CIDR、23b builtin 审计、23c Artifact sidecar）正好是 capture 模式的最小能力集**——围栏判定、保底审计、证据回传。P5 不是与形态无关的独立工作，它就是远程围栏节点的功能定义。

## 2. 已核实事实（构成约束）

1. vite `outDir` 直写 `../web/dist`，committed，2.0M 无压缩；`go build` 零 Node 依赖靠的是产物 committed。
2. zrcin-gate step 5 已有启发式新鲜度检查（`CheckEmbeddedFreshness` 前后快照 diff）。
3. hub 依赖树不含 chainreactors 模板栈（docs/03 §3.3）——平台侧解码只用标准库 `compress/flate`；**scan 不进 zrcin**（gogo/模板栈/引擎依赖树会破坏该边界）。
4. `tools/resources/template.go` 的 472K bundle **无标签全嵌**；`emptytemplates/noembed` 只裁 gogo fork 模板——scan 自身不裁资源：capture 模式是装配层的运行开关（docs/19 P6），资源在两种模式下都随二进制携带。
5. zrcin 已有 sidecar 消费先例：`internal/aop/trafficverdict.go` 的 `ParseTrafficVerdict`（Phase 1 判定回传）——23c 接收侧照此模式。
6. `internal/node/node.go` 已定义节点 = "机器 + harness host + 工具链"，本机即 "local" 节点；**docs/14-nodes.md 被 node.go 引用但文件缺失**，本设计的 T5 补写。
7. 补丁 22 设计已定（`webhost.Config` 加可选 `Listener net.Listener`，约 5 行，FORK.md 登记）。

## 3. 后端设计

### 3.1 zrcin 侧（4zrcin 仓库）

**M1 单二进制（补丁 22 消费，已落地）**

- `main.go` 启动顺序：hub 的 HTTP 面板跑在 in-process `webhost.Serve` 上——`webhost.Config{Listener: loopback}`（补丁 22 的入参），AOP 协议边界、模板/构建标签机制、host 监督语义全部保留；
- 本机节点形态（docs/19 P6）：`host.go` 内嵌构建默认自执行——`NodeBin` 指向 zrcin 自己（`os.Executable()`），argv 前面加 `node` 动词（`zrcin node …` 走引擎 `pkg/assembly.Exec`，与 cyber-scan 行为一致）；`--scan <路径>` 显式给外部 cyber-scan 时不加前缀（只读挂载、禁止自执行环境的逃生口）；`--no-local-node` 只起 Hub 不起节点；节点注册表语义不变（"local" 照旧，`node.Registry` 无感）；
- `--harness exec` 逃生舱保留：显式 flag 时回到 exec 路径（调试/回滚通道）；
- zrcin-gate 构建检查同步标签约定。

**落地决策（实施时确定）：**

- **模块解析**：fork 模块路径 `github.com/c1ayooo/cyber` 与仓库名 `cyber-harness` 不一致，无法 `go get`——`go.mod` 用 `replace => ../cyber-harness`（与 gate 的同父目录检出约定一致）。但 replace 只对真正 import 该模块的构建生效，所以 **inproc 只存在于 `-tags inprochost` 构建中**：默认构建的 import 图不含 cyber 模块，无检出环境照常编译（CI `-skip-harness` 不受影响）。gate 在 harness 检出可用时给 zrcin 构建加该 tag。
- **协议单份**：落地时曾用 vendored 副本加转发别名解决 registry 双注册冲突；杠杆三（docs/19）之后外壳直接 import 引擎协议包，vendored 副本、别名与漂移检查全部移除，图中天然只有一份类型。
- **验收锚点达成**：`go build -tags inprochost -o bin/zrcin ./cmd/zrcin` 单文件（67MB，含 console + hub）起完整实例——进程内 Hub 就绪（`host_event=ready`）、scan 子进程全引擎加载、console 与 hub HTTP 双面可用、节点 "local" 照旧。

**23c 接收（远程节点的证据回传，已落地）**

- hub 收到 Envelope 中的 `4zrcin.Artifact` anypb sidecar → 解析 → 落盘**中心** case 目录；实现照 `ParseTrafficVerdict` 模式（`internal/aop` 加 Parse，`internal/api` artifacts 落盘处接线）；
- 旧 scan（无 sidecar）走共享文件系统路径不变——**双模式共存**，升级不要求节点与 hub 同步换版。

**23 系实施要点（实施时确定）：**

- 23a 的 CIDR 匹配在 fork `core/scoperules.hostMatches`：IP 字面量直接比对，域名走有界缓存解析器（5 分钟 TTL、2 秒超时、512 条、负结果同缓存）；**所有失败形态都是 miss**（围栏是安全属性，基础设施抖动绝不能把操作者挡在自己的范围内），无法解析的规则行兜底精确主机名。
- 23b 语料 `tools/resources/guardrail_builtin.yaml` 经 `builtin_gen.go` 生成 `guardrail_builtin_gen.go`（**无 build tag**——保底审计语料不随任何运行模式裁剪，"审计责任不是能力"）；审计挂在 policy nil 检查之前，仅记录不裁决，命令随后照常走正常策略流。
- 23c 载荷 JSON-in-Any（fork 扩展惯例），`core/artifactsync` 定义 URL/限额（单文件 8 MiB、每批 16 文件/64 MiB、`.zrcin` 节点元数据与删除永不回传）；observe 新增 `artifacts` kind：进程完成事件携带快照 diff 的产物、文件 hook 的带内写入直接装 sidecar。
- **顺带修复现存 wire bug**：observe 的 emit 对预构造 `*anypb.Any` 再包一层 Any，导致控制面按 URL 精确匹配永远看不到 `4zrcin.TrafficVerdict`——预构造 sidecar 现在直接附加（单层）。
- tw 接收：`aop.ParseArtifact`（照 ParseTrafficVerdict 的"绝不猜测"纪律）→ `executor.Event.Artifacts`（传输期专用，`json:"-"`）→ `api.saveRemoteArtifact` 用节点侧 case 相对路径作稳定身份落盘（重放/重发哈希到同一文件），失败降级 warning 行不丢 run。

**节点能力路由（capture 模式的调度面）**

- `node.Spec` / host 报告扩展 capabilities 声明（host 启动时报告自己的配方：full / capture / agent——ListAgents 或 ConfigService 载荷加字段）；
- dispatch 按 capability 路由：capture-only 节点只收 traffic/guardrail 类任务，scan 类意图不再派给它（失败前移为"派发时过滤"而不是"执行时报错"）；
- `nodeProber.Health` 顺带携带 capability（态势页呈现的数据源）。

### 3.2 harness 侧（cyber-harness fork，FORK.md 登记）

| 补丁 | 内容 | 服务于 |
| --- | --- | --- |
| **22** | `webhost.Config` 加可选 `Listener net.Listener`（传入时忽略 `Addr`），约 5 行 | M1 单二进制 |
| **23a** | scope_rules 判定 CIDR 分支（通配/精确未命中且含 `/` → 解析 IP 匹配；未知形态兜底精确主机名） | capture 节点的围栏判定（P1 schema 的执行端实现，格式两侧一字不差） |
| **23b** | 内置高危命令集无条件审计，reason 带 `builtin.` 前缀；规则语料放 harness `tools/resources` 走**既有**生成器 | capture 节点的保底审计（zrcin P3 呈现已就绪，不复制语料） |
| **23c** | `4zrcin.Artifact` anypb sidecar：证据走 Envelope 流回 zrcin 中心 case 目录 | 远程节点的证据回传（解除共享文件系统耦合） |
| **新增：运行模式（P6 重构）** | 引擎块装配按 `--capture-only` 在运行时跳过（`appConfig.SkipEngines`），hello 自报 `scan-capture`；`pkg/assembly` 单一扫描器目录，capture 模式在扫描器执行前拒绝；host 报告 capabilities 字段 | 模式矩阵 + zrcin 能力路由 |

### 3.3 scan 运行模式矩阵（docs/19 P6 重构后）

| 模式 | 启动 | 装配 | 不装配 | 目标场景 |
| --- | --- | --- | --- | --- |
| full（默认） | `cyber-scan agent …` | 全引擎 + fingers/neutron + 模板 bundle + gogo | — | 本地节点 / 全功能远程节点 |
| capture | 同上 + `--capture-only` | traffic 代理 + guardrail（23b）+ 判定（23a）+ Artifact sidecar（23c） | 扫描引擎（扫描器执行前拒绝，帮助照常） | 远程围栏节点、纯流量记录节点 |

`build-harness.*`（`make harness` 转发引擎 Makefile）只产出 `cyber-scan` 一个产物；capture 模式不需要第二次构建。

**运行模式落地决策（docs/19 P6）：**

- 模式是**装配层的运行开关**：根 flag `--capture-only` 置 `appConfig.SkipEngines`，引擎块沿既有 seam 跳过；不在 capability 注册表内逐能力加开关；
- hello 自报为运行时判断：`--capture-only` 时 hello 带 `scan-capture`，否则不报——语义与两个配方时代的孪生逐条一致（保守缺省见 docs/14 §4：无标记即 full）；
- 扫描器目录在两种模式下全量（命令行解析与帮助不需要知道开关），拒绝放在 `runDirectScannerMode` 执行前；
- 隧道接入配方 capture 由启动器把开关带上命令行（docs/14 §7.2）；资源包不随模式裁剪。

## 4. 前端设计（console）

### 4.1 生成器 bundle（T1，独立可先行）

```
web/src …            源码（committed）
web/dist …           vite outDir（gitignore，中间产物）
internal/web/bundle  生成器产物：manifest.json + data/*.bin（committed）
web/dist  退役
```

- `internal/web/generate/main.go`（`go:build ignore`）：flate 压缩 + manifest（path/sha256/size）+ 可重复构建自检；预估 2.0M → 500–600K；
- `Static()/Index()` 签名与缓存语义不变（assets immutable、HTML no-cache），按请求解压（单文件 <300K，<5ms）；
- **`noconsole` 标签**：`console_full.go`（默认，bundle 实现）/ `console_empty.go`（404 stub）；测试与无 UI 构建甩掉 bundle；受影响测试加 `//go:build !noconsole`；
- **gate 升级**：step 5 主检查改为确定性 manifest 比对（dist 聚合 hash ≠ manifest 即失败），快照保留兜底；新增 `-tags noconsole` smoke build。

### 4.2 单二进制下的前端语义

- **版本一致性**：bundle manifest hash 进 buildinfo；单二进制后 console 与 API 天然同版本，manifest 的价值转为防"构建错配"（dist 变了没重新 generate）；
- **节点能力呈现**（新 UI 面，T5）：态势页节点卡显示 capabilities/配方徽章（数据源 = nodeProber.Health 扩展字段）；添加节点对话框给出对应配方的部署指令（`go build -tags … ./cmd/cyber-scan` 可复制）；
- **capture-only 节点的既有页面适配**：流量/策略页是项目维度，无需改动；捕获卡（P4 已可写）在 unfenced + capture 节点组合下的语义已由 P3/P4 文案覆盖。

## 5. 实施顺序

| 步骤 | 内容 | 仓库 | 依赖 |
| --- | --- | --- | --- |
| T1 ✅ | console 生成器 + `noconsole` + gate 升级（§4.1） | zrcin | 无 |
| T2 ✅ | **补丁 22**（Listener）→ **M1 inproc**（§3.1）→ 单二进制验收 | 双仓 | T1 无关可并行 |
| T3 ✅ | **补丁 23a → 23b → 23c** + zrcin 23c 接收（§3.1） | 双仓 | P1 schema 定稿（已合入） |
| T4 ✅ | scan 配方机制：资源裁剪 + capability 裁剪 + build 矩阵（§3.3） | harness | 23a/b/c 合入（capture 配方才有意义） |
| T5 ✅ | **docs/14-nodes.md**（已补写）+ 节点能力路由 + console 节点呈现（§3.1/§4.2）+ 远程 capture 节点验收 | 双仓 | T2+T3+T4 |

**T5 落地决策（实施时确定）：**

- **docs/14 已补写**：节点形态三矩阵、`--transport web` 接入步骤、配方矩阵与构建指令、能力路由语义、补丁 23 后的远程节点要求（不再依赖共享文件系统）；
- **路由的诚实语义**：zrcin 的方向没有结构化"scan 意图"标记，故过滤落点是**隐式默认路由**（方向未指定节点）——capture-only 节点在派发时即拒绝（错误明示改绑/显式指定），**显式指定**尊重操作员（流量记录/围栏会话按名字放到 capture 节点是刻意选择）；"只收 traffic/guardrail 类任务"的机制本质是 T4 的工具面裁剪（节点物理上无法执行扫描），路由过滤是失败前移的最后一层；
- **能力双通道**：声明（`Spec.capabilities`，操作员部署事实）+ 运行时报告（hello → ListAgents → `aop.Agent.Capabilities` → `nodeProber.Health.Capabilities` → 徽章）；自动发现节点的 Spec 从 hello 回填 capabilities（无操作员声明可依赖时以运行时报告为路由输入）；声明与报告不一致呈现在徽章，不做强制仲裁；
- **顺带修复既有缺陷**：`Registry.IDs()` 漏掉自动发现节点，probe 对它们永不执行——态势页上 discovered 节点永远 offline 且拿不到配方徽章；现在 IDs 纳入 discovered（`Spec()` 本就支持）。

验收锚点（全部达成）：T2 后 `bin/zrcin` 单文件起完整实例；T3 后远程 capture 节点在无共享文件系统的机器上完成"围栏判定 + 保底审计 + 证据回传"闭环；T5 后本地双进程验收：hello 报告 `scan-capture`、态势页徽章、隐式派发 409 拒绝、显式派发 202 放行。P6 后同一验收以 `--capture-only` 启动开关表达（引擎仓 `pkg/assembly/capture_test.go` 断言两向语义）。

## 6. 语料包与运行时三层

- **zrcin 侧语料包**（`internal/resources`：`resources/<kind>/*.yaml` → 生成器 → embedded，`ZRCIN_RESOURCES_DIR` override-only）：触发条件 = 真实语料 ≥2 个 kind 或 docs/14 定稿后需要 hub 下发。此前不建。
- **运行时三层**（embedded → 磁盘 → hub 下发）：磁盘一层已落地——本实例的指纹与 POC 是 `<data>/rules` 下的文件，节点上的 `exts/4zrules` 在运行时加进引擎，同名时实例规则生效（docs/20）；hub 下发留给远程节点，尚未建。
- **23b 规则集单源在 harness**，用其既有生成器；zrcin 呈现经 audit rows，不复制语料。

## 7. 风险与回滚

| 风险 | 缓解 |
| --- | --- |
| capability 报告与 zrcin 路由版本错配（旧 host 无 capabilities 字段） | 字段缺省 = full（保守路由，与现状行为一致） |
| 23c 双模式共存期证据路径分歧 | 中心 case 目录为唯一权威；共享文件系统路径保留只读兼容 |
| M1 inproc 改变启动故障面 | `--harness exec` 逃生舱保留；inproc 失败回退 exec 并告警 |
| dist 不再 committed，review diff 可读性下降 | manifest per-file sha256 可追溯；review 以 web/src 源码 diff 为主 |
| 双标签测试矩阵成本 | 已随 P6 消解：一个二进制一套测试面；gate 保持 `!noconsole`/full 全量与 noconsole smoke build |

回滚：各 T 独立 revert。T1 是纯文件移动 + 单文件服务替换；T2 保留 exec 逃生舱即运行时回滚；T3/T4 revert 后旧路径（共享文件系统 / 全量 scan）原样可用。
