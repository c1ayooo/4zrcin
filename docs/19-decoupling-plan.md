# 19 · 解耦与积木化方案（2026-11-27）

状态：**实施中**。P1 各步、围栏配置节迁移（已决 15、19、20）和围栏等价性验收都已落地。实施审查（附录 E，引擎 `eca06a22`、外壳 `401053c`）的结论是：骨架按设计搭起、等价性验收成立，但有 1 个已复现的回归、1 处顺序缺陷和若干与设计相反的做法待定，P1 的验收条款尚未全部满足。**E2 的两条围栏缺陷、提交卫生、已决 19 的诊断、--web-dir 与红线 1 机器检查已在第二十一版整改落地（引擎 `ef0f99fd`、外壳第二十一版各笔）；E3 的六条与 E4 的钩子压缩待第二轮定夺。**三轮需求对齐已完成，时间估算不再补。设计范式与现状符合度见 §1（符合度表停在实施前，见附录 E7），拓扑见 §2，判定标准见 §3，包归属见 §4，模板见 §5，前端见 §6，构建见 §7，分期见 §11，可达程度见 §13，风险、已决与实施约定见 §14，实施审查见附录 E。

第十九版（同日）：记录对已落地实施的审查（附录 E）。围栏等价性门禁独立复跑通过；同时有 1 个已复现的回归、1 处顺序缺陷和若干与设计相反的做法。第十八版「P1 验收条款至此全部满足」按实测不成立。

第二十一版（同日）：附录 E 第一轮整改落地（引擎 dc8ca932 + c4e7d1a1 + ef0f99fd + 7f1c54eb，外壳 2ba0a7a + 3b8d34b + 其余两笔）。

- **E2-1 已修**：判定 sidecar 改经 `exts/observe/sidecar.go`（新增文件）逐字附到 `event.Extensions`，不再经 `SetTypedExtension` 二次包裹；上游文件净改动缩到 3 行。observe 侧新增 Any-in-Any 回归测试（先红后绿），fence-parity 门禁复跑 16 条判定逐字一致。
- **E2-2 已修**：rotate 的先关后开顺序保留（续接会话与旧会话同 logicalID，换序会被 `rt.sessions` 拒绝），改在围栏侧修——`cleared`/`compacted`/`resumed` 三种轮换关闭改为把规则集移入有界 rotation park（scoperules 与 globalfence 各一份），续接会话的继承在父会话已不在活表时从 park 取；非轮换关闭照旧清表。fence 侧新增按 rotate 时序的继承测试。
- **E2-3 已复核并处置**：`789f8a3` 确实混入 56 个无关文件，全部仍在跟踪中（附录 E「二进制未入库」的存疑不成立，四个二进制与快照 YAML 都在库里）。已 `git rm --cached` 全部混入文件（磁盘保留）并补 `.gitignore`。**密钥扫描发现真实凭证**：界面快照里有 TSecBench 授权任务的 `BENCHMARK_TOKEN`（60a58f2b-…）与内网 VPN 信息，已随 `789f8a3` 推送。这些内容仍在 git 历史里，抹除需要重写已推送的历史——待操作员确认后再做；在那之前应轮换该 token。
- **已决 19 已补**：六处保存路径（guardrail 判定写、jev、node proxy、traffic capture、runtime 文档、MCP/search 节）在迁移真正落盘时各记一条诊断；读路径的内存迁移保持静默。
- **--web-dir 已补齐**：目录模式改为只替换读字节这一步（`readBundle` 目录优先），`/static/` 与 index 共用内嵌路径的缓存头与 Content-Type；`dirStatic`/`dirIndex` 死代码删除。顺带修了 P1-3 起的 `-tags noconsole` 构建破损（`web.SetSourceDir` 缺空实现）。
- **E4 机器检查已落地（报告模式）**：引擎 `scripts/check-fork.sh` + `make check-fork`（`STRICT=1` 强制）。实测超标面比 E4 表大得多：除表列的 guardrail/runtime.go +199、protocol.go +111 外，全量报告含约 40 个 UNREGISTERED（多为测试、docs、go.sum）与十余个 OVER BUDGET——收敛清单以脚本输出为准，压缩工作随 E3 第二轮。
- **附带**：`exts/4zsession` 自 P1-4 起未声明 Extension，`TestEveryExtensionSubtreeDeclaresAnExtension` 在被审查基线上就是红的；已补最小 Extension 壳（发布 `InputDecorators`，接线不变）。外壳 gate 的第 2 步自 `f3f6946` 起被 parity 基线孪生测试打断；该测试现挂 `fenceparity` 构建标签，孪生脚本传标签编译，parity 复跑仍 16 条一致。
- E3 的六条与 E4 的钩子压缩**待第二轮逐条定夺**，本轮未动它们的代码。

第十三版（同日）：P1-4 主体落地（引擎 58533ca7 + 98ad79b1 + bda20b08，无冻结窗口）。接缝 G2：代理判定逻辑收进 `tools/proxy/scope_judge.go`，`hub.go`/`mitm.go` 只留调用行与 `ScopeJudge` 接口。接缝 G1：回合/开局装饰器成为 `RuntimeConfig` 字段，`agent/session` 不再携带任何 fork 载荷代码，默认列表取消——不装配就不围栏，是有意的。4zfence 落地：`core/scoperules`、`core/globalfence` 迁入 `exts/4zfence/{scoperules,globalfence}`，策略载荷（scope rules、global fence、egress proxy）的装饰器与 SessionStart/SessionEnd 生命周期钩子收进 `exts/4zfence`，移动的测试随代码走，golden 语料全绿。4zsession 落地：输入增益载荷（run goal、case seed、tool env、work dir）收进 `exts/4zsession`，两块积木互不 import。接线每装配根一行（`pkg/harness`、`cmd/cyber-scan`、`cmd/agent`、`cmd/audit`）。第十四版（同日）：P1-4b 落地（引擎 751d3f8f + 77ba3c05）：4zfence 自持 `tool.before` 准入（保底审计 + 实例红线），装配序先于上游 guardrail；review 红线经 guardrail 的 SubmitReview 接缝（G3）走普通审批生命周期。guardrail 的 `Admit` 剩纯策略关注点（JEV/LLM/hybrid、scope、allow_prefixes、shadow），对 P1-4 前基线净 −41 行；`builtin.go` 与红线测试随迁。`core/egress` 仍是被共享的注册表（可在红线稳定后再收）。第十五版（同日）：P1-4c 落地（引擎 8e686e5a + b804f7a5）：scope 判定（HTTP + CONNECT）迁 `exts/4zfence/scope_judge.go`，review 挂起改走 SubmitReview 公共接缝、不再触碰 pending 内部；接缝类型（ScopeJudge/ScopeRequest/ScopeDecision）下沉到 `exts/4zfence/scoperules` 中立地带，`tools/proxy` 以别名引用——fence 实现代理契约而无需 import 代理。`exts/proxy` 从 fence 扩展取判定入口，不再认识 guardrail。附带修复：guardrail 不再 import tools/proxy，最小 agent 依赖闭包测试（此前遗留失败）转绿。§1 的「exts 和 tools 互相伸手」缺陷就此关闭。第十八版（同日）：样本集对比验收通过。样本集 16 例（保底审计、红线 deny/review/挂起、非命令调用；全局放行/禁访、显式行、默认未命中、被动未命中、shadow）落在引擎 `exts/4zfence/testdata/acceptance_samples.json`；验收走完整准入路径（当前树 `exts/4zfence/acceptance_test.go` 驱动 fence admission + scope 判定，基线孪生 `scripts/acceptance/acceptance_baseline_test.go` 在基线 worktree 的 guardrail 包内驱动同路径），门禁 `scripts/acceptance/fence-parity.sh`（默认基线 c0b54f5e）逐样本 diff VERDICT 表：**16 条判定逐字一致，PASS**。docs/19 的 P1 验收条款至此全部满足。

第十七版（同日）：已决 15/19/20 落地（引擎 e3c3a8cc + bbb5c945 + 271b3bb5 + 510befe8 + 外壳 789f8a3）。新节名定为 `fence`（YAML 顶层键不以数字开头）。引擎：`exts/4zfence/config`（fenceconfig）为配置结构与校验的单源（不依赖引擎内部，外壳经模块 import 同一份，做法同载荷契约包）；guardrail 节严格恢复上游原样（provider 只认 none/jev、mode、review_timeout），判定旋钮（provider llm/hybrid、on_error、shadow、scope、allow_prefixes、timeout、concurrency、llm）归 fence 节，fence 的 provider 覆盖生效判定方；fence 包自 Declare 节；pkg/config 以组合持有的 DocumentMigration 钩子识别旧写法（文件文档与显式值两条读取路径），内存态迁移、不动操作员文件，层界测试维持零例外。外壳：设置保存写 fence 节并从 guardrail 节剪除旧键，读取走合并视图，fence 列入产品顶层节白名单。判定行为与迁移前一致的验收由既有 guardrail/fence 测试覆盖（全绿）。剩余：样本集对比验收。

第十六版（同日）：P1 收尾盘点。已决 15/19/20（围栏配置节迁 `4zfence` 自己的节、guardrail 节恢复原样、读写认旧写法保存时迁移、配置校验单源）是 P1 剩余的唯一主体工作，涉及引擎节声明、hub→node 分发路径（pkg/config/guardrail.go）、外壳设置读写与诊断三处联动，单列一个工作项实施；两个待定设计点（节名、guardrail 节恢复的严格度）实施时定。样本集对比验收随其后。

修订记录（2026-11-27 第二版）：按 4zrcin 现状校正。P3 已完成，改为现状；§6 技术栈按 `web/` 实际更正；§8 改为 merge-base 口径并附命令；红线 1/3/5 与 docs/03、04、14 对齐；编号 18 → 19（18 已被 `18-webshell-management.md` 占用，代码注释里的 docs/18 都指那篇）。标「待复核」的条目依赖引擎仓或上游，数字是 2026-11-27 的实测值，P0 开工时重跑一遍。已决（2026-11-27）：4zweb 就是本仓的 `web/`，整体搬出，不替换、不重写。

第三版（同日）：未决项全部给结论，记入 §14 的「已决」与「实施约定」；红线 3、红线 5 与 §6、§7 的相关表述同步改定。

第四版（同日）：对齐需求。两条原则写进 §1 并给出验收；§2 加积木卡；入侵标准定为钩子级，协议只增不改；外壳内部做成真模块，仓库归属按标准逐个判断。

第五版（同日）：对照上游原仓库逐条核查 fork 的 37 条补丁，结果记在附录 A–D。更正了两处：fork 没有改任何协议消息（此前写的「30 个 `.pb.go` 因改 proto 重新生成」是错的），实质改动面是 187 个文件而不是 217；「协议迁出」这项工作不存在，已删。

第六版（同日）：P0 实施完成，§8 与 P0 一节改为实测（178 个文件），合并决策登记引擎 FORK.md。

第七版（同日）：对照上游开发者指南重新校准。终点定为「只用官方组合机制」，登记钩子改为过渡态（§1）；第一成功标准、推进方式与行为口径按实施前的对齐写定（§1、§14）。按两个仓的实测改写 §4（外壳包图无环，层级表零例外）、§5 与杠杆五（规则仓已存在，生成链路可复现）、§8（数字复核）与附录 A、C、D；新增可达程度（§13）与缺陷修复清单（§14）。

第八版（同日）：第二轮对齐写定三条（§14 已决 17–19）：`4zweb` 拆仓提到围栏搬家之前，P1 重排为六步；围栏静默失效的两个修法都做；围栏配置迁移时读写都认旧写法。§1 新增设计范式与现状符合度。核实了 Hub 的配置接口不校验产品配置节，配置的两件事列为第三轮的对齐项（第九版写定）。

第九版（同日）：第三轮对齐写定两条（§14 已决 20、21）：产品配置节的校验随积木走；公共配置的归属推迟到 P2 再定。需求对齐至此完成。

第十版（同日）：P1 前三步实施完成，记录实测。P0 附带发现并修复配置布局回归（#205 后嵌套 extensions: 被全部二进制拒绝，外壳的运行文档按受众拆为 Hub/节点两份，写入路径迁顶层，缺陷③随之修复——提交 82e5c9e）。守门决策接入控制台（2434a45）：provider/mode/on_error/shadow + llm 裁判可改，真实 scan 二进制验收通过。P1-0 围栏静默失效两个修法落地（引擎 c0b54f5e）：表满拒绝回合（FAILED_PRECONDITION）、关闭即清理；已决 18 的两个验收。P1-1 载荷契约包落地（引擎 70d00838 + 外壳 39e1741）：aop/payloads 收编 7 载荷 + 2 sidecar，两侧 import 同一份，证据上限统一 8MiB；代价如实记录——外壳默认构建自此需要引擎检出，CI 已检出公有引擎仓。P1-2c 子会话继承改用 SessionStart（引擎 15a6f8fe）：sessionexec/tool.go 对上游 diff 归零。P1-2a/b 也已落地（引擎 f320d91e）：4zevidence 拥有证据回传全套（快照差异、进程/文件 sidecar 载体、直附 Any 的 emit），exts/observe 恢复上游 + 一个 ~23 行登记钩子（flowCompleted 附着 verdict sidecar；Any-in-Any 修复随钩子，上游 PR 按 §14 已决 12 先提）。P1-5b 装配包也已落地（引擎 e5e3dae0）：配方孪生收进可导入的 pkg/assembly（EnginesInput 闭式入参，capture 孪生空实现），G3 SubmitReview 接缝先行（135dde28）；外壳层级表架构测试落地（728c14f，49 包九层零例外）。P1 剩 P1-4 主体（等冻结窗口与样本集）与文档时间估算。--observe artifacts 路由到 4zevidence；证据测试随扩展迁移并全绿。

