package main

// IM 通知中心的实例级 API（docs/05-im-notify.md）。
//
// 全部挂在 hub 鉴权之后（withConsoleAuth），经 ServeHTTP 的 /api/notify 前缀
// 分发到本文件。掩码契约沿用 4zreco：回显 config 时 SecretKeys 对应键替换为
// 掩码值；更新时掩码值原样回传=保留库中原值，改 DestinationKeys 必须重新
// 提交凭据（notify.PrepareConfigUpdate 负责）。

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/c1ayooo/4zrcin/internal/notify"
)

// channelKindLabels 是通道类型的中文名，经 /api/notify/meta 下发，
// 前端下拉据此渲染，不硬编码渠道知识。
var channelKindLabels = map[string]string{
	notify.KindDingTalk: "钉钉机器人",
	notify.KindFeishu:   "飞书机器人",
	notify.KindWeCom:    "企业微信机器人",
	notify.KindWebhook:  "通用 Webhook",
	notify.KindTelegram: "Telegram",
	notify.KindEmail:    "邮件 (SMTP)",
}

// notifyLink 把事件翻译成控制台详情回链。base 为空（未配公网地址）时返回
// 空串，渲染层据此省略链接。
func notifyLink(base string) func(notify.Snapshot) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return func(notify.Snapshot) string { return "" }
	}
	return func(s notify.Snapshot) string {
		if s.Kind == notify.EventVulnRecorded && s.RefID != "" {
			return base + "/g/?vuln=" + url.QueryEscape(s.RefID)
		}
		return base + "/g/?view=notifications"
	}
}

// serveNotify 分发 /api/notify/* 的全部路径。
func (h *hub) serveNotify(w http.ResponseWriter, r *http.Request) {
	if h.notifyStore == nil || h.notifier == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "通知功能不可用（data/notify.json 打开失败，见启动日志）"})
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/notify")
	switch {
	case rest == "/meta":
		h.notifyMeta(w, r)
	case rest == "/channels":
		h.notifyChannels(w, r)
	case strings.HasPrefix(rest, "/channels/"):
		h.notifyChannelItem(w, r, strings.TrimPrefix(rest, "/channels/"))
	case rest == "/deliveries":
		h.notifyDeliveries(w, r)
	case strings.HasPrefix(rest, "/deliveries/"):
		h.notifyDeliveryRetry(w, r, strings.TrimPrefix(rest, "/deliveries/"))
	case rest == "/settings":
		h.notifySettings(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "未知的通知接口"})
	}
}

