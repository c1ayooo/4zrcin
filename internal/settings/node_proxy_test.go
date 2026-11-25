package settings

import (
	"os"
	"strings"
	"testing"
)

// The write path round-trips through the read projection: what SetNodeProxy
// stores is exactly what NodeProxy reports, and the empty input removes the
// key instead of storing an explicit empty value.
func TestSetNodeProxyRoundTrip(t *testing.T) {
	st := guardrailStore(t, "")

	if err := st.SetNodeProxy(NodeProxyInput{Proxy: "socks5://127.0.0.1:1080"}); err != nil {
		t.Fatalf("socks5 write: %v", err)
	}
	view := st.NodeProxy()
	if !view.Configured || view.Proxy != "socks5://127.0.0.1:1080" {
		t.Fatalf("view after write = %+v", view)
	}
	if !strings.Contains(view.Source, "cyberhub.proxy") {
		t.Fatalf("source = %q", view.Source)
	}

	// An unchanged write is no change; a different endpoint is.
	changed, err := st.NodeProxyDiffers(NodeProxyInput{Proxy: "socks5://127.0.0.1:1080"})
	if err != nil || changed {
		t.Fatalf("same write differs = %v, %v", changed, err)
	}
	changed, err = st.NodeProxyDiffers(NodeProxyInput{Proxy: "trojan://relay.example:443"})
	if err != nil || !changed {
		t.Fatalf("different endpoint differs = %v, %v", changed, err)
	}

	// The empty input deletes the key — and a cyberhub section that only held
	// the proxy is removed entirely, so no empty husk survives.
	if err := st.SetNodeProxy(NodeProxyInput{}); err != nil {
		t.Fatalf("clear write: %v", err)
	}
	if view := st.NodeProxy(); view.Configured {
		t.Fatalf("cleared write left a configured proxy: %+v", view)
	}
	data, err := os.ReadFile(st.harness)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "cyberhub") {
		t.Fatalf("cleared write left a cyberhub section: %s", data)
	}
	changed, err = st.NodeProxyDiffers(NodeProxyInput{})
	if err != nil || changed {
		t.Fatalf("clear-vs-unconfigured differs = %v, %v", changed, err)
	}
}

// A cyberhub section the operator already maintains (url/key/mode) keeps its
// other keys through a proxy write and through a proxy clear.
func TestSetNodeProxyPreservesSection(t *testing.T) {
	st := guardrailStore(t, "cyberhub:\n  url: http://hub.test:9000\n  key: hk\n  mode: merge\n")
	if err := st.SetNodeProxy(NodeProxyInput{Proxy: "clash://https://sub.example/list"}); err != nil {
		t.Fatalf("clash write: %v", err)
	}
	view := st.NodeProxy()
	if !view.Configured || view.Proxy != "clash://https://sub.example/list" {
		t.Fatalf("view = %+v", view)
	}
	data, _ := os.ReadFile(st.harness)
	for _, want := range []string{"url: http://hub.test:9000", "key: hk", "mode: merge"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("section lost %q after proxy write:\n%s", want, data)
		}
	}
	if err := st.SetNodeProxy(NodeProxyInput{}); err != nil {
		t.Fatalf("clear write: %v", err)
	}
	data, _ = os.ReadFile(st.harness)
	for _, want := range []string{"url: http://hub.test:9000", "key: hk"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("section lost %q after proxy clear:\n%s", want, data)
		}
	}
}

// CYBER_PROXY overrides the file at scan start; the view reports it as the
// effective source instead of presenting the file value as final.
func TestNodeProxyEnvOverrideReported(t *testing.T) {
	t.Setenv("CYBER_PROXY", "socks5://env-proxy.test:1080")
	st := guardrailStore(t, "cyberhub:\n  proxy: trojan://file-proxy.test:443\n")
	view := st.NodeProxy()
	if !view.Configured || view.Proxy != "trojan://file-proxy.test:443" {
		t.Fatalf("view = %+v", view)
	}
	if view.EnvOverride != "socks5://env-proxy.test:1080" {
		t.Fatalf("env override = %q", view.EnvOverride)
	}
	if !strings.Contains(view.Source, "环境变量") {
		t.Fatalf("source does not say the env wins: %q", view.Source)
	}
}

// Malformed values never persist: the same pre-validation contract the
// capture section follows, and a rejected write leaves the document untouched.
func TestSetNodeProxyRejectsInvalid(t *testing.T) {
	st := guardrailStore(t, "cyberhub:\n  proxy: socks5://ok.test:1080\n")
	before, err := os.ReadFile(st.harness)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"unknown scheme", "gopher://x.test:70", "不在支持列表"},
		{"missing host", "socks5://", "缺少主机地址"},
		{"unparsable", "http://[::1", "不是可解析的 URL"},
	}
	for _, tc := range cases {
		if err := st.ValidateNodeProxy(NodeProxyInput{Proxy: tc.in}); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}
	after, _ := os.ReadFile(st.harness)
	if string(before) != string(after) {
		t.Fatal("rejected writes modified the document")
	}
	// The empty value is legal (direct egress again).
	if err := st.ValidateNodeProxy(NodeProxyInput{}); err != nil {
		t.Fatalf("empty proxy rejected: %v", err)
	}
}
