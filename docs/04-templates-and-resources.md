# 04 平台二进制形态与资源模板设计

状态：**设计定稿，未实施**。前身讨论见 docs/03 §3.3（构建标签已核实事实）与 docs/03 §11 P5（harness 补丁）。

## 1. 目标形态（三件套）

| 二进制 | 内容 | 形态 |
| --- | --- | --- |
| **`bin/zrcin`** | console（生成器 bundle）+ hub + **harness 控制面（webhost/AOP/host 监督）** | **单二进制**：一个文件即完整控制平面；本机 scan 仍为子进程，由 zrcin 按现状拉起并注册为 local 节点 |
| **`cyber-scan`** | 按构建配方裁剪的执行节点 | **可组合**：scan-full / scan-capture（/scan-agent），标签选择编入的引擎与资源 |
| **console** | 生成器压缩 bundle + `noconsole` 标签 | zrcin 的嵌入资产，与 hub 同生命周期 |

核心判断：**P5 的三个 23 系补丁（23a CIDR、23b builtin 审计、23c Artifact sidecar）正好是 scan-capture 配方的最小能力集**——围栏判定、保底审计、证据回传。P5 不是与形态无关的独立工作，它就是远程围栏节点的功能定义。

## 2. 已核实事实（构成约束）

1. vite `outDir` 直写 `../web/dist`，committed，2.0M 无压缩；`go build` 零 Node 依赖靠的是产物 committed。
2. zrcin-gate step 5 已有启发式新鲜度检查（`CheckEmbeddedFreshness` 前后快照 diff）。
3. hub 依赖树不含 chainreactors 模板栈（docs/03 §3.3）——平台侧解码只用标准库 `compress/flate`；**scan 不进 zrcin**（gogo/模板栈/引擎依赖树会破坏该边界）。
4. `tools/resources/template.go` 的 472K bundle **无标签全嵌**；`emptytemplates/noembed` 只裁 gogo fork 模板——scan 自身裁剪机制不存在，是本设计新增。
5. zrcin 已有 sidecar 消费先例：`internal/aop/trafficverdict.go` 的 `ParseTrafficVerdict`（Phase 1 判定回传）——23c 接收侧照此模式。
6. `internal/node/node.go` 已定义节点 = "机器 + harness host + 工具链"，本机即 "local" 节点；**docs/14-nodes.md 被 node.go 引用但文件缺失**，本设计的 T5 补写。
7. 补丁 22 设计已定（`webhost.Config` 加可选 `Listener net.Listener`，约 5 行，FORK.md 登记）。

## 3. 后端设计

### 3.1 zrcin 侧（4zrcin 仓库）

**M1 单二进制（补丁 22 消费，已落地）**

- `main.go` 启动顺序：hub 的 HTTP 面板跑在 in-process `webhost.Serve` 上——`webhost.Config{Listener: loopback}`（补丁 22 的入参），AOP 协议边界、模板/构建标签机制、host 监督语义全部保留；
- 本机节点形态：`host.go` 增加 inproc 模式——不再 exec `cyber-harness` 子进程，改为进程内起 host + 子进程 scan；节点注册表不变（"local" 照旧，`node.Registry` 无感）；
- `--harness exec` 逃生舱保留：显式 flag 时回到旧 exec 路径（调试/回滚通道）；
- zrcin-gate 构建检查同步标签约定。

**落地决策（实施时确定）：**

- **模块解析**：fork 模块路径 `github.com/c1ayooo/cyber` 与仓库名 `cyber-harness` 不一致，无法 `go get`——`go.mod` 用 `replace => ../cyber-harness`（与 gate 的同父目录检出约定一致）。但 replace 只对真正 import 该模块的构建生效，所以 **inproc 只存在于 `-tags inprochost` 构建中**：默认构建的 import 图不含 cyber 模块，无检出环境照常编译（CI `-skip-harness` 不受影响）。gate 在 harness 检出可用时给 zrcin 构建加该 tag。
- **协议类型冲突与转发别名**：进程内同时存在 vendored 副本（`internal/aop/*`）与 fork 的 `cyber/aop/*` 时，protobuf 全局 registry 注册同一 descriptor/message 即 panic（文件路径 rename 只解决文件冲突，message full name 仍冲突）。解法：inprochost 构建下 vendored generated 文件（`//go:build !inprochost`）替换为**转发别名包**（`alias_gen.go`，`//go:build inprochost`，`type X = fork.X`）——图中只留一份类型，GlobalTypes 只注册一次。alias 由 `-sync-aop-protos` 与 vendored 副本从同一 harness 源一并生成，两种构建形态永不描述不同协议版本。rawDesc 的 descriptor 路径 rename（vendored 命名空间 `4zrcin/aop/...`，长度前缀同步修正）随别名方案保留：exec 形态下 fork 不在图中、冲突不存在，但 vendored 与 harness 的漂移检查统一处理。
- **验收锚点达成**：`go build -tags inprochost -o bin/zrcin ./cmd/zrcin` 单文件（67MB，含 console + hub）起完整实例——进程内 Hub 就绪（`host_event=ready`）、scan 子进程全引擎加载、console 与 hub HTTP 双面可用、节点 "local" 照旧。

