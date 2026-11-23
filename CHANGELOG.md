# 变更记录

版本以 `tw --version` 为唯一口径（构建清单的 `control_plane` 里记录的就是同一串 `--version` 输出）；当前不打 tag、不做发布包，也不存在独立发布脚本。

## 未发布 — 2026-11-23 起

不打 tag、不出发布包；这一节记的改动已在 `main` 上，但还没并进任何一版口径（`tw --version` 仍是构建时注入的那串，不注入即 `tw dev`）。

### 变更（2026-11-23，按主题，括号内为提交）

- **嵌入产物新鲜度不再被行尾骗**（`7d8037b5`）：Windows 上 `core.autocrlf=true` 的检出把 `internal/web/static` 存成 CRLF，而 `vite build` 写 LF——同一份产物两种字节流，gate 曾据此判「本次 build 让产物产生新差异」并列出 87 个文件。现在文本文件（头部 8 KiB 无 NUL）算摘要前把 CRLF 归一成 LF，二进制仍逐字节比较（`internal/buildinfo/git.go:254`、`:303`；`bytesContainsCRLF` `:312`）。用例三面钉住：CRLF 检出 + LF 重建判无变化、真内容变化仍报、二进制里的 CRLF 仍算变化（`internal/buildinfo/freshness_test.go:93`）。现场验证：`eebba901` 上的 gate（11/11 全绿）跑完整重建，`embedded before=after changed=0`——在任何 CRLF 检出上重建 `internal/web/static` 都不会再被判「产物落后」；这条只对文本生效，头部含 NUL 的文件仍逐字节比较。
- **每轮收口记子进程峰值内存，只记账不设限**（`a74edc54`）：run 收口前把本机宿主子进程的峰值 RSS 写成一条 `kind=proc_peak` 进该 run 的 journal（`internal/api/proc_peak.go:45`；调用点 `internal/api/api.go:2448`、`internal/api/commander.go:572`）。峰值超过 4 GiB 只追加一条 warning，不设上限、不设调度、不杀进程（`internal/host/peakrss.go:33`、`internal/api/proc_peak.go:65`）；进程已退出读不到峰值时写 `available=false` 与原因，不写 0（`internal/host/peakrss.go:16`、`:55`）。采样范围只限本实例启动的子进程：Windows 走 job 成员（`internal/host/peakrss_windows.go`），Unix 走进程组 + `/proc`（`internal/host/peakrss_unix.go`）。
- **点节点不再把人拽进链路**（`a99b74a7`）：选中一个节点只开节点面板、在链路条上显示计数，进链路必须显式点「看完整 / 只看主路径」——旧的自动路径过滤（大闭包按主路径打开并过滤整张图）被报成「点一下节点就跳进一条链」（`web/src/components/graph/GraphPanel.tsx:273`、`:279`、`:287-293`）。守卫用例钉住 `setPathSet(chain.pathSet)` 与 `focusOnly: true` 在源码里各只出现一次（`web/tests/graph-chain-view.test.ts:34`）。
- **小地图跟着视口走，边界夹进整图**（`a99b74a7`、`eebba901`）：框固定 184×116，绘制边界取世界包围盒与视口的并集、再逐轴夹进世界范围（放大时跟着看的那一块，全图态停在图的外接框），等比 cover 铺满、不拉伸，缩放/平移实时重算（`web/src/components/graph/GraphMinimap.tsx:129`、`:158`）。`a99b74a7` 先只取并集，`eebba901` 修掉「缩到全图时把空台面也一起放大」：640 宽空区 30.4%→7.2%，1280 宽卡片被图填满。小地图与右下角「实时」药丸改成一个 flex 列（`gap-1.5` 即 6px、右缘对齐），不再靠写死的偏移量（`web/src/components/graph/GraphPanel.tsx:786`；`web/src/app.css:752`）。
- **时间轴收进工具条，默认不占地图高度**（`eebba901`）：收起态的贴底横幅与摘要行整段删掉、默认不渲染；入口是地图工具条里带 Clock 图标的「时间轴」按钮（`aria-expanded`、`aria-controls` 指向 `tw-graph-timeline-body`），工具条按钮与条内「收起」驱动同一个状态，刷新后仍是收起（`web/src/components/graph/GraphToolbar.tsx:122-131`；`web/src/components/graph/GraphPanel.tsx:186`、`:544`）。展开时仍是原横幅，`has-timeline` 的抬升只在展开时生效（条高 44px）。
- **切链路树/力导向时重新取景**（`a99b74a7`）：视图切换调用 `fit({ margin: 0.12, capAtOne: true })`——四边各留 12%，算出来会放大到 >1 的压到 0.9；数据到达仍然不 re-frame，只有操作员点「适配画布」或切布局才动相机（`web/src/components/GraphCanvas.tsx:1015`、`:1024-1030`；`web/src/components/graph/GraphPanel.tsx:268-271`）。
- **派发门、展示层、执行者目录共读一个「可复核」判据**（`c7793f34`、`84bcb2f6`、`8e9f6b0f`）：三张面各自写过一份判据——派发门 `PrepareRunInput` 只查证据 id 在不在，展示层 readiness 查摘要，执行者读的前提目录查「摘要非空」——于是登记了原件但摘要为空的前提会在页面上显示「不可派发」却仍被派发；摘要非空但不是 SHA-256 hex 的行（手改账本、导入）又被目录读成可用。现在三处都调同一个谓词 `store.EvidenceVerifiable`：摘要折小写后必须是 64 位 SHA-256 hex，空摘要、长度不对、非 hex 一律不算可复核（`internal/store/evidence_meta.go:20`、`:24`、`:55`；目录侧 `internal/api/graph.go:217`）。写入口同时拒收非法摘要，以及「指向保全副本却没记摘要」「摘要与副本名不一致」两种坏行（`evidence_meta.go:24-32`）。用例：三面一致性 `internal/api/premise_state_consistency_test.go:212`、谓词本身 `internal/store/evidence_verifiable_test.go:20`、派发门两个方向 `internal/store/input_digest_test.go:22`（无摘要拒）与 `:52`（有摘要放行）。
- **界面与后端文案统一叫「复核」**（`89938427`、`4dda40bf`）：复核已是可运行时开关，再叫「固定复核」就与事实相反。后端用户可见字符串 4 处（`internal/api/agent.go`、`internal/api/review_settlement.go` 两处、`internal/lifecycle/lifecycle.go`），界面 8 处（`ProjectSettingsCard`、`graph/GraphLegend`、`lib/format`、`views/ProjectState` 各两处）。判据：`internal`、`web/src`、`docs`、`cmd`、`README.md`、`CHANGELOG.md` 里 `固定复核` 现在 0 处。
- **小地图改成整张图的缩略（LoL 式）**（`48fa6b53`）：不再裁切、也不跟随镜头——永远画整张图的完整形状，只有代表当前视野的框随缩放平移动，出卡片就贴边裁掉；视野已框住整图（覆盖 ≥97%）时不画框（`web/src/components/graph/GraphMinimap.tsx:47`、`:397`）。卡片定稿为固定 132×132 方卡（`:28`、`:67-74`）：按包围盒比例算过一版，链路树那种宽高比 0.36 的图算成 56px 宽，而表头的 `max-content` 把卡片撑到 105px，结果画布贴左、右侧 49px 死区（现网 1280×900 实测卡片 105×160、画布 56×132）；力导向每帧都在改包围盒，卡片跟着变宽变窄还会一直抖。现在卡片尺寸恒定、宽度只有一个来源（画布），用例钉住「恒为方卡」与「画布填满卡片、右侧无死区」（`web/tests/graph-minimap-live.test.ts:265`、`:279`）。
- **小地图三种手势不再互相打架**（`48fa6b53`）：单击定位延迟 250ms 才落地，好让双击先被认出来，所以双击不会先跳一下再回正中；拖动阈值 4px（手抖仍算单击），拖动中实时跟手、抬手不再补一次定位（`web/src/components/graph/GraphMinimap.tsx:35`、`:37`、`:601`）。三种手势写进 `title` 与 `aria-label`；用例 `web/tests/graph-minimap-live.test.ts:313`。

