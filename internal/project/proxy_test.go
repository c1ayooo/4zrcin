package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func newProxyProject(t *testing.T) (*Manager, *Project) {
	t.Helper()
	root := t.TempDir()
	m, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Create("proxy test", "测试范围")
	if err != nil {
		t.Fatal(err)
	}
	return m, p
}

func validEgressProxy() EgressProxy {
	return EgressProxy{Enabled: true, Scheme: "socks5", Host: "10.0.0.9", Port: 1080, Username: "ops", Password: "secret"}
}

// A fresh project reports no proxy and the manifest does not carry the key.
func TestFreshProjectHasNoProxy(t *testing.T) {
	_, p := newProxyProject(t)
	if _, ok := p.ProxyOf(); ok {
		t.Fatal("a fresh project must report no proxy")
	}
	data, _ := os.ReadFile(filepath.Join(p.Dir, ManifestFile))
	if strings.Contains(string(data), `"proxy"`) {
		t.Fatal("a fresh manifest must not carry a proxy key")
	}
}

// The manifest is the source of truth: a normalized value survives reload, and
// a cleared value is really gone from disk, not just from memory.
func TestEgressProxyPersistAndClear(t *testing.T) {
	m, p := newProxyProject(t)
	saved, err := m.SetEgressProxy(p.ID, &[]EgressProxy{validEgressProxy()}[0])
	if err != nil {
		t.Fatal(err)
	}
	if !saved.Enabled || saved.Scheme != "socks5" {
		t.Fatalf("saved = %+v", saved)
	}
	if got, ok := p.ProxyOf(); !ok || !reflect.DeepEqual(got, saved) {
		t.Fatalf("in-memory view = %+v ok=%v, want %+v", got, ok, saved)
	}
	data, err := os.ReadFile(filepath.Join(p.Dir, ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Proxy *EgressProxy `json:"proxy"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Proxy == nil || manifest.Proxy.Host != "10.0.0.9" {
		t.Fatalf("manifest proxy = %+v", manifest.Proxy)
	}

	if _, err := m.SetEgressProxy(p.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := p.ProxyOf(); ok {
		t.Fatal("cleared proxy still visible in memory")
	}
	data, _ = os.ReadFile(filepath.Join(p.Dir, ManifestFile))
	if strings.Contains(string(data), `"proxy"`) {
		t.Fatal("cleared proxy still written to the manifest")
	}
}

// The receipt cannot mutate the stored value (same contract as ScopeRules and
// PeerPolicy): EgressProxy carries no slices, but the pointer discipline stays.
func TestEgressProxyReceiptCannotMutateStoredValue(t *testing.T) {
	m, p := newProxyProject(t)
	saved, err := m.SetEgressProxy(p.ID, &[]EgressProxy{validEgressProxy()}[0])
	if err != nil {
		t.Fatal(err)
	}
	saved.Host = "evil.example"
	stored, _ := p.ProxyOf()
	if stored.Host != "10.0.0.9" {
		t.Fatalf("mutating the receipt changed the stored proxy: %+v", stored)
	}
}

// Validation is unconditional: an invalid draft must not be storable just
// because Enabled is false, or flipping the switch later would arm an endpoint
// the dialer has to reject.
func TestEgressProxyValidateEvenWhenDisabled(t *testing.T) {
	m, p := newProxyProject(t)
	bad := validEgressProxy()
	bad.Enabled = false
	bad.Scheme = "gopher"
	if _, err := m.SetEgressProxy(p.ID, &bad); err == nil {
		t.Fatal("an unknown scheme must be rejected even when disabled")
	}
	if _, ok := p.ProxyOf(); ok {
		t.Fatal("the rejected write must not have persisted")
	}
}

func TestEgressProxyValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*EgressProxy)
	}{
		{"unknown scheme", func(e *EgressProxy) { e.Scheme = "clash" }},
		{"empty host", func(e *EgressProxy) { e.Host = "  " }},
		{"host with path", func(e *EgressProxy) { e.Host = "proxy.example/base" }},
		{"host with userinfo", func(e *EgressProxy) { e.Host = "user@proxy.example" }},
		{"negative port", func(e *EgressProxy) { e.Port = -1 }},
		{"port too large", func(e *EgressProxy) { e.Port = 65536 }},
		{"blank username", func(e *EgressProxy) { e.Username = "  " }},
		{"username whitespace", func(e *EgressProxy) { e.Username = "op s" }},
		{"password whitespace", func(e *EgressProxy) { e.Password = "s ecret" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bad := validEgressProxy()
			tc.mut(&bad)
			if _, err := NormalizeEgressProxy(bad); err == nil {
				t.Fatalf("NormalizeEgressProxy(%+v) accepted", bad)
			}
		})
	}
}

// Normalization fixes the shapes the operator typed, not just accepts them:
// scheme casing, the "://" suffix and a trimmed host all come back clean.
func TestEgressProxyNormalization(t *testing.T) {
	got, err := NormalizeEgressProxy(EgressProxy{Enabled: true, Scheme: "SOCKS5://", Host: " proxy.example ", Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	if got.Scheme != "socks5" || got.Host != "proxy.example" {
		t.Fatalf("normalized = %+v", got)
	}
	if got.DialURL() != "socks5://proxy.example:1080" {
		t.Fatalf("default port URL = %q", got.DialURL())
	}
}

// The URL is what the executing node parses: credentials land in userinfo,
// ports override the scheme default, and a username without a password is
// legal.
func TestEgressProxyURL(t *testing.T) {
	cases := []struct {
		name string
		in   EgressProxy
		want string
	}{
		{"socks5 default port", EgressProxy{Scheme: "socks5", Host: "10.0.0.9"}, "socks5://10.0.0.9:1080"},
		{"explicit port", EgressProxy{Scheme: "http", Host: "proxy.example", Port: 3128}, "http://proxy.example:3128"},
		{"auth", EgressProxy{Scheme: "socks5", Host: "10.0.0.9", Port: 1080, Username: "ops", Password: "secret"}, "socks5://ops:secret@10.0.0.9:1080"},
		{"username only", EgressProxy{Scheme: "trojan", Host: "relay.example", Port: 443, Username: "ops"}, "trojan://ops@relay.example:443"},
		{"trojan no default port", EgressProxy{Scheme: "trojan", Host: "relay.example"}, "trojan://relay.example"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			normalized, err := NormalizeEgressProxy(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if got := normalized.DialURL(); got != tc.want {
				t.Fatalf("DialURL() = %q, want %q", got, tc.want)
			}
			// The stored wire value must equal the rendered one: the executing
			// node reads URL as-is and never re-derives scheme defaults.
			if normalized.URL != tc.want {
				t.Fatalf("stored URL = %q, want %q", normalized.URL, tc.want)
			}
		})
	}
}

// Two projects keep their proxies independent: saving one never leaks into the
// other, and clearing one leaves the other's stored draft alone.
func TestEgressProxyIsolationAcrossProjects(t *testing.T) {
	root := t.TempDir()
	m, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	a, err := m.Create("A", "范围A")
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.Create("B", "范围B")
	if err != nil {
		t.Fatal(err)
	}
	first := validEgressProxy()
	if _, err := m.SetEgressProxy(a.ID, &first); err != nil {
		t.Fatal(err)
	}
	second := EgressProxy{Enabled: true, Scheme: "http", Host: "proxy-b.example", Port: 3128}
	if _, err := m.SetEgressProxy(b.ID, &second); err != nil {
		t.Fatal(err)
	}
	gotA, _ := a.ProxyOf()
	gotB, _ := b.ProxyOf()
	if gotA.Host != "10.0.0.9" || gotB.Host != "proxy-b.example" {
		t.Fatalf("proxies crossed: A=%+v B=%+v", gotA, gotB)
	}
	if _, err := m.SetEgressProxy(a.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.ProxyOf(); ok {
		t.Fatal("clearing A left a proxy")
	}
	if _, ok := b.ProxyOf(); !ok {
		t.Fatal("clearing A also cleared B")
	}
}

// Disabling is a stored state, not a deletion: the endpoint stays a draft and
// the wire payload keeps Enabled=false so a live session learns the switch.
func TestEgressProxyDisabledIsStored(t *testing.T) {
	m, p := newProxyProject(t)
	draft := validEgressProxy()
	draft.Enabled = false
	saved, err := m.SetEgressProxy(p.ID, &draft)
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := p.ProxyOf()
	if !ok || stored.Enabled {
		t.Fatalf("stored = %+v ok=%v, want a disabled draft", stored, ok)
	}
	raw, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"enabled":false`) {
		t.Fatalf("wire payload = %s", raw)
	}
}