第十一版（同日）：确认终点的一句话表述与范围（§1、§14 已决 22）。

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

对照前八条，2026-11-27 的代码是这样的。第 9 条约束的是这次的设计：只新开 G1、G2、G3 三个接缝，每个都在附录 A 写了「官方现有的为什么不够」。

| 范式规则 | 现在 | 证据 | 哪一步修正 |
| --- | --- | --- | --- |
| 三分；产品由入口装配决定 | 部分符合 | `4zwebshell` 一行链接；Hub 内嵌是纯新增的 `hubapp`。但围栏、证据、会话载荷的逻辑写进了上游函数体 | P1-2、P1-4 |
| 业务实现独立，扩展只做生命周期适配 | 不符合 | `Admit`（+229）、`Requestheaders`（+165）、`RunAOPTurn`（+394）三处内联 | P1-4 |
| 组合根显式有序，每块积木一行 | 部分符合 | 装配散在 `cmd/cyber-scan` 的 27 个文件里 | P1-5 |
| 贡献点与借用，不查全局注册表 | 不符合 | `core/scoperules`、`core/globalfence`、`core/egress` 三张进程级的表；`exts/proxy` 靠 import `exts/guardrail` 接线 | P1-2、P1-4 |
| 钩子做控制与观察 | 部分符合 | 上游围栏本来就挂在 `tool.before` 上；fork 没有自己的钩子登记，逻辑挤在上游的处理器里；代理发请求前没有官方钩子（G2） | P1-4 |
| 协议只增不改，走 AOP | 符合 | 7 个载荷搭在上游请求自带的 `extensions` 字段上，上游 proto 一个字段没改 | — |
| 每个功能声明自己的配置 | 不符合 | 围栏的判定配置加在上游 `guardrail` 的 `Config` 里（`exts/guardrail/extension.go` +253） | P1-4 |
| 分层由架构测试守着 | 部分符合 | 引擎的 `pkg/internal/architecture` 在 fork 上通过；外壳没有这类测试 | P5 |
| 宿主不绕过公开入口 | 符合 | 外壳对引擎只有两个 import，都走 `hubapp.App` | — |
| 外壳手里的引擎协议只有一份 | 不符合 | 12 份 vendored pb、手写的 WebSocket、手抄的配置校验 | P1-1、P2 |

结论：**目标状态按官方范式；现在的代码只有两条完全符合，四条部分符合，四条不符合。** 不符合的四条就是 P1 和 P2 要做的事。即使全部做完，仍有附录 A 列的几处差距留在上游文件里，等上游接收。

整份计划按两条原则验收。

**原则一 · 最小化入侵 cyber-harness。** fork 等于「上游 + 纯新增」。上游已有的文件只允许登记在册的挂钩，逻辑全部在新增文件或自己的模块里。协议只增不改：新内容写进新增的 proto 文件或 anypb sidecar（fork 现有的 JSON-in-Any 惯例，docs/04 §3.1），不改上游消息。

落地按三级优先，钩子是最后一级：

1. **协议**：控制面与节点之间要传的东西，一律走 AOP——新消息或 anypb sidecar，不改上游
2. **扩展**：节点里照数据办事的代码，放进引擎的扩展（`exts/4z*` 或独立模块），不改上游
3. **钩子**：上游函数中途中必须改、扩展点又够不着的，才留几行调用；能先向上游提 PR 的先提

AOP 只负责传，不负责做。数据到了节点，总得有代码去读它、照它办，所以第 2、3 级免不了。处置表里每条补丁都按这个顺序追问。

**第 3 级是过渡态，不是终点。** 每条钩子在 `FORK.md` 里登记出口：被上游接收，或改用官方已有的接缝。上游未接收期间它留在 fork 里，计入「离终点的差距」（§13）。差距清零这次做不到，缩到附录 A 列的那几处是能做到的。

验收（机器检查，进引擎 gate）：

- `git diff upstream/master...HEAD` 里，上游已有文件的改动只剩 `FORK.md` 列出的钩子文件；出现未登记的改动即失败
- 每个钩子不超过 20 行（初定，定处置表时校准）；超了说明逻辑没放对地方
- 上游的 `.proto` 文件与上游一致，fork 的协议内容只在新增的 proto 文件或 sidecar 里
- `go.mod`、`Makefile` 的改动逐条记账

**原则二 · 自己的东西拆成积木。** 每块积木是独立模块，只通过契约与外界相连：引擎契约（`aop`、`core/tool`、`core/extension`）或数据（YAML、`.bin`、JSON、HTTP）。积木之间不互相 import，能单独换、单独摘。外壳只负责装配。

验收：

- 每块积木有一张积木卡（§2），写清形态、装配点、契约、摘除方式
- 外壳内部按层级表和架构测试做成真模块（§4）
- 模块边界立住之后，仓库归属按 §3 的三条标准逐个判断：满足的可以各自成仓，不满足的留在同一个仓

**实施前对齐写定的三条**（2026-11-27，决定全文见 §14）：

- 第一成功标准是引擎 fork 回到「上游 + 纯新增 + 少量登记钩子」，升级上游不再大面积冲突；独立发版和分仓排在它后面
- 小步推进，每步可单独上线，不设整体冻结；只有围栏搬家那一段需要短冻结（§11）
- 拆解按「行为逐项不变」验收；读代码发现的缺陷走单独的修复清单，各自带复现测试，不和搬家混在一起（§14）

## 2. 目标形态：积木、引擎、外壳

终点不是「六个仓」，是「每块积木一行装配、越界可机器判定」。份数是结果：边界立住之后按 §3 的标准逐个判断，一次只多一个。

```
积木层（独立发版、独立 CI、互不 import 内部实现）
  4zweb          控制台前端，即现在的 web/（React 18 + Radix UI + Tailwind）；dev 挂载目录，prod 内嵌进外壳
  4ztemplates    规则 YAML（指纹/POC/字典/工作流）；仓库已存在，是 c1ayooo/templates
  4zwebshell     webshell 会话与后渗透通道（已存在）
  4zreport       报告渲染（findings JSON in、HTML/PDF out，可选；仓库内尚无对应实现，见 §3）

引擎层
  4zengine       cyber-harness fork（仓库名不改，4zengine 是逻辑名；基线分支 tianwu/fixes）；链接上面的扩展，出 cyber-harness / cyber-scan
    exts/4z*     载荷契约包、4zevidence、4zfence、4zsession、装配包：先放引擎仓的新增目录，边界立住后再判断是否成仓（附录 D）

组装层
  4zrcin         外壳：kernel + agent + fleet + domain + edge；不拆
```

依赖方向严格单向：

```
4zrcin ──► 4zengine ──► 4zwebshell / 4zfence ──► 引擎契约（见红线 3）
   │            │
   ├─► 4zweb（构建期产物，非代码依赖）
   ├─► 4ztemplates（构建期数据，非代码依赖）
   ├─► 4zwebshell/webshell（库；本仓 6 处 import，含 zrcin-node）
   └─► rem fork（replace => ../rem；cmd/rem-relay 与 -tags remtunnel 依赖它）
```

扩展永不 import 外壳；积木之间不互相 import，只通过引擎契约或数据文件交接。

两条补充：

- `4zrcin ──► 4zengine` 只在 `-tags inprochost` 构建里存在。默认构建不 import 引擎，靠 vendored 协议副本（见杠杆三）。
- rem 不算积木，但它已经是版本矩阵的一部分；份数不含它。

每块积木一张卡，写清它怎么接、怎么摘：

| 积木 | 形态 | 装配点 | 契约 | 摘除方式 |
| --- | --- | --- | --- | --- |
| 4zweb | 静态产物 | 构建期编进外壳（flate bundle）；开发期 `--web-dir` | 外壳的 HTTP API | 不装：`-tags noconsole` |
| 4ztemplates | 规则数据 | 引擎的生成器从规则仓检出读入，产出 `.bin` 进 `tools/resources` 与 `tools/proton/resources` | 资源 key，共 19 个，由引擎的生成器定义 | 不装：`-tags emptyresources`（只裁前一个包的 15 个） |
| 4zwebshell | Go 模块（库 + 扩展） | 外壳 import 库；引擎装配时链接扩展 | `core/extension`、`core/tool`、`aop`，加控制面 HTTP | 装配里去掉链接 |
| 载荷契约包 | Go 包，纯类型与编解码 | 外壳和引擎各自 import | 7 个请求载荷、Artifact、TrafficVerdict 的 JSON 形状 | 不能摘：它是两边的契约 |
| 4zevidence | 引擎扩展 | 引擎装配时链接，一行 | 订阅 `process.*` 与 `file.access` 钩子，自发带 sidecar 的事件 | 装配里去掉链接 |
| 4zfence | 引擎扩展 | 引擎装配时链接，一行 | `tool.before`；代理的请求准入接缝；上游的审批队列 | 装配里去掉链接 |
| 4zsession | 引擎扩展 | 引擎装配时链接，一行 | 请求装饰贡献点；`RunInput` 的加性字段 | 装配里去掉链接 |
| 装配包 | 引擎里的可导入包 | `cmd/cyber-scan` 里约 5 个单行触点 | `extension.Extension` 列表 | 换回上游的装配 |
| rem | Go 模块（隧道库的 fork） | 外壳 `-tags remtunnel`；`cmd/rem-relay` | rem 协议 | 不带标签编译 |
| 4zengine | 引擎 fork | `-tags inprochost` 把 Hub 编进外壳；scan 单独出二进制 | `aop`、`pkg/web/hubapp`、`pkg/harness` | — |

装配原则：装配尽量在自己的模块里完成。必须在引擎里链接的，每个积木一行，登记为钩子。

fork 里现有的自有逻辑拆成哪几块积木、各自接在哪儿，见附录 D。

## 3. 积木判定标准

一个东西能不能拆成独立仓，只看三条：

1. **边界是数据** — 输入输出都能用 JSON/YAML/二进制描述，双方不共享内存 → 能拆
2. **构建链不同** — Node 与 Go 混在一仓，CI 必然长补丁 → 必须拆
3. **共享事务或内存状态** — 拆开就要分布式事务或每日对账 → 不能拆

三条过一遍 49 个包：`4zweb` 命中第 2 条；`4ztemplates`、`4zwebshell` 命中第 1 条；`4zfence` 名义上命中第 1 条，但围栏协议近期仍在改（`internal/aop/guardrail` 持续同步），边界没稳定；`4zreport` 不命中——仓库里没有 HTML/PDF 渲染，`internal/report` 做的是报告目录与不变量校验，并依赖 executor、store、board、evidence。外壳的 `kernel`/`agent`/`edge` 命中第 3 条（store 与 ledger 共享 SQLite 事务边界、decider 读 ledger 全量历史、api 聚合所有业务 DTO）。

**结论：份数是结果，不是目标。** 第一成功标准是引擎 fork 回到「上游 + 纯新增」（§1），所以先把积木做成引擎仓里一行装配的扩展，仓要不要分开等边界立住后逐个判断。眼下只有 `4zweb` 命中第 2 条，必拆；`4ztemplates` 的仓库已经存在，`4zwebshell` 已经是独立模块。再多，版本矩阵会替代代码耦合成为新的痛点——改一个共享类型要发 3 个版本，`go.work` 里写 5 条 replace，所以不预先封顶，但一次只多一个。

外壳内部的模块不要求同仓。先按 §4 把边界立住，再逐个用上面三条标准判断：边界是数据、且不共享事务或内存状态的，可以各自成仓；共享 SQLite 事务边界的（账本层与决策层之间）留在同一个仓。现状里边界最干净的是不 import 任何内部包的 `kb`、`pocs`、`skills`，以及只依赖 `istate` 的 `notify`、`account`。成熟一个拆一个，不要求一次拆完。

## 4. 外壳内部：从 49 包到可判定的层级

外壳的内部包不搬仓，先让越界能被机器判定。本节的数字是 2026-11-27 按 `go list` 的非测试 import 实测的，含工作区未提交的改动。

**规模。** `internal/` 非测试 100.3K 行；`cmd/` 另有 14.4K 行（`cmd/zrcin` 9.5K，其中 `main()` 764 行，`hub.go` 3.8K）。`internal/api` 一个包 98 个文件 26,097 行，占 `internal/` 的 26%。

**包图没有环。** 49 个顶层目录，连同 `aop/*` 7 个、`host/inproc`、`web/bundle`，共 58 个包、148 条内部 import 边。强连通分量全是单包：没有任何两个包互相 import，只有一处测试级的环（`evidence` 的测试 import `store`，`store` import `evidence`）。早先按七组划分算出的「组间有环」有 6 对（agent 与 kernel、agent 与 fleet、agent 与 domain、agent 与 edge、domain 与 edge、edge 与 kernel），全是分组造成的。

**按实测依赖图排出的层级。** 规则只有一条：只能 import 更低的层。逐边核对过，没有例外。

| 层 | 包 |
| --- | --- |
| 类型 | `model` `collab` `contract` `istate` |
| 协议 | `aop` `ws` `connectretry` `ioa` 及 `aop/*` |
| 执行通道 | `executor` `aopexec` `enroll` `noderes` `platform` `tunnel` `relay` `deploy` |
| 账本 | `store` `ledger` `state` `project` `artifact` `evidence` `access` |
| 领域 | `board` `retrieval` `bundle` `traffic` `metrics` `report` `assets` `kb` `pocs` `skills` `vulnlib` `resource` |
| 决策 | `decider` `verifier` `lifecycle` `modelclient` |
| 编排 | `host` `node` `fleet` |
| 入口 | `api` `web` `settings` `account` `notify` |
| 支撑 | `buildinfo` `testutil` |

