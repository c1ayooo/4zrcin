package bundle

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/project"
	"github.com/c1ayooo/4zrcin/internal/store"
)

// docs/17 C11: a .twproj package exported from a SQLite ledger imports into
// PostgreSQL with the same content hash and the same evidence digests, and the
// PostgreSQL ledger exports a snapshot a receiver can verify the same way. This
// is the only guarantee that the two storage forms carry projects faithfully.
func TestProjectSnapshotRoundTripThroughPostgreSQL(t *testing.T) {
	dsn := os.Getenv("ZRCIN_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("需要 ZRCIN_TEST_PG_DSN（scripts/test-pg.sh）")
	}
	dir, st, proofDigest := seedProject(t)
	want := st.Snapshot()
	archive := filepath.Join(t.TempDir(), "p-test.twproj")
	if _, err := ExportProject(dir, st, archive, "test"); err != nil {
		t.Fatalf("ExportProject: %v", err)
	}

	// Import into a server-form manager: the snapshot's rows must land in the
	// project's schema, and the files beside them without a ledger file.
	dataDir := t.TempDir()
	mgr, err := project.OpenPooledDSN(filepath.Join(dataDir, "projects"), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, p := range mgr.All() {
			_ = p.Store.Close()
		}
	})
	m, staging, err := ImportProjectStaged(archive, dataDir)
	if err != nil {
		t.Fatalf("ImportProjectStaged: %v", err)
	}
	// The test database is shared across runs and the schema name follows the
	// project id, so a previous run's rows would trip the empty-ledger guard.
	if err := store.DropProjectPG(dsn, m.Project.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.DropProjectPG(dsn, m.Project.ID) })
	p, err := mgr.PublishServerImport(m.Project.ID, staging)
	if err != nil {
		t.Fatalf("PublishServerImport: %v", err)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "ledger.sqlite")); !os.IsNotExist(err) {
		t.Fatal("server-form import must not publish a ledger.sqlite file")
	}
	if !p.IsPaused() {
		t.Fatal("imported project must stay paused")
	}

	got := p.Store.Snapshot()
	if got.Version != want.Version || got.MessageSeq != want.MessageSeq {
		t.Fatalf("meta differs: version %d/%d seq %d/%d", got.Version, want.Version, got.MessageSeq, want.MessageSeq)
	}
	if got.Memory.ContentHash != want.Memory.ContentHash {
		t.Fatalf("content hash differs: %q vs %q", got.Memory.ContentHash, want.Memory.ContentHash)
	}
	if got.Memory.Epoch != want.Memory.Epoch {
		t.Fatalf("epoch differs: %q vs %q", got.Memory.Epoch, want.Memory.Epoch)
	}
	if len(got.Memory.History) != len(want.Memory.History) {
		t.Fatalf("history length %d, want %d", len(got.Memory.History), len(want.Memory.History))
	}
	if len(got.Facts) != len(want.Facts) || len(got.Evidence) != len(want.Evidence) || len(got.Runs) != len(want.Runs) {
		t.Fatalf("row counts differ: facts %d/%d evidence %d/%d runs %d/%d",
			len(got.Facts), len(want.Facts), len(got.Evidence), len(want.Evidence), len(got.Runs), len(want.Runs))
	}

	// The evidence digests must verify against the published case tree, with
	// exactly the gap the exporter recorded.
	gaps, err := projectEvidenceGaps(p.CaseDir, got)
	if err != nil {
		t.Fatalf("evidence re-check: %v", err)
	}
	if len(gaps) != 1 || gaps[0] != "in_1/tmp/already-gone.txt" {
		t.Fatalf("gaps = %v", gaps)
	}
	found := false
	for _, e := range got.Evidence {
		if e.URI == "in_1/tmp/admin.txt" && e.Digest == proofDigest {
			found = true
		}
	}
	if !found {
		t.Fatal("proof evidence row lost or its digest changed")
	}

	// Export back out of PostgreSQL: the rendered snapshot must describe the
	// same state, so the other direction of the transfer is verified too.
	archive2 := filepath.Join(t.TempDir(), "p-test-back.twproj")
	m2, err := ExportProject(p.Dir, p.Store, archive2, "test")
	if err != nil {
		t.Fatalf("ExportProject (server): %v", err)
	}
	if m2.Counts.Facts != m.Counts.Facts || m2.Counts.Evidence != m.Counts.Evidence {
		t.Fatalf("export counts differ: %+v vs %+v", m2.Counts, m.Counts)
	}
	if len(m2.Missing) != 1 || m2.Missing[0] != "in_1/tmp/already-gone.txt" {
		t.Fatalf("export missing = %v", m2.Missing)
	}
	// The rendered ledger is a standalone snapshot whose committed state is the
	// same one the schema holds.
	var ledgerBytes []byte
	for _, e := range readZipEntries(t, archive2) {
		if e.name == "ledger.sqlite" {
			ledgerBytes = e.body
		}
	}
	if ledgerBytes == nil {
		t.Fatal("server export has no ledger.sqlite entry")
	}
	ledgerCopy := filepath.Join(t.TempDir(), "from-pg.sqlite")
	if err := os.WriteFile(ledgerCopy, ledgerBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	exported, err := store.ReadSnapshot(ledgerCopy)
	if err != nil {
		t.Fatalf("ReadSnapshot (server export): %v", err)
	}
	if exported.Memory.ContentHash != got.Memory.ContentHash {
		t.Fatalf("re-exported content hash %q, want %q", exported.Memory.ContentHash, got.Memory.ContentHash)
	}
}
