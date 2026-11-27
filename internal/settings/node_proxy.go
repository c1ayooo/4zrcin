package settings

// Node-level scanner egress proxy (top-level `cyberhub.proxy` in the
// operator's cyber.yaml).
//
// The MITM hub is executed entirely by the harness/scan node; the console
// never dials anything itself. What the execution-environment page needs is
// the honest answer to "where does this node's tool traffic egress?" — and
// that answer lives in the operator's cyber.yaml, which this file reads and
// writes with the same comment-preserving discipline as the capture section.
//
// Shape (the scanner extension's section, exts/scanner/config.go, stored
// under extensions — the one place the Hub's strict parser keeps a scan-side
// section; a top-level cyberhub is rejected by the runtime-config validation
// every settings save runs):
//
//	extensions:
//	  cyberhub:
//	    url: ...          # operator-managed, untouched by this file
//	    key: ...          # operator-managed, untouched by this file
//	    proxy: socks5://… # the scanner egress default this file owns
//
// `clash://` is a subscription spec, not a host/port endpoint, and is legal
// here (the scanner supports subscriptions with load balancing) — unlike the
// project-level proxy, whose endpoints are single dialable nodes.
//
// The value is the NODE default: every project on this node starts from it,
// and a project-level proxy (the project manifest's `proxy`) overrides it per
// session. Scope discipline: this file edits the node default only.
//
// The file is the stored source of truth, not the delivery: a scan node in
// web mode replaces its file sections with what the Hub distributes, and the
// Hub distributes none of the product sections — a proxy left in the file is
// wiped the moment the node connects. Delivery is CYBER_PROXY in the scan's
// environment (internal/host for the local node, the launcher for tunnel
// nodes), which the harness applies on top of whatever it was distributed.

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// NodeProxySchemes are the proxy URL schemes the scanner accepts. clash:// is
// the subscription spec the scanner's auto mode load-balances over.
var NodeProxySchemes = []string{
	"http", "https", "socks5", "trojan", "vless", "vmess", "hysteria2", "anytls", "clash",
}

