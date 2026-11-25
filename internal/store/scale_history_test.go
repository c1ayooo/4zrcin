package store

// Version-history scale measurement (audit item N1). The versions table only
// grows and startup loads every row into Memory.History; this test builds a
// synthetic history (5k / 50k rows), reopens the ledger through the real
// startup path and reports: load time, in-memory history size and the read
// paths that scan the loaded slice. It is a measurement, not a performance
// gate: assertions are loose catastrophic-regression bounds only, so a change
// to the load strategy (e.g. a hot/cold window) can be judged against numbers.

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// seedVersionHistory inserts n synthetic change rows straight into the
// versions table and bumps Memory.Seq in the meta row so validation accepts
// them on reload. It writes nothing through the additive API on purpose: the
// subject is the load/read cost of a long history, not the commit cost of
// producing one.
func seedVersionHistory(t *testing.T, path string, n int) {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		base := s.db.Memory.Seq
		now := time.Now()
		// One transaction for the whole synthetic history: per-row autocommit
		// would pay a full WAL commit per row, which measures fsync, not scale.
		tx, err := s.sqlite.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		for i := 1; i <= n; i++ {
			c := Change{
				Seq: base + uint64(i), Transaction: "scale-seed", Kind: "fact",
				ID: "f_history_seed", Version: uint64(i), Operation: "update",
				Fields: []string{"summary"}, At: now,
			}
			raw, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec("INSERT INTO versions VALUES(?,?,?,?,?)",
				c.Seq, c.Kind, c.ID, c.Version, raw); err != nil {
				t.Fatal(err)
			}
		}
		// The meta row holds Memory with History stripped; its Seq is the
		// monotonic ceiling validation checks the reloaded history against.
		var payload []byte
		if err := tx.QueryRow("SELECT payload FROM meta WHERE id=1").Scan(&payload); err != nil {
			t.Fatal(err)
		}
		var meta struct {
			Version    int     `json:"version"`
			MessageSeq uint64  `json:"message_seq"`
			Memory     *Memory `json:"memory"`
		}
		if err := json.Unmarshal(payload, &meta); err != nil {
			t.Fatal(err)
		}
		meta.Memory.Seq = base + uint64(n)
		out, err := json.Marshal(meta)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec("UPDATE meta SET payload=? WHERE id=1", out); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestScaleVersionHistoryStartupLoad(t *testing.T) {
	for _, n := range []int{5000, 50000} {
		t.Run(fmt.Sprintf("history=%d", n), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "history.sqlite")
			seedVersionHistory(t, path, n)

			// Startup: the whole versions table is read and unmarshalled into
			// Memory.History before the instance can serve anything.
			start := time.Now()
			s, err := Open(path)
			load := time.Since(start)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			s.mu.RLock()
			loaded := len(s.db.Memory.History)
			loadedBytes := 0
			for _, c := range s.db.Memory.History {
				loadedBytes += len(c.Value) + len(c.Fields)
			}
			s.mu.RUnlock()
			if loaded != n {
				t.Fatalf("startup loaded %d history rows, want %d", loaded, n)
			}
			t.Logf("scale history=%d startup load %9s (rows=%d, payloads≈%dKB)",
				n, load.Round(time.Millisecond), loaded, loadedBytes/1024)

			snapshot := medianOf(t, 5, func() { _ = s.Snapshot() })
			scan := medianOf(t, 5, func() { _ = s.History("fact", "f_history_seed") })
			route := medianOf(t, 5, func() {
				if err := s.RouteFactChanges(64); err != nil {
					t.Fatalf("route: %v", err)
				}
			})
			t.Logf("scale history=%d snapshot %9s | History(kind,id) scan %9s | RouteFactChanges %9s",
				n, snapshot.Round(time.Millisecond), scan.Round(time.Microsecond), route.Round(time.Millisecond))

			// Loose bounds only: a linear startup over 50k rows must stay in
			// seconds, not minutes, and the read paths must not collapse.
			for _, bound := range []struct {
				name string
				d    time.Duration
				upTo time.Duration
			}{
				{"startup load", load, 15 * time.Second},
				{"Snapshot", snapshot, 5 * time.Second},
				{"History scan", scan, 1 * time.Second},
			} {
				if bound.d > bound.upTo {
					t.Errorf("%s took %s, over the loose %s bound", bound.name, bound.d, bound.upTo)
				}
			}
		})
	}
}
