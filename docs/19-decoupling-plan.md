# 19 · 解耦与积木化方案（2026-11-27）

状态：**主体完成**。七个阶段里该做的（P0–P4、P6）、r10 收尾，以及规则热加载（docs/20，已决 38）都已落地并推送（引擎 `tianwu/fixes` @ `6c428392`，外壳 `feat/server-minimal` @ `cd5643d`，4zgears `feat/rules-hot-reload` @ `6d95d1b`）；`make check-fork STRICT=1` 通过，没有上游文件超预算；外壳 CI 全绿（`4zwebshell` 已公开）。范式符合度十条全部符合，「还差什么」一列已清零；P5 的两半逐包判断后决定不做（已决 37）。未关的只剩操作员手里的：历史里有真实凭证，令牌轮换与历史重写都等所有会话收工后执行。设计范式与现状符合度见 §1，拓扑与产物见 §2，判定标准见 §3，层级见 §4，模板见 §5，前端见 §6，构建见 §7，分期见 §11，收益与代价见 §13，风险、已决与实施约定见 §14，接缝见附录 A，关闭记录见附录 E。

本文档只保留当前的约定和未完成的工作。第一版到第二十六版的修订记录和被删的章节在 git 历史里（`git log --oneline -- docs/19-decoupling-plan.md`）。

## 1. 需求与验收

把现在这个「一个二进制装下全部」的系统，拆成可独立发版的积木：外壳只当组装点，引擎、规则、前端、扩展各自出货，靠数据契约相连，靠构建脚本组装。

**终点：在 harness 的基础之上搭积木。** 具体说，就是自己的东西只用上游开发者指南列出的组合机制搭起来；指南之外的搭法都是差距，登记在册并逐步消除。积木是引擎里我们加的能力，共五块：webshell、围栏、证据、会话载荷、装配。外壳是宿主，规则是数据，前端是产物，引擎内核是框架本体，它们不做成扩展。扩展是编译期静态组合的，不做运行时热插拔。对应关系：

| 官方机制 | 我们用它装什么 |
| --- | --- |
| `harness.BaseExtensions` 之后按依赖顺序追加，`extension.New` 装配 | 组合根里每块积木一行 |
| `Define[T]` / `Add[T]` 贡献点 | 工具、AOP 命名空间、提示词、技能 |
| `Provide[T]` / `Use[T]` 借用 | 围栏状态、判定入口、审批队列；不再用包级注册表传状态 |
| 钩子（`tool.before`、`process.*`、`file.access`、`http.completed`、`SessionStart`） | 围栏判定、证据回传、子会话继承 |
| `Declare` | 每块积木自己的配置节 |
| `webhost.Serve`（fork 的 `hubapp.App` 是包在它外面的纯新增，带一个 override 回调） | 外壳往 Hub 里加东西的唯一入口 |
| 架构测试（引擎 `pkg/internal/architecture` 的写法） | 两个仓的越界判定 |

### 设计范式

范式就是上游开发者指南的那一套，按指南自己的表述列在这里。它是判断「有没有按官方来」的尺子：

1. **宿主、扩展组合、执行运行时三分。** 宿主接收输入并管理应用寿命，扩展安装所需能力，运行时用这些能力推进工作。产品功能由入口的导入和装配代码决定，没有哪个产品是特殊的。
2. **一切皆扩展。** 扩展是把一项能力接入应用的组合单元，可以贡献工具、知识、提示词，也可以持有模型连接、终端、服务这类运行资源。业务实现是普通 Go 代码，扩展只做生命周期适配。
3. **组合根显式、有序。** `extension.New(a, b, c)` 按依赖顺序加载：资源所有者在前，贡献者与消费者在后。没有依赖图，没有靠导入副作用发现的插件，没有服务表。
4. **贡献点与借用两种连法。** `Define[T]`、`Add[T]` 是多对一，`Provide[T]`、`Use[T]` 是一个能力供后来者借用。类型就是键；借用不能指向自身或后面的扩展；拿不到就加载失败。指南原话：构造参数显式传入依赖，「避免运行时查找全局注册表」；「不要把借来的工具执行器或 Skill Store 塞入全局状态供任何代码随取随用」。
5. **钩子在执行边界上做控制与观察。** 钩子参与当前执行、能否决；事件只记录已经发生的事实。执行准入由工具或钩子控制，提示词只提供行为指导。
6. **协议是唯一的跨进程契约。** 实时执行走 AOP，管理查询走 ConnectRPC；扩展自带的协议放进自己的命名空间。
7. **每个功能声明自己的配置。** `Declare` 只声明，不启动资源；宿主解析之后把确定的值传给构造函数。
8. **分层由架构测试守着。** `core` 不依赖 `agent`、`pkg`、`tools`，`agent/` 保持发行版中立。
9. **宁删不加抽象。** 接缝只在两边确实不一样时才留（上游 `AGENTS.md`），所以向上游要的每个接缝都要说明两边为什么必须分开。

### 现状符合度

对照前八条。下表是 r10 收尾并推送之后的代码。第 9 条约束的是这次的设计：只新开 G1、G2、G3 三个接缝，每个都在附录 A 写了「官方现有的为什么不够」。数字取自引擎的 `scripts/check-fork.sh`，口径是与 `upstream/master` 相比的新增行。

| 范式规则 | 现在 | 证据 | 还差什么 |
| --- | --- | --- | --- |
| 三分；产品由入口装配决定 | 符合 | `pkg/harness` 的基础组合恢复上游，只多一个 `Policy` 槽（`base.go` +11，`harness.go` 与上游零差异）；只有 `cyber-scan` 装配围栏；最小 agent 与 audit 的 `profile.go` 与上游零差异 | — |
| 业务实现独立，扩展只做生命周期适配 | 符合 | 判定策略在 `exts/4zfence/policy`；guardrail 的 `extension.go`、`jev.go` 与上游零差异，`runtime.go` 只剩登记在册的 `SubmitReview` 9 行；代理的 4z 策略在 `exts/4zfence` | — |
| 组合根显式有序，每块积木一行 | 符合 | scan 的整个装配在 fork 自有的 `pkg/assembly`，对外只有 `assembly.Exec` 一个入口；`cmd/cyber-scan` 与 `zrcin node` 各是调用它的一行；围栏、会话载荷经它装配。`make check-fork STRICT=1` 通过，超预算的上游文件为 0（引擎 `db67d40e`） | — |
| 贡献点与借用，不查全局注册表 | 符合 | 会话表归扩展自有的 `Store`，出口代理的会话路由表归围栏扩展；装饰器、会话钩子、代理判定器都绑定在扩展实例上 | — |
| 钩子做控制与观察 | 符合 | 代理的请求判定走接缝 G2：`tools/proxy/mitm.go` +15、`hub.go` +6；判定 sidecar 的登记钩子 +3 | — |
| 协议只增不改，走 AOP | 符合 | 上游 proto 一个字段没改；载荷契约包两侧 import 同一份 | — |
| 每个功能声明自己的配置 | 符合 | `fence` 节由 4zfence 自持 `Declare`，结构体与校验在 `exts/4zfence/config`；guardrail 节恢复上游。外壳的 traffic 校验改调引擎的 `Normalize`（已决 25）；MCP 节改调引擎 `tools/mcp` 的校验（2026-11-28，已决 20；`search.tavily_keys` 的形状检查与 `llm` 节的读取留在外壳：引擎没有可共用的那一份） | — |
| 分层由架构测试守着 | 符合 | 引擎 `pkg/internal/architecture` 与外壳的层级表测试都通过；`cmd/agent` 与 audit 的依赖边界测试通过 | — |
| 宿主不绕过公开入口 | 符合 | 外壳对引擎的 Hub 内嵌只走 `hubapp.App` | — |
| 外壳手里的引擎协议只有一份 | 符合 | 杠杆三落地：7 个 vendored 协议包删除，import 直指引擎；外壳四处手写 protowire 换成引擎生成类型。r10：两套回合级 absorb 合并为 `internal/executor` 的 `Absorb`；IOA 信封改用引擎的 `core/collaboration`；`ListAgents` 改走引擎生成的客户端，手抄的 JSON 标签删除 | — |

结论：**目标状态按官方范式；十条全部符合，「还差什么」一列已清零。** 修复类补丁与接缝按已决 27 长期留存，每个都在 20 行以内。

整份计划按两条原则验收。

**原则一 · 最小化入侵 cyber-harness。** fork 等于「上游 + 纯新增」。上游已有的文件只允许登记在册的挂钩，逻辑全部在新增文件或自己的模块里。协议只增不改：新内容写进新增的 proto 文件或 anypb sidecar（fork 现有的 JSON-in-Any 惯例，docs/04 §3.1），不改上游消息。

落地按三级优先，钩子是最后一级：

1. **协议**：控制面与节点之间要传的东西，一律走 AOP——新消息或 anypb sidecar，不改上游
2. **扩展**：节点里照数据办事的代码，放进引擎的扩展（`exts/4z*` 或独立模块），不改上游
3. **钩子**：上游函数中途中必须改、扩展点又够不着的，才留几行调用

AOP 只负责传，不负责做。数据到了节点，总得有代码去读它、照它办，所以第 2、3 级免不了。每条补丁都按这个顺序登记在引擎仓 `FORK.md` 的「Local patches」。

