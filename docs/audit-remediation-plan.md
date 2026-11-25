# 4zrcin 审计整改方案

> 状态：提案，未实施。  
> 基线：`HEAD = 3fa69b2`（两轮审计时的当前提交）。  
> 来源：第一轮架构审计（F1–F8）与第二轮补充审计（N1–N6）。  
> 本文只描述整改方案与验收标准；实施完成后按项目约定合并或删除，不把方案当成现行实现文档。

## 0. 目标、边界与原则

### 0.1 目标

1. **恢复可信门禁**：`go test ./... -p 1 -count=1` 与前端检查回到全绿，测试失败不再被收尾竞态淹没。
2. **收紧内部边界**：复核只读凭据只能读到它需要重算的事实、证据和运行事件，不能进行全项目扫描。
3. **消除半完成状态**：新建项目、保存项目文档不再留下“接口成功但数据不完整”或“页面 404 但项目存在”的状态。
4. **控制长期成本**：账本历史、态势页聚合和重复轮询有明确的容量与性能边界。
5. **补齐工程卫生**：依赖、格式、路由矩阵、CI 和符号链接防护有可持续的检查方式。

### 0.2 非目标

- 不新增硬编码的授权范围、目标白名单或攻击步骤；
- 不改变“账本是唯一真值”“复核独立于发起方”的设计取向；
- 不为了性能而删除审计数据或静默清理历史；
- 不在启动/读取路径隐式修复数据；修复必须显式、可预览、可回滚；
- 不把 0.x 的接口与账本格式承诺为稳定版。

### 0.3 工作原则

- **先测试基线，后改行为**：F4 不修完，后续改动无法判断是否回归。
- **每项行为修复配一条复现测试**：特别是失败注入和边界拒绝。
- **一次提交只解决一个主题**：权限、原子性、性能、UI 分开，避免大杂烩。
- **先小步止血，再做结构改造**：能用补偿/收窄解决的，不立刻重构全模块。
- **涉及账本 schema 的改动先备份 `data/`**，并保留 `tw-repair` 的显式修复路径。

---

## 1. 问题总表

等级说明：

- **P0**：正确性、权限边界或门禁问题，先修；
- **P1**：可靠性、性能或数据一致性，紧随其后；
- **P2**：体验、工程卫生和防御性加固。

| ID | 问题 | 等级 | 阶段 | 依赖 | 主要证据 |
|---|---|---|---|---|---|
| F4 | 当前 HEAD 测试基线红：旧提示词断言 + 测试收尾竞态 | P0 | 0 | 无 | `internal/decider/runner.go:621-629`、`internal/api/pause_resume_test.go:52-75`、`cmd/tw/hub_create_options_test.go:73-76` |
| F1 | 复核只读凭据能读全量 facts/evidence 和既往 reviews | P0 | 1 | F4 | `cmd/tw/auth.go:586-621`、`internal/api/api.go:2814-2837`、`internal/collab/collab.go:58-66` |
| F2 | 新建项目不是原子操作，失败会留下无目标/不可进入项目 | P0 | 1 | F4 | `cmd/tw/hub.go:1726-1769`、`cmd/tw/hub.go:1525-1531` |
| F3 | 文档保存跨 SQLite 与 `project.json`，失败后两处不一致 | P0 | 1 | F4 | `internal/api/documents.go:182-208`、`cmd/tw/hub.go:339-347` |
| F6 | `updateProject` 与 `deleteProject` 并发窗口可导致空指针/500 | P1 | 1 | F4 | `cmd/tw/hub.go:2108`、`:2221`、`:2258-2355` |
| N1 | `versions`/`Memory.History` 只增不减，启动加载全部历史 | P1 | 2 | F4 | `internal/store/sqlite.go:158-172`、`internal/store/memory.go:356-360` |
| N2 | Fleet 每 3 秒对每个项目做多次独立读取，快照不一致 | P1 | 2 | F4 | `internal/fleet/fleet.go:140-161` |
| N3 | 项目壳与页面重复轮询 board/inbox | P1 | 2 | F4 | `web/src/App.tsx:79`、`web/src/views/ProjectState.tsx:108`、`web/src/views/Messages.tsx:195` |
| N4 | `Snapshot()` 对 Messages/Documents 只做浅拷贝 | P2 | 2 | F4 | `internal/store/store.go:1533-1571` |
| F5 | 品牌字标固定白色，浅色主题不可见 | P1 | 3 | F4 | `web/src/components/Brand.tsx:13-18`、`web/packages/theme/src/ThemeProvider.tsx:21-46` |
| F7 | 根依赖 `express`/`vm2` 未使用 | P2 | 3 | 无 | `package.json:14-17`、`pnpm-lock.yaml:11-14` |
| N5 | `make test` 并行执行，与 gate/README 的串行策略不一致 | P2 | 3 | F4 | `Makefile:61-62` |
| N6 | 报告目录路径检查不拒绝符号链接 | P2 | 3 | F4 | `internal/report/validate.go:257-272`、`internal/api/api.go:3592-3628` |
| F8 | 超大文件、提示词字符串替换、无 CI、格式漂移 | P2 | 3 | F4 | `internal/api/api.go`（约 5371 行）、`internal/decider/decider.go:418-431`、`gofmt -l` |
| N7 | 项目 API 与 agent API 双路由表容易漂移 | P2 | 3 | F4 | `internal/api/api.go:586-`、`internal/api/agent.go:215-` |