和早先的七组相比有四处不同：两个叶子包（`model` 被 19 个包 import，`collab` 被 6 个）连同 `contract`、`istate` 归最底层的类型；`executor`、`aopexec` 和节点原语单独一层，放在账本之下，因为 `ledger` 依赖 `executor`；`host`、`node`、`fleet` 提到决策之上；`access` 归账本层，因为 `resource` 依赖它。

**所以 P5 的两个前置并成一个：先加架构测试，搬目录不急。** 测试照引擎 `pkg/internal/architecture` 的写法（它已经禁止 `agent/` import `exts/`、`tools/`、`pkg/`，也禁止 `pkg/config` import 产品实现），把上表的层号写死，越界即失败。目录按层归并只是移动加改 import，可以晚做或不做。

**对引擎的接触点已经很小。** 真正的 import 只有 `internal/host/inproc/inproc.go:22-23` 的两个（`pkg/web/host`、`pkg/web/hubapp`），都在 `-tags inprochost` 下。默认构建一个引擎包都不 import，靠 `internal/aop/*` 里的副本：12 个 `*.pb.go`（`!inprochost`）加 7 个 `alias_gen.go`（`inprochost`），共 10,695 行生成代码。`hubapp.App` 的 `override func(*webhost.Config)` 是外壳往 Hub 里加东西的入口，位置是对的；以后加扩展、加 `Capabilities` 都从这里进。

**`internal/aop` 根包要一分为二。** 根包 2,249 行：6 个客户端文件 1,846 行（连接、收发、PTY、Connect 调用），9 个载荷编解码文件 403 行（`workdir`、`rungoal`、`seed`、`toolenv`、`scoperules`、`globalfence`、`egressproxy`、`artifact`、`trafficverdict`），后者只依赖标准库和 anypb。这 9 个文件是载荷契约包的来源（§11 的 P1-1），搬走后根包才能以「客户端骨架」的身份留在协议层。

**拆 `api` 是另一件事。** 一个 `*api.Server` 上有 546 个方法、48 个导出字段，其中 36 个字段由 `cmd/zrcin/hub.go` 的 `attachState` 手工赋值；路由登记约 276 处，分在 7 个登记函数里，但项目级的 222 条里有 213 条挤在前两个函数中。拆法不是拆仓，是按资源切子包，每个子包只负责一到两组路由；546 个方法共用一套依赖，所以要把 `Server` 拆成各自的 handler 与依赖注入，是一次真正的重构，不是按文件归堆。子包按实际资源定，体量最大的是 intents、agent、prompt、state、journal、workers、collab、shell、dispatch、commander。它不在这次范围里，边界先由层级表守住。

**两处判断保留。** `contract` 现在只有 14 行，仍应扩成真正的接口定义：哪些操作对账本可写、哪些只可读。层级表管的是包与包之间，这份契约管的是账本对上层敞开多大。`buildinfo` 10 个文件 2,575 行，不是版本注入，是 gate 与构建清单的工具库（AOP 协议映射与漂移检查、git 与树快照、平台与模型探测）；杠杆三落地后其中约 840 行（`aopproto.go`、`aopalias.go`）会消失或大幅缩小，在那之前不建议单独压缩。

## 5. 模板与规则：4ztemplates

仓库已经存在：`c1ayooo/templates`，上游是 `chainreactors/templates`。4ztemplates 只是它在本文档里的逻辑名，仓库名不改。

### 5.1 仓库现状（2026-11-27 实测）

- **现在是一面镜子。** 与上游共有的 6 个分支（`master`、`dev`、`detached`、`detached2`、`found-v0.3.2-filters`、`spray-v1.3.1-template`）领先 0、落后 0，自有规则为零。
- **`feat/add-extract` 不是自有内容。** 它是上游作者 5 月 31 日的提交，36 个文件后来全部进了上游的 `spray/proton/`，fork 时被一起带了过来。
- **引擎通过子模块用它。** `tools/templates` 指向 `chainreactors/templates`，钉在 `cc5dd3f6`；上游 `master` 比它多 11 个提交，动的是 `found/`、`services/`、`proton_rules/`（迁到 `spray/proton/`）和 `zombie/`，没有碰 `fingers/`、`neutron/`、`port.yaml`、`workflows.yaml`、`extract.yaml`。
- **仓库只有数据和它自带的生成器。** `master` 上 4,848 个文件：`fingers/` 4,405、`found/` 199、`neutron/` 83、`services/` 75、`spray/` 63、`zombie/` 15，外加 `port.yaml`、`workflows.yaml`、`extract.yaml` 和 `templates_gen.go`。没有 `go.mod`。

想加一条指纹、加一个 POC，现在没有落在维护分支上的源可以改：引擎的子模块指着上游，fork 上也没有自有规则。这是把规则所有权拿回来的直接动机。

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

**升到上游 `master` 的代价已经量化。** 从上游 `master` 重生成，19 个文件里 16 个逐字节一致，只有 proton 的 3 个变了：`found_keys.bin` 从 15,641 字节变成 16,823，`found_spray.bin` 从 3,605 变成 3,606，`found_filter_dir.bin` 从 703 变成 1,272。升规则只需回归 proton。

**一处坑。** `tools/proton/resources/template.go` 没带 `!emptyresources` 标签，也没有空孪生文件；下次重跑生成会带上标签。所以生成命令必须两个包一起跑，孪生文件一起对齐。

**引擎自带一条运行时下发通道。** `--cyberhub-url`、`--cyberhub-key`、`--cyberhub-mode`（配置键 `cyberhub.url`、`cyberhub.key`、`cyberhub.mode`），带 24 小时磁盘缓存，可以把远端的指纹和 neutron 模板与内嵌的合并，或整体替换。它只覆盖这两类，仓库里也没有服务端。

### 5.3 目标形态

- **不新建仓。** 在 `c1ayooo/templates` 上开一条维护分支，等于上游 `master` 加自有文件，做法与引擎的 `tianwu/fixes` 相同。每月把上游 `master` 合进来，只在我们也改过的文件上冲突。
- **自有规则按上游的目录结构放，不另开子目录。** 新指纹放 `fingers/http/<新分类>/`，tag 就是分类名；新 POC 放 `neutron/<新产品>/`；新字典放 `spray/dict/<新文件>`。不用 `4z/` 子目录：它在 git 层零冲突，但会让指纹的 tag 变成 `4z`、让同名字典互相覆盖。`port.yaml`、`workflows.yaml`、`extract.yaml` 是单文件，自有改动必与上游冲突，能不改就不改。
- **生成留在引擎侧：引擎读规则仓，不是规则仓往引擎里写。** 引擎加一个 `make resources`，初始化子模块或接受一个检出路径，跑上面两个包的 `go generate`，再用 `git diff --exit-code` 比对产物。重生成的 `template.go` 与 `data/*.bin` 是生成物，替换它们不算改上游源码，红线允许。
- **零改动的验收条件**：重跑后 `template.go` 仍带 `//go:build !emptyresources`、`resources_empty.go` 不动、`make scan-capture` 照常出包。
- **节点拿的仍是二进制。** `internal/enroll/dist.go` 分发的产物是 `cyber-scan` / `cyber-scan-capture`，规则编在里面。改规则等于重跑生成、重编 scan、重新分发节点。「改规则不重编」不列入本次范围（§14）；以后要做，指纹和 POC 走 §5.2 的运行时通道，只需补一个 cyberhub 协议的服务端，字典、zombie 规则、端口表仍然只有编译期一条路。

### 5.4 本仓侧的约束（已核实）

- **产出必须保留 `!emptyresources` 孪生。** `tools/resources` 是 `template.go`（`//go:build !emptyresources`）加 `resources_empty.go` 的两文件模式（docs/04 §3.2），scan-capture 配方靠它把 472K 裁掉（docs/04 §3.3、docs/14 的构建配方表）。
- **围栏保底审计语料另有归属。** `tools/resources/guardrail_builtin.yaml` 单源在 harness，是 docs/04 §6 的既定决策；它不是扫描规则，红线 5 相应收窄。已决：留在引擎，不随 4ztemplates 走。

## 6. 前端：4zweb 双模式

前端独立成仓，`c1ayooo/4zweb`，内容就是现在的 `web/`。

现状（已核实）：React 18.3 + Radix UI + Tailwind 4 + Vite 6 + SWC + xterm；149 个 `.tsx`、239 个 `.ts`、0 个 `.vue`。pnpm workspace 三个成员：`web`，以及源码直引的组件库 `web/packages/ui`（`@cyber/ui`）和 `web/packages/theme`（`@cyber/theme`）。`web/tests` 下 148 个 `node --test` 测试文件，`scripts/acceptance/` 下 13 个 UI 验收脚本。这套前端从上游 tianwu 继承（原名 `@saker/web`），工作区里仍在持续修改。

开发与生产走两条路径，互不影响。

### 6.1 开发态：挂载目录

外壳新增参数 `--web-dir <path>`：指定时不使用内嵌资源，直接从目录读静态文件。

```bash
# 终端一：前端热更
cd ~/4zweb && pnpm dev            # Vite dev server :5173，/api 代理到 :8080

# 终端二：外壳
./bin/zrcin --web-dir ~/4zweb/dist --addr 127.0.0.1:8080
```

两种开发姿势都支持：改前端走 Vite HMR（`pnpm dev`，浏览器访问 5173）；验证内嵌路径走 `pnpm build` + `--web-dir dist`（浏览器访问 8080）。

改造点：`web.Register` 加「目录优先」分支——存在 `--web-dir` 时从目录读文件，否则走现有 bundle。不要直接换成本裸的 `http.FileServer`：`console_full.go` 里有按内容哈希的不可变缓存头、显式 Content-Type 表，以及 `/p/<id>/` 下原样返回 `index.html` 的语义。目录分支只替换「读字节」这一步（`readBundle`），其余服务逻辑共用。这是外壳代码，不涉及引擎。

现状（已核实）：前端产物不是直接 `go:embed` 原始文件，而是 `web/dist` →`internal/web/generate` →flate 压缩成 bundle（`bundle_gen.go` + `bundle_data`），`console_full.go` 按 `bundle.Entries` 的 manifest 提供服务；`-tags noconsole` 时编译 `console_empty.go` 的空实现，测试与无 UI 节点不携带 console 字节。这条链路保留，只加目录分支。

### 6.2 生产态：内嵌

```bash
# 4zrcin/Makefile
web:
	cd ../4zweb && pnpm install --frozen-lockfile && pnpm build
	rm -rf web/dist && cp -r ../4zweb/dist web/dist
	go generate ./internal/web        # 生成 flate bundle 到 internal/web/bundle
```

产物以 flate bundle 形式编进外壳二进制，部署方不需要额外拷贝静态资源。`make gate` 里已有的 `generate -check` 会验证提交的 bundle 与 `web/dist` 一致——前端产物与源码不同步时门禁直接拦下。

拆仓后本仓要同步改的地方（已核实）：

- `internal/web/generate` 的 `-src` 默认值是 `../../web/dist`，要改成可配置。
- gate 第 4 步（`pnpm --dir web typecheck` / `test`）随源码迁到 4zweb 的 CI。
- 第 5 步的嵌入产物新鲜度检查改为条件执行（已决）。它现在靠「本仓重建前端、再比对 bundle」工作；源码搬走后本仓没有前端可重建，所以沿用 AOP 漂移检查的惯例：gate 检测到 4zweb 检出就重建并比对，没有就记 skip。检出位置与 `-harness-dir` 用同一套解析顺序（参数、环境变量、`.build/`、同父目录的 `4zweb`）。
- `scripts/check-docs.mjs` 只用 Node 内置模块，本仓 gate 仍需要 Node 跑它。

### 6.3 版本对齐

`4zweb` 的构建产物要在 `4zrcin` 里记录来源版本，外壳 `/api/version` 一并返回，便于判断线上跑的是哪一版前端。

现状：`/api/version` 只返回 `version`（`cmd/zrcin/hub.go`）；bundle 已经带有 `SourceHash`（`web/dist` 树的聚合 sha256，写在 `bundle_gen.go`）。已决：生成器在写 bundle 时一并记下 4zweb 的 commit，不手维护 `VERSION` 文件；`/api/version` 同时返回 `console_hash` 与 `console_commit`。

## 7. 构建收敛：引擎与外壳的 Makefile，加 4zweb 的 CI

```
4zengine/Makefile
  make engine        cyber-harness（链接 4z* 扩展）
  make scan          cyber-scan full（约 185MB）
  make scan-capture  cyber-scan capture（约 158MB）
  make proto-gen     协议生成（现有：跑 cmd/gen，protoc 与插件版本钉在 cmd/gen/main.go）
  make ldflags       版本注入（现有，被上面引用）
  make resources     新增：跑 go generate ./tools/resources ./tools/proton/resources（现有目标 embed-resources），再比对产物
  make gate          架构门禁（现有目标叫 check-architecture）

4ztemplates（c1ayooo/templates，不设 Makefile）
  维护分支加每月同步上游；生成与校验都在引擎侧

4zweb               （pnpm，不设 Makefile）
  CI 跑 typecheck / test / build，即现在本仓 gate 第 4 步的内容

4zrcin/Makefile
  make gate          契约与漂移门禁（现有）
  make web           构建并同步 4zweb 产物（现有，改源路径）
  make test          全量测试（现有）
  make zrcin         外壳（含 4zweb 产物内嵌）——新增；现在由 gate 代建，没有独立目标
  make node          隧道节点启动器（-tags remtunnel）——新增；同上
  make rem-relay     反向连接公网侧的二进制——新增；现在 Makefile 与 gate 都不构建，只有 docs/14 §7.5 给了运行命令
  make release       goreleaser——暂缓；仓库里没有 .goreleaser 配置
```

