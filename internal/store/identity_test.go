package store

import (
	"regexp"
	"strings"
	"testing"
)

func TestLedgerIdentityHas35BitRandomPayload(t *testing.T) {
	seen := map[string]bool{}
	pattern := regexp.MustCompile(`^f_[a-z2-7]{7}$`)
	for i := 0; i < 1000; i++ {
		id := NewID("f")
		if !pattern.MatchString(id) || len(strings.TrimPrefix(id, "f_")) != 7 || seen[id] {
			t.Fatalf("weak or duplicate identity: %s", id)
		}
		seen[id] = true
	}
}

// NewCode is the shared generator behind both identifier families: it must hand
// back exactly n base32 characters, never a prefix of a different width.
func TestNewCodeReturnsRequestedWidth(t *testing.T) {
	for _, n := range []int{6, 7, 12} {
		if got := NewCode(n); len(got) != n {
			t.Fatalf("NewCode(%d) = %q, want %d characters", n, got, n)
		}
	}
	if NewCode(0) != "" || NewCode(-1) != "" {
		t.Fatal("NewCode with a non-positive width must be empty")
	}
}
