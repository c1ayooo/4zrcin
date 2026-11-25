package aop

import (
	"encoding/json"
	"testing"
)

// No configured proxy sends nothing: the wire must stay byte-identical to the
// pre-proxy behavior (same contract as ScopeRulesExtension).
func TestEgressProxyExtensionAbsent(t *testing.T) {
	if got := EgressProxyExtension(nil); got != nil {
		t.Fatalf("nil payload produced %+v", got)
	}
	if got := EgressProxyExtension(json.RawMessage("  ")); got != nil {
		t.Fatalf("blank payload produced %+v", got)
	}
}

// The payload wraps verbatim: this layer owns the type URL only, the manifest
// JSON is the contract and must not be re-encoded into a second shape. A
// disabled draft still rides (the fork must learn the switch), so the wrap
// never filters on Enabled.
func TestEgressProxyExtensionWrapsVerbatim(t *testing.T) {
	payload := json.RawMessage(`{"enabled":true,"scheme":"socks5","host":"10.0.0.9","port":1080,"username":"ops","password":"secret"}`)
	got := EgressProxyExtension(payload)
	if got == nil {
		t.Fatal("a configured proxy must produce an extension")
	}
	if got.TypeUrl != EgressProxyExtensionURL {
		t.Fatalf("type url = %q", got.TypeUrl)
	}
	if string(got.Value) != string(payload) {
		t.Fatalf("value = %s, want verbatim %s", got.Value, payload)
	}
}