---

## 2. 实施阶段

### 阶段 0：恢复测试基线（先做）

**范围**：F4、N5。

**为什么先做**：当前 `go test ./internal/api` 有 6 个 TempDir cleanup 失败，`go test ./internal/decider` 3 次中 2 次失败，`cmd/tw` 有一个旧文案断言失败。此时修改权限或原子性，无法区分“新回归”和“既有噪声”。

**步骤**

1. 更新 `cmd/tw/hub_create_options_test.go:73-76` 的旧提示词断言，改为当前实现（`internal/decider/decider.go:424` 的 `复核已关闭。必要前提的准入结果与阻塞原因见 readiness。`），并保留“复核开启时不得出现该标记”的对照断言。
2. 测试 helper 统一关闭账本：
   - `internal/api/pause_resume_test.go:52-75` 的 `newPauseServer` 增加 `t.Cleanup(func(){ _ = st.Close() })`；
   - `internal/decider/commander_test.go:38-45` 的 `newStore` 同样处理；
   - 其余直接 `store.Open` 的测试按同一模式补齐。
3. 让测试等待后台收尾：
   - `stopRunning`（`internal/api/redispatch_test.go:194-202`）除运行数归零外，等待 `pump`/`finish` 不再持有写锁；
   - 在 `internal/decider/runner.go:621-629` 的 `Run` 返回后、`refreshIdleOperatorRequest` 前检查 `ctx.Err()`，避免取消后继续写入。
4. `make test` 改为串行，与 gate 一致：
   ```make
   test:
       go test ./... -p 1 -count=1
   ```
5. 连续运行 3 次门禁，确认无 TempDir cleanup 失败；必要时把测试收尾检查纳入 gate。

**验收**

- `go test ./... -p 1 -count=1` 连续 3 次通过；
- `go test ./internal/api -count=1`、`go test ./internal/decider -count=1` 不再出现 `directory not empty`；
- `TestCreateProjectOptionsApplyBeforeAttach` 恢复通过；
- 没有新增 `time.Sleep` 作为主要同步手段。

**风险**

- `runner.loop` 的 ctx 检查可能改变取消后的最后一次写入行为；用现有取消/结算测试确认“取消后不再产生新状态”是期望。
- 测试 helper 关闭 store 后，若仍有 goroutine 使用，会暴露真实生命周期问题，不要用重试掩盖。

---

### 阶段 1：权限边界与原子性

**范围**：F1、F2、F3、F6。