`4zengine` 一栏里 `engine`、`scan`、`scan-capture` 是本文档起的名字，引擎 Makefile 里现在没有；`resources` 与 `gate` 对应引擎现有的 `embed-resources` 与 `check-architecture`；`proto-gen`、`ldflags` 已同名存在。

现有目标里，`manifest`、`repair`、`radar`、`run`、`race`、`fmt`、`clean` 保留。`harness` 与 `scan-capture` 在引擎有了对应目标后改为薄转发（调用引擎 Makefile），不再在 `4zrcin` 里拼构建标签（已决）。gate 第 8 步（构建 harness）和第 10 步（构建清单里的 `harness_commit`、`capture_saved_bytes`）都留在本仓：第 8 步同样改为调用引擎的目标，第 10 步不动——它本来就是「一次构建全部产物」的记录。

一处现存的不一致要在 P2 一并理顺：`cmd/rem-relay` 没有构建标签，`go build ./...` 就会编它，所以本仓的默认构建已经离不开 rem 检出；而 gate 里的注释假设默认构建图不含 rem。

引擎由 `4zengine` 自己构建，`4zrcin` 只 `require + replace` 拿版本，不去引擎仓跑 make。理由：改外壳不必重编 177MB 的 scan；引擎升级是一次依赖版本变更，不是源码改动。注意 inprochost 构建会把引擎的包编进 zrcin，所以「拿版本」的同时要把引擎的依赖 replace 一并对齐（见杠杆一）。

上游 Makefile（229 行、26 目标）只作参考：它的「能力 = 构建标签组合」组织方式值得照搬，文件不共享，上游改了不 merge，人工看有无值得吸收的新配方。

## 8. 量化现状（引擎 fork 相对 upstream/master）

统计对象在引擎仓，本文档只记录口径与命令。下列数字是 2026-11-27 P0 完成后在引擎仓 `tianwu/fixes` @ `ce7f848d` 的实测值。

```bash
cd <引擎仓>
git fetch upstream
git rev-list --left-right --count upstream/master...HEAD      # 领先 / 落后
git diff --shortstat upstream/master...HEAD                    # 三点：只算 fork 自己的改动
git diff --name-status upstream/master...HEAD | cut -f1 | sort | uniq -c
```

口径必须用三点（merge-base）。两点 diff（`upstream/master HEAD`）会把 upstream 的新提交算进来——上一版的「删除 33」「`subscription.go` −266」「`tools/search`、`tools/passive` 的定制」都属于这种：fork 当时落后 upstream 2 个提交（180 个文件），那些是 upstream 新加、fork 还没合入的改动。红线是只增不改，出现「删除」本身就说明口径不对。

实测（三点，2026-11-27 合并 #205/#208 并完成 P0 路径回退之后；提交 `0f006837` + `ce7f848d`）：

- 提交：领先 62，落后 0；merge-base 就是 `upstream/master`（`86c5acea`）
- 文件：**178 = 新增 67 / 修改 110 / 删除 1**；+12591 / −708（P0 验收达成，此前基线 912 = 67A/845M/0D）
- 110 个修改全部是实质改动（纯路径替换已随回退清零），其中非测试 80、测试 30
- 删的那 1 个是上游的 `exts/guardrail/config_test.go`，合并 #205 时丢的。它今天就不满足红线 1，guardrail 包恢复原样时一并找回
- 改动落在哪（只算上游已有的文件）：非测试代码 68 个文件 +2362/−424，测试 32 个文件 +880，文档与模块文件 10 个文件 +375。fork 自己的逻辑另有约 2.8K 行本来就在新增的非测试文件里
- 实质改动面：110 + 67 = **177 个文件**
- **fork 没有改任何协议消息**：仓库里 18 个 proto 源文件只动了 `go_package` 一行；30 个 `*.pb.go` 已恢复为上游逐字节一致（fork 从未改过消息，只改过路径）
- fork 的 7 个扩展载荷（WorkDir、ToolEnv、RunGoal、ScopeRules、GlobalFence、CaseSeed、EgressProxy）都走上游 `OpenSessionRequest`、`RunTurnRequest` 自带的 `extensions` 字段
- 红线 1 保护的三棵树里，被修改的上游文件非测试 30 个：`agent/` 17、`core/` 3、`exts/` 10；另有测试 11 个（协议生成文件不在其内，它们只是 module path 噪音）

实质修改最集中的地方（被修改的上游文件数，含测试）：`cmd/cyber-scan` 15、`agent/provider` 7、`agent/` 7、`tools/playwright` 6、`tools/proxy` 5、`tools/curl` 5、`exts/guardrail` 5、`agent/session` 5、`tools/resources` 4、`tools/files` 4、`exts/scanner` 4、`core` 4。`pkg/rpc`、`aop/`、`core/types` 在 P0 之后不再被改。

与围栏相关、P1 要处理的：

- `exts/guardrail/` — 新增 11 个文件（`policy_llm` 616 行、`policy`、`policy_hybrid`、`builtin`、`scoperules` 及测试）；另有 3 个上游文件被改：`extension.go`（+253/−25）、`runtime.go`（+229/−21）、`jev.go`（+33/−14）。
- `core/scoperules/` — 新包，464 行；`core/globalfence/` — 新包，258 行
- `agent/session/protocol.go` — +394/−2，会话钩子
- `tools/proxy/` — `mitm.go`（+165）、`hub.go`（+109）等出口代理改造
- 另有 `exts/observe/extension.go`（+101/−24）、`core/operation`、`core/tool` 的修改

纯路径替换的判定（逐文件归一化后比较，输出 725；`*.pb.go` 单独归类，因为 descriptor 里的长度前缀会跟着路径变，归一化比不出来）：

```bash
base=$(git merge-base upstream/master HEAD)
git diff --name-status upstream/master...HEAD | awk -F'\t' '$1=="M"{print $2}' |
while read -r f; do
  case "$f" in *.pb.go) echo "$f"; continue;; esac   # 剔除 descriptor 后与 merge-base 一致
  cmp -s <(git show "$base:$f") \
         <(git show "HEAD:$f" | sed 's#c1ayooo/cyber#chainreactors/cyber#g') && echo "$f"
done | wc -l
```

## 9. 五个杠杆

### 杠杆一：fork 不改 module path（已完成）

fork 的 `go.mod` 保持 `module github.com/chainreactors/cyber`，内部 import 全部回退原样。上层通过 replace 指过去：

```
replace github.com/chainreactors/cyber => github.com/c1ayooo/4zengine v0.0.0-<pseudo>
```

Go 校验「replace 左边」与「fork 内 module 声明」一致，不校验右边仓库地址。仓库名可以叫 `4zengine`，module 声明一个字不改。

收益：三点 diff 的文件数 912 → 约 187（§8），上游每次 merge 不再有路径冲突。

**这条与「防上游闭源」不冲突**：代码来源由 replace 决定，已全部指向 `c1ayooo/*`；`GOPROXY=off` 编译已验证 rc=0。module 声明是身份字符串，不发起任何网络请求。彻底改名（含 27 个依赖的 module 声明）的脚本留待上游删库/停更时再跑，届时零成本（前提：这些依赖的 fork 已在自己名下且可拉取——引擎的 replace 指向的就是它们，`scripts/build-harness.sh` 为此设了 `GONOSUMDB=github.com/c1ayooo/*`）。

本仓侧的触点（已核实）：`go.mod` 的两条 replace；`internal/buildinfo/aopalias.go` 的 `harnessModulePath` 与 `aopproto.go` 的 `harnessAOPProto` 常量；7 个 `alias_gen.go`；12 个 vendored `*.pb.go` 的 descriptor 字节里带着 `github.com/c1ayooo/cyber/...`，回退后要重跑 `-sync-aop-protos`；目录名 `cyber-harness` 与分支 `tianwu/fixes` 写在 `internal/buildinfo/git.go`、`Makefile`、`scripts/build-harness.*`、`scripts/fetch-harness.*`、`deploy/smoke-server.sh`。仅本仓就有 24 个非测试 Go 文件引用旧路径；P0 里原先写的「106 个文件」与 §8 的 695 对不上，以实测为准。

**replace 不会自动传递。** Go 只认主模块的 replace，引擎 go.mod 里对 chainreactors 依赖的 fork 替换不会带到本仓的 inprochost 构建。`scripts/build-harness.sh` 在引擎目录内构建，而 zrcin 的内嵌构建在本仓 `go.mod` 下进行，`go.sum` 里没有任何 c1ayooo 条目——内嵌 Hub 与 `cyber-scan` 链接的依赖来源不同。对齐办法见 P2 的 `go.work`。

### 杠杆二：判定逻辑外移到 exts/4z*

fork 的自有逻辑有三种叠法，拆法不同。

**已经是纯新增的。** `core/scoperules`、`core/globalfence`、`core/artifactsync`、`core/egress/sessionroute.go`，以及 `exts/guardrail` 里新增的 5 个源文件（`policy`、`policy_llm`、`policy_hybrid`、`builtin`、`scoperules`）。搬家只是换目录。

**写进了上游函数体的**，三处最重：

| 位置 | 现在的叠法 | 拆法 |
| --- | --- | --- |
| `exts/guardrail/runtime.go` 的 `Admit`（+229/−21） | 保底审计、实例围栏、scope、shadow、allow_prefixes 都在上游函数体里 | 4zfence 自己挂 `tool.before`；上游 guardrail 只当审批队列 |
| `tools/proxy/mitm.go` 的 `Requestheaders`（+165） | 判定、控制面豁免、被动子资源、403 响应体格式都在上游 | 代理只留请求准入接缝（G2），策略进 4zfence |
| `agent/session/protocol.go`（+394/−2） | 7 个载荷各一个解析函数，其中 3 个只是为了写包级注册表 | 一个请求装饰贡献点（G1），解析跟着消费方走 |

**用包级注册表传状态。** `core/scoperules`、`core/globalfence`、`core/egress` 里是进程级的全局表：`agent/session` 写，围栏和代理读，没有所有者管生命周期。指南明确不这么写。它还带来一个缺陷：两张围栏表容量都是 64，满了静默淘汰（§14 的缺陷修复清单）。拆开后状态由 4zfence `Provide`，随扩展关闭，随会话清理。

**`exts` 和 `tools` 互相伸手。** 判定接口 `ScopeJudge` 定义在 `tools/proxy`，实现在 `exts/guardrail`，接线在 `exts/proxy`，后者为此 import 了 `exts/guardrail`。拆开后 4zfence 实现判定接口，`exts/proxy` 只 `Provide` 一个判定入口，不再认识围栏。

落点：`core/scoperules`、`core/globalfence` 进 `exts/4zfence`，`exts/guardrail` 里新增的 11 个文件进 `exts/4zfence/policy`。

哪些补丁进哪块积木见附录 C、D，接缝的形状见附录 A。按钩子级标准（§1），`FORK.md` 的每条补丁都要有处置：**上游化**（提 PR，合入后从 fork 消失）、**改成挂钩**（逻辑挪进新增文件，上游文件里只留调用）、**外移到积木**（挪进 `exts/4z*` 或独立模块）、**保留**（说明为什么改不成挂钩）。处置表是 P1 的施工清单，每条按「能不能只走协议、能不能做成扩展、都不行才是钩子」的顺序追问（§1）。本仓 docs/03、04、14 按 harness 补丁号引用（22 = Hub 可内嵌的 Listener；23a/b/c = 围栏判定、保底审计与证据回传；26/27/28 = 种子、交接文件、artifacts 观测），全量清单以引擎仓 `FORK.md` 为准。

本仓侧要跟着改的：`internal/buildinfo/aopproto.go` 的 `AOPProtoFiles` 与 `aopalias.go` 的映射里，`internal/aop/guardrail` 指向引擎的 `exts/guardrail`。`guardrail.pb.go` 留在原处就不用动；若随判定逻辑一起搬，同步脚本、别名生成和 CONTRIBUTING 的说明都要改。

### 杠杆三：协议归一

删 vendored 协议副本，import 改指引擎包。范围是 7 个包、76 处 import，不只是 `internal/aop/pb`：

| vendored | import 处数 | 源 |
| --- | --- | --- |
| `internal/aop/pb` | 28 | 引擎 `aop` |
| `internal/aop/operation` | 12 | 引擎 `aop/operation` |
| `internal/aop/tool` | 12 | 引擎 `aop/tool` |
| `internal/aop/traffic` | 11 | 引擎 `aop/traffic` |
| `internal/aop/guardrail` | 6 | 引擎 `exts/guardrail` |
| `internal/aop/pty` | 4 | 引擎 `aop/pty` |
| `internal/aop/command` | 3 | 引擎 `core/types` |

处数含测试文件。连带删除：`scripts/sync-aop-protos.{sh,ps1}`；`cmd/zrcin-gate` 的 `-sync-aop-protos` / `-check-aop-protos` / `-aop-dry-run` 与 gate 第 7 步；`internal/buildinfo/aopproto.go` 与 `aopalias.go`；构建清单里的 `aop_proto`；`CONTRIBUTING.md` 与 `internal/aop/pb/README.md` 的对应说明；以及 `-tags inprochost` 的双形态（不再需要转发别名）。

**代价要先说清**：现在默认构建不 import 引擎，没有引擎检出也能编译和跑测试（`go.mod` 注释、CI 的 `-skip-harness`）。副本删掉后，外壳的每一次构建都硬依赖引擎模块——前提是引擎已经能按固定版本从远端拉取，杠杆一之后才成立。在那之前，vendored 副本加漂移检查仍然保证协议单源：gate 第 7 步会在副本与引擎源不一致时失败。

