package notify

// Snapshot 是一条待通知事件的载荷契约。写方是 hub 侧的事件接线点，读方是投递
// 引擎与过滤匹配。定义放在本包是因为它是「通知领域」的载荷：宿主只负责序列化，
// 不理解字段含义。
//
// 为什么冗余存字段而不在渲染时回查：对象事后会被改名、改状态，而推送内容应当
// 反映**事发当时**的结论。另外引擎因此不必回查任何业务表。
type Snapshot struct {
	// 事件类型：EventVulnRecorded 等常量之一。
	Kind string `json:"kind"`
	// RefID 是事件关联对象的稳定标识（漏洞 id、intent id、节点 id…），
	// 供事件去重与回链使用。
	RefID string `json:"ref,omitempty"`
	// ProjectID 是事件所属项目的 id；实例级事件（如节点异常）为空。
	ProjectID string `json:"project_id,omitempty"`
	// Title 是事件的一句话标题，如漏洞名称、失败意图的标题。
	Title string `json:"title"`
	// Severity 是级别（low/medium/high/critical），仅部分事件携带。
	// 空表示该事件无级别语义，min_severity 门槛对其不生效。
	Severity string `json:"severity,omitempty"`
	// Summary 是可读的补充说明，可为空。
	Summary string `json:"summary,omitempty"`
}

// Item 是一条待推送的通知条目，供渠道渲染。
type Item struct {
	Kind      string
	KindLabel string
	RefID     string
	// Project 是项目展示名。由引擎填充——本包不认识项目存储，拿不到名字。
	Project string
	Name    string
	Severity string
	Summary  string
	// DetailURL 是详情回链；为空表示未配 base_url，渲染时省略。
	DetailURL string
}

// Title 返回条目的展示标题：优先事件标题，为空时用占位符——绝不输出空标题。
func (i Item) Title() string {
	if i.Name != "" {
		return i.Name
	}
	return "(未命名事件)"
}

// Tag 返回条目的分类标签：有级别语义时用级别名，否则用事件类型名。
// 渲染成「[🔴 严重] 标题」或「[节点异常] 标题」里的方括号部分。
func (i Item) Tag() string {
	if i.Severity != "" {
		return SeverityLabel(i.Severity)
	}
	if i.KindLabel != "" {
		return i.KindLabel
	}
	return EventLabel(i.Kind)
}

// Message 是一次渠道发送的完整内容。
type Message struct {
	// 单条推送时长度为 1；汇总推送（digest）时为一整批。
	// 空切片是非法的，调用方须保证至少一条。
	Items []Item
	// Batch=true 时按汇总消息渲染（换标题、带上时间窗与条数）。
	Batch bool
	// WindowMinutes 是汇总周期（分钟），仅 Batch=true 时用于文案「近 N 分钟」。
	// 刻意由配置显式传入而不是渲染时算 time.Since：渲染保持确定性，才好测。
	WindowMinutes int
	// HomeURL 是平台面板地址（全局 base_url）；空则不带面板入口。
	HomeURL string
}
