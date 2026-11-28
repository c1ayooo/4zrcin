# 4zrcin部署清单（三个 exe）

一次部署要同时核对三个可执行文件。任何一个旧了，「修复是否在跑」都是假结论——
2026-11-21 的评估器用量问题就是只换了 hub、漏了真正执行会话的 scan，连续两次窗口白做。

| # | 文件 | 来源 | 从哪启动 | 装的是什么 |
|---|------|------|----------|------------|
| 1 | `4zrcin\.build\zrcin-zw.exe` | 本仓 `cmd/zrcin` | 人工/脚本直接启动 | 控制面：HTTP API、控制台静态资源、调度、账本 |
| 2 | `cyber-harness\cyber-harness.exe` | fork hub | zrcin 参数 `--harness <path>`，作为 zrcin 的子进程 | Hub：AOP 端点、会话托管、节点管理 |
| 3 | `4zrcin\bin\cyber-scan.exe` | fork scan 节点 | zrcin 参数 `--scan <path>`，作为 zrcin 的子进程 | **真正执行会话与工具调用的那一侧** |

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
5. **实际请求**：静态资源只走 `/static/assets/<chunk>`，且**必须带控制台会话**（登录后的 `tw_session` cookie 或 `Authorization: Bearer <JWT>`），
   核对 `content-type` 是 `javascript`。匿名请求任何非 `/static/` 路径都会被登录页接住
   （200 + 约 2.8KB HTML，落点 `cmd/zrcin/auth.go:449` 的兜底 `mux.HandleFunc("/")` → `loginPage`（`:577`）），
   带 token 后不存在即 404。所以「匿名 200」既不是 SPA 回退，也不能当 chunk 已加载。
   另：**static 没重建时新旧入口 chunk 同名**（2026-11-22 04:53 实测两边都是 `index-C2Ce3IyN.js`），
   此时「旧 chunk 必须 404」不适用，会自撞判红；只比新 chunk 200 + content-type 即可。

## 停启顺序

1. 停 zrcin（`Stop-Process -Id <zrcin>`）。`zrcin` 会带走**它自己拉起的** hub 与 scan：Windows 用设置了
   `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` 的 job 对象兜住，强杀（等同换 exe）也生效；Linux 走进程组
   （`internal/host/job_windows.go`，提交 `f4dfb23e`）。但这不代表端口一定立刻空出：不是本实例拉起的
   旧 hub 不会被碰到，2026-11-22 一天内就连续三次出现「停 zrcin 后旧 hub 仍占着 18799」。所以判据仍是
   **显式确认 18200 与 18799 两个端口都空出**，不是看 zrcin 的 PID 没了。
   新 zrcin 会拉起一对**新 PID**——所以换完 zrcin 后「PID 变了」正常，
   判据是映像身份四项（路径、大小、文件时间、提交号）不变，不是 PID 不变。
   另外**起完必须确认 hub 在 18799**：端口被占时 hub 不会回到 18799（现场见过落到 18790），
   24 个会话指针随之被判 stale 改名重建，等于一次冷启。端口不对就重做干净重启。
2. 备份要换的 exe，记录 SHA256，与换前值比对。
3. 换名（先把新文件放到目标路径，再起 zrcin）。
4. 起 zrcin 用原命令行（存档在 `_build-artifacts\zrcin-start-cmdline.txt`），参数一个不改。
5. 起完按上表逐个核 #1/#2/#3 的 PID、映像路径、文件时间、提交号、符号。

## 干净环境冷启动（Windows）

本节写「一台没碰过这个项目的机器要起来，最少需要什么」，全部条目取自代码与本机事实；**尚未在另一台机器实测**，实测清单由「集成」维护，本节只作核对基准。Linux 见末尾单独一节，标注未实测。

**依赖**（版本以仓库为准，不靠记忆）：

