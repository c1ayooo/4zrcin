package settings

// Traffic capture switch.
//
// MITM capture is executed entirely by the harness/scan proxy; the console
// never starts it and never issues certificates. What the operator needs from
// the settings page is the honest answer to "will tool traffic be recorded?"
// and the switch that controls it is the scan's own `cyberhub.mitm` key
// (exts/scanner/config.go: `mitm`, default true — the top-level cyberhub
// section this file shares with the egress proxy writer; each owns its own
// keys and never touches the other's).
//
// An earlier projection wrote a planned `extensions.traffic` section
// (capture_mode / decrypt_https / ca_cert). No harness binary ever read those
// keys — the certificate the MITM hub presents is its own — so the write path
// now sets the switch that exists and the read path derives from it. A legacy
// nested extensions.traffic block is reported as legacy, not honored.
//
// The value is read by scan at process start: a change is restart-gated like
// every product section.

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// TrafficCaptureView is the capture summary returned inside the instance
// settings view.
type TrafficCaptureView struct {
	// CaptureMode is the effective mode: "record" (default) or "relay".
	CaptureMode string `json:"capture_mode"`
	Source      string `json:"source,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// TrafficCapture reads the operator's capture switch. An unreadable file is
// reported, not an error: the settings save reports the real parse error when
// it tries to persist.
func (s *Store) TrafficCapture() TrafficCaptureView {
	view := TrafficCaptureView{
		CaptureMode: "record",
		Source:      "默认（cyberhub.mitm 未设置 = 记录）",
	}
	if s == nil || s.harness == "" {
		view.Reason = "没有 harness 配置路径（ZRCIN_HARNESS_CONFIG），状态未知"
		return view
	}
	_, doc, err := s.readDocument()
	if err != nil {
		view.Reason = "无法读取 harness 配置，捕获状态未知"
		return view
	}
	if node := mapGet(mapGet(doc, "extensions"), "traffic"); node != nil {
		view.Reason = "旧版 extensions.traffic 捕获段没有引擎读取端，已废弃；请重新保存捕获设置（写入 cyberhub.mitm）"
	}
	section, err := productMapping(doc, "cyberhub")
	if err != nil {
		view.Reason = err.Error()
		return view
	}
	if section == nil {
		return view
	}
	raw := mapGet(section, "mitm")
	if raw == nil {
		return view
	}
	if err := boolValue(raw); err != nil {
		view.Reason = "cyberhub.mitm 不是布尔值：" + err.Error()
		return view
	}
	if raw.Value == "false" {
		view.CaptureMode = "relay"
	}
	view.Source = "操作员 cyber.yaml 的 cyberhub.mitm（scan 启动时读取）"
	return view
}

// boolValue validates one boolean scalar.
func boolValue(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!bool" {
		return fmt.Errorf("值 %q 不是布尔", node.Value)
	}
	return nil
}

// TrafficCaptureInput is the closed write shape for the capture switch. The
// mode maps onto cyberhub.mitm: record = true, relay = false.
type TrafficCaptureInput struct {
	CaptureMode string `json:"capture_mode"`
}

// ValidateTrafficCapture rejects impossible values before anything is written.
func (s *Store) ValidateTrafficCapture(in TrafficCaptureInput) error {
	if normalizeCaptureMode(in.CaptureMode) == "" {
		return fmt.Errorf("capture_mode 只允许 relay/record（收到 %q）", in.CaptureMode)
	}
	return nil
}

// TrafficCaptureDiffers reports whether writing in would change the stored
// cyberhub.mitm value.
func (s *Store) TrafficCaptureDiffers(in TrafficCaptureInput) (bool, error) {
	mode := normalizeCaptureMode(in.CaptureMode)
	_, doc, err := s.readDocument()
	if err != nil {
		return false, err
	}
	section, err := productMapping(doc, "cyberhub")
	if err != nil {
		return false, err
	}
	stored := "record"
	if section != nil {
		if raw := mapGet(section, "mitm"); raw != nil && raw.Value == "false" {
			stored = "relay"
		}
	}
	return stored != mode, nil
}

// SetTrafficCapture writes the operator's cyberhub.mitm key. The document
// write follows SetNodeProxy (its sibling in the same section): under the
// write lock, comment- and section-preserving, legacy nested blocks migrated
// up first. relay is stored as an explicit false — the default is record, so
// the relay choice must survive every later read.
func (s *Store) SetTrafficCapture(in TrafficCaptureInput) error {
	if s.harness == "" {
		return fmt.Errorf("没有 harness 配置路径：设置 ZRCIN_HARNESS_CONFIG")
	}
	if err := s.ValidateTrafficCapture(in); err != nil {
		return err
	}
	mode := normalizeCaptureMode(in.CaptureMode)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	root, doc, err := s.readDocument()
	if err != nil {
		return err
	}
	migrateProductSections(doc)
	cyberhub := mapGet(doc, "cyberhub")
	if cyberhub != nil && cyberhub.Kind != yaml.MappingNode {
		return fmt.Errorf("harness 配置的 cyberhub 段必须是映射")
	}
	if cyberhub == nil {
		cyberhub = ensureMapping(doc, "cyberhub")
	}
	if mode == "record" {
		mapDelete(cyberhub, "mitm")
	} else {
		mapSet(cyberhub, "mitm", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "false"})
	}
	if len(cyberhub.Content) == 0 {
		mapDelete(doc, "cyberhub")
	}
	return s.writeHarnessDocument(root)
}

// normalizeCaptureMode maps the operator-facing empty value to the harness
// default, and reports anything outside the closed set as invalid (empty
// return). The default is RECORD: cyberhub.mitm is init_default=true, so an
// unset switch records. (The read path derives the same default from a
// missing key; the two must never disagree — a save whose body omitted
// capture_mode means "keep the default", not "switch to relay".)
func normalizeCaptureMode(raw string) string {
	mode := strings.ToLower(strings.TrimSpace(raw))
	switch mode {
	case "", "record":
		return "record"
	case "relay":
		return "relay"
	default:
		return ""
	}
}