#### 1.1 F1：复核只读凭据按事实收窄

**现状**

`collab.ReadOnlyCredential` 的注释明确写着“no list-wide scouting”（`internal/collab/collab.go:58-66`），但 `verifierReadAllowed` 允许 `GET /api/facts`、`GET /api/evidence` 全量列表（`cmd/tw/auth.go:604-618`）。`getFact` 对非 harness session 返回 `s.Store.Reviews(id)`（`internal/api/api.go:2837`），复核凭据不是 harness session，因此能读既往 verdict。

**方案**

推荐“凭据携带允许读取的事实集合”，而不是只在 handler 里做黑名单：

1. 扩展 `ReadOnlyCredential`：
   ```go
   type ReadOnlyCredential struct {
       ProjectID string
       RunID     string
       FactIDs   []string // 本次复核要读的事实（目标事实 + 明确引用的前提）
       ExpiresAt time.Time
   }
   ```
2. `Registry.IssueReadOnly(projectID, runID string, factIDs ...string)` 签发时写入集合；`RevokeRun` 仍按 run 撤销。
3. `cmd/tw` 的 auth gate 把整个 `ReadOnlyCredential` 放进 context（或一个 `verifierScope`），不再只暴露 `roleVerifier`。
4. `verifierReadAllowed` 的路径白名单改为：
   - 允许 `GET /facts/{id}`，但 handler 用凭据检查 id 是否在允许集合；
   - 允许 `GET /facts/{id}/history`，同样按 id 检查；
   - 取消 `GET /facts` 全量列表，或返回只含允许事实的过滤结果；
   - `GET /evidence/{id}` 只允许被允许事实引用的证据 id；取消全量 evidence 列表；
   - `GET /runs/{id}/events` 保持现有 `IsReviewerSurface` 的 support-run 过滤。
5. `getFact` 在复核上下文下：
   - 不返回 `reviews`，或只返回与当前被复核 claim 相绑定、且明确允许的历史；
   - 仍返回目标事实正文和它引用的证据。
6. 复核 prompt/`verifier` 侧传入事实 id 与前提 id；如果某条前提必须可读，显式加入集合，而不是放开全项目。

**验收**

- reviewer 凭据请求目标事实：200，正文和证据可读；
- reviewer 凭据请求其他事实、全量 facts/evidence 列表、`/reviews`：404 或空集合；
- reviewer 仍能读目标 run 的非 support 事件；
- 现有 `cmd/tw/verifier_read_allowlist_test.go` 更新为“路径 + 内容”双层断言；
- 新增一条回归测试：reviewer 读 `GET /p/x/api/facts/{other}` 不返回 `reviews`，且不能枚举其他事实。

**风险**

- 复核器可能确实需要读取被引用前提；用“显式允许集合”解决，不要退回全量扫描。
- 只读凭据签名变化涉及 `cmd/tw/hub.go:482-489` 与 `internal/verifier`，需要同时更新测试和调用方。

#### 1.2 F2：新建项目原子化

**现状**

`serveProjects` 的创建顺序是：`mgr.Create` → 写 options → `attach` → 写 goal。options 失败时直接 500 并 return，`attach` 未执行，项目留在 manager/磁盘但 `/p/<id>/` 因 `h.kit(id)==nil` 返回 404；goal 失败只记日志，最终仍 201。

**方案**

推荐“失败清理 + 不在必需数据缺失时返回成功”：

1. 在 `mgr.Create` 成功后建立清理边界：
   ```go
   committed := false
   defer func() {
       if committed { return }
       _ = p.Store.Close()
       _ = os.RemoveAll(p.Dir)
       h.mgr.Remove(p.ID)
   }()
   ```
   清理失败要记录并返回明确错误，不能吞掉。
2. 所有必需写入在 `attach` 前完成：
   - options（reviews/max_runs/round_timeout）；
   - description；
   - 第一个 goal；
   - premise policy（如果需要）。
