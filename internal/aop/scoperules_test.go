package aop

import (
	"encoding/json"
	"testing"
)

// Nothing configured means nothing on the wire: an unconfigured project must
// keep the turn request byte-identical to the pre-scope-rules behavior.
func TestScopeRulesExtensionAbsent(t *testing.T) {
	if got := ScopeRulesExtension(nil); got != nil {
		t.Fatalf("nil payload produced %+v", got)
	}
	if got := ScopeRulesExtension(json.RawMessage("  ")); got != nil {
		t.Fatalf("blank payload produced %+v", got)
	}
}

// The payload wraps verbatim: this layer owns the type URL only, the manifest
// JSON is the contract and must not be re-encoded into a second shape.
func TestScopeRulesExtensionWrapsVerbatim(t *testing.T) {
	payload := json.RawMessage(`{"enabled":true,"default_action":"review","rules":[{"id":"sr-1","action":"record","method":"*","host":"*.target-corp.example","path_prefix":"/"}]}`)
	got := ScopeRulesExtension(payload)
	if got == nil {
		t.Fatal("a configured set must produce an extension")
	}
	if got.TypeUrl != ScopeRulesExtensionURL {
		t.Fatalf("type url = %q", got.TypeUrl)
	}
	if string(got.Value) != string(payload) {
		t.Fatalf("value = %s, want verbatim %s", got.Value, payload)
	}
}
