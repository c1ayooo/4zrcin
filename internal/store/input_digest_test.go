package store

import (
	"strings"
	"testing"

	"github.com/kksty/tianwu/internal/model"
)

// testDigest is a well-formed SHA-256 for fixtures that need a preserved
// original. Dispatch now refuses a registered row without one, so a fixture about
// freezing versions (not about legacy rows) has to carry it.
const testDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// A registered original without a digest cannot be re-proven, so dispatch must
// refuse it. Before this rule the gate only asked whether the evidence row id
// existed: readiness said 「原件缺失或没有保全摘要」 while the direction was still
// dispatched — the page and the gate disagreed about the same row.
//
// Reviews are off in this fixture on purpose: with them on, the missing verdict
// would refuse the premise first and hide the evidence rule.
func TestPrepareRunInputRefusesRegisteredEvidenceWithoutDigest(t *testing.T) {
	s := open(t)
	empty, err := s.AddEvidence(model.Evidence{URI: "legacy/proof.html"})
	if err != nil {
		t.Fatal(err)
	}
	if empty.Digest != "" {
		t.Fatalf("fixture: digest = %q, want an empty legacy row", empty.Digest)
	}
	fact, err := s.AddFact(model.Fact{Kind: "vuln", Title: "越权", EvidenceIDs: []string{empty.ID}})
	if err != nil {
		t.Fatal(err)
	}
	in, err := s.CreateIntent(model.Intent{Title: "顺着越权打下去", FromFactIDs: []string{fact.ID}})
	if err != nil {
		t.Fatal(err)
	}
	_, run, err := s.StartIntentRun(in.ID, "local", "", model.Run{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareRunInput(run.ID, in, model.PremiseVerifiedOnly, true); err == nil {
		t.Fatal("dispatch admitted a premise whose registered original has no digest")
	} else if !strings.Contains(err.Error(), "保全摘要") {
		t.Fatalf("refusal does not name the digest rule: %v", err)
	}
}

// The control: the same shape with a digest is admitted, so the new rule rejects
// the empty digest and not merely "any evidence-backed premise".
func TestPrepareRunInputAdmitsRegisteredEvidenceWithDigest(t *testing.T) {
	s := open(t)
	kept, err := s.AddEvidence(model.Evidence{
		URI:    "legacy/proof.html",
		Digest: strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	fact, err := s.AddFact(model.Fact{Kind: "vuln", Title: "越权", EvidenceIDs: []string{kept.ID}})
	if err != nil {
		t.Fatal(err)
	}
	in, err := s.CreateIntent(model.Intent{Title: "顺着越权打下去", FromFactIDs: []string{fact.ID}})
	if err != nil {
		t.Fatal(err)
	}
	_, run, err := s.StartIntentRun(in.ID, "local", "", model.Run{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareRunInput(run.ID, in, model.PremiseVerifiedOnly, true); err != nil {
		t.Fatalf("a premise with a digest was refused: %v", err)
	}
}