**第 3 级长期留存。** 已决 27 定了不向上游提 PR，所以每条钩子在 `FORK.md` 里逐路径登记，由 `make check-fork` 报数：每个不超过 20 行，超预算的如实列出。差距清零做不到，压到登记在册的几处是能做到的。

验收（机器检查是引擎仓的 `make check-fork`：平时只报数，`STRICT=1` 时未登记或超预算即失败）：

- `git diff upstream/master...HEAD` 里，上游已有文件的改动只剩 `FORK.md` 列出的钩子文件；出现未登记的改动即失败
- 每个钩子不超过 20 行（口径见已决 24）；超了说明逻辑没放对地方
- 上游的 `.proto` 文件与上游一致，fork 的协议内容只在新增的 proto 文件或 sidecar 里
- `go.mod`、`Makefile` 的改动逐条记账

**原则二 · 自己的东西拆成积木。** 每块积木是独立模块，只通过契约与外界相连：引擎契约（`aop`、`core/tool`、`core/extension`）或数据（YAML、`.bin`、JSON、HTTP）。积木之间不互相 import，能单独换、单独摘。外壳只负责装配。

验收：

- 每块积木有一张积木卡（§2），写清形态、装配点、契约、摘除方式
- 外壳内部按层级表和架构测试做成真模块（§4）
- 模块边界立住之后，仓库归属按 §3 的三条标准逐个判断：满足的可以各自成仓，不满足的留在同一个仓

## 2. 目标形态：积木、引擎、外壳

终点不是「六个仓」，是「每块积木一行装配、越界可机器判定」。份数是结果：边界立住之后按 §3 的标准逐个判断，一次只多一个。

```
积木层（独立发版、独立 CI、互不 import 内部实现）
  4zweb          控制台前端，即现在的 web/（React 18 + Radix UI + Tailwind）；dev 挂载目录，prod 内嵌进外壳
  4ztemplates    规则 YAML（指纹/POC/字典/工作流）；仓库已存在，是 c1ayooo/templates
  4zwebshell     webshell 会话与后渗透通道（已存在）
  4zreport       报告渲染（findings JSON in、HTML/PDF out，可选；仓库内尚无对应实现，见 §3）

引擎层
  4zengine       cyber-harness fork（仓库名不改，4zengine 是逻辑名；基线分支 tianwu/fixes）；链接上面的扩展，出 scan；Hub 与 scan 的装配都编进外壳（见下方「产物与运行模式」）
    exts/4z*     载荷契约包、4zevidence、4zfence、4zsession、装配包：先放引擎仓的新增目录，边界立住后再判断是否成仓（§3）

组装层
  4zrcin         外壳：kernel + agent + fleet + domain + edge；不拆
```

依赖方向严格单向：

```
4zrcin ──► 4zengine ──► 4zwebshell / 4zfence ──► 引擎契约（见红线 3）
   │            │
   ├─► 4zweb（构建期产物，非代码依赖）
   ├─► 4ztemplates（构建期数据，非代码依赖）
   │            （外壳的 go.mod 镜像了引擎的 29 条 fork replace：CI 在 GOWORK=off 下构建，replace 不会穿过模块的 replace）
   ├─► 4zwebshell/webshell（库；本仓 6 处 import，含 zrcin-node）
   ├─► 4zgears（sdk 的 fork，`c1ayooo/4zgears`，模块路径仍是 `github.com/chainreactors/sdk`；引擎与外壳的 go.mod 各有一组 replace 钉它的提交，换版本号要两边一起换）
   └─► rem fork（replace => ../rem；cmd/rem-relay 与 -tags remtunnel 依赖它）
```

扩展永不 import 外壳；积木之间不互相 import，只通过引擎契约或数据文件交接。

两条补充：

- 默认构建直接引用引擎的协议包；Hub 与节点装配只在 `-tags inprochost` 构建里编进外壳。
- rem 不算积木，但它已经是版本矩阵的一部分；份数不含它。4zgears 同理：它进引擎的依赖，外壳的 `go.mod` 镜像那几行 replace，份数也不含它。

每块积木一张卡，写清它怎么接、怎么摘：

| 积木 | 形态 | 装配点 | 契约 | 摘除方式 |
| --- | --- | --- | --- | --- |
| 4zweb | 静态产物 | 构建期编进外壳（flate bundle）；开发期 `--web-dir` | 外壳的 HTTP API | 不装：`-tags noconsole` |
| 4ztemplates | 规则数据 | 引擎的生成器从规则仓检出读入，产出 `.bin` 进 `tools/resources` 与 `tools/proton/resources` | 资源 key，共 19 个，由引擎的生成器定义 | 不摘：规则是编进 scan 的数据；要换规则就重跑生成（`make resources`）再重编 |
| 4zwebshell | Go 模块（库 + 扩展） | 外壳 import 库；引擎装配时链接扩展 | `core/extension`、`core/tool`、`aop`，加控制面 HTTP | 装配里去掉链接 |
| 载荷契约包 | Go 包，纯类型与编解码 | 外壳和引擎各自 import | 7 个请求载荷、Artifact、TrafficVerdict 的 JSON 形状 | 不能摘：它是两边的契约 |
| 4zevidence | 引擎扩展 | 引擎装配时链接，一行 | 订阅 `process.*` 与 `file.access` 钩子，自发带 sidecar 的事件 | 装配里去掉链接 |
| 4zfence | 引擎扩展 | 引擎装配时链接，一行 | `tool.before`；代理的请求准入接缝；上游的审批队列 | 装配里去掉链接 |
| 4zsession | 引擎扩展 | 引擎装配时链接，一行 | 请求装饰贡献点；`RunInput` 的加性字段 | 装配里去掉链接 |
| 4zrules | 引擎扩展 | 引擎装配时链接，扫描器之后一行 | `Use[*engine.Set]` 借运行中的引擎集；`tool.started` 与 `tool.completed` 两个上游钩子点；`rules` 配置节（`CYBER_RULES_DIR`） | 装配里去掉链接 |
| 装配包 | 引擎里的可导入包（`pkg/assembly`） | `cmd/cyber-scan` 与 `zrcin node` 各一行调用 `assembly.Exec`；围栏、会话载荷、引擎配方都在它里面装配 | `extension.Extension` 列表 | 换回上游的装配 |
| rem | Go 模块（隧道库的 fork） | 外壳 `-tags remtunnel`；`cmd/rem-relay` | rem 协议 | 不带标签编译 |
| 4zengine | 引擎 fork | 发布只出 `zrcin` 与 `scan` 两个产物：Hub 和 scan 的装配都编进外壳（`-tags inprochost`），`scan` 另外单独编出给远程节点（已决 28、31） | `aop`、`pkg/web/hubapp`、`pkg/harness` | — |

**产物与运行模式**（2026-11-28 定，已决 28–32）。发布只有两个产物。

| 产物 | 给谁用 | 内容 | 体积 |
| --- | --- | --- | --- |
| `zrcin` | 本地使用 | 控制面、内嵌 Hub、scan 的全部能力 | 约 210MB（实测） |
| `scan` | 远程节点 | 与 `zrcin` 里的节点装配是同一份代码，单独编出 | 约 177MB |

配套件照旧：`zrcin-node` 启动器（约 13MB）、`rem-relay` 中继，算分布式模式的配套件，不算产品二进制。不再产出两个：独立的 Hub（`cyber-harness`）只按需构建，供调试；`cyber-scan-capture` 改为 `scan` 的一种运行模式。

| 模式 | 怎么跑 |
| --- | --- |
| 本地（默认） | 一个文件。`zrcin` 在进程内起 Hub，再用自己的可执行文件起一个节点进程（`zrcin node …`）；节点经回环 WebSocket 向 Hub 注册。Hub 看到的仍是一个连上来的 scan 节点 |
| 分布式 | 同一个 `zrcin`。远程机器部署 `scan`，或由启动器接入；节点经隧道、中继或直连接入 Hub。控制面不需要为分布式单独构建 |
| 纯控制面 | `zrcin --no-local-node`：不起本地节点，全部派给远程节点 |
| 逃生与调试 | `--harness exec` 让 Hub 走外部子进程；`--scan <路径>` 指定别处的节点二进制，比如旧版本的 scan |

本地模式选「自执行」（一个文件、两个进程），不选把节点跑成进程内的协程：后者节点崩溃会带走控制面，节点也不能单独重启。通信方式不变，现有的节点监督、日志收集、进程组守卫全部沿用。实施见 P6。

装配原则：装配尽量在自己的模块里完成。必须在引擎里链接的，每个积木一行，登记为钩子。

## 3. 积木判定标准

一个东西能不能拆成独立仓，只看三条：

1. **边界是数据** — 输入输出都能用 JSON/YAML/二进制描述，双方不共享内存 → 能拆
2. **构建链不同** — Node 与 Go 混在一仓，CI 必然长补丁 → 必须拆
3. **共享事务或内存状态** — 拆开就要分布式事务或每日对账 → 不能拆

三条是判断仓库归属的唯一标准。第一成功标准是引擎 fork 回到「上游 + 纯新增」（§1），所以积木先做成引擎仓里一行装配的扩展，仓要不要分开等边界立住后逐个判断：`4zweb` 已拆，`4zwebshell` 已是独立模块，`4ztemplates` 的仓库已存在；`4zreport` 不命中，仓库里没有 HTML/PDF 渲染。一次只多一个，不预先封顶。

外壳内部的模块不要求同仓：边界是数据、且不共享事务或内存状态的可以各自成仓；共享 SQLite 事务边界的（账本层与决策层之间）留在同一个仓。成熟一个拆一个。

