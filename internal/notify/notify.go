// Package notify 实现 IM / 邮件通知的渠道适配层。
//
// 分层：本包是**叶子包**，只依赖标准库。它不认识数据库、不认识 server。渠道配置
// 以 map[string]any 传入（对应 notify.json 里 Channel.Config 的 JSON 对象），
// 待推送内容以 Message 传入。这样拆开的好处是：签名计算、UTF-8 截断、过滤匹配这些
// 真正容易出错的地方可以脱离宿主单测，宿主只需在 hub 侧做编排。
//
// 并发约定：Channel 的实现必须**无状态**。同一个 Channel 实例会被多个渠道配置
// （甚至同一渠道的多个机器人实例）并发复用，所有凭据一律从 cfg 参数传入，
// 不允许把 webhook URL 之类的东西缓存进实现自身的字段。
package notify

// 渠道类型标识。取值同时是 Channel.Kind 的合法集合，由 hub 侧白名单校验
// （不用文件格式约束，方便后续加渠道）。
const (
	KindDingTalk = "dingtalk" // 钉钉自定义机器人
	KindFeishu   = "feishu"   // 飞书(含 Lark)自定义机器人
	KindWeCom    = "wecom"    // 企业微信群机器人
	KindWebhook  = "webhook"  // 通用 Webhook：自定义方法/头/JSON 模板
	KindTelegram = "telegram" // Telegram Bot API
	KindEmail    = "email"    // SMTP 邮件
)

// 事件类型，对应 Snapshot.Kind，也是每个通道独立勾选的通知内容类型。
const (
	EventVulnRecorded      = "vuln_recorded"      // 漏洞入库（可叠加 min_severity 门槛）
	EventIntentFailed      = "intent_failed"      // 意图失败 / 复核失败
	EventRunFailed         = "run_failed"         // Run 失败 / watchdog 终止
	EventModelStall        = "model_stall"        // 模型停摆 / 决策轮超时
	EventGuardrailPending  = "guardrail_pending"  // 高危操作等待操作员审批
	EventNodeUnhealthy     = "node_unhealthy"     // 节点健康探测转为不健康
	EventAttentionRequired = "attention_required" // 指挥官注意力事项需要操作员介入
)

// eventLabels 是事件类型的中文名，经 /api/notify/meta 下发，前端勾选列表据此渲染。
var eventLabels = map[string]string{
	EventVulnRecorded:      "漏洞入库",
	EventIntentFailed:      "意图失败",
	EventRunFailed:         "运行失败",
	EventModelStall:        "模型停摆",
	EventGuardrailPending:  "审批待处理",
	EventNodeUnhealthy:     "节点异常",
	EventAttentionRequired: "需要关注",
}

// EventLabel 返回事件类型的中文名；未知类型原样回显，不臆造。
func EventLabel(kind string) string {
	if s, ok := eventLabels[kind]; ok {
		return s
	}
	return kind
}

// ValidEvent 报告 kind 是否为受支持的事件类型。
func ValidEvent(kind string) bool {
	_, ok := eventLabels[kind]
	return ok
}

// ValidEvents 报告勾选列表是否全部合法（空列表合法：表示不勾选即不接收）。
func ValidEvents(kinds []string) bool {
	for _, k := range kinds {
		if !ValidEvent(k) {
			return false
		}
	}
	return true
}

// InitKind 是 config 里为空的 kind 的兜底值。
const InitKind = KindDingTalk

// severityRank 把级别映射成可比较的序数。未知级别返回 0，因此任何
// min_severity 设置都会把未知级别挡在外面——存疑时不推，避免误报刷屏。
var severityRank = map[string]int{
	"low":      1,
	"medium":   2,
	"high":     3,
	"critical": 4,
}

// SeverityRank 返回级别的序数；未知级别返回 0。
func SeverityRank(severity string) int { return severityRank[severity] }

// SeverityLabel 返回带 emoji 的中文级别名，用于消息标题与卡片配色。
// 未知级别原样回显，不臆造。
func SeverityLabel(severity string) string {
	switch severity {
	case "critical":
		return "🔴 严重"
	case "high":
		return "🟠 高危"
	case "medium":
		return "🟡 中危"
	case "low":
		return "🔵 低危"
	default:
		return severity
	}
}

// AtLeast 判断 severity 是否达到 min 门槛。min 为空表示不设门槛，一律通过。
// 注意未知 severity 的序数为 0，会被任何非空 min 拒掉（见 severityRank 注释）。
func AtLeast(severity, min string) bool {
	if min == "" {
		return true
	}
	return SeverityRank(severity) >= SeverityRank(min)
}

// EventKinds 返回全部受支持的事件类型，按常量声明序（供 UI 勾选列表稳定展示）。
func EventKinds() []string {
	return []string{
		EventVulnRecorded,
		EventIntentFailed,
		EventRunFailed,
		EventModelStall,
		EventGuardrailPending,
		EventNodeUnhealthy,
		EventAttentionRequired,
	}
}

// ValidMode 报告摘要模式是否合法；空串按 realtime 处理，由调用方补齐。
func ValidMode(mode string) bool {
	switch mode {
	case "", ModeRealtime, ModeDigest:
		return true
	}
	return false
}