// NodeProxyView is the read-only egress summary for the execution-environment
// page. Proxy is the stored URL as written (never empty-string-normalized:
// an empty value means "direct"), plus where the effective value comes from —
// the CYBER_PROXY environment variable overrides the file at scan start, and
// the console must say so instead of presenting the file value as effective.
type NodeProxyView struct {
	Configured bool `json:"configured"`
	// Proxy is the cyber.yaml value as written. Empty means direct egress.
	Proxy string `json:"proxy,omitempty"`
	// EnvOverride mirrors CYBER_PROXY when set: the scanner prefers it over
	// the file, so this is what actually applies until the variable is unset.
	EnvOverride string `json:"env_override,omitempty"`
	Source      string `json:"source,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// NodeProxy reads the node's scanner egress default. A file that cannot be
// read is not an error here: the view reports it, the settings save reports
// the real parse error when it tries to persist.
func (s *Store) NodeProxy() NodeProxyView {
	view := NodeProxyView{Source: "默认直连（cyber.yaml 未设置 cyberhub.proxy）"}
	if env := strings.TrimSpace(os.Getenv("CYBER_PROXY")); env != "" {
		view.EnvOverride = env
	}
	if s == nil || s.harness == "" {
		view.Reason = "没有 harness 配置路径（ZRCIN_HARNESS_CONFIG），状态未知"
		return view
	}
	_, doc, err := s.readDocument()
	if err != nil {
		view.Reason = "无法读取 harness 配置，出网代理状态未知"
		return view
	}
	section, err := cyberhubSection(doc)
	if err != nil {
		view.Reason = err.Error()
		return view
	}
	if section == nil {
		return view
	}
	raw := mapGet(section, "proxy")
	if raw == nil {
		return view
	}
	value := strings.TrimSpace(scalar(raw))
	if value == "" {
		return view
	}
	view.Proxy = value
	view.Configured = true
	view.Source = "操作员 cyber.yaml 的 extensions.cyberhub.proxy（本实例节点的默认出网，项目代理优先）"
	if reason := checkNodeProxyURL(value); reason != "" {
		view.Reason = reason
	}
	if view.EnvOverride != "" {
		view.Source += "；CYBER_PROXY 环境变量已设置，scan 启动时以环境变量为准"
	}
	return view
}

// checkNodeProxyURL reports the first problem with a stored proxy URL as one
// sentence ("" means fine). The scanner's own dialer is the final authority;
// this keeps the console honest about values it would watch fail.
func checkNodeProxyURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Sprintf("cyberhub.proxy 不是可解析的 URL：%v", err)
	}
	scheme := strings.ToLower(u.Scheme)
	allowed := false
	for _, s := range NodeProxySchemes {
		if scheme == s {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Sprintf("cyberhub.proxy 协议 %q 不在支持列表（%s）", u.Scheme, strings.Join(NodeProxySchemes, "/"))
	}
	if scheme != "clash" && u.Host == "" {
		return "cyberhub.proxy 缺少主机地址"
	}
	return ""
}

// NodeProxyInput is the closed write shape for cyberhub.proxy. An empty URL
// removes the key (direct egress again); the rest of the cyberhub section
// (url/key/mode) stays hand-edited and is never touched.
type NodeProxyInput struct {
	Proxy string `json:"proxy"`
}

// ValidateNodeProxy rejects impossible values before anything is written: the
// URL must parse, carry a supported scheme and a host (clash subscriptions
// are exempt from the host rule — they are specs, not endpoints). The empty
// value is legal: it removes the key and returns the node to direct egress.
func (s *Store) ValidateNodeProxy(in NodeProxyInput) error {
	value := strings.TrimSpace(in.Proxy)
	if value == "" {
		return nil
	}
	if reason := checkNodeProxyURL(value); reason != "" {
		return fmt.Errorf("%s", reason)
	}
	return nil
}

// NodeProxyDiffers reports whether writing in would change the stored
// cyberhub.proxy value (raw document comparison, not projection text).
func (s *Store) NodeProxyDiffers(in NodeProxyInput) (bool, error) {
	value := strings.TrimSpace(in.Proxy)
	_, doc, err := s.readDocument()
	if err != nil {
		return false, err
	}
	section, err := cyberhubSection(doc)
	if err != nil {
		return false, err
	}
	if section == nil {
		return value != "", nil
	}
	raw := mapGet(section, "proxy")
	stored := ""
	if raw != nil {
		stored = strings.TrimSpace(scalar(raw))
	}
	return stored != value, nil
}

// SetNodeProxy writes the operator's extensions.cyberhub.proxy key. The write
// follows SetTrafficCapture: under the write lock, comment- and section-
// preserving, and validated by the same rules the read path applies. An empty
// URL removes the key so the honest "direct" state returns; a cyberhub
// section (and an extensions mapping) that would be left empty is removed, so
// no empty husk survives in the file.
func (s *Store) SetNodeProxy(in NodeProxyInput) error {
	if s.harness == "" {
		return fmt.Errorf("没有 harness 配置路径：设置 ZRCIN_HARNESS_CONFIG")
	}
	if err := s.ValidateNodeProxy(in); err != nil {
		return err
	}
	value := strings.TrimSpace(in.Proxy)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	root, doc, err := s.readDocument()
	if err != nil {
		return err
	}
	extensions := mapGet(doc, "extensions")
	if extensions != nil && extensions.Kind != yaml.MappingNode {
		return fmt.Errorf("harness 配置的 extensions 段必须是映射")
	}
	cyberhub, err := cyberhubSection(doc)
	if err != nil {
		return err
	}
	if value == "" && cyberhub == nil {
		return nil
	}
	if extensions == nil {
		extensions = ensureMapping(doc, "extensions")
	}
	if cyberhub == nil {
		cyberhub = ensureMapping(extensions, "cyberhub")
	}
	if value == "" {
		mapDelete(cyberhub, "proxy")
	} else {
		mapSet(cyberhub, "proxy", stringScalar(value))
	}
	if len(cyberhub.Content) == 0 {
		mapDelete(extensions, "cyberhub")
	}
	if len(extensions.Content) == 0 {
		mapDelete(doc, "extensions")
	}
	return s.writeHarnessDocument(root)
}

// cyberhubSection returns the operator's cyberhub mapping under extensions,
// or nil when the file has none. A malformed shape is reported, never
// rewritten: it is the operator's text, and the console must say what is
// wrong with it instead of quietly replacing it.
func cyberhubSection(doc *yaml.Node) (*yaml.Node, error) {
	extensions := mapGet(doc, "extensions")
	if extensions == nil {
		return nil, nil
	}
	if extensions.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("cyber.yaml 的 extensions 段必须是映射")
	}
	section := mapGet(extensions, "cyberhub")
	if section == nil {
		return nil, nil
	}
	if section.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("cyber.yaml 的 extensions.cyberhub 段必须是映射")
	}
	return section, nil
}