## 4. 外壳内部：可判定的层级

外壳的内部包不搬仓，先让越界能被机器判定。规则只有一条：只能 import 同层或更低的层。

| 层 | 包 |
| --- | --- |
| 类型 | `model` `collab` `contract` `istate` |
| 协议 | `aop` `ws` `connectretry` `ioa` |
| 执行通道 | `executor` `aopexec` `enroll` `noderes` `platform` `tunnel` `relay` `deploy` |
| 账本 | `store` `ledger` `state` `project` `artifact` `evidence` `access` |
| 领域 | `board` `retrieval` `bundle` `traffic` `metrics` `report` `assets` `kb` `rules` `skills` `vulnlib` `resource` |
| 决策 | `decider` `verifier` `lifecycle` `modelclient` |
| 编排 | `host` `node` `fleet` |
| 入口 | `api` `web` `settings` `account` `notify` |
| 支撑 | `buildinfo` `testutil` |

层号写死在 `internal/architecture` 的测试里，越界即失败；上表与那个测试的 `layerTable` 是同一张表，改一处要改另一处。测试照引擎 `pkg/internal/architecture` 的写法。目录按层归并只是移动加改 import，做不做、什么时候做见 P5。

两件不在这次范围里的事，先记在这里：`contract` 只有十几行，应扩成真正的接口定义，写清哪些操作对账本可写、哪些只读；`internal/api` 一个包两万多行，一个 `Server` 上有几百个方法，拆成按资源的子包是一次真正的重构。边界先由层级表守住。

## 5. 模板与规则：4ztemplates

仓库已经存在：`c1ayooo/templates`，上游是 `chainreactors/templates`。4ztemplates 只是它在本文档里的逻辑名，仓库名不改。

### 5.1 仓库现状（2026-11-27 实测）

- **现在是一面镜子。** 与上游共有的 6 个分支（`master`、`dev`、`detached`、`detached2`、`found-v0.3.2-filters`、`spray-v1.3.1-template`）领先 0、落后 0，自有规则为零。
- **`feat/add-extract` 不是自有内容。** 它是上游作者 5 月 31 日的提交，36 个文件后来全部进了上游的 `spray/proton/`，fork 时被一起带了过来。
- **引擎通过子模块用它。** `tools/templates` 从 fork `c1ayooo/templates` 克隆，跟踪维护分支 `4z/rules`（2026-11-28 起；此前指向上游），钉在维护分支的尖端 `73d28d7`（即上游 `master`；2026-11-28 从 `cc5dd3f6` 升上来，编进引擎的只有 proton 的三个数据文件变了）；上游 `master` 比它多 11 个提交，动的是 `found/`、`services/`、`proton_rules/`（迁到 `spray/proton/`）和 `zombie/`，没有碰 `fingers/`、`neutron/`、`port.yaml`、`workflows.yaml`、`extract.yaml`。
- **仓库只有数据和它自带的生成器。** `master` 上 4,848 个文件：`fingers/` 4,405、`found/` 199、`neutron/` 83、`services/` 75、`spray/` 63、`zombie/` 15，外加 `port.yaml`、`workflows.yaml`、`extract.yaml` 和 `templates_gen.go`。没有 `go.mod`。

规则的所有权已经拿回来：维护分支 `4z/rules` 开在 fork 上，等于上游 `master`，引擎的子模块从它克隆。分支上还没有一条我们自己的规则——加第一条指纹或 POC 时，按 §5.3 的目录规则放上去。

### 5.2 生成链路（已核实）

**生成器有两份，不是同一份。** 规则仓自带的那份（381 行）默认 legacy 模式、遍历不排序、产物带 `!emptytemplates`。引擎用的是自己维护的那份：`tools/resources/templates_gen.go`，上游引擎的文件，fork 只加了 4 行；它恒为 embed 模式、遍历排序所以产物可复现、默认 `-need cyber`。给引擎用的只能是这一份。规则仓 README 里的 `-need gogo/spray/zombie/found` 分组是给上游那几个命令行工具用的，与引擎无关。

**引擎有两个资源包，都由它生成。** `tools/resources` 15 个 key（`http`、`socket`、`fingerprinthub_web`、`fingerprinthub_service`、`port`、`extract`、`workflow`、`neutron`、`spray_rule`、`spray_dict`、`spray_common`、`zombie_common`、`zombie_default`、`zombie_rule`、`zombie_template`，共 472K）；`tools/proton/resources` 4 个 key（`found_keys`、`found_spray`、`found_filter_ext`、`found_filter_dir`）。入口是 `go generate ./tools/resources ./tools/proton/resources`，引擎 Makefile 里对应 `embed-resources`。

**key 的含义由引擎的生成器定义，有三条会限制自有规则放哪：**

- 指纹的 `tag` 取父目录名：`fingers/http/<分类>/x.yaml` 的 tag 是 `<分类>`
- 字典和变换规则以文件名为 key，同名文件静默覆盖
- 没有任何 key 读新的顶层目录；`fingerprinthub_web` 和 `fingerprinthub_service` 是写死的空列表

**链路可复现。** 在引擎仓里对着钉住的版本重跑，19 个 `.bin` 和两个包的 `template.go` 与已提交的逐字节一致：

```bash
cd cyber-harness/tools/resources
go run templates_gen.go -t <规则仓检出> -o /tmp/out/template.go                 # 15 个 key
go run templates_gen.go -t <规则仓检出> -o /tmp/out2/template.go \
    -need found_keys,found_spray,found_filter_ext,found_filter_dir            # proton 的 4 个 key
```

**升到上游 `master` 的代价已经量化。** 从上游 `master` 重生成，19 个文件里 16 个逐字节一致，只有 proton 的 3 个变了：`found_keys.bin` 从 15,641 字节变成 16,823，`found_spray.bin` 从 3,605 变成 3,606，`found_filter_dir.bin` 从 703 变成 1,272。升规则只需回归 proton 2026-11-28 已升：三个 proton 数据文件的大小与量的一致，其余 16 个逐字节不变；引擎 CI 用的 scanner 功能回归与 proton 各包测试通过。

生成命令必须两个包一起跑（`make resources` 已这么做），否则 `tools/proton/resources` 的产物会漏更新。

**引擎自带一条运行时下发通道。** `--cyberhub-url`、`--cyberhub-key`、`--cyberhub-mode`（配置键 `cyberhub.url`、`cyberhub.key`、`cyberhub.mode`），带 24 小时磁盘缓存，可以把远端的指纹和 neutron 模板与内嵌的合并，或整体替换。它只覆盖这两类，仓库里也没有服务端。

### 5.3 目标形态

- **不新建仓。** 维护分支是 `c1ayooo/templates` 的 `4z/rules`，等于上游 `master` 加自有文件，做法与引擎的 `tianwu/fixes` 相同；fork 的 `master` 保持是上游的镜像。每月把上游 `master` 合进来，只在我们也改过的文件上冲突。引擎的子模块 `.gitmodules` 指向 fork 并写明这条分支，钉住的提交不动。
- **自有规则按上游的目录结构放，不另开子目录。** 新指纹放 `fingers/http/<新分类>/`，tag 就是分类名；新 POC 放 `neutron/<新产品>/`；新字典放 `spray/dict/<新文件>`。不用 `4z/` 子目录：它在 git 层零冲突，但会让指纹的 tag 变成 `4z`、让同名字典互相覆盖。`port.yaml`、`workflows.yaml`、`extract.yaml` 是单文件，自有改动必与上游冲突，能不改就不改。
- **生成留在引擎侧：引擎读规则仓，不是规则仓往引擎里写。** 引擎的 `make resources`（已落地，检出路径用 `TEMPLATES_DIR=` 指定）跑上面两个包的 `go generate`，再用 `git diff --exit-code` 比对产物。重生成的 `template.go` 与 `data/*.bin` 是生成物，替换它们不算改上游源码，红线允许。
- **零改动的验收条件**：重跑后产物零差异。
- **内置规则仍编在二进制里，实例规则在运行时进节点。** `internal/enroll/dist.go` 分发的产物是 scan，内置规则编在里面；改内置规则等于重跑生成、重编 scan、重新分发节点。本实例自己加的指纹与 POC 不走这条路：节点上的 `exts/4zrules` 盯着规则目录，变了就往运行中的引擎里加，本地节点的下一次工具调用就用上（docs/20，已决 38）。字典、zombie 规则、端口表仍然只有编译期一条路。

### 5.4 本仓侧的约束（已核实）

- **资源包不裁剪。** capture 不再单独出二进制之后（已决 29），`emptyresources` 的空孪生与构建标签一并删除；两个 `template.go` 回到与上游逐字节一致，任何运行模式下规则都编在 `scan` 里。
- **围栏保底审计语料另有归属。** `tools/resources/guardrail_builtin.yaml` 单源在 harness，是 docs/04 §6 的既定决策；它不是扫描规则，红线 5 相应收窄。已决：留在引擎，不随 4ztemplates 走。

## 6. 前端：4zweb 双模式

前端在独立仓 `c1ayooo/4zweb`，内容就是原来的 `web/`，已随 P1-3 拆出。开发与生产走两条路径，互不影响。

