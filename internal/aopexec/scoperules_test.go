package aopexec

import (
	"context"
	"strings"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/executor"
)

// scopeRulesFixture is one decoded 4zrcin.ScopeRules extension as the fake host
// read it off the wire, plus whether the request carried it at all. The fields
// mirror the manifest JSON because the manifest JSON is the wire contract.
type scopeRulesFixture struct {
	Enabled       bool   `json:"enabled"`
	DefaultAction string `json:"default_action"`
	Shadow        bool   `json:"shadow"`
	Rules         []struct {
		ID     string `json:"id"`
		Action string `json:"action"`
		Method string `json:"method"`
		Host   string `json:"host"`
	} `json:"rules"`
	Present bool   `json:"-"`
	Raw     string `json:"-"`
}

// A configured project rides its rule set on every turn: the fork judges the
// turn's traffic and commands against what the manifest held at dispatch, not
// against a push-once copy that a restarted host would keep forever.
func TestRunCarriesScopeRulesExtension(t *testing.T) {
	work := t.TempDir()
	h := &fakeHost{sessions: map[string]bool{}, hostDir: parentDir(work)}
	ex, done := newFake(t, h)
	defer done()

	payload := []byte(`{"enabled":true,"default_action":"review","shadow":false,"rules":[{"id":"sr-1","action":"review","method":"POST","host":"*","path_prefix":"/"}]}`)
	_, err := ex.Run(context.Background(), executor.Request{
		IntentID: "i1", Prompt: "find the token", WorkDir: work,
		ScopeRules: payload,
	}, func(executor.Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(h.scopeRules) != 1 {
		t.Fatalf("RunTurn extensions carried %d scope-rules payloads, want 1", len(h.scopeRules))
	}
	got := h.scopeRules[0]
	if !got.Present || !got.Enabled || got.DefaultAction != "review" || len(got.Rules) != 1 {
		t.Fatalf("scope rules on the wire = %+v", got)
	}
	if got.Rules[0].ID != "sr-1" || got.Rules[0].Host != "*" || got.Rules[0].Method != "POST" {
		t.Fatalf("rule row = %+v", got.Rules[0])
	}
}

// A disabled draft still travels: the fork must see Enabled=false, or a session
// that outlives the save would keep enforcing the last enabled rules.
func TestRunCarriesDisabledScopeRulesDraft(t *testing.T) {
	work := t.TempDir()
	h := &fakeHost{sessions: map[string]bool{}, hostDir: parentDir(work)}
	ex, done := newFake(t, h)
	defer done()

	payload := []byte(`{"enabled":false,"default_action":"record","rules":[]}`)
	_, err := ex.Run(context.Background(), executor.Request{
		IntentID: "i1", Prompt: "find the token", WorkDir: work,
		ScopeRules: payload,
	}, func(executor.Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(h.scopeRules) != 1 || !h.scopeRules[0].Present || h.scopeRules[0].Enabled {
		t.Fatalf("disabled draft = %+v", h.scopeRules)
	}
	if h.scopeRules[0].DefaultAction != "record" {
		t.Fatalf("default_action = %q", h.scopeRules[0].DefaultAction)
	}
}

// A project that never configured rules sends no extension at all: the wire
// must stay byte-identical to the pre-scope-rules behavior.
func TestRunOmitsScopeRulesWhenUnset(t *testing.T) {
	work := t.TempDir()
	h := &fakeHost{sessions: map[string]bool{}, hostDir: parentDir(work)}
	ex, done := newFake(t, h)
	defer done()

	_, err := ex.Run(context.Background(), executor.Request{
		IntentID: "i1", Prompt: "find the token", WorkDir: work,
	}, func(executor.Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(h.scopeRules) != 0 {
		t.Fatalf("unconfigured project sent scope rules: %+v", h.scopeRules)
	}
}

func parentDir(dir string) string {
	i := strings.LastIndex(dir, "/")
	if i <= 0 {
		return "/"
	}
	return dir[:i]
}
