package notify

import (
	"context"
	"sync"
	"time"
)

// 投递状态机：pending → sent | failed（failed 可经 API 手动重试回 pending）。
const (
	StatePending = "pending"
	StateSent    = "sent"
	StateFailed  = "failed"
)

// maxDeliveries 是投递历史的环形上限。投递记录是运维观察用的易失数据，
// 存内存即可，重启清空——不为此引入实例级数据库。
const maxDeliveries = 500

// maxQueue 是事件队列上限：超出即丢弃最旧并计数，防止故障风暴把内存打爆。
const maxQueue = 1024

// realtimeBatchPerTick 限制单通道每轮 tick 的即时投递次数。
const realtimeBatchPerTick = 5

// attemptDelays 是瞬时失败的退避序列；耗尽（3 次）即标 failed。
var attemptDelays = []time.Duration{time.Second, 5 * time.Second, 30 * time.Second}

// Delivery 是一次「事件 × 通道」的投递记录。
type Delivery struct {
	ID          string    `json:"id"`
	ChannelID   string    `json:"channel_id"`
	ChannelName string    `json:"channel_name"`
	Mode        string    `json:"mode"`
	EventKind   string    `json:"event_kind"`
	EventLabel  string    `json:"event_label"`
	Title       string    `json:"title"`
	Project     string    `json:"project,omitempty"`
	Severity    string    `json:"severity,omitempty"`
	State       string    `json:"state"`
	Attempts    int       `json:"attempts"`
	LastError   string    `json:"last_error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	BatchID     string    `json:"batch_id,omitempty"`

	// snapshot 不参与 JSON 序列化，仅供引擎渲染 Item。
	snapshot Snapshot
}

// Engine 是投递引擎：消费 Emit 的事件，按通道勾选的内容 fan-out 成投递记录，
// 再按模式（realtime/digest）与限流节奏发送。单 goroutine 驱动。
type Engine struct {
	store *Store
	// names 把 projectID 翻译成展示名；links 把事件翻译成详情回链。
	// 两者由 hub 注入，本包不认识项目存储与前端路由。
	names func(projectID string) string
	links func(s Snapshot) string

	mu         sync.Mutex
	events     []Snapshot
	dropped    int
	deliveries []Delivery // 追加序；超过 maxDeliveries 从头部裁剪
	nextAt     map[string]time.Time
	lastSend   map[string]time.Time
}

// NewEngine 创建引擎。names/links 可为 nil。
func NewEngine(store *Store, names func(string) string, links func(Snapshot) string) *Engine {
	if names == nil {
		names = func(string) string { return "" }
	}
	if links == nil {
		links = func(Snapshot) string { return "" }
	}
	return &Engine{
		store:    store,
		names:    names,
		links:    links,
		nextAt:   map[string]time.Time{},
		lastSend: map[string]time.Time{},
	}
}

// Emit 记录一条待通知事件。**盲插**：不做任何过滤匹配——过滤发生在 fan-out，
// 这样事件写入端不依赖通道配置的存在与正确性。任何地方都可以放心调用。
func (e *Engine) Emit(kind, ref, projectID, title, summary, severity string) {
	if !ValidEvent(kind) {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.events) >= maxQueue {
		e.events = e.events[1:]
		e.dropped++
	}
	e.events = append(e.events, Snapshot{
		Kind: kind, RefID: ref, ProjectID: projectID,
		Title: title, Summary: summary, Severity: severity,
	})
}

// DroppedEvents 返回因队列满而丢弃的事件数（运维观察用）。
func (e *Engine) DroppedEvents() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.dropped
}

// Deliveries 返回投递历史的拷贝（旧→新）。
func (e *Engine) Deliveries() []Delivery {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Delivery, len(e.deliveries))
	copy(out, e.deliveries)
	return out
}

// Retry 把失败投递放回待发队列。
func (e *Engine) Retry(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.nextAt, id)
	for i := range e.deliveries {
		d := &e.deliveries[i]
		if d.ID == id && d.State == StateFailed {
			d.State = StatePending
			d.Attempts = 0
			d.LastError = ""
			d.UpdatedAt = time.Now()
			return true
		}
	}
	return false
}

// Run 驱动引擎，直到 ctx 取消。每 tickSec 一轮：fan-out → 投递。
func (e *Engine) Run(ctx context.Context, tickSec int) {
	if tickSec <= 0 {
		tickSec = 3
	}
	t := time.NewTicker(time.Duration(tickSec) * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.step(time.Now())
		}
	}
}

type channelUnavailableError struct{ kind string }

func (e *channelUnavailableError) Error() string { return "不支持的通道类型 " + e.kind }

// TestChannel 同步发一条测试消息，返回渠道的原始错误（API 层 502 透传）。
func (e *Engine) TestChannel(ctx context.Context, c ChannelConfig) error {
	ch, ok := Get(c.Kind)
	if !ok {
		return Permanent(&channelUnavailableError{kind: c.Kind})
	}
	base := e.store.View().BaseURL
	m := Message{Items: []Item{{
		Kind: "test", KindLabel: "测试",
		Name:    "4zrcin 测试通知",
		Project: e.names(""),
		Summary: "这是一条来自通知中心的测试消息，收到即表示通道配置有效。",
	}}}
	if base != "" {
		m.HomeURL = base
	}
	_, err := ch.Send(ctx, c.Config, m)
	return err
}

// step 是一轮：fan-out → 投递。
func (e *Engine) step(now time.Time) {
	e.fanOut(now)
	e.deliver(now)
}

// fanOut 取出积压事件，为每个启用通道生成投递记录。
// 事件写入端盲插，过滤在这里做：通道未勾选该事件类型 → 跳过；
// MinSeverity 门槛只对携带级别的事件生效。
func (e *Engine) fanOut(now time.Time) {
	e.mu.Lock()
	events := e.events
	e.events = nil
	e.mu.Unlock()
	if len(events) == 0 {
		return
	}
	cfg := e.store.View()
	if !cfg.Enabled {
		return
	}
	chans := e.store.EnabledChannels()
	if len(chans) == 0 {
		return
	}
	// names() 可能持有 hub 侧锁，必须在 e.mu 之外调用，避免锁序颠倒。
	var fresh []Delivery
	for _, s := range events {
		for _, c := range chans {
			if !Match(c.Filter(), s) {
				continue
			}
			fresh = append(fresh, Delivery{
				ID: NewID(), ChannelID: c.ID, ChannelName: c.Name, Mode: c.Mode,
				EventKind: s.Kind, EventLabel: EventLabel(s.Kind),
				Title: s.Title, Project: e.names(s.ProjectID), Severity: s.Severity,
				State: StatePending, CreatedAt: now, UpdatedAt: now,
				snapshot: s,
			})
		}
	}
	if len(fresh) == 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.deliveries = append(e.deliveries, fresh...)
	if len(e.deliveries) > maxDeliveries {
		e.deliveries = e.deliveries[len(e.deliveries)-maxDeliveries:]
	}
}

// deliver 按通道投递到期的记录。realtime 逐条发；digest 等最老记录攒够周期后
// 合成一条。每通道每轮先取令牌，限流时不投（记录留在队列里，下轮再试）。
func (e *Engine) deliver(now time.Time) {
	cfg := e.store.View()
	if !cfg.Enabled {
		return
	}
	for _, c := range e.store.EnabledChannels() {
		ch, ok := Get(c.Kind)
		if !ok {
			continue
		}
		due := e.pendingDue(c.ID, now)
		if len(due) == 0 || !e.takeToken(c.ID, c.RatePerMin, now) {
			continue
		}
		if c.Mode == ModeDigest {
			oldest := due[0].CreatedAt
			if now.Sub(oldest) < time.Duration(cfg.DigestMinutes)*time.Minute {
				continue
			}
			e.sendBatch(ch, c, due, cfg, now)
			continue
		}
		n := len(due)
		if n > realtimeBatchPerTick {
			n = realtimeBatchPerTick
		}
		for _, d := range due[:n] {
			e.sendOne(ch, c, d, cfg, now)
		}
	}
}

// pendingDue 返回某通道到期可发的 pending 投递（按创建序）。
func (e *Engine) pendingDue(channelID string, now time.Time) []Delivery {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Delivery
	for _, d := range e.deliveries {
		if d.ChannelID != channelID || d.State != StatePending {
			continue
		}
		if at, ok := e.nextAt[d.ID]; ok && now.Before(at) {
			continue
		}
		out = append(out, d)
	}
	return out
}

// takeToken 是简化令牌桶：rate>0 时两次发送至少间隔 1 分钟/rate 次。
// 返回 false 表示本轮没额度，投递记录原样留队。
func (e *Engine) takeToken(channelID string, rate int, now time.Time) bool {
	if rate <= 0 {
		return true
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	interval := time.Minute / time.Duration(rate)
	if last, ok := e.lastSend[channelID]; ok && now.Sub(last) < interval {
		return false
	}
	e.lastSend[channelID] = now
	return true
}

// sendOne 投递单条（realtime）。
func (e *Engine) sendOne(ch Channel, c ChannelConfig, d Delivery, cfg File, now time.Time) {
	m := Message{Items: []Item{e.itemFor(d)}, HomeURL: cfg.BaseURL}
	_, err := ch.Send(context.Background(), c.Config, m)
	e.finish(d.ID, err, 1, now)
}

// sendBatch 投递一批（digest）。kept 精确落账：前 kept 条 sent，其余留在
// 队列里参与下一批——被长度上限截掉的内容不能跟着标记成功。
func (e *Engine) sendBatch(ch Channel, c ChannelConfig, due []Delivery, cfg File, now time.Time) {
	if len(due) > maxDigestBatch {
		due = due[:maxDigestBatch]
	}
	items := make([]Item, 0, len(due))
	for _, d := range due {
		items = append(items, e.itemFor(d))
	}
	m := Message{Items: items, Batch: true, WindowMinutes: cfg.DigestMinutes, HomeURL: cfg.BaseURL}
	kept, err := ch.Send(context.Background(), c.Config, m)
	if err != nil {
		for _, d := range due {
			e.finish(d.ID, err, 0, now)
		}
		return
	}
	for i, d := range due {
		if i < kept {
			e.finish(d.ID, nil, 1, now)
		} else {
			// 装不下的条目不耗重试预算，直接回到待发状态。
			e.mu.Lock()
			delete(e.nextAt, d.ID)
			e.mu.Unlock()
		}
	}
}

// maxDigestBatch 是单次汇总消息的最大条数，防止极端积压撑爆一次请求。
const maxDigestBatch = 500

// finish 落投递结果。err 非 nil 时：永久失败直接 failed；瞬时失败按退避
// 序列重排，耗尽 3 次也标 failed。
func (e *Engine) finish(id string, err error, kept int, now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range e.deliveries {
		d := &e.deliveries[i]
		if d.ID != id {
			continue
		}
		d.UpdatedAt = now
		if err != nil {
			d.Attempts++
			d.LastError = err.Error()
			if IsPermanent(err) || d.Attempts >= len(attemptDelays) {
				d.State = StateFailed
			} else {
				d.State = StatePending
				e.nextAt[id] = now.Add(attemptDelays[d.Attempts-1])
			}
			return
		}
		if kept <= 0 {
			return
		}
		d.State = StateSent
		d.LastError = ""
		delete(e.nextAt, id)
		return
	}
}

// itemFor 把投递记录还原成渠道渲染用的条目。回链由 hub 注入的 links 生成。
func (e *Engine) itemFor(d Delivery) Item {
	return Item{
		Kind: d.EventKind, KindLabel: d.EventLabel,
		RefID: d.snapshot.RefID, Project: d.Project,
		Name: d.Title, Severity: d.Severity,
		Summary: d.snapshot.Summary, DetailURL: e.links(d.snapshot),
	}
}

// Stats 返回引擎概况，供 /api/notify/meta 展示。
func (e *Engine) Stats() map[string]int {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := map[string]int{"pending": 0, "sent": 0, "failed": 0, "dropped": e.dropped}
	for _, d := range e.deliveries {
		out[d.State]++
	}
	return out
}