两部分代价不一样（已核实）：协议本身归到 `aop` 模块很便宜，它只带 3 个第三方依赖；配置校验归到 `pkg/config` 很贵，它会把 `agent/provider` 和 11 个第三方模块带进外壳，而且不在红线 3 列的引擎契约里。后者到 P2 再定（已决 21）。9 个载荷编解码文件不在这里等：它们不依赖引擎，P1-1 就搬。

同一把尺子用在协议上——**只增不改**（§1）。这一条 fork 已经做到了：7 个扩展载荷都走上游请求自带的 `extensions` 字段，上游 proto 一个字段没改（§8）。剩下的问题不在协议，在「谁来解析这些载荷」：上游的 `RunAOPTurn` 只认写死的 `AgentRunOptions`，`OpenAOPSession` 不读 `extensions`；fork 是在上游的 `agent/session/protocol.go` 里加了约 400 行解析自己的类型。处置见附录 C 和附录 A 的接缝 G1。

### 杠杆四：控制台归一

生产构建不编引擎 web；harness 的 web 仅在本地调试用 `full` 标签编译，不进分发包。`4zweb` 是唯一控制台。

本仓现状（已核实）：`cyber-scan` 已经不带 `full`；但 `Makefile` 第 21 行的 `TAGS` 给 `cyber-harness` 带了 `full`，gate 第 8 步构建 Hub 时也追加 `full`（`cmd/zrcin-gate/main.go`）。要做的就是去掉这两处，然后实测内嵌 Hub 在不带 `full` 时是否正常（待实测，见 §14）。

### 杠杆五：规则源自主

`c1ayooo/templates` 已经在自己的名下，现在是上游的镜像。要做的只有三件：开维护分支放自有规则、把子模块或生成参数指过去、引擎加 `make resources`。生成链路已验证可复现，规则源可以从一个逐字节一致的基线起步。约束与目录规则见 §5。

## 10. 红线

1. **fork 只增不改，钩子级** — 新增文件/目录允许；上游已有文件只允许登记在册的挂钩，每处不超过 20 行（初定）；`go.mod`、`Makefile`、`.goreleaser.yml` 的改动逐条记账。每条补丁在 `FORK.md` 里记处置、理由、回归测试、回滚，验收是 §1 的机器检查。现状离这条有距离：按 §8 口径，这三棵树里被改的上游文件非测试 30 个、测试 11 个（协议生成文件不在其内，它们只是 module path 噪音）。**钩子是过渡态**：每条登记出口（§1）
2. **一个扩展一个模块**，先同仓不同目录，成仓与否按 §3 判断；本地用 `go.work` 统管（尚未建立）；扩展只 import 引擎契约与标准库，**永不 import 4zrcin**。装配尽量在自己的模块里完成；必须在引擎里链接的，每个积木一行
3. **扩展只依赖引擎契约** — `aop`（协议），以及 `core/tool`、`core/extension` 这类注册接口；不 import 引擎的业务包（`agent/`、`tools/*`、`pkg/web/*` 等），也不形成包级 import 环。引擎的装配必须链接扩展，模块层面的互相 require 躲不掉，靠本地 replace 或 `go.work` 解决（已决）
4. **协议只有一份，只增不改** — 来自引擎 `aop/`，任何仓都不复制 pb；fork 加的协议内容只落在新增的 proto 文件或 anypb sidecar，不改上游消息
5. **扫描规则源只有一份**，在 `c1ayooo/templates` 的维护分支；引擎从中生成，节点拿编入规则的二进制。围栏保底审计语料单源在引擎（docs/04 §6，已决）。

## 11. 六阶段（P0–P5）

### P0 · fork 回退 module path（已完成，2026-11-27）

- 基线对齐已完成：`feat/remote-nodes` 快进进 `tianwu/fixes`（补上 webshell 接线的未提交改动），再合并 upstream 的 #205/#208（23 个冲突文件的解法登记在引擎 FORK.md「Upstream merge 2026-11-27」）
- 路径回退完成：引擎 `ce7f848d`（module 回退 + 30 个 pb.go 取上游逐字节一致版 + 11 个文件 gofmt 重排），4zwebshell `1a5272b`，本仓 `3916c09`（require/replace、buildinfo 常量、alias 目标、vendored 协议经 `-sync-aop-protos` 重新生成，漂移检查对准真实检出为绿）
- 实测：约 880 个 Go 文件的路径改写回退；§8 的数字更新为 178
- 验收达成：三仓 `go build ./...` 通过（外壳默认 + inprochost 单二进制 79MB；引擎 full + capture 155MB）；文件数 912 → 178
- 回滚：各仓单提交 revert
- 遗留：`tianwu/fixes` 有 14 个提交未推送 origin（第七版核对时仍是）。proto 工具链是现成的，此前写的「没有着落」不对：`make proto-gen` 跑 `cmd/gen`，protoc 与插件版本钉在 `cmd/gen/main.go`，新增 proto 文件要加进那里的列表
- 引擎合并时发现的既有问题（与本次无关）：`cmd/cyber-scan` 的部分组合测试在带 judge 的活代理上触发 `judgeRequest` 空指针 panic（net/http 恢复为 500，不挂测试）；已在合并前基线上复现

### P1 · 扩展外移（小步推进；各步的时间待估）

顺序是「不依赖接缝的先做」，每一步单提交、可单独回滚。只有 P1-4 需要围栏功能的短冻结。

**P1-0 缺陷修复（先行）。** 清单见 §14，各自带复现测试，不和搬家混在一起。围栏静默失效排第一，两个修法都做：会话关闭时清掉它的规则；表仍然满时拒绝这个会话的这一回合。

**P1-1 载荷契约包。** 把 `internal/aop` 根包的 9 个载荷编解码文件和引擎里同一批载荷的类型收进一个包，两边 import 同一份。它只依赖标准库和 anypb，先放引擎仓（纯新增的包，放 `aop` 模块目录下，外壳通过已有的 require 与 replace 引用）；要不要单独成模块，按 §3 判断。证据上限两边不一致随这一步消失。不依赖任何上游接缝。

**P1-2 4zevidence 与子会话继承。** 不依赖任何上游接缝：

- 证据回传做成独立扩展：自己订阅 `process.starting`、`process.completed`、`file.access`，自己发带 `4zrcin.Artifact` sidecar 的事件；`core/artifactsync` 随它搬，快照与差异比较从 `exts/observe` 挪出来。`--observe artifacts` 换成它自己的开关。`exts/observe` 恢复上游原样，「Any 套 Any」是上游 bug，单独提 PR。
- 子会话继承（补丁 33）改用 `SessionStart`：事件自带 `SessionID` 与 `ParentID`，围栏在订阅里把父会话的规则复制给子会话。`agent/subagent/sessionexec/tool.go` 的改动归零。实施时核对 `ParentID` 与父调用方的会话一致。

**P1-3 `4zweb` 拆仓。** 内容即本仓的 `web/`，提交历史用 `git subtree split` 带过去，外壳加 `--web-dir`，生产态沿用 §6.2 的 flate bundle 链路（§6）。它不碰引擎代码，所以放在围栏搬家之前，不和那段短冻结抢同一批人。不依赖任何上游接缝。

**P1-4 三个接缝，4zfence 与 4zsession。** 这一步需要围栏功能的短冻结：

- G1、G2、G3 先按钩子落地（形状见附录 A），同时提 PR；上游收了，钩子消失
- 4zfence：自己挂 `tool.before`，自己 `Provide` 状态，自己 `Declare` 配置节，实现代理的判定接口。4z 的产品配置里把上游 guardrail 设为 `provider: none`，让它只当审批队列；否则两个判定方都挂在 `tool.before` 上，上游排在前面，实例红线的拒绝会被它的「等人工审批」抢先
- 4zsession：WorkDir、SingleTask、RunGoal、CaseSeed、ToolEnv 的解析搬进扩展；ScopeRules、GlobalFence、EgressProxy 的解析跟着消费方走，进 4zfence，两块积木不互相 import
- 本仓侧：围栏配置节换名后，控制台的围栏设置卡同步改。迁移的规矩（已决 19）：读写两条路径都认旧写法，任何一次保存把它迁到新的配置节并记一条诊断，不设到期时间；不在启动读取时改操作员的文件。若 `guardrail.pb.go` 的归属变了，同步更新 `internal/buildinfo` 的映射
- 配置校验单源（已决 20）：围栏的配置结构和校验放进不依赖引擎的包，节点的 `Declare` 和外壳的设置保存共用一份，做法与载荷契约包相同。Hub 的配置接口不校验产品配置节，所以只能这样单源
- 验收：围栏拦截行为与 P0 完全一致，用同一批样本对比拦截率与误拦率，样本集要先建（本仓 `scripts/acceptance/` 里只有 UI 验收脚本）；旧配置按上面的规矩自动迁移，迁移前后判定一致；`git diff upstream/master...HEAD` 里上游已有文件的改动只剩 `FORK.md` 登记的钩子，且每处在行数上限内（§1 的机器检查进引擎 gate）
- 回滚：每块积木是装配里的一行，去掉即摘

**P1-5 装配包。** 配方孪生（`captureonly`）、`CYBER_EXCLUDE_TOOLS`、MCP 接线、关 recap、capability 标记收进一个可导入的新增包；`cmd/cyber-scan` 里每个触点收成一行，孪生文件随之搬出上游的 `package main`。不依赖上游。

### P2 · 协议归一 + 扩展独立成仓（2-3 天）

- 建父目录 `go.work`（4zrcin、引擎、4zwebshell、rem），把引擎的依赖 replace 提升到 workspace 层，让 inprochost 构建与引擎构建解析到同一批依赖（杠杆一）
- `4zwebshell` 进 workspace，确认能独立 `go build`
- 杠杆三：删 7 个 vendored 协议包、改 76 处 import，删除同步脚本与 gate 的 sync/check 分支。**前提是 P0 已完成且引擎可按版本拉取**，否则默认构建与 CI 断掉
- 引擎侧不涉及协议改动：fork 的协议本来就是只增不改（§8）。取而代之的是上游接缝的 PR（附录 A 的 G1–G8）
- 外壳去重（附录 B2）：前提与杠杆三相同——外壳无条件依赖引擎模块。配置布局跟上游 #205 改成顶层块；产品配置节（围栏、JEV、MCP、测绘）的校验随积木走（P1-4），Hub 不校验它们；公共配置的校验在这一步定（已决 21）：外壳 import `pkg/config`，或让 Hub 接管配置编辑；围栏和扩展载荷的结构体收进载荷契约包，两边共用；`internal/ioa/v1.go`、`internal/modelclient`、AOP 客户端改用引擎的包
- 验收：`go build`、全量测试、`scripts/gate.sh` 通过；`internal/aop` 只剩客户端骨架与领域包

### P3 · 单进程内嵌（已完成）

**已落地，不需要再做**（docs/03 M1、docs/04 T2）：外壳在 `-tags inprochost` 下通过 `internal/host/inproc` 把 Hub 跑在进程内，组装点是引擎的 `pkg/web/hubapp`（fork 补丁 22，`webhost.Config` 加可选 `Listener`）；`--harness` 显式给出时回到子进程模式，作为逃生舱；scan 节点仍是子进程。上一版写的 `pkg/harness.New` 是节点侧的组合根（扫描节点的扩展集），不是 Hub 的入口。

- 现状验收已达成：`bin/zrcin` 单文件起完整实例（docs/04 §3.1）
- 可选后续：给 Hub 一个不解析命令行参数的入口（现在内嵌走的是与 `cyber-harness` 命令相同的 argv）；验证进程内 Stop/Restart，为将来拆进程留口子
- 端口不会只有一个：console（8080）、Hub 回环监听（`--host-addr`）、可选的 relay 与 Postgres 各自独立

拆进程（保留为备选，触发条件：需独立发布引擎二进制 / 引擎多副本 / 崩溃隔离 / 各自热重启）：去内嵌、引擎独立进程、外壳走 AOP WebSocket，处理启动顺序与数据目录归属。

### P4 · 构建收敛 + 积木落地（3-5 天）

- `4zweb` 的拆仓已在 P1-3 完成，这一阶段只收它的构建脚本（`make web` 的源路径、gate 第 4、5 步的替代来源，见 §6.2）
- `4ztemplates`：不新建仓。在 `c1ayooo/templates` 上开维护分支，引擎加 `make resources`（§5）；产出保留 `!emptyresources` 孪生，两个资源包一起生成
- 构建脚本按 §7 落地，含 `rem-relay` 目标与 gate 第 8、10 步的替代来源
- 验收：上游 `git pull` 后只跑 `make`，不动任何源码；`make zrcin` 产出单二进制；前端改动不触发 Go 重编（开发态）；规则改动只重跑 `make resources`，再重编并分发 scan

### P5 · 外壳做成真模块（可选）

先加架构测试：把 §4 的层级表写成测试，越界即失败。纯新增，行为不变，可以随时做，不必等 P1。目录按层归并是移动加 import 改写，晚做或不做。边界立住之后，逐个模块用 §3 的三条标准判断是否成仓；满足的再拆，不设时间表。

## 12. 上游同步流程（解耦后）

```bash
# 引擎
cd ~/cyber-harness
git fetch upstream && git merge upstream/master   # 冲突应只出现在 FORK.md 登记的钩子文件与修复类补丁（目标状态，现状见 §8）
make engine                                        # 重编

# 规则（每月一次）
cd ~/templates                                     # 维护分支
git fetch upstream && git merge upstream/master    # 只在我们也改过的文件上冲突
cd ~/cyber-harness && make resources               # 重生成并比对；有差异就提交产物、重编 scan

# 外壳
cd ~/4zrcin
make gate && make zrcin
```

