package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/model"
)

// A restored ledger must be indistinguishable from the snapshot it came from:
// same schema version, same message high-water, same epoch and content
// fingerprint, same typed rows and same version history (docs/16 §13 M0
// ticket 5; docs/17 C11's row half). The desktop path never calls
// RestoreSnapshot, so this test runs it against both dialects' targets.
func TestRestoreSnapshotRebuildsAnEmptyLedger(t *testing.T) {
	src := open(t)
	in, err := src.CreateIntent(model.Intent{Title: "导入的意图"})
	if err != nil {
		t.Fatal(err)
	}
	f, err := src.AddFact(model.Fact{IntentID: in.ID, Kind: "vuln", Title: "恢复的事实", Summary: "快照恢复", Doc: "admin=1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.AddEvidence(model.Evidence{URI: "in_1/proof.txt", IntentID: in.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := src.AddRun(model.Run{Node: "local"}); err != nil {
		t.Fatal(err)
	}
	want := src.Snapshot()
	backup := filepath.Join(t.TempDir(), "snap.sqlite")
	if err := src.BackupTo(backup); err != nil {
		t.Fatalf("BackupTo: %v", err)
	}

	restore := func() *Store {
		if src.be.pg {
			return openPGSchema(t, os.Getenv("ZRCIN_TEST_PG_DSN"))
		}
		s, err := OpenSQLite(filepath.Join(t.TempDir(), "target.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	}

	dst := restore()
	if err := dst.RestoreSnapshot(backup); err != nil {
		t.Fatalf("RestoreSnapshot: %v", err)
	}
	got := dst.Snapshot()
	if got.Version != want.Version || got.MessageSeq != want.MessageSeq {
		t.Fatalf("meta differs: version %d/%d seq %d/%d", got.Version, want.Version, got.MessageSeq, want.MessageSeq)
	}
	if got.Memory.Epoch != want.Memory.Epoch || got.Memory.ContentHash != want.Memory.ContentHash {
		t.Fatalf("memory differs: epoch %q/%q hash %q/%q",
			got.Memory.Epoch, want.Memory.Epoch, got.Memory.ContentHash, want.Memory.ContentHash)
	}
	if len(got.Memory.History) != len(want.Memory.History) {
		t.Fatalf("history length %d, want %d", len(got.Memory.History), len(want.Memory.History))
	}
	if len(got.Intents) != 1 || got.Intents[0].Title != "导入的意图" {
		t.Fatalf("intents = %+v", got.Intents)
	}
	if len(got.Facts) != 1 || got.Facts[0].ID != f.ID {
		t.Fatalf("facts = %+v", got.Facts)
	}
	if len(got.Evidence) != 1 || len(got.Runs) != 1 {
		t.Fatalf("evidence=%d runs=%d", len(got.Evidence), len(got.Runs))
	}

	// A second restore must not merge into the restored rows.
	if err := dst.RestoreSnapshot(backup); err == nil {
		t.Fatal("second restore into a filled ledger must fail")
	}
	// The restored ledger keeps taking writes and stays consistent on reopen.
	if _, err := dst.AddFact(model.Fact{IntentID: in.ID, Kind: "note", Title: "恢复后写入"}); err != nil {
		t.Fatalf("write after restore: %v", err)
	}
	reopened := reopen(t, dst)
	got2 := reopened.Snapshot()
	if got2.Memory.ContentHash != dst.Snapshot().Memory.ContentHash {
		t.Fatal("reopened ledger disagrees with the live one after restore")
	}
	if len(got2.Facts) != 2 {
		t.Fatalf("facts after post-restore write = %d, want 2", len(got2.Facts))
	}
}

// A snapshot that fails validation must leave the empty target untouched.
func TestRestoreSnapshotRefusesBrokenSnapshot(t *testing.T) {
	src := open(t)
	if _, err := src.AddFact(model.Fact{Title: "x"}); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "snap.sqlite")
	if err := src.BackupTo(backup); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt one object payload in place: the fingerprint check in
	// ReadSnapshot must catch it before RestoreSnapshot writes anything.
	needle := []byte(`"title":"x"`)
	at := -1
	for i := 0; i+len(needle) <= len(raw); i++ {
		if string(raw[i:i+len(needle)]) == string(needle) {
			at = i + len(needle) - 1
			break
		}
	}
	if at < 0 {
		t.Fatal("fixture payload not found in snapshot bytes")
	}
	raw[at] ^= 0x20
	broken := filepath.Join(t.TempDir(), "broken.sqlite")
	if err := os.WriteFile(broken, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	dst := open(t)
	if err := dst.RestoreSnapshot(broken); err == nil {
		t.Fatal("RestoreSnapshot must refuse a corrupted snapshot")
	}
	if got := dst.Snapshot(); len(got.Facts) != 0 {
		t.Fatalf("rejected restore wrote rows: facts=%d", len(got.Facts))
	}
}
