// Project-level egress proxy configuration.
//
// Each project can route its traffic through its own upstream proxy: the
// manifest carries the operator's setting, dispatch rides it to the harness as
// a per-turn fork extension (4zrcin.EgressProxy, aop/egressproxy.go), and the
// executing node applies it per session — so two projects running on the same
// scan node egress through different proxies without touching each other.
//
// The shape mirrors scope_rules.go deliberately: one normalize/validate
// function, unconditional validation (an invalid draft must not be storable
// just because Enabled is false), nil in the manifest means "never
// configured", and a pointer receipt from the setter so the caller cannot
// mutate what was persisted.
//
// The value is dispatch-side configuration, never a brief input: like
// scope_rules it travels to the execution plane as wire JSON and is never
// merged into the scope document (D59). The credential lives in project.json
// beside the ledger — the same trust boundary as the manifest itself; nothing
// copies it into journals, briefs or events.
package project

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// EgressProxySchemes are the proxy URL schemes the executing node can dial.
// They are exactly what chainreactors/proxyclient accepts (socks5 built in;
// the extras registered by tools/proxy/register_command.go). clash:// is
// deliberately absent: a subscription is not a host/port endpoint, so it
// belongs to the host-level default (cyberhub.proxy), not to a project row.
var EgressProxySchemes = []string{
	"http", "https", "socks5", "trojan", "vless", "vmess", "hysteria2", "anytls",
}

// EgressProxy is one project's upstream proxy. Enabled=false keeps the stored
// endpoint as a draft: dispatch still sends it (a session that outlives the
// save must learn it was switched off), and flipping the switch back on does
// not require retyping the address.
type EgressProxy struct {
	Enabled bool `json:"enabled"`
	// Scheme is one of EgressProxySchemes ("socks5", "trojan", …), stored
	// lowercase without the "://".
	Scheme string `json:"scheme"`
	// Host is the proxy server address: an IP literal or a hostname. No
	// wildcard, no path, no userinfo — the proxy is infrastructure, not a
	// match target.
	Host string `json:"host"`
	// Port is the proxy server port. Empty/0 means the scheme's default
	// (80/443/1080); an explicit port is what most proxies require anyway.
	Port int `json:"port,omitempty"`
	// Username/Password are the optional proxy credentials. Both are stored
	// as given (no trimming of the password: spaces can be legal there).
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	// URL is the dialable proxy URL, computed by normalization at every save
	// so memory, manifest and the 4zrcin.EgressProxy wire payload all carry
	// the same value. The executing node reads this field first and never
	// re-derives scheme defaults, so there is no second port table to drift.
	URL string `json:"url,omitempty"`
}

// DialURL renders the dialable proxy URL from the normalized fields. It is
// what normalization stores into the URL field; callers should prefer that
// stored value (the executing node does) so both ends agree byte for byte.
func (e EgressProxy) DialURL() string {
	u := url.URL{Scheme: e.Scheme, Host: hostPort(e.Host, e.Port, defaultPort(e.Scheme))}
	if e.Username != "" {
		if e.Password != "" {
			u.User = url.UserPassword(e.Username, e.Password)
		} else {
			u.User = url.User(e.Username)
		}
	}
	return u.String()
}

func hostPort(host string, port, fallback int) string {
	if port == 0 {
		port = fallback
	}
	if port == 0 {
		return host
	}
	return fmt.Sprintf("%s:%d", host, port)
}

func defaultPort(scheme string) int {
	switch scheme {
	case "http":
		return 80
	case "https":
		return 443
	case "socks5":
		return 1080
	}
	return 0
}

// NormalizeEgressProxy validates and normalizes an operator-submitted proxy.
// Validation is unconditional — even while Enabled is false — so the stored
// draft can never hold an endpoint the dialer would reject at the moment the
// operator flips the switch.
func NormalizeEgressProxy(in EgressProxy) (EgressProxy, error) {
	out := EgressProxy{Enabled: in.Enabled, Port: in.Port, Username: in.Username, Password: in.Password}

	scheme := strings.ToLower(strings.TrimSpace(in.Scheme))
	scheme = strings.TrimSuffix(scheme, "://")
	allowed := false
	for _, s := range EgressProxySchemes {
		if scheme == s {
			allowed = true
			break
		}
	}
	if !allowed {
		return EgressProxy{}, fmt.Errorf("proxy.scheme 只能是 %s（收到 %q）", strings.Join(EgressProxySchemes, "/"), in.Scheme)
	}
	out.Scheme = scheme

	host := strings.TrimSpace(in.Host)
	if host == "" {
		return EgressProxy{}, errors.New("proxy.host 不能为空")
	}
	if strings.ContainsAny(host, " \t\r\n/@") || strings.Contains(host, "/") {
		return EgressProxy{}, fmt.Errorf("proxy.host 必须是主机名或 IP，不含路径或凭据（收到 %q）", in.Host)
	}
	out.Host = host

	if in.Port < 0 || in.Port > 65535 {
		return EgressProxy{}, fmt.Errorf("proxy.port 必须在 1-65535（收到 %d）", in.Port)
	}
	// A credential block needs a username; a username without a password is
	// legal, the reverse is not expressible in userinfo. An entirely blank
	// username field means "no credentials"; a whitespace-only one next to a
	// password is a typo, and silently dropping the credentials would turn the
	// next dial into a confusing auth failure — reject it instead.
	if strings.TrimSpace(in.Username) == "" {
		if in.Username != "" && in.Password != "" {
			return EgressProxy{}, errors.New("proxy.username 是空白但填写了 password：请填写用户名或清空凭据")
		}
		out.Username = ""
		out.Password = ""
	}
	if strings.ContainsAny(out.Username, " \t\r\n") {
		return EgressProxy{}, errors.New("proxy.username 不能含空白字符")
	}
	if strings.ContainsAny(out.Password, " \t\r\n") {
		return EgressProxy{}, errors.New("proxy.password 不能含空白字符")
	}
	// The dial URL is recomputed here on every write: the stored value can
	// never drift from the fields, and the executing node reads it as-is.
	out.URL = out.DialURL()
	return out, nil
}

// ProxyOf returns the project's stored proxy. ok=false means the manifest has
// none, which dispatch reads as "egress through the node default".
func (p *Project) ProxyOf() (EgressProxy, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.EgressProxy == nil {
		return EgressProxy{}, false
	}
	return *p.EgressProxy, true
}

// SetEgressProxy validates and persists the operator's proxy; nil clears it so
// the project goes back to the node's default route with no stored draft. The
// value is normalized before the write so memory, disk and any later dispatch
// all see the same shape.
func (m *Manager) SetEgressProxy(id string, proxy *EgressProxy) (EgressProxy, error) {
	var normalized EgressProxy
	if proxy != nil {
		var err error
		normalized, err = NormalizeEgressProxy(*proxy)
		if err != nil {
			return EgressProxy{}, err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.byID[id]
	if !ok {
		return EgressProxy{}, fmt.Errorf("没有这个项目：%s", id)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	before := p.Manifest
	if proxy == nil {
		p.EgressProxy = nil
	} else {
		// Copy the value: the receipt the caller keeps must not alias the
		// struct the manifest now owns (same contract as the ScopeRules and
		// PeerPolicy receipts).
		stored := normalized
		p.EgressProxy = &stored
	}
	if err := p.writeOrRestore(func() { p.EgressProxy = before.EgressProxy }); err != nil {
		return EgressProxy{}, err
	}
	return normalized, nil
}