// notifyChannelDTO 是通道的 API 形状：config 掩码回显，凭据键名随之下发，
// 前端据此渲染密码框，不持有渠道知识。
type notifyChannelDTO struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	Kind            string         `json:"kind"`
	KindLabel       string         `json:"kind_label"`
	Enabled         bool           `json:"enabled"`
	Config          map[string]any `json:"config"`
	SecretKeys      []string       `json:"secret_keys"`
	DestinationKeys []string       `json:"destination_keys"`
	Events          []string       `json:"events"`
	MinSeverity     string         `json:"min_severity,omitempty"`
	Mode            string         `json:"mode"`
	RatePerMin      int            `json:"rate_per_min"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

func toChannelDTO(c notify.ChannelConfig) notifyChannelDTO {
	ch, ok := notify.Get(c.Kind)
	dto := notifyChannelDTO{
		ID: c.ID, Name: c.Name, Kind: c.Kind, Enabled: c.Enabled,
		Config: notify.MaskConfig(c.Kind, c.Config),
		Events: c.Events, MinSeverity: c.MinSeverity,
		Mode: c.Mode, RatePerMin: c.RatePerMin,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
	if ok {
		dto.KindLabel = channelKindLabels[c.Kind]
		dto.SecretKeys = ch.SecretKeys()
		dto.DestinationKeys = ch.DestinationKeys()
	}
	return dto
}

// notifyMeta 一次拉全：全局开关 + 事件类型枚举 + 通道类型枚举 + 统计。
func (h *hub) notifyMeta(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "仅支持 GET"})
		return
	}
	cfg := h.notifyStore.View()
	kinds := make([]map[string]string, 0)
	for _, k := range notify.EventKinds() {
		kinds = append(kinds, map[string]string{"id": k, "label": notify.EventLabel(k)})
	}
	ckinds := make([]map[string]string, 0)
	for _, k := range notify.Kinds() {
		ckinds = append(ckinds, map[string]string{"id": k, "label": channelKindLabels[k]})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":        cfg.Enabled,
		"base_url":       cfg.BaseURL,
		"digest_minutes": cfg.DigestMinutes,
		"event_kinds":    kinds,
		"channel_kinds":  ckinds,
		"stats":          h.notifier.Stats(),
	})
}

// notifyChannels 列出全部通道（GET，掩码回显）或新建一个通道（POST）。
func (h *hub) notifyChannels(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := h.notifyStore.View()
		out := make([]notifyChannelDTO, 0, len(cfg.Channels))
		for _, c := range cfg.Channels {
			out = append(out, toChannelDTO(c))
		}
		writeJSON(w, http.StatusOK, map[string]any{"channels": out})
	case http.MethodPost:
		var in notify.ChannelConfig
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体不是 JSON"})
			return
		}
		if err := h.validateChannel(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		now := time.Now()
		in.ID = notify.NewID()
		in.CreatedAt, in.UpdatedAt = now, now
		f := h.notifyStore.View()
		f.Channels = append(f.Channels, in)
		if err := h.notifyStore.Save(f); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存失败：" + err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, toChannelDTO(in))
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "仅支持 GET/POST"})
	}
}

// validateChannel 是写路径的统一校验：kind 白名单、事件类型白名单、级别门槛、
// 摘要模式与渠道自身对连接参数的 Validate。缺省值在这里补齐。
func (h *hub) validateChannel(c *notify.ChannelConfig) error {
	if strings.TrimSpace(c.Name) == "" {
		return errString("通道名称不能为空")
	}
	c.Name = strings.TrimSpace(c.Name)
	if !notify.ValidKind(c.Kind) {
		return errString("不支持的通道类型 " + c.Kind)
	}
	if !notify.ValidEvents(c.Events) {
		return errString("勾选的通知内容类型无效")
	}
	if !notify.ValidMode(c.Mode) {
		return errString("mode 必须是 realtime 或 digest")
	}
	if c.Mode == "" {
		c.Mode = notify.ModeRealtime
	}
	if err := (notify.Filter{MinSeverity: c.MinSeverity}).Validate(); err != nil {
		return err
	}
	ch, _ := notify.Get(c.Kind)
	if err := ch.Validate(c.Config); err != nil {
		return err
	}
	if c.RatePerMin < 0 {
		c.RatePerMin = 0
	} else if c.RatePerMin == 0 {
		c.RatePerMin = ch.DefaultRatePerMin()
	}
	return nil
}

type errString string

func (e errString) Error() string { return string(e) }

// notifyChannelItem 处理单个通道：更新（PUT）、删除（DELETE）、测试发送（POST .../test）。
func (h *hub) notifyChannelItem(w http.ResponseWriter, r *http.Request, tail string) {
	id := tail
	isTest := false
	if i := strings.LastIndex(tail, "/"); i >= 0 {
		id, isTest = tail[:i], tail[i+1:] == "test"
		if !isTest {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "未知的通知接口"})
			return
		}
	}
	switch r.Method {
	case http.MethodPut:
		if isTest {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "未知的通知接口"})
			return
		}
		h.notifyUpdateChannel(w, r, id)
	case http.MethodDelete:
		if isTest {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "未知的通知接口"})
			return
		}
		h.notifyDeleteChannel(w, r, id)
	case http.MethodPost:
		if !isTest {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "未知的通知接口"})
			return
		}
		h.notifyTestChannel(w, r, id)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "仅支持 PUT/DELETE/POST"})
	}
}

func (h *hub) findChannel(id string) (int, notify.ChannelConfig, bool) {
	cfg := h.notifyStore.View()
	for i, c := range cfg.Channels {
		if c.ID == id {
			return i, c, true
		}
	}
	return 0, notify.ChannelConfig{}, false
}

// notifyUpdateChannel 更新通道。掩码契约：incoming.Config 里的掩码值原样回传
// 表示保留库中原值；改了目标地址却没重新提交凭据会被 PrepareConfigUpdate 拒绝。
func (h *hub) notifyUpdateChannel(w http.ResponseWriter, r *http.Request, id string) {
	i, stored, ok := h.findChannel(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "通道不存在"})
		return
	}
	var in notify.ChannelConfig
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体不是 JSON"})
		return
	}
	merged, err := notify.PrepareConfigUpdate(stored.Kind, stored.Config, in.Config)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	next := stored
	next.Config = merged
	next.Name = stored.Name
	if strings.TrimSpace(in.Name) != "" {
		next.Name = strings.TrimSpace(in.Name)
	}
	if in.Kind != "" && in.Kind != stored.Kind {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "通道类型不可更改，请新建通道"})
		return
	}
	next.Enabled = in.Enabled
	if in.Events != nil {
		next.Events = in.Events
	}
	next.MinSeverity = in.MinSeverity
	if in.Mode != "" {
		next.Mode = in.Mode
	}
	if in.RatePerMin > 0 {
		next.RatePerMin = in.RatePerMin
	}
	if err := h.validateChannel(&next); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	next.UpdatedAt = time.Now()
	f := h.notifyStore.View()
	f.Channels[i] = next
	if err := h.notifyStore.Save(f); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存失败：" + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, toChannelDTO(next))
}

func (h *hub) notifyDeleteChannel(w http.ResponseWriter, r *http.Request, id string) {
	i, _, ok := h.findChannel(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "通道不存在"})
		return
	}
	f := h.notifyStore.View()
	f.Channels = append(f.Channels[:i], f.Channels[i+1:]...)
	if err := h.notifyStore.Save(f); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存失败：" + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// notifyTestChannel 用已保存的连接参数同步发一条测试消息；渠道的原始错误
// 以 502 透传给前端（配置错误的定位要靠它）。
func (h *hub) notifyTestChannel(w http.ResponseWriter, r *http.Request, id string) {
	_, c, ok := h.findChannel(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "通道不存在"})
		return
	}
	ctx := r.Context()
	if err := h.notifier.TestChannel(ctx, c); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *hub) notifyDeliveries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "仅支持 GET"})
		return
	}
	state := r.URL.Query().Get("state")
	all := h.notifier.Deliveries()
	// 新→旧返回，控制台历史区总是最新的在前面。
	out := make([]notify.Delivery, 0, len(all))
	for i := len(all) - 1; i >= 0; i-- {
		if state != "" && all[i].State != state {
			continue
		}
		out = append(out, all[i])
	}
	writeJSON(w, http.StatusOK, map[string]any{"deliveries": out})
}

func (h *hub) notifyDeliveryRetry(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "仅支持 POST"})
		return
	}
	if !h.notifier.Retry(id) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "投递记录不存在或不可重试"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// notifySettings 更新全局开关 / 回链地址 / 摘要周期。
func (h *hub) notifySettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "仅支持 PUT"})
		return
	}
	var in struct {
		Enabled       *bool  `json:"enabled"`
		BaseURL       string `json:"base_url"`
		DigestMinutes *int   `json:"digest_minutes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体不是 JSON"})
		return
	}
	f := h.notifyStore.View()
	if in.Enabled != nil {
		f.Enabled = *in.Enabled
	}
	f.BaseURL = strings.TrimSpace(in.BaseURL)
	if in.DigestMinutes != nil {
		if *in.DigestMinutes < 1 || *in.DigestMinutes > 24*60 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "digest_minutes 必须在 1..1440 之间"})
			return
		}
		f.DigestMinutes = *in.DigestMinutes
	}
	if err := h.notifyStore.Save(f); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存失败：" + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
