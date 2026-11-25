package verifier

import (
	"testing"

	"github.com/c1ayooo/4zrcin/internal/model"
	"github.com/c1ayooo/4zrcin/internal/store"
)

// The credential's id sets must cover exactly the reviewed fact plus the
// premises its direction registered and the evidence those rows cite (review
// F1). A premise outside the intent, or evidence of another direction's fact,
// must not leak in.
func TestReviewReadScopeCoversFactAndPremisesOnly(t *testing.T) {
	db := store.DB{}
	db.Facts = []model.Fact{
		{ID: "f_target", IntentID: "in_1", EvidenceIDs: []string{"ev_1"}},
		{ID: "f_premise", IntentID: "in_0", EvidenceIDs: []string{"ev_2"}},
		{ID: "f_other", IntentID: "in_2", EvidenceIDs: []string{"ev_3"}},
	}
	db.Intents = []model.Intent{
		{ID: "in_1", FromFactIDs: []string{"f_premise", "f_missing"}},
	}
	facts, evidence := reviewReadScope(db, db.Facts[0])
	wantFacts := map[string]bool{"f_target": true, "f_premise": true, "f_missing": true}
	if len(facts) != len(wantFacts) {
		t.Fatalf("fact scope = %v", facts)
	}
	for _, id := range facts {
		if !wantFacts[id] {
			t.Fatalf("fact scope carries %q outside the reviewed direction", id)
		}
	}
	wantEvidence := map[string]bool{"ev_1": true, "ev_2": true}
	for _, id := range evidence {
		if !wantEvidence[id] {
			t.Fatalf("evidence scope carries %q, which no allowed fact cites", id)
		}
	}
	if len(evidence) != len(wantEvidence) {
		t.Fatalf("evidence scope = %v", evidence)
	}
	// An intent that is not in the ledger must not panic; the set is the fact
	// alone.
	facts, evidence = reviewReadScope(db, model.Fact{ID: "f_orphan", IntentID: "in_none", EvidenceIDs: []string{"ev_9"}})
	if len(facts) != 1 || facts[0] != "f_orphan" || len(evidence) != 1 || evidence[0] != "ev_9" {
		t.Fatalf("orphan intent scope = %v / %v", facts, evidence)
	}
}
