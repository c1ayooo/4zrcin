package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/collab"
	"github.com/c1ayooo/4zrcin/internal/model"
)

// lineageSnapshot reads the lineage of a stored intent under the store lock.
func lineageSnapshot(t *testing.T, s *Store, id string) (rootID string, depth, size int) {
	t.Helper()
	s.mu.RLock()
	defer s.mu.RUnlock()
	in, ok := s.intentLocked(id)
	if !ok {
		t.Fatalf("intent %s missing", id)
	}
	return s.lineageOfLocked(*in)
}

func mustPeerPublish(t *testing.T, s *Store, title, sourceID, node, run, key string, skills ...string) model.Intent {
	t.Helper()
	out, _, err := s.CreateSpawnIntent(model.Intent{
		Title: title, CreatedBy: collab.ParticipantID(sourceID),
		Node: node, DelegatedByRun: run, IdempotencyKey: key, Skills: skills,
	}, sourceID)
	if err != nil {
		t.Fatalf("publish %q: %v", title, err)
	}
	return out
}

// A commander-created direction is a root at depth 0. Each executor hop below
// it adds one level, the whole lineage shares one root, and size counts every
// member (root included).
func TestLineageOfRootAndDepth(t *testing.T) {
	s := newSpawnStore(t)
	root := mustIntent(t, s, model.Intent{Title: "指挥官意图", CreatedBy: collab.CommanderID, Node: "lab"})
	a := mustPeerPublish(t, s, "一层平级", root.ID, "", "r1", "a")
	b := mustPeerPublish(t, s, "二层平级", a.ID, "", "r2", "b")
	c := mustPeerPublish(t, s, "三层平级", b.ID, "", "r3", "c")

	cases := []struct {
		id          string
		rootID      string
		depth, size int
	}{
		{root.ID, root.ID, 0, 4},
		{a.ID, root.ID, 1, 4},
		{b.ID, root.ID, 2, 4},
		{c.ID, root.ID, 3, 4},
	}
	for _, tc := range cases {
		gotRoot, gotDepth, gotSize := lineageSnapshot(t, s, tc.id)
		if gotRoot != tc.rootID || gotDepth != tc.depth || gotSize != tc.size {
			t.Fatalf("lineage(%s) = root=%s depth=%d size=%d, want root=%s depth=%d size=%d",
				tc.id, gotRoot, gotDepth, gotSize, tc.rootID, tc.depth, tc.size)
		}
	}
}

// A CreatedBy loop must stop the walk instead of hanging: the lineage identity
// stays deterministic and bounded.
func TestLineageOfSurvivesCreatedByCycle(t *testing.T) {
	s := newSpawnStore(t)
	x := mustIntent(t, s, model.Intent{Title: "环一"})
	y := mustIntent(t, s, model.Intent{Title: "环二"})
	s.mu.Lock()
	for i := range s.db.Intents {
		switch s.db.Intents[i].ID {
		case x.ID:
			s.db.Intents[i].CreatedBy = collab.ParticipantID(y.ID)
		case y.ID:
			s.db.Intents[i].CreatedBy = collab.ParticipantID(x.ID)
		}
	}
	s.mu.Unlock()

	for _, id := range []string{x.ID, y.ID} {
		rootID, depth, size := lineageSnapshot(t, s, id)
		if rootID == "" || depth < 1 || depth > maxLineageDepth+1 || size < 1 {
			t.Fatalf("lineage(%s) in a cycle = root=%q depth=%d size=%d", id, rootID, depth, size)
		}
	}
}

// A dangling CreatedBy (creator row missing) must not invent a root and must not
// hang; the intent is treated as its own root.
func TestLineageOfDanglingCreator(t *testing.T) {
	s := newSpawnStore(t)
	in := mustIntent(t, s, model.Intent{Title: "孤儿", CreatedBy: "pt_in_missing"})
	rootID, depth, size := lineageSnapshot(t, s, in.ID)
	if rootID != in.ID || depth != 0 || size != 1 {
		t.Fatalf("dangling lineage = root=%q depth=%d size=%d", rootID, depth, size)
	}
}