## 13. 可达程度、收益与代价

**确定能做到，现有官方接缝就够：**

- 4zevidence 独立成扩展，`exts/observe` 恢复上游原样
- 子会话继承改用 `SessionStart`，`agent/subagent/sessionexec/tool.go` 的改动归零
- 载荷契约包两边共用，证据上限、主机匹配器各写一份的漂移消失
- 规则源接管：仓库已有，生成链路已验证可复现
- 外壳的架构测试：层级表零例外
- 外壳协议归一里便宜的部分：`aop` 模块只带 3 个第三方依赖

**确定能做到，但留钩子（上游不接收就一直留着）：**

- G1、G2、G3 三个接缝，每个 20 行以内；4zfence、4zsession 的逻辑全部搬出上游文件
- `RunInput`、`SessionOptions` 的加性字段，约 60 行（G4）
- 组合根约 5 个单行触点加 2 个构建标签。这部分不依赖上游，本来就该留着

**做不到：**

- fork 的差异归零。组合根的触点和构建标签会留着；接缝和修复类补丁要等上游
- 改字典、zombie 规则、端口表不重编。指纹和 POC 有运行时通道，但不在本次范围（§5）
- 外壳自身业务的模块化。这次只给边界和测试，拆 `api` 不在范围内
- `4zreport`。仓库里没有实现，无从拆
- 围栏协议稳定之前，4zfence 单独成仓（§3）

**离终点的差距，写在上游文件里的量**（后三档是估算）：

| 阶段 | 被改的上游代码文件 | 写在上游文件里的行数 |
| --- | --- | --- |
| 现在 | 68 | +2,362 |
| 4z 逻辑全部搬出，上游一个 PR 都不收 | 约 64 | 约 990 |
| 修复类 PR 被合入 | 约 30 | 约 290 |
| 接缝与 `Listener` 也被合入 | 约 11 | 约 60 |

第一档到第二档文件数降得不多，但性质变了：剩下的要么是 20 行以内的钩子，要么是纯 bug 修复，要么是组合根的单行。

收益：

- 上游同步成本下降：冲突面从 110 个被改的上游文件，缩到附录 A 的接缝和修复类补丁所在的文件；能压到多少，取决于有多少被上游接收
- 能拿能换：每块积木一行装配，不写就不装
- 接错线当场报错：`Provide` 与 `Use` 在加载时检查，包级注册表那种静默失效不再有藏身之处
- 前端可以独立发版：开发态改前端不重编 Go；生产态是内嵌，换前端仍要重链接 zrcin，部署方用 `--web-dir` 时才真正免重编
- 规则源回到自己手里：加指纹有源可改。但规则编在 scan 二进制里，改规则仍要重编并分发节点
- 协议单份（杠杆三之后）；在此之前由同步脚本加漂移检查保证
- 包名即职责，AI 改代码的越界可以机器判定（外壳的层级表已定，见 §4）

代价：

- 份数增加后的版本矩阵，`go.work` 与 replace 要维护
- 构建从「一条命令」变「引擎、外壳两处 make，有顺序」
- P1 有行为等价性风险，必须样本对比
- 围栏搬家那一段需要短冻结
- 围栏的配置节换名；迁移在保存时自动完成，读写都认旧写法，但操作员会在文件里看到节名变了
- 上游不接收的接缝长期以钩子形式存在

## 14. 风险、已决与实施约定

风险：

- **围栏会静默失效**（已核实的缺陷，未在运行中复现）：见下方缺陷修复清单第一条
- **P1 行为等价性**：围栏搬家后必须用同一批样本验证拦截率与误拦率不变；样本集要先建
- **常驻补丁不止一个**：`agent/session/protocol.go` 只是其中之一。每条补丁所在的上游文件一改就要手工合，`FORK.md` 要记明理由与上游 PR 状态；真实清单以引擎仓 `FORK.md` 为准
- **replace 不传递**：内嵌 Hub 与 scan 的依赖来源不同，`go.work` 落地前每次发版都要确认
- **每次上游合并都会打断登记的钩子和修复类补丁**：#205 合入时是 23 个冲突文件。钩子压到 20 行以内之后，冲突面只剩附录 A 的接缝和修复类补丁所在的文件
- **上游化取决于上游是否接收 PR**：截至第七版，本仓给上游提过的 PR 是 0 个；上游已关闭 167 个，近期 25 个里有一份外部贡献被合入（#196），也有被关掉没合的（#209）。不被接收的退回钩子级，每处不超过 20 行，在 `FORK.md` 记明
- **钩子级标准会把现有的大补丁顶出来**：`agent/session/protocol.go`（+394 行）这类要先重构成「新增文件加一行调用」，是 P1-4 里最费工的部分
- **围栏配置节换名是操作员可见的变化**：迁移要自动、可验证，迁移前后判定一致
- **引擎分支未推送**：`tianwu/fixes` 有 14 个提交未推送；外壳的构建脚本从这个分支取引擎，未推送的提交只存在于本机

缺陷修复清单（与搬家分开，各自带复现测试）：

1. **围栏静默失效。** `core/scoperules` 与 `core/globalfence` 各用一张容量 64 的环形表存每个会话的规则（`core/scoperules/scoperules.go:376-381`、`core/globalfence/globalfence.go:159` 起）。第 65 个会话登记时，最早那个会话的规则被静默删除；会话关闭时不清；老会话每回合重发也不刷新位置。被淘汰的会话若正在回合中，这一回合剩余的流量不再判定，项目围栏（含「未命中即拒绝」）和实例围栏同时失效，没有任何日志。已决：两个修法都做——会话关闭时清掉它的规则，表里只剩活着的会话；表仍然满时拒绝这个会话的这一回合，不让任何会话在没有围栏的情况下跑。只加日志不够——登记失败后那个会话照样不判定。
2. **证据文件上限两边不一致。** 外壳 `internal/aop/artifact.go` 收到 16MiB，引擎 `core/artifactsync` 只发 8MiB。随 P1-1 由载荷契约包统一，建议取引擎侧的 8MiB，节点本来就不会发出更大的
3. **流量采集模式的默认值不一致。** 工作区未提交的改动里，`internal/settings/traffic_capture.go` 的 `normalizeCaptureMode("")` 返回 `relay`，读取路径与差异判断默认 `record`。提交前处理

已决（2026-11-27）：

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
16. **规则源只做编译期接管**，「改规则不重编」不列入本次范围。
17. **`4zweb` 拆仓放在 P1-3**：载荷契约包和 4zevidence 之后、围栏搬家之前。它不碰引擎代码，不和围栏那段短冻结抢同一批人。
18. **围栏静默失效的两个修法都做**：会话关闭时清理；表满时拒绝回合，不让任何会话在没有围栏的情况下跑。
19. **围栏配置迁移**：读写两条路径都认旧写法；任何一次保存把它迁到新的配置节并记一条诊断；不设到期时间；不在启动读取时改操作员的文件。
20. **产品配置节的校验随积木走**：配置结构和校验放进不依赖引擎的包，节点的 `Declare` 和外壳共用一份。依据（已核实）：Hub 的配置接口不校验产品配置节——`pkg/web/host/config_store.go` 的 `SharedConfigCodec` 只暴露公共设置并转发网关配置，具体服务的 schema 归执行节点；外壳现在各抄一份（`internal/settings` 的 `guardrailSectionKeys`、`validateJEV`、`validateMCP` 等）。
21. **公共配置的归属这次不定**：P1 期间外壳继续自己校验公共配置，P2 协议归一时再定。两条路：外壳 import 引擎的 `pkg/config`，会带进 `agent/provider` 和 11 个第三方模块，而且不在红线 3 列的引擎契约里；或者让 Hub 接管配置编辑，那等于改配置文件的归属——外壳改的是用户级的 `~/.cyber/cyber.yaml`，Hub 的 `FileConfigStore` 改的是启动时 `--config` 指的那份运行时文档。
22. **终点的范围**：引擎里我们加的能力做成一行装配的扩展，共五块；外壳是宿主，规则是数据，前端是产物，引擎内核是框架本体，都不做成扩展。扩展是编译期静态组合的，不做运行时热插拔。

实施约定：

- **目录范围**：P1 动引擎仓，P1-3 新建 4zweb 仓；规则仓只开维护分支；P4 只收 4zweb 的构建脚本；除此之外不碰别的目录。
- **基线分支**：`tianwu/fixes`。本仓脚本固定从这里构建；`feat/remote-nodes` 已合入。
- **仓库名**：`cyber-harness` 与 `templates` 都不改名，`4zengine`、`4ztemplates` 只是本文档里的逻辑名。
- **4zweb 建仓**：`c1ayooo/4zweb`，公开（与本仓、引擎仓一致）——建仓前再确认一次。
- **不动的文件**：`docs/18-webshell-management.md` 是 webshell 的文档，与本计划无关。
- **开工状态**：`internal/settings` 有未提交的改动（配置布局迁到顶层块，对齐上游 #205），P1 开工前先提交或收尾。
- **Hub 内嵌保留**：它解决的是单二进制，不是通信，AOP 替代不了它。不想保留这个钩子时，回到 Hub 独立进程——外壳的 `--harness` 路径现在就能用。

未定的只剩一件：

- P1 各步的时间估算。P2、P4 沿用早先的天数；P1 重排成六步之后没有重估。

待实测（不需要决策，实施时验证）：

- 引擎 web 关掉后，内嵌 Hub 在不带 `full` 时是否正常（杠杆四）。
- 载荷契约包放进引擎 `aop` 模块目录后，外壳默认构建是否因此需要引擎检出。

## 附录 A · 上游已经提供的积木接口

对照对象是上游 `chainreactors/cyber-harness`。2026-11-27 已合并 `86c5acea`（#205 gateway、#208 passive），fork 落后 0。路径相对引擎仓。下列接口 fork 均未改动：`core/extension`、`core/resource`、`core/hooks`、`core/tool/hooks`、`agent/hooks`、`core/namespaces`、`aop`、`pkg/profile`、`pkg/node`、`pkg/harness`、`pkg/web/service`、`exts/web`。

**扩展（`core/extension`）。** 一个扩展就是实现 `Load(*Scope) error` 的类型，可选实现 `Close(context.Context) error`。`Set` 按声明顺序加载、逆序关闭，没有 ID、依赖图或服务表。它能用的入口只有 `Define[T]`（定义贡献点）、`Add[T]`（往贡献点里加）、`Provide[T]`（发布）、`Use[T]`（借用）、`scope.Init()`、`scope.Lifetime()`。约束（已读源码）：

- 同一类型不能重复 `Define` 或 `Provide`；`Add` 与 `Use` 只能指向更早加载的扩展定义的类型
- `Use` 是全量的：没人提供就加载失败。上游明确要求用「不装」或「提供空实现」表达可选，不许容忍缺失
- 能力的键必须是提供方包自己声明的具名类型，不能用内置类型或第三方的类型
- 加载完成后不能再 `Define`、`Provide`、`Use`；已有的贡献点仍可 `Add`

**钩子（26 个点）。** `core/tool/hooks/points.go` 16 个，`agent/hooks/points.go` 10 个，处理函数统一是 `func(context.Context, E) (R, error)`，按登记顺序执行。能否决的是 `tool.before`、`command.before`、`process.before`、`process.started.control`、`file.access.control`、`http.completed.control`、`model_request_policy`、`session_start`、`before_compact`、`inbox_wait`；`tool.after` 可以改写结果副本；其余只能观察。`tool.before` 是「先拒绝者胜」：第一个给出拒绝的处理函数生效，后面的跳过；它现在有两个登记方，上游围栏（基础组合第 7 个）和 `jev-supplement`。`SessionStart` 在会话打开时发出，事件带 `SessionID` 与 `ParentID`，处理函数出错则这次打开失败。

**AOP 命名空间（`aop.Binding`）。** 功能扩展用 `extension.Add(scope, aop.Binding{Prototype, Open})` 贡献一个顶层命名空间。同一命名空间只允许一个 binding，注册表与连接级 mux 各拦一次，扩展无法包装别人的命名空间。

**Web（`service.Capability`）。** 接口是 `ID`、`Manifest`、`SchemaModules`、`Routes`、`RegisterNamespaces`、`Close`，上游称它为「可独立部署的 Web 功能」的服务端接缝。上游没有任何生产代码实现它，只有测试，所以第一个真实实现要自己承担接缝不完整的风险。它的 `RegisterNamespaces` 拿不到 Hub 发往节点的通道（见本节末「已排除的做法」）。

**配置（`pkg/config`）。** 加载、校验、原子保存；`DistributeConfig` 从 Hub 分发到节点，Hub 上有配置接口。功能包约定提供一个 `Declare(resources)`，往宿主预先定义的 `cfg.Section` 贡献点加自己的配置节。宿主冻结并解析之后才构造运行扩展。

**组合根。** `pkg/harness.New`：在自己的 Go 应用里嵌入会话运行时，基础组合 14 个扩展，`BaseExtensions` 返回有序列表。`node.RunWebSocket` 加 `profile.Profile`：自定义节点。`webhost.Serve` 加 `webext.New`：在自己的应用里托管 Hub；`webhost.Config` 有 `StartNode`、`Ready`、`Management`、`Extensions`，没有 `Listener`（fork 补丁 22 加的）。

**协议。** `OpenSessionRequest`、`RunTurnRequest` 自带 `extensions []*anypb.Any`（`aop/chat.pb.go:160`、`:326`）；事件的 Envelope 也带 `Extensions`，`Event_Extension` 是扩展自带事件的通道。

