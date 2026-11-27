# 19 · 解耦与积木化方案（2026-11-27）

状态：**已决，待实施**。拓扑见 §2，包归属见 §4，模板见 §5，前端见 §6，构建见 §7，分期见 §11，风险、已决与实施约定见 §14。

修订记录（2026-11-27 第二版）：按 4zrcin 现状校正。P3 已完成，改为现状；§6 技术栈按 `web/` 实际更正；§8 改为 merge-base 口径并附命令；红线 1/3/5 与 docs/03、04、14 对齐；编号 18 → 19（18 已被 `18-webshell-management.md` 占用，代码注释里的 docs/18 都指那篇）。标「待复核」的条目依赖引擎仓或上游，数字是 2026-11-27 的实测值，P0 开工时重跑一遍。已决（2026-11-27）：4zweb 就是本仓的 `web/`，整体搬出，不替换、不重写。

第三版（同日）：未决项全部给结论，记入 §14 的「已决」与「实施约定」；红线 3、红线 5 与 §6、§7 的相关表述同步改定。

第四版（同日）：对齐需求。两条原则写进 §1 并给出验收；§2 加积木卡；入侵标准定为钩子级，协议只增不改；外壳内部做成真模块，仓库归属按标准逐个判断。

第五版（同日）：对照上游原仓库逐条核查 fork 的 37 条补丁，结果记在附录 A–D。更正了两处：fork 没有改任何协议消息（此前写的「30 个 `.pb.go` 因改 proto 重新生成」是错的），实质改动面是 187 个文件而不是 217；「协议迁出」这项工作不存在，已删。

第六版（同日）：P0 实施完成，§8 与 P0 一节改为实测（178 个文件），合并决策登记引擎 FORK.md。

## 1. 需求与验收

把现在这个「一个二进制装下全部」的系统，拆成可独立发版的积木：外壳只当组装点，引擎、规则、前端、扩展各自成仓、各自出货，靠数据契约相连，靠构建脚本组装。

整份计划按两条原则验收。

**原则一 · 最小化入侵 cyber-harness。** fork 等于「上游 + 纯新增」。上游已有的文件只允许登记在册的挂钩，逻辑全部在新增文件或自己的模块里。协议只增不改：新内容写进新增的 proto 文件或 anypb sidecar（fork 现有的 JSON-in-Any 惯例，docs/04 §3.1），不改上游消息。

落地按三级优先，钩子是最后一级：

1. **协议**：控制面与节点之间要传的东西，一律走 AOP——新消息或 anypb sidecar，不改上游
2. **扩展**：节点里照数据办事的代码，放进引擎的扩展（`exts/4z*` 或独立模块），不改上游
3. **钩子**：上游函数中途中必须改、扩展点又够不着的，才留几行调用；能先向上游提 PR 的先提

AOP 只负责传，不负责做。数据到了节点，总得有代码去读它、照它办，所以第 2、3 级免不了；第 3 级的目标是接近零。处置表里每条补丁都按这个顺序追问。

验收（机器检查，进引擎 gate）：

- `git diff upstream/master...HEAD` 里，上游已有文件的改动只剩 `FORK.md` 列出的钩子文件；出现未登记的改动即失败
- 每个钩子不超过 20 行（初定，定处置表时校准）；超了说明逻辑没放对地方
- 上游的 `.proto` 文件与上游一致，fork 的协议内容只在新增的 proto 文件或 sidecar 里
- `go.mod`、`Makefile` 的改动逐条记账

**原则二 · 自己的东西拆成积木。** 每块积木是独立模块，只通过契约与外界相连：引擎契约（`aop`、`core/tool`、`core/extension`）或数据（YAML、`.bin`、JSON、HTTP）。积木之间不互相 import，能单独换、单独摘。外壳只负责装配。

验收：

- 每块积木有一张积木卡（§2），写清形态、装配点、契约、摘除方式
- 外壳内部七组按允许依赖边表和架构测试做成真模块（§4）
- 模块边界立住之后，仓库归属按 §3 的三条标准逐个判断：满足的可以各自成仓，不满足的留在同一个仓

## 2. 目标形态：六仓积木

