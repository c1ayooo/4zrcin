package main

import (
	"net/http"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/project"
)

// A project can be created with its traffic fence in one shot. The assertions
// are on the persisted manifest: the fence shorthand is sugar over scope_rules,
// so the manifest must show exactly the shape the shorthand names — there is
// no separate fence field to drift from the rules it claims to summarize.
func TestCreateProjectFenceShorthand(t *testing.T) {
	h, mgr := createOptionsHub(t, hubConfig{NoDecider: true})

	// Default (no fence fields): unfenced — no scope_rules in the manifest.
	code, out := postProject(t, h, `{"name":"Open","scope":"open.example"}`)
	if code != http.StatusCreated {
		t.Fatalf("unfenced create = %d %v", code, out)
	}
	p := mgr.Get(out["id"].(string))
	if man := readManifest(t, p); man.ScopeRuleSet != nil {
		t.Fatalf("unfenced manifest stored scope_rules: %+v", man.ScopeRuleSet)
	}
	if got := p.FenceMode(); got != project.FenceUnfenced {
		t.Fatalf("unfenced project reports fence mode %q", got)
	}

	// fence=shadow with no table: enabled, shadow, record default.
	code, out = postProject(t, h, `{"name":"Trial","scope":"trial.example","fence":"shadow"}`)
	if code != http.StatusCreated {
		t.Fatalf("shadow create = %d %v", code, out)
	}
	p = mgr.Get(out["id"].(string))
	man := readManifest(t, p)
	if man.ScopeRuleSet == nil || !man.ScopeRuleSet.Enabled || !man.ScopeRuleSet.Shadow {
		t.Fatalf("shadow manifest scope_rules = %+v; want enabled+shadow", man.ScopeRuleSet)
	}
	if got := p.FenceMode(); got != project.FenceShadow {
		t.Fatalf("shadow project reports fence mode %q", got)
	}

	// fence=enforced with a table: enabled, not shadow, rows preserved.
	code, out = postProject(t, h, `{"name":"Fenced","scope":"fenced.example","fence":"enforced",
		"scope_rules":{"default_action":"review","rules":[
			{"action":"block","method":"*","host":"10.0.0.0/8","path_prefix":"/"},
			{"action":"record","method":"GET","host":"*.corp.example"}]}}`)
	if code != http.StatusCreated {
		t.Fatalf("enforced create = %d %v", code, out)
	}
	p = mgr.Get(out["id"].(string))
	man = readManifest(t, p)
	if man.ScopeRuleSet == nil || !man.ScopeRuleSet.Enabled || man.ScopeRuleSet.Shadow {
		t.Fatalf("enforced manifest scope_rules = %+v; want enabled, not shadow", man.ScopeRuleSet)
	}
	if len(man.ScopeRuleSet.Rules) != 2 || man.ScopeRuleSet.Rules[0].Host != "10.0.0.0/8" {
		t.Fatalf("enforced manifest rules = %+v", man.ScopeRuleSet.Rules)
	}
	if got := p.FenceMode(); got != project.FenceEnforced {
		t.Fatalf("enforced project reports fence mode %q", got)
	}
}

// Rejected fence inputs must fail the whole creation with nothing left behind:
// an invalid value or a contradictory pair is not a half-created engagement.
func TestCreateProjectFenceValidation(t *testing.T) {
	h, mgr := createOptionsHub(t, hubConfig{NoDecider: true})

	cases := []struct {
		name string
		body string
	}{
		{"unknown fence", `{"name":"Bad","scope":"bad.example","fence":"loose"}`},
		{"unfenced with rules", `{"name":"Bad","scope":"bad.example","fence":"unfenced",
			"scope_rules":{"default_action":"record","rules":[{"action":"record","method":"*","host":"*"}]}}`},
		{"invalid rule table", `{"name":"Bad","scope":"bad.example","fence":"enforced",
			"scope_rules":{"default_action":"obliterate"}}`},
		{"invalid CIDR host", `{"name":"Bad","scope":"bad.example","fence":"enforced",
			"scope_rules":{"default_action":"record","rules":[{"action":"record","method":"*","host":"10.0.0.0/99"}]}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, out := postProject(t, h, tc.body)
			if code != http.StatusBadRequest {
				t.Fatalf("%s: create = %d %v; want 400", tc.name, code, out)
			}
			if id, _ := out["id"].(string); id != "" && mgr.Get(id) != nil {
				t.Fatalf("%s: rejected creation left project %s in the manager", tc.name, id)
			}
			if got := len(mgr.All()); got != 0 {
				t.Fatalf("%s: rejected creation left %d projects behind", tc.name, got)
			}
		})
	}
}