- **开发态**：外壳带 `--web-dir <path>` 时直接从目录读静态文件（`internal/web/dirsource.go`），缓存头与 Content-Type 跟内嵌路径共用一套。
- **生产态**：`make web` 在 4zweb 检出里跑 `pnpm build`（检出默认在同父目录的 `4zweb`，可用 `WEB_DIR=` 改），再由 `internal/web/generate` 压成 flate bundle 编进外壳。产物以 bundle 入库，部署方不需要额外拷贝静态文件；`-tags noconsole` 时不带控制台。
- **版本对齐**：bundle 记录 4zweb 的来源哈希与提交，`/api/version` 返回 `console_hash` 与 `console_commit`（已决 6）。

```bash
cd ~/4zweb && pnpm dev                                    # Vite :5173，/api 代理到 :8080
./bin/zrcin --web-dir ~/4zweb/dist --addr 127.0.0.1:8080  # 验证内嵌路径：先 pnpm build
```

gate 检测到 4zweb 检出就跑 typecheck、test、build 并比对嵌入产物，没有检出就记 skip（已决 3）；这三步正式归 4zweb 自己的 CI。本仓 gate 仍需要 Node 跑 `scripts/check-docs.mjs`。

## 7. 构建收敛：引擎与外壳的 Makefile，加 4zweb 的 CI

```
4zengine/Makefile
  make scan          scan，给远程节点（约 177MB）；与 zrcin 内嵌的节点装配同源（P6）
  make engine        cyber-harness——不进发布包，只按需构建，供 --harness exec 调试
  make proto-gen     协议生成（现有：跑 cmd/gen，protoc 与插件版本钉在 cmd/gen/main.go）
  make ldflags       版本注入（现有，被上面引用）
  make resources     跑两个资源包的 go generate，再比对产物（现有）
  make check-architecture / check-fork   架构门禁；红线 1 的机器检查（现有，后者加 STRICT=1 才强制）

4ztemplates（c1ayooo/templates，不设 Makefile）
  维护分支加每月同步上游；生成与校验都在引擎侧

4zweb               （pnpm，不设 Makefile）
  CI 跑 typecheck / test / build；本仓 gate 检测到检出时也跑这三步并比对嵌入产物（已决 3）

4zrcin/Makefile
  make gate          本仓门禁（现有）
  make web           构建并同步 4zweb 产物（现有；`WEB_DIR=` 同时交给构建和生成器）
  make test          全量测试（现有）
  make zrcin         外壳（含 4zweb 产物、内嵌 Hub 与 scan 节点装配）——现有
  make test-inprochost  内嵌构建的单元测试（现有；没有引擎检出时如实跳过）
  make node          隧道节点启动器（-tags remtunnel）（现有）
  make rem-relay     反向连接公网侧的二进制（现有）
  make release       goreleaser——暂缓；仓库里没有 .goreleaser 配置
```

引擎 Makefile 现有 `engine`、`scan`、`resources`、`check-architecture`、`check-fork`，`proto-gen`、`ldflags` 同名存在；`scan-capture` 目标已随已决 29 删除。

现有目标里，`manifest`、`repair`、`radar`、`run`、`race`、`fmt`、`clean` 保留。`harness` 是薄转发（调用引擎 Makefile），按需的调试构建，不在 `4zrcin` 里拼构建标签。gate 的 harness 构建步和构建清单步（已决 4 里写作第 8、10 步）留在本仓：前者只构建 `scan`（独立 Hub 不进发布包，已决 28），后者是「一次构建全部产物」的记录。

一处现存的不一致要在 P2 一并理顺：`cmd/rem-relay` 没有构建标签，`go build ./...` 就会编它，所以本仓的默认构建已经离不开 rem 检出；而 gate 里的注释假设默认构建图不含 rem。

引擎由 `4zengine` 自己构建，`4zrcin` 只 `require + replace` 拿版本，不去引擎仓跑 make。P6 之后 `zrcin` 带着节点装配，改外壳也会重链接这部分；`scan` 作为独立产物仍由引擎出。注意 inprochost 构建会把引擎的包编进 zrcin，所以「拿版本」的同时要把引擎的依赖 replace 一并对齐（见 P2：外壳的 `go.mod` 镜像了引擎的 replace）。独立 Hub 的条目已随 P6 退役。

上游 Makefile（229 行、26 目标）只作参考：它的「能力 = 构建标签组合」组织方式值得照搬，文件不共享，上游改了不 merge，人工看有无值得吸收的新配方。

## 8. 量化现状（引擎 fork 相对 upstream/master）

数字以引擎仓的机器检查为准，不在本文档里抄：

```bash
cd <引擎仓>
make check-fork                                # 逐文件列出新增行、是否登记、是否超预算
make check-fork STRICT=1                       # 未登记或超预算即失败
git diff --shortstat upstream/master...HEAD
```

口径必须用三点（merge-base）。两点 diff 会把 upstream 的新提交算进来；出现「删除」本身就说明口径不对。
唯一登记在册的删除是整文件搬迁：`check-fork` 把它们单列为 `moved` 类，要求上游路径在 `FORK.md` 登记（P6 把 `cmd/cyber-scan` 的 50 个上游文件挪进了 `pkg/assembly`）。重生成出来的数据文件（`tools/*/resources/data/*.bin`）另列一类 `generated`：登记、不计预算（§5.3）。这一类之外的「删除」仍然说明口径不对。

## 9. 五个杠杆

杠杆一（fork 不改 module path）、杠杆二（判定逻辑外移到 `exts/4z*`）、杠杆三（协议归一）已落地。它们约束的几件事写在红线里（§10）：`go.mod` 的 module 声明不改，上游文件只留登记的钩子，协议只有一份。

### 杠杆四：控制台归一

生产构建不编引擎 web；harness 的 web 仅在本地调试用 `full` 标签编译，不进分发包。`4zweb` 是唯一控制台。

已落地（P6）：外壳 Makefile 的 `TAGS` 不再带 `full`，gate 只构建 `scan`。内嵌 Hub 从来不带 `full`——`zrcin` 的构建标签只有 `inprochost`——一直正常运行，不必再实测。

### 杠杆五：规则源自主

已落地（2026-11-28）：`c1ayooo/templates` 上开了维护分支 `4z/rules`，引擎的子模块改从它克隆，`make resources` 对着子模块零差异。分支上还没有自有规则。钉住的提交当天升到了分支尖端，代价与 §5.2 量的一致。以后每次升级都照此：子模块移到分支尖端、`make resources`、提交它报告的数据、跑 proton 与 scanner 回归。约束与目录规则见 §5。

## 10. 红线

1. **fork 只增不改，钩子级** — 新增文件/目录允许；上游已有文件只允许登记在册的挂钩，每处不超过 20 行；`go.mod`、`Makefile`、`.goreleaser.yml` 的改动逐条记账。每条补丁在 `FORK.md` 里记处置、理由、回归测试、回滚，验收是 §1 的机器检查。现状：超预算的文件与原因登记在引擎仓 `FORK.md`，机器检查是 `make check-fork`（已决 24、27）。
2. **一个扩展一个模块**，先同仓不同目录，成仓与否按 §3 判断；外壳的 `go.mod` 镜像引擎的 fork replace，`go.work` 只是本地开发的可选便利（未建）；扩展只 import 引擎契约与标准库，**永不 import 4zrcin**。装配尽量在自己的模块里完成；必须在引擎里链接的，每个积木一行
3. **扩展只依赖引擎契约** — `aop`（协议），以及 `core/tool`、`core/extension` 这类注册接口；不 import 引擎的业务包（`agent/`、`tools/*`、`pkg/web/*` 等），也不形成包级 import 环。**登记在册的例外（已决 23）**：接缝所在的包——接口方法按值传结构体时，实现方必须 import 定义它们的包才能不写适配器；当前的例外是 `tools/proxy` 的请求准入接缝与 `tools/scan/engine` 的引擎集（已决 38），另有过先例的 `tools/resources`（已决 2）。上游通用包反过来 import 积木仍然禁止。引擎的装配必须链接扩展，模块层面的互相 require 躲不掉，靠本地 replace 或 `go.work` 解决（已决）
4. **协议只有一份，只增不改** — 来自引擎 `aop/`，任何仓都不复制 pb；fork 加的协议内容只落在新增的 proto 文件或 anypb sidecar，不改上游消息
5. **内置规则的源只有一份**，在 `c1ayooo/templates` 的维护分支；引擎从中生成，节点拿编入规则的二进制。实例规则是第二层，归本实例的数据目录，节点在运行时加载（docs/20，已决 38）。围栏保底审计语料单源在引擎（docs/04 §6，已决）。

## 11. 七阶段（P0–P6）

### P0 · fork 回退 module path（已完成，2026-11-27）

fork 的 `go.mod` 保持 `module github.com/chainreactors/cyber`，上层通过 replace 指向 fork。与上游合并的结果登记在引擎仓 `FORK.md` 的「Upstream merge 2026-11-27」。细节在 git 历史里。

### P1 · 扩展外移（已完成）

六步都已落地并推送，按「不依赖接缝的先做」排的：

