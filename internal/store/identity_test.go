package store

import (
	"regexp"
	"strings"
	"testing"
)

func TestLedgerIdentityHas50BitRandomPayload(t *testing.T) {
	seen := map[string]bool{}
	pattern := regexp.MustCompile(`^f_[a-z2-7]{10}$`)
	for i := 0; i < 1000; i++ {
		id := NewID("f")
		if !pattern.MatchString(id) || len(strings.TrimPrefix(id, "f_")) != 10 || seen[id] {
			t.Fatalf("weak or duplicate identity: %s", id)
		}
		seen[id] = true
	}
}
