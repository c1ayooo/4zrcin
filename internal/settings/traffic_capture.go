package settings

// Traffic capture summary.
//
// MITM capture is executed entirely by the harness/scan proxy; the console
// never starts it and never issues certificates. What the operator needs from
// the settings page is the honest answer to "will HTTPS show up in plaintext
// in the traffic view?" — and that answer lives in the operator's cyber.yaml
// (`extensions.traffic`), which this file only reads.
//
// The shape follows the fork's planned capture section:
//
//	extensions:
//	  traffic:
//	    capture_mode: relay|record     # record = intercept (MITM) + record
//	    decrypt_https: true            # intercept CONNECT to MITM-decrypt HTTPS
//	    ca_cert: /path/ca.pem          # operator-generated, operator-trusted
//
// As with guardrail: a missing section is "not configured" (relay-only, HTTPS
// bodies stay ciphertext), a malformed one is reported with a short reason,
// and neither may break the settings page. The certificate is never returned —
// only its SHA-256 fingerprint, so the operator can match it against what the
// browser asks them to trust.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// TrafficCaptureView is the read-only capture summary returned inside the
// instance settings view. Configured is false when extensions.traffic is
// missing or malformed; Reason then says why (one sentence, no secrets).
type TrafficCaptureView struct {
	Configured bool `json:"configured"`
	// CaptureMode is relay|record; empty means the harness default (relay:
	// pass-through, no MITM). The console must render relay as the current
	// state, not as a misconfiguration.
	CaptureMode string `json:"capture_mode,omitempty"`
	// DecryptHTTPS mirrors decrypt_https. It only means anything together with
	// capture_mode=record: a relay mode cannot decrypt anything.
	DecryptHTTPS bool `json:"decrypt_https"`
	// CACertPath is the operator's certificate path as written (not verified to
	// exist beyond what Fingerprint reports). The file itself is never returned.
	CACertPath string `json:"ca_cert_path,omitempty"`
	// CAFingerprint is "sha256:<hex>" of the certificate file bytes, so the
	// operator can match the fingerprint the browser shows. Empty when the path
	// is unset or unreadable (the reason then says which).
	CAFingerprint string `json:"ca_fingerprint,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

// trafficSectionKeys are the fields the fork's traffic section declares. An
// unknown field would be rejected by the scan's strict decoder, so the summary
// reports it instead of claiming the configuration is in effect.
var trafficSectionKeys = map[string]bool{
	"capture_mode": true, "decrypt_https": true, "ca_cert": true,
}

// TrafficCapture reads the operator's capture configuration. It is a pure
// read: no file is written, no host is contacted, and no certificate bytes
// leave the summary — only the fingerprint.
func (s *Store) TrafficCapture() TrafficCaptureView {
	view := TrafficCaptureView{CaptureMode: "relay"}
	_, doc, err := s.readDocument()
	if err != nil {
		view.Reason = "无法读取 harness 配置，捕获状态未知"
		return view
	}
	extensions := mapGet(doc, "extensions")
	node := mapGet(extensions, "traffic")
	if node == nil {
		view.Reason = "未配置 extensions.traffic：默认 relay 透传，HTTPS 正文不解密"
		return view
	}
	if node.Kind != yaml.MappingNode {
		view.Reason = "extensions.traffic 必须是映射"
		return view
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		if !trafficSectionKeys[key] {
			view.Reason = fmt.Sprintf("extensions.traffic 含未知字段 %q，宿主会拒绝该配置", key)
			return view
		}
	}
	mode := strings.ToLower(scalar(mapGet(node, "capture_mode")))
	switch mode {
	case "", "relay":
		mode = "relay"
	case "record":
	default:
		view.Reason = fmt.Sprintf("extensions.traffic.capture_mode=%q 不合法（只允许 relay/record）", mode)
		return view
	}
	view.CaptureMode = mode
	if raw := mapGet(node, "decrypt_https"); raw != nil {
		value, err := boolScalar(raw)
		if err != nil {
			view.Reason = "extensions.traffic.decrypt_https 不是布尔值"
			return view
		}
		view.DecryptHTTPS = value
	}
	if raw := mapGet(node, "ca_cert"); raw != nil {
		path := strings.TrimSpace(scalar(raw))
		if path != "" {
			view.CACertPath = path
			view.CAFingerprint = caFingerprint(path)
			if view.CAFingerprint == "" {
				view.Reason = "ca_cert 指向的文件读不到：解密已配置，但指纹无法核对"
			}
		}
	}
	// decrypt_https without record mode is a no-op the console must not present
	// as "HTTPS will be decrypted": relay passes the CONNECT through untouched.
	if mode == "relay" && view.DecryptHTTPS {
		view.Reason = "capture_mode=relay 时 decrypt_https 不生效（透传模式无法解密）"
	}
	view.Configured = true
	return view
}

// caFingerprint hashes the certificate file's bytes. Any read error (missing
// file, permissions, a directory) returns "" and the caller states why.
func caFingerprint(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return ""
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// TrafficCaptureInput is the closed write shape for extensions.traffic. It
// mirrors the three keys the fork's scan node declares; anything else stays
// hand-edited in cyber.yaml. An all-default input (relay, no decrypt, no CA)
// removes the section, returning the document to the honest "not configured"
// state the read path reports.
type TrafficCaptureInput struct {
	CaptureMode  string `json:"capture_mode"`
	DecryptHTTPS bool   `json:"decrypt_https"`
	// CACertPath is the operator's certificate file on this machine. The file
	// is never read into the configuration — only its existence (and, in the
	// projection, its fingerprint) matters.
	CACertPath string `json:"ca_cert,omitempty"`
}

// ValidateTrafficCapture rejects impossible combinations before anything is
// written, so a rejected value cannot leave the model half of a combined
// settings save applied. decrypt_https without record mode is rejected rather
// than stored-with-a-warning: the scan's strict decoder accepts it, but the
// console would then have to explain a no-op the operator did not ask for. A
// CA path must be a readable non-empty file — the same honesty the fingerprint
// projection applies.
func (s *Store) ValidateTrafficCapture(in TrafficCaptureInput) error {
	mode := normalizeCaptureMode(in.CaptureMode)
	if mode == "" {
		return fmt.Errorf("capture_mode 只允许 relay/record（收到 %q）", in.CaptureMode)
	}
	if in.DecryptHTTPS && mode != "record" {
		return fmt.Errorf("decrypt_https 需要 capture_mode=record：relay 透传模式无法解密")
	}
	if in.DecryptHTTPS && strings.TrimSpace(in.CACertPath) == "" {
		return errors.New("启用 HTTPS 解密必须提供 ca_cert 证书路径")
	}
	if path := strings.TrimSpace(in.CACertPath); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("ca_cert 指向的文件读不到（%s）：%w", path, err)
		}
		if len(raw) == 0 {
			return fmt.Errorf("ca_cert 指向的文件是空的：%s", path)
		}
	}
	return nil
}

// TrafficCaptureDiffers reports whether writing in would change the stored
// extensions.traffic section. The section's raw document state decides, not
// the projection's reason text: a missing section only changes when the write
// is non-default, a malformed section is always repaired (even by the default
// write, which removes it), and a well-formed section is compared field by
// field against the desired effective values.
func (s *Store) TrafficCaptureDiffers(in TrafficCaptureInput) (bool, error) {
	mode := normalizeCaptureMode(in.CaptureMode)
	path := strings.TrimSpace(in.CACertPath)
	defaulted := mode == "relay" && !in.DecryptHTTPS && path == ""
	_, doc, err := s.readDocument()
	if err != nil {
		return false, err
	}
	node := mapGet(mapGet(doc, "extensions"), "traffic")
	if node == nil {
		return !defaulted, nil
	}
	if node.Kind != yaml.MappingNode {
		return true, nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if !trafficSectionKeys[node.Content[i].Value] {
			return true, nil
		}
	}
	if defaulted {
		// The write removes the stored section: an honest-state change even
		// when the behavior (relay) stays the same.
		return true, nil
	}
	view := s.TrafficCapture()
	return view.CaptureMode != mode || view.DecryptHTTPS != in.DecryptHTTPS || view.CACertPath != path, nil
}

// SetTrafficCapture writes the operator's extensions.traffic section. The
// document write follows SetTrafficBodyRetention: under the write lock, comment
// and unknown-section preserving, and validated by the same rules the read path
// applies — a stored document the next scan start would reject is never
// persisted.
func (s *Store) SetTrafficCapture(in TrafficCaptureInput) error {
	if s.harness == "" {
		return fmt.Errorf("没有 harness 配置路径：设置 TW_HARNESS_CONFIG")
	}
	if err := s.ValidateTrafficCapture(in); err != nil {
		return err
	}
	mode := normalizeCaptureMode(in.CaptureMode)
	path := strings.TrimSpace(in.CACertPath)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	root, doc, err := s.readDocument()
	if err != nil {
		return err
	}
	extensions := mapGet(doc, "extensions")
	if extensions == nil {
		extensions = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		mapSet(doc, "extensions", extensions)
	}
	if extensions.Kind != yaml.MappingNode {
		return fmt.Errorf("harness 配置的 extensions 段必须是映射")
	}
	// All-default means "not configured": the section is removed instead of
	// stored as an explicit relay block, so the read path keeps saying exactly
	// what the fork's default behavior is.
	if mode == "relay" && !in.DecryptHTTPS && path == "" {
		mapDelete(extensions, "traffic")
		return s.writeHarnessDocument(root)
	}
	traffic := mapGet(extensions, "traffic")
	if traffic == nil {
		traffic = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		mapSet(extensions, "traffic", traffic)
	}
	if traffic.Kind != yaml.MappingNode {
		return fmt.Errorf("harness 配置的 extensions.traffic 段必须是映射")
	}
	mapSet(traffic, "capture_mode", stringScalar(mode))
	if in.DecryptHTTPS {
		mapSet(traffic, "decrypt_https", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
	} else {
		mapDelete(traffic, "decrypt_https")
	}
	if path != "" {
		mapSet(traffic, "ca_cert", stringScalar(path))
	} else {
		mapDelete(traffic, "ca_cert")
	}
	return s.writeHarnessDocument(root)
}

// normalizeCaptureMode maps the operator-facing empty value to the harness
// default, and reports anything outside the closed set as invalid (empty
// return).
func normalizeCaptureMode(raw string) string {
	mode := strings.ToLower(strings.TrimSpace(raw))
	switch mode {
	case "", "relay":
		return "relay"
	case "record":
		return "record"
	default:
		return ""
	}
}

func stringScalar(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}
