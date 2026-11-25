package store

import (
	"path/filepath"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/model"
)

// A snapshot is a frozen read: mutating the slices inside a snapshot message
// must never reach the live ledger. Writers replace rows wholesale today, so
// the shared backing arrays were latent aliasing; detachMessage makes the
// contract structural (audit item N4).
func TestSnapshotMessagesDoNotShareSlicesWithLiveStore(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "ledger.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, _, err := st.AddMessage(model.Message{
		ProjectID: "p", FromParticipantID: "commander", ToParticipantID: "pt_a",
		Kind: "update", Text: "原文", Status: model.MessageQueued,
		FactRefs: []string{"f1", "f2"}, Chain: []string{"c1"}, DirectiveRecipients: []string{"d1"},
	}); err != nil {
		t.Fatal(err)
	}

	snap := st.Snapshot()
	if len(snap.Messages) != 1 {
		t.Fatalf("snapshot messages = %d", len(snap.Messages))
	}
	m := snap.Messages[0]
	m.FactRefs[0] = "mutated"
	m.FactRefs = append(m.FactRefs, "extra")
	m.Chain[0] = "mutated-chain"
	m.DirectiveRecipients[0] = "mutated-recipient"

	live := st.Messages(100)
	if len(live) != 1 {
		t.Fatalf("live messages = %d", len(live))
	}
	lm := live[0]
	if lm.FactRefs[0] != "f1" || len(lm.FactRefs) != 2 || lm.FactRefs[len(lm.FactRefs)-1] == "extra" {
		t.Fatalf("snapshot mutation reached the live ledger FactRefs: %v", lm.FactRefs)
	}
	if lm.Chain[0] != "c1" {
		t.Fatalf("snapshot mutation reached the live ledger Chain: %v", lm.Chain)
	}
	if lm.DirectiveRecipients[0] != "d1" {
		t.Fatalf("snapshot mutation reached the live ledger DirectiveRecipients: %v", lm.DirectiveRecipients)
	}
	// A second snapshot must be clean too: the first snapshot's mutation must
	// not leak into anything the store keeps.
	again := st.Snapshot()
	if again.Messages[0].FactRefs[0] != "f1" || len(again.Messages[0].FactRefs) != 2 {
		t.Fatalf("store retained a mutated message slice: %v", again.Messages[0].FactRefs)
	}
}
