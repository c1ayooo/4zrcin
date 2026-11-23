package store

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/kksty/tianwu/internal/collab"
	"github.com/kksty/tianwu/internal/model"
)

func newSpawnStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Repeating the same idempotency key finds the same worker; changing the
// payload under that key is a conflict, not a second worker.
func TestCreateSpawnIntentIsIdempotent(t *testing.T) {
	st := newSpawnStore(t)
	req := model.Intent{
		Title: "独立核对", Description: "只读", IdempotencyKey: "spawn-1",
		CreatedBy: "pt_in_a", DelegatedByRun: "r_a",
	}
	first, isNew, err := st.CreateSpawnIntent(req, "")
	if err != nil || !isNew {
		t.Fatalf("first: new=%v err=%v", isNew, err)
	}
	second, isNew, err := st.CreateSpawnIntent(req, "")
	if err != nil || isNew || second.ID != first.ID {
		t.Fatalf("retry: new=%v id=%s want %s err=%v", isNew, second.ID, first.ID, err)
	}
	if n := len(st.ListIntents()); n != 1 {
		t.Fatalf("intents = %d, want 1", n)
	}
	conflict := req
	conflict.Title = "另一个任务"
	if _, _, err := st.CreateSpawnIntent(conflict, ""); !errors.Is(err, ErrSpawnIdempotencyConflict) {
		t.Fatalf("conflict err = %v", err)
	}
}

// A spawn under a parent direction inherits the operator constraints, skills
// and node default; it must not inherit the parent's model profile.
func TestCreateSpawnIntentInheritsParentContext(t *testing.T) {
	st := newSpawnStore(t)
	parent, err := st.CreateIntent(model.Intent{
		Title: "父意图", Doc: "操作者约束", Skills: []string{"collab"}, Node: "lab",
	})
	if err != nil {
		t.Fatal(err)
	}
	child, isNew, err := st.CreateSpawnIntent(model.Intent{
		Title: "子工作者", ParentID: parent.ID, CreatedBy: "pt_in_a",
		IdempotencyKey: "spawn-child",
	}, "")
	if err != nil || !isNew {
		t.Fatalf("spawn: new=%v err=%v", isNew, err)
	}
	if child.Doc != "操作者约束" || len(child.Skills) != 1 || child.Skills[0] != "collab" || child.Node != "lab" {
		t.Fatalf("child did not inherit parent context: %+v", child)
	}
	if child.Profile != "" {
		t.Fatalf("child inherited a profile: %q", child.Profile)
	}
	if child.ParentID != parent.ID || child.Status != model.StatusPending {
		t.Fatalf("child = %+v", child)
	}
}

// A parentless spawn is legal: not every worker is a child of an existing
// direction (the commander can start independent work).
func TestCreateSpawnIntentWithoutParent(t *testing.T) {
	st := newSpawnStore(t)
	worker, isNew, err := st.CreateSpawnIntent(model.Intent{
		Title: "独立工作者", CreatedBy: "pt_in_a", IdempotencyKey: "spawn-root",
	}, "")
	if err != nil || !isNew || worker.ParentID != "" {
		t.Fatalf("worker = %+v new=%v err=%v", worker, isNew, err)
	}
}