**分层有测试守着。** `pkg/internal/architecture` 禁止 `agent/` import `pkg/`、`exts/`、`tools/`、`cmd/`、`internal/`、`core/extension`，并要求 `agent/` 不出现发行版身份；`pkg/config` 不得 import `exts/`、`cmd/`、`tools/`。fork 目前通过这个测试——围栏的三个注册表放进 `core/`，就是因为 `agent/session` 只能 import `core/`。

### 上游缺的接缝

fork 需要挂钩的地方，上游都没有现成接口。下表就是给上游的 PR 清单；合入之前，每一条在 fork 里按钩子级保留。

| 编号 | 缺什么 | 上游现状 | 形状 |
| --- | --- | --- | --- |
| G1 | 会话请求里扩展载荷的解析注册点 | 硬编码：7 个载荷各一个未导出的解析函数，按 type URL 逐个字符串匹配（`agent/session/protocol.go`）；`RunAOPTurn` 只认写死的 `AgentRunOptions`，`OpenAOPSession` 不读 `extensions`；一个命名空间只能有一个 binding，扩展无法包装 | 在 `agent/session` 声明请求装饰类型：打开会话 `func(*aop.OpenSessionRequest, *SessionOptions) error`，跑回合 `func(*aop.RunTurnRequest, *RunInput) error`；由会话扩展定义贡献点，`OpenAOPSession`、`RunAOPTurn` 各留一个循环，装饰出错即拒绝。类型必须声明在 `agent/session`，因为架构测试只允许 `agent/` import `core/` |
| G2 | 代理的请求判定接缝 | 只有 `SetProxyAuth`（`tools/proxy/hub.go:262`），没有生产调用方；它只拿到 `*http.Request`，看不到会话归属，不能挂起，只能以 407 拒绝 | 接口与 setter 放进 `tools/proxy` 的新文件（纯新增）；`Requestheaders` 只留调用；判定方的原话作为不透明值随 `http.completed` 事件带回订阅者 |
| G3 | 复用围栏审批队列的公开入口 | `*Runtime` 导出了 `Admit`、`Pending`、`Resolve`，没有「提交一条审批」的方法；`newRuntime` 未导出 | 导出一个方法，包住已有的私有 `review` |
| G4 | 按会话的工作目录、轮次种子、工具环境、评估目标 | `Invocation.WorkDir` 这个接缝在，但会话不填；`RunInput`、`SessionOptions` 没有对应字段 | 上游请求加 `AgentRunOptions.eval_goal`；`RunInput`、`SessionOptions` 加加性字段 |
| G5 | 按工具名排除 | 只有按组开关的 `--tools`、整次请求禁用的 `DisableTools`、逐次调用的 `tool.before` | 不需要：属于装配，组合根里不装 |
| G6 | 可导入的节点装配 | `cmd/cyber-scan` 的装配全在 `package main` 里；自定义节点要自己实现 `profile.Profile`；上游明确不再提供发行版构造包 | 不强求：组合根的触点收成单行即可 |
| G7 | 不受采集开关影响的 HTTP 流量观察 | 钩子在关闭采集或被过滤时直接跳过 | — |
| G8 | 新的 provider 类型 | 构造写死 anthropic 和 openai | provider 支持额外请求头 |

已排除的做法（查过源码，不必再试）：

- **给积木开自己的 AOP 命名空间来下发围栏规则。** `aop.Binding` 是官方机制，但 Hub 夹在控制面和节点之间：它的应用侧分发器只转发写死的命名空间（`pkg/web/service/application_dispatch.go:372-384`：core、command、guardrail、jev、file、scan、pty），其余回 `UNSUPPORTED_NAMESPACE`。每个被转发的命名空间各有一个 forward 函数，用的是 Hub 内部未导出的 `s.agents.dispatchMessage`，`service.Capability` 拿不到。要让 Hub 转发新命名空间，得改两个上游文件，入侵面不比 G1 小。sidecar 搭在上游请求上是唯一不改 Hub 就能穿过 Hub 的路径。
- **用 `SetProxyAuth` 当判定入口。** 它看不到会话归属，不能挂起等审批，只能以 407 拒绝。
- **让 4zfence 自带一套审批队列。** 官方机制够用，但会把「挂起、超时、事件、RPC、控制台绑定」复制一份，变成两个浅模块。G3 只是一个方法。

## 附录 B · 重复的轮子

### B1 · fork 的补丁和上游重叠

| 重叠点 | fork 做了什么 | 上游有什么 | 处置 |
| --- | --- | --- | --- |
| MCP 配置（补丁 17） | 新增 `exts/mcp/config.go`，用 `mcpServers` 键，改了装配根 | #205 正式加了 MCP 配置，键改成服务名直接作键，`mcpServers` 被废弃 | 合入 #205 后删除补丁 17，配置迁到新格式 |
| `on_error`（补丁 18） | 给 LLM 判定加了 `guardrail.on_error`，默认 `review`，可取 `allow` | JEV 规则已有 `on_error`，默认 `block`，只能 `review` 或 `block`（`exts/guardrail/jev.go:21`） | 合并成一个键，语义以上游为准 |
| `eval_rounds`（补丁 15） | `RunGoal` 载荷里又带了一份 | `AgentRunOptions.eval_rounds` 已有 | `RunGoal` 只留 `goal` |
| capture 配方（补丁 24、25） | `captureonly`、`emptyresources` 构建标签和孪生文件 | 运行时开关 `SkipEngines`（`cmd/cyber-scan/config.go:39`，只有测试在用）；hello 的 `capabilities` 可以用 `RunWebSocketWithCapabilities` 声明 | 上游化：把 `SkipEngines` 做成可用配方；标签只保留体积裁剪那部分 |
| 代理判定（补丁 21、31、33、34、36） | 新起 `ScopeJudge` 接口，改了 `mitm.go`（+171 行）、`hub.go`（+112 行） | 已有请求准入回调 `SetProxyAuth` | 上游化：把接缝做成通用的（G2） |
| Hub 内嵌（补丁 22） | 新增 `pkg/web/hubapp` 重新打包命令的装配，`webhost.Config` 加 `Listener` | 给嵌入者的路径是 `webext.New` 加 `webhost.Serve`，`Config` 有 `StartNode`、`Ready`，没有 `Listener` | 上游化：提 `Listener` 和可导入的装配 |
| 种子（补丁 26） | 按轮次的 sidecar 种子 | hub 到 node 的 `UploadRequest`，单文件，落临时目录 | 功能不同，保留 |

命名要避开一处：上游的 artifact 指扫描器的发现结果，fork 的 artifacts 指证据文件，同名不同义。

### B2 · 外壳手抄引擎

根因只有一个：外壳的默认构建不 import 引擎模块（`go.mod` 的注释写明了），所以引擎的每样东西都在外壳里抄了一份，抄的那份随后漂移。

| 外壳 | 抄的是引擎的 | 已经漂移的地方 |
| --- | --- | --- |
| 12 个 vendored `*.pb.go`；`internal/ws` 自实现了 RFC 6455；`internal/aop` 的信封收发 | `aop` 模块、`pkg/aopconn`、`pkg/aopws`、`pkg/rpc` | PTY 流 ID 的分支外壳 13 个、引擎 16 个 |
| `internal/settings` 的 `validateTraffic`、`validateMCP`、`runtimeRootKeys`、`guardrailSectionKeys`、`readHarnessModel` | `pkg/config` 导出的 `Validate`、`Normalize` | `validateMCP` 少一条上限检查；围栏键少三条约束；MCP 的格式上游 #205 已整个换掉 |
| 外壳往 cyber.yaml 写 `extensions:` 嵌套布局（`internal/settings/node_proxy.go`） | 上游 #205 起 section 是顶层块，字面的 `extensions` 键直接报错（`pkg/config/distribute.go:33`） | 合入上游后外壳写出的配置会被拒绝 |
| 围栏和 7 个扩展载荷的结构体（`internal/project/scope_rules.go`、`internal/settings/fence.go`、`internal/aop/{scoperules,globalfence,workdir,seed,rungoal,toolenv}.go`） | fork 的 `core/scoperules`、`core/globalfence`、`agent/session/protocol.go` 里的同名结构体 | 主机匹配器各写一份；证据文件上限外壳 16MiB、fork 8MiB；外壳等理由里带 `rule=<id>` 前缀，fork 从不发 |
| `internal/modelclient` | `agent/provider` | 配额关键词表两边不一样；OpenCode 的请求头两边各写一遍 |
| `internal/ioa/v1.go` | `core/collaboration` | `omitempty` 对 `omitzero`；校验规则两边不同 |
| `internal/aopexec`、`internal/executor` 里两套事件解码和手写的 protowire | 生成的 `core/types` 消息 | 同一件事两份实现 |

外壳的 `validateTraffic` 上方注释写得很直白：「Duplicating the rule is intentional — the control plane writes the file without the harness package available」。

外壳自己的东西不算重复：连接阶段的重试、终端打开重试、游标跟踪、HAR 导入、内容寻址的证据库、事实账本、进程监督、意图调度与准入、逐条事实的复核。

## 附录 C · fork 补丁处置表

编号沿用 `FORK.md`（里面「25」出现了两次）。处置分五种：**上游化**（提 PR，合入前按钩子级保留）、**积木**（逻辑搬进 `exts/4z*`）、**删除**、**装配**、**保留**。

| # | 补丁 | 上游现状 | 处置 | 落点 |
| --- | --- | --- | --- | --- |
| 1 | continued turn 的输入进收件箱 | 上游的 bug：`continue_session` 为真时丢弃 `input` | 上游化 | — |
| 2 | usage 带上 `context_window` | 上游只报已用 token，不报窗口 | 上游化 | — |
| 3 | 按会话的工作目录 | 载荷走上游 `extensions`；有 `Invocation.WorkDir` 接缝但会话不填 | 上游化（G4） | 解析进 4zsession |
| 4 | 慢端点也能激活 | 上游的 bug | 上游化 | — |
| 5 | `CYBER_EXCLUDE_TOOLS` | 上游没有按工具名排除（G5） | 装配 | 装配包：组合根里不装，不用 `tool.before` 去拒 |
| 6 | 原生命令的调用目录 | 上游的改进 | 上游化 | — |
| 7 | null 设备与环境约定 | curl 部分是上游 bug；提示词文字是 4z 的 | bug 上游化，文字进积木 | 4zsession 的 `prompt.Contribution` |
| 8 | 外部调度的邮箱 | `SingleTask` 是上游字段，载荷走 `extensions` | 改挂钩（G1） | 4zsession |
| 9 | 配额 429 快速失败 | 上游的 bug | 上游化 | — |
| 10 | OpenCode 路由 | 上游没有 OpenCode，provider 类型写死（G8） | 上游化：provider 支持额外请求头 | 在配置里解决 |
| 11 | 扫描测试与清单对齐 | 纯测试 | 上游化 | — |
| 12 | 会话稳定的系统提示词 | 上游的改进，关乎前缀缓存 | 上游化 | — |
| 13 | 接受 4zrcin 的 provider 元数据 | 与上游「未知字段报错」相反 | 删除 | 外壳把自己的元数据移出 cyber.yaml |
| 14 | 配置测试隔离 | 纯测试 | 上游化 | — |
| 15 | RunTurn 带评估目标 | 载荷走 `extensions`；上游只有 `eval_criteria`、`eval_rounds` | 上游化（加 `eval_goal`） | 过渡期进 4zsession |
| 16 | 关掉 recap | 上游没有开关（G6） | 装配 | 自己的组合根，或上游加 `recap.enabled` |
| 17 | 外部 MCP 配置 | 上游 #205 已有 | 删除 | 用上游 |
| 18 | 可插拔的围栏判定 | 上游没有策略注册表 | 积木 | 4zfence：自己挂 `tool.before`；审批队列要 G3 |
| 19 | neutron 快速参考文案 | 文档 | 上游化 | — |
| 20 | 兼容两个命名空间 | 自己的历史包袱 | 随 G1 收敛 | 控制面只发 `4zrcin.*` |
| 21 | 范围规则判定代理流量 | 上游没有判定接缝（G2） | 积木，接缝上游化 | 4zfence |
| 22 | Hub 可嵌入 | 给嵌入者的路径缺 `Listener` | 上游化 | 合入前是登记钩子 |
| 23a | CIDR 行 | 上游没有 | 积木 | 4zfence |
| 23b | 保底审计 | 上游没有 | 积木 | 4zfence，语料随行 |
| 23c | 证据 sidecar | 上游只有 hub 到 node 的上传 | 积木 | 4zevidence，不需要上游接缝；顺带修的双层 Any 上游化 |
| 24 | capture 配方 | 与 `SkipEngines` 重叠 | 上游化加装配 | 体积裁剪留作构建标签 |
| 25 | hello 宣告配方 | 上游有 `capabilities` | 装配 | `RunWebSocketWithCapabilities` |
| 25（重复编号） | FORK.md 文案 | 文档 | 保留 | FORK.md |
| 26 | 轮次种子 | 上游没有（G4） | 上游化，过渡期改挂钩（G1） | 4zsession |
| 27 | 交接文件回传 | 上游没有 | 积木 | 4zevidence |
| 28 | artifacts kind 单独可用 | kind 单独启用时不订阅，是上游 bug | bug 上游化，功能进积木 | 4zevidence |
| 29 | 域名行覆盖裸主机 | 上游没有 | 积木 | 4zfence |
| 30 | 默认未命中即拒绝 | 上游没有 | 积木 | 4zfence |
| 31 | 实例级围栏 | 上游没有 | 积木 | 4zfence |
| 32 | webshell | 已是独立模块 | 装配 | 4zwebshell：保留一行链接，移到自己的组合根（G6） |
| 33 | 判定原样传递、子会话继承 | 改了上游的代理和 subagent | 接缝上游化（G2）；继承改用 `SessionStart`，上游改动归零 | 4zfence |
| 34 | 被动子资源放行 | 改了上游的 `mitm.go` | 积木，接缝要带上请求头 | 4zfence |
| 35 | 命令红线只判类命令调用 | 改了上游的围栏运行时 | 积木 | 4zfence |
| 36 | 控制面自身调用豁免 | 改了上游的代理 | 积木，接缝要带上主机 | 4zfence |
| 37 | 全局默认放行 | 上游没有 | 积木 | 4zfence |