| 依赖 | 出处与用途 |
| --- | --- |
| Go 1.26.0 | `go.mod` 的 `go 1.26.0`；编译 `zrcin`、`zrcin-repair` 与 harness |
| Git | 取源码；Windows 上还提供 Bash 与 coreutils |
| Node 24+ / pnpm | Node 供文档链接检查与 gate；pnpm 只在 4zweb 仓里改前端时需要（产物已入库，常规启动不需要） |
| Git for Windows 的 Bash + `ls`/`grep`/`sh` | 宿主的 shell 工具是 POSIX 解释器，`internal/host/host.go:766-774` 会把 Git 的 `usr/bin` 补进子进程 PATH；缺了模型只能退到 `cmd`/`dir` |
| harness 源码（`tianwu/fixes`） | `scripts/build-harness.ps1 -HarnessDir <dir>` 一次编出 `cyber-harness.exe` + `cyber-scan.exe`（Hub 带 `full` tag，脚本 `:48`；[14-nodes.md](14-nodes.md)）；两者必须同一次构建 |
| 可达的模型端点 | OpenAI 兼容；不可达时宿主不就绪 |

**启动**（本机存档在 `_build-artifacts\zrcin-start-cmdline.txt`，参数一个不改）：

```powershell
.build\zrcin-zw.exe --addr 127.0.0.1:18200 --data <绝对数据目录> --project <项目ID> `
  --host-addr 127.0.0.1:18799 --harness <...\cyber-harness.exe> --scan <...\bin\cyber-scan.exe> `
  --model-url http://<网关> --model <模型名>
```

- 模型也可以不给参数，改用 `CYBER_BASE_URL` / `CYBER_MODEL` / `CYBER_API_KEY`，或启动后在“设置 → 模型连接”填（`cmd/zrcin/main.go:80-82`）。
- 控制台认证为账号密码 + JWT（`internal/account`）：首次打开控制台会跳到 `/setup` 初始化页（用户名 + 密码 + 确认密码），提交即创建管理员并直接登录；**仅允许这一次**，之后 `/setup` 永久关闭（重定向回 `/login`），注册接口对非空账号库一律 403 `REGISTRATION_CLOSED`。登录 `POST /api/auth/login` 返回 JWT，浏览器走 `tw_session` cookie，脚本走 `Authorization: Bearer`。实例签名密钥在 `<data>/jwt.key`（首次启动生成；旧 `console.token` 会被原样沿用），轮换密钥即全体会话下线。忘记密码无重置流程：停服删除 `<data>/account.db`（建议同时轮换 `jwt.key`）后经 `/setup` 重新初始化。**部署要点：初始化窗口期内任何先访问到端口的人都能抢注管理员，启动后应立即完成 `/setup`，或保持只监听回环地址。**

**端口只有两个是入站**：

| 端口 | 谁在听 | 默认值 |
| --- | --- | --- |
| 控制台 | `zrcin` | `--addr`，默认 `127.0.0.1:8080`；现场用 `18200` |
| Hub | `cyber-harness` | `--host-addr`，默认 `127.0.0.1:18766`；现场用 `18799` |

`cyber-scan` **不监听端口**：它作为 web 客户端连 Hub（`internal/host/host.go:652-665` 的 `agent --transport web --server-url …`），出站到 Hub 与模型端点。一个控制台地址只允许一个实例。

**数据目录**（必须可写本地盘）：空目录首次启动不建任何项目，`/api/projects` 返回空列表，从控制台新建第一个项目；已有目录必须是当前版本支持的 SQLite schema，不自动迁移旧 JSON 账本。落地物：`<data>/jwt.key`（会话签名密钥）、`<data>/account.db`（操作员账号）、`<data>/host/cyber-web.db`（Hub 会话库）与 `cyber-web.db.node.yaml`（scan 配置）、`<data>/projects/<id>/ledger.sqlite`。

**最小验证**（按顺序，任一失败就不算起来）：

1. 不带凭证 `GET /healthz` → 200（`cmd/zrcin/auth.go` 在鉴权之前放行）。
2. 登录拿 JWT（`POST /api/auth/login`），随后带 `Authorization: Bearer` `GET /p/<项目>/api/board` → 200，且 `state.kind` 有值。
3. 带 JWT `GET /static/assets/<入口 chunk>` → 200 且 `content-type` 是 javascript；同一路径**匿名**返回 200 是登录页，不是资源（见上文第 5 条）。
4. 18200 与 18799 各有一个监听进程，且分别是 `zrcin` 与 `cyber-harness`。
5. 三个进程都在：`zrcin`、`cyber-harness`、`cyber-scan`；停 `zrcin` 不保证带走后两个（见停启第 1 条）。

