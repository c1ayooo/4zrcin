package api

import (
	"testing"

	"github.com/kksty/tianwu/internal/model"
)

// The page and the dispatch gate must answer the same question with the same
// rule: a registered original without a digest is a broken original, so it is
// "missing" here and refused by store.PrepareRunInput (see
// internal/store/input_digest_test.go). The bug this pins: readiness blocked the
// direction while dispatch only checked that the evidence id existed.
func TestReadinessTreatsRegisteredEvidenceWithoutDigestAsMissing(t *testing.T) {
	s, p, _, _ := proofPolicyFixture(t)
	s.SetReviewsDisabled(true)
	legacy, err := p.Store.AddEvidence(model.Evidence{URI: "legacy/proof.html"})
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Digest != "" {
		t.Fatalf("fixture: digest = %q, want an empty legacy row", legacy.Digest)
	}
	fact, err := p.Store.AddFact(model.Fact{Kind: "vuln", Title: "越权", EvidenceIDs: []string{legacy.ID}})
	if err != nil {
		t.Fatal(err)
	}
	in, err := p.Store.CreateIntent(model.Intent{Title: "顺着越权打下去", FromFactIDs: []string{fact.ID}})
	if err != nil {
		t.Fatal(err)
	}
	got := s.readiness(p.Store.Snapshot(), in, s.premisePolicy(), false)
	if got.Ready {
		t.Fatalf("a registered original without a digest was reported ready: %+v", got.Premises)
	}
	if len(got.Premises) != 1 || got.Premises[0].Integrity != "missing" || got.Premises[0].Usable {
		t.Fatalf("premise = %+v, want integrity=missing and unusable", got.Premises)
	}
}
