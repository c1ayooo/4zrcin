package project

import "testing"

// JudgeHost is the console-side read of the fence's own host grammar: the
// import preview must not disagree with what the node will allow.
func TestJudgeHostGrammar(t *testing.T) {
	rules := ScopeRules{Enabled: true, DefaultAction: ScopeActionReview, Rules: []ScopeRule{
		{ID: "sr-block", Action: ScopeActionBlock, Method: "*", Host: "bad.example", PathPrefix: "/"},
		{ID: "sr-domain", Action: ScopeActionRecord, Method: "*", Host: "example.com", PathPrefix: "/"},
		{ID: "sr-ip", Action: ScopeActionRecord, Method: "*", Host: "10.1.2.3", PathPrefix: "/"},
		{ID: "sr-net", Action: ScopeActionRecord, Method: "*", Host: "10.0.0.0/8", PathPrefix: "/"},
		{ID: "sr-all", Action: ScopeActionRecord, Method: "*", Host: "*", PathPrefix: "/"},
	}}
	cases := []struct {
		host   string
		action string
		rule   string
	}{
		{"example.com", ScopeActionRecord, "sr-domain"},
		{"A.Example.com", ScopeActionRecord, "sr-domain"},
		{"notexample.com", ScopeActionRecord, "sr-all"},
		{"10.1.2.3", ScopeActionRecord, "sr-ip"},
		{"10.9.9.9", ScopeActionRecord, "sr-net"},
		{"::ffff:10.2.3.4", ScopeActionRecord, "sr-net"},
		{"bad.example", ScopeActionBlock, "sr-block"}, // first hit wins
		{"sub.bad.example", ScopeActionBlock, "sr-block"},
	}
	for _, tc := range cases {
		v := rules.JudgeHost(tc.host)
		if !v.Matched || v.Action != tc.action || v.RuleID != tc.rule {
			t.Fatalf("%s = %+v, want %s/%s", tc.host, v, tc.action, tc.rule)
		}
	}
}

// With no rows, nothing matches; with a disabled set, nothing is judged at
// all (the caller must say 无法判定, not out-of-range).
func TestJudgeHostUnmatchedAndDisabled(t *testing.T) {
	empty := ScopeRules{Enabled: true, DefaultAction: ScopeActionReview}
	if v := empty.JudgeHost("example.com"); v.Matched {
		t.Fatalf("empty table matched: %+v", v)
	}
	disabled := ScopeRules{Enabled: false, Rules: []ScopeRule{{ID: "sr-1", Action: ScopeActionRecord, Method: "*", Host: "*", PathPrefix: "/"}}}
	if v := disabled.JudgeHost("example.com"); v.Enabled || v.Matched {
		t.Fatalf("disabled table judged: %+v", v)
	}
	if v := empty.JudgeHost(""); v.Matched {
		t.Fatalf("empty host matched: %+v", v)
	}
}

func TestScopeRuleText(t *testing.T) {
	rule := ScopeRule{Action: ScopeActionRecord, Method: "GET", Host: "example.com", PathPrefix: "/"}
	if got := rule.Text(); got != "record GET example.com /" {
		t.Fatalf("text = %q", got)
	}
}
