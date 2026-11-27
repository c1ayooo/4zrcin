package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/settings"
)

// proxyStore builds a settings store over a harness file in a temp dir.
func proxyStore(t *testing.T, body string) *settings.Store {
	t.Helper()
	dir := t.TempDir()
	harness := filepath.Join(dir, "cyber.yaml")
	if err := os.WriteFile(harness, []byte("llm:\n  base_url: http://gateway.test/v1\n  api_key: gw-key\n  model: route\n"+body), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := settings.LoadAt(filepath.Join(dir, "settings.json"), harness)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// envValue returns the last definition of key in an "K=V" list ("" when absent).
func envValue(env []string, key string) string {
	prefix := key + "="
	value := ""
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			value = strings.TrimPrefix(kv, prefix)
		}
	}
	return value
}

// The node proxy the operator saved reaches the local scan as CYBER_PROXY in
// its environment. The file value alone is not delivery: a scan node takes
// the Hub's distributed config on connect, which carries none of the product
// sections, so a proxy left in the file is wiped (settings/node_proxy.go).
func TestHostEnvCarriesNodeProxy(t *testing.T) {
	t.Setenv("CYBER_PROXY", "")
	st := proxyStore(t, "extensions:\n  cyberhub:\n    proxy: socks5://127.0.0.1:1080\n")
	env := hostEnv([]string{"CYBER_MODEL=m1"}, "", nodeProxyValue(st))
	if got := envValue(env, "CYBER_PROXY"); got != "socks5://127.0.0.1:1080" {
		t.Fatalf("CYBER_PROXY = %q; env = %v", got, env)
	}
	if strings.Count(strings.Join(env, "\n"), "CYBER_PROXY=") != 1 {
		t.Fatalf("duplicate CYBER_PROXY entries: %v", env)
	}
	// Nothing saved: nothing is injected, and the scan egresses direct.
	env = hostEnv([]string{"CYBER_MODEL=m1"}, "", nodeProxyValue(proxyStore(t, "")))
	if got := envValue(env, "CYBER_PROXY"); got != "" {
		t.Fatalf("unsaved proxy injected: %q", got)
	}
}

// An operator's own CYBER_PROXY is the harness's documented override (the
// environment wins over the file); the host inherits it untouched instead of
// restating the file value over it.
func TestNodeProxyValueYieldsToInheritedEnv(t *testing.T) {
	t.Setenv("CYBER_PROXY", "http://env-proxy.test:8080")
	st := proxyStore(t, "extensions:\n  cyberhub:\n    proxy: socks5://127.0.0.1:1080\n")
	if nodeProxyValue(st) != "" {
		t.Fatal("a saved proxy would override the inherited CYBER_PROXY")
	}
}

// A CYBER_PROXY the operator put in the model env wins over the node default,
// the same rule the Python defaults follow, and the list keeps one definition.
func TestHostEnvModelEnvProxyWins(t *testing.T) {
	env := hostEnv([]string{"CYBER_PROXY=http://model-env.test:8080"}, "", "socks5://127.0.0.1:1080")
	if got := envValue(env, "CYBER_PROXY"); got != "http://model-env.test:8080" {
		t.Fatalf("CYBER_PROXY = %q; env = %v", got, env)
	}
	if strings.Count(strings.Join(env, "\n"), "CYBER_PROXY=") != 1 {
		t.Fatalf("duplicate CYBER_PROXY entries: %v", env)
	}
}