- **P1-0 缺陷修复**：围栏静默失效的两个修法（已决 18）。
- **P1-1 载荷契约包**：`aop/payloads`，7 个请求载荷加 Artifact、TrafficVerdict，两边 import 同一份。
- **P1-2 证据与继承**：P1-2a/b 是 4zevidence 独立成扩展、`exts/observe` 恢复上游；P1-2c 是子会话继承改用 `SessionStart`。
- **P1-3 `4zweb` 拆仓**（§6）。
- **P1-4 三个接缝与两块积木**，分三小步：P1-4 是 G1、G2、G3 按钩子落地（附录 A）和两块积木；P1-4b 把工具调用的判定搬进 4zfence；P1-4c 把代理请求的判定搬进 4zfence。4zfence 是唯一的判定方，上游 guardrail 只当审批队列；4zsession 收回合载荷；围栏配置节迁到 `fence`（已决 15、19、20）；等价性门禁 16 条判定一致。
- **P1-5 装配包与架构测试**：`pkg/assembly` 收配方孪生、`CYBER_EXCLUDE_TOOLS`、MCP 接线等触点与围栏装配；外壳的层级表架构测试（§4）。

### P2 · 协议归一 + 扩展独立成仓（已完成）

已落地：

- **协议归一**：7 个 vendored 协议包、同步脚本与 gate 的漂移检查全部删除，import 直指引擎包；四处手写 protowire 换成引擎生成类型。公共配置改为 import 引擎的 `pkg/config` 校验（已决 25）。
- **replace 对齐**（P6 的前提）：外壳主模块镜像了引擎的 29 条 fork replace，并钉住与引擎同一提交的 genproto 拆分模块。replace 不会穿过模块的 replace，而 CI 在 `GOWORK=off` 下构建、`go.work` 里的 replace 不生效，所以落点必须是外壳的 `go.mod`。引擎改 fork replace 时外壳要跟，gate 的内嵌构建步骤会在失配时编译失败。`go.work` 只是本地开发的可选便利，没有建；`4zwebshell` 已能独立 `go build`。
- **产品配置节的校验随积木走**（已决 20）：围栏、traffic、MCP 三节的规则只在引擎一份，外壳保存时调引擎的。`search.tavily_keys` 的形状检查与 `llm` 节的读取留在外壳：引擎对前者没有校验，对后者的类型化读取会要求控制面注册整棵扩展树。默认构建因此多链接 22 个包，带 `inprochost` 的构建不变。
- **传输层不再手写**：RFC 6555 的实现（`internal/ws`，425 行）换成 gorilla/websocket——引擎 `pkg/aopws` 的底座——之上约 200 行的薄适配，留着外壳特有的三样：文本与二进制帧的区分、可解到 `io.EOF` 的关闭错误、串行写加超时。管理类 Connect 调用七个里六个改走引擎 `pkg/rpc` 的生成客户端；`ListAgents` 是最后一个换的，它带着宿主拨号超时的重试（就绪判定靠它），重试循环原样保留；`TestLLM` 保留手写，因为控制台依赖的那份容错解码（延迟写成 `"1608ms"` 也不报错）protojson 表达不了。外壳的 DTO 与对外的 JSON 字段名一个没动。
- **引擎的 `pkg/aopws` 与 `pkg/aopconn` 没有采用**：外壳的终端等待要读超时、坏帧要跳过、断连要三分归因，这两个包都不透出；`aopconn.Connection` 是服务端形状的一次性分发循环，与外壳按 `reply_to` 交错关联、事件通道加游标跟踪不是一层。这是如实记录的偏离，不是遗漏。
- **依赖代价**：默认构建多链接 5 个包（gorilla 1 个、connect-go 4 个）；带 `inprochost` 的构建不变。

验收：外壳 gate 全过；`internal/aop` 留着的是产品特有的一层（连接重试、应答关联、事件通道与游标跟踪、终端打开重试、回合载荷的组装顺序），现有测试的断言一条没改。


### P3 · 单进程内嵌（已完成）

**已落地，不需要再做**（docs/03 M1、docs/04 T2）：外壳在 `-tags inprochost` 下通过 `internal/host/inproc` 把 Hub 跑在进程内，组装点是引擎的 `pkg/web/hubapp`（fork 新增的包；`webhost.Config` 多一个可选的 `Listener`，是登记在册的钩子）；`--harness` 显式给出时回到子进程模式，作为逃生舱；scan 节点仍是子进程。

- 现状验收已达成：`bin/zrcin` 单文件起完整实例（docs/04 §3.1）
- 可选后续：给 Hub 一个不解析命令行参数的入口（现在内嵌走的是与 `cyber-harness` 命令相同的 argv）；验证进程内 Stop/Restart，为将来拆进程留口子
- 本地只带一个文件已由 P6 落地：节点仍由外壳当子进程拉起，二进制换成 `zrcin` 自己，不再需要 `cyber-scan` 这个文件
- 端口不会只有一个：console（8080）、Hub 回环监听（`--host-addr`）、可选的 relay 与 Postgres 各自独立

拆进程（保留为备选，触发条件：需独立发布引擎二进制 / 引擎多副本 / 崩溃隔离 / 各自热重启）：去内嵌、引擎独立进程、外壳走 AOP WebSocket，处理启动顺序与数据目录归属。

### P4 · 构建收敛 + 积木落地（已完成）

- `4zweb` 的拆仓和构建脚本都已完成（§6）
- `4ztemplates`：不新建仓。引擎的 `make resources` 已落地，两个资源包一起生成并比对；维护分支 `4z/rules` 已开，子模块已指过去（§5）
- 构建目标已齐（§7）；gate 的 harness 构建步和构建清单里与独立 Hub 有关的条目在 P6 退役
- 验收：上游 `git pull` 后只跑 `make`，不动任何源码；`make zrcin` 产出单二进制；前端改动不触发 Go 重编（开发态）；改内置规则只重跑 `make resources`，再重编并分发 scan；本实例的规则不重编（docs/20）

### P5 · 外壳做成真模块（已决：不做，2026-11-28）

P5 里强制的那一件早做完了：层级表的架构测试在 P1-5 落地（§4），越界即失败。剩下两件 r10 逐包查过后定如下（已决 37）：

- **搬仓：不做。** 外壳 `internal/` 的 50 个包逐个过了 §3 的三条标准，没有一个的收益抵得过立仓的成本；账本簇 22 个包共享 SQLite 事务，按标准本就该留在同一个仓。唯一形态上干净的候选是 `internal/ioa`，出现第二个仓外消费者时再议。
- **目录按层归并：缓行。** 等 `feat/rules-hot-reload` 合并之后再评估——现在做会撞上它 29 个未提交的文件，其中包括定义层级表的那个测试。这一条是本计划里唯一挂着时点的后续工作。

### P6 · 本地单文件：节点自执行（已完成，2026-11-28）

目标是本地模式只有一个文件。做法是一个文件、两个进程：`zrcin` 带上 scan 的全部能力，启动时用自己的可执行文件再起一个节点进程。产物与四种运行模式见 §2。三步各自独立落地：

1. **引擎：scan 的完整装配挪进可导入的包。** `cmd/cyber-scan` 的 60 个文件整体搬进 `pkg/assembly`（纯搬迁，文件名不变），对外只有 `assembly.Exec`；`cmd/cyber-scan` 只剩进程壳。`check-fork` 多了一类 `moved`，登记这 50 个上游文件的新去处，合并上游时 git 跟着重命名走。这一步之后没有上游文件超预算，`make check-fork STRICT=1` 通过。
2. **外壳：`zrcin node` 与自执行。** `main()` 最前面分派 `zrcin node …`，它调 `assembly.Exec`，行为与 `cyber-scan …` 一致；内嵌构建下 `NodeBin` 默认是自己的可执行文件，参数前加 `node`。`--scan <路径>` 与 `--harness exec` 是逃生与调试口；`--no-local-node` 只起 Hub。
3. **capture 改运行模式，独立 Hub 退役。** `scan --capture-only` 不装配扫描引擎并自报 `scan-capture`，`captureonly` 与 `emptyresources` 两组构建标签的孪生文件删除。gate 只构建 `scan`，清单里独立 Hub 与 capture 的条目退役；启动器给 capture 接入带上开关。

前提是外壳的 `go.mod` 镜像引擎的 fork replace（见 P2）：不带节点装配时不需要，带上以后缺一条就编不过。

验收（`scripts/acceptance/single-file.sh`，无模型、无网络，隔离于运行中的实例）：一个文件起完整实例；杀掉节点进程后自动恢复；`--no-local-node` 下 Hub 就绪而节点列表里没有本地节点；独立的 `scan` 与 `zrcin node` 向 Hub 注册的 29 条能力与工具清单一致。gate 多了一步，给带 `inprochost` 标签的代码跑单元测试（此前的内嵌代码没有任何单元测试）。`zrcin` 实测 约 210MB。

**验收补全（r10，2026-11-28）**：「一次本地意图跑通，含一次扫描工具调用」由 `scripts/acceptance/local-turn.sh` 覆盖，不需要真模型：本地假模型服务（`fake-model.py`，只用 Python 标准库）先接住实例的启动探测，再用 SSE 回一次 `bash` 调用。脚本断言实例就绪且没有降级、目标经项目创建时的 `scope_rules` 进了围栏、run 事件里那次 `bash` 调用的结果带着目标应答的标记、第二个回合请求里带着 `role=tool` 的工具结果、意图以 done 收束。它证明链路与装配（模型连接、流式解析、工具派发、入栏、结果回传），不证明模型好坏。两个实测事实：启动探测是非流式调用；模型可见的 14 个工具里没有一级扫描工具，能对 HTTP 目标发起请求的是 `bash`。热构建约 8 秒，冷构建约 40 秒。两个验收脚本共用的脚手架在 `scripts/acceptance/lib.sh`。