**23c 接收（远程节点的证据回传，已落地）**

- hub 收到 Envelope 中的 `4zrcin.Artifact` anypb sidecar → 解析 → 落盘**中心** case 目录；实现照 `ParseTrafficVerdict` 模式（`internal/aop` 加 Parse，`internal/api` artifacts 落盘处接线）；
- 旧 scan（无 sidecar）走共享文件系统路径不变——**双模式共存**，升级不要求节点与 hub 同步换版。

**23 系实施要点（实施时确定）：**

- 23a 的 CIDR 匹配在 fork `core/scoperules.hostMatches`：IP 字面量直接比对，域名走有界缓存解析器（5 分钟 TTL、2 秒超时、512 条、负结果同缓存）；**所有失败形态都是 miss**（围栏是安全属性，基础设施抖动绝不能把操作者挡在自己的范围内），无法解析的规则行兜底精确主机名。
- 23b 语料 `tools/resources/guardrail_builtin.yaml` 经 `builtin_gen.go` 生成 `guardrail_builtin_gen.go`（**无 build tag**——保底审计语料随 `emptyresources` 裁剪保留，"审计责任不是能力"）；审计挂在 policy nil 检查之前，仅记录不裁决，命令随后照常走正常策略流。
- 23c 载荷 JSON-in-Any（fork 扩展惯例），`core/artifactsync` 定义 URL/限额（单文件 8 MiB、每批 16 文件/64 MiB、`.zrcin` 节点元数据与删除永不回传）；observe 新增 `artifacts` kind：进程完成事件携带快照 diff 的产物、文件 hook 的带内写入直接装 sidecar。
- **顺带修复现存 wire bug**：observe 的 emit 对预构造 `*anypb.Any` 再包一层 Any，导致控制面按 URL 精确匹配永远看不到 `4zrcin.TrafficVerdict`——预构造 sidecar 现在直接附加（单层）。
- tw 接收：`aop.ParseArtifact`（照 ParseTrafficVerdict 的"绝不猜测"纪律）→ `executor.Event.Artifacts`（传输期专用，`json:"-"`）→ `api.saveRemoteArtifact` 用节点侧 case 相对路径作稳定身份落盘（重放/重发哈希到同一文件），失败降级 warning 行不丢 run。

**节点能力路由（scan-capture 的调度面）**

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
| **新增：资源裁剪** | `tools/resources` 两文件模式：`//go:build !emptyresources` 的 template.go + `resources_empty.go` 空实现（仿 gogo `emptytemplates`） | scan-capture 配方甩掉 472K bundle |
| **新增：能力裁剪** | `cmd/cyber-scan` 引擎/capability 注册按标签裁剪（`capability.go` 注册表已存在）；host 报告 capabilities 字段 | 配方矩阵 + zrcin 能力路由 |

### 3.3 scan 构建配方矩阵

| 配方 | 标签 | 编入 | 排除 | 目标场景 |
| --- | --- | --- | --- | --- |
| `scan-full`（现状默认） | 无附加 | 全引擎 + fingers/neutron + 模板 bundle + gogo | — | 本地节点 / 全功能远程节点 |
| `scan-capture` | `emptyresources` + capability 裁剪标签 | traffic 代理 + guardrail（23b）+ 判定（23a）+ Artifact sidecar（23c） | 扫描引擎、472K bundle、gogo 模板 | P5 远程围栏节点、纯流量记录节点 |
| `scan-agent`（可选，暂缓） | 进一步裁剪 | 任务执行 + agent 面 | 扫描 + 流量 | 轻量执行节点——出现真实需求再定 |

`build-harness.*` / zrcin-gate 构建矩阵输出：`cyber-scan`（full）与 `cyber-scan-capture` 两个产物；体积对比进 build manifest。

**T4 落地决策（实施时确定）：**

