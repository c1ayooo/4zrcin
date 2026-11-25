# 05 · IM 通知中心 实施文档

> 目标：将 4zreco 的 IM 通知推送实现（`internal/notify`）迁移到本项目，在全局控制台新增
> 「通知中心」入口与 IM 配置页，支持多通知通道（钉钉/飞书/企业微信/通用 Webhook/
> Telegram/邮件），每个通道独立配置连接参数并独立勾选接收的通知内容类型。

## 一、可复用性评估（4zreco → 4zrcin）

| 4zreco 代码 | 结论 | 处理方式 |
|---|---|---|
| `internal/notify` 六个渠道 sender（dingtalk/feishu/wecom/webhook/telegram/email）、签名、SSRF 防护（`http.go`）、凭据掩码（`mask.go`）、双口径截断（`render.go`） | 整体迁移 | 原样拷贝，仅事件语义相关的渲染文字调整 |
| `Channel` 接口 + 显式注册表（`channel.go`） | 直接复用 | 原样拷贝 |
| 事件/快照模型（`notify.go`、`event.go`） | 重写 | 漏洞领域语义 → 通用事件（kind/title/severity/summary/project） |
| 过滤器（`filter.go`：task/asset/vulnclass） | 重写 | 简化为「事件类型勾选 + min_severity 门槛」，保留"Match 永不报错"不变量 |
| Markdown/HTML 渲染（`markdown.go`、`html.go`） | 改写 | 去掉 vulnclass/资产/状态变更，改为通用事件条目；保留转义与按整条打包 |
| 投递引擎（`internal/server/notifier.go`，PG 租约 + `SKIP LOCKED`） | 骨架复用、持久层替换 | SQLite 无行锁租约且本服务单进程 → 内存队列 + 内存投递历史（环形，500 条） |
| 三表 schema + settings 键值 | 不迁移 | 通道配置存 `data/notify.json`（仿 `internal/settings` 的锁式 JSON Store） |
| `internal/imhub`（双向聊天机器人） | 不迁移 | 深度耦合 4zreco 会话/TurnRunner，与通知推送无关 |

关键设计不变量（沿用 4zreco）：

1. **事件写入端盲插**：`Emit` 不做任何过滤匹配，过滤在 fan-out 时做；`Match` 永不返回 error，畸形配置一律视为命中（宁可多推不可静默漏推）。
2. **Channel 无状态**：凭据一律从 `cfg map[string]any` 传入。
3. **掩码契约**：API 回显 `SecretKeys` 对应键为 `****`；更新时掩码值原样回传 = 保留库中原值；改 `DestinationKeys` 必须重新提交凭据。
4. **按整条打包**：digest 批次超长时按条截断并明示"其余 N 条将在下一条消息继续"。

## 二、后端设计

### 2.1 包结构（全部为新增文件）

```
internal/notify/
  notify.go      渠道类型常量、事件类型常量、severity 工具（改写）
  event.go       Snapshot（事件载荷契约）/ Item / Message（改写为通用事件）
  channel.go     Channel 接口 + 注册表 + cfg helper（原样迁移）
  filter.go      事件勾选匹配 + min_severity 门槛（重写）
  dingtalk.go / feishu.go / wecom.go / telegram.go / email.go / webhook.go
                 渠道适配器（原样迁移）
  http.go / render.go / markdown.go / html.go / mask.go
                 共享基建（markdown/html 渲染改写）
  store.go       通道配置 + 全局开关的 JSON 文件 Store（新增）
  engine.go      内存事件队列 + fan-out + 投递引擎 goroutine（新增）
cmd/zrcin/
  notify_api.go  实例级 HTTP handler（新增）
  hub.go         路由 case、Store/引擎注入、事件 Emit 接线（修改）
```

### 2.2 配置数据模型（`data/notify.json`）

```go
type Channel struct {
    ID          string         `json:"id"`
    Name        string         `json:"name"`
    Kind        string         `json:"kind"`    // dingtalk|feishu|wecom|webhook|telegram|email
    Enabled     bool           `json:"enabled"`
    Config      map[string]any `json:"config"`  // 连接参数；SecretKeys 明文存盘、API 掩码回显
    Events      []string       `json:"events"`  // 本通道接收的通知内容类型（独立勾选）
    MinSeverity string         `json:"min_severity,omitempty"` // 仅对带级别的 vuln_recorded 生效
    Mode        string         `json:"mode"`    // realtime|digest
    RatePerMin  int            `json:"rate_per_min"` // 0=不限流
    CreatedAt   time.Time      `json:"created_at"`
    UpdatedAt   time.Time      `json:"updated_at"`
}

type File struct {
    Enabled  bool      `json:"enabled"`            // 全局总开关
    BaseURL  string    `json:"base_url,omitempty"` // 回链地址
    DigestMinutes int   `json:"digest_minutes"`     // digest 周期，默认 30
    Channels []Channel `json:"channels"`
}
```

各 Kind 的 `Config` 形状（沿用 4zreco）：dingtalk/feishu `{webhook, secret}`、
wecom `{webhook}`、webhook `{url, method, content_type, headers{}, body_template}`、
telegram `{bot_token, chat_id, base_url}`、email `{host, port, username, password, from, to[], tls}`。

