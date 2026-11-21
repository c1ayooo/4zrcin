# 天吴部署清单（三个 exe）

一次部署要同时核对三个可执行文件。任何一个旧了，「修复是否在跑」都是假结论——
2026-11-21 的评估器用量问题就是只换了 hub、漏了真正执行会话的 scan，连续两次窗口白做。

| # | 文件 | 来源 | 从哪启动 | 装的是什么 |
|---|------|------|----------|------------|
| 1 | `tianwu\.build\tw-zw.exe` | 本仓 `cmd/tw` | 人工/脚本直接启动 | 控制面：HTTP API、控制台静态资源、调度、账本 |
| 2 | `cyber-harness\cyber-harness.exe` | fork hub | tw 参数 `--harness <path>`，作为 tw 的子进程 | Hub：AOP 端点、会话托管、节点管理 |
| 3 | `tianwu\bin\cyber-scan.exe` | fork scan 节点 | tw 参数 `--scan <path>`，作为 tw 的子进程 | **真正执行会话与工具调用的那一侧** |

结论先说：**改 agent / 会话 / 评估器 / 工具实现，落在 #3；改 AOP 托管与节点管理，落在 #2；
改控制面、账本、界面，落在 #1。** 只换 hub 而会话代码在 scan 里，等于没换。

## 验收方法（每个 exe 都做，缺一不可）

1. **映像路径**：从监听端口反查 PID，再用 PID 查 `ExecutablePath`，不要凭参数推断。
2. **文件时间**：必须晚于修复提交时间。文件时间早于提交，说明跑的是旧产物。
3. **提交号**：`go version -m <exe>` 看 `vcs.revision`，并确认 `vcs.modified=false`；
   `modified=true` 说明构建时工作树不干净，产物无法与提交对应。
4. **符号/字符串**：按字节计数复核（`open(...,'rb').read().count(b'...')`），
   不要用 `Select-String`——它在几十 MB 的二进制上会假报 0 命中。
   查不到时用第二个工具（`findstr`、`go tool objdump`）复核再下结论。
5. **实际请求**：静态资源走 `/static/assets/<chunk>`；`/assets/*` 是 SPA 回退，
   任何名字都返回 200 + `index.html`（约 2.7KB），拿它当「chunk 已加载」会假通过。

## 停启顺序

1. 停 tw（`Stop-Process -Id <tw>`）；**宿主与 scan 不保证随之退出**，逐个确认端口空出。
2. 备份要换的 exe，记录 SHA256，与换前值比对。
3. 换名（先把新文件放到目标路径，再起 tw）。
4. 起 tw 用原命令行（存档在 `_build-artifacts\tw-start-cmdline.txt`），参数一个不改。
5. 起完按上表逐个核 #1/#2/#3 的 PID、映像路径、文件时间、提交号、符号。

## 2026-11-21 的现场教训

- 会话跑在 `cyber-scan.exe`（PID 由 tw 拉起），hub 只做托管；只换 hub 后，
  评估器仍是 11-19（`39eac04a`）的实现，`addTokenUsage`、`usage_missing`、
  `eval_model_hash` 在 scan 里都是 0，journal 里也就看不到任何标记。
- 取消/失败的回合会写 `turn_ended` 但用量字段全 0；那是「没有可用用量」，
  不是「少报」，不能算成负差值（tianwu 侧同样不会用全 0 覆盖逐次之和）。
- 回环拨号失败会偶发假红（`agent/provider`、`agent/session` 各一例），
  单跑/复跑全绿即按假红记录，不为它放宽超时。