- 能力裁剪用**单一 `captureonly` 标签在装配层切**（`engineExtensions` 的 full/empty 双孪生文件 + discovery/scanner-catalog 的空孪生），不在 capability 注册表内逐能力加标签——引擎扩展包不编入时整条 SDK 链（gogo/spray/zombie/neutron/katana）自然不链接，逐能力标签只增加构建矩阵成本而无收益；
- 实测体积：full scan 185 MB → capture 158 MB（省约 27 MB / 15%）；`capture_saved_bytes` 进 build manifest（`internal/buildinfo.captureSaved`，两个 scan 产物齐备才记录，缺一个即 nil 事实）；
- capture 节点的 hub 连接走 `CYBER_CYBERHUB_*` 环境变量/配置文件通道（CLI flag 组属于被裁引擎扩展，随之编出——flag 面是否需要回补是 T5 验收项）；
- 测试形态：断言 full 配方 CLI 面的测试文件带 `!captureonly`（capture 二进制确实没有 `scan` 子命令与扫描器目录）；`noconsole` 保持 gate 只做 smoke build。

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
| T1 | console 生成器 + `noconsole` + gate 升级（§4.1） | zrcin | 无 |
| T2 | **补丁 22**（Listener）→ **M1 inproc**（§3.1）→ 单二进制验收 | 双仓 | T1 无关可并行 |
| T3 | **补丁 23a → 23b → 23c** + zrcin 23c 接收（§3.1） | 双仓 | P1 schema 定稿（已合入） |
| T4 | scan 配方机制：资源裁剪 + capability 裁剪 + build 矩阵（§3.3） | harness | 23a/b/c 合入（capture 配方才有意义） |
| T5 | **docs/14-nodes.md**（补写缺失文件）+ 节点能力路由 + console 节点呈现（§3.1/§4.2）+ 远程 capture 节点验收 | 双仓 | T2+T3+T4 |

**T5 落地决策（实施时确定）：**

- **docs/14 已补写**：节点形态三矩阵、`--transport web` 接入步骤、配方矩阵与构建指令、能力路由语义、补丁 23 后的远程节点要求（不再依赖共享文件系统）；
- **路由的诚实语义**：zrcin 的方向没有结构化"scan 意图"标记，故过滤落点是**隐式默认路由**（方向未指定节点）——capture-only 节点在派发时即拒绝（错误明示改绑/显式指定），**显式指定**尊重操作员（流量记录/围栏会话按名字放到 capture 节点是刻意选择）；"只收 traffic/guardrail 类任务"的机制本质是 T4 的工具面裁剪（节点物理上无法执行扫描），路由过滤是失败前移的最后一层；
- **能力双通道**：声明（`Spec.capabilities`，操作员部署事实）+ 运行时报告（hello → ListAgents → `aop.Agent.Capabilities` → `nodeProber.Health.Capabilities` → 徽章）；自动发现节点的 Spec 从 hello 回填 capabilities（无操作员声明可依赖时以运行时报告为路由输入）；声明与报告不一致呈现在徽章，不做强制仲裁；
- **顺带修复既有缺陷**：`Registry.IDs()` 漏掉自动发现节点，probe 对它们永不执行——态势页上 discovered 节点永远 offline 且拿不到配方徽章；现在 IDs 纳入 discovered（`Spec()` 本就支持）。

验收锚点：T2 后 `bin/zrcin` 单文件起完整实例；T3 后远程 scan-capture 节点在无共享文件系统的机器上完成"围栏判定 + 保底审计 + 证据回传"闭环；T4 后 `cyber-scan-capture` 体积对比进 manifest。

## 6. 语料包与运行时三层（约定，暂不建）

- **zrcin 侧语料包**（`internal/resources`：`resources/<kind>/*.yaml` → 生成器 → embedded，`ZRCIN_RESOURCES_DIR` override-only）：触发条件 = 真实语料 ≥2 个 kind 或 docs/14 定稿后需要 hub 下发。此前不建。
- **运行时三层**（embedded → 磁盘 → hub 下发，merge/override 语义照 harness `resources.Set`）：接口方向预留（`GET /api/resources/manifest`），与 docs/14 一批定。
- **23b 规则集单源在 harness**，用其既有生成器；zrcin 呈现经 audit rows，不复制语料。

## 7. 风险与回滚

| 风险 | 缓解 |
| --- | --- |
| scan-capture 裁剪漏依赖（引擎 import 残留） | 配方构建即验证：`go build -tags … ./cmd/cyber-scan` 进 gate 矩阵；体积回归对比 |
| capability 报告与 zrcin 路由版本错配（旧 host 无 capabilities 字段） | 字段缺省 = full（保守路由，与现状行为一致） |
| 23c 双模式共存期证据路径分歧 | 中心 case 目录为唯一权威；共享文件系统路径保留只读兼容 |
| M1 inproc 改变启动故障面 | `--harness exec` 逃生舱保留；inproc 失败回退 exec 并告警 |
| dist 不再 committed，review diff 可读性下降 | manifest per-file sha256 可追溯；review 以 web/src 源码 diff 为主 |
| 双标签测试矩阵成本 | gate 默认 `!noconsole`/full 全量；noconsole 与 capture 配方只做 smoke build |

回滚：各 T 独立 revert。T1 是纯文件移动 + 单文件服务替换；T2 保留 exec 逃生舱即运行时回滚；T3/T4 revert 后旧路径（共享文件系统 / 全量 scan）原样可用。