### 2.3 通知内容类型（EventKind）

| Kind | 中文 | 事件源 |
|---|---|---|
| `vuln_recorded` | 漏洞入库 | 漏洞库新增记录（hub vulnlib 写入点），可叠加 min_severity |
| `intent_failed` | 意图失败 | intent 进入 failed/review_failed |
| `run_failed` | 运行失败 | Run 失败 / watchdog 终止 |
| `model_stall` | 模型停摆 | decider 心跳 stall / CancelCauseModelStall |
| `guardrail_pending` | 审批待处理 | 出现待审批的 guardrail 请求 |
| `node_unhealthy` | 节点异常 | nodehealth 探测状态翻转 |
| `attention_required` | 需要关注 | CommanderAttention 新增注意力事项 |

首期接线：`vuln_recorded`（hub `recordVuln`，经 `vulnlib.Has` 判定首次申报才推送）、
`node_unhealthy`（nodeProber 健康翻转回调 `OnFlip`）。其余枚举已下发（前端可勾选），
Emit 接线点在对应模块落点清晰处逐步补齐（意图/Run 失败、模型停摆、guardrail
待审批、指挥官注意力），避免侵入 store/decider 内部事务。

### 2.4 引擎（`engine.go`）

- `Emit(kind, ref, title, summary, severity, projectID)`：写入内存队列（容量 1024，满则丢弃最旧并计数）；事件附 UUID 与时间戳。
- 独立 goroutine，3s tick：fan-out（事件 × 启用通道，`Events` 勾选 + MinSeverity 匹配）→ 生成投递记录 → 按模式投递。
- 令牌桶限流（`rate_per_min`）；realtime 单发；digest 攒批到期合成一条。
- 失败处置：`PermanentError` 直接 failed；瞬时失败退避 1s/5s/30s，3 次耗尽 failed。
- 投递历史：内存环形缓冲 500 条（state/attempts/last_error/时间），重启清空——通知是易失副产物，不引入实例级 SQLite。
- 渲染：`Snapshot → Item`（Project 展示名由引擎查 hub 注入的 project 索引），`BaseURL` 拼 DetailURL。

### 2.5 API（实例级，挂在 hub 鉴权之后）

```
GET    /api/notify/meta                      事件类型枚举 + 渠道 kind 元数据 + 全局开关 + 统计
GET    /api/notify/channels                  列表（config 掩码 + secret_keys）
POST   /api/notify/channels                  新建（服务端 Validate）
PUT    /api/notify/channels/{id}             更新（掩码回传=保留原值）
DELETE /api/notify/channels/{id}
POST   /api/notify/channels/{id}/test        同步发测试消息
GET    /api/notify/deliveries                投递历史（分页）
POST   /api/notify/deliveries/{id}/retry     失败重发
PUT    /api/notify/settings                  全局开关/回链/digest 周期
```

## 三、前端设计

| # | 文件 | 改动 |
|---|---|---|
| 1 | `web/src/views/GlobalShell.tsx` | `GLOBAL_NAV` 加 `{ id:'notifications', label:'通知中心', icon:Bell }`；`PAGE_LOADERS` 加 loader；lazy + 渲染分支 |
| 2 | `web/src/views/Notifications.tsx`（新） | `Page` + 全局开关卡 + 通道列表 + 通道编辑 Dialog（连接参数按 `secret_keys` 动态渲染）+ 事件勾选 Checkbox 组 + 测试发送 + 投递历史 |
| 3 | `web/src/lib/notify-fields.ts`（新） | 字段表驱动：KIND_LABEL / CHANNEL_FIELDS / EVENT_KINDS，新增通道只改这一个文件 |
| 4 | `web/src/lib/types.ts` | NotifyChannel / NotifyMeta / NotifyDelivery 类型 |
| 5 | `web/src/lib/api.ts` | notifyMeta / notifyChannels / notifyCreateChannel / notifyUpdateChannel / notifyDeleteChannel / notifyTestChannel / notifyDeliveries / notifyRetryDelivery / notifySaveSettings（gbase 前缀） |

交互：进入页面一次拉 meta+channels+deliveries；添加通道 → 选 kind → 表单按字段表渲染
（secret 字段密码框，占位"已配置则留空保持不变"）→ 勾选接收的事件类型（选中
vuln_recorded 级联显示 min_severity）→ 保存 → 测试发送；历史区展示状态 Badge 与
手动重试。

## 四、实施步骤

1. 迁移 `internal/notify` 包，重写 notify.go / event.go / filter.go / markdown.go / html.go，`go test ./internal/notify/`。
2. 新增 store.go（JSON Store）+ engine.go（引擎），单测。
3. `cmd/zrcin/notify_api.go` + hub.go 路由与注入，事件接线（vulnlib 写入 / nodehealth / guardrail）。
4. 前端：types / api / notify-fields / Notifications 页 / GlobalShell 入口。
5. `pnpm build` + `go build` + `go test ./...` 验证。
