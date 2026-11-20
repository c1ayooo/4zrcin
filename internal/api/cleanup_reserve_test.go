package api

import (
	"testing"
	"time"
)

// The evidence-writing tail must stay a meaningful share of the run budget at
// both ends: a 60m drill used to get only 90s (2.5%), while a 5m run got 75s.
func TestCleanupReserveScalesWithTheBudget(t *testing.T) {
	cases := []struct {
		name      string
		remaining time.Duration
		want      time.Duration
	}{
		{"60m drill", 60 * time.Minute, 300 * time.Second},
		{"90m deep chain", 90 * time.Minute, 300 * time.Second},
		{"30m", 30 * time.Minute, 180 * time.Second},
		{"15m unchanged", 15 * time.Minute, 90 * time.Second},
		{"10m", 10 * time.Minute, 60 * time.Second},
		{"5m", 5 * time.Minute, 60 * time.Second},
		{"2m still keeps half", 2 * time.Minute, 60 * time.Second},
		{"no budget no window", 0, 0},
		{"90s keeps half for work", 90 * time.Second, 45 * time.Second},
		{"almost expired", time.Second, 500 * time.Millisecond},
		{"expired", -time.Minute, -30 * time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := cleanupReserve(c.remaining); got != c.want {
				t.Fatalf("cleanupReserve(%s) = %s, want %s", c.remaining, got, c.want)
			}
		})
	}
}
