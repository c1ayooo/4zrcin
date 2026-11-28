# 隔离验收与维护脚本

本目录提供协议、界面与演示目标的本地维护工具。基础检查见 [贡献指南](../../CONTRIBUTING.md)，验证边界见 [核心架构](../../docs/01-architecture.md)。所有验证使用临时目录、虚构目标和假凭据，不读取或改写正在使用的项目。

## 日常回归

基础检查（gate、Go 与前端测试、文档链接检查）只有一份实现，见[贡献指南](../../CONTRIBUTING.md#开发环境与检查)；本目录只列特有的脚本。前端测试包含真实 React 组件的草稿归属、版本冲突、统一模型连接和连通性检测等状态回归，不需要 Chrome、运行中的控制面或外部模型。

## 假模型回合验收（local-turn）

```bash
bash scripts/acceptance/local-turn.sh
```

一个 zrcin（`-tags inprochost`）单文件实例加本地假模型服务（`fake-model.py`，纯标准库），把 docs/19 P6 一直缺模型没覆盖的「一次本地意图跑通，含一次扫描工具调用」在隔离临时实例里真跑一遍：控制台 → 内嵌 Hub → 本机节点 → 假模型（SSE 回一次 `bash` 工具调用）→ `curl` 打到假模型服务的本地目标 → 工具结果随第二个模型请求回到模型 → 最终回复 → 意图 `done`。脚本逐条断言：实例 `host_event=ready` 无降级、目标 `127.0.0.1` 经创建项目时的 `scope_rules` 进入围栏（产品内正当流程，不做任何放宽）、run 事件里 `bash` 的 `tool_result` `ok=true` 且带目标真实应答的标记正文、假模型日志的第二个回合请求带着 `role=tool` 的工具结果。冷构建约 40s，热构建约 10s。共用脚手架在 `lib.sh`（`single-file.sh` 与本脚本 source 同一份）。

它证明的是链路与装配（实例装配、模型连接、SSE 解析、工具派发、围栏判定、结果回传），**不证明模型好坏**：假模型的应答是写死的，扫描发现能力、真实模型的提示词遵循与覆盖率仍然只有真实模型实验能回答（见下「演示目标」）。

## 浏览器界面检查

浏览器脚本需要本机 Chrome，具体依赖与环境变量见各脚本开头。先构建前端，再从仓库根目录运行，例如：

```bash
node scripts/acceptance/projects-ui.cjs
node scripts/acceptance/instructions-ui.cjs
node scripts/acceptance/focus-context-ui.cjs
node scripts/acceptance/attention-ui.cjs
node scripts/acceptance/upgrade-ui.cjs
node scripts/acceptance/settings-ui.cjs
node scripts/acceptance/chat-ui.cjs      # 自包含合成数据，不依赖 .build fixture
```

这些脚本使用合成 HTTP 响应，检查项目删除、持久指令、图谱与上下文编辑、事件待办、误点继续后再次暂停的收口、证据不足反馈、目标编辑、操作员提醒、原生多节点绑定、群发对象切换、运行事件参数解码与视口恢复、单模型连接（保存/显式检测/复核并发/项目运行设置）及窄屏布局；截图和结果写入 `.build/ui-qa/`。`chat-ui.cjs` 的数据全部写在脚本内：图谱右侧面板只是「通信」入口，统一下发框在通信页，检查对象/时效切换、请求体（含幂等键复用）、steer、持久指令与各自回执。部分其他脚本依赖本地未入库 fixture，公开 checkout 未必能直接运行全部脚本。不要把真实项目抓取结果作为公开 fixture 提交。离线 UI 检查不证明服务端行为或模型效果。

README 截图使用当前前端构建与离线 fixture，复现命令：

```bash
pnpm --dir web build
node scripts/acceptance/focus-context-ui.cjs
node scripts/acceptance/console-gallery.cjs
```

前者生成图谱、关系检查器和焦点界面；后者生成模型连接、执行者监督和项目管理界面，只允许读取合成 API，不连接控制面、真实项目或模型。检查截图后，将选用图片更新到 `docs/screenshots/`。

## 真实 harness 适配验证

从独立 fork 构建 `cyber-harness` 和 `cyber-scan` 后，可显式启用集成测试：

```powershell
$env:ZRCIN_HARNESS_BIN = (Resolve-Path ./bin/cyber-harness.exe).Path
$env:ZRCIN_SCAN_BIN = (Resolve-Path ./bin/cyber-scan.exe).Path
go test ./internal/api ./internal/host ./internal/node ./cmd/zrcin -run 'Harness|SplitHost|NativeMultiScan' -count=1 -v
```

测试使用临时项目、本地假模型网关和受控 fixture，启动测试宿主并在退出时停止它。它们验证 Skill 展开、协议和宿主适配，不证明实际漏洞发现能力。未设置变量时相关测试跳过。

## 演示目标

`target.py` 是含预置缺陷的虚构站点，仅供本机隔离验收。在专用终端中运行：

```powershell
$env:ZRCIN_TARGET_HOST = '127.0.0.1'
python scripts/acceptance/target.py
```

默认端口为 18099。真实模型实验必须使用独立控制面端口、全新数据目录及专用模型配置，明确授权范围后由操作员启动。固定样本的结果不能推广为真实业务覆盖率；比较协作策略时应保持模型、初始数据与资源条件一致，并记录独立复现、漏测、耗时、token 和人工介入。
