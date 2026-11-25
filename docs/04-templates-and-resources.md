# 04 平台构建形态与资源模板设计

状态：**设计定稿，未实施**。前身讨论见 docs/03 §3.3（构建标签与模板资源已核实事实）。

## 1. 背景与目标

harness（cyber-harness + gogo fork）的"模板机制"由三个**相互独立**的机制组成，平台侧逐一评估取用：

| 机制 | harness 实现 | 解决的问题 | 平台取舍 |
| --- | --- | --- | --- |
| 生成式 bundle | `tools/resources/templates_gen.go`（`go:build ignore` 生成器，go:generate 触发）：人工维护的 YAML 语料 → deflate 压缩 → `data/*.bin` → 生成 `go:embed` 文件（472K） | 语料进二进制：压缩、聚合、单一入口 | **取**（console dist + 未来语料） |
| 构建标签开关 | gogo fork `emptytemplates` / `noembed` | 不同构建形态要不要背这些字节 | **取**（`noconsole`，一个标签） |
| 运行时三层 | `resources.Set.Init`：embedded 默认 → 远端 cyberhub（merge/override）→ 磁盘缓存（24h TTL） | 发版后资源更新与覆盖 | **缓**（接口预留，与 docs/14 节点形态一起定） |

目标：console 嵌入方式升级（瘦身 + 确定性新鲜度）、构建标签约定补全、为未来平台语料定下形状——**不为不存在的语料建分发管道**。

## 2. 已核实事实（构成约束）

1. vite `outDir` 直写 `../internal/web/static`，该目录是 **committed 的构建产物**（internal/buildinfo/freshness.go 注释明示）；`go build` 不依赖 Node。
2. tw-gate step 5（`CheckEmbeddedFreshness`）已做"前端构建前后对 static 的快照 diff"——启发式新鲜度（预存脏树不算失败），无压缩。
3. console 嵌入体积 2.0M，无压缩；`cmd/tw` 70+ 测试文件每个测试二进制都背完整 console。
4. hub 依赖树**不得**引入 chainreactors 模板栈（docs/03 §3.3）——平台生成器的解码侧用标准库 `compress/flate`，**不用** `utils/encode`。
5. harness 侧生成器已存在且成熟（`tools/resources`），23b 内置高危规则集**不需要**平台侧新建机制——单源放 harness fork，走既有生成器。

## 3. Part A：console 生成器 + `noconsole` 标签（立即可做）

### 3.1 产物目录迁移

```
web/src …            源码（committed）
web/dist …           vite outDir（改为 gitignore，中间产物）
internal/web/bundle  生成器产物：manifest.json + data/*.bin（committed）
internal/web/*.go    服务代码（读 bundle，不再读 static）
internal/web/static  退役（git rm）
```

vite `outDir` 从 `../internal/web/static` 改为 `../web/dist`（node 侧标准位置）。committed 产物从"前端构建产物直混进 Go 包"变为"生成器输出的压缩 bundle"——`go build` 依旧零 Node 依赖。

### 3.2 生成器

`internal/web/generate/main.go`（`//go:build ignore`，`go:generate` 触发）：

- 输入 `web/dist`，对每个文件 `compress/flate` 压缩 → `internal/web/bundle/data/<n>.bin`；
- 生成 `bundle_gen.go`：`//go:embed data` + manifest 常量——`[{path, sha256, bytes}]`（path 是 dist 相对路径，即 URL 路径）；
- **一致性自检**：生成的 manifest 附带输入目录的聚合 hash；再次运行时输入未变则输出字节级不变（可重复构建）；
- 命令：`go generate ./internal/web`。

预估体积：2.0M → flate 后约 500–600K（JS/CSS 文本压缩率高）。

### 3.3 服务端

`Static()`/`Index()` 保持现有签名与缓存语义（`assets/` immutable、HTML no-cache），实现改为从 manifest 解压服务：

- 按请求解压目标文件（单文件 <300K，flate 解压 <5ms，可忽略）；
- Content-Type 按扩展名映射（与 `mime` 包现有用法一致）；
- `Index()` 读 manifest 中的 index.html 条目。

### 3.4 `noconsole` 标签

```
internal/web/console_full.go   //go:build !noconsole  — 生成 bundle 实现
internal/web/console_empty.go  //go:build noconsole   — Static/Index 返回 404 stub
```

