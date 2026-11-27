package traffic

// The provenance aggregation (待审审批): refused and passive records for one
// host read as a decision context — counts, window, URL shapes, probe flags —
// from the same .http index the traffic view reads. Nothing is invented: a
// host with no records gets no entry at all, and resolution failures leave
// SameOrigin unset, never guessed.

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeProvCapture(t *testing.T, dir, ref, url, runID, intentID, action, ruleID string, missed, passive bool, at time.Time) {
	t.Helper()
	c := Capture{
		Ref: ref, RunID: runID, IntentID: intentID, FlowID: ref,
		Method: "GET", URL: url, At: at,
		GuardrailAction: action, GuardrailRuleID: ruleID,
		GuardrailDefaultMissed: missed, GuardrailPassive: passive,
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".http", ref+".json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestProvenanceFor(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".http"), 0o700); err != nil {
		t.Fatal(err)
	}
	base := time.Unix(1700000000, 0)
	writeProvCapture(t, dir, "a1", "https://target.example/admin", "r_1", "in_a", "review", "", true, false, base)
	writeProvCapture(t, dir, "a2", "https://target.example/api/users", "r_1", "in_a", "review", "", true, false, base.Add(3*time.Minute))
	writeProvCapture(t, dir, "a3", "https://target.example/app.js", "r_2", "in_b", "record", "sr-1", false, true, base.Add(5*time.Minute))
	writeProvCapture(t, dir, "b1", "https://other.example/x", "r_9", "in_z", "review", "", true, false, base)

	prov := ProvenanceFor(dir, []string{"target.example"}, []string{"tpd.visionfin.cn"})
	p, ok := prov["target.example"]
	if !ok {
		t.Fatal("no provenance for the pending host")
	}
	if p.RefusedCount != 2 || p.PassiveCount != 1 {
		t.Fatalf("counts = refused %d / passive %d", p.RefusedCount, p.PassiveCount)
	}
	if p.FirstRefusedAt.IsZero() || !p.LastRefusedAt.Equal(base.Add(3*time.Minute)) {
		t.Fatalf("window = %v .. %v", p.FirstRefusedAt, p.LastRefusedAt)
	}
	if len(p.RefusedURLs) != 2 || p.RefusedURLs[0] != "https://target.example/admin" {
		t.Fatalf("urls = %+v", p.RefusedURLs)
	}
	if len(p.IntentIDs) != 1 || p.IntentIDs[0] != "in_a" {
		t.Fatalf("intent ids = %+v", p.IntentIDs)
	}
	if p.ProbeNote != "" {
		t.Fatalf("ordinary host marked probe: %q", p.ProbeNote)
	}
	if _, ok := prov["other.example"]; ok {
		t.Fatal("unrequested host got provenance")
	}
}

func TestProbeNoteFor(t *testing.T) {
	if note := probeNoteFor("foo"); note == "" {
		t.Fatal("single label not classified")
	}
	if note := probeNoteFor("114.111.27.202.evil.com"); note == "" {
		t.Fatal("IP-prefixed name not classified")
	}
	for _, ordinary := range []string{"tpd.visionfin.cn", "in-range.example", "10.0.0.8"} {
		if note := probeNoteFor(ordinary); note != "" {
			t.Fatalf("ordinary host %q marked: %q", ordinary, note)
		}
	}
}

// Same-origin: a pending host that resolves to an address an in-range host
// also resolves to is flagged with that host's name — the strongest approve
// signal there is. The DNS seam is stubbed so the test exercises the
// comparison, not the resolver.
func TestProvenanceSameOrigin(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".http"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeProvCapture(t, dir, "c1", "https://sibling.example/x", "r_1", "in_a", "review", "", true, false, time.Unix(1700000000, 0))

	orig := provResLookup
	resetProvenanceResolution()
	provResLookup = func(ctx context.Context, host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("10.1.2.3")}, nil
	}
	t.Cleanup(func() {
		provResLookup = orig
		resetProvenanceResolution()
	})

	prov := ProvenanceFor(dir, []string{"sibling.example"}, []string{"tpd.visionfin.cn"})
	p, ok := prov["sibling.example"]
	if !ok {
		t.Fatal("no provenance")
	}
	if len(p.SameOrigin) != 1 || p.SameOrigin[0] != "tpd.visionfin.cn" {
		t.Fatalf("same_origin = %+v", p.SameOrigin)
	}
	if len(p.ResolvedIPs) == 0 || p.ResolvedIPs[0] != "10.1.2.3" {
		t.Fatalf("resolved_ips = %+v", p.ResolvedIPs)
	}
}