**Linux：未实测。** 已知差异只有这些，别当作已验证：产物无 `.exe` 后缀、入口为 `scripts/gate.sh` / `run.sh`、Git for Windows 的 coreutils 段不适用（系统自带）、进程组回收走另一套实现（`internal/host/orphans_unix.go`）、路径大小写与分隔符按平台测试走。4zrcin声称两平台同等支持（`docs/01-architecture.md` 第 9 节），但**本仓库没有在 Linux 上跑过真实实例**。

## 从构建产物起一个实例

构建产物是四个文件：`zrcin.exe`（= `bin/zrcin`）、`zrcin-repair.exe`、`cyber-harness.exe`、`cyber-scan.exe`，外加构建清单 `.build/build-manifest.json`。它们由本地 gate 编出（`cmd/zrcin-gate/main.go:20-25`：第 8 步 harness + scan，第 9 步 `bin/zrcin` 与 `bin/zrcin-repair`，第 10 步写清单），版本号靠 `-ldflags "-X main.buildVersion=…"` 注入；**当前没有独立发布脚本，也不打 tag、不做发布包**，不注入版本时 `zrcin --version` 就是 `zrcin dev`。

**校验三步，缺一步就不算正确产物**：

1. `zrcin --version` 输出 `zrcin <版本>`（`cmd/zrcin/main.go:107`）；清单 `control_plane` 里该二进制的 `version` 记录的是同一串 `--version` 输出（`internal/buildinfo/buildinfo.go:122`），两边对不上即坏产物。`zrcin dev` 表示没注入版本，不是正式产物。
2. 清单 `source.dirty` 必须是 `false`，`source.commit` 是这次要对齐的那一笔。
3. 清单 `control_plane` 里每个二进制的 `sha256` 与磁盘上文件对得上；`harness` / `scan` 必须来自**同一次** harness 构建，混版等于没换。

**放置与启动**：四个文件放同一目录即可省略 `--harness`/`--scan`（同目录 → `./bin` → PATH 查找）；建议仍显式写出。启动命令、端口、数据目录与最小验证按上一节「干净环境冷启动」执行，参数一个不改。

**回滚**：保留上一版四个产物与其 SHA256，按「停启顺序」整组替换；只换 `zrcin` 而 hub/scan 还是旧的，属于混版，判据会红。

**可复现判据**：同一个 commit 连跑两次 gate（或同一条 `go build`），四个产物的 sha256 必须一致。不一致说明产物里内嵌了时间、绝对路径或随机值，先找出内嵌点再交付。

## 从源码起一个实例（不做任何部署包）

不需要打包，也不需要预编译产物：在一台新机器上从仓库直接编、直接起。这样做的好处是产物身份由 `build-manifest.json` 自己写下来，坏在哪一步一眼可见；代价是必须先把三个运行时装齐。