### 口径（既有设计，不是本次改动）

- **未达成的终点不进画布**：未达成、或已 `closed` 的终点不画成节点、也不连线，只在图谱左下「终点」卡显示；达成后才作为节点接到承载它证据的事实上（绘制侧 `web/src/graph/model.ts:575-580`，守卫用例 [`web/tests/graph-model.test.ts`](web/tests/graph-model.test.ts) 第 201 行「an unmet goal stays off the map, unconnected, until it is achieved」）。这是设计，不是缺陷：终点没有已完成的产出可以挂，硬画出来只能是一条猜出来的边。这条行为的权威说明在 [`docs/01-architecture.md`](docs/01-architecture.md) 第 93 段与第 107 段，本处只为让从变更记录翻进来的人不再当 bug 报。

## 1.0.0 — 2026-11-22

第一个「能交给别人安装、能说出自己是什么版本」的版本（本版不 tag、不出发布包，仅作本版口径）。

- 构建路径就是本地 gate 或直接 `go build`：`scripts/gate.ps1` / `gate.sh` 的第 8 步编 `cyber-harness` + `cyber-scan`、第 9 步编 `bin/tw` 与 `bin/tw-repair`、第 10 步写 `.build/build-manifest.json`（`cmd/tw-gate/main.go:20-25`）。版本号靠 `-ldflags "-X main.buildVersion=…"` 注入，不注入时 `tw --version` 输出 `tw dev`（`cmd/tw/main.go:52-55`、`:107`）。
- 安装与最小验证按 [`docs/02-deploy.md`](docs/02-deploy.md) 的「干净环境冷启动」与「从构建产物起一个实例」执行；判据是另一台机器只凭这两节能起来。

### 保留（1.0 具备的能力）