3. 任一必需写入失败：执行清理，返回 5xx，响应中说明没有项目被保留；不要返回 `project_id` 让操作员去一个 404 页面。
4. 全部成功后 `h.attach(p)`，再置 `committed = true`，返回 201。
5. 若“无 goal 也允许创建”是程序化调用者的需求，保留显式分支，但控制台默认必须要求终点；不要在 `goal` 字段非空时吞掉失败。

**验收**

- 注入 options 写入失败：返回 5xx，`mgr.Get` 为 nil，目录不存在，`GET /api/projects` 不出现该项目；
- 注入 goal 写入失败：返回 5xx，不返回 201，不留下半项目；
- 正常创建：201，goal、options、manifest、kit 全部存在；
- 新增失败注入测试覆盖两条路径。

#### 1.3 F3：文档保存的双写一致性

**现状**

`SaveDocument` 先提交账本新 revision，再 `PersistDoc` 写 `project.json`。第二步失败返回 500，但账本已是新文本、manifest 仍旧，重启后文档页和派发简报会看到两个版本。

**方案**

分两步走，先止血再收敛：

**短期（本阶段）**：

1. 在项目级锁内执行保存（复用 `controlMu` 或新增 `docWriteMu`，确保同一项目的文档保存串行）。
2. 保存前记录 manifest 旧字节/旧文本与账本当前 revision。
3. 先写 manifest，再提交账本：
   - 账本 `SaveDocument(expected)` 返回 `ErrDocumentConflict` 或 I/O 失败时，恢复 manifest 旧内容；
   - 恢复失败时返回带 `document_state_diverged` 的 500，并写一条可审计日志；不要假装成功。
4. 增加一个显式的 `tw-repair` 检查/修复模式：以账本 revision 为准重建 manifest，预览后 `--commit`。

**长期（后续迭代）**：

- 让账本文档成为唯一真值，`project.json` 只保存可重建投影；
- `scopeOf`/brief/export 从账本或投影读取，避免两个写点；
- 启动路径不隐式修复，只报告差异。

**验收**

- 注入 manifest 写失败：返回 5xx，账本 revision 与 manifest 文本都保持旧值；
- 注入账本写失败：返回 5xx，manifest 恢复旧值；
- 正常保存：账本 revision +1，manifest 文本一致；
- `tw-repair` 预览能看到差异，`--commit` 后一致；
- 文档历史接口仍能读到旧 revision。

#### 1.4 F6：`updateProject` 与删除的并发窗口

**现状**

`updateProject` 在 `cmd/tw/hub.go:2108` 取项目后长时间写入，末尾 `h.info(h.mgr.Get(id))` 未判空；`deleteProject` 可关闭 store、删目录并 `mgr.Remove`，窗口内会空指针或对已关闭账本操作。

**方案**

1. 最小修复：末尾重新取项目并判空，项目已删除则返回 404/410；不要调用 `h.info(nil)`。
2. 更完整：给 `Project` 增加“正在删除”状态（或复用 hub kit 的 deleting 门），`updateProject` 在每次写入前检查；删除期间设置该状态，更新请求返回 409。
3. 若要让“设置保存”和“删除”严格互斥，使用每项目锁，而不是全局 `transferMu`，避免删除的 90 秒 drain 阻塞其他项目的设置。

**验收**

- 并发测试：一个 goroutine 删除、一个 goroutine 保存设置，`-race` 下无 panic、无空响应；
- 删除后更新返回 404/410 或 409，错误码明确；
- 删除失败重建 kit 的路径仍可工作（现有 `deleteProject` 回退分支）。

---

### 阶段 2：数据容量与性能

**范围**：N1、N2、N3、N4。

#### 2.1 N1：版本历史的归档/加载策略

**现状**

`versions` 表只增不减；启动时 `readSQLiteDB` 把全部历史加载进 `Memory.History`。规模测试的合成账本为 4000 facts、5000 messages、1000 runs；更大账本会让启动时间和常驻内存线性增长。

**方案**