**工具链（本机实测版本，写在这里是为了让版本差异可追）**：Go 1.27.0、Node v24.20.0、pnpm 12.14.0。Go 版本低于 `go.mod` 的 `go` 指令时直接报错，不会静默降级；pnpm 供 [4zweb](https://github.com/c1ayooo/4zweb) 仓的类型检查、测试与 static 构建。

**一次性准备**（把三个仓库放在同一个父目录下，harness 与 4zweb 才能被自动找到）：

```powershell
git clone https://github.com/c1ayooo/4zrcin.git
git clone https://github.com/c1ayooo/cyber-harness.git   # fork，hub 与 scan 的来源
git clone https://github.com/c1ayooo/4zweb.git           # 控制台前端源码；只在改前端时才需要装依赖
```

控制台前端源码在 [4zweb](https://github.com/c1ayooo/4zweb) 仓（同父目录检出，或 `ZRCIN_WEB_DIR`）；产物 `internal/web/bundle` **已提交**，跑控制面的人不需要 Node。只有改了前端才需要在 4zweb 里 `pnpm build` 并重跑 `go generate ./internal/web`——重建后的 bundle 必须与源码同一提交提交进去，否则页面与接口会不同步。开发态可用 `--web-dir <4zweb>/dist` 免重编。

**一条命令编全部**：

```powershell
pwsh -File scripts/gate.ps1
```

串行检查的步骤清单只有一份，见[贡献指南](../CONTRIBUTING.md#开发环境与检查)；任一步失败即停，**没跑过的检查不会被当成通过**。产物落在 `bin/`，清单落在 `.build/build-manifest.json`，`source.dirty` 必须为 `false`——主树里有未提交或未跟踪的源文件时它会是 `true`，那时编出来的二进制不能用于交付。

**起实例**：四个产物放同一目录，按「干净环境冷启动」那条命令起；模型端点两种写法任选其一——

```powershell
# 线上 API
.\bin\zrcin.exe --addr 127.0.0.1:18200 --data <数据目录> --host-addr 127.0.0.1:18799 `
    --harness .\bin\cyber-harness.exe --scan .\bin\cyber-scan.exe `
    --model-url <线上 API 基址> --model <模型名> --model-key <密钥>

# 或自建网关（本机就是这种）
.\bin\zrcin.exe ... --model-url http://127.0.0.1:3425 --model group/smart-split
```

密钥不要写进命令行历史：用 `--model-key` 时可先读环境变量，或起实例前 `$env:ZRCIN_MODEL_KEY`。


## 远程节点接入（--public-url、端口与反代）

要让操作员的主机跨公网自动回连，控制面用 `--public-url https://<控制台公网基址>` 打开接入面；
不设置时 `/join`、`/node`、`/tunnel` 与节点相关接口全部 404。机制与生命周期见
[14-nodes.md](14-nodes.md) §7。

- **只需要一个对外的 HTTPS 入口**。平台内的 rem 服务端只监听 `127.0.0.1` 的随机端口，不对外开
  监听；公网这一跳就是反代后的 `/tunnel/<一次性票据>`。`--public-url` 用 `https://`（私有网段或回环
  才允许 `http://`）。
- **产物**：`bin/zrcin-node` 与 `bin/cyber-scan` 放在 zrcin 同目录（gate 在有 `../rem` 检出时会自动
  构建启动器）；其它平台放到 `<data>/node-dist/<os>-<arch>/`。缺产物时部署栏如实显示不可分发，接入
  表单禁用对应配方，不会到节点上才失败。
- **反代要求**（nginx 例）：TLS 终止在反代上；`/tunnel/` 必须 `proxy_http_version 1.1` 并透传
  `Upgrade`/`Connection`（`map $http_upgrade $connection_upgrade`），关闭 `proxy_buffering`，
  `proxy_read_timeout`/`proxy_send_timeout` 至少一小时；`/join/`、`/node/` 普通转发即可。客户端走
  HTTP/1.1 升级，不受 `http2 on` 影响。

  ```nginx
  location /tunnel/ {
    proxy_pass http://127.0.0.1:18080;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection $connection_upgrade;
    proxy_read_timeout 3600s;
    proxy_send_timeout 3600s;
    proxy_buffering off;
  }
  ```

- **TLS 校验**：启动器按系统根证书校验公网证书。私有 CA 把根证书装进节点主机的系统信任库（或用
  `SSL_CERT_FILE` 指给启动器/服务）；不校验证书的连接不会被接受。中国大陆云主机对未备案域名的
  80/443 会被拦截，公开部署用备案域名，或换用已放行的端口并在 `--public-url` 写明。
- **节点主机**：`install` 需要 root（或可 sudo 的用户）；有 systemd 时注册服务，无 systemd（如未启用
  systemd 的 WSL2）时明示并前台运行。预检会拒绝已被占用且没有本节点归属标记的数据目录，避免覆盖
  别人的目录。
- **SSH 部署**：表单里的密码/私钥/口令只在部署任务的进程内存里，任务结束清零；主机密钥首次连接时
  记录在 `<data>/ssh-host-keys.json`，变化即中止。主机必须能出站访问 `--public-url`；不需要把这台
  主机暴露到公网。

## 2026-11-21 的现场教训

- **从会被重启回收的父进程或会话后台上拉起的 zrcin，会跟着父进程重启一起消失。** 2026-11-21 20:35 左右父进程重启，
  zrcin / hub / scan 三个进程同时不见、端口全空（不是 zrcin 崩了：三个一起没，
  是宿主把整棵进程树带走了）。`paused=true` 之类的落盘状态没丢，20:36:56
  重新拉起后 zrcin `16628` / hub `27012`(18799) / scan `10032` 即恢复。
  因此：**长期控制面必须用独立终端、计划任务或系统服务拉起**，
  不要挂在会被重启回收的父进程或会话后台任务下。
  判定「实例还在不在」要同时看端口与三个进程，只看 zrcin 会漏掉
  「zrcin 在跑但 hub/scan 被回收」的半死状态。
- 会话跑在 `cyber-scan.exe`（PID 由 zrcin 拉起），hub 只做托管；只换 hub 后，
  评估器仍是 11-19（`39eac04a`）的实现，`addTokenUsage`、`usage_missing`、
  `eval_model_hash` 在 scan 里都是 0，journal 里也就看不到任何标记。
- 取消/失败的回合会写 `turn_ended` 但用量字段全 0；那是「没有可用用量」，
  不是「少报」，不能算成负差值（4zrcin 侧同样不会用全 0 覆盖逐次之和）。
- 回环拨号失败会偶发假红（`agent/provider`、`agent/session` 各一例），
  单跑/复跑全绿即按假红记录，不为它放宽超时。

## server 形态部署清单与恢复手册（docs/16 §11、§13 M0 第 7 条）

server 形态（`--postgres`/`ZRCIN_POSTGRES_DSN`）与桌面形态共用同一个二进制；
PostgreSQL 是唯一真相，文件留在数据卷。本节命令凡写「已验证」的都是
2026-11-27 在本机（WSL2，docker + postgres:16-alpine）实跑通过的；写
「未验证」的只有构型没跑，步骤本身按 compose/docker 官方语义给出。

### 冒烟（每次改动后必跑，已验证）

```bash
bash deploy/smoke-server.sh "$(bash scripts/test-pg.sh)"
```

做两件事：(a) 用 `inprochost,remtunnel` tags（判定逻辑同 `cmd/zrcin-gate/main.go:160-177`）
现编一个 zrcin，server 形态起在 **18431/18931**（临时数据目录，`--postgres`
指向测试库），`/healthz` 200 后核对 PG 里 `instance.instance_state`、
`instance.users` 与 `pg_trgm` 扩展都在，SIGTERM 退出；(b) 同一二进制不带
DSN 再起一次（桌面形态回滚检查），`/healthz` 200 后停止。可重复执行，
只占 18431/18931 两个独立端口。判绿有三道防假绿关卡：启动前端口预检
（被占即报出占用 pid 并退出）、`/healthz` 200 时用 `ss` 核对监听进程就是
本次 pid、server 形态退出后确认端口已释放。教训：2026-11-27 早期版本用
子 shell（不带 exec）起 zrcin，清理只杀了子 shell，zrcin 变孤儿继续占端
口应答 `/healthz`，当时的「连跑两次全绿」实际是孤儿在应答——修复（exec
+ 三道关卡）后重新连跑两次全绿，并实测预检对占用端口的假进程返回退出码 1。

### compose 栈（deploy/compose.yaml）

compose 栈只有单主 PostgreSQL，**不含流复制备机**：docs/16 §11 里的
`zrcin-standby` 属 Q8 的可选项（「有第二台主机时加」），本期未实现，
按 §11 预期找备机切换步骤的读者以本段为准。

四个服务：`zrcin`（server 形态）、`postgres:16-alpine`（`pg_trgm` 经
`deploy/pg-init.sql` 启用；`archive_mode=on`，`archive_timeout=60s`）、
`proxy`（caddy，`deploy/Caddyfile`，`/tunnel/*` 关缓冲透传 WebSocket）、
卷 `zrcin-data` 与 `pg-data` 分开。环境变量注入 `ZRCIN_POSTGRES_DSN` 与
`ZRCIN_JWT_KEY`；**注意当前代码的签名密钥仍从 `<data>/jwt.key` 读**
（`cmd/zrcin/auth.go:1252`），`ZRCIN_JWT_KEY` 是 docs/16 §11 声明的参数、
代码尚未读取——密钥持久化靠 zrcin-data 卷，环境变量先注入作前向兼容。

```bash
# 语法校验（已验证）：
ZRCIN_PG_PASSWORD=… ZRCIN_PUBLIC_URL=https://ops.example.com \
  docker compose -f deploy/compose.yaml config
# 起栈（未验证：测试库里有别的数据，不能在这里 up）：
cd deploy && ZRCIN_PG_PASSWORD=… ZRCIN_PUBLIC_URL=https://… \
  docker compose up -d
```

镜像由 `deploy/Dockerfile` 从仓库根构建；默认不带 go build tags（不需要
harness/rem 检出），要 `inprochost`/`remtunnel` 把对应源码放进构建上下文
并设 `ZRCIN_BUILD_TAGS`（未验证：本次只校验了 compose 语法，没 build 镜像）。
`ZRCIN_PG_PASSWORD`、`ZRCIN_PUBLIC_URL`、`ZRCIN_JWT_KEY`、
`ZRCIN_HTTPS_PORT`（默认 8443）写进 `deploy/.env`，不要进命令行历史。

### 备份

- **WAL 归档**（`deploy/archive-wal.sh`，已验证：归段、重试幂等、同名不同
  内容拒绝）。`postgresql.conf`：
  `archive_mode=on`、`archive_command='/绝对路径/deploy/archive-wal.sh %p %f'`、
  `archive_timeout=60`。compose 栈里等价参数已写在 postgres 服务的启动项里。
  本地目录只是示例，生产把脚本里的 `cp` 换成对象存储上传（脚本头注有
  S3/rclone 两行示例）。
- **每天基线**：`pg_basebackup -U postgres -D <备份目录> -Fp -Xs -P`（已验证，
  演练里同一条命令）。`archive_timeout` 是 RPO 上限的一半都不到才算数——
  没有它，不活跃的库永远不出新 WAL 段。
- **数据卷快照**：`deploy/snapshot-volume.sh <目录> <输出>`（已验证：tar +
  sha256，`sha256sum -c` 通过）。**先 `docker compose stop zrcin` 再快照**；
  PostgreSQL 卷不要用它，库的备份只走基线 + WAL。

### 恢复手册（RPO/RTO 口径按 docs/16 §11）

RPO = WAL 归档间隔（compose 栈 60s；实测演练用了 5s）。RTO = 恢复演练
实测，不是纸面值。已验证的演练（隔离库、postgres:16-alpine、约 30MB）：

1. 源库 `archive_mode=on` + `archive_timeout`，建表写数据；
2. `pg_basebackup -U postgres -D /base -Fp -Xs -P` 出基线；
3. 基线之后再写两行，等归档出段（`ls <归档目录>` 确认有段）；
4. 停源库；基线拷到恢复目录，清空 `pg_wal/`，`touch recovery.signal`，
   目录属主改容器内 postgres 用户（演练里 `chown -R 70:70`）；
5. 起恢复容器加 `-c restore_command='cp /walarch/%f %p'`；
6. 验收：`select count(*) from drill` 返回基线之后的行（演练实测
   `2:after-baseline-1,after-baseline-2`，即 WAL 重放成功），**RTO 实测
   4.4s**（本机、空载、30MB——真实体量另测，口径是「恢复容器
   `pg_isready` 就绪」）；**并核对扩展**：`select 1 from pg_extension
   where extname='pg_trgm'` 必须有行——`pg_trgm` 只在空卷首次初始化时装
   （deploy/pg-init.sql），从旧卷的基线恢复出来的集群可能没有，缺了它
   server 形态的全文检索静默降级；缺则补 `CREATE EXTENSION pg_trgm`。
   冒烟脚本 deploy/smoke-server.sh 的 PG 检查含同款断言。
7. 恢复后的实例按上面冒烟清单起 server 形态，再抽查证据哈希与 run 日志
   （docs/17 B4/B5，本次演练只做到数据行核对，**抽查证据哈希未验证**）。

### server 形态起一个实例（不用 compose，已验证）

```bash
bin/zrcin --data <数据目录> \
  --postgres "postgres://user:pass@db-host:5432/zrcin?sslmode=disable" \
  --addr 0.0.0.0:8080 --host-addr 0.0.0.0:18799 \
  --public-url https://ops.example.com \
  --harness bin/cyber-harness --scan bin/cyber-scan
```

`--public-url` 在 server 形态必填（`cmd/zrcin/main.go:219`），回环地址只允许
`http://`；PG 不可达时接口按失败模式返回 503（docs/16 §12.3）。最小验证
沿用上文「干净环境冷启动」的五条，另加：PG 里 `instance` schema 有
`instance_state` 与 `users` 两张表。
