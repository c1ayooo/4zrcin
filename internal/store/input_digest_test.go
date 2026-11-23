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

// The gate refuses by shape, not by presence: a digest that is not a SHA-256 hex
// string cannot be re-proven either, and only the predicate-level table covered
// those shapes. The rows are written straight into the in-memory ledger on
// purpose — the write path (normalizeEvidence) would refuse them, so the only
// rows like these come from a hand edit or an import that bypassed it.
//
// Case is folded, so an upper-case or padded digest is admitted: preserve.go's
// compareDigest and verifyFileDigest both fold case, and refusing here would
// refuse a row the reader can verify perfectly well.
func TestPrepareRunInputJudgesDigestShape(t *testing.T) {
	cases := []struct {
		name   string
		digest string
		admit  bool
	}{
		{"lower-case sha256", testDigest, true},
		{"upper-case sha256", strings.ToUpper(testDigest), true},
		{"padded sha256", "  " + testDigest + "\n", true},
		{"non-hex character", "g" + strings.Repeat("a", 63), false},
		{"one byte short", strings.Repeat("a", 62), false},
		{"one byte long", strings.Repeat("a", 65), false},
		{"internal space", strings.Repeat("a", 32) + " " + strings.Repeat("a", 31), false},
		{"prefix instead of hex", "sha256:ok", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := open(t)
			kept, err := s.AddEvidence(model.Evidence{URI: "legacy/proof.html", Digest: testDigest})
			if err != nil {
				t.Fatal(err)
			}
			edited := false
			for i := range s.db.Evidence {
				if s.db.Evidence[i].ID == kept.ID {
					s.db.Evidence[i].Digest = tc.digest
					edited = true
				}
			}
			if !edited {
				t.Fatalf("fixture: evidence %s not found in the in-memory ledger", kept.ID)
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
			_, err = s.PrepareRunInput(run.ID, in, model.PremiseVerifiedOnly, true)
			if tc.admit {
				if err != nil {
					t.Fatalf("dispatch refused a re-provable digest %q: %v", tc.digest, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("dispatch admitted a digest that cannot be re-proven: %q", tc.digest)
			}
			if !strings.Contains(err.Error(), "保全摘要") {
				t.Fatalf("refusal does not name the digest rule: %v", err)
			}
		})
	}
}
