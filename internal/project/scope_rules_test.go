package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func newScopeProject(t *testing.T) (*Manager, *Project) {
	t.Helper()
	m, err := Open(filepath.Join(t.TempDir(), "projects"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Create("scope rules", "")
	if err != nil {
		t.Fatal(err)
	}
	return m, p
}

func validScopeRules() ScopeRules {
	return ScopeRules{
		Enabled:       true,
		DefaultAction: ScopeActionReview,
		Rules: []ScopeRule{
			{Action: ScopeActionRecord, Method: "*", Host: "*.target-corp.example", PathPrefix: "/"},
			{Action: ScopeActionReview, Method: "post|delete", Host: "*", PathPrefix: "/api/"},
		},
	}
}

// A project without scope rules is the historical state: the pipeline reads
// ok=false and stays record-only. Nothing may synthesize a default set.
func TestScopeRulesAbsentMeansNotConfigured(t *testing.T) {
	_, p := newScopeProject(t)
	if _, ok := p.ScopeRulesOf(); ok {
		t.Fatal("a fresh project must report no scope rules")
	}
}

// The manifest is the source of truth: a normalized set survives reload, and a
// cleared set is really gone from disk, not just from memory.
func TestScopeRulesPersistAndClear(t *testing.T) {
	m, p := newScopeProject(t)
	saved, err := m.SetScopeRules(p.ID, &[]ScopeRules{validScopeRules()}[0])
	if err != nil {
		t.Fatal(err)
	}
	if !saved.Enabled || saved.DefaultAction != ScopeActionReview {
		t.Fatalf("saved = %+v", saved)
	}
	// The method form is normalized: the operator typed lowercase, the stored
	// value is what the engine matches.
	if saved.Rules[1].Method != "POST|DELETE" {
		t.Fatalf("method normalization lost: %q", saved.Rules[1].Method)
	}
	if saved.Rules[0].ID == "" {
		t.Fatal("rule id was not generated; journal events could not name the rule")
	}
	if got, ok := p.ScopeRulesOf(); !ok || !reflect.DeepEqual(got, saved) {
		t.Fatalf("in-memory view = %+v ok=%v, want %+v", got, ok, saved)
	}
	data, err := os.ReadFile(filepath.Join(p.Dir, ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.ScopeRuleSet == nil || len(manifest.ScopeRuleSet.Rules) != 2 {
		t.Fatalf("manifest rules = %+v", manifest.ScopeRuleSet)
	}
	if _, err := m.SetScopeRules(p.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := p.ScopeRulesOf(); ok {
		t.Fatal("cleared rules still visible in memory")
	}
	data, _ = os.ReadFile(filepath.Join(p.Dir, ManifestFile))
	if strings.Contains(string(data), "scope_rules") {
		t.Fatal("cleared rules still written to the manifest")
	}
}

// The receipt cannot mutate the stored policy (same contract as PeerPolicy).
func TestScopeRulesReceiptCannotMutateStoredSet(t *testing.T) {
	m, p := newScopeProject(t)
	rules := validScopeRules()
	saved, err := m.SetScopeRules(p.ID, &rules)
	if err != nil {
		t.Fatal(err)
	}
	saved.Rules[0].Host = "evil.example"
	stored, _ := p.ScopeRulesOf()
	if stored.Rules[0].Host != "*.target-corp.example" {
		t.Fatalf("mutating the receipt changed the stored rules: %+v", stored)
	}
}

// Validation is unconditional: an invalid draft must not be storable just
// because Enabled is false, or flipping the switch later would arm a set the
// engine has to reject at request time.
func TestScopeRulesValidateEvenWhenDisabled(t *testing.T) {
	m, p := newScopeProject(t)
	bad := validScopeRules()
	bad.Enabled = false
	bad.DefaultAction = ""
	if _, err := m.SetScopeRules(p.ID, &bad); err == nil {
		t.Fatal("an empty default_action must be rejected even when disabled")
	}
	if _, ok := p.ScopeRulesOf(); ok {
		t.Fatal("the rejected write must not have persisted")
	}
}

func TestScopeRulesValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*ScopeRules)
	}{
		{"unknown action", func(r *ScopeRules) { r.Rules[0].Action = "forward" }},
		{"unknown method", func(r *ScopeRules) { r.Rules[1].Method = "BREW" }},
		{"empty method token", func(r *ScopeRules) { r.Rules[1].Method = "POST||GET" }},
		{"duplicate method", func(r *ScopeRules) { r.Rules[1].Method = "POST|POST" }},
		{"host with scheme", func(r *ScopeRules) { r.Rules[0].Host = "https://a.example.com" }},
		{"host with port", func(r *ScopeRules) { r.Rules[0].Host = "a.example.com:8080" }},
		{"host with path", func(r *ScopeRules) { r.Rules[0].Host = "a.example.com/api" }},
		{"host empty label", func(r *ScopeRules) { r.Rules[0].Host = "a..example.com" }},
		{"path without slash", func(r *ScopeRules) { r.Rules[1].PathPrefix = "api/" }},
		{"path with space", func(r *ScopeRules) { r.Rules[1].PathPrefix = "/api /x" }},
		{"duplicate rule", func(r *ScopeRules) { r.Rules[1] = r.Rules[0] }},
		{"duplicate id", func(r *ScopeRules) {
			r.Rules[0].ID, r.Rules[1].ID = "same", "same"
		}},
		{"too many rules", func(r *ScopeRules) {
			r.Rules = make([]ScopeRule, MaxScopeRules+1)
			for i := range r.Rules {
				r.Rules[i] = ScopeRule{Action: ScopeActionRecord, Method: "*", Host: "*", PathPrefix: "/" + string(rune('a'+i%26))}
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rules := validScopeRules()
			tc.mut(&rules)
			if _, err := NormalizeScopeRules(rules); err == nil {
				t.Fatalf("%s: expected an error", tc.name)
			}
		})
	}
}

// CIDR hosts are the fourth accepted form: the operator states a whole subnet
// as in range when a hostname wildcard cannot express it.
func TestScopeRulesCIDRHosts(t *testing.T) {
	for _, host := range []string{"10.0.0.0/8", "192.168.1.0/24", "2001:DB8::/32"} {
		got, err := NormalizeScopeRules(ScopeRules{
			Enabled:       true,
			DefaultAction: ScopeActionReview,
			Rules:         []ScopeRule{{Action: ScopeActionRecord, Method: "*", Host: host, PathPrefix: "/"}},
		})
		if err != nil {
			t.Fatalf("host %q: unexpected error: %v", host, err)
		}
		if want := strings.ToLower(host); got.Rules[0].Host != want {
			t.Fatalf("host %q normalized to %q", host, got.Rules[0].Host)
		}
	}
	for _, host := range []string{"10.0.0.0/99", "10.0.0/8", "*.corp/24", "10.0.0.0/", "10.0.0.0/8/8"} {
		rules := ScopeRules{
			Enabled:       true,
			DefaultAction: ScopeActionReview,
			Rules:         []ScopeRule{{Action: ScopeActionRecord, Method: "*", Host: host, PathPrefix: "/"}},
		}
		if _, err := NormalizeScopeRules(rules); err == nil {
			t.Fatalf("host %q: expected an error", host)
		}
	}
}

// The fence mode is derived, never stored: each manifest shape must classify
// into exactly one mode, and nil stays "unfenced".
func TestFenceModeOf(t *testing.T) {
	cases := []struct {
		name  string
		rules *ScopeRules
		want  string
	}{
		{"nil", nil, FenceUnfenced},
		{"disabled draft", &ScopeRules{Enabled: false, DefaultAction: ScopeActionRecord}, FenceDraft},
		{"shadow", &ScopeRules{Enabled: true, Shadow: true, DefaultAction: ScopeActionRecord}, FenceShadow},
		{"enforced", &ScopeRules{Enabled: true, DefaultAction: ScopeActionBlock}, FenceEnforced},
	}
	for _, tc := range cases {
		if got := FenceModeOf(tc.rules); got != tc.want {
			t.Fatalf("%s: FenceModeOf = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Suffix-form and wildcard hosts, "*" methods and an omitted path prefix (the
// operator leaves the cell blank) are the intended shapes and must normalize.
func TestScopeRulesNormalization(t *testing.T) {
	got, err := NormalizeScopeRules(ScopeRules{
		Enabled:       true,
		DefaultAction: "REVIEW",
		Shadow:        true,
		Rules: []ScopeRule{
			{Action: " record ", Method: " get ", Host: "Target-Corp.Example", PathPrefix: ""},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	rule := got.Rules[0]
	if rule.Action != ScopeActionRecord || rule.Method != "GET" || rule.Host != "target-corp.example" || rule.PathPrefix != "/" {
		t.Fatalf("normalized rule = %+v", rule)
	}
	if got.DefaultAction != ScopeActionReview || !got.Shadow {
		t.Fatalf("normalized set = %+v", got)
	}
}
