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