**落地后查出的一处回归（已修）**：capture 不再单独出二进制之后，外壳的产物状态仍把 `scan_capture` 钉成 false，控制台的 capture 部署选项因此一直是灰的（`4zweb` 按 `scan_capture` 判断可不可选）。现在它跟随 `scan` 二进制；前端的两张配方卡也改成同一个构建命令，capture 的区别只在启动时加 `--capture-only`。同批修掉的还有 `go mod tidy` 跑不通：`scripts/acceptance` 的围栏基线顶着 `.go` 的名字，tidy 会去解析它引用的两个早已搬走的引擎包；基线改成 `.go.tmpl` 模板，由 `fence-parity.sh` 拷贝时再命名。

代价与注意：`zrcin` 从 69MB 涨到 约 210MB（已决 30）；上游改 scan 的装配时，fork 的装配包要跟一次，这是今天就在做的事，只是从上游文件挪到了 fork 自己的文件；只读挂载或禁止自执行的少数环境不适用，那时退回 `--scan <路径>`。

回滚：`--scan` 指向外部 scan、`make engine` 产出独立 Hub，即回到 P3 的形态。

### 收尾（r10，2026-11-28）

P6 之后剩下的事一次做完。每件都先把现状钉住再动：改行为的先写测试看它红，纯重构的先写特征测试。

- **两套回合级 absorb 合并。** `internal/aopexec` 与进程模式（audit 节点）各有一份从事件流取回合级事实的函数，合成 `internal/executor` 的 `Absorb` / `AbsorbJudgeUsage`，两条路径都调它。进程模式路径上可观察的变化只有两处，都是记账更准：逐次用量从「只取第一次」改成累加；有真实用量时 `UsageKnown` 为真。其余字段由特征测试逐个钉住，一个没变。
- **围栏的命令键绕过（新发现，已修）。** 围栏按精确的小写 `command` 取命令，而 bash 与 `webshell_exec` 用 `encoding/json` 解参数：键名大小写不敏感，同一个键后者覆盖前者。`{"Command":"rm -rf /"}` 因此会执行，却跳过全部实例红线，连保底审计都不产生；`{"command":"echo hi","Command":"rm -rf /"}` 围栏看到的是前一个。现在围栏按同一套规则取命令（`exts/4zfence/commandkey.go`），两条对照测试把它钉在 bash 的解码上：一张手写的载荷表，加每次约 1.9 万个随机生成的载荷（种子固定）。红线那条测试用真实规则走真实的 `admit`。
- **IOA 信封换引擎的 `core/collaboration`（已决 34）。** 手抄的 `internal/ioa/v1.go` 删除。观察器的判定随之变成引擎那份——九个历史上两边判得不一样的形状全部翻转（外壳更严的三个、引擎更严的六个）；JSONL 里空的 `source` / `target` 不再输出成 `{}`，旧文件仍可读；`SendEvent` / `SendResult` 改走 `Envelope.Meta()` 并在发出前先校验，空 chain 写成 `"chain":null` 的隐患一并消除。`core/collaboration` 本来就在 `cmd/zrcin` 的依赖闭包里，依赖净增量为 0。
- **模型客户端保留并瘦身（已决 35）。** `internal/modelclient` 的包注释还写着它是「给决策者用的客户端」，其实推理调用只剩设置页在宿主不在线时的兜底探针。`Chat`、`Complete`、`FromEnv` 等六个包外没人调用的符号，连同只有它们走得到的 JSON 模式分支一并删除；留下的行为补齐特征测试，三处与引擎有意的宽松各钉一条漂移测试（`drift_test.go`，注明引擎侧出处）。依赖包数不变。
- **前端产品卡保存不再带模型半边**（`4zweb`）。服务端自 `b5f3c5a` 起对请求里没带的模型字段保持原值，六处产品卡保存却仍把页面快照里的模型字段展开进请求体：两个标签页同时开着时，甲页的一次产品保存会把乙刚改的模型悄悄写回去。现在产品卡只发自己的字段，模型半边只由模型表单那一个入口写。
- **`go mod tidy` 恢复可用**，控制台的 capture 部署选项恢复可选（见 P6 的回归记录）。


## 12. 上游同步流程（解耦后）

```bash
# 引擎
cd ~/cyber-harness
git fetch upstream && git merge upstream/master   # 冲突应只出现在 FORK.md 登记的钩子文件与修复类补丁（目标状态，现状见 §8）
make scan                                          # 重编远程节点用的产物（独立 Hub 按需：make engine）

# 规则（每月一次）
cd ~/templates && git switch 4z/rules             # 维护分支
git fetch upstream && git merge upstream/master    # 只在我们也改过的文件上冲突
git push                                          # 引擎的子模块从这条分支克隆
cd ~/cyber-harness && git -C tools/templates fetch && git -C tools/templates checkout <分支尖端>
make resources                                     # 重生成并比对；有差异就提交产物、子模块指针、重编 scan

# 外壳
cd ~/4zrcin
make gate && make zrcin
```

## 13. 收益与代价

收益：

- 上游同步成本下降：冲突面从 110 个被改的上游文件，缩到登记在册的钩子和修复类补丁所在的文件，由 `make check-fork` 如实报数
- 能拿能换：每块积木一行装配，不写就不装
- 接错线当场报错：`Provide` 与 `Use` 在加载时检查，包级注册表那种静默失效不再有藏身之处
- 前端独立成仓：开发态改前端不重编 Go；生产态是内嵌，换前端仍要重链接 `zrcin`，部署方用 `--web-dir` 时才真正免重编
- 规则源回到自己手里：加指纹有源可改。内置规则编在 scan 里，改它仍要重编并分发节点；本实例的规则经控制台录入，节点在运行时加载（docs/20）
- 包名即职责，AI 改代码的越界可以机器判定，两个仓都有架构测试

代价：

- 份数增加后的版本矩阵，`go.work` 与 replace 要维护
- 构建从「一条命令」变「引擎、外壳两处 make，有顺序」
- 围栏的配置节换了名字；迁移在保存时自动完成，读写都认旧写法，但操作员会在文件里看到节名变了
- 修复类补丁和接缝长期以钩子形式存在（已决 27），上游合并时这些文件要手工合
- P6 落地后 `zrcin` 约 210MB（已决 30 接受约 200MB）
- 外壳的 `go.mod` 镜像引擎的 29 条 fork replace，引擎改 replace 时外壳要跟

做不到的：fork 的差异归零；改内置规则不重编（§5），改字典、zombie 规则、端口表不重编；远程节点读不到控制面的规则目录（docs/20）；外壳自身业务的模块化，拆 `api` 不在范围内（§4）；`4zreport`，仓库里没有实现，无从拆。

## 14. 风险、已决与实施约定

风险：

- **每次上游合并都会打断登记的钩子和修复类补丁。** #205 合入时是 23 个冲突文件。钩子压到 20 行以内之后，冲突面只剩登记在册的那些文件；合并后以 `make check-fork` 和全量测试验收
- **replace 不传递**：外壳主模块必须镜像引擎的 fork replace（P6 已对齐 29 条）。引擎改 replace 而外壳没跟时，带 `inprochost` 的构建会编译失败——gate 的第 8 步能发现，默认构建发现不了
- **历史里有真实凭证**：界面快照里的 `BENCHMARK_TOKEN` 与内网 VPN 信息已随一次提交推送到了公开仓。令牌要轮换，历史要重写，两件都等操作员。这些快照 YAML 已停止跟踪，但根目录那批还没有被 `.gitignore` 挡住，`git add -A` 会把它们重新带进去

P1-0 的三条缺陷（围栏静默失效、证据上限不一致、流量采集默认值不一致）都已修，记录在 git 历史。

已决（2026-11-27 起）。后面的决定会了结前面的：25 了结 21，27 了结 12，也把 10 里「钩子是过渡态」改成长期留存，38 了结 16 的后半句。

