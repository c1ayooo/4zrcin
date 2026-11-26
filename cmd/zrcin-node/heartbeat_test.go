package main

import (
	"testing"
	"time"
)

// The backoff is the node's polite face during a platform outage: the first
// beats retry at the heartbeat interval, then double up to a 5m ceiling, and
// the jitter stays inside ±20% so a fleet does not retry in lockstep.
func TestHeartbeatBackoffBounds(t *testing.T) {
	// The jitter adds at most +10%: the first beats stay near the 30s tick.
	if d := heartbeatBackoff(0); d > 33*time.Second {
		t.Fatalf("first backoff = %s, want within the jittered 30s", d)
	}
	if d := heartbeatBackoff(3); d < 30*time.Second || d > 3*time.Minute {
		t.Fatalf("backoff(3) = %s, want the doubled range", d)
	}
	for beat := 8; beat < 12; beat++ {
		if d := heartbeatBackoff(beat); d > 5*time.Minute+30*time.Second {
			t.Fatalf("backoff(%d) = %s, want near the 5m ceiling", beat, d)
		}
	}
	// The jitter never pushes the delay negative or past the ceiling +20%.
	for beat := 1; beat <= 12; beat++ {
		if d := heartbeatBackoff(beat); d < 0 {
			t.Fatalf("backoff(%d) = %s, negative", beat, d)
		}
	}
}