// Gate 1: one run may publish at most 8 directions. The text names the gate,
// the usage, the cap and the next action, and says a new idempotency key cannot
// help.
func TestLineagePerRunPublishLimit(t *testing.T) {
	s := newSpawnStore(t)
	root := mustIntent(t, s, model.Intent{Title: "指挥官意图", CreatedBy: collab.CommanderID})
	var keys []string
	for i := 0; i < maxPublishPerRun; i++ {
		key := fmt.Sprintf("run-a-%d", i)
		keys = append(keys, key)
		mustPeerPublish(t, s, "本轮意图 "+key, root.ID, "", "r_run_a", key)
	}
	_, _, err := s.CreateSpawnIntent(model.Intent{
		Title: "第九个意图", CreatedBy: collab.ParticipantID(root.ID),
		DelegatedByRun: "r_run_a", IdempotencyKey: "run-a-over",
	}, root.ID)
	if !errors.Is(err, ErrLineageBudget) {
		t.Fatalf("9th publish in one run = %v, want ErrLineageBudget", err)
	}
	for _, needle := range []string{"闸门=本轮发布", "当前用量=8", "上限=8", "该怎么做", "不要换 idempotency_key 重试"} {
		if !strings.Contains(err.Error(), needle) {
			t.Fatalf("budget text lacks %q: %s", needle, err.Error())
		}
	}
	if n := len(s.ListIntents()); n != maxPublishPerRun+1 {
		t.Fatalf("intents = %d, want root + %d publishes", n, maxPublishPerRun)
	}

	// A retry of an already recorded publish still returns the original row even
	// though the gate is now exhausted ...
	original, created, err := s.CreateSpawnIntent(model.Intent{
		Title: "本轮意图 " + keys[len(keys)-1], CreatedBy: collab.ParticipantID(root.ID),
		DelegatedByRun: "r_run_a", IdempotencyKey: keys[len(keys)-1],
	}, root.ID)
	if err != nil || created {
		t.Fatalf("idempotent retry after budget: created=%v err=%v", created, err)
	}
	if original.IdempotencyKey != keys[len(keys)-1] {
		t.Fatalf("retry returned %+v", original)
	}
	// ... while a genuinely new publish is still refused.
	if _, _, err := s.CreateSpawnIntent(model.Intent{
		Title: "第九个意图", CreatedBy: collab.ParticipantID(root.ID),
		DelegatedByRun: "r_run_a", IdempotencyKey: "run-a-over",
	}, root.ID); !errors.Is(err, ErrLineageBudget) {
		t.Fatalf("new publish after exhaustion = %v, want ErrLineageBudget", err)
	}
}

// Gate 2: one lineage may hold at most 32 intents. The usage shown includes the
// row the request would have added.
func TestLineageTotalLimit(t *testing.T) {
	s := newSpawnStore(t)
	root := mustIntent(t, s, model.Intent{Title: "指挥官意图", CreatedBy: collab.CommanderID})
	// Build the lineage directly: the creation budget must not block the fixture
	// itself. Each row is executor-created, so it belongs to root's lineage.
	for i := 0; i < maxLineageIntents-1; i++ {
		mustIntent(t, s, model.Intent{
			Title:     fmt.Sprintf("谱系意图 %02d", i),
			CreatedBy: collab.ParticipantID(root.ID), DelegatedByRun: fmt.Sprintf("r_total_%d", i),
		})
	}
	if _, _, size := lineageSnapshot(t, s, root.ID); size != maxLineageIntents {
		t.Fatalf("fixture lineage size = %d, want %d", size, maxLineageIntents)
	}
	_, _, err := s.CreateSpawnIntent(model.Intent{
		Title: "超量意图", CreatedBy: collab.ParticipantID(root.ID),
		DelegatedByRun: "r_new", IdempotencyKey: "total-over",
	}, root.ID)
	if !errors.Is(err, ErrLineageBudget) {
		t.Fatalf("publish over lineage cap = %v, want ErrLineageBudget", err)
	}
	for _, needle := range []string{"闸门=谱系总量", fmt.Sprintf("当前用量=%d", maxLineageIntents+1), fmt.Sprintf("上限=%d", maxLineageIntents), "该怎么做", "不要换 idempotency_key 重试"} {
		if !strings.Contains(err.Error(), needle) {
			t.Fatalf("budget text lacks %q: %s", needle, err.Error())
		}
	}
}