```
积木层（独立发版、独立 CI、互不 import 内部实现）
  4zweb          控制台前端，即现在的 web/（React 18 + Radix UI + Tailwind）；dev 挂载目录，prod 内嵌进外壳
  4ztemplates    规则 YAML（指纹/POC/字典/工作流）+ 生成器，产出 .bin
  4zwebshell     webshell 会话与后渗透通道（已存在）
  4zfence        围栏判定（规则快照 in、判定 out，可选）
  4zreport       报告渲染（findings JSON in、HTML/PDF out，可选；仓库内尚无对应实现，见 §3）

引擎层
  4zengine       cyber-harness fork（仓库名不改，4zengine 是逻辑名；基线分支 tianwu/fixes）；链接上面的扩展，出 cyber-harness / cyber-scan

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
- rem 不算积木，但它已经是版本矩阵的一部分；§3 的「6 个仓」不含它。

每块积木一张卡，写清它怎么接、怎么摘：

| 积木 | 形态 | 装配点 | 契约 | 摘除方式 |
| --- | --- | --- | --- | --- |
| 4zweb | 静态产物 | 构建期编进外壳（flate bundle）；开发期 `--web-dir` | 外壳的 HTTP API | 不装：`-tags noconsole` |
| 4ztemplates | 规则数据 | 构建期生成 `.bin` 进引擎的 `tools/resources` | 资源 key（`http`、`neutron` 等） | 不装：`-tags emptyresources` |
| 4zwebshell | Go 模块（库 + 扩展） | 外壳 import 库；引擎装配时链接扩展 | `core/extension`、`core/tool`、`aop`，加控制面 HTTP | 装配里去掉链接 |
| 4zfence | 引擎扩展 | 引擎装配时链接 `exts/4zfence` | 规则快照 in、判定 out，经 `aop` sidecar | 装配里去掉链接 |
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

**结论：先做 6 个仓。** 再多，版本矩阵会替代代码耦合成为新的痛点——改一个共享类型要发 3 个版本，`go.work` 里写 5 条 replace，所以不预先封顶，但一次只多一个。当前阶段只做 3 个必拆：`4zweb`、`4ztemplates`、`4zwebshell`（已有）。

外壳内部的模块不要求同仓。先按 §4 把边界立住，再逐个用上面三条标准判断：边界是数据、且不共享事务或内存状态的，可以各自成仓；共享 SQLite 事务边界的（kernel 与 agent 之间）留在同一个仓。现状里边界最干净的是不 import 任何内部包的 `kb`、`pocs`、`skills`，以及只依赖 `istate` 的 `notify`、`account`。成熟一个拆一个，不要求一次拆完。

## 4. 外壳内部：49 包 → 7 组目录

不改仓库，只改目录分组，对齐 harness 的 `core / aop / agent / exts / pkg / tools / cmd` 骨架。

小计按 2026-11-27 工作区的非测试行数重算，合计 99.6K，只统计 `internal/`。`cmd/` 另有约 14.4K 行（`cmd/zrcin` 约 9.5K），顶层路由 `hub.go` 就在那里，不在 49 包之内。

**`internal/protocol/` — 协议与传输**（对应 `aop/`，13.8K 行）
`aop`（拆掉领域逻辑后的客户端骨架）、`ws`、`connectretry`、`ioa`

**`internal/kernel/` — 账本与状态**（对应 `core/`，16.0K 行）
`store` 11091、`ledger` 2042、`state` 192、`istate` 181、`project` 1804、`artifact` 168、`evidence` 461、`contract` 14

`contract` 现在只有 14 行，应扩成真正的接口定义：哪些操作对账本可写、哪些只可读。这是拆积木的前置契约。

**`internal/agent/` — 决策与执行**（对应 `agent/`，10.6K 行）
`decider` 2296、`verifier` 789、`executor` 1900、`aopexec` 2189、`lifecycle` 1132、`model` 1656、`modelclient` 613

**`internal/fleet/` — 节点与隧道**（对应 `pkg/node`，8.7K 行）
`node` 548、`enroll` 2375、`tunnel` 503、`relay` 304、`deploy` 1144、`host` 2834、`platform` 433、`fleet` 288、`noderes` 271

**`internal/domain/` — 领域能力**（对应 `tools/`，13.0K 行）
`traffic` 1314、`retrieval` 2721、`kb` 894、`vulnlib` 412、`pocs` 294、`report` 1211、`bundle` 1208、`metrics` 2028、`board` 1900、`skills` 150、`resource` 543、`assets` 352

**`internal/edge/` — API 与界面**（对应 `pkg/web` + `console`，34.5K 行）
`api` 26097、`web` 503、`settings` 3259、`access` 572、`account` 517、`notify` 3261、`collab` 327

`api` 一个包 98 文件 26097 行，占 `internal/` 的 26%。拆法不是拆仓，是按资源切子包，每个子包只负责一到两组路由。现状是单个 `Server` 上的 547 个方法共用一套依赖，所以切子包是一次真正的重构（要把 `Server` 拆成各自的 handler 与依赖注入），不是按文件归堆；子包也要按实际资源重定——体量最大的是 intents、agent、prompt、state、journal、workers、collab、shell、dispatch、commander。

**`internal/support/` — 支撑**
`buildinfo` 2550、`testutil` 409

`buildinfo` 10 个文件 2550 行，但它不是版本注入：是 gate 与构建清单的工具库（AOP 协议映射与漂移检查 641 行、git 与树快照、平台与模型探测）。杠杆三落地后其中约 840 行（`aopproto.go`、`aopalias.go`）会消失或大幅缩小，在那之前不建议单独压缩。

**分组前要知道的三件事**（已核实）

- **七组现在不是分层关系。** 按 `go list` 的非测试 import 统计，组与组之间有环：kernel↔agent（5/6 条边，如 `ledger→executor`、`decider→store`）、agent↔fleet（3/6）、agent↔domain（2/11）、domain↔edge（3/12）、kernel↔edge（1/12，`store→collab`）。只有 `protocol` 不依赖其它组。
- **两个叶子包放错了层。** `model` 被 19 个包 import，`collab` 被 6 个，它们自己不 import 任何内部包，却分在 agent 和 edge。照现表搬，kernel 与 domain 的每个包都会「依赖 agent」。
- **`internal/aop` 不只是协议。** 12.4K 行里 10.2K 是 vendored 协议副本（杠杆三之后消失）；根包 2.2K 行是客户端加外壳自己的领域逻辑（`scoperules.go`、`globalfence.go`、`guardrail.go`、`seed.go`、`rungoal.go`、`workdir.go` 等）。后者先拆到 agent / domain，`aop` 才能以「客户端骨架」的身份进 `protocol/`。

所以搬目录本身只是移动；要让「越界可机器判定」成立，先定一张允许依赖边表（含例外）并加架构测试，再动 P5。

## 5. 模板与规则：4ztemplates

### 5.1 上游是什么

`chainreactors/templates` 不是规则库，是**按目标工具裁剪的构建输入**。README 末尾的对应关系：

- gogo 用 `fingers/` `neutron/` `port.yaml` `workflows.yaml` `extract.yaml`，`-need gogo`
- spray 用 `spray/` `extract.yaml` `found/keys/` `port.yaml`，`-need spray`
- zombie 用 `zombie/` `neutron/login/` `services/` `port.yaml` `fingers/`，`-need zombie`
- found 用 `found/`，`-need found`

生成器 `templates_gen.go`（381 行，`package main`，无 go.mod）：读 YAML → deflate 压缩 → 产出 Go 代码或 `go:embed` 的 `.bin`。两种模式：legacy（base64 Go 源，默认）、embed（`//go:embed` + `.bin`）。