- 默认（无标签）嵌 console——与"bundle committed"语义一致；
- `-tags noconsole`：测试构建与未来的无 UI 节点构建甩掉 bundle；
- 受影响测试（`web_test.go`、route_matrix 等 index.html 断言）加 `//go:build !noconsole`。

### 3.5 gate 检查升级

tw-gate step 5 从启发式快照升级为确定性比对：

- **主检查**：重算 `web/dist` 聚合 hash，与 `bundle_gen.go` manifest 内嵌的 hash 比对——不一致即失败（"dist 变了但没重新 generate"）；
- 现有 `CheckEmbeddedFreshness` 前后快照保留为兜底（防 generate 步骤被跳过）；
- 新增 smoke：`-tags noconsole go build ./cmd/tw`（只构建不测试）。

### 3.6 标签约定总表

| 目标 | 标签 | 来源 |
| --- | --- | --- |
| tw / hub 全量构建与测试 | `forceposix osusergo sqlite`（非 Windows 加 `netgo`） | 现状不变 |
| tw 测试 / 无 UI 节点 | 上述 + `noconsole` | **新增** |
| cyber-harness / scan | `forceposix emptytemplates noembed osusergo sqlite`（独立构建加 `full`） | 现状不变 |

`noconsole` 对 hub 包无效、可带可不带（同 `emptytemplates` 的既有约定）。

## 4. Part B：平台语料包形状（暂不建，约定先行）

若未来出现 tw 侧语料（报告模板、知识库种子、项目模板等），形状定为：

```
resources/<kind>/*.yaml          语料源（committed）
internal/resources/gen/main.go   生成器（同 Part A 形状：flate + manifest + 可重复）
internal/resources/bundle_gen.go 产物（committed）
```

API 面向 harness `resources.Set` 简化：`Kinds() / Get(kind, name)`，embedded 为默认，`TW_RESOURCES_DIR` 磁盘同名覆盖（override-only）。

**触发条件**（满足其一才建包）：真实语料 ≥2 个 kind；或 docs/14 定稿后节点需要 tw 下发语料。此前**不建**——不为空集建分发管道。

**guardrail 内置规则集（23b）不走平台侧**：权威在执行端（harness），单源放 harness fork 并用其**既有** `tools/resources` 生成器；tw 呈现经 audit rows 的 `builtin.` 前缀（P3 已做），不复制语料。

## 5. Part C：运行时三层（缓，与 docs/14 绑定）

仅定接口方向，不实现：

- hub 侧预留 `GET /api/resources/manifest`（kinds/版本/sha256）与 `GET /api/resources/{kind}/{name}`；
- 节点侧获取模式（内嵌 vs 启动时从 hub 拉 vs 磁盘预置）与节点形态一起在 docs/14 定；
- merge/override 语义沿用 harness 的现成答案；磁盘缓存 TTL 抄 24h。

## 6. 实施步骤

| 步骤 | 内容 | 验证 |
| --- | --- | --- |
| T1 | Part A 全部：outDir 迁移、生成器、noconsole、gate 升级、static 退役 | gate 全绿；`bin/tw` 体积对比（预期 -1.4M）；`-tags noconsole` 构建可跑；web_test/route_matrix 两种标签下过 |
| T2 | harness 23b 内置规则集走 harness 既有生成器（并入 P5） | 23b 测试 + FORK.md 登记 |
| T3 | tw 语料包 + 下发层 | 条件触发（§4），与 docs/14 同批 |

## 7. 风险与回滚

| 风险 | 缓解 |
| --- | --- |
| dist 不再 committed，review diff 可读性下降 | bundle manifest 含 per-file sha256，可追溯；review 关注 web/src 源码 diff |
| CI 需要 Node（原本就要求，gate step 4 已依赖 pnpm） | 无新增依赖 |
| 双标签测试矩阵成本 | gate 默认只跑 `!noconsole` 全量；noconsole 只做 smoke build |
| flate 解压每请求开销 | 单文件 <300K、解压 <5ms；assets immutable 缓存不变 |

回滚：T1 revert 即回旧形态（产物目录切换是纯文件移动 + 服务代码单文件替换）；T2/T3 各自独立 revert。