// Gate 3: executor-created depth below the root is capped at 3. The 4th hop is
// refused before anything is written.
func TestLineageDepthLimit(t *testing.T) {
	s := newSpawnStore(t)
	root := mustIntent(t, s, model.Intent{Title: "指挥官意图", CreatedBy: collab.CommanderID})
	parent := root.ID
	leaf := ""
	for hop := 1; hop <= maxLineageDepth; hop++ {
		leaf = mustPeerPublish(t, s, fmt.Sprintf("%d 层", hop), parent, "", fmt.Sprintf("r%d", hop), fmt.Sprintf("d%d", hop)).ID
		parent = leaf
	}
	if depth := func(id string) int { _, d, _ := lineageSnapshot(t, s, id); return d }(leaf); depth != maxLineageDepth {
		t.Fatalf("depth = %d, want %d", depth, maxLineageDepth)
	}
	before := len(s.ListIntents())
	_, _, err := s.CreateSpawnIntent(model.Intent{
		Title: "再深一层", CreatedBy: collab.ParticipantID(leaf),
		DelegatedByRun: "r_over", IdempotencyKey: "d-over",
	}, leaf)
	if !errors.Is(err, ErrLineageBudget) {
		t.Fatalf("hop beyond depth cap = %v, want ErrLineageBudget", err)
	}
	for _, needle := range []string{"闸门=谱系深度", fmt.Sprintf("当前用量=%d", maxLineageDepth+1), fmt.Sprintf("上限=%d", maxLineageDepth), "该怎么做", "不要换 idempotency_key 重试"} {
		if !strings.Contains(err.Error(), needle) {
			t.Fatalf("budget text lacks %q: %s", needle, err.Error())
		}
	}
	if after := len(s.ListIntents()); after != before {
		t.Fatalf("refused publish wrote a row: %d -> %d", before, after)
	}
}

// Commander and operator directions are not budgeted: shaping the project's
// frontier is their decision, so neither the per-run nor the lineage caps apply.
func TestLineageCommanderAndOperatorNotBudgeted(t *testing.T) {
	s := newSpawnStore(t)
	root := mustIntent(t, s, model.Intent{Title: "指挥官意图", CreatedBy: collab.CommanderID})
	for i := 0; i < maxPublishPerRun+4; i++ {
		if _, _, err := s.CreateSpawnIntent(model.Intent{
			Title: fmt.Sprintf("指挥官造人 %02d", i), CreatedBy: collab.CommanderID,
			DelegatedByRun: "r_cmd", ParentID: root.ID, IdempotencyKey: fmt.Sprintf("cmd-%d", i),
		}, ""); err != nil {
			t.Fatalf("commander publish %d: %v", i, err)
		}
	}
	for i := 0; i < maxPublishPerRun+4; i++ {
		if _, _, err := s.CreateSpawnIntent(model.Intent{
			Title: fmt.Sprintf("操作员造人 %02d", i), CreatedBy: collab.OperatorID,
			DelegatedByRun: "r_op", IdempotencyKey: fmt.Sprintf("op-%d", i),
		}, ""); err != nil {
			t.Fatalf("operator publish %d: %v", i, err)
		}
	}
}
