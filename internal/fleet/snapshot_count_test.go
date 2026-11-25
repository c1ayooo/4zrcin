package fleet

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/c1ayooo/4zrcin/internal/executor"
	"github.com/c1ayooo/4zrcin/internal/model"
	"github.com/c1ayooo/4zrcin/internal/store"
)

// One fleet build must freeze each project exactly once: board, intents, runs
// and token aggregation all derive from that single clone. A second freeze is
// another full clone the 3s poll pays per project, and mixed reads would let
// counts and rows describe different instants.
func TestBuildFreezesEachProjectOnce(t *testing.T) {
	open := func(t *testing.T) *store.Store {
		t.Helper()
		st, err := store.Open(filepath.Join(t.TempDir(), "ledger.json"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.Close() })
		return st
	}
	a, b := open(t), open(t)
	for _, st := range []*store.Store{a, b} {
		if _, err := st.CreateIntent(model.Intent{Title: "意图"}); err != nil {
			t.Fatal(err)
		}
	}

	freezes := 0
	original := projectSnapshot
	projectSnapshot = func(st *store.Store) store.DB {
		freezes++
		return original(st)
	}
	defer func() { projectSnapshot = original }()

	Build(time.Now(), []Source{
		{ID: "p1", Name: "项目一", Store: a},
		{ID: "p2", Name: "项目二", Store: b},
	}, []executor.NodeInfo{}, map[string]Health{})

	if freezes != 2 {
		t.Fatalf("two projects froze the ledger %d times; want exactly 2", freezes)
	}
}

// Under concurrent writes every project slice still describes one instant:
// a run row in RecentRuns always finds its intent in the same freeze, so its
// title is filled. Mixed per-path reads (the old behaviour) could pair a fresh
// run with a stale intent map and ship an untitled row.
func TestBuildRecentRunsAlwaysCarryIntentTitleUnderConcurrentWrites(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "ledger.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			in, err := st.CreateIntent(model.Intent{Title: "并发意图"})
			if err != nil {
				return
			}
			_, _ = st.AddRun(model.Run{IntentID: in.ID, Node: "local", Status: model.RunDone, Tokens: 10})
		}
	}()

	for build := 0; build < 40; build++ {
		snap := Build(time.Now(), []Source{{ID: "p1", Name: "项目一", Store: st}},
			[]executor.NodeInfo{}, map[string]Health{})
		if len(snap.Projects) != 1 {
			t.Fatalf("build %d: projects = %d", build, len(snap.Projects))
		}
		for _, row := range snap.Projects[0].RecentRuns {
			if row.Title == "" {
				close(stop)
				wg.Wait()
				t.Fatalf("build %d shipped a run row without its intent title: %+v", build, row)
			}
		}
	}
	close(stop)
	wg.Wait()
}