1. 先测量：在 `scale_test.go` 增加 5k/50k 版本历史场景，记录启动加载时间、`Memory.History` 大小、`Snapshot`/`RouteFactChanges` 延迟；没有数据前不改结构。
2. 设计“热历史 + 冷归档”：
   - 保留最近 N 条（按 seq 或时间窗口）在 `Memory.History`，供 attention/routing/change window 使用；
   - 更早的 `versions` 行移到 `versions-archive` 或按文件分段存储，保留原 seq、payload 和哈希链；
   - `History(kind,id)`/document history 在需要时从冷存储按索引读取；
   - 归档不删除审计记录，只改变加载方式。
3. 若暂不做归档，至少增加：
   - 启动时只加载 `seq > 最近窗口` 的历史；
   - 旧历史通过 SQLite 按需查询；
   - 文档明确“历史不裁剪，查询按需”。
4. 所有 schema/存储变更前要求备份 `data/`，并提供预览式修复；不做启动时隐式迁移。

**验收**

- 50k 版本历史的启动时间与常驻内存有基线对比；
- `History`、document history、change window、routing 测试全部通过；
- 旧账本无需重写即可读取；
- `.twproj` 导出仍包含完整审计链（或明确声明归档文件也随包导出）。

#### 2.2 N2：Fleet 单快照

**现状**

`fleet.Build` 对每个项目做 `board.Build`、`ListIntents`、`Runs`、`Tokens` 多次独立读取，页面每 3 秒轮询。

**方案**

1. `fleet.Source` 只暴露 `Store`；在 `Build` 内每个项目取一次 `Snapshot()`：
   ```go
   db := src.Store.Snapshot()
   b := board.BuildFromDB(db, 20)
   // intents/runs/token 聚合都从同一个 db 派生
   ```
2. 为 `board.Tokens` 增加接受 `[]model.Run` 或 `store.DB` 的入口，避免再次 `Store.Runs()`。
3. `hub.serveFleet` 的项目列表和 decider status 可继续独立读取；只有同一项目内的 board/rows/token 必须同快照。
4. 若还需要更低成本，再考虑按项目缓存 board 结果，用 `revision`/`ContentHash` 失效；先不做。

**验收**

- 同一项目的一次 fleet 构建只调用一次 `Snapshot()`（可用计数 fake 测试）；
- fleet 的 counts 与 recent runs 来自同一快照；
- 现有 fleet 测试通过，新增并发写入下的快照一致性测试。

#### 2.3 N3：收敛前端 board/inbox 轮询

**现状**

`App` 已轮询 board；`ProjectState`、`Messages` 又各自轮询 board；`GraphView` 与 `Overview`/`Access` 各自轮询 operator inbox。

**方案**

1. 把项目级 `board` 和 `operatorInbox` 提到 `ProjectApp`/`GraphView` 的 context：
   - 子视图通过 props/context 读取同一个 `AsyncState`；
   - 删除 `ProjectState.tsx:108`、`Messages.tsx:195` 的重复 board 轮询；
   - 删除 `Overview.tsx:122`、`Access.tsx:60` 的重复 inbox 轮询，或让它们订阅共享状态。
2. 若某些页面必须独立刷新，使用 keyed shared poll（同一 URL 的 in-flight 请求合并 + 结果缓存），而不是每个组件各自 `fetch`。
3. 对 board 增加 `ETag`/`If-None-Match` 或基于 `changes` 的失效；先做共享轮询，再评估是否值得改协议。

**验收**

- 项目页同一时刻每个共享端点只有一个 in-flight 请求（fake fetch 计数测试）；
- 切换视图不会清空/重置 board 的新鲜度状态；
- 现有 `fa4-hooks-store`、`fa5-legacy-arrays-views` 等测试通过；
- 页面行为不变，只减少重复请求。

#### 2.4 N4：Snapshot 的 Message/Document detach

**现状**

`DB.clone` 只复制 `Messages`/`Documents` 的 slice header，内部 slice 仍共享。当前调用方基本只读，因此是潜在 aliasing。

