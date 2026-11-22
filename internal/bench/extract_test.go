package bench

import (
	"regexp"
	"testing"
)

// The default pattern is a default: a text with no flag yields nothing, and the
// caller is expected to report that rather than call the challenge solved.
func TestExtractFlagsDedupesAndReportsNothing(t *testing.T) {
	re := regexp.MustCompile(DefaultFlagPattern)
	got := ExtractFlags("found flag{a} and flag{b}, repeat flag{a}", re)
	if len(got) != 2 || got[0] != "flag{a}" || got[1] != "flag{b}" {
		t.Fatalf("extracted %v, want [flag{a} flag{b}]", got)
	}
	if got := ExtractFlags("no flags here, not even flag{", re); len(got) != 0 {
		t.Fatalf("extracted %v from a text without a complete flag", got)
	}
	// A different platform shape is handled by passing a different pattern; the
	// function must not have its own opinion about the format.
	other := regexp.MustCompile(`TSEC-[A-Z0-9]{8}`)
	if got := ExtractFlags("the answer is TSEC-ABCD1234", other); len(got) != 1 || got[0] != "TSEC-ABCD1234" {
		t.Fatalf("custom pattern extracted %v", got)
	}
}

// A fact is attributed to a challenge by the address it names. Guessing would
// submit a flag against the wrong challenge and burn an attempt there.
func TestResolveChallengeMatchesByAddressOrHost(t *testing.T) {
	active := []Challenge{
		{UniqueCode: "web_sql_injection_01", ContainerAddr: []string{"10.0.1.5:8080"}},
		{UniqueCode: "pwn_stack_03", ContainerAddr: []string{"10.0.2.7:9000", "10.0.2.8:9000"}},
	}
	cases := []struct {
		entity string
		want   string
		ok     bool
	}{
		{entity: "10.0.1.5:8080", want: "web_sql_injection_01", ok: true},
		{entity: "10.0.1.5", want: "web_sql_injection_01", ok: true},
		{entity: "10.0.2.8:9000", want: "pwn_stack_03", ok: true},
		{entity: "10.0.9.9:8080", ok: false},
		{entity: "", ok: false},
	}
	for _, tc := range cases {
		got, ok := ResolveChallenge(tc.entity, active)
		if ok != tc.ok || got != tc.want {
			t.Errorf("ResolveChallenge(%q) = %q,%v want %q,%v", tc.entity, got, ok, tc.want, tc.ok)
		}
	}
}