### 5.2 现状缺口

`4zengine` 里已经有一套机制，但**只有产物，没有源**：

```
tools/resources/templates_gen.go     生成器（从 templates 仓拷入）
tools/resources/resources.go         go:generate: -t ../templates -o template.go
tools/resources/template.go          //go:embed data/*.bin（生成物，DO NOT EDIT）
tools/resources/data/*.bin           472K，15 个，已提交 git
tools/templates/                     空目录
```

`data/` 里：`http.bin` 193K、`zombie_rule.bin` 114K、`spray_dict.bin` 87K、`neutron.bin` 19K、`zombie_template.bin` 5.1K、`port.bin` 1.8K、`socket.bin` 1.7K、`extract.bin` 1.2K、`workflow.bin` 328B，另有 `fingerprinthub_web.bin` 与 `fingerprinthub_service.bin` 各 8 字节（空数据）。

结论：想加一条指纹、加一个 POC，当前**没有源可以改**，只能等上游。这是把规则所有权拿回来的直接动机。

同目录还有 fork 自己加的 `resources_empty.go`（`emptyresources` 孪生）与 `guardrail_builtin.yaml` + `builtin_gen.go`（围栏保底审计语料）。它们不是扫描规则，不归 4ztemplates，见 §5.4。

### 5.3 目标形态

`c1ayooo/4ztemplates` 独立仓：

```
4ztemplates/
├── templates_gen.go            生成器（跟随上游，不改）
├── templates/                  YAML 源
│   ├── fingers/http/*.yaml     上游规则
│   ├── fingers/4z/*.yaml       自有指纹（新增目录）
│   ├── neutron/*.yaml          上游 POC
│   ├── neutron/4z/*.yaml       自有 POC（新增目录）
│   ├── port.yaml  workflows.yaml  extract.yaml
│   └── spray/  zombie/  found/  services/
├── Makefile
└── README.md                   记录 -need 分组与本仓自有规则
```

**同步策略**：保留 `upstream=chainreactors/templates`，定期 `merge-upstream` 拿新规则。自有规则只落在 `4z/` 子目录，上游 merge 零冲突（生成器 walk 整个目录，子目录自动被包含；语义上的冲突见下方「待复核」）。

**产出方式**（引擎侧零改动）：

```bash
# 4ztemplates
make gen TARGET=../4zengine/tools/resources -NEED=all
# 等价于 go run templates_gen.go -t templates -o $TARGET/template.go -embed
#          并写出 $TARGET/data/*.bin
```

引擎的 `tools/resources/template.go` 与 `data/*.bin` 都是生成物，内容被替换不属于「改上游源码」，红线允许。

零改动的验收条件：重跑后 `template.go` 仍带 `//go:build !emptyresources`、`resources_empty.go` 不动、`make scan-capture` 照常出包（原因见 §5.4）。

**`-need` 分组要重定**：上游按 gogo/spray/zombie/found 分四组，我们的工具线是 `cyber-scan`（扫描节点，需要 fingers + neutron + port + workflows + extract + spray 字典）和 `cyber-harness`（引擎，目前 proton 侧只要 `found_keys,found_spray,found_filter_ext,found_filter_dir`）。在 `4ztemplates/Makefile` 里定义两个组合：`NEED=scan` 与 `NEED=engine`。`-need` 接受逗号分隔的 key 列表，所以这两个组合可以在 Makefile 里映射成 key 列表而不改生成器；但 key 集合必须与引擎 `tools/resources` 实际注册的一致。

**待复核（引擎仓 / 上游，2026-11-27 实测）**：

- 引擎里现有的 `tools/resources/templates_gen.go` 与上游 templates 仓的不是同一份：引擎内 299 行，默认 `-need cyber`、恒为 embed 模式、输出带 `//go:build !emptyresources`；上游 381 行，默认 legacy、不带 tag。若属实，§5.1 描述的是上游版，「生成器跟随上游，不改」会让产出丢掉 `!emptyresources`。二选一：4ztemplates 带自己的生成器，或给上游生成器加 tag 选项。
- 上游 templates 仓没有 `go.mod`，而生成器 import 了非标准库包。按上面的目录结构，`go run templates_gen.go` 解析不了依赖——要么加 `go.mod`，要么通过引擎模块运行。
- 「`4z/` 子目录零冲突」只在 git 层成立。生成器把指纹的 `tag` 写成父目录名（`fingers/http/4z/x.yaml` 的 tag 是 `4z` 而不是 `http`）；spray 字典与 zombie 规则以文件名为 key，同名文件静默覆盖；`port.yaml`、`workflows.yaml`、`extract.yaml` 是单文件，自有改动必冲突。`make lint` 要加唯一性与 tag 检查。
- `fingerprinthub_web` / `fingerprinthub_service` 两个 key 现在是硬编码的空列表。若它本就是为自有指纹预留的入口，自有指纹优先走它。