**方案**

1. 增加 `detachMessage`、`detachDocument`，复制 `FactRefs`、`DirectiveRecipients`、`Chain` 等切片。
2. 在 `DB.clone` 中对 `Messages`、`Documents` 使用 detach。
3. 加测试：修改 `Snapshot().Messages[0].FactRefs`，随后读 live store，断言不变。

**验收**

- 快照修改不污染 store；
- `Snapshot` 性能无明显退化（规模测试对比）。

---

### 阶段 3：体验、依赖与工程卫生

**范围**：F5、F7、F8、N5、N6、N7。

#### 3.1 F5：品牌随主题切换

1. `Brand` 根据 `useTheme().isDark` 选择 `wordmark-white.svg` / `wordmark-black.svg`；或用 CSS mask + `currentColor`。
2. 删除已无引用的 `.tw-brand-cn` / `.dark .tw-brand-cn` 死样式。
3. 增加浅色/深色渲染测试：断言 `src` 或可见样式随主题变化；如无主题切换入口，仍保留 `ThemeProvider initial="light"` 的组件测试。

**验收**：浅色、深色下品牌均可见；无死 CSS；测试覆盖两种主题。

#### 3.2 F7：移除未使用依赖

1. 删除根 `package.json` 的 `express`、`vm2`；
2. 更新 `pnpm-lock.yaml`；
3. 搜索确认没有脚本/验收工具依赖它们；若有，移到 `devDependencies` 并在脚本 README 说明；
4. gate 增加“根依赖无未使用项”的检查（可先用 `rg`/脚本，不必引入新工具）。

**验收**：`pnpm install --frozen-lockfile` 通过；`rg` 无 import；安装体积下降；无运行时行为变化。

#### 3.3 F8：结构、提示词与门禁

1. **超大文件**：把 `internal/api/api.go` 按领域拆成 `project/intent/review/report/operator` 子文件或子服务；先做文件级拆分，保持 `Server` 门面不变，避免一次大重构。
2. **提示词字符串替换**：`commanderIdentityFor` 改为显式模板/分支，或对 `strings.Replace` 命中数做断言，避免基线文案改变后静默退回复核开启语义。
3. **CI/格式**：增加 CI（至少 gate + gofmt + 前端 typecheck/test）；`gofmt -l` 清零；把 `gofmt -l` 纳入 gate 的非阻断/阻断检查。
4. **路由矩阵**：把 `sessionWriteRoutes`、`agentRouteHidden`、`registerAgent` 收敛为声明式表，增加“路由漂移”测试。
5. **N5**：`make test` 串行化（阶段 0 已做）。

**验收**：CI 能在干净环境跑 gate；`gofmt -l` 为空；新增路由必须同时更新矩阵测试；提示词测试不再依赖整句文案的偶然匹配。

#### 3.4 N6：报告文件拒绝符号链接

1. `safeJoin` 之后对 `abs` 做 `Lstat`，若为 symlink 直接拒绝；
2. 或 `EvalSymlinks` 后再次确认仍在报告目录内；
3. 增加测试：在报告目录创建指向目录外文件的符号链接，断言 403/404，不返回目标内容；
4. 与证据路径处理保持一致，避免只修一个入口。

**验收**：符号链接被拒绝；普通报告文件仍可预览/下载；错误信息不泄露目标路径内容。

---

## 3. 依赖关系与建议排期

| 阶段 | 内容 | 依赖 | 工作量 |
|---|---|---|---|
| 0 | F4 测试基线、N5 make test | 无 | S |
| 1 | F1 复核收窄 | F4 | M |
| 1 | F2 新建项目原子化 | F4 | S/M |
| 1 | F3 文档双写 | F4、F2 的清理模式可复用 | M |
| 1 | F6 更新/删除并发 | F4 | S |
| 2 | N2 Fleet 单快照 | F4 | S |
| 2 | N3 前端轮询收敛 | F4、N2 可独立 | S/M |
| 2 | N1 历史归档 | F4；需先测量 | M/L |
| 2 | N4 Snapshot detach | F4 | S |
| 3 | F5 品牌主题 | 无 | S |
| 3 | F7 依赖清理 | 无 | S |
| 3 | F8 结构/CI/路由 | 阶段 0–2 完成后 | M |
| 3 | N6 报告符号链接 | F4 | S |