1. **4zweb 就是本仓的 `web/`**，整体搬出，不替换、不重写；提交历史带过去（`git subtree split`）。
2. **围栏保底审计语料留在引擎**。4zfence 暂不拆，外移没有落点。
3. **前端拆仓后的嵌入产物新鲜度**：本仓 gate 沿用 AOP 漂移检查的惯例——检测到 4zweb 检出就重建并比对，没有就记 skip；typecheck / test / build 归 4zweb 自己的 CI。
4. **gate 第 8、10 步留在本仓**：第 8 步改为调用引擎 Makefile 的目标，第 10 步的构建清单不动。
5. **红线 3 按「`aop` 加注册接口」表述**：禁止的是 import 引擎的业务包与包级 import 环。
6. **`/api/version` 同时返回 `console_hash` 与 `console_commit`**。
7. **入侵标准定为钩子级**：上游已有文件只留登记在册的挂钩，§1 的机器检查进引擎 gate。
8. **协议只增不改**：新内容落在 anypb sidecar。fork 现状已经满足，上游消息一个字段没改。
9. **外壳内部做成真模块，仓库归属逐个判断**：边界立住后，满足 §3 标准的模块可以各自成仓。
10. **终点是只用官方组合机制**（§1）。登记钩子是过渡态，每条登记出口。
11. **第一成功标准**是引擎 fork 回到「上游 + 纯新增 + 少量登记钩子」；独立发版和分仓排在其后。
12. **上游化是加分项，不是前提**：计划按钩子长期留存来定。先提纯修复类 PR（补丁 1、4、9，Any 套 Any，`ResolvePaths`，PTY 流 ID），合入一个就少一处；接缝类的等修复类有回音再提。
13. **小步推进，不设整体冻结**；只有围栏搬家那一段需要短冻结。
14. **拆解按行为逐项不变验收**；已知缺陷走上面的修复清单，不和搬家混在一起，围栏静默失效排最前。
15. **围栏的判定配置搬进自己的配置节**，上游 `guardrail` 节恢复原样。验收含「旧配置自动迁移，迁移前后判定一致」。
16. **规则源只做编译期接管**，「改规则不重编」不列入本次范围。后半句由已决 38 了结：实例规则可以在运行时进节点。
17. **`4zweb` 拆仓放在 P1-3**：载荷契约包和 4zevidence 之后、围栏搬家之前。它不碰引擎代码，不和围栏那段短冻结抢同一批人。
18. **围栏静默失效的两个修法都做**：会话关闭时清理；表满时拒绝回合，不让任何会话在没有围栏的情况下跑。
19. **围栏配置迁移**：读写两条路径都认旧写法；任何一次保存把它迁到新的配置节并记一条诊断；不设到期时间；不在启动读取时改操作员的文件。
20. **产品配置节的校验随积木走**：配置结构和校验放进不依赖引擎的包，节点的 `Declare` 和外壳共用一份。依据（已核实）：Hub 的配置接口不校验产品配置节——`pkg/web/host/config_store.go` 的 `SharedConfigCodec` 只暴露公共设置并转发网关配置，具体服务的 schema 归执行节点；外壳现在各抄一份（`internal/settings` 的 `guardrailSectionKeys`、`validateJEV`、`validateMCP` 等）。
21. **公共配置的归属这次不定**：P1 期间外壳继续自己校验公共配置，P2 协议归一时再定。两条路：外壳 import 引擎的 `pkg/config`，会带进 `agent/provider` 和 11 个第三方模块，而且不在红线 3 列的引擎契约里；或者让 Hub 接管配置编辑，那等于改配置文件的归属——外壳改的是用户级的 `~/.cyber/cyber.yaml`，Hub 的 `FileConfigStore` 改的是启动时 `--config` 指的那份运行时文档。
22. **终点的范围**：引擎里我们加的能力做成一行装配的扩展，共五块；外壳是宿主，规则是数据，前端是产物，引擎内核是框架本体，都不做成扩展。扩展是编译期静态组合的，不做运行时热插拔。
23. **红线 3 的接缝例外**：积木可以 import 登记在册的接缝所在的包。理由：`ScopeJudge` 的方法按值传 `ScopeRequest`/`ScopeDecision`，不 import 定义它们的包就得写一层转换适配器；把类型挪进 `core/` 又会把 MITM 库的响应类型带进 `core`。上游通用包反过来 import 积木（E3-1 的原问题）仍然禁止。
24. **E4 的口径**：上游测试文件（`*_test.go`）、文档与 golden 不计入每处 20 行的钩子预算，但仍须在 `FORK.md` 登记；`go.mod`、`go.sum` 逐条记账。机器检查（`scripts/check-fork.sh`）按此分类实现。
25. **公共配置归引擎**（了结已决 21）：外壳直接 import 引擎的 `pkg/config` 校验公共配置，删掉手抄的那份。依据是实测：协议归一之后默认构建已链接 `pkg/config`、`agent/provider` 与 `core/collaboration`，再 import 不多带任何包；已决 21 写的「会带进 11 个第三方模块」是归一之前的数字。
26. **编译产物清出历史**：`tw`、`tw-gate`、`tw-repair` 等曾长期被跟踪的编译产物，除了停止跟踪（108522b），还要从 git 历史里清除。在外壳仓工作的会话全部收工后执行，执行后所有分支与 worktree 重建到新历史上。
27. **不向上游提 PR**（了结已决 12 的「先提修复类 PR」）：操作员决定本 fork 不向上游提交补丁，修复类与接缝类都按长期留存处理。后果：修复类补丁和接缝长期留在 fork 里，不会因上游合入而减少。修复类补丁的 14 个文件已压到 20 行以内；装配根随 P6 挪进了 fork 自有的包，超预算为 0。
28. **发布只有两个产物**：`zrcin`（控制面、内嵌 Hub、scan 的全部能力）与 `scan`（远程节点）。独立的 Hub 不进发布包，只按需构建供调试；`--harness exec` 与 `--scan <路径>` 留作逃生与调试。启动器与 relay 算配套件，不算产品二进制。
29. **capture 不再单独编**：`cyber-scan-capture` 改为 `scan` 的一种运行模式。代价是这种节点多约 26MB；换来删掉一组构建标签的孪生文件。
30. **接受 `zrcin` 约 200MB**。本地模式的卖点是不用拖家带口，体积换的是这个；不做编译期裁剪。
31. **本地单文件走「自执行」**：一个文件、两个进程，`zrcin` 用自己的可执行文件起节点进程。不选进程内协程，因为节点崩溃会带走控制面，节点也不能单独重启。
32. **启动器与 relay 不强行合并**：`zrcin-node`（约 13MB）接入快；并进 scan 会让远程接入多下载约 160MB。发布包里写清：核心两个，分布式模式另带两个配套件。
33. **前端字段名保持外壳现有的**：外壳的 console API 是字段名的所有者，前端从不直接消费引擎的 protojson 名（API 边界重新包装过；事件载荷按内容嗅探，不依赖字段名）。唯一的类型差异是 int64 在 protojson 里写成字符串，`flexInt` 消化了它并有测试钉住。前提：前端不直连宿主的 Connect 端点；目前的架构里没有这条路。
34. **IOA 信封用引擎的 `core/collaboration`**：手抄的那份与引擎在九个形状上判得不一样，引擎那份才是执行端生效的判据。引擎包是只依赖标准库的叶子，外壳多链接的包数为 0。
35. **模型客户端保留，删掉没人调用的部分**：它的现实职责是「宿主不在线时的自检兜底，加端点规整」。与引擎 `agent/provider` 的三处差异是有意的宽松，各钉一条测试：裸地址补 `/v1`、未知的 provider 名按 openai 继续、地址以 `/messages` 结尾判 anthropic。全换成引擎的包会改变构建清单记模型与 `max_tokens` 上限表这两条兜底路的行为，不换。
36. **命令字段的判据**：围栏取命令与工具解码用同一套规则，修掉实证的绕过（见 §11 收尾）。结构性的改造——按各工具声明的 schema 取命令字段，或维护一张「会起 shell 的工具」名单——不做。重新触发的条件：某个命令工具不再用 `encoding/json` 解参数，或新增一个命令字段名；那时除了改键名表，必须同步扩对照测试的载荷表。
37. **P5 的两半都不做**：搬仓——外壳 `internal/` 的 50 个包逐个过了 §3 的三条标准，没有一个的收益抵得过立仓的成本；账本簇 22 个包共享 SQLite 事务，实例簇共享 `istate` 的句柄，按 §3 都该留在同一个仓。唯一形态上干净的候选是 `internal/ioa`，出现第二个仓外消费者时再议。目录归并——可选，不排期。原先等规则热加载那条线合并，它已经并入，现在没有阻塞它的了。
38. **规则分两层，实例规则热加载**（了结已决 16 的后半句）：内置规则的源只有规则仓，编进 scan；实例规则是数据目录里的文件，由节点上的 `exts/4zrules` 在运行时加进引擎，本地节点的下一次工具调用生效，不重启、不重编、不分发。新增、改写、删除都热生效；改写被拒绝时节点继续运行上一版并说明原因。它依赖 4zgears（sdk 的 fork）新增的两个移除方法，两个仓的 `go.mod` 把 sdk 钉在那个提交上。远程节点不在范围内。方案与实施见 docs/20。

实施约定：

- **目录范围**：七个阶段与 r10 收尾都已不占目录。代码侧没有排期中的工作；剩下的只有操作员手里的历史清理。
- **基线分支**：`tianwu/fixes`。本仓脚本固定从这里构建；`feat/remote-nodes` 已合入。
- **仓库名**：`cyber-harness` 与 `templates` 都不改名，`4zengine`、`4ztemplates` 只是本文档里的逻辑名。sdk 的 fork 叫 `4zgears`（`c1ayooo/4zgears`），模块路径仍是 `github.com/chainreactors/sdk`，上层靠 replace 指过去（P0 的做法）。
- **不动的文件**：`docs/18-webshell-management.md` 是 webshell 的文档，与本计划无关。
- **Hub 内嵌保留**：它解决的是单二进制，不是通信，AOP 替代不了它。不想保留这个钩子时，回到 Hub 独立进程——外壳的 `--harness` 路径现在就能用。

未定的：无。r10 之前的三条都已决（已决 34–36）。


## 附录 A · 接缝与已排除的做法

上游没有现成接口的地方，fork 用登记的钩子开接缝。扩展、钩子、AOP 命名空间、配置、组合根、协议的官方接口以上游开发者指南为准（引擎仓 `docs/developer/`），这里不复述。下表的编号供代码注释和 `FORK.md` 引用；每条接缝的形状与登记在引擎仓 `FORK.md`。