### 5.4 本仓侧的约束（已核实）

- **产出必须保留 `!emptyresources` 孪生。** `tools/resources` 是 `template.go`（`//go:build !emptyresources`）加 `resources_empty.go` 的两文件模式（docs/04 §3.2），scan-capture 配方靠它把 472K 裁掉（docs/04 §3.3、docs/14 的构建配方表）。
- **围栏保底审计语料另有归属。** `tools/resources/guardrail_builtin.yaml` 单源在 harness，是 docs/04 §6 的既定决策；它不是扫描规则，红线 5 相应收窄。已决：留在引擎，不随 4ztemplates 走。
- **节点拿的是二进制，不是模板。** `internal/enroll/dist.go` 分发的产物是 `cyber-scan` / `cyber-scan-capture`，规则编在里面，没有模板下发通道。改规则 = 重跑 `make resources` + 重编 scan + 重新分发节点。4ztemplates 解决的是「有源可改」；「改规则不重编」要靠 docs/04 §6 的运行时三层（内嵌 → 磁盘 → hub 下发），目前仍是「约定，暂不建」。

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

## 7. 构建收敛：三处 Makefile，加 4zweb 的 CI

```
4ztemplates/Makefile
  make gen TARGET=<dir> [NEED=scan|engine|all]   生成规则 .bin
  make lint                                        YAML 语法与字段校验

4zengine/Makefile
  make engine        cyber-harness（链接 4z* 扩展）
  make scan          cyber-scan full（约 185MB）
  make scan-capture  cyber-scan capture（约 158MB）
  make proto         协议生成
  make ldflags       版本注入（被上面引用）
  make resources     调 4ztemplates 生成并覆盖 tools/resources/{template.go,data/}
  make gate          架构门禁

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

- 提交：领先 61，落后 0；merge-base `585d8643`
- 文件：**178 = 新增 67 / 修改 110 / 删除 1**；+12591 / −708（P0 验收达成，此前基线 912 = 67A/845M/0D）
- 110 个修改全部是实质改动（纯路径替换已随回退清零）
- 实质改动面：110 + 67 = **177 个文件**
- **fork 没有改任何协议消息**：仓库里 18 个 proto 源文件只动了 `go_package` 一行；30 个 `*.pb.go` 已恢复为上游逐字节一致（fork 从未改过消息，只改过路径）
- fork 的 7 个扩展载荷（WorkDir、ToolEnv、RunGoal、ScopeRules、GlobalFence、CaseSeed、EgressProxy）都走上游 `OpenSessionRequest`、`RunTurnRequest` 自带的 `extensions` 字段
- 红线 1 保护的三棵树里，被修改的上游文件约 28 个：`agent/` 17、`core/` 3、`exts/` 8

实质修改最集中的地方（被修改的上游文件数，含测试）：`cmd/cyber-scan` 16、`pkg/rpc` 7、`aop/` 7、`core/types` 7、`agent/provider` 7、`agent/` 7、`tools/playwright` 6、`cmd/audit` 6、`tools/proxy` 5、`tools/curl` 5、`agent/session` 5。

与围栏相关、P1 要处理的：

- `exts/guardrail/` — 新增 11 个文件（`policy_llm` 616 行、`scoperules`、`policy_hybrid`、`builtin` 及测试）；另有 3 个上游文件被改：`extension.go`（+256/−28）、`runtime.go`（+234/−26）、`jev.go`。上一版把 `runtime.go` 算成了新文件。
- `core/scoperules/` — 新包，464 行；`core/globalfence/` — 新包
- `agent/session/protocol.go` — +396/−4，会话钩子
- `tools/proxy/` — `mitm.go`、`hub.go` 等出口代理改造
- 另有 `exts/observe/extension.go`（+112/−35）、`core/operation`、`core/tool` 的修改

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

### 杠杆一：fork 不改 module path（消掉 725 个纯路径差异）

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

`core/scoperules`、`core/globalfence` → `exts/4zfence/`；`exts/guardrail/` 里新增的 11 个文件 → `exts/4zfence/policy/`；上游 `exts/guardrail` 的改动收缩到钩子行。

这一步能拿走的是「新增文件」；拿不走的是已经被改的上游文件：`exts/guardrail/extension.go`、`runtime.go`、`jev.go`，以及 `agent/session/protocol.go`（+396/−4）这类扩展点够不着的会话钩子。它们都是常驻补丁，不是只有一个——本仓 docs/03、04、14 登记的 harness 补丁号已经到 28（22 = Hub 可内嵌的 Listener；23a/b/c = 围栏判定、保底审计与证据回传；26/27/28 = 种子、交接文件、artifacts 观测）。全量清单以引擎仓 `FORK.md` 为准，逐条记明理由与回归测试。

按钩子级标准（§1），`FORK.md` 的每条补丁都要有处置：**上游化**（给上游提 PR，合入后从 fork 消失）、**改成挂钩**（逻辑挪进新增文件，上游文件里只留调用）、**外移到积木**（挪进 `exts/4z*` 或独立模块）、**保留**（说明为什么改不成挂钩）。超限的先动，`agent/session/protocol.go`（+396 行）是第一个。处置表是 P1 的施工清单，每条按「能不能只走协议 → 能不能做成扩展 → 都不行才是钩子」的顺序追问（§1）。

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

同一把尺子用在协议上——**只增不改**（§1）。这一条 fork 已经做到了：7 个扩展载荷都走上游请求自带的 `extensions` 字段，上游 proto 一个字段没改（§8）。剩下的问题不在协议，在「谁来解析这些载荷」：上游的 `RunAOPTurn` 只认写死的 `AgentRunOptions`，`OpenAOPSession` 不读 `extensions`；fork 是在上游的 `agent/session/protocol.go` 里加了约 400 行解析自己的类型。处置见附录 C 和附录 A 的接缝 G1。

### 杠杆四：控制台归一

生产构建不编引擎 web；harness 的 web 仅在本地调试用 `full` 标签编译，不进分发包。`4zweb` 是唯一控制台。

本仓现状（已核实）：`cyber-scan` 已经不带 `full`；但 `Makefile` 第 21 行的 `TAGS` 给 `cyber-harness` 带了 `full`，gate 第 8 步构建 Hub 时也追加 `full`（`cmd/zrcin-gate/main.go`）。要做的就是去掉这两处，然后实测内嵌 Hub 在不带 `full` 时是否正常（待实测，见 §14）。

### 杠杆五：规则源自主

`4ztemplates` 接管规则源，自有规则落 `4z/` 子目录，引擎侧只换生成物。约束见 §5.3 与 §5.4。

## 10. 红线

1. **fork 只增不改，钩子级** — 新增文件/目录允许；上游已有文件只允许登记在册的挂钩，每处不超过 20 行（初定）；`go.mod`、`Makefile`、`.goreleaser.yml` 的改动逐条记账。每条补丁在 `FORK.md` 里记处置、理由、回归测试、回滚，验收是 §1 的机器检查。现状离这条有距离：按 §8 口径，这三棵树里约有 28 个上游文件被改（协议生成文件不在其内，它们只是 module path 噪音）。
2. **一个扩展一个仓库**，本地用 `go.work` 统管（尚未建立）；扩展只 import 引擎契约与标准库，**永不 import 4zrcin**。装配尽量在自己的模块里完成；必须在引擎里链接的，每个积木一行
3. **扩展只依赖引擎契约** — `aop`（协议），以及 `core/tool`、`core/extension` 这类注册接口；不 import 引擎的业务包（`agent/`、`tools/*`、`pkg/web/*` 等），也不形成包级 import 环。引擎的装配必须链接扩展，模块层面的互相 require 躲不掉，靠本地 replace 或 `go.work` 解决（已决）
4. **协议只有一份，只增不改** — 来自引擎 `aop/`，任何仓都不复制 pb；fork 加的协议内容只落在新增的 proto 文件或 anypb sidecar，不改上游消息
5. **扫描规则源只有一份**，在 `4ztemplates/templates/`；引擎从中生成，节点拿编入规则的二进制。围栏保底审计语料单源在引擎（docs/04 §6，已决）。

## 11. 六阶段（P0–P5）

### P0 · fork 回退 module path（已完成，2026-11-27）

- 基线对齐已完成：`feat/remote-nodes` 快进进 `tianwu/fixes`（补上 webshell 接线的未提交改动），再合并 upstream 的 #205/#208（23 个冲突文件的解法登记在引擎 FORK.md「Upstream merge 2026-11-27」）
- 路径回退完成：引擎 `ce7f848d`（module 回退 + 30 个 pb.go 取上游逐字节一致版 + 11 个文件 gofmt 重排），4zwebshell `1a5272b`，本仓 `3916c09`（require/replace、buildinfo 常量、alias 目标、vendored 协议经 `-sync-aop-protos` 重新生成，漂移检查对准真实检出为绿）
- 实测：约 880 个 Go 文件的路径改写回退；§8 的数字更新为 178
- 验收达成：三仓 `go build ./...` 通过（外壳默认 + inprochost 单二进制 79MB；引擎 full + capture 155MB）；文件数 912 → 178
- 回滚：各仓单提交 revert
- 遗留：`tianwu/fixes` 未推送 origin；引擎仓的 proto 生成器（源头不在引擎仓内）没有着落——下次上游改协议时要能重新生成，需要找到或搭起 protoc 工具链
- 引擎合并时发现的既有问题（与本次无关）：`cmd/cyber-scan` 的部分组合测试在带 judge 的活代理上触发 `judgeRequest` 空指针 panic（net/http 恢复为 500，不挂测试）；已在合并前基线上复现

### P1 · 扩展外移（3-5 天）

- 在 P0 对齐后的基线上动手
- 先定补丁处置表（杠杆二）：逐条结论见附录 C，拆出来的积木见附录 D。超限的钩子优先，把逻辑挪进新增文件，原处只留调用
- `core/scoperules`、`core/globalfence` → `exts/4zfence/`；guardrail 新增的 11 文件同步搬走
- `tools/proxy` 的定制按同规则处理：能独立成 ext 的搬走，不能的记入补丁清单。`tools/search`、`tools/passive` 没有 fork 定制（见 §8），不用处理
- 本仓侧：若 `guardrail.pb.go` 的归属变了，同步更新 `internal/buildinfo` 的映射（见杠杆二）
- 验收：围栏拦截行为与 P0 完全一致——用同一批样本对比拦截率与误拦率，样本集要先建（本仓 `scripts/acceptance/` 里只有 UI 验收脚本）；`git diff upstream/master...HEAD` 里上游已有文件的改动只剩 `FORK.md` 登记的钩子，且每处在行数上限内（§1 的机器检查进引擎 gate）
- 回滚：ext 可用构建标签摘掉

### P2 · 协议归一 + 扩展独立成仓（2-3 天）

- 建父目录 `go.work`（4zrcin、引擎、4zwebshell、rem），把引擎的依赖 replace 提升到 workspace 层，让 inprochost 构建与引擎构建解析到同一批依赖（杠杆一）
- `4zwebshell` 进 workspace，确认能独立 `go build`
- 杠杆三：删 7 个 vendored 协议包、改 76 处 import，删除同步脚本与 gate 的 sync/check 分支。**前提是 P0 已完成且引擎可按版本拉取**，否则默认构建与 CI 断掉
- 引擎侧不涉及协议改动：fork 的协议本来就是只增不改（§8）。取而代之的是上游接缝的 PR（附录 A 的 G1–G6）
- 外壳去重（附录 B2）：前提与杠杆三相同——外壳无条件依赖引擎模块。配置校验改调 `pkg/config` 导出的函数，配置布局跟上游 #205 改成顶层块；围栏和扩展载荷的结构体收进一个共享包，两边共用；`internal/ioa/v1.go`、`internal/modelclient`、AOP 客户端改用引擎的包
- 验收：`go build`、全量测试、`scripts/gate.sh` 通过；`internal/aop` 只剩客户端骨架与领域包

### P3 · 单进程内嵌（已完成）

**已落地，不需要再做**（docs/03 M1、docs/04 T2）：外壳在 `-tags inprochost` 下通过 `internal/host/inproc` 把 Hub 跑在进程内，组装点是引擎的 `pkg/web/hubapp`（fork 补丁 22，`webhost.Config` 加可选 `Listener`）；`--harness` 显式给出时回到子进程模式，作为逃生舱；scan 节点仍是子进程。上一版写的 `pkg/harness.New` 是节点侧的组合根（扫描节点的扩展集），不是 Hub 的入口。

- 现状验收已达成：`bin/zrcin` 单文件起完整实例（docs/04 §3.1）
- 可选后续：给 Hub 一个不解析命令行参数的入口（现在内嵌走的是与 `cyber-harness` 命令相同的 argv）；验证进程内 Stop/Restart，为将来拆进程留口子
- 端口不会只有一个：console（8080）、Hub 回环监听（`--host-addr`）、可选的 relay 与 Postgres 各自独立

拆进程（保留为备选，触发条件：需独立发布引擎二进制 / 引擎多副本 / 崩溃隔离 / 各自热重启）：去内嵌、引擎独立进程、外壳走 AOP WebSocket，处理启动顺序与数据目录归属。

### P4 · 构建收敛 + 积木落地（3-5 天）

- `4zweb` 独立仓（内容即本仓的 `web/`，见 §6；提交历史用 `git subtree split` 带过去），外壳加 `--web-dir`；生产态沿用 §6.2 的 flate bundle 链路
- `4ztemplates` 独立仓，`make gen TARGET=... NEED=scan|engine` 覆盖引擎生成物，产出保留 `!emptyresources` 孪生
- 构建脚本按 §7 落地，含 `rem-relay` 目标与 gate 第 8、10 步的替代来源
- 验收：上游 `git pull` 后只跑 `make`，不动任何源码；`make zrcin` 产出单二进制；前端改动不触发 Go 重编（开发态）；规则改动只重跑 `make resources`，再重编并分发 scan

### P5 · 外壳做成真模块（可选，2 天）

按 §4 把 49 包归到 7 组目录。纯移动 + import 改写，行为不变。可与 P1/P2 穿插做。边界立住之后，逐个模块用 §3 的三条标准判断是否成仓；满足的再拆，不设时间表。

前置两件事，否则搬完边界仍然不可判定：

1. 先定允许依赖边表（含例外）并加架构测试，再把 `model`、`collab` 这类叶子包归到共享层
2. 先把 `internal/aop` 根包里的领域逻辑拆到 agent / domain，它才能以「客户端骨架」的身份进 `protocol/`

## 12. 上游同步流程（解耦后）

```bash
# 引擎
cd ~/4zengine
git fetch upstream && git merge upstream/master   # 冲突应只出现在 FORK.md 登记的补丁文件（目标状态，现状见 §8）
make engine                                        # 重编

# 规则（每月一次）
cd ~/4ztemplates
git fetch upstream && git merge upstream/master    # 只可能在上游文件冲突，4z/ 子目录零冲突（git 层；语义冲突见 §5.3）

# 外壳
cd ~/4zrcin
make gate && make zrcin
```

## 13. 收益与代价

收益：

- 上游同步成本下降：约 725 个纯路径替换的文件从差异里消失，冲突面从 187 个实质文件收缩到 `FORK.md` 登记的常驻补丁；能压到多少，取决于有多少补丁能上游化
- 前端可以独立发版：开发态改前端不重编 Go；生产态是内嵌，换前端仍要重链接 zrcin，部署方用 `--web-dir` 时才真正免重编
- 规则源回到自己手里：加指纹有源可改。但规则编在 scan 二进制里，改规则仍要重编并分发节点
- 协议单份（杠杆三之后）；在此之前由同步脚本加漂移检查保证
- 包名即职责，AI 改代码的越界可以机器判定（要先定边表，见 P5）

代价：

- 6 个仓加 rem 的版本矩阵，`go.work` 与 replace 要维护
- 构建从「一条命令」变「三处 make，有顺序」
- P1 有行为等价性风险，必须样本对比
- 搬迁期建议冻结功能 2-3 周

## 14. 风险、已决与实施约定

风险：

- **P1 行为等价性**：围栏搬家后必须用同一批样本验证拦截率与误拦率不变；样本集要先建
- **常驻补丁不止一个**：`agent/session/protocol.go` 只是其中之一。每条补丁所在的上游文件一改就要手工合，`FORK.md` 要记明理由与上游 PR 状态；真实清单以引擎仓 `FORK.md` 为准（待复核）
- **replace 不传递**：内嵌 Hub 与 scan 的依赖来源不同，`go.work` 落地前每次发版都要确认
- **§8 与引擎侧的数字未经本仓复核**：P0 开工时按 §8 的命令重跑
- **上游 #205 合入时会打断现有的挂钩**：它把 `ProxyHub` 改名为 `Hub`，把 MCP 配置改成顶层服务名，fork 在 `tools/proxy`、`exts/proxy`、`exts/guardrail`、`cmd/cyber-scan` 里的改动都会冲突；外壳写的 `extensions:` 嵌套布局也会被上游拒绝（附录 B2）。合并上游排在 P1 最前面
- **上游化取决于上游是否接收 PR**：不被接收的退回钩子级，每处不超过 20 行，在 `FORK.md` 记明
- **钩子级标准会把现有的大补丁顶出来**：`agent/session/protocol.go`（+396 行）这类要先重构成「新增文件 + 一行调用」，是 P1 里最费工的部分

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

实施约定：

- **目录范围**：P0、P1、P2 要动引擎仓和 4zwebshell，P4 要新建 4zweb；除此之外不碰别的目录。
- **基线分支**：`tianwu/fixes`。本仓脚本固定从这里构建；`feat/remote-nodes` 先合进去。
- **仓库名**：`cyber-harness` 不改名，`4zengine` 只是本文档里的逻辑名。
- **4zweb 建仓**：`c1ayooo/4zweb`，公开（与本仓、引擎仓一致）——建仓前再确认一次。
- **不动的文件**：`docs/18-webshell-management.md` 是 webshell 的文档，与本计划无关。
- **开工状态**：本仓的未提交改动已在实施前全部提交。
- **Hub 内嵌保留**：它解决的是单二进制，不是通信，AOP 替代不了它。不想保留这个钩子时，回到 Hub 独立进程——外壳的 `--harness` 路径现在就能用。

待实测（不需要决策，实施时验证）：引擎 web 关掉后，内嵌 Hub 在不带 `full` 时是否正常（杠杆四）。

## 附录 A · 上游已经提供的积木接口

对照对象是上游 `chainreactors/cyber-harness` 的 `86c5acea`。fork 落后它 2 个提交（#205 外部 API 网关、#208 passive 来源）。路径相对引擎仓。

**扩展（`core/extension`）。** 一个扩展就是实现 `Load(*Scope)` 的类型。它能贡献：工具（`core/tool.Tool`）、命令（`tool.Command`）、自己的 AOP 命名空间（`aop.Binding`，proto 由功能包自带）、提示词片段（`prompt.Contribution`）、技能包、子 Agent、终端绑定、Hub 的路由与服务（`webpkg.Capability`）、推理循环、provider 状态。服务用 `Provide[T]` 发布、`Use[T]` 借用。同名注册会被拒绝，没有覆盖或移除接口。

**钩子（26 个点）。** `core/tool/hooks/points.go` 16 个，`agent/hooks/points.go` 10 个。能否决的是 `tool.before`、`command.before`、`process.before`、`process.started.control`、`model_request_policy`、`session_start`、`before_compact`、`inbox_wait`；其余只能观察。上游围栏自己就挂在 `tool.before` 上（`exts/guardrail/extension.go:85`），源码注释写明「策略通过 tool.before 组合，没有嵌套的策略注册表」（`exts/guardrail/runtime.go:54`）。

**配置（`pkg/config`）。** 加载、校验、原子保存，`Declare` 声明 section，`DistributeConfig` 从 Hub 分发到节点，Hub 上有配置 API。

**组合根。** `pkg/harness.New`：在自己的 Go 应用里嵌入会话运行时。`node.RunWebSocket` 加 `profile.Profile`：自定义节点。`webext.New` 加 `webhost.Serve`：在自己的应用里托管 Hub。

**协议。** `OpenSessionRequest`、`RunTurnRequest` 自带 `extensions []*anypb.Any`（`aop/chat.pb.go:160`、`:326`）；事件的 Envelope 也带 `Extensions`，`Event_Extension` 是扩展自带事件的通道。

### 上游缺的接缝

fork 需要挂钩的地方，上游都没有现成接口。下表就是给上游的 PR 清单；合入之前，每一条在 fork 里按钩子级保留。

| 编号 | 缺什么 | 上游现状 |
| --- | --- | --- |
| G1 | 会话请求里扩展载荷的解析注册点 | `RunAOPTurn` 只认写死的 `AgentRunOptions`（`agent/session/protocol.go:34-43`），`OpenAOPSession` 不读 `extensions`；一个命名空间只能有一个 binding（`aop/mux.go`），扩展无法包装 |
| G2 | 代理的请求判定接缝 | 只有 `SetProxyAuth`（`tools/proxy/hub.go:159`），没有调用方，只能以 407 拒绝，不能挂起等审批 |
| G3 | 复用围栏审批队列的公开入口 | `*Runtime` 导出了 `Admit`、`Pending`、`Resolve`，没有「提交一条审批」的方法；`newRuntime` 未导出 |
| G4 | 按会话的工作目录、轮次种子 | 请求和结构体都没有对应字段；`Invocation.WorkDir` 这个接缝在，但会话不填 |
| G5 | 按工具名排除 | 只有按组开关的 `--tools`、整次请求禁用的 `DisableTools`、逐次调用的 `tool.before` |
| G6 | 可导入的节点装配 | `cmd/cyber-scan` 的装配全在 `package main` 里；自定义节点要自己实现 `profile.Profile` |
| G7 | 不受采集开关影响的 HTTP 流量观察 | 钩子在关闭采集或被过滤时直接跳过 |
| G8 | 新的 provider 类型 | 构造写死 anthropic 和 openai |

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
| 5 | `CYBER_EXCLUDE_TOOLS` | 上游没有按工具名排除（G5） | 积木 | 4zsession：挂 `tool.before` 拒绝指定工具 |
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
| 18 | 可插拔的围栏判定 | 上游没有策略注册表 | 积木 | 4zfence：挂 `tool.before`；审批队列要 G3 |
| 19 | neutron 快速参考文案 | 文档 | 上游化 | — |
| 20 | 兼容两个命名空间 | 自己的历史包袱 | 随 G1 收敛 | 控制面只发 `4zrcin.*` |
| 21 | 范围规则判定代理流量 | 上游没有判定接缝（G2） | 积木，接缝上游化 | 4zfence |
| 22 | Hub 可嵌入 | 给嵌入者的路径缺 `Listener` | 上游化 | 合入前是登记钩子 |
| 23a | CIDR 行 | 上游没有 | 积木 | 4zfence |
| 23b | 保底审计 | 上游没有 | 积木 | 4zfence，语料随行 |
| 23c | 证据 sidecar | 上游只有 hub 到 node 的上传 | 积木 | 4zevidence；顺带修的双层 Any 上游化 |
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
| 33 | 判定原样传递、子会话继承 | 改了上游的代理和 subagent | 接缝上游化（G2），继承进积木 | 4zfence |
| 34 | 被动子资源放行 | 改了上游的 `mitm.go` | 积木，接缝要带上请求头 | 4zfence |
| 35 | 命令红线只判类命令调用 | 改了上游的围栏运行时 | 积木 | 4zfence |
| 36 | 控制面自身调用豁免 | 改了上游的代理 | 积木，接缝要带上主机 | 4zfence |
| 37 | 全局默认放行 | 上游没有 | 积木 | 4zfence |

合计 40 条（23 拆成三条）：上游化 15 条，进积木 16 条，删除 2 条，装配 3 条，保留 1 条，混合 3 条（7、8、20）。

## 附录 D · 拆出来的积木和装配点

| 积木 | 装进去的补丁 | 怎么接上引擎 | 需要的上游接缝 |
| --- | --- | --- | --- |
| 4zfence（围栏） | 18、21、23a、23b、29、30、31、33、34、35、36、37 | `tool.before` 钩子；代理的判定接缝；请求扩展的解析注册点 | G1、G2、G3 |
| 4zevidence（证据回传） | 23c、27、28 | 自己订阅 `process.completed` 和 `file.access` 钩子，用 `Event_Extension` 发 sidecar，不改 observe | 无 |
| 4zsession（会话载荷） | 3、5、7 的文字部分、8、15、20、26 | 请求扩展的解析注册点；`tool.before`；`prompt.Contribution` | G1、G4 |
| 4zwebshell | 32 | 已是独立模块，装配时链接一行 | 无 |
| 装配与配方 | 16、24、25 | 自己的组合根 | G6 |
| Hub 内嵌 | 22 | `webhost.Serve` 加 `Listener` | 上游化 |

代理里的改动分两类，拆法不同。通用的接缝——`ScopeJudge` 接口、在请求钩子里调用判定、支持挂起等审批、把合成响应写回流量——是给上游的 PR。4z 自己的策略——控制面主机豁免、按 `Sec-Fetch-Dest` 判断被动子资源、403 响应体的格式、对 `core/scoperules` 类型的依赖——整体搬进 4zfence。

建议的路径分两步。先把这些逻辑挪进引擎仓的 `exts/4z*` 目录：全是新增文件，上游已有文件里只留接缝的调用，「只增不改」在这一步达成。接缝上游化之后，再把 `exts/4z*` 提成独立模块，做法和 4zwebshell 一样；要不要提，按 §3 的标准逐个判断。
