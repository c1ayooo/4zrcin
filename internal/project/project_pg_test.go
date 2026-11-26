package project

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/c1ayooo/4zrcin/internal/model"
	"github.com/c1ayooo/4zrcin/internal/store"
)

// Server form: the project directory carries working files only, the ledger
// lives in one PostgreSQL schema per project id (docs/16 §9.2). The roundtrip
// is the whole point: create → write → reopen the manager → the rows are
// there; drop → the schema is gone.
func TestPGLedgerManagerRoundTrip(t *testing.T) {
	dsn := os.Getenv("ZRCIN_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("需要 ZRCIN_TEST_PG_DSN（scripts/test-pg.sh）")
	}
	root := t.TempDir()
	mgr, err := OpenPooledDSN(root, dsn)
	if err != nil {
		t.Fatal(err)
	}
	p, err := mgr.Create("服务器形态回归", "")
	if err != nil {
		t.Fatal(err)
	}
	f, err := p.Store.AddFact(model.Fact{IntentID: "in_1", Kind: "vuln", Title: "跨重启的事实"})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.LedgerPath()); !os.IsNotExist(err) {
		t.Fatalf("server form must not materialize a ledger file: %v", err)
	}

	// A fresh manager over the same root must load the engagement from the
	// schema — no ledger file, no loss.
	mgr2, err := OpenPooledDSN(root, dsn)
	if err != nil {
		t.Fatal(err)
	}
	got := mgr2.Get(p.ID)
	if got == nil {
		t.Fatal("reopened manager lost the project")
	}
	row, ok := got.Store.FactByID(f.ID)
	if !ok || row.Title != "跨重启的事实" {
		t.Fatalf("ledger rows lost across manager reopen: %+v %v", row, ok)
	}
	if err := got.Store.Close(); err != nil {
		t.Fatal(err)
	}

	if err := mgr2.DropLedger(p.ID); err != nil {
		t.Fatal(err)
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	var n int
	if err := admin.QueryRow("SELECT count(*) FROM pg_namespace WHERE nspname=$1", "ledger_"+p.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("DropLedger left the schema behind")
	}
}

// The import crash window: rows restored, directory not yet renamed. The
// schema is unreachable data; a retry of the same import must clean it instead
// of tripping the empty-ledger guard, and the boot sweep
// (DropOrphanLedgers) must remove schemas whose directory is gone.
func TestPublishServerImportCleansOrphanSchemaAndSweepDropsIt(t *testing.T) {
	dsn := os.Getenv("ZRCIN_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("需要 ZRCIN_TEST_PG_DSN（scripts/test-pg.sh）")
	}
	root := t.TempDir()
	mgr, err := OpenPooledDSN(root, dsn)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate the crash: rows in the schema, no directory.
	st, err := store.OpenProjectPG(dsn, "p-orphan1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddFact(model.Fact{IntentID: "in_1", Kind: "vuln", Title: "孤儿行"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// The boot sweep drops it because no directory carries the id.
	if err := mgr.DropOrphanLedgers(); err != nil {
		t.Fatal(err)
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	var n int
	if err := admin.QueryRow("SELECT count(*) FROM pg_namespace WHERE nspname=$1", "ledger_p-orphan1").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("DropOrphanLedgers left the orphan schema behind")
	}

	// A mid-import crash between restore and rename must not block a retry:
	// PublishServerImport drops the non-empty orphan schema itself.
	if err := store.DropProjectPG(dsn, "p-orphan2"); err != nil {
		t.Fatal(err)
	}
	st2, err := store.OpenProjectPG(dsn, "p-orphan2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st2.AddFact(model.Fact{IntentID: "in_1", Kind: "vuln", Title: "again"}); err != nil {
		t.Fatal(err)
	}
	if err := st2.Close(); err != nil {
		t.Fatal(err)
	}
	staging := t.TempDir()
	if err := os.MkdirAll(filepath.Join(staging, "cases"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A package ledger must exist and be a valid empty ledger for the restore.
	pkgLedger := filepath.Join(staging, "ledger.sqlite")
	src, err := store.Open(pkgLedger)
	if err != nil {
		t.Fatal(err)
	}
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}
	p2, err := mgr.PublishServerImport("p-orphan2", staging)
	if err != nil {
		t.Fatalf("retry over an orphan schema must clean it and succeed: %v", err)
	}
	if snap := p2.Store.Snapshot(); len(snap.Facts) != 0 {
		t.Fatalf("restored snapshot should be empty: %+v", snap.Facts)
	}
	if err := p2.Store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := mgr.DropLedger("p-orphan2"); err != nil {
		t.Fatal(err)
	}
}
