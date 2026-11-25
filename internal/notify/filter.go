package notify

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Filter 是通道对通知内容的选择条件：勾选的事件类型 + 漏洞级别门槛。
// 对应 Channel.Events / Channel.MinSeverity 两个字段的匹配语义。
//
// 所有字段都可选：Events 为空表示不限（全部事件都收），MinSeverity 为空表示
// 不设门槛——这正是畸形配置的兜底语义。
type Filter struct {
	// Events 是勾选的通知内容类型；空数组表示不限。
	Events []string `json:"events"`
	// MinSeverity 是最低级别门槛（low/medium/high/critical），空=不设门槛。
	// 只对携带级别的事件（vuln_recorded）生效；无级别事件一律放行。
	MinSeverity string `json:"min_severity"`
}

// ParseFilter 解析通道过滤配置。
//
// **永不返回 error。** 这是刻意的设计选择：过滤条件配置畸形时一律退化为零值
// Filter（= 不限 = 全部命中），因为对一个通知系统来说，**多推一条远好过
// 静默漏掉一条高危**。让解析失败变成「不推送」，等于给用户一个看起来配好了、
// 实际什么都不推的通道——这是最糟的失败模式。
func ParseFilter(raw []byte) Filter {
	var f Filter
	if len(raw) == 0 {
		return f
	}
	// 解析失败时 f 保持零值，即不过滤。
	_ = json.Unmarshal(raw, &f)
	return f
}

// ValidMinSeverity 报告 s 是否为合法的级别门槛（空串表示不设门槛）。
func ValidMinSeverity(s string) bool {
	if s == "" {
		return true
	}
	_, ok := severityRank[s]
	return ok
}

// Validate 校验过滤配置里**取值受限**的字段，供保存通道时调用。
//
// 为什么必须在写入时拦：Match 对未知门槛的判定是 `rank >= 0`，恒为真——
// 也就是说 min_severity 打错一个字（"hgih"），过滤器会**静默失效**变成
// 「全推」。这类「静默降级」正应该在入口处拦掉。
//
// 注意 Validate 只用于**写入**路径。读取路径仍走 ParseFilter 的宽容语义。
func (f Filter) Validate() error {
	if !ValidMinSeverity(f.MinSeverity) {
		return fmt.Errorf("最低级别 %q 无效，可选：low / medium / high / critical，或留空表示不限", f.MinSeverity)
	}
	return nil
}

// Match 判定一个事件是否应投递到带有该过滤条件的通道。
//
// **永不返回 error**，理由同 ParseFilter：任何内部异常都按「命中」处理。
// 判定顺序：事件类型勾选 → 级别门槛。
func Match(f Filter, s Snapshot) bool {
	// 勾选列表非空时要求事件命中其一；为空表示不限。
	if len(f.Events) > 0 && !slices.Contains(f.Events, s.Kind) {
		return false
	}
	// 级别门槛只对携带级别的事件生效：无级别事件（intent_failed 等）
	// 的 SeverityRank 为 0，若按 AtLeast 判会被任何非空门槛挡掉——
	// 那不是配置者的本意，门槛只该筛漏洞。
	if s.Severity != "" && !AtLeast(s.Severity, f.MinSeverity) {
		return false
	}
	return true
}

// containsAnyFold 报告 s 是否包含 keywords 中任一关键词（大小写不敏感）。
// 保留供未来的关键词过滤扩展使用。
func containsAnyFold(s string, keywords []string) bool {
	lower := strings.ToLower(s)
	for _, kw := range keywords {
		kw = strings.ToLower(strings.TrimSpace(kw))
		if kw != "" && strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}