- **事实与意图图**：任务前提、产出、引用与固定上下文是显式关系；事实断言、线索与原始证据分开，撤回与复核否决是两件事。
- **任务生命周期**：方向派发、子任务、等待、交接、重试、停放与完成；每次有账本记录的执行都留输入、状态、用量与停止原因。
- **前提与复核**：必要前提须在当前断言版本上通过独立核验（全项目 `verified_only`）；成功方向进入复核阶段，复核绑定具体断言版本，未通过进 `review_failed`。
- **长会话状态管理**：笔记保住判断与回执（写入者、run、轮次、版本），压力周期以有效交接收口后轮换会话；记忆载体是笔记与账本，不是会话本身。
- **操作员监督**：暂停/继续、持久指令、人工接管、待办收件箱、运行停止；暂停原因分类落盘，操作员盖章与撤销写 `operator-actions.jsonl`。
- **工具审批**：需要人工决定的调用在 guardrail 命名空间挂起，由操作员批准或拒绝；裁判可插拔（`none`/`jev`/`llm`/`hybrid`），JEV 沿用 harness 原生实现。
- **执行观测与 token 分账**：AOP 采集运行事件、工具调用、命令与 HTTP 流量；token 按指挥官/执行者/裁判分账，只记录不设阈值。
- **模型与容量管理**：单一模型连接，shared 段在线热更新、产品段需重启；项目与实例并发上限、round timeout、idle 检测。
- **项目迁移**：`.twproj` 带账本快照与证据摘要，导入后固定以暂停状态落地。
- **场景由文档决定**：起点、终点、内置约束三份项目文档原文进提示词；红队与日常渗透是预设记录，接入方式由项目文档决定——三者都只是文档内容，代码里没有「场景」这个概念，也没有内置攻击步骤或渗透流程。新建对话框的场景按钮只填起点与终点，内置约束始终由人维护、不由预设代写。

### 修复（2026-11-22，按主题，括号内为提交）

- **丢 SYN 不再当成「不是超时」**：生产拨号、开桥与管理接口共用一份有界重试（0.3/1/2/4s，整段上限 8s），只重试建立连接阶段，认出 Windows 的丢 SYN 报错；请求发出后的读超时一律原样返回（`9221087f`、`a426baf9`、`1fe4fdf4`）。测试侧的裸拨号收进同一预算，数值只落在一处（`b02b967a`、`f743f9c7`、`a426baf9`）。
- **操作员盖章不可被会话抹掉**：目标记录 `MetByRun`，活着的指挥官轮次只能撤自己盖的章，操作员的手工章会话撤不动；盖章与撤销事后追加 `operator-actions.jsonl`（`8b06fa71`、`ac387a25`）。
- **暂停不再被误标**：控制面重启造成的暂停记为 `restart_pause`；「追加目标并继续」会清掉机器产生的暂停，原因未记录（`unrecorded`）的仍要求显式「继续项目」，界面如实说「未记录暂停原因」（`1fe4fdf4`、`b83c1fb4`）。
- **写者身份与写入口**：文档改动按写入者署名（`operator` 或指挥官那条 run id，空作者被 store 拒绝），历史行按控制台字段名（`updated_at`/`updated_by`）返回；session 写路由改为默认拒绝 + 显式白名单，文档写入口加操作员身份闸（`83ec773d`、`4176952d`）。
- **授权模型收敛**：约束只剩三份项目文档原文与审批模型，提示词不再带模板裁决句与授权元话术，只带文档原文与运行事实（`56221cc5`）。
- **控制台**：页头按频次分层、窄栏不再压瘪标题、面包屑与页名同组；图谱力导 quadtree 阈值从 600 降到 256，553 节点不再走 O(n²)（`2448821d`、`8f9a29e2`、`773ba23f`、`e30ce83e`）。

### 已知风险（1.0 不解决，只记录，不美化）

- **本机回环丢 SYN**：主机级故障，失败率随时间波动（实测在 0/300 到 21%/1200 之间摆动）。现在由有界重试兜住，但根因未消除；重试只覆盖建立连接阶段，请求发出后的任何错误原样上报，不重试。机制见 [`docs/01-architecture.md`](docs/01-architecture.md) 第 9 节。
- **未在 Linux 实测**：「两平台同等支持」是产品口径，本仓库没有在 Linux 上跑过真实实例；冷启动清单只对 Windows 负责，Linux 差异单列标注未实测。
- **暂停原因历史数据留白**：`pause_kind` 只在三条暂停写入口落盘。旧清单、建项目即暂停等情况本来就是空，界面显示「未记录暂停原因」而不是补一个默认值；现网样本 `p-015c82af` 就是这一状态。
- **模型端点是单点**：本机 `127.0.0.1:3425`（部署方自选）。网关卡住时没有外部看护，长跑会整场停摆；1.0 不含停摆自动重启。
- **隔离未实现**：进程隔离、网络隔离、MCP 命令注册表的项目/角色级可见性都没有实现，`GET /api/platform-boundary` 以 `unsupported_boundary`/`enforced=false` 机器可读列出，该状态不构成授权依据。
- **成本只观测不设闸**：每轮 token 只记录，不设费用上限或停轮闸门；长会话成本靠交接机制控制，不承诺上限。