| 编号 | 缺什么 | 上游现状 | 现状 |
| --- | --- | --- | --- |
| G1 | 会话请求里扩展载荷的解析注册点 | 7 个载荷的解析写死，`OpenAOPSession` 不读 `extensions` | 已落地：请求装饰器，`OpenAOPSession` 与 `RunAOPTurn` 各留一个循环 |
| G2 | 代理的请求判定接缝 | 只有 `SetProxyAuth`，看不到会话归属，不能挂起 | 已落地：类型原生定义在 `tools/proxy`，围栏实现它（已决 23） |
| G3 | 复用围栏审批队列的公开入口 | `*Runtime` 没有「提交一条审批」的方法 | 已落地：`SubmitReview` |
| G4 | 按会话的工作目录、轮次种子、工具环境、评估目标 | `RunInput`、`SessionOptions` 没有对应字段 | 加性字段留在 `agent/session`，长期留存（已决 27） |
| G5 | 按工具名排除 | 只有按组开关、整次禁用、逐次调用的 `tool.before` | 不需要：属于装配 |
| G6 | 可导入的节点装配 | 装配全在 `cmd/cyber-scan` 的 `package main` 里 | 已落地（P6，已决 31）：scan 的完整装配在 fork 的 `pkg/assembly`，`Exec` 一个入口，`cmd/cyber-scan` 与 `zrcin node` 共用。已决 27 定了不向上游提 PR，长期留在 fork |
| G7 | 不受采集开关影响的 HTTP 流量观察 | 钩子在关闭采集或被过滤时直接跳过 | 未开 |
| G8 | 新的 provider 类型 | 构造写死 anthropic 和 openai | 未开 |

已排除的做法（查过源码，不必再试）：

- **给积木开自己的 AOP 命名空间来下发围栏规则。** `aop.Binding` 是官方机制，但 Hub 夹在控制面和节点之间：它的应用侧分发器只转发写死的命名空间（`pkg/web/service/application_dispatch.go:372-384`：core、command、guardrail、jev、file、scan、pty），其余回 `UNSUPPORTED_NAMESPACE`。每个被转发的命名空间各有一个 forward 函数，用的是 Hub 内部未导出的 `s.agents.dispatchMessage`，`service.Capability` 拿不到。要让 Hub 转发新命名空间，得改两个上游文件，入侵面不比 G1 小。sidecar 搭在上游请求上是唯一不改 Hub 就能穿过 Hub 的路径。
- **用 `SetProxyAuth` 当判定入口。** 它看不到会话归属，不能挂起等审批，只能以 407 拒绝。
- **让 4zfence 自带一套审批队列。** 官方机制够用，但会把「挂起、超时、事件、RPC、控制台绑定」复制一份，变成两个浅模块。G3 只是一个方法。

## 附录 B · 重复的轮子

协议归一之后，「外壳手抄引擎」的清单已基本清零，原表在 git 历史。仍手抄的几处列在 §14 的未定项里。

## 附录 C · 补丁处置表

fork 的每条补丁和每个被改的上游文件，逐路径登记在引擎仓 `FORK.md` 的「Local patches」。能搬的已经搬进 `exts/4z*` 或 `pkg/assembly`，剩下的是登记在册的钩子（已决 27）。补丁不再按编号登记；代码注释里的旧编号（如「patches 21/30/33」）对应当时的处置表，见 git 历史里的本附录。

## 附录 D · 拆出来的积木和装配点

积木卡在 §2；每块积木怎么接上引擎见 §11 的 P1、P6。

## 附录 E · 实施审查的关闭记录

2026-11-27 对已落地的实施做过一次两路审查（规范、规格），对象是引擎 `eca06a22` 与外壳 `401053c`。发现按 E1（测试与验收证据）、E2（必须处理）、E3（与设计相反的做法）、E4（钩子预算与登记）、E5（其余规范项）、E6（核对无误）、E7（正文与修订记录不一致）编号。发现清单在 git 历史；代码注释里 `docs/19 E3-4` 这类引用指向的就是这些编号。

- **E1**：围栏等价性门禁 `scripts/acceptance/fence-parity.sh` 独立复跑通过，搬家前后 16 条判定逐字一致。它只比对判定函数的输出，不覆盖判定结果怎么发上线、会话轮换和配置迁移。
- **E2 的三条都已处置**：E2-1 流量判定结果在线上不可见的回归（带回归测试）、E2-2 会话轮换后围栏丢一轮，两条已修；E2-3 混入无关文件的提交已复核，清出历史等操作员确认（§14）。
- **E3 到 E7** 见下面的关闭记录。

### E8 关闭记录（2026-11-28）

对照 E2–E7 逐项记录处置结果。落点以引擎 `tianwu/fixes` 第三、四批和外壳 `feat/server-minimal` 第二批为准。

**E3 六条，全部关闭。**

| 编号 | 条 | 怎么关的 |
| --- | --- | --- |
| E3-1 | 上游通用包反过来 import 4z 积木 | 接缝类型原生定义在 `tools/proxy`，围栏经 `scoperules/judge.go` 的别名实现它。这是红线 3 的登记例外（已决 23）。最小 agent 与 audit 不再装配围栏，两个依赖边界测试转绿 |
| E3-2 | 围栏和会话积木写进 `pkg/harness` 基础组合 | 基础组合恢复上游的 guardrail，`BaseConfig.Policy` 只放产品根加在它前面的判定块；`harness.go` 与上游零差异 |
| E3-3 | 判定留在上游 guardrail | `extension.go`、`jev.go` 与上游零差异，`runtime.go` 剩 `SubmitReview` 9 行；策略与 JEV 用量上报在 `exts/4zfence/policy` |
| E3-4 | 规则表是包级全局 | 会话表归扩展自有的 `Store`；两个 Store 包的包级入口全部删除，装饰器、会话钩子、代理判定器绑定在扩展实例上。会话路由表也已归 `exts/4zfence/egressroutes` 的 `Store`，`core/egress` 只剩契约类型 `Dial` |
| E3-5 | 4z 自己的代理策略留在 `tools/proxy` | 控制面豁免、被动子资源、403 响应体在 `exts/4zfence`；`mitm.go` +15、`hub.go` +6 |
| E3-6 | 审批队列靠手工 `Bind`、吞掉 `Use` 的错误 | `exts/proxy` 的配置直接接收判定入口，由组合根显式传入；没有判定方是显式的无判定转发，同上游 `Egress` 留空即 `NoEgress` 的写法。围栏拿到 guardrail 运行时的那一步仍是 `Bind`，但它用的 `Use` 是硬的，缺了就加载失败 |

**E4：全部关闭。**

登记：`FORK.md` 在 Local patches 下有逐字路径索引，覆盖全部被改的上游文件；`scripts/check-fork.sh` 按已决 24 分三类，另加第四类 `moved`（整文件搬进 fork 自有包的上游文件，P6）。预算：分两轮把写在上游文件里的逻辑搬进 fork 自有文件——第一轮是带 4z 逻辑的六个文件，第二轮是修复类的 14 个——P6 把装配根挪进 `pkg/assembly` 之后，没有上游文件超过 20 行。`make check-fork STRICT=1` 通过。

**E5。**

- 外壳七条：`--web-dir`、`ZRCIN_WEB_DIR` 的两义（生成器改用 `ZRCIN_WEB_DIST`）、文档与 CI 里的根目录 pnpm、已决 19 的诊断、`/api/version` 的前端来源、架构测试的变量遮蔽，都已关。旧的 `extensions.traffic` 改为读路径只报不迁。
- 引擎：测试专用的 `NewRuntime` 已删；`tools/proxy` 的类型别名已删（类型原生定义在代理包）；4zevidence 不再 import observe；`docs/configuration.md` 改到 `fence` 节；`--observe artifacts` 换成独立的 `--evidence`；判定 sidecar 的钩子缩到 +3。
- 引擎未动的一条：请求装饰器是 `RuntimeConfig` 的字段而不是贡献点（偏离较小，已决不记入差距）。`core/egress` 的包级表与 `"http.request"` 的重复常量都已关闭。
- 按关键词特判命令字段：已处置（r10，已决 36）。实证的绕过已修——`commandLikeCall` / `commandText` 改为与工具解码同一套规则（`exts/4zfence/commandkey.go`），两条对照测试把它们钉在 bash 的解码上；判据的结构性改造不做，触发条件见已决 36。

**E7。** 正文已按现状收口：历史章节删除，§4、§8、§11、§13 改为现状。

**E4 第二轮（E4 round 2，2026-11-28）。** 出口代理的会话路由表归围栏扩展自己的存储（`exts/4zfence/egressroutes`），`core/egress` 只剩契约类型；E5 的包级表一条关闭。

**现状（2026-11-28，引擎 `db67d40e`）。** 修复类补丁的 14 个文件在 20 行以内；装配根随 P6 挪进 `pkg/assembly`，超预算为 0；`check-fork` 的 `moved` 类登记了 50 个搬走的上游文件。


### r10 关闭记录（2026-11-28）

- **新发现并关闭：围栏命令键的大小写绕过**（§11 收尾）。这是审查查出来的，不是计划里的项。
- **§1 表「还差什么」清零**：两套 absorb 合并为一个函数；IOA 信封与 `ListAgents` 改用引擎的包。
- **E5 的最后一条关闭**：`commandLikeCall` / `commandText` 不再是「位置已定位、未改」。
- **§14 的三条未定项落成已决**（34、35、36），P5 落成已决 37。
- **P6 那条需要模型的验收已覆盖**（`local-turn.sh`）。
- **规则热加载（docs/20）并入**：四条车道里只有 4zgears 推送过，引擎、外壳、4zweb 三条在此整合；它在 §14 里是已决 38，了结已决 16 的后半句。
- 仍未关、只能由操作员做的：历史里的真实凭证（§14 的风险）。