合计 40 条（23 拆成三条）：上游化 15 条，进积木 15 条，删除 2 条，装配 4 条，保留 1 条，混合 3 条（7、8、20）。

## 附录 D · 拆出来的积木和装配点

| 积木 | 装进去的补丁 | 怎么接上引擎 | 需要的上游接缝 |
| --- | --- | --- | --- |
| 载荷契约包 | 7 个请求载荷与 Artifact、TrafficVerdict 的类型，被多条补丁共用 | 外壳和引擎 import 同一个包 | 无 |
| 4zevidence（证据回传） | 23c、27、28 | 自己订阅 `process.starting`、`process.completed`、`file.access`，自发带 sidecar 的事件，不改 observe | 无 |
| 4zfence（围栏） | 18、21、23a、23b、29、30、31、33–37 | `tool.before`；`SessionStart` 做子会话继承；`Declare` 自己的配置节；借用上游的审批队列 | G1（读自己的三个载荷）、G2、G3 |
| 4zsession（会话载荷） | 3、7 的文字部分、8、15、20、26 | 请求装饰贡献点；`prompt.Contribution` | G1、G4 |
| 4zwebshell | 32 | 已是独立模块，装配时链接一行 | 无 |
| 装配包 | 5、16、24、25 | 组合根 | 无（G6 可选） |
| Hub 内嵌 | 22 | `webhost.Serve` 加 `Listener` | 上游化 |

三点说明：

- ScopeRules、GlobalFence、EgressProxy 三个载荷的解析进 4zfence，不进 4zsession：它们是围栏和出口代理的配置，解析跟着消费方走，两块积木就不需要互相 import。
- 4zfence 是唯一的判定方，上游 guardrail 在 4z 的产品配置里设为 `provider: none`，只当审批队列。`tool.before` 按登记顺序执行、先拒绝者胜，上游排在基础组合的第 7 个，两个判定方并存时它的「等人工审批」会抢在实例红线的拒绝前面。
- 4zfence 的判定配置搬进自己的配置节，代价是操作员的 `cyber.yaml` 迁移一次（已决 15）。

代理里的改动分两类，拆法不同。通用的接缝——`ScopeJudge` 接口、在请求钩子里调用判定、支持挂起等审批、把合成响应写回流量——是给上游的 PR。4z 自己的策略——控制面主机豁免、按 `Sec-Fetch-Dest` 判断被动子资源、403 响应体的格式、对 `core/scoperules` 类型的依赖——整体搬进 4zfence。

建议的路径分两步。先把这些逻辑挪进引擎仓的 `exts/4z*` 目录：全是新增文件，上游已有文件里只留接缝的调用，「只增不改」在这一步达成。接缝上游化之后，再把 `exts/4z*` 提成独立模块，做法和 4zwebshell 一样；要不要提，按 §3 的标准逐个判断。

## 附录 E · 实施审查（2026-11-27）

对象是已提交的实施：引擎 `eca06a22`、外壳 `401053c`，两仓工作区干净、均已推送。路径相对各自的仓，行号取自这两个提交。

方法：两路独立审查（规范、规格）先在引擎 `510befe8`、外壳 `55fb796` 上各出一份报告，之后每一条都在最终提交上重量；测试和复现都跑在仓库外的快照里，全程只读，没有改动两个仓的任何文件。

结论：**骨架按设计搭起来了，围栏搬家的等价性验收成立；但有 1 个已复现的回归、1 处顺序缺陷，以及若干与设计相反的做法。** 第十八版说「P1 验收条款至此全部满足」，按本附录的实测不成立。

### E1 测试与验收证据

- 重点包的测试在最终提交上全部通过，共 41 个包，含引擎和外壳的架构测试。排除一个依赖未初始化子模块的互通性测试，与改动无关。
- 围栏等价性门禁独立复跑通过：`scripts/acceptance/fence-parity.sh` 在仓库外的克隆里跑，搬家前的 `c0b54f5e` 与当前提交 16 条判定逐字一致。这满足已决 14 对围栏搬家的要求。
- 门禁有盲区：它比对判定函数的输出，不覆盖判定结果怎么发上线、会话轮换和配置迁移。E2 的前两条它都测不到。

### E2 必须处理

| # | 问题 | 证据 | 违反的条款 |
| --- | --- | --- | --- |
| 1 | 回归，已复现：围栏的流量判定结果在线上不可见。`observe` 恢复上游时，「Any 不套 Any」的修复一并消失，判定结果仍走 `observe.flowCompleted` 发出，类型变成 `google.protobuf.Any` | `exts/observe/extension.go:150-157` 的 `eventFor` 与上游逐字相同；`:255-262` 传入预构建的 `4zrcin.TrafficVerdict`。把一个预构建的 sidecar 传给 `eventFor`、断言 `event.Extensions` 里有它的 type URL，测试失败。第十版说该修复随钩子保留，实测不成立；`exts/observe` 没有这条线的测试 | 已决 14（行为逐项不变） |
| 2 | 会话轮换后围栏丢一轮。`rotate` 先关旧会话，触发围栏的关闭清理；再开续接会话，继承时已无物可继承。`/clear`、`/compact`、续跑恢复都走这条路 | `agent/session/session.go:1199-1205`；外壳的续跑恢复会调用它（`internal/api/session_rpc.go:159`）。读代码确认，未在运行中复现。下一个回合会重新登记，丢的是轮换到下一回合之间那段 | 已决 18（不让任何会话在没有围栏的情况下跑） |
| 3 | 外壳提交 `789f8a3` 混入 56 个无关文件，并已推送 | 60 个文件里只有 4 个属于那次改动。其余是根目录 18 个界面快照 YAML、`.playwright-cli/` 15 个、`.scratch/` 19 个，以及四个编译出的二进制（`zrcin`、`zrcin-metrics`、`zrcin-node`、`zrcin-repair`，共约 100MB）。抽查命中敏感字段名的都是界面文字，没看到真实密钥，但没有做全量密钥扫描。`.gitignore` 只挡了 `/bin/` 和 `/platform.yaml`；更早的 `tw`、`tw-gate`、`tw-repair` 三个二进制也一直在库里 | `agent.md` §3「提交按功能成批」 |

### E3 与设计相反的做法

修订记录第十三至十五版把这些写成了有意的做法，设计正文没有跟。每一条要定：接受并改正文，还是改回代码。

| 做法 | 修订记录怎么说 | 设计怎么写 | 实测与后果 |
| --- | --- | --- | --- |
| 上游的通用包反过来 import 4z 积木 | 第十五版：接缝类型下沉到「中立地带」，`tools/proxy` 以别名引用 | 附录 A 的 G2：接口与 setter 放在 `tools/proxy` 的新文件；红线 3 | 10 个非测试文件：`tools/proxy` 四个、`exts/observe`、`exts/proxy`、`pkg/harness` 两个、`exts/guardrail` 两个。装配里去掉一行只能让它不加载，摘不掉 |
| 围栏和会话积木写进 `pkg/harness` 的基础组合 | 第十三版：每个装配根一行 | §2 和附录 D：产品组合根里一行装配 | `harness.New` 的每个嵌入方都带上围栏 |
| 判定留在上游 guardrail | 第十四版：`Admit` 剩纯策略关注点 | 附录 D：4zfence 是唯一的判定方，guardrail 设为 `provider: none`，只当审批队列 | `exts/guardrail/runtime.go` 仍 +199、`extension.go` +128；`policy*.go` 仍在 guardrail 包里 |
| 规则表是包级全局表，只是搬进了积木 | 第十三版只记了搬迁 | §1 设计范式第 4 条：不再用包级注册表传状态 | `exts/4zfence/scoperules` 里 `storeSets`、`verdictByFlow` 仍是进程级的；扩展 `Close` 不清表；`SetForTest`、`DropForTest` 留在生产包里 |
| 4z 自己的代理策略留在 `tools/proxy` | 第十三版：判定逻辑收进 `scope_judge.go` | 附录 D：整体搬进 4zfence | 控制面豁免、被动子资源、403 响应体都在上游包的新文件里 |
| 审批队列靠手工 `Bind` 接上 | 第十四版 | §1 设计范式第 4 条：借用拿不到就加载失败 | `exts/proxy/extension.go` 吞掉了 `Use` 的错误，接错线不会在加载时暴露 |

### E4 钩子预算与登记（红线 1）

| 上游文件 | 现在剩下的新增行 | 设计目标 |
| --- | --- | --- |
| `agent/subagent/sessionexec/tool.go` | 与上游一致 | 归零 |
| `tools/proxy/hub_traffic.go`、`pkg/web/host/host.go` | +10、+14 | 达标 |
| `tools/proxy/hub.go` | +21 | 约 +15 |
| `tools/proxy/mitm.go` | +39 | 约 +12 |
| `exts/observe/extension.go` | +23 | 0 |
| `agent/session/protocol.go` | +111 | 约 +12 |
| `exts/guardrail/runtime.go` | +199 | 约 +10 |
| `exts/guardrail/extension.go` | +128 | 0 |
| `pkg/harness/base.go`、`harness.go`、`pkg/config/sections.go` | +20、+8、+14 | 设计里没有这三处 |

- 设计要求的「未登记即失败、每处不超过 20 行」的机器检查没有做。
- `FORK.md` 没有登记 `pkg/harness/base.go`、`pkg/harness/harness.go`、`pkg/config/sections.go`，也没有登记 4zevidence 和围栏配置包。
- 被删的上游测试 `exts/guardrail/config_test.go` 仍是删除状态。
- 组合根 `cmd/cyber-scan` 相对上游仍是 25 个文件、8 个非测试文件被改；设计目标是约 5 个单行触点加 2 个构建标签。

### E5 其余发现（规范）

外壳：

- `--web-dir` 只换了一半：`index.html` 读目录，`/static/` 仍由 `Static()` 读内嵌包，目录版的 `dirStatic` 写了没人调用。前端一改，资源哈希就对不上。偏离 §6.1「只替换读字节这一步」。
- 前端拆仓后 `CONTRIBUTING.md`、`README.md`、`docs/02-deploy.md`、`.github/workflows/ci.yml` 仍写根目录 `pnpm install`，根目录已经没有 `package.json`。
- `ZRCIN_WEB_DIR` 在 gate 里指 4zweb 的检出，在生成器里指构建产物目录，同一个变量两个含义。
- `migrateProductSections` 把旧的 `extensions.traffic` 整个抬到顶层 `traffic`：顶层已有 `traffic` 时旧块被丢弃，没有时会带进引擎不认的字段。条件性风险，未复现，测试只测了读路径。
- 新的架构测试里内层变量遮蔽了外层的 `path`，报错不指向违规行。
- 已决 19 要求迁移时记一条诊断，`internal/settings/fence_section.go` 里没有看到。

引擎：

- 引擎 `AGENTS.md` 禁别名、转发层和只给测试用的 helper：`NewRuntime` 只给测试用，`SubmitReview` 是纯转发，`tools/proxy/hub.go` 有类型别名。
- `4zevidence` 仍 import `exts/observe`；设计要求把快照与差异比较挪出来。
- `docs/configuration.md` 仍把 LLM 裁判键写在 `guardrail` 下；`exts/observe` 的钩子注释自称不超过 20 行，实测 +23。
- `--observe artifacts` 仍是证据积木的开关，没有换成它自己的。
- 请求装饰做成了 `RuntimeConfig` 的字段，设计要求的是贡献点。偏离较小。
- `core/egress` 的会话路由表仍是包级注册表，第十四版已注明留待红线稳定后再收。

两仓共有的判断项：`internal/aop` 下的别名与转发；`"http.request"` 定义了两次；搬过去的代码里有按关键词特判命令字段（`agent.md` §3）。

### E6 核对无误

- 协议零改动；`agent/subagent/sessionexec/tool.go` 与上游一致。
- `core/scoperules`、`core/globalfence` 两个包级注册表已从 `core/` 删除。
- 载荷契约包两边共用，外壳直接 import `aop/payloads`；围栏配置包 `exts/4zfence/config` 不依赖引擎；证据上限统一为 8MiB；流量采集的默认值已统一。
- `/api/version` 带上了前端的来源信息；gate 第 5 步已改为条件执行。
- 外壳的层级表架构测试与 §4 一致，零例外。
- 已决 15、19、20 的读和写两侧都已做：引擎识别旧写法并在内存里迁移，外壳保存时迁到 `fence` 节。

### E7 正文与修订记录不一致

正文还停在实施之前，需要按实测更新：

- 附录 D 写「4zfence 是唯一的判定方」，附录 A 的 G2 写接缝类型放在 `tools/proxy`；修订记录说的是另一种做法（E3）。定了之后改一边。
- §1 的现状符合度表描述的是实施前的状态，例如仍写着注册表在 `core/scoperules`。
- §11 的 P1 各步和 §14 的缺陷修复清单没有标完成，三条缺陷都已修。
- 第十八版「P1 验收条款至此全部满足」：E2、E3、E4 关闭之前不成立。
