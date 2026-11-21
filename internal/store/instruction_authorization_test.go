package store

import (
	"testing"

	"github.com/kksty/tianwu/internal/collab"
	"github.com/kksty/tianwu/internal/model"
)

func authorizedFixture(t *testing.T, s *Store, target, key string, authorization bool) model.Instruction {
	t.Helper()
	in, _, err := s.CreateInstruction(model.Instruction{
		CreatedBy: collab.OperatorID, IntentID: target, Text: "指令 " + key,
		Authorization: authorization, IdempotencyKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	return in
}

// Only marked instructions are permissions. The projection keeps the same scope
// rules as the guidance list and drops revoked entries, so a 授权来源 section can
// render it directly instead of re-deriving "which of these is a boundary".
func TestAuthorizedInstructionsAreTheMarkedSubset(t *testing.T) {
	s := open(t)
	parent, _ := s.CreateIntent(model.Intent{Title: "parent"})
	child, _, err := s.CreateSpawnIntent(model.Intent{Title: "child", ParentID: parent.ID, CreatedBy: "commander", IdempotencyKey: "child"}, "")
	if err != nil {
		t.Fatal(err)
	}
	other, _ := s.CreateIntent(model.Intent{Title: "other"})
	globalAuth := authorizedFixture(t, s, "", "global-auth", true)
	globalPlain := authorizedFixture(t, s, "", "global-plain", false)
	localAuth := authorizedFixture(t, s, parent.ID, "local-auth", true)

	db := s.Snapshot()
	got := AuthorizedInstructions(db, child.ID, false)
	if len(got) != 2 || got[0].ID != globalAuth.ID || got[1].ID != localAuth.ID {
		t.Fatalf("child permissions = %+v", got)
	}
	if got := AuthorizedInstructions(db, other.ID, false); len(got) != 1 || got[0].ID != globalAuth.ID {
		t.Fatalf("other permissions = %+v", got)
	}
	// The commander sees every direction's permissions, and nothing else.
	if got := AuthorizedInstructions(db, "", true); len(got) != 2 {
		t.Fatalf("commander permissions = %+v", got)
	}
	for _, in := range AuthorizedInstructions(db, child.ID, false) {
		if in.ID == globalPlain.ID {
			t.Fatal("a plain instruction appeared as a permission")
		}
	}
	// Revoking removes it from the permission list, not just from guidance.
	if _, err := s.UpdateInstruction(localAuth.ID, 1, localAuth.Text, model.InstructionRevoked); err != nil {
		t.Fatal(err)
	}
	if got := AuthorizedInstructions(s.Snapshot(), child.ID, false); len(got) != 1 || got[0].ID != globalAuth.ID {
		t.Fatalf("revoked permission still listed: %+v", got)
	}
}
