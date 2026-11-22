package bench

import (
	"strings"
	"testing"
)

// The scope is the union of what is available, not the newest start: with three
// containers alive, dropping the older addresses would forbid work already in
// flight. Duplicates collapse and the order is stable, so a poll that saw the
// same set twice writes the same text (and therefore no new revision).
func TestScopeTextIsTheSortedUnionOfAvailableAddresses(t *testing.T) {
	text, err := ScopeText([]string{"10.0.2.7:8080", "10.0.1.5:8080", " 10.0.2.7:8080 ", ""})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"10.0.1.5:8080", "10.0.2.7:8080"} {
		if strings.Count(text, want) != 1 {
			t.Fatalf("address %s appears %d times in:\n%s", want, strings.Count(text, want), text)
		}
	}
	if strings.Index(text, "10.0.1.5:8080") > strings.Index(text, "10.0.2.7:8080") {
		t.Fatalf("addresses are not sorted stably:\n%s", text)
	}
	if !strings.Contains(text, "不能长期绑定") {
		t.Fatalf("scope lost the lifetime caveat:\n%s", text)
	}

	again, err := ScopeText([]string{"10.0.1.5:8080", "10.0.2.7:8080"})
	if err != nil {
		t.Fatal(err)
	}
	if again != text {
		t.Fatalf("the same set rendered differently:\n%s\n---\n%s", text, again)
	}
}

// An empty set must stop the run, not clear the scope: "no addresses" is a lie
// in both readings, and a cleared document is the one way to turn a scope check
// into a rubber stamp.
func TestScopeTextRefusesAnEmptySet(t *testing.T) {
	for _, addrs := range [][]string{nil, {}, {"", "  "}} {
		text, err := ScopeText(addrs)
		if err == nil {
			t.Fatalf("ScopeText(%v) = %q, want an error", addrs, text)
		}
		if text != "" {
			t.Fatalf("ScopeText(%v) returned text %q alongside an error", addrs, text)
		}
	}
}

// A malformed address is not silently dropped: dropping it would shrink the
// scope behind the model's back, and it would be blamed for not reaching a
// target we removed.
func TestScopeTextRejectsAMalformedAddress(t *testing.T) {
	if _, err := ScopeText([]string{"10.0.1.5:8080", "not-an-address"}); err == nil {
		t.Fatal("a malformed address was accepted into the scope")
	}
}