**建议并行方式**

- 阶段 0 单独一个提交/PR，先把门禁变绿；
- F1 与 F2/F3 可由不同人并行，但都要基于阶段 0；
- N2/N3 是性能/体验，等权限和原子性稳定后再做，避免同时改前后端行为；
- F8 的结构拆分放最后，避免大 diff 干扰前面的行为修复。

---

## 4. 总体验收清单

### 4.1 后端

- [ ] `go test ./... -p 1 -count=1` 连续 3 次通过；
- [ ] `go test -race` 覆盖 `internal/store`、`internal/api`、`internal/lifecycle`、`internal/decider`（至少受影响包）；
- [ ] 新建项目失败注入：无孤儿项目、无 201 无目标；
- [ ] 文档保存失败注入：账本与 manifest 不分裂；
- [ ] reviewer 凭据：目标事实可读，其他事实/reviews/全量列表不可读；
- [ ] 并发删除+更新：无 panic、返回明确状态码；
- [ ] fleet 单快照：计数与列表一致；
- [ ] 报告符号链接：拒绝；
- [ ] 版本历史：有规模基线，启动内存/时间可解释。

### 4.2 前端

- [ ] `pnpm --dir web typecheck` 通过；
- [ ] `pnpm --dir web test` 通过（Node 24）；
- [ ] 项目页同一端点只有一个 in-flight 轮询；
- [ ] 浅色/深色品牌均可见，有测试；
- [ ] 现有图谱、设置、运行、消息、无障碍测试不回归。

### 4.3 交付

- [ ] `./scripts/gate.sh` 通过；
- [ ] `gofmt -l cmd internal` 为空；
- [ ] 根依赖无未使用项；
- [ ] 每个修复都有失败注入/边界回归测试；
- [ ] 文档只写当前实现；方案实施完成后合并或删除本文件；
- [ ] 涉及 schema/账本变更的提交附带 `data/` 备份与回滚说明。

---

## 5. 风险与回滚

| 变更 | 风险 | 回滚/缓解 |
|---|---|---|
| 复核凭据收窄 | 复核器需要读取前提，集合漏项导致复核失败 | 允许集合显式包含目标事实 + 引用前提；先加测试再切换 |
| 新建项目清理 | 清理失败或误删正常项目 | 只在 `committed=false` 时清理；清理失败返回路径，不静默 |
| 文档双写补偿 | manifest 恢复失败仍可能分裂 | 返回 `document_state_diverged`；`tw-repair` 显式修复；不做隐式启动修复 |
| 历史归档 | 旧历史读取/导出不完整 | 保留原 seq/payload/哈希链；先测量、后迁移；备份 `data/` |
| 路由表重构 | 权限面变化 | 声明式表 + 漂移测试；逐路由迁移，不同时改行为 |
| 前端轮询收敛 | 页面新鲜度下降 | 共享状态保留手动 reload；用 fake fetch 计数测试 |
| 报告 symlink 拒绝 | 正常报告文件被误判 | 只拒绝 symlink，不拒绝普通文件；测试普通预览 |

**总原则**：任何会改变权限、账本或导出格式的改动，都先做“预览/干跑/失败注入”，再提交；不使用静默重试或默认值掩盖失败。

---

## 6. 实施后的文档处理

- 本文件是整改方案，不是现行架构文档；每项完成后，把“当前实现”写回 `docs/01-architecture.md` 或对应模块注释。
- 不在架构文档中保留已删除功能的旧描述。
- 如果某项方案被否决，在本文件记录理由和替代方案，再在最终清理时删除本文件。
