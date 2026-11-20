package store

// Dependency propagation is the one inference the ledger makes about
// provenance. These tests pin the four judgements the specification names, in
// both directions, so a later "simplification" cannot quietly drop the
// inheritance or turn a lost premise into a rejection.

import (
	"strings"
	"testing"

	"github.com/kksty/tianwu/internal/model"
)

// observedPremise writes a fact with no direction behind it, so its confidence
// is exactly what the writer declared, and returns it.
func observedPremise(t *testing.T, s *Store, title string) model.Fact {
	t.Helper()
	f, err := s.AddFact(model.Fact{Kind: "host", Title: title})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func verifiedFact(t *testing.T, s *Store, title string) model.Fact {
	t.Helper()
	f := observedPremise(t, s, title)
	if _, err := s.ReviewFact(model.Review{FactID: f.ID, Verdict: "pass", VerifierRun: "run-test"}); err != nil {
		t.Fatal(err)
	}
	return f
}

func direction(t *testing.T, s *Store, title string, premises ...string) model.Intent {
	t.Helper()
	in, err := s.CreateIntent(model.Intent{Title: title, FromFactIDs: premises})
	if err != nil {
		t.Fatal(err)
	}
	return in
}

// S2: a fact cannot look more certain than the direction's premises.
func TestFactConfidenceInheritsUnverifiedPremise(t *testing.T) {
	s := open(t)
	premise := observedPremise(t, s, "10.0.0.5:8443")
	in := direction(t, s, "probe behind the premise", premise.ID)

	f, err := s.AddFact(model.Fact{Kind: "vuln", IntentID: in.ID, Title: "derived", Confidence: "observed"})
	if err != nil {
		t.Fatal(err)
	}
	if f.Confidence != "inferred" {
		t.Fatalf("confidence = %q, want inferred: an unverified premise cannot produce an observed claim", f.Confidence)
	}
}

// S2 false sample: with every premise verified, the declared value stands.
func TestFactConfidenceKeepsDeclaredValueWhenPremisesVerified(t *testing.T) {
	s := open(t)
	premise := verifiedFact(t, s, "10.0.0.5:8443")
	in := direction(t, s, "probe behind verified premise", premise.ID)

	for _, declared := range []string{"observed", "inferred", ""} {
		f, err := s.AddFact(model.Fact{Kind: "vuln", IntentID: in.ID, Title: "derived " + declared, Confidence: declared})
		if err != nil {
			t.Fatal(err)
		}
		want := declared
		if want == "" {
			want = "observed"
		}
		if f.Confidence != want {
			t.Fatalf("declared %q: confidence = %q, want %q", declared, f.Confidence, want)
		}
	}
}

// S2 keeps holding on later writes: without the recompute, the next content
// edit would drift an inferred row back to observed.
func TestFactConfidenceIsRecomputedOnUpdate(t *testing.T) {
	s := open(t)
	premise := observedPremise(t, s, "10.0.0.5:8443")
	in := direction(t, s, "probe behind the premise", premise.ID)
	f, err := s.AddFact(model.Fact{Kind: "vuln", IntentID: in.ID, Title: "derived", Confidence: "observed"})
	if err != nil {
		t.Fatal(err)
	}

	updated, err := s.UpdateFact(f.ID, func(row *model.Fact) {
		row.Summary = "corrected wording"
		row.Confidence = "observed"
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Confidence != "inferred" {
		t.Fatalf("confidence = %q after update, want inferred", updated.Confidence)
	}
}

// A bookkeeping-only write stays inert: renaming or re-attributing a row must
// not bump its version, drop a review, or move its confidence. Only an actual
// assertion change re-derives inheritance.
func TestBookkeepingWriteDoesNotRederiveInheritance(t *testing.T) {
	s := open(t)
	premise := observedPremise(t, s, "10.0.0.5:8443")
	in := direction(t, s, "probe behind the premise", premise.ID)
	f, err := s.AddFact(model.Fact{Kind: "vuln", IntentID: in.ID, Title: "derived"})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := s.UpdateFact(f.ID, func(row *model.Fact) { row.SourceRunID = "run-late-attribution" })
	if err != nil {
		t.Fatal(err)
	}
	if updated.ClaimVersion != f.ClaimVersion || updated.Status != f.Status {
		t.Fatalf("bookkeeping write changed the claim: version %d→%d status %s→%s",
			f.ClaimVersion, updated.ClaimVersion, f.Status, updated.Status)
	}
}

// S3: withdrawing a verified premise removes its endorsement, not the
// downstream conclusion's authorship.
func TestWithdrawPropagatesToVerifiedDownstream(t *testing.T) {
	s := open(t)
	premise := verifiedFact(t, s, "10.0.0.5:8443")
	in := direction(t, s, "derived direction", premise.ID)
	down, err := s.AddFact(model.Fact{Kind: "vuln", IntentID: in.ID, Title: "downstream"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReviewFact(model.Review{FactID: down.ID, Verdict: "pass", VerifierRun: "run-down"}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.WithdrawFact(premise.ID, "pt_operator", "", "前提不成立"); err != nil {
		t.Fatal(err)
	}
	got, ok := s.FactByID(down.ID)
	if !ok {
		t.Fatal("downstream fact disappeared")
	}
	if got.Status != "unverified" {
		t.Fatalf("downstream status = %q, want unverified", got.Status)
	}

	var reason string
	for _, c := range s.History("fact", down.ID) {
		if c.Reason != "" {
			reason = c.Reason
		}
	}
	if reason != premiseLostReason+premise.ID {
		t.Fatalf("journal reason = %q, want %q", reason, premiseLostReason+premise.ID)
	}
}

// S3 false sample: a row that never carried an endorsement is left exactly as
// it was — no version, no journal line, no status change.
func TestWithdrawLeavesUnverifiedDownstreamAlone(t *testing.T) {
	s := open(t)
	premise := verifiedFact(t, s, "10.0.0.5:8443")
	in := direction(t, s, "derived direction", premise.ID)
	down, err := s.AddFact(model.Fact{Kind: "vuln", IntentID: in.ID, Title: "downstream"})
	if err != nil {
		t.Fatal(err)
	}
	before := len(s.History("fact", down.ID))

	if _, err := s.WithdrawFact(premise.ID, "pt_operator", "", "前提不成立"); err != nil {
		t.Fatal(err)
	}
	if after := len(s.History("fact", down.ID)); after != before {
		t.Fatalf("unverified downstream gained %d journal entries, want 0", after-before)
	}
	if got, _ := s.FactByID(down.ID); got.Status != "unverified" || got.ClaimVersion != down.ClaimVersion {
		t.Fatalf("row changed: status=%q version=%d", got.Status, got.ClaimVersion)
	}
}

// S3 reaches the whole chain, and never turns "not backed" into "false".
func TestPremiseLossReachesTransitiveDownstreamWithoutRejecting(t *testing.T) {
	s := open(t)
	root := verifiedFact(t, s, "root premise")
	first := direction(t, s, "first hop", root.ID)
	middle, err := s.AddFact(model.Fact{Kind: "vuln", IntentID: first.ID, Title: "middle"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReviewFact(model.Review{FactID: middle.ID, Verdict: "pass", VerifierRun: "run-middle"}); err != nil {
		t.Fatal(err)
	}
	second := direction(t, s, "second hop", middle.ID)
	leaf, err := s.AddFact(model.Fact{Kind: "vuln", IntentID: second.ID, Title: "leaf"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReviewFact(model.Review{FactID: leaf.ID, Verdict: "pass", VerifierRun: "run-leaf"}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.WithdrawFact(root.ID, "pt_operator", "", "前提不成立"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{middle.ID, leaf.ID} {
		got, _ := s.FactByID(id)
		if got.Status != "unverified" {
			t.Fatalf("%s status = %q, want unverified", id, got.Status)
		}
	}
}

// S3 also hangs off a rejection: the verifier saying "this does not reproduce"
// pulls the backing the same way a withdrawal does.
func TestRejectPropagatesToVerifiedDownstream(t *testing.T) {
	s := open(t)
	premise := verifiedFact(t, s, "premise")
	in := direction(t, s, "derived direction", premise.ID)
	down, err := s.AddFact(model.Fact{Kind: "vuln", IntentID: in.ID, Title: "downstream"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReviewFact(model.Review{FactID: down.ID, Verdict: "pass", VerifierRun: "run-down"}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ReviewFact(model.Review{FactID: premise.ID, Verdict: "reject", Notes: "重跑不成立"}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.FactByID(down.ID)
	if got.Status != "unverified" {
		t.Fatalf("downstream status = %q, want unverified", got.Status)
	}
	if rejected, _ := s.FactByID(premise.ID); rejected.Status != "rejected" {
		t.Fatalf("premise status = %q, want rejected", rejected.Status)
	}
}

// S4: a pass proves the assertion, and the verdict stands even when the chain
// above is unverified — refusing it would deadlock downstream behind an
// upstream nobody re-reviews. What must not happen is the claim getting more
// certain than its premises, or the gap going unrecorded.
func TestPassKeepsInferredAndRecordsThePremiseGap(t *testing.T) {
	s := open(t)
	premise := observedPremise(t, s, "10.0.0.5:8443")
	in := direction(t, s, "derived direction", premise.ID)
	f, err := s.AddFact(model.Fact{Kind: "vuln", IntentID: in.ID, Title: "downstream"})
	if err != nil {
		t.Fatal(err)
	}

	review, err := s.ReviewFact(model.Review{FactID: f.ID, Verdict: "pass", VerifierRun: "run-review"})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.FactByID(f.ID)
	if got.Status != "verified" {
		t.Fatalf("status = %q, want verified: a pass is a verdict, not a permission", got.Status)
	}
	if got.Confidence != "inferred" {
		t.Fatalf("confidence = %q, want inferred", got.Confidence)
	}
	if !strings.Contains(review.Notes, "premise_unverified:"+premise.ID) {
		t.Fatalf("review notes = %q, want the premise gap named", review.Notes)
	}
}

// S4 must not touch a row whose premises are verified.
func TestPassWithVerifiedPremisesLeavesConfidenceAlone(t *testing.T) {
	s := open(t)
	premise := verifiedFact(t, s, "premise")
	in := direction(t, s, "derived direction", premise.ID)
	f, err := s.AddFact(model.Fact{Kind: "vuln", IntentID: in.ID, Title: "downstream"})
	if err != nil {
		t.Fatal(err)
	}

	review, err := s.ReviewFact(model.Review{FactID: f.ID, Verdict: "pass", VerifierRun: "run-review"})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.FactByID(f.ID)
	if got.Status != "verified" || got.Confidence != "observed" {
		t.Fatalf("status=%q confidence=%q, want verified/observed", got.Status, got.Confidence)
	}
	if strings.Contains(review.Notes, "premise_unverified") {
		t.Fatalf("review notes = %q, want no premise gap", review.Notes)
	}
}