// Spawning under a finished parent is refused: the work would belong to a
// direction nobody is waiting for.
func TestCreateSpawnIntentRefusesFinishedParent(t *testing.T) {
	st := newSpawnStore(t)
	parent, err := st.CreateIntent(model.Intent{Title: "结束后"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateIntent(parent.ID, func(i *model.Intent) { i.Status = model.StatusDone }); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CreateSpawnIntent(model.Intent{
		Title: "晚了", ParentID: parent.ID, CreatedBy: "pt_in_a",
	}, ""); err == nil {
		t.Fatal("spawn under a finished parent was accepted")
	}
}

// A peer publish (no ParentID) inherits Doc and Node from the publishing
// direction and appends its skills to the source's, keeping the inherited order.
// The same idempotency key retried with only its own additions matches the
// stored union instead of looking like a changed payload.
func TestCreateSpawnIntentPeerPublishInheritsSourceContext(t *testing.T) {
	st := newSpawnStore(t)
	source, err := st.CreateIntent(model.Intent{
		Title: "执行者意图", Doc: "操作者约束", Node: "lab",
		Skills: []string{"collab", "verify"}, CreatedBy: collab.CommanderID,
	})
	if err != nil {
		t.Fatal(err)
	}
	peer, isNew, err := st.CreateSpawnIntent(model.Intent{
		Title: "平级意图", CreatedBy: collab.ParticipantID(source.ID), DelegatedByRun: "r_peer",
		IdempotencyKey: "peer-1", Skills: []string{"verify", "extra"},
	}, source.ID)
	if err != nil || !isNew {
		t.Fatalf("peer publish: new=%v err=%v", isNew, err)
	}
	if peer.ParentID != "" || peer.Doc != "操作者约束" || peer.Node != "lab" {
		t.Fatalf("peer did not inherit doc/node: %+v", peer)
	}
	wantSkills := []string{"collab", "verify", "extra"}
	if len(peer.Skills) != len(wantSkills) {
		t.Fatalf("peer skills = %v, want %v", peer.Skills, wantSkills)
	}
	for i, want := range wantSkills {
		if peer.Skills[i] != want {
			t.Fatalf("peer skills = %v, want %v", peer.Skills, wantSkills)
		}
	}
	// A retry naming only its own addition unions to the same stored payload.
	again, created, err := st.CreateSpawnIntent(model.Intent{
		Title: "平级意图", CreatedBy: collab.ParticipantID(source.ID), DelegatedByRun: "r_peer",
		IdempotencyKey: "peer-1", Skills: []string{"extra"},
	}, source.ID)
	if err != nil || created || again.ID != peer.ID {
		t.Fatalf("union retry: created=%v id=%s want %s err=%v", created, again.ID, peer.ID, err)
	}
}

// A peer publish must stay on the source direction's node: the same node (or an
// empty node that inherits it) is accepted, a different one is refused. The
// child-delegation path keeps the old explicit-node-wins rule and is not locked.
func TestCreateSpawnIntentPeerNodeLock(t *testing.T) {
	st := newSpawnStore(t)
	source, err := st.CreateIntent(model.Intent{Title: "执行者意图", Node: "lab", CreatedBy: collab.CommanderID})
	if err != nil {
		t.Fatal(err)
	}
	create := func(node, key string) (model.Intent, error) {
		out, _, err := st.CreateSpawnIntent(model.Intent{
			Title: "平级 " + key, CreatedBy: collab.ParticipantID(source.ID),
			DelegatedByRun: "r_" + key, Node: node, IdempotencyKey: key,
		}, source.ID)
		return out, err
	}
	if _, err := create("web", "n-wrong"); !errors.Is(err, ErrPeerNodeLocked) {
		t.Fatalf("different peer node = %v, want ErrPeerNodeLocked", err)
	}
	same, err := create("lab", "n-same")
	if err != nil || same.Node != "lab" {
		t.Fatalf("same peer node: %+v err=%v", same, err)
	}
	inherited, err := create("", "n-empty")
	if err != nil || inherited.Node != "lab" {
		t.Fatalf("inherited peer node: %+v err=%v", inherited, err)
	}
	// A delegated child may still name a node explicitly.
	child, _, err := st.CreateChildIntent(source.ID, model.Intent{
		Title: "子意图", CreatedBy: collab.ParticipantID(source.ID), Node: "web",
	})
	if err != nil || child.Node != "web" {
		t.Fatalf("child node = %+v err=%v", child, err)
	}
}

// Child delegation appends the child's skills to the parent's (it cannot drop
// the parent's admission constraints) and inherits Doc; an explicit node wins.
func TestCreateChildIntentUnionsSkills(t *testing.T) {
	st := newSpawnStore(t)
	parent, err := st.CreateIntent(model.Intent{
		Title: "父意图", Doc: "操作者约束", Skills: []string{"collab"}, Node: "lab",
	})
	if err != nil {
		t.Fatal(err)
	}
	child, _, err := st.CreateChildIntent(parent.ID, model.Intent{
		Title: "子意图", Skills: []string{"collab", "extra"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if child.Doc != "操作者约束" || child.Node != "lab" {
		t.Fatalf("child did not inherit doc/node: %+v", child)
	}
	if len(child.Skills) != 2 || child.Skills[0] != "collab" || child.Skills[1] != "extra" {
		t.Fatalf("child skills = %v", child.Skills)
	}
}
